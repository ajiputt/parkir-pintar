package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
)

// ----- internal-package fakes for OverdueWorker -----

type owFakeRepo struct {
	mu         sync.Mutex
	candidates []*domain.Invoice
	findErr    error
	saved      []*domain.Invoice
	saveErr    error
	saveErrFor map[uuid.UUID]error
}

func (r *owFakeRepo) FindOverdueCandidates(_ context.Context, _ time.Time, _ int) ([]*domain.Invoice, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	return r.candidates, nil
}

func (r *owFakeRepo) Save(_ context.Context, inv *domain.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.saveErrFor[inv.ID]; ok {
		return e
	}
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, inv)
	return nil
}

type owFakePub struct {
	mu      sync.Mutex
	overdue []*domain.Invoice
	err     error
}

func (p *owFakePub) PublishInvoiceIssued(_ context.Context, _ *domain.Invoice) error { return nil }
func (p *owFakePub) PublishInvoicePaid(_ context.Context, _ *domain.Invoice) error   { return nil }
func (p *owFakePub) PublishInvoiceOverdue(_ context.Context, inv *domain.Invoice) error {
	if p.err != nil {
		return p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.overdue = append(p.overdue, inv)
	return nil
}

// Helper: build an ISSUED MANUAL invoice (eligible for OVERDUE).
func makeIssuedManual() *domain.Invoice {
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
	inv.Issue(time.Now().UTC().Add(-time.Hour)) // issued long enough ago
	return inv
}

func newWorker(repo *owFakeRepo, pub *owFakePub) *OverdueWorker {
	return &OverdueWorker{
		Invoices:    repo,
		Pub:         pub,
		Interval:    time.Hour, // not used by scanOnce
		GracePeriod: 15 * time.Minute,
		BatchSize:   100,
		Logger:      zap.NewNop(),
	}
}

// ----- scanOnce tests -----

func TestScanOnce_NoCandidates_NoOp(t *testing.T) {
	t.Parallel()
	repo := &owFakeRepo{}
	pub := &owFakePub{}
	w := newWorker(repo, pub)
	w.scanOnce(context.Background())

	if len(repo.saved) != 0 || len(pub.overdue) != 0 {
		t.Error("no candidates → no saves & no publishes")
	}
}

func TestScanOnce_FindError_NoCrash(t *testing.T) {
	t.Parallel()
	repo := &owFakeRepo{findErr: errors.New("db down")}
	pub := &owFakePub{}
	w := newWorker(repo, pub)
	// Should not panic; just logs.
	w.scanOnce(context.Background())
}

func TestScanOnce_MarkAndPublish(t *testing.T) {
	t.Parallel()
	repo := &owFakeRepo{candidates: []*domain.Invoice{makeIssuedManual(), makeIssuedManual()}}
	pub := &owFakePub{}
	w := newWorker(repo, pub)

	w.scanOnce(context.Background())

	if len(repo.saved) != 2 {
		t.Errorf("expected 2 saves, got %d", len(repo.saved))
	}
	if len(pub.overdue) != 2 {
		t.Errorf("expected 2 publishes, got %d", len(pub.overdue))
	}
	for _, inv := range repo.saved {
		if inv.Status != domain.InvoiceOverdue {
			t.Errorf("saved invoice status = %q, want OVERDUE", inv.Status)
		}
	}
}

func TestScanOnce_MarkOverdueRejected_SkipsContinues(t *testing.T) {
	t.Parallel()
	// AUTO mode invoice cannot transition to OVERDUE → MarkOverdue rejects.
	autoInv := domain.NewWithMode(uuid.New(), "drv-x", domain.PaymentAuto)
	autoInv.Issue(time.Now().UTC().Add(-time.Hour))

	manual := makeIssuedManual()
	repo := &owFakeRepo{candidates: []*domain.Invoice{autoInv, manual}}
	pub := &owFakePub{}
	w := newWorker(repo, pub)

	w.scanOnce(context.Background())

	// Auto invoice should be skipped; manual should be saved & published.
	if len(repo.saved) != 1 {
		t.Errorf("expected 1 save (manual only), got %d", len(repo.saved))
	}
	if len(pub.overdue) != 1 {
		t.Errorf("expected 1 publish (manual only), got %d", len(pub.overdue))
	}
	if repo.saved[0].ID != manual.ID {
		t.Error("saved invoice should be the MANUAL one")
	}
}

func TestScanOnce_SaveError_SkipsPublish(t *testing.T) {
	t.Parallel()
	inv := makeIssuedManual()
	repo := &owFakeRepo{
		candidates: []*domain.Invoice{inv},
		saveErrFor: map[uuid.UUID]error{inv.ID: errors.New("save failed")},
	}
	pub := &owFakePub{}
	w := newWorker(repo, pub)

	w.scanOnce(context.Background())

	if len(pub.overdue) != 0 {
		t.Error("publish must be skipped when save fails")
	}
}

func TestScanOnce_PublishError_Tolerated(t *testing.T) {
	t.Parallel()
	repo := &owFakeRepo{candidates: []*domain.Invoice{makeIssuedManual()}}
	pub := &owFakePub{err: errors.New("nats down")}
	w := newWorker(repo, pub)
	// Should not panic; publish best-effort.
	w.scanOnce(context.Background())

	if len(repo.saved) != 1 {
		t.Error("save still happens even if publish fails")
	}
}

// ----- Run lifecycle -----

func TestRun_ContextCancellation_Returns(t *testing.T) {
	t.Parallel()
	repo := &owFakeRepo{}
	pub := &owFakePub{}
	w := &OverdueWorker{
		Invoices:    repo,
		Pub:         pub,
		Interval:    50 * time.Millisecond,
		GracePeriod: time.Minute,
		BatchSize:   10,
		Logger:      zap.NewNop(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Give it a moment to start.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// returned ok
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

func TestRun_AppliesDefaults_WhenZero(t *testing.T) {
	t.Parallel()
	w := &OverdueWorker{
		Invoices: &owFakeRepo{},
		Pub:      &owFakePub{},
		// Interval / GracePeriod / BatchSize / Logger = zero
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}

	// Defaults applied:
	if w.Interval == 0 {
		t.Error("Interval default not applied")
	}
	if w.GracePeriod == 0 {
		t.Error("GracePeriod default not applied")
	}
	if w.BatchSize == 0 {
		t.Error("BatchSize default not applied")
	}
	if w.Logger == nil {
		t.Error("Logger default (Nop) not applied")
	}
}

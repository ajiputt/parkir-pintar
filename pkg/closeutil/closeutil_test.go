package closeutil_test

import (
	"errors"
	"testing"

	"github.com/ajiperdana/parkir-pintar/pkg/closeutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// fakeCloser — io.Closer test double. Track call count + optional return error.
type fakeCloser struct {
	callCount int
	err       error
}

func (f *fakeCloser) Close() error {
	f.callCount++
	return f.err
}

func TestQuiet_HappyPath(t *testing.T) {
	t.Parallel()
	c := &fakeCloser{}
	closeutil.Quiet(c)
	if c.callCount != 1 {
		t.Fatalf("Close() expected 1 call, got %d", c.callCount)
	}
}

func TestQuiet_SwallowsError(t *testing.T) {
	t.Parallel()
	c := &fakeCloser{err: errors.New("close failed")}
	// Should NOT panic atau propagate — error discarded by design.
	closeutil.Quiet(c)
	if c.callCount != 1 {
		t.Fatalf("Close() expected 1 call, got %d", c.callCount)
	}
}

func TestQuiet_NilClosersafe(t *testing.T) {
	t.Parallel()
	// Should NOT panic walaupun closer nil.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Quiet(nil) panicked: %v", r)
		}
	}()
	closeutil.Quiet(nil)
}

func TestLog_HappyPath_NoLogEmitted(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	c := &fakeCloser{} // no error
	closeutil.Log(c, log, "test-resource")

	if c.callCount != 1 {
		t.Fatalf("Close() expected 1 call, got %d", c.callCount)
	}
	if recorded.Len() != 0 {
		t.Fatalf("expected 0 log entries (no error), got %d", recorded.Len())
	}
}

func TestLog_ErrorEmitted(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	c := &fakeCloser{err: errors.New("connection refused")}
	closeutil.Log(c, log, "redis-client")

	if c.callCount != 1 {
		t.Fatalf("Close() expected 1 call, got %d", c.callCount)
	}
	if recorded.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", recorded.Len())
	}
	entry := recorded.All()[0]
	if entry.Message != "close failed" {
		t.Errorf("log message mismatch: got %q", entry.Message)
	}
	// Verify structured fields
	fields := entry.ContextMap()
	if fields["resource"] != "redis-client" {
		t.Errorf("resource field mismatch: got %v", fields["resource"])
	}
	if fields["error"] != "connection refused" {
		t.Errorf("error field mismatch: got %v", fields["error"])
	}
}

func TestLog_NilCloserSafe(t *testing.T) {
	t.Parallel()
	log := zap.NewNop()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Log(nil, ...) panicked: %v", r)
		}
	}()
	closeutil.Log(nil, log, "resource")
}

func TestLog_NilLoggerSafe(t *testing.T) {
	t.Parallel()
	c := &fakeCloser{err: errors.New("boom")}
	// Should NOT panic even kalau logger nil.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Log(c, nil, ...) panicked: %v", r)
		}
	}()
	closeutil.Log(c, nil, "resource")
	if c.callCount != 1 {
		t.Fatalf("Close() should still be called, got %d", c.callCount)
	}
}

package template_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/adapter/template"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

// All 6 template files yang di-expect oleh kindToFile map di loader.go.
// NewLoader load semua sekaligus → kalau ada satu missing, return error.
// Untuk test happy path butuh write SEMUA template ke temp dir.
var allTemplates = map[string]string{
	"reservation_confirmed.txt": "Subject: Reservation confirmed\n\nHello {{.Name}}, your booking is confirmed.",
	"reservation_expired.txt":   "Subject: Reservation expired\n\nHello {{.Name}}, your booking has expired.",
	"invoice_issued.txt":        "Subject: Invoice issued\n\nHello {{.Name}}, your invoice {{.InvoiceID}} is issued.",
	"invoice_overdue.txt":       "Subject: Invoice overdue\n\nHello {{.Name}}, your invoice {{.InvoiceID}} is overdue.",
	"payment_succeeded.txt":     "Subject: Payment succeeded\n\nHello {{.Name}}, payment received.",
	"payment_failed.txt":        "Subject: Payment failed\n\nHello {{.Name}}, payment failed.",
}

// writeAllTemplates — bootstrap temp dir dengan template files. Optional override
// untuk replace satu file dengan content khusus (untuk test render error path).
func writeAllTemplates(t *testing.T, override map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range allTemplates {
		if o, ok := override[name]; ok {
			content = o
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestRender_HappyPath(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, nil)
	l, err := template.NewLoader(dir)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	subject, body, err := l.Render(domain.KindReservationConfirmed, map[string]any{
		"Name": "Aji",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if subject != "Reservation confirmed" {
		t.Errorf("subject mismatch: got %q", subject)
	}
	if !strings.Contains(body, "Hello Aji") {
		t.Errorf("body should contain greeting; got %q", body)
	}
}

func TestRender_TemplateNotFound(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, nil)
	l, err := template.NewLoader(dir)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	// Pakai Kind yang tidak terdaftar di kindToFile.
	_, _, err = l.Render(domain.Kind("unknown-kind"), nil)
	if !errors.Is(err, domain.ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

// Cover line 78 — subject render error wrapping.
// Trigger dengan template yang reference field undefined supaya text/template
// .Execute return error.
func TestRender_SubjectRenderFails_WrapsSentinel(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, map[string]string{
		"reservation_confirmed.txt": "Subject: Hello {{.Name.InvalidField}}\n\nbody",
	})
	l, err := template.NewLoader(dir)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	_, _, err = l.Render(domain.KindReservationConfirmed, map[string]any{
		"Name": "Aji", // string tidak punya .InvalidField → render error
	})
	if err == nil {
		t.Fatalf("expected render error, got nil")
	}
	// Sentinel wrapped via %w — caller bisa errors.Is check.
	if !errors.Is(err, domain.ErrTemplateRenderFailed) {
		t.Fatalf("expected ErrTemplateRenderFailed in error chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), "subject render") {
		t.Errorf("error message should mention subject render; got %q", err.Error())
	}
}

// Cover line 81 — body render error wrapping.
// Subject render fine, body has invalid field reference.
func TestRender_BodyRenderFails_WrapsSentinel(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, map[string]string{
		"reservation_confirmed.txt": "Subject: OK\n\nBody {{.Name.NotExist}}",
	})
	l, err := template.NewLoader(dir)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	_, _, err = l.Render(domain.KindReservationConfirmed, map[string]any{
		"Name": "Aji",
	})
	if err == nil {
		t.Fatalf("expected render error, got nil")
	}
	if !errors.Is(err, domain.ErrTemplateRenderFailed) {
		t.Fatalf("expected ErrTemplateRenderFailed in error chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), "body render") {
		t.Errorf("error message should mention body render; got %q", err.Error())
	}
}

func TestNewLoader_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // empty — no template files
	_, err := template.NewLoader(dir)
	if err == nil {
		t.Fatalf("expected error for missing template files, got nil")
	}
}

func TestNewLoader_InvalidFormat_NoEmptyLine(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, map[string]string{
		// No empty line separator → parseFile returns error.
		"reservation_confirmed.txt": "Subject: foo",
	})
	_, err := template.NewLoader(dir)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "missing empty line") {
		t.Errorf("error should mention missing empty line; got %q", err.Error())
	}
}

func TestNewLoader_InvalidFormat_MissingSubjectHeader(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, map[string]string{
		"reservation_confirmed.txt": "NotSubject: foo\n\nbody",
	})
	_, err := template.NewLoader(dir)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "Subject:") {
		t.Errorf("error should mention Subject header; got %q", err.Error())
	}
}

func TestNewLoader_WindowsLineEndings(t *testing.T) {
	t.Parallel()
	dir := writeAllTemplates(t, map[string]string{
		"reservation_confirmed.txt": "Subject: WinLine\r\n\r\nBody content {{.Name}}",
	})
	l, err := template.NewLoader(dir)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	subject, _, err := l.Render(domain.KindReservationConfirmed, map[string]any{"Name": "Aji"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if subject != "WinLine" {
		t.Errorf("subject mismatch: got %q", subject)
	}
}

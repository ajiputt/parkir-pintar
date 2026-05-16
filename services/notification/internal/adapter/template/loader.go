// Package template — load + render email templates dari file.
//
// File format:
//
//	Subject: <one-line subject template>
//	<empty line>
//	<multi-line body template>
//
// Variables di-render via text/template syntax {{.VarName}}.
//
// Templates di-load sekali saat startup, di-cache di memory. Kalau template
// di-edit, restart service.
package template

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	tt "text/template"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

// Loader — implements usecase.TemplateRenderer port.
type Loader struct {
	dir       string
	templates map[domain.Kind]*parsed
	mu        sync.RWMutex
}

type parsed struct {
	subject *tt.Template
	body    *tt.Template
}

// kindToFile — mapping Kind → filename. Convention: lowercase + underscore.
var kindToFile = map[domain.Kind]string{
	domain.KindReservationConfirmed: "reservation_confirmed.txt",
	domain.KindReservationExpired:   "reservation_expired.txt",
	domain.KindInvoiceIssued:        "invoice_issued.txt",
	domain.KindInvoiceOverdue:       "invoice_overdue.txt",
	domain.KindPaymentSucceeded:     "payment_succeeded.txt",
	domain.KindPaymentFailed:        "payment_failed.txt",
}

// NewLoader — load semua template dari dir. Return error kalau ada file missing
// atau parse error.
func NewLoader(dir string) (*Loader, error) {
	l := &Loader{
		dir:       dir,
		templates: make(map[domain.Kind]*parsed),
	}
	for kind, fname := range kindToFile {
		p, err := l.parseFile(filepath.Join(dir, fname))
		if err != nil {
			return nil, fmt.Errorf("template %s (%s): %w", kind, fname, err)
		}
		l.templates[kind] = p
	}
	return l, nil
}

// Render — implements usecase.TemplateRenderer.
func (l *Loader) Render(kind domain.Kind, vars map[string]any) (string, string, error) {
	l.mu.RLock()
	p, ok := l.templates[kind]
	l.mu.RUnlock()
	if !ok {
		return "", "", domain.ErrTemplateNotFound
	}

	var subjectBuf, bodyBuf bytes.Buffer
	if err := p.subject.Execute(&subjectBuf, vars); err != nil {
		return "", "", fmt.Errorf("%w: subject render: %s", domain.ErrTemplateRenderFailed, err.Error())
	}
	if err := p.body.Execute(&bodyBuf, vars); err != nil {
		return "", "", fmt.Errorf("%w: body render: %s", domain.ErrTemplateRenderFailed, err.Error())
	}
	return strings.TrimSpace(subjectBuf.String()), bodyBuf.String(), nil
}

// parseFile — parse file dengan format:
//
//	Subject: <subject>
//	<empty line>
//	<body>
func (l *Loader) parseFile(path string) (*parsed, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(raw)
	idx := strings.Index(content, "\n\n")
	if idx < 0 {
		// Try Windows line endings
		idx = strings.Index(content, "\r\n\r\n")
		if idx < 0 {
			return nil, errors.New("invalid template: missing empty line after Subject header")
		}
	}

	header := strings.TrimSpace(content[:idx])
	body := content[idx+2:] // skip empty line

	if !strings.HasPrefix(header, "Subject:") {
		return nil, errors.New("invalid template: first line must start with 'Subject:'")
	}
	subjectStr := strings.TrimSpace(strings.TrimPrefix(header, "Subject:"))

	subjectTpl, err := tt.New("subject").Parse(subjectStr)
	if err != nil {
		return nil, fmt.Errorf("parse subject: %w", err)
	}
	bodyTpl, err := tt.New("body").Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}

	return &parsed{subject: subjectTpl, body: bodyTpl}, nil
}

package domain

import "github.com/ajiperdana/parkir-pintar/pkg/errs"

var (
	ErrContactNotFound      = errs.New(errs.KindNotFound, "NOT-001", "contact not found for driver")
	ErrContactOptedOut      = errs.New(errs.KindFailedPrecondition, "NOT-002", "contact opted out from notifications")
	ErrTemplateNotFound     = errs.New(errs.KindNotFound, "NOT-003", "email template not found")
	ErrTemplateRenderFailed = errs.New(errs.KindInternal, "NOT-004", "template render failed")
	ErrSendFailed           = errs.New(errs.KindUnavailable, "NOT-005", "email delivery failed")
	ErrAlreadyDispatched    = errs.New(errs.KindAlreadyExists, "NOT-006", "notification for event_id already dispatched (dedup)")
)

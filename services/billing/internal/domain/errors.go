package domain

import "github.com/ajiperdana/parkir-pintar/pkg/errs"

var (
	ErrInvoiceNotFound        = errs.New(errs.KindNotFound, "BIL-001", "invoice not found")
	ErrInvalidStateTransition = errs.New(errs.KindFailedPrecondition, "BIL-002", "invalid invoice state transition")
	ErrDuplicateEvent         = errs.New(errs.KindAlreadyExists, "BIL-010", "event already processed")
)

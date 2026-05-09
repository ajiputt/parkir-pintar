// Package errs mendefinisikan typed domain errors yang dapat dipetakan
// ke gRPC status code dan HTTP status (oleh grpc-gateway error handler).
//
// Pakai errors.Is untuk pengecekan, dan ToStatus untuk konversi ke gRPC status.
package errs

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Kind — kategori error domain.
type Kind int

const (
	KindUnknown Kind = iota
	KindInvalidArgument
	KindNotFound
	KindAlreadyExists
	KindPermissionDenied
	KindUnauthenticated
	KindFailedPrecondition // mis. state transition tidak valid
	KindConflict           // mis. spot sudah dibooking
	KindIdempotencyConflict
	KindInternal
	KindUnavailable
	KindDeadlineExceeded
)

// E adalah typed error dengan kind, message, dan optional cause.
type E struct {
	Kind    Kind
	Code    string // stable string code, mis. "RES-001"
	Message string
	Cause   error
	Meta    map[string]string
}

func (e *E) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s [%s]: %s: %v", e.Code, kindName(e.Kind), e.Message, e.Cause)
	}
	return fmt.Sprintf("%s [%s]: %s", e.Code, kindName(e.Kind), e.Message)
}

func (e *E) Unwrap() error { return e.Cause }

// Is supports errors.Is comparison by Kind.
func (e *E) Is(target error) bool {
	t, ok := target.(*E)
	if !ok {
		return false
	}
	if t.Code != "" {
		return e.Code == t.Code
	}
	return e.Kind == t.Kind
}

// New membuat error baru.
func New(kind Kind, code, msg string) *E {
	return &E{Kind: kind, Code: code, Message: msg}
}

// Wrap melapisi error dengan kind+code.
func Wrap(err error, kind Kind, code, msg string) *E {
	return &E{Kind: kind, Code: code, Message: msg, Cause: err}
}

// WithMeta menambahkan metadata key-value (untuk logging/tracing).
func (e *E) WithMeta(k, v string) *E {
	if e.Meta == nil {
		e.Meta = make(map[string]string)
	}
	e.Meta[k] = v
	return e
}

// ToStatus konversi ke gRPC status. Otomatis dipakai grpc-gateway untuk HTTP code.
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	var e *E
	if !errors.As(err, &e) {
		return status.Error(codes.Internal, err.Error())
	}
	return status.Error(toCode(e.Kind), e.Error())
}

// IsKind helper untuk pattern matching.
func IsKind(err error, k Kind) bool {
	var e *E
	return errors.As(err, &e) && e.Kind == k
}

func toCode(k Kind) codes.Code {
	switch k {
	case KindInvalidArgument:
		return codes.InvalidArgument
	case KindNotFound:
		return codes.NotFound
	case KindAlreadyExists:
		return codes.AlreadyExists
	case KindPermissionDenied:
		return codes.PermissionDenied
	case KindUnauthenticated:
		return codes.Unauthenticated
	case KindFailedPrecondition:
		return codes.FailedPrecondition
	case KindConflict:
		return codes.Aborted
	case KindIdempotencyConflict:
		return codes.AlreadyExists
	case KindUnavailable:
		return codes.Unavailable
	case KindDeadlineExceeded:
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}

func kindName(k Kind) string {
	switch k {
	case KindInvalidArgument:
		return "invalid_argument"
	case KindNotFound:
		return "not_found"
	case KindAlreadyExists:
		return "already_exists"
	case KindPermissionDenied:
		return "permission_denied"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindFailedPrecondition:
		return "failed_precondition"
	case KindConflict:
		return "conflict"
	case KindIdempotencyConflict:
		return "idempotency_conflict"
	case KindInternal:
		return "internal"
	case KindUnavailable:
		return "unavailable"
	case KindDeadlineExceeded:
		return "deadline_exceeded"
	default:
		return "unknown"
	}
}

// Pre-defined common errors. Service-specific errors di-define di internal/domain/errors.go masing-masing.
var (
	ErrInternal       = New(KindInternal, "PP-INT-001", "internal error")
	ErrUnauthorized   = New(KindUnauthenticated, "PP-AUT-001", "missing or invalid credentials")
	ErrForbidden      = New(KindPermissionDenied, "PP-AUT-002", "permission denied")
	ErrIdempotencyKey = New(KindIdempotencyConflict, "PP-IDM-001", "idempotency key conflict (different payload for same key)")
)

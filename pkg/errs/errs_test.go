package errs

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNew(t *testing.T) {
	e := New(KindNotFound, "PP-RES-001", "reservation not found")
	assert.Equal(t, "PP-RES-001 [not_found]: reservation not found", e.Error())
}

func TestWrap_PreservesCause(t *testing.T) {
	cause := errors.New("db timeout")
	e := Wrap(cause, KindUnavailable, "PP-DB-001", "db unavailable")
	assert.Contains(t, e.Error(), "db timeout")
	assert.ErrorIs(t, e, cause)
}

func TestToStatus_MapsCodes(t *testing.T) {
	cases := []struct {
		kind Kind
		want codes.Code
	}{
		{KindInvalidArgument, codes.InvalidArgument},
		{KindNotFound, codes.NotFound},
		{KindConflict, codes.Aborted},
		{KindIdempotencyConflict, codes.AlreadyExists},
		{KindUnavailable, codes.Unavailable},
		{KindInternal, codes.Internal},
	}
	for _, c := range cases {
		s, ok := status.FromError(ToStatus(New(c.kind, "X", "x")))
		assert.True(t, ok)
		assert.Equal(t, c.want, s.Code(), "kind=%v", c.kind)
	}
}

func TestIsKind(t *testing.T) {
	e := New(KindNotFound, "PP-X", "x")
	assert.True(t, IsKind(e, KindNotFound))
	assert.False(t, IsKind(e, KindConflict))
}

func TestNilError(t *testing.T) {
	assert.Nil(t, ToStatus(nil))
}

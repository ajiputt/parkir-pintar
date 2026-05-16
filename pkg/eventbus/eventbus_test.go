package eventbus_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
)

type samplePayload struct {
	Foo string `json:"foo"`
	Bar int    `json:"bar"`
}

func TestEncode_HappyPath(t *testing.T) {
	before := time.Now().UTC()
	env, err := eventbus.Encode(
		"evt-id-1",
		"reservation.confirmed.v1",
		"reservation",
		"res-123",
		samplePayload{Foo: "hello", Bar: 7},
	)
	after := time.Now().UTC()
	require.NoError(t, err)

	assert.Equal(t, "evt-id-1", env.ID)
	assert.Equal(t, "reservation.confirmed.v1", env.Type)
	assert.Equal(t, "reservation", env.AggregateType)
	assert.Equal(t, "res-123", env.AggregateID)
	assert.False(t, env.OccurredAt.Before(before))
	assert.False(t, env.OccurredAt.After(after))
	// OccurredAt should be UTC.
	assert.Equal(t, time.UTC, env.OccurredAt.Location())

	// Payload is a JSON-encoded version of the input struct.
	var got samplePayload
	require.NoError(t, json.Unmarshal(env.Payload, &got))
	assert.Equal(t, "hello", got.Foo)
	assert.Equal(t, 7, got.Bar)
}

func TestEncode_NilPayload(t *testing.T) {
	env, err := eventbus.Encode[any](
		"id-1",
		"t.v1",
		"agg",
		"agg-1",
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage("null"), env.Payload)
}

// unmarshalable contains a chan which cannot be marshaled to JSON.
type unmarshalable struct {
	C chan int
}

func TestEncode_MarshalError(t *testing.T) {
	env, err := eventbus.Encode(
		"id",
		"t",
		"a",
		"a-1",
		unmarshalable{C: make(chan int)},
	)
	require.Error(t, err)
	assert.Equal(t, eventbus.Envelope{}, env)
}

func TestDecode_HappyPath(t *testing.T) {
	env, err := eventbus.Encode(
		"id-1",
		"t.v1",
		"agg",
		"agg-1",
		samplePayload{Foo: "x", Bar: 42},
	)
	require.NoError(t, err)

	got, err := eventbus.Decode[samplePayload](env)
	require.NoError(t, err)
	assert.Equal(t, "x", got.Foo)
	assert.Equal(t, 42, got.Bar)
}

func TestDecode_TypeMismatch(t *testing.T) {
	// Encode a struct, then decode as something incompatible.
	env, err := eventbus.Encode(
		"id-1",
		"t.v1",
		"agg",
		"agg-1",
		samplePayload{Foo: "x", Bar: 42},
	)
	require.NoError(t, err)

	// Decoding as a plain int should fail (object → int).
	_, err = eventbus.Decode[int](env)
	require.Error(t, err)
}

func TestDecode_InvalidJSON(t *testing.T) {
	env := eventbus.Envelope{Payload: json.RawMessage("not json")}
	_, err := eventbus.Decode[samplePayload](env)
	require.Error(t, err)
}

func TestEnvelope_RoundTripJSON(t *testing.T) {
	original, err := eventbus.Encode(
		"id-1",
		"t.v1",
		"agg",
		"agg-1",
		samplePayload{Foo: "hi", Bar: 5},
	)
	require.NoError(t, err)
	original.TraceID = "trace-xyz"
	original.Headers = map[string]string{"k": "v"}

	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded eventbus.Envelope
	require.NoError(t, json.Unmarshal(raw, &decoded))

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.AggregateType, decoded.AggregateType)
	assert.Equal(t, original.AggregateID, decoded.AggregateID)
	assert.Equal(t, original.TraceID, decoded.TraceID)
	assert.Equal(t, original.Headers, decoded.Headers)
	// Time round-trip via JSON keeps the instant (may lose nanosecond precision
	// on some platforms, but Equal/Truncate handles this here since UTC).
	assert.True(t, original.OccurredAt.Equal(decoded.OccurredAt))

	// Payload survives.
	var p samplePayload
	require.NoError(t, json.Unmarshal(decoded.Payload, &p))
	assert.Equal(t, "hi", p.Foo)
	assert.Equal(t, 5, p.Bar)
}

func TestErrEmptySubject_IsSentinel(t *testing.T) {
	// Just ensure the exported sentinel is non-nil and stable.
	require.NotNil(t, eventbus.ErrEmptySubject)
	assert.True(t, errors.Is(eventbus.ErrEmptySubject, eventbus.ErrEmptySubject))
	assert.Contains(t, eventbus.ErrEmptySubject.Error(), "eventbus")
}

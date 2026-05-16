package midtrans

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifySignatureKey_Valid(t *testing.T) {
	orderID := "ORD-1"
	statusCode := "200"
	gross := "30000.00"
	serverKey := "SB-Mid-server-XYZ"
	sig := sha512.Sum512([]byte(orderID + statusCode + gross + serverKey))
	expected := hex.EncodeToString(sig[:])

	assert.True(t, VerifySignatureKey(orderID, statusCode, gross, serverKey, expected))
	assert.False(t, VerifySignatureKey(orderID, statusCode, gross, serverKey, "wrong"))
}

func TestConstantTimeEqual(t *testing.T) {
	assert.True(t, constantTimeEqual("abc", "abc"))
	assert.False(t, constantTimeEqual("abc", "abd"))
	assert.False(t, constantTimeEqual("abc", "ab"))
}

// TestCharge_MockMode — fast-path: Mock=true short-circuit, no HTTP call.
func TestCharge_MockMode(t *testing.T) {
	c := &Client{Mock: true}
	resp, err := c.Charge(context.Background(), ChargeRequest{
		OrderID:  "ORD-mock-1",
		GrossIDR: 25000,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ORD-mock-1", resp.OrderID)
	assert.NotEmpty(t, resp.QRString, "mock should return synthetic qr_string")
}

// TestCharge_HTTPSuccess — real HTTP path via httptest server. Covers
// `defer closeutil.Quiet(resp.Body)` di Charge() supaya coverage tidak drop.
func TestCharge_HTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/charge", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"transaction_id":"tx-123",
			"order_id":"ORD-real-1",
			"status_code":"201",
			"status_message":"ok",
			"qr_string":"00020101021126...",
			"actions":[{"name":"generate-qr-code","url":"https://api.sandbox.midtrans.com/qr/x.png"}]
		}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, ServerKey: "test-key", Mock: false}
	resp, err := c.Charge(context.Background(), ChargeRequest{
		OrderID:  "ORD-real-1",
		GrossIDR: 30000,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "tx-123", resp.TransactionID)
	assert.Equal(t, "ORD-real-1", resp.OrderID)
}

// TestCharge_HTTPError — defer Body.Close() tetap di-trigger walaupun status 4xx.
func TestCharge_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, ServerKey: "test-key", Mock: false}
	_, err := c.Charge(context.Background(), ChargeRequest{OrderID: "ORD-err", GrossIDR: 1000})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http 500")
}

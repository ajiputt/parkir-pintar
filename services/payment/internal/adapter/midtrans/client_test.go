package midtrans

import (
	"crypto/sha512"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
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

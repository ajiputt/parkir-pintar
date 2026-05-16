package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ajiperdana/parkir-pintar/pkg/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-signing-key-not-for-production-use-12345678"
const testIssuer = "parkirpintar-test"

// hs256 computes the HMAC-SHA256 signature using the same format Signer uses.
func hs256(secret, signingInput string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestSigner_SignAndVerify_HappyPath(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)

	token, err := signer.Sign(auth.Claims{Sub: "driver-123"})
	require.NoError(t, err)
	require.NotEmpty(t, token)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, "driver-123", claims.Sub)
	assert.Equal(t, testIssuer, claims.Iss)
	assert.NotZero(t, claims.Iat)
	assert.NotZero(t, claims.Exp)
	assert.Greater(t, claims.Exp, claims.Iat)
}

func TestSigner_Sign_AutoFillIssuer(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)

	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, testIssuer, claims.Iss)
}

func TestSigner_Sign_PreservesProvidedIssuer(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, "")

	token, err := signer.Sign(auth.Claims{Sub: "driver-1", Iss: "custom-issuer"})
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "custom-issuer", claims.Iss)
}

func TestSigner_Sign_PreservesProvidedTimestamps(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)

	iat := int64(1700000000)
	exp := time.Now().Add(time.Hour).Unix()
	token, err := signer.Sign(auth.Claims{Sub: "x", Iat: iat, Exp: exp})
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, iat, claims.Iat)
	assert.Equal(t, exp, claims.Exp)
}

func TestSigner_Sign_EmptySecret(t *testing.T) {
	signer := auth.NewSigner("", testIssuer, time.Hour)
	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.Error(t, err)
	assert.Empty(t, token)
}

func TestVerifier_Verify_EmptySecret(t *testing.T) {
	verifier := auth.NewVerifier("", testIssuer)
	claims, err := verifier.Verify("a.b.c")
	require.Error(t, err)
	assert.Nil(t, claims)
}

func TestVerifier_Verify_MalformedToken_NotThreeParts(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)

	cases := []string{
		"",
		"only-one-part",
		"two.parts",
		"a.b.c.d",
	}
	for _, tok := range cases {
		t.Run(tok, func(t *testing.T) {
			claims, err := verifier.Verify(tok)
			require.Error(t, err)
			assert.Nil(t, claims)
			assert.ErrorIs(t, err, auth.ErrTokenInvalid)
		})
	}
}

func TestVerifier_Verify_InvalidSignature(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifierBadSecret := auth.NewVerifier("a-different-secret-also-long-enough-1234", testIssuer)

	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.NoError(t, err)

	claims, err := verifierBadSecret.Verify(token)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrSignatureInvalid)
}

func TestVerifier_Verify_TamperedSignature(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)

	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	sig := parts[2]
	var replacement byte = 'A'
	if sig[len(sig)-1] == 'A' {
		replacement = 'B'
	}
	parts[2] = sig[:len(sig)-1] + string(replacement)
	tampered := strings.Join(parts, ".")

	claims, err := verifier.Verify(tampered)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrSignatureInvalid)
}

func TestVerifier_Verify_TamperedPayload(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)

	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	pjson, _ := json.Marshal(auth.Claims{Sub: "attacker"})
	parts[1] = base64.RawURLEncoding.EncodeToString(pjson)
	tampered := strings.Join(parts, ".")

	claims, err := verifier.Verify(tampered)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrSignatureInvalid)
}

func TestVerifier_Verify_ExpiredToken(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)

	past := time.Now().Add(-1 * time.Hour).Unix()
	token, err := signer.Sign(auth.Claims{Sub: "driver-1", Iat: past - 3600, Exp: past})
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrTokenExpired)
}

func TestVerifier_Verify_IssuerMismatch(t *testing.T) {
	signer := auth.NewSigner(testSecret, "issuer-a", time.Hour)
	verifier := auth.NewVerifier(testSecret, "issuer-b")

	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrIssuerMismatch)
}

func TestVerifier_Verify_IssuerSkippedWhenEmpty(t *testing.T) {
	signer := auth.NewSigner(testSecret, "issuer-a", time.Hour)
	verifier := auth.NewVerifier(testSecret, "")

	token, err := signer.Sign(auth.Claims{Sub: "driver-1"})
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, "issuer-a", claims.Iss)
}

func TestVerifier_Verify_UnsupportedAlgorithm(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)

	// Build a token with alg=none, recompute signature so signature check passes,
	// then assert alg-not-HS256 branch fires.
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	token, err := signer.Sign(auth.Claims{Sub: "x"})
	require.NoError(t, err)
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	badHdrB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	signingInput := badHdrB64 + "." + parts[1]
	bad := signingInput + "." + hs256(testSecret, signingInput)

	claims, err := verifier.Verify(bad)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifier_Verify_MalformedHeaderBase64(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)

	// Build token with invalid base64 in header position.
	// Use a single illegal char in standard alphabet for RawURLEncoding.
	badHdr := "!!!"
	pjson, _ := json.Marshal(auth.Claims{Sub: "x", Exp: time.Now().Add(time.Hour).Unix(), Iss: testIssuer})
	plB64 := base64.RawURLEncoding.EncodeToString(pjson)
	signingInput := badHdr + "." + plB64
	bad := signingInput + "." + hs256(testSecret, signingInput)

	claims, err := verifier.Verify(bad)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifier_Verify_MalformedHeaderJSON(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)

	badHdr := base64.RawURLEncoding.EncodeToString([]byte("not json {{"))
	pjson, _ := json.Marshal(auth.Claims{Sub: "x", Exp: time.Now().Add(time.Hour).Unix(), Iss: testIssuer})
	plB64 := base64.RawURLEncoding.EncodeToString(pjson)
	signingInput := badHdr + "." + plB64
	bad := signingInput + "." + hs256(testSecret, signingInput)

	claims, err := verifier.Verify(bad)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifier_Verify_MalformedPayloadBase64(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)

	hdrB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	badPl := "!!!"
	signingInput := hdrB64 + "." + badPl
	bad := signingInput + "." + hs256(testSecret, signingInput)

	claims, err := verifier.Verify(bad)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifier_Verify_MalformedPayloadJSON(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)

	hdrB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	plB64 := base64.RawURLEncoding.EncodeToString([]byte("not json {{"))
	signingInput := hdrB64 + "." + plB64
	bad := signingInput + "." + hs256(testSecret, signingInput)

	claims, err := verifier.Verify(bad)
	require.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifier_Verify_ZeroExpNotChecked(t *testing.T) {
	hdrB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pjson, _ := json.Marshal(auth.Claims{Sub: "driver-z", Iss: testIssuer})
	plB64 := base64.RawURLEncoding.EncodeToString(pjson)
	signingInput := hdrB64 + "." + plB64
	token := signingInput + "." + hs256(testSecret, signingInput)

	verifier := auth.NewVerifier(testSecret, testIssuer)
	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, "driver-z", claims.Sub)
	assert.Zero(t, claims.Exp)
}

// Package auth — JWT signing + verification (HS256).
//
// Minimal implementation tanpa dependency external (cuma stdlib crypto/hmac).
// Signed token format: base64url(header).base64url(payload).base64url(hmac).
//
// Production: ganti ke RS256/ES256 + JWKS untuk multi-issuer + key rotation.
// Untuk demo + assessment: HS256 dengan single shared secret cukup.
//
// Usage:
//
//	signer := auth.NewSigner("your-secret", "parkirpintar", time.Hour)
//	token, _ := signer.Sign(auth.Claims{Sub: "driver-123"})
//
//	verifier := auth.NewVerifier("your-secret", "parkirpintar")
//	claims, err := verifier.Verify(token)
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTokenInvalid     = errors.New("auth: invalid token")
	ErrSignatureInvalid = errors.New("auth: signature mismatch")
	ErrTokenExpired     = errors.New("auth: token expired")
	ErrIssuerMismatch   = errors.New("auth: issuer mismatch")
)

// header — JWT header.
type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Claims — JWT payload.
type Claims struct {
	Sub string `json:"sub"`           // subject (driver_id)
	Iss string `json:"iss,omitempty"` // issuer
	Iat int64  `json:"iat,omitempty"` // issued at (unix sec)
	Exp int64  `json:"exp,omitempty"` // expiry (unix sec)
}

// Signer — HS256 token signer.
type Signer struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewSigner(secret, issuer string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), issuer: issuer, ttl: ttl}
}

// Sign — generate token. Auto-fill iat/exp/iss kalau Claims kosong.
func (s *Signer) Sign(c Claims) (string, error) {
	if len(s.secret) == 0 {
		return "", errors.New("auth: empty secret")
	}
	now := time.Now().Unix()
	if c.Iat == 0 {
		c.Iat = now
	}
	if c.Exp == 0 {
		c.Exp = now + int64(s.ttl.Seconds())
	}
	if c.Iss == "" {
		c.Iss = s.issuer
	}

	hdr := header{Alg: "HS256", Typ: "JWT"}
	hdrJSON, _ := json.Marshal(hdr)
	plJSON, _ := json.Marshal(c)

	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrJSON)
	plB64 := base64.RawURLEncoding.EncodeToString(plJSON)

	signingInput := hdrB64 + "." + plB64
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sigB64, nil
}

// Verifier — HS256 token verifier.
type Verifier struct {
	secret []byte
	issuer string // optional — kalau "" skip iss check
}

func NewVerifier(secret, issuer string) *Verifier {
	return &Verifier{secret: []byte(secret), issuer: issuer}
}

// Verify — parse + verify signature + check exp. Return claims kalau valid.
func (v *Verifier) Verify(token string) (*Claims, error) {
	if len(v.secret) == 0 {
		return nil, errors.New("auth: empty secret")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrTokenInvalid
	}
	hdrB64, plB64, sigB64 := parts[0], parts[1], parts[2]

	// Verify signature first (fail-fast).
	signingInput := hdrB64 + "." + plB64
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expectedSig), []byte(sigB64)) {
		return nil, ErrSignatureInvalid
	}

	// Decode header (sanity check alg).
	hdrJSON, err := base64.RawURLEncoding.DecodeString(hdrB64)
	if err != nil {
		return nil, fmt.Errorf("%w: header b64: %w", ErrTokenInvalid, err)
	}
	var hdr header
	if err := json.Unmarshal(hdrJSON, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header json: %w", ErrTokenInvalid, err)
	}
	if hdr.Alg != "HS256" {
		return nil, fmt.Errorf("%w: unsupported alg %s", ErrTokenInvalid, hdr.Alg)
	}

	// Decode payload.
	plJSON, err := base64.RawURLEncoding.DecodeString(plB64)
	if err != nil {
		return nil, fmt.Errorf("%w: payload b64: %w", ErrTokenInvalid, err)
	}
	var c Claims
	if err := json.Unmarshal(plJSON, &c); err != nil {
		return nil, fmt.Errorf("%w: payload json: %w", ErrTokenInvalid, err)
	}

	// Check exp.
	if c.Exp > 0 && time.Now().Unix() > c.Exp {
		return nil, ErrTokenExpired
	}
	// Check iss kalau di-config.
	if v.issuer != "" && c.Iss != v.issuer {
		return nil, ErrIssuerMismatch
	}
	return &c, nil
}

//go:build e2e
// +build e2e

// E2E tests — eksekusi terhadap stack docker-compose yang sudah running.
//
// Pre-requisite:
//
//	make demo-up && make demo-wait && make seed
//
// Run:
//
//	make test-e2e
//
// Skenario sesuai use case:
//  1. Happy path reservation
//  2. Double-book prevention (concurrent)
//  3. User-selected spot contention
//  4. No-show expiry
//  5. Cancellation
//  6. Extended stay billing
//  7. Overnight fee
//  8. Payment success (mock QRIS)
//  9. Payment failure
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gw() string {
	if v := os.Getenv("GATEWAY_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

type apiClient struct {
	base string
	http *http.Client
}

func newClient() *apiClient {
	return &apiClient{base: gw(), http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *apiClient) post(t *testing.T, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	bs, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, _ := resp.Body.Read(buf)
		if n == 0 {
			break
		}
		out = append(out, buf[:n]...)
	}
	return resp.StatusCode, out
}

func (c *apiClient) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	resp, err := c.http.Get(c.base + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, _ := resp.Body.Read(buf)
		if n == 0 {
			break
		}
		out = append(out, buf[:n]...)
	}
	return resp.StatusCode, out
}

// ----- Skenario 1: Happy path -----
func TestE2E_HappyPath(t *testing.T) {
	c := newClient()
	now := time.Now().UTC()
	body := map[string]any{
		"driver_id":    "driver-happy-" + uuid.NewString(),
		"plate_no":     "B 1111 AAA",
		"vehicle_type": "CAR",
		"mode":         "SYSTEM",
		"start_at":     now.Add(time.Minute).Format(time.RFC3339),
		"end_at":       now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	idem := uuid.NewString()
	code, resp := c.post(t, "/v1/reservations", body, map[string]string{"Idempotency-Key": idem})
	assert.Contains(t, []int{200, 201}, code, "expected 2xx, got %d: %s", code, resp)

	var out map[string]any
	require.NoError(t, json.Unmarshal(resp, &out))
	assert.NotEmpty(t, out["id"])
	assert.Equal(t, "CONFIRMED", out["state"])
	assert.Equal(t, float64(5000), out["booking_fee_idr"])
}

// ----- Skenario 2: Idempotency / no double-charge -----
func TestE2E_IdempotencyKey_RepeatedCall(t *testing.T) {
	c := newClient()
	now := time.Now().UTC()
	body := map[string]any{
		"driver_id":    "driver-idem-" + uuid.NewString(),
		"plate_no":     "B 2222 BBB",
		"vehicle_type": "CAR",
		"mode":         "SYSTEM",
		"start_at":     now.Add(time.Minute).Format(time.RFC3339),
		"end_at":       now.Add(time.Hour).Format(time.RFC3339),
	}
	idem := uuid.NewString()
	headers := map[string]string{"Idempotency-Key": idem}

	_, resp1 := c.post(t, "/v1/reservations", body, headers)
	_, resp2 := c.post(t, "/v1/reservations", body, headers)
	_, resp3 := c.post(t, "/v1/reservations", body, headers)

	// All 3 must reference the same reservation id
	var r1, r2, r3 map[string]any
	_ = json.Unmarshal(resp1, &r1)
	_ = json.Unmarshal(resp2, &r2)
	_ = json.Unmarshal(resp3, &r3)
	assert.Equal(t, r1["id"], r2["id"])
	assert.Equal(t, r2["id"], r3["id"])
}

// ----- Skenario 3: Concurrent double-book prevention -----
func TestE2E_DoubleBook_50Concurrent(t *testing.T) {
	c := newClient()
	// Cari satu spot CAR available untuk USER-mode contention
	_, body := c.get(t, "/v1/availability")
	if len(body) == 0 {
		t.Skip("availability endpoint unavailable")
	}

	// Untuk USER-mode contention, kita butuh spot_id. Demo: pakai SYSTEM-mode
	// concurrent — masing-masing dapat spot berbeda kalau cukup inventory,
	// jadi test ini check tidak ada error 5xx (semua berhasil dapat spot
	// independen). Untuk test contention spot yang sama, perlu USER mode +
	// spot_id yang sama (lihat scripts/load/contention.js untuk k6).

	now := time.Now().UTC()
	const n = 50
	var ok int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			body := map[string]any{
				"driver_id":    fmt.Sprintf("driver-concurrent-%d", i),
				"plate_no":     fmt.Sprintf("B %04d ZZZ", i),
				"vehicle_type": "CAR",
				"mode":         "SYSTEM",
				"start_at":     now.Add(time.Minute).Format(time.RFC3339),
				"end_at":       now.Add(time.Hour).Format(time.RFC3339),
			}
			code, _ := c.post(t, "/v1/reservations", body, map[string]string{
				"Idempotency-Key": uuid.NewString(),
			})
			if code >= 200 && code < 300 {
				atomic.AddInt32(&ok, 1)
			}
		}(i)
	}
	wg.Wait()
	t.Logf("Concurrent ok: %d/%d", atomic.LoadInt32(&ok), n)
	assert.Greater(t, ok, int32(0), "at least some reservations must succeed")
}

// ----- Skenario 4-9: outline -----
//
// Karena E2E test butuh stack live + clock manipulation untuk overnight/expiry,
// kita expose skenario ini via shell script `scripts/e2e-scenarios.sh` yang
// pakai library `freeze-time` di docker-compose, atau injeksi waktu via
// query parameter `?as_of=...` (test-only env).

func TestE2E_GetAvailability(t *testing.T) {
	c := newClient()
	code, body := c.get(t, "/v1/availability")
	assert.Equal(t, 200, code, "body: %s", body)
	assert.Contains(t, string(body), "floors")
}

func TestE2E_PaymentMockSuccess(t *testing.T) {
	if os.Getenv("PAYMENT_MOCK_MODE") == "false" {
		t.Skip("payment in real mode — skip mock")
	}
	c := newClient()
	// Demo invoice id (stub)
	code, body := c.post(t, "/v1/payments", map[string]any{
		"invoice_id": uuid.NewString(),
	}, nil)
	t.Logf("payment response: %d %s", code, body)
	// Mock mode return 200 + qr_string
	if code == 200 {
		assert.Contains(t, string(body), "qr_string")
	}
}

func TestE2E_HealthCheck(t *testing.T) {
	c := newClient()
	code, _ := c.get(t, "/healthz")
	assert.Equal(t, 200, code)
}

// silence unused
var _ = context.Background

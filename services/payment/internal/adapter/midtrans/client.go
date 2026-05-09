// Package midtrans — client untuk Midtrans CoreAPI / Snap (QRIS sandbox).
//
// Untuk demo: jika MOCK=true, return fake QR. Untuk real sandbox: panggil
// /v2/charge dengan payment_type=qris.
package midtrans

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	ServerKey  string
	Mock       bool
	HTTPClient *http.Client
}

type ChargeRequest struct {
	OrderID    string
	GrossIDR   int64
	CustomerID string
}

type ChargeResponse struct {
	TransactionID string
	OrderID       string
	StatusCode    string
	StatusMessage string
	QRString      string
	ExpiryTime    time.Time
	RawResponse   []byte
}

// Charge — call Midtrans /v2/charge dengan payment_type=qris.
func (c *Client) Charge(ctx context.Context, req ChargeRequest) (*ChargeResponse, error) {
	if c.Mock {
		return c.mockCharge(req)
	}

	body := map[string]any{
		"payment_type": "qris",
		"transaction_details": map[string]any{
			"order_id":     req.OrderID,
			"gross_amount": req.GrossIDR,
		},
		"qris": map[string]any{
			"acquirer": "gopay",
		},
	}
	bs, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v2/charge", bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(c.ServerKey, "")

	cli := c.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := cli.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("midtrans charge: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("midtrans charge: http %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		TransactionID string `json:"transaction_id"`
		OrderID       string `json:"order_id"`
		StatusCode    string `json:"status_code"`
		StatusMessage string `json:"status_message"`
		QRString      string `json:"qr_string"`
		Actions       []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"actions"`
		TransactionTime string `json:"transaction_time"`
		ExpiryTime      string `json:"expiry_time"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	exp, _ := time.Parse("2006-01-02 15:04:05", out.ExpiryTime)
	return &ChargeResponse{
		TransactionID: out.TransactionID,
		OrderID:       out.OrderID,
		StatusCode:    out.StatusCode,
		StatusMessage: out.StatusMessage,
		QRString:      out.QRString,
		ExpiryTime:    exp,
		RawResponse:   raw,
	}, nil
}

func (c *Client) mockCharge(req ChargeRequest) (*ChargeResponse, error) {
	return &ChargeResponse{
		TransactionID: "MOCK-" + req.OrderID,
		OrderID:       req.OrderID,
		StatusCode:    "201",
		StatusMessage: "QRIS transaction is created",
		QRString:      fmt.Sprintf("00020101021126670016COM.MOCK.MIDTRANS.WWW0118MOCK%s5204594753033605802ID5907MOCKQRS6011JAKARTA62120708MOCKMOCK6304XXXX", req.OrderID),
		ExpiryTime:    time.Now().Add(15 * time.Minute),
		RawResponse:   []byte(`{"mocked":true}`),
	}, nil
}

// VerifySignatureKey — verifikasi signature notification Midtrans.
//
// Algoritma resmi Midtrans:
//
//	signature = sha512(order_id + status_code + gross_amount + server_key)
//
// Notification body wajib berisi field yang diverifikasi.
func VerifySignatureKey(orderID, statusCode, grossAmount, serverKey, signatureKey string) bool {
	h := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	expected := hex.EncodeToString(h[:])
	return constantTimeEqual(expected, signatureKey)
}

// constantTimeEqual — supaya tidak vulnerable ke timing attack.
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := 0; i < len(a); i++ {
		x |= a[i] ^ b[i]
	}
	return x == 0
}

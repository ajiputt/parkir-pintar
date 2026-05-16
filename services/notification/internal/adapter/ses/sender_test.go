// Tests untuk SES Client. Mostly exercises Mock=true paths (no AWS network).
// The non-mock branch is partially covered by constructing a real client; the
// actual `awsClient.SendEmail` call is not exercised here because the SDK
// client is a concrete *sesclient.Client without a DI seam (production-only
// path). Adding tests for that would require refactoring production code.
package ses

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// helper — build mock client.
func newMockClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(context.Background(), "ap-southeast-1", "no-reply@example.test", "support@example.test", true, zap.NewNop())
	if err != nil {
		t.Fatalf("New(mock=true) error: %v", err)
	}
	if c == nil {
		t.Fatal("New returned nil client")
	}
	return c
}

func TestNew_MockMode_DoesNotInitAWS(t *testing.T) {
	c := newMockClient(t)
	if !c.Mock {
		t.Error("expected Mock=true")
	}
	if c.awsClient != nil {
		t.Error("expected awsClient nil in mock mode")
	}
	if c.Region != "ap-southeast-1" {
		t.Errorf("region mismatch: %s", c.Region)
	}
	if c.From != "no-reply@example.test" {
		t.Errorf("from mismatch: %s", c.From)
	}
	if c.ReplyTo != "support@example.test" {
		t.Errorf("reply-to mismatch: %s", c.ReplyTo)
	}
}

func TestNew_NonMockMode_InitsAWS(t *testing.T) {
	// LoadDefaultConfig is lenient and doesn't require credentials at config
	// load time, so this should succeed even without env vars set. We don't
	// call Send so no network is touched.
	c, err := New(context.Background(), "ap-southeast-1", "no-reply@example.test", "", false, zap.NewNop())
	if err != nil {
		t.Skipf("LoadDefaultConfig failed in this environment (expected on some CI without any AWS profile): %v", err)
	}
	if c == nil {
		t.Fatal("nil client")
	}
	if c.Mock {
		t.Error("expected Mock=false")
	}
	if c.awsClient == nil {
		t.Error("expected awsClient initialised in non-mock mode")
	}
}

func TestSend_MockMode_ShortBody(t *testing.T) {
	c := newMockClient(t)
	err := c.Send(context.Background(), "driver@example.test", "Driver Name", "Welcome", "Hello world")
	if err != nil {
		t.Errorf("Send returned err: %v", err)
	}
}

func TestSend_MockMode_LongBody_TriggersTruncationBranch(t *testing.T) {
	c := newMockClient(t)
	// Build a body > 200 bytes to exercise the truncation path inside Send.
	body := strings.Repeat("A", 250)
	err := c.Send(context.Background(), "driver@example.test", "Driver", "Subj", body)
	if err != nil {
		t.Errorf("Send returned err: %v", err)
	}
}

func TestSend_MockMode_EmptyAddrs_StillReturnsNil(t *testing.T) {
	c := newMockClient(t)
	// In mock mode, no validation happens — we just log and return nil. This
	// is documented behaviour: mock = log-only.
	err := c.Send(context.Background(), "", "", "", "")
	if err != nil {
		t.Errorf("Send returned err: %v", err)
	}
}

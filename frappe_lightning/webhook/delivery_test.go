package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"context"
)

// TestHMACSign verifies the signature is deterministic and matches independent verification.
func TestHMACSign(t *testing.T) {
	secret := "test-secret-key"
	payload := []byte(`{"doctype":"Customer","name":"CUST-0001"}`)

	sig1 := hmacSign(secret, payload)
	sig2 := hmacSign(secret, payload)

	if sig1 != sig2 {
		t.Fatal("HMAC signatures should be deterministic")
	}

	// Independently verify using stdlib.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))

	if sig1 != expected {
		t.Fatalf("HMAC mismatch: got %q, expected %q", sig1, expected)
	}
}

// TestHMACSigns_DifferentSecrets verifies different secrets produce different signatures.
func TestHMACSigns_DifferentSecrets(t *testing.T) {
	payload := []byte(`{"name":"test"}`)
	sig1 := hmacSign("secret-a", payload)
	sig2 := hmacSign("secret-b", payload)

	if sig1 == sig2 {
		t.Fatal("different secrets should produce different HMAC signatures")
	}
}

// TestHMACSigns_DifferentPayloads verifies different payloads produce different signatures.
func TestHMACSigns_DifferentPayloads(t *testing.T) {
	secret := "shared-secret"
	sig1 := hmacSign(secret, []byte(`{"name":"A"}`))
	sig2 := hmacSign(secret, []byte(`{"name":"B"}`))

	if sig1 == sig2 {
		t.Fatal("different payloads should produce different HMAC signatures")
	}
}

// TestDeliverer_SuccessfulDelivery verifies a 200 response is marked as success.
func TestDeliverer_SuccessfulDelivery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify required headers are present.
		if r.Header.Get("X-Lightning-Signature") == "" {
			t.Error("missing X-Lightning-Signature header")
		}
		if r.Header.Get("X-Lightning-Event") == "" {
			t.Error("missing X-Lightning-Event header")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"received"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	del := NewDeliverer()
	task := DeliveryTask{
		DeliveryID: "test-001",
		Event: WebhookEvent{
			Site:    "erp.local",
			DocType: "Customer",
			Name:    "CUST-0001",
			Event:   "on_update",
		},
		Sub: WebhookSubscription{
			EndpointURL: srv.URL,
			SecretKey:   "my-secret",
			TimeoutSec:  5,
		},
	}

	result := del.Send(context.Background(), task)

	if !result.Success {
		t.Fatalf("expected success, got error: %s (status %d)", result.Error, result.StatusCode)
	}
	if result.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", result.StatusCode)
	}
	if result.LatencyMs < 0 {
		t.Fatal("latency should be non-negative")
	}
}

// TestDeliverer_FailureOn5xx verifies a 500 response is marked as failed.
func TestDeliverer_FailureOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`internal error`)) //nolint:errcheck
	}))
	defer srv.Close()

	del := NewDeliverer()
	task := DeliveryTask{
		DeliveryID: "test-002",
		Event:      WebhookEvent{DocType: "Customer", Event: "on_update"},
		Sub:        WebhookSubscription{EndpointURL: srv.URL, TimeoutSec: 5},
	}

	result := del.Send(context.Background(), task)

	if result.Success {
		t.Fatal("expected failure for 500 response")
	}
	if result.StatusCode != 500 {
		t.Fatalf("expected 500, got %d", result.StatusCode)
	}
}

// TestDeliverer_ReceiverCanVerifySignature verifies the full sign-and-verify cycle.
func TestDeliverer_ReceiverCanVerifySignature(t *testing.T) {
	secret := "webhook-secret-123"
	var capturedSig string
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = r.Header.Get("X-Lightning-Signature")
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	del := NewDeliverer()
	event := WebhookEvent{
		Site:    "erp.local",
		DocType: "Sales Invoice",
		Name:    "SINV-0001",
		Event:   "on_submit",
	}
	task := DeliveryTask{
		DeliveryID: "test-003",
		Event:      event,
		Sub:        WebhookSubscription{EndpointURL: srv.URL, SecretKey: secret, TimeoutSec: 5},
	}

	result := del.Send(context.Background(), task)
	if !result.Success {
		t.Fatalf("delivery failed: %s", result.Error)
	}

	// Receiver-side verification: re-compute signature and compare.
	expectedPayload, _ := json.Marshal(event)
	expectedSig := "sha256=" + hmacSign(secret, expectedPayload)

	if capturedSig != expectedSig {
		t.Fatalf("signature mismatch:\n  sent:     %s\n  expected: %s", capturedSig, expectedSig)
	}
	if string(capturedBody) != string(expectedPayload) {
		t.Fatalf("body mismatch:\n  sent:     %s\n  expected: %s", capturedBody, expectedPayload)
	}
}

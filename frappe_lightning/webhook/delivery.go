package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// DeliveryTask is a unit of work dispatched to a delivery worker goroutine.
type DeliveryTask struct {
	DeliveryID string              `json:"delivery_id"`
	Event      WebhookEvent        `json:"event"`
	Sub        WebhookSubscription `json:"sub"`
	AttemptNum int                 `json:"attempt_num"`
	StreamMsgID string             `json:"stream_msg_id,omitempty"`
}

// DeliveryResult holds the outcome of a single HTTP delivery attempt.
type DeliveryResult struct {
	DeliveryID   string
	AttemptNum   int
	StatusCode   int
	LatencyMs    int64
	Success      bool
	Error        string
	ResponseBody string
	AttemptedAt  time.Time
}

// Deliverer sends webhook payloads to subscriber endpoints.
type Deliverer struct{}

// NewDeliverer creates a Deliverer.
func NewDeliverer() *Deliverer { return &Deliverer{} }

// Send performs a single HTTP POST delivery attempt and returns the result.
func (d *Deliverer) Send(ctx context.Context, task DeliveryTask) DeliveryResult {
	result := DeliveryResult{
		DeliveryID:  task.DeliveryID,
		AttemptNum:  task.AttemptNum,
		AttemptedAt: time.Now(),
	}

	payload, err := json.Marshal(task.Event)
	if err != nil {
		result.Error = fmt.Sprintf("marshal payload: %v", err)
		return result
	}

	sig := hmacSign(task.Sub.SecretKey, payload)
	deliveryID := task.DeliveryID
	if deliveryID == "" {
		deliveryID = uuid.New().String()
		result.DeliveryID = deliveryID
	}

	timeout := time.Duration(task.Sub.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, task.Sub.EndpointURL, bytes.NewReader(payload))
	if err != nil {
		result.Error = fmt.Sprintf("build request: %v", err)
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lightning-Signature", "sha256="+sig)
	req.Header.Set("X-Lightning-Event", task.Event.Event)
	req.Header.Set("X-Lightning-Delivery", deliveryID)
	req.Header.Set("X-Lightning-Attempt", fmt.Sprintf("%d", task.AttemptNum+1))

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result.StatusCode = resp.StatusCode
	result.ResponseBody = string(body)
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 300
	return result
}

// hmacSign returns the hex-encoded HMAC-SHA256 signature of payload.
func hmacSign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// NewDeliveryID generates a fresh unique delivery ID.
func NewDeliveryID() string { return uuid.New().String() }

package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Embedder client for interacting with the Python embedding server.
type Embedder struct {
	url     string
	client  *http.Client
}

// NewEmbedder initializes the embedding client with a given server URL.
func NewEmbedder(url string) *Embedder {
	return &Embedder{
		url: url,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// EmbedRequest represents the JSON payload sent to the embedding server.
type EmbedRequest struct {
	Text string `json:"text"`
}

// EmbedResponse represents the JSON payload received from the embedding server.
type EmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed generates a vector representation of the provided text.
func (e *Embedder) Embed(text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	payload := EmbedRequest{Text: text}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embed request: %w", err)
	}

	resp, err := e.client.Post(e.url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("embedding server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding server returned status %d", resp.StatusCode)
	}

	var result EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	return result.Embedding, nil
}

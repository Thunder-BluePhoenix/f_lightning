package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestEmbedder_Embed(t *testing.T) {
	// Mock embedding server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST request, got %s", r.Method)
		}

		var req EmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		if req.Text == "hello" {
			resp := EmbedResponse{Embedding: []float32{0.1, 0.2, 0.3}}
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	embedder := NewEmbedder(server.URL)

	tests := []struct {
		name    string
		text    string
		want    []float32
		wantErr bool
	}{
		{
			name:    "successful embedding",
			text:    "hello",
			want:    []float32{0.1, 0.2, 0.3},
			wantErr: false,
		},
		{
			name:    "empty text",
			text:    "",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "server error",
			text:    "error",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := embedder.Embed(tt.text)
			if (err != nil) != tt.wantErr {
				t.Errorf("Embedder.Embed() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Embedder.Embed() = %v, want %v", got, tt.want)
			}
		})
	}
}

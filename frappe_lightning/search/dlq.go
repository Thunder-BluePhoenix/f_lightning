package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DLQEvent represents a document that permanently failed to index.
type DLQEvent struct {
	IndexName  string                 `json:"index_name"`
	Timestamp  string                 `json:"timestamp"`
	Document   map[string]interface{} `json:"document,omitempty"`
	RawPayload interface{}            `json:"raw_payload,omitempty"`
	Error      string                 `json:"error"`
}

// DLQManager handles persisting failed events to a local file queue.
type DLQManager struct {
	baseDir string
	mu      sync.Mutex
	log     *zap.Logger
}

func NewDLQManager(log *zap.Logger) *DLQManager {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".lightning", "dlq")
	_ = os.MkdirAll(dir, 0755)
	return &DLQManager{
		baseDir: dir,
		log:     log,
	}
}

// Push writes a permanently failed event to the Dead Letter Queue.
func (d *DLQManager) Push(indexName string, payload interface{}, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	event := DLQEvent{
		IndexName:  indexName,
		Timestamp:  time.Now().Format(time.RFC3339),
		RawPayload: payload,
		Error:      err.Error(),
	}

	data, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		d.log.Error("failed to marshal DLQ event", zap.Error(marshalErr))
		return
	}
	data = append(data, '\n')

	filename := filepath.Join(d.baseDir, fmt.Sprintf("%s.jsonl", indexName))
	f, fileErr := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if fileErr != nil {
		d.log.Error("failed to open DLQ file", zap.String("file", filename), zap.Error(fileErr))
		return
	}
	defer f.Close()

	if _, writeErr := f.Write(data); writeErr != nil {
		d.log.Error("failed to write to DLQ file", zap.Error(writeErr))
	} else {
		d.log.Info("event pushed to dead letter queue", zap.String("index", indexName))
	}
}

package search

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	maxBatchSize = 100
	maxBatchWait = 500 * time.Millisecond
	maxRetries   = 5
)

// Batcher buffers documents and flushes them to Meilisearch in batches.
// It flushes when either maxBatchSize docs are queued OR maxBatchWait elapses.
type Batcher struct {
	mu      sync.Mutex
	buffers map[string][]interface{} // indexName → pending docs
	flushFn func(indexName string, docs []interface{}) error
	log     *zap.Logger
	dlq     *DLQManager
	ticker  *time.Ticker
	quit    chan struct{}
}

// NewBatcher creates and starts a Batcher.
func NewBatcher(flushFn func(string, []interface{}) error, log *zap.Logger, dlq *DLQManager) *Batcher {
	b := &Batcher{
		buffers: make(map[string][]interface{}),
		flushFn: flushFn,
		log:     log,
		dlq:     dlq,
		ticker:  time.NewTicker(maxBatchWait),
		quit:    make(chan struct{}),
	}
	go b.tickerLoop()
	return b
}

// Add appends a document to the buffer for the given index.
// If the buffer reaches maxBatchSize it is flushed immediately.
func (b *Batcher) Add(indexName string, doc interface{}) {
	b.mu.Lock()
	b.buffers[indexName] = append(b.buffers[indexName], doc)
	shouldFlush := len(b.buffers[indexName]) >= maxBatchSize
	b.mu.Unlock()

	if shouldFlush {
		b.flushIndex(indexName)
	}
}

// tickerLoop flushes all pending buffers every maxBatchWait.
func (b *Batcher) tickerLoop() {
	for {
		select {
		case <-b.ticker.C:
			b.flushAll()
		case <-b.quit:
			return
		}
	}
}

func (b *Batcher) flushAll() {
	b.mu.Lock()
	indexes := make([]string, 0, len(b.buffers))
	for idx := range b.buffers {
		indexes = append(indexes, idx)
	}
	b.mu.Unlock()

	for _, idx := range indexes {
		b.flushIndex(idx)
	}
}

func (b *Batcher) flushIndex(indexName string) {
	b.mu.Lock()
	docs := b.buffers[indexName]
	b.buffers[indexName] = nil
	b.mu.Unlock()

	if len(docs) == 0 {
		return
	}
	go b.sendWithRetry(indexName, docs, 0)
}

var retryDelays = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	60 * time.Second,
}

func (b *Batcher) sendWithRetry(indexName string, docs []interface{}, attempt int) {
	if err := b.flushFn(indexName, docs); err != nil {
		if attempt < maxRetries {
			delay := retryDelays[attempt]
			b.log.Warn("meilisearch flush failed, retrying",
				zap.String("index", indexName),
				zap.Int("attempt", attempt+1),
				zap.Duration("delay", delay),
				zap.Error(err),
			)
			time.Sleep(delay)
			b.sendWithRetry(indexName, docs, attempt+1)
		} else {
			b.log.Error("meilisearch flush permanently failed — docs sent to DLQ",
				zap.String("index", indexName),
				zap.Int("docs", len(docs)),
				zap.Error(err),
			)
			if b.dlq != nil {
				b.dlq.Push(indexName, docs, err)
			}
		}
	} else {
		b.log.Info("flushed batch to meilisearch",
			zap.String("index", indexName),
			zap.Int("docs", len(docs)),
		)
	}
}

// Stop gracefully shuts down the ticker loop.
func (b *Batcher) Stop() {
	b.ticker.Stop()
	close(b.quit)
	b.flushAll() // Final flush on shutdown
}

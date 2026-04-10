package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// WebhookSubscription mirrors a row in the `tabLightning Webhook` DocType.
type WebhookSubscription struct {
	Name        string   `json:"name"`
	DocType     string   `json:"doctype"`      // "*" matches all
	Events      []string `json:"events"`       // ["after_insert", "on_update"]
	EndpointURL string   `json:"endpoint_url"`
	SecretKey   string   `json:"secret_key"`
	Enabled     bool     `json:"enabled"`
	MaxRetries  int      `json:"max_retries"` // default 5
	TimeoutSec  int      `json:"timeout_sec"` // default 10
}

// SubscriptionStore loads subscriptions from MariaDB and caches them in memory.
// A background goroutine refreshes the cache every 60 seconds.
type SubscriptionStore struct {
	site  string
	dsn   string
	rdb   *redis.Client
	mu    sync.RWMutex
	cache []WebhookSubscription
	log   *zap.Logger
}

// NewSubscriptionStore creates a SubscriptionStore. Call Start() to begin the
// background refresh goroutine.
func NewSubscriptionStore(site, dsn string, rdb *redis.Client, log *zap.Logger) *SubscriptionStore {
	return &SubscriptionStore{site: site, dsn: dsn, rdb: rdb, log: log}
}

// Start loads subscriptions immediately, then refreshes every 60 seconds until
// ctx is cancelled.
func (s *SubscriptionStore) Start(ctx context.Context) {
	s.reload()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reload()
		}
	}
}

// Match returns all enabled subscriptions that apply to the given doctype/event.
func (s *SubscriptionStore) Match(doctype, event string) []WebhookSubscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []WebhookSubscription
	for _, sub := range s.cache {
		if !sub.Enabled {
			continue
		}
		if sub.DocType != "*" && !strings.EqualFold(sub.DocType, doctype) {
			continue
		}
		if !containsEvent(sub.Events, event) {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// All returns a snapshot of all subscriptions (for CLI display).
func (s *SubscriptionStore) All() []WebhookSubscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]WebhookSubscription, len(s.cache))
	copy(out, s.cache)
	return out
}

func (s *SubscriptionStore) reload() {
	subs, err := s.loadFromDB()
	if err != nil {
		s.log.Error("failed to reload webhook subscriptions",
			zap.String("site", s.site), zap.Error(err))
		return
	}
	s.mu.Lock()
	s.cache = subs
	s.mu.Unlock()
	s.log.Debug("webhook subscriptions reloaded",
		zap.String("site", s.site), zap.Int("count", len(subs)))
}

func (s *SubscriptionStore) loadFromDB() ([]WebhookSubscription, error) {
	if s.dsn == "" {
		return nil, fmt.Errorf("no DSN configured for site %q", s.site)
	}
	db, err := sql.Open("mysql", s.dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT name, doctype, events, endpoint_url,
		       IFNULL(secret_key,''), enabled,
		       IFNULL(max_retries,5), IFNULL(timeout_sec,10)
		FROM   ` + "`tabLightning Webhook`" + `
		WHERE  enabled = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []WebhookSubscription
	for rows.Next() {
		var sub WebhookSubscription
		var eventsJSON string
		var enabled int
		if err := rows.Scan(
			&sub.Name, &sub.DocType, &eventsJSON, &sub.EndpointURL,
			&sub.SecretKey, &enabled, &sub.MaxRetries, &sub.TimeoutSec,
		); err != nil {
			continue
		}
		sub.Enabled = enabled == 1
		// events stored as JSON array in the DB
		json.Unmarshal([]byte(eventsJSON), &sub.Events) //nolint:errcheck
		if sub.MaxRetries == 0 {
			sub.MaxRetries = 5
		}
		if sub.TimeoutSec == 0 {
			sub.TimeoutSec = 10
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func containsEvent(events []string, target string) bool {
	for _, e := range events {
		if strings.EqualFold(e, target) {
			return true
		}
	}
	return false
}

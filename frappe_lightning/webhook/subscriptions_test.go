package webhook

import (
	"testing"
)

// TestSubscriptionMatch_ExactDocType verifies that a subscription for a specific
// DocType only matches that DocType.
func TestSubscriptionMatch_ExactDocType(t *testing.T) {
	store := &SubscriptionStore{
		cache: []WebhookSubscription{
			{
				Name:        "sub-1",
				DocType:     "Customer",
				Events:      []string{"on_update", "after_insert"},
				EndpointURL: "https://example.com/hook",
				Enabled:     true,
				MaxRetries:  5,
				TimeoutSec:  10,
			},
		},
	}

	matches := store.Match("Customer", "on_update")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match for Customer/on_update, got %d", len(matches))
	}

	// Different DocType should not match.
	noMatch := store.Match("Supplier", "on_update")
	if len(noMatch) != 0 {
		t.Fatalf("expected 0 matches for Supplier/on_update, got %d", len(noMatch))
	}
}

// TestSubscriptionMatch_Wildcard verifies that a "*" DocType subscription
// matches any DocType.
func TestSubscriptionMatch_Wildcard(t *testing.T) {
	store := &SubscriptionStore{
		cache: []WebhookSubscription{
			{
				Name:        "global-sub",
				DocType:     "*",
				Events:      []string{"after_insert"},
				EndpointURL: "https://example.com/all",
				Enabled:     true,
				MaxRetries:  3,
				TimeoutSec:  5,
			},
		},
	}

	doctypes := []string{"Customer", "Supplier", "Sales Invoice", "Purchase Order"}
	for _, dt := range doctypes {
		matches := store.Match(dt, "after_insert")
		if len(matches) != 1 {
			t.Errorf("wildcard should match %q/after_insert, got %d matches", dt, len(matches))
		}
	}
}

// TestSubscriptionMatch_DisabledSkipped verifies disabled subscriptions are excluded.
func TestSubscriptionMatch_DisabledSkipped(t *testing.T) {
	store := &SubscriptionStore{
		cache: []WebhookSubscription{
			{
				Name:        "disabled-sub",
				DocType:     "Customer",
				Events:      []string{"on_update"},
				EndpointURL: "https://example.com/hook",
				Enabled:     false, // disabled
			},
		},
	}

	matches := store.Match("Customer", "on_update")
	if len(matches) != 0 {
		t.Fatalf("disabled subscription should not match, got %d matches", len(matches))
	}
}

// TestSubscriptionMatch_EventNotInList verifies that an event not in the
// subscription's Events list does not match.
func TestSubscriptionMatch_EventNotInList(t *testing.T) {
	store := &SubscriptionStore{
		cache: []WebhookSubscription{
			{
				Name:        "insert-only",
				DocType:     "Customer",
				Events:      []string{"after_insert"},
				EndpointURL: "https://example.com/hook",
				Enabled:     true,
				MaxRetries:  3,
				TimeoutSec:  10,
			},
		},
	}

	matches := store.Match("Customer", "on_update") // not subscribed to on_update
	if len(matches) != 0 {
		t.Fatalf("expected no matches for unsubscribed event, got %d", len(matches))
	}
}

// TestSubscriptionMatch_MultipleSubscriptions verifies that multiple matching
// subscriptions are all returned.
func TestSubscriptionMatch_MultipleSubscriptions(t *testing.T) {
	store := &SubscriptionStore{
		cache: []WebhookSubscription{
			{Name: "sub-a", DocType: "Customer", Events: []string{"on_update"}, EndpointURL: "https://a.com", Enabled: true, MaxRetries: 3, TimeoutSec: 10},
			{Name: "sub-b", DocType: "Customer", Events: []string{"on_update"}, EndpointURL: "https://b.com", Enabled: true, MaxRetries: 3, TimeoutSec: 10},
			{Name: "sub-c", DocType: "Supplier", Events: []string{"on_update"}, EndpointURL: "https://c.com", Enabled: true, MaxRetries: 3, TimeoutSec: 10},
		},
	}

	matches := store.Match("Customer", "on_update")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches for Customer/on_update, got %d", len(matches))
	}
}

// TestContainsEvent verifies case-insensitive event matching.
func TestContainsEvent(t *testing.T) {
	events := []string{"on_update", "after_insert", "on_submit"}

	if !containsEvent(events, "on_update") {
		t.Error("should match on_update")
	}
	if !containsEvent(events, "ON_UPDATE") {
		t.Error("should match ON_UPDATE (case insensitive)")
	}
	if containsEvent(events, "on_trash") {
		t.Error("should not match on_trash")
	}
}

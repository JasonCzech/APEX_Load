//go:build jms_servicebus_real

package jms

import (
	"testing"
	"time"
)

// TestServiceBusEvictsExpiredInflight covers xcut-resource-2: peek-locked entries
// whose broker lock has expired must be evicted (the lock is lost server-side, so
// the token can never be settled), while live and lock-less entries are kept.
func TestServiceBusEvictsExpiredInflight(t *testing.T) {
	a := &serviceBusAdapter{inflight: make(map[string]inflightMessage)}
	now := time.Now()
	a.inflight["expired"] = inflightMessage{lockedUntil: now.Add(-time.Minute)}
	a.inflight["live"] = inflightMessage{lockedUntil: now.Add(time.Minute)}
	a.inflight["nolock"] = inflightMessage{} // zero expiry: never evicted by expiry

	a.mu.Lock()
	a.evictExpiredInflightLocked(now)
	a.mu.Unlock()

	if _, ok := a.inflight["expired"]; ok {
		t.Fatal("expired peek-lock entry should have been evicted")
	}
	if _, ok := a.inflight["live"]; !ok {
		t.Fatal("live peek-lock entry must be retained")
	}
	if _, ok := a.inflight["nolock"]; !ok {
		t.Fatal("entry without a lock expiry must not be evicted by expiry")
	}
}

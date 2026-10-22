package browser

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestBrowserOutcomeClockCapturesEffectiveDeadlineBeforeDelayedRead(t *testing.T) {
	b := &Browser{}
	action := &protocol.Action{Sync: &protocol.BrowserSyncPolicy{Type: "duration", Value: "15", TimeoutMs: 1000}}
	var clock protocol.BrowserOutcomeClock
	if err := b.runPostActionSyncWithClock(context.Background(), action, &clock); err != nil {
		t.Fatal(err)
	}
	if !clock.Valid() || clock.ElapsedMS < 10 {
		t.Fatal(clock)
	}
	observed := clock.ObservedAt
	time.Sleep(30 * time.Millisecond)
	if clock.ObservedAt != observed || time.Since(observed) < 25*time.Millisecond {
		t.Fatal("later read changed observed end")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	action.Sync.Value = "500"
	if err := b.runPostActionSyncWithClock(ctx, action, &clock); err == nil {
		t.Fatal("parent deadline not honored")
	}
	if !clock.Valid() || clock.DeadlineAt.Sub(clock.StartedAt) >= 100*time.Millisecond || clock.BudgetMS != 1000 {
		t.Fatal("parent deadline was reported as the authored budget", clock)
	}
	var unknown protocol.BrowserOutcomeClock
	action.Sync.Type = "unsupported"
	if err := b.runPostActionSyncWithClock(context.Background(), action, &unknown); err != nil || unknown.Valid() {
		t.Fatal("unevaluated wait acquired clock", unknown, err)
	}
}

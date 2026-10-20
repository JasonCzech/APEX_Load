package storage

import (
	"context"
	"testing"
	"time"
)

func TestTelemetryNotificationDeliveryAdmissionRechecksLeaseAndIncarnation(t *testing.T) {
	s := newHAFencingStore(t)
	checkTelemetryNotificationDeliveryAdmission(t, s)
}
func checkTelemetryNotificationDeliveryAdmission(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	for _, mode := range []string{"valid", "expired", "replacement", "deleted", "incarnation", "completed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			event, now := telemetryOutboxFixture(t, s, "admit-"+mode)
			id, _, err := s.EnqueueTelemetryNotification(ctx, event, now)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.DeleteTest(event.TestID); err != nil {
					t.Error(err)
				}
			}()
			claim, err := s.ClaimTelemetryNotificationDelivery(ctx, TelemetryNotificationDelivery{EventID: id, Destination: event.Destinations[0]}, now, time.Second)
			if err != nil || claim == nil {
				t.Fatal(claim, err)
			}
			at := now
			checkCtx := ctx
			switch mode {
			case "expired":
				at = now.Add(time.Second)
			case "replacement":
				next, err := s.ClaimTelemetryNotificationDelivery(ctx, *claim, now.Add(time.Second), time.Minute)
				if err != nil || next == nil {
					t.Fatal(next, err)
				}
				at = now.Add(time.Second)
			case "deleted":
				if err := s.DeleteTest(event.TestID); err != nil {
					t.Fatal(err)
				}
			case "incarnation":
				// SaveTest preserves initial provenance; emulate an actual replaced
				// durable incarnation rather than an ignored mutable projection.
				if _, err := s.db.Exec(`UPDATE tests SET initial_provenance_json=? WHERE id=?`, `{"run_incarnation":"replacement"}`, event.TestID); err != nil {
					t.Fatal(err)
				}
			case "completed":
				if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *claim, "delivered", now); err != nil || !ok {
					t.Fatal(ok, err)
				}
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				checkCtx = c
			}
			ok, err := s.AuthorizeTelemetryNotificationDelivery(checkCtx, *claim, at)
			if ok != (mode == "valid") || (err != nil) != (mode == "canceled") {
				t.Fatal("admission", ok, err)
			}
		})
	}
}

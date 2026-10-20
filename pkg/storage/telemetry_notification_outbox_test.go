package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func telemetryOutboxFixture(t *testing.T, s *SQLiteStore, id string) (TelemetryNotification, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := now.Add(-time.Minute)
	if err := s.SaveTest(&TestRecord{ID: id, ProjectID: DefaultProjectID, Name: id, Status: "FAILED", StartTime: start, EndTime: &now, InitialProvenance: map[string]any{"run_incarnation": "incarnation-" + id}}); err != nil {
		t.Fatal(err)
	}
	return TelemetryNotification{TestID: id, ProjectID: DefaultProjectID, RunIncarnation: "incarnation-" + id, PolicyID: "policy-a", PolicyRevision: 1, AssessmentRevision: strings.Repeat("a", 64), Outcome: "fail", CompletedAt: now, Payload: json.RawMessage(`{"type":"telemetry_acceptance","summary":"missing source window"}`), Destinations: []string{strings.Repeat("b", 64), strings.Repeat("c", 64)}}, now
}

func TestTelemetryNotificationOutboxContract(t *testing.T) {
	checkTelemetryNotificationOutboxContract(t, newHAFencingStore(t))
}
func checkTelemetryNotificationOutboxContract(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	in, now := telemetryOutboxFixture(t, s, "outbox-contract")
	id, state, err := s.EnqueueTelemetryNotification(ctx, in, now)
	if err != nil || state != "enqueued" {
		t.Fatalf("enqueue=%s %v", state, err)
	}
	in.Destinations[0], in.Destinations[1] = in.Destinations[1], in.Destinations[0]
	if again, state, err := s.EnqueueTelemetryNotification(ctx, in, now.Add(time.Second)); err != nil || state != "replayed" || again != id {
		t.Fatalf("replay=%s %s %v", again, state, err)
	}
	changed := in
	changed.Payload = json.RawMessage(`{"summary":"changed"}`)
	if _, _, err := s.EnqueueTelemetryNotification(ctx, changed, now); err == nil {
		t.Fatal("immutable body changed")
	}
	changed = in
	changed.Destinations = []string{strings.Repeat("e", 64)}
	if _, _, err := s.EnqueueTelemetryNotification(ctx, changed, now); err == nil {
		t.Fatal("immutable destinations changed")
	}
	stored, err := s.GetTelemetryNotification(ctx, id)
	if err != nil || string(stored.Payload) != string(in.Payload) || len(stored.Destinations) != 2 {
		t.Fatalf("stored=%+v %v", stored, err)
	}
	if claimed, err := s.ClaimRunNotification(in.TestID, "telemetry_acceptance:fail", now); err != nil || claimed {
		t.Fatal("old binary could duplicate new outcome")
	}
	changed = in
	changed.AssessmentRevision = strings.Repeat("d", 64)
	if another, state, err := s.EnqueueTelemetryNotification(ctx, changed, now); err != nil || state != "enqueued" || another == id {
		t.Fatal("assessment revision did not create independent outcome")
	}
	changed = in
	changed.PolicyRevision++
	if another, state, err := s.EnqueueTelemetryNotification(ctx, changed, now); err != nil || state != "enqueued" || another == id {
		t.Fatal("policy revision did not create independent outcome")
	}
	withoutRecipients := in
	withoutRecipients.PolicyRevision = 3
	withoutRecipients.Destinations = nil
	suppressedID, state, err := s.EnqueueTelemetryNotification(ctx, withoutRecipients, now)
	if err != nil || state != "enqueued" {
		t.Fatalf("no-recipient decision=%s %v", state, err)
	}
	withoutRecipients.Destinations = []string{}
	if again, state, err := s.EnqueueTelemetryNotification(ctx, withoutRecipients, now); err != nil || state != "replayed" || again != suppressedID {
		t.Fatalf("empty recipient replay=%s %s %v", again, state, err)
	}
	withoutRecipients.Destinations = in.Destinations
	if _, _, err := s.EnqueueTelemetryNotification(ctx, withoutRecipients, now); err == nil {
		t.Fatal("later configuration rerouted no-recipient outcome")
	}
	if stored, err := s.GetTelemetryNotification(ctx, suppressedID); err != nil || len(stored.Destinations) != 0 {
		t.Fatal("no-recipient decision not retained", err)
	}
	due, err := s.ListDueTelemetryNotificationDeliveries(ctx, now, 100)
	if err != nil || len(due) != 6 {
		t.Fatalf("due=%d %v", len(due), err)
	}
	var candidate TelemetryNotificationDelivery
	for _, item := range due {
		if item.EventID == id {
			candidate = item
			break
		}
	}
	claim, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now, time.Minute)
	if err != nil || claim == nil || claim.Attempts != 1 || claim.OwnerToken == "" {
		t.Fatalf("claim=%+v %v", claim, err)
	}
	if again, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now, time.Minute); err != nil || again != nil {
		t.Fatal("same attempt double claimed")
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *claim, "retry", now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("retry completion=%v %v", ok, err)
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *claim, "delivered", now.Add(time.Second)); err != nil || ok {
		t.Fatal("consumed owner token reused")
	}
	due, err = s.ListDueTelemetryNotificationDeliveries(ctx, now.Add(2*time.Second), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range due {
		if item.EventID == id && item.Destination == candidate.Destination {
			candidate = item
		}
	}
	next, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now.Add(2*time.Second), time.Second)
	if err != nil || next == nil || next.Attempts != 2 || next.OwnerToken == claim.OwnerToken {
		t.Fatalf("retry claim=%+v %v", next, err)
	}
	// Crash after sending but before recording the result: expiry is retryable.
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *next, "delivered", now.Add(3*time.Second)); err != nil || ok {
		t.Fatal("expired acknowledgement committed")
	}
	newer, err := s.ClaimTelemetryNotificationDelivery(ctx, *next, now.Add(3*time.Second), time.Minute)
	if err != nil || newer == nil || newer.Attempts != 3 {
		t.Fatalf("expired lease recovery=%+v %v", newer, err)
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *next, "delivered", now.Add(3*time.Second)); err != nil || ok {
		t.Fatal("stale owner replaced successor")
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *newer, "delivered", now.Add(4*time.Second)); err != nil || !ok {
		t.Fatalf("delivered=%v %v", ok, err)
	}
	due, err = s.ListDueTelemetryNotificationDeliveries(ctx, now.Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range due {
		if item.EventID == id && item.Destination == candidate.Destination {
			t.Fatal("delivered outcome retried")
		}
	}
	// Delete cascades through both event and destinations; recreation cannot
	// attach stale completion to the same textual ID.
	if err := s.DeleteTest(in.TestID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTelemetryNotification(ctx, id); err != sql.ErrNoRows {
		t.Fatalf("deleted event=%v", err)
	}
	due, err = s.ListDueTelemetryNotificationDeliveries(ctx, now.Add(time.Hour), 100)
	if err != nil || len(due) != 0 {
		t.Fatalf("deleted deliveries=%+v %v", due, err)
	}
	fresh, _ := telemetryOutboxFixture(t, s, in.TestID)
	fresh.RunIncarnation = "replacement"
	if _, err := s.db.Exec(`UPDATE tests SET initial_provenance_json=? WHERE id=?`, `{"run_incarnation":"replacement"}`, in.TestID); err != nil {
		t.Fatal(err)
	}
	if _, state, err := s.EnqueueTelemetryNotification(ctx, in, now); err != nil || state != "fenced" {
		t.Fatalf("stale recreation enqueue=%s %v", state, err)
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *newer, "delivered", now.Add(4*time.Second)); err != nil || ok {
		t.Fatal("deleted owner affected recreation")
	}
	if newID, state, err := s.EnqueueTelemetryNotification(ctx, fresh, now); err != nil || state != "enqueued" || newID == id {
		t.Fatalf("fresh recreation=%s %s %v", newID, state, err)
	}
}

func TestTelemetryNotificationOutboxAdmissionAndBounds(t *testing.T) {
	checkTelemetryNotificationOutboxAdmissionAndBounds(t, newHAFencingStore(t))
}
func checkTelemetryNotificationOutboxAdmissionAndBounds(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	in, now := telemetryOutboxFixture(t, s, "outbox-bounds")
	for name, mutate := range map[string]func(*TelemetryNotification){
		"invalid identity UTF-8": func(v *TelemetryNotification) { v.PolicyID = string([]byte{255}) },
		"invalid payload UTF-8": func(v *TelemetryNotification) {
			v.Payload = append(append(json.RawMessage(`{"summary":"`), byte(255)), []byte(`"}`)...)
		},
		"empty identity":     func(v *TelemetryNotification) { v.RunIncarnation = "" },
		"invalid assessment": func(v *TelemetryNotification) { v.AssessmentRevision = "not-a-digest" },
		"unversioned policy": func(v *TelemetryNotification) { v.PolicyRevision = 0 },
		"invalid outcome":    func(v *TelemetryNotification) { v.Outcome = "success" },
		"large payload": func(v *TelemetryNotification) {
			v.Payload = json.RawMessage(strings.Repeat("x", MaxTelemetryNotificationBytes+1))
		},
		"escaped payload exceeds stored bound": func(v *TelemetryNotification) {
			v.Payload = json.RawMessage(`{"summary":"` + strings.Repeat("<", MaxTelemetryNotificationBytes/2) + `"}`)
		},
		"metadata exceeds stored bound": func(v *TelemetryNotification) {
			v.Payload = json.RawMessage(`{"summary":"` + strings.Repeat("x", MaxTelemetryNotificationBytes-20) + `"}`)
		},
		"nonobject payload":     func(v *TelemetryNotification) { v.Payload = json.RawMessage(`null`) },
		"invalid payload":       func(v *TelemetryNotification) { v.Payload = json.RawMessage(`{`) },
		"duplicate destination": func(v *TelemetryNotification) { v.Destinations = []string{in.Destinations[0], in.Destinations[0]} },
		"credential destination": func(v *TelemetryNotification) {
			v.Destinations = []string{"https://example.test/webhook?token=forbidden"}
		},
		"destination limit": func(v *TelemetryNotification) {
			for i := 0; i <= MaxTelemetryNotificationDestinations; i++ {
				v.Destinations = append(v.Destinations, fmt.Sprintf("%064x", i))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := in
			bad.Destinations = append([]string(nil), in.Destinations...)
			mutate(&bad)
			if _, _, err := s.EnqueueTelemetryNotification(ctx, bad, now); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*TelemetryNotification){
		"foreign project":     func(v *TelemetryNotification) { v.ProjectID = "another" },
		"foreign incarnation": func(v *TelemetryNotification) { v.RunIncarnation = "another" },
		"wrong completion":    func(v *TelemetryNotification) { v.CompletedAt = v.CompletedAt.Add(time.Second) },
		"deleted run":         func(v *TelemetryNotification) { v.TestID = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := in
			mutate(&bad)
			if _, state, err := s.EnqueueTelemetryNotification(ctx, bad, now); err != nil || state != "fenced" {
				t.Fatalf("foreign envelope=%s %v", state, err)
			}
		})
	}
	t.Run("maximum destinations", func(t *testing.T) {
		full, stamp := telemetryOutboxFixture(t, s, "outbox-destination-boundary")
		defer func() {
			if err := s.DeleteTest(full.TestID); err != nil {
				t.Error(err)
			}
		}()
		full.Destinations = nil
		for i := 0; i < MaxTelemetryNotificationDestinations; i++ {
			full.Destinations = append(full.Destinations, fmt.Sprintf("%064x", i))
		}
		id, state, err := s.EnqueueTelemetryNotification(ctx, full, stamp)
		if err != nil || state != "enqueued" {
			t.Fatalf("maximum destinations=%s %v", state, err)
		}
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_deliveries WHERE event_id=?`, id).Scan(&count); err != nil || count != MaxTelemetryNotificationDestinations {
			t.Fatalf("retained destinations=%d %v", count, err)
		}
	})
	for i := 0; i < MaxTelemetryNotificationsPerRun; i++ {
		next := in
		next.AssessmentRevision = fmt.Sprintf("%064x", i)
		if _, state, err := s.EnqueueTelemetryNotification(ctx, next, now); err != nil || state != "enqueued" {
			t.Fatalf("event %d=%s %v", i, state, err)
		}
	}
	if _, _, err := s.EnqueueTelemetryNotification(ctx, in, now); err == nil {
		t.Fatal("per-run limit bypassed")
	}
	legacy, _ := telemetryOutboxFixture(t, s, "outbox-legacy")
	if claimed, err := s.ClaimRunNotification(legacy.TestID, "telemetry_acceptance:fail", now); err != nil || !claimed {
		t.Fatal(err)
	}
	if _, state, err := s.EnqueueTelemetryNotification(ctx, legacy, now); err != nil || state != "legacy_claim" {
		t.Fatalf("legacy=%s %v", state, err)
	}
	marker, err := s.GetSetting(RunNotificationClaimPrefix(legacy.TestID) + "telemetry_acceptance:fail")
	if err != nil || marker == telemetryNotificationLegacyMarker {
		t.Fatal("legacy delivery uncertainty overwritten")
	}
	active, _ := telemetryOutboxFixture(t, s, "outbox-active")
	if _, err := s.db.Exec(`UPDATE tests SET status='RUNNING' WHERE id=?`, active.TestID); err != nil {
		t.Fatal(err)
	}
	if _, state, err := s.EnqueueTelemetryNotification(ctx, active, now); err != nil || state != "fenced" {
		t.Fatalf("active=%s %v", state, err)
	}
}

func TestTelemetryNotificationOutboxExhaustionAndRevocation(t *testing.T) {
	checkTelemetryNotificationOutboxExhaustionAndRevocation(t, newHAFencingStore(t))
}
func checkTelemetryNotificationOutboxExhaustionAndRevocation(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	in, now := telemetryOutboxFixture(t, s, "outbox-exhaustion")
	id, state, err := s.EnqueueTelemetryNotification(ctx, in, now)
	if err != nil || state != "enqueued" {
		t.Fatal(err)
	}
	candidate := TelemetryNotificationDelivery{EventID: id, Destination: in.Destinations[0]}
	for attempt := 1; attempt <= MaxTelemetryNotificationAttempts; attempt++ {
		claim, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now, time.Minute)
		if err != nil || claim == nil || claim.Attempts != attempt {
			t.Fatalf("attempt %d=%+v %v", attempt, claim, err)
		}
		if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *claim, "retry", now); err != nil || !ok {
			t.Fatal("retry not persisted", err)
		}
		candidate = *claim
		now = now.Add(2 * time.Minute)
	}
	if claim, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now, time.Minute); err != nil || claim != nil {
		t.Fatal("exhausted retried")
	}
	// Crash on every lease including the last permitted attempt also exhausts.
	candidate = TelemetryNotificationDelivery{EventID: id, Destination: in.Destinations[1]}
	for attempt := 1; attempt <= MaxTelemetryNotificationAttempts; attempt++ {
		claim, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now, time.Second)
		if err != nil || claim == nil {
			t.Fatal("crash claim", err)
		}
		candidate = *claim
		now = now.Add(time.Minute)
	}
	if claim, err := s.ClaimTelemetryNotificationDelivery(ctx, candidate, now, time.Minute); err != nil || claim != nil {
		t.Fatal("crash exhausted retried")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_deliveries WHERE event_id=? AND state='exhausted'`, id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("exhaustion not durable=%d %v", count, err)
	}
	in.AssessmentRevision = strings.Repeat("e", 64)
	in.Destinations = in.Destinations[:1]
	id, state, err = s.EnqueueTelemetryNotification(ctx, in, now)
	if err != nil || state != "enqueued" {
		t.Fatal(err)
	}
	claim, err := s.ClaimTelemetryNotificationDelivery(ctx, TelemetryNotificationDelivery{EventID: id, Destination: in.Destinations[0]}, now, time.Minute)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *claim, "revoked", now); err != nil || !ok {
		t.Fatal("revocation not durable", err)
	}
	due, err := s.ListDueTelemetryNotificationDeliveries(ctx, now.Add(time.Hour), 100)
	if err != nil || len(due) != 0 {
		t.Fatal("terminal delivery listed", err)
	}
}

func TestTelemetryNotificationOutboxIndependentStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	checkTelemetryNotificationOutboxConcurrent(t, first, second)
}
func checkTelemetryNotificationOutboxConcurrent(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	in, now := telemetryOutboxFixture(t, first, "outbox-concurrent")
	var enqueued atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 != 0 {
				s = second
			}
			_, state, err := s.EnqueueTelemetryNotification(ctx, in, now)
			if err != nil {
				t.Error(err)
			} else if state == "enqueued" {
				enqueued.Add(1)
			} else if state != "replayed" {
				t.Errorf("concurrent enqueue=%s", state)
			}
		}(i)
	}
	wg.Wait()
	if enqueued.Load() != 1 {
		t.Fatalf("enqueued %d times", enqueued.Load())
	}
	due, err := first.ListDueTelemetryNotificationDeliveries(ctx, now, 100)
	if err != nil || len(due) != 2 {
		t.Fatalf("due=%+v %v", due, err)
	}
	var claims atomic.Int32
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 != 0 {
				s = second
			}
			claim, err := s.ClaimTelemetryNotificationDelivery(ctx, due[0], now, time.Minute)
			if err != nil {
				t.Error(err)
			} else if claim != nil {
				claims.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("claimed %d times", claims.Load())
	}
}

func TestTelemetryNotificationOutboxObsoleteIncarnationCannotStarveQueue(t *testing.T) {
	s := newHAFencingStore(t)
	ctx := context.Background()
	in, now := telemetryOutboxFixture(t, s, "outbox-obsolete")
	id, state, err := s.EnqueueTelemetryNotification(ctx, in, now)
	if err != nil || state != "enqueued" {
		t.Fatal(err)
	}
	due, err := s.ListDueTelemetryNotificationDeliveries(ctx, now, 100)
	if err != nil || len(due) != 2 {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET initial_provenance_json=? WHERE id=?`, `{"run_incarnation":"changed"}`, in.TestID); err != nil {
		t.Fatal(err)
	}
	if claim, err := s.ClaimTelemetryNotificationDelivery(ctx, due[0], now, time.Minute); err != nil || claim != nil {
		t.Fatal("obsolete event leased", err)
	}
	if remaining, err := s.ListDueTelemetryNotificationDeliveries(ctx, now, 100); err != nil || len(remaining) != 0 {
		t.Fatal("obsolete deliveries starve scan", err)
	}
	if stored, err := s.GetTelemetryNotification(ctx, id); err != nil || stored.RunIncarnation != in.RunIncarnation {
		t.Fatal("obsolete evidence erased", err)
	}
}

func TestTelemetryNotificationOutboxPreservesFutureVersions(t *testing.T) {
	s := newHAFencingStore(t)
	ctx := context.Background()
	in, now := telemetryOutboxFixture(t, s, "outbox-future")
	id, state, err := s.EnqueueTelemetryNotification(ctx, in, now)
	if err != nil || state != "enqueued" {
		t.Fatal(err)
	}
	due, err := s.ListDueTelemetryNotificationDeliveries(ctx, now, 100)
	if err != nil || len(due) != 2 {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE telemetry_notification_events SET format_version=2 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTelemetryNotification(ctx, id); err == nil {
		t.Fatal("future envelope interpreted")
	}
	if _, _, err := s.EnqueueTelemetryNotification(ctx, in, now); err == nil {
		t.Fatal("future envelope replaced")
	}
	if claim, err := s.ClaimTelemetryNotificationDelivery(ctx, due[0], now, time.Minute); err == nil || claim != nil {
		t.Fatal("future delivery interpreted")
	}
	if remaining, err := s.ListDueTelemetryNotificationDeliveries(ctx, now, 100); err != nil || len(remaining) != 0 {
		t.Fatal("future delivery admitted", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_deliveries WHERE event_id=? AND state='pending' AND attempts=0`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal("future deliveries not preserved", err)
	}
}

func TestTelemetryNotificationOutboxUpgradeReopenAndProjectDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	in, now := telemetryOutboxFixture(t, s, "outbox-upgrade")
	// Model an existing database predating only these additive tables. Existing
	// tests, provenance, application hierarchy and settings must survive upgrade.
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>=68`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"telemetry_notification_deliveries", "telemetry_notification_events"} {
		if _, err := s.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 {
		t.Fatal("notification schema release was not recorded", status, err)
	}
	var prior string
	if err := s.db.QueryRow(`SELECT fingerprint FROM schema_migrations WHERE version=67`).Scan(&prior); err != nil || prior != "6e53190738516893c37874c83619953523d9cd18d05aba2bbf5a36aac3be3d32" {
		t.Fatal("prior release identity changed", prior, err)
	}
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.EnqueueTelemetryNotification(canceled, in, now); err == nil {
		t.Fatal("canceled enqueue committed")
	}
	id, state, err := s.EnqueueTelemetryNotification(ctx, in, now)
	if err != nil || state != "enqueued" {
		t.Fatalf("upgrade enqueue=%s %v", state, err)
	}
	due, err := s.ListDueTelemetryNotificationDeliveries(ctx, now, 100)
	if err != nil || len(due) != 2 {
		t.Fatalf("due=%+v %v", due, err)
	}
	claim, err := s.ClaimTelemetryNotificationDelivery(ctx, due[0], now, time.Second)
	if err != nil || claim == nil {
		t.Fatal("lease before restart", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ClaimTelemetryNotificationDelivery(ctx, *claim, now.Add(time.Second), time.Minute)
	if err != nil || recovered == nil || recovered.Attempts != 2 || recovered.OwnerToken == claim.OwnerToken {
		t.Fatalf("restart claim=%+v %v", recovered, err)
	}
	if ok, err := s.FinishTelemetryNotificationDelivery(ctx, *claim, "delivered", now.Add(time.Second)); err != nil || ok {
		t.Fatal("pre-restart owner completed", err)
	}
	if _, err := s.db.Exec(`UPDATE telemetry_notification_events SET format_version=2 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if remaining, err := s.ListDueTelemetryNotificationDeliveries(ctx, now.Add(time.Hour), 100); err != nil || len(remaining) != 0 {
		t.Fatal("reopen admitted future envelope", err)
	}
	var version, count int
	if err := s.db.QueryRow(`SELECT format_version FROM telemetry_notification_events WHERE id=?`, id).Scan(&version); err != nil || version != 2 {
		t.Fatal("reopen mutated future format", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_deliveries WHERE event_id=?`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal("reopen lost future deliveries", err)
	}

	project := &Project{ID: "outbox-project-delete", Name: "Outbox deletion", CreatedAt: now}
	if err := s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	owned, stamp := telemetryOutboxFixture(t, s, "outbox-project-owned")
	if _, err := s.db.Exec(`UPDATE tests SET project_id=? WHERE id=?`, project.ID, owned.TestID); err != nil {
		t.Fatal(err)
	}
	owned.ProjectID = project.ID
	ownedID, state, err := s.EnqueueTelemetryNotification(ctx, owned, stamp)
	if err != nil || state != "enqueued" {
		t.Fatalf("project enqueue=%s %v", state, err)
	}
	if err := s.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTelemetryNotification(ctx, ownedID); err != sql.ErrNoRows {
		t.Fatal("project event did not cascade", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_deliveries WHERE event_id=?`, ownedID).Scan(&count); err != nil || count != 0 {
		t.Fatal("project deliveries did not cascade", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_events WHERE id=?`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("other project evidence removed", err)
	}
}

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func notificationPublicationFixture(t *testing.T, s *SQLiteStore, id string) (TelemetryNotification, *ArchiveFinalizationJob, ArchiveFinalizationRunIdentity) {
	t.Helper()
	event, now := telemetryOutboxFixture(t, s, id)
	job := &ArchiveFinalizationJob{TestID: id, ProjectID: DefaultProjectID, ArchiveRecord: RunArchiveRecord{TestID: id, CreatedAt: now, CompletedAt: now, ArchivePath: "/archive/" + id, ManifestPath: "/archive/" + id + "/manifest.json", ManifestSHA256: strings.Repeat("d", 64)}}
	if err := s.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	identity := ArchiveFinalizationRunIdentity{RunIncarnation: event.RunIncarnation, ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), EndTime: now, Status: "FAILED"}
	return event, job, identity
}

func TestTelemetryNotificationAtomicPublication(t *testing.T) {
	checkTelemetryNotificationAtomicPublication(t, newHAFencingStore(t))
}
func checkTelemetryNotificationAtomicPublication(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	for _, mode := range []string{"success", "no recipients", "legacy claim", "projection rejected", "identity unavailable", "not applicable", "immutable conflict", "capacity", "stale intent", "deleted", "foreign incarnation"} {
		t.Run(mode, func(t *testing.T) {
			id := fmt.Sprintf("notification-publication-%d", time.Now().UnixNano())
			event, job, identity := notificationPublicationFixture(t, s, id)
			defer func() {
				if err := s.DeleteTest(id); err != nil {
					t.Error(err)
				}
			}()
			publication := TelemetryNotificationPublication{Event: &event}
			wantDecision := "enqueued"
			wantEvents := 1
			wantSuppressed := int64(0)
			wantCommitted := true
			wantError := false
			switch mode {
			case "no recipients":
				event.Destinations = nil
				wantDecision = "no_recipients"
			case "legacy claim":
				if ok, err := s.ClaimRunNotification(id, "telemetry_acceptance:fail", time.Now()); err != nil || !ok {
					t.Fatal("legacy claim", ok, err)
				}
				wantDecision = "legacy_claim"
				wantEvents = 0
			case "projection rejected", "identity unavailable", "not applicable":
				wantDecision = strings.ReplaceAll(mode, " ", "_")
				publication = TelemetryNotificationPublication{Decision: wantDecision}
				wantEvents = 0
				if mode != "not applicable" {
					wantSuppressed = 1
				}
			case "immutable conflict":
				if _, _, err := s.EnqueueTelemetryNotification(ctx, event, time.Now()); err != nil {
					t.Fatal(err)
				}
				event.Payload = json.RawMessage(`{"summary":"changed after original intent"}`)
				wantCommitted = false
				wantError = true
			case "capacity":
				for i := 0; i < MaxTelemetryNotificationsPerRun; i++ {
					prior := event
					prior.AssessmentRevision = fmt.Sprintf("%064x", i)
					if _, _, err := s.EnqueueTelemetryNotification(ctx, prior, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				wantEvents = MaxTelemetryNotificationsPerRun
				wantDecision = "capacity_exhausted"
				wantSuppressed = 1
			case "stale intent":
				replacement := *job
				if err := s.SaveArchiveFinalizationJob(&replacement); err != nil {
					t.Fatal(err)
				}
				wantCommitted = false
				wantEvents = 0
			case "deleted":
				if err := s.DeleteTest(id); err != nil {
					t.Fatal(err)
				}
				wantCommitted = false
				wantEvents = 0
			case "foreign incarnation":
				event.RunIncarnation = "foreign"
				wantCommitted = false
				wantError = true
				wantEvents = 0
			}
			committed, err := s.CompleteArchiveFinalizationWithTelemetry(id, job.IntentToken, "", &job.ArchiveRecord, identity, publication)
			if committed != wantCommitted || (err != nil) != wantError {
				t.Fatalf("commit=%v err=%v", committed, err)
			}
			var events int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_events WHERE test_id=?`, id).Scan(&events); err != nil || events != wantEvents {
				t.Fatalf("events=%d err=%v", events, err)
			}
			catalog, catalogErr := s.GetRunArchive(id)
			state, stateErr := s.GetTelemetryNotificationPublication(ctx, DefaultProjectID, id)
			if !wantCommitted {
				if !errors.Is(catalogErr, sql.ErrNoRows) || catalog != nil || !errors.Is(stateErr, sql.ErrNoRows) {
					t.Fatal("uncommitted catalog or decision", catalog, catalogErr, state, stateErr)
				}
				if mode != "deleted" {
					if _, err := s.GetArchiveFinalizationJob(id); err != nil {
						t.Fatal("uncommitted job lost", err)
					}
				}
				return
			}
			if catalogErr != nil || stateErr != nil || state.Decision != wantDecision || state.SuppressedPublications != wantSuppressed || state.ManifestSHA256 != job.ArchiveRecord.ManifestSHA256 {
				t.Fatal("incomplete atomic publication", catalog, catalogErr, state, stateErr)
			}
			if mode == "success" && state.Deliveries["pending"] != 2 {
				t.Fatal("destinations missing", state)
			}
			if _, err := s.GetArchiveFinalizationJob(id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("publication job not acknowledged", err)
			}
			if _, err := s.GetTelemetryNotificationPublication(ctx, "foreign", id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("foreign project read", err)
			}
			if ok, err := s.CompleteArchiveFinalizationWithTelemetry(id, job.IntentToken, "", &job.ArchiveRecord, identity, publication); err != nil || ok {
				t.Fatal("retired intent repeated", ok, err)
			}
			again, err := s.GetTelemetryNotificationPublication(ctx, DefaultProjectID, id)
			if err != nil || again.SuppressedPublications != wantSuppressed {
				t.Fatal("retry duplicated suppression", again, err)
			}
			if err := s.DeleteTest(id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetTelemetryNotificationPublication(ctx, DefaultProjectID, id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("deleted decision retained", err)
			}
		})
	}
}

func TestTelemetryNotificationAtomicPublicationRejectsInvalidDecision(t *testing.T) {
	s := newHAFencingStore(t)
	for _, mode := range []string{"missing", "arbitrary error", "event and decision", "foreign project", "wrong end", "invalid digest"} {
		t.Run(mode, func(t *testing.T) {
			event, job, identity := notificationPublicationFixture(t, s, "invalid-"+mode)
			publication := TelemetryNotificationPublication{Event: &event}
			switch mode {
			case "missing":
				publication = TelemetryNotificationPublication{}
			case "arbitrary error":
				publication = TelemetryNotificationPublication{Decision: "https://secret.example/token"}
			case "event and decision":
				publication.Decision = "not_applicable"
			case "foreign project":
				event.ProjectID = "foreign"
			case "wrong end":
				event.CompletedAt = event.CompletedAt.Add(time.Second)
			case "invalid digest":
				job.ArchiveRecord.ManifestSHA256 = "bad"
			}
			if ok, err := s.CompleteArchiveFinalizationWithTelemetry(event.TestID, job.IntentToken, "", &job.ArchiveRecord, identity, publication); err == nil || ok {
				t.Fatal("invalid decision committed", ok, err)
			}
		})
	}
}

package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestElasticInspectionCleanupBlocksReexecutionUntilExactAcknowledgement(t *testing.T) {
	s, source, _ := elasticMonitorFixture(t)
	assertElasticInspectionCleanupBlocksReexecutionUntilExactAcknowledgement(t, s, source)
}

func TestElasticInspectionOwnerCancellationCannotReplaceNewClaim(t *testing.T) {
	s, source, _ := elasticMonitorFixture(t)
	assertElasticInspectionOwnerCancellationCannotReplaceNewClaim(t, s, source)
}

func assertElasticInspectionOwnerCancellationCannotReplaceNewClaim(t *testing.T, s *SQLiteStore, source MonitorSource) {
	t.Helper()
	now := time.Now().UTC()
	created := now.Add(-3 * time.Minute)
	op := &ElasticInspection{ID: "owner-cancel-fence", ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: 1, Kind: "verify", PayloadJSON: `{}`, CreatedAt: created, ExpiresAt: now.Add(10 * time.Minute)}
	if err := s.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "old-owner", created, created.Add(time.Minute)); err != nil || !ok {
		t.Fatal("original claim", ok, err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "new-owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal("replacement claim", ok, err)
	}
	if ok, err := s.CancelElasticInspectionIfOwner(op.ID, op.ProjectID, op.ConnectionID, "old-owner", now); err != nil || ok {
		t.Fatal("former owner cancelled replacement", ok, err)
	}
	current, err := s.GetElasticInspection(op.ID)
	if err != nil || current.State != "running" || current.OwnerToken != "new-owner" {
		t.Fatal("replacement claim changed", current, err)
	}
	if ok, err := s.CancelElasticInspectionIfOwner(op.ID, op.ProjectID, op.ConnectionID, "new-owner", now); err != nil || !ok {
		t.Fatal("current owner could not cancel", ok, err)
	}
	if ok, err := s.CancelElasticInspectionIfOwner(op.ID, op.ProjectID, op.ConnectionID, "new-owner", now); err != nil || ok {
		t.Fatal("duplicate cancellation changed state", ok, err)
	}
}

func assertElasticInspectionCleanupBlocksReexecutionUntilExactAcknowledgement(t *testing.T, s *SQLiteStore, source MonitorSource) {
	t.Helper()
	now := time.Now().UTC()
	created := now.Add(-2 * time.Minute)
	op := &ElasticInspection{ID: "recovered-inspection", ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: 1, Kind: "preview", PayloadJSON: `{}`, CreatedAt: created, ExpiresAt: created.Add(15 * time.Minute)}
	if err := s.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "original-owner", created, created.Add(time.Minute)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "recoverable-inspection-context", WindowFrom: created, WindowUntil: created.Add(time.Minute), KeepAliveUntil: now.Add(time.Hour)}
	if ok, err := s.RetainElasticInspectionCleanup(op.ID, "original-owner", []*analysis.ProviderContinuation{cursor}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if pending, err := s.ListElasticInspections(op.ProjectID, "", 10); err != nil || len(pending) != 0 {
		t.Fatalf("expired claim with owned cleanup was assigned for another read: %+v %v", pending, err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "new-owner", now, now.Add(time.Minute)); err != nil || ok {
		t.Fatal("claim race replaced cleanup owner", ok, err)
	}
	retained, err := s.GetElasticInspection(op.ID)
	if err != nil || retained.OwnerToken != "original-owner" || len(retained.CleanupContinuations) != 1 {
		t.Fatal("blocked claim lost original ownership", retained, err)
	}
	wrong := *cursor
	wrong.PITID = "other"
	if ok, err := s.ClearElasticInspectionCleanup(op.ID, []*analysis.ProviderContinuation{&wrong}); err != nil || ok {
		t.Fatal("different acknowledgement released claim backpressure", ok, err)
	}
	if ok, err := s.ClearElasticInspectionCleanup(op.ID, []*analysis.ProviderContinuation{cursor}); err != nil || !ok {
		t.Fatal("exact cleanup acknowledgement failed", ok, err)
	}
	if pending, err := s.ListElasticInspections(op.ProjectID, "", 10); err != nil || len(pending) != 1 {
		t.Fatal("cleaned operation was not recoverable", pending, err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "new-owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal("cleaned operation could not acquire a new claim", ok, err)
	}
	if ok, err := s.RetainElasticInspectionCleanup(op.ID, "original-owner", []*analysis.ProviderContinuation{cursor}, now); err != nil || ok {
		t.Fatal("former owner changed new execution ownership", ok, err)
	}
}

func TestElasticInspectionCleanupOutlivesResultHorizonAndRemainsPrivate(t *testing.T) {
	for _, state := range []string{"complete", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			s, source, _ := elasticMonitorFixture(t)
			connection, err := s.GetELKConnection(source.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(struct {
				Connection *ExpandedElasticConnectionSnapshot `json:"connection"`
			}{connection.Snapshot()})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			created := now.Add(-16 * time.Minute)
			op := &ElasticInspection{ID: "inspection", ProjectID: source.ProjectID, ConnectionID: connection.ID, ConnectionRevision: connection.Revision, Kind: "preview", PayloadJSON: string(payload), CreatedAt: created, ExpiresAt: created.Add(15 * time.Minute)}
			if err = s.CreateElasticInspection(op); err != nil {
				t.Fatal(err)
			}
			claim := created.Add(time.Second)
			if ok, err := s.ClaimElasticInspection(op.ID, "owner", claim, claim.Add(time.Minute)); err != nil || !ok {
				t.Fatal(ok, err)
			}
			cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "private-inspection-handle", WindowFrom: now.Add(-time.Minute), WindowUntil: now, KeepAliveUntil: now.Add(time.Minute)}
			if state == "cancelled" {
				if ok, err := s.CancelElasticInspection(op.ID, op.ProjectID, op.ConnectionID, claim.Add(time.Second)); err != nil || !ok {
					t.Fatal(ok, err)
				}
			} else {
				if ok, err := s.CompleteElasticInspection(op.ID, "owner", `{"safe":true}`, "", claim.Add(time.Second)); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			if ok, err := s.RetainElasticInspectionCleanup(op.ID, "owner", []*analysis.ProviderContinuation{cursor}, now); err != nil || !ok {
				t.Fatal("terminal executor could not preserve cleanup", ok, err)
			}
			if ok, err := s.RetainElasticInspectionCleanup(op.ID, "other-owner", []*analysis.ProviderContinuation{cursor}, now); err != nil || ok {
				t.Fatal("stale owner retained cleanup", ok, err)
			}
			cleanup, err := s.ListElasticInspectionCleanup(op.ProjectID, "", 100)
			if err != nil || len(cleanup) != 1 || cleanup[0].CleanupContinuations[0].PITID != cursor.PITID || cleanup[0].State != state {
				t.Fatalf("cleanup lost after result expiry: %+v %v", cleanup, err)
			}
			public, err := json.Marshal(cleanup[0])
			if err != nil || strings.Contains(string(public), cursor.PITID) || strings.Contains(string(public), "API_KEY") {
				t.Fatalf("private cleanup leaked: %s %v", public, err)
			}
			if err = s.DeleteMonitorSource(source.ProjectID, source.ID, source.Revision); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.DeleteELKConnection(connection.ID, connection.ProjectID); err == nil || ok {
				t.Fatal("deleted connection before owned inspection cleanup", ok, err)
			}
			consumers, err := s.ListElasticSecretConsumers(op.ProjectID, "API_KEY")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range consumers {
				found = found || c.ResourceType == "elastic_inspection" && c.ResourceID == op.ID
			}
			if !found {
				t.Fatal("owned inspection no longer retains immutable credentials")
			}
			wrong := *cursor
			wrong.PITID = "replacement"
			if ok, err := s.ClearElasticInspectionCleanup(op.ID, []*analysis.ProviderContinuation{&wrong}); err != nil || ok {
				t.Fatal("different cleanup assignment cleared ownership", ok, err)
			}
			if ok, err := s.ClearElasticInspectionCleanup(op.ID, []*analysis.ProviderContinuation{cursor}); err != nil || !ok {
				t.Fatal("matching cleanup acknowledgement failed", ok, err)
			}
			if cleanup, err = s.ListElasticInspectionCleanup(op.ProjectID, "", 100); err != nil || len(cleanup) != 0 {
				t.Fatal(cleanup, err)
			}
			stored, err := s.GetElasticInspection(op.ID)
			if err != nil || stored.State != state {
				t.Fatal("cleanup changed public operation status", stored, err)
			}
			if ok, err := s.DeleteELKConnection(connection.ID, connection.ProjectID); err != nil || !ok {
				t.Fatal("cleaned inspection still blocks connection deletion", ok, err)
			}
		})
	}
}

func TestElasticInspectionCleanupDropsOnlyKnownExpiry(t *testing.T) {
	s, source, _ := elasticMonitorFixture(t)
	now := time.Now().UTC()
	op := &ElasticInspection{ID: "inspection", ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: 1, Kind: "verify", PayloadJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := s.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	expired := &analysis.ProviderContinuation{QueryID: "q", PITID: "expired", WindowFrom: now.Add(-time.Minute), WindowUntil: now, KeepAliveUntil: now.Add(-time.Second)}
	unknown := *expired
	unknown.PITID = "unknown"
	unknown.KeepAliveUntil = time.Time{}
	if ok, err := s.RetainElasticInspectionCleanup(op.ID, "owner", []*analysis.ProviderContinuation{expired, &unknown}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cleanup, err := s.ListElasticInspectionCleanup(op.ProjectID, "", 100)
	if err != nil || len(cleanup) != 1 || len(cleanup[0].CleanupContinuations) != 1 || cleanup[0].CleanupContinuations[0].PITID != "unknown" {
		t.Fatalf("unknown expiry silently discarded: %+v %v", cleanup, err)
	}
	if all, err := s.ListElasticInspectionCleanup(op.ProjectID, "other-collector", 100); err != nil || len(all) != 0 {
		t.Fatal("cleanup collector isolation", all, err)
	}
}

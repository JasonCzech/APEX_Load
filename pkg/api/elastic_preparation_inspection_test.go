package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPreparedElasticDirectInspectionRetainsCleanupIndependently(t *testing.T) {
	for _, name := range []string{"verified", "failed", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			server, _, job, _, _ := apiElasticMonitorFixture(t)
			store := server.store.(storage.ElasticInspectionStore)
			op, owner, err := beginPreparedElasticInspection(context.Background(), store, job.Source, job.ElasticConnectionSnapshot)
			if err != nil || owner == "" || op.CollectorID != "" {
				t.Fatal("preparation not privately owned", op, owner, err)
			}
			var immutable elasticInspectionWork
			if json.Unmarshal([]byte(op.PayloadJSON), &immutable) != nil || immutable.Connection.Revision != job.ElasticConnectionSnapshot.Revision || immutable.Connection.APIKeySecretName != "protected-key-reference" || immutable.Request.Selection == nil {
				t.Fatal("immutable configuration lost")
			}
			owned := &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: "private-preparation-handle", WindowFrom: op.CreatedAt, WindowUntil: op.CreatedAt.Add(time.Nanosecond), KeepAliveUntil: op.CreatedAt.Add(2 * time.Minute)}
			result := collectoragent.ElasticInspectionData{Capabilities: elastic.Capabilities{Product: "elasticsearch", DSL: true, PIT: true}, CleanupContinuations: []*analysis.ProviderContinuation{owned}}
			var inspectionErr error
			if name == "failed" {
				inspectionErr = errors.New("vendor secret body")
			}
			if name == "cancelled" {
				if ok, err := store.CancelElasticInspection(op.ID, op.ProjectID, op.ConnectionID, time.Now().UTC()); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			err = completePreparedElasticInspection(store, op, owner, result, inspectionErr, time.Now().UTC())
			if (err != nil) != (name == "cancelled") {
				t.Fatal("cleanup changed independent verification", err)
			}
			current, err := store.GetElasticInspection(op.ID)
			wantState := "complete"
			if name == "failed" {
				wantState = "error"
			} else if name == "cancelled" {
				wantState = "cancelled"
			}
			if err != nil || len(current.CleanupContinuations) != 1 || current.CleanupContinuations[0].PITID != owned.PITID || current.State != wantState {
				t.Fatal("probe cleanup lost or changed result", current, err)
			}
			visible, _ := json.Marshal(elasticInspectionResponse(current))
			for _, private := range []string{owned.PITID, owner, "protected-key-reference", "vendor secret body", "cleanup_continuations"} {
				if strings.Contains(string(visible), private) {
					t.Fatal("private configuration disclosed", private)
				}
			}
			pending, err := store.ListElasticInspectionCleanup(job.ProjectID, "", 2)
			if err != nil || len(pending) != 1 || pending[0].ID != op.ID {
				t.Fatal("cleanup not recoverable", pending, err)
			}
		})
	}
}

func TestPreparedElasticInspectionHonorsExecutionAndOwnerFences(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t)
	store := server.store.(storage.ElasticInspectionStore)
	collector := job.Source
	collector.ExecutorMode = "collector"
	collector.CollectorID = "assigned-collector"
	op, owner, err := beginPreparedElasticInspection(context.Background(), store, collector, job.ElasticConnectionSnapshot)
	if err != nil || owner != "" || op.CollectorID != collector.CollectorID {
		t.Fatal("collector was locally claimed", op, owner, err)
	}
	current, _ := store.GetElasticInspection(op.ID)
	if current.State != "pending" || current.OwnerToken != "" {
		t.Fatal("collector assignment unavailable")
	}
	op, owner, err = beginPreparedElasticInspection(context.Background(), store, job.Source, job.ElasticConnectionSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	owned := &analysis.ProviderContinuation{QueryID: "inspection-async", AsyncID: "owned-preparation-async", WindowFrom: op.CreatedAt, WindowUntil: op.CreatedAt.Add(time.Nanosecond), KeepAliveUntil: op.CreatedAt.Add(time.Minute)}
	if err = completePreparedElasticInspection(store, op, "replaced-owner", collectoragent.ElasticInspectionData{CleanupContinuations: []*analysis.ProviderContinuation{owned}}, nil, time.Now().UTC()); err == nil {
		t.Fatal("wrong owner retained cleanup")
	}
	current, _ = store.GetElasticInspection(op.ID)
	if len(current.CleanupContinuations) != 0 || current.OwnerToken != owner || current.State != "running" {
		t.Fatal("wrong owner altered operation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = beginPreparedElasticInspection(ctx, store, job.Source, job.ElasticConnectionSnapshot); err == nil {
		t.Fatal("cancelled preparation queued work")
	}
}

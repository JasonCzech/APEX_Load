package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRCABrowserSelectionAdmissionFreezesAndExportsRefusal(t *testing.T) {
	server, store := rcaTestServer(t)
	policy := e1Policy(e1LCP)
	policy.Baseline = BrowserPolicyBaseline{Mode: "latest_compatible"}
	pinRCAAdmissionPolicy(t, server, saveBrowserPolicy(t, store, "web", 1, policy))
	reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if reply.Code != http.StatusAccepted {
		t.Fatalf("queue: %d %s", reply.Code, reply.Body)
	}
	queued := decodeRCAResponse(t, reply.Body.Bytes())
	row, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	payload := rcaAnalysisPayloadOf(row)
	if payload.BrowserSelection == nil || payload.BrowserSelection.Status != "current_identity_unavailable" || payload.Inputs.BrowserSelection == "" || validateRCABrowserSelection(payload) != nil {
		t.Fatalf("receipt: %+v", payload)
	}
	policy.Baseline = BrowserPolicyBaseline{Mode: "none"}
	saveBrowserPolicy(t, store, "web", 2, policy)
	repeat := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if repeat.Code != http.StatusOK || decodeRCAResponse(t, repeat.Body.Bytes()).AnalysisID != queued.AnalysisID {
		t.Fatalf("dedup: %d %s", repeat.Code, repeat.Body)
	}
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); !ran || err != nil {
		t.Fatal(ran, err)
	}
	done, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	if err != nil || done.State != storage.RCAStateCompleted {
		t.Fatalf("execution: %+v %v", done, err)
	}
	if a := rcaAnalysisPayloadOf(done).Assessment; a == nil || a.BrowserSelection == nil || a.BrowserSelection.Status != payload.BrowserSelection.Status {
		t.Fatalf("lost receipt: %+v", a)
	}
	bundle := timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID+"/bundle", nil)
	var exported RCABundle
	if bundle.Code != http.StatusOK || json.Unmarshal(bundle.Body.Bytes(), &exported) != nil || !reflect.DeepEqual(exported.Assessment.BrowserSelection, payload.BrowserSelection) {
		t.Fatalf("bundle: %d %s", bundle.Code, bundle.Body)
	}
	// Explicit callers retain authority and do not receive an automatic-selection receipt.
	explicit := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": "rca-baseline"})
	if explicit.Code != http.StatusAccepted {
		t.Fatalf("explicit: %d %s", explicit.Code, explicit.Body)
	}
	explicitRow, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", decodeRCAResponse(t, explicit.Body.Bytes()).AnalysisID)
	if err != nil || rcaAnalysisPayloadOf(explicitRow).BrowserSelection != nil {
		t.Fatalf("explicit receipt: %+v %v", explicitRow, err)
	}
}

func TestRCABrowserSelectionFrozenReceiptValidation(t *testing.T) {
	if err := validateRCABrowserSelection(rcaAnalysisPayload{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*rcaAnalysisPayload)
	}{
		{"unchanged", func(*rcaAnalysisPayload) {}},
		{"receipt changed", func(p *rcaAnalysisPayload) { p.BrowserSelection.Policy = "other" }},
		{"baseline changed", func(p *rcaAnalysisPayload) { p.Request.BaselineTestID = "other" }},
		{"digest changed", func(p *rcaAnalysisPayload) { p.Inputs.Baseline = "other" }},
		{"receipt removed", func(p *rcaAnalysisPayload) { p.BrowserSelection = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := rcaAnalysisPayload{BrowserSelection: &rca.BrowserBaselineSelection{Version: rca.BrowserSelectionVersion, Policy: "p@1", Status: "selected", SelectedTestID: "base", SelectedDigest: "digest"}}
			p.Request.BaselineTestID = "base"
			p.Inputs.Baseline = "digest"
			raw, _ := json.Marshal(p.BrowserSelection)
			p.Inputs.BrowserSelection = sha256BytesHex(raw)
			tc.mutate(&p)
			if err := validateRCABrowserSelection(p); (err == nil) != (tc.name == "unchanged") {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestRCABrowserSelectionArchiveIdentity(t *testing.T) {
	start := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	for _, tc := range []struct {
		name   string
		mutate func(*storage.TestRecord, *storage.RunArchiveRecord, *RunArchiveManifest)
	}{
		{"valid", func(*storage.TestRecord, *storage.RunArchiveRecord, *RunArchiveManifest) {}},
		{"replaced incarnation", func(r *storage.TestRecord, _ *storage.RunArchiveRecord, _ *RunArchiveManifest) {
			r.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
		}},
		{"different project", func(r *storage.TestRecord, _ *storage.RunArchiveRecord, _ *RunArchiveManifest) { r.ProjectID = "other" }},
		{"different application", func(r *storage.TestRecord, _ *storage.RunArchiveRecord, _ *RunArchiveManifest) {
			r.ApplicationID = "other"
		}},
		{"different scenario", func(r *storage.TestRecord, _ *storage.RunArchiveRecord, _ *RunArchiveManifest) {
			r.ScenarioID = "other"
		}},
		{"different initial scope", func(_ *storage.TestRecord, _ *storage.RunArchiveRecord, m *RunArchiveManifest) {
			p := m.Provenance
			p.ApplicationID = "other"
			m.InitialProvenance = &p
		}},
		{"different generation", func(r *storage.TestRecord, _ *storage.RunArchiveRecord, _ *RunArchiveManifest) {
			r.ExecutionGeneration++
		}},
		{"different completion", func(_ *storage.TestRecord, _ *storage.RunArchiveRecord, m *RunArchiveManifest) {
			m.CompletedAt = m.CompletedAt.Add(time.Nanosecond)
		}},
		{"different requested time", func(_ *storage.TestRecord, _ *storage.RunArchiveRecord, m *RunArchiveManifest) {
			m.CreatedAt = m.CreatedAt.Add(time.Nanosecond)
		}},
		{"different archive ID", func(_ *storage.TestRecord, r *storage.RunArchiveRecord, _ *RunArchiveManifest) { r.TestID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := &storage.TestRecord{ID: "run", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: start, EndTime: &end, InitialProvenance: map[string]any{"run_incarnation": "incarnation"}, ExecutionGeneration: 2}
			record := &storage.RunArchiveRecord{TestID: "run", CompletedAt: end}
			manifest := &RunArchiveManifest{TestID: "run", Status: "COMPLETE", CreatedAt: start, CompletedAt: end, Provenance: RunArchiveProvenance{ProjectID: storage.DefaultProjectID, RunIncarnation: "incarnation", ExecutionGeneration: 2}}
			tc.mutate(row, record, manifest)
			if got := browserSelectionArchiveIdentity(row, record, manifest, nil); got != (tc.name == "valid") {
				t.Fatalf("identity accepted=%v", got)
			}
		})
	}
}

// Fault injection isolates the publication-state decision from SQL behavior;
// storage's saturated-pool test separately exercises actual cancellable reads.
type browserSelectionPublicationProbe struct {
	final                *storage.ArchiveFinalizationJob
	refresh              *storage.ArchiveRefreshJob
	finalErr, refreshErr error
}

func (p browserSelectionPublicationProbe) GetArchiveFinalizationJobContext(context.Context, string) (*storage.ArchiveFinalizationJob, error) {
	return p.final, p.finalErr
}
func (p browserSelectionPublicationProbe) GetArchiveRefreshJobContext(context.Context, string) (*storage.ArchiveRefreshJob, error) {
	return p.refresh, p.refreshErr
}
func TestRCABrowserSelectionPublicationFailsClosed(t *testing.T) {
	unavailable := errors.New("database unavailable")
	for _, tc := range []struct {
		name  string
		store any
		want  error
	}{
		{"stable", browserSelectionPublicationProbe{finalErr: sql.ErrNoRows, refreshErr: sql.ErrNoRows}, nil},
		{"finalizing", browserSelectionPublicationProbe{final: &storage.ArchiveFinalizationJob{}}, errRCAFinalizing},
		{"failed retryable finalization", browserSelectionPublicationProbe{final: &storage.ArchiveFinalizationJob{LastError: "retry pending"}}, errRCAFinalizing},
		{"refreshing", browserSelectionPublicationProbe{finalErr: sql.ErrNoRows, refresh: &storage.ArchiveRefreshJob{}}, errRCAFinalizing},
		{"finalization read error", browserSelectionPublicationProbe{finalErr: unavailable}, unavailable},
		{"refresh read error", browserSelectionPublicationProbe{finalErr: sql.ErrNoRows, refreshErr: unavailable}, unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := browserSelectionArchivePublication(context.Background(), tc.store, "run"); !errors.Is(err, tc.want) {
				t.Fatalf("publication state: %v", err)
			}
		})
	}
	if err := browserSelectionArchivePublication(context.Background(), struct{}{}, "run"); err == nil {
		t.Fatal("missing publication reader accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := browserSelectionArchivePublication(ctx, browserSelectionPublicationProbe{}, "run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

type browserSelectionCurrentPublicationStore struct {
	*storage.SQLiteStore
	calls  int
	failOn int
	job    *storage.ArchiveFinalizationJob
	err    error
}

func (s *browserSelectionCurrentPublicationStore) GetArchiveFinalizationJobContext(ctx context.Context, id string) (*storage.ArchiveFinalizationJob, error) {
	if id == "rca-run" {
		s.calls++
		if s.calls == s.failOn {
			return s.job, s.err
		}
	}
	return s.SQLiteStore.GetArchiveFinalizationJobContext(ctx, id)
}
func TestRCABrowserSelectionCurrentPublicationMustRemainSettled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failOn int
		job    *storage.ArchiveFinalizationJob
		err    error
	}{
		{"failed retryable current job", 1, &storage.ArchiveFinalizationJob{LastError: "retry pending"}, nil},
		{"current publication read error", 1, nil, errors.New("publication read unavailable")},
		{"current publication changed during inspection", 2, &storage.ArchiveFinalizationJob{}, nil},
		{"current publication recheck error", 2, nil, errors.New("publication recheck unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store := rcaTestServer(t)
			policy := e1Policy(e1LCP)
			policy.Baseline = BrowserPolicyBaseline{Mode: "latest_compatible"}
			pinRCAAdmissionPolicy(t, server, saveBrowserPolicy(t, store, "web", 1, policy))
			probe := &browserSelectionCurrentPublicationStore{SQLiteStore: store, failOn: tc.failOn, job: tc.job, err: tc.err}
			server.runner.archiveStore = probe
			reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
			if reply.Code != http.StatusServiceUnavailable || probe.calls != tc.failOn {
				t.Fatalf("current publication accepted: %d %s; calls=%d", reply.Code, reply.Body, probe.calls)
			}
		})
	}
}

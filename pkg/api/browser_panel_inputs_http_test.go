package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func browserPanelHTTPFixture(t *testing.T) (*Server, string, *storage.RunArchiveRecord, storage.AnalyticsQuery) {
	t.Helper()
	server, project := newGrafanaDashboardAPIServer(t)
	closeBatchTestServer(t, server)
	record, start := nativeSeriesFixture(t)
	attachD2Documents(t, record, d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141"), at: start.Add(2 * time.Second), value: 100, final: true})
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.ProjectID = project
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(raw)
	server.runner.mu.Lock()
	server.runner.tests[record.TestID] = &ManagedTest{ID: record.TestID, Name: "Current", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives[record.TestID] = record
	server.runner.mu.Unlock()
	if err := server.store.SaveTest(&storage.TestRecord{ID: record.TestID, Name: "Browser fixture", ProjectID: project, Status: "COMPLETE", StartTime: start}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	q := tupleQuery()
	q.QueryVersion = 4
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 200 {
		t.Fatalf("preview %d %s", response.Code, response.Body.String())
	}
	var result NativeArchiveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.BrowserInputs == nil || len(result.BrowserInputs.Current.TupleIDs) != 1 {
		t.Fatalf("missing measured pins: %+v", result.BrowserInputs)
	}
	q.BrowserInputs = result.BrowserInputs
	return server, project, record, q
}

func browserPanelDefinitionFixture(project string, q storage.AnalyticsQuery) storage.DashboardDefinition {
	return storage.DashboardDefinition{ProjectID: project, Name: "Frozen browser", Visibility: "project", Tabs: []storage.DashboardTab{{ID: "main", Title: "Main", Panels: []storage.DashboardPanel{{ID: "browser", Title: "Browser", Kind: "line", X: 0, Y: 0, W: 24, H: 8, Query: &q}}}}}
}

func TestBrowserPanelInputPinsHTTPPublicationChangeAndHistoricalMetadata(t *testing.T) {
	server, project, record, q := browserPanelHTTPFixture(t)
	definition := browserPanelDefinitionFixture(project, q)
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", definition)
	if response.Code != 201 {
		t.Fatalf("save %d %s", response.Code, response.Body.String())
	}
	var saved storage.DashboardDefinition
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tabs[0].Panels[0].Query.BrowserInputs.Current.ManifestSHA256 != record.ManifestSHA256 {
		t.Fatal("save changed pins")
	}
	path := "/api/v1/monitoring/dashboard-definitions/" + saved.ID
	response = dashboardAPIRequest(t, server, http.MethodGet, path, nil)
	if response.Code != 200 {
		t.Fatal("saved panel unavailable", response.Body.String())
	}
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, ' ')
	if err := os.WriteFile(record.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	next := *record
	next.ManifestSHA256 = sha256BytesHex(raw)
	server.runner.mu.Lock()
	server.runner.archives[record.TestID] = &next
	if err := server.store.SaveRunArchive(&next); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Unlock()
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 422 {
		t.Fatalf("stale read accepted %d %s", response.Code, response.Body.String())
	}
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", definition)
	if response.Code != 400 {
		t.Fatalf("stale save accepted %d %s", response.Code, response.Body.String())
	}
	response = dashboardAPIRequest(t, server, http.MethodPatch, path, map[string]any{"revision": 1, "name": "Historical frozen browser"})
	if response.Code != 200 {
		t.Fatalf("metadata edit refreshed or refused historical pins %d %s", response.Code, response.Body.String())
	}
	var renamed storage.DashboardDefinition
	if err := json.Unmarshal(response.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Tabs[0].Panels[0].Query.BrowserInputs.Current.ManifestSHA256 != record.ManifestSHA256 {
		t.Fatal("metadata edit silently refreshed pins")
	}
}

func TestBrowserPanelInputPinsHTTPRefusesForeignMissingAndOversizedSave(t *testing.T) {
	server, project, record, q := browserPanelHTTPFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*storage.DashboardDefinition)
	}{
		{"foreign project", func(d *storage.DashboardDefinition) { d.ProjectID = "other" }},
		{"forged run", func(d *storage.DashboardDefinition) {
			d.Tabs[0].Panels[0].Query.BrowserInputs.Current.RunID = "missing"
		}},
		{"changed query", func(d *storage.DashboardDefinition) { d.Tabs[0].Panels[0].Query.EndSeconds = 30 }},
		{"missing pins", func(d *storage.DashboardDefinition) { d.Tabs[0].Panels[0].Query.BrowserInputs = nil }},
		{"too many panels", func(d *storage.DashboardDefinition) {
			panel := d.Tabs[0].Panels[0]
			d.Tabs[0].Panels = make([]storage.DashboardPanel, 21)
			for i := range d.Tabs[0].Panels {
				d.Tabs[0].Panels[i] = panel
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(browserPanelDefinitionFixture(project, q))
			var d storage.DashboardDefinition
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&d)
			response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", d)
			if response.Code != 400 {
				t.Fatalf("invalid save %d %s", response.Code, response.Body.String())
			}
		})
	}
	server.runner.mu.Lock()
	delete(server.runner.tests, record.TestID)
	delete(server.runner.archives, record.TestID)
	server.runner.mu.Unlock()
	if err := server.store.DeleteTest(record.TestID); err != nil {
		t.Fatal(err)
	}
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", browserPanelDefinitionFixture(project, q))
	if response.Code != 400 {
		t.Fatalf("deleted inputs saved %d %s", response.Code, response.Body.String())
	}
}

func TestBrowserPanelInputPinsHTTPTwoPublicationsAndForeignBaseline(t *testing.T) {
	server, project, _, q := browserPanelHTTPFixture(t)
	record, start := nativeSeriesFixture(t)
	attachD2Documents(t, record, d2Document{id: 2, metric: "lcp", cohort: d2Cohort("142"), at: start.Add(3 * time.Second), value: 140, final: true})
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	record.TestID = "baseline"
	manifest.TestID = record.TestID
	manifest.Provenance.ProjectID = project
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(raw)
	server.runner.mu.Lock()
	server.runner.tests[record.TestID] = &ManagedTest{ID: record.TestID, Name: "Baseline", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives[record.TestID] = record
	server.runner.mu.Unlock()
	if err := server.store.SaveTest(&storage.TestRecord{ID: record.TestID, Name: "Browser fixture", ProjectID: project, Status: "COMPLETE", StartTime: start}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	q.ComparisonRunID = record.TestID
	q.BrowserInputs = nil
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 200 {
		t.Fatalf("comparison preview %d %s", response.Code, response.Body.String())
	}
	var result NativeArchiveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.BrowserInputs == nil || result.BrowserInputs.Baseline == nil || result.BrowserInputs.Baseline.ManifestSHA256 != record.ManifestSHA256 || result.BrowserInputs.Current.ManifestSHA256 == record.ManifestSHA256 {
		t.Fatal("comparison did not bind distinct publications")
	}
	// Each input fits one tuple, but their differing runtime cohorts require
	// two rendered series. Both reusable and frozen previews enforce the union.
	for _, version := range []int{3, 4} {
		limited := q
		limited.QueryVersion = version
		limited.MaxSeries = 1
		response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", limited)
		if response.Code != 422 || !strings.Contains(response.Body.String(), "combined current/baseline browser tuple limit exceeded") {
			t.Fatalf("oversized v%d union read %d %s", version, response.Code, response.Body.String())
		}
	}
	limited := q
	limited.MaxSeries = 1
	pins := *result.BrowserInputs
	limited.BrowserInputs = &pins
	pins.QuerySHA256 = storage.BrowserPanelQueryDigest(limited)
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", browserPanelDefinitionFixture(project, limited))
	if response.Code != 400 {
		t.Fatalf("oversized union saved %d %s", response.Code, response.Body.String())
	}
	q.BrowserInputs = result.BrowserInputs
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", browserPanelDefinitionFixture(project, q))
	if response.Code != 201 {
		t.Fatalf("comparison save %d %s", response.Code, response.Body.String())
	}
	server.runner.mu.Lock()
	server.runner.tests[record.TestID].ProjectID = "foreign"
	server.runner.mu.Unlock()
	if err := server.store.SaveProject(&storage.Project{ID: "foreign", Name: "Foreign", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.DeleteTest(record.TestID); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveTest(&storage.TestRecord{ID: record.TestID, Name: "Foreign", ProjectID: "foreign", Status: "COMPLETE", StartTime: start}); err != nil {
		t.Fatal(err)
	}
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 404 {
		t.Fatalf("foreign baseline read %d %s", response.Code, response.Body.String())
	}
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", browserPanelDefinitionFixture(project, q))
	if response.Code != 400 {
		t.Fatalf("foreign baseline saved %d %s", response.Code, response.Body.String())
	}
}

func TestBrowserPanelInputPinsHTTPRefusesUnprovedOrTamperedBytes(t *testing.T) {
	for _, mode := range []string{"missing publication proof", "changed manifest bytes", "changed document bytes"} {
		t.Run(mode, func(t *testing.T) {
			server, project, record, q := browserPanelHTTPFixture(t)
			switch mode {
			case "missing publication proof":
				next := *record
				next.ManifestSHA256 = ""
				server.runner.mu.Lock()
				server.runner.archives[record.TestID] = &next
				if err := server.store.SaveRunArchive(&next); err != nil {
					t.Fatal(err)
				}
				server.runner.mu.Unlock()
			case "changed manifest bytes":
				raw, err := os.ReadFile(record.ManifestPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record.ManifestPath, append(raw, ' '), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed document bytes":
				path := record.ArchivePath + "/" + browserDocumentsFileName
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
			if response.Code != 422 {
				t.Fatalf("unproved bytes read %d %s", response.Code, response.Body.String())
			}
			response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", browserPanelDefinitionFixture(project, q))
			if response.Code != 400 {
				t.Fatalf("unproved bytes saved %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBrowserPanelInputPinsHTTPDefaultLimitRoundTrip(t *testing.T) {
	server, project, _, q := browserPanelHTTPFixture(t)
	q.MaxSeries = 0
	q.BrowserInputs = nil
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 200 {
		t.Fatalf("preview %d %s", response.Code, response.Body.String())
	}
	var preview NativeArchiveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	q.BrowserInputs = preview.BrowserInputs
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", browserPanelDefinitionFixture(project, q))
	if response.Code != 201 {
		t.Fatalf("save %d %s", response.Code, response.Body.String())
	}
	var saved storage.DashboardDefinition
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	query := saved.Tabs[0].Panels[0].Query
	if query.MaxSeries != 20 || query.BrowserInputs.QuerySHA256 != preview.BrowserInputs.QuerySHA256 {
		t.Fatalf("default changed query identity: %+v", query)
	}
	response = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", query)
	if response.Code != 200 {
		t.Fatalf("saved query rejected %d %s", response.Code, response.Body.String())
	}
}

// The separate real SQL pool models connection contention without changing the
// server's other store operations or leaking a background read after cancellation.
type browserPanelDeadlineStore struct {
	archive bool
	storage.Store
	pool  *sql.DB
	calls int
}

func (s *browserPanelDeadlineStore) GetTestContext(ctx context.Context, id string) (*storage.TestRecord, error) {
	if s.archive {
		return s.Store.(interface {
			GetTestContext(context.Context, string) (*storage.TestRecord, error)
		}).GetTestContext(ctx, id)
	}
	s.calls++
	var selected string
	if err := s.pool.QueryRowContext(ctx, "SELECT id FROM tests WHERE id = ?", id).Scan(&selected); err != nil {
		return nil, err
	}
	return s.Store.(interface {
		GetTestContext(context.Context, string) (*storage.TestRecord, error)
	}).GetTestContext(ctx, selected)
}
func (s *browserPanelDeadlineStore) GetRunArchiveContext(ctx context.Context, id string) (*storage.RunArchiveRecord, error) {
	if s.archive {
		s.calls++
		var selected string
		if err := s.pool.QueryRowContext(ctx, "SELECT id FROM tests WHERE id = ?", id).Scan(&selected); err != nil {
			return nil, err
		}
	}
	return s.Store.(interface {
		GetRunArchiveContext(context.Context, string) (*storage.RunArchiveRecord, error)
	}).GetRunArchiveContext(ctx, id)
}
func TestBrowserPanelSavedValidationHonorsIdentityDeadline(t *testing.T) {
	for _, archive := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("archive=%v/canceled=%v", archive, canceled), func(t *testing.T) {
				server, project, _, q := browserPanelHTTPFixture(t)
				actual := server.store.(*storage.SQLiteStore)
				pool, err := sql.Open("sqlite", actual.BackendInfo().DSN)
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Close()
				pool.SetMaxOpenConns(1)
				conn, err := pool.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				probe := &browserPanelDeadlineStore{Store: server.store, pool: pool, archive: archive}
				server.store = probe
				defer func() { server.store = actual }()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				if canceled {
					cancel()
				}
				request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
				response := httptest.NewRecorder()
				definition := browserPanelDefinitionFixture(project, q)
				expectedCalls := 1
				if archive && canceled {
					expectedCalls = 0
				}
				before := len(analysisPipelineJobs)
				finished := make(chan bool, 1)
				go func() { finished <- server.validateSavedBrowserPanelInputs(response, request, &definition) }()
				select {
				case accepted := <-finished:
					if accepted || response.Code != 400 || probe.calls != expectedCalls || len(analysisPipelineJobs) != before {
						t.Fatal("identity deadline or admission release failed", accepted, response.Code, probe.calls)
					}
				case <-time.After(time.Second):
					conn.Close()
					<-finished
					t.Fatal("saved panel validation ignored identity deadline")
				}
			})
		}
	}
}

func TestBrowserPanelSavedInputsRejectForeignPublicationIdentity(t *testing.T) {
	for _, field := range []string{"project", "incarnation"} {
		t.Run(field, func(t *testing.T) {
			server, project, record, q := browserPanelHTTPFixture(t)
			manifest, err := loadRunArchiveManifestFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			if field == "project" {
				manifest.Provenance.ProjectID = "foreign"
			} else {
				manifest.Provenance.RunIncarnation = "another-execution"
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(record.ManifestPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			record.ManifestSHA256 = sha256BytesHex(raw)
			if err := server.store.SaveRunArchive(record); err != nil {
				t.Fatal(err)
			}
			q.BrowserInputs.Current.ManifestSHA256 = record.ManifestSHA256
			definition := browserPanelDefinitionFixture(project, q)
			w := httptest.NewRecorder()
			if server.validateSavedBrowserPanelInputs(w, httptest.NewRequest(http.MethodPost, "/", nil), &definition) || w.Code != 400 {
				t.Fatal("foreign publication accepted", w.Code)
			}
		})
	}
}

func TestBrowserPanelInputPinsHTTPV2PolicyTombstoneAndSelectionReceipt(t *testing.T) {
	server, project, record, _ := browserPanelHTTPFixture(t)
	defStore := server.store.(storage.AnalysisDefinitionStore)
	rcaStore := server.store.(storage.RCAStore)

	browserPolicyRaw := json.RawMessage(`{"version":1,"metrics":[{"metric":"lcp","good":2500,"poor":4000,"weight":1}],"baseline":{"mode":"latest_compatible"}}`)
	browserPolicySHA := sha256BytesHex(browserPolicyRaw)
	if _, err := defStore.SaveAnalysisDefinition(storage.AnalysisDefinition{
		ProjectID:  project,
		ID:         "pol-browser",
		Kind:       storage.AnalysisDefinitionKindBrowserPolicy,
		Name:       "Browser policy",
		Definition: browserPolicyRaw,
	}, 0); err != nil {
		t.Fatal(err)
	}
	journeyPolicyRaw := json.RawMessage(`{"version":1,"journeys":[{"id":"checkout","name":"Checkout","start":{"step_path":"actions[0]"},"complete":[{"step_path":"actions[1]"}]}]}`)
	journeyPolicySHA := sha256BytesHex(journeyPolicyRaw)
	if _, err := defStore.SaveAnalysisDefinition(storage.AnalysisDefinition{
		ProjectID:  project,
		ID:         "pol-journey",
		Kind:       storage.AnalysisDefinitionKindJourneyPolicy,
		Name:       "Journey policy",
		Definition: journeyPolicyRaw,
	}, 0); err != nil {
		t.Fatal(err)
	}

	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.PolicyPins = []RunPolicyPin{
		{Kind: storage.AnalysisDefinitionKindJourneyPolicy, ID: "pol-journey", Name: "Journey policy", Revision: 1, SHA256: journeyPolicySHA, Source: "run_selection"},
		{Kind: storage.AnalysisDefinitionKindBrowserPolicy, ID: "pol-browser", Name: "Browser policy", Revision: 1, SHA256: browserPolicySHA, Source: "run_selection"},
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, rawManifest, 0600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(rawManifest)
	server.runner.mu.Lock()
	server.runner.archives[record.TestID] = record
	server.runner.mu.Unlock()
	if err := server.store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}

	archiveDigest, err := rcaEvidenceDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	selectionReceipt := &rca.BrowserBaselineSelection{
		Version: rca.BrowserSelectionVersion,
		Policy:  "pol-browser@1",
		Status:  "no_compatible_candidate",
	}
	selectionBytes, err := json.Marshal(selectionReceipt)
	if err != nil {
		t.Fatal(err)
	}
	selectionSHA := sha256BytesHex(selectionBytes)
	saveCompletedRCA := func(sel *rca.BrowserBaselineSelection, selSHA string, attempt int) {
		t.Helper()
		rcaInputs := rca.InputDigests{
			Archive:          archiveDigest,
			BrowserSelection: selSHA,
			Engine:           rcaEngineVersion,
			SignatureSet:     "sig.v1",
		}
		payloadBytes, err := json.Marshal(rcaAnalysisPayload{
			Inputs:           rcaInputs,
			ArchiveManifest:  record.ManifestSHA256,
			BrowserSelection: sel,
			Attempt:          attempt,
			Assessment: &rca.Assessment{
				SchemaVersion: rca.AssessmentSchemaVersion,
				Inputs:        rcaInputs,
				BrowserBaseline: &rca.BrowserBaseline{
					Version:      rca.BrowserBaselineVersion,
					Status:       rca.DetectionComplete,
					Policy:       "pol-browser@1",
					PolicySHA256: browserPolicySHA,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		analysis, _, err := rcaStore.CreateRCAAnalysisWithOrigin(project, record.TestID, rcaInputDigest(rcaInputs, attempt), storage.RCAOriginAutomatic, payloadBytes, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := rcaStore.ClaimRCAAnalysis("rca-worker", time.Now().UTC(), time.Minute)
		if err != nil || claimed == nil || claimed.ID != analysis.ID {
			t.Fatalf("claim RCA analysis = %+v, err=%v", claimed, err)
		}
		completedAt := time.Now().UTC()
		if recorded, err := rcaStore.CompleteRCAAnalysisFenced(claimed.ID, "rca-worker", storage.RCAStateCompleted, payloadBytes, completedAt, "", "", 0); err != nil || !recorded {
			t.Fatalf("CompleteRCAAnalysisFenced recorded=%v err=%v", recorded, err)
		}
	}
	saveCompletedRCA(selectionReceipt, selectionSHA, 0)
	// A subsequent manual/ad-hoc RCA analysis without a BrowserSelection receipt
	// (or a tampered row with BrowserSelection removed while Inputs.BrowserSelection is set)
	// must not shadow the earlier policy-matched selection receipt for v2 panels.
	saveCompletedRCA(nil, "", 1)
	saveCompletedRCA(nil, strings.Repeat("7", 64), 2)

	q := tupleQuery()
	q.QueryVersion = 5
	previewResp := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if previewResp.Code != 200 {
		t.Fatalf("v5 preview %d %s", previewResp.Code, previewResp.Body.String())
	}
	var preview NativeArchiveResponse
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.BrowserInputs == nil || preview.BrowserInputs.Version != 2 || preview.BrowserInputs.SelectionReceiptSHA256 != selectionSHA || len(preview.BrowserInputs.PolicyPins) != 2 || preview.BrowserInputs.PolicyPins[0].Kind != storage.AnalysisDefinitionKindBrowserPolicy || preview.BrowserInputs.PolicyPins[1].Kind != storage.AnalysisDefinitionKindJourneyPolicy {
		t.Fatalf("unexpected v5 browser_inputs: %+v", preview.BrowserInputs)
	}
	q.BrowserInputs = preview.BrowserInputs
	definition := browserPanelDefinitionFixture(project, q)
	saveResp := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", definition)
	if saveResp.Code != 201 {
		t.Fatalf("v5 save %d %s", saveResp.Code, saveResp.Body.String())
	}

	// Advancing the browser policy to revision 2 keeps historical revision 1 readable.
	if _, err := defStore.SaveAnalysisDefinition(storage.AnalysisDefinition{
		ProjectID:  project,
		ID:         "pol-browser",
		Kind:       storage.AnalysisDefinitionKindBrowserPolicy,
		Name:       "Browser policy v2",
		Definition: browserPolicyRaw,
	}, 1); err != nil {
		t.Fatal(err)
	}
	if reopen := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q); reopen.Code != 200 {
		t.Fatalf("v5 reopen after policy update %d %s", reopen.Code, reopen.Body.String())
	}

	// If a newer completed RCA analysis changes the selection receipt, the saved panel refuses until re-previewed.
	otherReceipt := &rca.BrowserBaselineSelection{
		Version: rca.BrowserSelectionVersion,
		Policy:  "pol-browser@2",
		Status:  "no_compatible_candidate",
	}
	otherReceiptBytes, err := json.Marshal(otherReceipt)
	if err != nil {
		t.Fatal(err)
	}
	saveCompletedRCA(otherReceipt, sha256BytesHex(otherReceiptBytes), 3)
	if reopen := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q); reopen.Code != 422 {
		t.Fatalf("stale selection receipt accepted %d %s", reopen.Code, reopen.Body.String())
	}
	if staleSave := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", definition); staleSave.Code != 400 {
		t.Fatalf("stale selection receipt saved %d %s", staleSave.Code, staleSave.Body.String())
	}

	// Restore matching selection receipt, verify tampered BrowserSelection receipt is rejected by rcaBrowserBaselineMatchesPolicyDefinition,
	// then tombstone the pinned browser policy.
	saveCompletedRCA(selectionReceipt, selectionSHA, 4)
	tamperedPayload := rcaAnalysisPayload{
		Inputs: rca.InputDigests{
			Archive:          archiveDigest,
			BrowserSelection: selectionSHA,
		},
		BrowserSelection: &rca.BrowserBaselineSelection{
			Version:        rca.BrowserSelectionVersion,
			Status:         "selected",
			SelectedTestID: "base-1",
		},
		Request: rcaAnalysisRequest{BaselineTestID: "base-1"},
		Assessment: &rca.Assessment{
			BrowserBaseline: &rca.BrowserBaseline{
				Policy:       "pol-browser@1",
				PolicySHA256: browserPolicySHA,
			},
		},
	}
	if rcaBrowserBaselineMatchesPolicyDefinition(manifest, tamperedPayload, BrowserPolicyRef{ID: "pol-browser", Revision: 1, SHA256: browserPolicySHA}, BrowserPolicyDefinition{
		Version:  1,
		Baseline: BrowserPolicyBaseline{Mode: "latest_compatible"},
	}) {
		t.Fatal("expected rcaBrowserBaselineMatchesPolicyDefinition to reject tampered BrowserSelection receipt whose digest does not match Inputs.BrowserSelection")
	}
	tamperedRemovedReceipt := tamperedPayload
	tamperedRemovedReceipt.BrowserSelection = nil
	if rcaBrowserBaselineMatchesPolicyDefinition(manifest, tamperedRemovedReceipt, BrowserPolicyRef{ID: "pol-browser", Revision: 1, SHA256: browserPolicySHA}, BrowserPolicyDefinition{
		Version:  1,
		Baseline: BrowserPolicyBaseline{Mode: "pinned_run", RunID: "base-1"},
	}) {
		t.Fatal("expected rcaBrowserBaselineMatchesPolicyDefinition to reject tampered payload with BrowserSelection removed while Inputs.BrowserSelection is set")
	}
	if reopen := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q); reopen.Code != 200 {
		t.Fatalf("restored selection receipt rejected %d %s", reopen.Code, reopen.Body.String())
	}
	if _, err := defStore.SaveAnalysisDefinition(storage.AnalysisDefinition{
		ProjectID:  project,
		ID:         "pol-browser",
		Kind:       storage.AnalysisDefinitionKindBrowserPolicy,
		Name:       "Browser policy v2",
		Definition: browserPolicyRaw,
		Deleted:    true,
	}, 2); err != nil {
		t.Fatal(err)
	}
	if reopen := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q); reopen.Code != 422 || !strings.Contains(reopen.Body.String(), "pinned analysis policy has been deleted") {
		t.Fatalf("tombstoned policy accepted on reopen %d %s", reopen.Code, reopen.Body.String())
	}
	if tombstonedSave := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", definition); tombstonedSave.Code != 400 {
		t.Fatalf("tombstoned policy accepted on save %d %s", tombstonedSave.Code, tombstonedSave.Body.String())
	}
}

func TestSavedPanelInputsV2ComparisonToSavedPanelToReportAndStaleMissingStates(t *testing.T) {
	server, project, record, _ := browserPanelHTTPFixture(t)
	baseRecord, baseStart := nativeSeriesFixture(t)
	baseRecord.TestID = "run-baseline"
	rawBase, err := os.ReadFile(baseRecord.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var baseManifest RunArchiveManifest
	if err := json.Unmarshal(rawBase, &baseManifest); err != nil {
		t.Fatal(err)
	}
	baseManifest.TestID = baseRecord.TestID
	baseManifest.Provenance.ProjectID = project
	rawBase, err = json.Marshal(baseManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseRecord.ManifestPath, rawBase, 0o600); err != nil {
		t.Fatal(err)
	}
	baseRecord.ManifestSHA256 = sha256BytesHex(rawBase)
	server.runner.mu.Lock()
	server.runner.tests[baseRecord.TestID] = &ManagedTest{ID: baseRecord.TestID, Name: "Baseline", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives[baseRecord.TestID] = baseRecord
	server.runner.mu.Unlock()
	if err := server.store.SaveTest(&storage.TestRecord{ID: baseRecord.TestID, Name: "Baseline", ProjectID: project, Status: "COMPLETE", StartTime: baseStart}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveRunArchive(baseRecord); err != nil {
		t.Fatal(err)
	}

	// Step 1: Preview an archive comparison query with UI defaults omitted; controller returns PanelInputs v2 with frozen defaults.
	q := storage.AnalyticsQuery{
		Population:      "action",
		Metric:          "response_time",
		Aggregation:     "p95",
		ComparisonRunID: baseRecord.TestID,
		EndSeconds:      60,
		BucketSeconds:   10,
	}
	previewResp := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", q)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview %d: %s", previewResp.Code, previewResp.Body.String())
	}
	var preview NativeArchiveResponse
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.PanelInputs == nil || preview.PanelInputs.Version != 2 || preview.PanelInputs.Current.ManifestSHA256 != record.ManifestSHA256 || preview.PanelInputs.Baseline == nil || preview.PanelInputs.Baseline.ManifestSHA256 != baseRecord.ManifestSHA256 {
		t.Fatalf("unexpected preview panel_inputs: %+v", preview.PanelInputs)
	}

	// Verify buildComparisonSavedPanelInputs for a protocol- and transaction-filtered byte comparison passes ValidateSavedPanelInputs and saves cleanly into a dashboard definition.
	byteCompareReq := AnalysisComparisonRequest{
		Left: AnalysisSeriesSelection{
			RunID:       record.TestID,
			Metric:      "bytes_sent",
			Aggregation: "sum",
			Protocol:    "http",
			Transaction: "login",
		},
		Right: AnalysisSeriesSelection{
			RunID:       baseRecord.TestID,
			Metric:      "bytes_sent",
			Aggregation: "sum",
			Protocol:    "http",
			Transaction: "login",
		},
		From:          baseStart,
		To:            baseStart.Add(60 * time.Second),
		BucketSeconds: 10,
		Mode:          "overlay",
	}
	bytePins := server.buildComparisonSavedPanelInputs(context.Background(), project, byteCompareReq)
	if bytePins == nil || bytePins.Population != "byte" || bytePins.Drilldown.MaxSeries != 1 {
		t.Fatalf("expected byte comparison PanelInputs with Population=byte and MaxSeries=1, got %+v", bytePins)
	}
	bytePanelQuery := storage.AnalyticsQuery{
		QueryVersion:    5,
		Population:      "byte",
		Metric:          "bytes_sent",
		Aggregation:     "sum",
		ComparisonRunID: baseRecord.TestID,
		Alignment:       bytePins.Alignment,
		CoveragePolicy:  bytePins.CoveragePolicy,
		CatalogVersion:  bytePins.CatalogVersion,
		BucketSeconds:   10,
		EndSeconds:      60,
		Filters:         map[string]string{"protocol": "http", "action": "login"},
		PanelInputs:     bytePins,
	}
	if err := storage.ValidateSavedPanelInputs(bytePanelQuery); err != nil {
		t.Fatalf("ValidateSavedPanelInputs rejected comparison byte panel: %v", err)
	}

	// Step 2: Save the general panel definition with frozen PanelInputs v2.
	q.PanelInputs = preview.PanelInputs
	definition := storage.DashboardDefinition{
		ProjectID:  project,
		Name:       "General Pinned Comparison Panel",
		Visibility: "project",
		Tabs: []storage.DashboardTab{{
			ID:    "main",
			Title: "Main",
			Panels: []storage.DashboardPanel{{
				ID:    "p-rt",
				Title: "Response Time P95",
				Kind:  "line",
				W:     12,
				H:     8,
				Query: &q,
			}},
		}},
	}
	saveResp := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", definition)
	if saveResp.Code != http.StatusCreated {
		t.Fatalf("save general pinned panel %d: %s", saveResp.Code, saveResp.Body.String())
	}
	var savedDef storage.DashboardDefinition
	if err := json.Unmarshal(saveResp.Body.Bytes(), &savedDef); err != nil {
		t.Fatal(err)
	}
	savedQ := *savedDef.Tabs[0].Panels[0].Query
	if savedQ.Alignment != "elapsed" || savedQ.CoveragePolicy != "complete_bucket" || savedQ.CatalogVersion == "" || savedQ.MaxSeries != 1 {
		t.Fatalf("expected frozen UI defaults on saved query, got %+v", savedQ)
	}

	// Step 3: Check RunArchiveReport includes SavedAnalysisPanels with Status "ready", and replay with an Overlay pin preserves Overlay in response.PanelInputs.
	overlayQ := savedQ
	overlayInputs := *savedQ.PanelInputs
	overlayInputs.Overlay = &storage.SavedPanelSourcePin{
		RunID:          baseRecord.TestID,
		ManifestSHA256: baseRecord.ManifestSHA256,
	}
	overlayQ.PanelInputs = &overlayInputs
	overlayReplay := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", overlayQ)
	if overlayReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 on replay with Overlay pin, got %d: %s", overlayReplay.Code, overlayReplay.Body.String())
	}
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: record.TestID, Provenance: RunArchiveProvenance{ProjectID: project}}}
	server.attachDashboardPanels(report)
	if len(report.SavedAnalysisPanels) != 1 || report.SavedAnalysisPanels[0].Status != "ready" {
		t.Fatalf("expected 1 ready SavedAnalysisPanel on archive report, got %+v", report.SavedAnalysisPanels)
	}

	// Step 4: Modify the baseline archive manifest -> Report and Replay surface explicit "stale" state.
	mutatedBase := append([]byte(nil), rawBase...)
	mutatedBase = append(mutatedBase, ' ')
	if err := os.WriteFile(baseRecord.ManifestPath, mutatedBase, 0o600); err != nil {
		t.Fatal(err)
	}
	nextBase := *baseRecord
	nextBase.ManifestSHA256 = sha256BytesHex(mutatedBase)
	server.runner.mu.Lock()
	server.runner.archives[baseRecord.TestID] = &nextBase
	server.runner.mu.Unlock()
	if err := server.store.SaveRunArchive(&nextBase); err != nil {
		t.Fatal(err)
	}
	staleReport := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: record.TestID, Provenance: RunArchiveProvenance{ProjectID: project}}}
	server.attachDashboardPanels(staleReport)
	if len(staleReport.SavedAnalysisPanels) != 1 || staleReport.SavedAnalysisPanels[0].Status != "stale" {
		t.Fatalf("expected stale status in report after baseline archive change, got %+v", staleReport.SavedAnalysisPanels)
	}
	staleReplay := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", savedQ)
	if staleReplay.Code != http.StatusUnprocessableEntity || !strings.Contains(staleReplay.Body.String(), "stale:") {
		t.Fatalf("expected 422 stale error on replay, got %d: %s", staleReplay.Code, staleReplay.Body.String())
	}

	// Step 5: Delete the baseline run -> Report and Replay surface explicit "missing_input" state.
	server.runner.mu.Lock()
	delete(server.runner.tests, baseRecord.TestID)
	delete(server.runner.archives, baseRecord.TestID)
	server.runner.mu.Unlock()
	if err := server.store.DeleteTest(baseRecord.TestID); err != nil {
		t.Fatal(err)
	}
	missingReport := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: record.TestID, Provenance: RunArchiveProvenance{ProjectID: project}}}
	server.attachDashboardPanels(missingReport)
	if len(missingReport.SavedAnalysisPanels) != 1 || missingReport.SavedAnalysisPanels[0].Status != "missing_input" {
		t.Fatalf("expected missing_input status in report after baseline deletion, got %+v", missingReport.SavedAnalysisPanels)
	}
	missingReplay := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/run/archive/native-series", savedQ)
	if (missingReplay.Code != http.StatusNotFound && missingReplay.Code != http.StatusUnprocessableEntity) || !strings.Contains(missingReplay.Body.String(), "missing_input:") {
		t.Fatalf("expected missing_input error on replay after baseline deletion, got %d: %s", missingReplay.Code, missingReplay.Body.String())
	}
}

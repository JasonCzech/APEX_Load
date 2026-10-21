package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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
	record.ManifestSHA256 = sha256BytesHex(raw)
	server.runner.mu.Lock()
	server.runner.tests[record.TestID] = &ManagedTest{ID: record.TestID, Name: "Current", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives[record.TestID] = record
	server.runner.mu.Unlock()
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

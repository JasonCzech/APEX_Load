package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPrometheusDashboardDraftResolvesCommittedBindingsWithoutMigratingAcceptance(t *testing.T) {
	dashboard := &storage.GrafanaDashboard{ID: "dashboard", ProjectID: "proj-a", SourceSHA256: "source-hash", Bindings: map[string]string{"service": "checkout"}, Panels: []grafana.Panel{{PanelID: 1, Unit: "requests/s", DatasourceUID: "legacy-datasource", Thresholds: []grafana.PanelThreshold{{Severity: "critical"}}, Targets: []grafana.Target{
		{Expr: `rate(http_requests_total{job="$service",project_id="$project_id"}[$__rate_interval])`, LabelFields: []string{"job"}},
		{Expr: `up{test_id="$test_id"}`},
		{Expr: `up{job="$unbound"}`},
	}}}}
	result := prometheusDashboardDraft(dashboard, "connection", 3, 15, 300)
	if len(result.Selection.Queries) != 1 || result.Selection.Queries[0].Expression != `rate(http_requests_total{job="checkout",project_id="proj-a"}[60s])` || result.Selection.Queries[0].Unit != "requests/s" || len(result.DatasourceUIDs) != 1 || result.DatasourceUIDs[0] != "legacy-datasource" {
		t.Fatalf("resolved draft: %+v", result)
	}
	if result.ConnectionRevision != 3 || result.DashboardSHA256 != "source-hash" || len(result.Warnings) != 5 || result.Selection.Targets || result.Selection.Alerts {
		t.Fatalf("provenance or warnings missing: %+v", result)
	}
	body, err := json.Marshal(result)
	if err != nil || strings.Contains(string(body), "threshold") && strings.Contains(string(body), "critical") {
		t.Fatal("dashboard threshold adopted", string(body), err)
	}
	if dashboard.Panels[0].Targets[0].Expr == result.Selection.Queries[0].Expression {
		t.Fatal("stored dashboard was mutated")
	}
}

func TestPrometheusDashboardDraftBoundsAndUnsafeLabels(t *testing.T) {
	dashboard := &storage.GrafanaDashboard{ID: "d", ProjectID: "proj-a", Bindings: map[string]string{"service": `bad\"value`}}
	dashboard.Panels = []grafana.Panel{{PanelID: 1, Targets: []grafana.Target{{Expr: `up{job="$service"}`}}}}
	if result := prometheusDashboardDraft(dashboard, "c", 1, 15, 300); len(result.Selection.Queries) != 0 {
		t.Fatal("unsafe binding resolved", result)
	}
	dashboard.Bindings = nil
	dashboard.Panels = []grafana.Panel{{PanelID: 1, Targets: []grafana.Target{{Expr: "up", LabelFields: []string{"password"}}}}}
	if result := prometheusDashboardDraft(dashboard, "c", 1, 15, 300); len(result.Selection.Queries) != 0 {
		t.Fatal("sensitive labels retained", result)
	}
	dashboard.Panels = nil
	for i := 0; i < 21; i++ {
		dashboard.Panels = append(dashboard.Panels, grafana.Panel{PanelID: i, Targets: []grafana.Target{{Expr: "up"}}})
	}
	result := prometheusDashboardDraft(dashboard, "c", 1, 15, 300)
	if len(result.Selection.Queries) != 20 || !result.Truncated {
		t.Fatal("unbounded adoption", result)
	}
}

func TestPrometheusDashboardAdoptionProjectIsolationAndDraftOnly(t *testing.T) {
	server, durable := newTimeslotServer(t)
	store := durable.(storage.PrometheusConnectionStore)
	connection := &storage.PrometheusConnection{ID: "adoption-connection", ProjectID: "proj-a", Name: "Prometheus", CollectionMode: "direct", Config: prometheus.Config{Endpoint: "https://prom.example.com"}}
	if err := store.CreatePrometheusConnection(connection); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/projects/proj-a/monitoring/prometheus-connections/" + connection.ID + "/adopt-dashboard"
	if response := timeslotRequest(t, server, http.MethodPost, path, PrometheusDashboardAdoptionRequest{DashboardID: "d"}); response.Code != 409 {
		t.Fatal("unverified accepted", response.Code)
	}
	if ok, err := store.UpdatePrometheusConnectionHealth(connection.ID, "proj-a", 1, "connected", "", prometheus.Capabilities{Metrics: true}, time.Now().UTC()); !ok || err != nil {
		t.Fatal(ok, err)
	}
	connection, _ = store.GetPrometheusConnection(connection.ID)
	connection.Enabled = true
	if ok, err := store.UpdatePrometheusConnectionIfRevision(connection, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	dashboard := &storage.GrafanaDashboard{ID: "d", ProjectID: "proj-a", Name: "Grafana", SourceSHA256: "hash", Panels: []grafana.Panel{{PanelID: 1, Kind: grafana.PanelKindLine, GridPos: grafana.GridPos{W: 12, H: 6}, Targets: []grafana.Target{{Expr: "up"}}}}}
	if err := durable.SaveGrafanaDashboard(dashboard); err != nil {
		t.Fatal(err)
	}
	response := timeslotRequest(t, server, http.MethodPost, path, PrometheusDashboardAdoptionRequest{DashboardID: "d"})
	if response.Code != 200 {
		t.Fatalf("adoption %d %s", response.Code, response.Body.String())
	}
	var draft PrometheusDashboardAdoptionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &draft); err != nil || len(draft.Selection.Queries) != 1 {
		t.Fatal(draft, err)
	}
	if got := timeslotRequest(t, server, http.MethodPost, strings.Replace(path, "proj-a", "proj-b", 1), PrometheusDashboardAdoptionRequest{DashboardID: "d"}); got.Code != 404 {
		t.Fatal("cross-project connection", got.Code)
	}
	other := *dashboard
	other.ID, other.ProjectID = "foreign", "proj-b"
	if err := durable.SaveProject(&storage.Project{ID: "proj-b", Name: "Other project"}); err != nil {
		t.Fatal(err)
	}
	if err := durable.SaveGrafanaDashboard(&other); err != nil {
		t.Fatal(err)
	}
	if got := timeslotRequest(t, server, http.MethodPost, path, PrometheusDashboardAdoptionRequest{DashboardID: other.ID}); got.Code != 404 {
		t.Fatal("cross-project dashboard", got.Code)
	}
	for _, window := range []int{-1, 601} {
		if got := timeslotRequest(t, server, http.MethodPost, path, PrometheusDashboardAdoptionRequest{DashboardID: "d", RangeSeconds: window}); got.Code != 400 {
			t.Fatal(fmt.Sprintf("window %d: %d", window, got.Code))
		}
	}
	if sources, err := durable.(storage.RunMonitorStore).ListMonitorSources("proj-a"); err != nil || len(sources) != 0 {
		t.Fatal("adoption created sources", sources, err)
	}
}

func TestPrometheusDashboardAdoptionExcludesTransformedPanelsWithLegacyProvenance(t *testing.T) {
	converted, err := grafana.Convert(grafana.ImportRequest{Content: `{"title":"Transforms","schemaVersion":39,"panels":[{"id":1,"type":"text","title":"Excluded"},{"id":2,"type":"timeseries","title":"Derived CPU","transformations":[{"id":"reduce"}],"targets":[{"expr":"cpu","refId":"A"}]},{"id":3,"type":"timeseries","title":"Raw memory","targets":[{"expr":"memory","refId":"A"}]}]}`})
	if err != nil || len(converted.Dashboard.Panels) != 2 {
		t.Fatal(converted, err)
	}
	dashboard := &storage.GrafanaDashboard{ID: "d", ProjectID: "proj-a", Panels: converted.Dashboard.Panels, Diagnostics: converted.Unsupported}
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-%t", legacy), func(t *testing.T) {
			if legacy {
				for index := range dashboard.Panels {
					dashboard.Panels[index].UnsupportedTransformations = nil
				}
			}
			draft := prometheusDashboardDraft(dashboard, "c", 1, 15, 300)
			if len(draft.Selection.Queries) != 1 || draft.Selection.Queries[0].Expression != "memory" || !strings.Contains(strings.Join(draft.Warnings, " "), "unsupported Grafana transformations") {
				t.Fatalf("transformed panel adopted: %+v", draft)
			}
			if dashboard.Panels[0].Targets[0].Expr != "cpu" || len(dashboard.Panels[0].Targets) != 1 {
				t.Fatal("adoption changed existing dashboard snapshot targets")
			}
		})
	}
	// A legacy anonymous diagnostic cannot safely use the original panel index
	// because a preceding panel may have been excluded during import.
	for index := range dashboard.Diagnostics {
		if dashboard.Diagnostics[index].Element == "transformations" {
			dashboard.Diagnostics[index].Location = "panels[1]"
		}
	}
	if draft := prometheusDashboardDraft(dashboard, "c", 1, 15, 300); len(draft.Selection.Queries) != 0 || !strings.Contains(strings.Join(draft.Warnings, " "), "legacy transformation provenance") {
		t.Fatal("unverifiable legacy transformations adopted", draft)
	}
}

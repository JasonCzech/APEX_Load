package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/grafana"
)

func newGrafanaDashboardTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "grafana-dashboards.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func sampleDashboard(id, projectID string) *GrafanaDashboard {
	return &GrafanaDashboard{
		ID: id, ProjectID: projectID, Name: "Infra", SourceTitle: "Infra Dashboard",
		SourceSHA256: "abc123", SchemaVersion: 39, Enabled: true,
		Panels: []grafana.Panel{{
			PanelID: 1, Title: "CPU", Kind: grafana.PanelKindLine, Unit: "%", GridPos: grafana.GridPos{W: 12, H: 8},
			Targets: []grafana.Target{{RefID: "A", Expr: "cpu", LabelFields: []string{"instance"}}},
		}},
		Bindings:    map[string]string{"env": "prod"},
		Diagnostics: diag.Diagnostics{{Element: "panel", Detail: "note", Severity: diag.Info}},
	}
}

func TestGrafanaDashboardRoundTrip(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	if err := store.SaveGrafanaDashboard(sampleDashboard("dash-1", "project-a")); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetGrafanaDashboard("dash-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Fatalf("Enabled = false, want true (bool must survive INTEGER round-trip)")
	}
	if len(got.Panels) != 1 || got.Panels[0].Title != "CPU" || got.Panels[0].Targets[0].Expr != "cpu" {
		t.Fatalf("panels not preserved: %#v", got.Panels)
	}
	if got.Bindings["env"] != "prod" {
		t.Fatalf("bindings not preserved: %#v", got.Bindings)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Severity != diag.Info {
		t.Fatalf("diagnostics not preserved: %#v", got.Diagnostics)
	}
}

func TestGrafanaDashboardListScopedToProject(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	if err := store.SaveGrafanaDashboard(sampleDashboard("dash-a", "project-a")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGrafanaDashboard(sampleDashboard("dash-b", "project-b")); err != nil {
		t.Fatal(err)
	}
	listA, err := store.ListGrafanaDashboards("project-a", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0].ID != "dash-a" {
		t.Fatalf("project-a list = %#v, want only dash-a", listA)
	}
}

func TestGrafanaDashboardReimportUpdatesExistingSourceUID(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	first := sampleDashboard("dash-original", "project-a")
	first.SourceUID = "grafana-uid"
	first.Description = "curated description"
	if err := store.SaveGrafanaDashboard(first); err != nil {
		t.Fatal(err)
	}
	second := sampleDashboard("dash-new-random-id", "project-a")
	second.SourceUID = first.SourceUID
	second.SourceSHA256 = "new-sha"
	second.Panels[0].Title = "Updated CPU"
	if err := store.SaveGrafanaDashboard(second); err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("reimport id = %q, want %q", second.ID, first.ID)
	}
	list, err := store.ListGrafanaDashboards("project-a", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Panels[0].Title != "Updated CPU" || list[0].Description != "curated description" {
		t.Fatalf("synced dashboards = %#v", list)
	}
}

func TestGrafanaDashboardRejectsCrossProjectTakeover(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	if err := store.SaveGrafanaDashboard(sampleDashboard("shared-id", "project-a")); err != nil {
		t.Fatal(err)
	}
	// A save of the same id from another project must not overwrite the row.
	err := store.SaveGrafanaDashboard(sampleDashboard("shared-id", "project-b"))
	if err == nil {
		t.Fatal("expected cross-project takeover to be rejected")
	}
	got, err := store.GetGrafanaDashboard("shared-id")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != "project-a" {
		t.Fatalf("owner project = %q, want project-a (takeover leaked)", got.ProjectID)
	}
}

func TestGrafanaDashboardDeleteScoped(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	if err := store.SaveGrafanaDashboard(sampleDashboard("dash-1", "project-a")); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetGrafanaDashboard("dash-1")
	if err != nil {
		t.Fatal(err)
	}
	// Wrong project cannot delete.
	deleted, err := store.DeleteGrafanaDashboardIfUnchanged("dash-1", "project-b", loaded.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("delete from wrong project should not succeed")
	}
	// Owner can delete.
	deleted, err = store.DeleteGrafanaDashboardIfUnchanged("dash-1", "project-a", loaded.UpdatedAt)
	if err != nil || !deleted {
		t.Fatalf("owner delete = %v, %v; want true, nil", deleted, err)
	}
}

func TestGrafanaDashboardMetadataMutationsRejectStaleVersions(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	if err := store.SaveGrafanaDashboard(sampleDashboard("dash-cas", "project-a")); err != nil {
		t.Fatal(err)
	}
	first, err := store.GetGrafanaDashboard("dash-cas")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.GetGrafanaDashboard("dash-cas")
	if err != nil {
		t.Fatal(err)
	}
	first.Name = "First update"
	if updated, err := store.UpdateGrafanaDashboardIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = %v, %v; want true, nil", updated, err)
	}
	stale.Name = "Stale overwrite"
	if updated, err := store.UpdateGrafanaDashboardIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = %v, %v; want false, nil", updated, err)
	}
	if deleted, err := store.DeleteGrafanaDashboardIfUnchanged(stale.ID, stale.ProjectID, stale.UpdatedAt); err != nil || deleted {
		t.Fatalf("stale delete = %v, %v; want false, nil", deleted, err)
	}
	got, err := store.GetGrafanaDashboard("dash-cas")
	if err != nil || got.Name != "First update" {
		t.Fatalf("dashboard after stale mutations = %#v, %v", got, err)
	}
}

func TestGrafanaDashboardRejectsOversizedMetadata(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	dashboard := sampleDashboard("dash-oversized", "project-a")
	dashboard.Description = strings.Repeat("x", MaxGrafanaDashboardDescriptionBytes+1)
	if err := store.SaveGrafanaDashboard(dashboard); err == nil {
		t.Fatal("expected oversized description to be rejected")
	}
}

func TestGrafanaDashboardRejectsInvalidRuntimeShape(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)

	invalidKind := sampleDashboard("dash-invalid-kind", "project-a")
	invalidKind.Panels[0].Kind = "pie"
	if err := store.SaveGrafanaDashboard(invalidKind); err == nil {
		t.Fatal("expected unsupported panel kind to be rejected")
	}

	invalidGrid := sampleDashboard("dash-invalid-grid", "project-a")
	invalidGrid.Panels[0].GridPos.X = 24
	if err := store.SaveGrafanaDashboard(invalidGrid); err == nil {
		t.Fatal("expected invalid grid position to be rejected")
	}

	invalidThreshold := sampleDashboard("dash-invalid-threshold", "project-a")
	invalidThreshold.Panels[0].Thresholds = []grafana.PanelThreshold{{Color: "red", Severity: "catastrophic"}}
	if err := store.SaveGrafanaDashboard(invalidThreshold); err == nil {
		t.Fatal("expected invalid threshold severity to be rejected")
	}

	invalidBinding := sampleDashboard("dash-invalid-binding", "project-a")
	invalidBinding.Bindings = map[string]string{"project_id": "other"}
	if err := store.SaveGrafanaDashboard(invalidBinding); err == nil {
		t.Fatal("expected reserved binding to be rejected")
	}

	tooManyQueries := sampleDashboard("dash-too-many-queries", "project-a")
	tooManyQueries.Panels = nil
	for panelID := 1; panelID <= 7; panelID++ {
		targets := make([]grafana.Target, grafana.MaxTargetsPerPanel)
		for i := range targets {
			targets[i] = grafana.Target{RefID: "A", Expr: "up"}
		}
		tooManyQueries.Panels = append(tooManyQueries.Panels, grafana.Panel{
			PanelID: panelID, Title: "Panel", Kind: grafana.PanelKindLine, Targets: targets,
		})
	}
	if err := store.SaveGrafanaDashboard(tooManyQueries); err == nil {
		t.Fatal("expected aggregate query limit to be enforced")
	}
}

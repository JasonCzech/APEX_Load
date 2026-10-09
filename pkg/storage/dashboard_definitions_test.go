package storage

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func sampleDashboardDefinition(id string) *DashboardDefinition {
	return &DashboardDefinition{
		ID: id, ProjectID: DefaultProjectID, Name: "Shared operations", Visibility: "project",
		Tabs: []DashboardTab{{ID: "overview", Title: "Overview", Panels: []DashboardPanel{{
			ID: "latency", Title: "Latency", Kind: "line", X: 0, Y: 0, W: 12, H: 8,
			Query: &AnalyticsQuery{Metric: "response_time", Aggregation: "p95", Dimensions: []string{"region"}, BucketSeconds: 10, MaxSeries: 20},
		}}}},
	}
}

func TestDashboardDefinitionRevisionAndProjectIsolation(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/dashboard.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	definition := sampleDashboardDefinition("shared-dashboard")
	if err := store.SaveDashboardDefinition(definition); err != nil {
		t.Fatalf("save: %v", err)
	}
	if definition.Revision != 1 {
		t.Fatalf("revision = %d", definition.Revision)
	}
	loaded, err := store.GetDashboardDefinition(definition.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	loaded.Name = "Shared operations v2"
	updated, err := store.UpdateDashboardDefinitionIfRevision(loaded, 1)
	if err != nil || !updated || loaded.Revision != 2 {
		t.Fatalf("update = %v, %v, revision %d", updated, err, loaded.Revision)
	}
	stale := sampleDashboardDefinition(definition.ID)
	stale.ProjectID = definition.ProjectID
	if updated, err := store.UpdateDashboardDefinitionIfRevision(stale, 1); err != nil || updated {
		t.Fatalf("stale update = %v, %v", updated, err)
	}
	items, err := store.ListDashboardDefinitions(definition.ProjectID, 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %#v, %v", items, err)
	}
	if deleted, err := store.DeleteDashboardDefinitionIfRevision(definition.ID, definition.ProjectID, 1); err != nil || deleted {
		t.Fatalf("stale delete = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteDashboardDefinitionIfRevision(definition.ID, definition.ProjectID, 2); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
}

func TestDashboardDefinitionRejectsUnboundedQuery(t *testing.T) {
	definition := sampleDashboardDefinition("bad-dashboard")
	definition.Tabs[0].Panels[0].Query.MaxSeries = MaxDashboardSeries + 1
	if err := validateDashboardDefinition(definition); err == nil {
		t.Fatal("unbounded query was accepted")
	}
}

func TestDashboardDefinitionRejectsUnknownPanelAndFilter(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DashboardDefinition)
	}{
		{"panel kind", func(definition *DashboardDefinition) { definition.Tabs[0].Panels[0].Kind = "iframe" }},
		{"filter dimension", func(definition *DashboardDefinition) {
			definition.Tabs[0].Panels[0].Query.Filters = map[string]string{"raw_response": "secret"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := sampleDashboardDefinition("bad-dashboard")
			test.mutate(definition)
			if err := validateDashboardDefinition(definition); err == nil {
				t.Fatalf("%s was accepted", test.name)
			}
		})
	}
}

func TestNativeArchiveQueryValidation(t *testing.T) {
	q := AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 60, PropertyName: "kind", PropertyValue: "paid"}
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*AnalyticsQuery){func(q *AnalyticsQuery) { q.Population = "transaction" }, func(q *AnalyticsQuery) { q.EndSeconds = 0 }, func(q *AnalyticsQuery) { q.BucketSeconds = 0 }, func(q *AnalyticsQuery) { q.EndSeconds = 86400; q.BucketSeconds = 1 }, func(q *AnalyticsQuery) { q.PropertyValue = "" }, func(q *AnalyticsQuery) { q.Filters = map[string]string{"sql": "select"} }, func(q *AnalyticsQuery) { q.Metric = "resource_cpu" }} {
		bad := q
		mutate(&bad)
		if ValidateNativeArchiveQuery(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	d := sampleDashboardDefinition("archive")
	d.Tabs[0].Panels[0].Query = &q
	if err := validateDashboardDefinition(d); err != nil {
		t.Fatal(err)
	}
}

func TestNativeArchiveDefinitionRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/panels.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	d := sampleDashboardDefinition("filtered")
	d.Tabs[0].Panels[0].Query = &AnalyticsQuery{Population: "action", Metric: "observation_count", Aggregation: "count", BucketSeconds: 10, EndSeconds: 60, PropertyName: "kind", PropertyValue: "paid", ComparisonRunID: "baseline", Filters: map[string]string{"action": "Checkout"}}
	if err := store.SaveDashboardDefinition(d); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDashboardDefinition(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := got.Tabs[0].Panels[0].Query
	if q.Population != "action" || q.PropertyName != "kind" || q.PropertyValue != "paid" || q.ComparisonRunID != "baseline" || q.Filters["action"] != "Checkout" || q.EndSeconds != 60 || q.MaxSeries != 1 {
		t.Fatalf("lost saved selection: %+v", q)
	}
}

func TestDashboardDefinitionRevisionsAreKeptAndReopenable(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/dashboard-revisions.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	definition := sampleDashboardDefinition("revisioned-dashboard")
	if err := store.SaveDashboardDefinition(definition); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetDashboardDefinition(definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Name, loaded.Tabs[0].Panels[0].Query.Aggregation = "Shared operations v2", "p99"
	if updated, err := store.UpdateDashboardDefinitionIfRevision(loaded, 1); err != nil || !updated {
		t.Fatalf("update = %v %v", updated, err)
	}
	first, err := store.GetDashboardDefinitionRevision(definition.ID, 1)
	if err != nil || first.Revision != 1 || first.Name != "Shared operations" || first.Tabs[0].Panels[0].Query.Aggregation != "p95" || first.ProjectID != DefaultProjectID {
		t.Fatalf("revision 1 = %#v %v", first, err)
	}
	second, err := store.GetDashboardDefinitionRevision(definition.ID, 2)
	if err != nil || second.Name != "Shared operations v2" || second.Tabs[0].Panels[0].Query.Aggregation != "p99" {
		t.Fatalf("revision 2 = %#v %v", second, err)
	}
	if listed, err := store.ListDashboardDefinitionRevisions(definition.ID); err != nil || len(listed) != 2 || listed[0].Revision != 2 || listed[1].Revision != 1 {
		t.Fatalf("revisions = %#v %v", listed, err)
	}
	if _, err := store.GetDashboardDefinitionRevision(definition.ID, 3); !errors.Is(err, ErrDashboardRevisionNotRetained) {
		t.Fatalf("unsaved revision = %v", err)
	}
	// A stale update saves no revision.
	stale := sampleDashboardDefinition(definition.ID)
	if updated, _ := store.UpdateDashboardDefinitionIfRevision(stale, 1); updated {
		t.Fatal("stale update accepted")
	}
	if listed, _ := store.ListDashboardDefinitionRevisions(definition.ID); len(listed) != 2 {
		t.Fatalf("stale update recorded a revision: %#v", listed)
	}
	// The newest maxDashboardDefinitionRevisions are kept.
	for revision := int64(2); revision < 2+maxDashboardDefinitionRevisions; revision++ {
		loaded.Name = fmt.Sprintf("Shared operations r%d", revision+1)
		if updated, err := store.UpdateDashboardDefinitionIfRevision(loaded, revision); err != nil || !updated {
			t.Fatalf("update %d = %v %v", revision, updated, err)
		}
	}
	listed, err := store.ListDashboardDefinitionRevisions(definition.ID)
	if err != nil || len(listed) != maxDashboardDefinitionRevisions || listed[0].Revision != 2+maxDashboardDefinitionRevisions || listed[len(listed)-1].Revision != 3 {
		t.Fatalf("pruned revisions = %d first %d last %d %v", len(listed), listed[0].Revision, listed[len(listed)-1].Revision, err)
	}
	if _, err := store.GetDashboardDefinitionRevision(definition.ID, 2); !errors.Is(err, ErrDashboardRevisionNotRetained) {
		t.Fatalf("pruned revision = %v", err)
	}
	// Deleting the definition deletes its revisions.
	if deleted, err := store.DeleteDashboardDefinitionIfRevision(definition.ID, DefaultProjectID, 2+maxDashboardDefinitionRevisions); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if listed, err := store.ListDashboardDefinitionRevisions(definition.ID); err != nil || len(listed) != 0 {
		t.Fatalf("revisions after delete = %#v %v", listed, err)
	}
	if _, err := store.GetDashboardDefinitionRevision(definition.ID, 3); !errors.Is(err, ErrDashboardRevisionNotRetained) {
		t.Fatalf("revision of a deleted definition = %v", err)
	}
}

func TestDashboardQueryVersionIsValidated(t *testing.T) {
	definition := sampleDashboardDefinition("versioned-dashboard")
	definition.Tabs[0].Panels[0].Query.QueryVersion = AnalyticsQueryVersion
	if err := validateDashboardDefinition(definition); err != nil {
		t.Fatalf("current query version refused: %v", err)
	}
	definition.Tabs[0].Panels[0].Query.QueryVersion = AnalyticsQueryVersion + 1
	if err := validateDashboardDefinition(definition); err == nil {
		t.Fatal("a future query version was accepted")
	}
	// Running a query refuses newer semantics too, not only saving one.
	future := *definition.Tabs[0].Panels[0].Query
	if err := ValidateNativeArchiveQuery(future); err == nil || !strings.Contains(err.Error(), "query_version") {
		t.Fatalf("archive query with a future version = %v", err)
	}
	future.EvidenceMode = "archive"
	if err := ValidateNativeEvidenceQuery(future); err == nil || !strings.Contains(err.Error(), "query_version") {
		t.Fatalf("evidence query with a future version = %v", err)
	}
}

// An edit locks its project row: once the project is gone (here its row is
// removed under the definition), the edit is refused and records no revision
// a project deletion would leave behind.
func TestDashboardDefinitionEditOfAGoneProjectRecordsNoRevision(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/dashboard-gone-project.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveProject(&Project{ID: "gone-project", Name: "Gone"}); err != nil {
		t.Fatal(err)
	}
	definition := sampleDashboardDefinition("gone-project-dashboard")
	definition.ProjectID = "gone-project"
	if err := store.SaveDashboardDefinition(definition); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM projects WHERE id = ?`, "gone-project"); err != nil {
		t.Fatal(err)
	}
	definition.Name = "edited after the project went"
	if updated, err := store.UpdateDashboardDefinitionIfRevision(definition, 1); err != nil || updated {
		t.Fatalf("edit of a gone project = %v %v", updated, err)
	}
	var revisions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM dashboard_definition_revisions WHERE definition_id = ? AND revision > 1`, definition.ID).Scan(&revisions); err != nil || revisions != 0 {
		t.Fatalf("revisions after the refused edit = %d %v", revisions, err)
	}
}

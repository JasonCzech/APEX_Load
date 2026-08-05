package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestSQLiteDSNAddsPerformancePragmas(t *testing.T) {
	dsn := sqliteDSN("/tmp/apexload.db?mode=rwc&_pragma=busy_timeout(250)")

	if strings.Count(dsn, "_pragma=busy_timeout") != 1 {
		t.Fatalf("busy_timeout pragma count in %q = %d, want 1", dsn, strings.Count(dsn, "_pragma=busy_timeout"))
	}
	for _, want := range []string{"_pragma=foreign_keys(ON)", "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("sqliteDSN(%q) missing %s", dsn, want)
		}
	}
}

func TestSQLiteStoreUsesSingleWriterPool(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	if got := store.db.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestSQLiteStorePersistsInitialProvenanceOnce(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	record := &TestRecord{
		ID: "initial-provenance-run", Name: "Initial provenance", Status: "QUEUED",
		StartTime: time.Now().UTC(),
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest without provenance: %v", err)
	}
	saved, err := store.SaveTestInitialProvenance(record.ID, "another-project", map[string]any{"total_vusers": 99})
	if err != nil || saved {
		t.Fatalf("wrong-project SaveTestInitialProvenance = (%v, %v), want (false, nil)", saved, err)
	}
	saved, err = store.SaveTestInitialProvenance(record.ID, DefaultProjectID, map[string]any{"total_vusers": 3, "duration": "3m"})
	if err != nil || !saved {
		t.Fatalf("SaveTestInitialProvenance = (%v, %v), want (true, nil)", saved, err)
	}
	// Later full-row saves carry mutable current state but cannot replace the
	// immutable initial snapshot. The targeted write likewise remains write-once.
	record.InitialProvenance = map[string]any{"total_vusers": 9, "duration": "9m"}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest replacement provenance: %v", err)
	}
	saved, err = store.SaveTestInitialProvenance(record.ID, DefaultProjectID, map[string]any{"total_vusers": 12})
	if err != nil || !saved {
		t.Fatalf("second SaveTestInitialProvenance = (%v, %v), want matched row", saved, err)
	}

	stored, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	payload, err := json.Marshal(stored.InitialProvenance)
	if err != nil {
		t.Fatalf("marshal stored provenance: %v", err)
	}
	var provenance map[string]any
	if err := json.Unmarshal(payload, &provenance); err != nil {
		t.Fatalf("decode stored provenance: %v", err)
	}
	if got := int(provenance["total_vusers"].(float64)); got != 3 {
		t.Fatalf("stored total_vusers = %d, want immutable 3", got)
	}
}

func TestSQLiteStoreSaveTestPersistsBlankOptionalRelationsAsNull(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	var foreignKeysEnabled int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeysEnabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeysEnabled != 1 {
		t.Fatalf("foreign_keys pragma = %d, want 1", foreignKeysEnabled)
	}

	ptr := func(value string) *string { return &value }
	tests := []struct {
		name       string
		scenarioID string
		folderID   *string
		timeslotID *string
	}{
		{name: "nil pointers"},
		{name: "empty strings", folderID: ptr(""), timeslotID: ptr("")},
		{name: "whitespace strings", scenarioID: " \t", folderID: ptr("  "), timeslotID: ptr("\n")},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &TestRecord{
				ID:         fmt.Sprintf("test-blank-relations-%d", index),
				Name:       "Run without optional relations",
				Status:     "COMPLETE",
				ScenarioID: tt.scenarioID,
				FolderID:   tt.folderID,
				TimeslotID: tt.timeslotID,
				StartTime:  time.Now().UTC(),
			}
			if err := store.SaveTest(record); err != nil {
				t.Fatalf("SaveTest with blank optional relations returned error: %v", err)
			}

			var scenarioID, folderID, timeslotID sql.NullString
			if err := store.db.QueryRow(`SELECT scenario_id, folder_id, timeslot_id FROM tests WHERE id = ?`, record.ID).Scan(&scenarioID, &folderID, &timeslotID); err != nil {
				t.Fatalf("read raw optional relation values: %v", err)
			}
			if scenarioID.Valid || folderID.Valid || timeslotID.Valid {
				t.Fatalf("raw optional relations = scenario %#v folder %#v timeslot %#v, want SQL NULL values", scenarioID, folderID, timeslotID)
			}

			roundTrip, err := store.GetTest(record.ID)
			if err != nil {
				t.Fatalf("GetTest returned error: %v", err)
			}
			if roundTrip.ScenarioID != "" || roundTrip.FolderID != nil || roundTrip.TimeslotID != nil {
				t.Fatalf("round-trip optional relations = scenario %q folder %#v timeslot %#v, want empty/nil/nil", roundTrip.ScenarioID, roundTrip.FolderID, roundTrip.TimeslotID)
			}
			// Application, release, and project are not nullable run relations:
			// blank input deliberately resolves to seeded compatibility records.
			if roundTrip.ApplicationID != LegacyApplicationID || roundTrip.ReleaseID != LegacyReleaseID || roundTrip.ProjectID != DefaultProjectID {
				t.Fatalf("default context = application %q release %q project %q", roundTrip.ApplicationID, roundTrip.ReleaseID, roundTrip.ProjectID)
			}
		})
	}

	invalidRelations := []struct {
		name       string
		scenarioID string
		folderID   *string
	}{
		{name: "scenario", scenarioID: "missing-scenario"},
		{name: "folder", folderID: ptr("missing-folder")},
	}
	for index, tt := range invalidRelations {
		t.Run("reject unknown "+tt.name, func(t *testing.T) {
			invalid := &TestRecord{
				ID:         fmt.Sprintf("test-unknown-relation-%d", index),
				Name:       "Run with invalid relation",
				Status:     "COMPLETE",
				ScenarioID: tt.scenarioID,
				FolderID:   tt.folderID,
				StartTime:  time.Now().UTC(),
			}
			if err := store.SaveTest(invalid); err == nil {
				t.Fatalf("SaveTest with an unknown non-blank %s succeeded; want foreign-key enforcement", tt.name)
			}
		})
	}
}

func TestSQLiteStoreSaveScriptPreservesMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-1",
		Name:     "Imported Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
		DataSources: []dsl.DataSource{{
			Name:           "url",
			Type:           "dat",
			InlineData:     "URL\nhttps://csi-gw-ext.att.com:443\n",
			Strategy:       "sequential",
			FirstRowHeader: true,
		}},
		Parameters: []dsl.Parameter{{
			Name:       "url",
			Type:       "file",
			DataSource: "url",
			Column:     "URL",
			Update:     "each_iteration",
		}},
		Actions: []dsl.Action{{
			Name:   "Homepage",
			Type:   "GET",
			Target: "{{var.url}}/",
		}},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source:           "enterprise_script_archive",
				SourceScriptName: "Demo",
				Preserved: map[string]any{
					"runtime_files": map[string]any{
						"default.cfg": map[string]any{
							"HTTP": map[string]any{
								"ConnectTimeout": "45",
							},
						},
					},
				},
			},
		},
	}

	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	roundTrip, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.Metadata == nil || roundTrip.Metadata.Import == nil {
		t.Fatal("expected metadata to round-trip through sqlite")
	}
	payload, err := json.Marshal(roundTrip.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"enterprise_script_archive\"", "default.cfg", "\"ConnectTimeout\":\"45\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected metadata JSON to contain %q, got %s", want, text)
		}
	}
	if len(roundTrip.Parameters) != 1 || roundTrip.Parameters[0].Name != "url" || roundTrip.Parameters[0].Column != "URL" {
		t.Fatalf("expected parameters to round-trip through sqlite, got %#v", roundTrip.Parameters)
	}
	if len(roundTrip.DataSources) != 1 || !strings.Contains(roundTrip.DataSources[0].InlineData, "https://csi-gw-ext.att.com:443") {
		t.Fatalf("expected data source to round-trip through sqlite, got %#v", roundTrip.DataSources)
	}
	listed, err := store.ListScripts("", nil)
	if err != nil {
		t.Fatalf("ListScripts returned error: %v", err)
	}
	if len(listed) != 1 || len(listed[0].Parameters) != 1 || listed[0].Parameters[0].Name != "url" {
		t.Fatalf("expected listed script parameters to round-trip through sqlite, got %#v", listed)
	}
}

func TestSQLiteStoreAuthoringSessionsRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "project-1", Name: "Project 1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	session := &AuthoringSession{
		ID:        "session-1",
		ProjectID: "project-1",
		Source:    "enterprise_script_archive",
		Name:      "Checkout migration",
		Status:    "editing",
		Draft: &dsl.Script{
			Name:     "Checkout",
			Version:  "1.0.0",
			Protocol: "http",
			Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions:  []dsl.Action{{Name: "Home", Type: "request", Target: "/"}},
		},
		ReviewState: map[string]any{"active_tab": "runtime", "blockers": []any{"correlation"}},
		CreatedAt:   now.Add(-time.Hour),
	}
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatalf("SaveAuthoringSession returned error: %v", err)
	}

	roundTrip, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession returned error: %v", err)
	}
	if roundTrip.ProjectID != "project-1" || roundTrip.Source != "enterprise_script_archive" || roundTrip.Name != "Checkout migration" {
		t.Fatalf("roundTrip metadata = %#v", roundTrip)
	}
	if roundTrip.Draft == nil || roundTrip.Draft.Name != "Checkout" || roundTrip.Draft.Config.BaseURL != "https://example.test" {
		t.Fatalf("roundTrip draft = %#v", roundTrip.Draft)
	}
	if roundTrip.ReviewState["active_tab"] != "runtime" {
		t.Fatalf("review state = %#v, want active_tab runtime", roundTrip.ReviewState)
	}

	listed, err := store.ListAuthoringSessions("project-1")
	if err != nil {
		t.Fatalf("ListAuthoringSessions returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != session.ID {
		t.Fatalf("listed sessions = %#v, want session-1", listed)
	}
	if err := store.DeleteAuthoringSession(session.ID); err != nil {
		t.Fatalf("DeleteAuthoringSession returned error: %v", err)
	}
	if _, err := store.GetAuthoringSession(session.ID); err == nil {
		t.Fatal("expected deleted authoring session lookup to fail")
	}
}

func TestSQLiteStoreAuthoringSessionCompareAndSwapFencesStaleMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	replica, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(replica): %v", err)
	}
	defer replica.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "project-1", Name: "Project 1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	session := &AuthoringSession{
		ID: "authoring-cas", ProjectID: "project-1", Source: "test", Name: "Draft", Status: "ready",
		Draft:     &dsl.Script{Name: "Draft", Protocol: "http", ProjectID: "project-1"},
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatalf("SaveAuthoringSession: %v", err)
	}
	first, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession(first): %v", err)
	}
	stale, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession(stale): %v", err)
	}
	first.Status = "approving"
	first.Draft.ID = "approved-script"
	won, err := store.CompareAndSwapAuthoringSession(first, first.UpdatedAt, "ready")
	if err != nil || !won {
		t.Fatalf("approval CAS: won=%v err=%v", won, err)
	}
	stale.Name = "Stale edit"
	won, err = store.CompareAndSwapAuthoringSession(stale, stale.UpdatedAt, "ready")
	if err != nil {
		t.Fatalf("stale CAS: %v", err)
	}
	if won {
		t.Fatal("stale authoring edit replaced durable approval intent")
	}
	deleted, err := store.DeleteAuthoringSessionUnlessApproving(session.ID)
	if err != nil {
		t.Fatalf("DeleteAuthoringSessionUnlessApproving: %v", err)
	}
	if deleted {
		t.Fatal("approval intent was deleted while approving")
	}
	persisted, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession(persisted): %v", err)
	}
	if persisted.Status != "approving" || persisted.Draft.ID != "approved-script" {
		t.Fatalf("persisted approval intent = %#v", persisted)
	}
	expectedUpdatedAt := persisted.UpdatedAt
	persisted.Status = "approved"
	persisted.UpdatedAt = persisted.UpdatedAt.Add(time.Second)
	persisted.LastAccessedAt = persisted.UpdatedAt
	won, err = store.CompareAndSwapAuthoringSession(persisted, expectedUpdatedAt, "approving")
	if err != nil || !won {
		t.Fatalf("approval finalization CAS: won=%v err=%v", won, err)
	}
	forged, err := replica.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatalf("replica GetAuthoringSession(approved): %v", err)
	}
	forged.Name = "Cross-replica stale overwrite"
	if err := replica.SaveAuthoringSession(forged); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("cross-replica SaveAuthoringSession error = %v, want immutable approved evidence", err)
	}
	won, err = replica.CompareAndSwapAuthoringSession(forged, forged.UpdatedAt, "approved")
	if err != nil || won {
		t.Fatalf("cross-replica approved CAS: won=%v err=%v, want fenced", won, err)
	}
	if err := replica.DeleteAuthoringSession(session.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("DeleteAuthoringSession(approved) error = %v, want immutable approved evidence", err)
	}
	if current, err := store.GetAuthoringSession(session.ID); err != nil || current.Status != "approved" || current.Name == forged.Name {
		t.Fatalf("approved evidence after direct storage mutations = %#v err=%v", current, err)
	}
	deleted, err = replica.DeleteAuthoringSessionUnlessApproving(session.ID)
	if err != nil {
		t.Fatalf("DeleteAuthoringSessionUnlessApproving(approved): %v", err)
	}
	if deleted {
		t.Fatal("finalized approval evidence was deleted")
	}
}

func TestSQLiteStoreProjectsFoldersAndFiltering(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects returned error: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != DefaultProjectID {
		t.Fatalf("expected default project, got %#v", projects)
	}

	project := &Project{
		ID:   "project-1",
		Name: "Payments",
		Settings: ProjectSettings{
			RedactionRules:     []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
			MaxConcurrentTests: 2,
			MaxVUsers:          250,
		},
		CreatedAt: time.Now(),
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	roundTripProject, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject returned error: %v", err)
	}
	if len(roundTripProject.Settings.RedactionRules) != 1 || roundTripProject.Settings.RedactionRules[0].Pattern != `customer-[0-9]+` {
		t.Fatalf("expected persisted project settings, got %#v", roundTripProject.Settings)
	}
	if roundTripProject.Settings.MaxConcurrentTests != 2 {
		t.Fatalf("expected persisted project max concurrent tests, got %#v", roundTripProject.Settings)
	}
	if roundTripProject.Settings.MaxVUsers != 250 {
		t.Fatalf("expected persisted project max vusers, got %#v", roundTripProject.Settings)
	}
	folder := &Folder{ID: "folder-1", ProjectID: project.ID, Name: "Checkout", Category: "scripts", CreatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	script := &dsl.Script{
		ID:        "script-1",
		Name:      "Checkout API",
		Protocol:  "http",
		Version:   "1.0",
		ProjectID: project.ID,
		FolderID:  &folder.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Cart", Type: "request", Method: "GET", Target: "/cart"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	filtered, err := store.ListScripts(project.ID, &folder.ID)
	if err != nil {
		t.Fatalf("ListScripts returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ProjectID != project.ID || filtered[0].FolderID == nil || *filtered[0].FolderID != folder.ID {
		t.Fatalf("unexpected filtered scripts: %#v", filtered)
	}

	if err := store.DeleteFolder(folder.ID); err == nil {
		t.Fatal("expected non-empty folder delete to fail")
	}
	if err := store.MoveScript(script.ID, nil); err != nil {
		t.Fatalf("MoveScript returned error: %v", err)
	}
	if err := store.DeleteFolder(folder.ID); err != nil {
		t.Fatalf("DeleteFolder returned error after moving script: %v", err)
	}
}

func TestSQLiteStoreDomainRepositoryHierarchyAndProjectReleases(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	domains, err := store.ListDomains()
	if err != nil {
		t.Fatalf("ListDomains returned error: %v", err)
	}
	if len(domains) != 1 || domains[0].ID != DefaultDomainID {
		t.Fatalf("default domains = %#v, want default domain", domains)
	}
	repository, err := store.GetApplicationRepository(DefaultDomainID)
	if err != nil {
		t.Fatalf("GetApplicationRepository returned error: %v", err)
	}
	if repository.ID != DefaultApplicationRepositoryID {
		t.Fatalf("default repository = %#v, want %s", repository, DefaultApplicationRepositoryID)
	}
	categories, err := store.ListRepositoryCategories(DefaultDomainID)
	if err != nil {
		t.Fatalf("ListRepositoryCategories returned error: %v", err)
	}
	if len(categories) != 1 || categories[0].ID != DefaultRepositoryCategoryID {
		t.Fatalf("default categories = %#v, want uncategorized", categories)
	}

	project, err := store.GetProject(DefaultProjectID)
	if err != nil {
		t.Fatalf("GetProject default returned error: %v", err)
	}
	if project.DomainID != DefaultDomainID || project.CategoryID != DefaultRepositoryCategoryID {
		t.Fatalf("default project placement = %#v", project)
	}
	legacyRelease, err := store.GetRelease(LegacyReleaseID)
	if err != nil {
		t.Fatalf("GetRelease legacy returned error: %v", err)
	}
	if legacyRelease.ProjectID != DefaultProjectID {
		t.Fatalf("legacy release project_id = %q, want default project", legacyRelease.ProjectID)
	}

	now := time.Now()
	domain := &Domain{ID: "domain-payments", Name: "Payments Domain", CreatedAt: now}
	if err := store.SaveDomain(domain); err != nil {
		t.Fatalf("SaveDomain returned error: %v", err)
	}
	repo := &ApplicationRepository{ID: "repo-payments", DomainID: domain.ID, Name: "Payments Repository", CreatedAt: now}
	if err := store.SaveApplicationRepository(repo); err != nil {
		t.Fatalf("SaveApplicationRepository returned error: %v", err)
	}
	category := &RepositoryCategory{ID: "category-core", DomainID: domain.ID, RepositoryID: repo.ID, Name: "Core", SortOrder: 1, CreatedAt: now}
	if err := store.SaveRepositoryCategory(category); err != nil {
		t.Fatalf("SaveRepositoryCategory returned error: %v", err)
	}
	project = &Project{ID: "project-payments", DomainID: domain.ID, CategoryID: category.ID, Name: "Payments", CreatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	app := &Application{ID: "app-payments", ProjectID: project.ID, Name: "Payments App", CreatedAt: now}
	if err := store.SaveApplication(app); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &Release{ID: "release-payments", ApplicationID: app.ID, Name: "2026.05", CreatedAt: now}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}
	releases, err := store.ListReleasesByProject(project.ID, false)
	if err != nil {
		t.Fatalf("ListReleasesByProject returned error: %v", err)
	}
	if len(releases) != 1 || releases[0].ProjectID != project.ID {
		t.Fatalf("project releases = %#v, want release scoped to project", releases)
	}
}

func TestSQLiteStoreServiceAccountsAndAuditEvents(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	sum := sha256.Sum256([]byte("apexload-key"))
	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(24 * time.Hour)
	keyRotatedAt := now.Add(-24 * time.Hour)
	if err := store.SaveProject(&Project{ID: "project-a", DomainID: DefaultDomainID, Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	account := &ServiceAccount{
		ID:          "svc-1",
		Name:        "CI Pipeline",
		Description: "Runs nightly tests",
		KeyHash:     fmt.Sprintf("%x", sum[:]),
		Active:      true,
		GlobalRoles: []string{"system_admin"},
		ProjectRoles: map[string][]string{
			"project-a": {"editor"},
		},
		CreatedAt:            now,
		UpdatedAt:            now,
		LastUsedAt:           &now,
		ExpiresAt:            &expiresAt,
		KeyRotatedAt:         &keyRotatedAt,
		RotationIntervalDays: 30,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatalf("SaveServiceAccount returned error: %v", err)
	}

	byID, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatalf("GetServiceAccount returned error: %v", err)
	}
	if byID.Name != account.Name || byID.KeyHash != account.KeyHash {
		t.Fatalf("service account round trip = %#v, want name/hash preserved", byID)
	}
	if !byID.Active {
		t.Fatalf("expected service account to be active, got %#v", byID)
	}
	if got := byID.ProjectRoles["project-a"]; len(got) != 1 || got[0] != "editor" {
		t.Fatalf("project roles = %#v, want editor for project-a", byID.ProjectRoles)
	}
	if byID.ExpiresAt == nil || !byID.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %#v, want %v", byID.ExpiresAt, expiresAt)
	}
	wantRotationDue := keyRotatedAt.AddDate(0, 0, 30)
	if byID.KeyRotatedAt == nil || !byID.KeyRotatedAt.Equal(keyRotatedAt) || byID.RotationDueAt == nil || !byID.RotationDueAt.Equal(wantRotationDue) {
		t.Fatalf("rotation metadata = key_rotated_at %#v due %#v, want %v/%v", byID.KeyRotatedAt, byID.RotationDueAt, keyRotatedAt, wantRotationDue)
	}

	byHash, err := store.GetServiceAccountByKeyHash(account.KeyHash)
	if err != nil {
		t.Fatalf("GetServiceAccountByKeyHash returned error: %v", err)
	}
	if byHash.ID != account.ID {
		t.Fatalf("service account by hash = %#v, want id %q", byHash, account.ID)
	}

	accounts, err := store.ListServiceAccounts()
	if err != nil {
		t.Fatalf("ListServiceAccounts returned error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != account.ID {
		t.Fatalf("service account list = %#v, want one account", accounts)
	}

	rotatedSum := sha256.Sum256([]byte("apexload-key-rotated"))
	if err := store.RotateServiceAccountKey(account.ID, fmt.Sprintf("%x", rotatedSum[:]), now.Add(time.Minute)); err != nil {
		t.Fatalf("RotateServiceAccountKey returned error: %v", err)
	}
	if err := store.UpdateServiceAccountActive(account.ID, false, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("UpdateServiceAccountActive returned error: %v", err)
	}
	inactiveAccount, err := store.GetServiceAccountByKeyHash(fmt.Sprintf("%x", rotatedSum[:]))
	if err != nil {
		t.Fatalf("GetServiceAccountByKeyHash inactive returned error: %v", err)
	}
	if inactiveAccount.Active {
		t.Fatalf("expected inactive service account lookup to preserve active=false, got %#v", inactiveAccount)
	}
	if err := store.UpdateServiceAccountActive(account.ID, true, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("UpdateServiceAccountActive returned error: %v", err)
	}
	lastUsedAt := now.Add(4 * time.Minute)
	if err := store.UpdateServiceAccountLastUsed(account.ID, lastUsedAt); err != nil {
		t.Fatalf("UpdateServiceAccountLastUsed returned error: %v", err)
	}
	rotatedAccount, err := store.GetServiceAccountByKeyHash(fmt.Sprintf("%x", rotatedSum[:]))
	if err != nil {
		t.Fatalf("GetServiceAccountByKeyHash after rotate returned error: %v", err)
	}
	if rotatedAccount.LastUsedAt == nil || !rotatedAccount.LastUsedAt.Equal(lastUsedAt) {
		t.Fatalf("last used at = %#v, want %v", rotatedAccount.LastUsedAt, lastUsedAt)
	}
	wantRotatedAt := now.Add(time.Minute)
	wantRotatedDue := wantRotatedAt.AddDate(0, 0, 30)
	if rotatedAccount.KeyRotatedAt == nil || !rotatedAccount.KeyRotatedAt.Equal(wantRotatedAt) || rotatedAccount.RotationDueAt == nil || !rotatedAccount.RotationDueAt.Equal(wantRotatedDue) {
		t.Fatalf("rotated metadata = key_rotated_at %#v due %#v, want %v/%v", rotatedAccount.KeyRotatedAt, rotatedAccount.RotationDueAt, wantRotatedAt, wantRotatedDue)
	}

	expiredHash := sha256.Sum256([]byte("expired-key"))
	expiredAt := now.Add(-time.Minute)
	if err := store.SaveServiceAccount(&ServiceAccount{
		ID:                   "svc-expired",
		Name:                 "Expired",
		KeyHash:              fmt.Sprintf("%x", expiredHash[:]),
		Active:               true,
		GlobalRoles:          []string{"viewer"},
		ProjectRoles:         map[string][]string{},
		CreatedAt:            now,
		UpdatedAt:            now,
		ExpiresAt:            &expiredAt,
		KeyRotatedAt:         &now,
		RotationIntervalDays: 30,
	}); err != nil {
		t.Fatalf("SaveServiceAccount expired returned error: %v", err)
	}
	expiredAccount, err := store.GetServiceAccountByKeyHash(fmt.Sprintf("%x", expiredHash[:]))
	if err != nil {
		t.Fatalf("GetServiceAccountByKeyHash expired returned error: %v", err)
	}
	if expiredAccount.ExpiresAt == nil || !expiredAccount.ExpiresAt.Equal(expiredAt) {
		t.Fatalf("expected expired service account lookup to preserve expires_at, got %#v", expiredAccount)
	}

	event := &AuditEvent{
		ID:               "audit-1",
		Action:           "system.settings.update",
		ActorType:        "service_account",
		ActorSubject:     "service-account:svc-1",
		ActorDisplayName: "CI Pipeline",
		ProjectID:        "project-a",
		ResourceType:     "system_settings",
		ResourceID:       "global",
		Status:           "success",
		Details:          map[string]any{"mode": "trusted_headers"},
		CreatedAt:        now,
	}
	if err := store.SaveAuditEvent(event); err != nil {
		t.Fatalf("SaveAuditEvent returned error: %v", err)
	}
	if err := store.SaveAuditEvent(&AuditEvent{
		ID:           "audit-2",
		Action:       "project.update",
		ActorType:    "user",
		ActorSubject: "alice",
		ProjectID:    "project-b",
		ResourceType: "project",
		ResourceID:   "project-b",
		Status:       "success",
		CreatedAt:    now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveAuditEvent returned error: %v", err)
	}

	events, err := store.ListAuditEvents(AuditEventFilter{Limit: 10, ProjectID: "project-a", ActorType: "service_account"})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Action != event.Action {
		t.Fatalf("audit events = %#v, want one saved event", events)
	}
	if got := events[0].Details["mode"]; got != "trusted_headers" {
		t.Fatalf("audit details = %#v, want mode trusted_headers", events[0].Details)
	}
	secondPage, err := store.ListAuditEvents(AuditEventFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ListAuditEvents second page returned error: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != "audit-1" {
		t.Fatalf("audit event page 2 = %#v, want audit-1", secondPage)
	}
}

func TestSQLiteSchemaIncludesAuditFilterIndexes(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	rows, err := store.db.Query(`PRAGMA index_list('audit_events')`)
	if err != nil {
		t.Fatalf("PRAGMA index_list returned error: %v", err)
	}
	defer rows.Close()

	found := map[string]bool{}
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index row: %v", err)
		}
		found[name] = true
	}
	for _, name := range []string{
		"idx_audit_events_action_status_created",
		"idx_audit_events_status_created",
		"idx_audit_events_resource_created",
	} {
		if !found[name] {
			t.Fatalf("missing audit index %s in %#v", name, found)
		}
	}
}

func TestSQLiteStoreUsersRoleBindingsAndAuditFilters(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveDomain(&Domain{ID: "domain-a", Name: "Domain A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveDomain returned error: %v", err)
	}
	if err := store.SaveProject(&Project{ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	user := &User{
		ID:          "user-1",
		Subject:     "oidc|alice",
		Email:       "alice@example.com",
		DisplayName: "Alice Admin",
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser returned error: %v", err)
	}
	user.SessionEpoch = 3
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser with session epoch returned error: %v", err)
	}
	epochUser, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser after session epoch save returned error: %v", err)
	}
	if epochUser.SessionEpoch != 3 {
		t.Fatalf("session epoch = %d, want 3", epochUser.SessionEpoch)
	}
	if err := store.SaveUserRoleBinding(&UserRoleBinding{
		ID:        "binding-1",
		UserID:    user.ID,
		ScopeType: "project",
		ScopeID:   "project-a",
		Role:      "project_admin",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUserRoleBinding returned error: %v", err)
	}
	if err := store.SaveUserRoleBinding(&UserRoleBinding{
		ID:        "binding-duplicate",
		UserID:    user.ID,
		ScopeType: "project",
		ScopeID:   "project-a",
		Role:      "project_admin",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveUserRoleBinding duplicate returned error: %v", err)
	}

	roundTrip, err := store.GetUserBySubject("oidc|alice")
	if err != nil {
		t.Fatalf("GetUserBySubject returned error: %v", err)
	}
	if roundTrip.Email != user.Email || len(roundTrip.Bindings) != 1 || roundTrip.Bindings[0].Role != "project_admin" {
		t.Fatalf("round-trip user = %#v, want one project_admin binding", roundTrip)
	}

	filtered, err := store.ListUsers(UserFilter{Query: "ALICE", Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != user.ID {
		t.Fatalf("filtered users = %#v, want alice", filtered)
	}
	roleFiltered, err := store.ListUsers(UserFilter{Role: "PROJECT_ADMIN", Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers role returned error: %v", err)
	}
	if len(roleFiltered) != 1 || roleFiltered[0].ID != user.ID {
		t.Fatalf("role filtered users = %#v, want alice", roleFiltered)
	}
	projectFiltered, err := store.ListUsers(UserFilter{ProjectID: "project-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers project returned error: %v", err)
	}
	if len(projectFiltered) != 1 || projectFiltered[0].ID != user.ID {
		t.Fatalf("project filtered users = %#v, want alice", projectFiltered)
	}
	if count, err := store.CountUsers(UserFilter{ProjectID: "missing-project"}); err != nil || count != 0 {
		t.Fatalf("missing project count = %d, %v, want 0", count, err)
	}
	domainFiltered, err := store.ListUsers(UserFilter{DomainID: DefaultDomainID, Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers project-derived domain returned error: %v", err)
	}
	if len(domainFiltered) != 1 || domainFiltered[0].ID != user.ID {
		t.Fatalf("project-derived domain filtered users = %#v, want alice", domainFiltered)
	}
	if err := store.SaveUserRoleBinding(&UserRoleBinding{
		ID: "binding-domain", UserID: user.ID, ScopeType: "domain", ScopeID: "domain-a", Role: "viewer", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUserRoleBinding domain returned error: %v", err)
	}
	domainFiltered, err = store.ListUsers(UserFilter{DomainID: "domain-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers domain returned error: %v", err)
	}
	if len(domainFiltered) != 1 || domainFiltered[0].ID != user.ID {
		t.Fatalf("domain filtered users = %#v, want alice", domainFiltered)
	}
	scopeFiltered, err := store.ListUsers(UserFilter{Query: "project-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers scope query returned error: %v", err)
	}
	if len(scopeFiltered) != 1 || scopeFiltered[0].ID != user.ID {
		t.Fatalf("scope filtered users = %#v, want alice", scopeFiltered)
	}
	roleCount, err := store.CountUsers(UserFilter{Role: "project_admin"})
	if err != nil {
		t.Fatalf("CountUsers role returned error: %v", err)
	}
	if roleCount != 1 {
		t.Fatalf("role count = %d, want 1", roleCount)
	}

	disabledAt := now.Add(2 * time.Minute)
	if err := store.UpdateUserActive(user.ID, false, disabledAt); err != nil {
		t.Fatalf("UpdateUserActive disable returned error: %v", err)
	}
	active := false
	inactiveUsers, err := store.ListUsers(UserFilter{Active: &active, Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers inactive returned error: %v", err)
	}
	if len(inactiveUsers) != 1 || inactiveUsers[0].DisabledAt == nil {
		t.Fatalf("inactive users = %#v, want disabled alice", inactiveUsers)
	}
	if err := store.UpdateUserActive(user.ID, true, disabledAt.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateUserActive enable returned error: %v", err)
	}
	enabled, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser enabled returned error: %v", err)
	}
	if !enabled.Active || enabled.DisabledAt != nil {
		t.Fatalf("enabled user = %#v, want active without disabled_at", enabled)
	}

	if err := store.SaveAuditEvent(&AuditEvent{
		ID:           "audit-user-1",
		Action:       "auth.user.create",
		ActorType:    "user",
		ActorSubject: "oidc|alice",
		DomainID:     "domain-a",
		ProjectID:    "project-a",
		ResourceType: "user",
		ResourceID:   user.ID,
		Status:       "success",
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("SaveAuditEvent user create returned error: %v", err)
	}
	if err := store.SaveAuditEvent(&AuditEvent{
		ID:           "audit-user-2",
		Action:       "auth.user.disable",
		ActorType:    "user",
		ActorSubject: "oidc|bob",
		DomainID:     "domain-b",
		ProjectID:    "project-b",
		ResourceType: "user",
		ResourceID:   "user-2",
		Status:       "success",
		CreatedAt:    now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAuditEvent user disable returned error: %v", err)
	}

	createdBefore := now.Add(time.Minute)
	events, err := store.ListAuditEvents(AuditEventFilter{
		ActorSubject:  "oidc|alice",
		DomainID:      "domain-a",
		CreatedBefore: &createdBefore,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents user filters returned error: %v", err)
	}
	if len(events) != 1 || events[0].ID != "audit-user-1" || events[0].DomainID != "domain-a" {
		t.Fatalf("filtered audit events = %#v, want audit-user-1", events)
	}
}

func TestSQLiteStorePersistsPendingSince(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	pendingSince := time.Now().UTC().Truncate(time.Second)
	record := &TestRecord{
		ID:           "test-pending",
		Name:         "Queued Checkout",
		Status:       "PENDING",
		ProjectID:    DefaultProjectID,
		TotalVUsers:  25,
		Duration:     int64(30 * time.Second),
		StartTime:    time.Now().UTC(),
		PendingSince: &pendingSince,
		Groups: []map[string]any{{
			"name": "Checkout",
		}},
	}

	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	roundTrip, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest returned error: %v", err)
	}
	if roundTrip.PendingSince == nil {
		t.Fatal("expected pending_since to round-trip through sqlite")
	}
	if !roundTrip.PendingSince.Equal(pendingSince) {
		t.Fatalf("pending_since = %v, want %v", roundTrip.PendingSince, pendingSince)
	}
}

func TestSQLiteStorePersistsScheduledStartAt(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	scheduledAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	startedAt := scheduledAt.Add(17 * time.Second)
	record := &TestRecord{
		ID:               "test-scheduled",
		Name:             "Scheduled Checkout",
		Status:           "SCHEDULED",
		ProjectID:        DefaultProjectID,
		TotalVUsers:      25,
		Duration:         int64(30 * time.Second),
		StartTime:        time.Now().UTC(),
		ScheduledStartAt: &scheduledAt,
		StartedAt:        &startedAt,
		Groups: []map[string]any{{
			"name": "Checkout",
		}},
	}

	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	roundTrip, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest returned error: %v", err)
	}
	if roundTrip.ScheduledStartAt == nil || !roundTrip.ScheduledStartAt.Equal(scheduledAt) {
		t.Fatalf("scheduled_start_at = %v, want %v", roundTrip.ScheduledStartAt, scheduledAt)
	}
	if roundTrip.StartedAt == nil || !roundTrip.StartedAt.Equal(startedAt) {
		t.Fatalf("started_at = %v, want %v", roundTrip.StartedAt, startedAt)
	}
}

func TestSQLiteStorePersistsTimeslotID(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	slot := "timeslot-77"
	record := &TestRecord{
		ID:          "test-bound",
		Name:        "Bound Checkout",
		Status:      "PENDING",
		ProjectID:   DefaultProjectID,
		TotalVUsers: 25,
		Duration:    int64(30 * time.Second),
		StartTime:   time.Now().UTC(),
		TimeslotID:  &slot,
		Groups:      []map[string]any{{"name": "Checkout"}},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	roundTrip, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest returned error: %v", err)
	}
	if roundTrip.TimeslotID == nil || *roundTrip.TimeslotID != slot {
		t.Fatalf("timeslot_id = %v, want %q", roundTrip.TimeslotID, slot)
	}
}

func TestSQLiteStorePersistsExecutionPlanApprovalSnapshot(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	record := &TestRecord{
		ID:          "test-exec-plan-approval",
		Name:        "Approved Plan Entry",
		Status:      "QUEUED",
		ProjectID:   DefaultProjectID,
		TotalVUsers: 25,
		Duration:    int64(30 * time.Second),
		StartTime:   time.Now().UTC(),
		Groups:      []map[string]any{{"name": "Checkout"}},
		ExecutionPlanApproval: map[string]any{
			"id":              "approval-1",
			"plan_id":         "plan-1",
			"policy":          "quota_burst",
			"status":          "approved",
			"requested_by":    "user:requester",
			"resolved_by":     "user:approver",
			"decision_reason": "release gate approved",
			"required_role":   "project-admin",
		},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	roundTrip, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest returned error: %v", err)
	}
	approval, ok := roundTrip.ExecutionPlanApproval.(map[string]any)
	if !ok {
		t.Fatalf("execution approval = %#v, want JSON map", roundTrip.ExecutionPlanApproval)
	}
	if approval["plan_id"] != "plan-1" || approval["required_role"] != "project-admin" || approval["decision_reason"] != "release gate approved" {
		t.Fatalf("execution approval = %#v, want approval snapshot", approval)
	}
}

func TestSQLiteStorePersistsBaselinePin(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	pinnedAt := time.Now().UTC().Truncate(time.Second)
	record := &TestRecord{
		ID:               "test-baseline-pin",
		Name:             "Checkout Baseline",
		Status:           "COMPLETE",
		ProjectID:        DefaultProjectID,
		TotalVUsers:      25,
		Duration:         int64(30 * time.Second),
		StartTime:        time.Now().UTC(),
		BaselinePinned:   true,
		BaselinePinnedAt: &pinnedAt,
		Groups: []map[string]any{{
			"name": "Checkout",
		}},
	}

	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	roundTrip, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest returned error: %v", err)
	}
	if !roundTrip.BaselinePinned {
		t.Fatal("expected baseline_pinned to round-trip through sqlite")
	}
	if roundTrip.BaselinePinnedAt == nil || !roundTrip.BaselinePinnedAt.Equal(pinnedAt) {
		t.Fatalf("baseline_pinned_at = %v, want %v", roundTrip.BaselinePinnedAt, pinnedAt)
	}
}

func TestSQLiteStoreBackfillsDefaultProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	script := &dsl.Script{
		ID:       "legacy-script",
		Name:     "Legacy",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	store.Close()

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore returned error: %v", err)
	}
	defer reopened.Close()
	roundTrip, err := reopened.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.ProjectID != DefaultProjectID {
		t.Fatalf("project id = %q, want %q", roundTrip.ProjectID, DefaultProjectID)
	}
	application := &Application{ID: "legacy-application", Name: "Legacy App", CreatedAt: time.Now()}
	if err := reopened.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	applicationRoundTrip, err := reopened.GetApplication(application.ID)
	if err != nil {
		t.Fatalf("GetApplication returned error: %v", err)
	}
	if applicationRoundTrip.ProjectID != DefaultProjectID {
		t.Fatalf("application project id = %q, want %q", applicationRoundTrip.ProjectID, DefaultProjectID)
	}
	pool := &SharedDataPool{ID: "legacy-pool", Name: "Legacy Pool", CreatedAt: time.Now()}
	if err := reopened.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	poolRoundTrip, err := reopened.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool returned error: %v", err)
	}
	if poolRoundTrip.ProjectID != DefaultProjectID {
		t.Fatalf("data pool project id = %q, want %q", poolRoundTrip.ProjectID, DefaultProjectID)
	}
}

func TestSQLiteStoreApplicationsReleasesScenariosAndContext(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	app := &Application{ID: "app-1", ProjectID: DefaultProjectID, Name: "Payments", CreatedAt: time.Now()}
	if err := store.SaveApplication(app); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	if app.ProjectID != DefaultProjectID {
		t.Fatalf("saved application project id = %q, want %q", app.ProjectID, DefaultProjectID)
	}
	release := &Release{ID: "rel-1", ApplicationID: app.ID, Name: "2026.05", CreatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	script := &dsl.Script{
		ID:       "script-context",
		RefKey:   "LOC-SCR-0001-v1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Checkout", Type: "request", Method: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	roundScript, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundScript.RefKey != script.RefKey {
		t.Fatalf("script ref key = %q, want %q", roundScript.RefKey, script.RefKey)
	}

	scenario := &Scenario{
		ID:           "scenario-1",
		RefKey:       "LOC-SCN-0001",
		Name:         "Checkout Smoke",
		Duration:     "2m",
		ScenarioMode: "vuser_driven",
		CreatedAt:    time.Now(),
	}
	groups := []ScenarioScript{{
		Order:          0,
		ScriptID:       script.ID,
		Role:           "observer",
		VUsers:         25,
		Initialization: "at_runtime",
		Stages: []LoadStage{
			{Name: "Warmup", Type: "ramp_up", Duration: "30s", TargetVUsers: 25},
			{Name: "Hold", Type: "hold", Duration: "2m", TargetVUsers: 25},
		},
	}}
	if err := store.SaveScenario(scenario, groups); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}
	roundTrip, scripts, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario returned error: %v", err)
	}
	if roundTrip.Name != scenario.Name || len(scripts) != 1 || scripts[0].ScriptID != script.ID {
		t.Fatalf("unexpected scenario round trip: %#v scripts=%#v", roundTrip, scripts)
	}
	if roundTrip.RefKey != scenario.RefKey {
		t.Fatalf("scenario ref key = %q, want %q", roundTrip.RefKey, scenario.RefKey)
	}
	if len(scripts[0].Stages) != 2 || scripts[0].Stages[0].Type != "ramp_up" || scripts[0].Stages[0].TargetVUsers != 25 {
		t.Fatalf("scenario stages = %#v, want persisted staged load model", scripts[0].Stages)
	}
	if scripts[0].Role != "observer" {
		t.Fatalf("scenario script role = %q, want observer round-tripped", scripts[0].Role)
	}

	record := &TestRecord{
		ID:            "test-1",
		RefKey:        "LOC-RUN-20260603-001",
		Name:          scenario.Name,
		Status:        "COMPLETE",
		ApplicationID: app.ID,
		ReleaseID:     release.ID,
		ScenarioID:    scenario.ID,
		TotalVUsers:   25,
		Duration:      int64(2 * time.Minute),
		StartTime:     time.Now(),
		Groups:        []map[string]any{{"script_id": script.ID, "vusers": 25}},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	roundTest, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest returned error: %v", err)
	}
	if roundTest.RefKey != record.RefKey {
		t.Fatalf("test ref key = %q, want %q", roundTest.RefKey, record.RefKey)
	}
	archive := &RunArchiveRecord{
		TestID:           record.ID,
		RefKey:           record.RefKey,
		SchemaVersion:    archiveRecordSchemaVersion,
		ArchivePath:      "/tmp/apexload/archive",
		ManifestPath:     "/tmp/apexload/archive/manifest.json",
		ManifestSHA256:   "abc123",
		TransactionCount: 10,
		TrendPointJSON:   `{"run_id":"compact"}`,
		CreatedAt:        time.Now(),
		CompletedAt:      time.Now(),
	}
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}
	roundArchive, err := store.GetRunArchive(record.ID)
	if err != nil {
		t.Fatalf("GetRunArchive returned error: %v", err)
	}
	if roundArchive.RefKey != archive.RefKey {
		t.Fatalf("archive ref key = %q, want %q", roundArchive.RefKey, archive.RefKey)
	}
	if roundArchive.ManifestSHA256 != archive.ManifestSHA256 {
		t.Fatalf("archive manifest hash = %q, want %q", roundArchive.ManifestSHA256, archive.ManifestSHA256)
	}
	if roundArchive.TrendPointJSON != archive.TrendPointJSON {
		t.Fatalf("archive trend point = %q, want %q", roundArchive.TrendPointJSON, archive.TrendPointJSON)
	}
	filtered, err := store.ListTestsByContext(app.ID, release.ID, scenario.ID)
	if err != nil {
		t.Fatalf("ListTestsByContext returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ReleaseID != release.ID || filtered[0].ScenarioID != scenario.ID || filtered[0].RefKey != record.RefKey {
		t.Fatalf("unexpected filtered tests: %#v", filtered)
	}

	if err := store.DeleteRelease(release.ID); err == nil {
		t.Fatal("expected DeleteRelease to fail while results exist")
	}
	if err := store.ArchiveRelease(release.ID); err == nil {
		t.Fatal("expected ArchiveRelease to fail while results exist")
	}
}

func TestSQLiteStoreSaveHistoryUsesTypedIntervalSummaries(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.SaveTest(&TestRecord{ID: "test-1", Name: "History", Status: "RUNNING", StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	timestamp := time.Date(2026, time.May, 13, 17, 0, 0, 0, time.UTC)
	metrics := map[string]any{
		"timestamp":          timestamp,
		"active_vusers":      12,
		"tps":                48.5,
		"avg_response_ms":    220.5,
		"error_pct":          1.25,
		"total_transactions": 120,
		"total_errors":       2,
		"p50_ms":             150.0,
		"p90_ms":             300.0,
		"p95_ms":             350.0,
		"p99_ms":             420.0,
		"browser": map[string]any{
			"samples":                2,
			"avg_fcp_ms":             120.0,
			"avg_lcp_ms":             240.0,
			"avg_cls_score":          0.031,
			"avg_inp_ms":             55.0,
			"avg_blocking_resources": 2.0,
			"avg_long_task_count":    1.5,
			"avg_js_heap_used_mb":    18.0,
		},
		"browser_by_device": map[string]any{
			"iphone_se": map[string]any{
				"device_profile":         "iphone_se",
				"device_tier":            "low",
				"samples":                2,
				"avg_fcp_ms":             120.0,
				"avg_lcp_ms":             240.0,
				"avg_cls_score":          0.031,
				"avg_inp_ms":             55.0,
				"avg_blocking_resources": 2.0,
				"avg_long_task_count":    1.5,
				"avg_js_heap_used_mb":    18.0,
			},
		},
		"latency": map[string]any{
			"targets": []map[string]any{{
				"name":             "edge",
				"endpoint":         "https://example.test",
				"latency_ms":       42.5,
				"probe_total_ms":   51.25,
				"dns_ms":           2.5,
				"connect_ms":       12.25,
				"tls_ms":           8.5,
				"ttfb_ms":          45.75,
				"http_status":      204,
				"probe_method":     "HEAD",
				"ip":               "192.0.2.10",
				"hops":             3,
				"last_success":     true,
				"jitter_ms":        1.75,
				"success_rate_pct": 91.666,
				"sample_count":     12,
				"route_status":     "ok",
				"error":            "previous HTTP probe failed",
				"timestamp":        timestamp.Add(-100 * time.Millisecond),
			}},
		},
		"telemetry": map[string]any{
			"dropped_results":             3,
			"websocket_clients":           2,
			"websocket_dropped_messages":  4,
			"websocket_dropped_events":    1,
			"generator_published_at":      timestamp.Add(-400 * time.Millisecond),
			"controller_received_at":      timestamp.Add(-200 * time.Millisecond),
			"collected_at":                timestamp.Add(-time.Second),
			"published_at":                timestamp,
			"generator_processing_lag_ms": 600.0,
			"transport_lag_ms":            200.0,
			"controller_fanout_lag_ms":    200.0,
			"sample_age_ms":               1000.0,
		},
	}

	if err := store.SaveHistory("test-1", timestamp, metrics); err != nil {
		t.Fatalf("SaveHistory returned error: %v", err)
	}

	var typedCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_interval_summaries WHERE test_id = ?`, "test-1").Scan(&typedCount); err != nil {
		t.Fatalf("count typed summaries: %v", err)
	}
	if typedCount != 1 {
		t.Fatalf("typed summary count = %d, want 1", typedCount)
	}

	var legacyCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_history WHERE test_id = ?`, "test-1").Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy history: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("legacy history count = %d, want 0", legacyCount)
	}

	history, err := store.GetHistory("test-1")
	if err != nil {
		t.Fatalf("GetHistory returned error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	record, ok := history[0].(IntervalSummaryRecord)
	if !ok {
		t.Fatalf("expected typed interval summary, got %#v", history[0])
	}
	if record.ActiveVUsers != 12 || record.TotalTx != 120 || record.DroppedResults != 3 || record.WebsocketDroppedMessages != 4 {
		t.Fatalf("unexpected typed interval summary: %#v", record)
	}
	if record.Browser == nil || record.Browser.Samples != 2 || record.Browser.AvgCLSScore != 0.031 {
		t.Fatalf("unexpected browser summary: %#v", record.Browser)
	}
	if record.BrowserByDevice == nil || record.BrowserByDevice["iphone_se"] == nil || record.BrowserByDevice["iphone_se"].DeviceTier != "low" {
		t.Fatalf("unexpected browser by device summary: %#v", record.BrowserByDevice)
	}
	if record.Latency == nil || len(record.Latency.Targets) != 1 || record.Latency.Targets[0].LatencyMs != 42.5 {
		t.Fatalf("unexpected latency summary: %#v", record.Latency)
	}
	latencyTarget := record.Latency.Targets[0]
	if latencyTarget.ProbeTotalMs != 51.25 || latencyTarget.DNSMs != 2.5 || latencyTarget.ConnectMs != 12.25 || latencyTarget.TLSMs != 8.5 || latencyTarget.TTFBMs != 45.75 {
		t.Fatalf("unexpected route timing fields: %#v", latencyTarget)
	}
	if latencyTarget.HTTPStatus != 204 || latencyTarget.ProbeMethod != "HEAD" || latencyTarget.Hops != 3 || latencyTarget.RouteStatus != "ok" {
		t.Fatalf("unexpected route metadata fields: %#v", latencyTarget)
	}
	if latencyTarget.JitterMs != 1.75 || latencyTarget.SuccessRatePct != 91.666 || latencyTarget.SampleCount != 12 || latencyTarget.Error != "previous HTTP probe failed" {
		t.Fatalf("unexpected route stability fields: %#v", latencyTarget)
	}
	if !record.GeneratorPublishedAt.Equal(timestamp.Add(-400 * time.Millisecond)) {
		t.Fatalf("generator_published_at = %s, want %s", record.GeneratorPublishedAt, timestamp.Add(-400*time.Millisecond))
	}
	if !record.ControllerReceivedAt.Equal(timestamp.Add(-200 * time.Millisecond)) {
		t.Fatalf("controller_received_at = %s, want %s", record.ControllerReceivedAt, timestamp.Add(-200*time.Millisecond))
	}
	if record.GeneratorProcessingLagMs != 600 || record.TransportLagMs != 200 || record.ControllerFanoutLagMs != 200 {
		t.Fatalf("unexpected lag breakdown values: %#v", record)
	}
	if record.SchemaVersion != intervalSummarySchemaVersion {
		t.Fatalf("schema version = %q, want %q", record.SchemaVersion, intervalSummarySchemaVersion)
	}

	var storedSchemaVersion string
	if err := store.db.QueryRow(`SELECT schema_version FROM test_interval_summaries WHERE test_id = ?`, "test-1").Scan(&storedSchemaVersion); err != nil {
		t.Fatalf("select typed schema version: %v", err)
	}
	if storedSchemaVersion != intervalSummarySchemaVersion {
		t.Fatalf("stored schema version = %q, want %q", storedSchemaVersion, intervalSummarySchemaVersion)
	}
}

func TestSQLiteStoreGetHistoryLimitReturnsNewestWindowAscending(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.SaveTest(&TestRecord{ID: "test-window", Name: "Window", Status: "RUNNING", StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, time.May, 13, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		timestamp := base.Add(time.Duration(i) * time.Second)
		metrics := map[string]any{
			"timestamp":          timestamp,
			"active_vusers":      i + 1,
			"tps":                float64(i + 1),
			"avg_response_ms":    100.0,
			"error_pct":          0.0,
			"total_transactions": i + 1,
			"total_errors":       0,
		}
		if err := store.SaveHistory("test-window", timestamp, metrics); err != nil {
			t.Fatalf("SaveHistory(%d) returned error: %v", i, err)
		}
	}

	history, err := store.GetHistoryLimit("test-window", 2)
	if err != nil {
		t.Fatalf("GetHistoryLimit returned error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	first, ok := history[0].(IntervalSummaryRecord)
	if !ok {
		t.Fatalf("first history entry = %#v, want IntervalSummaryRecord", history[0])
	}
	second, ok := history[1].(IntervalSummaryRecord)
	if !ok {
		t.Fatalf("second history entry = %#v, want IntervalSummaryRecord", history[1])
	}
	if !first.Timestamp.Equal(base.Add(3*time.Second)) || !second.Timestamp.Equal(base.Add(4*time.Second)) {
		t.Fatalf("window timestamps = %s, %s; want newest two ascending", first.Timestamp, second.Timestamp)
	}
}

func TestSQLiteStoreMigratesTelemetrySchemaVersionsForExistingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}

	statements := []string{
		`CREATE TABLE applications (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE releases (id TEXT PRIMARY KEY, application_id TEXT NOT NULL, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scenarios (id TEXT PRIMARY KEY, name TEXT NOT NULL, duration TEXT NOT NULL, start_delay TEXT, assignment_json TEXT, scenario_mode TEXT, shakeout_enabled INTEGER DEFAULT 0, resource_baseline_enabled INTEGER DEFAULT 0, goal_config_json TEXT, runtime_settings_json TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scenario_scripts (scenario_id TEXT NOT NULL, script_id TEXT NOT NULL, script_order INTEGER NOT NULL, vusers INTEGER NOT NULL, ramp_up TEXT, ramp_down TEXT, iterations INTEGER, initialization TEXT, PRIMARY KEY (scenario_id, script_order))`,
		`CREATE TABLE shared_data_pools (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, schema_json TEXT, archived_at DATETIME, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE shared_data_pool_rows (id TEXT PRIMARY KEY, pool_id TEXT NOT NULL, data_json TEXT NOT NULL, status TEXT, version INTEGER NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scenario_data_pool_bindings (id TEXT PRIMARY KEY, scenario_id TEXT NOT NULL, pool_id TEXT NOT NULL, alias TEXT NOT NULL, access_mode TEXT NOT NULL, selection_strategy TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE folders (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, name TEXT NOT NULL, category TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scripts (id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL, version TEXT NOT NULL, script_json TEXT NOT NULL, metadata_json TEXT, created_at DATETIME)`,
		`CREATE TABLE tests (id TEXT PRIMARY KEY, name TEXT NOT NULL, status TEXT NOT NULL, script_id TEXT, total_vusers INTEGER, duration_ns INTEGER, start_time DATETIME, end_time DATETIME, error TEXT, groups_json TEXT, goal_config_json TEXT)`,
		`CREATE TABLE test_history (test_id TEXT, timestamp DATETIME, metrics_json TEXT, FOREIGN KEY(test_id) REFERENCES tests(id))`,
		`CREATE TABLE test_interval_summaries (test_id TEXT, timestamp DATETIME, active_vusers INTEGER, tps REAL, avg_response_ms REAL, error_pct REAL, total_transactions INTEGER, total_errors INTEGER, p50_ms REAL, p90_ms REAL, p95_ms REAL, p99_ms REAL, dropped_results INTEGER, websocket_clients INTEGER, websocket_dropped_messages INTEGER, websocket_dropped_events INTEGER, collected_at DATETIME, published_at DATETIME, sample_age_ms REAL, FOREIGN KEY(test_id) REFERENCES tests(id))`,
		`CREATE TABLE run_archives (test_id TEXT PRIMARY KEY, archive_path TEXT, manifest_path TEXT, transaction_count INTEGER, resource_sample_count INTEGER, interval_summary_count INTEGER, created_at DATETIME, completed_at DATETIME, FOREIGN KEY(test_id) REFERENCES tests(id))`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT, updated_at DATETIME)`,
	}
	for _, statement := range statements {
		if _, err := rawDB.Exec(statement); err != nil {
			t.Fatalf("exec statement failed: %v", err)
		}
	}

	timestamp := time.Date(2026, time.May, 13, 17, 30, 0, 0, time.UTC)
	if _, err := rawDB.Exec(`INSERT INTO tests (id, name, status, total_vusers, duration_ns, start_time, groups_json, goal_config_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-legacy", "Legacy", "COMPLETE", 1, 0, timestamp, `[]`, `{}`); err != nil {
		t.Fatalf("insert legacy test failed: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO test_interval_summaries (test_id, timestamp, active_vusers, tps, avg_response_ms, error_pct, total_transactions, total_errors, p50_ms, p90_ms, p95_ms, p99_ms, dropped_results, websocket_clients, websocket_dropped_messages, websocket_dropped_events, collected_at, published_at, sample_age_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-legacy", timestamp, 7, 12.5, 180.0, 0.5, 50, 1, 120.0, 200.0, 250.0, 300.0, 2, 1, 3, 1, timestamp.Add(-time.Second), timestamp, 1000.0); err != nil {
		t.Fatalf("insert legacy typed summary failed: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO run_archives (test_id, archive_path, manifest_path, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-legacy", "/tmp/archive", "/tmp/archive/manifest.json", 10, 2, 3, timestamp, timestamp); err != nil {
		t.Fatalf("insert legacy archive row failed: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw db failed: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	history, err := store.GetHistory("test-legacy")
	if err != nil {
		t.Fatalf("GetHistory returned error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	record, ok := history[0].(IntervalSummaryRecord)
	if !ok {
		t.Fatalf("expected typed interval summary, got %#v", history[0])
	}
	if record.SchemaVersion != intervalSummarySchemaVersionV1 {
		t.Fatalf("migrated summary schema version = %q, want %q", record.SchemaVersion, intervalSummarySchemaVersionV1)
	}

	archive, err := store.GetRunArchive("test-legacy")
	if err != nil {
		t.Fatalf("GetRunArchive returned error: %v", err)
	}
	if archive.SchemaVersion != archiveRecordSchemaVersion {
		t.Fatalf("archive schema version = %q, want %q", archive.SchemaVersion, archiveRecordSchemaVersion)
	}
}

func TestSQLiteStoreSharedDataPoolsAndScenarioBindings(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-data-pool",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Checkout", Type: "request", Method: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &Scenario{ID: "scenario-data-pool", Name: "Checkout Flow", Duration: "5m", CreatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []ScenarioScript{{Order: 0, ScriptID: script.ID, VUsers: 10}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	pool := &SharedDataPool{
		ID:          "pool-1",
		Name:        "Customers",
		Description: "Reusable customer identities",
		Schema:      map[string]string{"customer_id": "string", "zip": "string"},
		ProjectID:   DefaultProjectID,
		CreatedAt:   time.Now(),
	}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	roundTripPool, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool returned error: %v", err)
	}
	if roundTripPool.ProjectID != DefaultProjectID {
		t.Fatalf("data pool project id = %q, want %q", roundTripPool.ProjectID, DefaultProjectID)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID:     "row-1",
		PoolID: pool.ID,
		Data:   map[string]string{"customer_id": "C-100", "zip": "10001"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID:     "row-1",
		PoolID: pool.ID,
		Data:   map[string]string{"customer_id": "C-101", "zip": "10002"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow update returned error: %v", err)
	}

	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Data["customer_id"] != "C-101" || rows[0].Version != 2 {
		t.Fatalf("unexpected rows after update: %#v", rows)
	}

	bindings := []ScenarioDataPoolBinding{{
		ID:                "binding-1",
		PoolID:            pool.ID,
		Alias:             "customers",
		AccessMode:        "read_write",
		SelectionStrategy: "unique",
	}}
	if err := store.SaveScenarioDataPoolBindings(scenario.ID, bindings); err != nil {
		t.Fatalf("SaveScenarioDataPoolBindings returned error: %v", err)
	}
	roundTrip, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioDataPoolBindings returned error: %v", err)
	}
	if len(roundTrip) != 1 || roundTrip[0].PoolName != pool.Name || roundTrip[0].Alias != "customers" || roundTrip[0].SelectionStrategy != "unique" {
		t.Fatalf("unexpected scenario data pool bindings: %#v", roundTrip)
	}
	if err := store.DeleteDataPool(pool.ID); err == nil {
		t.Fatal("expected DeleteDataPool to fail while scenario binding exists")
	}
	if err := store.DeleteScenarioDataPoolBinding("binding-1"); err != nil {
		t.Fatalf("DeleteScenarioDataPoolBinding returned error: %v", err)
	}
	if err := store.DeleteDataPool(pool.ID); err != nil {
		t.Fatalf("DeleteDataPool returned error after unbinding: %v", err)
	}
}

func TestSQLiteStoreUsageAccountingSnapshots(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	firstGeneratedAt := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	secondGeneratedAt := firstGeneratedAt.Add(time.Hour)
	if err := store.SaveUsageAccountingSnapshot(&UsageAccountingSnapshot{
		ID:          "usage-snapshot-1",
		ProjectID:   "project-a",
		GeneratedAt: firstGeneratedAt,
		PayloadJSON: `{"project_id":"project-a","totals":{"run_count":1}}`,
		CreatedAt:   firstGeneratedAt,
	}); err != nil {
		t.Fatalf("SaveUsageAccountingSnapshot first returned error: %v", err)
	}
	if err := store.SaveUsageAccountingSnapshot(&UsageAccountingSnapshot{
		ID:          "usage-snapshot-2",
		ProjectID:   "project-b",
		GeneratedAt: secondGeneratedAt,
		PayloadJSON: `{"project_id":"project-b","totals":{"run_count":2}}`,
		CreatedAt:   secondGeneratedAt,
	}); err != nil {
		t.Fatalf("SaveUsageAccountingSnapshot second returned error: %v", err)
	}

	projectSnapshots, err := store.ListUsageAccountingSnapshots("project-a", 10, 0)
	if err != nil {
		t.Fatalf("ListUsageAccountingSnapshots project returned error: %v", err)
	}
	if len(projectSnapshots) != 1 || projectSnapshots[0].ID != "usage-snapshot-1" || projectSnapshots[0].PayloadJSON == "" {
		t.Fatalf("project snapshots = %#v, want project-a snapshot", projectSnapshots)
	}

	allSnapshots, err := store.ListUsageAccountingSnapshots("", 10, 0)
	if err != nil {
		t.Fatalf("ListUsageAccountingSnapshots all returned error: %v", err)
	}
	if len(allSnapshots) != 2 || allSnapshots[0].ID != "usage-snapshot-2" || allSnapshots[1].ID != "usage-snapshot-1" {
		t.Fatalf("all snapshots = %#v, want newest first", allSnapshots)
	}
}

// TestDeleteUserRemovesRoleBindings locks in that DeleteUser cleans up role
// bindings explicitly (the declared FK cascade is non-functional on both
// backends), so a deleted user cannot leave orphaned RBAC grants.
func TestDeleteUserRemovesRoleBindings(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	user := &User{ID: "user-del", Subject: "oidc|del", Email: "del@example.com", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser returned error: %v", err)
	}
	if err := store.SaveUserRoleBinding(&UserRoleBinding{ID: "b-del", UserID: user.ID, ScopeType: "global", Role: "system_admin", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveUserRoleBinding returned error: %v", err)
	}

	if err := store.DeleteUser(user.ID); err != nil {
		t.Fatalf("DeleteUser returned error: %v", err)
	}

	bindings, err := store.ListUserRoleBindings(user.ID)
	if err != nil {
		t.Fatalf("ListUserRoleBindings returned error: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no role bindings after DeleteUser, got %d: %#v", len(bindings), bindings)
	}
}

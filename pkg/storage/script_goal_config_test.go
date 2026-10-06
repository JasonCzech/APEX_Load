package storage

import (
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func scriptGoalsFixture() *dsl.GoalConfig {
	zero, ceiling, errorCount, minimum, maximum := 0.0, 125.5, 0, 0, 25
	return &dsl.GoalConfig{
		TargetAverageTPS: &zero, P95LatencyMs: &ceiling, MaxErrorPct: &zero,
		MaxAvgServerProcessingMs: &zero, MaxAvgTransferMs: &zero, MaxErrorCount: &errorCount,
		MinVUsers: &minimum, MaxVUsers: &maximum, StopOnSLABreach: false,
		StopOnMaxErrors: true, SLABreachWindow: "30s",
		TransactionRules: []dsl.TransactionSLARule{
			{Population: "load", Protocol: "http", Group: "Checkout", Transaction: "Pay", Metric: "error_pct", Comparator: "lte", Threshold: 0, Severity: "warning"},
			{Population: "observer", Protocol: "grpc", Group: "Canary", Transaction: "Observe", Metric: "p95_ms", Comparator: "lte", Threshold: 125.5, Severity: "error"},
		},
	}
}

func requireScriptGoals(t *testing.T, store *SQLiteStore, id string, want *dsl.GoalConfig) *dsl.Script {
	t.Helper()
	got, err := store.GetScript(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.GoalConfig, want) {
		t.Fatalf("script %s goals = %#v, want %#v", id, got.GoalConfig, want)
	}
	listed, err := store.ListScripts(got.ProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range listed {
		if script.ID == id {
			if !reflect.DeepEqual(script.GoalConfig, want) {
				t.Fatalf("listed script %s goals = %#v, want %#v", id, script.GoalConfig, want)
			}
			return got
		}
	}
	t.Fatalf("script %s missing from listing", id)
	return nil
}

func exerciseScriptGoalConfigWriters(t *testing.T, store *SQLiteStore) {
	t.Helper()
	for _, writer := range []string{"save", "conditional", "create-only"} {
		t.Run(writer, func(t *testing.T) {
			script := &dsl.Script{ID: "goals-" + writer, Name: writer, Protocol: "http", ProjectID: DefaultProjectID}
			if writer == "create-only" {
				script.GoalConfig = scriptGoalsFixture()
				created, err := store.CreateScriptIfAbsent(script)
				if err != nil || !created {
					t.Fatalf("create = %t, %v", created, err)
				}
				requireScriptGoals(t, store, script.ID, script.GoalConfig)
				conflict := *script
				conflict.GoalConfig = nil
				created, err = store.CreateScriptIfAbsent(&conflict)
				if err != nil || created {
					t.Fatalf("existing create = %t, %v", created, err)
				}
				requireScriptGoals(t, store, script.ID, script.GoalConfig)
				return
			}
			if err := store.SaveScript(script); err != nil {
				t.Fatal(err)
			}
			for _, goals := range []*dsl.GoalConfig{scriptGoalsFixture(), {}, nil} {
				observed := requireScriptGoals(t, store, script.ID, script.GoalConfig)
				script = observed
				script.GoalConfig = goals
				if writer == "save" {
					if err := store.SaveScript(script); err != nil {
						t.Fatal(err)
					}
				} else {
					expected := observed.UpdatedAt
					updated, err := store.UpdateScriptIfUnchanged(script, expected)
					if err != nil || !updated {
						t.Fatalf("conditional update = %t, %v", updated, err)
					}
					stale := *script
					stale.GoalConfig = scriptGoalsFixture()
					updated, err = store.UpdateScriptIfUnchanged(&stale, expected)
					if err != nil || updated {
						t.Fatalf("stale update = %t, %v", updated, err)
					}
				}
				requireScriptGoals(t, store, script.ID, goals)
			}
		})
	}
}

func TestScriptGoalConfigWritersAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script-goals.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	exerciseScriptGoalConfigWriters(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	requireScriptGoals(t, reopened, "goals-save", nil)
	requireScriptGoals(t, reopened, "goals-conditional", nil)
	requireScriptGoals(t, reopened, "goals-create-only", scriptGoalsFixture())
}

func TestScriptGoalConfigGitHubGoalsOnlyChangesAndRetentionReader(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-goals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseScriptGoalConfigGitHub(t, store)
}

func exerciseScriptGoalConfigGitHub(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	script := &dsl.Script{ID: "github-goals", Name: "GitHub goals", Protocol: "http", Version: "1", ProjectID: DefaultProjectID, CreatedAt: now, UpdatedAt: now, GoalConfig: scriptGoalsFixture()}
	link := &GitHubScriptLink{ScriptID: script.ID, Owner: "owner", Repo: "repo", Branch: "main", Path: "goals.json"}
	if outcome, err := store.CommitGitHubScriptImport(script, link, time.Time{}); err != nil || outcome != GitHubScriptImportCreated {
		t.Fatalf("create = %s, %v", outcome, err)
	}
	observed := requireScriptGoals(t, store, script.ID, scriptGoalsFixture())
	retry := *observed
	if outcome, err := store.CommitGitHubScriptImport(&retry, link, observed.UpdatedAt); err != nil || outcome != GitHubScriptImportUnchanged {
		t.Fatalf("unchanged goals import = %s, %v", outcome, err)
	}
	// The change touches no other authored field, so the transactional reader's
	// source fingerprint must include goals to distinguish it from a no-op.
	for _, goals := range []*dsl.GoalConfig{{}, nil} {
		candidate := *observed
		candidate.GoalConfig = goals
		candidate.UpdatedAt = observed.UpdatedAt.Add(time.Second)
		if outcome, err := store.CommitGitHubScriptImport(&candidate, link, observed.UpdatedAt); err != nil || outcome != GitHubScriptImportUpdated {
			t.Fatalf("goals-only import = %s, %v", outcome, err)
		}
		observed = requireScriptGoals(t, store, script.ID, goals)
	}
}

func TestScriptGoalConfigBrunoCommitRollbackAndNativeReceipt(t *testing.T) {
	store := newBrunoImportTestStore(t)
	exerciseScriptGoalConfigBruno(t, store)
}

func exerciseScriptGoalConfigBruno(t *testing.T, store *SQLiteStore) {
	t.Helper()
	first := brunoImportCommitFixture("bruno-goals", "project-bruno", "goals-revision-1", "goals-raw-1", "goals-sanitized-1", "goals-raw-sha-1", "goals-sanitized-sha-1")
	first.Script.GoalConfig = scriptGoalsFixture()
	if _, err := store.CommitBrunoImport(first); err != nil {
		t.Fatal(err)
	}
	observed := requireScriptGoals(t, store, first.Script.ID, scriptGoalsFixture())
	second := brunoImportCommitFixture(first.Script.ID, "project-bruno", "goals-revision-2", "goals-raw-2", "goals-sanitized-2", "goals-raw-sha-2", "goals-sanitized-sha-2")
	second.ExpectedScriptUpdatedAt = &observed.UpdatedAt
	second.ExpectedActiveRevisionID = first.Revision.ID
	if _, err := store.CommitBrunoImport(second); err != nil {
		t.Fatal(err)
	}
	requireScriptGoals(t, store, first.Script.ID, nil)
	if _, err := store.ActivateImportSourceRevision(first.Script.ID, "project-bruno", first.Revision.ID, second.Revision.ID); err != nil {
		t.Fatal(err)
	}
	// Rollback changes the immutable source binding and preserves current APEX
	// goals; it must not resurrect the earlier source revision's goals.
	requireScriptGoals(t, store, first.Script.ID, nil)
	revision, err := store.GetImportSourceRevision(first.Revision.ID, "project-bruno")
	if err != nil || revision.CanonicalScript == nil || !reflect.DeepEqual(revision.CanonicalScript.GoalConfig, scriptGoalsFixture()) {
		t.Fatalf("immutable revision goals lost: %#v, %v", revision, err)
	}

	batch := brunoNativeImportCommitFixture("native-goals", "native-goals-fingerprint")
	batch.Scripts[0].GoalConfig = scriptGoalsFixture()
	batch.Scripts[1].GoalConfig = &dsl.GoalConfig{}
	validFolder := batch.Scripts[1].FolderID
	missingFolder := "missing-goals-folder"
	batch.Scripts[1].FolderID = &missingFolder
	if _, err := store.CommitBrunoNativeImport(batch); err == nil {
		t.Fatal("invalid second destination committed the goal-bearing batch")
	}
	for _, script := range batch.Scripts {
		if _, err := store.GetScript(script.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("rolled-back goal-bearing script %s lookup = %v", script.ID, err)
		}
	}
	if _, err := store.GetBrunoNativeImportByIdempotencyKey(batch.Batch.IdempotencyKey, "project-bruno"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled-back goal-bearing receipt lookup = %v", err)
	}
	batch.Scripts[1].FolderID = validFolder
	committed, err := store.CommitBrunoNativeImport(batch)
	if err != nil {
		t.Fatal(err)
	}
	for i, script := range committed.Scripts {
		requireScriptGoals(t, store, script.ID, batch.Scripts[i].GoalConfig)
	}
	receipt, err := store.GetBrunoNativeImportByIdempotencyKey(batch.Batch.IdempotencyKey, "project-bruno")
	if err != nil {
		t.Fatal(err)
	}
	for i, script := range receipt.Scripts {
		if !reflect.DeepEqual(script.GoalConfig, batch.Scripts[i].GoalConfig) {
			t.Fatalf("receipt %d goals changed", i)
		}
	}
}

func TestScriptGoalConfigLegacyNullAndCorruptFailClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-goals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	script := &dsl.Script{ID: "legacy-goals", Name: "Legacy", ProjectID: DefaultProjectID}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, "", "null"} {
		if _, err := store.db.Exec(`UPDATE scripts SET goal_config_json=? WHERE id=?`, value, script.ID); err != nil {
			t.Fatal(err)
		}
		requireScriptGoals(t, store, script.ID, nil)
	}
	for _, value := range []string{"{", `[]`, `{"max_error_count":"bad"}`} {
		if _, err := store.db.Exec(`UPDATE scripts SET goal_config_json=? WHERE id=?`, value, script.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := store.GetScript(script.ID); err == nil || got != nil || !strings.Contains(err.Error(), "goal_config_json") {
			t.Fatalf("corrupt goals returned script: %#v, %v", got, err)
		}
		if got, err := store.ListScripts(DefaultProjectID, nil); err == nil || got != nil || !strings.Contains(err.Error(), "goal_config_json") {
			t.Fatalf("corrupt goals returned listing: %#v, %v", got, err)
		}
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		got, decodeErr := loadImportSourceRetentionScriptTx(tx, script.ID, false)
		_ = tx.Rollback()
		if decodeErr == nil || got != nil || !strings.Contains(decodeErr.Error(), "goal_config_json") {
			t.Fatalf("corrupt goals returned transactional source: %#v, %v", got, decodeErr)
		}
	}
}

func TestScriptGoalConfigEncodingFailureDoesNotMutateStoredGoals(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "invalid-goals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	script := &dsl.Script{ID: "invalid-goals", Name: "Original", ProjectID: DefaultProjectID, GoalConfig: scriptGoalsFixture()}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	observed := requireScriptGoals(t, store, script.ID, scriptGoalsFixture())
	nan := math.NaN()
	invalid := *observed
	invalid.GoalConfig = &dsl.GoalConfig{P95LatencyMs: &nan}
	if err := store.SaveScript(&invalid); err == nil {
		t.Fatal("non-finite goals were persisted")
	}
	if updated, err := store.UpdateScriptIfUnchanged(&invalid, observed.UpdatedAt); err == nil || updated {
		t.Fatalf("non-finite goals CAS = %t, %v", updated, err)
	}
	requireScriptGoals(t, store, script.ID, scriptGoalsFixture())
}

func TestScriptGoalConfigMigrationPreservesPublishedLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release35-goals.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{ID: "migration-goals", Name: "Legacy goals absent", ProjectID: DefaultProjectID}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	before, err := store.SchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	if err := removeScriptRevisionTrackingForTest(store); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`ALTER TABLE scripts DROP COLUMN content_revision`, `ALTER TABLE scripts DROP COLUMN goal_config_json`, `DELETE FROM schema_migrations WHERE version>=36`} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	after, err := upgraded.SchemaStatus()
	if err != nil || after.AppliedVersion != currentSchemaRelease().Version || len(after.Applied) != len(schemaReleases) || !reflect.DeepEqual(before.Applied[:35], after.Applied[:35]) {
		t.Fatalf("upgrade changed released identities: %#v, %v", after, err)
	}
	requireScriptGoals(t, upgraded, script.ID, nil)
	var raw *string
	if err := upgraded.db.QueryRow(`SELECT goal_config_json FROM scripts WHERE id=?`, script.ID).Scan(&raw); err != nil || raw != nil {
		t.Fatalf("legacy goal column = %#v, %v; want SQL NULL", raw, err)
	}
	persisted := requireScriptGoals(t, upgraded, script.ID, nil)
	persisted.GoalConfig = scriptGoalsFixture()
	if err := upgraded.SaveScript(persisted); err != nil {
		t.Fatal(err)
	}
	requireScriptGoals(t, upgraded, script.ID, scriptGoalsFixture())
}

func TestScriptGoalConfigRulesetFencedUpdate(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "ruleset-goals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ruleset := &CorrelationRuleset{ID: "goals-ruleset", ProjectID: DefaultProjectID, Name: "Ruleset"}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{ID: "ruleset-goals", Name: "Ruleset goals", ProjectID: DefaultProjectID}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	observed := requireScriptGoals(t, store, script.ID, nil)
	candidate := *observed
	candidate.GoalConfig = scriptGoalsFixture()
	updated, err := store.UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(&candidate, observed.UpdatedAt, ruleset.ID, DefaultProjectID, ruleset.UpdatedAt)
	if err != nil || !updated {
		t.Fatalf("fenced goals update = %t, %v", updated, err)
	}
	requireScriptGoals(t, store, script.ID, scriptGoalsFixture())
}

func TestScriptGoalConfigBlankIDRepairPreservesGoals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair-goals.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := marshalScriptForStorage(&dsl.Script{GoalConfig: scriptGoalsFixture()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO scripts (id, name, protocol, version, project_id, goal_config_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at)
		VALUES (' ', 'Repair goals', 'http', '1', ?, ?, '{}', '[]', '[]', '[]', '[]', '[]', ?, ?)`, DefaultProjectID, encoded.goals, now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	repaired, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repaired.Close()
	var id string
	if err := repaired.db.QueryRow(`SELECT id FROM scripts WHERE name='Repair goals'`).Scan(&id); err != nil || strings.TrimSpace(id) == "" {
		t.Fatalf("blank ID not repaired: %q, %v", id, err)
	}
	requireScriptGoals(t, repaired, id, scriptGoalsFixture())
}

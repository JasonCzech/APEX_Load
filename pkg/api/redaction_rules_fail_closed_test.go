package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// redactionProjectStore answers GetProject with a fixed project or error so the
// redaction resolver can be exercised without a database.
type redactionProjectStore struct {
	project *storage.Project
	err     error
	calls   atomic.Int64
}

func (s *redactionProjectStore) GetProject(string) (*storage.Project, error) {
	s.calls.Add(1)
	return s.project, s.err
}

func TestResolveEffectiveRedactionRulesFailsClosedOnProjectLookupError(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.projectStore = &redactionProjectStore{err: errors.New("database is locked")}
	explicit := []engine.RedactionRule{{Name: "explicit", Pattern: "secret-[0-9]+"}}

	rules, err := runner.resolveEffectiveRedactionRules("proj-a", explicit)

	if err == nil {
		t.Fatalf("rules = %v, want an error when the project's redaction policy cannot be read", rules)
	}
	if rules != nil {
		t.Fatalf("rules = %v, want none returned alongside the error", rules)
	}
	if !strings.Contains(err.Error(), `"proj-a"`) || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("error = %v, want the project and the cause", err)
	}
}

func TestResolveEffectiveRedactionRulesTreatsMissingProjectAsNoRules(t *testing.T) {
	explicit := []engine.RedactionRule{{Name: "explicit", Pattern: "secret-[0-9]+"}}
	for name, lookupErr := range map[string]error{
		"no rows":         sql.ErrNoRows,
		"wrapped no rows": fmt.Errorf("get project: %w", sql.ErrNoRows),
		"nil project":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			runner.projectStore = &redactionProjectStore{err: lookupErr}

			rules, err := runner.resolveEffectiveRedactionRules("proj-a", explicit)

			if err != nil {
				t.Fatalf("error = %v, want a missing project to contribute no rules", err)
			}
			if len(rules) != 1 || rules[0].Name != "explicit" {
				t.Fatalf("rules = %v, want only the explicit rule", rules)
			}
		})
	}
}

func TestResolveEffectiveRedactionRulesAppendsProjectRules(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	project := &storage.Project{ID: "proj-a"}
	project.Settings.RedactionRules = []engine.RedactionRule{{Name: "project", Pattern: "ssn-[0-9]+"}}
	runner.projectStore = &redactionProjectStore{project: project}

	rules, err := runner.resolveEffectiveRedactionRules("proj-a", []engine.RedactionRule{{Name: "explicit", Pattern: "secret"}})

	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Name != "explicit" || rules[1].Name != "project" {
		t.Fatalf("rules = %v, want explicit then project rules", rules)
	}
}

func TestResolveEffectiveRedactionRulesFailsClosedOnUndecodableProjectSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "redaction.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project := &storage.Project{ID: "proj-corrupt", Name: "Corrupt settings"}
	project.Settings.RedactionRules = []engine.RedactionRule{{Name: "project", Pattern: "ssn-[0-9]+"}}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE projects SET settings_json = ? WHERE id = ?`, `{"redaction_rules":`, "proj-corrupt"); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.projectStore = store

	rules, err := runner.resolveEffectiveRedactionRules("proj-corrupt", nil)

	if err == nil {
		t.Fatalf("rules = %v, want settings that no longer decode to fail closed", rules)
	}
}

func TestReplayDoesNotRunWithoutProjectRedactionPolicy(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()
	script := &dsl.Script{
		Name:      "Redaction-gated replay",
		Protocol:  "http",
		ProjectID: "proj-a",
		Config:    dsl.ScriptConfig{BaseURL: target.URL},
		Actions:   []dsl.Action{{Name: "Orders", Type: "GET", Target: "/orders"}},
	}

	failing := NewTestRunner(1, NewHub(), nil)
	failing.projectStore = &redactionProjectStore{err: errors.New("connection reset by peer")}
	if _, err := failing.ReplayScriptDefinitionContext(context.Background(), script, engine.ReplayOptions{}); err == nil ||
		!strings.Contains(err.Error(), "load redaction rules") {
		t.Fatalf("ReplayScriptDefinitionContext error = %v, want the redaction policy failure", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("target received %d requests, want none while the redaction policy is unavailable", got)
	}

	// Control: the same script does reach the target when the project simply
	// has no redaction settings, so the zero above is not a harness artifact.
	readable := NewTestRunner(1, NewHub(), nil)
	readable.projectStore = &redactionProjectStore{err: sql.ErrNoRows}
	if _, err := readable.ReplayScriptDefinitionContext(context.Background(), script, engine.ReplayOptions{}); err != nil {
		t.Fatalf("control replay error = %v", err)
	}
	if got := requests.Load(); got == 0 {
		t.Fatal("control replay never reached the target")
	}
}

func TestGroupCompilationWithoutQuotaEvidenceFailsClosedOnProjectLookupError(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:        "script-redaction",
		Name:      "Checkout",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "proj-a",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "Orders", Type: "GET", Target: "/orders"}},
	})
	runner.projectStore = &redactionProjectStore{err: errors.New("database is locked")}

	_, _, _, err := runner.compileManagedTestGroupsLocked([]CreateGroupEntry{{ScriptID: "script-redaction", VUsers: 1}}, nil)

	if err == nil || !strings.Contains(err.Error(), "resolve redaction rules") {
		t.Fatalf("compile error = %v, want the redaction policy failure", err)
	}
}

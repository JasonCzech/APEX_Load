package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsRulesRequireSelectedApprovedNumericSource(t *testing.T) {
	source := storage.MonitorSource{ID: "s", Enabled: true, Pack: "appdynamics", AppDynamics: &appdynamics.Selection{Metrics: []appdynamics.MetricSelection{{Name: "calls", Path: "calls", Unit: "count"}}}}
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: "s"}}}
	rule := analysis.MonitorRule{SourceID: "s", Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "lte", Threshold: 10}
	for _, tc := range []struct {
		name      string
		change    func(*analysis.MonitorRule)
		wantError bool
	}{
		{"approved", func(*analysis.MonitorRule) {}, false},
		{"unselected source", func(r *analysis.MonitorRule) { r.SourceID = "another" }, true},
		{"unselected metric", func(r *analysis.MonitorRule) { r.Metric = "heap" }, true},
		{"wrong unit", func(r *analysis.MonitorRule) { r.Unit = "ms" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rule
			tc.change(&r)
			err := validateSelectedMonitorRules(bindings, []storage.MonitorSource{source}, &GoalConfig{MonitorRules: []analysis.MonitorRule{r}})
			if (err != nil) != tc.wantError {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	if err := validateSelectedMonitorRules(&storage.MonitorBindings{}, []storage.MonitorSource{source}, &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}}); err == nil {
		t.Fatal("disabled test capture accepted a rule")
	}
}

func TestAppDynamicsRequiredProjectGateAndSharedArtifacts(t *testing.T) {
	server, _, job, _ := apiAppDynamicsMonitorFixture(t)
	server.runner.mu.Lock()
	server.runner.tests[job.RunID] = &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID, Required: true}}}}
	server.runner.mu.Unlock()
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	if err := server.runner.validatePreparedMonitorReadiness(job.RunID); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("required monitoring silently disabled: %v", err)
	}
	server.runner.leadership = &executionLeadership{}
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID}}}
	t.Setenv("APEXLOAD_MONITOR_SHARED_FILESYSTEM", "")
	if err := server.runner.validateAppDynamicsSharedArtifacts([]storage.MonitorSource{job.Source}, bindings); err == nil {
		t.Fatal("unattested HA filesystem accepted")
	}
	t.Setenv("APEXLOAD_MONITOR_SHARED_FILESYSTEM", "true")
	if err := server.runner.validateAppDynamicsSharedArtifacts([]storage.MonitorSource{job.Source}, bindings); err != nil {
		t.Fatal(err)
	}
}

func TestAppDynamicsReadinessBoundsSharedAccountQueries(t *testing.T) {
	server, _, job, _ := apiAppDynamicsMonitorFixture(t)
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID, Required: true}}}
	if err := server.runner.validateAppDynamicsCollectionBudgets(bindings, []storage.MonitorSource{job.Source}); err != nil {
		t.Fatal(err)
	}
	large := job.Source
	selection := *large.AppDynamics
	large.AppDynamics = &selection
	selection.Applications = make([]string, 32)
	if err := server.runner.validateAppDynamicsCollectionBudgets(bindings, []storage.MonitorSource{large}); err == nil {
		t.Fatal("required query set cannot settle before its deadline")
	}
	bindings.Sources[0].Required = false
	if err := server.runner.validateAppDynamicsCollectionBudgets(bindings, []storage.MonitorSource{large}); err != nil {
		t.Fatal("optional collection should report partial coverage at runtime", err)
	}
}

func TestAppDynamicsSafeJobOmitsPrivateCursorState(t *testing.T) {
	job := storage.RunMonitorJob{ConnectionSnapshot: &storage.AppDynamicsConnectionSnapshot{PasswordSecretName: "private-reference"}, QueryCoverage: map[string]appdynamics.Coverage{"private-query": {Message: "internal-progress"}}, FinalDigest: "private-hash"}
	body, err := json.Marshal(safeMonitorJob(job))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-reference", "query_coverage", "private-hash", "internal-progress"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("public job disclosed %s", private)
		}
	}
	var projection map[string]json.RawMessage
	if err := json.Unmarshal(body, &projection); err != nil {
		t.Fatal(err)
	}
	if _, exists := projection["query_watermark"]; exists {
		t.Fatal("unset watermark must be omitted from the public job")
	}
	job.QueryWatermark = time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	body, err = json.Marshal(safeMonitorJob(job))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &projection); err != nil {
		t.Fatal(err)
	}
	if string(projection["query_watermark"]) != `"2026-10-03T12:00:00Z"` {
		t.Fatalf("source watermark was lost: %s", body)
	}
}

func TestAppDynamicsTerminalRulesDoNotCountAsLiveGoals(t *testing.T) {
	config := &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "s", Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "lt", Threshold: 0}}}
	if !hasGoalConfig(config) || hasLiveGoalConfig(config) {
		t.Fatal("terminal rules entered live acceptance")
	}
	limit := 10.0
	config.P95LatencyMs = &limit
	if !hasLiveGoalConfig(config) {
		t.Fatal("existing live guardrail disappeared")
	}
}

func TestAppDynamicsPreparationRejectsChangedExecutionLocationBeforeProbe(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*storage.AppDynamicsConnection)
	}{
		{"mode", func(c *storage.AppDynamicsConnection) {
			c.CollectionMode, c.CollectorAgentID = "collector", "assigned-collector"
		}},
		{"origin", func(c *storage.AppDynamicsConnection) { c.Config.Endpoint = "https://changed-controller.example" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			server, _, job, _ := apiAppDynamicsMonitorFixture(t)
			connections := server.store.(storage.AppDynamicsConnectionStore)
			connection, err := connections.GetAppDynamicsConnection(job.Source.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			change.edit(connection)
			if ok, err := connections.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
				t.Fatalf("connection edit %v %v", ok, err)
			}
			if _, err := connections.UpdateAppDynamicsConnectionHealth(connection.ID, job.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true, Violations: true}, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			server.runner.mu.Lock()
			server.runner.tests[job.RunID] = &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID, Required: true}}}}
			server.runner.mu.Unlock()
			called := false
			server.runner.appDynamicsPrepare = func(context.Context, storage.MonitorSource, *storage.AppDynamicsConnectionSnapshot) error {
				called = true
				return nil
			}
			if err := server.runner.validatePreparedMonitorReadiness(job.RunID); err == nil || !strings.Contains(err.Error(), "match") || called {
				t.Fatalf("changed location was probed or accepted: called=%v error=%v", called, err)
			}
		})
	}
}

func TestAppDynamicsPreparationRejectsRotatedCollectorKeyBeforeProbe(t *testing.T) {
	server, monitors, job, _ := apiAppDynamicsMonitorFixture(t)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	now := time.Now().UTC()
	collectorID := "readiness-appd-collector"
	if err := server.store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: collectorID, ProjectID: job.ProjectID, Name: "collector", TokenHash: "fixture", Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"appdynamics"}}); err != nil {
		t.Fatal(err)
	}
	connections := server.store.(storage.AppDynamicsConnectionStore)
	connection, err := connections.GetAppDynamicsConnection(job.Source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	connection.CollectionMode, connection.CollectorAgentID = "collector", collectorID
	if ok, err := connections.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
		t.Fatalf("connection edit %v %v", ok, err)
	}
	if _, err := connections.UpdateAppDynamicsConnectionHealth(connection.ID, job.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Metrics: true, Violations: true}, now); err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	if err := monitors.SaveCollectorRunReadiness(job.ProjectID, collectorID, []string{"appdynamics", "appdynamics.v1"}, []string{connection.Config.Endpoint + "#" + public}, now); err != nil {
		t.Fatal(err)
	}
	source := job.Source
	source.ExecutorMode, source.CollectorID, source.CollectorPublicKey = "collector", collectorID, public
	if _, err := monitors.SaveMonitorSource(source, source.Revision); err != nil {
		t.Fatal(err)
	}
	key, err = ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rotated := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	if err := monitors.SaveCollectorRunReadiness(job.ProjectID, collectorID, []string{"appdynamics", "appdynamics.v1"}, []string{connection.Config.Endpoint + "#" + rotated}, now); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests[job.RunID] = &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}}
	server.runner.mu.Unlock()
	called := false
	server.runner.appDynamicsPrepare = func(context.Context, storage.MonitorSource, *storage.AppDynamicsConnectionSnapshot) error {
		called = true
		return nil
	}
	if err := server.runner.validatePreparedMonitorReadiness(job.RunID); err == nil || !strings.Contains(err.Error(), "enrollment changed") || called {
		t.Fatalf("rotated encryption identity was probed or accepted: called=%v error=%v", called, err)
	}
}

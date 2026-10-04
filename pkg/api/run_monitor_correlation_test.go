package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type correlationHeaderCase struct {
	name     string
	edit     func(*dsl.Script, string)
	conflict bool
}

func correlationHeaderCases() []correlationHeaderCase {
	return []correlationHeaderCase{
		{"matching_literal", func(s *dsl.Script, id string) {
			s.Config.HTTP.DefaultHeaders = map[string]string{"x-apex-test-run": id}
			s.Config.HTTP.DefaultHeaderList = dsl.HeaderList{{Name: "X-Apex-Test-Run", Value: id}}
			s.Actions[0].Headers = map[string]string{"X-APEX-Test-Run": id}
		}, false},
		{"matching_run_id", func(s *dsl.Script, _ string) {
			s.Config.HTTP.DefaultHeaders = map[string]string{"x-apex-test-run": "{{ run.id }}"}
			s.Config.HTTP.DefaultHeaderList = dsl.HeaderList{{Name: "X-Apex-Test-Run", Value: "{{run.id}}"}}
			s.Actions[0].HeaderList = dsl.HeaderList{{Name: "X-APEX-Test-Run", Value: "{{ run.id }}"}}
		}, false},
		{"default_map_conflict", func(s *dsl.Script, _ string) {
			s.Config.HTTP.DefaultHeaders = map[string]string{"x-apex-test-run": "another-run"}
		}, true},
		{"default_list_conflict", func(s *dsl.Script, id string) {
			s.Config.HTTP.DefaultHeaderList = dsl.HeaderList{{Name: "X-Apex-Test-Run", Value: id}, {Name: "x-apex-test-run", Value: "another-run"}}
		}, true},
		{"action_map_conflict", func(s *dsl.Script, _ string) {
			s.Actions[0].Headers = map[string]string{"X-APEX-Test-Run": "another-run"}
		}, true},
		{"action_list_conflict", func(s *dsl.Script, id string) {
			s.Actions[0].HeaderList = dsl.HeaderList{{Name: "X-Apex-Test-Run", Value: id}, {Name: "x-apex-test-run", Value: "another-run"}}
		}, true},
		{"nested_sequence_map_conflict", func(s *dsl.Script, _ string) {
			s.Actions = []dsl.Action{{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "nested", "method": "GET", "url": "/nested", "headers": map[string]any{"X-Apex-Test-Run": "another-run"}}}}}}}}}
		}, true},
		{"nested_sequence_list_conflict", func(s *dsl.Script, _ string) {
			s.Actions = []dsl.Action{{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "nested", "method": "GET", "url": "/nested", "header_list": []any{map[string]any{"name": "X-Apex-Test-Run", "value": "another-run"}}}}}}}}}}
		}, true},
	}
}

func correlationTestTarget(t *testing.T) (*httptest.Server, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	requests, connections := new(atomic.Int64), new(atomic.Int64)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	target.Start()
	t.Cleanup(target.Close)
	return target, requests, connections
}

// The reusable AppDynamics fixture already froze a disabled run-header source
// in appd-run. Update a detached live selection before creating any test run.
func correlationMonitorFixture(t *testing.T, origin string) (*Server, providerMonitorStore, storage.MonitorSource) {
	t.Helper()
	server, monitors, oldJob, _ := apiAppDynamicsMonitorFixture(t)
	sources, err := monitors.ListMonitorSources(oldJob.ProjectID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("live monitor source count=%d error=%v", len(sources), err)
	}
	source := sources[0]
	raw, err := json.Marshal(source.AppDynamics)
	if err != nil {
		t.Fatal(err)
	}
	var selection appdynamics.Selection
	if err := json.Unmarshal(raw, &selection); err != nil {
		t.Fatal(err)
	}
	selection.RunHeader, selection.HeaderOrigins = true, []string{origin}
	source.AppDynamics = &selection
	updated, err := monitors.SaveMonitorSource(source, source.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return server, monitors, *updated
}

func correlationScript(protocol, origin string) *dsl.Script {
	return &dsl.Script{ID: "correlation-script", Name: "correlation", Version: "1", Protocol: protocol, ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: origin}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/load"}}}
}

func installCorrelationReadinessRun(t *testing.T, server *Server, monitors providerMonitorStore, source storage.MonitorSource, id string, script *dsl.Script) {
	t.Helper()
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: false}}}
	now := time.Now().UTC()
	if err := server.store.SaveTest(&storage.TestRecord{ID: id, ProjectID: source.ProjectID, Name: "correlation", Status: "QUEUED", StartTime: now, MonitorBindings: bindings}); err != nil {
		t.Fatal(err)
	}
	jobs, err := monitors.StartRunMonitors(source.ProjectID, id, "", now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].Source.AppDynamics == nil || !jobs[0].Source.AppDynamics.RunHeader {
		t.Fatalf("fresh enabled monitor snapshot count=%d error=%v", len(jobs), err)
	}
	server.runner.mu.Lock()
	server.runner.tests[id] = &ManagedTest{ID: id, ProjectID: source.ProjectID, MonitorBindings: bindings, groupSpecs: []engine.GroupConfig{{Script: script}}}
	server.runner.mu.Unlock()
	if got := server.runner.runMonitorCorrelationOrigins(id); !reflect.DeepEqual(got, source.AppDynamics.HeaderOrigins) || len(got) == 0 {
		t.Fatalf("correlation fixture has no approved frozen origin: %v", got)
	}
}

func TestRunMonitorCorrelationPreparationIncludesHTTPAliases(t *testing.T) {
	for _, proto := range []string{"http", "http/1.1", "http/2", "soap"} {
		t.Run(proto, func(t *testing.T) {
			target, requests, connections := correlationTestTarget(t)
			server, monitors, source := correlationMonitorFixture(t, target.URL)
			var probes atomic.Int64
			server.runner.appDynamicsPrepare = func(context.Context, storage.MonitorSource, *storage.AppDynamicsConnectionSnapshot) error {
				probes.Add(1)
				return nil
			}
			for index, tc := range correlationHeaderCases() {
				t.Run(tc.name, func(t *testing.T) {
					id := fmt.Sprintf("correlation-run-%d", index)
					script := correlationScript(proto, target.URL)
					tc.edit(script, id)
					before, _ := json.Marshal(script)
					installCorrelationReadinessRun(t, server, monitors, source, id, script)
					err := server.runner.validatePreparedMonitorReadiness(id)
					if tc.conflict {
						if err == nil || !strings.Contains(err.Error(), "authored X-Apex-Test-Run conflicts with managed run correlation") {
							t.Fatalf("conflict was not rejected: %v", err)
						}
					} else if err != nil {
						t.Fatalf("matching header rejected: %v", err)
					}
					after, _ := json.Marshal(script)
					if string(before) != string(after) || probes.Load() != 0 || requests.Load() != 0 || connections.Load() != 0 {
						t.Fatalf("readiness mutated or contacted a target: changed=%v probes=%d requests=%d connections=%d", string(before) != string(after), probes.Load(), requests.Load(), connections.Load())
					}
				})
			}
		})
	}
	t.Run("frozen origins and excluded bridges", func(t *testing.T) {
		target, requests, connections := correlationTestTarget(t)
		server, monitors, source := correlationMonitorFixture(t, target.URL)
		for index, proto := range []string{"java_http", "java_vuser", "bruno"} {
			id := fmt.Sprintf("bridge-run-%d", index)
			script := correlationScript(proto, target.URL)
			script.Actions[0].Headers = map[string]string{"X-Apex-Test-Run": "another-run"}
			installCorrelationReadinessRun(t, server, monitors, source, id, script)
			if err := server.runner.validatePreparedMonitorReadiness(id); err != nil {
				t.Fatalf("bridge %s was included in native HTTP correlation gate: %v", proto, err)
			}
		}
		id := "frozen-run"
		frozenScript := correlationScript("http/2", target.URL)
		frozenScript.Actions[0].Headers = map[string]string{"X-Apex-Test-Run": "another-run"}
		installCorrelationReadinessRun(t, server, monitors, source, id, frozenScript)
		selection := *source.AppDynamics
		selection.RunHeader, selection.HeaderOrigins = false, nil
		source.AppDynamics = &selection
		if _, err := monitors.SaveMonitorSource(source, source.Revision); err != nil {
			t.Fatal(err)
		}
		if got := server.runner.runMonitorCorrelationOrigins(id); !reflect.DeepEqual(got, []string{target.URL}) {
			t.Fatalf("live edit replaced frozen origins: %v", got)
		}
		if err := server.runner.validatePreparedMonitorReadiness(id); err == nil || !strings.Contains(err.Error(), "authored X-Apex-Test-Run conflicts with managed run correlation") {
			t.Fatalf("live disable bypassed the frozen enabled conflict: %v", err)
		}
		bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}
		if err := server.store.SaveTest(&storage.TestRecord{ID: "disabled-run", ProjectID: source.ProjectID, Name: "disabled", Status: "QUEUED", MonitorBindings: bindings}); err != nil {
			t.Fatal(err)
		}
		script := correlationScript("http/2", target.URL)
		script.Actions[0].Headers = map[string]string{"X-Apex-Test-Run": "another-run"}
		server.runner.mu.Lock()
		server.runner.tests["disabled-run"] = &ManagedTest{ID: "disabled-run", ProjectID: source.ProjectID, MonitorBindings: bindings, groupSpecs: []engine.GroupConfig{{Script: script}}}
		server.runner.mu.Unlock()
		if origins := server.runner.runMonitorCorrelationOrigins("disabled-run"); len(origins) != 0 {
			t.Fatalf("disabled source supplied origins: %v", origins)
		}
		if err := server.runner.validatePreparedMonitorReadiness("disabled-run"); err != nil {
			t.Fatalf("ordinary authored header was rejected: %v", err)
		}
		t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
		if err := server.runner.validatePreparedMonitorReadiness(id); err != nil {
			t.Fatalf("disabled project changed optional baseline behavior: %v", err)
		}
		if requests.Load() != 0 || connections.Load() != 0 {
			t.Fatal("readiness contacted the application")
		}
	})
}

func TestPreparationManagedRunHeaderAliasConflictNeverStartsTraffic(t *testing.T) {
	for _, proto := range []string{"http/1.1", "http/2"} {
		for _, tc := range correlationHeaderCases() {
			// The public run ID is allocated by CreateTest. The template positive
			// case exercises that exact ID; literal acceptance is covered above.
			if tc.name == "matching_literal" {
				continue
			}
			t.Run(proto+"/"+tc.name, func(t *testing.T) {
				t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
				target, requests, connections := correlationTestTarget(t)
				server, _, source := correlationMonitorFixture(t, target.URL)
				backend := &joinedLocalExecutionBackend{done: make(chan struct{})}
				WithExecutionBackend(backend)(server.runner)
				script := correlationScript(proto, target.URL)
				tc.edit(script, "unused-public-run-id")
				stored, err := server.runner.StoreScriptDurable(script)
				if err != nil {
					t.Fatal(err)
				}
				bindings := []storage.MonitorBinding{{SourceID: source.ID, Required: false}}
				run, err := server.runner.CreateTest(CreateTestRequest{Name: "correlation preparation", ProjectID: source.ProjectID, ScriptID: stored.ID, VUsers: 1, Duration: "1s", MonitorBindings: &bindings, scriptShakeoutProvided: true, resourceBaselineProvided: true})
				if err != nil {
					t.Fatal(err)
				}
				if origins := server.runner.runMonitorCorrelationOrigins(run.ID); !reflect.DeepEqual(origins, []string{target.URL}) {
					t.Fatalf("public run has no approved origin: %v", origins)
				}
				path := "/api/v1/tests/" + run.ID + "/preparation"
				reply := timeslotRequest(t, server, http.MethodPost, path, nil)
				if reply.Code != http.StatusAccepted {
					t.Fatalf("prepare status=%d", reply.Code)
				}
				t.Cleanup(func() { _ = server.runner.StopTest(run.ID); backend.finish(t, server.runner) })
				wantState := "ready"
				if tc.conflict {
					wantState = "failed"
				}
				var preparation storage.Preparation
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					reply = timeslotRequest(t, server, http.MethodGet, path, nil)
					if reply.Code != http.StatusOK {
						t.Fatalf("get preparation status=%d", reply.Code)
					}
					preparation = storage.Preparation{}
					if err := json.Unmarshal(reply.Body.Bytes(), &preparation); err != nil {
						t.Fatal(err)
					}
					if preparation.State == "ready" || preparation.State == "failed" {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				if preparation.State != wantState {
					t.Fatalf("preparation state=%q want=%q", preparation.State, wantState)
				}
				if tc.conflict {
					var failed ManagedTest
					// Preparation and test terminal publication are asynchronous. Use
					// the original deadline for both public observations.
					for time.Now().Before(deadline) {
						reply = timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+run.ID, nil)
						if reply.Code != http.StatusOK {
							t.Fatalf("get failed run status=%d", reply.Code)
						}
						failed = ManagedTest{}
						if err := json.Unmarshal(reply.Body.Bytes(), &failed); err != nil {
							t.Fatal(err)
						}
						if failed.Status == "FAILED" {
							break
						}
						if failed.Status == "RUNNING" || failed.Status == "COMPLETE" {
							t.Fatalf("conflicting alias started execution: %s", failed.Status)
						}
						time.Sleep(20 * time.Millisecond)
					}
					if failed.Status != "FAILED" || !strings.Contains(failed.Error, "authored X-Apex-Test-Run conflicts with managed run correlation") {
						t.Fatalf("public failure did not preserve correlation reason: run_status=%s", failed.Status)
					}
				} else {
					if preparation.Monitors == nil {
						t.Fatal("ready preparation has no monitor snapshot")
					}
					frozen := preparation.Monitors.Sources[source.ID].AppDynamics
					if frozen == nil || !frozen.RunHeader || !reflect.DeepEqual(frozen.HeaderOrigins, []string{target.URL}) {
						t.Fatal("ready preparation did not freeze approved origins")
					}
				}
				time.Sleep(100 * time.Millisecond)
				if requests.Load() != 0 || connections.Load() != 0 {
					t.Fatalf("preparation contacted application: requests=%d connections=%d", requests.Load(), connections.Load())
				}
			})
		}
	}
}

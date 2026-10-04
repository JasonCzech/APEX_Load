package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

type managedVUserWire struct {
	path, neutral string
	markers       []string
	major         int
}

func managedVUserTLS(t *testing.T, connections *atomic.Int64, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func managedNativeVUser(t *testing.T, script *dsl.Script, origins []string, variables map[string]string) *VUser {
	t.Helper()
	instance, err := protocol.Get(script.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	group := GroupConfig{RunID: "managed-run-123", RunCorrelationOrigins: origins, Script: script}
	if err := instance.Init(context.Background(), buildGroupProtoConfig(group, "")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Teardown(context.Background()) })
	return &VUser{ID: 1, RunID: group.RunID, Script: script, Proto: instance, Variables: variables, Metrics: NewMetricsCollector(32), SnapshotStore: NewSnapshotStore(8)}
}

func TestConcurrentHTTPManagedRunConflictRejectsBeforeTraffic(t *testing.T) {
	cases := []struct {
		name string
		edit func(*dsl.Script, []dsl.Action)
	}{
		{"action map", func(_ *dsl.Script, children []dsl.Action) {
			children[1].Headers = map[string]string{"x-apex-test-run": "{{var.other_run}}"}
		}},
		{"ordered duplicate", func(_ *dsl.Script, children []dsl.Action) {
			children[1].HeaderList = dsl.HeaderList{{Name: httpprotocol.ManagedRunHeader, Value: "{{ run.id }}"}, {Name: "x-APEX-test-run", Value: "{{var.other_run}}"}}
		}},
		{"resolved default map", func(script *dsl.Script, children []dsl.Action) {
			script.Config.HTTP.DefaultHeaders = map[string]string{httpprotocol.ManagedRunHeader: "{{var.other_run}}"}
			children[0].Headers = map[string]string{httpprotocol.ManagedRunHeader: "{{ run.id }}"}
		}},
		{"resolved default list", func(script *dsl.Script, children []dsl.Action) {
			script.Config.HTTP.DefaultHeaderList = dsl.HeaderList{{Name: httpprotocol.ManagedRunHeader, Value: "{{var.other_run}}"}}
			children[0].HeaderList = dsl.HeaderList{{Name: httpprotocol.ManagedRunHeader, Value: "{{ run.id }}"}}
		}},
	}
	for _, alias := range []string{"http", "http/1.1", "http/2"} {
		for _, tc := range cases {
			t.Run(alias+"/"+tc.name, func(t *testing.T) {
				var calls, connections atomic.Int64
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					fmt.Fprint(w, "unexpected traffic")
				})
				one := managedVUserTLS(t, &connections, handler)
				two := managedVUserTLS(t, &connections, handler)
				children := []dsl.Action{
					{Name: "Valid first sibling", Type: "GET", Target: one.URL + "/one", HTTPVersion: engineVersion(t, "1.1")},
					{Name: "Conflicting second sibling", Type: "GET", Target: two.URL + "/two", HTTPVersion: engineVersion(t, "2")},
				}
				script := &dsl.Script{Name: "managed concurrent preflight", Protocol: alias, Config: dsl.ScriptConfig{BaseURL: one.URL, HTTP: dsl.HTTPConfig{TLS: dsl.TLSConfig{InsecureSkipVerify: true}}}}
				tc.edit(script, children)
				group := dsl.Action{Name: "Mixed-policy group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": children}}
				script.Actions = []dsl.Action{group}
				vu := managedNativeVUser(t, script, []string{one.URL, two.URL}, map[string]string{"other_run": "another-run"})
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				result, err := vu.executeHTTPSequenceAction(ctx, group, dsl.ErrorHandlingConfig{})
				if err != nil || result == nil || result.Success || result.ErrorMessage != "concurrent HTTP child 2: request validation failed" {
					t.Fatalf("expected whole-group preflight rejection: result=%+v err=%v", result, err)
				}
				if calls.Load() != 0 || connections.Load() != 0 {
					t.Fatalf("group sent traffic before all-sibling admission: calls=%d connections=%d", calls.Load(), connections.Load())
				}
				if result.HTTPVersionSource != "" || result.HTTPVersionNegotiated != "" {
					t.Fatalf("rejected group invented accepted wire evidence: %+v", result.ProtocolEvidence)
				}
				if vu.Variables["other_run"] != "another-run" {
					t.Fatal("preflight mutated source variable")
				}
			})
		}
	}
}

func TestHTTPAliasesResolveManagedDefaultHeadersOnWire(t *testing.T) {
	for _, alias := range []string{"http/1.1", "http/2"} {
		for _, ordered := range []bool{false, true} {
			for _, managed := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/ordered=%t/managed=%t", alias, ordered, managed), func(t *testing.T) {
					var connections atomic.Int64
					var mu sync.Mutex
					var requests []managedVUserWire
					server := managedVUserTLS(t, &connections, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						requests = append(requests, managedVUserWire{path: r.URL.Path, neutral: r.Header.Get("X-Current-Value"), markers: append([]string(nil), r.Header.Values(httpprotocol.ManagedRunHeader)...), major: r.ProtoMajor})
						mu.Unlock()
						fmt.Fprintf(w, "%d", r.ProtoMajor)
					}))
					inheritedMajor, opposite, oppositeMajor := 1, "2", 2
					if alias == "http/2" {
						inheritedMajor, opposite, oppositeMajor = 2, "1.1", 1
					}
					script := &dsl.Script{Name: "alias default interpolation", Protocol: alias, Config: dsl.ScriptConfig{BaseURL: server.URL, HTTP: dsl.HTTPConfig{TLS: dsl.TLSConfig{InsecureSkipVerify: true}}}}
					if ordered {
						script.Config.HTTP.DefaultHeaderList = dsl.HeaderList{{Name: "X-Current-Value", Value: "{{var.neutral}}"}, {Name: httpprotocol.ManagedRunHeader, Value: "{{ run.id }}"}}
					} else {
						script.Config.HTTP.DefaultHeaders = map[string]string{"X-Current-Value": "{{var.neutral}}", httpprotocol.ManagedRunHeader: "{{ run.id }}"}
					}
					children := []dsl.Action{
						{Name: "Concurrent inherited", Type: "GET", Target: "/concurrent-inherited", Extractors: []dsl.Extractor{{Name: "concurrent_inherited", Type: "regex", Source: "body", Expression: "([12])"}}},
						{Name: "Concurrent opposite", Type: "GET", Target: "/concurrent-opposite", HTTPVersion: engineVersion(t, opposite), Extractors: []dsl.Extractor{{Name: "concurrent_opposite", Type: "regex", Source: "body", Expression: "([12])"}}},
					}
					script.Actions = []dsl.Action{
						{Name: "Inherited alias", Type: "GET", Target: "/inherited", Extractors: []dsl.Extractor{{Name: "inherited_version", Type: "regex", Source: "body", Expression: "([12])"}}},
						{Name: "Opposite override", Type: "GET", Target: "/opposite", HTTPVersion: engineVersion(t, opposite), Extractors: []dsl.Extractor{{Name: "opposite_version", Type: "regex", Source: "body", Expression: "([12])"}}},
						{Name: "Concurrent aliases", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": children}},
					}
					script.Config.RunLogic.Iterations = 1
					var origins []string
					if managed {
						origins = []string{server.URL}
					}
					vu := managedNativeVUser(t, script, origins, map[string]string{"neutral": "actual-current-value"})
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					vu.Run(ctx)
					mu.Lock()
					actual := append([]managedVUserWire(nil), requests...)
					mu.Unlock()
					if len(actual) != 4 {
						t.Fatalf("native alias actions did not all execute: requests=%+v variables=%v", actual, vu.Variables)
					}
					wantPaths := map[string]int{"/inherited": inheritedMajor, "/opposite": oppositeMajor, "/concurrent-inherited": inheritedMajor, "/concurrent-opposite": oppositeMajor}
					seen := make(map[string]int)
					for _, request := range actual {
						seen[request.path]++
						if request.major != wantPaths[request.path] || request.neutral != "actual-current-value" || !reflect.DeepEqual(request.markers, []string{"managed-run-123"}) {
							t.Fatalf("alias default interpolation/version lost: %+v", request)
						}
					}
					for path := range wantPaths {
						if seen[path] != 1 {
							t.Fatalf("duplicate/missing native path: %v", seen)
						}
					}
					wantVariables := map[string]string{"inherited_version": fmt.Sprint(inheritedMajor), "opposite_version": fmt.Sprint(oppositeMajor), "concurrent_inherited": fmt.Sprint(inheritedMajor), "concurrent_opposite": fmt.Sprint(oppositeMajor)}
					for name, want := range wantVariables {
						if got := vu.Variables[name]; got != want || strings.Contains(got, "{{") {
							t.Fatalf("actual version extraction %s=%q want %q", name, got, want)
						}
					}
					if connections.Load() == 0 {
						t.Fatal("wire assertions did not establish a real connection")
					}
				})
			}
		}
	}
}

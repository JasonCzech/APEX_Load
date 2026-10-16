package requestimport

import (
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAnalyzeVariableReadinessDecodesBase64WireBody(t *testing.T) {
	wireBody := `{"request_id":"{{var.request_id}}","note":"unchanged"}`
	script := &dsl.Script{
		Actions: []dsl.Action{{
			Name:         "encoded request",
			Type:         "request",
			Method:       "POST",
			Target:       "/orders",
			Body:         base64.StdEncoding.EncodeToString([]byte(wireBody)),
			BodyEncoding: "base64",
		}},
	}
	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"request_id"}) ||
		!reflect.DeepEqual(got.ByAction["actions[0]"], []string{"request_id"}) {
		t.Fatalf("AnalyzeVariableReadiness() = %#v, want encoded request_id missing", got)
	}

	got = AnalyzeVariableReadiness(script, map[string]string{"request_id": "ready"})
	if len(got.Missing) != 0 || len(got.ByAction) != 0 {
		t.Fatalf("seeded AnalyzeVariableReadiness() = %#v, want ready", got)
	}

	finalizedScript := validBaseScript()
	finalizedScript.Actions = append([]dsl.Action(nil), script.Actions...)
	result, err := FinalizeScript(finalizedScript, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if result.Readiness.State != importreview.Blocked ||
		!reflect.DeepEqual(result.Readiness.BlockingCodes, []string{"import_missing_variables"}) {
		t.Fatalf("base64 variable readiness = %#v", result.Readiness)
	}
	if got := result.Script.Actions[0].Metadata["missing_variables"]; !reflect.DeepEqual(got, []string{"request_id"}) {
		t.Fatalf("missing_variables metadata = %#v", got)
	}
}

func TestAnalyzeVariableReadinessBlocksOnlyReferencesWithoutPlaceholderFallback(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Protocol: "http",
		Actions: []dsl.Action{
			{Name: "store", Type: "request", Method: "POST", Target: "/stores/{{var.store}}", Headers: map[string]string{"X-Trace": "{{var.trace}}"}, Body: `{"ban":"{{var.ban}}"}`},
			{Name: "eval", Type: "source_json", Params: map[string]any{"json_object": "doc", "buffer": "{{var.document}}"}},
			{Name: "search", Type: "request", Method: "GET", Target: "/search", Assertions: []dsl.Assertion{{Type: "body_contains", Expected: "{{var.expected}}"}}},
		},
	}
	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"ban", "document", "expected", "store", "trace"}) {
		t.Fatalf("missing = %#v", got.Missing)
	}
	if !reflect.DeepEqual(got.Blocking, []string{"document"}) {
		t.Fatalf("blocking = %#v, want only the JSON helper buffer", got.Blocking)
	}
	script.Protocol = "grpc"
	if got := AnalyzeVariableReadiness(script, nil); !reflect.DeepEqual(got.Blocking, got.Missing) {
		t.Fatalf("non-HTTP blocking = %#v, want every missing reference", got.Blocking)
	}
}

func TestAnalyzeVariableReadinessIncludesHTTPConfigAtAffectedRequests(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://{{var.host}}",
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{"X-Tenant": "{{var.tenant}}"},
			},
		},
		Actions: []dsl.Action{
			{Name: "absolute", Type: "GET", Method: "GET", Target: "https://bootstrap.example.test/host"},
			{Name: "relative", Type: "GET", Method: "GET", Target: "/orders"},
		},
	}

	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"host", "tenant"}) {
		t.Fatalf("missing = %#v", got.Missing)
	}
	if !reflect.DeepEqual(got.ByAction["actions[0]"], []string{"tenant"}) {
		t.Fatalf("absolute request missing = %#v, want shared config only", got.ByAction["actions[0]"])
	}
	if !reflect.DeepEqual(got.ByAction["actions[1]"], []string{"host", "tenant"}) {
		t.Fatalf("relative request missing = %#v, want base URL and shared config", got.ByAction["actions[1]"])
	}
	if !reflect.DeepEqual(got.Blocking, []string{"host", "tenant"}) {
		t.Fatalf("blocking = %#v, protocol config has no placeholder fallback", got.Blocking)
	}

	got = AnalyzeVariableReadiness(script, map[string]string{"host": "api.example.test", "tenant": "tenant-a"})
	if !reflect.DeepEqual(got.Missing, []string{"host", "tenant"}) {
		t.Fatalf("seeded config readiness = %#v, protocol config is not runtime-interpolated", got)
	}
}

func TestAnalyzeVariableReadinessIncludesBaseURLForNamedHTTPRequestTypes(t *testing.T) {
	t.Parallel()
	for _, actionType := range []string{"soap_request", "java_http_request"} {
		actionType := actionType
		t.Run(actionType, func(t *testing.T) {
			t.Parallel()
			script := &dsl.Script{
				Config: dsl.ScriptConfig{
					BaseURL: "https://{{var.runtime_host}}",
				},
				Actions: []dsl.Action{{
					Name:   "request",
					Type:   actionType,
					Target: "/orders",
				}},
			}
			got := AnalyzeVariableReadiness(script, nil)
			if !reflect.DeepEqual(got.ByAction["actions[0]"], []string{"runtime_host"}) {
				t.Fatalf("readiness = %#v, want base URL variable at named HTTP request", got)
			}
		})
	}
}

func TestAnalyzeVariableReadinessDoesNotTreatPriorExtractorAsProtocolConfigInterpolation(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://{{var.host}}"},
		Actions: []dsl.Action{
			{
				Name: "bootstrap", Type: "GET", Method: "GET",
				Target: "https://bootstrap.example.test/host",
				Extractors: []dsl.Extractor{{
					Name: "host", Source: "body", Type: "regex", Expression: `host=(\S+)`,
				}},
			},
			{Name: "relative", Type: "GET", Method: "GET", Target: "/orders"},
		},
	}
	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"host"}) ||
		!reflect.DeepEqual(got.ByAction["actions[1]"], []string{"host"}) {
		t.Fatalf("readiness = %#v, protocol config must remain unresolved after an extractor", got)
	}
}

func TestAnalyzeVariableReadinessScopesProtocolConfigToHTTPFamily(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{"X-Tenant": "{{var.tenant}}"},
			},
			Database: dsl.DatabaseConfig{DSN: "{{var.database_dsn}}"},
			WebSocket: dsl.WebSocketConfig{
				Origin: "{{var.websocket_origin}}",
			},
			JavaVuser: dsl.JavaVuserConfig{
				RunnerCommand: "{{var.java_runner}}",
			},
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"source_note": "{{var.import_evidence}}",
				},
			},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "http_request", Method: "GET", Target: "https://example.test/orders",
		}},
	}

	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"tenant"}) ||
		!reflect.DeepEqual(got.ByAction["actions[0]"], []string{"tenant"}) {
		t.Fatalf("readiness = %#v, want only HTTP runtime config dependency", got)
	}
}

func TestAnalyzeVariableReadinessScopesSOAPAndJavaHTTPConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		protocol string
		action   string
		want     string
	}{
		{name: "soap", protocol: "soap", action: "soap_request", want: "soap_namespace"},
		{name: "java http", protocol: "java_http", action: "java_http_request", want: "java_schema"},
		{name: "plain http", protocol: "http", action: "http_request", want: ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := &dsl.Script{
				Protocol: test.protocol,
				Config: dsl.ScriptConfig{
					SOAP: dsl.SOAPConfig{
						Namespaces: map[string]string{"svc": "{{var.soap_namespace}}"},
					},
					JavaHTTP: dsl.JavaHTTPConfig{
						ObjectSchema: "{{var.java_schema}}",
					},
				},
				Actions: []dsl.Action{{
					Name: "request", Type: test.action, Method: "POST", Target: "https://example.test/orders",
				}},
			}
			got := AnalyzeVariableReadiness(script, map[string]string{
				"soap_namespace": "seeded",
				"java_schema":    "seeded",
			})
			if test.want == "" {
				if len(got.Missing) != 0 {
					t.Fatalf("readiness = %#v, unrelated protocol config must not gate HTTP", got)
				}
				return
			}
			if !reflect.DeepEqual(got.Missing, []string{test.want}) {
				t.Fatalf("readiness = %#v, want %q unresolved", got, test.want)
			}
		})
	}
}

func TestAnalyzeVariableReadinessParametersExtractorsAndSaveAll(t *testing.T) {
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{Name: "tenant"}},
		Actions: []dsl.Action{
			{
				Name: "list", Type: "http_request", Target: "/{{var.tenant}}/items",
				Extractors: []dsl.Extractor{
					{Name: "items", Source: "body", Type: "regex", Expression: `id=(\d+)`, Mode: "all"},
				},
			},
			{Name: "first", Type: "http_request", Target: "/items/{{var.items_1}}?n={{var.items_count}}"},
			{Name: "bare", Type: "http_request", Target: "/items/{{var.items}}/{{var.absent}}"},
		},
	}

	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"absent", "items"}) {
		t.Fatalf("missing = %#v", got.Missing)
	}
	if !reflect.DeepEqual(got.ByAction["actions[2]"], []string{"absent", "items"}) {
		t.Fatalf("actions[2] missing = %#v", got.ByAction["actions[2]"])
	}
}

func TestAnalyzeVariableReadinessCountsLiveTableResultsAfterTheirStep(t *testing.T) {
	table := func(name, prefix string) dsl.Action {
		return dsl.Action{Name: name, Type: "live_table", Params: map[string]any{"alias": "jobs", "operation": "lease", "result_prefix": prefix}}
	}
	use := func(name, ref string) dsl.Action {
		return dsl.Action{Name: name, Type: "http_request", Target: "/jobs/{{var." + ref + "}}"}
	}
	script := &dsl.Script{Actions: []dsl.Action{
		use("before", "job.values.id"),
		table("lease", "job"),
		use("after", "job.values.id"),
		use("status", "job.status"),
		{Name: "maybe", Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "x", Op: "exists"}, Steps: []dsl.Action{table("inner", "maybe")}}},
		use("unsure", "maybe.record_id"),
		{Name: "twice", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{table("looped", "loop_row")}}},
		use("looped_use", "loop_row.values.id"),
		{Name: "lanes", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "a", Steps: []dsl.Action{table("lane", "lane_row")}}, {Name: "b", Steps: []dsl.Action{use("noop", "job.status")}}}}},
		use("lane_use", "parallel.a.lane_row.status"),
		{Name: "either", Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "x", Op: "exists"}, Steps: []dsl.Action{table("then", "both")}, Else: []dsl.Action{table("else", "both"), table("else_only", "one")}}},
		use("both_use", "both.status"),
		use("one_use", "one.status"),
	}}
	got := AnalyzeVariableReadiness(script, nil)
	// Only the use before the step, and those after a step that may not run, lack a value.
	if !reflect.DeepEqual(got.Missing, []string{"job.values.id", "maybe.record_id", "one.status"}) || len(got.ByAction["actions[0]"]) != 1 {
		t.Fatalf("missing = %#v by action = %#v", got.Missing, got.ByAction)
	}
}

func TestAnalyzeVariableReadinessMatchesRuntimeParameterCaseFolding(t *testing.T) {
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{Name: "Token"}},
		Actions: []dsl.Action{{
			Name: "use", Type: "http_request", Target: "/{{var.TOKEN}}",
		}},
	}

	if got := AnalyzeVariableReadiness(script, nil); len(got.Missing) != 0 {
		t.Fatalf("missing = %#v, parameter names resolve case-insensitively at runtime", got.Missing)
	}
}

func TestAnalyzeVariableReadinessSequenceChildrenAreOrdered(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{{
			Name: "sequence", Type: "http_sequence", Method: "SEQUENCE", Target: "/",
			Params: map[string]any{"requests": []any{
				map[string]any{
					"name": "login", "type": "POST", "method": "POST", "target": "/login/{{var.seed}}",
					"extractors": []any{map[string]any{
						"name": "token", "source": "body", "type": "jsonpath", "expression": "$.token",
					}},
				},
				map[string]any{
					"name": "list", "type": "GET", "method": "GET", "target": "/list?t={{var.token}}",
					"extractors": []any{map[string]any{
						"name": "items", "source": "body", "type": "regex", "expression": "id=(\\d+)", "mode": "all",
					}},
				},
			}},
		}, {
			Name: "use", Type: "http_request", Target: "/{{var.items_1}}/{{var.items_count}}",
		}},
	}

	got := AnalyzeVariableReadiness(script, map[string]string{"seed": "ready"})
	if len(got.Missing) != 0 {
		t.Fatalf("missing = %#v", got.Missing)
	}
}

func TestAnalyzeVariableReadinessDoesNotPromoteOptionalBranchExtractor(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{
			Name: "conditional", Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.ready}}", Op: "eq", Right: "yes"},
				Steps: []dsl.Action{{
					Name: "then", Type: "http_request", Target: "/then",
					Extractors: []dsl.Extractor{{Name: "branch_value", Source: "body", Type: "regex", Expression: "(.*)"}},
				}},
				Else: []dsl.Action{{Name: "else", Type: "http_request", Target: "/else"}},
			},
		},
		{Name: "after", Type: "http_request", Target: "/{{var.branch_value}}"},
	}}

	got := AnalyzeVariableReadiness(script, map[string]string{"ready": "yes"})
	if !reflect.DeepEqual(got.Missing, []string{"branch_value"}) {
		t.Fatalf("missing = %#v", got.Missing)
	}
	if !reflect.DeepEqual(got.ByAction["actions[1]"], []string{"branch_value"}) {
		t.Fatalf("actions[1] missing = %#v", got.ByAction["actions[1]"])
	}
}

func TestAnalyzeVariableReadinessPromotesExtractorProducedByBothBranches(t *testing.T) {
	extractor := dsl.Extractor{Name: "branch_value", Source: "body", Type: "regex", Expression: "(.*)"}
	script := &dsl.Script{Actions: []dsl.Action{
		{
			Name: "conditional", Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.ready}}", Op: "eq", Right: "yes"},
				Steps: []dsl.Action{{
					Name: "then", Type: "http_request", Target: "/then",
					Extractors: []dsl.Extractor{extractor},
				}},
				Else: []dsl.Action{{
					Name: "else", Type: "http_request", Target: "/else",
					Extractors: []dsl.Extractor{extractor},
				}},
			},
		},
		{Name: "after", Type: "http_request", Target: "/{{var.branch_value}}"},
	}}

	if got := AnalyzeVariableReadiness(script, map[string]string{"ready": "yes"}); len(got.Missing) != 0 {
		t.Fatalf("missing = %#v", got.Missing)
	}
}

func TestAnalyzeVariableReadinessLoopExtractorRequiresGuaranteedIteration(t *testing.T) {
	tests := []struct {
		name        string
		control     *dsl.ControlFlow
		wantMissing bool
		seeded      map[string]string
	}{
		{
			name: "positive fixed count",
			control: &dsl.ControlFlow{
				Count: 1,
			},
		},
		{
			name: "do while",
			control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.keep_going}}", Op: "eq", Right: "yes"},
				DoWhile:   true,
			},
			seeded: map[string]string{"keep_going": "yes"},
		},
		{
			name: "pre-test condition",
			control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.keep_going}}", Op: "eq", Right: "yes"},
			},
			wantMissing: true,
			seeded:      map[string]string{"keep_going": "yes"},
		},
		{
			name: "for each",
			control: &dsl.ControlFlow{
				Count:   1,
				ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row"},
			},
			wantMissing: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			control := *tt.control
			control.Steps = []dsl.Action{{
				Name: "loop request", Type: "http_request", Target: "/loop",
				Extractors: []dsl.Extractor{{
					Name: "loop_value", Source: "body", Type: "regex", Expression: "(.*)",
				}},
			}}
			script := &dsl.Script{Actions: []dsl.Action{
				{Name: "loop", Type: "control_loop", Control: &control},
				{Name: "after", Type: "http_request", Target: "/{{var.loop_value}}"},
			}}

			got := AnalyzeVariableReadiness(script, tt.seeded)
			if tt.wantMissing {
				if !reflect.DeepEqual(got.Missing, []string{"loop_value"}) {
					t.Fatalf("missing = %#v", got.Missing)
				}
				return
			}
			if len(got.Missing) != 0 {
				t.Fatalf("missing = %#v", got.Missing)
			}
		})
	}
}

func TestAnalyzeVariableReadinessRecognizesOAuthTokenOutput(t *testing.T) {
	script := &dsl.Script{
		Init: []dsl.Action{{
			Name: "token", Type: "http_oauth2_token", Target: "https://auth.example.test/token",
			Params: map[string]any{"save_as": "orders_token"},
		}},
		Actions: []dsl.Action{{
			Name: "orders", Type: "request", Method: "GET", Target: "/orders",
			Auth: &dsl.AuthConfig{Type: "bearer", Token: "{{var.orders_token}}"},
		}},
	}

	if got := AnalyzeVariableReadiness(script, nil); len(got.Missing) != 0 {
		t.Fatalf("missing = %#v, OAuth save_as must publish the token variable", got.Missing)
	}
}

func TestAnalyzeVariableReadinessIgnoresNonExecutedWrapperExtractors(t *testing.T) {
	tests := []struct {
		name    string
		wrapper dsl.Action
	}{
		{
			name: "control if owner",
			wrapper: dsl.Action{
				Name: "conditional", Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
					Steps:     []dsl.Action{{Name: "then", Type: "http_request", Method: "GET", Target: "/then"}},
					Else:      []dsl.Action{{Name: "else", Type: "http_request", Method: "GET", Target: "/else"}},
				},
			},
		},
		{
			name: "control loop owner",
			wrapper: dsl.Action{
				Name: "loop", Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 1,
					Steps: []dsl.Action{{Name: "step", Type: "http_request", Method: "GET", Target: "/step"}},
				},
			},
		},
		{
			name: "http sequence owner",
			wrapper: dsl.Action{
				Name: "sequence", Type: "http_sequence", Method: "SEQUENCE", Target: "/",
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "child", Type: "http_request", Method: "GET", Target: "/child",
				}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := tt.wrapper
			wrapper.Extractors = []dsl.Extractor{{
				Name: "wrapper_token", Source: "body", Type: "jsonpath", Expression: "$.token",
			}}
			script := &dsl.Script{Actions: []dsl.Action{
				wrapper,
				{
					Name: "use token", Type: "http_request", Method: "GET", Target: "/profile",
					Auth: &dsl.AuthConfig{Type: "bearer", Token: "{{var.wrapper_token}}"},
				},
			}}

			got := AnalyzeVariableReadiness(script, nil)
			if !reflect.DeepEqual(got.Missing, []string{"wrapper_token"}) {
				t.Fatalf("missing = %#v, wrapper extractors do not execute", got.Missing)
			}
			if !reflect.DeepEqual(got.ByAction["actions[1]"], []string{"wrapper_token"}) {
				t.Fatalf("actions[1] missing = %#v", got.ByAction["actions[1]"])
			}
		})
	}
}

func TestFinalizeAddsDurableMissingVariableGate(t *testing.T) {
	script := &dsl.Script{
		Name: "draft", Version: "1.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/{{var.order_id}}"}},
	}

	result, err := (&Draft{Script: script}).Finalize(FinalizeOptions{})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_missing_variables" {
		t.Fatalf("execution_blocker = %#v", action.Metadata["execution_blocker"])
	}
	if !reflect.DeepEqual(action.Metadata["missing_variables"], []string{"order_id"}) {
		t.Fatalf("missing_variables = %#v", action.Metadata["missing_variables"])
	}
	if result.Readiness.ReplayReady {
		t.Fatal("missing-variable draft must not be replay ready")
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %#v", issues)
	}
}

func TestRecordingControlLocalsAreScopedDuringReadiness(t *testing.T) {
	request := func(target string) dsl.Action { return dsl.Action{Type: "request", Method: "GET", Target: target} }
	loop := dsl.Action{Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{request("/{{var.loop_index}}"), {Type: "control_loop", Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row"}, Steps: []dsl.Action{request("/{{var.row.value}}/{{var.loop_index}}")}}}}, Condition: &dsl.Condition{Left: "{{var.loop_index}}", Op: "lte", Right: "2"}}}
	for _, tc := range []struct {
		name    string
		actions []dsl.Action
		missing []string
	}{
		{"nested locals", []dsl.Action{loop}, nil},
		{"locals do not escape", []dsl.Action{loop, request("/{{var.loop_index}}/{{var.row.value}}")}, []string{"loop_index", "row.value"}},
		{"other references still fail", []dsl.Action{loop, request("/{{var.unbound}}")}, []string{"unbound"}},
		{"browser extraction", []dsl.Action{{Type: "browser_evaluate_js", Target: "document.title"}, request("/{{var.result}}")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{DataSources: []dsl.DataSource{{Name: "rows", Type: "csv", InlineData: "value\na", FirstRowHeader: true}}, Actions: tc.actions}
			if got := MissingVariables(script, nil); !reflect.DeepEqual(got, tc.missing) {
				t.Fatalf("missing=%v want %v", got, tc.missing)
			}
		})
	}
}

func TestParallelReadinessIsolatesBranchExtractors(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{{Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
		{Name: "left", Steps: []dsl.Action{{Type: "request", Target: "/", Extractors: []dsl.Extractor{{Name: "token", Type: "jsonpath", Expression: "$.token"}}}}},
		{Name: "right", Steps: []dsl.Action{{Type: "request", Target: "/{{var.token}}/{{var.parallel.left.token}}"}}},
	}}}, {Type: "request", Target: "/{{var.parallel.left.token}}"}}}
	got := AnalyzeVariableReadiness(script, nil)
	if !reflect.DeepEqual(got.Missing, []string{"parallel.left.token", "token"}) {
		t.Fatalf("missing=%v", got.Missing)
	}
	if len(got.ByAction["actions[1]"]) != 0 {
		t.Fatal("joined branch extraction unavailable")
	}
}

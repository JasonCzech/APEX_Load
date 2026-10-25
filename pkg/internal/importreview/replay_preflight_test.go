package importreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	convertreview "github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
)

func TestReplayPreflightListsLateProducedVariables(t *testing.T) {
	script := &dsl.Script{Name: "order", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example.test"}, Actions: []dsl.Action{
		{Name: "store", Type: "GET", Method: "GET", Target: "/stores/{{var.C_StoreId}}/{{var.c_absent}}"},
		{Name: "stores", Type: "GET", Method: "GET", Target: "/stores", Extractors: []dsl.Extractor{{Name: "C_StoreId", Source: "body", Type: "regex", Expression: `id=(\d+)`}}},
	}}
	preflight := BuildReplayPreflight(script, nil, nil, nil)
	if !preflight.Valid ||
		!slices.Equal(preflight.MissingVariables, []string{"C_StoreId", "c_absent"}) ||
		!slices.Equal(preflight.PlaceholderVariables, []string{"C_StoreId", "c_absent"}) ||
		!slices.Equal(preflight.LateProducedVariables, []string{"C_StoreId"}) {
		t.Fatalf("preflight = %+v", preflight)
	}
	payload, err := json.Marshal(preflight)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"late_produced_variables":["C_StoreId"]`) {
		t.Fatalf("late_produced_variables missing from %s", payload)
	}
	cloned := CloneReplayPreflight(preflight)
	cloned.LateProducedVariables[0] = "changed"
	if preflight.LateProducedVariables[0] != "C_StoreId" {
		t.Fatal("cloned preflight shares late_produced_variables")
	}
	if clean := BuildReplayPreflight(&dsl.Script{Name: "clean", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example.test"}, Actions: []dsl.Action{{Name: "home", Type: "GET", Method: "GET", Target: "/"}}}, nil, nil, nil); clean.LateProducedVariables != nil {
		t.Fatalf("clean script late_produced_variables = %#v", clean.LateProducedVariables)
	}
}

// A blocking name that an undeclared {{param.*}} use or a protocol
// configuration reference blocks is listed apart, so a client words its fix
// (declare a parameter, write a literal configuration value) rather than
// producer order, even where an unrelated {{var.*}} use of the name is late.
func TestReplayPreflightListsParamAndConfigBlockingVariables(t *testing.T) {
	script := &dsl.Script{Name: "kinds", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://{{var.host}}"}, Actions: []dsl.Action{
		{Name: "use", Type: "GET", Method: "GET", Target: "/r/{{param.region}}/{{var.region}}/{{var.host}}"},
		{Name: "late", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"h", "host"}}},
		{Name: "late region", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"r", "region"}}},
	}}
	preflight := BuildReplayPreflight(script, nil, nil, nil)
	if !slices.Equal(preflight.BlockingVariables, []string{"host", "region"}) ||
		!slices.Equal(preflight.LateProducedVariables, []string{"host", "region"}) ||
		!slices.Equal(preflight.ParamBlockingVariables, []string{"region"}) ||
		!slices.Equal(preflight.ConfigBlockingVariables, []string{"host"}) {
		t.Fatalf("preflight = %+v", preflight)
	}
	payload, err := json.Marshal(preflight)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"param_blocking_variables":["region"]`, `"config_blocking_variables":["host"]`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("%s missing from %s", want, payload)
		}
	}
	cloned := CloneReplayPreflight(preflight)
	cloned.ParamBlockingVariables[0], cloned.ConfigBlockingVariables[0] = "changed", "changed"
	if preflight.ParamBlockingVariables[0] != "region" || preflight.ConfigBlockingVariables[0] != "host" {
		t.Fatal("cloned preflight shares param_blocking_variables or config_blocking_variables")
	}
	// A computed preflight always sends both lists, empty when none, so a
	// client trusts them rather than analyzing the script again. A preflight
	// stamped by an older controller has neither, and still omits them.
	clean := BuildReplayPreflight(&dsl.Script{Name: "clean", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example.test"}, Actions: []dsl.Action{{Name: "home", Type: "GET", Method: "GET", Target: "/"}}}, nil, nil, nil)
	if clean.ParamBlockingVariables == nil || len(clean.ParamBlockingVariables) != 0 || clean.ConfigBlockingVariables == nil || len(clean.ConfigBlockingVariables) != 0 {
		t.Fatalf("clean script kinds = %#v / %#v", clean.ParamBlockingVariables, clean.ConfigBlockingVariables)
	}
	for _, sent := range []*ReplayPreflight{clean, CloneReplayPreflight(clean)} {
		payload, err := json.Marshal(sent)
		if err != nil || !strings.Contains(string(payload), `"param_blocking_variables":[]`) || !strings.Contains(string(payload), `"config_blocking_variables":[]`) || strings.Contains(string(payload), `"blocking_variables"`) {
			t.Fatalf("clean preflight JSON = %s (%v)", payload, err)
		}
		var stored ReplayPreflight
		if err := json.Unmarshal(payload, &stored); err != nil || stored.ParamBlockingVariables == nil || stored.ConfigBlockingVariables == nil {
			t.Fatalf("stored clean preflight lost its empty kinds: %#v (%v)", stored, err)
		}
	}
	var older ReplayPreflight
	if err := json.Unmarshal([]byte(`{"valid":false,"blocking_variables":["region"]}`), &older); err != nil {
		t.Fatal(err)
	}
	if cloned := CloneReplayPreflight(&older); cloned.ParamBlockingVariables != nil || cloned.ConfigBlockingVariables != nil {
		t.Fatalf("older stamp gained kinds: %#v / %#v", cloned.ParamBlockingVariables, cloned.ConfigBlockingVariables)
	}
	if payload, err := json.Marshal(CloneReplayPreflight(&older)); err != nil || strings.Contains(string(payload), "param_blocking_variables") || strings.Contains(string(payload), "config_blocking_variables") {
		t.Fatalf("older stamp JSON = %s (%v)", payload, err)
	}
}

// Source steps publish their outputs (exact and lowercase) before the next
// step, so none of them is sent as placeholder text.
func TestReplayPreflightPlaceholdersExcludeSourceStepOutputs(t *testing.T) {
	script := &dsl.Script{Name: "source steps", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example.test"}, Actions: []dsl.Action{
		{Name: "token", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"abc", "C_Token"}}},
		{Name: "token length", Type: "source_parameter", Params: map[string]any{"function": "web_save_param_length", "args": []any{"C_Token"}}},
		{Name: "token url", Type: "source_parameter", Params: map[string]any{"function": "web_convert_param", "save_param": "c_Token_URL", "source_string": "{{var.c_token}}"}},
		{Name: "offers", Type: "source_json", Params: map[string]any{"function": "lr_eval_json", "json_object": "offers", "buffer": `{"offers":[{"id":1}]}`}},
		{Name: "offer ids", Type: "source_json", Params: map[string]any{"function": "lr_json_get_values", "json_object": "offers", "value_param": "C_OfferIds", "query": "$.offers[*].id", "select_all": "Yes"}},
		{Name: "use", Type: "GET", Method: "GET", Target: "/offers/{{var.c_offerids_3}}?n={{var.c_offerids_count}}&t={{var.C_Token}}&l={{var.c_token_length}}&u={{var.c_token_url}}&p={{var.c_absent}}"},
	}}
	preflight := BuildReplayPreflight(script, nil, nil, nil)
	if !preflight.Valid ||
		!slices.Equal(preflight.MissingVariables, []string{"c_absent"}) ||
		!slices.Equal(preflight.PlaceholderVariables, []string{"c_absent"}) ||
		len(preflight.BlockingVariables) != 0 || len(preflight.LateProducedVariables) != 0 {
		t.Fatalf("preflight = %+v", preflight)
	}
}

// Import review and replay preflight share one execution-order analysis, so
// the values import review asks about are exactly the preflight's: the same
// missing names (the undeclared {{param.*}} namespace included), the same
// blocking subset, and "producer order" for exactly the late-produced ones.
// The one difference is protocol configuration: replay readiness reports a
// reference there, and import review leaves it to replay readiness.
func TestImportDependencyFindingsMatchReplayPreflightOverReadinessCorpus(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "dsl", "varflow", "testdata", "readiness_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string     `json:"name"`
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(payload, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("readiness corpus is empty")
	}
	for _, test := range cases {
		test := test
		t.Run(test.Name, func(t *testing.T) {
			assertDependencyFindingsMatchPreflight(t, &test.Script)
		})
	}
}

func assertDependencyFindingsMatchPreflight(t *testing.T, script *dsl.Script) {
	t.Helper()
	findings, err := convertreview.AnalyzeDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	preflight := BuildReplayPreflight(script, nil, nil, nil)
	// The names some step uses, and those a step's use blocks, apart from
	// protocol configuration references.
	used, blockedByUse := map[string]bool{}, map[string]bool{}
	for _, miss := range varflow.Analyze(script, nil).Misses {
		if !miss.Config {
			used[miss.Reference.Name] = true
			blockedByUse[miss.Reference.Name] = blockedByUse[miss.Reference.Name] || miss.Blocking
		}
	}
	configBlocking := requestimport.AnalyzeVariableReadiness(script, nil).ConfigBlocking
	withoutConfigOnly := func(field string, names []string, kept map[string]bool) []string {
		var out []string
		for _, name := range names {
			if kept[name] {
				out = append(out, name)
			} else if !slices.Contains(configBlocking, name) {
				t.Errorf("preflight %s lists %s, which neither a step nor protocol configuration uses", field, name)
			}
		}
		return out
	}
	missing, blocking, late := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, finding := range findings {
		conditional := strings.HasPrefix(finding.Code, "conditional_producer:")
		if !conditional && !strings.HasPrefix(finding.Code, "missing_producer:") {
			continue
		}
		missing[finding.Element] = struct{}{}
		if finding.Gate == convertreview.GateExecution {
			blocking[finding.Element] = struct{}{}
		}
		if conditional {
			late[finding.Element] = struct{}{}
		}
	}
	for _, check := range []struct {
		field string
		got   map[string]struct{}
		want  []string
	}{
		{"missing_variables", missing, withoutConfigOnly("missing_variables", preflight.MissingVariables, used)},
		{"blocking_variables", blocking, withoutConfigOnly("blocking_variables", preflight.BlockingVariables, blockedByUse)},
		{"late_produced_variables", late, preflight.LateProducedVariables},
	} {
		if got := SortedSetKeys(check.got); !slices.Equal(got, check.want) {
			t.Errorf("import review %s = %#v, preflight = %#v (findings %+v)", check.field, got, check.want, findings)
		}
	}
}

// readinessParityFixture holds synthetic enterprise-style drafts after
// RefreshDependencies, with the findings stamped on them and the replay
// preflight the server builds for them. The dashboard's local preflight and
// import review tests read it to stay equal to the server. Set
// UPDATE_READINESS_PARITY_FIXTURE=1 to rewrite it.
const readinessParityFixture = "testdata/readiness_parity.json"

type readinessParityCase struct {
	Name      string                  `json:"name"`
	Script    *dsl.Script             `json:"script"`
	Findings  []convertreview.Finding `json:"findings"`
	Preflight *ReplayPreflight        `json:"preflight"`
}

func TestReadinessParityFixtureIsFresh(t *testing.T) {
	var built []readinessParityCase
	for _, test := range readinessParityScripts() {
		// Round-trip first: the fixture must describe a stored draft, whose
		// metadata numbers decode as JSON numbers.
		script := roundTripParityScript(t, test.script)
		findings, err := convertreview.RefreshDependencies(script)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		preflight := BuildReplayPreflight(script, nil, ValidateScript(script), nil)
		t.Run(test.name, func(t *testing.T) {
			assertDependencyFindingsMatchPreflight(t, script)
			codes := make([]string, 0, len(findings))
			for _, finding := range findings {
				codes = append(codes, finding.ActionPath+" "+finding.Code+" "+string(finding.Gate))
			}
			sort.Strings(codes)
			if !slices.Equal(codes, test.codes) {
				t.Errorf("findings = %#v, want %#v", codes, test.codes)
			}
			if preflight.Valid != test.valid || len(ValidateScript(script)) != 0 {
				t.Errorf("valid = %v (want %v), validation = %v, issues = %v", preflight.Valid, test.valid, ValidateScript(script), preflight.Issues)
			}
		})
		built = append(built, readinessParityCase{Name: test.name, Script: script, Findings: findings, Preflight: preflight})
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(built); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_READINESS_PARITY_FIXTURE") == "1" {
		if err := os.WriteFile(readinessParityFixture, encoded.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	current, err := os.ReadFile(readinessParityFixture)
	if err != nil {
		t.Fatalf("read %s: %v (set UPDATE_READINESS_PARITY_FIXTURE=1 to write it)", readinessParityFixture, err)
	}
	if !bytes.Equal(current, encoded.Bytes()) {
		t.Fatalf("%s is stale; set UPDATE_READINESS_PARITY_FIXTURE=1 and rerun", readinessParityFixture)
	}
}

func roundTripParityScript(t *testing.T, script *dsl.Script) *dsl.Script {
	t.Helper()
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	var decoded dsl.Script
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	return &decoded
}

type readinessParityScript struct {
	name   string
	script *dsl.Script
	// codes are "<action path> <code> <gate>" for every stamped finding.
	codes []string
	valid bool
}

// readinessParityScripts are synthetic drafts shaped like LoadRunner archive
// conversions: method-typed requests and source steps carrying their source
// position, and gates stamped the way the converter stamps them.
func readinessParityScripts() []readinessParityScript {
	at := func(file string, line int) map[string]any {
		return map[string]any{"source_file": file, "source_line": line}
	}
	request := func(name, method, target string, line int, extract ...string) dsl.Action {
		action := dsl.Action{Name: name, Type: method, Method: method, Target: target, Metadata: at("Action.c", line)}
		for _, variable := range extract {
			action.Extractors = append(action.Extractors, dsl.Extractor{Name: variable, Source: "body", Type: "regex", Expression: variable + `=(\w+)`})
		}
		return action
	}
	step := func(name, actionType string, line int, params map[string]any) dsl.Action {
		return dsl.Action{Name: name, Type: actionType, Params: params, Metadata: at("Action.c", line)}
	}
	gated := func(action dsl.Action, findings ...convertreview.Finding) dsl.Action {
		convertreview.ApplyToAction(&action, findings)
		return action
	}
	tlsGate := func(line int) dsl.Action {
		metadata := at("vuser_init.c", line)
		metadata["source_function"] = "web_set_sockets_option"
		return gated(dsl.Action{Name: "Select_supported_TLS", Type: "source_review", Metadata: metadata}, convertreview.Finding{
			Code: "legacy_tls_replacement", ReasonCode: "legacy_tls_replacement", Severity: diag.Blocker, Gate: convertreview.GateExecution,
			Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("vuser_init.c:%d", line), Resolution: "select_tls",
			Detail: "Choose a supported TLS replacement for legacy SSL; this is an intentional migration.",
		})
	}
	staleDependency := func(code string, gate convertreview.GateScope) convertreview.Finding {
		severity := diag.Warning
		if gate == convertreview.GateExecution {
			severity = diag.Blocker
		}
		return convertreview.Finding{Code: code, ReasonCode: code, Severity: severity, Gate: gate, Source: "enterprise_script_archive", SourceRef: "Action.c:2", Resolution: "bind_producer", Detail: "Stamped by an earlier import."}
	}
	script := func(name string, init, actions []dsl.Action) *dsl.Script {
		return &dsl.Script{Name: name, Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example.test"}, Init: init, Actions: actions}
	}
	when := func(variable, value string) *dsl.Condition {
		return &dsl.Condition{Left: "{{var." + variable + "}}", Op: "eq", Right: value}
	}

	sourceSteps := script("source_steps", nil, []dsl.Action{
		step("token", "source_parameter", 2, map[string]any{"function": "lr_save_string", "args": []any{"abc", "C_Token"}}),
		step("token length", "source_parameter", 3, map[string]any{"function": "web_save_param_length", "args": []any{"C_Token"}}),
		step("token url", "source_parameter", 4, map[string]any{"function": "web_convert_param", "save_param": "c_Token_URL", "source_string": "{{var.c_token}}", "args": []any{"c_Token_URL", "SourceEncoding=PLAIN", "TargetEncoding=URL"}}),
		step("offers", "source_json", 5, map[string]any{"function": "lr_eval_json", "json_object": "offers", "buffer": `{"offers":[{"id":1},{"id":2},{"id":3}]}`}),
		step("offer ids", "source_json", 6, map[string]any{"function": "lr_json_get_values", "json_object": "offers", "value_param": "C_OfferIds", "query": "$.offers[*].id", "select_all": "Yes", "save_param": "C_OfferIds", "save_all": true}),
		step("offers text", "source_json", 7, map[string]any{"function": "lr_json_stringify", "json_object": "offers", "output_param": "C_OffersText"}),
		request("offer", "POST", "/offers/{{var.c_offerids_3}}?n={{var.c_offerids_count}}&first={{var.C_OfferIds_1}}", 8),
		{Name: "offer body", Type: "POST", Method: "POST", Target: "/offers", Body: "token={{var.C_Token}}&len={{var.c_token_length}}&url={{var.c_token_url}}&doc={{var.c_offerstext}}", Metadata: at("Action.c", 9)},
	})
	producerOrder := script("producer_order", nil, []dsl.Action{
		request("store", "GET", "/stores/{{var.C_StoreId}}", 2),
		step("cart document", "source_json", 3, map[string]any{"function": "lr_eval_json", "json_object": "cart", "buffer": "{{var.c_cart_body}}"}),
		request("stores", "GET", "/stores", 4, "C_StoreId"),
		request("cart", "GET", "/cart", 5, "c_cart_body"),
		request("promo", "GET", "/promo/{{var.c_promo}}", 6),
	})
	parameters := script("parameter_namespace", nil, []dsl.Action{
		request("store", "GET", "/stores/{{param.store}}/{{var.STORE}}/{{param.region}}", 2),
	})
	parameters.Parameters = []dsl.Parameter{{Name: "Store", Type: "list", Values: []string{"s1"}, Update: "once"}}
	nested := script("nested_producers", nil, []dsl.Action{
		request("mode", "GET", "/mode", 2, "c_mode"),
		{Name: "only a", Type: "control_if", Metadata: at("Action.c", 3), Control: &dsl.ControlFlow{Condition: when("c_mode", "a"), Steps: []dsl.Action{
			request("a", "GET", "/a", 4, "c_only_if"),
		}}},
		{Name: "pages", Type: "control_loop", Metadata: at("Action.c", 5), Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{
			request("page", "GET", "/page/{{var.loop_index}}", 6, "c_page"),
		}}},
		{Name: "sequence", Type: "http_sequence", Metadata: at("Action.c", 7), Params: map[string]any{"requests": []any{
			map[string]any{"name": "first", "type": "GET", "method": "GET", "target": "/seq/1", "extractors": []any{map[string]any{"name": "c_seq", "source": "body", "type": "regex", "expression": `c_seq=(\w+)`}}, "metadata": at("Action.c", 8)},
			map[string]any{"name": "second", "type": "GET", "method": "GET", "target": "/seq/2?x={{var.c_seq}}", "metadata": at("Action.c", 9)},
		}}},
		request("use", "GET", "/use/{{var.c_only_if}}/{{var.c_page}}/{{var.c_seq}}", 10),
	})
	stale := script("stale_dependency_codes", nil, []dsl.Action{
		request("login", "GET", "/login", 2, "c_session"),
		gated(request("home", "GET", "/home?s={{var.c_session}}", 3), staleDependency("missing_producer:c_session", convertreview.GatePromotion)),
		gated(request("cart", "GET", "/cart", 4), staleDependency("missing_producer:c_removed", convertreview.GateExecution)),
	})
	tls := script("legacy_tls_gates", []dsl.Action{
		tlsGate(3),
		tlsGate(4),
		request("login", "GET", "/login", 5, "C_Session"),
	}, []dsl.Action{
		request("home", "GET", "/home?session={{var.C_Session}}", 2),
	})
	// An imported web_set_user leaves its user in the script's auth settings,
	// and a default header names a value no step sets. Configuration is
	// applied before any step runs, so both block replay as configuration;
	// import review stamps only the request's own use of the header value.
	configured := script("protocol_configuration", nil, []dsl.Action{
		request("login", "POST", "/login", 2),
		request("tenant", "GET", "/tenants/{{var.c_tenant}}", 3),
	})
	configured.Config.HTTP.Auth = dsl.AuthConfig{Type: "basic", Username: "{{var.pUser}}", Hosts: []string{"shop.example.test"}}
	configured.Config.HTTP.DefaultHeaders = map[string]string{"X-Tenant": "{{var.c_tenant}}"}
	configured.Parameters = []dsl.Parameter{{Name: "pUser", Type: "list", Values: []string{"perf_user_01"}, Update: "once"}}
	// One request held back for two reasons: each blocks replay on its own.
	held := func(code, resolution, detail string) convertreview.Finding {
		return convertreview.Finding{Code: code, ReasonCode: code, Severity: diag.Blocker, Gate: convertreview.GateExecution, Source: "enterprise_script_archive", SourceRef: "Action.c:2", Resolution: resolution, Detail: detail}
	}
	twoCodes := script("two_code_gate", nil, []dsl.Action{
		gated(request("submit order", "POST", "/orders", 2),
			held("restored_body_required", "restore_body", "The recorded request body was masked and must be restored."),
			held("request_body_xml_json_content_type_mismatch", "", "The request body is XML, but the Content-Type header declares JSON."),
		),
	})

	return []readinessParityScript{
		{name: "source step outputs supply their consumers in both spellings", script: sourceSteps, valid: true},
		{name: "producer order and absent values", script: producerOrder, codes: []string{
			"actions[0] conditional_producer:C_StoreId promotion",
			"actions[1] conditional_producer:c_cart_body execution",
			"actions[4] missing_producer:c_promo promotion",
		}},
		{name: "an undeclared parameter blocks and a declared one matches any case", script: parameters, codes: []string{
			"actions[0] missing_producer:region execution",
		}},
		{name: "nested producers follow execution order", script: nested, valid: true, codes: []string{
			"actions[4] conditional_producer:c_only_if promotion",
		}},
		{name: "stale dependency codes are refreshed away", script: stale, valid: true},
		{name: "legacy TLS gates in init block replay", script: tls, codes: []string{
			"init[0] legacy_tls_replacement execution",
			"init[1] legacy_tls_replacement execution",
		}},
		{name: "protocol configuration references block replay as configuration, not as step gates", script: configured, codes: []string{
			"actions[1] missing_producer:c_tenant promotion",
		}},
		{name: "a request gated by two codes blocks replay once per code", script: twoCodes, codes: []string{
			"actions[0] request_body_xml_json_content_type_mismatch execution",
			"actions[0] restored_body_required execution",
		}},
	}
}

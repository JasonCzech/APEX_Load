package importreview

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

func TestDependenciesIgnoreCommentsAndTrackPrecedingProducers(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Parameters: []dsl.Parameter{{Name: "known"}}, Init: []dsl.Action{{Type: "GET", Target: "https://example.test", Extractors: []dsl.Extractor{{Name: "token"}}}}, Actions: []dsl.Action{{Type: "source_parameter", Params: map[string]any{"save_param": "encoded", "source_string": "{{var.token}}"}}, {Type: "POST", Target: "https://example.test/{{var.encoded}}", Body: "{{param.known}} {{var.absent}}", Metadata: map[string]any{"raw": "// producer absent"}}}}
	findings, err := AnalyzeDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	// An unresolved {{var.*}} is replayed as placeholder text with a warning.
	if len(findings) != 1 || findings[0].Code != "missing_producer:absent" || findings[0].Gate != GatePromotion || findings[0].Severity != "warning" {
		t.Fatalf("findings=%+v", findings)
	}
}
func TestDependenciesNestedProducerRequiresReview(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "GET", Target: "/", Extractors: []dsl.Extractor{{Name: "conditional"}}}}}}, {Type: "GET", Target: "/{{var.conditional}}"}}}
	findings, err := AnalyzeDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Gate != GatePromotion {
		t.Fatalf("findings=%+v", findings)
	}
}

func TestSourceParameterLowercaseAliasIsReachable(t *testing.T) {
	s := &dsl.Script{Init: []dsl.Action{{Type: "source_parameter", Params: map[string]any{"save_param": "MixedCase"}}}, Actions: []dsl.Action{{Type: "GET", Target: "https://example.test/{{var.mixedcase}}"}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 0 {
		t.Fatalf("source alias was lost: %v %+v", err, findings)
	}
	s.Init[0] = dsl.Action{Type: "GET", Extractors: []dsl.Extractor{{Name: "MixedCase"}}}
	findings, err = AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("extractor incorrectly acquired case alias: %v %+v", err, findings)
	}
}

// A save-all extractor publishes values_N before the consumer runs, so
// values_2 needs no review; only the parameter-only reference remains.
func TestDependencyNamespacesAndArrayProducer(t *testing.T) {
	s := &dsl.Script{Init: []dsl.Action{{Type: "GET", Extractors: []dsl.Extractor{{Name: "token"}, {Name: "values", Mode: "all"}}}}, Actions: []dsl.Action{{Type: "GET", Target: "https://example.test/{{param.token}}/{{var.values_2}}"}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	if f := findings[0]; f.Code != "missing_producer:token" || f.Gate != GateExecution {
		t.Fatalf("extractor satisfied parameter-only namespace: %+v", f)
	}
	// A {{param.*}} use blocks any step, not only a request, so the detail
	// says replay refuses to start. Only a declared parameter resolves it: a
	// data source alone publishes data.<source>.<column>, never a parameter.
	if want := "No parameter supplies token, and replay refuses to start with an unresolved parameter. Declare a script parameter named token (it can read its values from a data source)."; findings[0].Detail != want {
		t.Fatalf("parameter detail = %q, want %q", findings[0].Detail, want)
	}
}

func TestDependencyRefreshPreservesUnrelatedLegacyReview(t *testing.T) {
	s := &dsl.Script{Actions: []dsl.Action{{Type: "source_review", Metadata: map[string]any{"review_required": true}}}}
	findings, err := RefreshDependencies(s)
	if err != nil || len(findings) != 1 || findings[0].Code != "import_review_required" {
		t.Fatalf("unrelated review lost: %v %+v", err, findings)
	}
}

func TestResolvingDependencyCannotClearAnEarlierGenericReview(t *testing.T) {
	s := &dsl.Script{Actions: []dsl.Action{{Type: "POST", Body: "{{var.missing}}", Metadata: map[string]any{"review_required": true}}}}
	if _, err := RefreshDependencies(s); err != nil {
		t.Fatal(err)
	}
	s.Parameters = []dsl.Parameter{{Name: "missing"}}
	findings, err := RefreshDependencies(s)
	if err != nil || len(findings) != 1 || findings[0].Code != "import_review_required" {
		t.Fatalf("unrelated review cleared by dependency repair: %v %+v", err, findings)
	}
}

func TestDependencyNamespacesCannotHideExecutionBlocker(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Type: "POST", Body: "{{var.token}} {{param.token}}"},
		{Type: "GET", Extractors: []dsl.Extractor{{Name: "token"}}},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil {
		t.Fatal(err)
	}
	gates := map[string]GateScope{}
	for _, f := range findings {
		gates[f.Code] = f.Gate
	}
	if gates["missing_producer:token"] != GateExecution || gates["conditional_producer:token"] != GatePromotion {
		t.Fatalf("namespace collision hid a dependency: %+v", findings)
	}
}

func TestVerificationTemplateDependenciesAreExecutable(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "source_verification", Params: map[string]any{"operation": "register", "id": "check", "text": "{{var.missing}}", "scope": "body"}, Metadata: map[string]any{"raw_source": "{{var.comment}}"}}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 || findings[0].Code != "missing_producer:missing" || findings[0].Gate != GatePromotion {
		t.Fatalf("verification dependencies=%+v err=%v", findings, err)
	}
}

func TestJSONHelperMissingProducerBlocksExecution(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Type: "source_json", Params: map[string]any{"json_object": "doc", "buffer": "{{var.absent}}"}},
		{Type: "POST", Target: "https://example.test", Body: "{{var.absent}}"},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 2 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	for _, f := range findings {
		want := GatePromotion
		if f.ActionPath == "actions[0]" {
			want = GateExecution
		}
		if f.Code != "missing_producer:absent" || f.Gate != want {
			t.Fatalf("finding at %s = %+v, want gate %s", f.ActionPath, f, want)
		}
	}
}

// lr_json_get_values SelectAll=Yes publishes NAME_1..n and NAME_count under
// the exact ValueParam and its lowercase alias, which is how an imported
// {c_OfferIds_3} reference is spelled.
func TestDependencySelectAllMixedCaseValueParamProducesSlots(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Type: "source_json", Params: map[string]any{"function": "lr_eval_json", "json_object": "offers", "buffer": `{"offers":[{"id":1}]}`}},
		{Type: "source_json", Params: map[string]any{"function": "lr_json_get_values", "json_object": "offers", "value_param": "C_OfferIds", "query": "$.offers[*].id", "select_all": "Yes", "save_param": "C_OfferIds", "save_all": true}},
		{Type: "GET", Target: "https://example.test/{{var.c_offerids_3}}?n={{var.c_offerids_count}}&first={{var.C_OfferIds_1}}"},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 0 {
		t.Fatalf("SelectAll slots reported as unproduced: %+v %v", findings, err)
	}
}

func TestDependencyConsumerBeforeProducerRequiresOrderReview(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Type: "GET", Target: "https://example.test/stores/{{var.C_StoreId}}"},
		{Type: "GET", Target: "https://example.test/stores", Extractors: []dsl.Extractor{{Name: "C_StoreId"}}},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	if f := findings[0]; f.Code != "conditional_producer:C_StoreId" || f.Gate != GatePromotion || f.Severity != "warning" || f.ActionPath != "actions[0]" || f.Element != "C_StoreId" {
		t.Fatalf("consumer before producer = %+v", f)
	}
}

// A JSON helper fails its step without a value, so its use gates execution
// even when the producer only runs too late.
func TestDependencyJSONBufferBeforeItsProducerGatesExecution(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Type: "source_json", Params: map[string]any{"function": "lr_eval_json", "json_object": "doc", "buffer": "{{var.body}}"}},
		{Type: "GET", Target: "https://example.test/doc", Extractors: []dsl.Extractor{{Name: "body"}}},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	if f := findings[0]; f.Code != "conditional_producer:body" || f.Gate != GateExecution || f.Severity != "blocker" || !strings.Contains(f.Detail, "JSON helper") {
		t.Fatalf("late JSON buffer = %+v", f)
	}
}

func TestDependencyProtocolWithoutPlaceholderFallbackGatesExecution(t *testing.T) {
	s := &dsl.Script{Protocol: "grpc", Actions: []dsl.Action{{Type: "grpc_call", Target: "orders.Orders/Get", Body: `{"id":"{{var.order_id}}"}`}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	if f := findings[0]; f.Code != "missing_producer:order_id" || f.Gate != GateExecution || !strings.Contains(f.Detail, "no placeholder fallback") {
		t.Fatalf("non-HTTP missing value = %+v", f)
	}
}

// SOAP sends every request through the HTTP adapter, which sends an
// unresolved {{var.*}} as its placeholder text and warns, so a LoadRunner Web
// Services import stays launchable: each missing value gates promotion only.
func TestDependencySOAPMissGatesOnlyPromotion(t *testing.T) {
	s := &dsl.Script{Protocol: "soap", Actions: []dsl.Action{
		{Type: "POST", Target: "https://example.test/OrderService/{{var.service}}", Headers: map[string]string{"SOAPAction": "{{var.operation}}"}, Body: "<GetOrder><id>{{var.order_id}}</id></GetOrder>"},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 3 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	for _, f := range findings {
		if !strings.HasPrefix(f.Code, "missing_producer:") || f.Gate != GatePromotion || f.Severity != "warning" || !strings.Contains(f.Detail, "Replay sends its placeholder text unchanged and warns") {
			t.Fatalf("SOAP missing value = %+v", f)
		}
	}
}

// Java HTTP sends its target and headers through the HTTP adapter, so a
// missing value there gates promotion. Its stored body is decoded as base64
// first, so a placeholder there fails the step and gates execution, with a
// reason that names the body instead of a missing placeholder fallback.
func TestDependencyJavaHTTPGatesExecutionOnlyForBodyUses(t *testing.T) {
	s := &dsl.Script{Protocol: "java_http", Actions: []dsl.Action{
		{Type: "POST", Target: "https://example.test/invoker/{{var.bean}}", Headers: map[string]string{"X-Session": "{{var.session}}"}, Body: "{{var.serialized}}"},
		{Type: "POST", Target: "https://example.test/invoker", Body: "{{var.token}}"},
		{Type: "POST", Target: "https://example.test/login", Extractors: []dsl.Extractor{{Name: "token"}}},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 4 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	byCode := map[string]Finding{}
	for _, f := range findings {
		byCode[f.Code] = f
	}
	for _, code := range []string{"missing_producer:bean", "missing_producer:session"} {
		if f := byCode[code]; f.Gate != GatePromotion || f.Severity != "warning" || f.ActionPath != "actions[0]" {
			t.Fatalf("%s = %+v", code, f)
		}
	}
	if f := byCode["missing_producer:serialized"]; f.Gate != GateExecution || f.Severity != "blocker" || f.Detail != "No preceding producer supplies serialized, and Java HTTP replay decodes the request body as base64, so placeholder text there fails the step. Bind a producer, parameter, or response extractor." {
		t.Fatalf("Java HTTP body use = %+v", f)
	}
	if f := byCode["conditional_producer:token"]; f.Gate != GateExecution || f.Severity != "blocker" || f.ActionPath != "actions[1]" || f.Detail != "Producer ordering or conditional execution for token requires review. Java HTTP replay decodes the request body as base64, so the step fails when the value is absent." {
		t.Fatalf("late Java HTTP body use = %+v", f)
	}
}

// Configuration is applied before any step runs, so no step and no guided
// repair can supply a reference in it. Import review leaves it to replay
// readiness, which recomputes it from the current configuration on every
// preflight, so replacing the reference with a literal clears it; a stamped
// gate would outlive that edit.
func TestDependencyConfigReferenceIsLeftToReplayReadiness(t *testing.T) {
	for name, config := range map[string]dsl.ScriptConfig{
		"base url":      {BaseURL: "https://{{var.host}}"},
		"auth username": {BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Username: "{{var.host}}"}}},
	} {
		s := &dsl.Script{Protocol: "http", Config: config, Actions: []dsl.Action{
			{Type: "GET", Target: "/login", Extractors: []dsl.Extractor{{Name: "host"}}},
			{Type: "http_sequence", Method: "SEQUENCE", Target: "/", Params: map[string]any{"requests": []any{map[string]any{"type": "GET", "method": "GET", "target": "/home"}}}},
		}}
		findings, err := AnalyzeDependencies(s)
		if err != nil || len(findings) != 0 {
			t.Fatalf("%s: findings=%+v err=%v", name, findings, err)
		}
		if readiness := varflow.Analyze(s, nil); !reflect.DeepEqual(readiness.Blocking, []string{"host"}) || !reflect.DeepEqual(readiness.ConfigBlocking, []string{"host"}) {
			t.Fatalf("%s: readiness blocking = %#v, config blocking = %#v", name, readiness.Blocking, readiness.ConfigBlocking)
		}
	}
}

// A name a configuration reference and an action both use is still reported
// for the action's own use.
func TestDependencyActionUseOfAConfigNameIsStillReported(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://{{var.host}}"}, Actions: []dsl.Action{
		{Type: "GET", Target: "/hosts/{{var.host}}"},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	if f := findings[0]; f.Code != "missing_producer:host" || f.Gate != GatePromotion || strings.Contains(f.Detail, "configuration") {
		t.Fatalf("action use of a config name = %+v", f)
	}
}

// The runtime never resolves the arguments of transaction, timer, metric,
// runtime and rendezvous steps: it uses them literally or only logs them. So
// an LR transaction name or data point read through lr_eval_string is not a
// dependency, whether or not the protocol has a placeholder fallback.
func TestDependencyLiteralEngineStepsReadNothing(t *testing.T) {
	for _, protocol := range []string{"http", "java_vuser"} {
		s := &dsl.Script{Protocol: protocol, Actions: []dsl.Action{
			{Name: "start", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{"{{var.TxName}}"}}},
			{Name: "point", Type: "source_metric", Params: map[string]any{"function": "lr_user_data_point", "args": []any{"m", "{{var.Value}}"}}},
			{Name: "timer", Type: "source_timer", Params: map[string]any{"function": "lr_start_timer", "args": []any{"{{param.Timer}}"}}},
			{Name: "runtime", Type: "source_runtime", Params: map[string]any{"function": "lr_exit", "args": []any{"{{var.Mode}}", "LR_PASS"}}},
			{Name: "meet", Type: "rendezvous", Params: map[string]any{"key": "{{var.Meeting}}"}},
			{Name: "end", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction", "args": []any{"{{var.TxName}}", "LR_AUTO"}}},
		}}
		findings, err := AnalyzeDependencies(s)
		if err != nil || len(findings) != 0 {
			t.Fatalf("%s: findings=%+v err=%v", protocol, findings, err)
		}
	}
}

// Metadata is import evidence: a bound request keeps its original reference
// there, and that copy must not reopen the dependency. Replay keeps import
// evidence as recorded and never refuses over it, so even a {{param.*}} in it
// is not a read.
func TestDependencyMetadataEvidenceIsNotARead(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Parameters: []dsl.Parameter{{Name: "order"}}, Actions: []dsl.Action{
		{Type: "POST", Target: "https://example.test/orders", Body: "{{var.order}}", Metadata: map[string]any{"source_evidence": "{{var.original_order}}"}},
		{Type: "POST", Target: "https://example.test/orders", Body: "{{param.order}}", Metadata: map[string]any{"source_evidence": "{{param.original_order}}", "original_url": "https://example.test/{{param.original_order}}"}},
	}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 0 {
		t.Fatalf("metadata evidence reported as a dependency: %+v %v", findings, err)
	}
}

// Kafka sends metadata as record headers and JMS as message properties, and
// replay refuses a protocol step that keeps an unresolved {{param.*}} in its
// metadata, so those uses gate execution like any other unsendable value.
func TestDependencyMetadataThatReplaySendsOrRefusesGatesExecution(t *testing.T) {
	for _, test := range []struct {
		name   string
		script *dsl.Script
		code   string
		detail string
	}{
		{
			name: "kafka header",
			script: &dsl.Script{Protocol: "kafka", Actions: []dsl.Action{
				{Type: "kafka_produce", Target: "orders", Body: "{}", Metadata: map[string]any{"trace_id": "{{var.trace_id}}", "source_path": "{{var.provenance}}"}},
			}},
			code:   "missing_producer:trace_id",
			detail: "No preceding producer supplies trace_id, and this protocol has no placeholder fallback, so replay refuses the step without a value. Bind a producer, parameter, or response extractor.",
		},
		{
			name: "jms property",
			script: &dsl.Script{Protocol: "jms-solace", Actions: []dsl.Action{
				{Type: "jms_publish", Target: "orders", Body: "{}", Metadata: map[string]any{"tenant": "{{var.tenant}}", "asyncapi_channel": "{{var.provenance}}"}},
			}},
			code:   "missing_producer:tenant",
			detail: "No preceding producer supplies tenant, and this protocol has no placeholder fallback, so replay refuses the step without a value. Bind a producer, parameter, or response extractor.",
		},
		{
			name: "http parameter",
			script: &dsl.Script{Protocol: "http", Actions: []dsl.Action{
				{Type: "GET", Target: "https://example.test/orders", Metadata: map[string]any{"source_evidence": "{{var.original}} {{param.original}}", "note": "{{param.region}}"}},
			}},
			code:   "missing_producer:region",
			detail: "No parameter supplies region, and replay refuses to start with an unresolved parameter. Declare a script parameter named region (it can read its values from a data source).",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			findings, err := AnalyzeDependencies(test.script)
			if err != nil || len(findings) != 1 {
				t.Fatalf("findings=%+v err=%v", findings, err)
			}
			if f := findings[0]; f.Code != test.code || f.Gate != GateExecution || f.Severity != "blocker" || f.Detail != test.detail || f.ActionPath != "actions[0]" {
				t.Fatalf("finding = %+v", f)
			}
		})
	}
}

// An LR transaction group (buildSourceHTTPSequence,
// pkg/convert/enterprisearchive/grouping.go) copies its first request's
// target and metadata into the wrapper, but the runtime resolves and sends
// only the requests. Each use is reported once, at the request a repair edits,
// and the wrapper produces no finding of its own.
func TestDependencySequenceWrapperCopyOfItsFirstRequestIsNotARead(t *testing.T) {
	first := map[string]any{"name": "orders", "type": "GET", "method": "GET", "target": "/orders/{{var.order}}?region={{param.region}}", "metadata": map[string]any{"source_file": "Action.c", "source_line": 7}}
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{
		Name:     "checkout",
		Type:     "http_sequence",
		Method:   "SEQUENCE",
		Target:   first["target"].(string),
		Params:   map[string]any{"requests": []any{first}},
		Metadata: map[string]any{"source_file": "Action.c", "source_line": 7, "source_transaction": "checkout", "source_request_count": 1},
	}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.ActionPath+" "+f.Code)
	}
	sort.Strings(got)
	if want := []string{"actions[0].params.requests[0] missing_producer:order", "actions[0].params.requests[0] missing_producer:region"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %#v\nwant %#v", got, want)
	}
}

// Findings are reported on varflow paths and resolve their source position
// through dslwalk, so both must name every control body and http_sequence
// child the same way.
func TestDependencyPathsMatchDSLWalk(t *testing.T) {
	leaf := func(name string, line int) dsl.Action {
		return dsl.Action{Name: name, Type: "GET", Target: "https://example.test/{{var." + name + "}}", Metadata: map[string]any{"source_file": "Action.c", "source_line": line}}
	}
	when := func(ref string) *dsl.Condition {
		return &dsl.Condition{Left: "{{var." + ref + "}}", Op: "eq", Right: "x"}
	}
	s := &dsl.Script{Protocol: "http", Init: []dsl.Action{leaf("init_ref", 1)}, Actions: []dsl.Action{
		{Name: "if", Type: "control_if", Metadata: map[string]any{"source_file": "Action.c", "source_line": 2}, Control: &dsl.ControlFlow{Condition: when("if_ref"), Steps: []dsl.Action{leaf("then_ref", 3)}, Else: []dsl.Action{leaf("else_ref", 4)}}},
		{Name: "switch", Type: "control_switch", Control: &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Condition: when("case_cond"), Steps: []dsl.Action{leaf("case0_ref", 5)}}, {Condition: when("case_cond"), Steps: []dsl.Action{leaf("case1_ref", 6)}}}, Default: []dsl.Action{leaf("default_ref", 7)}}},
		{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{
			{Name: "nested", Type: "control_if", Control: &dsl.ControlFlow{Condition: when("loop_index"), Steps: []dsl.Action{leaf("nested_ref", 8)}}},
		}}},
		{Name: "parallel", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "a", Steps: []dsl.Action{leaf("branch0_ref", 9)}}, {Name: "b", Steps: []dsl.Action{leaf("branch1_ref", 10)}}}}},
		{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{
			map[string]any{"name": "child0", "type": "GET", "target": "https://example.test/{{var.child0_ref}}", "metadata": map[string]any{"source_file": "Action.c", "source_line": 11}},
			map[string]any{"name": "child1", "type": "GET", "target": "https://example.test/{{var.child1_ref}}", "metadata": map[string]any{"source_file": "Action.c", "source_line": 12}},
		}}},
	}, Teardown: []dsl.Action{leaf("teardown_ref", 13)}}

	walked := map[string]string{}
	if err := dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		walked[ref.Path] = fmt.Sprintf("%v:%v", metadata["source_file"], metadata["source_line"])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	readiness := varflow.Analyze(s, nil)
	var analyzed []string
	for path := range readiness.ByAction {
		if _, ok := walked[path]; !ok {
			t.Errorf("varflow path %s is not a dslwalk path", path)
		}
		analyzed = append(analyzed, path)
	}
	sort.Strings(analyzed)
	want := []string{
		"actions[0]", "actions[0].control.else[0]", "actions[0].control.steps[0]",
		"actions[1]", "actions[1].control.cases[0].steps[0]", "actions[1].control.cases[1].steps[0]", "actions[1].control.default[0]",
		"actions[2].control.steps[0].control.steps[0]",
		"actions[3].control.branches[0].steps[0]", "actions[3].control.branches[1].steps[0]",
		"actions[4].params.requests[0]", "actions[4].params.requests[1]",
		"init[0]", "teardown[0]",
	}
	if !reflect.DeepEqual(analyzed, want) {
		t.Fatalf("analyzed paths = %#v\nwant %#v", analyzed, want)
	}

	findings, err := AnalyzeDependencies(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(readiness.Misses) {
		t.Fatalf("findings = %d, misses = %d: %+v", len(findings), len(readiness.Misses), findings)
	}
	for _, f := range findings {
		if f.SourceRef != walked[f.ActionPath] {
			t.Errorf("%s source ref = %q, want %q", f.ActionPath, f.SourceRef, walked[f.ActionPath])
		}
		if !strings.HasPrefix(f.Code, "missing_producer:") || f.Gate != GatePromotion {
			t.Errorf("%s finding = %+v", f.ActionPath, f)
		}
	}
	if walked["actions[4].params.requests[1]"] != "Action.c:12" || walked["actions[0]"] != "Action.c:2" {
		t.Fatalf("fixture source positions = %#v", walked)
	}
}

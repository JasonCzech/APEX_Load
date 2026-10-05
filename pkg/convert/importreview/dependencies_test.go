package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

func TestDependenciesIgnoreCommentsAndTrackPrecedingProducers(t *testing.T) {
	script := &dsl.Script{Parameters: []dsl.Parameter{{Name: "known"}}, Init: []dsl.Action{{Type: "GET", Target: "https://example.test", Extractors: []dsl.Extractor{{Name: "token"}}}}, Actions: []dsl.Action{{Type: "source_parameter", Params: map[string]any{"save_param": "encoded", "source_string": "{{var.token}}"}}, {Type: "POST", Target: "https://example.test/{{var.encoded}}", Body: "{{param.known}} {{var.absent}}", Metadata: map[string]any{"raw": "// producer absent"}}}}
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
	script := &dsl.Script{Actions: []dsl.Action{{Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "GET", Target: "/", Extractors: []dsl.Extractor{{Name: "conditional"}}}}}}, {Type: "GET", Target: "/{{var.conditional}}"}}}
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

func TestDependencyNamespacesAndArrayProducer(t *testing.T) {
	s := &dsl.Script{Init: []dsl.Action{{Type: "GET", Extractors: []dsl.Extractor{{Name: "token"}, {Name: "values", Mode: "all"}}}}, Actions: []dsl.Action{{Type: "GET", Target: "https://example.test/{{param.token}}/{{var.values_2}}"}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 2 {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	for _, f := range findings {
		if f.Code == "missing_producer:token" && f.Gate != GateExecution {
			t.Fatal("extractor satisfied parameter-only namespace")
		}
		if f.Code == "missing_producer:values_2" {
			t.Fatal("array extractor was treated as absent")
		}
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
	s := &dsl.Script{Actions: []dsl.Action{
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
	s := &dsl.Script{Actions: []dsl.Action{{Type: "source_verification", Params: map[string]any{"operation": "register", "id": "check", "text": "{{var.missing}}", "scope": "body"}, Metadata: map[string]any{"raw_source": "{{var.comment}}"}}}}
	findings, err := AnalyzeDependencies(s)
	if err != nil || len(findings) != 1 || findings[0].Code != "missing_producer:missing" || findings[0].Gate != GatePromotion {
		t.Fatalf("verification dependencies=%+v err=%v", findings, err)
	}
}

func TestJSONHelperMissingProducerBlocksExecution(t *testing.T) {
	s := &dsl.Script{Actions: []dsl.Action{
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

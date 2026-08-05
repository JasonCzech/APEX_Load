package enterprisearchive

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSplitJavaTopLevelDistinguishesComparisonsShiftsAndGenerics(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want []string
	}{
		{
			name: "comparisons",
			expr: "a < b, c > d, tail",
			want: []string{"a < b", "c > d", "tail"},
		},
		{
			name: "comparison ending with greater than or equal",
			expr: "a < b, c >= d, tail",
			want: []string{"a < b", "c >= d", "tail"},
		},
		{
			name: "shifts",
			expr: "value << bits, other >> shift, tail",
			want: []string{"value << bits", "other >> shift", "tail"},
		},
		{
			name: "generic constructor",
			expr: "new Pair<String, Integer>(), tail",
			want: []string{"new Pair<String, Integer>()", "tail"},
		},
		{
			name: "nested generics",
			expr: "new Map<String, List<Integer>>(), tail",
			want: []string{"new Map<String, List<Integer>>()", "tail"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := splitJavaTopLevel(test.expr, ','); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("splitJavaTopLevel(%q) = %#v, want %#v", test.expr, got, test.want)
			}
		})
	}
}

func TestJavaClassificationConsumesProvidedRuntimeReachability(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    lr.not_a_real_api("value");
    return 0;
  }
}`,
	}
	program := mustParseJavaTestProgram(t, files)
	reachability := buildJavaVuserRuntimeReachability(program)
	if !javaReachabilityHasUnknownLR(reachability) {
		t.Fatal("fixture did not produce reachable unknown lr.* evidence")
	}
	profile := classifyJavaArchiveWithReachability(program, nil, files, reachability)
	if !containsString(profile.ReviewFamilies, "unrecognized_lr_method") {
		t.Fatalf("review families = %#v, want unrecognized_lr_method", profile.ReviewFamilies)
	}

	// Supplying an intentionally empty result proves this classifier variant does
	// not discard its input and rebuild the expensive reachability graph.
	empty := javaVuserRuntimeReachability{}
	profile = classifyJavaArchiveWithReachability(program, nil, files, empty)
	if containsString(profile.ReviewFamilies, "unrecognized_lr_method") {
		t.Fatalf("review families = %#v, classifier rebuilt discarded reachability", profile.ReviewFamilies)
	}
}

func TestSourceRuntimeControlPreservesEmptyMiddleArgument(t *testing.T) {
	fn := ParsedFunction{Args: map[string]string{}}
	parseSourceRuntimeControl(&fn, `"operator", "", "password"`)
	if got, want := sourceControlArgs(fn), []string{"operator", "", "password"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sourceControlArgs() = %#v, want %#v", got, want)
	}
}

func TestStructureFlowActionsKeepsNestedLoopInsideOneOuterWrapper(t *testing.T) {
	actions := []dsl.Action{
		{Name: "outer-a", Type: "GET", Metadata: map[string]any{
			sourceFlowLoopIDKey: "10:1", sourceFlowLoopKey: "for (i=0; i<2; i++)", sourceFlowLoopCountKey: "2",
		}},
		{Name: "inner", Type: "GET", Metadata: map[string]any{
			sourceFlowLoopIDKey: "10:1>>12:3", sourceFlowLoopKey: "for (i=0; i<2; i++) >> for (j=0; j<3; j++)",
		}},
		{Name: "outer-b", Type: "GET", Metadata: map[string]any{
			sourceFlowLoopIDKey: "10:1", sourceFlowLoopKey: "for (i=0; i<2; i++)", sourceFlowLoopCountKey: "2",
		}},
	}
	counters := &sourceFlowCounters{}
	structured := structureFlowActions(
		actions,
		func(index int) string { return "actions[" + strconv.Itoa(index) + "]" },
		func(index int) string { return "actions[" + strconv.Itoa(index) + "]" },
		counters,
		map[string]string{},
		true,
	)
	if len(structured) != 1 || structured[0].Type != "control_loop" || structured[0].Control == nil {
		t.Fatalf("structured actions = %#v, want one outer control_loop", structured)
	}
	if got := structured[0].Control.Steps; len(got) != 3 || got[0].Name != "outer-a" || got[1].Name != "inner" || got[2].Name != "outer-b" {
		t.Fatalf("outer loop steps = %#v, want source order preserved across nested path", got)
	}
}

func TestFindFileRejectsAmbiguousBasenameUnlessContentIsIdentical(t *testing.T) {
	if _, ok := findFile(map[string]string{
		"a/users.dat": "a",
		"b/users.dat": "b",
	}, "users.dat"); ok {
		t.Fatal("ambiguous basename with different contents resolved nondeterministically")
	}

	path, content, ok := findFileEntry(map[string]string{
		"b/users.dat": "same",
		"a/users.dat": "same",
	}, "users.dat")
	if !ok || path != "a/users.dat" || content != "same" {
		t.Fatalf("identical basename match = (%q, %q, %v), want deterministic a/users.dat", path, content, ok)
	}
}

func TestParseParameterFileIsDeterministicAndSeparatesConflictingLayouts(t *testing.T) {
	content := `[parameter:B]
ParamName=B
Table=fixtures/users.dat
ColumnName=b
Delimiter=;

[parameter:A]
ParamName=A
Table=fixtures/users.dat
ColumnName=a
Delimiter=,
`
	files := map[string]string{"fixtures/users.dat": "a,b\n1,2\n"}
	var baseline []byte
	for iteration := 0; iteration < 25; iteration++ {
		parsed := ParseParameterFile("params.prm", content, files)
		if len(parsed.DataSources) != 2 {
			t.Fatalf("iteration %d data sources = %#v, want two layout-specific sources", iteration, parsed.DataSources)
		}
		if len(parsed.Parameters) != 2 || parsed.Parameters[0].Name != "a" || parsed.Parameters[1].Name != "b" {
			t.Fatalf("iteration %d parameters = %#v, want stable case-insensitive section order", iteration, parsed.Parameters)
		}
		if !strings.Contains(strings.Join(parsed.Warnings, "\n"), "conflicting delimiter/column layouts") {
			t.Fatalf("iteration %d warnings = %#v, want explicit layout conflict", iteration, parsed.Warnings)
		}
		encoded, err := json.Marshal(parsed)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			baseline = encoded
		} else if string(encoded) != string(baseline) {
			t.Fatalf("iteration %d changed deterministic result:\n%s\n%s", iteration, baseline, encoded)
		}
	}
}

func TestConvertPreservesAndGatesContributorsAfterFinalRequest(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action() {
  web_url("first", "URL=https://example.test/", LAST);
  web_reg_save_param("late", "LB=<id>", "RB=</id>", LAST);
  web_add_header("X-Late", "value");
  return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	preserved := ensureEnterpriseArchiveImportPreserved(result.Script)
	summary, ok := preserved[trailingRequestContributorsMetadataKey].(map[string]int)
	if !ok || summary["extractors"] != 1 || summary["headers"] != 1 {
		t.Fatalf("trailing contributor summary = %#v", preserved[trailingRequestContributorsMetadataKey])
	}
	if len(result.Script.Actions) != 1 || len(result.Script.Actions[0].Extractors) != 0 {
		t.Fatalf("late contributor was rebound to an earlier request: %#v", result.Script.Actions)
	}
	var reviewOnly int
	for _, mapping := range sourceReviewMappings(result) {
		if mapping.TargetPath == "metadata.import.preserved."+trailingRequestContributorsMetadataKey && mapping.Status == "review_only" {
			reviewOnly++
		}
	}
	if reviewOnly != 2 {
		t.Fatalf("source mappings = %#v, want two explicit review-only dispositions", sourceReviewMappings(result))
	}
	report := BuildImportFidelityReport(result, map[string]string{"Action.c": "source"}, nil, []*ParsedAction{parsed})
	if report.PromotionStatus != ImportPromotionBlocked || !strings.Contains(strings.Join(report.Blockers, "\n"), "after the final request") {
		t.Fatalf("fidelity report = %#v, want promotion blocker", report)
	}
}

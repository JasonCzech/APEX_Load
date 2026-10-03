package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

func concurrentSourceResult(t *testing.T, source string, opts ConvertOptions) *ConvertResult {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func concurrentSourceGroups(t *testing.T, script *dsl.Script) map[string]dsl.Action {
	t.Helper()
	groups := map[string]dsl.Action{}
	if err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Type == "http_sequence" && ref.Action.Params["execution_mode"] == dsl.HTTPSequenceConcurrent {
			groups[ref.Path] = *ref.Action
		}
		if _, exists := ref.Action.Metadata[sourceConcurrentMarkerKey]; exists {
			t.Error("private planning marker leaked")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return groups
}
func concurrentSourceRequests(t *testing.T, action dsl.Action) []dsl.Action {
	t.Helper()
	raw, err := json.Marshal(action.Params["requests"])
	if err != nil {
		t.Fatal(err)
	}
	var children []dsl.Action
	if err := json.Unmarshal(raw, &children); err != nil {
		t.Fatal(err)
	}
	return children
}
func concurrentSourceSummary(t *testing.T, script *dsl.Script) map[string]any {
	t.Helper()
	preserved := ensureEnterpriseArchiveImportPreserved(script)
	summary, ok := preserved["source_concurrency"].(map[string]any)
	if !ok {
		t.Fatal("missing concurrency outcome accounting")
	}
	return summary
}
func assertConcurrentSourceMapping(t *testing.T, result *ConvertResult, fn, path string, mode SourceFunctionSupportMode) {
	t.Helper()
	found := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == fn && mapping.TargetPath == path && mapping.SupportMode == string(mode) {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing exact %s mapping at %s with mode %s", fn, path, mode)
	}
}

func TestSourceConcurrentGroupPreservesTypedRequestsAndContributorPaths(t *testing.T) {
	source := `Action(){
 web_concurrent_start(NULL);
 web_add_header("X-Trace", "first");
 web_reg_save_param_json("ParamName=first_id", "QueryString=$.id", LAST);
 web_reg_find("Text=ready", LAST);
 web_custom_request("First", "URL=https://example.test/a", "Method=POST", "Body=payload", LAST);
 web_add_header("X-Trace", "second");
 web_reg_save_param_json("ParamName=second_id", "QueryString=$.id", LAST);
 web_url("Second", "URL=https://example.test/b", LAST);
 web_concurrent_end(NULL);
 return 0; }`
	result := concurrentSourceResult(t, source, ConvertOptions{IncludeAssertions: true})
	groups := concurrentSourceGroups(t, result.Script)
	group, ok := groups["actions[0]"]
	if !ok || len(groups) != 1 || len(result.Script.Actions) != 1 {
		t.Fatalf("expected one exact concurrent wrapper, groups=%d actions=%d", len(groups), len(result.Script.Actions))
	}
	if _, exists := group.Params["max_concurrency"]; exists {
		t.Fatal("importer must preserve runtime default fanout")
	}
	children := concurrentSourceRequests(t, group)
	if len(children) != 2 || children[0].Body != "payload" || children[0].Method != "POST" || children[1].Method != "GET" || children[0].Headers["X-Trace"] != "first" || children[1].Headers["X-Trace"] != "second" || len(children[0].Assertions) != 2 || len(children[0].Extractors) != 1 || len(children[1].Extractors) != 1 {
		t.Fatal("typed request or per-request contributor semantics were lost")
	}
	for _, marker := range []string{"web_concurrent_start", "web_concurrent_end"} {
		assertConcurrentSourceMapping(t, result, marker, "actions[0]", SourceFunctionSupportExecutable)
	}
	assertConcurrentSourceMapping(t, result, "web_custom_request", "actions[0].params.requests[0]", SourceFunctionSupportExecutable)
	assertConcurrentSourceMapping(t, result, "web_url", "actions[0].params.requests[1]", SourceFunctionSupportExecutable)
	assertConcurrentSourceMapping(t, result, "web_reg_save_param_json", "actions[0].params.requests[0].extractors[0]", SourceFunctionSupportExecutable)
	assertConcurrentSourceMapping(t, result, "web_reg_find", "actions[0].params.requests[0].assertions[0]", SourceFunctionSupportExecutable)
	summary := concurrentSourceSummary(t, result.Script)
	if summary["groups"] != 1 || summary["native_groups"] != 1 || summary["review_groups"] != 0 || summary["inspection_complete"] != true {
		t.Fatalf("wrong outcome accounting: %#v", summary)
	}
}

func TestSourceConcurrentGroupsStayExplicitForUnsafeSourceSpans(t *testing.T) {
	request := `web_url("A", "URL=https://example.test/a", LAST);`
	cases := []struct{ name, body string }{
		{"empty", `web_concurrent_start(NULL);web_concurrent_end(NULL);`},
		{"missing end", `web_concurrent_start(NULL);` + request},
		{"unmatched end", request + `web_concurrent_end(NULL);`},
		{"nested", `web_concurrent_start(NULL);` + request + `web_concurrent_start(NULL);` + request + `web_concurrent_end(NULL);web_concurrent_end(NULL);`},
		{"marker arguments", `web_concurrent_start("synthetic");` + request + `web_concurrent_end(NULL);`},
		{"runtime barrier", `web_concurrent_start(NULL);web_set_timeout("CONNECT","4",LAST);` + request + `web_concurrent_end(NULL);`},
		{"transaction crossing", `web_concurrent_start(NULL);lr_start_transaction("inside");` + request + `lr_end_transaction("inside",LR_AUTO);web_concurrent_end(NULL);`},
		{"think time", `web_concurrent_start(NULL);` + request + `lr_think_time(0.1);` + request + `web_concurrent_end(NULL);`},
		{"previous response", `web_concurrent_start(NULL);` + request + `web_find("old", "What=ready",LAST);` + request + `web_concurrent_end(NULL);`},
		{"non HTTP", `web_concurrent_start(NULL);web_stream_open("stream");` + request + `web_concurrent_end(NULL);`},
		{"cross condition", `web_concurrent_start(NULL);if(1){` + request + `}web_concurrent_end(NULL);`},
		{"cross loop", `web_concurrent_start(NULL);for(int i=0;i<2;i++){` + request + `}web_concurrent_end(NULL);`},
		{"duplicate output", `web_concurrent_start(NULL);web_reg_save_param_json("ParamName=same", "QueryString=$.id",LAST);` + request + `web_reg_save_param_json("ParamName=same", "QueryString=$.id",LAST);` + request + `web_concurrent_end(NULL);`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := concurrentSourceResult(t, "Action(){"+tc.body+"return 0;}", ConvertOptions{IncludeAssertions: true, PreserveTransactions: true})
			if len(concurrentSourceGroups(t, result.Script)) != 0 {
				t.Fatal("unsafe source group gained concurrent parity")
			}
			markers := 0
			if err := dslwalk.WalkActions(result.Script, func(ref dslwalk.ActionRef) error {
				if ref.Action.Type == "source_review" && (ref.Action.Params["function"] == "web_concurrent_start" || ref.Action.Params["function"] == "web_concurrent_end") {
					markers++
					if ref.Action.Metadata["execution_blocker"] != "source_concurrent_group_requires_review" {
						t.Error("unsafe marker lost durable execution gate")
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if markers == 0 {
				t.Fatal("unsafe boundaries were silently dropped")
			}
			wire, err := json.Marshal(result.Script)
			if err != nil {
				t.Fatal(err)
			}
			var reopened dsl.Script
			if err := json.Unmarshal(wire, &reopened); err != nil {
				t.Fatal(err)
			}
			if len(concurrentSourceGroups(t, &reopened)) != 0 {
				t.Fatal("reopen invented concurrency")
			}
		})
	}
}

func TestSourceConcurrentGroupChildCountBoundary(t *testing.T) {
	for _, count := range []int{1, 256, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var body strings.Builder
			body.WriteString("Action(){web_concurrent_start(NULL);")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&body, `web_url("r%d","URL=https://example.test/%d",LAST);`, i, i)
			}
			body.WriteString("web_concurrent_end(NULL);return 0;}")
			result := concurrentSourceResult(t, body.String(), ConvertOptions{})
			groups := concurrentSourceGroups(t, result.Script)
			if count <= dsl.MaxHTTPConcurrentChildren {
				if len(groups) != 1 || len(concurrentSourceRequests(t, groups["actions[0]"])) != count {
					t.Fatal("supported child boundary not preserved")
				}
			} else if len(groups) != 0 {
				t.Fatal("oversized group became executable")
			}
			summary := concurrentSourceSummary(t, result.Script)
			want := 0
			if count <= 256 {
				want = 1
			}
			if summary["native_groups"] != want || summary["review_groups"] != 1-want {
				t.Fatal("bounded group counts lost")
			}
		})
	}
}

func TestSourceConcurrentGroupPreservesLifecycleAndControlScope(t *testing.T) {
	body := `if (atoi(lr_eval_string("{ready}")) > 0) {for(int i=0;i<2;i++) {
 web_concurrent_start(NULL);web_url("A","URL=https://example.test/a",LAST);web_url("B","URL=https://example.test/b",LAST);web_concurrent_end(NULL);
 }} else {web_url("Fallback","URL=https://example.test/fallback",LAST);}return 0;`
	result := concurrentSourceResult(t, "Action(){"+body+"}", ConvertOptions{})
	groups := concurrentSourceGroups(t, result.Script)
	path := "actions[0].control.steps[0].control.steps[0]"
	if len(groups) != 1 {
		t.Fatalf("native scope groups=%d", len(groups))
	}
	if _, ok := groups[path]; !ok {
		t.Fatalf("concurrency not inside exact source condition and loop: %v", reflect.ValueOf(groups).MapKeys())
	}
	assertConcurrentSourceMapping(t, result, "web_url", path+".params.requests[0]", SourceFunctionSupportExecutable)
	for _, marker := range []string{"web_concurrent_start", "web_concurrent_end"} {
		assertConcurrentSourceMapping(t, result, marker, path, SourceFunctionSupportExecutable)
	}
}

func TestSourceConcurrentGroupPreservesEnclosingTransaction(t *testing.T) {
	result := concurrentSourceResult(t, `Action(){lr_start_transaction("Business");
 web_url("Before","URL=https://example.test/before",LAST);
 web_concurrent_start(NULL);web_url("A","URL=https://example.test/a",LAST);web_url("B","URL=https://example.test/b",LAST);web_concurrent_end(NULL);
 web_url("After","URL=https://example.test/after",LAST);lr_end_transaction("Business",LR_AUTO);return 0;}`, ConvertOptions{PreserveTransactions: true})
	groups := concurrentSourceGroups(t, result.Script)
	if len(groups) != 1 {
		t.Fatal("enclosing transaction dropped concurrency")
	}
	for path, group := range groups {
		if actionTransactionName(group) != "Business" {
			t.Fatal("wrapper lost source transaction attribution")
		}
		for _, child := range concurrentSourceRequests(t, group) {
			if actionTransactionName(child) != "Business" {
				t.Fatal("child lost enclosing transaction")
			}
		}
		assertConcurrentSourceMapping(t, result, "web_url", path+".params.requests[0]", SourceFunctionSupportExecutable)
	}
	starts, ends := 0, 0
	for _, a := range result.Script.Actions {
		if a.Type == "source_transaction" && importedSourceFunction(a) == "lr_start_transaction" {
			starts++
		}
		if a.Type == "source_transaction" && importedSourceFunction(a) == "lr_end_transaction" {
			ends++
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("transaction lifecycle lost starts=%d ends=%d", starts, ends)
	}
}

func TestSourceConcurrentGroupPreservesEveryTypedExecutionField(t *testing.T) {
	fn := ParsedFunction{Name: "web_concurrent_start", Filename: "Action.c", Phase: "action", sourceFunction: "Action", CallArgs: []ParsedArgument{{Raw: "NULL"}}}
	end := fn
	end.Name = "web_concurrent_end"
	requestFn := fn
	requestFn.Name = "web_url"
	plan := planSourceConcurrentGroups([]ParsedFunction{fn, requestFn, end}, nil)
	child := dsl.Action{Name: "request", Type: "POST", Method: "POST", Target: "https://example.test/a", Body: "cGF5bG9hZA==", BodyEncoding: "base64", Headers: map[string]string{"X": "one"}, HeaderList: dsl.HeaderList{{Name: "X", Value: "one"}, {Name: "X", Value: "two"}}, Params: map[string]any{"custom": "value"}, Metadata: map[string]any{"runtime_option": "kept"}, TimeoutMs: 27, ThinkTimeAfter: true, Extractors: []dsl.Extractor{{Name: "id", Type: "jsonpath", Expression: "$.id"}}, Assertions: []dsl.Assertion{{Type: "status", Value: "200"}}, Attribution: &dsl.AnalyticsAttribution{HierarchyPath: "synthetic/page", PageName: "page"}}
	redirects := 3
	child.MaxRedirects = &redirects
	result := &ConvertResult{Script: &dsl.Script{Protocol: "http"}, SourceReview: &SourceReview{}}
	appendSourceConcurrentMarker(result, fn, 0, plan)
	appendSourceAction(result, requestFn, child, SourceFunctionSupportExecutable, "")
	appendSourceConcurrentMarker(result, end, 2, plan)
	before := result.Script.Actions[1]
	structureSourceConcurrentGroups(result, plan)
	children := concurrentSourceRequests(t, result.Script.Actions[0])
	wireBefore, _ := json.Marshal(before)
	wireAfter, _ := json.Marshal(children[0])
	if string(wireBefore) != string(wireAfter) {
		t.Fatal("typed child execution semantics changed during group construction")
	}
}

func TestSourceConcurrentGroupCrossFileAndFunctionRemainReview(t *testing.T) {
	a, err := ParseActionFile("Action.c", `Action(){web_concurrent_start(NULL);web_url("A","URL=https://example.test/a",LAST);return 0;}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseActionFile("Other.c", `Other(){web_url("B","URL=https://example.test/b",LAST);web_concurrent_end(NULL);return 0;}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{a, b}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(concurrentSourceGroups(t, result.Script)) != 0 {
		t.Fatal("cross-file/function markers were joined")
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_concurrent_start", SourceFunctionSupportReviewOnly)
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_concurrent_end", SourceFunctionSupportReviewOnly)
}

func TestSourceConcurrentZipKeepsParametersCredentialsAndReopenedSourcePaths(t *testing.T) {
	source := `Action(){
 web_concurrent_start(NULL);
 web_add_header("Authorization", "Bearer synthetic-exact-source-credential");
 web_custom_request("First", "URL=http://{server_url}/first", "Method=POST", "Body=exact={row}&keep=untouched", LAST);
 web_url("Second", "URL=http://{server_url}/second?row={row}", LAST);
 web_concurrent_end(NULL);return 0;}`
	archive := buildSourceZip(t, map[string]string{
		"Concurrent.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Parameters]\nParameter1=rows.prm\n[Actions]\nAction1=Action.c\n",
		"default.cfg":    "[HTTP]\nHTTPVersion=HTTP/1.1\nReceiveTimeout=7\n",
		"Action.c":       source,
		"rows.prm":       "[server_url]\nType=File\nTable=rows.dat\nColumnName=server_url\nSelectNextRow=Sequential\nUpdateValueOn=Each Iteration\n\n[row]\nType=File\nTable=rows.dat\nColumnName=row\nSelectNextRow=Sequential\nUpdateValueOn=Each Iteration\n",
		"rows.dat":       "server_url,row\nexample.test,synthetic-record\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	groups := concurrentSourceGroups(t, result.Script)
	if len(groups) != 1 {
		t.Fatal("ZIP import lost validated group")
	}
	children := concurrentSourceRequests(t, groups["actions[0]"])
	if children[0].Target != "/first" || children[0].Body != "exact={{param.row}}&keep=untouched" || children[1].Target != "/second?row={{param.row}}" {
		t.Fatalf("post-group parameter binding mismatch: %q %q %q", children[0].Target, children[0].Body, children[1].Target)
	}
	if children[0].Headers["Authorization"] != "Bearer synthetic-exact-source-credential" {
		t.Fatal("private executable source credential changed")
	}
	if result.Script.Config.BaseURL != "http://example.test" || len(result.Script.Parameters) != 2 || len(result.Script.DataSources) != 1 || result.Script.Config.HTTP.RequestTimeoutMs != 7000 {
		t.Fatalf("ZIP execution context mismatch: base=%q parameters=%d data_sources=%d timeout=%d", result.Script.Config.BaseURL, len(result.Script.Parameters), len(result.Script.DataSources), result.Script.Config.HTTP.RequestTimeoutMs)
	}
	wire, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	var reopened dsl.Script
	if err := json.Unmarshal(wire, &reopened); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(concurrentSourceRequests(t, concurrentSourceGroups(t, &reopened)["actions[0]"]), children) {
		t.Fatal("reopened typed requests changed")
	}
	raw, err := json.Marshal(reopened.Metadata.Import.Preserved["source_review"])
	if err != nil {
		t.Fatal(err)
	}
	var review SourceReview
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	reopenedResult := &ConvertResult{Script: &reopened, SourceReview: &review}
	assertConcurrentSourceMapping(t, reopenedResult, "web_custom_request", "actions[0].params.requests[0]", SourceFunctionSupportExecutable)
	assertConcurrentSourceMapping(t, reopenedResult, "web_url", "actions[0].params.requests[1]", SourceFunctionSupportExecutable)
	foundSource := false
	for _, file := range review.SourceFiles {
		if file.Path == "Action.c" && file.Content == source {
			foundSource = true
		}
	}
	if !foundSource {
		t.Fatal("durable source evidence lost exact source text")
	}
	if issues := dsl.ValidateScriptIssues(&reopened); len(issues) != 0 {
		t.Fatalf("reopened concurrent archive invalid: %v", issues)
	}
}

func TestSourceConcurrentGroupsKeepLifecycleAndDistinctBranchIdentity(t *testing.T) {
	sources := []struct{ file, source string }{
		{"vuser_init.c", `vuser_init(){web_concurrent_start(NULL);web_url("Init","URL=https://example.test/init",LAST);web_concurrent_end(NULL);return 0;}`},
		{"Action.c", `Action(){if(atoi(lr_eval_string("{ready}"))>0){web_concurrent_start(NULL);web_url("Then","URL=https://example.test/then",LAST);web_concurrent_end(NULL);}else{web_concurrent_start(NULL);web_url("Else","URL=https://example.test/else",LAST);web_concurrent_end(NULL);}return 0;}`},
		{"vuser_end.c", `vuser_end(){web_concurrent_start(NULL);web_url("End","URL=https://example.test/end",LAST);web_concurrent_end(NULL);return 0;}`},
	}
	actions := make([]*ParsedAction, 0, len(sources))
	for _, source := range sources {
		parsed, err := ParseActionFile(source.file, source.source)
		if err != nil {
			t.Fatal(err)
		}
		phase := "action"
		if source.file == "vuser_init.c" {
			phase = "init"
		}
		if source.file == "vuser_end.c" {
			phase = "teardown"
		}
		parsed.Phase = phase
		for i := range parsed.Functions {
			parsed.Functions[i].Phase = phase
		}
		actions = append(actions, parsed)
	}
	result, err := Convert(actions, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	groups := concurrentSourceGroups(t, result.Script)
	for _, path := range []string{"init[0]", "actions[0].control.steps[0]", "actions[0].control.else[0]", "teardown[0]"} {
		if _, ok := groups[path]; !ok {
			t.Fatalf("missing lifecycle/branch group at %s; group paths=%v", path, reflect.ValueOf(groups).MapKeys())
		}
	}
	if len(groups) != 4 {
		t.Fatal("distinct original branch identities merged")
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "web_concurrent_start" || mapping.Function == "web_concurrent_end" {
			group, ok := groups[mapping.TargetPath]
			if !ok || group.Name != mapping.TargetName {
				t.Fatal("source boundary name/path did not survive final wrapper naming")
			}
		}
	}
}

func TestSourceConcurrentMarkerDetailLimitKeepsFullAccounting(t *testing.T) {
	var body strings.Builder
	body.WriteString("Action(){")
	const groups = 600
	for i := 0; i < groups; i++ {
		fmt.Fprintf(&body, `web_concurrent_start(NULL);web_url("r%d","URL=https://example.test/%d",LAST);web_concurrent_end(NULL);`, i, i)
	}
	body.WriteString("return 0;}")
	result := concurrentSourceResult(t, body.String(), ConvertOptions{})
	if len(concurrentSourceGroups(t, result.Script)) != groups {
		t.Fatal("detail retention cap incorrectly limits execution coverage")
	}
	summary := concurrentSourceSummary(t, result.Script)
	if summary["groups"] != groups || summary["native_groups"] != groups || summary["markers"] != groups*2 || summary["details_omitted"] != groups*2-maxSourceConcurrencyDetails || summary["inspection_complete"] != true {
		t.Fatalf("full count/omission reconciliation failed: %#v", summary)
	}
	if lenSourceConcurrencyDetails(result.Script) != maxSourceConcurrencyDetails {
		t.Fatal("marker details exceed fixed cap")
	}
}

func TestSourceConcurrentInspectionLimitKeepsUninspectedMarkersReview(t *testing.T) {
	fn := ParsedFunction{Name: "web_concurrent_start", Filename: "Action.c", Phase: "action", sourceFunction: strings.Repeat("x", 257), CallArgs: []ParsedArgument{{Raw: "NULL"}}}
	request := fn
	request.Name = "web_url"
	end := fn
	end.Name = "web_concurrent_end"
	plan := planSourceConcurrentGroups([]ParsedFunction{fn, request, end}, nil)
	if !plan.limited || plan.groups != 1 || plan.markerCount != 2 || plan.markers[0].reason != "inspection_limit" || plan.markers[2].reason != "inspection_limit" {
		t.Fatal("bounded source inspection lost full counts/explicit review")
	}
	result := &ConvertResult{Script: &dsl.Script{Protocol: "http"}, SourceReview: &SourceReview{}}
	appendSourceConcurrentMarker(result, fn, 0, plan)
	appendSourceAction(result, request, dsl.Action{Name: "r", Type: "GET", Target: "https://example.test"}, SourceFunctionSupportExecutable, "")
	appendSourceConcurrentMarker(result, end, 2, plan)
	structureSourceConcurrentGroups(result, plan)
	summary := concurrentSourceSummary(t, result.Script)
	if summary["inspection_complete"] != false || summary["review_groups"] != 1 || len(concurrentSourceGroups(t, result.Script)) != 0 {
		t.Fatal("incomplete source inspection claimed native coverage")
	}
}

func TestSourceConcurrentDependenciesRemainExplicitReview(t *testing.T) {
	for _, output := range []string{"items", "items_count", "items_1"} {
		t.Run(output, func(t *testing.T) {
			source := `Action(){web_concurrent_start(NULL);web_reg_save_param_json("ParamName=items","QueryString=$.items","SelectAll=Yes",LAST);web_url("Producer","URL=https://example.test/producer",LAST);web_url("Consumer","URL=https://example.test/consumer?item={` + output + `}",LAST);web_concurrent_end(NULL);return 0;}`
			result := concurrentSourceResult(t, source, ConvertOptions{})
			if len(concurrentSourceGroups(t, result.Script)) != 0 {
				t.Fatal("same-group extractor dependency claimed native source parity")
			}
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_concurrent_start", SourceFunctionSupportReviewOnly)
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_concurrent_end", SourceFunctionSupportReviewOnly)
		})
	}
}

func TestSourceConcurrentInspectionBudgetIsNotRecycled(t *testing.T) {
	fn := ParsedFunction{Name: "web_concurrent_start", Filename: "Action.c", Phase: "action", sourceFunction: "Action", CallArgs: []ParsedArgument{{Raw: "NULL"}}}
	end := fn
	end.Name = "web_concurrent_end"
	request := fn
	request.Name = "web_url"
	fns := []ParsedFunction{fn, request, end, fn, request, end}
	plan := planSourceConcurrentGroups(fns, nil)
	plan.workRemaining = 1
	result := &ConvertResult{Script: &dsl.Script{Protocol: "http"}, SourceReview: &SourceReview{}}
	for i, fn := range fns {
		if fn.Name == "web_url" {
			appendSourceAction(result, fn, dsl.Action{Name: "r", Type: "GET", Target: "https://example.test"}, SourceFunctionSupportExecutable, "")
		} else {
			appendSourceConcurrentMarker(result, fn, i, plan)
		}
	}
	structureSourceConcurrentGroups(result, plan)
	if plan.workRemaining != 0 || !plan.limited || plan.native != 0 || plan.review != 2 {
		t.Fatal("failed work admission recycled its allowance or lost counts")
	}
	if len(concurrentSourceGroups(t, result.Script)) != 0 || concurrentSourceSummary(t, result.Script)["inspection_complete"] != false {
		t.Fatal("limited inspection claimed native completeness")
	}
}

func TestSourceConcurrentCommentedMarkersDoNotCreateGroups(t *testing.T) {
	source := `Action(){
 // web_concurrent_start(NULL);
 web_custom_request("A","URL=https://example.test/a","Method=POST","Body=web_concurrent_start(NULL)",LAST);
 /* web_concurrent_end(NULL); */
 web_url("B","URL=https://example.test/b",LAST);
 return 0;}`
	result := concurrentSourceResult(t, source, ConvertOptions{})
	if len(concurrentSourceGroups(t, result.Script)) != 0 {
		t.Fatal("lexical marker text inferred source concurrency")
	}
	if _, exists := ensureEnterpriseArchiveImportPreserved(result.Script)["source_concurrency"]; exists {
		t.Fatal("comments/payloads created marker evidence")
	}
}

func TestSourceConcurrentAnnotatedAuraDecodedDependencyStaysReview(t *testing.T) {
	for _, annotated := range []bool{false, true} {
		t.Run(fmt.Sprint(annotated), func(t *testing.T) {
			start := ParsedFunction{Name: "web_concurrent_start", Filename: "Action.c", Phase: "action", sourceFunction: "Action", CallArgs: []ParsedArgument{{Raw: "NULL"}}}
			end := start
			end.Name = "web_concurrent_end"
			request := start
			request.Name = "web_custom_request"
			plan := planSourceConcurrentGroups([]ParsedFunction{start, request, request, end}, nil)
			result := &ConvertResult{Script: &dsl.Script{Protocol: "http"}, SourceReview: &SourceReview{}}
			appendSourceConcurrentMarker(result, start, 0, plan)
			appendSourceAction(result, request, dsl.Action{Name: "producer", Type: "GET", Target: "https://example.test/producer", Extractors: []dsl.Extractor{{Name: "first", Type: "jsonpath", Expression: "$.first"}}}, SourceFunctionSupportExecutable, "")
			metadata := map[string]any{}
			if annotated {
				metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] = dsl.ImportAuraJSONTemplateValueEncoding
			}
			body := `{"actions":[{"id":"synthetic","descriptor":"synthetic-action","params":{"input":"{\"recordId\":\"\\u007b\\u007bvar.first\\u007d\\u007d\"}"}}]}`
			appendSourceAction(result, request, dsl.Action{Name: "consumer", Type: "POST", Target: "https://example.test/aura", Body: body, Metadata: metadata}, SourceFunctionSupportExecutable, "")
			appendSourceConcurrentMarker(result, end, 3, plan)
			structureSourceConcurrentGroups(result, plan)
			groups := concurrentSourceGroups(t, result.Script)
			if annotated {
				if len(groups) != 0 || plan.review != 1 {
					t.Fatal("decoded same-group dependency gained false native source parity")
				}
				assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_concurrent_start", SourceFunctionSupportReviewOnly)
			} else if len(groups) != 1 {
				t.Fatal("unannotated literal JSON body was implicitly decoded")
			}
		})
	}
}

func TestSourceConcurrentMarkerArgumentAdmissionIsBounded(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(fmt.Sprint(structured), func(t *testing.T) {
			fn := ParsedFunction{Name: "web_concurrent_start", Filename: "Action.c", Phase: "action", sourceFunction: "Action"}
			if structured {
				fn.CallArgs = []ParsedArgument{{Raw: strings.Repeat(" ", 4097) + "NULL"}}
			} else {
				fn.RawArgs = []string{strings.Repeat(" ", 4097) + "NULL"}
			}
			if sourceConcurrentMarkerArgs(fn) {
				t.Fatal("oversized marker argument admitted")
			}
			plan := planSourceConcurrentGroups([]ParsedFunction{fn}, nil)
			if !plan.limited || plan.groups != 1 || plan.markerCount != 1 || plan.markers[0].reason == "" {
				t.Fatal("oversized marker lost explicit limited outcome")
			}
		})
	}
}

func TestSourceConcurrentLegacyMarkerArgumentMapsStayBounded(t *testing.T) {
	cases := []map[string]string{
		{"arg1": "NULL", "arg2": "0", "value": "NULL"},
		{"arg1": strings.Repeat(" ", 4097) + "NULL"},
		{strings.Repeat("key", 12): "NULL"},
	}
	for i, args := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			fn := ParsedFunction{Name: "web_concurrent_start", Filename: "Action.c", Phase: "action", sourceFunction: "Action", Args: args}
			if sourceConcurrentMarkerArgs(fn) {
				t.Fatal("oversized legacy marker argument map admitted")
			}
			plan := planSourceConcurrentGroups([]ParsedFunction{fn}, nil)
			if !plan.limited || plan.groups != 1 || plan.markerCount != 1 || plan.markers[0].reason == "" {
				t.Fatal("uninspected map did not retain bounded review counts")
			}
		})
	}
	result := convertSingleWebSource(t, `web_concurrent_start(NULL);web_url("A","URL=https://example.test/a",LAST);web_concurrent_end(NULL);`)
	if len(concurrentSourceGroups(t, result.Script)) != 1 {
		t.Fatal("normal parsed arg1/value aliases were rejected")
	}
}

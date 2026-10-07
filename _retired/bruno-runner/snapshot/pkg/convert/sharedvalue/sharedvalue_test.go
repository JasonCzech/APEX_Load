package sharedvalue

import (
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func listParam(name, value string) dsl.Parameter {
	return dsl.Parameter{Name: name, Type: "list", Update: "once", Values: []string{value}}
}

func httpAction(name, target string) dsl.Action {
	return dsl.Action{Name: name, Type: "http_request", Method: "GET", Target: target}
}

// Bruno native emits one script per request, each carrying its own copy of the
// same collection variable. Collapsing that fan-out is the whole point.
func TestDetectGroupsExistingParameterAcrossScripts(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "get-user", Parameters: []dsl.Parameter{listParam("baseUrl", "https://stage.example.com")},
			Actions: []dsl.Action{httpAction("get", "{{var.baseUrl}}/users")}},
		{Name: "get-order", Parameters: []dsl.Parameter{listParam("baseUrl", "https://stage.example.com")},
			Actions: []dsl.Action{httpAction("get", "{{var.baseUrl}}/orders")}},
	}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "baseUrl")
	if candidate.Kind != KindExistingParameter {
		t.Fatalf("kind = %q, want existing_parameter", candidate.Kind)
	}
	if len(candidate.ScriptNames) != 2 {
		t.Fatalf("script names = %v, want both scripts", candidate.ScriptNames)
	}
	if candidate.CurrentValue != "https://stage.example.com" {
		t.Fatalf("current value = %q, want the value to pre-fill", candidate.CurrentValue)
	}
}

func TestApplyUpdatesExistingParameterInEveryScript(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "a", Parameters: []dsl.Parameter{listParam("baseUrl", "https://stage.example.com")}},
		{Name: "b", Parameters: []dsl.Parameter{listParam("baseUrl", "https://stage.example.com")}},
	}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "baseUrl")

	diffs, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("diffs = %d, want one per script", len(diffs))
	}
	for _, script := range scripts {
		if got := script.Parameters[0].Values[0]; got != "https://prod.example.com" {
			t.Fatalf("script %q value = %q, want the new value", script.Name, got)
		}
		if len(script.Parameters) != 1 {
			t.Fatalf("script %q gained a duplicate parameter: %+v", script.Name, script.Parameters)
		}
	}
}

// A second Apply with the same values must not append duplicate parameters —
// pkg/dsl rejects case-insensitively duplicated names.
func TestApplyIsIdempotent(t *testing.T) {
	scripts := []*dsl.Script{{
		Name:     "only",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions:  []dsl.Action{httpAction("get", "/{{var.tenant}}/orders")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")

	for pass := 0; pass < 2; pass++ {
		if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "acme"}); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if len(scripts[0].Parameters) != 1 {
		t.Fatalf("parameters = %+v, want exactly one", scripts[0].Parameters)
	}
	if issues := dsl.ValidateScriptIssues(scripts[0]); len(issues) > 0 {
		t.Fatalf("script no longer validates: %v", issues)
	}
}

func TestDetectSurfacesUnresolvedSourceVariable(t *testing.T) {
	scripts := []*dsl.Script{{
		Name:    "only",
		Actions: []dsl.Action{httpAction("get", "https://api.example.com/{{var.tenant}}/orders")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")
	if candidate.Kind != KindSourceVariable {
		t.Fatalf("kind = %q, want source_variable", candidate.Kind)
	}
}

func TestDetectRepeatedOriginAcrossScripts(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "a", Actions: []dsl.Action{httpAction("one", "https://api.example.com/users")}},
		{Name: "b", Actions: []dsl.Action{httpAction("two", "https://api.example.com/orders")}},
	}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "base_url")
	if candidate.Kind != KindRepeatedLiteral {
		t.Fatalf("kind = %q, want repeated_literal", candidate.Kind)
	}
	if candidate.OccurrenceCount != 2 {
		t.Fatalf("occurrences = %d, want 2", candidate.OccurrenceCount)
	}

	diffs, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("diffs = %d, want one rewrite per script", len(diffs))
	}
	for _, script := range scripts {
		if got := script.Actions[0].Target; !strings.HasPrefix(got, "{{param.base_url}}") {
			t.Fatalf("script %q target = %q, want the parameter reference", script.Name, got)
		}
		if parameterIndex(script, "base_url") < 0 {
			t.Fatalf("script %q did not gain the parameter", script.Name)
		}
	}
}

// A single occurrence is not a collection-wide value.
func TestDetectIgnoresSingleOccurrenceLiteral(t *testing.T) {
	scripts := []*dsl.Script{{
		Name:    "only",
		Actions: []dsl.Action{httpAction("one", "https://api.example.com/users")},
	}}
	for _, candidate := range Detect(scripts).Candidates {
		if candidate.Kind == KindRepeatedLiteral {
			t.Fatalf("unexpected repeated-literal candidate %+v", candidate)
		}
	}
}

// config.base_url is flattened once at protocol init, so it must receive the
// literal value and never a template.
func TestApplyWritesLiteralIntoBaseURL(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "a", Config: dsl.ScriptConfig{BaseURL: "https://api.example.com"},
			Actions: []dsl.Action{httpAction("one", "https://api.example.com/users")}},
		{Name: "b", Config: dsl.ScriptConfig{BaseURL: "https://api.example.com"},
			Actions: []dsl.Action{httpAction("two", "https://api.example.com/orders")}},
	}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "base_url")
	if !candidate.TargetsBaseURL {
		t.Fatal("candidate should record that it targets config.base_url")
	}
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, script := range scripts {
		if got := script.Config.BaseURL; got != "https://prod.example.com" {
			t.Fatalf("script %q base_url = %q, want the literal", script.Name, got)
		}
	}
}

func TestApplyRejectsCredentialShapedCandidate(t *testing.T) {
	candidate := Candidate{ID: "sv_test", Kind: KindSourceVariable, Name: "password",
		CredentialShaped: true, ScriptIndexes: []int{0}, ScriptNames: []string{"only"}}
	scripts := []*dsl.Script{{Name: "only"}}
	_, err := Apply(scripts, []Candidate{candidate}, map[string]string{"sv_test": "hunter2"})
	if !errors.Is(err, ErrCredentialShaped) {
		t.Fatalf("err = %v, want ErrCredentialShaped", err)
	}
}

// A credential-shaped name must never carry a value out of Detect, because the
// disclosure-safe projection would mask a parameter of that name and the value
// would be lost on the next save.
func TestDetectStripsCredentialShapedValues(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "a", Parameters: []dsl.Parameter{listParam("password", "hunter2")},
			Actions: []dsl.Action{httpAction("one", "{{var.password}}")}},
		{Name: "b", Parameters: []dsl.Parameter{listParam("password", "hunter2")},
			Actions: []dsl.Action{httpAction("two", "{{var.password}}")}},
	}
	candidate := findCandidate(t, Detect(scripts), "password")
	if !candidate.CredentialShaped {
		t.Fatal("password must be flagged credential-shaped")
	}
	if candidate.CurrentValue != "" {
		t.Fatalf("credential-shaped candidate leaked a value: %+v", candidate)
	}
}

// Structural headers are protocol mechanics, not environment settings.
func TestDetectSkipsStructuralHeaders(t *testing.T) {
	action := httpAction("one", "/users")
	action.Headers = map[string]string{"Content-Type": "application/json", "X-Tenant": "acme"}
	other := httpAction("two", "/orders")
	other.Headers = map[string]string{"Content-Type": "application/json", "X-Tenant": "acme"}
	scripts := []*dsl.Script{{Name: "a", Actions: []dsl.Action{action}}, {Name: "b", Actions: []dsl.Action{other}}}

	result := Detect(scripts)
	findCandidate(t, result, "X_Tenant")
	for _, candidate := range result.Candidates {
		if strings.EqualFold(candidate.Name, "Content_Type") {
			t.Fatalf("Content-Type must not be offered as a shared value: %+v", candidate)
		}
	}
}

// Candidate IDs are structural descriptors. A value must never be derivable
// from one, or an ID would become a credential oracle.
func TestCandidateIDExcludesValues(t *testing.T) {
	base := Candidate{Kind: KindRepeatedLiteral, Name: "token_hint",
		Locations: []Location{{ScriptIndex: 0, Path: "actions[0]", Field: FieldTarget}}}
	withValue := base
	withValue.LiteralValue = "s3cret"
	withValue.CurrentValue = "s3cret"
	if candidateID(base) != candidateID(withValue) {
		t.Fatal("candidate ID must not depend on the value")
	}
}

func TestDetectEmptyInput(t *testing.T) {
	if got := Detect(nil); len(got.Candidates) != 0 || got.Summary.Total != 0 {
		t.Fatalf("empty input produced %+v", got)
	}
}

func findCandidate(t *testing.T, result Result, name string) Candidate {
	t.Helper()
	for _, candidate := range result.Candidates {
		if strings.EqualFold(candidate.Name, name) {
			return candidate
		}
	}
	t.Fatalf("no candidate named %q in %+v", name, result.Candidates)
	return Candidate{}
}

// A single-script import still has a collection-wide base address, so
// config.base_url is offered without needing a second occurrence to prove it.
func TestDetectOffersBaseURLInSingleScriptImport(t *testing.T) {
	scripts := []*dsl.Script{{
		Name:     "only",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		Actions:  []dsl.Action{httpAction("get", "/users")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "base_url")
	if candidate.CurrentValue != "https://stage.example.com" {
		t.Fatalf("current value = %q, want the configured base URL", candidate.CurrentValue)
	}
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := scripts[0].Config.BaseURL; got != "https://prod.example.com" {
		t.Fatalf("base_url = %q, want the literal", got)
	}
	if issues := dsl.ValidateScriptIssues(scripts[0]); len(issues) > 0 {
		t.Fatalf("script no longer validates: %v", issues)
	}
}

// A gate stamped at conversion time does not heal itself, so supplying the
// value the gate asked for must also clear the gate.
func TestApplyClearsResolvedMissingVariableGate(t *testing.T) {
	action := httpAction("get", "/{{var.tenant}}/orders")
	action.Metadata = map[string]any{
		"execution_blocker": "bruno_missing_variables",
		"missing_variables": []string{"tenant"},
	}
	scripts := []*dsl.Script{{
		Name:     "only",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions:  []dsl.Action{action},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "acme"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	metadata := scripts[0].Actions[0].Metadata
	if _, blocked := metadata["execution_blocker"]; blocked {
		t.Fatalf("execution blocker survived: %+v", metadata)
	}
	if _, listed := metadata["missing_variables"]; listed {
		t.Fatalf("missing_variables survived: %+v", metadata)
	}
}

// An action blocked for another reason keeps that gate.
func TestApplyKeepsUnrelatedExecutionBlocker(t *testing.T) {
	action := httpAction("get", "/{{var.tenant}}/orders")
	action.Metadata = map[string]any{
		"execution_blocker":  "import_missing_variables",
		"execution_blockers": []string{"import_missing_variables", "unsupported_step"},
		"missing_variables":  []string{"tenant"},
	}
	scripts := []*dsl.Script{{
		Name:     "only",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions:  []dsl.Action{action},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "acme"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	metadata := scripts[0].Actions[0].Metadata
	if got := metadata["execution_blocker"]; got != "unsupported_step" {
		t.Fatalf("execution_blocker = %v, want the unrelated gate to survive", got)
	}
}

func csvBackedScript(name string) *dsl.Script {
	return &dsl.Script{
		Name:     name,
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		DataSources: []dsl.DataSource{{
			Name:           "postman",
			Type:           "csv",
			Delimiter:      ",",
			FirstRowHeader: true,
			Columns:        []string{"apiVersion", "tenant"},
			InlineData:     "apiVersion,tenant\nv1,acme\n",
		}},
		Parameters: []dsl.Parameter{
			{Name: "apiVersion", Type: "list", Update: "once", DataSource: "postman", Column: "apiVersion", Selection: "sequential"},
			{Name: "tenant", Type: "list", Update: "once", DataSource: "postman", Column: "tenant", Selection: "sequential"},
		},
		Actions: []dsl.Action{httpAction(name, "/{{var.apiVersion}}/users")},
	}
}

// Postman's default shape puts collection variables in a one-row inline CSV
// rather than inline parameter values. Those are still constants and must be
// editable, or the panel would be blank for the most common import.
func TestDetectAndApplyCSVBackedConstants(t *testing.T) {
	scripts := []*dsl.Script{csvBackedScript("collection")}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")
	if candidate.Kind != KindExistingParameter {
		t.Fatalf("kind = %q, want existing_parameter", candidate.Kind)
	}
	if candidate.CurrentValue != "acme" {
		t.Fatalf("current value = %q, want the CSV cell", candidate.CurrentValue)
	}

	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "globex"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	data := scripts[0].DataSources[0].InlineData
	if !strings.Contains(data, "v1,globex") {
		t.Fatalf("inline data = %q, want the tenant cell rewritten and apiVersion preserved", data)
	}
	if len(scripts[0].Parameters) != 2 {
		t.Fatalf("parameters = %+v, want no duplicate minted", scripts[0].Parameters)
	}
	if issues := dsl.ValidateScriptIssues(scripts[0]); len(issues) > 0 {
		t.Fatalf("script no longer validates: %v", issues)
	}
}

// A multi-row CSV is real per-VUser test data. Collapsing it to one
// operator-entered value would destroy that data, so it is never offered.
func TestDetectSkipsMultiRowDataSource(t *testing.T) {
	script := csvBackedScript("collection")
	script.DataSources[0].InlineData = "apiVersion,tenant\nv1,acme\nv2,globex\n"
	for _, candidate := range Detect([]*dsl.Script{script}).Candidates {
		if candidate.Kind == KindExistingParameter {
			t.Fatalf("multi-row data must not be offered as a constant: %+v", candidate)
		}
	}
}

// One header reachable through both the legacy map and HeaderList is one
// occurrence the operator can see, not two.
func TestDetectCountsHeaderOccurrenceOnce(t *testing.T) {
	build := func(name string) *dsl.Script {
		action := httpAction(name, "/users")
		action.Headers = map[string]string{"X-Tenant": "acme"}
		action.HeaderList = dsl.HeaderList{{Name: "X-Tenant", Value: "acme"}}
		return &dsl.Script{Name: name, Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://stage.example.com"}, Actions: []dsl.Action{action}}
	}
	scripts := []*dsl.Script{build("a"), build("b")}
	candidate := findCandidate(t, Detect(scripts), "X_Tenant")
	if candidate.OccurrenceCount != 2 {
		t.Fatalf("occurrences = %d, want one per request", candidate.OccurrenceCount)
	}
}

// Both physical spellings of a header must still be rewritten, or the legacy
// map and HeaderList would disagree after an apply.
func TestApplyRewritesBothHeaderSpellings(t *testing.T) {
	build := func(name string) *dsl.Script {
		action := httpAction(name, "/users")
		action.Headers = map[string]string{"X-Tenant": "acme"}
		action.HeaderList = dsl.HeaderList{{Name: "X-Tenant", Value: "acme"}}
		return &dsl.Script{Name: name, Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://stage.example.com"}, Actions: []dsl.Action{action}}
	}
	scripts := []*dsl.Script{build("a"), build("b")}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "X_Tenant")
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "globex"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, script := range scripts {
		action := script.Actions[0]
		if action.Headers["X-Tenant"] != "{{param.X_Tenant}}" {
			t.Fatalf("script %q header map = %q", script.Name, action.Headers["X-Tenant"])
		}
		if action.HeaderList[0].Value != "{{param.X_Tenant}}" {
			t.Fatalf("script %q header list = %q", script.Name, action.HeaderList[0].Value)
		}
	}
}

// The Bruno native importer copies the source variable straight into
// config.base_url, which the HTTP runtime flattens before any VUser exists.
// Supplying the value must substitute the literal there and clear the gate, or
// the operator does the fix and the script still refuses to run.
func TestApplyLiteralizesTemplatedBaseURLAndClearsGate(t *testing.T) {
	build := func(name, path string) *dsl.Script {
		action := httpAction(name, "{{var.baseUrl}}"+path)
		action.Metadata = map[string]any{
			"execution_blocker":  "bruno_missing_variables",
			"execution_blockers": []string{"bruno_missing_variables", "import_review_required"},
			"missing_variables":  []string{"baseUrl"},
			"import_review": map[string]any{
				"version":      1,
				"reason_codes": []any{"base_url_runtime_template_unavailable"},
				"source_refs":  []any{"/config/base_url"},
				"resolution":   "replace_with_literal_or_secret_reference_and_remove_gate",
			},
		}
		return &dsl.Script{
			Name: name, Protocol: "http",
			Config:  dsl.ScriptConfig{BaseURL: "{{var.baseUrl}}" + path},
			Actions: []dsl.Action{action},
		}
	}
	scripts := []*dsl.Script{build("get-users", "/users"), build("get-orders", "/orders")}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "baseUrl")
	if candidate.Kind != KindSourceVariable {
		t.Fatalf("kind = %q, want source_variable", candidate.Kind)
	}

	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, script := range scripts {
		if strings.Contains(script.Config.BaseURL, "{{") {
			t.Fatalf("script %q base_url still templated: %q", script.Name, script.Config.BaseURL)
		}
		if !strings.HasPrefix(script.Config.BaseURL, "https://prod.example.com") {
			t.Fatalf("script %q base_url = %q", script.Name, script.Config.BaseURL)
		}
		metadata := script.Actions[0].Metadata
		if _, blocked := metadata["execution_blocker"]; blocked {
			t.Fatalf("script %q kept a gate: %+v", script.Name, metadata)
		}
		if _, review := metadata["import_review"]; review {
			t.Fatalf("script %q kept its review record: %+v", script.Name, metadata)
		}
		// The action target keeps the parameter reference; only base_url must be
		// a literal, because only base_url resolves before a VUser exists.
		if script.Actions[0].Target != "{{var.baseUrl}}"+strings.TrimPrefix(script.Config.BaseURL, "https://prod.example.com") {
			t.Fatalf("script %q target was rewritten unexpectedly: %q", script.Name, script.Actions[0].Target)
		}
	}
}

// A template naming a variable this apply did not touch is a pre-existing
// importer condition; failing the whole apply over it would block the operator
// from fixing the parts they can.
func TestApplyLeavesUnrelatedBaseURLTemplateAlone(t *testing.T) {
	scripts := []*dsl.Script{{
		Name: "only", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "{{var.region}}.example.com/{{var.tenant}}"},
		Actions: []dsl.Action{httpAction("get", "/orders")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "acme"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := scripts[0].Config.BaseURL; got != "{{var.region}}.example.com/acme" {
		t.Fatalf("base_url = %q, want only the applied variable substituted", got)
	}
}

// Two collection folders can hold a request of the same name. Matching apply
// targets by name would apply a candidate found in one script to its namesake.
func TestApplyTargetsScriptsByPositionNotName(t *testing.T) {
	build := func(value string) *dsl.Script {
		return &dsl.Script{
			Name: "get-users", Protocol: "http",
			Config:     dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
			Parameters: []dsl.Parameter{listParam("tenant", value)},
			Actions:    []dsl.Action{httpAction("get", "/users")},
		}
	}
	scripts := []*dsl.Script{build("acme"), build("globex")}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "tenant")

	// Both scripts are affected and both are reported, even though they share a
	// name — otherwise the operator is told one script is in scope while two
	// get overwritten.
	if len(candidate.ScriptIndexes) != 2 || len(candidate.ScriptNames) != 2 {
		t.Fatalf("affected scripts = %v / %v, want both positions", candidate.ScriptIndexes, candidate.ScriptNames)
	}

	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "initech"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, script := range scripts {
		if script.Parameters[0].Values[0] != "initech" {
			t.Fatalf("script value = %q, want the applied value", script.Parameters[0].Values[0])
		}
	}
}

// A candidate detected against a shorter slice must not mutate an unrelated
// script if the caller hands Apply a different one.
func TestApplyIgnoresOutOfRangeScriptIndex(t *testing.T) {
	candidate := Candidate{
		ID: "sv_test", Kind: KindSourceVariable, Name: "tenant",
		ScriptIndexes: []int{5}, ScriptNames: []string{"gone"},
	}
	scripts := []*dsl.Script{{Name: "only", Protocol: "http"}}
	if _, err := Apply(scripts, []Candidate{candidate}, map[string]string{"sv_test": "acme"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(scripts[0].Parameters) != 0 {
		t.Fatalf("out-of-range index mutated a script: %+v", scripts[0].Parameters)
	}
}

// Two different headers that happen to carry the same string are two settings.
// Merging them would name the candidate after whichever was walked last and let
// two single-occurrence headers clear the shared-reach bar.
func TestDetectKeepsDistinctHeadersApart(t *testing.T) {
	build := func(name, header string) *dsl.Script {
		action := httpAction("get", "/users")
		action.Headers = map[string]string{header: "acme"}
		return &dsl.Script{Name: name, Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://stage.example.com"}, Actions: []dsl.Action{action}}
	}
	scripts := []*dsl.Script{build("a", "X-Tenant"), build("b", "X-Region")}
	for _, candidate := range Detect(scripts).Candidates {
		if candidate.Kind == KindRepeatedLiteral && candidate.Name != "base_url" {
			t.Fatalf("distinct headers were merged into %+v", candidate)
		}
	}
}

// The operator's value is used, not the whitespace around it.
func TestApplyTrimsOperatorValue(t *testing.T) {
	scripts := []*dsl.Script{{
		Name: "only", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		Actions: []dsl.Action{httpAction("get", "/users")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "base_url")
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "  https://prod.example.com  "}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := scripts[0].Config.BaseURL; got != "https://prod.example.com" {
		t.Fatalf("base_url = %q, want the trimmed value", got)
	}
}

// The engine trims whitespace inside {{ }}, so a spaced reference in base_url is
// live and must be substituted like any other.
func TestApplyLiteralizesSpacedBaseURLTemplate(t *testing.T) {
	scripts := []*dsl.Script{{
		Name: "only", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "{{ var.baseUrl }}/api"},
		Actions: []dsl.Action{httpAction("get", "{{var.baseUrl}}/api/users")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "baseUrl")
	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := scripts[0].Config.BaseURL; got != "https://prod.example.com/api" {
		t.Fatalf("base_url = %q, want the spaced template substituted", got)
	}
}

// A literal that only exists because the disclosure projection replaced it can
// never match the exact draft the mutation lands on, so it must not be offered.
func TestDetectSkipsRedactedLiterals(t *testing.T) {
	build := func(name string) *dsl.Script {
		action := httpAction("get", "/users")
		action.Headers = map[string]string{"X-Trace": credential.Redacted}
		return &dsl.Script{Name: name, Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://stage.example.com"}, Actions: []dsl.Action{action}}
	}
	scripts := []*dsl.Script{build("a"), build("b")}
	for _, candidate := range Detect(scripts).Candidates {
		if strings.EqualFold(candidate.Name, "X_Trace") {
			t.Fatalf("a redacted value was offered as a shared value: %+v", candidate)
		}
	}
}

// Rewriting nothing must not mint a parameter. The config.base_url diff alone
// used to satisfy the guard, leaving one that no field references.
func TestApplyMintsNoParameterWhenNoFieldRewritten(t *testing.T) {
	scripts := []*dsl.Script{{
		Name: "only", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		Actions: []dsl.Action{httpAction("get", "https://stage.example.com/users")},
	}}
	result := Detect(scripts)
	candidate := findCandidate(t, result, "base_url")
	// Parameterize the action target out from under the candidate so the walk
	// finds nothing left to replace, while config.base_url still changes.
	scripts[0].Actions[0].Target = "/users"

	if _, err := Apply(scripts, result.Candidates, map[string]string{candidate.ID: "https://prod.example.com"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(scripts[0].Parameters) != 0 {
		t.Fatalf("orphan parameter minted: %+v", scripts[0].Parameters)
	}
	if got := scripts[0].Config.BaseURL; got != "https://prod.example.com" {
		t.Fatalf("base_url = %q", got)
	}
}

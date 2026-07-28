package dsl

import (
	"testing"
)

func TestValidateScriptIssues_Parameters(t *testing.T) {
	script := &Script{
		Name:     "param-invalid",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.com",
		},
		Parameters: []Parameter{
			{Name: "", Type: "counter", Step: 1},
			{Name: "dup", Type: "file"},
			{Name: "dup", Type: "random_number", Min: 10, Max: 1},
			{Name: "clock", Type: "date_time", Timezone: "Mars/Olympus"},
			{Name: "token", Type: "random_string", Length: 0},
			{Name: "password", Type: "file", DataSource: "users", Column: "password", SameLineAs: "missing"},
		},
		Actions: []Action{{Name: "call", Type: "request", Target: "/ping"}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "parameters[0]: name is required")
	assertIssue(t, issues, "parameters[1] \"dup\": data_source is required for file parameters")
	assertIssue(t, issues, "parameters[1] \"dup\": column is required for file parameters")
	assertIssue(t, issues, "parameters[2] \"dup\": duplicate name")
	assertIssue(t, issues, "parameters[2] \"dup\": min must be less than or equal to max")
	assertIssue(t, issues, "parameters[3] \"clock\": invalid timezone \"Mars/Olympus\"")
	assertIssue(t, issues, "parameters[4] \"token\": length must be greater than zero for random_string parameters")
	assertIssue(t, issues, "parameters[5] \"password\": same_line_as references unknown parameter \"missing\"")
}

func TestValidateScriptIssues_RejectsCaseInsensitiveParameterDuplicates(t *testing.T) {
	script := &Script{
		Name:     "case-duplicate",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.com"},
		Parameters: []Parameter{
			{Name: "Token", Type: "list", Values: []string{"first"}},
			{Name: " token ", Type: "list", Values: []string{"second"}},
		},
		Actions: []Action{{Name: "call", Type: "request", Target: "/{{var.TOKEN}}"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `parameters[1] "token": duplicate name`)
}

func TestValidateScriptIssuesRejectsHeaderAsData(t *testing.T) {
	script := &Script{
		Name: "soap", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"},
		DataSources: []DataSource{{Name: "users", Type: "dat", InlineData: "UserName,Password\nalice,secret", Columns: []string{"UserName", "Password"}, FirstRowHeader: false}},
		Parameters:  []Parameter{{Name: "version", Type: "file", DataSource: "users", Column: "Version"}},
		Actions:     []Action{{Name: "call", Type: "POST", Target: "/soap"}},
	}
	issues := ValidateScriptIssues(script)
	assertIssueContains(t, issues, `first record matches declared columns while first_row_header is false`)
}

func TestResolveDataSourceDelimiterDetectsLegacyCommaDelimitedDat(t *testing.T) {
	source := DataSource{
		Name:           "accounts",
		Type:           "dat",
		InlineData:     "billingAccountNumber,billingMarket\n177054701279,337116375\n",
		FirstRowHeader: true,
	}
	if got := ResolveDataSourceDelimiter(source, source.InlineData); got != ',' {
		t.Fatalf("ResolveDataSourceDelimiter() = %q, want comma", got)
	}

	singleColumn := DataSource{
		Name:       "notes",
		Type:       "dat",
		InlineData: "hello, world\ngoodbye, world\n",
	}
	if got := ResolveDataSourceDelimiter(singleColumn, singleColumn.InlineData); got != '\t' {
		t.Fatalf("single-column legacy .dat delimiter = %q, want tab fallback", got)
	}
}

func TestValidateScriptIssues_ParametersAllowsSupportedValues(t *testing.T) {
	seed := int64(42)
	script := &Script{
		Name:     "param-valid",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.com",
		},
		Parameters: []Parameter{
			{Name: "account_id", Type: "counter", Update: "each_iteration", Step: 1, Start: 1000, End: 1002, OnExhaustion: "wrap", PadWidth: 6},
			{Name: "iteration", Type: "current_iteration", Update: "each_iteration", PadWidth: 4},
			{Name: "lg_name", Type: "host", Update: "once"},
			{Name: "clock", Type: "time", Format: "15:04:05", Timezone: "UTC"},
			{Name: "lr_iteration_alias", Type: "CurrentIteration", Update: "each_iteration"},
			{Name: "lr_host_alias", Type: "Host", Update: "once"},
			{Name: "lr_time_alias", Type: "Time", Format: "15:04:05"},
			{Name: "region", Type: "list", Selection: "random", Values: []string{"us-east-1", "us-west-2"}, Seed: &seed},
			{Name: "username", Type: "file", DataSource: "users", Column: "username", Selection: "unique", OnExhaustion: "wrap"},
			{Name: "password", Type: "file", DataSource: "users", Column: "password", Selection: "random", SameLineAs: "username"},
			{Name: "timestamp", Type: "timestamp", Format: "iso8601", Timezone: "UTC"},
		},
		DataSources: []DataSource{{Name: "users", Type: "csv", InlineData: "username,password\nalice,secret\n", FirstRowHeader: true}},
		Actions:     []Action{{Name: "call", Type: "request", Target: "/ping"}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

// A parameter column that does not exist in its data source is a data-source FIT
// problem, not a structural one. ValidateScriptIssues gates script save, test
// creation, persistence reload AND generator-side execution staging, so making
// this structural would reject already-stored scripts that run today. The fit
// report (authoring health "data_source_fit") is the channel that raises it - see
// TestDraftValidationReturnsDataSourceFitHealth, which asserts structural
// validity stays true while fit reports blocked.
//
// This asserts the POSITIVE contract (no issues at all for the fixture). An
// earlier version asserted the absence of a specific message string that no
// validator ever emits, so it passed unconditionally and would not have caught a
// re-escalation worded any other way.
func TestValidateScriptIssues_UnresolvableParameterColumnIsFitNotStructural(t *testing.T) {
	script := &Script{
		Name:        "legacy-param",
		Protocol:    "http",
		Config:      ScriptConfig{BaseURL: "https://example.com"},
		Parameters:  []Parameter{{Name: "username", Type: "file", DataSource: "users", Column: "UserName"}},
		DataSources: []DataSource{{Name: "users", Type: "dat", InlineData: "alice\n", Strategy: "sequential"}},
		Actions:     []Action{{Name: "call", Type: "request", Target: "/ping"}},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, a missing parameter column must not be a structural validation error", issues)
	}
}

// The structural checks the function DOES own must still fire, so the test above
// cannot pass merely because validation stopped running.
func TestValidateScriptIssues_RejectsUnknownDataSourceReference(t *testing.T) {
	script := &Script{
		Name:        "missing-source",
		Protocol:    "http",
		Config:      ScriptConfig{BaseURL: "https://example.com"},
		Parameters:  []Parameter{{Name: "username", Type: "file", DataSource: "nope", Column: "UserName"}},
		DataSources: []DataSource{{Name: "users", Type: "dat", InlineData: "alice\n", Strategy: "sequential"}},
		Actions:     []Action{{Name: "call", Type: "request", Target: "/ping"}},
	}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `parameters[0] "username": data_source "nope" does not exist`)
}

func TestValidateScriptIssues_RejectsParameterHeaderConsumedAsData(t *testing.T) {
	script := &Script{
		Name:        "header-as-data",
		Protocol:    "http",
		Config:      ScriptConfig{BaseURL: "https://example.com"},
		Parameters:  []Parameter{{Name: "username", Type: "file", DataSource: "users", Column: "UserName"}},
		DataSources: []DataSource{{Name: "users", Type: "dat", InlineData: "UserName\nalice\n", Columns: []string{"UserName"}, Strategy: "sequential"}},
		Actions:     []Action{{Name: "call", Type: "request", Target: "/ping"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `parameters[0] "username": data_source "users" first record matches its columns but first_row_header is false`)
}

func TestValidateScriptIssues_ParameterColumnsRespectInspectableSchema(t *testing.T) {
	tests := []struct {
		name   string
		source DataSource
	}{
		{name: "tab header", source: DataSource{Name: "users", Type: "dat", InlineData: "UserName\tPassword\nalice\tsecret\n", Delimiter: "tab", FirstRowHeader: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &Script{
				Name:        "parameter-schema",
				Protocol:    "http",
				Config:      ScriptConfig{BaseURL: "https://example.com"},
				Parameters:  []Parameter{{Name: "username", Type: "file", DataSource: "users", Column: "UserName"}},
				DataSources: []DataSource{test.source},
				Actions:     []Action{{Name: "call", Type: "request", Target: "/ping"}},
			}
			if issues := ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("issues = %v, want none", issues)
			}
		})
	}
}

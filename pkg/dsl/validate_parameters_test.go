package dsl

import "testing"

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
			{Name: "timestamp", Type: "timestamp", Format: "iso8601", Timezone: "UTC"},
		},
		DataSources: []DataSource{{Name: "users", Type: "csv", InlineData: "username\nalice\n", FirstRowHeader: true}},
		Actions:     []Action{{Name: "call", Type: "request", Target: "/ping"}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

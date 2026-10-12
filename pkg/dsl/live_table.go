package dsl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// LiveTableAction is the authored operation. Identities, credentials and table
// IDs are deliberately absent: trusted run bindings supply those at execution.
type LiveTableAction struct {
	Alias           string            `json:"alias"`
	Operation       string            `json:"operation"`
	ResultPrefix    string            `json:"result_prefix,omitempty"`
	RecordID        string            `json:"record_id,omitempty"`
	Position        int               `json:"position,omitempty"`
	Columns         []string          `json:"columns,omitempty"`
	Values          map[string]string `json:"values,omitempty"`
	Expected        map[string]string `json:"expected,omitempty"`
	ExpectedVersion string            `json:"expected_version,omitempty"`
	Delta           int64             `json:"delta,omitempty"`
	LeaseRef        string            `json:"lease_ref,omitempty"`
	LeaseMS         int               `json:"lease_ms,omitempty"`
	WaitMS          int               `json:"wait_ms,omitempty"`
	Limit           int               `json:"limit,omitempty"`
	Cursor          string            `json:"cursor,omitempty"`
	OnEmpty         string            `json:"on_empty,omitempty"`
	OnCondition     string            `json:"on_condition,omitempty"`
}

var liveAliasPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,127}$`)
var livePrefixPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

// ParseLiveTableAction validates types and operation-specific fields, including
// explicitly supplied zero/empty fields. It is used again at execution so
// direct Go callers cannot bypass the DSL contract.
func ParseLiveTableAction(params map[string]any) (LiveTableAction, error) {
	var action LiveTableAction
	encoded, err := json.Marshal(params)
	if err != nil || len(encoded) > 1<<20 {
		return action, fmt.Errorf("live_table parameters are invalid or exceed 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return action, fmt.Errorf("live_table parameters have invalid fields or types")
	}
	if !liveAliasPattern.MatchString(action.Alias) {
		return action, fmt.Errorf("live_table alias must be a static identifier of 1..128 characters")
	}
	allowed := map[string]bool{"alias": true, "operation": true, "result_prefix": true}
	allow := func(fields ...string) {
		for _, field := range fields {
			allowed[field] = true
		}
	}
	switch action.Operation {
	case "read":
		allow("record_id", "position", "columns", "limit", "wait_ms", "on_empty")
		if (action.RecordID == "") == (action.Position == 0) {
			return action, fmt.Errorf("live_table read requires exactly one of record_id or position")
		}
	case "next", "pop":
		allow("columns", "wait_ms", "on_empty")
		if action.Operation == "next" {
			allow("cursor")
		}
	case "lease":
		allow("lease_ms", "wait_ms", "on_empty")
		if action.ResultPrefix == "" {
			return action, fmt.Errorf("live_table lease requires result_prefix as its private lease reference")
		}
	case "renew", "ack", "release":
		allow("lease_ref")
		if action.Operation == "renew" {
			allow("lease_ms")
		}
		if !livePrefixPattern.MatchString(action.LeaseRef) {
			return action, fmt.Errorf("live_table %s requires a static lease_ref", action.Operation)
		}
	case "push":
		allow("values")
		if len(action.Values) == 0 {
			return action, fmt.Errorf("live_table push requires values")
		}
	case "update":
		allow("record_id", "position", "values", "expected", "expected_version", "on_condition")
		if len(action.Values) == 0 || ((action.RecordID == "") == (action.Position == 0)) {
			return action, fmt.Errorf("live_table update requires values and exactly one of record_id or position")
		}
		if (action.RecordID != "" && action.ExpectedVersion == "") || (action.Position != 0 && action.ExpectedVersion != "") {
			return action, fmt.Errorf("live_table native update requires expected_version; positional update uses expected cells")
		}
	case "increment":
		allow("record_id", "position", "columns", "delta", "expected", "on_condition")
		if len(action.Columns) != 1 || ((action.RecordID == "") == (action.Position == 0)) {
			return action, fmt.Errorf("live_table increment requires one column and exactly one of record_id or position")
		}
	case "clear":
		allow("position", "columns", "expected", "on_condition")
		if action.Position <= 0 {
			return action, fmt.Errorf("live_table clear requires a positive position")
		}
	case "rotate":
		allow("columns", "on_empty")
	case "create_column":
		allow("columns")
		if len(action.Columns) != 1 {
			return action, fmt.Errorf("live_table create_column requires exactly one column")
		}
	case "read_column":
		allow("columns", "position", "limit", "on_empty")
		if len(action.Columns) != 1 {
			return action, fmt.Errorf("live_table read_column requires exactly one column")
		}
	default:
		return action, fmt.Errorf("live_table operation is unsupported")
	}
	for field, value := range params {
		if !allowed[field] {
			return action, fmt.Errorf("live_table %s does not accept %s", action.Operation, field)
		}
		if value == nil {
			return action, fmt.Errorf("live_table %s must not be null", field)
		}
	}
	if action.ResultPrefix != "" && !livePrefixPattern.MatchString(action.ResultPrefix) {
		return action, fmt.Errorf("live_table result_prefix must be a static variable prefix")
	}
	if action.Position < 0 || action.Position > 1_000_000 || action.Limit < 0 || action.Limit > 1000 {
		return action, fmt.Errorf("live_table position or limit exceeds its bounds")
	}
	if action.WaitMS < 0 || action.WaitMS > 60_000 || action.LeaseMS < 0 || action.LeaseMS > 300_000 {
		return action, fmt.Errorf("live_table wait_ms must be 0..60000 and lease_ms 0..300000")
	}
	if action.OnEmpty != "" && action.OnEmpty != "error" && action.OnEmpty != "skip" {
		return action, fmt.Errorf("live_table on_empty must be error or skip")
	}
	if action.OnCondition != "" && action.OnCondition != "error" && action.OnCondition != "skip" {
		return action, fmt.Errorf("live_table on_condition must be error or skip")
	}
	if action.ExpectedVersion != "" && !strings.Contains(action.ExpectedVersion, "{{") {
		version, err := strconv.ParseUint(action.ExpectedVersion, 10, 64)
		if err != nil || version == 0 {
			return action, fmt.Errorf("live_table expected_version must be a positive decimal string or template")
		}
	}
	if len(action.Columns) > 128 || len(action.Values) > 128 || len(action.Expected) > 128 {
		return action, fmt.Errorf("live_table accepts at most 128 columns")
	}
	seen := map[string]bool{}
	for _, column := range action.Columns {
		if !liveAliasPattern.MatchString(column) || seen[column] {
			return action, fmt.Errorf("live_table columns must be distinct static identifiers")
		}
		seen[column] = true
	}
	for _, values := range []map[string]string{action.Values, action.Expected} {
		for column, value := range values {
			if !liveAliasPattern.MatchString(column) || len(value) > 16<<10 {
				return action, fmt.Errorf("live_table values require valid column names and at most 16 KiB per value")
			}
			if strings.Contains(value, "{{secret.") {
				return action, fmt.Errorf("live_table cannot store managed secret references")
			}
		}
	}
	return action, nil
}

func validateLiveTableAction(index int, action Action) []string {
	if _, err := ParseLiveTableAction(action.Params); err != nil {
		return []string{fmt.Sprintf("actions[%d]: %v", index, err)}
	}
	return nil
}

// ScriptLiveTableAliases visits every lifecycle and nested action. The sorted
// result is useful to execution admission and authoring capability checks.
func ScriptLiveTableAliases(script *Script) ([]string, error) {
	if script == nil {
		return nil, nil
	}
	if issues := validateScriptStructureIssues(script); len(issues) != 0 {
		return nil, fmt.Errorf("live table action inspection: %s", issues[0])
	}
	seen := map[string]bool{}
	var walk func([]Action) error
	walk = func(actions []Action) error {
		for _, action := range actions {
			if CanonicalActionType(action.Type) == "live_table" {
				parsed, err := ParseLiveTableAction(action.Params)
				if err != nil {
					return err
				}
				seen[parsed.Alias] = true
			}
			if action.Control != nil {
				for _, branch := range action.Control.ActionBranches() {
					if err := walk(branch.Actions); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, actions := range [][]Action{script.Init, script.Actions, script.Teardown} {
		if err := walk(actions); err != nil {
			return nil, err
		}
	}
	aliases := make([]string, 0, len(seen))
	for alias := range seen {
		aliases = append(aliases, alias)
	}
	slices.Sort(aliases)
	return aliases, nil
}

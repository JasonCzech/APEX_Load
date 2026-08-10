package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	scriptDataTargetListValue   = "list-parameter-password-canary"
	scriptDataTargetColumnValue = "inline-data-password-canary"
	scriptDataTargetSecondRow   = "inline-data-password-canary-two"
	scriptDataTargetNeighbour   = "inline-data-username-neighbour"
)

// scriptDataTargetScript carries one credential-supplying list parameter and one
// parameter the runtime resolves out of an inline data source column, which are
// the two surfaces pkg/convert/requestimport publishes locators for.
func scriptDataTargetScript(targets []any) *dsl.Script {
	return &dsl.Script{
		Name:     "retained-script-data-credentials",
		Protocol: "stub",
		Parameters: []dsl.Parameter{
			{
				Name:   "login_password",
				Type:   "list",
				Values: []string{scriptDataTargetListValue},
			},
			{
				Name:       "db_password",
				Type:       "file",
				DataSource: "creds",
				Column:     "password",
			},
		},
		DataSources: []dsl.DataSource{{
			Name:           "creds",
			Type:           "csv",
			Strategy:       "sequential",
			FirstRowHeader: true,
			InlineData: "user,password,retries\n" +
				scriptDataTargetNeighbour + "," + scriptDataTargetColumnValue + ",3\n" +
				"bob," + scriptDataTargetSecondRow + ",4\n",
		}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "requestimport",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey:          dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: targets,
			},
		}},
	}
}

func scriptDataTarget(field, pointer string) []any {
	target := map[string]any{"field": field}
	if pointer != "" {
		target["pointer"] = pointer
	}
	return []any{target}
}

// A parameter locator must resolve to the values the runtime substitutes. It
// used to hit the unsupported-field branch, which refused the run outright.
func TestParameterValuesLocatorResolvesRetainedValues(t *testing.T) {
	t.Parallel()
	script := scriptDataTargetScript(scriptDataTarget("parameters[0].values", ""))

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !containsExactString(values, scriptDataTargetListValue) {
		t.Fatalf("retained runtime values = %#v, want the parameter value %q",
			values, scriptDataTargetListValue)
	}
}

// The data-source locator names its credential column, and only that column may
// enter the matcher: registering the neighbouring cells would redact unrelated
// data out of the operator's own run evidence.
func TestDataSourceInlineDataLocatorResolvesOnlyItsColumn(t *testing.T) {
	t.Parallel()
	script := scriptDataTargetScript(scriptDataTarget("data_sources[0].inline_data", "password"))

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{scriptDataTargetColumnValue, scriptDataTargetSecondRow} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want the column value %q", values, want)
		}
	}
	for _, unwanted := range []string{scriptDataTargetNeighbour, "3", "4"} {
		if containsExactString(values, unwanted) {
			t.Fatalf("retained runtime values = %#v, want no cell from an unrelated column (%q)",
				values, unwanted)
		}
	}
}

// The importer matches columns case-insensitively, so a header whose case
// differs from the parameter binding must still resolve rather than silently
// registering nothing.
func TestDataSourceInlineDataLocatorResolvesFoldedColumn(t *testing.T) {
	t.Parallel()
	script := scriptDataTargetScript(scriptDataTarget("data_sources[0].inline_data", "PassWord"))

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !containsExactString(values, scriptDataTargetColumnValue) {
		t.Fatalf("retained runtime values = %#v, want the folded column resolved", values)
	}
}

// A locator with no column resolves to nothing, but it must still RESOLVE: an
// error here refuses the run, which is exactly the failure this contract fixed.
func TestDataSourceInlineDataLocatorWithoutColumnResolvesToNothing(t *testing.T) {
	t.Parallel()
	script := scriptDataTargetScript(scriptDataTarget("data_sources[0].inline_data", ""))

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, unwanted := range []string{
		scriptDataTargetColumnValue,
		scriptDataTargetSecondRow,
		scriptDataTargetNeighbour,
	} {
		if containsExactString(values, unwanted) {
			t.Fatalf("retained runtime values = %#v, want no unscoped inline-data cell (%q)",
				values, unwanted)
		}
	}
}

// A locator that no longer addresses anything means the import moved under it,
// so it keeps the fail-closed behaviour every other retained locator has.
func TestScriptDataLocatorsFailClosedWhenTheyNoLongerMatch(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		field   string
		pointer string
		want    string
	}{
		{
			name:  "parameter index past the end",
			field: "parameters[7].values",
			want:  "retained parameter credential locator no longer matches executable script data",
		},
		{
			name:    "data source index past the end",
			field:   "data_sources[7].inline_data",
			pointer: "password",
			want:    "retained data source credential locator no longer matches executable script data",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			script := scriptDataTargetScript(scriptDataTarget(testCase.field, testCase.pointer))

			_, err := ImportedCredentialValues(script)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("ImportedCredentialValues = %v, want %q", err, testCase.want)
			}
		})
	}
}

// An in-range parameter whose values a later conversion pass emptied is not a
// corrupted import: there is no value left to mask, and failing the run would
// re-create the defect this contract removed.
func TestParameterValuesLocatorToleratesAnEmptiedValueList(t *testing.T) {
	t.Parallel()
	script := scriptDataTargetScript(scriptDataTarget("parameters[0].values", ""))
	script.Parameters[0].Values = []string{""}

	if _, err := ImportedCredentialValues(script); err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
}

// The defect these locators caused was a REFUSED RUN, not a missed redaction:
// runtimeSecretValuesForScript propagates the locator error before it ever
// consults the operator's setting, so a script carrying them failed to start
// with run-evidence redaction ON and OFF alike. Both readings must now reach the
// chokepoint cleanly, and the setting must still decide which script-sourced
// values participate.
func TestScriptDataLocatorsReachTheRunChokepointInBothModes(t *testing.T) {
	t.Parallel()
	for _, redact := range []bool{true, false} {
		t.Run(fmt.Sprintf("redact_run_evidence=%t", redact), func(t *testing.T) {
			t.Parallel()
			script := scriptDataTargetScript([]any{
				map[string]any{"field": "parameters[0].values"},
				map[string]any{"field": "data_sources[0].inline_data", "pointer": "password"},
			})
			setting := redact
			script.Config.Log.RedactRunEvidence = &setting

			platformManaged, scriptSourced, _, err := runtimeSecretValuesForScript(script, nil, nil)
			if err != nil {
				t.Fatalf("runtimeSecretValuesForScript: %v", err)
			}
			if len(platformManaged) != 0 {
				t.Fatalf("platform-managed values = %#v, want none for a script-sourced-only import",
					platformManaged)
			}
			for _, value := range []string{scriptDataTargetListValue, scriptDataTargetColumnValue} {
				if containsExactString(scriptSourced, value) != redact {
					t.Fatalf("script-sourced values = %#v, want %q registered=%t",
						scriptSourced, value, redact)
				}
			}
		})
	}
}

// Column naming without a header row must follow the runtime loader, which uses
// the declared columns and otherwise generates col_1, col_2, ...
func TestDataSourceInlineDataLocatorResolvesHeaderlessColumns(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		columns []string
		pointer string
	}{
		{name: "declared columns", columns: []string{"user", "password"}, pointer: "password"},
		{name: "generated columns", pointer: "col_2"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			script := scriptDataTargetScript(
				scriptDataTarget("data_sources[0].inline_data", testCase.pointer),
			)
			script.DataSources[0].FirstRowHeader = false
			script.DataSources[0].Columns = testCase.columns
			script.DataSources[0].InlineData = scriptDataTargetNeighbour + "," +
				scriptDataTargetColumnValue + "\n"

			values, err := ImportedCredentialValues(script)
			if err != nil {
				t.Fatalf("ImportedCredentialValues: %v", err)
			}
			if !containsExactString(values, scriptDataTargetColumnValue) {
				t.Fatalf("retained runtime values = %#v, want the %q column resolved",
					values, testCase.pointer)
			}
			if containsExactString(values, scriptDataTargetNeighbour) {
				t.Fatalf("retained runtime values = %#v, want no cell from an unrelated column",
					values)
			}
		})
	}
}

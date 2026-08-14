package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMergeParsedParameterFilesReusesIdenticalDefinitions(t *testing.T) {
	script := parameterMergeTestScript()
	state := newParameterMergeState(script)
	parsed := parameterMergeParsedFile("alice,basic\n")

	if collisions := mergeParsedParameterFile(script, parsed, "first.prm", state); len(collisions) != 0 {
		t.Fatalf("first merge collisions = %#v", collisions)
	}
	if collisions := mergeParsedParameterFile(script, parsed, "second.prm", state); len(collisions) != 0 {
		t.Fatalf("identical merge collisions = %#v", collisions)
	}
	if len(script.DataSources) != 1 || len(script.Parameters) != 2 {
		t.Fatalf("sources/parameters = %d/%d, want 1/2", len(script.DataSources), len(script.Parameters))
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("merged script validation issues = %v", issues)
	}
}

func TestMergeParsedParameterFilesAllocatesConflictsAndGatesReplay(t *testing.T) {
	script := parameterMergeTestScript()
	state := newParameterMergeState(script)
	if collisions := mergeParsedParameterFile(script, parameterMergeParsedFile("alice,basic\n"), "first.prm", state); len(collisions) != 0 {
		t.Fatalf("first merge collisions = %#v", collisions)
	}
	collisions := mergeParsedParameterFile(script, parameterMergeParsedFile("bob,admin\n"), "second.prm", state)
	if len(collisions) != 2 {
		t.Fatalf("collisions = %#v, want user and role", collisions)
	}

	if got := []string{script.DataSources[0].Name, script.DataSources[1].Name}; got[0] != "users" || got[1] != "users_2" {
		t.Fatalf("data-source names = %v, want [users users_2]", got)
	}
	if len(script.Parameters) != 4 {
		t.Fatalf("parameters = %#v, want four retained definitions", script.Parameters)
	}
	if got := script.Parameters[2]; got.Name != "user_2" || got.DataSource != "users_2" {
		t.Fatalf("conflicting user = %#v, want user_2 -> users_2", got)
	}
	if got := script.Parameters[3]; got.Name != "role_2" || got.DataSource != "users_2" || got.SameLineAs != "user_2" {
		t.Fatalf("conflicting role = %#v, want role_2 same-line user_2", got)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("collision-safe script validation issues = %v", issues)
	}

	result := &ConvertResult{Script: script}
	applyParameterDefinitionCollisionGate(result, script, collisions)
	metadata := script.Actions[0].Metadata
	if metadata["execution_blocker"] != parameterDefinitionCollisionExecutionBlocker {
		t.Fatalf("execution blocker = %#v", metadata["execution_blocker"])
	}
	review, _ := metadata["import_review"].(map[string]any)
	reasonCodes, _ := review["reason_codes"].([]string)
	if len(reasonCodes) != 1 || reasonCodes[0] != parameterDefinitionCollisionExecutionBlocker {
		t.Fatalf("review reason codes = %#v", review["reason_codes"])
	}
	sourceRefs, _ := review["source_refs"].([]string)
	if len(sourceRefs) != 1 || sourceRefs[0] != "second.prm" {
		t.Fatalf("review source refs = %#v, want second.prm", review["source_refs"])
	}
	joinedWarnings := strings.Join(result.Warnings, "\n")
	if !strings.Contains(joinedWarnings, `parameter "user"`) ||
		!strings.Contains(joinedWarnings, "first.prm") ||
		!strings.Contains(joinedWarnings, "second.prm") ||
		!strings.Contains(joinedWarnings, `"user_2"`) {
		t.Fatalf("warnings lack collision provenance: %q", joinedWarnings)
	}
	if len(result.SourceReview.Mappings) != 2 ||
		result.SourceReview.Mappings[0].SourceFile != "second.prm" ||
		result.SourceReview.Mappings[0].TargetPath != "parameters.user_2" ||
		result.SourceReview.Mappings[1].TargetPath != "parameters.role_2" {
		t.Fatalf("source review mappings = %#v, want exact source and affected parameters", result.SourceReview.Mappings)
	}
}

func TestMergeParsedParameterFilesRemapsForwardSameLineDependency(t *testing.T) {
	script := parameterMergeTestScript()
	state := newParameterMergeState(script)
	first := parameterMergeParsedFile("alice,basic\n")
	second := parameterMergeParsedFile("bob,admin\n")
	// Exercise the source order that previously compared role before the later
	// user conflict had allocated its destination name.
	first.Parameters[0], first.Parameters[1] = first.Parameters[1], first.Parameters[0]
	second.Parameters[0], second.Parameters[1] = second.Parameters[1], second.Parameters[0]

	if collisions := mergeParsedParameterFile(script, first, "first.prm", state); len(collisions) != 0 {
		t.Fatalf("first merge collisions = %#v", collisions)
	}
	if collisions := mergeParsedParameterFile(script, second, "second.prm", state); len(collisions) != 2 {
		t.Fatalf("second merge collisions = %#v, want role and user", collisions)
	}
	if got := script.Parameters[2]; got.Name != "role_2" || got.SameLineAs != "user_2" || got.DataSource != "users_2" {
		t.Fatalf("forward-dependent parameter = %#v, want role_2 same-line user_2", got)
	}
	if got := script.Parameters[3]; got.Name != "user_2" || got.DataSource != "users_2" {
		t.Fatalf("forward dependency target = %#v, want user_2", got)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("forward-remapped script validation issues = %v", issues)
	}
}

func TestMergeParsedParameterFilesReusesIdenticalAlternateProfile(t *testing.T) {
	script := parameterMergeTestScript()
	state := newParameterMergeState(script)
	if collisions := mergeParsedParameterFile(script, parameterMergeParsedFile("alice,basic\n"), "first.prm", state); len(collisions) != 0 {
		t.Fatalf("first merge collisions = %#v", collisions)
	}
	if collisions := mergeParsedParameterFile(script, parameterMergeParsedFile("bob,admin\n"), "second.prm", state); len(collisions) != 2 {
		t.Fatalf("second merge collisions = %#v", collisions)
	}
	if collisions := mergeParsedParameterFile(script, parameterMergeParsedFile("bob,admin\n"), "third.prm", state); len(collisions) != 0 {
		t.Fatalf("repeated alternate profile collisions = %#v", collisions)
	}
	if len(script.DataSources) != 2 || len(script.Parameters) != 4 {
		t.Fatalf("sources/parameters = %d/%d, repeated alternate should be reused", len(script.DataSources), len(script.Parameters))
	}
	for _, unexpected := range []string{"users_3", "user_3", "role_3"} {
		if _, reserved := state.usedDataSourceNames[unexpected]; reserved {
			t.Fatalf("reused alternate reserved unused data source %q", unexpected)
		}
		if _, reserved := state.usedParameterNames[unexpected]; reserved {
			t.Fatalf("reused alternate reserved unused parameter %q", unexpected)
		}
	}
}

func TestMergeParsedParameterFilesHandlesHighCardinalityLinearly(t *testing.T) {
	script := parameterMergeTestScript()
	state := newParameterMergeState(script)
	const parameterCount = 4096
	for index := 0; index < parameterCount; index++ {
		parsed := ParsedParameterFile{Parameters: []dsl.Parameter{{
			Name:   fmt.Sprintf("value_%04d", index),
			Type:   "list",
			Values: []string{fmt.Sprintf("row-%04d", index)},
		}}}
		if collisions := mergeParsedParameterFile(script, parsed, fmt.Sprintf("part-%04d.prm", index), state); len(collisions) != 0 {
			t.Fatalf("merge %d collisions = %#v", index, collisions)
		}
	}
	if len(script.Parameters) != parameterCount {
		t.Fatalf("parameters = %d, want %d", len(script.Parameters), parameterCount)
	}
	if len(state.parameterIndexes) != parameterCount ||
		len(state.parameterDefinitions) != parameterCount ||
		len(state.usedParameterNames) != parameterCount {
		t.Fatalf("merge registry sizes = %d/%d/%d, want %d", len(state.parameterIndexes), len(state.parameterDefinitions), len(state.usedParameterNames), parameterCount)
	}
}

func TestMergeParsedParameterFilesPropagatesLargeForwardDependencyChain(t *testing.T) {
	const parameterCount = 4096
	profile := func(lastValue string) ParsedParameterFile {
		parameters := make([]dsl.Parameter, parameterCount)
		for index := range parameters {
			parameters[index] = dsl.Parameter{
				Name:   fmt.Sprintf("value_%04d", index),
				Type:   "list",
				Values: []string{"same"},
			}
			if index+1 < parameterCount {
				parameters[index].SameLineAs = fmt.Sprintf("value_%04d", index+1)
			}
		}
		parameters[parameterCount-1].Values = []string{lastValue}
		return ParsedParameterFile{Parameters: parameters}
	}

	script := parameterMergeTestScript()
	state := newParameterMergeState(script)
	if collisions := mergeParsedParameterFile(script, profile("first"), "first.prm", state); len(collisions) != 0 {
		t.Fatalf("first merge collisions = %d", len(collisions))
	}
	collisions := mergeParsedParameterFile(script, profile("second"), "second.prm", state)
	if len(collisions) != parameterCount {
		t.Fatalf("dependency-chain collisions = %d, want %d", len(collisions), parameterCount)
	}
	firstAlternate := script.Parameters[parameterCount]
	if firstAlternate.Name != "value_0000_2" || firstAlternate.SameLineAs != "value_0001_2" {
		t.Fatalf("first propagated alternate = %#v", firstAlternate)
	}
	lastAlternate := script.Parameters[len(script.Parameters)-1]
	if lastAlternate.Name != "value_4095_2" || len(lastAlternate.Values) != 1 || lastAlternate.Values[0] != "second" {
		t.Fatalf("last propagated alternate = %#v", lastAlternate)
	}
}

func TestParameterCollisionGateAggregatesAcrossLargeActionBatch(t *testing.T) {
	const (
		collisionCount = 2048
		actionCount    = 4096
	)
	script := parameterMergeTestScript()
	script.Actions = make([]dsl.Action, actionCount)
	for index := range script.Actions {
		script.Actions[index] = dsl.Action{Name: fmt.Sprintf("action-%04d", index), Type: "request", Method: "GET", Target: "/"}
	}
	collisions := make([]parameterDefinitionCollision, collisionCount)
	for index := range collisions {
		collisions[index] = parameterDefinitionCollision{
			Name:              fmt.Sprintf("parameter_%04d", index),
			ExistingSource:    "first.prm",
			ConflictingSource: fmt.Sprintf("part_%04d.prm", index),
			AlternateName:     fmt.Sprintf("parameter_%04d_2", index),
		}
	}
	result := &ConvertResult{Script: script}
	applyParameterDefinitionCollisionGate(result, script, collisions)
	if len(result.SourceReview.Mappings) != collisionCount || len(result.Warnings) != collisionCount {
		t.Fatalf("mappings/warnings = %d/%d, want %d/%d", len(result.SourceReview.Mappings), len(result.Warnings), collisionCount, collisionCount)
	}
	for index, action := range script.Actions {
		review, _ := action.Metadata["import_review"].(map[string]any)
		reasons, _ := review["reason_codes"].([]string)
		if len(reasons) != 1 || reasons[0] != parameterDefinitionCollisionExecutionBlocker {
			t.Fatalf("action %d review = %#v, want one aggregate reason", index, review)
		}
	}
}

func parameterMergeTestScript() *dsl.Script {
	return &dsl.Script{
		Name:     "parameter merge",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: "GET",
			Target: "/accounts/{{param.user}}",
		}},
	}
}

func parameterMergeParsedFile(row string) ParsedParameterFile {
	return ParsedParameterFile{
		DataSources: []dsl.DataSource{{
			Name:           "users",
			Type:           "csv",
			InlineData:     "user,role\n" + row,
			Strategy:       "sequential",
			FirstRowHeader: true,
		}},
		Parameters: []dsl.Parameter{
			{Name: "user", Type: "file", DataSource: "users", Column: "user", Selection: "sequential", OnExhaustion: "wrap"},
			{Name: "role", Type: "file", DataSource: "users", Column: "role", Selection: "sequential", OnExhaustion: "wrap", SameLineAs: "user"},
		},
	}
}

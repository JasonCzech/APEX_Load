package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScriptAuthoringIfMatchExactVersionedGrammar(t *testing.T) {
	revision := "scr-v1-" + strings.Repeat("a", 64)
	tag, err := scriptAuthoringETag(revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []http.Header{{"If-Match": {tag}}, {"if-match": {" \t" + tag + " \t"}}} {
		got, present, err := parseScriptAuthoringIfMatch(header)
		if err != nil || !present || got != revision {
			t.Fatalf("valid precondition not admitted: %q %t %v", got, present, err)
		}
	}
	for _, header := range []http.Header{
		{"If-Match": {}}, {"If-Match": {""}}, {"If-Match": {"*"}},
		{"If-Match": {"W/" + tag}}, {"If-Match": {tag + ", " + tag}},
		{"If-Match": {tag, tag}}, {"If-Match": {tag}, "if-match": {tag}},
		{"If-Match": {strings.Trim(tag, `"`)}}, {"If-Match": {tag + `"`}},
		{"If-Match": {strings.Replace(tag, "script-authoring-v1:", "script-authoring-v2:", 1)}},
		{"If-Match": {strings.Replace(tag, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)}},
		{"If-Match": {"\n" + tag}}, {"If-Match": {tag + "\r"}}, {"If-Match": {"\v" + tag}},
	} {
		if got, present, err := parseScriptAuthoringIfMatch(header); err == nil || !present || got != "" {
			t.Fatalf("malformed explicit precondition accepted: %#v got %q %t %v", header, got, present, err)
		}
	}
	if got, present, err := parseScriptAuthoringIfMatch(http.Header{}); err != nil || present || got != "" {
		t.Fatal("absence invented a precondition")
	}
}

func TestScriptAuthoringProjectionExcludesIndependentContextAndPreservesGoalsAndSource(t *testing.T) {
	goal := 217.5
	created := time.Now().UTC()
	stored := &StoredScript{ID: "script", ProjectID: "project", ContentRevision: "scr-v1-" + strings.Repeat("b", 64),
		Script: &dsl.Script{ID: "script", Name: "Flow", Version: "1", Protocol: "http", ProjectID: "project", CreatedAt: created, UpdatedAt: created,
			GoalConfig: &dsl.GoalConfig{TargetAverageTPS: &goal}, DataSources: []dsl.DataSource{{Type: "csv", InlineData: "account,password\nuser,exact-source-value\n"}}, Actions: []dsl.Action{}},
		LibraryAssignment: &storage.LibraryAssignment{Application: &storage.LibraryAssignmentApplication{ID: "app", Name: "joined name"}, Releases: []storage.LibraryAssignmentRelease{{ID: "z", Name: "Z"}, {ID: "a", Name: "A"}}, Revision: 8}}
	first, err := buildScriptAuthoringProjection(stored)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	stored.ScenarioCount = 99
	stored.CreatedAt = created.Add(time.Hour)
	stored.Script.CreatedAt = created.Add(time.Hour)
	stored.Script.UpdatedAt = created.Add(time.Hour)
	stored.Script.ArchivedAt = &created
	stored.LibraryAssignment.Revision = 19
	stored.LibraryAssignment.Application.Name = "new joined name"
	stored.LibraryAssignment.Releases[0].Name = "renamed"
	second, err := buildScriptAuthoringProjection(stored)
	if err != nil {
		t.Fatal(err)
	}
	final, err := json.Marshal(second)
	if err != nil || string(initial) != string(final) {
		t.Fatalf("independent context changed strong representation: %s vs %s (%v)", initial, final, err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(initial, &fields) != nil {
		t.Fatal("projection invalid JSON")
	}
	scriptJSON := fields["script"]
	fields = nil
	if json.Unmarshal(scriptJSON, &fields) != nil {
		t.Fatal("projection invalid JSON")
	}
	for _, key := range []string{"created_at", "updated_at", "archived_at", "content_revision", "scenario_count", "activity", "authoring_health"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("non-authored field %s entered canonical script", key)
		}
	}
	if second.Script.GoalConfig == nil || *second.Script.GoalConfig.TargetAverageTPS != goal || second.Script.DataSources[0].InlineData != stored.Script.DataSources[0].InlineData {
		t.Fatal("authored goals/source fidelity changed")
	}
	if got := strings.Join(second.LibraryPlacement.ReleaseIDs, ","); got != "a,z" {
		t.Fatalf("placement is not stable: %s", got)
	}
	clone := cloneScriptAuthoringProjection(second)
	clone.Script.GoalConfig.TargetAverageTPS = new(float64)
	clone.LibraryPlacement.ReleaseIDs[0] = "mutated"
	if *second.Script.GoalConfig.TargetAverageTPS != goal || second.LibraryPlacement.ReleaseIDs[0] != "a" {
		t.Fatal("projection clone retained aliases")
	}
}

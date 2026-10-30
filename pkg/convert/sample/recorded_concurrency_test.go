package sample

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func concurrencyExchange(startMs, durationMs int) exchange {
	return exchange{StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(startMs) * time.Millisecond), HasStartedAt: true, DurationMs: float64(durationMs)}
}

func concurrencyRequest(name, target string) dsl.Action {
	return dsl.Action{Name: name, Type: "request", Method: "GET", Target: target}
}

// Requests the browser had in flight together replay as one concurrent group;
// a request that starts after an earlier member finished, one that uses a
// member's extracted value, and steps that are not requests end the group.
// Source mappings follow each request into its group, and the requests in a
// group leave the replay baseline.
func TestRecordedConcurrencyGroupsRequestsInFlightTogether(t *testing.T) {
	producer := concurrencyRequest("b", "/b")
	producer.Extractors = []dsl.Extractor{{Name: "corr_b", Source: "body", Type: "regex", Expression: "x=(\\d+)"}}
	consumer := concurrencyRequest("c", "/c?v={{var.corr_b}}")
	slow := concurrencyRequest("a", "/a")
	slow.ThinkTimeAfter = false
	script := &dsl.Script{Actions: []dsl.Action{
		slow,                          // 0: 0-1000
		producer,                      // 1: 10-50
		concurrencyRequest("d", "/d"), // 2: 20-900, in flight with a and b
		consumer,                      // 3: 30-60, uses b's value: ends the group
		concurrencyRequest("e", "/e"), // 4: 2000-2100, alone
		concurrencyRequest("f", "/f"), // 5: 2050-2300, overlaps e
	}}
	script.Actions[5].ThinkTimeAfter = true
	script.Actions[5].ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1500}
	exchanges := []exchange{
		concurrencyExchange(0, 1000), concurrencyExchange(10, 40), concurrencyExchange(20, 880),
		concurrencyExchange(30, 30), concurrencyExchange(2000, 100), concurrencyExchange(2050, 250),
	}
	mappings := []sourceview.Mapping{{ID: "m2", TargetPath: "actions[2].headers"}, {ID: "m3", TargetPath: "actions[3]"}, {ID: "m5", TargetPath: "actions[5]"}}

	remapped, positions, groups, _ := groupRecordedConcurrency(script, exchanges, nil, mappings, 500*time.Millisecond)
	if groups != 2 || len(script.Actions) != 3 {
		t.Fatalf("groups = %d, actions = %#v, want [a b d] [c] [e f]", groups, script.Actions)
	}
	first := script.Actions[0]
	children, _ := first.Params["requests"].([]dsl.Action)
	if first.Type != "http_sequence" || first.Params["execution_mode"] != dsl.HTTPSequenceConcurrent || len(children) != 3 || children[2].Name != "d" {
		t.Fatalf("first group = %#v", first)
	}
	if script.Actions[1].Name != "c" {
		t.Fatalf("second action = %q, want the dependent request alone", script.Actions[1].Name)
	}
	last := script.Actions[2]
	if last.Type != "http_sequence" || !last.ThinkTimeAfter || last.ThinkTime.MeanMs != 1500 {
		t.Fatalf("last group = %#v, want the last member's think time on the group", last)
	}
	if lastChildren, _ := last.Params["requests"].([]dsl.Action); lastChildren[1].ThinkTimeAfter || lastChildren[1].ThinkTime.MeanMs != 0 {
		t.Fatalf("group child kept its think time: %#v", lastChildren[1])
	}
	want := map[string]string{"m2": "actions[0].params.requests[2].headers", "m3": "actions[1]", "m5": "actions[2].params.requests[1]"}
	for _, mapping := range remapped {
		if mapping.TargetPath != want[mapping.ID] {
			t.Errorf("mapping %s = %q, want %q", mapping.ID, mapping.TargetPath, want[mapping.ID])
		}
	}
	if positions[3] != 1 || positions[0] != -1 || positions[5] != -1 {
		t.Fatalf("positions = %v, want grouped requests out of the baseline and c at 1", positions)
	}
	script.Name, script.Protocol, script.Config.BaseURL = "grouped", "http", "https://app.test"
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("grouped script does not validate: %q", issues)
	}
}

func TestRecordedConcurrencyLeavesSequentialRecordingsAlone(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{concurrencyRequest("a", "/a"), concurrencyRequest("b", "/b")}}
	exchanges := []exchange{concurrencyExchange(0, 100), concurrencyExchange(100, 100)}
	if _, _, groups, _ := groupRecordedConcurrency(script, exchanges, nil, nil, 500*time.Millisecond); groups != 0 || len(script.Actions) != 2 {
		t.Fatalf("groups = %d, actions = %d, want nothing grouped", groups, len(script.Actions))
	}
}

// The pause after a group is measured from the group's last request to
// finish, not from the last one to start; the group is transparent, so it
// records no transaction of its own.
func TestRecordedConcurrencyGroupThinkTimeStartsWhenTheGroupEnds(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{concurrencyRequest("a", "/a"), concurrencyRequest("b", "/b"), concurrencyRequest("next", "/next")}}
	script.Actions[1].ThinkTimeAfter = true
	script.Actions[1].ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 5940}
	exchanges := []exchange{concurrencyExchange(0, 3000), concurrencyExchange(10, 50), concurrencyExchange(6000, 100)}
	if _, _, groups, _ := groupRecordedConcurrency(script, exchanges, nil, nil, 500*time.Millisecond); groups != 1 {
		t.Fatalf("groups = %d, want one", groups)
	}
	group := script.Actions[0]
	if !group.ThinkTimeAfter || group.ThinkTime.MeanMs != 3000 {
		t.Fatalf("group think time = %+v (after %v), want the 3000 ms from the group's end to the next step", group.ThinkTime, group.ThinkTimeAfter)
	}
	if transparent, _ := group.Metadata[dsl.HTTPSequenceTransparentMetadataKey].(bool); !transparent || dsl.HTTPSequenceRecordsTransaction(group) {
		t.Fatalf("group metadata = %v, want a transparent group", group.Metadata)
	}
}

// A run the dependency pre-check passes but the group validator rejects (a
// later member extracts a name an earlier one reads) is cut back until it
// validates, instead of leaving the whole run sequential.
func TestRecordedConcurrencyShrinksAGroupThatDoesNotValidate(t *testing.T) {
	reader := concurrencyRequest("reader", "/reader?v={{var.late_value}}")
	late := concurrencyRequest("late", "/late")
	late.Extractors = []dsl.Extractor{{Name: "late_value", Source: "body", Type: "regex", Expression: "v=(\\d+)"}}
	script := &dsl.Script{Actions: []dsl.Action{concurrencyRequest("a", "/a"), reader, late}}
	exchanges := []exchange{concurrencyExchange(0, 1000), concurrencyExchange(10, 900), concurrencyExchange(20, 800)}
	_, _, groups, refused := groupRecordedConcurrency(script, exchanges, nil, nil, 500*time.Millisecond)
	if groups != 1 || refused != 0 || len(script.Actions) != 2 {
		t.Fatalf("groups = %d, refused = %d, actions = %d, want [a reader] then late", groups, refused, len(script.Actions))
	}
	children, _ := script.Actions[0].Params["requests"].([]dsl.Action)
	if len(children) != 2 || script.Actions[1].Name != "late" {
		t.Fatalf("actions = %#v", script.Actions)
	}
}

// With transaction markers after a group, the marker that carries the pause
// gets the one measured from the group's end, and the group none.
func TestRecordedConcurrencyMovesTheGroupPauseOntoTheEndMarker(t *testing.T) {
	end := dsl.TransactionMarker("Step", false)
	end.ThinkTimeAfter = true
	end.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 5940}
	script := &dsl.Script{Actions: []dsl.Action{concurrencyRequest("a", "/a"), concurrencyRequest("b", "/b"), end, concurrencyRequest("next", "/next")}}
	exchanges := []exchange{concurrencyExchange(0, 3000), concurrencyExchange(10, 50), concurrencyExchange(6000, 100)}
	newIndex := []int{0, 1, 3}
	if _, _, groups, _ := groupRecordedConcurrency(script, exchanges, newIndex, nil, 500*time.Millisecond); groups != 1 {
		t.Fatalf("groups = %d, want one", groups)
	}
	if group := script.Actions[0]; group.ThinkTimeAfter {
		t.Fatalf("group think time = %+v, want it on the marker", group.ThinkTime)
	}
	if marker := script.Actions[1]; !marker.ThinkTimeAfter || marker.ThinkTime.MeanMs != 3000 {
		t.Fatalf("marker think time = %+v (after %v), want 3000 ms from the group's end", marker.ThinkTime, marker.ThinkTimeAfter)
	}
}

// A group must leave the runtime half its preparation budget for resolving
// its requests: two 3 MiB bodies stay sequential (counted once), small ones
// group.
func TestRecordedConcurrencyLeavesRoomForRuntimePreparation(t *testing.T) {
	post := func(name string, size int) dsl.Action {
		action := concurrencyRequest(name, "/upload/"+name)
		action.Type, action.Method, action.Body = "POST", "POST", strings.Repeat("x", size)
		action.Type = "request"
		return action
	}
	exchanges := []exchange{concurrencyExchange(0, 1000), concurrencyExchange(10, 900), concurrencyExchange(20, 800)}
	large := &dsl.Script{Actions: []dsl.Action{post("a", 3<<20), post("b", 3<<20), post("c", 3<<20)}}
	if _, _, groups, refused := groupRecordedConcurrency(large, exchanges, nil, nil, 500*time.Millisecond); groups != 0 || refused != 1 {
		t.Fatalf("large: groups = %d, refused = %d, want none grouped and one run counted", groups, refused)
	}
	small := &dsl.Script{Actions: []dsl.Action{post("a", 1<<10), post("b", 1<<10), post("c", 1<<10)}}
	if _, _, groups, _ := groupRecordedConcurrency(small, exchanges, nil, nil, 500*time.Millisecond); groups != 1 {
		t.Fatalf("small: groups = %d, want one", groups)
	}
}

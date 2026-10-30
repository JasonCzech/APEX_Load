package sample

import (
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

	remapped, positions, groups := groupRecordedConcurrency(script, exchanges, nil, mappings)
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
	if _, _, groups := groupRecordedConcurrency(script, exchanges, nil, nil); groups != 0 || len(script.Actions) != 2 {
		t.Fatalf("groups = %d, actions = %d, want nothing grouped", groups, len(script.Actions))
	}
}

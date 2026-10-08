package eventhubscapture

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/eventhubs"
)

func TestFixtureCaptureReplayPreservesPayloadAndProperties(t *testing.T) {
	for _, fixture := range []struct{ name, body string }{{"json_unicode", `{"device":"sensor-7","label":"café","value":21.5}`}, {"binary", "\xff\xfe\x00\x01capture-payload"}} {
		t.Run(fixture.name, func(t *testing.T) {
			data := writeCapture(t, []map[string]any{captureRecord(1, "device-7", "fixture-origin", fixture.body)})
			result, err := Convert(ConvertRequest{Name: fixture.name, Data: data, EventHub: "fixture-" + fixture.name})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Script.Actions) != 1 || !result.Script.Config.EventHubs.Mock {
				t.Fatal("capture should produce one mock producer draft")
			}
			// Persist/reload the draft before execution to catch binary JSON loss.
			encoded, err := json.Marshal(result.Script)
			if err != nil {
				t.Fatal(err)
			}
			var script dsl.Script
			if err := json.Unmarshal(encoded, &script); err != nil {
				t.Fatal(err)
			}
			script.Config.EventHubs.StartPosition = "earliest"
			script.Actions = append(script.Actions, dsl.Action{Name: "Observe exact capture", Type: "eventhubs_consume", Assertions: []dsl.Assertion{{Type: "message_received"}, {Type: "body_contains", Value: fixture.body}}, Extractors: []dsl.Extractor{{Name: "captured_source", Type: "property", Source: "property", Expression: "source"}}})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			report, err := engine.ReplayScript(ctx, &script, engine.ReplayOptions{IgnoreThinkTime: true})
			if err != nil {
				t.Fatal(err)
			}
			if !report.Success || len(report.Events) != 2 {
				t.Fatalf("capture replay failed: %s", report.Error)
			}
			if report.Events[1].Extractions["captured_source"] != "fixture-origin" {
				t.Fatal("application property did not survive capture import and replay")
			}
		})
	}
}

func TestFixtureAvroRecordReplaySendsTheRecordBodyExactly(t *testing.T) {
	const comments = "<script>alert('hi')</script> & \ufeff\u202etest\u202e 👩🏽 \u2029"
	data := writeOCF(t, userDataSchema, map[string]any{"registration_dttm": "2016-02-03T18:29:04Z", "id": int64(3), "email": "m@example.jp", "cc": int64(4017953398128713005), "salary": nil, "comments": comments})
	result, err := Convert(ConvertRequest{Name: "userdata", Data: data, EventHub: "fixture-records"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	var script dsl.Script
	if err := json.Unmarshal(encoded, &script); err != nil {
		t.Fatal(err)
	}
	if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
		t.Fatalf("imported record script does not validate: %v", issues)
	}
	want := script.Actions[0].Body
	script.Config.EventHubs.StartPosition = "earliest"
	script.Actions = append(script.Actions, dsl.Action{Name: "Observe exact record", Type: "eventhubs_consume", Assertions: []dsl.Assertion{{Type: "message_received"}}, Extractors: []dsl.Extractor{{Name: "observed", Type: "regex", Source: "body", Expression: `(?s)^(.*)$`}}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := engine.ReplayScript(ctx, &script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || len(report.Events) != 2 {
		t.Fatalf("record replay failed: %s", report.Error)
	}
	if got := report.Events[1].Extractions["observed"]; got != want {
		t.Fatalf("replayed body:\n got %q\nwant %q", got, want)
	}
}

package enterprisearchive

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// pollingProtocol answers the polling request with an empty check-in id (the
// on_miss "empty" value of a NotFound=warning registration) until foundOn.
type pollingProtocol struct {
	names   []string
	polls   int
	foundOn int
}

func (p *pollingProtocol) Name() string                               { return "http" }
func (p *pollingProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *pollingProtocol) Teardown(context.Context) error             { return nil }
func (p *pollingProtocol) HealthCheck(context.Context) error          { return nil }
func (p *pollingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.names = append(p.names, action.Name)
	result := &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond, Extractions: map[string]string{}}
	if action.Name == "poll" {
		p.polls++
		value := ""
		if p.polls >= p.foundOn {
			value = "a3Cir000000KundEAC"
		}
		result.Extractions["c_checkinid02"] = value
	}
	return result, nil
}

// The imported polling loop must poll until the check-in id arrives, then
// leave the loop, exactly as the VuGen script does.
func TestImportedPollingLoopRunsUntilTheFlagIsSet(t *testing.T) {
	source := "Action()\n{\n" +
		"\tint assertionPassed= 0;\n" +
		"\twhile (!assertionPassed){\n" +
		"\t\tweb_reg_save_param(\"c_checkinId02\",\"LB=\\\"checkinId\\\":\\\"\",\"RB=\\\"\",\"NotFound=warning\",LAST);\n" +
		"\t\tweb_url(\"poll\",\"URL=https://example.com/poll\",LAST);\n" +
		"\t\tif (strlen(lr_eval_string(\"{c_checkinId02}\")) != 0) {\n" +
		"\t\t\tassertionPassed = 1;\n" +
		"\t\t}\n" +
		"\t\telse{\n" +
		"\t\t\tweb_url(\"retry_marker\",\"URL=https://example.com/retry\",LAST);\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\tweb_url(\"after\",\"URL=https://example.com/after\",LAST);\n" +
		"\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "polling"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Script
	script.Config.RunLogic.Iterations = 1
	proto := &pollingProtocol{foundOn: 3}
	vu := &engine.VUser{ID: 1, Group: "g", Proto: proto, Variables: map[string]string{}, Metrics: engine.NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	want := []string{"poll", "retry_marker", "poll", "retry_marker", "poll", "after"}
	if len(proto.names) != len(want) {
		t.Fatalf("executed %v, want %v", proto.names, want)
	}
	for index := range want {
		if proto.names[index] != want[index] {
			t.Fatalf("executed %v, want %v", proto.names, want)
		}
	}
}

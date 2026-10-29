package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/database"
)

type replayInitPanicProtocol struct {
	replayTestProtocol
	value string
}

func (p *replayInitPanicProtocol) Init(context.Context, map[string]any) error { panic(p.value) }

func TestReplayStartupErrorsRespectAuthorizedDebugMode(t *testing.T) {
	const secret = "private-startup-value"
	for _, panicInit := range []bool{false, true} {
		name := fmt.Sprintf("replay-debug-init-%t", panicInit)
		protocol.Register(name, func() protocol.Protocol {
			if panicInit {
				return &replayInitPanicProtocol{value: secret}
			}
			return &replayTestProtocol{initErr: fmt.Errorf("init failure %s", secret)}
		})
		for _, tc := range []struct{ debug, controller bool }{{true, true}, {true, false}, {false, true}} {
			t.Run(fmt.Sprintf("panic=%t/debug=%t/controller=%t", panicInit, tc.debug, tc.controller), func(t *testing.T) {
				opts := ReplayOptions{UnredactedDebug: tc.debug, RedactionRules: []RedactionRule{{Pattern: secret}}}
				if tc.controller {
					opts.DebugController = replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil })
				}
				_, err := ReplayScript(context.Background(), &dsl.Script{Name: "init", Protocol: name}, opts)
				if err == nil || strings.Contains(err.Error(), secret) != (tc.debug && tc.controller) {
					t.Fatalf("unexpected init error: %v", err)
				}
				if panicInit && !strings.Contains(err.Error(), "panicked") {
					t.Fatalf("panic not contained: %v", err)
				}
			})
		}
	}
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) { return &protocol.Result{Success: true}, nil })
	for _, debug := range []bool{false, true} {
		script := &dsl.Script{Name: "data", Protocol: name, DataSources: []dsl.DataSource{{Name: secret, Type: "unrecognized"}}}
		_, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: debug, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: secret}}})
		if err == nil || strings.Contains(err.Error(), secret) != debug {
			t.Fatalf("debug=%t data-source error=%v", debug, err)
		}
	}
}

func TestReplayNativeEvidenceRestoresRawActionMetadata(t *testing.T) {
	const secret = "private-metadata-value"
	for _, debug := range []bool{false, true} {
		script := &dsl.Script{Name: "network", Protocol: "network", Actions: []dsl.Action{{Name: "bad", Type: "network_invalid", Target: "{{var.session_token}}", Headers: map[string]string{"X-Token": "{{var.session_token}}"}, Params: map[string]any{"token": "{{var.session_token}}"}}}}
		var progress *ReplayActionEvent
		report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: debug, Variables: map[string]string{"session_token": secret}, RedactionRules: []RedactionRule{{Pattern: secret}}, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
			if p.Type == ReplayProgressEventRun {
				progress = p.Event
			}
		}})
		if err != nil || len(report.Events) != 1 || progress == nil {
			t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
		}
		for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
			metadata, _ := event.ProtocolEvidence["metadata"].(map[string]any)
			data, _ := json.Marshal(metadata)
			if strings.Contains(string(data), secret) != debug || strings.Contains(fmt.Sprint(event.ProtocolEvidence["target"]), secret) != debug || event.ProtocolEvidence["protocol"] != "network" {
				t.Fatalf("debug=%t evidence=%#v", debug, event.ProtocolEvidence)
			}
		}
	}
}

func TestReplayDatabaseProducerReportsCompaction(t *testing.T) {
	columns1 := []string{"'" + strings.Repeat("x", 5000) + "' AS c0"}
	columns2 := []string{"'two' AS c0"}
	for i := 1; i < 17; i++ {
		columns1 = append(columns1, fmt.Sprintf("%d AS c%d", i, i))
		columns2 = append(columns2, fmt.Sprintf("%d AS c%d", i, i))
	}
	query := "SELECT " + strings.Join(columns1, ",") + " UNION ALL SELECT " + strings.Join(columns2, ",")
	script := &dsl.Script{Name: "database", Protocol: "database", Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}}, Actions: []dsl.Action{{Name: "query", Type: "db_query", Target: query}}}
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil || !report.Events[0].Success {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(event)
		var decoded ReplayActionEvent
		if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.ProtocolEvidenceOmittedCount < 4 {
			t.Fatalf("database omission receipt missing: count=%d err=%v", decoded.ProtocolEvidenceOmittedCount, err)
		}
	}
}

func TestDebugInitErrorPreservesCancellationClass(t *testing.T) {
	p := &replayTestProtocol{initErr: fmt.Errorf("private failure: %w", context.Canceled)}
	err := callProtocolInit(context.Background(), p, nil, nil, true)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "private failure") {
		t.Fatalf("debug init cancellation lost: %v", err)
	}
}

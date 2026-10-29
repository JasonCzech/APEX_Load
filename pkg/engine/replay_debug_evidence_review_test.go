package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner/ipc"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/network"
)

type testJavaRunnerEvent struct {
	Type       string `json:"type"`
	Name       string `json:"name,omitempty"`
	Message    string `json:"message,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
}

func TestReplayDebugEvidenceRetainsTypedJavaValues(t *testing.T) {
	success, duration := true, int64(42)
	summary := ipc.Summary{Contract: ipc.ContractV2, Kind: "action", Success: &success, ClassName: "Actions"}
	events := []testJavaRunnerEvent{{Type: "transaction_start", Name: "login"}, {Type: "transaction_end", Name: "login", DurationMS: &duration}}
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, ProtocolEvidence: map[string]any{"runner_result": summary, "runner_events": events}}, nil
	})
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "java evidence", Protocol: name, Actions: []dsl.Action{{Name: "run", Type: "GET"}}}, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(event)
		var decoded ReplayActionEvent
		if err != nil || json.Unmarshal(data, &decoded) != nil {
			t.Fatalf("serialized event=%s err=%v", data, err)
		}
		gotSummary, _ := decoded.ProtocolEvidence["runner_result"].(map[string]any)
		gotEvents, _ := decoded.ProtocolEvidence["runner_events"].([]any)
		if gotSummary["class_name"] != "Actions" || gotSummary["success"] != true || len(gotEvents) != 2 || gotEvents[1].(map[string]any)["duration_ms"] != float64(42) || event.ProtocolEvidenceOmittedCount != 0 {
			t.Fatalf("typed evidence lost: %s", data)
		}
	}
}

func TestReplayNetworkProducerOmissionsReachProgressAndReport(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 ready\r\n")
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverDone <- err
				return
			}
			line = strings.TrimSpace(line)
			if inData {
				if line == "." {
					inData = false
					fmt.Fprint(conn, "250 queued\r\n")
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "DATA"):
				inData = true
				fmt.Fprint(conn, "354 send\r\n")
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(conn, "221 bye\r\n")
				serverDone <- nil
				return
			default:
				fmt.Fprint(conn, "250 ok\r\n")
			}
		}
	}()
	to := make([]string, 150)
	for i := range to {
		to[i] = fmt.Sprintf("recipient%d@example.test", i)
	}
	script := &dsl.Script{Name: "smtp evidence", Protocol: "network", Actions: []dsl.Action{{Name: "smtp", Type: "smtp_send", Target: listener.Addr().String(), Params: map[string]any{"from": "sender@example.test", "to": to}}}}
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil || !report.Events[0].Success {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(event)
		var decoded ReplayActionEvent
		if err != nil || json.Unmarshal(data, &decoded) != nil {
			t.Fatalf("serialized event=%s err=%v", data, err)
		}
		transcript, _ := decoded.ProtocolEvidence["transcript"].([]any)
		if len(transcript) != 128 || decoded.ProtocolEvidenceOmittedCount < 28 {
			t.Fatalf("producer omission receipt missing: transcript=%d omitted=%d", len(transcript), decoded.ProtocolEvidenceOmittedCount)
		}
	}
}

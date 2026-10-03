package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

var registerAuraReplayHTTPProtocol, registerAuraLargeHTTPProtocol sync.Once

func TestReplayAuraValidatesActualOutgoingRequestAndPreservesHTTPStatus(t *testing.T) {
	protocolName := "aura-replay-local-http"
	registerAuraReplayHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	for _, tc := range []struct {
		name    string
		mode    string
		success bool
	}{
		{"reordered fresh IDs", "reordered", true},
		{"business error under HTTP200", "error", false},
		{"missing result", "missing", false},
		{"duplicate result", "duplicate", false},
		{"unknown state", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeID := "fresh-" + tc.mode
			incoming := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				var request struct {
					Actions []struct {
						ID string `json:"id"`
					} `json:"actions"`
				}
				if err := json.Unmarshal([]byte(r.Form.Get("message")), &request); err != nil || len(request.Actions) != 2 {
					t.Errorf("invalid fixture request: %v", err)
					w.WriteHeader(400)
					return
				}
				id := request.Actions[0].ID
				incoming <- id
				w.Header().Set("Content-Type", "application/json")
				switch tc.mode {
				case "error":
					fmt.Fprintf(w, `{"actions":[{"id":"static","state":"SUCCESS"},{"id":%q,"state":"ERROR","error":[{"message":"private-business-error"}]}]}`, id)
				case "missing":
					fmt.Fprint(w, `{"actions":[{"id":"static","state":"SUCCESS"}]}`)
				case "duplicate":
					fmt.Fprintf(w, `{"actions":[{"id":%q,"state":"SUCCESS"},{"id":%q,"state":"SUCCESS"}]}`, id, id)
				case "unknown":
					fmt.Fprintf(w, `{"actions":[{"id":"static","state":"SUCCESS"},{"id":%q,"state":"private-unknown-state"}]}`, id)
				default:
					fmt.Fprintf(w, `{"actions":[{"id":"static","state":"SUCCESS"},{"id":%q,"state":"SUCCESS"}]}`, id)
				}
			}))
			defer server.Close()
			script := &dsl.Script{Name: "Aura fixture", Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{{Name: "Aura batch", Type: "request", Method: "POST", Target: "/aura", Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, Body: `message=%7B%22actions%22%3A%5B%7B%22id%22%3A%22{{var.aura_id}}%22%2C%22descriptor%22%3A%22fixture%22%7D%2C%7B%22id%22%3A%22static%22%2C%22descriptor%22%3A%22fixture%22%7D%5D%7D`, Assertions: []dsl.Assertion{{Type: "aura_actions_success"}}}}}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{Variables: map[string]string{"aura_id": runtimeID}, IgnoreThinkTime: true})
			if err != nil {
				t.Fatal(err)
			}
			if report.Success != tc.success || len(report.Events) != 1 {
				t.Fatalf("unexpected replay success/events: %v %d %s", report.Success, len(report.Events), report.Error)
			}
			select {
			case got := <-incoming:
				if got != runtimeID {
					t.Fatalf("sent captured/unresolved ID %q rather than %q", got, runtimeID)
				}
			default:
				t.Fatal("replay did not reach the local Aura fixture")
			}
			event := report.Events[0]
			if event.StatusCode != 200 || event.Success != tc.success {
				t.Fatalf("HTTP status/outcome lost: %+v", event)
			}
			evidence, err := json.Marshal(event.ProtocolEvidence["aura"])
			if err != nil || string(evidence) == "null" {
				t.Fatalf("missing structural replay evidence: %s %v", evidence, err)
			}
			var observed aura.Report
			if err := json.Unmarshal(evidence, &observed); err != nil || observed.RequestActions != 2 || !observed.HasResponse || observed.Success != tc.success {
				t.Fatalf("structural evidence fields lost through replay projection: %s %v", evidence, err)
			}
			if tc.success && (observed.MatchedActions != 2 || !observed.Complete || len(observed.Outcomes) != 2) {
				t.Fatalf("successful matched outcome evidence lost: %s", evidence)
			}
			if tc.mode == "error" && (len(observed.Outcomes) != 2 || observed.Outcomes[0].State != aura.Error) {
				t.Fatalf("failed business outcome evidence lost: %s", evidence)
			}
			for _, private := range []string{runtimeID, "private-business-error", "private-unknown-state"} {
				if strings.Contains(string(evidence), private) || strings.Contains(event.ErrorMessage, private) {
					t.Fatalf("Aura diagnostic leaked source value %q", private)
				}
			}
		})
	}
}

func TestReplayAuraLargeBatchDisclosesRetainedEvidenceLimits(t *testing.T) {
	protocolName := "aura-large-local-http"
	registerAuraLargeHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	for _, allErrors := range []bool{false, true} {
		t.Run(fmt.Sprintf("all-errors-%v", allErrors), func(t *testing.T) {
			requests := make([]map[string]any, aura.MaxActions)
			responses := make([]map[string]any, aura.MaxActions)
			for i := range requests {
				id := fmt.Sprintf("private-fixture-%d", i)
				requests[i] = map[string]any{"id": id, "descriptor": "fixture"}
				state := "SUCCESS"
				if allErrors || i == aura.MaxActions-1 {
					state = "ERROR"
				}
				responses[i] = map[string]any{"id": id, "state": state}
			}
			outgoing, _ := json.Marshal(map[string]any{"actions": requests})
			returning, _ := json.Marshal(map[string]any{"actions": responses})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(returning)
			}))
			defer server.Close()
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "Large Aura fixture", Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{{Name: "Aura batch", Type: "request", Method: "POST", Target: "/aura", Body: string(outgoing), Assertions: []dsl.Assertion{{Type: "aura_actions_success"}}}}}, ReplayOptions{IgnoreThinkTime: true})
			if err != nil || report.Success || len(report.Events) != 1 {
				t.Fatalf("unexpected replay: %v %+v", err, report)
			}
			evidence, err := json.Marshal(report.Events[0].ProtocolEvidence["aura"])
			var observed aura.Report
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(evidence, &observed); err != nil {
				t.Fatalf("evidence lost: %s %v", evidence, err)
			}
			if !observed.Complete || observed.MatchedActions != aura.MaxActions || observed.Success || len(observed.Outcomes) != 64 || observed.OutcomesOmitted != aura.MaxActions-64 {
				t.Fatalf("silent outcome truncation: %s", evidence)
			}
			if allErrors && (len(observed.Issues) != 64 || observed.IssuesOmitted != aura.MaxActions-64) {
				t.Fatalf("silent issue truncation: %s", evidence)
			}
			foundError := false
			for _, outcome := range observed.Outcomes {
				if outcome.State == aura.Error && (allErrors || outcome.Ordinal == aura.MaxActions) {
					foundError = true
				}
			}
			if !foundError || strings.Contains(string(evidence), "private-fixture") {
				t.Fatalf("late failure evidence missing or unsafe: %s", evidence)
			}
		})
	}
}

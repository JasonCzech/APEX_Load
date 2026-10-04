package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

var registerAuraContractRepairReviewHTTP sync.Once

type auraContractRepairReviewObserver struct {
	session ScriptDebugSession
}

func (*auraContractRepairReviewObserver) BeforeAction(context.Context, engine.ReplayDebugAction) error {
	return nil
}
func (o *auraContractRepairReviewObserver) ObserveCorrelationEvidence(_ context.Context, action engine.ReplayDebugAction, event *engine.ReplayActionEvent) {
	o.session.appendCorrelationObservationLocked(action, event, time.Now())
}

// A valid contract can remain after the operator reverts a newly inserted
// business parameter to a literal. Wire equality then proves byte offsets but
// cannot prove that raw insertion preserves contextual occurrence identities.
func TestAuraContractCorrelationRepairNeverClaimsRawBodyProof(t *testing.T) {
	const recordedValue = "SYNTHETIC-RECORDED-123456"
	const replayValue = "SYNTHETIC-REPLAYED-654321"
	const protocolName = "api-aura-contract-repair-review-http"
	registerAuraContractRepairReviewHTTP.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"a","descriptor":"fixture","params":{"note":"`+recordedValue+` {{var.first}}","recordId":"business"}}]}`)
	contract, _, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, "", "", nil, aurapayload.MaxWorkBytes)
	if err != nil || contract == nil || len(contract.LiteralOccurrences) != 1 {
		t.Fatal("fixture did not retain its original encoded literal")
	}
	for _, test := range []struct {
		name        string
		metadata    map[string]any
		transformed bool
	}{
		{"plain exact form remains repairable", nil, false},
		{"structural exact form remains manual", map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding}, true},
		{"contract exact form remains manual", map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding, dsl.ImportAuraTemplateContractMetadataKey: contract}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"csrf_token":"`+replayValue+`"}`)
					return
				}
				wire, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Error(readErr)
				}
				received <- string(wire)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"actions":[{"id":"a","state":"SUCCESS"}]}`)
			}))
			defer server.Close()
			observer := &auraContractRepairReviewObserver{}
			script := &dsl.Script{Name: "Contextual repair proof", Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{{Type: "GET", Target: "/bootstrap"}, {Type: "POST", Target: "/aura", Body: body, Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, Metadata: test.metadata}}}
			report, replayErr := engine.ReplayScript(context.Background(), script, engine.ReplayOptions{IgnoreThinkTime: true, DebugController: observer, Variables: map[string]string{"first": "{{var.first}}"}})
			if replayErr != nil || report == nil || !report.Success {
				t.Fatalf("replay failed: %v", replayErr)
			}
			if wire := <-received; wire != body {
				t.Fatal("fixture must replay exact authored bytes")
			}
			if len(observer.session.correlationObservations) != 2 || observer.session.correlationEvidenceErr != "" {
				t.Fatal("trusted observation not retained")
			}
			observed := observer.session.correlationObservations[1]
			if observed.RequestBodyTransformed != test.transformed || observed.EditableRequestBody == test.transformed {
				t.Fatal("contextual body was given an unsafe raw-offset proof")
			}
			// Verify the actual trusted observation yields manual advice rather than
			// changing occurrence identities through an apparently applicable repair.
			recorded := []correlate.Observation{{ID: 0, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`}, {ID: 1, RequestBody: body}}
			replayed := observer.session.correlationObservations
			diagnosis, diagnosisErr := correlate.DiagnoseReplayReviewChecked(recorded, replayed)
			if diagnosisErr != nil || len(diagnosis.Suggestions) != 1 {
				t.Fatalf("repair diagnosis unavailable: %v", diagnosisErr)
			}
			advice := diagnosis.Suggestions[0]
			if test.transformed {
				if advice.ReviewAction != "manual_value_replacement_required" || len(advice.RepairLocations) != 0 {
					t.Fatal("contextual body advertised unsafe automatic repair")
				}
			} else if len(advice.RepairLocations) != 1 || !advice.RepairLocations[0].EditableSurfaceExact {
				t.Fatal("ordinary exact body lost its supported repair")
			}
		})
	}
}

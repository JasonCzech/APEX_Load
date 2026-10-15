package browser

import (
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	playwright "github.com/mxschmitt/playwright-go"
)

// A Salesforce Lightning page talks to its server in Aura requests (/aura),
// each a batch of named actions. A step records the actions it triggered:
// the action's descriptor, or for an Apex call the Apex class and method, its
// batch's timing, size and bytes, and the state the response gave it. Only
// names are kept, never what the actions sent or got back.

const (
	// maxAuraBatchActions bounds the actions named per Aura request.
	maxAuraBatchActions = 64
	// maxStepAuraActions bounds the actions a step's evidence lists.
	maxStepAuraActions = 64
)

// auraRequest reports a POST to an Aura endpoint (/aura, or a site's
// /s/sfsites/aura).
func auraRequest(request playwright.Request) bool {
	if !strings.EqualFold(request.Method(), "POST") {
		return false
	}
	parsed, err := url.Parse(request.URL())
	return err == nil && strings.HasSuffix(parsed.Path, "/aura")
}

// summarizeAuraRequest names an Aura request's actions and, when its response
// can be read, the state each ended in.
func summarizeAuraRequest(request playwright.Request, response playwright.Response, sizes *playwright.RequestSizesResult) *protocol.AuraBatch {
	body, err := request.PostDataBuffer()
	if err != nil || len(body) == 0 || len(body) > aura.MaxEnvelopeBytes {
		return nil
	}
	actions, err := aura.RequestActions(body)
	if err != nil || len(actions) == 0 {
		return nil
	}
	batch := &protocol.AuraBatch{}
	states := map[int]aura.State{}
	if response != nil && sizes != nil && sizes.ResponseBodySize > 0 && sizes.ResponseBodySize <= aura.MaxEnvelopeBytes {
		if responseBody, err := response.Body(); err == nil {
			report := aura.Validate(body, responseBody)
			for _, outcome := range report.Outcomes {
				states[outcome.Ordinal] = outcome.State
			}
			if len(report.Issues) > 0 && aura.IsFrameworkError(report.Issues[0].Code) {
				batch.FrameworkError = string(report.Issues[0].Code)
			}
		}
	}
	for index, action := range actions {
		if index == maxAuraBatchActions {
			break
		}
		batch.Actions = append(batch.Actions, protocol.AuraActionOutcome{
			Name:  action.Name(),
			Apex:  action.Apex != "",
			State: string(states[index+1]),
		})
	}
	return batch
}

// auraActionsEvidence is a step's Aura actions: one record per action, with
// its batch's duration, size and bytes and its state. Nil when the step sent
// no Aura request.
func auraActionsEvidence(entries []protocol.NetworkEntry) map[string]any {
	requests := 0
	omitted := 0
	actions := []any{}
	for _, entry := range entries {
		if entry.Aura == nil || len(entry.Aura.Actions) == 0 {
			continue
		}
		requests++
		for _, action := range entry.Aura.Actions {
			if len(actions) == maxStepAuraActions {
				omitted++
				continue
			}
			record := map[string]any{
				"name":        action.Name,
				"duration_ms": entry.DurationMs,
				"batch_size":  len(entry.Aura.Actions),
				"bytes":       entry.BytesReceived,
			}
			if action.Apex {
				record["apex"] = true
			}
			state := action.State
			if entry.Aura.FrameworkError != "" {
				state = entry.Aura.FrameworkError
			}
			if state != "" {
				record["state"] = state
			}
			actions = append(actions, record)
		}
	}
	if requests == 0 {
		return nil
	}
	evidence := map[string]any{"requests": requests, "actions": actions}
	if omitted > 0 {
		evidence["omitted"] = omitted
	}
	return evidence
}

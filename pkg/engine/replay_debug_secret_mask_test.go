package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type debugSecretMaskController struct{ seen []any }

func (c *debugSecretMaskController) BeforeAction(_ context.Context, action ReplayDebugAction) error {
	c.seen = append(c.seen, action)
	return nil
}

func (c *debugSecretMaskController) BeforeActionWithRuntimeState(_ context.Context, action ReplayDebugAction, snapshot ReplayRuntimeSnapshot) error {
	c.seen = append(c.seen, action, snapshot)
	return nil
}

func (c *debugSecretMaskController) AfterActionWithRuntimeState(_ context.Context, action ReplayDebugAction, event *ReplayActionEvent, snapshot ReplayRuntimeSnapshot) error {
	c.seen = append(c.seen, action, event, snapshot)
	return nil
}

func TestReplayUnredactedDebugMasksSecretStoreValues(t *testing.T) {
	const secret = "vault-customer-7781"
	// Credential-looking but not from the secret store: debug shows it raw.
	const raw = "token=raw-visible-123"
	name := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			Success:          true,
			Extractions:      map[string]string{"echo": action.Target},
			ProtocolEvidence: map[string]any{"nested": []any{map[string]any{"url": action.Target}}},
			Snapshot: &protocol.ErrorSnapshot{
				RequestURL:        action.Target,
				RequestHeaders:    map[string]string{"X-Customer": action.Headers["X-Customer"], "X-Raw": raw},
				ResponseBody:      `{"customer":"` + secret + `"}`,
				ResponseBodyState: "captured",
				ScreenshotBase64:  "cGl4ZWxz",
			},
		}, nil
	})
	script := &dsl.Script{Name: "debug", Protocol: name, Actions: []dsl.Action{{
		Name:    "request",
		Type:    "GET",
		Target:  "https://example.test/?customer={{secret.customer_value}}",
		Headers: map[string]string{"X-Customer": "{{secret.customer_value}}", "X-Raw": raw},
	}}}
	controller := &debugSecretMaskController{}
	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		UnredactedDebug: true,
		DebugController: controller,
		SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
			return secret, name == "customer_value"
		}),
		OnProgress: func(event ReplayProgressEvent) { progress = append(progress, event) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 1 || !report.Events[0].Success {
		t.Fatalf("replay events = %#v", report.Events)
	}
	if len(progress) == 0 || len(controller.seen) == 0 {
		t.Fatalf("progress=%d controller=%d", len(progress), len(controller.seen))
	}
	outputs := map[string]any{"report": report, "progress": progress, "controller": controller.seen}
	for label, output := range outputs {
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		text := string(encoded)
		if strings.Contains(text, secret) {
			t.Errorf("%s leaked the secret-store value: %s", label, text)
		}
		if strings.Contains(text, "cGl4ZWxz") {
			t.Errorf("%s kept a screenshot", label)
		}
	}
	event := report.Events[0]
	if event.Target != "https://example.test/?customer="+redactedPayloadValue {
		t.Errorf("target = %q", event.Target)
	}
	if event.Headers["X-Raw"] != raw || event.Snapshot == nil || event.Snapshot.RequestHeaders["X-Raw"] != raw {
		t.Errorf("non-secret values were not left raw: headers=%v snapshot=%#v", event.Headers, event.Snapshot)
	}
	if event.Snapshot.ResponseBody != `{"customer":"`+redactedPayloadValue+`"}` {
		t.Errorf("response body = %q", event.Snapshot.ResponseBody)
	}
}

func TestMaskDebugSecretsCopiesInsteadOfMutating(t *testing.T) {
	const secret = "vault-secret-0042"
	mask, err := newDebugSecretMask([]string{secret})
	if err != nil {
		t.Fatal(err)
	}
	original := &ReplayActionEvent{
		Target:           "https://example.test/" + secret,
		Headers:          map[string]string{"X": secret},
		StepPath:         []string{"step-" + secret},
		ProtocolEvidence: map[string]any{"value": secret},
		Screenshot:       "cGl4ZWxz",
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: secret},
	}
	masked := maskDebugSecrets(mask, original)
	if masked == original {
		t.Fatal("masked event shares the original pointer")
	}
	if original.Target != "https://example.test/"+secret || original.Headers["X"] != secret || original.StepPath[0] != "step-"+secret || original.ProtocolEvidence["value"] != secret || original.Screenshot == "" || original.Snapshot.ResponseBody != secret {
		t.Fatalf("original mutated: %#v", original)
	}
	encoded, _ := json.Marshal(masked)
	if strings.Contains(string(encoded), secret) || masked.Screenshot != "" {
		t.Fatalf("masked event = %s", encoded)
	}
	if unchanged := maskDebugSecrets(mask, &ReplayActionEvent{Target: "plain"}); unchanged.Target != "plain" {
		t.Fatalf("unchanged event = %#v", unchanged)
	}
	if maskDebugSecrets[*ReplayActionEvent](nil, original) != original {
		t.Fatal("a nil mask must return the value as is")
	}
}

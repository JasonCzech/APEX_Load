package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	networkprotocol "github.com/jasonczech/apexload/pkg/protocol/network"
)

// protocol_evidence's request-describing entries (target, metadata.headers,
// metadata.params) follow config.log.redact_run_evidence like the rest of the
// authored request; response-derived entries keep the full scrub and a
// platform-managed value is masked in both modes. Fed by real Network adapter
// output and published through the engine's result and replay-event scrubbers.
func TestProtocolEvidenceAuthoredRequestFollowsRunEvidenceSetting(t *testing.T) {
	const (
		literalPwd    = "lit-evidence-pwd-9301"
		literalSecret = "lit-evidence-cs-9302"
		literalTok    = "lit-evidence-tok-9303"
	)
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			n := &networkprotocol.Network{}
			if err := n.Init(context.Background(), map[string]any{protocol.RedactAuthoredEvidenceConfigKey: mode.redact}); err != nil {
				t.Fatal(err)
			}
			result, err := n.Execute(context.Background(), &protocol.Action{
				Name: "ldap", Type: "ldap_bind",
				Target:  "ldap://svc:" + literalPwd + "@directory.example.test/?password=" + literalPwd,
				Headers: map[string]string{"Authorization": "Bearer " + literalTok, "X-Api-Key": authoredPlatformSecret},
				Params: map[string]any{
					"timeout_ms":    "not-a-number",
					"password":      literalPwd,
					"client_secret": literalSecret,
					"api_key":       authoredPlatformSecret,
				},
			})
			if err != nil || result == nil || len(result.ProtocolEvidence) == 0 {
				t.Fatalf("%+v %v", result, err)
			}
			// The server/result side of the evidence carries a sensitively
			// named extraction; it keeps the name mask in both modes.
			adapterMeta, ok := result.ProtocolEvidence["metadata"].(map[string]any)
			if !ok || adapterMeta["params"] == nil || adapterMeta["headers"] == nil {
				t.Fatalf("adapter evidence lacks the authored request: %+v", result.ProtocolEvidence)
			}
			adapterMeta["extractions"] = map[string]string{"session_token": "resp-evidence-tok-9304"}
			result.ProtocolEvidence["records"] = map[string]any{"filter": "password=resp-evidence-pw-9305", "password": "resp-evidence-pw-9305"}
			config := authoredEvidenceRootConfig(t, mode.redact)
			event := &ReplayActionEvent{ProtocolEvidence: cloneAnyMap(result.ProtocolEvidence)}
			scrubSensitiveStructuredResult(result, config)
			scrubSensitiveResultWithOptions(result, config, true)
			scrubReplayActionEvent(event, config)
			for label, published := range map[string]map[string]any{"result": result.ProtocolEvidence, "event": event.ProtocolEvidence} {
				encoded, err := json.Marshal(published)
				if err != nil {
					t.Fatal(err)
				}
				text := string(encoded)
				for _, value := range []string{authoredPlatformSecret, "resp-evidence-tok-9304", "resp-evidence-pw-9305"} {
					if strings.Contains(text, value) {
						t.Fatalf("%s: platform/response value %q published: %s", label, value, text)
					}
				}
				for _, value := range []string{literalPwd, literalSecret, literalTok} {
					if strings.Contains(text, value) == mode.redact {
						t.Fatalf("%s: authored %q visible=%v, want %v: %s", label, value, !mode.redact, !mode.redact, text)
					}
				}
				meta, _ := published["metadata"].(map[string]any)
				headers := normalizeEvidenceValue(meta["headers"])
				if got := evidenceStringAt(headers, "X-Api-Key"); got != redactedPayloadValue {
					t.Fatalf("%s: platform-managed header = %q, want the whole value masked", label, got)
				}
				if marked, _ := published[ProtocolEvidenceAuthoredRequestMarker].(bool); marked == mode.redact {
					t.Fatalf("%s: authored marker = %v, want %v", label, marked, !mode.redact)
				}
			}
		})
	}
}

func evidenceStringAt(value any, key string) string {
	switch typed := value.(type) {
	case map[string]any:
		text, _ := typed[key].(string)
		return text
	case map[string]string:
		return typed[key]
	}
	return ""
}

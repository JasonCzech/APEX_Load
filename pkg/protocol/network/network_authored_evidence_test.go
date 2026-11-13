package network

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Name-based masking of the authored request (sensitive header names and param
// keys) follows config.log.redact_run_evidence, carried to the adapter as
// protocol.RedactAuthoredEvidenceConfigKey. A value the engine's exact matcher
// knows as platform-managed stays masked in both modes; the server response
// keeps its scrub.
func TestNetworkAuthoredRequestEvidenceFollowsRunEvidenceSetting(t *testing.T) {
	const (
		literalPwd = "lit-net-pwd-7701"
		literalTok = "lit-net-tok-7702"
		platform   = "platform-net-val-7703"
	)
	exact := func(value string) string { return strings.ReplaceAll(value, platform, "[REDACTED]") }
	for _, mode := range []struct {
		name   string
		config map[string]any
		redact bool
	}{
		{name: "redaction_on", config: map[string]any{protocol.RedactAuthoredEvidenceConfigKey: true}, redact: true},
		{name: "redaction_off", config: map[string]any{protocol.RedactAuthoredEvidenceConfigKey: false}, redact: false},
		{name: "direct_caller_without_key", config: map[string]any{}, redact: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			n := &Network{}
			if err := n.Init(context.Background(), mode.config); err != nil {
				t.Fatal(err)
			}
			n.SetRuntimeTextRedactor(exact)
			n.SetRuntimeAuthoredTextRedactor(exact)
			action := &protocol.Action{
				Name:    "smtp",
				Type:    "smtp_send",
				Target:  "smtp://mail.example.test?password=" + literalPwd,
				Headers: map[string]string{"Authorization": "Bearer " + literalTok, "X-Api-Key": platform},
				Params: map[string]any{
					"password": literalPwd,
					"nested":   map[string]any{"client_secret": literalTok, "ok": "fine"},
					"tags":     map[string]string{"token": literalTok},
				},
				Body: []byte("password=" + literalPwd + " key=" + platform),
			}
			result := &protocol.Result{StatusCode: 535, ResponseBody: `{"password":"server-echo-7704"}`}
			snapshot := n.networkSnapshot(action, result)
			evidence := n.redactedEvidenceAction(action)
			encoded, err := json.Marshal(map[string]any{"snapshot": snapshot, "headers": evidence.RedactedHeaders, "params": evidence.RedactedParams, "target": evidence.RedactedTarget})
			if err != nil {
				t.Fatal(err)
			}
			text := string(encoded)
			if strings.Contains(text, platform) {
				t.Fatalf("platform-managed value leaked: %s", text)
			}
			if snapshot.RequestHeaders["X-Api-Key"] != "[REDACTED]" {
				t.Fatalf("platform-managed header = %q", snapshot.RequestHeaders["X-Api-Key"])
			}
			if strings.Contains(snapshot.ResponseBody, "server-echo-7704") {
				t.Fatalf("response lost its scrub: %q", snapshot.ResponseBody)
			}
			for _, value := range []string{literalPwd, literalTok} {
				if strings.Contains(text, value) == mode.redact {
					t.Fatalf("authored %q visible=%v, want %v: %s", value, !mode.redact, !mode.redact, text)
				}
			}
			if got := evidence.RedactedParams["password"]; (got == literalPwd) == mode.redact {
				t.Fatalf("param password = %v", got)
			}
			if got := snapshot.RequestHeaders["Authorization"]; strings.Contains(got, literalTok) == mode.redact {
				t.Fatalf("Authorization = %q", got)
			}
		})
	}
}

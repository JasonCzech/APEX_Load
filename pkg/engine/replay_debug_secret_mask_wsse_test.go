package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func deepDebugEvidence(levels int, leaf string) any {
	var value any = leaf
	for i := 0; i < levels; i++ {
		value = []any{value}
	}
	return value
}

func deepDebugEvidenceLeaf(value any) (string, bool) {
	for {
		switch typed := value.(type) {
		case []any:
			if len(typed) != 1 {
				return "", false
			}
			value = typed[0]
		case string:
			return typed, true
		default:
			return "", false
		}
	}
}

// A debug replay that references no secret-store values must not lose deeply
// nested evidence to the mask's depth bound; only managed-secret masks drop it.
func TestMaskDebugSecretsKeepsDeepEvidenceWithoutManagedSecrets(t *testing.T) {
	mask, err := newDebugSecretMask(nil)
	if err != nil {
		t.Fatal(err)
	}
	event := &ReplayActionEvent{ProtocolEvidence: map[string]any{"payload": deepDebugEvidence(maxDebugSecretMaskDepth+10, "deep-leaf")}}
	masked := maskDebugSecrets(mask, event)
	leaf, ok := deepDebugEvidenceLeaf(masked.ProtocolEvidence["payload"])
	if !ok || leaf != "deep-leaf" {
		t.Fatalf("deep evidence was altered without managed secrets: leaf=%q ok=%v", leaf, ok)
	}

	const secret = "vault-secret-deep-0042"
	secretMask, err := newDebugSecretMask([]string{secret})
	if err != nil {
		t.Fatal(err)
	}
	secretEvent := &ReplayActionEvent{ProtocolEvidence: map[string]any{"payload": deepDebugEvidence(maxDebugSecretMaskDepth+10, secret)}}
	secretMasked := maskDebugSecrets(secretMask, secretEvent)
	if leaf, ok := deepDebugEvidenceLeaf(secretMasked.ProtocolEvidence["payload"]); ok && leaf == secret {
		t.Fatal("managed-secret mask must still drop values past the depth bound")
	}
}

// The debug mask hides secret-store values only. A WS-Security password
// element holding a script-sourced value is left as the protocol saw it; one
// holding a secret-store value (raw or XML-escaped) is masked.
func TestMaskDebugSecretsWSSecurityPasswordsByOrigin(t *testing.T) {
	const managed = "vault<wsse>pass-0042"
	scripted := `<wsse:UsernameToken><wsse:Username>svc</wsse:Username><wsse:Password Type="x#PasswordText">script-sourced-pass</wsse:Password></wsse:UsernameToken>`
	if mask, err := newDebugSecretMask(nil); err != nil || maskDebugSecrets(mask, scripted) != scripted {
		t.Fatalf("script-sourced WS-Security password changed without managed secrets: err=%v", err)
	}
	mask, err := newDebugSecretMask([]string{managed})
	if err != nil {
		t.Fatal(err)
	}
	if got := maskDebugSecrets(mask, scripted); got != scripted {
		t.Fatalf("script-sourced WS-Security password changed: %q", got)
	}
	for _, spelling := range []string{managed, "vault&lt;wsse&gt;pass-0042"} {
		body := `<wsse:UsernameToken><wsse:Password Type="x#PasswordText">` + spelling + `</wsse:Password></wsse:UsernameToken>`
		if got := maskDebugSecrets(mask, body); strings.Contains(got, spelling) || strings.Contains(got, "pass-0042") {
			t.Fatalf("secret-store WS-Security password %q not masked: %q", spelling, got)
		}
	}
}

// The SOAP adapter keeps the WS-Security header (with the resolved password)
// in its snapshot and evidence bodies. When that password came from the
// secret store, run-evidence publication masks it through the runtime secret
// matcher, in raw and XML-escaped spellings.
func TestRunEvidenceMasksSecretStoreWSSecurityPassword(t *testing.T) {
	const managed = "vault&wsse-run-0042"
	header := func(password string) string {
		return `<soap:Envelope><soap:Header><wsse:Security><wsse:UsernameToken><wsse:Password Type="x#PasswordText">` + password + `</wsse:Password></wsse:UsernameToken></wsse:Security></soap:Header><soap:Body/></soap:Envelope>`
	}
	config := WithSecretValueRedaction(nil, []string{managed})
	result := &protocol.Result{
		Success: false,
		Snapshot: &protocol.ErrorSnapshot{
			RequestBody:  header("vault&amp;wsse-run-0042"),
			ResponseBody: header(managed),
		},
		ProtocolEvidence: map[string]any{"request_body": header("vault&amp;wsse-run-0042")},
	}
	scrubSensitiveResultWithOptions(result, config, true)
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "wsse-run-0042") {
		t.Fatalf("run evidence leaked the secret-store WS-Security password: %s", raw)
	}
}

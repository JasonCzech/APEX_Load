package api

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMalformedCompleteCredentialBodyRetainsGatedReviewAndMasksReplicas(t *testing.T) {
	const secret = "synthetic-complete-credential"
	for _, encoding := range []string{"", "base64"} {
		body := `{"password":"` + secret + `"`
		if encoding != "" {
			body = base64.StdEncoding.EncodeToString([]byte(body))
		}
		script := &dsl.Script{Name: secret, Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: secret, Type: "request", Method: http.MethodPost, Target: "/login", Headers: map[string]string{"Content-Type": "application/json"}, Body: body, BodyEncoding: encoding, Metadata: map[string]any{"note": secret}}}}
		safe := maskScriptSecrets(script)
		if safe == nil || safe.Name != maskedScriptSecret || safe.Actions[0].Name != maskedScriptSecret || safe.Actions[0].Metadata["note"] != maskedScriptSecret || !scriptHasExecutionBlockerCode(safe, "import_review_required") {
			t.Fatal("complete malformed credential did not produce safe gated review")
		}
		wire := safe.Actions[0].Body
		if encoding != "" {
			decoded, err := base64.StdEncoding.DecodeString(wire)
			if err != nil {
				t.Fatal(err)
			}
			wire = string(decoded)
		}
		if wire != maskedScriptSecret || script.Name != secret || script.Actions[0].Body != body {
			t.Fatal("gated projection leaked credential or mutated executable source")
		}
	}
}

func TestUnknownCredentialTemplateReplicasDoNotEscapeOrdinaryReview(t *testing.T) {
	const literal = "{{fn.not_a_builtin(raw-token)}}"
	script := &dsl.Script{Name: literal, Protocol: "http", Actions: []dsl.Action{{Name: literal, Type: "request", Method: http.MethodPost, Target: "/", Body: "password={{raw-secret}}&token=" + literal, Metadata: map[string]any{"note": literal}}}}
	safe := maskScriptSecrets(script)
	if safe == nil || safe.Name != maskedScriptSecret || safe.Actions[0].Name != maskedScriptSecret || safe.Actions[0].Metadata["note"] != maskedScriptSecret || strings.Contains(safe.Actions[0].Body, "raw-secret") || strings.Contains(safe.Actions[0].Body, "raw-token") {
		t.Fatal("unknown credential templates or copied labels survived ordinary review")
	}
	if script.Name != literal || script.Actions[0].Body != "password={{raw-secret}}&token="+literal {
		t.Fatal("ordinary review changed executable source")
	}
}

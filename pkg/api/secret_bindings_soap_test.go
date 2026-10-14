package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// soapTokenLikeMessageID is a WS-Addressing message ID that reads as a token,
// so review shows it as projectedXMLMessageID.
const soapTokenLikeMessageID = "Q2hhbGxlbmdlVG9rZW4tOWYzYTdjMWIyZDRlNWY2YTdiOGM5ZDBl"

func soapLoginBody(password string) string {
	return `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:wsa="http://www.w3.org/2005/08/addressing" ` +
		`xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"><soap:Header>` +
		`<wsa:MessageID>` + soapTokenLikeMessageID + `</wsa:MessageID><wsse:Security><wsse:UsernameToken>` +
		`<wsse:Username>alice</wsse:Username><wsse:Password>` + password + `</wsse:Password></wsse:UsernameToken></wsse:Security>` +
		`</soap:Header><soap:Body><Ping/></soap:Body></soap:Envelope>`
}

// Review shows a token-like WS-Addressing message ID as a fresh UUID, but the
// recording keeps its own, so the WS-Security password beside it still lines
// up with the recording: it binds to a project secret through the API, and
// the bound body keeps the recorded message ID.
func TestSOAPPasswordRebindsDespiteMessageIDRewrite(t *testing.T) {
	const password = "synthetic-wsse-password-8c2f"
	script := loginScript("SOAP login", "text/xml; charset=utf-8", soapLoginBody(password))
	if shown := maskScriptSecrets(script).Actions[1].Body; !strings.Contains(shown, "<wsa:MessageID>"+projectedXMLMessageID+"</wsa:MessageID>") ||
		!strings.Contains(shown, "<wsse:Password>"+maskedScriptSecret+"</wsse:Password>") {
		t.Fatalf("review shows %q, want the message ID as a UUID and the password masked", shown)
	}

	server := NewServer(ServerConfig{})
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	requirement := passwordRequirement(t, preview["secret_binding_requirements"].([]SecretBindingRequirement))
	if requirement.RebindUnavailableReason != "" || !requirement.DestinationValuePreserved {
		t.Fatalf("requirement = %#v, want it kept and rebindable", requirement)
	}
	bindings := []secretBindingSelection{{RequirementID: requirement.ID, SecretName: "SOAP_PASSWORD"}}

	resolved, remaining, err := applyCredentialPreservingSecretBindings(script, 0, bindings)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if want := soapLoginBody("{{secret.SOAP_PASSWORD}}"); resolved.Actions[1].Body != want {
		t.Fatalf("bound body = %q, want the recorded message ID and the reference", resolved.Actions[1].Body)
	}
	for _, left := range remaining {
		if left.Path == "/actions/1/body" {
			t.Fatalf("requirement %#v remained after binding", left)
		}
	}

	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	applied := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script":   masked[0],
		"bindings": []map[string]string{{"requirement_id": requirement.ID, "secret_name": "SOAP_PASSWORD"}},
	})
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", applied.Code, applied.Body.String())
	}
	var result struct {
		Script       dsl.Script                 `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(applied.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// The draft the apply returns is exact: the recorded message ID, and the
	// reference in the password's place.
	if want := soapLoginBody("{{secret.SOAP_PASSWORD}}"); result.Script.Actions[1].Body != want {
		t.Fatalf("apply returned %q, want %q", result.Script.Actions[1].Body, want)
	}
	for _, left := range result.Requirements {
		if left.Path == "/actions/1/body" {
			t.Fatalf("requirement %#v remained after the apply", left)
		}
	}
}

// A capture exported already scrubbed records its message ID as the
// placeholder. Putting that back would add a sentinel review never showed, so
// the password would be read, and bound, at the message ID's place. The body
// stays unaligned instead: kept as recorded, not offered for rebinding.
func TestSOAPRecordedPlaceholderMessageIDKeepsPasswordAsRecorded(t *testing.T) {
	body := strings.Replace(soapLoginBody("synthetic-wsse-password-6d0b"), soapTokenLikeMessageID, maskedScriptSecret, 1)
	script := loginScript("Scrubbed SOAP", "text/xml; charset=utf-8", body)
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirement := passwordRequirement(t, response["secret_binding_requirements"].([]SecretBindingRequirement))
	if requirement.RebindUnavailableReason == "" || !requirement.DestinationValuePreserved {
		t.Fatalf("requirement = %#v, want it kept as recorded and not rebindable", requirement)
	}
	_, _, err := applyCredentialPreservingSecretBindings(script, 0, []secretBindingSelection{{RequirementID: requirement.ID, SecretName: "SOAP_PASSWORD"}})
	var refusal *secretBindingRebindUnavailableError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a rebind refusal", err)
	}
}

func TestProjectedMessageIDsAsRecorded(t *testing.T) {
	for _, test := range []struct{ name, raw, projected, want string }{
		{
			name:      "rewritten ID",
			raw:       `<a:MessageID>urn:tok-1</a:MessageID><p>secret</p>`,
			projected: `<a:MessageID>{{fn.uuid()}}</a:MessageID><p>[REDACTED]</p>`,
			want:      `<a:MessageID>urn:tok-1</a:MessageID><p>[REDACTED]</p>`,
		},
		{
			name:      "whitespace the rewrite dropped",
			raw:       "<MessageId>\n  tok-2\n</MessageId>",
			projected: `<MessageId>{{fn.uuid()}}</MessageId>`,
			want:      "<MessageId>\n  tok-2\n</MessageId>",
		},
		{
			name:      "only the rewritten one of two",
			raw:       `<MessageID>{{fn.uuid()}}</MessageID><MessageID>tok-3</MessageID>`,
			projected: `<MessageID>{{fn.uuid()}}</MessageID><MessageID>{{fn.uuid()}}</MessageID>`,
			want:      `<MessageID>{{fn.uuid()}}</MessageID><MessageID>tok-3</MessageID>`,
		},
		{
			name:      "counts disagree",
			raw:       `<MessageID>tok-4</MessageID>`,
			projected: `<MessageID>{{fn.uuid()}}</MessageID><MessageID>{{fn.uuid()}}</MessageID>`,
			want:      `<MessageID>{{fn.uuid()}}</MessageID><MessageID>{{fn.uuid()}}</MessageID>`,
		},
		{
			name:      "recorded placeholder",
			raw:       `<MessageID>[REDACTED]</MessageID><p>secret</p>`,
			projected: `<MessageID>{{fn.uuid()}}</MessageID><p>[REDACTED]</p>`,
			want:      `<MessageID>{{fn.uuid()}}</MessageID><p>[REDACTED]</p>`,
		},
		{
			name:      "no rewrite",
			raw:       `password=secret`,
			projected: `password=[REDACTED]`,
			want:      `password=[REDACTED]`,
		},
	} {
		if got := projectedMessageIDsAsRecorded(test.raw, test.projected); got != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}

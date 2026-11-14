package engine

import (
	"crypto/sha1"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func wsseDigestBody(digest, nonce, created string) string {
	return `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header>` +
		`<wsse:Security xmlns:wsse="x" xmlns:wsu="y"><wsse:UsernameToken><wsse:Username>svc</wsse:Username>` +
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + digest + `</wsse:Password>` +
		`<wsse:Nonce>` + nonce + `</wsse:Nonce><wsu:Created>` + created + `</wsu:Created></wsse:UsernameToken></wsse:Security>` +
		`</soapenv:Header><soapenv:Body><ping/></soapenv:Body></soapenv:Envelope>`
}

// The decision is by origin, never by placeholder shape. The SOAP adapter
// withholds a secret-store PasswordDigest and its Nonce itself, so the engine
// never sees that nonce. A script that authors a literal "[REDACTED]" digest
// (for example, imported from an already redacted HAR) beside a literal nonce
// keeps both visible with redact_run_evidence off, in request bodies and
// snapshot bodies alike; so does a script-literal digest.
func TestWSSecurityAuthoredPlaceholderDigestKeepsScriptNonce(t *testing.T) {
	const nonce = "6rLctRkpMUiv88DYd2XxMg=="
	const created = "2026-11-14T00:37:01Z"
	nonceBytes, _ := base64.StdEncoding.DecodeString(nonce)
	literalSum := sha1.Sum(append(append(append([]byte(nil), nonceBytes...), created...), "script-literal-pass"...))
	literalDigest := base64.StdEncoding.EncodeToString(literalSum[:])

	config := authoredEvidenceRootConfig(t, false)

	placeholder := wsseDigestBody(redactedPayloadValue, nonce, created)
	if got := maskPlatformDerivedWSSecurityDigests(placeholder, config); got != placeholder {
		t.Fatalf("authored placeholder digest masked its script-sourced nonce: %s", got)
	}

	literal := wsseDigestBody(literalDigest, nonce, created)
	if got := maskPlatformDerivedWSSecurityDigests(literal, config); got != literal {
		t.Fatalf("script-literal digest evidence changed: %s", got)
	}

	for _, body := range []string{placeholder, literal} {
		result := &protocol.Result{Snapshot: &protocol.ErrorSnapshot{RequestBody: body}}
		scrubSensitiveResultWithOptions(result, config, true)
		if !strings.Contains(result.Snapshot.RequestBody, "<wsse:Nonce>"+nonce+"</wsse:Nonce>") {
			t.Fatalf("snapshot masked a script-sourced nonce: %s", result.Snapshot.RequestBody)
		}
	}
	literalResult := &protocol.Result{Snapshot: &protocol.ErrorSnapshot{RequestBody: literal}}
	scrubSensitiveResultWithOptions(literalResult, config, true)
	if !strings.Contains(literalResult.Snapshot.RequestBody, literalDigest) {
		t.Fatalf("snapshot masked a script-literal digest: %s", literalResult.Snapshot.RequestBody)
	}
}

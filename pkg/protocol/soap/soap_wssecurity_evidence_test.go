package soap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The evidence/snapshot request body must carry the same WS-Security Nonce and
// Created values the server received. The script-sourced Password token is
// preserved: it is not a secret, and the adapter never redacts it by shape.
func TestSOAPEvidenceWSSecurityHeaderMatchesWireRequest(t *testing.T) {
	var wireRequestBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		wireRequestBody = string(b)
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Client</faultcode><faultstring>nonce already used</faultstring></soap:Fault></soap:Body></soap:Envelope>`))
	}))
	defer ts.Close()

	calls := 0
	ticks := 0
	base := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s := &SOAP{
		nonceGenerator: func() ([]byte, error) {
			calls++
			return []byte(fmt.Sprintf("nonce-%d", calls)), nil
		},
		now: func() time.Time {
			ticks++
			return base.Add(time.Duration(ticks) * time.Second)
		},
	}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                       ts.URL,
		"soap_fault_fail_on_fault":       true,
		"soap_ws_security_username":      "svc",
		"soap_ws_security_password":      "wire-secret",
		"soap_ws_security_password_type": "digest",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = s.Teardown(context.Background()) }()

	res, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "NonceEvidence",
		Target: "/ws",
		Body:   []byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetOrder/></soap:Body></soap:Envelope>`),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || res.Snapshot == nil {
		t.Fatalf("expected error snapshot, got %#v", res)
	}
	if calls != 1 {
		t.Fatalf("nonce generator called %d times, want exactly once per request", calls)
	}
	nonceRe := regexp.MustCompile(`<wsse:Nonce>([^<]*)</wsse:Nonce>`)
	createdRe := regexp.MustCompile(`<wsu:Created>([^<]*)</wsu:Created>`)
	wireNonce := nonceRe.FindStringSubmatch(wireRequestBody)
	wireCreated := createdRe.FindStringSubmatch(wireRequestBody)
	if len(wireNonce) != 2 || len(wireCreated) != 2 {
		t.Fatalf("wire body missing Nonce/Created: %s", wireRequestBody)
	}
	snapNonce := nonceRe.FindStringSubmatch(res.Snapshot.RequestBody)
	snapCreated := createdRe.FindStringSubmatch(res.Snapshot.RequestBody)
	if len(snapNonce) != 2 || snapNonce[1] != wireNonce[1] {
		t.Fatalf("snapshot Nonce = %v, wire Nonce = %q; snapshot body %s", snapNonce, wireNonce[1], res.Snapshot.RequestBody)
	}
	if len(snapCreated) != 2 || snapCreated[1] != wireCreated[1] {
		t.Fatalf("snapshot Created = %v, wire Created = %q", snapCreated, wireCreated[1])
	}
	wirePassword := regexp.MustCompile(`<wsse:Password[^>]*>([^<]*)</wsse:Password>`).FindStringSubmatch(wireRequestBody)
	if len(wirePassword) != 2 || !strings.Contains(res.Snapshot.RequestBody, ">"+wirePassword[1]+"</wsse:Password>") {
		t.Fatalf("snapshot must carry the wire password token unchanged: %s", res.Snapshot.RequestBody)
	}
	rawEvidence, _ := json.Marshal(res.ProtocolEvidence)
	if strings.Contains(string(rawEvidence), "<wsse:Nonce>") && !strings.Contains(string(rawEvidence), "<wsse:Nonce>"+wireNonce[1]+"</wsse:Nonce>") &&
		!strings.Contains(string(rawEvidence), `<wsse:Nonce>`+wireNonce[1]+`</wsse:Nonce>`) {
		t.Fatalf("protocol evidence Nonce differs from wire Nonce %q: %s", wireNonce[1], rawEvidence)
	}
}

// A WS-Security password resolved from the secret store ({{secret.NAME}})
// stays hidden in every evidence copy, unredacted debug included. In digest
// mode the exact-value secret matcher cannot recognize the PasswordDigest, so
// the adapter withholds the Password element itself by origin, and with it
// the Nonce the digest was computed from; Created is kept. In text mode the
// Nonce is kept. The wire request is untouched.
func TestSOAPEvidenceWithholdsSecretStoreWSSecurityPassword(t *testing.T) {
	for _, passwordType := range []string{"digest", "text"} {
		for _, unredacted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/unredacted=%v", passwordType, unredacted), func(t *testing.T) {
				var wireRequestBody string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					wireRequestBody = string(b)
					w.Header().Set("Content-Type", "text/xml; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Server</faultcode><faultstring>boom</faultstring></soap:Fault></soap:Body></soap:Envelope>`))
				}))
				defer ts.Close()

				const resolved = "resolved-secret-store-pass"
				s := &SOAP{}
				config := map[string]any{
					"base_url":                       ts.URL,
					"soap_fault_fail_on_fault":       true,
					"soap_ws_security_username":      "svc",
					"soap_ws_security_password":      resolved,
					"soap_ws_security_password_type": passwordType,
					SecretStorePasswordConfigKey:     true,
				}
				if unredacted {
					config[protocol.ReplayUnredactedDebugConfigKey] = true
				}
				if err := s.Init(context.Background(), config); err != nil {
					t.Fatalf("Init: %v", err)
				}
				defer func() { _ = s.Teardown(context.Background()) }()

				res, err := s.Execute(context.Background(), &protocol.Action{
					Name:   "SecretStoreWSSE",
					Target: "/ws",
					Body:   []byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetOrder/></soap:Body></soap:Envelope>`),
				})
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if res == nil || res.Snapshot == nil {
					t.Fatalf("expected error snapshot, got %#v", res)
				}
				wireToken := regexp.MustCompile(`<wsse:Password[^>]*>([^<]*)</wsse:Password>`).FindStringSubmatch(wireRequestBody)
				if len(wireToken) != 2 || wireToken[1] == "" || wireToken[1] == redactedSOAPPassword {
					t.Fatalf("wire request must send the real password token: %s", wireRequestBody)
				}
				if passwordType == "digest" && wireToken[1] == resolved {
					t.Fatalf("digest mode must not send the plain password")
				}
				wireNonce := regexp.MustCompile(`<wsse:Nonce>([^<]*)</wsse:Nonce>`).FindStringSubmatch(wireRequestBody)
				if passwordType == "digest" {
					if len(wireNonce) != 2 || wireNonce[1] == "" {
						t.Fatalf("digest wire request must send a Nonce: %s", wireRequestBody)
					}
					// The nonce is an input of the withheld digest: the adapter
					// withholds it by origin, so the engine never has to guess
					// from the placeholder's shape.
					wantNonce := "<wsse:Nonce>" + redactedSOAPPassword + "</wsse:Nonce>"
					if !strings.Contains(res.Snapshot.RequestBody, wantNonce) {
						t.Fatalf("snapshot Nonce: want %s in %s", wantNonce, res.Snapshot.RequestBody)
					}
				}
				if !strings.Contains(res.Snapshot.RequestBody, ">"+redactedSOAPPassword+"</wsse:Password>") {
					t.Fatalf("snapshot must withhold the secret-store Password element: %s", res.Snapshot.RequestBody)
				}
				all, _ := json.Marshal(struct {
					Snapshot any
					Evidence any
					Network  any
				}{res.Snapshot, res.ProtocolEvidence, res.NetworkEntries})
				if passwordType == "digest" && strings.Contains(string(all), wireNonce[1]) {
					t.Fatalf("evidence carries the secret-store digest's Nonce %q: %s", wireNonce[1], all)
				}
				if strings.Contains(string(all), wireToken[1]) || strings.Contains(string(all), resolved) {
					t.Fatalf("evidence carries the secret-store password token %q: %s", wireToken[1], all)
				}
			})
		}
	}
}

// A secret-store password containing '$' must reach the wire byte-for-byte
// (no regexp template expansion) and stay withheld from the unredacted
// snapshot, replay HAR and protocol evidence, for envelopes that already carry
// a Header element (open/close and self-closing).
func TestSOAPSecretStorePasswordWithDollarIsLiteralOnWireAndWithheld(t *testing.T) {
	const resolved = "Store$$Pass$word9$0${1}"
	envelopes := map[string]string{
		"header-close":      `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Header><x:Trace xmlns:x="urn:t">1</x:Trace></soap:Header><soap:Body><GetOrder/></soap:Body></soap:Envelope>`,
		"header-self-close": `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Header a="b"/><soap:Body><GetOrder/></soap:Body></soap:Envelope>`,
		"no-header":         `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetOrder/></soap:Body></soap:Envelope>`,
	}
	for name, envelope := range envelopes {
		for _, secretStore := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/secretStore=%v", name, secretStore), func(t *testing.T) {
				var wireRequestBody string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					wireRequestBody = string(b)
					w.Header().Set("Content-Type", "text/xml; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Server</faultcode><faultstring>boom</faultstring></soap:Fault></soap:Body></soap:Envelope>`))
				}))
				defer ts.Close()

				capture, err := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "both"})
				if err != nil {
					t.Fatalf("NewReplayHARCapture: %v", err)
				}
				s := &SOAP{}
				config := map[string]any{
					"base_url":                              ts.URL,
					"soap_fault_fail_on_fault":              true,
					"soap_ws_security_username":             "svc",
					"soap_ws_security_password":             resolved,
					"soap_ws_security_password_type":        "text",
					protocol.ReplayUnredactedDebugConfigKey: true,
					protocol.ReplayHARCaptureConfigKey:      capture,
					protocol.ReplayNetworkCaptureConfigKey:  true,
				}
				if secretStore {
					config[SecretStorePasswordConfigKey] = true
				}
				if err := s.Init(context.Background(), config); err != nil {
					t.Fatalf("Init: %v", err)
				}
				defer func() { _ = s.Teardown(context.Background()) }()

				res, err := s.Execute(context.Background(), &protocol.Action{Name: "DollarPass", Target: "/ws", Body: []byte(envelope)})
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if res == nil || res.Snapshot == nil {
					t.Fatalf("expected error snapshot, got %#v", res)
				}
				if !strings.Contains(wireRequestBody, ">"+resolved+"</wsse:Password>") {
					t.Fatalf("wire must carry the exact password: %s", wireRequestBody)
				}
				if strings.Count(wireRequestBody, "<wsse:Security") != 1 || strings.Count(wireRequestBody, ":Header") != 2 {
					t.Fatalf("wire must carry exactly one Security header inside one Header element: %s", wireRequestBody)
				}
				if name == "header-self-close" && !strings.Contains(wireRequestBody, `<soap:Header a="b">`) {
					t.Fatalf("self-closing Header attributes must be kept: %s", wireRequestBody)
				}
				harTexts := []string{}
				for _, entry := range res.NetworkEntries {
					if entry.HAR != nil && entry.HAR.RequestBody != nil {
						harTexts = append(harTexts, entry.HAR.RequestBody.Text)
					}
				}
				if len(harTexts) == 0 {
					t.Fatalf("expected a replay HAR request body")
				}
				rawEvidence, _ := json.Marshal(res.ProtocolEvidence)
				copies := append([]string{res.Snapshot.RequestBody, string(rawEvidence)}, harTexts...)
				for i, text := range copies {
					if secretStore {
						for _, fragment := range []string{resolved, "Store$Pass", "Pass$word9", "Store$$Pass"} {
							if strings.Contains(text, fragment) {
								t.Fatalf("evidence copy %d carries secret-store password fragment %q: %s", i, fragment, text)
							}
						}
						if i != 1 && !strings.Contains(text, ">"+redactedSOAPPassword+"</wsse:Password>") {
							t.Fatalf("evidence copy %d must show the withheld Password element: %s", i, text)
						}
					} else if i != 1 && text != wireRequestBody {
						t.Fatalf("script-sourced evidence copy %d must equal the wire body\nwire: %s\ngot:  %s", i, wireRequestBody, text)
					}
				}
			})
		}
	}
}

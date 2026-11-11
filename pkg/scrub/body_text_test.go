package scrub

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestBodyTextRedactsInlineCredentialAssignments(t *testing.T) {
	for _, input := range []string{`<script>fetch('/',{body:JSON.stringify({password:'private-value',safe:'visible'})})</script>`, `const access_token = "private-value"; let safe="visible"`, `{"password":"private-value","safe":"visible"}`, `password: 'private\'value', safe:'visible'`} {
		out := BodyText(input)
		if strings.Contains(out, "private") || !strings.Contains(out, "visible") {
			t.Fatalf("%q", out)
		}
	}
	if got := ValueLabel("password:'ordinary-source-label'", Policy{}); got != "password:'ordinary-source-label'" {
		t.Fatal("label behavior changed")
	}
}

func TestHARBodyAssignmentRedactionIncludesBase64Text(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(`const password = 'private-value'; let safe='visible'`))
	raw, err := HAR([]byte(fmt.Sprintf(`{"log":{"entries":[{"request":{"url":"https://example.test/"},"response":{"content":{"mimeType":"application/javascript","encoding":"base64","text":%q}}}]}}`, body)), Policy{})
	if err != nil || strings.Contains(string(raw), body) {
		t.Fatalf("body not scrubbed: %s %v", raw, err)
	}
}

func TestSensitiveCamelCaseAndConcatenatedCredentialNames(t *testing.T) {
	for _, sensitive := range []string{
		"accPassword",
		"emailToken",
		"approverPasswd",
		"managerPassword",
		"userPassword",
		"eddauthenticationtoken",
		"_CONFIRMATIONTOKEN",
		"ns:userPassword",
		"wsse:Password",
	} {
		if !SensitiveName(sensitive) && !SensitiveXMLName(sensitive) {
			t.Errorf("expected %q to be classified as sensitive", sensitive)
		}
	}

	for _, benign := range []string{
		"tokenURL",
		"apiTokenCount",
		"businessTokenLabel",
		"clientSecretRef",
		"soap_operation",
		"maxTokens",
		"completionTokens",
		"secretRequired",
	} {
		if SensitiveName(benign) || SensitiveXMLName(benign) {
			t.Errorf("expected descriptor %q not to be classified as sensitive", benign)
		}
	}

	jsonBody := `{"accPassword":"synth-acc-pwd-101","emailToken":"synth-email-tok-102","approverPasswd":"synth-approver-103","eddauthenticationtoken":"synth-edd-104","tokenURL":"https://auth.example.test/oauth/token","apiTokenCount":3,"businessTokenLabel":"OrderFlow"}`
	scrubbedJSON := BodyText(jsonBody)
	for _, secret := range []string{"synth-acc-pwd-101", "synth-email-tok-102", "synth-approver-103", "synth-edd-104"} {
		if strings.Contains(scrubbedJSON, secret) {
			t.Fatalf("scrubbed JSON leaked %q: %s", secret, scrubbedJSON)
		}
	}
	for _, keep := range []string{"https://auth.example.test/oauth/token", "OrderFlow"} {
		if !strings.Contains(scrubbedJSON, keep) {
			t.Fatalf("scrubbed JSON dropped benign descriptor value %q: %s", keep, scrubbedJSON)
		}
	}

	soapBody := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="urn:example" xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"><soapenv:Header><wsse:Security><wsse:UsernameToken><wsse:Username>operator</wsse:Username><wsse:Password>synth-wsse-pwd-201</wsse:Password></wsse:UsernameToken></wsse:Security></soapenv:Header><soapenv:Body><ns:Login><ns:userID>operator</ns:userID><ns:userPassword>synth-soap-pwd-202</ns:userPassword></ns:Login></soapenv:Body></soapenv:Envelope>`
	scrubbedSOAP := BodyText(soapBody)
	for _, secret := range []string{"synth-wsse-pwd-201", "synth-soap-pwd-202"} {
		if strings.Contains(scrubbedSOAP, secret) {
			t.Fatalf("scrubbed SOAP leaked %q: %s", secret, scrubbedSOAP)
		}
	}
	if !strings.Contains(scrubbedSOAP, "operator") {
		t.Fatalf("scrubbed SOAP dropped non-secret username: %s", scrubbedSOAP)
	}

	formBody := `username=operator&managerPassword=synth-mgr-pwd-301&_CONFIRMATIONTOKEN=synth-confirm-302&soap_operation=CreateOrder`
	scrubbedForm := BodyText(formBody)
	for _, secret := range []string{"synth-mgr-pwd-301", "synth-confirm-302"} {
		if strings.Contains(scrubbedForm, secret) {
			t.Fatalf("scrubbed form leaked %q: %s", secret, scrubbedForm)
		}
	}
	if !strings.Contains(scrubbedForm, "CreateOrder") {
		t.Fatalf("scrubbed form dropped benign field: %s", scrubbedForm)
	}

	jsBody := `const accPassword = 'synth-js-pwd-401'; const tokenURL = 'https://auth.example.test/token';`
	scrubbedJS := BodyText(jsBody)
	if strings.Contains(scrubbedJS, "synth-js-pwd-401") || !strings.Contains(scrubbedJS, "https://auth.example.test/token") {
		t.Fatalf("scrubbed JS assignment unexpected: %s", scrubbedJS)
	}

	hdrs := Headers(map[string]string{
		"X-AccPassword":  "pin9",
		"accPassword":    "pin8",
		"ApproverPasswd": "pin7",
		"Cookie":         "locale=en-US",
		"Content-Type":   "application/json",
	}, Policy{KeepCookies: true})
	for _, key := range []string{"X-AccPassword", "accPassword", "ApproverPasswd"} {
		if hdrs[key] != Redacted {
			t.Fatalf("Headers did not redact %q: %v", key, hdrs)
		}
	}
	if hdrs["Cookie"] != "locale=en-US" || hdrs["Content-Type"] != "application/json" {
		t.Fatalf("Headers altered Cookie (KeepCookies=true) or Content-Type: %v", hdrs)
	}

	harInput := `{"log":{"entries":[{"request":{"url":"https://example.test/","headers":[{"name":"X-AccPassword","value":"pin9"}],"cookies":[{"name":"locale","value":"en-US"}]},"response":{"content":{"mimeType":"text/plain","text":"ok"}}}]}}`
	harOut, err := HAR([]byte(harInput), Policy{KeepCookies: true})
	if err != nil {
		t.Fatalf("HAR scrub failed: %v", err)
	}
	if strings.Contains(string(harOut), "pin9") || !strings.Contains(string(harOut), "en-US") {
		t.Fatalf("HAR camelCase password header / KeepCookies mismatch: %s", harOut)
	}
}

func TestBodyTextQuoteDesyncJSONEscapedAndMultilineBounds(t *testing.T) {
	// 1. Unmatched quote in preceding HTML attribute must not desynchronize
	// subsequent inline JS credential assignment.
	htmlDesync := `<div data-hint="e.g. role='admin"><script>const accPassword = 'short-pin-123';</script>`
	outDesync := BodyText(htmlDesync)
	if strings.Contains(outDesync, "short-pin-123") || !strings.Contains(outDesync, "role='admin") {
		t.Fatalf("HTML attribute quote desync leaked secret or altered hint: %s", outDesync)
	}

	// 2. JSON-encoded HTML/JS string scalar with escaped inner quotes must redact.
	jsonEscaped := `{"html":"<script>const accPassword = \"short-pin-123\";</script>","safe":"visible"}`
	outJSONEscaped := BodyText(jsonEscaped)
	if strings.Contains(outJSONEscaped, "short-pin-123") || !strings.Contains(outJSONEscaped, `"safe":"visible"`) {
		t.Fatalf("JSON-escaped inline JS assignment not redacted cleanly: %s", outJSONEscaped)
	}

	// 3. Unclosed sensitive quote on line 1 must not span across newline to eat line 2.
	multiline := "<div title=\"token: 'unclosed\">\n<p>Safe. It's ok</p>"
	outMultiline := BodyText(multiline)
	if !strings.Contains(outMultiline, "Safe. It's ok") {
		t.Fatalf("unclosed quote spanned newline and over-redacted subsequent prose: %s", outMultiline)
	}

	// 4. Closed non-sensitive string containing "password=" must not treat its closing
	// quote as an opening sensitive assignment quote.
	closedSafe := `const note = 'password='; const other = 'safe';`
	outClosedSafe := BodyText(closedSafe)
	if !strings.Contains(outClosedSafe, `'safe'`) {
		t.Fatalf("closed non-sensitive string falsely redacted subsequent assignment: %s", outClosedSafe)
	}

	// 5. Same-quote desynchronization on a single line (template literal, regex literal,
	// plain unmatched quote, leading non-token secret characters, and unmatched quotes both before and after).
	for _, sample := range []string{
		"const hint = `role=\"admin`; const accPassword = \"short-pin-123\";",
		`const re = /role="/; const accPassword = "short-pin-123";`,
		`error: filter="open; accPassword="short-pin-123"`,
		`error: filter="open; accPassword="short-pin-123"; tail="also_open`,
		`error: filter="open; accPassword="!short-pin-123"; tail="also_open`,
		`error: filter="accPassword=" !short-pin-123"; tail="also_open`,
		`error: filter="open; accPassword=";short-pin-123"; tail="also_open`,
		`error: filter="open; accPassword=",short-pin-123"; tail="also_open`,
		`error: filter="open; accPassword=" ;short-pin-123"; tail="also_open`,
	} {
		out := BodyText(sample)
		if strings.Contains(out, "short-pin-123") {
			t.Fatalf("same-quote desync leaked secret on %q: %s", sample, out)
		}
	}

	// 6. Closed non-sensitive strings ending with "; password=" followed by ';' or ','
	// must keep subsequent benign assignments intact (including "" and ";" values).
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`const note = "prefix; password="; const other = "safe";`, `"safe"`},
		{`const note = "accPassword="; const other = "";`, `other = ""`},
		{`const note = "accPassword="; const other = ";";`, `other = ";"`},
		{`{"note": "prefix; password=", "other": "safe"}`, `"safe"`},
	} {
		out := BodyText(tc.input)
		if !strings.Contains(out, tc.want) {
			t.Fatalf("closed non-sensitive literal over-redacted subsequent value on %q: %s", tc.input, out)
		}
	}

	// 7. Multi-layer (doubly JSON-encoded) strings, unicode-escaped \u003d / \u003a,
	// RFC 8259 escaped forward slashes (\/), and UTF-16 surrogate pairs (\uD83D\uDE00)
	// must recursively redact inner JS/JSON assignments.
	for _, nestedJSON := range []string{
		`{"outer":"{\"html\":\"<script>const accPassword = \\\"short-pin-123\\\";</script>\",\"safe\":\"visible\"}"}`,
		`{"outer":"{\"accPassword\":\"short-pin-123\",\"safe\":\"visible\"}"}`,
		`{"html":"\u003cscript\u003econst accPassword \u003d \"short-pin-123\"; const safe \u003d \"visible\";\u003c/script\u003e"}`,
		`{"payload":"{\"accPassword\"\u003a\"short-pin-123\",\"safe\"\u003a\"visible\"}"}`,
		`{"html":"<script>const loginUrl = \"https:\/\/login.example.test\/auth\"; const accPassword = \"short-pin-123\"; const safe = \"visible\";<\/script>"}`,
		`{"body":"{\"note\":\"emoji \uD83D\uDE00\",\"accPassword\":\"short-pin-123\",\"safe\":\"visible\"}"}`,
	} {
		outNested := BodyText(nestedJSON)
		if strings.Contains(outNested, "short-pin-123") || !strings.Contains(outNested, "visible") {
			t.Fatalf("JSON-embedded string assignment redaction failed on %q: %s", nestedJSON, outNested)
		}
	}
}

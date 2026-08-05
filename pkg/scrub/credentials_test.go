package scrub

import (
	"net/url"
	"strings"
	"testing"
)

func TestRedactCredentialHeaders(t *testing.T) {
	headers := map[string]string{
		"Authorization": "Basic dXNlcjpwYXNz",
		"X-Api-Key":     "secret-key-123",
		"X-CSRF-Token":  "csrf-keep-me",
		"Cookie":        "SESSIONID=keep-this-session",
	}
	out := RedactCredentialHeaders(headers)

	if out["Authorization"] != Redacted {
		t.Errorf("Basic auth should be redacted, got %q", out["Authorization"])
	}
	if out["X-Api-Key"] != Redacted {
		t.Errorf("api key should be redacted, got %q", out["X-Api-Key"])
	}
	// Correlation candidates must survive.
	if out["X-CSRF-Token"] != "csrf-keep-me" {
		t.Errorf("CSRF token must be preserved, got %q", out["X-CSRF-Token"])
	}
	if out["Cookie"] != "SESSIONID=keep-this-session" {
		t.Errorf("session cookie must be preserved, got %q", out["Cookie"])
	}
	// Input must not be mutated.
	if headers["Authorization"] != "Basic dXNlcjpwYXNz" {
		t.Errorf("input header map was mutated")
	}
}

func TestRedactCredentialHeadersKeepsBearer(t *testing.T) {
	out := RedactCredentialHeaders(map[string]string{"Authorization": "Bearer dynamic-token-abc"})
	if out["Authorization"] != "Bearer dynamic-token-abc" {
		t.Errorf("bearer token (possibly dynamic) must be preserved, got %q", out["Authorization"])
	}
}

func TestRedactCredentialBodyJSON(t *testing.T) {
	body := `{"username":"alice","password":"hunter2","csrf":"tok-keep","nested":{"client_secret":"s3cr3t"}}`
	out := RedactCredentialBody(body)

	if strings.Contains(out, "hunter2") {
		t.Errorf("password leaked: %q", out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("nested client_secret leaked: %q", out)
	}
	// Correlation candidate + non-secret fields preserved.
	if !strings.Contains(out, "tok-keep") {
		t.Errorf("CSRF token must be preserved: %q", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("username must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyForm(t *testing.T) {
	out := RedactCredentialBody("user=alice&clientSecret=hunter2&__RequestVerificationToken=csrf-keep")
	if strings.Contains(out, "hunter2") {
		t.Errorf("form password leaked: %q", out)
	}
	if !strings.Contains(out, "csrf-keep") {
		t.Errorf("CSRF token must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyXML(t *testing.T) {
	body := `<soap:Envelope><soap:Body><login apiKey="static-key" data-secret="tracking"><password>hunter2</password><csrf>csrf-keep</csrf><sessionId>sid-keep</sessionId></login></soap:Body></soap:Envelope>`
	out := RedactCredentialBody(body)

	for _, secret := range []string{"hunter2", "static-key"} {
		if strings.Contains(out, secret) {
			t.Errorf("XML credential %q leaked: %q", secret, out)
		}
	}
	for _, dynamic := range []string{"csrf-keep", "sid-keep", `data-secret="tracking"`} {
		if !strings.Contains(out, dynamic) {
			t.Errorf("correlation candidate %q must be preserved: %q", dynamic, out)
		}
	}
}

func TestRedactCredentialBodyXMLSharedAliases(t *testing.T) {
	for _, name := range []string{"pwd", "pass", "passcode", "passphrase"} {
		t.Run(name, func(t *testing.T) {
			body := `<root><auth:` + name + `>element-value</auth:` + name + `>` +
				`<entry auth:` + name + `="double-value"/>` +
				`<entry auth:` + name + `='single-value'/></root>`
			out := RedactCredentialBody(body)
			for _, expected := range []string{
				`<auth:` + name + `>` + Redacted + `</auth:` + name + `>`,
				`auth:` + name + `="` + Redacted + `"`,
				`auth:` + name + `='` + Redacted + `'`,
			} {
				if !strings.Contains(out, expected) {
					t.Errorf("RedactCredentialBody did not redact %q in %q: %s", expected, name, out)
				}
			}
			for _, secret := range []string{"element-value", "double-value", "single-value"} {
				if strings.Contains(out, secret) {
					t.Errorf("RedactCredentialBody leaked %q for XML alias %q: %s", secret, name, out)
				}
			}
		})
	}
}

func TestRedactCredentialBodyXMLUsesStaticFieldSemantics(t *testing.T) {
	const namespace = "https://schemas.example.test/security/passphrase"
	body := `<credentials xmlns:passphrase="` + namespace + `">` +
		`<auth:passphrase>phrase-value</auth:passphrase>` +
		`<auth:passcode>code-value</auth:passcode>` +
		`<csrf>csrf-keep</csrf></credentials>`
	out := RedactCredentialBody(body)

	for _, secret := range []string{"phrase-value", "code-value"} {
		if strings.Contains(out, secret) {
			t.Errorf("static XML credential %q leaked: %s", secret, out)
		}
	}
	for _, expected := range []string{
		`xmlns:passphrase="` + namespace + `"`,
		`<credentials`,
		`<csrf>csrf-keep</csrf>`,
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("selective XML redaction lost %q: %s", expected, out)
		}
	}
}

func TestRedactCredentialBodyXMLPreservesNonStaticCredentialContainers(t *testing.T) {
	input := `<root xmlns:credential="urn:example:credential">` +
		`<credential>correlation-container</credential>` +
		`<credentials value="correlation-map"/>` +
		`<csrf>dynamic-token</csrf></root>`
	got := RedactCredentialBody(input)
	if got != input {
		t.Fatalf("selective XML scrubber changed non-static correlation fields:\n got: %s\nwant: %s", got, input)
	}
}

func TestRedactCredentialBodyFailsClosedForMalformedUnquotedXMLCredentialAttributes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "spaces around equals",
			input: `<root password = short-secret note=visible>safe text</root>`,
			want:  `<root password = ` + Redacted + ` note=visible>safe text</root>`,
		},
		{
			name:  "space before equals",
			input: `<root password =short-secret note=visible/>`,
			want:  `<root password =` + Redacted + ` note=visible/>`,
		},
		{
			name:  "space after equals",
			input: `<root password= short-secret note=visible/>`,
			want:  `<root password= ` + Redacted + ` note=visible/>`,
		},
		{
			name:  "no spaces and self close",
			input: `<root password=short-secret/>`,
			want:  `<root password=` + Redacted + `/>`,
		},
		{
			name:  "truncated",
			input: `<root password = short-secret`,
			want:  `<root password = ` + Redacted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RedactCredentialBody(test.input); got != test.want {
				t.Fatalf("RedactCredentialBody(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	for _, safe := range []string{
		`<root note = short-safe text=visible/>`,
		`<root note="password=short-safe value" text=visible/>`,
		`<root note = short-safe`,
	} {
		if got := RedactCredentialBody(safe); got != safe {
			t.Fatalf("nonsensitive malformed XML changed:\n got: %q\nwant: %q", got, safe)
		}
	}
}

func TestRedactCredentialBodySensitiveSelfClosingXMLElementRedactsAttributePayload(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: `<password value="short-secret"/>`,
			want:  `<password value="` + Redacted + `"/>`,
		},
		{
			input: `<root><auth:password xmlns:auth="urn:example:auth" value='short-secret' hint=short-hint/></root>`,
			want:  `<root><auth:password xmlns:auth="urn:example:auth" value='` + Redacted + `' hint=` + Redacted + `/></root>`,
		},
		{
			input: `<password meta:value="short-secret"/>`,
			want:  `<password meta:value="` + Redacted + `"/>`,
		},
	}
	for _, test := range tests {
		if got := RedactCredentialBody(test.input); got != test.want {
			t.Fatalf("RedactCredentialBody(%q) = %q, want %q", test.input, got, test.want)
		}
	}

	safe := `<root><note value="short-safe" hint=short-hint/></root>`
	if got := RedactCredentialBody(safe); got != safe {
		t.Fatalf("safe self-closing element changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestRedactCredentialBodySensitiveXMLRedactsNestedRepresentations(t *testing.T) {
	for _, input := range []string{
		`<password><![CDATA[short-cdata]]></password>`,
		`<password><!-- short-comment --></password>`,
		`<password><nested>short-nested</nested></password>`,
	} {
		if got := RedactCredentialBody(input); got != `<password>`+Redacted+`</password>` {
			t.Fatalf("RedactCredentialBody(%q) = %q", input, got)
		}
	}
}

func TestRedactCredentialBodyFailsClosedForTruncatedStaticCredentials(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		secret string
	}{
		{
			name:   "unterminated JSON string",
			input:  `{"credentials[client_secret]":"short-json-secret`,
			secret: "short-json-secret",
		},
		{
			name:   "unterminated XML element",
			input:  `<root><password>short-xml-secret`,
			secret: "short-xml-secret",
		},
		{
			name:   "unterminated JSON credential object",
			input:  `{"password":{"part":"short-json-object-secret`,
			secret: "short-json-object-secret",
		},
		{
			name:   "unterminated double-quoted XML credential attribute",
			input:  `<root password="short-xml-attribute-secret`,
			secret: "short-xml-attribute-secret",
		},
		{
			name:   "unterminated single-quoted XML credential attribute",
			input:  `<root passphrase='short-xml-single-secret`,
			secret: "short-xml-single-secret",
		},
		{
			name:   "truncated sensitive opening element payload",
			input:  `<password short-opening-secret`,
			secret: "short-opening-secret",
		},
		{
			name:   "truncated namespaced sensitive opening element payload",
			input:  `<auth:password short-opening-secret`,
			secret: "short-opening-secret",
		},
		{
			name: "multipart final part at EOF",
			input: "--b\r\nContent-Disposition: form-data; name=passphrase\r\n" +
				"X-Part-Trace: retained\r\n\r\nshort-multipart-secret",
			secret: "short-multipart-secret",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RedactCredentialBody(test.input)
			if strings.Contains(got, test.secret) || !strings.Contains(got, Redacted) {
				t.Fatalf("truncated static credential leaked: %q", got)
			}
		})
	}

	for _, safe := range []string{
		`{"credentials[session_id]":"dynamic-session`,
		`<root><csrf>dynamic-session`,
		`<root csrf="dynamic-session`,
		`<root csrf='dynamic-session`,
		"--b\r\nContent-Disposition: form-data; name=csrf\r\n\r\ndynamic-session",
	} {
		if got := RedactCredentialBody(safe); got != safe {
			t.Fatalf("truncated dynamic correlation data changed:\n got: %q\nwant: %q", got, safe)
		}
	}
}

func TestRedactCredentialBodyMultipart(t *testing.T) {
	body := "--b\r\n" +
		"Content-Disposition: form-data; name=\"username\"\r\n\r\nalice\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"password\"\r\n\r\nhunter2\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"csrf\"\r\n\r\ncsrf-keep\r\n" +
		"--b--"

	out := RedactCredentialBody(body)
	if strings.Contains(out, "hunter2") {
		t.Errorf("multipart credential leaked: %q", out)
	}
	if !strings.Contains(out, "alice") || !strings.Contains(out, "csrf-keep") {
		t.Errorf("non-secret multipart fields must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyMultipartUsesStaticFieldSemantics(t *testing.T) {
	for _, name := range []string{"passphrase", "passcode"} {
		t.Run(name, func(t *testing.T) {
			body := "--b\r\n" +
				"Content-Disposition: form-data; name=\"" + name + "\"\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"static-value\r\n" +
				"--b\r\n" +
				"Content-Disposition: form-data; name=\"csrf\"\r\n\r\n" +
				"csrf-keep\r\n" +
				"--b--"
			out := RedactCredentialBody(body)
			if strings.Contains(out, "static-value") || !strings.Contains(out, Redacted) {
				t.Errorf("multipart %s was not redacted: %s", name, out)
			}
			if !strings.Contains(out, "csrf-keep") {
				t.Errorf("multipart dynamic field was not preserved: %s", out)
			}
		})
	}
}

func TestRedactCredentialBodyMultipartUsesStaticFieldVocabulary(t *testing.T) {
	body := "--b\r\n" +
		"Content-Disposition: form-data; name=\"passphrase\"\r\n\r\nphrase-secret\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"passcode\"\r\n\r\ncode-secret\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"credentials\"\r\n\r\ncorrelation-container\r\n" +
		"--b--"
	got := RedactCredentialBody(body)
	for _, secret := range []string{"phrase-secret", "code-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("multipart selective scrubber leaked %q: %s", secret, got)
		}
	}
	if strings.Count(got, Redacted) != 2 || !strings.Contains(got, "correlation-container") {
		t.Fatalf("multipart selective scrubber did not follow StaticField vocabulary: %s", got)
	}
}

func TestRedactCredentialBodyMultipartSupportsUnquotedStructuredNamesAndArbitraryHeaders(t *testing.T) {
	body := "--b\r\n" +
		"Content-Disposition: form-data; name=passphrase\r\n" +
		"X-Part-Trace: keep-header\r\n\r\n" +
		"short-secret\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"credentials[client_secret]\"\r\n\r\n" +
		"structured-secret\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"credentials[session_id]\"\r\n" +
		"X-Part-Trace: keep-dynamic\r\n\r\n" +
		"dynamic-session\r\n--b--"
	got := RedactCredentialBody(body)
	if strings.Contains(got, "short-secret") || strings.Contains(got, "structured-secret") ||
		strings.Count(got, Redacted) != 2 {
		t.Fatalf("structured static multipart field leaked: %s", got)
	}
	for _, expected := range []string{"X-Part-Trace: keep-header", "dynamic-session", "X-Part-Trace: keep-dynamic"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("multipart scrubber lost %q: %s", expected, got)
		}
	}
}

func TestRedactCredentialBodyMultipartUsesExactBoundaryAndFailsClosedForMalformedName(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name: "boundary-like payload line",
			input: "--b\r\nContent-Disposition: form-data; name=password\r\n\r\n" +
				"prefix\r\n--notboundary\r\nshort-secret\r\n--b--\r\n",
		},
		{
			name: "truncated quoted name",
			input: "--b\r\nContent-Disposition: form-data; name=\"password\r\n" +
				"X-Part-Trace: retained\r\n\r\nshort-secret\r\n--b--\r\n",
		},
		{
			name:  "truncated quoted name at EOF",
			input: "--b\r\nContent-Disposition: form-data; name=\"password\r\n\r\nshort-secret",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RedactCredentialBody(test.input)
			if strings.Contains(got, "short-secret") ||
				strings.Contains(got, "--notboundary") ||
				!strings.Contains(got, Redacted) {
				t.Fatalf("multipart credential leaked: %q", got)
			}
			if strings.Contains(test.input, "--b--") && !strings.Contains(got, "\r\n--b--") {
				t.Fatalf("declared multipart boundary was not preserved: %q", got)
			}
			if strings.Contains(test.input, "X-Part-Trace") &&
				!strings.Contains(got, "X-Part-Trace: retained") {
				t.Fatalf("multipart header evidence was not preserved: %q", got)
			}
		})
	}

	safe := "--b\r\nContent-Disposition: form-data; name=\"note\r\n\r\nshort-safe\r\n--b--\r\n"
	if got := RedactCredentialBody(safe); got != safe {
		t.Fatalf("malformed nonsensitive multipart changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestRedactCredentialBodyMalformedNestedURLSchemeAcrossBodyShapes(t *testing.T) {
	const nested = `https://outer.test/?next=h%ZZttps://inner.test/?pwd=short-secret`
	tests := []struct {
		name  string
		input string
		keep  string
	}{
		{
			name:  "raw URL",
			input: nested,
			keep:  `h%ZZttps://inner.test/`,
		},
		{
			name:  "JSON string",
			input: `{"redirect":"` + nested + `","csrf":"dynamic-session"}`,
			keep:  `"csrf":"dynamic-session"`,
		},
		{
			name:  "XML text",
			input: `<root><redirect>` + nested + `</redirect><csrf>dynamic-session</csrf></root>`,
			keep:  `<csrf>dynamic-session</csrf>`,
		},
		{
			name:  "form value",
			input: `redirect=` + nested + `&csrf=dynamic-session`,
			keep:  `csrf=dynamic-session`,
		},
		{
			name: "multipart value",
			input: "--b\r\nContent-Disposition: form-data; name=redirect\r\n\r\n" +
				nested + "\r\n--b\r\n" +
				"Content-Disposition: form-data; name=csrf\r\n\r\n" +
				"dynamic-session\r\n--b--",
			keep: `dynamic-session`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RedactCredentialBody(test.input)
			if strings.Contains(got, "short-secret") ||
				!strings.Contains(got, `pwd=`+Redacted) {
				t.Fatalf("nested static query credential leaked: %s", got)
			}
			if !strings.Contains(got, test.keep) {
				t.Fatalf("selective scrubber lost correlation/safe evidence %q: %s", test.keep, got)
			}
		})
	}

	const safe = `https://outer.test/?next=h%ZZttps://inner.test/?page=short-safe`
	if got := RedactCredentialBody(safe); got != safe {
		t.Fatalf("malformed nonsensitive nested query changed:\n got: %s\nwant: %s", got, safe)
	}
}

func TestRedactCredentialBodyMalformedJSONUsesStaticFieldVocabulary(t *testing.T) {
	input := `{"credentials[client_secret]":"short-secret","credentials[session_id]":"dynamic-session",`
	got := RedactCredentialBody(input)
	if strings.Contains(got, "short-secret") ||
		!strings.Contains(got, `"credentials[client_secret]":"`+Redacted+`"`) {
		t.Fatalf("malformed JSON static credential leaked: %s", got)
	}
	if !strings.Contains(got, `"credentials[session_id]":"dynamic-session"`) {
		t.Fatalf("malformed JSON dynamic correlation field was changed: %s", got)
	}
}

func TestRedactCredentialBodyMalformedJSONBareSensitiveScalarsFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "EOF",
			input: `{"password":short-secret`,
			want:  `{"password":` + Redacted,
		},
		{
			name:  "whitespace and object delimiter",
			input: `{"password" :  short-secret   }`,
			want:  `{"password" :  ` + Redacted + `   }`,
		},
		{
			name:  "comma delimiter",
			input: `{"password":short-secret,"note":"safe"}`,
			want:  `{"password":` + Redacted + `,"note":"safe"}`,
		},
		{
			name:  "valid scalar prefix cannot leak suffix",
			input: `{"password":123abc,"note":"safe"}`,
			want:  `{"password":` + Redacted + `,"note":"safe"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RedactCredentialBody(test.input); got != test.want {
				t.Fatalf("RedactCredentialBody(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	safe := `{"note":short-safe,"status":"ok"}`
	if got := RedactCredentialBody(safe); got != safe {
		t.Fatalf("nonsensitive malformed JSON changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestRedactCredentialBodyPreservesLargeAdjacentJSONNumber(t *testing.T) {
	input := `{"credentials[client_secret]":"short-secret","request_id":9007199254740993}`
	got := RedactCredentialBody(input)
	if strings.Contains(got, "short-secret") || !strings.Contains(got, `"request_id":9007199254740993`) {
		t.Fatalf("selective JSON scrubber leaked a secret or corrupted the adjacent integer: %s", got)
	}
}

func TestRedactCredentialBodyFormUsesStructuredStaticFieldVocabulary(t *testing.T) {
	got := RedactCredentialBody("credentials%5Bclient_secret%5D=short-secret&credentials%5Bsession_id%5D=dynamic-session")
	values, err := url.ParseQuery(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "short-secret") || values.Get("credentials[client_secret]") != Redacted {
		t.Fatalf("structured form credential leaked: %s", got)
	}
	if !strings.Contains(got, "dynamic-session") {
		t.Fatalf("structured dynamic form field was changed: %s", got)
	}
}

func TestRedactCredentialBodyFormPreservesWireRepresentation(t *testing.T) {
	input := "z=first+value&password=a+b%20c&password=[REDACTED]&password=[REDACTED]suffix&safe=%2fkeep&client%5fsecret=tiny&z=last"
	want := "z=first+value&password=[REDACTED]&password=[REDACTED]&password=[REDACTED]&safe=%2fkeep&client%5fsecret=[REDACTED]&z=last"
	if got := RedactCredentialBody(input); got != want {
		t.Fatalf("surgical form redaction changed ordering or escaping:\n got: %q\nwant: %q", got, want)
	}
}

func TestRedactCredentialBodyLeavesUnknownBodiesAlone(t *testing.T) {
	xml := "<root><note>no credentials here</note></root>"
	if RedactCredentialBody(xml) != xml {
		t.Errorf("XML without credential fields should be returned unchanged")
	}
	if RedactCredentialBody("") != "" {
		t.Errorf("empty body should stay empty")
	}
}

package scrub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSensitiveHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		policy Policy
		want   bool
	}{
		{name: "authorization", header: "Authorization", want: true},
		{name: "case insensitive", header: "X-API-KEY", want: true},
		{name: "cookie scrubbed by default", header: "Cookie", want: true},
		{name: "set-cookie scrubbed by default", header: "Set-Cookie", want: true},
		{name: "cookie kept when policy allows", header: "Cookie", policy: Policy{KeepCookies: true}, want: false},
		{name: "regular header passes", header: "Content-Type", want: false},
		{name: "extra header honored", header: "X-Tenant-Secret", policy: Policy{ExtraHeaders: []string{"x-tenant-secret"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.SensitiveHeader(tt.header); got != tt.want {
				t.Errorf("SensitiveHeader(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	in := map[string]string{
		"Authorization": "Bearer abc123",
		"Cookie":        "session=xyz",
		"Content-Type":  "application/json",
	}
	out := Headers(in, Policy{})
	if out["Authorization"] != Redacted || out["Cookie"] != Redacted {
		t.Errorf("sensitive headers not redacted: %v", out)
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("regular header changed: %v", out)
	}
	if in["Authorization"] != "Bearer abc123" {
		t.Error("input map must not be mutated")
	}
}

func TestSensitiveNameNormalizedVocabulary(t *testing.T) {
	for _, name := range []string{
		"private_key", "client-secret", "client%5Fsecret", "sessionId",
		"csrf_token", "XSRF-TOKEN", "credentials[connection_string]",
		"AWS_ACCESS_KEY_ID", "SharedAccessKey", "shared_access_signature",
		"jms_settlement_token", "lock-token", "receipt_handle", "sid",
		"pwd", "pass", "passphrase", "credential", "credentials",
	} {
		if !SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"author", "authority", "client_id", "correlation_id", "soap_operation", "status"} {
		if SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = true, want false", name)
		}
	}
}

func TestValuePreservesXMLNamespaceURIsButRedactsNamespacedCredentials(t *testing.T) {
	input := `<root xmlns:token="https://schemas.example.test/security/token" auth:password="short"><!-- https://docs.example.test/a/0123456789abcdef0123456789abcdef --></root>`
	got := Value(input, Policy{})
	if !strings.Contains(got, `xmlns:token="https://schemas.example.test/security/token"`) {
		t.Fatalf("namespace declaration was corrupted: %s", got)
	}
	if !strings.Contains(got, `auth:password="`+Redacted+`"`) || strings.Contains(got, `password="short"`) {
		t.Fatalf("namespaced credential was not redacted: %s", got)
	}
	if !strings.Contains(got, "https://docs.example.test/a/0123456789abcdef0123456789abcdef") {
		t.Fatalf("safe comment URL was corrupted: %s", got)
	}
}

func TestValuePreservesURNNamespacesAndRedactsSingleQuotedCredentials(t *testing.T) {
	const namespace = `urn:example:AbCdEf0123456789GhIjKlMn0Z`
	input := `<root xmlns:token='` + namespace + `' auth:password='short'/>`
	got := Value(input, Policy{})
	if !strings.Contains(got, `xmlns:token='`+namespace+`'`) {
		t.Fatalf("URN namespace declaration was corrupted: %s", got)
	}
	if !strings.Contains(got, `auth:password='`+Redacted+`'`) || strings.Contains(got, `password='short'`) {
		t.Fatalf("single-quoted credential was not redacted: %s", got)
	}
}

func TestValueDoesNotTreatXMLNSNameSuffixAsNamespaceDeclaration(t *testing.T) {
	input := `<root xmlns:foo_xmlns="urn:foo" foo_xmlns:password="short"/>`
	got := Value(input, Policy{})
	if !strings.Contains(got, `xmlns:foo_xmlns="urn:foo"`) {
		t.Fatalf("real namespace declaration was corrupted: %s", got)
	}
	if !strings.Contains(got, `foo_xmlns:password="`+Redacted+`"`) || strings.Contains(got, `password="short"`) {
		t.Fatalf("credential prefix ending in xmlns bypassed redaction: %s", got)
	}
}

func TestValueFailsClosedForMalformedUnquotedXMLCredentialAttributes(t *testing.T) {
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
			if got := Value(test.input, Policy{}); got != test.want {
				t.Fatalf("Value(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	for _, safe := range []string{
		`<root note = short-safe text=visible/>`,
		`<root note="password=short-safe value" text=visible/>`,
		`<root note = short-safe`,
	} {
		if got := Value(safe, Policy{}); got != safe {
			t.Fatalf("nonsensitive malformed XML changed:\n got: %q\nwant: %q", got, safe)
		}
	}
}

func TestValueSensitiveSelfClosingXMLElementRedactsAttributePayload(t *testing.T) {
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
		if got := Value(test.input, Policy{}); got != test.want {
			t.Fatalf("Value(%q) = %q, want %q", test.input, got, test.want)
		}
	}

	safe := `<root><note value="short-safe" hint=short-hint/></root>`
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("safe self-closing element changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestValueRedactsSharedXMLCredentialAliases(t *testing.T) {
	for _, name := range []string{"pwd", "pass", "passcode", "passphrase", "credential", "credentials"} {
		t.Run(name, func(t *testing.T) {
			input := `<root><auth:` + name + `>element-value</auth:` + name + `>` +
				`<entry auth:` + name + `="double-value"/>` +
				`<entry auth:` + name + `='single-value'/></root>`
			got := Value(input, Policy{})
			for _, expected := range []string{
				`<auth:` + name + `>` + Redacted + `</auth:` + name + `>`,
				`auth:` + name + `="` + Redacted + `"`,
				`auth:` + name + `='` + Redacted + `'`,
			} {
				if !strings.Contains(got, expected) {
					t.Errorf("Value did not redact %q in %q: %s", expected, name, got)
				}
			}
			for _, secret := range []string{"element-value", "double-value", "single-value"} {
				if strings.Contains(got, secret) {
					t.Errorf("Value leaked %q for XML alias %q: %s", secret, name, got)
				}
			}
		})
	}
}

func TestValueMalformedJSONFallbackUsesSharedSensitiveNames(t *testing.T) {
	const safeURL = "https://safe.example/assets/AbCdEf0123456789GhIjKlMn0Z"
	input := `{"pwd":"pwd-value","shared_access_key":"key-value","endpoint":"` + safeURL + `","status":"ok",`
	got := Value(input, Policy{})

	for _, secret := range []string{"pwd-value", "key-value"} {
		if strings.Contains(got, secret) {
			t.Errorf("malformed JSON fallback leaked %q: %s", secret, got)
		}
	}
	for _, expected := range []string{
		`"pwd":"` + Redacted + `"`,
		`"shared_access_key":"` + Redacted + `"`,
		`"status":"ok"`,
		safeURL,
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("malformed JSON fallback lost %q: %s", expected, got)
		}
	}
}

func TestValueMalformedJSONBareSensitiveScalarsFailClosed(t *testing.T) {
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
			if got := Value(test.input, Policy{}); got != test.want {
				t.Fatalf("Value(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	safe := `{"note":short-safe,"status":"ok"}`
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("nonsensitive malformed JSON changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestValueXMLFallbackUsesSharedNamesWithoutCorruptingNamespaces(t *testing.T) {
	const namespace = "https://schemas.example.test/security/shared_access_key"
	const commentURL = "https://docs.example.test/assets/AbCdEf0123456789GhIjKlMn0Z"
	input := `<root xmlns:shared_access_key="` + namespace + `">` +
		`<!-- ` + commentURL + ` -->` +
		`<auth:pwd>pwd-value</auth:pwd>` +
		`<auth:shared_access_key>key-value</auth:shared_access_key>` +
		`<entry auth:shared_access_key="attribute-value"/>`
	got := Value(input, Policy{})

	for _, secret := range []string{"pwd-value", "key-value", "attribute-value"} {
		if strings.Contains(got, secret) {
			t.Errorf("XML fallback leaked %q: %s", secret, got)
		}
	}
	for _, expected := range []string{
		`xmlns:shared_access_key="` + namespace + `"`,
		commentURL,
		`<auth:pwd>` + Redacted + `</auth:pwd>`,
		`<auth:shared_access_key>` + Redacted + `</auth:shared_access_key>`,
		`auth:shared_access_key="` + Redacted + `"`,
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("XML fallback lost %q: %s", expected, got)
		}
	}
}

func TestValueSensitiveXMLElementsRedactAllInnerRepresentations(t *testing.T) {
	tests := []string{
		`<password><![CDATA[short-cdata]]></password>`,
		`<password><!-- short-comment --></password>`,
		`<password><nested>short-nested</nested></password>`,
	}
	for _, input := range tests {
		got := Value(input, Policy{})
		if got != `<password>`+Redacted+`</password>` {
			t.Fatalf("Value(%q) = %q, want complete sensitive-element redaction", input, got)
		}
	}
}

func TestValueFailsClosedForTruncatedCredentialStructures(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		secret    string
		wantPiece string
	}{
		{
			name:      "unterminated JSON string",
			input:     `{"password":"short-json-secret`,
			secret:    "short-json-secret",
			wantPiece: `"password":"` + Redacted,
		},
		{
			name:      "unterminated XML element",
			input:     `<root><password>short-xml-secret`,
			secret:    "short-xml-secret",
			wantPiece: `<password>` + Redacted,
		},
		{
			name:      "unterminated JSON credential object",
			input:     `{"password":{"part":"short-json-object-secret`,
			secret:    "short-json-object-secret",
			wantPiece: `"password":` + Redacted,
		},
		{
			name:      "unterminated double-quoted XML credential attribute",
			input:     `<root password="short-xml-attribute-secret`,
			secret:    "short-xml-attribute-secret",
			wantPiece: `password="` + Redacted,
		},
		{
			name:      "unterminated single-quoted XML credential attribute",
			input:     `<root passphrase='short-xml-single-secret`,
			secret:    "short-xml-single-secret",
			wantPiece: `passphrase='` + Redacted,
		},
		{
			name:      "XML ancestor recovery",
			input:     `<root><password>short-xml-secret</root>`,
			secret:    "short-xml-secret",
			wantPiece: `<password>` + Redacted + `</root>`,
		},
		{
			name:      "truncated sensitive opening element payload",
			input:     `<password short-opening-secret`,
			secret:    "short-opening-secret",
			wantPiece: `<password ` + Redacted,
		},
		{
			name:      "truncated namespaced sensitive opening element payload",
			input:     `<auth:password short-opening-secret`,
			secret:    "short-opening-secret",
			wantPiece: `<auth:password ` + Redacted,
		},
		{
			name: "multipart final part at EOF",
			input: "--b\r\nContent-Disposition: form-data; name=password\r\n" +
				"X-Part-Trace: retained\r\n\r\nshort-multipart-secret",
			secret:    "short-multipart-secret",
			wantPiece: "X-Part-Trace: retained\r\n\r\n" + Redacted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Value(test.input, Policy{})
			if strings.Contains(got, test.secret) || !strings.Contains(got, test.wantPiece) {
				t.Fatalf("truncated credential structure was not scrubbed:\n got: %q\nwant marker: %q", got, test.wantPiece)
			}
		})
	}

	for _, safe := range []string{
		`{"note":"short-safe-text`,
		`<root><note>short-safe-text`,
		`<root note="short-safe-text`,
		`<root note='short-safe-text`,
		"--b\r\nContent-Disposition: form-data; name=note\r\n\r\nshort-safe-text",
	} {
		if got := Value(safe, Policy{}); got != safe {
			t.Fatalf("safe truncated structure changed:\n got: %q\nwant: %q", got, safe)
		}
	}
}

func TestRedactXMLFieldsDeepUnmatchedClosesIsBounded(t *testing.T) {
	const depth = 12_000
	var input strings.Builder
	for index := 0; index < depth; index++ {
		fmt.Fprintf(&input, "<node%d>", index)
	}
	for index := 0; index < depth; index++ {
		input.WriteString("</unmatched>")
	}
	value := input.String()
	if got := redactXMLFields(value, func(string) bool { return false }); got != value {
		t.Fatal("deep safe malformed XML changed")
	}

	dense := strings.Repeat("<n/>", 250_000)
	if got := redactXMLFields(dense, func(string) bool { return false }); got != dense {
		t.Fatal("dense safe XML changed")
	}
}

func TestValueMultipartSupportsUnquotedNamesAndArbitraryPartHeaders(t *testing.T) {
	input := "--b\r\n" +
		"Content-Disposition: form-data; name=shared_access_key\r\n" +
		"X-Part-Trace: keep-header\r\n" +
		"Content-Transfer-Encoding: binary\r\n\r\n" +
		"short-secret\r\n--b--"
	got := Value(input, Policy{})
	if strings.Contains(got, "short-secret") ||
		!strings.Contains(got, "X-Part-Trace: keep-header") ||
		!strings.Contains(got, Redacted) {
		t.Fatalf("multipart fallback did not redact an unquoted field with arbitrary headers: %s", got)
	}
}

func TestValueMultipartUsesExactBoundaryAndFailsClosedForMalformedName(t *testing.T) {
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
			got := Value(test.input, Policy{})
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
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("malformed nonsensitive multipart changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestValueURLRedactsSemicolonSeparatedEncodedKeys(t *testing.T) {
	input := "https://safe.example/path?safe=x;client%5Fsecret=short#view=full;pwd=code"
	got := ValueURL(input, Policy{})
	for _, leaked := range []string{"client%5Fsecret=short", "pwd=code"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("ValueURL leaked %q: %s", leaked, got)
		}
	}
	for _, expected := range []string{
		"safe=x;client%5Fsecret=" + Redacted,
		"view=full;pwd=" + Redacted,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("ValueURL lost semicolon-delimited structure %q: %s", expected, got)
		}
	}

	embedded := Value("request failed for "+input+"; retrying", Policy{})
	if strings.Contains(embedded, "client%5Fsecret=short") || strings.Contains(embedded, "pwd=code") {
		t.Fatalf("embedded URL leaked semicolon-delimited credentials: %s", embedded)
	}
}

func TestValueURLRecursivelySanitizesEncodedNestedHTTPURLs(t *testing.T) {
	input := "https://outer.example.test/start?" +
		"redirect=https%3A%2F%2Finner.example.test%2Flogin%3Fpassword%3Dshort%26safe%3Dok" +
		"#next=https%3A%2F%2Finner.example.test%2Fcallback%3Fclient%255Fsecret%3Dcode"
	got := ValueURL(input, Policy{})

	for _, secret := range []string{"password%3Dshort", "client%255Fsecret%3Dcode"} {
		if strings.Contains(got, secret) {
			t.Fatalf("nested encoded URL leaked %q: %s", secret, got)
		}
	}
	// Only the credential changes; the placeholder stays literal, as in a
	// top-level parameter, so credential review finds it.
	for _, exactShape := range []string{
		"redirect=https%3A%2F%2Finner.example.test%2Flogin%3Fpassword%3D[REDACTED]%26safe%3Dok",
		"next=https%3A%2F%2Finner.example.test%2Fcallback%3Fclient%255Fsecret%3D[REDACTED]",
	} {
		if !strings.Contains(got, exactShape) {
			t.Fatalf("nested URL encoding/shape changed; missing %q in %s", exactShape, got)
		}
	}
}

// A placeholder written two URLs deep stays literal and decodes to itself at
// every level.
func TestValueURLWritesDoublyNestedRedactionLiterally(t *testing.T) {
	inner := "https://inner.example.test/login?password=short-secret&safe=ok"
	middle := "https://middle.example.test/next?to=" + url.QueryEscape(inner)
	got := ValueURL("https://outer.example.test/?redirect="+url.QueryEscape(middle), Policy{})
	if strings.Contains(got, "short-secret") || strings.Count(got, Redacted) != 1 {
		t.Fatalf("doubly nested URL = %s, want one literal placeholder", got)
	}
	outer, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	decodedMiddle, err := url.Parse(outer.Query().Get("redirect"))
	if err != nil {
		t.Fatal(err)
	}
	if password := mustParseQuery(t, decodedMiddle.Query().Get("to")).Get("password"); password != Redacted {
		t.Fatalf("innermost password = %q, want the placeholder", password)
	}
}

func mustParseQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query()
}

func TestQueryEscapeRedaction(t *testing.T) {
	for redaction, want := range map[string]string{
		Redacted:                  Redacted,
		url.QueryEscape(Redacted): "%255BREDACTED%255D",
		"a b&c=d+e#f;g\x00\u00e9": "a%20b%26c%3Dd%2Be%23f%3Bg%00%C3%A9",
	} {
		if got := queryEscapeRedaction(redaction); got != want {
			t.Fatalf("queryEscapeRedaction(%q) = %q, want %q", redaction, got, want)
		}
		if decoded, err := url.QueryUnescape(queryEscapeRedaction(redaction)); err != nil || decoded != redaction {
			t.Fatalf("%q decodes to %q (%v)", redaction, decoded, err)
		}
	}
}

func TestValueURLNestedSanitizationDepthLimitFailsClosed(t *testing.T) {
	nested := "https://level4.example.test/?password=short"
	for level := 3; level >= 0; level-- {
		nested = "https://level" + strconv.Itoa(level) + ".example.test/?redirect=" + url.QueryEscape(nested)
	}
	got := ValueURL("https://outer.example.test/?redirect="+url.QueryEscape(nested), Policy{})
	if strings.Contains(got, "password") || strings.Contains(got, "short") || !strings.Contains(got, "REDACTED") {
		t.Fatalf("depth-limited nested URL did not fail closed: %s", got)
	}
}

func TestValueURLMalformedNestedHTTPURLFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "literal nested URL",
			input: "https://outer.example.test/?next=http://inner.example.test/?pwd=short-secret%ZZ",
		},
		{
			name:  "encoded nested URL",
			input: "https://outer.example.test/?next=https%3A%2F%2Finner.example.test%2F%3Fpwd%3Dshort-secret%ZZ",
		},
		{
			name:  "mixed encoded nested URL",
			input: "https://outer.example.test/?next=h%74tps:%2F%2Finner.example.test%2F%3Fpwd%3Dshort-secret%ZZ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ValueURL(test.input, Policy{})
			if strings.Contains(got, "short-secret") || !strings.Contains(got, "next=%5BREDACTED%5D") {
				t.Fatalf("ValueURL failed open for malformed nested URL:\n got: %s", got)
			}

			embedded := Value("request failed for "+test.input+" retrying", Policy{})
			if strings.Contains(embedded, "short-secret") || !strings.Contains(embedded, "next=%5BREDACTED%5D") {
				t.Fatalf("Value failed open for embedded malformed nested URL:\n got: %s", embedded)
			}
		})
	}
}

func TestMalformedNestedURLSchemeStillRedactsNestedSensitiveQuery(t *testing.T) {
	const input = `https://outer.test/?next=h%ZZttps://inner.test/?pwd=short-secret`
	for name, scrubbed := range map[string]string{
		"ValueURL": ValueURL(input, Policy{}),
		"Value":    Value(input, Policy{}),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(scrubbed, "short-secret") ||
				!strings.Contains(scrubbed, `pwd=`+Redacted) {
				t.Fatalf("%s leaked the malformed nested query credential: %s", name, scrubbed)
			}
			if !strings.Contains(scrubbed, `h%ZZttps://inner.test/`) {
				t.Fatalf("%s discarded safe malformed URL evidence: %s", name, scrubbed)
			}
		})
	}

	const safe = `https://outer.test/?next=h%ZZttps://inner.test/?page=short-safe`
	if got := ValueURL(safe, Policy{}); got != safe {
		t.Fatalf("ValueURL changed a malformed nonsensitive nested query:\n got: %s\nwant: %s", got, safe)
	}
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("Value changed a malformed nonsensitive nested query:\n got: %s\nwant: %s", got, safe)
	}
}

func TestValueProtectsJSONEscapedSafeHTTPURL(t *testing.T) {
	const safeURL = `https:\/\/cdn.example.test\/assets\/AbCdEf0123456789GhIjKlMn0Z`
	if !embeddedEscapedHTTPURL.MatchString(safeURL) {
		t.Fatalf("escaped URL detector did not match %q with %q", safeURL, embeddedEscapedHTTPURL)
	}
	input := `{"documentation":"` + safeURL + `","status":"ok"}`
	if got := Value(input, Policy{}); got != input {
		t.Fatalf("JSON-escaped safe URL changed:\n got: %s\nwant: %s", got, input)
	}

	credentialURL := `{"callback":"https:\/\/login.example.test\/?password=short&safe=ok"}`
	got := Value(credentialURL, Policy{})
	if strings.Contains(got, "password=short") ||
		!strings.Contains(got, `password=`+Redacted) ||
		!strings.Contains(got, `https:\/\/login.example.test\/`) ||
		!strings.Contains(got, `&safe=ok`) {
		t.Fatalf("JSON-escaped credential URL was not safely sanitized: %s", got)
	}
}

func TestValueURLRedactsCommonPasswordAliases(t *testing.T) {
	for _, name := range []string{"pwd", "pass", "passphrase", "credential", "credentials"} {
		got := ValueURL("https://example.test/login?"+name+"=short", Policy{})
		if strings.Contains(got, "=short") || !strings.Contains(got, name+"="+Redacted) {
			t.Errorf("%s alias leaked from URL: %s", name, got)
		}
	}
}

func TestURLTokenSensitive(t *testing.T) {
	if !URLTokenSensitive("AbCdEf0123456789GhIjKlMn0Z") {
		t.Fatal("credential-shaped URL token was not detected")
	}
	if URLTokenSensitive("page-2") {
		t.Fatal("ordinary URL value was classified as a credential")
	}
}

func TestVocabularyAppliesAcrossStructuredEncodings(t *testing.T) {
	secret := "short"
	values := []string{
		`{"client%5Fsecret":"short","safe":"ok"}`,
		"client%255Fsecret=short&safe=ok",
		`<root private_key="short"><csrf_token>short</csrf_token></root>`,
		"--b\r\nContent-Disposition: form-data; name=\"session%5Fid\"\r\n\r\nshort\r\n--b--",
	}
	for _, input := range values {
		if got := Value(input, Policy{}); strings.Contains(got, secret) || !strings.Contains(got, Redacted) {
			t.Errorf("Value(%q) = %q, want short secret redacted", input, got)
		}
	}
	if got := ValueURL("https://user:short@example.test/cb?client%5Fsecret=short", Policy{}); strings.Contains(got, "user:short") || strings.Contains(got, "=short") {
		t.Fatalf("ValueURL leaked userinfo/query secret: %q", got)
	}
	mapValue := StringMap(map[string]string{"jms_settlement_token": "x", "soap_operation": "Create"}, Policy{})
	if mapValue["jms_settlement_token"] != Redacted || mapValue["soap_operation"] != "Create" {
		t.Fatalf("StringMap = %#v", mapValue)
	}
	structured, _ := Structured(map[string]any{
		"credentials": map[string]any{"private_key": "tiny"},
		"events":      []any{map[string]string{"xsrf": "x", "status": "ok"}},
	}, Policy{}).(map[string]any)
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "tiny") || strings.Contains(string(encoded), `"xsrf":"x"`) {
		t.Fatalf("Structured leaked named secret: %s", encoded)
	}
}

func TestValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		policy Policy
		want   string
	}{
		{
			name:  "json token field",
			input: `{"access_token":"eyJhbGc.secret","user":"jason"}`,
			want:  `{"access_token":"` + Redacted + `","user":"jason"}`,
		},
		{
			name:  "json array and object secret fields",
			input: `{"clientSecret":["alpha","beta"],"nested":{"apiKey":{"raw":"key-123"}},"csrf":"csrf-secret","largeId":9007199254740993}`,
			want:  `{"clientSecret":["` + Redacted + `","` + Redacted + `"],"csrf":"` + Redacted + `","largeId":9007199254740993,"nested":{"apiKey":{"raw":"` + Redacted + `"}}}`,
		},
		{
			name:  "form password field",
			input: "username=jason&password=hunter2&kind=login",
			want:  "username=jason&password=" + Redacted + "&kind=login",
		},
		{
			name:  "query token",
			input: "https://x.example/cb?code=ok&token=abc123",
			want:  "https://x.example/cb?code=ok&token=" + Redacted,
		},
		{
			name:  "xml token element and attribute",
			input: `<soap><auth token="abc123" data-secret="tracking"><password>hunter2</password></auth></soap>`,
			want:  `<soap><auth token="` + Redacted + `" data-secret="` + Redacted + `">` + Redacted + `</auth></soap>`,
		},
		{
			name:  "multipart password part",
			input: "--b\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\nhunter2\r\n--b--",
			want:  "--b\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\n" + Redacted + "\r\n--b--",
		},
		{
			name:  "bare jwt",
			input: "Authorization failed for eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature",
			want:  "Authorization failed for " + Redacted,
		},
		{
			name:  "high entropy bare token",
			input: "token AbCdEfGh1234567890ijklMN should not persist",
			want:  "token " + Redacted + " should not persist",
		},
		{
			// A plain multi-label hostname structurally matches the JWT pattern
			// (three 8+-char dotted labels) but is not a secret: it must survive.
			name:  "multi-label hostname preserved",
			input: "connecting to dashboard.internal.megacorp now",
			want:  "connecting to dashboard.internal.megacorp now",
		},
		{
			name:   "custom pattern",
			input:  "ssn=123-45-6789 name=j",
			policy: Policy{ExtraPatterns: []Rule{{Pattern: `\d{3}-\d{2}-\d{4}`}}},
			want:   "ssn=" + Redacted + " name=j",
		},
		{
			name:   "custom replacement",
			input:  "card 4111111111111111",
			policy: Policy{ExtraPatterns: []Rule{{Pattern: `4\d{15}`, Replacement: "****"}}},
			want:   "card ****",
		},
		{
			name:   "invalid custom pattern skipped",
			input:  "plain text",
			policy: Policy{ExtraPatterns: []Rule{{Pattern: `([`}}},
			want:   "plain text",
		},
		{name: "empty value", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Value(tt.input, tt.policy); got != tt.want {
				t.Errorf("Value(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValueScrubsCredentialsInsideEmbeddedURLs(t *testing.T) {
	secret := "short-password"
	input := `request failed for https://user:` + secret + `@example.test/cb?api_key=` + secret + `; retrying`
	got := Value(input, Policy{})
	if strings.Contains(got, secret) || strings.Contains(got, "user:"+secret) {
		t.Fatalf("Value leaked embedded URL credentials: %q", got)
	}
	if !strings.Contains(got, "request failed for") || !strings.Contains(got, "retrying") {
		t.Fatalf("Value discarded surrounding diagnostic text: %q", got)
	}
}

func TestValuePreservesEmbeddedXMLURLsAndBareSecretDetection(t *testing.T) {
	input := `<!-- endpoint http://p100csg8c1.vci.att.com:18000/Services/ACME_ISBA_WEB.jws -->` +
		`<Envelope xmlns="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<callback>https://safe.example/a/AbCdEf0123456789GhIjKlMn0Z</callback>` +
		`<outside>AbCdEf0123456789GhIjKlMn0Z</outside></Envelope>`
	got := Value(input, Policy{})
	for _, safe := range []string{
		"http://p100csg8c1.vci.att.com:18000/Services/ACME_ISBA_WEB.jws",
		"http://schemas.xmlsoap.org/soap/envelope/",
		"http://www.w3.org/2001/XMLSchema-instance",
		"https://safe.example/a/AbCdEf0123456789GhIjKlMn0Z",
	} {
		if !strings.Contains(got, safe) {
			t.Errorf("Value dropped safe embedded URL %q: %s", safe, got)
		}
	}
	if !strings.Contains(got, "<outside>"+Redacted+"</outside>") {
		t.Fatalf("Value did not redact genuine bare secret outside URL: %s", got)
	}
}

func TestValueRedactsXMLNamedCredentialFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "named value attribute",
			input: `<root><entry name="pass&#x77;ord" value="short-xml-secret"/></root>`,
		},
		{
			name:  "named element text",
			input: `<root><property key="client_secret">short-xml-secret</property></root>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Value(test.input, Policy{})
			if strings.Contains(got, "short-xml-secret") || !strings.Contains(got, Redacted) {
				t.Fatalf("named XML credential leaked: %s", got)
			}
		})
	}

	const safe = `<root><entry name="nickname" value="short-safe"/></root>`
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("nonsensitive named XML field changed:\n got: %s\nwant: %s", got, safe)
	}
}

func TestValueEmbeddedURLPlaceholdersCannotCollide(t *testing.T) {
	input := `APEXURL0X0Z APEXURL0XX0Z ` +
		`https://user:password@example.test/path?token=secret ` +
		`https://safe.example/AbCdEf0123456789GhIjKlMn0Z`
	got := Value(input, Policy{})
	for _, literal := range []string{"APEXURL0X0Z", "APEXURL0XX0Z"} {
		if !strings.Contains(got, literal) {
			t.Errorf("Value consumed collision candidate %q: %s", literal, got)
		}
	}
	if strings.Contains(got, "user:password") || strings.Contains(got, "token=secret") {
		t.Fatalf("Value leaked embedded URL credentials: %s", got)
	}
	if !strings.Contains(got, "https://safe.example/AbCdEf0123456789GhIjKlMn0Z") {
		t.Fatalf("Value corrupted safe URL path: %s", got)
	}
}

func TestValueDoesNotExemptNonHTTPURLLikeTokens(t *testing.T) {
	const token = "AbCdEf0123456789GhIjKlMn0Z"
	got := Value("custom://example/"+token, Policy{})
	if strings.Contains(got, token) || !strings.Contains(got, Redacted) {
		t.Fatalf("non-HTTP URL-like token bypassed generic redaction: %q", got)
	}
}

func TestValueAppliesCustomRulesAcrossEmbeddedHTTPURLs(t *testing.T) {
	const input = "BEGIN https://safe.example/resource END"
	got := Value(input, Policy{ExtraPatterns: []Rule{{
		Pattern:     `BEGIN https://safe[.]example/resource END`,
		Replacement: "custom-redaction",
	}}})
	if got != "custom-redaction" {
		t.Fatalf("custom whole-context rule was bypassed by URL protection: %q", got)
	}
}

func TestHARBase64BodiesScrubTextAndPreserveBinary(t *testing.T) {
	textBody := base64.StdEncoding.EncodeToString([]byte(`{"access_token":"secret-token-value"}`))
	binaryBody := base64.StdEncoding.EncodeToString([]byte{0, 0xff, 0x10, 0x80})
	input := fmt.Sprintf(`{"log":{"entries":[
		{"request":{"url":"https://example.test","postData":{"text":%q,"encoding":"base64"}},"response":{"content":{"text":%q,"encoding":"base64"}}},
		{"request":{"url":"https://example.test"},"response":{"content":{"text":"access_token=visible-secret%%%%","encoding":"base64"}}}
	]}}`, textBody, binaryBody)

	scrubbed, err := HAR([]byte(input), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["log"].(map[string]any)["entries"].([]any)
	first := entries[0].(map[string]any)
	post := first["request"].(map[string]any)["postData"].(map[string]any)
	decodedText, err := base64.StdEncoding.DecodeString(post["text"].(string))
	if err != nil || strings.Contains(string(decodedText), "secret-token-value") || !strings.Contains(string(decodedText), Redacted) {
		t.Fatalf("textual base64 body = %q err=%v", decodedText, err)
	}
	content := first["response"].(map[string]any)["content"].(map[string]any)
	if content["text"] != binaryBody {
		t.Fatalf("binary base64 body changed: %q", content["text"])
	}
	second := entries[1].(map[string]any)
	malformed := second["response"].(map[string]any)["content"].(map[string]any)["text"].(string)
	decodedMarker, err := base64.StdEncoding.DecodeString(malformed)
	if err != nil || string(decodedMarker) != Redacted {
		t.Fatalf("malformed sensitive base64 replacement = %q decoded=%q err=%v", malformed, decodedMarker, err)
	}
	if len(second["_apexloadScrubDiagnostics"].([]any)) != 1 {
		t.Fatalf("missing malformed-body diagnostic: %#v", second)
	}
}

func TestHARKnownBinaryMIMEPreservesPrintableBase64Body(t *testing.T) {
	const printableBinary = "access_token=printable-binary-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(printableBinary))
	input := fmt.Sprintf(`{"log":{"entries":[{"request":{"url":"https://example.test"},"response":{"content":{"mimeType":"application/octet-stream","text":%q,"encoding":"base64"}}}]}}`, encoded)

	scrubbed, err := HAR([]byte(input), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["log"].(map[string]any)["entries"].([]any)
	content := entries[0].(map[string]any)["response"].(map[string]any)["content"].(map[string]any)
	if got := content["text"]; got != encoded {
		t.Fatalf("printable octet-stream body changed: got %q, want %q", got, encoded)
	}
}

func TestHARKnownBinaryMIMERedactsMalformedSensitiveBase64(t *testing.T) {
	const malformed = "access_token=visible-secret%%%%"
	input := fmt.Sprintf(`{"log":{"entries":[{"request":{"url":"https://example.test"},"response":{"content":{"mimeType":"application/octet-stream","text":%q,"encoding":"base64"}}}]}}`, malformed)

	scrubbed, err := HAR([]byte(input), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["log"].(map[string]any)["entries"].([]any)
	entry := entries[0].(map[string]any)
	content := entry["response"].(map[string]any)["content"].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(content["text"].(string))
	if err != nil || string(decoded) != Redacted {
		t.Fatalf("malformed sensitive binary MIME body = %q decoded=%q err=%v", content["text"], decoded, err)
	}
	if diagnostics, ok := entry["_apexloadScrubDiagnostics"].([]any); !ok || len(diagnostics) != 1 {
		t.Fatalf("missing malformed-body diagnostic: %#v", entry)
	}
}

func TestHARKnownTextMIMEDoesNotDependOnPrintableHeuristic(t *testing.T) {
	body := []byte("access_token=text-secret\x00\x01\x02\x03\x04")
	encoded := base64.StdEncoding.EncodeToString(body)
	input := fmt.Sprintf(`{"log":{"entries":[{"request":{"url":"https://example.test","postData":{"mimeType":"text/plain","text":%q,"encoding":"base64"}},"response":{}}]}}`, encoded)

	scrubbed, err := HAR([]byte(input), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["log"].(map[string]any)["entries"].([]any)
	postData := entries[0].(map[string]any)["request"].(map[string]any)["postData"].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(postData["text"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(decoded), "text-secret") || !strings.Contains(string(decoded), Redacted) {
		t.Fatalf("known textual MIME body was not scrubbed: %q", decoded)
	}
}

// TestHARPreservesURLHostAndHashedPaths locks in that scrubbing a HAR does not
// corrupt request URLs: multi-label hosts and content-hashed asset paths (both
// JWT/high-entropy look-alikes) must survive, while sensitive query params are
// still redacted. Regression guard for the blanket-redaction-over-URLs bug.
func TestHARPreservesURLHostAndHashedPaths(t *testing.T) {
	input := []byte(`{
	  "log": {
	    "version": "1.2",
	    "creator": {"name": "test", "version": "1"},
	    "entries": [{
	      "request": {"method": "GET", "url": "https://dashboard.internal.megacorp/assets/app.a1b2c3d4e5f6a7b8c9d0.js?token=secret-token-value-1234", "headers": []},
	      "response": {"status": 200, "headers": []}
	    }]
	  }
	}`)
	scrubbed, err := HAR(input, Policy{})
	if err != nil {
		t.Fatalf("HAR: %v", err)
	}
	text := string(scrubbed)
	for _, keep := range []string{"dashboard.internal.megacorp", "app.a1b2c3d4e5f6a7b8c9d0.js"} {
		if !strings.Contains(text, keep) {
			t.Fatalf("scrubbed HAR dropped non-secret URL part %q: %s", keep, text)
		}
	}
	if strings.Contains(text, "secret-token-value-1234") {
		t.Fatalf("scrubbed HAR leaked sensitive query token: %s", text)
	}
}

// TestValueURLRedactsOpaqueQueryAndFragmentSecrets locks in that opaque
// credentials in a URL's query and fragment — signed-URL signatures, OAuth
// codes/tokens under param names outside the fixed sensitive list — are redacted,
// while the scheme/host/path (including content-hashed asset filenames) survive.
func TestValueURLRedactsOpaqueQueryAndFragmentSecrets(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		keep   []string
		redact []string
	}{
		{
			name: "azure sas signature keeps param name",
			in:   "https://storage.example/blob/app.a1b2c3d4e5f6a7b8.js?sig=AbCdEf0123456789GhIjKlMn0Z&se=2026",
			// The param NAME must survive (not be swallowed into the redaction).
			keep:   []string{"storage.example", "app.a1b2c3d4e5f6a7b8.js", "se=2026", "sig="},
			redact: []string{"AbCdEf0123456789GhIjKlMn0Z"},
		},
		{
			name:   "oauth implicit fragment token",
			in:     "https://app.example/callback#access_token=AbCdEf0123456789GhIjKlMn0Z&state=xyz",
			keep:   []string{"app.example", "/callback", "state=xyz"},
			redact: []string{"AbCdEf0123456789GhIjKlMn0Z"},
		},
		{
			name: "percent-encoded signature is decoded and caught",
			// A base64 signature whose +,/ are percent-encoded (%2B/%2F) must still
			// be detected via the decoded form, and the raw encoded run redacted.
			in:     "https://x.example/dl?Signature=YWJjZGVm%2Bghijkl%2Fmnop0123456789QRS",
			keep:   []string{"x.example", "Signature="},
			redact: []string{"YWJjZGVm", "mnop0123456789QRS"},
		},
		{
			name:   "low-entropy values preserved",
			in:     "https://x.example/v2/orders?page=2&size=100&code=US&sort=name",
			keep:   []string{"page=2", "size=100", "code=US", "sort=name", "/v2/orders"},
			redact: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValueURL(tc.in, Policy{})
			for _, keep := range tc.keep {
				if !strings.Contains(got, keep) {
					t.Errorf("ValueURL dropped non-secret part %q: %s", keep, got)
				}
			}
			for _, secret := range tc.redact {
				if strings.Contains(got, secret) {
					t.Errorf("ValueURL leaked opaque secret %q: %s", secret, got)
				}
			}
		})
	}
}

func TestHARScrubsWebSocketMessages(t *testing.T) {
	input := []byte(`{
	  "log": {
	    "version": "1.2",
	    "creator": {"name": "test", "version": "1"},
	    "entries": [{
	      "request": {"method": "GET", "url": "wss://example.test/ws", "headers": []},
	      "response": {"status": 101, "headers": []},
	      "_webSocketMessages": [
	        {"type": "send", "data": "{\"access_token\":\"ws-secret-token\"}"},
	        {"type": "receive", "data": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature"}
	      ]
	    }]
	  }
	}`)
	scrubbed, err := HAR(input, Policy{})
	if err != nil {
		t.Fatalf("HAR: %v", err)
	}
	text := string(scrubbed)
	for _, secret := range []string{"ws-secret-token", "eyJhbGciOiJIUzI1NiJ9"} {
		if strings.Contains(text, secret) {
			t.Fatalf("websocket HAR payload leaked %q in %s", secret, text)
		}
	}
	if !strings.Contains(text, Redacted) {
		t.Fatalf("scrubbed HAR = %s, want redaction marker", text)
	}
}

const sampleHAR = `{
  "log": {
    "version": "1.2",
    "creator": {"name": "test", "version": "1"},
    "entries": [{
      "startedDateTime": "2026-06-11T10:00:00Z",
      "request": {
        "method": "POST",
        "url": "https://shop.example/login?session=abc123",
        "headers": [
          {"name": "Authorization", "value": "Bearer tok123"},
          {"name": "Content-Type", "value": "application/json"}
        ],
        "cookies": [{"name": "sid", "value": "cookie-secret"}],
        "queryString": [
          {"name": "session", "value": "abc123"},
          {"name": "next", "value": "/cart"}
        ],
        "postData": {
          "mimeType": "application/json",
          "text": "{\"password\":\"hunter2\",\"user\":\"jason\"}"
        }
      },
      "response": {
        "status": 200,
        "headers": [{"name": "Set-Cookie", "value": "sid=new-secret"}],
        "cookies": [{"name": "sid", "value": "new-secret"}],
        "content": {
          "mimeType": "application/json",
          "text": "{\"access_token\":\"jwt-secret\",\"ok\":true}"
        }
      },
      "customField": {"preserved": true}
    }]
  }
}`

func TestHAR(t *testing.T) {
	scrubbed, err := HAR([]byte(sampleHAR), Policy{})
	if err != nil {
		t.Fatalf("HAR: %v", err)
	}
	text := string(scrubbed)

	for _, secret := range []string{"Bearer tok123", "cookie-secret", "hunter2", "jwt-secret", "new-secret", "session=abc123"} {
		if strings.Contains(text, secret) {
			t.Errorf("scrubbed HAR still contains %q", secret)
		}
	}
	for _, kept := range []string{"application/json", `"user\":\"jason`, "/cart", "customField", "preserved"} {
		if !strings.Contains(text, strings.ReplaceAll(kept, `\`, ``)) && !strings.Contains(text, kept) {
			t.Errorf("scrubbed HAR lost expected content %q", kept)
		}
	}

	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		t.Fatalf("scrubbed HAR is not valid JSON: %v", err)
	}
	if HARSensitivity(scrubbed) != SensitivityScrubbed {
		t.Error("scrubbed HAR must carry the scrubbed sensitivity label")
	}
	if HARSensitivity([]byte(sampleHAR)) != SensitivityRaw {
		t.Error("unscrubbed HAR must classify as raw")
	}
}

func TestHARSingleEntryRootObject(t *testing.T) {
	input := []byte(`{
	  "request": {
	    "method": "POST",
	    "url": "https://shop.example/login?token=abc123",
	    "headers": [{"name": "Authorization", "value": "Bearer secret-token"}],
	    "postData": {"text": "{\"password\":\"hunter2\"}"}
	  },
	  "response": {
	    "status": 200,
	    "headers": [{"name": "Set-Cookie", "value": "sid=cookie-secret"}],
	    "content": {"text": "{\"access_token\":\"jwt-secret\"}"}
	  }
	}`)
	scrubbed, err := HAR(input, Policy{})
	if err != nil {
		t.Fatalf("HAR single entry: %v", err)
	}
	text := string(scrubbed)
	for _, secret := range []string{"secret-token", "hunter2", "cookie-secret", "jwt-secret", "token=abc123"} {
		if strings.Contains(text, secret) {
			t.Fatalf("single-entry scrub leaked %q in %s", secret, text)
		}
	}
	if HARSensitivity(scrubbed) != SensitivityScrubbed {
		t.Fatalf("single-entry sensitivity = %q, want scrubbed", HARSensitivity(scrubbed))
	}
}

func TestHARErrors(t *testing.T) {
	if _, err := HAR([]byte("not json"), Policy{}); err == nil {
		t.Error("expected error for invalid JSON")
	}
	if _, err := HAR([]byte(`{"notlog": {}}`), Policy{}); err == nil {
		t.Error("expected error for missing log section")
	}
}

// TestPolicyCompile asserts custom patterns compile once and that invalid
// patterns are surfaced as errors rather than silently swallowed.
func TestPolicyCompile(t *testing.T) {
	t.Run("valid patterns compile", func(t *testing.T) {
		valid, errs := Policy{ExtraPatterns: []Rule{
			{Pattern: `\d{3}-\d{2}-\d{4}`},
			{Pattern: `4\d{15}`, Replacement: "****"},
		}}.Compile()
		if len(errs) != 0 {
			t.Fatalf("unexpected compile errors: %v", errs)
		}
		if len(valid) != 2 {
			t.Fatalf("expected 2 compiled patterns, got %d", len(valid))
		}
	})

	t.Run("invalid pattern surfaced", func(t *testing.T) {
		valid, errs := Policy{ExtraPatterns: []Rule{
			{Pattern: `\d{3}-\d{2}-\d{4}`},
			{Pattern: `([`}, // unbalanced group → invalid
		}}.Compile()
		if len(errs) != 1 {
			t.Fatalf("expected 1 compile error, got %d: %v", len(errs), errs)
		}
		if !strings.Contains(errs[0].Error(), "([") {
			t.Errorf("error should name the offending pattern, got %q", errs[0].Error())
		}
		if len(valid) != 1 {
			t.Errorf("valid patterns should still be returned, got %d", len(valid))
		}
	})

	t.Run("empty policy compiles clean", func(t *testing.T) {
		valid, errs := Policy{}.Compile()
		if len(valid) != 0 || len(errs) != 0 {
			t.Errorf("zero-value policy should yield no patterns/errors, got %d/%d", len(valid), len(errs))
		}
	})
}

// TestHARWithErrorsSurfacesInvalidPattern proves a typo'd custom pattern is
// reported once through the HAR entry point while built-in redaction still runs.
func TestHARWithErrorsSurfacesInvalidPattern(t *testing.T) {
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `([`}}}
	scrubbed, patternErrs, err := HARWithErrors([]byte(sampleHAR), policy)
	if err != nil {
		t.Fatalf("HARWithErrors: %v", err)
	}
	if len(patternErrs) != 1 {
		t.Fatalf("expected the invalid pattern to be surfaced, got %d errors", len(patternErrs))
	}
	// Built-in protections must still apply despite the bad custom rule.
	if strings.Contains(string(scrubbed), "Bearer tok123") {
		t.Error("built-in redaction must still run when a custom pattern is invalid")
	}

	// HAR (the legacy signature) keeps working and just drops the errors.
	if _, err := HAR([]byte(sampleHAR), policy); err != nil {
		t.Fatalf("HAR with invalid custom pattern should not error: %v", err)
	}
}

// TestCompileOnceManyValues feeds a single policy through many values and HAR
// entries, asserting the custom pattern redacts every match. Compiling per call
// would still be correct, so this guards behavior; the benchmark below guards
// against the per-call recompile regression.
func TestCompileOnceManyValues(t *testing.T) {
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `SSN-\d{4}`, Replacement: "[PII]"}}}

	for i := 0; i < 500; i++ {
		got := Value("user SSN-1234 record", policy)
		if got != "user [PII] record" {
			t.Fatalf("iteration %d: Value = %q, want %q", i, got, "user [PII] record")
		}
	}

	hdrs := map[string]string{"X-Note": "SSN-9999 here", "Content-Type": "text/plain"}
	out := Headers(hdrs, policy)
	if out["X-Note"] != "[PII] here" {
		t.Errorf("Headers custom pattern not applied: %q", out["X-Note"])
	}
	if out["Content-Type"] != "text/plain" {
		t.Errorf("Headers altered a non-matching value: %q", out["Content-Type"])
	}
}

// BenchmarkValueCustomPattern guards the compile-once optimization: Value
// applies the same custom policy to many values without recompiling the regexp
// on every call. A regression that recompiled per call would inflate this.
func BenchmarkValueCustomPattern(b *testing.B) {
	policy := Policy{ExtraPatterns: []Rule{
		{Pattern: `SSN-\d{4}`, Replacement: "[PII]"},
		{Pattern: `\b\d{16}\b`, Replacement: "[CARD]"},
	}}
	const input = "account SSN-1234 paid with 4111111111111111 today"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Value(input, policy)
	}
}

// BenchmarkHARCustomPattern exercises the hot path where one compiled policy is
// reused across every header/cookie/param/body value in a HAR document.
func BenchmarkHARCustomPattern(b *testing.B) {
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `SSN-\d{4}`, Replacement: "[PII]"}}}
	data := []byte(sampleHAR)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := HAR(data, policy); err != nil {
			b.Fatal(err)
		}
	}
}

// TestValueRedactsAuthorizationJSONField locks in that an "authorization" JSON
// body field (a Bearer token, never a correlation key) is redacted.
func TestValueRedactsAuthorizationJSONField(t *testing.T) {
	got := Value(`{"authorization":"Bearer eyJ.abc.def"}`, Policy{})
	if !strings.Contains(got, Redacted) || strings.Contains(got, "eyJ.abc.def") {
		t.Fatalf("expected authorization value redacted, got %s", got)
	}
}

// TestValuePreservesCorrelationTargets locks in that JSON body fields the
// correlation engine captures-and-replays (session/sessionid/csrf/xsrf) are NOT
// redacted — the HAR/recorder pipelines scrub before correlation, so redacting
// these would break replay parameterization (see pkg/convert/har TestConvertHAR).
// TestValueRedactsSessionAndCSRFTokens locks in that live session/CSRF tokens in
// JSON bodies are redacted. These were previously preserved for correlation, but
// correlation-time replay reconstruction now runs against the raw server-side
// recording sidecar and raw import content (not this scrubbed view), so leaving
// them un-redacted only leaked live credentials into scrubbed artifacts.
func TestValueRedactsSessionAndCSRFTokens(t *testing.T) {
	cases := []struct {
		in     string
		secret string
	}{
		{`{"sessionId":"abc12345"}`, "abc12345"},
		{`{"session":"sid-123"}`, "sid-123"},
		{`{"csrf":"csrf-tok"}`, "csrf-tok"},
		{`{"csrf_token":"csrf-tok2"}`, "csrf-tok2"},
		{`{"xsrf":"xsrf-tok"}`, "xsrf-tok"},
	}
	for _, tc := range cases {
		got := Value(tc.in, Policy{})
		if strings.Contains(got, tc.secret) {
			t.Fatalf("session/csrf token %q leaked into scrubbed body: %s", tc.secret, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Fatalf("expected redaction marker for %q, got: %s", tc.in, got)
		}
	}
}

// TestValueRedactsJWTInsideEmbeddedURLQuery pins that embedded-URL protection
// does not create a disclosure hole. protectEmbeddedURLs deliberately hides
// http(s) spans from redactBareTokens/jwtPattern so safe hosts and content
// hashes survive; the URL-aware query governance inside valueURL is what must
// still catch a credential-shaped query value, whether or not the parameter
// name is in the sensitive vocabulary.
func TestValueRedactsJWTInsideEmbeddedURLQuery(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIxMjM0NTY3ODkwIn0." +
		"dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	for _, parameter := range []string{"access_token", "trace"} {
		input := "callback https://safe.example/cb?" + parameter + "=" + jwt
		got := Value(input, Policy{})
		if strings.Contains(got, jwt) {
			t.Fatalf("JWT survived inside an embedded URL query (%s=): %q", parameter, got)
		}
		if !strings.Contains(got, "https://safe.example/cb?"+parameter+"=") {
			t.Fatalf("Value corrupted the surrounding URL (%s=): %q", parameter, got)
		}
	}
}

// TestValueRedactsNonHTTPConnectionStringCredentials covers the schemes that
// carry credentials in URL userinfo but are deliberately excluded from the
// http(s)-only placeholder exemption, plus the opaque jdbc: DSN form that has
// no "://" at all and therefore matches no URL pattern.
func TestValueRedactsNonHTTPConnectionStringCredentials(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		secret  string
		keepers []string
	}{
		{
			name:    "amqp userinfo",
			input:   "broker amqp://svcuser:s3cr3t-broker-pass@broker.example/vhost",
			secret:  "s3cr3t-broker-pass",
			keepers: []string{"broker.example"},
		},
		{
			name:    "mongodb userinfo",
			input:   "mongo mongodb://svcuser:s3cr3t-mongo-pass@db.example:27017/app",
			secret:  "s3cr3t-mongo-pass",
			keepers: []string{"db.example"},
		},
		{
			name:    "oracle thin opaque dsn",
			input:   "dsn jdbc:oracle:thin:appuser/s3cr3t-db-pass@//db.example:1521/ORCL",
			secret:  "s3cr3t-db-pass",
			keepers: []string{"jdbc:oracle:thin:appuser/", "db.example:1521/ORCL"},
		},
		{
			name:    "jdbc url form with password parameter",
			input:   "dsn jdbc:mysql://db.example:3306/app?user=svc&password=s3cr3t-my-pass",
			secret:  "s3cr3t-my-pass",
			keepers: []string{"db.example:3306/app"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Value(test.input, Policy{})
			if strings.Contains(got, test.secret) {
				t.Fatalf("credential survived: %q", got)
			}
			if !strings.Contains(got, Redacted) {
				t.Fatalf("no redaction marker was emitted: %q", got)
			}
			for _, keeper := range test.keepers {
				if !strings.Contains(got, keeper) {
					t.Fatalf("Value dropped non-credential context %q: %q", keeper, got)
				}
			}
		})
	}
}

// TestValueLeavesOrdinaryPathLikeTextIntact guards the opaque-DSN rule against
// over-matching: "a/b@c" shaped text is ordinary prose, not a connection string.
func TestValueLeavesOrdinaryPathLikeTextIntact(t *testing.T) {
	for _, input := range []string{
		"path a/b@c.example",
		"handler pkg/convert@v1.2.3",
		"see docs/setup@latest for details",
	} {
		if got := Value(input, Policy{}); got != input {
			t.Fatalf("Value rewrote ordinary text %q -> %q", input, got)
		}
	}
}

// A nested URL is inspected value by value, as a top-level URL is: one with
// nothing secret-shaped in its query stays readable instead of being judged
// whole as a high-entropy token, while its own token-shaped values are still
// redacted.
func TestValueURLInspectsNestedURLValueByValue(t *testing.T) {
	plain := "https://outer.example.test/?next=https%3A%2F%2Finner.example.test%2Fapp%2F0015g00000AbCdE7%3Fid%3D42%26tab%3D7"
	if got := ValueURL(plain, Policy{}); got != plain {
		t.Fatalf("credential-free nested URL = %s, want it unchanged", got)
	}
	signed := "https://outer.example.test/?next=https%3A%2F%2Finner.example.test%2F%3Fsig%3DkQ9vX2mL7pR4tY8wZ3nB6cF1hJ5dG0sA%26tab%3D7"
	want := "https://outer.example.test/?next=https%3A%2F%2Finner.example.test%2F%3Fsig%3D" + Redacted + "%26tab%3D7"
	if got := ValueURL(signed, Policy{}); got != want {
		t.Fatalf("nested token-shaped value = %s, want %s", got, want)
	}
	if got := ValueURL("https://outer.example.test/?next=kQ9vX2mL7pR4tY8wZ3nB6cF1hJ5dG0sA", Policy{}); got != "https://outer.example.test/?next="+Redacted {
		t.Fatalf("token-shaped value that is not a URL = %s, want it redacted whole", got)
	}
}

func TestProtectNestedQueryPairs(t *testing.T) {
	references := regexp.MustCompile(`\{\{[^{}]+\}\}`)
	for _, test := range []struct {
		value     string
		protected []string
	}{
		{"/x?u=https%3A%2F%2Fa%2F%3Fpassword%3D{{secret.A}}%26b%3D1", []string{"password%3D{{secret.A}}"}},
		{"/x?u=https%253A%252F%252Fa%252F%253Ftoken%253D{{secret.A}}", []string{"token%253D{{secret.A}}"}},
		{"/x?u=https%3a%2f%2fa%2f%3fk%3d{{secret.A}}%23top", []string{"k%3d{{secret.A}}"}},
		{"/x?u=https%3A%2F%2Fa%2F%3Fa%3D{{secret.A}}%26b%3D{{secret.B}}", []string{"a%3D{{secret.A}}", "b%3D{{secret.B}}"}},
		{"/x?u=a%3Dprefix{{secret.A}}", nil},                               // the reference is not the whole value
		{"/x?u=https%3A%2F%2Fa%2F%3Fk%3D{{secret.A}}tail", nil},            // nor here
		{"/x?u=k={{secret.A}}", nil},                                       // a literal '=' is the top level's
		{"%3D{{secret.A}}", nil},                                           // not inside a query
		{"/x?note=kQ9vX2mL7pR4tY8wZ3nB6cF1%3D{{secret.A}}", nil},           // the "name" is part of a value
		{"/x?a%3D{{secret.A}}", nil},                                       // a top-level segment, not a nested pair
		{"/x?u=https%3A%2F%2Fa%2F%3Ftoken%3DkQ9vX2mL%3D{{secret.A}}", nil}, // nor this, after a nested '='
		{"/x?u=https%3A%2F%2Fa%2F%3Fk%253D{{secret.A}}", nil},              // '=' escaped deeper than its '?'
		{"/x?u=https%3A%2F%2Fa%2F%3Fk%3D{{secret.A}}%2526tail", nil},       // the value goes on past a deeper '&'
		{"/x?u=https%3A%2F%2Fa%2F%3Fa%3D{{secret.A}}%26b%3D{{secret.B}}%2526tail", []string{"a%3D{{secret.A}}"}},
		{"/x?u=https%3A%2F%2Fm%2F%3Fr%3Dhttps%253A%252F%252Fi%252F%253Fk%253D{{secret.A}}%26x%3D1", []string{"k%253D{{secret.A}}"}},
	} {
		protected, pairs := ProtectNestedQueryPairs(test.value, references.FindAllStringIndex(test.value, -1), func(index int) string {
			return "apex~" + strconv.Itoa(index) + "=safe"
		})
		var got []string
		for index := range len(pairs) {
			placeholder := "apex~" + strconv.Itoa(index) + "=safe"
			if !strings.Contains(protected, placeholder) || ValueURL(placeholder, Policy{}) != placeholder {
				t.Fatalf("%s: placeholder %s missing from %s", test.value, placeholder, protected)
			}
			got = append(got, pairs[placeholder])
			protected = strings.Replace(protected, placeholder, pairs[placeholder], 1)
		}
		if protected != test.value || strings.Join(got, " ") != strings.Join(test.protected, " ") {
			t.Fatalf("%s: protected %q (restored %q), want %q", test.value, got, protected, test.protected)
		}
	}
}

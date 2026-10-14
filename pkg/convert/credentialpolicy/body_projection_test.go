package credentialpolicy

import (
	"bytes"
	"strings"
	"testing"
)

func TestProjectRequestBodyPreservesNonsensitiveWireBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		payload     string
	}{
		{
			name:        "formatted JSON",
			contentType: "application/json",
			payload:     "{\n  \"count\": 1.00e+2,\n  \"ordinary\": true\n}",
		},
		{
			name:        "XML namespaces and quotes",
			contentType: "application/soap+xml",
			payload:     `<s:Envelope xmlns:s='urn:test'><s:Body ordinary="keep">value</s:Body></s:Envelope>`,
		},
		{
			name:        "form raw escaping",
			contentType: "application/x-www-form-urlencoded",
			payload:     "ordinary=a%2Fb%2Bc&duplicate=one&duplicate=two",
		},
		{
			name:        "raw high entropy",
			contentType: "application/octet-stream",
			payload:     "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output, result, err := ProjectRequestBody(
				[]byte(test.payload),
				test.contentType,
				projectionOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Disposition == Rejected || !bytes.Equal(output, []byte(test.payload)) {
				t.Fatalf("body changed: %q / %#v", output, result)
			}
		})
	}
}

func TestProjectRequestBodyProjectsStructuredCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		payload     string
		stable      string
	}{
		{
			name:        "json",
			contentType: "text/plain",
			payload:     "{\n \"password\": \"" + projectionCanary + "\", \"count\": 1.00e+2\n}",
			stable:      `"count": 1.00e+2`,
		},
		{
			name:        "xml",
			contentType: "text/plain",
			payload:     `<root ordinary='keep'><password>` + projectionCanary + `</password></root>`,
			stable:      `ordinary='keep'`,
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			payload:     "ordinary=a%2Fb&password=" + projectionCanary,
			stable:      "ordinary=a%2Fb",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output, result, err := ProjectRequestBody(
				[]byte(test.payload),
				test.contentType,
				projectionOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(output, []byte(test.stable)) {
				t.Fatalf("nonsensitive bytes changed: %s", output)
			}
			assertDisclosureSafe(t, output, result)
		})
	}
}

func TestProjectRequestBodyHonorsDeclaredStructuredMediaType(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		contentType string
		payload     string
		disposition Disposition
		output      bool
	}{
		{
			name:        "declared JSON is not reinterpreted as form",
			contentType: "application/json",
			payload:     "ordinary=1",
			disposition: BlockedDraft,
			output:      true,
		},
		{
			name:        "declared XML is not reinterpreted as JSON",
			contentType: "application/xml",
			payload:     `{"ordinary":1}`,
			disposition: BlockedDraft,
			output:      true,
		},
		{
			name:        "declared JSON is not reinterpreted as multipart",
			contentType: "application/json",
			payload: "--wire\r\n" +
				"Content-Disposition: form-data; name=\"ordinary\"\r\n\r\n" +
				"keep\r\n--wire--\r\n",
			disposition: BlockedDraft,
			output:      true,
		},
		{
			name:        "malformed declared JSON with credential is rejected",
			contentType: "application/json",
			payload:     "password=" + projectionCanary,
			disposition: Rejected,
		},
		{
			name:        "malformed declared XML with credential is rejected",
			contentType: "application/xml",
			payload:     "password=" + projectionCanary,
			disposition: Rejected,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output, result, err := ProjectRequestBody(
				[]byte(test.payload),
				test.contentType,
				projectionOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Disposition != test.disposition || (output != nil) != test.output {
				t.Fatalf("declared body = %q / %#v", output, result)
			}
			assertDisclosureSafe(t, output, result)
		})
	}

	for _, contentType := range []string{"", "text/plain", "application/octet-stream"} {
		output, result, err := ProjectRequestBody(
			[]byte(`{"password":"`+projectionCanary+`","ordinary":1}`),
			contentType,
			projectionOptions(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition == Rejected || !bytes.Contains(output, []byte(`"ordinary":1`)) {
			t.Fatalf("generic %q body was not sniffed: %s / %#v", contentType, output, result)
		}
		assertDisclosureSafe(t, output, result)
	}
}

func TestProjectRequestBodyGovernsCredentialNamedRawBodyAsWholeValue(t *testing.T) {
	t.Parallel()
	payload := []byte("password = " + projectionCanary + "\nordinary prose")
	output, result, err := ProjectRequestBody(payload, "text/plain", projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "{{secret.FIXTURE_RAW_BODY}}" ||
		result.Disposition != ExecutableExact ||
		len(result.Requirements) != 1 ||
		result.Requirements[0].Value != string(payload) {
		t.Fatalf("raw credential body = %q / %#v", output, result)
	}
	assertDisclosureSafe(t, output, result)
}

func TestProjectRequestBodyRejectsUnsafeTypedAndGraphQLCredentials(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		contentType string
		payload     string
	}{
		{contentType: "application/json", payload: `{"password":7}`},
		{contentType: "application/graphql", payload: `mutation { login(password: "` + projectionCanary + `") }`},
		{contentType: "application/json", payload: `{"query":"mutation { login(password: \"` + projectionCanary + `\") }"}`},
		{contentType: "application/json", payload: `{"qu\u0065ry":"mutation { login(password: \"` + projectionCanary + `\") }"}`},
	} {
		output, result, err := ProjectRequestBody(
			[]byte(test.payload),
			test.contentType,
			projectionOptions(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if output != nil || result.Disposition != Rejected {
			t.Fatalf("unsafe body retained: %s / %#v", output, result)
		}
		assertDisclosureSafe(t, output, result)
	}
}

func TestProjectMultipartWirePreservesEnvelopeAndProjectsNestedCredential(t *testing.T) {
	t.Parallel()
	const boundary = "wire-boundary"
	payload := "preamble\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"payload\"\r\n" +
		"Content-Type: application/json\r\n" +
		"X-Trace: keep  \r\n\r\n" +
		"{\n  \"password\": \"" + projectionCanary + "\",\n  \"count\": 1.00e+2\n}\r\n" +
		"--" + boundary + "--\r\n" +
		"epilogue"
	output, result, err := ProjectRequestBody(
		[]byte(payload),
		"multipart/form-data; boundary="+boundary,
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, stable := range []string{
		"preamble\r\n",
		"Content-Disposition: form-data; name=\"payload\"\r\n",
		"X-Trace: keep  \r\n",
		`"count": 1.00e+2`,
		"--" + boundary + "--\r\nepilogue",
	} {
		if !bytes.Contains(output, []byte(stable)) {
			t.Fatalf("multipart bytes %q changed: %s", stable, output)
		}
	}
	if strings.Contains(string(output), projectionCanary) {
		t.Fatalf("nested multipart credential remained: %s", output)
	}
	assertDisclosureSafe(t, output, result)
}

func TestProjectMultipartWireRejectsUnsafeNestedTypedCredential(t *testing.T) {
	t.Parallel()
	const boundary = "wire-boundary"
	payload := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"payload\"\r\n" +
		"Content-Type: application/json\r\n\r\n" +
		`{"password":7}` + "\r\n" +
		"--" + boundary + "--\r\n"
	output, result, err := ProjectRequestBody(
		[]byte(payload),
		"multipart/form-data; boundary="+boundary,
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected {
		t.Fatalf("unsafe multipart retained: %s / %#v", output, result)
	}
}

func TestProjectMultipartWireRejectsAmbiguousCredentialDisposition(t *testing.T) {
	t.Parallel()
	const boundary = "wire-boundary"
	payload := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"password\"\r\n" +
		"content-disposition: form-data; name=\"ordinary\"\r\n\r\n" +
		projectionCanary + "\r\n" +
		"--" + boundary + "--\r\n"
	output, result, err := ProjectRequestBody(
		[]byte(payload),
		"multipart/form-data; boundary="+boundary,
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected {
		t.Fatalf("ambiguous multipart credential disposition was retained: %s / %#v", output, result)
	}
	assertDisclosureSafe(t, output, result)
}

// A template the caller trusts may stand where a JSON value goes (a refreshed
// client timestamp stays a number); the rest of the body is still projected.
// An untrusted unquoted expression leaves the body malformed, as before.
func TestProjectRequestBodyKeepsTrustedUnquotedTemplates(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"password":"synthetic-pass-6612","events":[{"t":{{fn.timestamp.unix_ms}}}]}`)
	options := Options{
		Mode:               Redact,
		Source:             SourceRef{Format: "test"},
		RequestKey:         "events",
		TrustedWholeValues: map[string]struct{}{"{{fn.timestamp.unix_ms}}": {}},
	}
	projected, result, err := ProjectRequestBody(payload, "application/json", options)
	if err != nil || result.Disposition == Rejected {
		t.Fatalf("projection = %v, %+v", err, result)
	}
	if strings.Contains(string(projected), "synthetic-pass-6612") || !bytes.Contains(projected, []byte(`"t":{{fn.timestamp.unix_ms}}`)) {
		t.Fatalf("projected = %s, want the password masked and the template kept", projected)
	}

	options.TrustedWholeValues = nil
	projected, result, _ = ProjectRequestBody(payload, "application/json", options)
	if result.Disposition != Rejected && bytes.Contains(projected, []byte("synthetic-pass-6612")) {
		t.Fatalf("untrusted template body = %s, %+v; want it refused or masked", projected, result)
	}
}

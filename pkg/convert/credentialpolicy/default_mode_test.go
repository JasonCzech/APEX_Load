package credentialpolicy

import (
	"strings"
	"testing"
)

// The zero-value projection mode must preserve executable credential values.
// An adapter that forgets to set Mode used to fall through to SecretReference,
// which silently replaced the operator's own imported value with an unbound
// {{secret.NAME}} reference and produced a script that could not replay.
func TestOmittedProjectionModePreservesExecutableValues(t *testing.T) {
	t.Parallel()
	const password = "omitted-mode-canary-4b91"

	headers, headerResult, err := ProjectHeaders(
		[]HeaderOccurrence{{Name: "Authorization", Value: password}},
		Options{
			Source:     SourceRef{Format: "fixture", Pointer: "/request"},
			RequestKey: "request-one",
			NamePrefix: "fixture",
		},
	)
	if err != nil {
		t.Fatalf("ProjectHeaders: %v", err)
	}
	if len(headers) != 1 || headers[0].Value != password {
		t.Fatalf("header value = %#v, want the exact imported value preserved", headers)
	}
	if len(headerResult.Requirements) != 0 {
		t.Fatalf("header requirements = %#v, want no re-entry binding", headerResult.Requirements)
	}
	if len(headerResult.PreservedTargets) != 1 ||
		headerResult.PreservedTargets[0].Field != "header_list" {
		t.Fatalf("header preserved targets = %#v, want one runtime redaction locator",
			headerResult.PreservedTargets)
	}

	body, bodyResult, err := ProjectRequestBody(
		[]byte(`{"password":"`+password+`"}`),
		"application/json",
		Options{
			Source:     SourceRef{Format: "fixture", Pointer: "/request"},
			RequestKey: "request-one",
			NamePrefix: "fixture",
		},
	)
	if err != nil {
		t.Fatalf("ProjectRequestBody: %v", err)
	}
	if !strings.Contains(string(body), password) {
		t.Fatalf("projected body = %s, want the exact imported value preserved", body)
	}
	if strings.Contains(string(body), "{{secret.") {
		t.Fatalf("projected body = %s, want no secret reference substitution", body)
	}
	if len(bodyResult.Requirements) != 0 {
		t.Fatalf("body requirements = %#v, want no re-entry binding", bodyResult.Requirements)
	}

	url, urlResult, err := ProjectRawURL(
		"https://api.example.test/login?password="+password,
		Options{
			Source:     SourceRef{Format: "fixture", Pointer: "/request"},
			RequestKey: "request-one",
			NamePrefix: "fixture",
		},
	)
	if err != nil {
		t.Fatalf("ProjectRawURL: %v", err)
	}
	if !strings.Contains(url, password) || strings.Contains(url, "{{secret.") {
		t.Fatalf("projected URL = %q, want the exact imported value preserved", url)
	}
	if len(urlResult.Requirements) != 0 {
		t.Fatalf("URL requirements = %#v, want no re-entry binding", urlResult.Requirements)
	}
}

// An explicitly requested non-preserving mode must keep working exactly as
// before: this default change must not weaken platform-managed redaction.
func TestExplicitProjectionModesAreUnaffectedByDefault(t *testing.T) {
	t.Parallel()
	const password = "explicit-mode-canary-77c2"
	for _, mode := range []ProjectionMode{SecretReference, Redact} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			headers, _, err := ProjectHeaders(
				[]HeaderOccurrence{{Name: "Authorization", Value: password}},
				Options{
					Mode:       mode,
					Source:     SourceRef{Format: "fixture", Pointer: "/request"},
					RequestKey: "request-one",
					NamePrefix: "fixture",
				},
			)
			if err != nil {
				t.Fatalf("ProjectHeaders: %v", err)
			}
			if len(headers) != 1 || strings.Contains(headers[0].Value, password) {
				t.Fatalf("header value = %#v, want the explicit %s projection", headers, mode)
			}
		})
	}
}

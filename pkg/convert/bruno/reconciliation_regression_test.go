package bruno

import (
	"bytes"
	"strings"
	"testing"
)

func TestInspectNativeBuildsCandidatesOnceWhenProjectionIsUnchanged(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: One pass, uid: one-pass, type: http}
http:
  method: GET
  url: https://example.test/health
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.nativeRequestPasses != 1 {
		t.Fatalf("native request candidate passes = %d, want 1", result.nativeRequestPasses)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Script == nil {
		t.Fatalf("native candidates = %#v", result.Scripts)
	}
}

func TestInspectNativePreservesAuthoredQueryAndResolvesManagedParamProjection(t *testing.T) {
	const sourcePart = "managed-query-secret-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Managed query, uid: managed-query, type: http}
variables:
  - {name: tokenSuffix, value: ` + sourcePart + `}
http:
  method: GET
  url: "https://api.test/login?debug=1&token=abc-{{tokenSuffix}}"
  params:
    - {name: debug, type: query, value: "1", disabled: true}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	if !bytes.Contains(sanitized, []byte("debug=1&token=abc-{{tokenSuffix}}")) ||
		!bytes.Contains(sanitized, []byte(sourcePart)) {
		t.Fatalf("authored source was unexpectedly scrubbed:\n%s", sanitized)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("unexpected secret requirements = %#v", result.SecretRequirements)
	}
	if got := result.Scripts[0].Script.Actions[0].Target; strings.Contains(got, "debug=") ||
		got != "https://api.test/login?token=abc-"+sourcePart {
		t.Fatalf("projected target = %q", got)
	}
}

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

func TestInspectNativeScrubsAuthoredQueryByNameAfterManagedParamProjection(t *testing.T) {
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
	if !bytes.Contains(sanitized, []byte("debug=1&token={{secret.")) {
		t.Fatalf("credential query pair was not scrubbed in authored position:\n%s", sanitized)
	}
	for _, forbidden := range []string{
		"debug={{secret.",
		"abc-{{tokenSuffix}}",
		sourcePart,
	} {
		if bytes.Contains(sanitized, []byte(forbidden)) {
			t.Fatalf("sanitized source retained or misplaced %q:\n%s", forbidden, sanitized)
		}
	}
	if got := result.Scripts[0].Script.Actions[0].Target; strings.Contains(got, "debug=") ||
		!strings.Contains(got, "token={{secret.") {
		t.Fatalf("projected target = %q", got)
	}
}

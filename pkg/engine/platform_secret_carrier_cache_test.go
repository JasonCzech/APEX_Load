package engine

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// platformSecretCarrier runs on every authenticated action execution. With an
// unchanged matcher it must not rebuild or rescan the run's value set: a warm
// call allocates (almost) nothing whatever the number of registered values,
// and the answers keep their origin split.
func TestPlatformSecretCarrierIsMemoizedPerMatcherPublication(t *testing.T) {
	platform := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		platform = append(platform, fmt.Sprintf("platform-value-%05d-x", i))
	}
	config, err := withRuntimeSecretValueOverlay(nil, platform, []string{"script-literal-77001"})
	if err != nil {
		t.Fatal(err)
	}
	vu := &VUser{RedactionConfig: config}
	halves := []string{"alice", "pre-platform-value-01234-x-post", "script-literal-77001", "plain-literal-password"}
	want := []bool{false, true, false, false}
	for i, half := range halves {
		if got := vu.platformSecretCarrier()(half); got != want[i] {
			t.Fatalf("carrier(%q) = %v, want %v", half, got, want[i])
		}
	}
	allocs := testing.AllocsPerRun(50, func() {
		carries := vu.platformSecretCarrier()
		for _, half := range halves {
			_ = carries(half)
		}
	})
	if allocs > 4 {
		t.Fatalf("warm platformSecretCarrier allocates %.0f times per action with an unchanged matcher", allocs)
	}

	// A new publication is seen at once.
	next, err := withRuntimeSecretValueOverlay(nil, []string{"plain-literal-password"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vu.RedactionConfig = next
	if !vu.platformSecretCarrier()("plain-literal-password") {
		t.Fatalf("carrier kept a stale answer after the matcher was republished")
	}
}

// The carrier memo answers short credential halves; a request body is never
// copied into it, so data-driven bodies cannot pin megabytes per VUser.
func TestPlatformSecretCarrierDoesNotMemoizeLargeValues(t *testing.T) {
	config, err := withRuntimeSecretValueOverlay(nil, []string{"platform-value-large-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vu := &VUser{RedactionConfig: config}
	large := strings.Repeat("x", 64<<10) + "platform-value-large-1"
	if !vu.platformSecretCarrier()(large) {
		t.Fatal("large value carrying a platform value not reported")
	}
	if strings.Repeat("y", 64<<10) == "" || vu.platformSecretCarrier()(strings.Repeat("y", 64<<10)) {
		t.Fatal("large plain value reported as a carrier")
	}
	for key := range vu.platformCarrier.results {
		if len(key) > maxPlatformCarrierMemoBytes {
			t.Fatalf("carrier memo retained a %d-byte value", len(key))
		}
	}
}

// A transformed body with no runtime template cannot carry a platform value
// the authored-surface registration does not already cover, so the per-request
// body scan is skipped for it.
func TestPlatformDerivedBodySpellingsSkipStaticBodies(t *testing.T) {
	config, err := withRuntimeSecretValueOverlay(nil, []string{"platform-value-static-2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoding := range []string{"utf-16le", "base64"} {
		vu := &VUser{RedactionConfig: config}
		body := `{"note":"static body text"}`
		if encoding == "base64" {
			body = base64.StdEncoding.EncodeToString([]byte(body))
		}
		resolved := vu.resolveAction(dsl.Action{Name: "Post", Type: "POST", Target: "http://example.invalid/", Body: body, BodyEncoding: encoding})
		_ = vu.platformDerivedActionComposites(resolved, nil)
		if cache := vu.platformCarrier; cache != nil && len(cache.results) > 0 {
			t.Fatalf("%s: static body scanned for platform values: %d memo entries", encoding, len(cache.results))
		}
	}
	// A body with a runtime template is still checked.
	vu := &VUser{RedactionConfig: config, Variables: map[string]string{"note": "platform-value-static-2"}}
	resolved := vu.resolveAction(dsl.Action{Name: "Post", Type: "POST", Target: "http://example.invalid/", Body: `{"note":"{{var.note}}"}`, BodyEncoding: "utf-16le"})
	if derived := vu.platformDerivedActionComposites(resolved, nil); len(derived) == 0 {
		t.Fatal("variable-carried platform value in a utf-16le body derived no spelling")
	}
}

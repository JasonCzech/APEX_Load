package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTP3VersionVocabulary(t *testing.T) {
	for raw, want := range map[string]string{
		"HTTP/3": "3", "http/3.0": "3", "h3": "3", "H3": "3", " h3 ": "3",
		"3": "3", "auto+h3": "auto+h3", "h3-29": "h3-29", "hq-interop": "hq-interop",
		"HTTP/2": "2", "HTTP/1.1": "1.1", "": "",
	} {
		if got := NormalizeVersion(raw); got != want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", raw, got, want)
		}
	}
	for _, valid := range []string{"", "1.1", "2", "3", "auto+h3", "HTTP/3", "h3"} {
		if err := ValidateVersion(valid); err != nil {
			t.Errorf("ValidateVersion(%q) = %v", valid, err)
		}
	}
	for _, invalid := range []string{"h3-29", "auto+h2", "auto", "4", "AUTO+H3"} {
		var unsupported *UnsupportedVersionError
		if err := ValidateVersion(invalid); !errors.As(err, &unsupported) {
			t.Errorf("ValidateVersion(%q) = %v, want an unsupported version", invalid, err)
		}
	}
	// HTTP/3 is vocabulary, not yet an executable mode.
	for _, raw := range []string{"3", "auto+h3", "h3"} {
		if VersionExecutable(raw) {
			t.Errorf("VersionExecutable(%q) = true", raw)
		}
	}
	if got := SupportedVersions(); !slices.Equal(got, []string{VersionHTTP11, VersionHTTP2}) {
		t.Errorf("SupportedVersions() = %v", got)
	}
	if !VersionUsesQUIC(VersionHTTP3) || !VersionUsesQUIC(VersionAutoH3) || VersionUsesQUIC(VersionHTTP2) || VersionUsesQUIC("") {
		t.Error("VersionUsesQUIC misclassified a policy")
	}
}

// countingTLS serves TLS with HTTP/2 enabled and counts the requests that
// reached it.
func countingTLS(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	var seen atomic.Int64
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		seen.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	return server.URL, &seen
}

func refusedHTTP3(t *testing.T, h *HTTP, target string, leaf string) {
	t.Helper()
	action := &protocol.Action{Name: "h3", Type: "GET", Target: target}
	if leaf != "" {
		action.HTTPVersion = versionOverride(t, leaf)
	}
	result, err := h.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("leaf %q: %v", leaf, err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, ErrHTTP3NotEnabled.Error()) || !strings.Contains(result.ErrorMessage, EnvHTTP3) {
		t.Fatalf("leaf %q: success=%v message=%q, want the HTTP/3 refusal naming the flag", leaf, result.Success, result.ErrorMessage)
	}
	if err := h.ValidateSharedSessionAction(action); !errors.Is(err, ErrHTTP3NotEnabled) {
		t.Fatalf("leaf %q: concurrent validation = %v", leaf, err)
	}
}

func TestHTTP3PoliciesAreRefusedBeforeAnyTraffic(t *testing.T) {
	t.Setenv(EnvHTTP3, "")
	target, seen := countingTLS(t)
	auto := versionHTTP(t, nil)
	for _, leaf := range []string{VersionHTTP3, VersionAutoH3} {
		refusedHTTP3(t, auto, target, leaf)
	}
	for _, config := range []string{"3", "auto+h3", "HTTP/3"} {
		h := versionHTTP(t, map[string]any{"http_version": config})
		refusedHTTP3(t, h, target, "")
	}
	if n := seen.Load(); n != 0 {
		t.Fatalf("a refused HTTP/3 request reached the target %d times", n)
	}
}

func TestHTTP3SessionPolicyKeepsLeafPrecedence(t *testing.T) {
	t.Setenv(EnvHTTP3, "")
	target, seen := countingTLS(t)
	h := versionHTTP(t, map[string]any{"http_version": "3"})
	for _, leaf := range []string{"auto", "1.1", "2"} {
		result := executeVersion(t, h, target, leaf)
		if result.HTTPVersionPolicy != leaf {
			t.Fatalf("leaf %s ran with policy %q", leaf, result.HTTPVersionPolicy)
		}
	}
	if n := seen.Load(); n != 3 {
		t.Fatalf("target saw %d requests, want 3", n)
	}
	// An explicit config beats the protocol alias, as for 1.1 and 2.
	instance, err := protocol.Get("http/2")
	if err != nil {
		t.Fatal(err)
	}
	aliased := instance.(*HTTP)
	if err := aliased.Init(context.Background(), map[string]any{"tls_insecure_skip_verify": true, "http_version": "auto+h3"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = aliased.Teardown(context.Background()) })
	refusedHTTP3(t, aliased, target, "")
}

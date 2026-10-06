package analysis

import (
	"strings"
	"testing"
)

func TestProviderScrubbingProtectsProseAndPreservesTraceIdentifiers(t *testing.T) {
	for _, value := range []string{"Authorization: Bearer short-secret", "authorization=Basic short-secret", "Authorization: ApiKey short-secret", "user alice@example.com password=short-secret", `{"authorization":"short-secret"}`} {
		for _, scrub := range []func(string) string{ScrubProviderText, ScrubProviderLabel} {
			got := scrub(value)
			if strings.Contains(got, "short-secret") || strings.Contains(got, "alice@example.com") {
				t.Fatalf("disclosed %q", got)
			}
			if second := scrub(got); second != got {
				t.Fatal("scrub changed a redacted projection")
			}
		}
	}
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	if ScrubProviderLabel(trace) != trace {
		t.Fatal("trace identity lost")
	}
}

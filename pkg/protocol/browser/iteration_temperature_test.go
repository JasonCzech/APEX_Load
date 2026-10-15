package browser

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A session's first iteration in a browser context is cold and the rest warm;
// a new context starts cold again. A session that starts every iteration
// afresh, or has no context, reports nothing.
func TestBrowserIterationTemperature(t *testing.T) {
	b := &Browser{newUserPolicy: "persistent_context"}
	b.setBrowserContext(&harCloseFixtureContext{close: func() error { return nil }})
	for i, want := range []string{protocol.IterationCold, protocol.IterationWarm, protocol.IterationWarm} {
		if got := b.IterationTemperature(); got != want {
			t.Fatalf("iteration %d = %q, want %q", i+1, got, want)
		}
	}
	b.setBrowserContext(&harCloseFixtureContext{close: func() error { return nil }})
	if got := b.IterationTemperature(); got != protocol.IterationCold {
		t.Fatalf("first iteration in a new context = %q", got)
	}
	b.newUserPolicy = "cookies_each_iteration"
	if got := b.IterationTemperature(); got != protocol.IterationWarm {
		t.Fatalf("cleared cookies with a kept cache = %q, want warm", got)
	}
	for _, policy := range []string{"context_each_iteration", "cookies_and_cache_each_iteration"} {
		b.newUserPolicy = policy
		if got := b.IterationTemperature(); got != "" {
			t.Fatalf("%s = %q, want untagged", policy, got)
		}
	}
	b.newUserPolicy = "persistent_context"
	b.setBrowserContext(nil)
	if got := b.IterationTemperature(); got != "" {
		t.Fatalf("no context = %q, want untagged", got)
	}
}

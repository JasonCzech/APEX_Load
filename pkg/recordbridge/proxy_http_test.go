package recordbridge

import (
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/recorder"
)

func TestProxySessionScrubSensitiveLifecycle(t *testing.T) {
	const (
		starting = "scrub-lifecycle-starting"
		settled  = "scrub-lifecycle-settled"
		evicted  = "scrub-lifecycle-evicted"
		failed   = "scrub-lifecycle-failed"
		optedOut = "scrub-lifecycle-opted-out"
	)
	t.Cleanup(func() {
		for _, id := range []string{starting, settled, evicted, failed, optedOut} {
			DeleteProxySessionScrubSensitive(id)
		}
	})
	scrubbed := func(id string) bool {
		return ProxySessionScrubSensitive(&recorder.Session{ID: id})
	}

	BeginProxySessionScrubSensitive(starting, true)
	BeginProxySessionScrubSensitive(settled, true)
	CommitProxySessionScrubSensitive(settled)
	BeginProxySessionScrubSensitive(evicted, true)
	CommitProxySessionScrubSensitive(evicted)
	BeginProxySessionScrubSensitive(failed, true)
	DeleteProxySessionScrubSensitive(failed)
	BeginProxySessionScrubSensitive(optedOut, false)

	if !scrubbed(starting) {
		t.Fatal("a starting session's opt-in must be visible before the recorder lists it")
	}
	if scrubbed(failed) {
		t.Fatal("a failed start must not leave an opt-in behind")
	}
	if scrubbed(optedOut) {
		t.Fatal("a session that did not opt in must not be scrubbed")
	}

	// The recorder retains only the settled session: the evicted one is
	// pruned, while the still-starting one survives because the recorder does
	// not list it yet.
	PruneProxySessionScrubSensitive([]string{settled, " "})
	if !scrubbed(settled) {
		t.Fatal("prune dropped a retained session's opt-in")
	}
	if scrubbed(evicted) {
		t.Fatal("prune kept an evicted session's opt-in")
	}
	if !scrubbed(starting) {
		t.Fatal("prune dropped an in-flight start's opt-in")
	}

	// A stale snapshot captured at an earlier generation must not prune a
	// session that committed after that snapshot was taken.
	staleGen := ProxySessionScrubGeneration()
	CommitProxySessionScrubSensitive(starting)
	PruneProxySessionScrubSensitiveBefore(staleGen, nil)
	if !scrubbed(starting) {
		t.Fatal("stale-generation prune dropped a session committed after the snapshot")
	}

	PruneProxySessionScrubSensitive(nil)
	if scrubbed(starting) || scrubbed(settled) {
		t.Fatal("prune with no retained sessions must drop every settled opt-in")
	}
	if ProxySessionScrubSensitive(nil) || scrubbed("") {
		t.Fatal("nil or blank sessions must never report an opt-in")
	}
}

func TestShouldScrubRecorderSessionMatrix(t *testing.T) {
	const optedIn = "should-scrub-opted-in"
	SetProxySessionScrubSensitive(optedIn, true)
	t.Cleanup(func() { DeleteProxySessionScrubSensitive(optedIn) })
	plain := &recorder.Session{ID: "should-scrub-plain"}
	opted := &recorder.Session{ID: optedIn}

	for _, tc := range []struct {
		name         string
		query        string
		session      *recorder.Session
		canExportRaw bool
		want         bool
	}{
		{"default raw for editors", "", plain, true, false},
		{"viewer without raw access is scrubbed", "", plain, false, true},
		{"explicit scrub wins", "?scrub=true", plain, true, true},
		{"opted-in session scrubs by default", "", opted, true, true},
		{"explicit raw overrides the opt-in", "?raw=true", opted, true, false},
		{"explicit scrub beats explicit raw", "?raw=true&scrub=true", opted, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/recorder/x"+tc.query, nil)
			if got := ShouldScrubRecorderSession(r, tc.session, tc.canExportRaw); got != tc.want {
				t.Fatalf("ShouldScrubRecorderSession = %v, want %v", got, tc.want)
			}
		})
	}
}

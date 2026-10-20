package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func expireHARFinalization(t *testing.T, o *harCaptureOwner) {
	t.Helper()
	o.mu.Lock()
	o.finalizationDeadline = time.Now().Add(-time.Second)
	o.mu.Unlock()
}

func TestHARFinalizationUsesOneDeadlineAcrossPhases(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	t.Cleanup(o.finishFinalizationBudget)
	writeOwnedHARFixture(t, o)
	before := time.Now()
	o.startDocumentJoin(func() {})
	deadline := o.startFinalizationBudget()
	if deadline.Before(before.Add(runtimeHARFinalizationBudget)) || deadline.After(time.Now().Add(runtimeHARFinalizationBudget)) {
		t.Fatalf("deadline not 30 seconds from first phase: %s", deadline)
	}
	o.startRuntimeJoin(func() {})
	o.startClose()
	o.startFinish(true, nil)
	if got := o.startFinalizationBudget(); !got.Equal(deadline) {
		t.Fatal("phase restarted finalization budget")
	}
	artifact, err := o.waitFinish(context.Background())
	if err != nil || artifact == "" || !o.retiredSuccessfully() {
		t.Fatalf("within-budget capture: %q %v", artifact, err)
	}
	// A later retry cannot retroactively expire already qualified publication.
	expireHARFinalization(t, o)
	if err := o.finalizationBudgetError(); err != nil {
		t.Fatal("published artifact expired", err)
	}
	if got, err := o.waitFinish(context.Background()); got != artifact || err != nil {
		t.Fatalf("retry changed publication: %q %v", got, err)
	}
}

// An overflow or memory-watchdog close happens mid-session; the VUser keeps
// running until teardown, which alone starts the finalization deadline.
func TestHARFinalizationBudgetStartsAtTeardownNotAtAResourceLimitClose(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	t.Cleanup(o.finishFinalizationBudget)
	writeOwnedHARFixture(t, o)
	o.startClose()
	<-o.closeDone
	o.mu.Lock()
	started := !o.finalizationDeadline.IsZero()
	o.mu.Unlock()
	if started {
		t.Fatal("mid-session close started the finalization budget")
	}
	before := time.Now()
	o.startFinish(true, nil)
	if deadline := o.startFinalizationBudget(); deadline.Before(before.Add(runtimeHARFinalizationBudget)) {
		t.Fatalf("teardown deadline %s predates finalization", deadline)
	}
	if artifact, err := o.waitFinish(context.Background()); err != nil || artifact == "" {
		t.Fatalf("finalization after an earlier close: %q %v", artifact, err)
	}
}

func TestHARFinalizationExpiredJoinRetainsOwnershipUntilProof(t *testing.T) {
	for _, phase := range []string{"writer", "document", "runtime"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			o := fixtureHAROwner(t, dir)
			t.Cleanup(o.finishFinalizationBudget)
			writeOwnedHARFixture(t, o)
			gate := make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate) }) }
			defer unblock()
			switch phase {
			case "writer":
				if err := o.beginWriter(); err != nil {
					t.Fatal(err)
				}
				o.bindWriter(func() error { <-gate; return nil })
			case "document":
				o.startDocumentJoin(func() { <-gate })
			case "runtime":
				o.startRuntimeJoin(func() { <-gate })
			}
			expireHARFinalization(t, o)
			o.startFinish(true, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			artifact, err := o.waitFinish(ctx)
			if artifact != "" || !errors.Is(err, errHARFinalizationBudget) || !errors.Is(err, errHAROwnershipUnproven) {
				t.Fatalf("expired unjoined capture: %q %v", artifact, err)
			}
			if o.retiredSuccessfully() {
				t.Fatal("timeout invented join proof")
			}
			if _, err := os.Stat(o.path); err != nil {
				t.Fatal("unjoined raw removed", err)
			}
			runtimeHAROwners.Lock()
			charged := runtimeHAROwners.owners[o]
			runtimeHAROwners.Unlock()
			if !charged {
				t.Fatal("timeout released owner admission charge")
			}
			unblock()
			artifact, err = o.waitFinish(context.Background())
			if artifact != "" || !errors.Is(err, errHARFinalizationBudget) || !o.retiredSuccessfully() {
				t.Fatalf("late join: %q %v retired=%v", artifact, err, o.retiredSuccessfully())
			}
			if _, err := os.Stat(o.path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("joined raw not retired", err)
			}
			matches, _ := filepath.Glob(filepath.Join(dir, "*_scrubbed.har"))
			if len(matches) != 0 {
				t.Fatal("expired capture became discoverable", matches)
			}
		})
	}
}

func TestHARFinalizationExpiryDuringScrubRefusesPublication(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	t.Cleanup(o.finishFinalizationBudget)
	writeOwnedHARFixture(t, o)
	o.startFinish(true, func(value string) string { expireHARFinalization(t, o); return value })
	artifact, err := o.waitFinish(context.Background())
	if artifact != "" || !errors.Is(err, errHARFinalizationBudget) || !o.retiredSuccessfully() {
		t.Fatalf("expired scrub: %q %v", artifact, err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*_scrubbed.har"))
	if len(matches) != 0 {
		t.Fatal("expired scrub published", matches)
	}
}

func TestHARFinalizationExpiryAfterLinkRemovesOnlyOwnedPublication(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "own link", true: "foreign replacement"}[replacement], func(t *testing.T) {
			dir := t.TempDir()
			o := fixtureHAROwner(t, dir)
			t.Cleanup(o.finishFinalizationBudget)
			writeOwnedHARFixture(t, o)
			final := filepath.Join(dir, "apexload_browser_"+o.generation+"_scrubbed.har")
			harFinalizeCheckpoint = func(stage string) {
				if stage != "publish" {
					return
				}
				if replacement {
					if err := os.Remove(final); err != nil {
						panic(err)
					}
					if err := os.WriteFile(final, []byte("foreign artifact"), 0600); err != nil {
						panic(err)
					}
				}
				expireHARFinalization(t, o)
			}
			defer func() { harFinalizeCheckpoint = nil }()
			artifact, err := finishFixtureHAR(t, o, true)
			if artifact != "" || !errors.Is(err, errHARFinalizationBudget) {
				t.Fatalf("expired publication: %q %v", artifact, err)
			}
			if !o.retiredSuccessfully() {
				t.Fatal("joined owner not retired")
			}
			data, readErr := os.ReadFile(final)
			if replacement {
				if string(data) != "foreign artifact" || readErr != nil || !errors.Is(err, errHAROwnershipUnproven) {
					t.Fatalf("foreign artifact changed: %q %v %v", data, readErr, err)
				}
			} else if !errors.Is(readErr, os.ErrNotExist) {
				t.Fatal("expired final link survived", readErr)
			}
		})
	}
}

func TestHARFinalizationReportsExhaustionOnceWhileWriterIsBlocked(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	t.Cleanup(o.finishFinalizationBudget)
	writeOwnedHARFixture(t, o)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	if err := o.beginWriter(); err != nil {
		t.Fatal(err)
	}
	o.bindWriter(func() error { <-gate; return nil })
	o.mu.Lock()
	o.finalizationDeadline = time.Now().Add(20 * time.Millisecond)
	o.mu.Unlock()
	before := BrowserEvidenceStatsSnapshot().HARFinalizationExhausted
	o.startFinish(true, nil)
	limit := time.Now().Add(time.Second)
	for BrowserEvidenceStatsSnapshot().HARFinalizationExhausted == before && time.Now().Before(limit) {
		time.Sleep(time.Millisecond)
	}
	if BrowserEvidenceStatsSnapshot().HARFinalizationExhausted != before+1 {
		t.Fatal("blocked writer did not report expiry")
	}
	for i := 0; i < 3; i++ {
		if !errors.Is(o.finalizationBudgetError(), errHARFinalizationBudget) {
			t.Fatal("expired budget not retained")
		}
	}
	if BrowserEvidenceStatsSnapshot().HARFinalizationExhausted != before+1 {
		t.Fatal("exhaustion reported more than once")
	}
	release.Do(func() { close(gate) })
	if _, err := o.waitFinish(context.Background()); !errors.Is(err, errHARFinalizationBudget) {
		t.Fatal(err)
	}
}

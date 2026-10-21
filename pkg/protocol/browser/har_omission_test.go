package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHARCaptureOmissionPolicyAndSingleHandoff(t *testing.T) {
	for _, tc := range []struct {
		name, body, policy string
		omitted            bool
	}{{"valid", ownedHARFixture, HARCaptureAlways, false}, {"invalid", "broken", HARCaptureAlways, true}, {"discard", "broken", HARCaptureOff, false}} {
		t.Run(tc.name, func(t *testing.T) {
			owner := fixtureHAROwner(t, t.TempDir())
			if err := os.WriteFile(owner.path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			b := &Browser{harOwner: owner, harCapturePolicy: tc.policy}
			_, err := b.finalizeHARCaptureContext(context.Background())
			if tc.omitted && err == nil {
				t.Fatal("expected finalization failure")
			}
			got := b.TakeBrowserHARCaptureOmissions()
			if (got[protocol.BrowserHAROmittedCaptureFailed] == 1) != tc.omitted {
				t.Fatalf("counts %v err=%v", got, err)
			}
			if got := b.TakeBrowserHARCaptureOmissions(); len(got) != 0 {
				t.Fatal("repeated take", got)
			}
			b.noteHARCaptureOmission(owner, protocol.BrowserHAROmittedCaptureFailed)
			if tc.omitted && len(b.TakeBrowserHARCaptureOmissions()) != 0 {
				t.Fatal("same owner counted twice")
			}
		})
	}
}
func TestHARCaptureOmissionConcurrentOwnerAndFailureClassification(t *testing.T) {
	b := &Browser{}
	owner := &harCaptureOwner{}
	var wg sync.WaitGroup
	for n := 0; n < 32; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.reportHARFinalizationFailure(owner, errors.New("fixture close failure")) }()
	}
	wg.Wait()
	if got := b.TakeBrowserHARCaptureOmissions(); len(got) != 1 || got[protocol.BrowserHAROmittedOwnerUnretired] != 1 {
		t.Fatal(got)
	}
	b.reportHARFinalizationFailure(&harCaptureOwner{retired: true}, errHARFinalizationBudget)
	if got := b.TakeBrowserHARCaptureOmissions(); len(got) != 1 || got[protocol.BrowserHAROmittedFinalizationBudget] != 1 {
		t.Fatal(got)
	}
	if owner.retiredSuccessfully() {
		t.Fatal("reporting released physical ownership")
	}
}
func TestHARCaptureNativeRetentionFailureReportsLoss(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>owned failure fixture</title><h1>Owned capture fixture</h1>")
	}))
	defer target.Close()
	dir := t.TempDir()
	for n := 0; n <= maxRuntimeHARRetentionScanEntries; n++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("foreign-%04d", n)), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := &Browser{}
	if err := b.Init(ctx, map[string]any{"base_url": target.URL, "browser_engine": "chromium", "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": dir}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err := b.Teardown(ctx); err == nil {
		t.Fatal("retention scan unexpectedly completed")
	}
	if len(b.TakeBrowserHARArtifacts()) != 0 {
		t.Fatal("failed capture published an artifact")
	}
	if got := b.TakeBrowserHARCaptureOmissions(); len(got) != 1 || got[protocol.BrowserHAROmittedCaptureFailed] != 1 {
		t.Fatal(got)
	}
	_ = b.Teardown(ctx)
	if len(b.TakeBrowserHARCaptureOmissions()) != 0 {
		t.Fatal("teardown repeated omission")
	}
	for n := 0; n <= maxRuntimeHARRetentionScanEntries; n++ {
		body, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("foreign-%04d", n)))
		if err != nil || string(body) != "preserve" {
			t.Fatal("foreign file changed", n, err)
		}
	}
}

func TestHARCapturePublicationQueueReportsLossAndKeepsOwnedFile(t *testing.T) {
	owner := fixtureHAROwner(t, t.TempDir())
	writeOwnedHARFixture(t, owner)
	b := &Browser{harOwner: owner, harCapturePolicy: HARCaptureAlways, publishedHARArtifacts: make([]protocol.BrowserHARArtifact, protocol.MaxBrowserHARArtifactsPerRun)}
	if err := b.closeWithContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := b.TakeBrowserHARCaptureOmissions(); len(got) != 1 || got[protocol.BrowserHAROmittedPublicationLimit] != 1 {
		t.Fatal(got)
	}
	if len(b.TakeBrowserHARArtifacts()) != protocol.MaxBrowserHARArtifactsPerRun {
		t.Fatal("queue bound changed")
	}
	if b.lastHARArtifactPath == "" {
		t.Fatal("queue loss erased retained artifact path")
	}
	if _, err := os.Stat(b.lastHARArtifactPath); err != nil {
		t.Fatal("queue loss removed owned retained bytes", err)
	}
}

// Successful physical joins do not prove namespace retirement. The diagnostic
// must describe the retained owner without claiming its writers are still live.
func TestHARCaptureOmissionAfterJoinRetainsUnretiredOwner(t *testing.T) {
	done := make(chan struct{})
	close(done)
	owner := &harCaptureOwner{closeDone: done, runtimeDone: done, documentDone: done}
	b := &Browser{}
	b.reportHARFinalizationFailure(owner, errors.New("retirement identity changed"))
	if got := b.TakeBrowserHARCaptureOmissions(); len(got) != 1 || got[protocol.BrowserHAROmittedOwnerUnretired] != 1 {
		t.Fatal(got)
	}
	if owner.retiredSuccessfully() {
		t.Fatal("reporting retired an unproven owner")
	}
}

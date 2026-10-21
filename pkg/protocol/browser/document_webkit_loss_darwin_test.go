//go:build darwin

package browser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// Run in a separate test process: SIGKILL terminates its owned native WebKit
// process, including every context it owns. The fixture must never
// terminate a browser used by another test or an operator's browser.
func TestBrowserDocumentNativeWebKitLossPreservesContextsAndReplacesPool(t *testing.T) {
	const childFlag = "APEX_DOCUMENT_WEBKIT_LOSS_CHILD"
	if os.Getenv(childFlag) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-test.timeout=70s")
		// Bound pipe draining too: a timed-out child must not leave this test
		// waiting on a driver descendant that inherited an output handle.
		child.WaitDelay = 5 * time.Second
		child.Env = append(os.Environ(), childFlag+"=1")
		output, err := child.CombinedOutput()
		t.Logf("isolated native browser loss:\n%s", output)
		if err != nil {
			t.Fatalf("native browser loss subprocess: %v (deadline: %v)", err, ctx.Err())
		}
		if strings.Contains(string(output), "--- SKIP: "+t.Name()+" ") {
			t.Skip("native browser runtime unavailable in isolated subprocess")
		}
		if !strings.Contains(string(output), "--- PASS: "+t.Name()+" ") {
			t.Fatal("native browser subprocess exited without completing the fixture")
		}
		return
	}

	desktop, _, desktopBefore := documentFailureFixture(t, "webkit", "")
	mobile, _, mobileBefore := documentFailureFixture(t, "webkit", "iphone_15_pro")
	native := desktop.currentBrowserContext().Browser()
	if native == nil || native != mobile.currentBrowserContext().Browser() {
		t.Fatal("fixture contexts must share the same owned browser process")
	}
	disconnected := make(chan struct{}, 1)
	native.OnDisconnected(func(playwright.Browser) {
		select {
		case disconnected <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() {
		if !ShutdownPool(5 * time.Second) {
			t.Error("isolated WebKit pool did not close within its shutdown budget")
		}
	})
	killOwnedWebKitProcess(t)
	select {
	case <-disconnected:
	case <-time.After(10 * time.Second):
		t.Fatal("native browser disconnect event not observed")
	}
	if native.IsConnected() {
		t.Fatal("crashed native browser remains connected")
	}
	// Playwright reports process loss as native page/context closure. Do not
	// invent a renderer-crash boundary or successful document finalization.
	desktopAfter := assertDocumentFailurePopulation(t, desktop, desktopBefore, "context_closed")
	mobileAfter := assertDocumentFailurePopulation(t, mobile, mobileBefore, "context_closed")

	replacement, replacementCtx, _ := documentFailureFixture(t, "webkit", "")
	fresh := replacement.currentBrowserContext().Browser()
	if fresh == native || !fresh.IsConnected() {
		t.Fatal("the pool reused its disconnected browser instead of launching a replacement")
	}
	oldGenerations := map[string]bool{}
	for _, population := range []map[string]protocol.BrowserDocumentObservation{desktopBefore, mobileBefore} {
		for _, o := range population {
			oldGenerations[o.SourceGeneration] = true
		}
	}
	if len(oldGenerations) != 2 {
		t.Fatal("shared-browser contexts did not retain separate source generations")
	}
	freshEvidence := replacement.BrowserDocumentEvidence()
	if oldGenerations[freshEvidence.SourceGeneration] || freshEvidence.SourceGeneration == "" {
		t.Fatal("replacement browser adopted a lost source generation")
	}
	for _, o := range freshEvidence.Observations {
		if o.SourceGeneration != freshEvidence.SourceGeneration {
			t.Fatal("replacement browser mixed observations from another generation")
		}
	}
	for _, tc := range []struct {
		name          string
		browser       *Browser
		before, after map[string]protocol.BrowserDocumentObservation
	}{{"desktop", desktop, desktopBefore, desktopAfter}, {"iphone_15_pro", mobile, mobileBefore, mobileAfter}} {
		if err := tc.browser.Teardown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := assertDocumentFailurePopulation(t, tc.browser, tc.before, "context_closed"); !reflect.DeepEqual(got, tc.after) {
			t.Fatalf("pool replacement or later teardown revised the lost %s population", tc.name)
		}
	}
	var title string
	if err := replacement.evaluatePageExpression(replacementCtx, "document.title", &title); err != nil || title != "failure evidence" || !fresh.IsConnected() {
		t.Fatalf("lost-context teardown damaged the replacement: title=%q err=%v connected=%t", title, err, fresh.IsConnected())
	}
	t.Log("native WebKit browser process loss: two contexts censored; final observations preserved; fresh pool replacement observed; old populations unchanged; replacement survives lost-context teardown")
}

// Only the isolated child's driver descendant at the installed WebKit's exact
// executable path may be killed. Matching an executable name alone could target
// an operator or another test's browser. Unknown/ambiguous topology fails closed.
func killOwnedWebKitProcess(t *testing.T) {
	t.Helper()
	globalMutex.Lock()
	if globalPlaywright == nil {
		globalMutex.Unlock()
		t.Fatal("missing isolated driver")
	}
	driver := globalPlaywright.Pid()
	expected := filepath.Join(filepath.Dir(globalPlaywright.WebKit.ExecutablePath()), "Playwright.app", "Contents", "MacOS", "Playwright")
	globalMutex.Unlock()
	if driver <= 0 || driver == os.Getpid() {
		t.Fatal("invalid isolated driver PID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	samples, err := listProcessTreeSamples(ctx, driver)
	if err != nil {
		t.Fatal(err)
	}
	parents := make(map[int]int, len(samples))
	for _, sample := range samples {
		parents[sample.PID] = sample.PPID
	}
	if parents[driver] != os.Getpid() {
		t.Fatal("driver is not owned by this isolated test process")
	}
	owned := func(pid int) bool {
		seen := map[int]bool{}
		for pid > 0 && !seen[pid] {
			if pid == driver {
				return true
			}
			seen[pid] = true
			pid = parents[pid]
		}
		return false
	}
	var candidates []int
	for _, sample := range samples {
		if sample.PID == driver || !owned(sample.PID) {
			continue
		}
		out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(sample.PID), "-o", "comm=").Output()
		if err == nil && strings.TrimSpace(string(out)) == expected {
			candidates = append(candidates, sample.PID)
		}
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one owned native WebKit executable; found %d", len(candidates))
	}
	pid := candidates[0]
	// Recheck both executable and immediate parent immediately before signalling.
	out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=,comm=").Output()
	fields := strings.Fields(string(out))
	if err != nil || len(fields) < 2 || fields[0] != strconv.Itoa(parents[pid]) || strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), fields[0])) != expected {
		t.Fatalf("owned native WebKit process changed before signal: %v", err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	t.Log("SIGKILL sent to exact native WebKit executable under the isolated Playwright driver")
}

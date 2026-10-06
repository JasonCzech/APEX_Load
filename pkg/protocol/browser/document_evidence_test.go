package browser

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func validBrowserDocumentPayloadMap(t *testing.T) map[string]any {
	t.Helper()
	payload := browserDocumentPayload{Kind: "begin", DocumentToken: "0123456789abcdef0123456789abcdef", LifecycleToken: "0123456789abcdef0123456789abcdef", TimeOrigin: 1710000000000, Offset: 0, State: "provisional", Boundary: "progress"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("fixture decode")
	}
	return fields
}
func TestBrowserDocumentBindingRejectsUnboundedObjectsBeforeMarshal(t *testing.T) {
	if _, ok := decodeBrowserDocumentPayload(validBrowserDocumentPayloadMap(t)); !ok {
		t.Fatal("native flat envelope refused")
	}
	for name, change := range map[string]func(map[string]any){
		"nested":  func(m map[string]any) { m["value"] = map[string]any{"nested": strings.Repeat("x", 1<<20)} },
		"cycle":   func(m map[string]any) { m["value"] = m },
		"large":   func(m map[string]any) { m["kind"] = strings.Repeat("x", 1<<20) },
		"unknown": func(m map[string]any) { delete(m, "state"); m["extra"] = true },
		"missing": func(m map[string]any) { delete(m, "restored") },
		"nan":     func(m map[string]any) { m["offset"] = math.NaN() },
		"typed":   func(m map[string]any) { m["restored"] = "false" },
	} {
		t.Run(name, func(t *testing.T) {
			fields := validBrowserDocumentPayloadMap(t)
			change(fields)
			if _, ok := decodeBrowserDocumentPayload(fields); ok {
				t.Fatal("malformed binding envelope admitted")
			}
		})
	}
}

func TestBrowserDocumentBindingAdmitsPinnedSDKIntegralAndFractionalPrimitives(t *testing.T) {
	fields := validBrowserDocumentPayloadMap(t)
	fields["time_origin"] = int(1710000000000)
	fields["offset"] = int(0)
	fields["interaction_count"] = int(0)
	fields["value"] = int(0)
	payload, ok := decodeBrowserDocumentPayload(fields)
	if !ok || payload.Value == nil || *payload.Value != 0 {
		t.Fatal("pinned SDK integral zero refused")
	}
	fields["offset"] = float64(0.5)
	fields["value"] = float64(0.1)
	payload, ok = decodeBrowserDocumentPayload(fields)
	if !ok || payload.Offset != 0.5 || payload.Value == nil || *payload.Value != 0.1 {
		t.Fatal("pinned SDK fractional primitive refused")
	}
	fields["offset"] = "0"
	if _, ok := decodeBrowserDocumentPayload(fields); ok {
		t.Fatal("typed target string admitted as numeric primitive")
	}
}

type browserGenerationUntouchedFrame struct{ playwright.Frame }
type browserGenerationUntouchedPage struct{ playwright.Page }
type browserGenerationUntouchedRequest struct{ playwright.Request }

func TestBrowserEvidenceOldContextCallbacksCannotRebindNewGeneration(t *testing.T) {
	old, newGeneration := "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"
	page := &browserGenerationUntouchedPage{}
	request := &browserGenerationUntouchedRequest{}
	b := &Browser{pwPage: page, pages: []playwright.Page{page}, networkTasks: make(chan browserNetworkTask, 1), networkWorkerStop: make(chan struct{}), evidence: browserEvidenceState{generation: newGeneration, issued: 1, inFlight: 1, requests: map[playwright.Request]*browserRequestIdentity{request: {}}, frames: map[playwright.Frame]string{}, documents: map[string]*browserDocumentState{}}}
	source := &playwright.BindingSource{Frame: &browserGenerationUntouchedFrame{}, Page: page}
	if b.receiveDocumentEvidence(source, validBrowserDocumentPayloadMap(t), old) {
		t.Fatal("old binding rebound new document generation")
	}
	b.beginRequestEvidence(request, old)
	if b.completeRequestEvidence(request, true, old) != nil {
		t.Fatal("old completion accepted")
	}
	b.enqueueNetworkRequest(request, true, old)
	// A stale task must be rejected before even touching its native SDK request.
	b.recordNetworkTask(browserNetworkTask{generation: old})
	b.registerPage(&browserGenerationUntouchedPage{}, true, old)
	b.appendConsoleEntry(protocol.ConsoleEntry{Text: "old context"}, old)
	b.rememberResourceValidator("https://example.invalid", map[string]string{"etag": "old"}, old)
	b.harLimitExceeded = make(chan struct{})
	b.noteHARRequest(request, old)
	b.noteHARBytes(4096, old)
	b.pendingDialog = &dialogPolicy{action: "accept", promptText: "current owned prompt"}
	if policy, admitted := b.takeDialogPolicy(old); admitted || policy != nil || b.pendingDialog == nil {
		t.Fatal("old context consumed new dialog policy")
	}
	if b.harObservedEntries != 0 || b.harObservedBytes != 0 {
		t.Fatal("old callback mutated new HAR protection counters")
	}
	if len(b.evidence.documents) != 0 || len(b.evidence.frames) != 0 || b.evidence.issued != 1 || b.evidence.inFlight != 1 || b.evidence.requests[request].finished || len(b.networkTasks) != 0 || len(b.networkEntries) != 0 || len(b.consoleEntries) != 0 || len(b.resourceValidators) != 0 || b.currentPage() != page {
		t.Fatal("old callback mutated admitted new context")
	}
	if policy, admitted := b.takeDialogPolicy(newGeneration); !admitted || policy == nil || policy.promptText != "current owned prompt" || b.pendingDialog != nil {
		t.Fatal("admitted native context could not consume its one-shot policy")
	}
}

func TestBrowserDocumentCohortReportsEffectiveNativeCacheSetting(t *testing.T) {
	for name, change := range map[string]func(*Browser){"unrouted": func(b *Browser) {}, "download": func(b *Browser) { b.downloadResources = "none" }, "egress": func(b *Browser) { b.allowedTargetHosts = []string{"example.invalid"} }, "revalidate": func(b *Browser) { b.checkNewerVersions = true }, "disabled": func(b *Browser) { b.cacheEnabled = false }} {
		t.Run(name, func(t *testing.T) {
			b := &Browser{}
			b.applyDefaultConfig()
			change(b)
			if err := b.resetBrowserEvidence("native-test-version"); err != nil {
				t.Fatal(err)
			}
			cache := b.evidence.cohort.CacheEnabled
			if cache == nil || *cache != (name == "unrouted") {
				t.Fatal("requested cache setting masqueraded as effective native cache")
			}
		})
	}
}

type browserFinalizationHeldFrame struct {
	playwright.Frame
	entered, release, exited chan struct{}
}

func (f *browserFinalizationHeldFrame) Evaluate(string, ...any) (any, error) {
	close(f.entered)
	defer close(f.exited)
	<-f.release
	return nil, errors.New("owned context closed")
}

type browserFinalizationOwnedContext struct {
	playwright.BrowserContext
	frame     *browserFinalizationHeldFrame
	closeOnce sync.Once
	calls     atomic.Int64
	joined    chan struct{}
}

func (c *browserFinalizationOwnedContext) Close(...playwright.BrowserContextCloseOptions) error {
	c.calls.Add(1)
	c.closeOnce.Do(func() { close(c.frame.release); <-c.frame.exited; close(c.joined) })
	return nil
}
func TestBrowserDocumentFinalizePhysicallyClosesAndJoinsOwnedNativeWorkers(t *testing.T) {
	frame := &browserFinalizationHeldFrame{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	owned := &browserFinalizationOwnedContext{frame: frame, joined: make(chan struct{})}
	b := &Browser{pwContext: owned, evidence: browserEvidenceState{finalizer: "owned-finalizer", lifecycleContext: context.Background(), documents: map[string]*browserDocumentState{"owned": {frame: frame, page: &browserGenerationUntouchedPage{}, observations: map[string]protocol.BrowserDocumentObservation{}}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); b.finalizeDocumentEvidence(ctx) }()
	select {
	case <-frame.entered:
	case <-time.After(time.Second):
		t.Fatal("native drain never started")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("native drain did not physically join on cancellation")
	}
	if owned.calls.Load() != 1 {
		t.Fatal("owned context cancellation cleanup duplicated or absent")
	}
	select {
	case <-frame.exited:
	default:
		t.Fatal("native evaluate worker survived finalizer")
	}
	select {
	case <-owned.joined:
	default:
		t.Fatal("owned close callback survived finalizer")
	}
}

type browserEvidenceOptInContext struct {
	playwright.BrowserContext
	bindings, scripts int
}

func (c *browserEvidenceOptInContext) ExposeBinding(_ string, _ playwright.BindingCallFunction) error {
	c.bindings++
	return nil
}
func (c *browserEvidenceOptInContext) AddInitScript(playwright.Script) error { c.scripts++; return nil }
func TestBrowserDocumentExplicitOptInOwnsObserverAndBindingInstallation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		b := &Browser{}
		b.applyDefaultConfig()
		b.documentMetricsEnabled = enabled
		owned := &browserEvidenceOptInContext{}
		if err := b.installDocumentEvidence(owned, "actual-version"); err != nil {
			t.Fatal(err)
		}
		if b.evidence.generation == "" {
			t.Fatal("passive request identity lost")
		}
		want := 0
		if enabled {
			want = 1
		}
		if owned.bindings != want || owned.scripts != want {
			t.Fatal("document observers installed without explicit opt-in")
		}
	}
}

func TestBrowserDocumentHARUsageAndThresholdBelongToOneGeneration(t *testing.T) {
	generation := "0123456789abcdef0123456789abcdef"
	old := "fedcba9876543210fedcba9876543210"
	b := &Browser{evidence: browserEvidenceState{generation: generation}, harLimitExceeded: make(chan struct{})}
	b.noteHARBytes(maxRuntimeHARBytes+1, old)
	b.noteHARNetworkUsage(maxRuntimeHARBytes+1, old)
	b.closeOversizedHARContext(old)
	select {
	case <-b.harLimitExceeded:
		t.Fatal("old source closed current protected HAR")
	default:
	}
	if b.harObservedBytes != 0 || b.harObservedEntries != 0 || b.harErrorSeen {
		t.Fatal("old source changed current HAR admission")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); b.noteHARBytes(maxRuntimeHARBytes+1, generation) }()
	}
	workers.Wait()
	select {
	case <-b.harLimitExceeded:
	default:
		t.Fatal("current owned source failed to close its exceeded protection signal")
	}
}

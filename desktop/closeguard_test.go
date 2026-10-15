package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

type closeGuardHarness struct {
	mu       sync.Mutex
	emitted  []string
	quits    int
	quitDone chan struct{}
}

func newCloseGuardApp(t *testing.T, ackWait time.Duration) (*App, *closeGuardHarness) {
	t.Helper()
	app := NewApp()
	harness := &closeGuardHarness{quitDone: make(chan struct{}, 4)}
	app.closeAckWait = ackWait
	app.emitCloseRequest = func(_ context.Context, id string) {
		harness.mu.Lock()
		harness.emitted = append(harness.emitted, id)
		harness.mu.Unlock()
	}
	app.quitApp = func(context.Context) {
		harness.mu.Lock()
		harness.quits++
		harness.mu.Unlock()
		harness.quitDone <- struct{}{}
	}
	app.setRuntimeContext(context.Background())
	return app, harness
}

func (h *closeGuardHarness) snapshot() ([]string, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.emitted...), h.quits
}

func waitForEmit(t *testing.T, h *closeGuardHarness, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if emitted, _ := h.snapshot(); len(emitted) >= want {
			return emitted
		}
		time.Sleep(5 * time.Millisecond)
	}
	emitted, _ := h.snapshot()
	t.Fatalf("emitted %v, want %d close requests", emitted, want)
	return nil
}

// Without a mounted renderer guard a close goes ahead at once.
func TestCloseIsNotHeldWithoutARendererGuard(t *testing.T) {
	app, harness := newCloseGuardApp(t, time.Hour)
	if app.holdCloseForRenderer(context.Background()) {
		t.Fatal("a close was held with no renderer guard")
	}
	token, err := app.EnableCloseGuard()
	if err != nil {
		t.Fatal(err)
	}
	app.DisableCloseGuard(token)
	if app.holdCloseForRenderer(context.Background()) {
		t.Fatal("a close was held after the guard was removed")
	}
	if emitted, quits := harness.snapshot(); len(emitted) != 0 || quits != 0 {
		t.Fatalf("emitted %v and quit %d times", emitted, quits)
	}
}

// A held close asks the renderer, which confirms once its work is resolved;
// the confirmed close then goes through.
func TestHeldCloseQuitsWhenTheRendererConfirms(t *testing.T) {
	app, harness := newCloseGuardApp(t, time.Hour)
	token, err := app.EnableCloseGuard()
	if err != nil {
		t.Fatal(err)
	}
	if !app.beforeClose(context.Background()) {
		t.Fatal("the close was not held for the renderer")
	}
	id := waitForEmit(t, harness, 1)[0]
	app.AcknowledgeCloseRequest(token, id)
	if err := app.ConfirmClose("another-token", id); err == nil {
		t.Fatal("a stale guard confirmed the close")
	}
	if err := app.ConfirmClose(token, id); err != nil {
		t.Fatal(err)
	}
	<-harness.quitDone
	if app.holdCloseForRenderer(context.Background()) {
		t.Fatal("the confirmed close was held again")
	}
}

// A renderer that keeps editing leaves the app open; an unacknowledged
// request (a hung or reloading page) quits after the wait.
func TestHeldCloseQuitsOnlyWhenTheRendererNeverTakesIt(t *testing.T) {
	app, harness := newCloseGuardApp(t, 20*time.Millisecond)
	token, err := app.EnableCloseGuard()
	if err != nil {
		t.Fatal(err)
	}
	if !app.holdCloseForRenderer(context.Background()) {
		t.Fatal("the close was not held")
	}
	app.AcknowledgeCloseRequest(token, waitForEmit(t, harness, 1)[0])
	time.Sleep(80 * time.Millisecond)
	if _, quits := harness.snapshot(); quits != 0 {
		t.Fatalf("quit %d times after the renderer took the request", quits)
	}

	if !app.holdCloseForRenderer(context.Background()) {
		t.Fatal("the second close was not held")
	}
	select {
	case <-harness.quitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("an unacknowledged close request never quit")
	}
}

// A reloaded page's registration replaces the old page's, whatever order the
// old page's late RendererUnready arrives in; a request id the shell never
// sent is refused.
func TestCloseGuardSurvivesAReloadRace(t *testing.T) {
	app, harness := newCloseGuardApp(t, time.Hour)
	if app.RendererReady("renderer-old-page") == "" {
		t.Fatal("renderer was not activated")
	}
	if _, err := app.EnableCloseGuard(); err != nil {
		t.Fatal(err)
	}
	// The new page registers before the old page's pagehide call lands.
	token, err := app.EnableCloseGuard()
	if err != nil {
		t.Fatal(err)
	}
	app.RendererUnready("renderer-old-page")
	if err := app.ConfirmClose(token, "7"); err == nil {
		t.Fatal("a request the shell never sent confirmed the close")
	}
	if !app.holdCloseForRenderer(context.Background()) {
		t.Fatal("the reloaded page's guard no longer held the close")
	}
	app.AcknowledgeCloseRequest(token, waitForEmit(t, harness, 1)[0])
}

// View > Reload asks a page holding a guard first: it reloads at once when no
// guard is registered, waits while the page answers, and reloads a page that
// never takes the request, dropping that page's registration but not a newer
// page's.
func TestReloadAsksTheRendererGuardFirst(t *testing.T) {
	// Wide enough for the acknowledgement below to land inside it on a busy machine.
	const ackWait = 200 * time.Millisecond
	app, harness := newCloseGuardApp(t, ackWait)
	reloaded := make(chan struct{}, 4)
	originalReload := windowReloadApp
	windowReloadApp = func(context.Context) { reloaded <- struct{}{} }
	t.Cleanup(func() { windowReloadApp = originalReload })
	app.emitReloadRequest = func(_ context.Context, id string) {
		harness.mu.Lock()
		harness.emitted = append(harness.emitted, id)
		harness.mu.Unlock()
	}
	waitForReload := func() {
		t.Helper()
		select {
		case <-reloaded:
		case <-time.After(2 * time.Second):
			t.Fatal("the page was not reloaded")
		}
	}
	ctx := context.Background()

	app.reloadWithRendererGuard(ctx)
	waitForReload()

	token, err := app.EnableCloseGuard()
	if err != nil {
		t.Fatal(err)
	}
	app.reloadWithRendererGuard(ctx)
	app.AcknowledgeCloseRequest(token, waitForEmit(t, harness, 1)[0])
	time.Sleep(2 * ackWait)
	select {
	case <-reloaded:
		t.Fatal("a reload the page took went ahead without its answer")
	default:
	}

	app.reloadWithRendererGuard(ctx)
	waitForEmit(t, harness, 2)
	waitForReload()
	if app.holdCloseForRenderer(ctx) {
		t.Fatal("a registration that left a reload unanswered still holds closes")
	}

	if _, err := app.EnableCloseGuard(); err != nil {
		t.Fatal(err)
	}
	app.reloadWithRendererGuard(ctx)
	newer, err := app.EnableCloseGuard()
	if err != nil {
		t.Fatal(err)
	}
	waitForReload()
	app.closeGuardMu.Lock()
	current := app.closeGuardToken
	app.closeGuardMu.Unlock()
	if current != newer {
		t.Fatal("an unanswered reload dropped the newer page's registration")
	}
	if _, quits := harness.snapshot(); quits != 0 {
		t.Fatalf("a reload quit the app %d times", quits)
	}
}

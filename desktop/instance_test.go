package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

const testRendererSession = "renderer-test-session"
const reloadedRendererSession = "renderer-reloaded-session"

func TestIsOpenableFile(t *testing.T) {
	cases := map[string]bool{
		"/x/order.json":      true,
		"/x/archive.zip":     true,
		"/x/project.USZ":     true,
		"/x/capture.HAR":     true, // case-insensitive
		"/x/notes.txt":       false,
		"/x/script.exe":      false,
		"":                   false,
		"apexload://run/123": false,
	}
	for path, want := range cases {
		if got := isOpenableFile(path); got != want {
			t.Errorf("isOpenableFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsApexloadURL(t *testing.T) {
	cases := map[string]bool{
		"apexload://run/123":  true,
		"APEXLOAD://open?x=1": true, // scheme is case-insensitive
		"https://example.com": false,
		"/x/order.json":       false,
		"":                    false,
		"file:///etc/passwd":  false,
	}
	for raw, want := range cases {
		if got := isApexloadURL(raw); got != want {
			t.Errorf("isApexloadURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestClassifyOpenArgs(t *testing.T) {
	files, urls := classifyOpenArgs([]string{
		"/Applications/APEX Load.app/Contents/MacOS/apexload",
		"--flag",
		"/Users/x/order.json",
		"apexload://run/abc",
		"/Users/x/notes.txt",
		"/Users/x/archive.zip",
	})
	wantFiles := []string{"/Users/x/order.json", "/Users/x/archive.zip"}
	wantURLs := []string{"apexload://run/abc"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Errorf("files = %v, want %v", files, wantFiles)
	}
	if !reflect.DeepEqual(urls, wantURLs) {
		t.Errorf("urls = %v, want %v", urls, wantURLs)
	}
}

func TestClassifyOpenArgsResolvesSecondInstanceWorkingDirectory(t *testing.T) {
	workingDir := filepath.Join(t.TempDir(), "launcher")
	files, urls := classifyOpenArgsFromDir([]string{
		"relative/order.json",
		"--config.json",
		"apexload://open/test-1",
	}, workingDir)
	wantFiles := []string{filepath.Join(workingDir, "relative/order.json")}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("files = %v, want %v", files, wantFiles)
	}
	if !reflect.DeepEqual(urls, []string{"apexload://open/test-1"}) {
		t.Fatalf("urls = %v", urls)
	}
}

func TestClassifyOpenArgsDecodesLocalFileURLs(t *testing.T) {
	files, urls := classifyOpenArgs([]string{
		"file:///tmp/APEX%20captures/order%20flow.har",
		"file://localhost/tmp/local.har",
		"file://fileserver/share/remote.har",
		"file:///tmp/not-a-har.txt",
		"apexload://run/test-1",
	})
	wantFiles := []string{"/tmp/APEX captures/order flow.har", "/tmp/local.har"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("files = %v, want %v", files, wantFiles)
	}
	if !reflect.DeepEqual(urls, []string{"apexload://run/test-1"}) {
		t.Fatalf("urls = %v", urls)
	}
}

func TestLocalFileURLPathWindowsDrive(t *testing.T) {
	got, ok := localFileURLPath("file:///C:/Users/Ada%20Lovelace/capture.har", "windows")
	if !ok || got != `C:\Users\Ada Lovelace\capture.har` {
		t.Fatalf("localFileURLPath = %q, %v", got, ok)
	}
	for _, raw := range []string{
		"file://server/share/capture.har",
		"file:relative/capture.har",
		"file:///tmp/capture.har?token=secret",
		"file:///tmp/capture.har#fragment",
	} {
		if got, ok := localFileURLPath(raw, "windows"); ok {
			t.Errorf("localFileURLPath(%q) accepted %q", raw, got)
		}
	}
}

func TestHandleOpenFileWithoutCtxIsNoop(t *testing.T) {
	app := NewApp()
	// Should not panic without a runtime context.
	app.handleOpenFile("/x/order.json")
	app.handleOpenURL("apexload://run/1")
}

func TestRendererReadyAtomicallyDrainsEarlyActivations(t *testing.T) {
	app := NewApp()
	file := filepath.Join(t.TempDir(), "order.json")
	app.handleOpenFile(file)
	app.handleOpenFile(file) // duplicate OS callbacks are coalesced
	app.handleOpenURL("apexload://run/1")
	app.handleOpenURL("apexload://run/1")

	var got pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &got); err != nil {
		t.Fatal(err)
	}
	wantEvents := []pendingRendererEvent{
		{Name: eventOpenFile, Payload: file},
		{Name: eventOpenURL, Payload: "apexload://run/1"},
	}
	if !reflect.DeepEqual(got.Events, wantEvents) {
		t.Fatalf("events = %+v, want %+v", got.Events, wantEvents)
	}
	if got.DeliveryID == "" {
		t.Fatal("non-empty activation batch is missing a delivery id")
	}

	var retry pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &retry); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retry, got) {
		t.Fatalf("unacknowledged retry = %+v, want %+v", retry, got)
	}

	var empty pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReadyAck(testRendererSession, got.DeliveryID)), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Events) != 0 {
		t.Fatalf("second drain should be empty, got %+v", empty)
	}
}

func TestRendererReadyDrainsEarlyMenuEventsInOrder(t *testing.T) {
	app := NewApp()
	app.menuEmit(menuEventOpenBrowser)(nil)
	app.menuAction("new-test")(nil)
	app.menuZoom("in")(nil)
	app.emitOrQueueRendererEvent(eventOpenRecent, "#/scripts/s1")

	var got pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &got); err != nil {
		t.Fatal(err)
	}
	want := []pendingRendererEvent{
		{Name: menuEventOpenBrowser},
		{Name: menuEventAction, Payload: "new-test"},
		{Name: menuEventZoom, Payload: "in"},
		{Name: eventOpenRecent, Payload: "#/scripts/s1"},
	}
	if !reflect.DeepEqual(got.Events, want) {
		t.Fatalf("events = %+v, want %+v", got.Events, want)
	}

	var empty pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReadyAck(testRendererSession, got.DeliveryID)), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Events) != 0 {
		t.Fatalf("second drain should not replay events, got %+v", empty.Events)
	}
}

func TestRendererReadyPreservesArrivalOrderAcrossActivationKinds(t *testing.T) {
	app := NewApp()
	file := filepath.Join(t.TempDir(), "capture.har")
	app.menuAction("open-script")(nil)
	app.handleOpenURL("apexload://run/older-link")
	app.handleOpenFile(file)
	app.menuAction("new-test")(nil)

	var got pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &got); err != nil {
		t.Fatal(err)
	}
	want := []pendingRendererEvent{
		{Name: menuEventAction, Payload: "open-script"},
		{Name: eventOpenURL, Payload: "apexload://run/older-link"},
		{Name: eventOpenFile, Payload: file},
		{Name: menuEventAction, Payload: "new-test"},
	}
	if !reflect.DeepEqual(got.Events, want) {
		t.Fatalf("mixed activation order = %+v, want %+v", got.Events, want)
	}
}

func TestEarlyMenuEventQueueIsBounded(t *testing.T) {
	app := NewApp()
	for i := 0; i < maxPendingRendererEvents+5; i++ {
		app.emitOrQueueRendererEvent(menuEventAction, fmt.Sprintf("action-%d", i))
	}

	var got pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != maxPendingRendererEvents {
		t.Fatalf("queued %d events, want %d", len(got.Events), maxPendingRendererEvents)
	}
	if got.Events[0].Payload != "action-5" || got.Events[len(got.Events)-1].Payload != "action-68" {
		t.Fatalf("bounded queue retained the wrong events: first=%q last=%q", got.Events[0].Payload, got.Events[len(got.Events)-1].Payload)
	}
}

func TestRendererReadyPreservesOrderAcrossReplayAndLiveBoundary(t *testing.T) {
	app := NewApp()
	app.menuZoom("in")(nil)

	var first pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &first); err != nil {
		t.Fatal(err)
	}
	if app.rendererReady {
		t.Fatal("renderer became live before the queued batch was acknowledged")
	}

	// This newer click occurs after RendererReady returned, but while the older
	// batch is still replaying. It must remain behind that batch rather than emit
	// live and overtake it.
	app.menuZoom("out")(nil)
	var repeatedFirst pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &repeatedFirst); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repeatedFirst, first) {
		t.Fatalf("in-flight batch changed before acknowledgement: %+v", repeatedFirst)
	}

	var second pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReadyAck(testRendererSession, first.DeliveryID)), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].Payload != "out" {
		t.Fatalf("second batch = %+v, want queued zoom out", second)
	}
	if second.DeliveryID == "" || second.DeliveryID == first.DeliveryID {
		t.Fatalf("second delivery id = %q, first = %q", second.DeliveryID, first.DeliveryID)
	}

	// A retry of the prior acknowledgement must return the current batch without
	// accidentally committing it.
	var retriedSecond pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReadyAck(testRendererSession, first.DeliveryID)), &retriedSecond); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retriedSecond, second) {
		t.Fatalf("idempotent ack retry = %+v, want %+v", retriedSecond, second)
	}

	var empty pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReadyAck(testRendererSession, second.DeliveryID)), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.DeliveryID != "" || !app.rendererReady {
		t.Fatalf("final ack did not open live delivery: response=%+v ready=%v", empty, app.rendererReady)
	}
}

func TestEarlyMenuEventQueueKeepsInFlightBatchWhileBoundingNewTail(t *testing.T) {
	app := NewApp()
	app.menuAction("oldest")(nil)
	var first pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &first); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < maxPendingRendererEvents+3; i++ {
		app.menuAction(fmt.Sprintf("new-%d", i))(nil)
	}
	var retry pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &retry); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retry, first) {
		t.Fatalf("bounded tail displaced in-flight batch: %+v", retry)
	}

	var next pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReadyAck(testRendererSession, first.DeliveryID)), &next); err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != maxPendingRendererEvents {
		t.Fatalf("new tail length = %d, want %d", len(next.Events), maxPendingRendererEvents)
	}
	if next.Events[0].Payload != "new-3" {
		t.Fatalf("new tail retained wrong oldest event: %+v", next.Events[0])
	}
}

func TestRendererUnreadyQueuesActivationsAcrossReload(t *testing.T) {
	app := NewApp()
	app.RendererReady(testRendererSession)
	app.RendererUnready(testRendererSession)
	app.handleOpenURL("apexload://tests/run-1")

	var got pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(reloadedRendererSession)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0] != (pendingRendererEvent{Name: eventOpenURL, Payload: "apexload://tests/run-1"}) {
		t.Fatalf("queued reload activations = %+v", got)
	}
	app.RendererReadyAck(reloadedRendererSession, got.DeliveryID)
}

func TestStaleRendererReadyAndAckCannotReopenAfterUnload(t *testing.T) {
	app := NewApp()
	app.menuAction("new-test")(nil)
	var first pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &first); err != nil {
		t.Fatal(err)
	}
	app.RendererUnready(testRendererSession)

	var staleAck pendingOpenRequests
	if err := json.Unmarshal(
		[]byte(app.RendererReadyAck(testRendererSession, first.DeliveryID)),
		&staleAck,
	); err != nil {
		t.Fatal(err)
	}
	if staleAck.SessionAccepted || app.rendererReady {
		t.Fatalf("stale ack reopened renderer: response=%+v ready=%v", staleAck, app.rendererReady)
	}
	var staleReady pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &staleReady); err != nil {
		t.Fatal(err)
	}
	if staleReady.SessionAccepted || app.rendererReady {
		t.Fatalf("retired renderer session was reaccepted: response=%+v ready=%v", staleReady, app.rendererReady)
	}

	var replacement pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(reloadedRendererSession)), &replacement); err != nil {
		t.Fatal(err)
	}
	if !replacement.SessionAccepted || !reflect.DeepEqual(replacement.Events, first.Events) {
		t.Fatalf("replacement renderer did not receive retained batch: %+v", replacement)
	}
	var ready pendingOpenRequests
	if err := json.Unmarshal(
		[]byte(app.RendererReadyAck(reloadedRendererSession, replacement.DeliveryID)),
		&ready,
	); err != nil {
		t.Fatal(err)
	}
	if !ready.SessionAccepted || !app.rendererReady {
		t.Fatalf("replacement renderer did not become ready: response=%+v ready=%v", ready, app.rendererReady)
	}

	// A delayed unload callback from the retired page must not close the current
	// renderer's live-delivery window.
	app.RendererUnready(testRendererSession)
	if !app.rendererReady || app.rendererSession != reloadedRendererSession {
		t.Fatalf("stale unload disturbed replacement renderer: ready=%v session=%q", app.rendererReady, app.rendererSession)
	}
}

func TestReplacementReadyBeforeOldUnloadRetiresOldSession(t *testing.T) {
	app := NewApp()
	var first pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &first); err != nil {
		t.Fatal(err)
	}
	if !first.SessionAccepted || !app.rendererReady {
		t.Fatalf("first renderer was not accepted: response=%+v ready=%v", first, app.rendererReady)
	}

	// WebKit can mount the replacement before the old page's asynchronous
	// pagehide bridge call arrives. The replacement must win immediately.
	var replacement pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(reloadedRendererSession)), &replacement); err != nil {
		t.Fatal(err)
	}
	if !replacement.SessionAccepted || !app.rendererReady || app.rendererSession != reloadedRendererSession {
		t.Fatalf("replacement renderer was not activated: response=%+v ready=%v session=%q", replacement, app.rendererReady, app.rendererSession)
	}

	app.RendererUnready(testRendererSession)
	if !app.rendererReady || app.rendererSession != reloadedRendererSession {
		t.Fatalf("late old unload closed replacement: ready=%v session=%q", app.rendererReady, app.rendererSession)
	}
	var stale pendingOpenRequests
	if err := json.Unmarshal([]byte(app.RendererReady(testRendererSession)), &stale); err != nil {
		t.Fatal(err)
	}
	if stale.SessionAccepted {
		t.Fatalf("superseded renderer was reaccepted: %+v", stale)
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestClassicReplayReportsLeaseLossBeforeDerivedContextCancellation(t *testing.T) {
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	cancelExecution()
	leaseSignal := newReplaySessionLeaseSignal()
	leaseSignal.fail(errReplaySessionLeaseLost)
	if err := replayExecutionFenceError(executionCtx, leaseSignal); !errors.Is(err, errReplaySessionLeaseLost) {
		t.Fatalf("fence error = %v, want replay-session lease loss before leadership loss", err)
	}
}

func waitForReplayWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("replay worker did not stop after execution epoch cancellation")
	}
}

func waitForReplayLifecycleCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newAdmittedHAReplayServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-entrypoint-epoch.db"))
	if err != nil {
		t.Fatalf("open replay entry-point store: %v", err)
	}
	return newAdmittedHAReplayServerWithStore(t, store)
}

func newAdmittedHAReplayServerWithStore(t *testing.T, store storage.Store) *Server {
	t.Helper()
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	t.Cleanup(func() {
		if server.leadership.IsLeader() {
			server.leadership.relinquish()
		}
		server.browserReplay.Close()
		server.scriptDebug.Close()
		server.browserRecording.Close()
	})

	server.leadership.tick()
	deadline := time.Now().Add(3 * time.Second)
	for !server.isExecutionLeader() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !server.isExecutionLeader() {
		t.Fatal("server was not admitted as execution leader")
	}
	return server
}

func assertReplayEntryPointUsesExecutionEpoch(t *testing.T, server *Server, invoke func(http.ResponseWriter)) {
	t.Helper()
	observedContext := make(chan context.Context, 1)
	testFinished := make(chan struct{})
	defer close(testFinished)
	server.browserReplay.setSessionIDReserver(func(ctx context.Context, _, _ string) (string, func(), *replaySessionLeaseSignal, error) {
		observedContext <- ctx
		select {
		case <-ctx.Done():
			return "", func() {}, nil, ctx.Err()
		case <-testFinished:
			return "", func() {}, nil, errors.New("test released replay reservation")
		}
	})

	response := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		invoke(response)
	}()

	var executionCtx context.Context
	select {
	case executionCtx = <-observedContext:
	case <-time.After(2 * time.Second):
		t.Fatal("replay entry point did not reach session reservation")
	}
	if err := executionCtx.Err(); err != nil {
		t.Fatalf("execution context was already cancelled before leadership loss: %v", err)
	}
	select {
	case <-handlerDone:
		t.Fatal("replay entry point returned before leadership loss")
	default:
	}

	server.leadership.relinquish()
	select {
	case <-executionCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("leadership loss did not cancel the replay entry point execution context")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("replay entry point did not stop after leadership loss")
	}
}

func TestRecorderImportReplayCancelsWhenExecutionLeadershipIsLost(t *testing.T) {
	server := newAdmittedHAReplayServer(t)
	body, err := json.Marshal(map[string]any{
		"name":    "Epoch-bound recorder import",
		"content": chromeRecorderFixture,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertReplayEntryPointUsesExecutionEpoch(t, server, func(w http.ResponseWriter) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder/replay", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.handleReplayRecorderImport(w, req)
	})
}

func TestBrowserRecordingVerificationCancelsWhenExecutionLeadershipIsLost(t *testing.T) {
	server := newAdmittedHAReplayServer(t)
	recordingID := "recording-execution-epoch"
	now := time.Now().UTC()
	server.browserRecording.mu.Lock()
	server.browserRecording.sessions[recordingID] = &BrowserRecordingSession{
		ID:        recordingID,
		Name:      "Epoch-bound recording",
		ProjectID: "project-epoch",
		Status:    BrowserRecordingStopped,
		StartedAt: now.Add(-time.Minute),
		StoppedAt: &now,
		final:     recordedBrowserArtifact(),
	}
	server.browserRecording.mu.Unlock()

	assertReplayEntryPointUsesExecutionEpoch(t, server, func(w http.ResponseWriter) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+recordingID+"/verify", nil)
		req.SetPathValue("id", recordingID)
		server.handleVerifyBrowserRecording(w, req)
	})
}

func TestReplayManagersFenceLostExecutionEpochWithoutClosingManager(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	script := &dsl.Script{
		Name:     "epoch fenced replay",
		Protocol: protocolName,
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}

	t.Run("script debugger", func(t *testing.T) {
		manager := NewScriptDebugManager(nil)
		t.Cleanup(manager.Close)
		var completionHooks atomic.Int32
		manager.setCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
			completionHooks.Add(1)
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.StartChecked("debug-script", script, ScriptDebugStartRequest{
			ReplaySessionID:  "debug-fast-fence-reuse",
			StartPaused:      true,
			executionContext: epoch,
		})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		loseLeadership()
		waitForReplayWorker(t, runtime.workerDone)
		runtime.mu.RLock()
		status, errMsg, finalReport := runtime.Status, runtime.Error, runtime.FinalReport
		runtime.mu.RUnlock()
		if status != ScriptDebugFailed || errMsg != errNotExecutionLeader.Error() || finalReport != nil {
			t.Fatalf("debug runtime after leadership loss = status %q error %q report %#v", status, errMsg, finalReport)
		}
		if _, ok := manager.Get(snapshot.ID); ok {
			t.Fatal("unfenced debug capability remained locally addressable after its global release")
		}
		if completionHooks.Load() != 0 {
			t.Fatal("lost-epoch debug worker ran its completion/self-heal hook")
		}

		next, err := manager.StartChecked("debug-script", script, ScriptDebugStartRequest{
			ReplaySessionID: snapshot.ID,
			StartPaused:     true,
		})
		if err != nil {
			t.Fatalf("manager did not accept the retired session ID in the next epoch: %v", err)
		}
		if _, ok := manager.Stop(next.ID); !ok {
			t.Fatal("could not stop next-epoch debug session")
		}
	})

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil)
		t.Cleanup(manager.Close)
		var completionHooks atomic.Int32
		manager.setCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
			completionHooks.Add(1)
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.Start("browser-script", script, BrowserReplayStartRequest{
			ReplaySessionID:  "browser-fast-fence-reuse",
			StartPaused:      true,
			executionContext: epoch,
		})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		loseLeadership()
		waitForReplayWorker(t, runtime.workerDone)
		runtime.mu.RLock()
		status, errMsg, finalReport := runtime.Status, runtime.Error, runtime.FinalReport
		runtime.mu.RUnlock()
		if status != BrowserReplayFailed || errMsg != errNotExecutionLeader.Error() || finalReport != nil {
			t.Fatalf("browser runtime after leadership loss = status %q error %q report %#v", status, errMsg, finalReport)
		}
		if _, ok := manager.Get(snapshot.ID); ok {
			t.Fatal("unfenced browser capability remained locally addressable after its global release")
		}
		if completionHooks.Load() != 0 {
			t.Fatal("lost-epoch browser worker ran its completion/self-heal hook")
		}

		next, err := manager.Start("browser-script", script, BrowserReplayStartRequest{
			ReplaySessionID: snapshot.ID,
			StartPaused:     true,
		})
		if err != nil {
			t.Fatalf("manager did not accept the retired session ID in the next epoch: %v", err)
		}
		if _, ok := manager.Stop(next.ID); !ok {
			t.Fatal("could not stop next-epoch browser session")
		}
	})
}

func TestReplayManagersSuppressReportsThatFinishAfterExecutionFenceLoss(t *testing.T) {
	newBlockedReplay := func(t *testing.T) (string, <-chan struct{}, func()) {
		t.Helper()
		started := make(chan struct{})
		release := make(chan struct{})
		var startedOnce sync.Once
		protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
			startedOnce.Do(func() { close(started) })
			<-release // Deliberately return only after the execution epoch is lost.
			return &protocol.Result{
				ActionName: action.Name,
				Success:    true,
				StatusCode: http.StatusOK,
				CorrelationEvidence: &protocol.CorrelationEvidence{
					RequestURL:   "/private",
					ResponseBody: "must-not-survive-fence-loss",
				},
			}, nil
		})
		var releaseOnce sync.Once
		return protocolName, started, func() { releaseOnce.Do(func() { close(release) }) }
	}
	waitStarted := func(t *testing.T, started <-chan struct{}) {
		t.Helper()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("replay action did not start")
		}
	}

	t.Run("script debugger", func(t *testing.T) {
		protocolName, started, release := newBlockedReplay(t)
		manager := NewScriptDebugManager(nil)
		manager.retention.workerAbandonGrace = time.Second
		t.Cleanup(manager.Close)
		t.Cleanup(release)
		var completionHooks atomic.Int32
		manager.setCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
			completionHooks.Add(1)
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.StartChecked("debug-script", &dsl.Script{
			Name: "debug fence", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, ScriptDebugStartRequest{executionContext: epoch})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		waitStarted(t, started)
		loseLeadership()
		release()
		waitForReplayWorker(t, runtime.workerDone)

		runtime.mu.RLock()
		status, errMsg, finalReport := runtime.Status, runtime.Error, runtime.FinalReport
		runtime.mu.RUnlock()
		if status != ScriptDebugFailed || errMsg != errNotExecutionLeader.Error() || finalReport != nil {
			t.Fatalf("debug runtime after leadership loss = status %q error %q report %#v", status, errMsg, finalReport)
		}
		if _, ok := manager.Get(snapshot.ID); ok {
			t.Fatal("lost-fence debug capability remained addressable after global release")
		}
		if completionHooks.Load() != 0 {
			t.Fatal("lost-fence debug worker ran its completion/self-heal hook")
		}
		runtime.mu.RLock()
		evidenceErr := runtime.correlationEvidenceErr
		evidenceCount := len(runtime.correlationObservations)
		runtime.mu.RUnlock()
		if evidenceCount != 0 ||
			(!strings.Contains(evidenceErr, "integrity fence") && !strings.Contains(evidenceErr, "execution leadership")) {
			t.Fatalf("debug correlation evidence survived fence loss: count=%d err=%q", evidenceCount, evidenceErr)
		}
	})

	t.Run("browser replay", func(t *testing.T) {
		protocolName, started, release := newBlockedReplay(t)
		manager := NewBrowserReplayManager(nil)
		manager.retention.workerAbandonGrace = time.Second
		t.Cleanup(manager.Close)
		t.Cleanup(release)
		var completionHooks atomic.Int32
		manager.setCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
			completionHooks.Add(1)
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.Start("browser-script", &dsl.Script{
			Name: "browser fence", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, BrowserReplayStartRequest{executionContext: epoch})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		waitStarted(t, started)
		loseLeadership()
		release()
		waitForReplayWorker(t, runtime.workerDone)

		runtime.mu.RLock()
		status, errMsg, finalReport := runtime.Status, runtime.Error, runtime.FinalReport
		runtime.mu.RUnlock()
		if status != BrowserReplayFailed || errMsg != errNotExecutionLeader.Error() || finalReport != nil {
			t.Fatalf("browser runtime after leadership loss = status %q error %q report %#v", status, errMsg, finalReport)
		}
		if _, ok := manager.Get(snapshot.ID); ok {
			t.Fatal("lost-fence browser capability remained addressable after global release")
		}
		if completionHooks.Load() != 0 {
			t.Fatal("lost-fence browser worker ran its completion/self-heal hook")
		}
	})
}

func TestReplayManagersDoNotRewriteNormalCompletionWhenOperationContextIsReleased(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	script := &dsl.Script{
		Name: "normal completion", Protocol: protocolName,
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	newRelease := func(t *testing.T) (context.Context, context.CancelFunc, <-chan struct{}, func()) {
		t.Helper()
		epoch, cancelEpoch := context.WithCancel(context.Background())
		releaseStarted := make(chan struct{})
		releaseBlocked := make(chan struct{})
		var releaseOnce sync.Once
		release := func() {
			cancelEpoch()
			releaseOnce.Do(func() { close(releaseStarted) })
			<-releaseBlocked
		}
		var unblockOnce sync.Once
		unblock := func() { unblockOnce.Do(func() { close(releaseBlocked) }) }
		t.Cleanup(unblock)
		return epoch, release, releaseStarted, unblock
	}
	waitRelease := func(t *testing.T, started <-chan struct{}) {
		t.Helper()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("operation-context release callback did not run")
		}
	}

	t.Run("script debugger", func(t *testing.T) {
		manager := NewScriptDebugManager(nil)
		t.Cleanup(manager.Close)
		epoch, release, releaseStarted, unblock := newRelease(t)
		snapshot, err := manager.StartChecked("debug-script", script, ScriptDebugStartRequest{
			executionContext:        epoch,
			releaseExecutionContext: release,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitRelease(t, releaseStarted)
		current, ok := manager.Get(snapshot.ID)
		if !ok || current.Status != ScriptDebugCompleted || current.FinalReport == nil {
			t.Fatalf("normal debug completion was rewritten while releasing its context: %#v", current)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		unblock()
		waitForReplayWorker(t, runtime.workerDone)
		current, ok = manager.Get(snapshot.ID)
		if !ok || current.Status != ScriptDebugCompleted || current.FinalReport == nil {
			t.Fatalf("normal debug completion changed after operation-context release: %#v", current)
		}
	})

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil)
		t.Cleanup(manager.Close)
		epoch, release, releaseStarted, unblock := newRelease(t)
		snapshot, err := manager.Start("browser-script", script, BrowserReplayStartRequest{
			executionContext:        epoch,
			releaseExecutionContext: release,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitRelease(t, releaseStarted)
		current, ok := manager.Get(snapshot.ID)
		if !ok || current.Status != BrowserReplayCompleted || current.FinalReport == nil {
			t.Fatalf("normal browser completion was rewritten while releasing its context: %#v", current)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		unblock()
		waitForReplayWorker(t, runtime.workerDone)
		current, ok = manager.Get(snapshot.ID)
		if !ok || current.Status != BrowserReplayCompleted || current.FinalReport == nil {
			t.Fatalf("normal browser completion changed after operation-context release: %#v", current)
		}
	})
}

func TestScriptDebugReportRemainsPrivateUntilTerminalReservationIsRetained(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	manager := NewScriptDebugManager(nil)
	t.Cleanup(manager.Close)
	retainStarted := make(chan struct{})
	allowRetain := make(chan struct{})
	var retainOnce sync.Once
	var allowOnce sync.Once
	releaseRetain := func() { allowOnce.Do(func() { close(allowRetain) }) }
	t.Cleanup(releaseRetain)
	manager.setSessionIDReserver(func(_ context.Context, requested, _ string) (string, func(), *replaySessionLeaseSignal, error) {
		signal := newReplaySessionLeaseSignal()
		signal.retainForTerminalFn = func(time.Duration) error {
			retainOnce.Do(func() { close(retainStarted) })
			<-allowRetain
			return nil
		}
		return requested, func() {}, signal, nil
	})

	snapshot, err := manager.StartChecked("debug-script", &dsl.Script{
		Name: "terminal fence", Protocol: protocolName,
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-terminal-fence"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-retainStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("debug replay never reached terminal reservation retention")
	}
	current, ok := manager.Get(snapshot.ID)
	if !ok {
		t.Fatal("debug session disappeared while terminal retention was in flight")
	}
	if current.FinalReport != nil {
		t.Fatalf("debug report crossed the terminal fence before retention succeeded: %#v", current.FinalReport)
	}

	releaseRetain()
	manager.mu.RLock()
	runtime := manager.sessions[snapshot.ID]
	manager.mu.RUnlock()
	waitForReplayWorker(t, runtime.workerDone)
	current, ok = manager.Get(snapshot.ID)
	if !ok || current.Status != ScriptDebugCompleted || current.FinalReport == nil {
		t.Fatalf("debug report was not published after terminal retention: %#v", current)
	}
}

func TestReplayManagersAbandonWorkersThatIgnoreExecutionFenceLoss(t *testing.T) {
	newBlockedReplay := func(t *testing.T) (string, <-chan struct{}, func()) {
		t.Helper()
		started := make(chan struct{})
		release := make(chan struct{})
		var startedOnce sync.Once
		protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
			startedOnce.Do(func() { close(started) })
			<-release // Ignore context cancellation beyond the abandon grace.
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
		})
		var releaseOnce sync.Once
		return protocolName, started, func() { releaseOnce.Do(func() { close(release) }) }
	}
	waitStarted := func(t *testing.T, started <-chan struct{}) {
		t.Helper()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("replay action did not start")
		}
	}

	t.Run("script debugger", func(t *testing.T) {
		protocolName, started, releaseWorker := newBlockedReplay(t)
		manager := NewScriptDebugManager(nil)
		manager.retention.maxSessions = 1
		manager.retention.workerAbandonGrace = 40 * time.Millisecond
		t.Cleanup(manager.Close)
		t.Cleanup(releaseWorker)
		var activeReleased, allReleased atomic.Bool
		manager.setSessionIDReserver(func(_ context.Context, requested, _ string) (string, func(), *replaySessionLeaseSignal, error) {
			signal := newReplaySessionLeaseSignal()
			if requested != "debug-wedged" {
				return requested, func() {}, signal, nil
			}
			var activeOnce, allOnce sync.Once
			signal.releaseAllFn = func() { allOnce.Do(func() { allReleased.Store(true) }) }
			return requested, func() { activeOnce.Do(func() { activeReleased.Store(true) }) }, signal, nil
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.StartChecked("debug-script", &dsl.Script{
			Name: "debug wedged", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, ScriptDebugStartRequest{ReplaySessionID: "debug-wedged", executionContext: epoch})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		waitStarted(t, started)
		loseLeadership()

		waitForReplayLifecycleCondition(t, "debug abandon backstop", func() bool {
			runtime.mu.RLock()
			status, errMsg, abandoned := runtime.Status, runtime.Error, runtime.workerAbandoned
			runtime.mu.RUnlock()
			_, addressable := manager.Get(snapshot.ID)
			return abandoned && status == ScriptDebugFailed &&
				strings.Contains(errMsg, "ignored cancellation") &&
				!addressable && activeReleased.Load() && allReleased.Load()
		})
		if _, err := manager.StartChecked("replacement-debug", &dsl.Script{
			Name: "replacement", Protocol: protocolName,
		}, ScriptDebugStartRequest{ReplaySessionID: snapshot.ID, StartPaused: true}); err != nil {
			t.Fatalf("reuse exact debug session ID after abandon: %v", err)
		}
		releaseWorker()
		waitForReplayWorker(t, runtime.workerDone)
		runtime.mu.RLock()
		finalReport := runtime.FinalReport
		runtime.mu.RUnlock()
		if finalReport != nil {
			t.Fatalf("late debug worker published report after abandonment: %#v", finalReport)
		}
		if _, ok := manager.Stop(snapshot.ID); !ok {
			t.Fatal("replacement debug session could not be stopped")
		}
	})

	t.Run("browser replay", func(t *testing.T) {
		protocolName, started, releaseWorker := newBlockedReplay(t)
		manager := NewBrowserReplayManager(nil)
		manager.retention.maxSessions = 1
		manager.retention.workerAbandonGrace = 40 * time.Millisecond
		t.Cleanup(manager.Close)
		t.Cleanup(releaseWorker)
		var activeReleased, allReleased atomic.Bool
		manager.setSessionIDReserver(func(_ context.Context, requested, _ string) (string, func(), *replaySessionLeaseSignal, error) {
			signal := newReplaySessionLeaseSignal()
			if requested != "browser-wedged" {
				return requested, func() {}, signal, nil
			}
			var activeOnce, allOnce sync.Once
			signal.releaseAllFn = func() { allOnce.Do(func() { allReleased.Store(true) }) }
			return requested, func() { activeOnce.Do(func() { activeReleased.Store(true) }) }, signal, nil
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.Start("browser-script", &dsl.Script{
			Name: "browser wedged", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, BrowserReplayStartRequest{ReplaySessionID: "browser-wedged", executionContext: epoch})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		waitStarted(t, started)
		loseLeadership()

		waitForReplayLifecycleCondition(t, "browser abandon backstop", func() bool {
			runtime.mu.RLock()
			status, errMsg, abandoned := runtime.Status, runtime.Error, runtime.workerAbandoned
			runtime.mu.RUnlock()
			_, addressable := manager.Get(snapshot.ID)
			return abandoned && status == BrowserReplayFailed &&
				strings.Contains(errMsg, "ignored cancellation") &&
				!addressable && activeReleased.Load() && allReleased.Load()
		})
		if _, err := manager.Start("replacement-browser", &dsl.Script{
			Name: "replacement", Protocol: protocolName,
		}, BrowserReplayStartRequest{ReplaySessionID: snapshot.ID, StartPaused: true}); err != nil {
			t.Fatalf("reuse exact browser session ID after abandon: %v", err)
		}
		releaseWorker()
		waitForReplayWorker(t, runtime.workerDone)
		runtime.mu.RLock()
		finalReport := runtime.FinalReport
		runtime.mu.RUnlock()
		if finalReport != nil {
			t.Fatalf("late browser worker published report after abandonment: %#v", finalReport)
		}
		if _, ok := manager.Stop(snapshot.ID); !ok {
			t.Fatal("replacement browser replay could not be stopped")
		}
	})
}

func TestReplayManagerTerminalRetentionStartsWhenWorkerArtifactsBecomeVisible(t *testing.T) {
	type controlledClock struct {
		mu  sync.Mutex
		now time.Time
	}
	newClock := func() (*controlledClock, func() time.Time, func(time.Duration)) {
		clock := &controlledClock{now: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)}
		read := func() time.Time {
			clock.mu.Lock()
			defer clock.mu.Unlock()
			return clock.now
		}
		advance := func(delta time.Duration) {
			clock.mu.Lock()
			clock.now = clock.now.Add(delta)
			clock.mu.Unlock()
		}
		return clock, read, advance
	}
	newBlockedProtocol := func(t *testing.T) (string, <-chan struct{}, func()) {
		t.Helper()
		started := make(chan struct{})
		release := make(chan struct{})
		var startedOnce sync.Once
		var releaseOnce sync.Once
		name := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
			startedOnce.Do(func() { close(started) })
			<-release
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
		})
		return name, started, func() { releaseOnce.Do(func() { close(release) }) }
	}

	t.Run("browser replay", func(t *testing.T) {
		_, readClock, advanceClock := newClock()
		protocolName, started, releaseWorker := newBlockedProtocol(t)
		manager := NewBrowserReplayManager(nil)
		manager.retention.now = readClock
		manager.retention.terminalTTL = time.Minute
		manager.retention.workerAbandonGrace = time.Hour
		t.Cleanup(manager.Close)
		t.Cleanup(releaseWorker)

		snapshot, err := manager.Start("browser-retention", &dsl.Script{
			Name: "browser retention", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, BrowserReplayStartRequest{ReplaySessionID: "browser-retention"})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		advanceClock(2 * time.Minute)
		if _, ok := manager.Stop(snapshot.ID); !ok {
			t.Fatal("stop browser replay")
		}
		advanceClock(2 * time.Minute)
		releaseWorker()
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		waitForReplayWorker(t, runtime.workerDone)
		advanceClock(30 * time.Second)
		if removed := manager.PruneExpired(); removed != 0 {
			t.Fatalf("pruned %d browser session(s) before the post-report TTL elapsed", removed)
		}
		current, ok := manager.Get(snapshot.ID)
		if !ok || current.FinalReport == nil {
			t.Fatalf("browser terminal report was not visible for its full TTL: %#v", current)
		}
	})

	t.Run("script debugger", func(t *testing.T) {
		_, readClock, advanceClock := newClock()
		protocolName, started, releaseWorker := newBlockedProtocol(t)
		manager := NewScriptDebugManager(nil)
		manager.retention.now = readClock
		manager.retention.terminalTTL = time.Minute
		manager.retention.workerAbandonGrace = time.Hour
		t.Cleanup(manager.Close)
		t.Cleanup(releaseWorker)

		snapshot, err := manager.StartChecked("debug-retention", &dsl.Script{
			Name: "debug retention", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, ScriptDebugStartRequest{ReplaySessionID: "debug-retention"})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		advanceClock(2 * time.Minute)
		if _, ok := manager.Stop(snapshot.ID); !ok {
			t.Fatal("stop script debugger")
		}
		advanceClock(2 * time.Minute)
		releaseWorker()
		manager.mu.RLock()
		runtime := manager.sessions[snapshot.ID]
		manager.mu.RUnlock()
		waitForReplayWorker(t, runtime.workerDone)
		advanceClock(30 * time.Second)
		if removed := manager.PruneExpired(); removed != 0 {
			t.Fatalf("pruned %d debug session(s) before the post-report TTL elapsed", removed)
		}
		current, ok := manager.Get(snapshot.ID)
		if !ok || current.FinalReport == nil {
			t.Fatalf("debug terminal report was not visible for its full TTL: %#v", current)
		}
	})
}

func TestReplaySessionOperationsRejectAuthorizedProjectSubstitution(t *testing.T) {
	const (
		sessionID         = "globally-reused-session-id"
		authorizedProject = "project-a"
		replacementOwner  = "project-b"
	)

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil)
		lastActivity := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
		// Pin the retention clock inside the idle-cancel window so the on-access
		// prune cannot stop the session and mask a project-guard regression.
		manager.retention.now = func() time.Time { return lastActivity.Add(time.Minute) }
		runtime := &BrowserReplaySession{
			ID:             sessionID,
			ProjectID:      replacementOwner,
			Status:         BrowserReplayRunning,
			lastActivityAt: lastActivity,
			stateChanged:   make(chan struct{}),
		}
		manager.sessions[sessionID] = runtime

		request := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/"+sessionID, nil)
		request.SetPathValue("session_id", sessionID)
		projectID, status, message := resolveBrowserReplaySessionProjectID(manager)(request)
		if projectID != replacementOwner || status != 0 || message != "" {
			t.Fatalf("authorization lookup = %q, %d, %q", projectID, status, message)
		}
		runtime.mu.RLock()
		activityAfterLookup := runtime.lastActivityAt
		runtime.mu.RUnlock()
		if !activityAfterLookup.Equal(lastActivity) {
			t.Fatalf("authorization lookup refreshed idle retention: %s -> %s", lastActivity, activityAfterLookup)
		}

		if session, ok := manager.GetForProject(sessionID, authorizedProject); ok || session != nil {
			t.Fatalf("foreign GetForProject returned %#v", session)
		}
		if session, ok, changed := manager.PauseForProject(sessionID, authorizedProject); ok || changed || session != nil {
			t.Fatalf("foreign PauseForProject = %#v, %v, %v", session, ok, changed)
		}
		if session, ok, changed := manager.ResumeForProject(sessionID, authorizedProject); ok || changed || session != nil {
			t.Fatalf("foreign ResumeForProject = %#v, %v, %v", session, ok, changed)
		}
		if session, ok := manager.StopForProject(sessionID, authorizedProject); ok || session != nil {
			t.Fatalf("foreign StopForProject returned %#v", session)
		}
		runtime.mu.RLock()
		statusAfter := runtime.Status
		runtime.mu.RUnlock()
		if statusAfter != BrowserReplayRunning {
			t.Fatalf("foreign operation changed browser status to %q", statusAfter)
		}

		server := &Server{browserReplay: manager}
		bindAuthorizedProjectToRequest(request, authorizedProject)
		response := httptest.NewRecorder()
		server.handleGetBrowserReplaySession(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("project-substituted browser handler = %d body=%s, want 404", response.Code, response.Body.String())
		}
	})

	t.Run("script debugger", func(t *testing.T) {
		manager := NewScriptDebugManager(nil)
		lastActivity := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
		// Pin the retention clock inside the idle-cancel window so the on-access
		// prune cannot stop the session and mask a project-guard regression.
		manager.retention.now = func() time.Time { return lastActivity.Add(time.Minute) }
		runtime := &ScriptDebugSession{
			ID:             sessionID,
			ProjectID:      replacementOwner,
			Status:         ScriptDebugRunning,
			LogLevel:       "info",
			lastActivityAt: lastActivity,
			stateChanged:   make(chan struct{}),
		}
		manager.sessions[sessionID] = runtime

		request := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/"+sessionID, nil)
		request.SetPathValue("session_id", sessionID)
		projectID, status, message := resolveScriptDebugSessionProjectID(manager)(request)
		if projectID != replacementOwner || status != 0 || message != "" {
			t.Fatalf("authorization lookup = %q, %d, %q", projectID, status, message)
		}
		runtime.mu.RLock()
		activityAfterLookup := runtime.lastActivityAt
		runtime.mu.RUnlock()
		if !activityAfterLookup.Equal(lastActivity) {
			t.Fatalf("authorization lookup refreshed idle retention: %s -> %s", lastActivity, activityAfterLookup)
		}

		if session, ok := manager.GetForProject(sessionID, authorizedProject); ok || session != nil {
			t.Fatalf("foreign GetForProject returned %#v", session)
		}
		if session, ok, changed := manager.PauseForProject(sessionID, authorizedProject); ok || changed || session != nil {
			t.Fatalf("foreign PauseForProject = %#v, %v, %v", session, ok, changed)
		}
		if session, ok, changed := manager.ResumeForProject(sessionID, authorizedProject); ok || changed || session != nil {
			t.Fatalf("foreign ResumeForProject = %#v, %v, %v", session, ok, changed)
		}
		if session, ok, changed := manager.StepModeForProject(sessionID, authorizedProject, "into"); ok || changed || session != nil {
			t.Fatalf("foreign StepModeForProject = %#v, %v, %v", session, ok, changed)
		}
		if session, ok := manager.StopForProject(sessionID, authorizedProject); ok || session != nil {
			t.Fatalf("foreign StopForProject returned %#v", session)
		}
		if session, ok := manager.UpdateBreakpointsForProject(sessionID, authorizedProject, []ScriptDebugBreakpoint{{Phase: "action"}}); ok || session != nil {
			t.Fatalf("foreign UpdateBreakpointsForProject returned %#v", session)
		}
		if session, ok := manager.SetBreakOnFailureForProject(sessionID, authorizedProject, true); ok || session != nil {
			t.Fatalf("foreign SetBreakOnFailureForProject returned %#v", session)
		}
		if payload, contentType, ok := manager.ExportForProject(sessionID, authorizedProject, "json"); ok || payload != nil || contentType != "" {
			t.Fatalf("foreign ExportForProject = %q, %q, %v", payload, contentType, ok)
		}
		runtime.mu.RLock()
		statusAfter, breakOnFailure, breakpointCount, logCount := runtime.Status, runtime.BreakOnFailure, len(runtime.Breakpoints), len(runtime.Logs)
		runtime.mu.RUnlock()
		if statusAfter != ScriptDebugRunning || breakOnFailure || breakpointCount != 0 || logCount != 0 {
			t.Fatalf("foreign operation mutated debug session: status=%q break=%v breakpoints=%d logs=%d", statusAfter, breakOnFailure, breakpointCount, logCount)
		}

		server := &Server{scriptDebug: manager}
		bindAuthorizedProjectToRequest(request, authorizedProject)
		response := httptest.NewRecorder()
		server.handleGetScriptDebugSession(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("project-substituted debug handler = %d body=%s, want 404", response.Code, response.Body.String())
		}
	})
}

func TestReplayManagersStopServingCapabilitiesPastLocalLeaseFence(t *testing.T) {
	expiredSignal := func() *replaySessionLeaseSignal {
		signal := newReplaySessionLeaseSignal()
		signal.setValidUntil(time.Now().Add(-time.Second))
		return signal
	}

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil)
		manager.sessions["expired-browser"] = &BrowserReplaySession{
			ID:           "expired-browser",
			ProjectID:    "project-a",
			Status:       BrowserReplayRunning,
			stateChanged: make(chan struct{}),
			reservation:  expiredSignal(),
		}
		if projectID, ok := manager.LookupProjectID("expired-browser"); ok || projectID != "" {
			t.Fatalf("expired browser authorization lookup = %q, %v", projectID, ok)
		}
		if session, ok := manager.GetForProject("expired-browser", "project-a"); ok || session != nil {
			t.Fatalf("expired browser GetForProject returned %#v", session)
		}
		if session, ok, changed := manager.PauseForProject("expired-browser", "project-a"); ok || changed || session != nil {
			t.Fatalf("expired browser PauseForProject = %#v, %v, %v", session, ok, changed)
		}
	})

	t.Run("script debugger", func(t *testing.T) {
		manager := NewScriptDebugManager(nil)
		manager.sessions["expired-debug"] = &ScriptDebugSession{
			ID:           "expired-debug",
			ProjectID:    "project-a",
			Status:       ScriptDebugRunning,
			LogLevel:     "info",
			stateChanged: make(chan struct{}),
			reservation:  expiredSignal(),
		}
		if projectID, ok := manager.LookupProjectID("expired-debug"); ok || projectID != "" {
			t.Fatalf("expired debug authorization lookup = %q, %v", projectID, ok)
		}
		if session, ok := manager.GetForProject("expired-debug", "project-a"); ok || session != nil {
			t.Fatalf("expired debug GetForProject returned %#v", session)
		}
		if session, ok, changed := manager.PauseForProject("expired-debug", "project-a"); ok || changed || session != nil {
			t.Fatalf("expired debug PauseForProject = %#v, %v, %v", session, ok, changed)
		}
		if observations, err := manager.CorrelationObservations("expired-debug", "project-a", "", ""); observations != nil ||
			!errors.Is(err, errScriptDebugCorrelationSessionNotFound) {
			t.Fatalf("expired debug CorrelationObservations = %#v, %v", observations, err)
		}
	})
}

func TestReplayStartHandlersRejectExecutionFollower(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-follower.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	request := httptest.NewRequest(http.MethodPost, "/", nil)

	tests := []struct {
		name  string
		start func(http.ResponseWriter)
	}{
		{
			name: "classic replay",
			start: func(w http.ResponseWriter) {
				server.handleReplayRequest(w, "script-1", &dsl.Script{}, ReplayScriptRequest{})
			},
		},
		{
			name: "script debugger",
			start: func(w http.ResponseWriter) {
				server.handleStartScriptDebugRequest(w, request, "script-1", &dsl.Script{}, ScriptDebugStartRequest{})
			},
		},
		{
			name: "browser replay",
			start: func(w http.ResponseWriter) {
				server.handleStartBrowserReplayRequest(w, request, "script-1", &dsl.Script{}, BrowserReplayStartRequest{})
			},
		},
		{
			name: "browser recording verification",
			start: func(w http.ResponseWriter) {
				server.handleVerifyBrowserRecording(w, request)
			},
		},
		{
			name: "recorder import replay",
			start: func(w http.ResponseWriter) {
				server.handleReplayRecorderImport(w, request)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.start(response)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCloseReplayManagersUsesOneCallerDeadline(t *testing.T) {
	server := NewServer(ServerConfig{})
	browserWorkerDone := make(chan struct{})
	debugWorkerDone := make(chan struct{})
	server.browserReplay.closeTimeout = 2 * time.Second
	server.scriptDebug.closeTimeout = 2 * time.Second
	server.browserReplay.sessions["browser-blocked"] = &BrowserReplaySession{
		ID:           "browser-blocked",
		Status:       BrowserReplayRunning,
		cancel:       func() {},
		stateChanged: make(chan struct{}),
		workerDone:   browserWorkerDone,
	}
	server.scriptDebug.sessions["debug-blocked"] = &ScriptDebugSession{
		ID:           "debug-blocked",
		Status:       ScriptDebugRunning,
		cancel:       func() {},
		stateChanged: make(chan struct{}),
		workerDone:   debugWorkerDone,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	started := time.Now()
	err := server.closeReplayManagers(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("parallel replay-manager drain took %s, want caller deadline below 300ms", elapsed)
	}

	close(browserWorkerDone)
	close(debugWorkerDone)
	for name, done := range map[string]<-chan struct{}{
		"browser": server.browserReplay.closeDone,
		"debug":   server.scriptDebug.closeDone,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s replay manager did not finish after its worker drained", name)
		}
	}
}

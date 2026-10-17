package recordbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayAPIProtocol struct {
	execute func(action *protocol.Action) (*protocol.Result, error)
}

var replayAPIProtocolCounter atomic.Uint64

const replayHandlerTestSessionID = "77777777-7777-4777-8777-777777777777"

func (p *replayAPIProtocol) Name() string                                   { return "replay-api" }
func (p *replayAPIProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *replayAPIProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return p.execute(action)
}
func (p *replayAPIProtocol) Teardown(_ context.Context) error    { return nil }
func (p *replayAPIProtocol) HealthCheck(_ context.Context) error { return nil }

func registerReplayAPIProtocol(t *testing.T, execute func(action *protocol.Action) (*protocol.Result, error)) string {
	t.Helper()
	name := "replay-api-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + strconv.FormatUint(replayAPIProtocolCounter.Add(1), 10)
	protocol.Register(name, func() protocol.Protocol {
		return &replayAPIProtocol{execute: execute}
	})
	return name
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

type replaySessionLeaseSignal struct {
	once                sync.Once
	done                chan struct{}
	mu                  sync.RWMutex
	err                 error
	validUntil          time.Time
	retainForTerminalFn func(time.Duration) error
	releaseRetainedIDFn func()
	releaseAllFn        func()
}

func newReplaySessionLeaseSignal() *replaySessionLeaseSignal {
	return &replaySessionLeaseSignal{done: make(chan struct{})}
}

func (s *replaySessionLeaseSignal) fail(err error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if err == nil {
			err = errReplaySessionLeaseLost
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *replaySessionLeaseSignal) setValidUntil(validUntil time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.validUntil = validUntil
	s.mu.Unlock()
}

func (s *replaySessionLeaseSignal) clearValidUntil() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.validUntil = time.Time{}
	s.mu.Unlock()
}

// failureOrExpired closes the local fence even when the renewal goroutine was
// delayed long enough that it has not yet observed the deadline. Callers must
// use this immediately before publishing a terminal result.
func (s *replaySessionLeaseSignal) failureOrExpired(now time.Time) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	err := s.err
	validUntil := s.validUntil
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if !validUntil.IsZero() && !now.Before(validUntil) {
		s.fail(errReplaySessionLeaseLost)
		return errReplaySessionLeaseLost
	}
	return nil
}

func (s *replaySessionLeaseSignal) retainForTerminal(ttl time.Duration) error {
	if s == nil {
		return nil
	}
	if err := s.failureOrExpired(time.Now()); err != nil {
		return err
	}
	if s.retainForTerminalFn == nil {
		return nil
	}
	return s.retainForTerminalFn(ttl)
}

func (s *replaySessionLeaseSignal) releaseRetainedID() {
	if s != nil && s.releaseRetainedIDFn != nil {
		s.releaseRetainedIDFn()
	}
}

// releaseAll atomically tears down whichever lifecycle state currently owns
// the session ID. Shutdown cannot safely compose releaseRetainedID with the
// active-release callback: a worker could transition active -> retained between
// those two calls and leave a durable tombstone behind.
func (s *replaySessionLeaseSignal) releaseAll() {
	if s != nil && s.releaseAllFn != nil {
		s.releaseAllFn()
	}
}

// releaseRetainedReplaySessionSignals releases retained tombstone reservations
// collected while a manager mutex was held. Callers must invoke it only after
// dropping the manager mutex: in HA mode every release is a synchronous store
// call (up to the lease operation timeout each), and performing them under the
// manager lock freezes every handler and running replay's progress path.
// releaseRetainedID is idempotent and lifecycle-fenced, so racing a concurrent
// release of the same reservation is safe.
func (s *replaySessionLeaseSignal) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}
func (s *replaySessionLeaseSignal) Fail(err error) { s.fail(err) }
func (s *replaySessionLeaseSignal) FailureOrExpired(now time.Time) error {
	return s.failureOrExpired(now)
}
func (s *replaySessionLeaseSignal) RetainForTerminal(ttl time.Duration) error {
	return s.retainForTerminal(ttl)
}
func (s *replaySessionLeaseSignal) ReleaseRetainedID() { s.releaseRetainedID() }
func (s *replaySessionLeaseSignal) ReleaseAll()        { s.releaseAll() }

var errReplaySessionLeaseLost = errors.New("replay session ownership was lost")
var errNotExecutionLeader = errors.New("controller is not the active execution leader")

func TestBrowserReplayManagerRetainsStreamedEvents(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:       action.Name,
			Success:          true,
			PageURL:          "https://example.com/",
			PageTitle:        "Example",
			ScreenshotBase64: "c2NyZWVu",
			DOMSummary:       &protocol.DOMSummary{Title: "Example", ReadyState: "complete"},
		}, nil
	})

	manager := NewBrowserReplayManager(newTestEventSink())
	session, err := manager.Start("", &dsl.Script{
		Name:     "Browser Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "shot", Type: "browser_screenshot"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "session-events"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "session-events" {
		t.Fatalf("session.ID = %q, want session-events", session.ID)
	}

	var got *BrowserReplaySession
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		got, ok = manager.Get("session-events")
		if !ok {
			t.Fatal("session not found")
		}
		if got.Status == BrowserReplayCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got.Status != BrowserReplayCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CurrentEventIndex != 0 {
		t.Fatalf("current_event_index = %d, want 0", got.CurrentEventIndex)
	}
	if got.CurrentEvent != nil || len(got.Events) != 0 {
		t.Fatalf("completed session duplicated terminal event payload: current=%#v events=%#v", got.CurrentEvent, got.Events)
	}
	if got.FinalReport == nil || len(got.FinalReport.Events) != 1 || got.FinalReport.Events[0].Screenshot != "c2NyZWVu" || got.FinalReport.Events[0].PageTitle != "Example" {
		t.Fatalf("report events = %#v, want authoritative retained screenshot event", got.FinalReport)
	}
}

func TestBrowserReplaySessionSnapshotReturnsDetachedCopy(t *testing.T) {
	now := time.Now()
	session := &BrowserReplaySession{
		ID:                "session-detached",
		Status:            BrowserReplayRunning,
		StartedAt:         &now,
		CurrentEventIndex: 0,
		CurrentEvent: &engine.ReplayActionEvent{
			ActionName:    "step-1",
			Headers:       map[string]string{"Authorization": "Bearer original"},
			ClientMetrics: &protocol.ClientMetrics{LCPMs: 240},
		},
		Events: []engine.ReplayActionEvent{{
			ActionName:    "step-1",
			Headers:       map[string]string{"Authorization": "Bearer original"},
			ClientMetrics: &protocol.ClientMetrics{LCPMs: 240},
		}},
		FinalReport: &engine.ReplayReport{
			Group:          "browser",
			FinalVariables: map[string]string{"token": "original"},
			Events: []engine.ReplayActionEvent{{
				ActionName:    "step-1",
				Headers:       map[string]string{"Authorization": "Bearer original"},
				ClientMetrics: &protocol.ClientMetrics{LCPMs: 240},
			}},
		},
	}

	snapshot := session.snapshot()
	if snapshot == nil {
		t.Fatal("snapshot returned nil")
	}

	session.CurrentEvent.Headers["Authorization"] = "Bearer updated"
	session.CurrentEvent.ClientMetrics.LCPMs = 999
	session.Events[0].Headers["Authorization"] = "Bearer updated"
	session.Events[0].ClientMetrics.LCPMs = 999
	session.FinalReport.FinalVariables["token"] = "updated"
	session.FinalReport.Events[0].Headers["Authorization"] = "Bearer updated"
	session.FinalReport.Events[0].ClientMetrics.LCPMs = 999

	if got := snapshot.CurrentEvent.Headers["Authorization"]; got != "Bearer original" {
		t.Fatalf("snapshot current event header = %q, want original value", got)
	}
	if got := snapshot.Events[0].Headers["Authorization"]; got != "Bearer original" {
		t.Fatalf("snapshot event header = %q, want original value", got)
	}
	if got := snapshot.FinalReport.FinalVariables["token"]; got != "original" {
		t.Fatalf("snapshot final variable = %q, want original value", got)
	}
	if got := snapshot.FinalReport.Events[0].Headers["Authorization"]; got != "Bearer original" {
		t.Fatalf("snapshot final report header = %q, want original value", got)
	}
	if snapshot.CurrentEvent.ClientMetrics == nil || snapshot.CurrentEvent.ClientMetrics.LCPMs != 240 {
		t.Fatalf("snapshot current event client metrics = %#v, want detached LCP 240", snapshot.CurrentEvent.ClientMetrics)
	}
	if snapshot.Events[0].ClientMetrics == nil || snapshot.Events[0].ClientMetrics.LCPMs != 240 {
		t.Fatalf("snapshot event client metrics = %#v, want detached LCP 240", snapshot.Events[0].ClientMetrics)
	}
	if snapshot.FinalReport.Events[0].ClientMetrics == nil || snapshot.FinalReport.Events[0].ClientMetrics.LCPMs != 240 {
		t.Fatalf("snapshot report client metrics = %#v, want detached LCP 240", snapshot.FinalReport.Events[0].ClientMetrics)
	}
}

func TestBrowserReplayManagerPrunesTerminalSessions(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-2 * time.Hour)
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Hour,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	manager.sessions["old"] = &BrowserReplaySession{
		ID:             "old",
		Status:         BrowserReplayCompleted,
		StartedAt:      &terminalAt,
		CompletedAt:    &terminalAt,
		lastActivityAt: terminalAt,
		terminalAt:     &terminalAt,
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.Get("old"); ok {
		t.Fatal("expected old terminal session to be pruned")
	}
}

// Pruning a retained tombstone must perform the durable lease release after
// dropping the manager mutex: in HA mode the release is a synchronous store
// call and running it under m.mu freezes every handler and progress path.
func TestBrowserReplayManagerPruneReleasesRetainedLeaseOffManagerLock(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-2 * time.Hour)
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Hour,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	released := false
	releasedOffLock := false
	signal := newReplaySessionLeaseSignal()
	signal.releaseRetainedIDFn = func() {
		released = true
		if manager.mu.TryLock() {
			releasedOffLock = true
			manager.mu.Unlock()
		}
	}
	manager.sessions["old"] = &BrowserReplaySession{
		ID:             "old",
		Status:         BrowserReplayCompleted,
		StartedAt:      &terminalAt,
		CompletedAt:    &terminalAt,
		lastActivityAt: terminalAt,
		terminalAt:     &terminalAt,
		reservation:    wrapSessionLease(signal),
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if !released {
		t.Fatal("retained lease was not released by prune")
	}
	if !releasedOffLock {
		t.Fatal("retained lease release ran while the manager mutex was held")
	}
}

// The completion hook performs storage writes and must run without the manager
// lock held; acquiring the write lock from inside the hook deadlocks if the
// hook is still invoked under m.mu.RLock.
func TestBrowserReplayManagerCompletionHookRunsOffManagerLock(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	manager := NewBrowserReplayManager(newTestEventSink())
	t.Cleanup(manager.Close)
	hookDone := make(chan struct{})
	manager.SetCompletionHook(func(_ context.Context, projectID string, script *dsl.Script, report *engine.ReplayReport) {
		manager.mu.Lock() //nolint:staticcheck // deadlocks (and fails the test) if the hook runs under m.mu.RLock
		manager.mu.Unlock()
		close(hookDone)
	})

	if _, err := manager.Start("hook-script", &dsl.Script{
		Name: "hook", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/step"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "hook-session"}); err != nil {
		t.Fatalf("start browser replay: %v", err)
	}
	select {
	case <-hookDone:
	case <-time.After(2 * time.Second):
		t.Fatal("completion hook did not run off the manager lock")
	}
}

// A worker that ignores cancellation past the abandon grace period must lose
// its slot and lease. The failed terminal transition is pushed before the
// capability is retired, and a late finalization must not corrupt a replacement
// that reuses the released ID.
func TestBrowserReplayManagerAbandonBackstopFreesSlotAndLease(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	releaseWorker := func() { unblockOnce.Do(func() { close(unblock) }) }
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		close(started)
		<-unblock // Deliberately ignore cancellation.
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	hub := newTestEventSink()
	manager := NewBrowserReplayManager(hub)
	manager.retention.MaxSessions = 1
	manager.retention.WorkerAbandonGrace = 50 * time.Millisecond
	t.Cleanup(manager.Close)
	t.Cleanup(releaseWorker) // run before Close so the bounded drain is fast

	var activeReleased, allReleased atomic.Bool
	manager.SetSessionIDReserver(func(_ context.Context, requested, projectID string) (string, func(), SessionLease, error) {
		signal := newReplaySessionLeaseSignal()
		signal.releaseAllFn = func() { allReleased.Store(true) }
		return requested, func() { activeReleased.Store(true) }, signal, nil
	})

	snapshot, err := manager.Start("wedged-script", &dsl.Script{
		Name: "wedged", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "blocked", Type: "request", Target: "/blocked"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "wedged-session"})
	if err != nil {
		t.Fatalf("start wedged replay: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("replay did not start")
	}
	manager.mu.RLock()
	runtime := manager.sessions[snapshot.ID]
	manager.mu.RUnlock()
	if runtime == nil {
		t.Fatal("runtime session was not retained by manager")
	}

	if _, ok := manager.Stop(snapshot.ID); !ok {
		t.Fatal("stop wedged replay returned not found")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.mu.RLock()
		status, errMsg, abandoned := runtime.Status, runtime.Error, runtime.workerAbandoned
		runtime.mu.RUnlock()
		if abandoned && status == BrowserReplayFailed &&
			strings.Contains(errMsg, "ignored cancellation") &&
			activeReleased.Load() && allReleased.Load() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session status=%q error=%q abandoned=%t active_released=%t lease_released=%t; backstop never fully orphaned the wedged worker",
				status, errMsg, abandoned, activeReleased.Load(), allReleased.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := manager.Get(snapshot.ID); ok {
		t.Fatal("orphaned session remained addressable after its global capability was released")
	}

	terminalPublished := false
	for !terminalPublished {
		select {
		case message := <-hub.broadcast:
			var envelope struct {
				Type            string                `json:"type"`
				ReplaySessionID string                `json:"replay_session_id"`
				Session         *BrowserReplaySession `json:"session"`
			}
			if err := json.Unmarshal(message.payload, &envelope); err != nil {
				t.Fatalf("decode browser replay broadcast: %v", err)
			}
			if envelope.Type != "browser_replay_failed" || envelope.ReplaySessionID != snapshot.ID {
				continue
			}
			if envelope.Session == nil || envelope.Session.Status != BrowserReplayFailed ||
				!strings.Contains(envelope.Session.Error, "ignored cancellation") {
				t.Fatalf("terminal broadcast = %#v, want orphaned failure snapshot", envelope)
			}
			terminalPublished = true
		case <-time.After(2 * time.Second):
			t.Fatal("orphaned terminal state was not published before capability retirement")
		}
	}

	// While the worker is still wedged, both its capacity slot and externally
	// supplied ID must already be reusable without a restart.
	replacement, err := manager.Start("replacement-script", &dsl.Script{
		Name: "replacement", Protocol: protocolName, ProjectID: "project-a",
	}, BrowserReplayStartRequest{ReplaySessionID: snapshot.ID, StartPaused: true})
	if err != nil {
		t.Fatalf("start exact-ID replacement after backstop = %v, want a free slot and released capability", err)
	}
	if replacement.ID != snapshot.ID {
		t.Fatalf("replacement id = %q, want released id %q", replacement.ID, snapshot.ID)
	}

	// A late-finishing worker must detect the takeover and no-op.
	releaseWorker()
	select {
	case <-runtime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("wedged worker did not finish after release")
	}
	runtime.mu.RLock()
	status, errMsg, finalReport := runtime.Status, runtime.Error, runtime.FinalReport
	runtime.mu.RUnlock()
	if status != BrowserReplayFailed || !strings.Contains(errMsg, "ignored cancellation") {
		t.Fatalf("late worker overwrote orphaned state: status=%q error=%q", status, errMsg)
	}
	if finalReport != nil {
		t.Fatal("late worker attached its report to the orphaned session")
	}
	if current, ok := manager.Get(snapshot.ID); !ok || current.Status != BrowserReplayPaused {
		t.Fatalf("late worker corrupted replacement: session=%#v addressable=%t", current, ok)
	}
}

func TestBrowserReplayManagerSessionCapEvictsOldestTerminal(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Hour,
		MaxSessions: 2,
		Now:         func() time.Time { return now },
	}
	for i, id := range []string{"oldest", "middle", "newest"} {
		terminalAt := now.Add(-time.Duration(3-i) * time.Minute)
		manager.sessions[id] = &BrowserReplaySession{
			ID:             id,
			Status:         BrowserReplayCompleted,
			StartedAt:      &terminalAt,
			CompletedAt:    &terminalAt,
			lastActivityAt: terminalAt,
			terminalAt:     &terminalAt,
		}
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.sessions["oldest"]; ok {
		t.Fatal("expected oldest terminal session to be evicted")
	}
	if len(manager.sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(manager.sessions))
	}
}

func TestBrowserReplayManagerGetDoesNotSweepUnrelatedSessions(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-2 * time.Hour)
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Hour,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	manager.sessions["active"] = &BrowserReplaySession{
		ID:             "active",
		Status:         BrowserReplayRunning,
		lastActivityAt: now.Add(-time.Minute),
	}
	manager.sessions["expired"] = &BrowserReplaySession{
		ID:             "expired",
		Status:         BrowserReplayCompleted,
		CompletedAt:    &expiredAt,
		terminalAt:     &expiredAt,
		lastActivityAt: expiredAt,
	}

	if session, ok := manager.Get("active"); !ok || session == nil || session.ID != "active" {
		t.Fatalf("Get(active) = %#v, %t", session, ok)
	}
	if _, exists := manager.sessions["expired"]; !exists {
		t.Fatal("Get(active) swept an unrelated expired session")
	}
	if got := manager.sessions["active"].lastActivityAt; !got.Equal(now) {
		t.Fatalf("active session last activity = %s, want %s", got, now)
	}
	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("janitor prune removed %d sessions, want 1", removed)
	}
	if _, exists := manager.sessions["expired"]; exists {
		t.Fatal("janitor prune retained expired session")
	}
}

func TestBrowserReplayManagerSessionCapPreservesNonTerminalSessionsWhenOverCap(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan string, 1)
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Hour,
		MaxSessions: 2,
		Now:         func() time.Time { return now },
	}
	for i, id := range []string{"oldest", "middle", "newest"} {
		lastActivity := now.Add(-time.Duration(3-i) * time.Minute)
		sessionID := id
		manager.sessions[id] = &BrowserReplaySession{
			ID:             id,
			Status:         BrowserReplayRunning,
			StartedAt:      &lastActivity,
			lastActivityAt: lastActivity,
			cancel:         func() { cancelled <- sessionID },
			stateChanged:   make(chan struct{}),
		}
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if len(manager.sessions) != 3 {
		t.Fatalf("session count = %d, want 3", len(manager.sessions))
	}
	for _, id := range []string{"oldest", "middle", "newest"} {
		if _, ok := manager.sessions[id]; !ok {
			t.Fatalf("active session %q was evicted", id)
		}
	}
	select {
	case id := <-cancelled:
		t.Fatalf("active session %q was cancelled to enforce the cap", id)
	default:
	}
}

func TestBrowserReplayManagerCancelsIdleRunningSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-2 * time.Minute)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Minute,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	manager.sessions["idle"] = &BrowserReplaySession{
		ID:             "idle",
		Status:         BrowserReplayRunning,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected idle session cancel function to be called")
	}
	session, ok := manager.Get("idle")
	if !ok {
		t.Fatal("idle session should remain fetchable until terminal TTL expires")
	}
	if session.Status != BrowserReplayStopped {
		t.Fatalf("status = %q, want stopped", session.Status)
	}
}

func TestBrowserReplayManagerCancelsIdlePausedSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-2 * time.Minute)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Minute,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	manager.sessions["paused-idle"] = &BrowserReplaySession{
		ID:             "paused-idle",
		Status:         BrowserReplayPaused,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
		stateChanged:   make(chan struct{}),
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected paused idle session cancel function to be called")
	}
	session, ok := manager.Get("paused-idle")
	if !ok {
		t.Fatal("paused idle session should remain fetchable until terminal TTL expires")
	}
	if session.Status != BrowserReplayStopped {
		t.Fatalf("status = %q, want stopped", session.Status)
	}
}

func TestBrowserReplayManagerDoesNotCancelRecentlyActivePausedSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-30 * time.Second)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Minute,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	manager.sessions["paused-active"] = &BrowserReplaySession{
		ID:             "paused-active",
		Status:         BrowserReplayPaused,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
		stateChanged:   make(chan struct{}),
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
		t.Fatal("recently active paused session should not be cancelled")
	default:
	}
	session, ok := manager.Get("paused-active")
	if !ok {
		t.Fatal("paused active session should remain fetchable")
	}
	if session.Status != BrowserReplayPaused {
		t.Fatalf("status = %q, want paused", session.Status)
	}
}

func TestBrowserReplayManagerStartReusedIDRejectsCollision(t *testing.T) {
	const sessionID = "33333333-3333-4333-8333-333333333333"
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
	manager.retention = ReplayRetentionConfig{
		TerminalTTL: time.Hour,
		IdleCancel:  time.Hour,
		MaxSessions: 250,
		Now:         func() time.Time { return now },
	}
	manager.sessions[sessionID] = &BrowserReplaySession{
		ID:             sessionID,
		ProjectID:      "project-a",
		Status:         BrowserReplayRunning,
		StartedAt:      &now,
		lastActivityAt: now,
		cancel:         func() { close(cancelled) },
	}

	started, err := manager.Start("", &dsl.Script{Name: "Replacement", Version: "1.0.0", Protocol: "browser"}, BrowserReplayStartRequest{
		ReplaySessionID: sessionID,
		StartPaused:     true,
	})
	if !errors.Is(err, ErrBrowserReplaySessionIDConflict) || started != nil {
		t.Fatalf("Start collision = session %#v, error %v", started, err)
	}
	select {
	case <-cancelled:
		t.Fatal("existing session was cancelled by colliding start")
	default:
	}
	session, ok := manager.Get(sessionID)
	if !ok {
		t.Fatal("existing session not found")
	}
	if session.Status != BrowserReplayRunning {
		t.Fatalf("existing status = %q, want running", session.Status)
	}
	_, _ = manager.Stop(sessionID)
}

func TestBrowserReplayManagerStopCancelsPausedSession(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		time.Sleep(150 * time.Millisecond)
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
		}, nil
	})

	manager := NewBrowserReplayManager(newTestEventSink())
	session, err := manager.Start("", &dsl.Script{
		Name:     "Browser Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "browser_wait_for_timeout", Value: "25"},
			{Name: "second", Type: "browser_wait_for_timeout", Value: "25"},
		},
	}, BrowserReplayStartRequest{ReplaySessionID: "session-paused-stop"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "session-paused-stop" {
		t.Fatalf("session.ID = %q, want session-paused-stop", session.ID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.Get("session-paused-stop")
		if !ok {
			t.Fatal("session not found")
		}
		if current.CurrentEventIndex >= 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	paused, ok, changed := manager.Pause("session-paused-stop")
	if !ok {
		t.Fatal("session not found while pausing")
	}
	if !changed {
		t.Fatalf("expected pause to change state, got %#v", paused)
	}
	if paused.Status != BrowserReplayPaused {
		t.Fatalf("paused status = %q, want paused", paused.Status)
	}

	stopped, ok := manager.Stop("session-paused-stop")
	if !ok {
		t.Fatal("session not found while stopping")
	}
	if stopped.Status != BrowserReplayStopped {
		t.Fatalf("stopped status = %q, want stopped", stopped.Status)
	}

	for time.Now().Before(deadline) {
		current, ok := manager.Get("session-paused-stop")
		if !ok {
			t.Fatal("session not found after stop")
		}
		if current.Status == BrowserReplayStopped && current.CompletedAt != nil {
			if current.FinalReport == nil || !current.FinalReport.Stopped || current.FinalReport.Success || current.FinalReport.Error != "" {
				t.Fatalf("stopped report: %+v", current.FinalReport)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	current, ok := manager.Get("session-paused-stop")
	if !ok {
		t.Fatal("session not found after stop deadline")
	}
	t.Fatalf("session = %#v, want stopped session with completion timestamp", current)
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

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
		t.Cleanup(manager.Close)
		var completionHooks atomic.Int32
		manager.SetCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
			completionHooks.Add(1)
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.Start("browser-script", script, BrowserReplayStartRequest{
			ReplaySessionID:  "browser-fast-fence-reuse",
			StartPaused:      true,
			ExecutionContext: epoch,
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

	t.Run("browser replay", func(t *testing.T) {
		protocolName, started, release := newBlockedReplay(t)
		manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
		manager.retention.WorkerAbandonGrace = time.Second
		t.Cleanup(manager.Close)
		t.Cleanup(release)
		var completionHooks atomic.Int32
		manager.SetCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
			completionHooks.Add(1)
		})
		epoch, loseLeadership := context.WithCancel(context.Background())
		snapshot, err := manager.Start("browser-script", &dsl.Script{
			Name: "browser fence", Protocol: protocolName,
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}, BrowserReplayStartRequest{ExecutionContext: epoch})
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

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
		t.Cleanup(manager.Close)
		epoch, release, releaseStarted, unblock := newRelease(t)
		snapshot, err := manager.Start("browser-script", script, BrowserReplayStartRequest{
			ExecutionContext:        epoch,
			ReleaseExecutionContext: release,
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

	t.Run("browser replay", func(t *testing.T) {
		protocolName, started, releaseWorker := newBlockedReplay(t)
		manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
		manager.retention.MaxSessions = 1
		manager.retention.WorkerAbandonGrace = 40 * time.Millisecond
		t.Cleanup(manager.Close)
		t.Cleanup(releaseWorker)
		var activeReleased, allReleased atomic.Bool
		manager.SetSessionIDReserver(func(_ context.Context, requested, _ string) (string, func(), SessionLease, error) {
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
		}, BrowserReplayStartRequest{ReplaySessionID: "browser-wedged", ExecutionContext: epoch})
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
		manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
		manager.retention.Now = readClock
		manager.retention.TerminalTTL = time.Minute
		manager.retention.WorkerAbandonGrace = time.Hour
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

}
func TestReplayManagersStopServingCapabilitiesPastLocalLeaseFence(t *testing.T) {
	expiredSignal := func() *replaySessionLeaseSignal {
		signal := newReplaySessionLeaseSignal()
		signal.setValidUntil(time.Now().Add(-time.Second))
		return signal
	}

	t.Run("browser replay", func(t *testing.T) {
		manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader})
		manager.sessions["expired-browser"] = &BrowserReplaySession{
			ID:           "expired-browser",
			ProjectID:    "project-a",
			Status:       BrowserReplayRunning,
			stateChanged: make(chan struct{}),
			reservation:  wrapSessionLease(expiredSignal()),
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

}

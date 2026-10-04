package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func runtimeStateFixture(t *testing.T) (*ScriptDebugManager, *ScriptDebugSession, scriptDebugController, engine.ReplayRuntimeSnapshot) {
	t.Helper()
	m := NewScriptDebugManager(nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &ScriptDebugSession{ID: "runtime-state", SessionIncarnation: uuid.NewString(), Status: ScriptDebugPaused,
		workerContext: ctx, executionContext: context.Background(), cancel: cancel, stateChanged: make(chan struct{}),
		runtimeUnavailableReason: ScriptDebugRuntimePausePending}
	m.sessions[s.ID] = s
	t.Cleanup(m.Close)
	value := "42"
	ordinal := 2
	snapshot := engine.ReplayRuntimeSnapshot{State: "complete", Boundary: "before_action",
		Location:   engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, EventIndex: 0, ActionType: "GET", ActionName: "Request"},
		Variables:  []engine.ReplayRuntimeBinding{{Name: "saved", ValueState: "assigned", Value: &value}},
		Parameters: []engine.ReplayRuntimeParameter{{Name: "counter", State: "observed", UpdatePolicy: "each_occurrence", ValueState: "observed", Value: &value, ObservedEventID: "observed-event"}},
		Rows:       []engine.ReplayRuntimeRow{{Kind: "parameter", Source: "counter", Name: "counter", RowOrdinal: &ordinal, Selection: "sequential", State: "selected"}},
		Frames:     []engine.ReplayRuntimeFrame{{Kind: "phase", Phase: "action", ActionIndex: -1}, {Kind: "foreach", Phase: "action", ActionIndex: 0, StepPath: []string{"loop[0]"}, IterationPath: []int{2}}}}
	if !engine.ValidateReplayRuntimeSnapshotBounds(snapshot) {
		t.Fatal("runtime fixture violates engine admission contract")
	}
	return m, s, scriptDebugController{manager: m, session: s}, snapshot
}

func waitRuntimeBoundary(t *testing.T, m *ScriptDebugManager, id string) *ScriptDebugSession {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, ok := m.Get(id); ok && snapshot.RuntimeState.State == "paused" {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("engine hook did not hold an authoritative runtime boundary")
	return nil
}

func TestScriptDebugRuntimeStateRequiresHeldBoundaryAndInvalidatesBeforeResume(t *testing.T) {
	m, s, c, input := runtimeStateFixture(t)
	initial, ok := m.Get(s.ID)
	if !ok || initial.StateRevision != "0" || initial.RuntimeState.Snapshot != nil || initial.RuntimeState.Reason != ScriptDebugRuntimePausePending {
		t.Fatal("requested start pause incorrectly authorized runtime values")
	}
	done := make(chan error, 1)
	go func() { done <- c.BeforeActionWithRuntimeState(s.workerContext, input.Location, input) }()
	paused := waitRuntimeBoundary(t, m, s.ID)
	if paused.RuntimeState.Version != 1 || paused.RuntimeState.Kind != "full" || paused.RuntimeState.Reason != ScriptDebugRuntimeBoundaryHeld || paused.RuntimeState.StateRevision != paused.StateRevision || paused.RuntimeState.SessionIncarnation != s.SessionIncarnation {
		t.Fatal("held pause wrapper did not bind exact identity and revision")
	}
	// Public copies and the hook input must never remain connected to the lease.
	*input.Variables[0].Value = "caller mutation"
	input.Frames[1].StepPath[0] = "caller mutation"
	*paused.RuntimeState.Snapshot.Rows[0].RowOrdinal = 99
	*paused.RuntimeState.Snapshot.Parameters[0].Value = "public mutation"
	stable, ok := m.Get(s.ID)
	if !ok || stable.StateRevision != paused.StateRevision || *stable.RuntimeState.Snapshot.Variables[0].Value != "42" || *stable.RuntimeState.Snapshot.Parameters[0].Value != "42" || *stable.RuntimeState.Snapshot.Rows[0].RowOrdinal != 2 || stable.RuntimeState.Snapshot.Frames[1].StepPath[0] != "loop[0]" {
		t.Fatal("GET advanced execution or retained caller-owned runtime state")
	}
	for _, change := range []func() *ScriptDebugSession{
		func() *ScriptDebugSession {
			v, _ := m.UpdateBreakpoints(s.ID, []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 1}})
			return v
		},
		func() *ScriptDebugSession { v, _ := m.SetBreakOnFailure(s.ID, true); return v },
	} {
		updated := change()
		if updated == nil || updated.RuntimeState.State != "paused" || updated.RuntimeState.Snapshot == nil {
			t.Fatal("configuration invalidated the same held boundary")
		}
	}
	s.mu.Lock()
	s.runtimeRevision = 9
	s.mu.Unlock()
	beforeResume, _ := m.Get(s.ID)
	resumed, ok, changed := m.Resume(s.ID)
	beforeRevision, beforeErr := strconv.ParseUint(beforeResume.StateRevision, 10, 64)
	afterRevision, afterErr := strconv.ParseUint(resumed.StateRevision, 10, 64)
	if !ok || !changed || beforeErr != nil || afterErr != nil || afterRevision <= beforeRevision || resumed.RuntimeState.Snapshot != nil || resumed.RuntimeState.Reason != ScriptDebugRuntimeNotPaused {
		t.Fatal("resume response retained the old boundary values")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("resumed boundary returned an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not release the engine hook")
	}
	after, _ := m.Get(s.ID)
	if after.StateRevision != resumed.StateRevision || after.RuntimeState.Snapshot != nil {
		t.Fatal("hook exit restored or changed a cleared boundary")
	}
}

func TestScriptDebugRuntimeStatePendingPausesAndAfterFailure(t *testing.T) {
	for _, kind := range []string{"user pause", "step completion", "success after", "already paused after"} {
		t.Run(kind, func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			s.Status = ScriptDebugRunning
			s.runtimeUnavailableReason = ScriptDebugRuntimeNotPaused
			s.BreakOnFailure = true
			switch kind {
			case "user pause":
				m.Pause(s.ID)
			case "step completion":
				s.stepOnce = true
				m.handleReplayProgress(s.workerContext, s, engine.ReplayProgressEvent{Type: engine.ReplayProgressEventRun})
			case "success after":
				input.Boundary = "after_action"
				if err := c.AfterActionWithRuntimeState(s.workerContext, input.Location, &engine.ReplayActionEvent{Success: true}, input); err != nil {
					t.Fatal(err)
				}
			case "already paused after":
				s.Status = ScriptDebugPaused
				input.Boundary = "after_action"
				if err := c.AfterActionWithRuntimeState(s.workerContext, input.Location, &engine.ReplayActionEvent{}, input); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := m.Get(s.ID)
			if !ok || got.RuntimeState.Snapshot != nil || got.RuntimeState.State != "unavailable" {
				t.Fatal("status-only pause or nonwaiting after hook authorized values")
			}
		})
	}
	m, s, c, input := runtimeStateFixture(t)
	s.Status = ScriptDebugRunning
	s.runtimeUnavailableReason = ScriptDebugRuntimeNotPaused
	s.BreakOnFailure = true
	input.Boundary = "after_action"
	done := make(chan error, 1)
	go func() {
		done <- c.AfterActionWithRuntimeState(s.workerContext, input.Location, &engine.ReplayActionEvent{Success: false}, input)
	}()
	paused := waitRuntimeBoundary(t, m, s.ID)
	if paused.RuntimeState.Snapshot.Boundary != "after_action" {
		t.Fatal("failure wait lost its actual after-action boundary")
	}
	m.Resume(s.ID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("failure boundary did not resume")
	}
}

func TestScriptDebugRuntimeStatePauseDuringExecuteWaitsForNextActualBoundary(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/first" {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("X-Saved", "actual-boundary-value")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	m := NewScriptDebugManager(nil)
	t.Cleanup(m.Close)
	script := &dsl.Script{Name: "Actual pending pause", Protocol: "http", Version: "1", Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{
		{Name: "Held request", Type: "GET", Target: "/first", Extractors: []dsl.Extractor{{Name: "saved", Source: "header", Type: "header", Expression: "X-Saved"}}},
		{Name: "Consumer", Type: "GET", Target: "/second/{{var.saved}}"},
	}}
	initial, err := m.StartChecked("script", script, ScriptDebugStartRequest{ReplaySessionID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not enter real protocol execution")
	}
	pending, ok, changed := m.Pause(initial.ID)
	if !ok || !changed || pending.Status != ScriptDebugPaused || pending.RuntimeState.Reason != ScriptDebugRuntimePausePending || pending.RuntimeState.Snapshot != nil {
		t.Fatal("mid-request pause incorrectly authorized runtime values")
	}
	for range 3 {
		got, ok := m.Get(initial.ID)
		if !ok || got.StateRevision != pending.StateRevision || got.RuntimeState.Snapshot != nil || hits.Load() != 1 {
			t.Fatal("mid-request GET advanced execution or exposed a false paused state")
		}
	}
	releaseOnce.Do(func() { close(release) })
	paused := waitRuntimeBoundary(t, m, initial.ID)
	if paused.RuntimeState.Snapshot.Boundary != "before_action" || paused.RuntimeState.Snapshot.Location.ActionIndex != 1 || hits.Load() != 1 {
		t.Fatal("pause did not wait at the next actual action boundary")
	}
	found := false
	for _, binding := range paused.RuntimeState.Snapshot.Variables {
		if binding.Name == "saved" && binding.Value != nil && *binding.Value == "actual-boundary-value" {
			found = true
		}
	}
	if !found {
		t.Fatal("held boundary omitted an actual completed extraction")
	}
	m.Resume(initial.ID)
	finished := waitForScriptDebugStatus(t, m, initial.ID, ScriptDebugCompleted)
	if hits.Load() != 2 || finished.FinalReport == nil || len(finished.FinalReport.FinalVariables) == 0 || finished.RuntimeState.Snapshot != nil || finished.RuntimeState.Reason != ScriptDebugRuntimeSessionTerminal {
		t.Fatal("completion reconstructed current runtime state from final variables")
	}
}

// Gate only the first wait expression, after the hook has published its held
// boundary. Real controls can complete while that invocation cannot return.
type runtimeWaitGateContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *runtimeWaitGateContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Context.Done()
}

func TestScriptDebugRuntimeStateRapidResumePauseReadmitsOnlyTheLiveHook(t *testing.T) {
	for _, boundary := range []string{"before_action", "after_action"} {
		t.Run(boundary, func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			input.Boundary = boundary
			if boundary == "after_action" {
				s.Status = ScriptDebugRunning
				s.runtimeUnavailableReason = ScriptDebugRuntimeNotPaused
				s.BreakOnFailure = true
			}
			gate := &runtimeWaitGateContext{Context: s.workerContext, entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate.release) }) }
			t.Cleanup(release)
			done := make(chan error, 1)
			go func() {
				if boundary == "before_action" {
					done <- c.BeforeActionWithRuntimeState(gate, input.Location, input)
				} else {
					done <- c.AfterActionWithRuntimeState(gate, input.Location, &engine.ReplayActionEvent{Success: false}, input)
				}
			}()
			first := waitRuntimeBoundary(t, m, s.ID)
			select {
			case <-gate.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("hook did not reach synchronized wait")
			}
			s.mu.Lock()
			oldToken := s.heldBoundary
			s.mu.Unlock()
			resumed, ok, changed := m.Resume(s.ID)
			if !ok || !changed || resumed.RuntimeState.Snapshot != nil {
				t.Fatal("resume retained values before waking its held hook")
			}
			pending, ok, changed := m.Pause(s.ID)
			if !ok || !changed || pending.RuntimeState.Snapshot != nil || pending.RuntimeState.Reason != ScriptDebugRuntimePausePending {
				t.Fatal("pause request authorized values before the hook waited again")
			}
			release()
			second := waitRuntimeBoundary(t, m, s.ID)
			if second.StateRevision == first.StateRevision || second.StateRevision == pending.StateRevision || *second.RuntimeState.Snapshot.Variables[0].Value != "42" || second.RuntimeState.Snapshot.Boundary != boundary {
				t.Fatal("live hook failed to re-admit its exact boundary candidate")
			}
			select {
			case <-done:
				t.Fatal("re-paused hook escaped its wait")
			default:
			}
			stable, _ := m.Get(s.ID)
			if stable.StateRevision != second.StateRevision {
				t.Fatal("same-token admission advanced revision repeatedly")
			}
			m.Resume(s.ID)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("resumed hook returned an error")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("old boundary did not leave after resume")
			}
			m.Pause(s.ID)
			left, _ := m.Get(s.ID)
			if left.RuntimeState.Snapshot != nil {
				t.Fatal("pause resurrected a candidate from a hook that already left")
			}
			// The next full snapshot replaces every array, including deleted bindings.
			input.Boundary = "before_action"
			input.Location.ActionIndex = 1
			input.Location.EventIndex = 1
			input.Variables = []engine.ReplayRuntimeBinding{}
			input.Parameters = []engine.ReplayRuntimeParameter{}
			input.Rows = []engine.ReplayRuntimeRow{}
			input.Frames = []engine.ReplayRuntimeFrame{}
			go func() { done <- c.BeforeActionWithRuntimeState(s.workerContext, input.Location, input) }()
			next := waitRuntimeBoundary(t, m, s.ID)
			if len(next.RuntimeState.Snapshot.Variables) != 0 || len(next.RuntimeState.Snapshot.Parameters) != 0 || len(next.RuntimeState.Snapshot.Rows) != 0 || len(next.RuntimeState.Snapshot.Frames) != 0 {
				t.Fatal("full snapshot retained deleted bindings or frames")
			}
			c.leaveRuntimeBoundary(oldToken)
			got, _ := m.Get(s.ID)
			if got.RuntimeState.State != "paused" || got.StateRevision != next.StateRevision {
				t.Fatal("stale hook cleanup cleared the next held boundary")
			}
			m.Resume(s.ID)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("new boundary did not resume")
			}
		})
	}
}

func TestScriptDebugRuntimeStateSynchronousAuthorityChecks(t *testing.T) {
	for _, kind := range []string{"boundary context", "execution context", "worker context", "worker finished", "lease failure", "lease expiry"} {
		t.Run(kind, func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			boundaryCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			token := &scriptDebugHeldBoundary{ctx: boundaryCtx, snapshot: c.runtimeCandidate(&input, input.Location, "before_action")}
			s.mu.Lock()
			c.holdRuntimeBoundaryLocked(token)
			s.mu.Unlock()
			prior, _ := m.Get(s.ID)
			reason := ScriptDebugRuntimeContextCanceled
			switch kind {
			case "boundary context":
				cancel()
			case "execution context":
				s.executionContext = boundaryCtx
				cancel()
				reason = ScriptDebugRuntimeIntegrityFenceLost
			case "worker context":
				s.workerContext = boundaryCtx
				cancel()
			case "worker finished":
				s.workerFinished = true
				reason = ScriptDebugRuntimeSessionTerminal
			case "lease failure":
				s.reservation = newReplaySessionLeaseSignal()
				s.reservation.fail(errReplaySessionLeaseLost)
				reason = ScriptDebugRuntimeIntegrityFenceLost
			case "lease expiry":
				s.reservation = newReplaySessionLeaseSignal()
				s.reservation.setValidUntil(time.Now().Add(-time.Second))
				reason = ScriptDebugRuntimeIntegrityFenceLost
			}
			// No reconciliation goroutine is present in this fixture.
			got, ok := m.Get(s.ID)
			if strings.HasPrefix(kind, "lease") && ok {
				t.Fatal("lost lease remained a public capability")
			}
			if !ok {
				got = s.snapshot()
			}
			if got.RuntimeState.Snapshot != nil || got.RuntimeState.Reason != reason || got.StateRevision == prior.StateRevision {
				t.Fatal("synchronous reader retained values after authority loss")
			}
			data, _, exported := m.Export(s.ID, "json")
			if exported {
				var exportedSession struct {
					Session ScriptDebugSession `json:"session"`
				}
				if err := json.Unmarshal(data, &exportedSession); err != nil {
					t.Fatal("export did not contain a valid session")
				}
				if exportedSession.Session.RuntimeState.State != "unavailable" || exportedSession.Session.RuntimeState.Snapshot != nil || exportedSession.Session.RuntimeState.Reason != reason {
					t.Fatal("export resurrected invalidated values")
				}
			}
		})
	}
}

func TestScriptDebugRuntimeStateFinishWorkerRevokesBeforeOwnershipRelease(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "held boundary", true: "resumed active candidate"}[resumed], func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			token := &scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: c.runtimeCandidate(&input, input.Location, "before_action")}
			s.mu.Lock()
			c.holdRuntimeBoundaryLocked(token)
			s.mu.Unlock()
			if resumed {
				m.Resume(s.ID)
			}
			s.workerDone = make(chan struct{})
			report := &engine.ReplayReport{Success: true}
			s.FinalReport = report
			status := s.Status
			releasedClean := false
			s.releaseID = func() {
				s.mu.Lock()
				releasedClean = s.heldBoundary == nil && token.snapshot == nil && token.candidate == nil && token.invalidated && s.workerFinished
				s.mu.Unlock()
			}
			m.finishWorker(s)
			if !releasedClean || s.Status != status || s.FinalReport != report {
				t.Fatal("worker release retained authority or altered execution status/report")
			}
			select {
			case <-s.workerDone:
			default:
				t.Fatal("finished worker did not close its ownership signal")
			}
			got, ok := m.Get(s.ID)
			if !ok || got.RuntimeState.Snapshot != nil || got.RuntimeState.Reason != ScriptDebugRuntimeSessionTerminal {
				t.Fatal("finished worker remained authoritative in a synchronous reader")
			}
			s.mu.Lock()
			s.Status = ScriptDebugPaused
			c.holdRuntimeBoundaryLocked(token)
			admitted := s.heldBoundary != nil
			s.mu.Unlock()
			if admitted {
				t.Fatal("finished worker re-admitted a runtime boundary")
			}
		})
	}
}

func TestScriptDebugRuntimeStateIrreversibleLossClearsResumedHookCandidate(t *testing.T) {
	for _, kind := range []string{"privacy", "boundary context", "execution context", "worker context", "lease", "terminal", "worker finish", "retire", "close", "revision exhaustion", "action started"} {
		t.Run(kind, func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			token := &scriptDebugHeldBoundary{ctx: ctx, snapshot: c.runtimeCandidate(&input, input.Location, "before_action")}
			s.mu.Lock()
			c.holdRuntimeBoundaryLocked(token)
			s.mu.Unlock()
			m.Resume(s.ID)
			s.mu.Lock()
			retained := token.candidate != nil && token.snapshot == nil && s.heldBoundary == nil
			s.mu.Unlock()
			if !retained {
				t.Fatal("reversible resume did not preserve only the live hook candidate")
			}
			switch kind {
			case "privacy":
				s.mu.Lock()
				s.omitRuntimeEvidenceLocked()
				s.mu.Unlock()
			case "boundary context":
				cancel()
			case "execution context":
				s.executionContext = ctx
				cancel()
			case "worker context":
				s.workerContext = ctx
				cancel()
			case "lease":
				s.reservation = newReplaySessionLeaseSignal()
				s.reservation.fail(errReplaySessionLeaseLost)
			case "terminal":
				s.Status = ScriptDebugCompleted
			case "worker finish":
				m.finishWorker(s)
			case "retire":
				m.retireUnfencedSession(s)
			case "close":
				m.Close()
			case "revision exhaustion":
				s.mu.Lock()
				s.runtimeRevision = math.MaxUint64
				s.advanceRuntimeRevisionLocked()
				s.mu.Unlock()
			case "action started":
				m.handleReplayProgress(s.workerContext, s, engine.ReplayProgressEvent{Type: engine.ReplayProgressActionStarted, Action: &input.Location})
			}
			m.Get(s.ID)
			s.snapshot() // Also reconcile a record whose public lookup was retired.
			s.mu.Lock()
			cleared := token.candidate == nil && token.snapshot == nil && token.invalidated
			s.Status = ScriptDebugPaused
			c.holdRuntimeBoundaryLocked(token)
			resurrected := s.heldBoundary == token
			s.mu.Unlock()
			if !cleared || resurrected {
				t.Fatal("irreversible authority loss retained or re-admitted a resumed hook candidate")
			}
		})
	}
}

func TestScriptDebugRuntimeStateInvalidationLifecycleAndTokenIdentity(t *testing.T) {
	for _, kind := range []string{"step", "step over", "step loop", "stop", "idle", "terminal", "worker finish", "abandon", "retire", "prune", "close", "action started", "hook exit"} {
		t.Run(kind, func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			token := &scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: c.runtimeCandidate(&input, input.Location, "before_action")}
			s.mu.Lock()
			c.holdRuntimeBoundaryLocked(token)
			s.mu.Unlock()
			prior := s.snapshot()
			switch kind {
			case "step":
				m.Step(s.ID)
			case "step over":
				m.StepMode(s.ID, "over")
			case "step loop":
				m.StepMode(s.ID, "loop_end")
			case "stop":
				m.Stop(s.ID)
			case "idle":
				s.mu.Lock()
				m.cancelSessionLocked(s, time.Now(), "idle timeout")
				s.mu.Unlock()
			case "terminal":
				s.mu.Lock()
				s.Status = ScriptDebugCompleted
				s.mu.Unlock()
			case "worker finish":
				m.finishWorker(s)
			case "retire":
				m.retireUnfencedSession(s)
			case "abandon":
				m.abandonUnresponsiveWorker(s, time.Second)
			case "prune":
				s.mu.Lock()
				s.Status = ScriptDebugCompleted
				past := time.Now().Add(-3 * time.Hour)
				s.terminalAt = &past
				s.mu.Unlock()
				m.PruneExpired()
			case "close":
				m.Close()
			case "action started":
				m.handleReplayProgress(s.workerContext, s, engine.ReplayProgressEvent{Type: engine.ReplayProgressActionStarted, Action: &input.Location})
			case "hook exit":
				c.leaveRuntimeBoundary(token)
			}
			got := s.snapshot()
			if got.RuntimeState.Snapshot != nil || got.StateRevision == prior.StateRevision {
				t.Fatal("lifecycle transition retained boundary authority")
			}
		})
	}
	_, s, c, input := runtimeStateFixture(t)
	old := &scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: &input}
	newToken := &scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: c.runtimeCandidate(&input, input.Location, "before_action")}
	s.mu.Lock()
	c.holdRuntimeBoundaryLocked(newToken)
	s.mu.Unlock()
	prior := s.snapshot()
	c.leaveRuntimeBoundary(old)
	got := s.snapshot()
	if got.RuntimeState.Snapshot == nil || got.StateRevision != prior.StateRevision {
		t.Fatal("old hook defer cleared a newer held boundary")
	}
}

func TestScriptDebugRuntimeStateMalformedAdmissionPrivacyAndRevisionExhaustion(t *testing.T) {
	for _, kind := range []string{"nil legacy", "oversized", "wrong boundary", "wrong location", "work limit", "classification failure"} {
		t.Run(kind, func(t *testing.T) {
			m, s, c, input := runtimeStateFixture(t)
			source := &input
			switch kind {
			case "nil legacy":
				source = nil
			case "oversized":
				input.Location.Target = strings.Repeat("x", engine.MaxReplayRuntimeSnapshotValueBytes+1)
			case "wrong boundary":
				input.Boundary = "after_action"
			case "wrong location":
				input.Location.ActionIndex = 99
			case "work limit":
				input.State = "unavailable"
				input.UnavailableReason = "work_limit"
			case "classification failure":
				input.RuntimeEvidenceUnavailable = true
				input.State = "malformed"
				input.Location.Target = "unpublishable-private-detail"
			}
			action := engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, EventIndex: 0, ActionType: "GET", ActionName: "Request"}
			candidate := c.runtimeCandidate(source, action, "before_action")
			if candidate != nil {
				t.Fatal("invalid engine DTO was retained")
			}
			s.mu.Lock()
			c.holdRuntimeBoundaryLocked(&scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: candidate})
			s.mu.Unlock()
			got, _ := m.Get(s.ID)
			if got.RuntimeState.Snapshot != nil {
				t.Fatal("malformed admission authorized values")
			}
			if (kind == "classification failure") != got.RuntimeEvidenceUnavailable {
				t.Fatal("bounds failure and sticky privacy failure were conflated")
			}
			if kind == "classification failure" {
				s.mu.Lock()
				c.holdRuntimeBoundaryLocked(&scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: &input})
				s.mu.Unlock()
				if s.snapshot().RuntimeState.Reason != ScriptDebugRuntimeEvidenceUnavailable {
					t.Fatal("later candidate restored classified evidence")
				}
			}
		})
	}
	_, s, c, input := runtimeStateFixture(t)
	s.runtimeRevision = math.MaxUint64
	s.mu.Lock()
	c.holdRuntimeBoundaryLocked(&scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: &input})
	s.mu.Unlock()
	got := s.snapshot()
	if got.StateRevision != strconv.FormatUint(math.MaxUint64, 10) || got.RuntimeState.Reason != ScriptDebugRuntimeRevisionExhausted || got.RuntimeState.Snapshot != nil {
		t.Fatal("revision overflow wrapped or retained values")
	}
	s.mu.Lock()
	s.invalidateRuntimeStateLocked(ScriptDebugRuntimePausePending)
	c.holdRuntimeBoundaryLocked(&scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: &input})
	s.mu.Unlock()
	if s.snapshot().RuntimeState.Reason != ScriptDebugRuntimeRevisionExhausted {
		t.Fatal("revision exhaustion was not permanent")
	}
}

func TestScriptDebugRuntimeStateWireFullReplacementAndCheapDeltas(t *testing.T) {
	m, s, c, input := runtimeStateFixture(t)
	hub := NewHub()
	t.Cleanup(hub.Stop)
	m.hub = hub
	candidate := c.runtimeCandidate(&input, input.Location, "before_action")
	s.mu.Lock()
	c.holdRuntimeBoundaryLocked(&scriptDebugHeldBoundary{ctx: s.workerContext, snapshot: candidate})
	s.mu.Unlock()
	m.broadcast("script_debug_paused", s, 0, nil, nil, nil)
	var full map[string]json.RawMessage
	if err := json.Unmarshal((<-hub.broadcast).payload, &full); err != nil {
		t.Fatal(err)
	}
	var session ScriptDebugSession
	if err := json.Unmarshal(full["session"], &session); err != nil {
		t.Fatal(err)
	}
	if string(full["state_revision"]) != `"`+session.StateRevision+`"` || string(full["session_incarnation"]) != `"`+session.SessionIncarnation+`"` || session.RuntimeState.Snapshot == nil {
		t.Fatal("lifecycle frame mixed identity, revision or state")
	}
	for _, kind := range []string{"script_debug_action", "script_debug_action_started", "script_debug_event", "script_debug_action_completed", "script_debug_action_failed", "script_debug_condition"} {
		m.broadcast(kind, s, 0, nil, nil, nil)
		var delta map[string]json.RawMessage
		if err := json.Unmarshal((<-hub.broadcast).payload, &delta); err != nil {
			t.Fatal(err)
		}
		if delta["session"] != nil || delta["runtime_state"] != nil || delta["snapshot"] != nil || delta["session_incarnation"] == nil || delta["state_revision"] == nil || delta["status"] == nil {
			t.Fatal("leaf WebSocket frame retained runtime maps or omitted identity metadata")
		}
	}
	s.mu.Lock()
	s.Status = ScriptDebugCompleted
	s.mu.Unlock()
	data, _, ok := m.Export(s.ID, "json")
	if !ok || !strings.Contains(string(data), `"runtime_state"`) || !strings.Contains(string(data), `"snapshot": null`) || strings.Contains(string(data), `"value": "42"`) {
		t.Fatal("terminal export omitted full invalidation or retained values")
	}
}

func TestScriptDebugRuntimeStateIncarnationChangesWhenPublicIDIsReused(t *testing.T) {
	m := NewScriptDebugManager(nil)
	t.Cleanup(m.Close)
	script := &dsl.Script{Name: "Empty", Protocol: "http", Version: "1"}
	first, err := m.StartChecked("script", script, ScriptDebugStartRequest{ReplaySessionID: "reused-public-id"})
	if err != nil {
		t.Fatal(err)
	}
	waitForScriptDebugStatus(t, m, first.ID, ScriptDebugCompleted)
	m.mu.Lock()
	live := m.sessions[first.ID]
	m.mu.Unlock()
	select {
	case <-live.workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not drain")
	}
	live.mu.Lock()
	past := time.Now().Add(-3 * time.Hour)
	live.terminalAt = &past
	live.mu.Unlock()
	m.PruneExpired()
	second, err := m.StartChecked("script", script, ScriptDebugStartRequest{ReplaySessionID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionIncarnation == second.SessionIncarnation || first.SessionIncarnation == "" || second.SessionIncarnation == "" {
		t.Fatal("reused public ID retained the server incarnation")
	}
	if _, err := uuid.Parse(second.SessionIncarnation); err != nil {
		t.Fatal("server incarnation is not a UUID")
	}
}

package recordbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type portReplayProtocol struct{}

func (*portReplayProtocol) Name() string                               { return "recordbridge-port" }
func (*portReplayProtocol) Init(context.Context, map[string]any) error { return nil }
func (*portReplayProtocol) Execute(_ context.Context, a *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: a.Name, Success: true, StatusCode: 200}, nil
}
func (*portReplayProtocol) Teardown(context.Context) error    { return nil }
func (*portReplayProtocol) HealthCheck(context.Context) error { return nil }

var portReplaySerial atomic.Uint64

func portReplayScript(t *testing.T) *dsl.Script {
	t.Helper()
	name := fmt.Sprintf("recordbridge-port-%d", portReplaySerial.Add(1))
	protocol.Register(name, func() protocol.Protocol { return &portReplayProtocol{} })
	return &dsl.Script{Name: "portable replay", Protocol: name, ProjectID: "project-port", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}}
}

type portLease struct {
	mu       sync.Mutex
	done     chan struct{}
	err      error
	retained int
	released int
}

func (l *portLease) Done() <-chan struct{} { return l.done }
func (l *portLease) Fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		if err == nil {
			err = context.Canceled
		}
		l.err = err
		close(l.done)
	}
}
func (l *portLease) FailureOrExpired(time.Time) error { l.mu.Lock(); defer l.mu.Unlock(); return l.err }
func (l *portLease) RetainForTerminal(time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.retained++
	return nil
}
func (l *portLease) ReleaseRetainedID() { l.mu.Lock(); defer l.mu.Unlock(); l.released++ }
func (l *portLease) ReleaseAll()        { l.ReleaseRetainedID() }
func waitPortWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("replay worker did not finish")
	}
}

func TestReplayManagerPortsPreserveScopeAndCompletion(t *testing.T) {
	sink := newTestEventSink()
	lease := &portLease{done: make(chan struct{})}
	epoch := context.WithValue(context.Background(), struct{}{}, "original epoch")
	var released atomic.Int32
	completion := make(chan context.Context, 1)
	manager := NewBrowserReplayManager(sink, ReplayManagerOptions{
		ReserveID: func(ctx context.Context, id, project string) (string, func(), SessionLease, error) {
			if ctx != epoch || id != "requested" || project != "project-port" {
				t.Errorf("reservation inputs = %v %q %q", ctx, id, project)
			}
			return "reserved", func() { released.Add(1) }, lease, nil
		},
		OnComplete: func(ctx context.Context, id string, _ *dsl.Script, report *engine.ReplayReport) {
			if id != "project-port" || report == nil {
				t.Errorf("completion = %q %#v", id, report)
			}
			completion <- ctx
		},
	})
	t.Cleanup(manager.Close)
	snapshot, err := manager.Start("script-id", portReplayScript(t), BrowserReplayStartRequest{ReplaySessionID: "requested", ExecutionContext: epoch})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	live := manager.sessions[snapshot.ID]
	manager.mu.RUnlock()
	waitPortWorker(t, live.workerDone)
	select {
	case ctx := <-completion:
		if ctx != epoch {
			t.Fatal("completion lost original epoch")
		}
	default:
		t.Fatal("completion was not called")
	}
	final, ok := manager.Get("reserved")
	if !ok || final.Status != BrowserReplayCompleted || !final.WorkerSettled() || final.FinalReport == nil {
		t.Fatalf("terminal snapshot: %#v %v", final, ok)
	}
	if released.Load() != 1 {
		t.Fatalf("active reservation release calls = %d", released.Load())
	}
	lease.mu.Lock()
	retained := lease.retained
	lease.mu.Unlock()
	if retained != 1 {
		t.Fatalf("retain calls = %d", retained)
	}
	found := false
	for len(sink.broadcast) > 0 {
		event := <-sink.broadcast
		if event.projectID != "project-port" || event.testID != "" {
			t.Fatalf("event scope = %#v", event)
		}
		var payload map[string]any
		if err := json.Unmarshal(event.payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["type"] == "browser_replay_completed" {
			found = true
		}
	}
	if !found {
		t.Fatal("completion event missing")
	}
}

func TestReplayManagerAdmissionFailureReleasesExecutionContext(t *testing.T) {
	want := errors.New("admission refused")
	var released atomic.Int32
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ValidateStart: func(*dsl.Script) error { return want }})
	t.Cleanup(manager.Close)
	_, err := manager.Start("", nil, BrowserReplayStartRequest{ReleaseExecutionContext: func() { released.Add(1) }})
	if !errors.Is(err, want) || released.Load() != 1 {
		t.Fatalf("admission = %v, release calls = %d", err, released.Load())
	}
}

func TestReplayManagerLeaseLossRetiresCapabilityWithoutCompletion(t *testing.T) {
	lease := &portLease{done: make(chan struct{})}
	want := errors.New("lease lost")
	var completed atomic.Int32
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{
		ReserveID: func(context.Context, string, string) (string, func(), SessionLease, error) {
			return "lost", func() {}, lease, nil
		},
		OnComplete: func(context.Context, string, *dsl.Script, *engine.ReplayReport) { completed.Add(1) },
	})
	t.Cleanup(manager.Close)
	started, err := manager.Start("script", portReplayScript(t), BrowserReplayStartRequest{StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	live := manager.sessions[started.ID]
	manager.mu.RUnlock()
	lease.Fail(want)
	waitPortWorker(t, live.workerDone)
	if _, ok := manager.Get(started.ID); ok {
		t.Fatal("lost capability remains addressable")
	}
	if completed.Load() != 0 {
		t.Fatal("lost lease invoked completion")
	}
	live.mu.RLock()
	failure := live.Error
	live.mu.RUnlock()
	if failure != want.Error() {
		t.Fatalf("failure = %q", failure)
	}
}

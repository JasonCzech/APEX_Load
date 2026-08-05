package api

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type localBlockingPreflightFailureProtocol struct {
	entered chan struct{}
	release chan struct{}
	once    *sync.Once
}

func (*localBlockingPreflightFailureProtocol) Name() string {
	return "local_blocking_preflight_failure_archive_test"
}
func (*localBlockingPreflightFailureProtocol) Init(context.Context, map[string]any) error { return nil }
func (*localBlockingPreflightFailureProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*localBlockingPreflightFailureProtocol) Teardown(context.Context) error    { return nil }
func (*localBlockingPreflightFailureProtocol) HealthCheck(context.Context) error { return nil }
func (p *localBlockingPreflightFailureProtocol) PreflightRuntime(context.Context, protocol.RuntimePreflightRequest) (protocol.RuntimePreflightResult, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return protocol.RuntimePreflightResult{}, errors.New("fixture local runtime preflight failed")
}

func TestLocalExecutionBuildFailureFinalizesArchiveWriter(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID: "build-failure-finalizes-archive", Name: "invalid workload", ProjectID: storage.DefaultProjectID,
		Status: "INITIALIZING", Duration: time.Minute, DurationStr: time.Minute.String(),
		executionCtx: ctx, cancel: cancel,
		Groups: []TestGroup{{Name: "invalid", ScriptName: "script", VUsers: 0}},
		groupSpecs: []engine.GroupConfig{{
			Name: "invalid", VUsers: 0,
			Script: &dsl.Script{Name: "script", Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}},
		}},
	}
	runner := NewTestRunner(1, nil, nil)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	(localExecutionBackend{}).Run(runner, test)

	runner.mu.RLock()
	status := test.Status
	archive := test.Archive
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status = %q, want FAILED", status)
	}
	if archive == nil || archive.ManifestPath == "" {
		t.Fatalf("failed pre-run archive = %#v, want finalized manifest", archive)
	}
}

func TestLocalExecutionBuildFailurePreservesConcurrentAbortOutcome(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID: "build-failure-abort-race", Name: "invalid workload", ProjectID: storage.DefaultProjectID,
		Status: "INITIALIZING", Duration: time.Minute, DurationStr: time.Minute.String(),
		executionCtx: ctx, cancel: cancel,
		Groups: []TestGroup{{Name: "invalid", ScriptName: "script", VUsers: 0}},
		groupSpecs: []engine.GroupConfig{{
			Name: "invalid", VUsers: 0,
			Script: &dsl.Script{Name: "script", Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}},
		}},
	}
	runner := NewTestRunner(1, nil, nil)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	buildFailed := make(chan struct{})
	releaseBuilder := make(chan struct{})
	runner.beforeLocalBuildFailureFinalize = func() {
		close(buildFailed)
		<-releaseBuilder
	}
	done := make(chan struct{})
	go func() {
		(localExecutionBackend{}).Run(runner, test)
		close(done)
	}()
	<-buildFailed
	if err := runner.AbortTest(test.ID); err != nil {
		t.Fatalf("AbortTest: %v", err)
	}
	close(releaseBuilder)
	<-done

	runner.mu.RLock()
	status := test.Status
	archive := test.Archive
	runner.mu.RUnlock()
	if status != "ABORTED" {
		t.Fatalf("status = %q, want ABORTED", status)
	}
	if archive == nil || archive.ManifestPath == "" {
		t.Fatalf("aborted pre-run archive = %#v, want finalized manifest", archive)
	}
	manifest, err := os.ReadFile(archive.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"status": "ABORTED"`) || strings.Contains(string(manifest), `"status": "FAILED"`) {
		t.Fatalf("manifest recorded the losing build outcome: %s", manifest)
	}
}

func TestLocalRuntimePreflightFailurePreservesConcurrentAbortOutcome(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	preflightEntered := make(chan struct{})
	releasePreflight := make(chan struct{})
	var enteredOnce sync.Once
	protocol.Register("local_blocking_preflight_failure_archive_test", func() protocol.Protocol {
		return &localBlockingPreflightFailureProtocol{entered: preflightEntered, release: releasePreflight, once: &enteredOnce}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := &dsl.Script{
		Name: "preflight", Protocol: "local_blocking_preflight_failure_archive_test",
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	test := &ManagedTest{
		ID: "preflight-failure-abort-race", Name: "preflight failure", ProjectID: storage.DefaultProjectID,
		Status: "INITIALIZING", Duration: time.Minute, DurationStr: time.Minute.String(),
		executionCtx: ctx, cancel: cancel, TotalVUsers: 1,
		Groups:     []TestGroup{{Name: "default", ScriptName: script.Name, VUsers: 1}},
		groupSpecs: []engine.GroupConfig{{Name: "default", VUsers: 1, Script: script}},
	}
	runner := NewTestRunner(1, nil, nil)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	done := make(chan struct{})
	go func() {
		(localExecutionBackend{}).Run(runner, test)
		close(done)
	}()
	select {
	case <-preflightEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("local runtime preflight did not start")
	}
	if err := runner.AbortTest(test.ID); err != nil {
		t.Fatalf("AbortTest: %v", err)
	}
	close(releasePreflight)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("local run did not finish after preflight release")
	}

	runner.mu.RLock()
	status := test.Status
	archive := test.Archive
	runner.mu.RUnlock()
	if status != "ABORTED" {
		t.Fatalf("status = %q, want ABORTED", status)
	}
	if archive == nil || archive.ManifestPath == "" {
		t.Fatalf("aborted preflight archive = %#v, want finalized manifest", archive)
	}
	manifest, err := os.ReadFile(archive.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"status": "ABORTED"`) || strings.Contains(string(manifest), `"status": "FAILED"`) {
		t.Fatalf("manifest recorded the losing preflight outcome: %s", manifest)
	}
}

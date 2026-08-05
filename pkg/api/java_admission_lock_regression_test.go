package api

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type lockObservingAuthoringScriptStore struct {
	scriptPersistence
	reader    authoringSessionReader
	runner    *TestRunner
	reads     atomic.Int32
	underLock atomic.Bool
}

func (s *lockObservingAuthoringScriptStore) GetAuthoringSession(id string) (*storage.AuthoringSession, error) {
	s.reads.Add(1)
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.underLock.Store(true)
		}
	}
	return s.reader.GetAuthoringSession(id)
}

func (s *lockObservingAuthoringScriptStore) reset() {
	s.reads.Store(0)
	s.underLock.Store(false)
}

func TestJavaAdmissionSessionReadsRunOutsideLiveRunnerLock(t *testing.T) {
	const projectID = "project-java-admission-lock"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPersistentLifecycleV1)
	reader, ok := server.runner.scriptStore.(authoringSessionReader)
	if !ok {
		t.Fatal("configured script store cannot read authoring sessions")
	}
	observer := &lockObservingAuthoringScriptStore{
		scriptPersistence: server.runner.scriptStore,
		reader:            reader,
		runner:            server.runner,
	}
	server.runner.scriptStore = observer

	preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "Java admission preflight", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	}})
	if err != nil || preflight == nil || !preflight.StartNowAllowed {
		t.Fatalf("PreflightLaunch = (%#v, %v), want start-now approval", preflight, err)
	}
	if observer.reads.Load() == 0 || observer.underLock.Load() {
		t.Fatalf("preflight authoring reads = %d, under runner lock = %t", observer.reads.Load(), observer.underLock.Load())
	}

	created, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Java admission start", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	observer.reset()
	startAt := time.Now().UTC()
	evidence, err := server.runner.loadStartLaunchEvidence(created.ID, startAt)
	if err != nil {
		t.Fatalf("loadStartLaunchEvidence: %v", err)
	}
	server.runner.mu.Lock()
	blocked, gateErr := server.runner.preflightStartBlocksLockedWithExpectedTermAndEvidence(
		created.ID, StartTestOptions{}, nil, startAt, evidence,
	)
	server.runner.mu.Unlock()
	if gateErr != nil || blocked {
		t.Fatalf("start gate = (blocked=%t, err=%v), want allowed", blocked, gateErr)
	}
	if observer.reads.Load() == 0 || observer.underLock.Load() {
		t.Fatalf("start authoring reads = %d, under runner lock = %t", observer.reads.Load(), observer.underLock.Load())
	}
}

func TestStartRuntimeEvidenceRejectsAdmissionStateChange(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	storePreflightScript(runner, "start-evidence-script", storage.DefaultProjectID)
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "start evidence fence", Duration: "1m", ScriptID: "start-evidence-script", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	evidence, err := runner.loadStartLaunchEvidence(created.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("loadStartLaunchEvidence: %v", err)
	}
	runner.mu.Lock()
	live := runner.tests[created.ID]
	live.Groups[0].VUsers++
	_, _, gateErr := runner.startRuntimeGateConflictsLocked(live, evidence)
	runner.mu.Unlock()
	if !errors.Is(gateErr, errStartAdmissionEvidenceStale) {
		t.Fatalf("stale start runtime evidence error = %v, want typed retryable error", gateErr)
	}
}

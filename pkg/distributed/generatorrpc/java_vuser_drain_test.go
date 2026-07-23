package generatorrpc

import (
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

func cachePersistentJavaScriptForDrainTest(t *testing.T, service *Service, scriptID string) {
	t.Helper()
	if err := service.cacheScriptLocked(scriptID, []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
	}`), ""); err != nil {
		t.Fatal(err)
	}
}

func cacheLegacyJavaScriptForDrainTest(t *testing.T, service *Service, scriptID string) {
	t.Helper()
	if err := service.cacheScriptLocked(scriptID, []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"full_lifecycle"}}]
	}`), ""); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentJavaScaleDownRetainsCapacityUntilReap(t *testing.T) {
	service := newIsolatedService(t, 20)
	service.mu.Lock()
	cachePersistentJavaScriptForDrainTest(t, service, "java")
	service.assignments["run"] = assignedTest{
		testID:       "run",
		lifecycle:    assignmentLifecycleRunning,
		committed:    true,
		vuserCount:   5,
		groups:       map[string]int32{"group": 5},
		activeGroups: map[string]int32{"group": 5},
		groupScripts: map[string]string{"group": "java"},
	}

	decision := service.lifecycleCoordinator.planScaleLocked(service, &pb.VUserAdjustment{
		TestId: "run", GroupName: "group", TargetVusers: 2,
	}, javaVuserCapacityObservation{})
	if decision.err != nil || decision.plan == nil {
		service.mu.Unlock()
		t.Fatalf("persistent Java downscale required a live runtime probe: %+v", decision)
	}
	if got := service.activePersistentJavaVusersLocked(); got != 5 {
		service.mu.Unlock()
		t.Fatalf("capacity after 5 -> 2 reservation = %d, want old live demand 5", got)
	}
	if got := service.persistentJavaDrainDemandLocked(); got != 3 {
		service.mu.Unlock()
		t.Fatalf("scale drain demand = %d, want 3", got)
	}

	service.prepareScaleRollbackOverlapLocked(decision.plan)
	if got := service.activePersistentJavaVusersLocked(); got != 8 {
		service.mu.Unlock()
		t.Fatalf("capacity during downscale rollback overlap = %d, want 8", got)
	}
	service.normalizeScaleRollbackDrainLocked(decision.plan)
	service.lifecycleCoordinator.rollbackScaleLocked(service, decision.plan)
	if got := service.activePersistentJavaVusersLocked(); got != 8 {
		service.mu.Unlock()
		t.Fatalf("capacity after downscale rollback = %d, want restored 5 plus 3 tearing down", got)
	}
	service.releaseScaleDrainLocked(decision.plan)
	if got := service.activePersistentJavaVusersLocked(); got != 5 {
		service.mu.Unlock()
		t.Fatalf("capacity after pre-apply rollback = %d, want restored assignment 5", got)
	}
	service.mu.Unlock()
}

func TestPersistentJavaDetachedStopRetainsCapacityUntilExecutionDone(t *testing.T) {
	service := newIsolatedService(t, 20)
	service.mu.Lock()
	if got := service.heartbeatJavaVuserOccupancyModeLocked(); got != RuntimeOccupancyModeIdle {
		service.mu.Unlock()
		t.Fatalf("initial occupancy mode = %q, want idle", got)
	}
	cachePersistentJavaScriptForDrainTest(t, service, "java")
	service.assignments["run"] = assignedTest{
		testID:       "run",
		lifecycle:    assignmentLifecycleRunning,
		committed:    true,
		vuserCount:   4,
		groups:       map[string]int32{"group": 4},
		activeGroups: map[string]int32{"group": 4},
		groupScripts: map[string]string{"group": "java"},
	}

	plan := service.lifecycleCoordinator.planStopLocked(service, &pb.StopRequest{TestId: "run"}, false, false, 0)
	if plan.kind != stopTransitionDetached || plan.persistentJavaDrainToken == 0 {
		service.mu.Unlock()
		t.Fatalf("detached stop plan = %+v, want persistent drain", plan)
	}
	if _, assigned := service.assignments["run"]; assigned {
		service.mu.Unlock()
		t.Fatal("detached stop left assignment published")
	}
	if got := service.activePersistentJavaVusersLocked(); got != 4 {
		service.mu.Unlock()
		t.Fatalf("capacity after detach = %d, want 4 until JVM reap", got)
	}
	if got := service.heartbeatJavaVuserOccupancyModeLocked(); got != RuntimeOccupancyModePersistent {
		service.mu.Unlock()
		t.Fatalf("detached persistent occupancy mode = %q, want persistent", got)
	}
	service.releasePersistentJavaDrainLocked(plan.persistentJavaDrainToken)
	if got := service.activePersistentJavaVusersLocked(); got != 0 {
		service.mu.Unlock()
		t.Fatalf("capacity after execution completion = %d, want 0", got)
	}
	if got := service.heartbeatJavaVuserOccupancyModeLocked(); got != RuntimeOccupancyModeIdle {
		service.mu.Unlock()
		t.Fatalf("occupancy mode after execution completion = %q, want idle", got)
	}
	service.mu.Unlock()
}

func TestLegacyJavaDetachedStopBlocksPersistentPlacementUntilExecutionDone(t *testing.T) {
	service := newIsolatedService(t, 20)
	service.mu.Lock()
	cacheLegacyJavaScriptForDrainTest(t, service, "legacy-java")
	service.assignments["run"] = assignedTest{
		testID:       "run",
		lifecycle:    assignmentLifecycleRunning,
		committed:    true,
		vuserCount:   4,
		groups:       map[string]int32{"group": 4},
		activeGroups: map[string]int32{"group": 4},
		groupScripts: map[string]string{"group": "legacy-java"},
	}
	if got := service.heartbeatJavaVuserCapacityInUseLocked(); got != int64(^uint64(0)>>1) {
		service.mu.Unlock()
		t.Fatalf("heartbeat java_vuser occupancy = %d, want persistent placement blocked by active legacy work", got)
	}
	if got := service.heartbeatJavaVuserOccupancyModeLocked(); got != RuntimeOccupancyModeLegacy {
		service.mu.Unlock()
		t.Fatalf("legacy occupancy mode = %q, want legacy", got)
	}

	plan := service.lifecycleCoordinator.planStopLocked(service, &pb.StopRequest{TestId: "run"}, false, false, 0)
	if plan.kind != stopTransitionDetached || plan.persistentJavaDrainToken == 0 {
		service.mu.Unlock()
		t.Fatalf("detached stop plan = %+v, want legacy process-slot drain", plan)
	}
	if !service.legacyJavaRuntimeOccupiedLocked() {
		service.mu.Unlock()
		t.Fatal("legacy process-slot occupancy disappeared before execution teardown")
	}
	if got := service.persistentJavaDrainDemandLocked(); got != 0 {
		service.mu.Unlock()
		t.Fatalf("legacy drain consumed persistent demand = %d, want mode exclusion only", got)
	}
	service.releasePersistentJavaDrainLocked(plan.persistentJavaDrainToken)
	if service.legacyJavaRuntimeOccupiedLocked() {
		service.mu.Unlock()
		t.Fatal("legacy process-slot occupancy remained after execution teardown")
	}
	if got := service.heartbeatJavaVuserCapacityInUseLocked(); got != 0 {
		service.mu.Unlock()
		t.Fatalf("heartbeat java_vuser occupancy after teardown = %d, want 0", got)
	}
	if got := service.heartbeatJavaVuserOccupancyModeLocked(); got != RuntimeOccupancyModeIdle {
		service.mu.Unlock()
		t.Fatalf("legacy occupancy mode after teardown = %q, want idle", got)
	}
	service.mu.Unlock()
}

func TestZeroStartJavaLifecycleModeRetainedUntilDetachedExecutionDone(t *testing.T) {
	tests := []struct {
		name       string
		scriptID   string
		cache      func(*testing.T, *Service, string)
		persistent bool
	}{
		{name: "persistent", scriptID: "persistent-zero", cache: cachePersistentJavaScriptForDrainTest, persistent: true},
		{name: "legacy", scriptID: "legacy-zero", cache: cacheLegacyJavaScriptForDrainTest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newIsolatedService(t, 20)
			service.mu.Lock()
			test.cache(t, service, test.scriptID)
			service.assignments["run"] = assignedTest{
				testID:       "run",
				lifecycle:    assignmentLifecycleRunning,
				committed:    true,
				groups:       map[string]int32{"group": 0},
				activeGroups: map[string]int32{"group": 0},
				groupScripts: map[string]string{"group": test.scriptID},
			}
			plan := service.lifecycleCoordinator.planStopLocked(service, &pb.StopRequest{TestId: "run"}, false, false, 0)
			if plan.kind != stopTransitionDetached || plan.persistentJavaDrainToken == 0 {
				service.mu.Unlock()
				t.Fatalf("zero-start detached stop plan = %+v, want lifecycle-mode drain", plan)
			}
			if test.persistent && !service.persistentJavaRuntimeOccupiedLocked() {
				service.mu.Unlock()
				t.Fatal("zero-start persistent mode disappeared before execution completion")
			}
			if !test.persistent && !service.legacyJavaRuntimeOccupiedLocked() {
				service.mu.Unlock()
				t.Fatal("zero-start legacy mode disappeared before execution completion")
			}
			wantMode := RuntimeOccupancyModeLegacy
			if test.persistent {
				wantMode = RuntimeOccupancyModePersistent
			}
			if got := service.heartbeatJavaVuserOccupancyModeLocked(); got != wantMode {
				service.mu.Unlock()
				t.Fatalf("zero-start occupancy mode = %q, want %q", got, wantMode)
			}
			service.releasePersistentJavaDrainLocked(plan.persistentJavaDrainToken)
			if service.persistentJavaRuntimeOccupiedLocked() || service.legacyJavaRuntimeOccupiedLocked() {
				service.mu.Unlock()
				t.Fatal("zero-start lifecycle mode remained after execution completion")
			}
			service.mu.Unlock()
		})
	}
}

func TestPersistentJavaFailTestRetainsCapacityUntilExecutionDone(t *testing.T) {
	service := newIsolatedService(t, 20)
	execution := &groupExecution{
		cancel:        func() {},
		publisherDone: make(chan struct{}),
		done:          make(chan struct{}),
	}
	close(execution.publisherDone)
	service.mu.Lock()
	cachePersistentJavaScriptForDrainTest(t, service, "java")
	service.assignments["run"] = assignedTest{
		testID:       "run",
		lifecycle:    assignmentLifecycleRunning,
		committed:    true,
		vuserCount:   3,
		groups:       map[string]int32{"group": 3},
		activeGroups: map[string]int32{"group": 3},
		groupScripts: map[string]string{"group": "java"},
	}
	service.executions["run"] = map[string]*groupExecution{"group": execution}
	service.mu.Unlock()

	finished := make(chan struct{})
	go func() {
		service.FailTest("run", "injected failure")
		close(finished)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		_, assigned := service.assignments["run"]
		active := service.activePersistentJavaVusersLocked()
		service.mu.RUnlock()
		if !assigned {
			if active != 3 {
				t.Fatalf("capacity after failure detach = %d, want 3", active)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("FailTest did not detach assignment")
		}
		time.Sleep(time.Millisecond)
	}

	close(execution.done)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("FailTest did not finish after execution completion")
	}
	service.mu.RLock()
	active := service.activePersistentJavaVusersLocked()
	service.mu.RUnlock()
	if active != 0 {
		t.Fatalf("capacity after failed execution completion = %d, want 0", active)
	}
}

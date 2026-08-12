package generatorrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAssignWorkDoesNotTreatJavaProcessQueueAsVUserCapacity(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")
	service := newIsolatedService(t, 10)
	script := []byte(`{
		"name":"combined-java",
		"protocol":"java_vuser",
		"actions":[{"name":"Action","type":"java_vuser_run","target":"Actions","params":{"method":"action"}}]
	}`)
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "java-capacity",
		GroupName:     "default",
		ScriptId:      "java-script",
		ScriptContent: script,
		VuserCount:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := strings.ToLower(ack.GetErrorMessage())
	if strings.Contains(message, "insufficient java_vuser runtime capacity") {
		t.Fatalf("process queue was treated as placement capacity: %#v", ack)
	}
	if !ack.GetAccepted() {
		t.Fatalf("process queue should not reject placement when execution is disabled: %#v", ack)
	}
}

func TestAssignWorkOneShotJavaVuserBypassesProcessCapacityCap(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	t.Setenv(config.EnvJavaVuserMaxProcesses, "1")
	service := newIsolatedService(t, 10)
	// full_lifecycle (and method-less) scripts time-share JVM process slots per
	// action, so MaxVUsers=MaxProcesses must not reject them at admission. With
	// the runner disabled the assignment still fails later, at the legacy
	// runtime-availability check — the point is that the capacity gate no
	// longer fires for one-shot scripts.
	script := []byte(`{
		"name":"one-shot-java",
		"protocol":"java_vuser",
		"actions":[{"name":"Action","type":"java_vuser_run","target":"Actions","params":{"method":"full_lifecycle"}}]
	}`)
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "java-one-shot-capacity",
		GroupName:     "default",
		ScriptId:      "java-one-shot-script",
		ScriptContent: script,
		VuserCount:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := strings.ToLower(ack.GetErrorMessage())
	if strings.Contains(message, "insufficient java_vuser runtime capacity") {
		t.Fatalf("one-shot script was rejected by the persistent-runtime capacity cap: %#v", ack)
	}
	if ack.GetAccepted() {
		return
	}
	if !strings.Contains(message, "legacy one-shot runtime is unavailable") {
		t.Fatalf("unexpected rejection reason for one-shot script: %#v", ack)
	}
}

func TestAssignWorkLegacyJavaVuserRequiresDistributedContainment(t *testing.T) {
	script := []byte(`{
		"name":"legacy-java",
		"protocol":"java_vuser",
		"actions":[{"name":"Action","type":"java_vuser_run","target":"Actions","params":{"method":"full_lifecycle"}}]
	}`)
	for _, test := range []struct {
		name         string
		features     []string
		wantAccepted bool
	}{
		{
			name: "stale sidecar missing owner lease",
			features: []string{
				javasidecar.FeatureOwnerFencingV1,
				javasidecar.FeatureExternalRuntimeV1,
				javasidecar.FeatureRuntimeLeaseV1,
			},
		},
		{
			name: "stale sidecar missing runtime lease",
			features: []string{
				javasidecar.FeatureOwnerFencingV1,
				javasidecar.FeatureExternalRuntimeV1,
				javasidecar.FeatureRunnerOwnerLeaseV1,
			},
		},
		{
			name: "fully leased external sidecar",
			features: []string{
				javasidecar.FeatureOwnerFencingV1,
				javasidecar.FeatureExternalRuntimeV1,
				javasidecar.FeatureRunnerOwnerLeaseV1,
				javasidecar.FeatureRuntimeLeaseV1,
			},
			wantAccepted: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/v1/capabilities":
					_ = json.NewEncoder(response).Encode(javasidecar.CapabilitiesResponse{
						Contract:              javasidecar.ContractVersion,
						Healthy:               true,
						Features:              test.features,
						MinimumJavaMajor:      17,
						JavaMajor:             17,
						MaxProcesses:          4,
						MaxExecutionTimeoutMS: javasidecar.MaximumExecutionTimeout.Milliseconds(),
						MaxCaptureBytes:       javasidecar.MaxCaptureBytes,
					})
				case request.Method == http.MethodPost && request.URL.Path == "/v1/validate":
					_ = json.NewEncoder(response).Encode(javasidecar.ValidateResponse{
						Contract:  javasidecar.ContractVersion,
						Valid:     true,
						JavaMajor: 17,
					})
				default:
					http.NotFound(response, request)
				}
			}))
			defer runner.Close()
			t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
			t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
			t.Setenv(config.EnvJavaVuserRunnerURL, runner.URL)
			t.Setenv(config.EnvJavaVuserRunnerAuthToken, "test-"+strings.ReplaceAll(test.name, " ", "-"))
			t.Setenv(config.EnvJavaVuserMaxProcesses, "4")

			service := newIsolatedService(t, 10, WithExecution())
			ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
				TestId:        "legacy-java-" + strings.ReplaceAll(test.name, " ", "-"),
				GroupName:     "default",
				ScriptId:      "legacy-java-script",
				ScriptContent: script,
				VuserCount:    1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ack.GetAccepted() != test.wantAccepted {
				t.Fatalf("legacy Java assignment accepted = %v, want %v (ack %#v)", ack.GetAccepted(), test.wantAccepted, ack)
			}
			if !test.wantAccepted && !strings.Contains(strings.ToLower(ack.GetErrorMessage()), "containment") {
				t.Fatalf("legacy Java assignment error = %q, want containment rejection", ack.GetErrorMessage())
			}
		})
	}
}

func TestJavaVuserDistributedContainmentAdmission(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilities protocol.RuntimeCapabilities
		wantError    bool
	}{
		{
			name: "contained local process",
			capabilities: protocol.RuntimeCapabilities{
				Available: true,
				Features:  []string{"java_vuser_local_process_containment_v1"},
			},
		},
		{
			name: "fully leased external process",
			capabilities: protocol.RuntimeCapabilities{
				Available: true,
				Features: []string{
					javasidecar.FeatureExternalRuntimeV1,
					javasidecar.FeatureRunnerOwnerLeaseV1,
					javasidecar.FeatureRuntimeLeaseV1,
				},
			},
		},
		{
			name: "external process missing owner lease",
			capabilities: protocol.RuntimeCapabilities{
				Available: true,
				Features: []string{
					javasidecar.FeatureExternalRuntimeV1,
					javasidecar.FeatureRuntimeLeaseV1,
				},
			},
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := javaVuserDistributedContainmentError(test.capabilities, true)
			if (err != nil) != test.wantError {
				t.Fatalf("containment admission error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func persistentJavaCapacityObservation(maxVUsers int64) javaVuserCapacityObservation {
	return javaVuserCapacityObservation{
		probed:    true,
		supported: true,
		capabilities: protocol.RuntimeCapabilities{
			Available: true,
			Features: []string{
				protocol.FeatureJavaVuserPersistentLifecycleV1,
				protocol.FeatureJavaVuserSessionFrameTokenV1,
			},
			MaxVUsers: maxVUsers,
		},
	}
}

func TestPersistentJavaCapacityObservationRejectsTokenlessRunner(t *testing.T) {
	observation := persistentJavaCapacityObservation(2)
	observation.capabilities.Features = []string{protocol.FeatureJavaVuserPersistentLifecycleV1}
	if maximum, available := observation.persistentMaxVUsers(); available || maximum != 0 {
		t.Fatalf("persistentMaxVUsers(tokenless) = %d, %t, want unavailable", maximum, available)
	}
	if err := observation.persistentRuntimeError(); err == nil {
		t.Fatal("persistentRuntimeError(tokenless) = nil, want governed capability rejection")
	} else if !strings.Contains(err.Error(), protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("persistentRuntimeError(tokenless) = %v, want missing frame-token capability", err)
	}
}

func TestCachedJavaVuserCapacityProfileUsesLiveOpenWorkerBound(t *testing.T) {
	_, persistent, capacity := cachedScriptRuntimeProfile([]byte(`{
		"name":"open-java",
		"protocol":"java_vuser",
		"config":{
			"run_logic":{"workload_model":"open","max_in_flight":20,"iterations":3,"global_iterations":5},
			"pacing":{"type":"tps","tps":10}
		},
		"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
	}`))
	if !persistent {
		t.Fatal("cached profile did not classify phase lifecycle as persistent")
	}
	if got := capacity.Demand(2); got != 5 {
		t.Fatalf("cached open Java demand = %d, want global finite-arrival bound 5", got)
	}

	_, persistent, capacity = cachedScriptRuntimeProfile([]byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"full_lifecycle"}}]
	}`))
	if persistent || capacity.Demand(100) != 0 {
		t.Fatalf("legacy one-shot profile consumed pinned capacity: persistent=%t demand=%d", persistent, capacity.Demand(100))
	}
}

func TestAssignmentCoordinatorReservesPersistentJavaCapacityAcrossPendingTests(t *testing.T) {
	service := newIsolatedService(t, 20)
	observation := persistentJavaCapacityObservation(2)
	script := []byte(`{
		"name":"persistent-java",
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","target":"Actions","params":{"method":"action"}}]
	}`)
	first := validatedStageRequest(t, &pb.WorkAssignment{
		TestId: "java-a", GroupName: "group", ScriptId: "java-a-script", ScriptContent: script, VuserCount: 2,
	})
	second := validatedStageRequest(t, &pb.WorkAssignment{
		TestId: "java-b", GroupName: "group", ScriptId: "java-b-script", ScriptContent: script, VuserCount: 1,
	})

	service.mu.Lock()
	firstDecision := service.lifecycleCoordinator.reserveStageLocked(service, first, observation)
	secondDecision := service.lifecycleCoordinator.reserveStageLocked(service, second, observation)
	if firstDecision.plan != nil {
		service.lifecycleCoordinator.rollbackStageLocked(first.request.GetTestId(), firstDecision.plan.token)
	}
	service.mu.Unlock()

	if firstDecision.kind != assignmentStagePrepare || firstDecision.plan == nil {
		t.Fatalf("first persistent Java stage = %+v, want prepare", firstDecision)
	}
	if secondDecision.kind != assignmentStageRejected || !strings.Contains(secondDecision.errorMessage, "maximum 2") {
		t.Fatalf("second persistent Java stage = %+v, want aggregate capacity rejection", secondDecision)
	}
}

func TestAssignmentCommitRechecksPersistentJavaCapacity(t *testing.T) {
	service := newIsolatedService(t, 20)
	validated := validatedStageRequest(t, &pb.WorkAssignment{
		TestId: "java-commit", GroupName: "group", ScriptId: "java-commit-script", VuserCount: 2,
		ScriptContent: []byte(`{
			"protocol":"java_vuser",
			"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
		}`),
	})
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	if decision.plan == nil {
		service.mu.Unlock()
		t.Fatalf("persistent Java stage = %+v, want prepared reservation", decision)
	}
	_, _, commitErr := service.lifecycleCoordinator.commitStageLocked(service, decision.plan, &preparedAssignmentStage{
		javaCapacity: persistentJavaCapacityObservation(1),
	})
	service.lifecycleCoordinator.rollbackStageLocked(validated.request.GetTestId(), decision.plan.token)
	service.mu.Unlock()
	if status.Code(commitErr) != codes.ResourceExhausted {
		t.Fatalf("commit capacity error = %v (%s), want ResourceExhausted", commitErr, status.Code(commitErr))
	}
}

func TestAssignmentCommitRequiresRuntimeForZeroStartPersistentLifecycle(t *testing.T) {
	service := newIsolatedService(t, 20, WithExecution())
	validated := validatedStageRequest(t, &pb.WorkAssignment{
		TestId: "java-zero", GroupName: "group", ScriptId: "java-zero-script", VuserCount: 0,
		ScriptContent: []byte(`{
			"protocol":"java_vuser",
			"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
		}`),
	})
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(
		service,
		validated,
		persistentJavaCapacityObservation(1),
	)
	if decision.plan == nil || !decision.plan.persistentJavaLifecycle || decision.plan.persistentJavaDemand != 0 {
		service.mu.Unlock()
		t.Fatalf("zero-start persistent Java stage = %+v, want lifecycle reservation with zero demand", decision)
	}
	_, _, commitErr := service.lifecycleCoordinator.commitStageLocked(service, decision.plan, &preparedAssignmentStage{
		execution: &groupExecution{},
	})
	service.lifecycleCoordinator.rollbackStageLocked(validated.request.GetTestId(), decision.plan.token)
	service.mu.Unlock()
	if status.Code(commitErr) != codes.FailedPrecondition {
		t.Fatalf("zero-start commit runtime error = %v (%s), want FailedPrecondition", commitErr, status.Code(commitErr))
	}
}

func TestAssignmentCoordinatorRejectsAtOncePersistentJavaLifecycle(t *testing.T) {
	service := newIsolatedService(t, 20)
	validated := validatedStageRequest(t, &pb.WorkAssignment{
		TestId:       "java-at-once",
		GroupName:    "group",
		ScriptId:     "java-at-once-script",
		VuserCount:   1,
		InitStrategy: "at_once",
		ScriptContent: []byte(`{
			"protocol":"java_vuser",
			"init":[{"type":"java_vuser_run","params":{"method":"init"}}],
			"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
		}`),
	})
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(service, validated, persistentJavaCapacityObservation(10))
	service.mu.Unlock()
	if decision.kind != assignmentStageRejected || !strings.Contains(decision.errorMessage, "at_once") {
		t.Fatalf("persistent at_once decision = %+v, want lifecycle rejection", decision)
	}
}

func TestPlanScaleEnforcesGeneratorWidePersistentJavaCapacity(t *testing.T) {
	service := newIsolatedService(t, 20)
	persistentScript := []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
	}`)
	oneShotScript := []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"full_lifecycle"}}]
	}`)
	service.mu.Lock()
	if err := service.cacheScriptLocked("persistent-a", persistentScript, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	if err := service.cacheScriptLocked("persistent-b", persistentScript, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	if err := service.cacheScriptLocked("one-shot", oneShotScript, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	service.assignments["java-a"] = assignedTest{
		testID: "java-a", committed: true, lifecycle: assignmentLifecycleRunning,
		vuserCount: 1, groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
		groupScripts: map[string]string{"group": "persistent-a"},
	}
	service.assignments["java-b"] = assignedTest{
		testID: "java-b", committed: true, lifecycle: assignmentLifecycleRunning,
		vuserCount: 1, groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
		groupScripts: map[string]string{"group": "persistent-b"},
	}
	service.assignments["legacy"] = assignedTest{
		testID: "legacy", committed: true, lifecycle: assignmentLifecycleRunning,
		vuserCount: 1, groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
		groupScripts: map[string]string{"group": "one-shot"},
	}

	rejected := service.lifecycleCoordinator.planScaleLocked(service, &pb.VUserAdjustment{
		TestId: "java-a", GroupName: "group", TargetVusers: 2,
	}, persistentJavaCapacityObservation(2))
	legacy := service.lifecycleCoordinator.planScaleLocked(service, &pb.VUserAdjustment{
		TestId: "legacy", GroupName: "group", TargetVusers: 5,
	}, javaVuserCapacityObservation{})
	if legacy.plan != nil {
		service.lifecycleCoordinator.rollbackScaleLocked(service, legacy.plan)
	}
	service.mu.Unlock()

	if status.Code(rejected.err) != codes.ResourceExhausted {
		t.Fatalf("persistent scale error = %v (%s), want ResourceExhausted", rejected.err, status.Code(rejected.err))
	}
	if status.Code(legacy.err) != codes.ResourceExhausted || legacy.plan != nil {
		t.Fatalf("legacy one-shot scale = %+v, want mixed-lifecycle process-slot rejection", legacy)
	}
}

func TestAssignmentCoordinatorRejectsMixedJavaLifecycleModes(t *testing.T) {
	service := newIsolatedService(t, 20)
	persistentScript := []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
	}`)
	legacyScript := []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"full_lifecycle"}}]
	}`)

	service.mu.Lock()
	if err := service.cacheScriptLocked("active-legacy", legacyScript, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	service.assignments["legacy"] = assignedTest{
		testID: "legacy", committed: true, lifecycle: assignmentLifecycleRunning,
		vuserCount: 1, groups: map[string]int32{"group": 1},
		groupScripts: map[string]string{"group": "active-legacy"},
	}
	persistent := validatedStageRequest(t, &pb.WorkAssignment{
		TestId: "persistent", GroupName: "group", ScriptId: "persistent-script",
		ScriptContent: persistentScript, VuserCount: 1,
	})
	persistentDecision := service.lifecycleCoordinator.reserveStageLocked(
		service,
		persistent,
		persistentJavaCapacityObservation(25),
	)
	delete(service.assignments, "legacy")

	if err := service.cacheScriptLocked("active-persistent", persistentScript, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	service.assignments["persistent"] = assignedTest{
		testID: "persistent", committed: true, lifecycle: assignmentLifecycleRunning,
		vuserCount: 1, groups: map[string]int32{"group": 1},
		groupScripts: map[string]string{"group": "active-persistent"},
	}
	legacy := validatedStageRequest(t, &pb.WorkAssignment{
		TestId: "new-legacy", GroupName: "group", ScriptId: "legacy-script",
		ScriptContent: legacyScript, VuserCount: 1,
	})
	legacyDecision := service.lifecycleCoordinator.reserveStageLocked(service, legacy)
	service.mu.Unlock()

	if persistentDecision.kind != assignmentStageRejected ||
		!strings.Contains(persistentDecision.errorMessage, "cannot share generator process slots") {
		t.Fatalf("persistent stage with active legacy = %+v, want process-slot isolation rejection", persistentDecision)
	}
	if legacyDecision.kind != assignmentStageRejected ||
		!strings.Contains(legacyDecision.errorMessage, "cannot share generator process slots") {
		t.Fatalf("legacy stage with active persistent = %+v, want process-slot isolation rejection", legacyDecision)
	}
}

func TestAssignmentCoordinatorRejectsMixedJavaLifecycleModesAtZeroStart(t *testing.T) {
	persistentScript := []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"action"}}]
	}`)
	legacyScript := []byte(`{
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","params":{"method":"full_lifecycle"}}]
	}`)

	t.Run("persistent blocks legacy", func(t *testing.T) {
		service := newIsolatedService(t, 20)
		service.mu.Lock()
		if err := service.cacheScriptLocked("persistent-zero", persistentScript, ""); err != nil {
			service.mu.Unlock()
			t.Fatal(err)
		}
		service.assignments["persistent"] = assignedTest{
			testID: "persistent", committed: true, lifecycle: assignmentLifecycleRunning,
			groups: map[string]int32{"group": 0}, groupScripts: map[string]string{"group": "persistent-zero"},
		}
		legacy := validatedStageRequest(t, &pb.WorkAssignment{
			TestId: "legacy", GroupName: "group", ScriptId: "legacy-script",
			ScriptContent: legacyScript, VuserCount: 0,
		})
		decision := service.lifecycleCoordinator.reserveStageLocked(service, legacy)
		service.mu.Unlock()
		if decision.kind != assignmentStageRejected ||
			!strings.Contains(decision.errorMessage, "cannot share generator process slots") {
			t.Fatalf("legacy stage with zero-start persistent assignment = %+v, want process-slot isolation rejection", decision)
		}
	})

	t.Run("legacy blocks persistent", func(t *testing.T) {
		service := newIsolatedService(t, 20)
		service.mu.Lock()
		if err := service.cacheScriptLocked("legacy-zero", legacyScript, ""); err != nil {
			service.mu.Unlock()
			t.Fatal(err)
		}
		service.assignments["legacy"] = assignedTest{
			testID: "legacy", committed: true, lifecycle: assignmentLifecycleRunning,
			groups: map[string]int32{"group": 0}, groupScripts: map[string]string{"group": "legacy-zero"},
		}
		persistent := validatedStageRequest(t, &pb.WorkAssignment{
			TestId: "persistent", GroupName: "group", ScriptId: "persistent-script",
			ScriptContent: persistentScript, VuserCount: 0,
		})
		decision := service.lifecycleCoordinator.reserveStageLocked(
			service,
			persistent,
			persistentJavaCapacityObservation(25),
		)
		service.mu.Unlock()
		if decision.kind != assignmentStageRejected ||
			!strings.Contains(decision.errorMessage, "cannot share generator process slots") {
			t.Fatalf("persistent stage with zero-start legacy assignment = %+v, want process-slot isolation rejection", decision)
		}
	})
}

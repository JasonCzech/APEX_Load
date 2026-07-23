package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
	"github.com/jasonczech/apexload/pkg/storage"
)

func storePreflightScript(runner *TestRunner, id string, projectID string) {
	runner.StoreScript(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
}

func storePreflightBrowserScript(runner *TestRunner, id string, projectID string) {
	runner.StoreScript(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "browser",
		Version:   "1.0.0",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			Browser: dsl.BrowserConfig{
				DeviceProfile: "iphone_15_pro",
			},
		},
		Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "https://example.test"}},
	})
}

func storePreflightSAPGUIScript(runner *TestRunner, id string, projectID string) {
	runner.StoreScript(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "sap_gui",
		Version:   "1.0.0",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{
			SAPGUI: dsl.SAPGUIConfig{
				ConnectionString: "{{secret.sapgui_connection}}",
				Password:         "{{secret.sapgui_password}}",
			},
		},
		Actions: []dsl.Action{{Name: "SetUser", Type: "sapgui_set", Target: "wnd[0]/usr/txtRSYST-BNAME", Value: "perfuser"}},
	})
}

func storePreflightJavaVuserScript(t *testing.T, runner *TestRunner, id string, projectID string, enabled bool) {
	t.Helper()
	if _, err := runner.storeScriptDurableForRestore(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "java_vuser",
		Version:   "1.0.0",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{
			JavaVuser: dsl.JavaVuserConfig{
				Enabled:         enabled,
				HarnessJar:      "runners/java_vuser/apexload-java-vuser-runner.jar",
				MaxProcesses:    25,
				ActionTimeoutMs: 30000,
			},
		},
		Actions: []dsl.Action{{Name: "Run Java", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "full_lifecycle"}}},
	}); err != nil {
		t.Fatalf("restore pre-existing Java Vuser script: %v", err)
	}
}

func storePreflightCombinedJavaVuserScript(t *testing.T, runner *TestRunner, id string, projectID string) {
	t.Helper()
	if _, err := runner.storeScriptDurableForRestore(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "java_vuser",
		Version:   "1.0.0",
		ProjectID: projectID,
		Init:      []dsl.Action{{Name: "Java init", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init"}}},
		Actions:   []dsl.Action{{Name: "Java action", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"}}},
		Teardown:  []dsl.Action{{Name: "Java end", Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "end"}}},
	}); err != nil {
		t.Fatalf("restore pre-existing combined Java Vuser script: %v", err)
	}
}

func setPreflightJavaVuserInventory(runner *TestRunner, features ...string) {
	if protocol.RuntimeFeaturesSatisfy(features, protocol.FeatureJavaVuserPersistentLifecycleV1) {
		// Already a complete governed persistent feature set.
	} else if stringSliceContainsEqualFold(features, protocol.FeatureJavaVuserPersistentLifecycleV1) {
		features = append(features, protocol.FeatureJavaVuserSessionFrameTokenV1)
	}
	entry := GeneratorInventoryEntry{
		Target:             "gen-jvm:9090",
		GeneratorID:        "gen-jvm",
		Status:             "connected",
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: append([]string(nil), features...),
		DrainState:         "active",
		AvailableCapacity:  25,
		MaxVUsers:          25,
	}
	if protocol.RuntimeFeaturesSatisfy(features, protocol.FeatureJavaVuserPersistentLifecycleV1) {
		entry.RuntimeCapacities = []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       25,
			AvailableVUsers: 25,
			Features:        append([]string(nil), features...),
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}}
	}
	runner.liveInventoryMu.Lock()
	runner.liveInventory = []GeneratorInventoryEntry{entry}
	runner.liveInventoryAt = time.Now().UTC()
	runner.liveInventoryMu.Unlock()
}

func setPreflightJavaVuserInventoryEntries(runner *TestRunner, entries ...GeneratorInventoryEntry) {
	runner.liveInventoryMu.Lock()
	runner.liveInventory = cloneGeneratorInventoryEntries(entries)
	runner.liveInventoryAt = time.Now().UTC()
	runner.liveInventoryMu.Unlock()
}

func storePreflightJavaReviewScript(runner *TestRunner, id string, projectID string) {
	runner.StoreScript(&dsl.Script{
		ID:        id,
		Name:      id,
		Protocol:  "java_vuser",
		Version:   "1.0.0",
		ProjectID: projectID,
		Actions: []dsl.Action{{
			Name:   "Review Java Statement",
			Type:   "source_review",
			Target: "CustomClient.invokeBusinessOperation",
			Params: map[string]any{"function": "CustomClient.invokeBusinessOperation"},
		}},
	})
}

func TestPreflightLaunchAllowsImmediateStartWhenCapacityAvailable(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-start", "")

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Start Now",
			Duration: "5m",
			ScriptID: "script-preflight-start",
			VUsers:   10,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if !preflight.StartNowAllowed || preflight.QueueAllowed || preflight.Decision != "start_now" {
		t.Fatalf("preflight = %#v, want start-now decision", preflight)
	}
}

func TestPreflightLaunchBlocksOnSuspendedPool(t *testing.T) {
	server, store := newTimeslotServer(t)
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-pool-preflight",
		Name:     "Pool Preflight",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	if err := store.SaveFleetPool(&storage.FleetPool{ID: "pool-suspended", Name: "Edge", Status: "suspended"}); err != nil {
		t.Fatalf("save pool: %v", err)
	}
	if err := store.SaveFleetPoolMembers("pool-suspended", []*storage.FleetPoolMember{
		{ID: "m1", PoolID: "pool-suspended", GeneratorID: "gen-a", Target: "gen-a:9090"},
	}); err != nil {
		t.Fatalf("save members: %v", err)
	}
	preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:       "Pool run",
			Duration:   "5m",
			ScriptID:   "script-pool-preflight",
			VUsers:     1,
			Assignment: &GeneratorAssignmentSettings{PoolID: "pool-suspended"},
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	found := false
	for _, c := range preflight.BlockingConflicts {
		if c.Code == "generator_pool_unavailable" {
			found = true
		}
	}
	if !found || preflight.StartNowAllowed {
		t.Fatalf("preflight = %#v, want blocked with generator_pool_unavailable", preflight)
	}
}

func TestPreflightLaunchBlocksJavaVuserWithoutRunnerCapability(t *testing.T) {
	// Enablement is operator-owned: with the runner env unset the gate blocks,
	// regardless of any config.java_vuser.enabled flag in the uploaded script.
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightJavaVuserScript(t, runner, "script-preflight-java-vuser", "", true)

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Java Vuser",
			Duration: "5m",
			ScriptID: "script-preflight-java-vuser",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.Decision != "blocked" || preflight.StartNowAllowed {
		t.Fatalf("preflight = %#v, want blocked Java Vuser runner decision", preflight)
	}
	if len(preflight.BlockingConflicts) != 1 || preflight.BlockingConflicts[0].Code != "java_vuser_runner_required" {
		t.Fatalf("blocking conflicts = %#v, want Java Vuser runner conflict", preflight.BlockingConflicts)
	}
	if !strings.Contains(preflight.BlockingConflicts[0].Message, config.EnvJavaVuserRunnerEnabled) {
		t.Fatalf("conflict message = %q, want operator-enablement guidance referencing %s", preflight.BlockingConflicts[0].Message, config.EnvJavaVuserRunnerEnabled)
	}
	if !strings.Contains(preflight.BlockingConflicts[0].Message, "java_vuser_run") || !strings.Contains(preflight.BlockingConflicts[0].Message, "Import review and authoring approval do not require this runner") {
		t.Fatalf("conflict message = %q, want sidecar-action-specific guidance", preflight.BlockingConflicts[0].Message)
	}
	if preflight.RecommendedAssignment == nil {
		t.Fatalf("blocked preflight should surface a recommended assignment for recovery, got nil")
	}
}

func TestPreflightLaunchBlocksJavaVuserReviewOnlyAtExecutionBoundary(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightJavaReviewScript(runner, "script-preflight-java-review", "")

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Java Vuser Review",
			Duration: "5m",
			ScriptID: "script-preflight-java-review",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.StartNowAllowed || preflight.Decision != "blocked" {
		t.Fatalf("preflight = %#v, want review-only Java source blocked from execution", preflight)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "java_vuser_executable_action_required" {
		t.Fatalf("blocking conflicts = %#v, want executable Java lifecycle action requirement", preflight.BlockingConflicts)
	}
}

func TestCreateTestBlocksJavaVuserReviewOnlyAtExecutionBoundary(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightJavaReviewScript(runner, "script-create-java-review", "")

	_, err := runner.CreateTest(CreateTestRequest{
		Name: "Java Vuser Review", Duration: "5m", ScriptID: "script-create-java-review", VUsers: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "java_vuser_executable_action_required") {
		t.Fatalf("CreateTest error = %v, want review-only Java execution rejection", err)
	}
}

func TestCreateTestBlocksApprovedCombinedJavaVuserWithoutLocalRunner(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	const projectID = "project-java-approved-no-local-runner"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)

	_, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Approved combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "java_vuser_runner_required") {
		t.Fatalf("CreateTest error = %v, want governed local runner rejection", err)
	}
	if strings.Contains(err.Error(), "authoring_approval_required") {
		t.Fatalf("CreateTest error = %v, authoring approval should remain valid without a runner", err)
	}
}

func TestPreflightLaunchBlocksIncompatibleGroupAssignment(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-browser:9090|protocols=browser"}, time.Second, nil))
	storePreflightScript(runner, "script-preflight-placement", "")

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Pinned HTTP",
			Duration: "5m",
			Groups: []CreateGroupEntry{{
				Name:     "HTTP Group",
				ScriptID: "script-preflight-placement",
				VUsers:   1,
				Assignment: &GeneratorAssignmentSettings{
					Mode:            generatorAssignmentModeSpecificHosts,
					SelectedTargets: []string{"gen-browser:9090"},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.Decision != "blocked" || preflight.StartNowAllowed {
		t.Fatalf("preflight = %#v, want blocked placement decision", preflight)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "generator_placement" {
		t.Fatalf("blocking conflicts = %#v, want generator_placement", preflight.BlockingConflicts)
	}
	if !strings.Contains(preflight.BlockingConflicts[0].Message, "does not support protocol http") {
		t.Fatalf("placement conflict message = %q, want protocol detail", preflight.BlockingConflicts[0].Message)
	}
}

func TestCreateTestBlocksJavaVuserWithoutRunnerCapability(t *testing.T) {
	// Even a script that sets config.java_vuser.enabled=true cannot self-clear
	// the gate: only the operator env enables the runner.
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightJavaVuserScript(t, runner, "script-create-java-vuser", "", true)

	_, err := runner.CreateTest(CreateTestRequest{
		Name:     "Java Vuser",
		Duration: "5m",
		ScriptID: "script-create-java-vuser",
		VUsers:   1,
	})
	if err == nil || !strings.Contains(err.Error(), "java_vuser_runner_required") {
		t.Fatalf("CreateTest error = %v, want Java Vuser runner required", err)
	}
}

func TestPreflightLaunchAllowsJavaVuserWithRunnerCapability(t *testing.T) {
	// The operator has enabled the runner on this fleet; the gate clears.
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightJavaVuserScript(t, runner, "script-preflight-java-vuser-enabled", "", false)

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Java Vuser",
			Duration: "5m",
			ScriptID: "script-preflight-java-vuser-enabled",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if !preflight.StartNowAllowed || preflight.Decision != "start_now" {
		t.Fatalf("preflight = %#v, want start_now when runner capability is enabled", preflight)
	}
}

func TestPreflightLaunchBlocksUnapprovedCombinedJavaVuserWithDistributedRunnerCapability(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	for _, feature := range []string{protocol.FeatureJavaVuserPersistentLifecycleV1} {
		t.Run(feature, func(t *testing.T) {
			runner := NewTestRunner(10, NewHub(), nil)
			runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"}, time.Second, nil))
			setPreflightJavaVuserInventory(runner, feature)
			storePreflightCombinedJavaVuserScript(t, runner, "script-preflight-java-vuser-fleet", "")

			preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
				Name: "Java Vuser", Duration: "5m", ScriptID: "script-preflight-java-vuser-fleet", VUsers: 1,
			}})
			if err != nil {
				t.Fatalf("PreflightLaunch returned error: %v", err)
			}
			if preflight.StartNowAllowed || preflight.Decision != "blocked" ||
				len(preflight.BlockingConflicts) == 0 ||
				preflight.BlockingConflicts[0].Code != "java_vuser_authoring_approval_required" {
				t.Fatalf("preflight = %#v, want authoring approval block with %s runner available", preflight, feature)
			}
		})
	}
}

func TestPreflightLaunchBlocksCombinedJavaVuserWithoutPersistentRunnerCapability(t *testing.T) {
	const projectID = "project-java-no-persistent-runner"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"}, time.Second, nil))
	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPhaseLifecycleV1)

	preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "Combined Java Vuser", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.Decision != "blocked" || len(preflight.BlockingConflicts) == 0 {
		t.Fatalf("preflight = %#v, want persistent-runner capability block", preflight)
	}
	conflict := preflight.BlockingConflicts[0]
	if conflict.Code != "java_vuser_runner_required" ||
		!strings.Contains(conflict.Message, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!strings.Contains(conflict.Message, protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("conflict = %#v, want missing persistent lifecycle capability", conflict)
	}
}

func TestPreflightLaunchRejectsTokenlessPersistentJavaVuserRunner(t *testing.T) {
	const projectID = "project-java-tokenless-runner"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	setPreflightJavaVuserInventoryEntries(server.runner, GeneratorInventoryEntry{
		Target: "gen-jvm:9090", GeneratorID: "gen-jvm", Status: "connected", DrainState: "active",
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: []string{protocol.FeatureJavaVuserPersistentLifecycleV1},
		AvailableCapacity:  25,
		MaxVUsers:          25,
	})

	preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "Combined Java Vuser", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch: %v", err)
	}
	if preflight.Decision != "blocked" || len(preflight.BlockingConflicts) == 0 {
		t.Fatalf("preflight = %#v, want tokenless-runner block", preflight)
	}
	conflict := preflight.BlockingConflicts[0]
	if conflict.Code != "java_vuser_runner_required" ||
		!strings.Contains(conflict.Message, protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("conflict = %#v, want authenticated frame-token requirement", conflict)
	}
}

func TestPreflightLaunchDoesNotUsePersistentRunnerOutsideRootAssignment(t *testing.T) {
	const projectID = "project-java-pinned-runtime"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend([]string{
		"gen-persistent:9090|protocols=java_vuser|labels=runtime:java_vuser",
		"gen-pinned:9090|protocols=java_vuser|labels=runtime:java_vuser",
	}, time.Second, nil))
	for _, tc := range []struct {
		name       string
		status     string
		drainState string
		features   []string
	}{
		{name: "phase only", status: "connected", drainState: "active", features: []string{protocol.FeatureJavaVuserPhaseLifecycleV1}},
		{name: "unreachable", status: "unreachable", drainState: "active", features: []string{protocol.FeatureJavaVuserPersistentLifecycleV1}},
		{name: "draining", status: "connected", drainState: "draining", features: []string{protocol.FeatureJavaVuserPersistentLifecycleV1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPreflightJavaVuserInventoryEntries(server.runner,
				GeneratorInventoryEntry{
					Target: "gen-persistent:9090", GeneratorID: "gen-persistent", Status: "connected", DrainState: "active",
					SupportedProtocols: []string{"java_vuser"}, CompatibleFeatures: governedPersistentJavaRuntimeFeatures(),
					AvailableCapacity: 25, MaxVUsers: 25,
				},
				GeneratorInventoryEntry{
					Target: "gen-pinned:9090", GeneratorID: "gen-pinned", Status: tc.status, DrainState: tc.drainState,
					SupportedProtocols: []string{"java_vuser"}, CompatibleFeatures: tc.features,
					AvailableCapacity: 25, MaxVUsers: 25,
				},
			)

			preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
				Name: "Pinned combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
				Assignment: &GeneratorAssignmentSettings{
					Mode:            generatorAssignmentModeSpecificHosts,
					SelectedTargets: []string{"gen-pinned:9090"},
				},
			}})
			if err != nil {
				t.Fatalf("PreflightLaunch: %v", err)
			}
			if preflight.StartNowAllowed || preflight.Decision != "blocked" {
				t.Fatalf("preflight = %#v, want ineligible assigned target to block launch", preflight)
			}
			if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "java_vuser_runner_required" {
				t.Fatalf("blocking conflicts = %#v, want Java runner conflict", preflight.BlockingConflicts)
			}
		})
	}
}

func TestPreflightLaunchDoesNotUsePersistentRunnerOutsideGroupLabels(t *testing.T) {
	const projectID = "project-java-labelled-runtime"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend([]string{
		"gen-general:9090|protocols=java_vuser|labels=runtime:java_vuser,pool:general",
		"gen-isolated:9090|protocols=java_vuser|labels=runtime:java_vuser,pool:isolated",
	}, time.Second, nil))
	setPreflightJavaVuserInventoryEntries(server.runner,
		GeneratorInventoryEntry{
			Target: "gen-general:9090", GeneratorID: "gen-general", Status: "connected", DrainState: "active",
			SupportedProtocols: []string{"java_vuser"}, CompatibleFeatures: governedPersistentJavaRuntimeFeatures(),
			AvailableCapacity: 25, MaxVUsers: 25,
		},
		GeneratorInventoryEntry{
			Target: "gen-isolated:9090", GeneratorID: "gen-isolated", Status: "connected", DrainState: "active",
			SupportedProtocols: []string{"java_vuser"}, CompatibleFeatures: []string{protocol.FeatureJavaVuserPhaseLifecycleV1},
			AvailableCapacity: 25, MaxVUsers: 25,
		},
	)

	preflight, err := server.runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "Labelled combined Java", ProjectID: projectID, Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name: "java", ScriptID: approved.Script.ID, VUsers: 1,
			Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto, RequiredLabels: map[string]string{"pool": "isolated"}},
		}},
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch: %v", err)
	}
	if preflight.StartNowAllowed || preflight.Decision != "blocked" {
		t.Fatalf("preflight = %#v, want labelled phase-only target to block launch", preflight)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "java_vuser_runner_required" {
		t.Fatalf("blocking conflicts = %#v, want Java runner conflict", preflight.BlockingConflicts)
	}
}

func TestStartTestRevalidatesPersistentJavaRunnerCapability(t *testing.T) {
	const projectID = "project-java-start-revalidation"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPersistentLifecycleV1)
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	// The queued test was valid when created, but the generator subsequently
	// downgraded to phase-only execution. Start must fail before backend dispatch.
	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPhaseLifecycleV1)
	err = server.runner.StartTest(test.ID)
	if err == nil || !strings.Contains(err.Error(), "java_vuser_runner_required") {
		t.Fatalf("StartTest error = %v, want persistent Java runtime rejection", err)
	}
}

func TestStartTestRevalidatesAggregatePersistentJavaRunnerCapacity(t *testing.T) {
	const projectID = "project-java-start-capacity"
	server := newJavaVuserGovernanceServer(t, projectID)
	first := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	second := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	features := governedPersistentJavaRuntimeFeatures()
	setPreflightJavaVuserInventoryEntries(server.runner, GeneratorInventoryEntry{
		Target:             "gen-jvm:9090",
		GeneratorID:        "gen-jvm",
		Status:             "connected",
		DrainState:         "active",
		AvailableCapacity:  100,
		MaxVUsers:          100,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: features,
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       6,
			AvailableVUsers: 6,
			Features:        features,
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	})
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:      "Combined Java aggregate capacity",
		ProjectID: projectID,
		Duration:  "5m",
		Groups: []CreateGroupEntry{
			{Name: "first-java", ScriptID: first.Script.ID, VUsers: 4},
			{Name: "second-java", ScriptID: second.Script.ID, VUsers: 3},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	err = server.runner.StartTest(test.ID)
	if err == nil || !strings.Contains(err.Error(), "generator_placement") ||
		!strings.Contains(err.Error(), "available java_vuser runtime slots") {
		t.Fatalf("StartTest error = %v, want aggregate Java runtime-capacity rejection", err)
	}
	if got, _ := server.runner.GetTest(test.ID); got == nil || got.Status == "INITIALIZING" || got.Status == "RUNNING" {
		t.Fatalf("test status after rejected start = %#v, want pre-initialization rejection", got)
	}
}

func TestStartTestReservesPersistentJavaCapacityAcrossInitializingTests(t *testing.T) {
	const projectID = "project-java-cross-test-capacity"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	features := governedPersistentJavaRuntimeFeatures()
	server.runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target:             "gen-jvm:9090",
		GeneratorID:        "gen-jvm",
		Status:             "connected",
		DrainState:         "active",
		AvailableCapacity:  100,
		MaxVUsers:          100,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: features,
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       6,
			AvailableVUsers: 6,
			Features:        features,
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	}}, time.Now())
	first, err := server.runner.CreateTest(CreateTestRequest{
		Name: "First combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 4,
	})
	if err != nil {
		t.Fatalf("CreateTest(first): %v", err)
	}
	second, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Second combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 3,
	})
	if err != nil {
		t.Fatalf("CreateTest(second): %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[first.ID].Status = "INITIALIZING"
	server.runner.mu.Unlock()

	err = server.runner.StartTest(second.ID)
	if err == nil || !strings.Contains(err.Error(), "generator_placement") ||
		!strings.Contains(err.Error(), "available java_vuser runtime slots") {
		t.Fatalf("StartTest(second) error = %v, want capacity reserved by initializing first test", err)
	}
}

func TestJavaPlacementUsesFreshPostStartCapacityWithoutDoubleReservation(t *testing.T) {
	const projectID = "project-java-fresh-capacity"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	features := governedPersistentJavaRuntimeFeatures()
	setCapacity := func(available int64, capturedAt time.Time) {
		server.runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
			Target:             "gen-jvm:9090",
			GeneratorID:        "gen-jvm",
			Status:             "connected",
			DrainState:         "active",
			AvailableCapacity:  100,
			MaxVUsers:          100,
			SupportedProtocols: []string{"java_vuser"},
			CompatibleFeatures: features,
			RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
				Protocol:        "java_vuser",
				Available:       true,
				MaxVUsers:       6,
				AvailableVUsers: available,
				Features:        features,
				OccupancyMode:   generatorrpc.RuntimeOccupancyModePersistent,
			}},
		}}, capturedAt)
	}
	setCapacity(6, time.Now())
	first, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Running combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(first): %v", err)
	}
	second, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Next combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 5,
	})
	if err != nil {
		t.Fatalf("CreateTest(second): %v", err)
	}
	startedAt := time.Now().Add(-2 * time.Second)
	server.runner.mu.Lock()
	server.runner.tests[first.ID].Status = "RUNNING"
	server.runner.tests[first.ID].StartedAt = &startedAt
	server.runner.mu.Unlock()
	setCapacity(5, time.Now())

	server.runner.mu.RLock()
	conflicts := server.runner.evaluateGeneratorPlacementConflictsLocked(second)
	server.runner.mu.RUnlock()
	if len(conflicts) != 0 {
		t.Fatalf("fresh generator capacity was double-reserved: %#v", conflicts)
	}
}

func TestPreflightLaunchPreservesLegacyJavaVuserOneShotRunnerGate(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"}, time.Second, nil))
	setPreflightJavaVuserInventory(runner)
	storePreflightJavaVuserScript(t, runner, "script-preflight-java-vuser-legacy", "", false)

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "Legacy Java Vuser", Duration: "5m", ScriptID: "script-preflight-java-vuser-legacy", VUsers: 1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if !preflight.StartNowAllowed || preflight.Decision != "start_now" {
		t.Fatalf("preflight = %#v, want legacy one-shot runner compatibility", preflight)
	}
}

func TestPreflightLaunchBlocksJavaVuserWhenOnlyControllerEnvEnabled(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-http:9090|protocols=http"}, time.Second, nil))
	storePreflightJavaVuserScript(t, runner, "script-preflight-java-vuser-controller-only", "", false)

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Java Vuser",
			Duration: "5m",
			ScriptID: "script-preflight-java-vuser-controller-only",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.Decision != "blocked" || preflight.StartNowAllowed {
		t.Fatalf("preflight = %#v, want blocked without distributed runner capability", preflight)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "java_vuser_runner_required" {
		t.Fatalf("blocking conflicts = %#v, want Java Vuser runner conflict", preflight.BlockingConflicts)
	}
}

func TestPreflightLaunchBlocksSAPGUIExternalRunner(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightSAPGUIScript(runner, "script-preflight-sapgui", "")

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "SAP GUI",
			Duration: "5m",
			ScriptID: "script-preflight-sapgui",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.Decision != "blocked" || preflight.StartNowAllowed {
		t.Fatalf("preflight = %#v, want blocked external runner decision", preflight)
	}
	if len(preflight.BlockingConflicts) != 1 || preflight.BlockingConflicts[0].Code != "external_runner_required" {
		t.Fatalf("blocking conflicts = %#v, want external runner conflict", preflight.BlockingConflicts)
	}
	if !strings.Contains(preflight.BlockingConflicts[0].Message, "windows_sap_gui_runner") {
		t.Fatalf("conflict message = %q, want windows runner guidance", preflight.BlockingConflicts[0].Message)
	}
}

func TestCreateTestBlocksSAPGUIExternalRunner(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightSAPGUIScript(runner, "script-create-sapgui", "")

	_, err := runner.CreateTest(CreateTestRequest{
		Name:     "SAP GUI",
		Duration: "5m",
		ScriptID: "script-create-sapgui",
		VUsers:   1,
	})
	if err == nil || !strings.Contains(err.Error(), "external_runner_required") {
		t.Fatalf("CreateTest error = %v, want external runner required", err)
	}
}

func TestPreflightLaunchBlocksBrowserDensityWithoutBenchmarkEvidence(t *testing.T) {
	runner := NewTestRunner(100, NewHub(), nil)
	storePreflightBrowserScript(runner, "script-preflight-browser-density", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.HeadroomPct = 0
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Browser Density",
			Duration: "5m",
			ScriptID: "script-preflight-browser-density",
			VUsers:   30,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.Decision != "blocked" || preflight.StartNowAllowed {
		t.Fatalf("preflight = %#v, want blocked browser density gate", preflight)
	}
	if len(preflight.BlockingConflicts) != 1 || preflight.BlockingConflicts[0].Code != "browser_density_release_gate" {
		t.Fatalf("blocking conflicts = %#v, want browser density gate only", preflight.BlockingConflicts)
	}
	if preflight.CapacityAdvisory == nil || !preflight.CapacityAdvisory.BrowserDensityOverrideRequired || preflight.CapacityAdvisory.RequiresOverride {
		t.Fatalf("capacity advisory = %#v, want browser density gate without capacity shortfall", preflight.CapacityAdvisory)
	}
}

func TestPreflightLaunchAllowsBoundedBrowserDensityWithEvidenceOverride(t *testing.T) {
	runner := NewTestRunner(100, NewHub(), nil)
	storePreflightBrowserScript(runner, "script-preflight-browser-density-cleared", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.HeadroomPct = 0
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Bounded Browser Density",
			Duration: "5m",
			ScriptID: "script-preflight-browser-density-cleared",
			VUsers:   30,
			RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{
				"APEXLOAD_BROWSER_BENCHMARK_EVIDENCE": "benchmarks/browser-density-2026-06-09.txt",
				"APEXLOAD_BROWSER_DENSITY_OVERRIDE":   "true",
			}},
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if !preflight.StartNowAllowed || preflight.Decision != "start_now" {
		t.Fatalf("preflight = %#v, want start_now after browser density evidence override", preflight)
	}
	if len(preflight.BlockingConflicts) != 0 {
		t.Fatalf("blocking conflicts = %#v, want none", preflight.BlockingConflicts)
	}
	if preflight.CapacityAdvisory == nil || preflight.CapacityAdvisory.BrowserDensityReleaseGate != browserDensityReleaseGateCleared {
		t.Fatalf("capacity advisory = %#v, want cleared browser density release gate", preflight.CapacityAdvisory)
	}
}

func TestPreflightLaunchQueuesWhenCurrentControllerCapacityUnavailable(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-queue", "")

	active, err := runner.CreateTest(CreateTestRequest{Name: "Active", Duration: "30m", ScriptID: "script-preflight-queue", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(active) returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Queued",
			Duration: "5m",
			ScriptID: "script-preflight-queue",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.StartNowAllowed || !preflight.QueueAllowed || preflight.Decision != "queue" {
		t.Fatalf("preflight = %#v, want queue decision", preflight)
	}
	if len(preflight.BlockingConflicts) != 0 || len(preflight.Warnings) == 0 {
		t.Fatalf("preflight conflicts = blocking %#v warnings %#v, want queue warning only", preflight.BlockingConflicts, preflight.Warnings)
	}
}

func TestPreflightLaunchBlocksFutureCronCapacityOverlap(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-cron", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	settings.CapacityPlanning.HeadroomPct = 0
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Cron Scenario",
		Duration: "10m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-preflight-cron", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	if _, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: "Every Minute", CronExpr: "* * * * *"}); err != nil {
		t.Fatalf("CreateScenarioSchedule returned error: %v", err)
	}

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:     "Overlapping Ad Hoc",
			Duration: "10m",
			ScriptID: "script-preflight-cron",
			VUsers:   1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.StartNowAllowed || preflight.QueueAllowed || len(preflight.BlockingConflicts) == 0 {
		t.Fatalf("preflight = %#v, want blocking cron capacity conflict", preflight)
	}
	if preflight.BlockingConflicts[0].Code != "generator_capacity_window" {
		t.Fatalf("conflict = %#v, want generator_capacity_window", preflight.BlockingConflicts[0])
	}
}

func TestPreflightLaunchBlocksReservationStartingMidRun(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-reservation", "project-a")
	now := time.Now().UTC()
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	settings.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID:          "project-b",
		ReservedGenerators: 2,
		StartsAt:           ptr(now.Add(2 * time.Minute)),
		EndsAt:             ptr(now.Add(30 * time.Minute)),
	}}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:      "Reservation Conflict",
			Duration:  "10m",
			ProjectID: "project-a",
			ScriptID:  "script-preflight-reservation",
			VUsers:    1,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "reservation_window" {
		t.Fatalf("preflight conflicts = %#v, want reservation_window", preflight.BlockingConflicts)
	}
}

func TestPreflightLaunchProjectsActiveRunAtFutureReservationBoundary(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-active-projection", "project-a")
	now := time.Now().UTC()
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	settings.CapacityPlanning.HeadroomPct = 0
	settings.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID:          "project-b",
		ReservedGenerators: 1,
		StartsAt:           ptr(now.Add(2 * time.Minute)),
		EndsAt:             ptr(now.Add(20 * time.Minute)),
	}}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	active, err := runner.CreateTest(CreateTestRequest{
		Name:       "Active delayed run",
		Duration:   "1m",
		StartDelay: "5m",
		ProjectID:  "project-a",
		ScriptID:   "script-preflight-active-projection",
		VUsers:     1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active) returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.tests[active.ID].StartedAt = ptr(now)
	runner.mu.Unlock()

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name:      "Overlapping proposal",
		Duration:  "10m",
		ProjectID: "project-a",
		ScriptID:  "script-preflight-active-projection",
		VUsers:    1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	found := false
	for _, conflict := range preflight.BlockingConflicts {
		if conflict.Code == "reservation_window" {
			found = true
			break
		}
	}
	if !found || preflight.StartNowAllowed || preflight.QueueAllowed {
		t.Fatalf("preflight = %#v, want active-load reservation_window conflict", preflight)
	}
}

func TestLaunchScheduleWindowOverlapsNarrowWindowBetweenFormerSamples(t *testing.T) {
	start := time.Date(2026, time.July, 15, 12, 0, 30, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	window := storage.ScenarioScheduleWindow{
		Name:      "one-minute maintenance",
		StartTime: "12:02",
		EndTime:   "12:03",
		Timezone:  "UTC",
	}
	if !launchScheduleWindowOverlaps(window, "UTC", start, end) {
		t.Fatal("one-minute maintenance window inside launch interval was not detected")
	}
}

func TestLaunchScheduleAvailabilityRequiresContinuousUnionCoverage(t *testing.T) {
	start := time.Date(2026, time.July, 15, 12, 1, 0, 0, time.UTC)
	end := time.Date(2026, time.July, 15, 12, 9, 0, 0, time.UTC)
	schedule := &storage.ScenarioSchedule{
		Timezone: "UTC",
		AvailabilityWindows: []storage.ScenarioScheduleWindow{
			{Name: "first", StartTime: "12:00", EndTime: "12:03"},
			{Name: "second", StartTime: "12:04", EndTime: "12:10"},
		},
	}
	if launchScheduleAvailabilityAllows(schedule, start, end) {
		t.Fatal("availability union with a one-minute gap allowed the launch")
	}
	schedule.AvailabilityWindows[0].EndTime = "12:04"
	if !launchScheduleAvailabilityAllows(schedule, start, end) {
		t.Fatal("adjacent availability windows did not continuously cover the launch")
	}
}

func TestLaunchScheduleAvailabilityHandlesOvernightAndDSTWindows(t *testing.T) {
	t.Run("overnight", func(t *testing.T) {
		schedule := &storage.ScenarioSchedule{
			Timezone: "UTC",
			AvailabilityWindows: []storage.ScenarioScheduleWindow{{
				StartTime: "22:00",
				EndTime:   "02:00",
			}},
		}
		start := time.Date(2026, time.July, 15, 23, 30, 0, 0, time.UTC)
		if !launchScheduleAvailabilityAllows(schedule, start, start.Add(2*time.Hour)) {
			t.Fatal("overnight availability did not cover a launch spanning midnight")
		}
	})

	t.Run("spring-forward", func(t *testing.T) {
		schedule := &storage.ScenarioSchedule{
			Timezone: "America/New_York",
			AvailabilityWindows: []storage.ScenarioScheduleWindow{{
				StartTime: "01:00",
				EndTime:   "04:00",
			}},
		}
		// 01:30 EST through 03:30 EDT on the 2026 spring-forward day.
		start := time.Date(2026, time.March, 8, 6, 30, 0, 0, time.UTC)
		end := time.Date(2026, time.March, 8, 7, 30, 0, 0, time.UTC)
		if !launchScheduleAvailabilityAllows(schedule, start, end) {
			t.Fatal("availability window did not remain continuous across DST spring-forward")
		}
	})
}

func TestPreflightLaunchBlocksMaintenanceWindow(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(10, NewHub(), store)
	storePreflightScript(runner, "script-preflight-maintenance", storage.DefaultProjectID)
	now := time.Now().UTC()
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Maintenance Scenario",
		Duration: "5m",
		Groups:   []CreateGroupEntry{{ScriptID: "script-preflight-maintenance", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	schedule := &storage.ScenarioSchedule{
		ID:                 "schedule-preflight-maintenance",
		ScenarioID:         scenario.ID,
		ProjectID:          storage.DefaultProjectID,
		Name:               "Maintenance",
		CronExpr:           "* * * * *",
		Timezone:           "UTC",
		Enabled:            true,
		MaintenanceWindows: []storage.ScenarioScheduleWindow{{Name: "shared", DaysOfWeek: []int{int(now.Weekday())}, StartTime: now.Add(-time.Minute).Format("15:04"), EndTime: now.Add(20 * time.Minute).Format("15:04"), Timezone: "UTC"}},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	runner.mu.Lock()
	runner.scenarios[scenario.ID].Schedules = append(runner.scenarios[scenario.ID].Schedules, schedule)
	runner.mu.Unlock()

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{
		CreateTestRequest: CreateTestRequest{
			Name:       "Maintenance Conflict",
			Duration:   "5m",
			ScenarioID: scenario.ID,
		},
	})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "maintenance_window" {
		t.Fatalf("preflight conflicts = %#v, want maintenance_window", preflight.BlockingConflicts)
	}
}

func TestCreateTestWithScheduledStartPersistsScheduledStatus(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(10, NewHub(), store)
	storePreflightScript(runner, "script-preflight-scheduled", "")
	scheduledAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:             "Future Run",
		Duration:         "5m",
		ScriptID:         "script-preflight-scheduled",
		VUsers:           1,
		ScheduledStartAt: &scheduledAt,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if testRun.Status != "SCHEDULED" || testRun.ScheduledStartAt == nil || !testRun.ScheduledStartAt.Equal(scheduledAt) {
		t.Fatalf("testRun = %#v, want scheduled run", testRun)
	}
	if len(store.savedTests) == 0 || store.savedTests[len(store.savedTests)-1].ScheduledStartAt == nil {
		t.Fatalf("saved tests = %#v, want scheduled_start_at persisted", store.savedTests)
	}
}

func TestCreateTestWithReservedLaunchWindowRoundsAndPersistsReservedStatus(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(10, NewHub(), store)
	storePreflightScript(runner, "script-preflight-reserved", "")

	base := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour)
	start := base.Add(7*time.Minute + 45*time.Second)
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:                "Manual Reservation",
		Duration:            "5m",
		ScriptID:            "script-preflight-reserved",
		VUsers:              1,
		ReserveLaunchWindow: true,
		ScheduledStartAt:    &start,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	wantRounded := base.Add(15 * time.Minute)
	if testRun.Status != "RESERVED" || testRun.ScheduledStartAt == nil || !testRun.ScheduledStartAt.Equal(wantRounded) {
		t.Fatalf("testRun = %#v, want reserved run at %s", testRun, wantRounded)
	}
	if len(store.savedTests) == 0 || store.savedTests[len(store.savedTests)-1].Status != "RESERVED" || store.savedTests[len(store.savedTests)-1].ScheduledStartAt == nil {
		t.Fatalf("saved tests = %#v, want RESERVED with scheduled_start_at", store.savedTests)
	}

	exact := base.Add(30 * time.Minute)
	exactRun, err := runner.CreateTest(CreateTestRequest{
		Name:                "Exact Manual Reservation",
		Duration:            "5m",
		ScriptID:            "script-preflight-reserved",
		VUsers:              1,
		ReserveLaunchWindow: true,
		ScheduledStartAt:    &exact,
	})
	if err != nil {
		t.Fatalf("CreateTest exact returned error: %v", err)
	}
	if exactRun.ScheduledStartAt == nil || !exactRun.ScheduledStartAt.Equal(exact) {
		t.Fatalf("exact reserved start = %v, want %s", exactRun.ScheduledStartAt, exact)
	}
}

func TestCreateReservedLaunchWindowBlocksOverlappingReservation(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "script-preflight-reserved-overlap", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	settings.CapacityPlanning.HeadroomPct = 0
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	base := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour)
	firstStart := base.Add(7 * time.Minute)
	if _, err := runner.CreateTest(CreateTestRequest{
		Name:                "Reserved A",
		Duration:            "10m",
		ScriptID:            "script-preflight-reserved-overlap",
		VUsers:              1,
		ReserveLaunchWindow: true,
		ScheduledStartAt:    &firstStart,
	}); err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}

	secondStart := base.Add(8 * time.Minute)
	_, err := runner.CreateTest(CreateTestRequest{
		Name:                "Reserved B",
		Duration:            "10m",
		ScriptID:            "script-preflight-reserved-overlap",
		VUsers:              1,
		ReserveLaunchWindow: true,
		ScheduledStartAt:    &secondStart,
	})
	if err == nil || !strings.Contains(err.Error(), "launch window conflict") {
		t.Fatalf("CreateTest(second) error = %v, want launch window conflict", err)
	}
}

func TestScenarioSchedulerIgnoresReservedManualStartRun(t *testing.T) {
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(backend)
	storePreflightScript(runner, "script-preflight-reserved-scheduler", "")
	now := time.Date(2026, time.June, 10, 15, 0, 0, 0, time.UTC)
	reservedAt := now.Add(-time.Hour)
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:                "Reserved Manual Run",
		Duration:            "1m",
		ScriptID:            "script-preflight-reserved-scheduler",
		VUsers:              1,
		ReserveLaunchWindow: true,
		ScheduledStartAt:    &reservedAt,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}
	select {
	case startedID := <-backend.started:
		t.Fatalf("reserved run was auto-started by scheduler: %s", startedID)
	default:
	}
	current, ok := runner.GetTest(testRun.ID)
	if !ok || current.Status != "RESERVED" {
		t.Fatalf("reserved run status = %#v, want RESERVED", current)
	}
}

func TestStartTestStartsReservedManualRun(t *testing.T) {
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(backend)
	storePreflightScript(runner, "script-preflight-reserved-start", "")
	reservedAt := time.Now().UTC().Add(24 * time.Hour).Truncate(15 * time.Minute)
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:                "Reserved Start",
		Duration:            "1m",
		ScriptID:            "script-preflight-reserved-start",
		VUsers:              1,
		ReserveLaunchWindow: true,
		ScheduledStartAt:    &reservedAt,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTestWithOptions(testRun.ID, StartTestOptions{SkipLaunchWindowPreflight: true}); err != nil {
		t.Fatalf("StartTestWithOptions returned error: %v", err)
	}
	select {
	case startedID := <-backend.started:
		if startedID != testRun.ID {
			t.Fatalf("started id = %s, want %s", startedID, testRun.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reserved run to start")
	}
}

func TestScenarioSchedulerStartsDueOneTimeScheduledRun(t *testing.T) {
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner := NewTestRunner(10, NewHub(), nil)
	runner.execution = backend
	storePreflightScript(runner, "script-preflight-due", "")
	now := time.Now().UTC()
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:             "Due Run",
		Duration:         "1ms",
		ScriptID:         "script-preflight-due",
		VUsers:           1,
		ScheduledStartAt: ptr(now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[testRun.ID].Status = "SCHEDULED"
	runner.tests[testRun.ID].ScheduledStartAt = ptr(now.Add(-time.Minute))
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}
	select {
	case startedID := <-backend.started:
		if startedID != testRun.ID {
			t.Fatalf("started id = %s, want %s", startedID, testRun.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for due scheduled run to start")
	}
}

func TestBuildStartGovernanceErrorEnforcesBrowserDensityGate(t *testing.T) {
	blockingAdvisory := &CapacityAdvisory{
		BrowserVUsers:                      30,
		MaxBrowserCanaryVUsersPerGenerator: 5,
		BrowserDensityOverrideRequired:     true,
		BrowserDensityReleaseGate:          browserDensityReleaseGateRequired,
	}
	clearedAdvisory := &CapacityAdvisory{
		BrowserVUsers:                      30,
		MaxBrowserCanaryVUsersPerGenerator: 5,
		BrowserDensityOverrideRequired:     true,
		BrowserBenchmarkEvidenceAttached:   true,
		BrowserDensityOverrideRequested:    true,
		BrowserDensityReleaseGate:          browserDensityReleaseGateCleared,
	}
	clearGateAdvisory := &CapacityAdvisory{BrowserDensityReleaseGate: browserDensityReleaseGateClear}

	tests := []struct {
		name      string
		advisory  *CapacityAdvisory
		opts      StartTestOptions
		wantBlock bool
	}{
		{name: "nil advisory allowed", advisory: nil, wantBlock: false},
		{name: "clear gate allowed", advisory: clearGateAdvisory, wantBlock: false},
		{name: "cleared gate allowed", advisory: clearedAdvisory, wantBlock: false},
		{name: "blocking gate blocks", advisory: blockingAdvisory, wantBlock: true},
		{
			name:      "capacity override does not clear browser gate",
			advisory:  blockingAdvisory,
			opts:      StartTestOptions{OverrideCapacityShortfall: true},
			wantBlock: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := buildStartGovernanceError("test-id", tc.advisory, tc.opts)
			if !tc.wantBlock {
				if err != nil {
					t.Fatalf("buildStartGovernanceError = %v, want nil", err)
				}
				return
			}
			var gateErr *BrowserDensityGateError
			if !errors.As(err, &gateErr) {
				t.Fatalf("buildStartGovernanceError = %T (%v), want *BrowserDensityGateError", err, err)
			}
			if gateErr.Advisory == nil || gateErr.Advisory.BrowserDensityReleaseGate != browserDensityReleaseGateRequired {
				t.Fatalf("gate error advisory = %#v, want blocking release gate", gateErr.Advisory)
			}
		})
	}
}

func TestStartTestBlocksBrowserDensityGateAndIgnoresCapacityOverride(t *testing.T) {
	runner := NewTestRunner(100, NewHub(), nil)
	storePreflightBrowserScript(runner, "script-start-browser-density", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.HeadroomPct = 0
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Browser Density Start",
		Duration: "5m",
		ScriptID: "script-start-browser-density",
		VUsers:   30,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	// A direct start must apply the same gate the preflight endpoint reports.
	err = runner.StartTestWithOptions(test.ID, StartTestOptions{})
	var gateErr *BrowserDensityGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("StartTestWithOptions = %T (%v), want *BrowserDensityGateError", err, err)
	}

	// The capacity-shortfall override flag must not clear the browser-density gate.
	err = runner.StartTestWithOptions(test.ID, StartTestOptions{OverrideCapacityShortfall: true})
	if !errors.As(err, &gateErr) {
		t.Fatalf("StartTestWithOptions(OverrideCapacityShortfall) = %T (%v), want *BrowserDensityGateError", err, err)
	}

	queued, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("expected test %s to remain in runner", test.ID)
	}
	if queued.Status != "QUEUED" {
		t.Fatalf("test status = %s, want QUEUED", queued.Status)
	}
}

func TestStartTestAllowsBrowserDensityWithBenchmarkEvidenceOverride(t *testing.T) {
	runner := NewTestRunner(100, NewHub(), nil)
	storePreflightBrowserScript(runner, "script-start-browser-density-cleared", "")
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.HeadroomPct = 0
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Browser Density Cleared Start",
		Duration: "5m",
		ScriptID: "script-start-browser-density-cleared",
		VUsers:   30,
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{
			"APEXLOAD_BROWSER_BENCHMARK_EVIDENCE": "benchmarks/browser-density-2026-06-09.txt",
			"APEXLOAD_BROWSER_DENSITY_OVERRIDE":   "true",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	// With evidence + override the gate clears; the start must not be blocked by it.
	if err := runner.StartTestWithOptions(test.ID, StartTestOptions{SkipLaunchWindowPreflight: true}); err != nil {
		var gateErr *BrowserDensityGateError
		if errors.As(err, &gateErr) {
			t.Fatalf("StartTestWithOptions returned browser density gate error after override: %v", err)
		}
	}
}

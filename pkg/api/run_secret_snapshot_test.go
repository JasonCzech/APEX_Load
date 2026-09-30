package api

import (
	"bytes"
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
)

func TestRunPinnedSecretsSurviveRotationAndSealForRecovery(t *testing.T) {
	state := &runPinnedSecretState{}
	state.setIfAbsent(map[int]map[string]string{0: {"LOGIN_PASSWORD": "before-rotation"}})
	test := &ManagedTest{ID: "run-one", ProjectID: "project-one", ApplicationID: "app-one", pinnedSecrets: state}
	resolver := (&TestRunner{}).secretResolverForRunGroup(test, 0, test.ProjectID)
	if value, ok := engine.ResolveSecret(context.Background(), resolver, "LOGIN_PASSWORD"); !ok || value != "before-rotation" {
		t.Fatalf("pinned value = (%q, %t)", value, ok)
	}
	state.setIfAbsent(map[int]map[string]string{0: {"LOGIN_PASSWORD": "after-rotation"}})
	if value, _ := engine.ResolveSecret(context.Background(), resolver, "LOGIN_PASSWORD"); value != "before-rotation" {
		t.Fatalf("rotation changed running test value to %q", value)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x57}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, wrapped, err := sealRunPinnedSecrets(context.Background(), wrapper, test.ID, state.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := openRunPinnedSecrets(context.Background(), wrapper, test.ID, encoded, wrapped)
	if err != nil || recovered[0]["LOGIN_PASSWORD"] != "before-rotation" {
		t.Fatalf("recovered value = %q, %v", recovered[0]["LOGIN_PASSWORD"], err)
	}
	if _, err := openRunPinnedSecrets(context.Background(), wrapper, "different-run", encoded, wrapped); err == nil {
		t.Fatal("snapshot opened under a different run")
	}
	if _, err := openRunPinnedSecrets(context.Background(), wrapper, test.ID, encoded, nil); err == nil {
		t.Fatal("snapshot with missing wrapped key accepted")
	}
}

func TestQueuedStartSecretRetryRefreshesPinBeforeFirstExecution(t *testing.T) {
	store := &versionedShakeoutSecretStore{value: "before-rotation"}
	runner := NewTestRunner(1, NewHub(), nil, WithProjectSecretStore(store))
	script := &dsl.Script{ID: "shared-login", Name: "Shared login", ProjectID: "project-one",
		Actions: []dsl.Action{{Name: "login", Type: "request", Headers: map[string]string{
			"Authorization": "{{secret.LOGIN_PASSWORD}}",
		}}},
	}
	run := &ManagedTest{ID: "queued-run", ProjectID: "project-one", Status: "QUEUED",
		groupSpecs: []engine.GroupConfig{{Name: "group", Script: script}},
		Groups:     []TestGroup{{Name: "group"}}, pinnedSecrets: &runPinnedSecretState{}}
	runner.tests[run.ID] = run
	if err := runner.validateStartSecretRequirements(run.ID); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.value = "after-rotation"
	store.mu.Unlock()
	if err := runner.validateStartSecretRequirements(run.ID); err != nil {
		t.Fatal(err)
	}
	if got := run.pinnedSecrets.snapshot()[0]["LOGIN_PASSWORD"]; got != "after-rotation" {
		t.Fatalf("queued retry kept the old value: %q", got)
	}
	run.Status = "INITIALIZING"
	store.mu.Lock()
	store.value = "another-rotation"
	store.mu.Unlock()
	if err := runner.validateStartSecretRequirements(run.ID); err != nil {
		t.Fatal(err)
	}
	if got := run.pinnedSecrets.snapshot()[0]["LOGIN_PASSWORD"]; got != "after-rotation" {
		t.Fatalf("admitted run changed its value: %q", got)
	}
	run.Status = "QUEUED"
	run.StartAuthorization = &QueuedStartAuthorization{ScriptShakeout: &ScriptShakeoutStartAuthorization{}}
	if err := runner.validateStartSecretRequirements(run.ID); err != nil {
		t.Fatal(err)
	}
	if got := run.pinnedSecrets.snapshot()[0]["LOGIN_PASSWORD"]; got != "after-rotation" {
		t.Fatalf("reviewed shakeout changed its value: %q", got)
	}
	run.pinnedSecrets = nil
	if err := runner.validateStartSecretRequirements(run.ID); err == nil {
		t.Fatal("start admitted a secret-dependent run without pin state")
	}
	noSecretRun := &ManagedTest{ID: "no-secret-run", ProjectID: "project-one", Status: "QUEUED",
		groupSpecs: []engine.GroupConfig{{Name: "group", Script: &dsl.Script{ID: "no-secret-script",
			Actions: []dsl.Action{{Name: "call", Type: "request", Target: "/"}}}}},
		Groups: []TestGroup{{Name: "group"}}}
	runner.tests[noSecretRun.ID] = noSecretRun
	if err := runner.validateStartSecretRequirements(noSecretRun.ID); err != nil {
		t.Fatalf("secret-free run required pin state: %v", err)
	}
}

func TestTerminalRunsReleaseSharedSecretPins(t *testing.T) {
	for _, status := range []string{"COMPLETE", "FAILED", "ABORTED", "PENDING", "RUNNING"} {
		t.Run(status, func(t *testing.T) {
			state := &runPinnedSecretState{}
			state.setIfAbsent(map[int]map[string]string{0: {"TOKEN": "private-value"}})
			test := &ManagedTest{Status: status, pinnedSecrets: state}
			retainedCopy := *test
			revision := state.version()
			trimTerminalManagedTestRuntime(test)
			if !isTerminalTestStatus(status) {
				if values, _ := state.get(0); values["TOKEN"] != "private-value" {
					t.Fatal("live pin cleared")
				}
				return
			}
			if retainedCopy.pinnedSecrets.snapshot() != nil {
				t.Fatal("retained test copy still holds plaintext")
			}
			if state.pinRevision(map[int]map[string]string{0: {"TOKEN": "late-value"}}, revision, false) {
				t.Fatal("terminal state accepted late admission")
			}
			state.setIfAbsent(map[int]map[string]string{0: {"TOKEN": "late-adoption"}})
			if values, pinned := state.get(0); !pinned || len(values) != 0 {
				t.Fatal("terminal state permits repopulation or provider fallback")
			}
		})
	}
}

func TestEditedRunRejectsStaleSecretResolution(t *testing.T) {
	state := &runPinnedSecretState{}
	revision := state.version()
	state.invalidate(false)
	if state.pinRevision(map[int]map[string]string{0: {"OLD": "old"}}, revision, false) {
		t.Fatal("accepted resolution from before edit")
	}
	if !state.pinRevision(map[int]map[string]string{0: {"NEW": "new"}}, state.version(), false) {
		t.Fatal("rejected current resolution")
	}
}

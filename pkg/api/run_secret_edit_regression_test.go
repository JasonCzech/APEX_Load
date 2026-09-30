package api

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestQueuedRunExecutableEditsRefreshSecretPins(t *testing.T) {
	for _, parameters := range []bool{false, true} {
		t.Run(map[bool]string{false: "runtime", true: "parameters"}[parameters], func(t *testing.T) {
			server, _ := newRuntimeOverrideTestServer(t)
			test := createQueuedGroupSettingsTest(t, server)
			runner := server.runner
			runner.mu.Lock()
			managed := runner.tests[test.ID]
			state := managed.pinnedSecrets
			state.setIfAbsent(map[int]map[string]string{0: {"OLD": "old-value"}})
			bound := runner.boundSecretResolverForRunGroup(managed, 0, managed.ProjectID)
			projectID := managed.ProjectID
			runner.mu.Unlock()
			t.Setenv(engine.ProjectSecretEnvironmentName(projectID, "NEW"), "new-value")
			if parameters {
				view, err := runner.GetTestGroupParametersView(test.ID, "Buyers")
				if err != nil {
					t.Fatal(err)
				}
				next := cloneScriptParameters(view.Parameters)
				next[1].Selection = "sequential"
				if _, err := runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next); err != nil {
					t.Fatal(err)
				}
			} else {
				http := dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: "{{secret.NEW}}"}}
				if _, err := runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, runner, test.ID, "Buyers"), &RuntimeSettingsOverride{HTTP: &http}, []string{"http"}); err != nil {
					t.Fatal(err)
				}
			}
			if state.snapshot() != nil {
				t.Fatal("edit retained obsolete snapshot")
			}
			if err := runner.validateStartSecretRequirements(test.ID); err != nil {
				t.Fatalf("re-admission: %v", err)
			}
			if !parameters {
				if value, ok := engine.ResolveSecret(context.Background(), bound, "NEW"); !ok || value != "new-value" {
					t.Fatalf("compiled resolver = %q, %t", value, ok)
				}
			}
		})
	}
}

func TestRejectedExecutableEditPreservesSecretPins(t *testing.T) {
	for _, parameters := range []bool{false, true} {
		t.Run(map[bool]string{false: "runtime", true: "parameters"}[parameters], func(t *testing.T) {
			server, _ := newRuntimeOverrideTestServer(t)
			test := createQueuedGroupSettingsTest(t, server)
			runner := server.runner
			runner.mu.Lock()
			state := runner.tests[test.ID].pinnedSecrets
			state.setIfAbsent(map[int]map[string]string{0: {"TOKEN": "original"}})
			originalStore := runner.testStore
			runner.testStore = failingTestPersistence{err: errors.New("save unavailable")}
			runner.mu.Unlock()
			defer func() { runner.mu.Lock(); runner.testStore = originalStore; runner.mu.Unlock() }()
			var err error
			if parameters {
				view, e := runner.GetTestGroupParametersView(test.ID, "Buyers")
				if e != nil {
					t.Fatal(e)
				}
				next := cloneScriptParameters(view.Parameters)
				next[1].Selection = "sequential"
				_, err = runner.UpdateTestGroupParameters(test.ID, "Buyers", view.Revision, next)
			} else {
				_, err = runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", mustGroupSettingsRevision(t, runner, test.ID, "Buyers"), &RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 1777}}, []string{"pacing"})
			}
			if !errors.Is(err, errTestGroupSettingsPersist) {
				t.Fatalf("edit error = %v", err)
			}
			if values, ok := state.get(0); !ok || values["TOKEN"] != "original" {
				t.Fatal("failed edit destroyed snapshot")
			}
		})
	}
}

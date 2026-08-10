package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// The generator RPC boundary is the third copy of the static credential
// chokepoint, alongside engine.New/NewMultiGroup/ReplayScript/shakeout.
//
// It reads the same operator setting off the SCRIPT, so the decision travels with
// scriptContent and changes the assignment fingerprint on its own — there is no
// proto field for it. The platform-managed secret bundle is registered first and
// is never gated.
func TestStagedAssignmentScriptSourcedRedactionFollowsScriptSetting(t *testing.T) {
	const (
		retainedCredential = "staged-retained-credential-4471"
		platformSecret     = "staged-platform-secret-9053"
	)
	stagedScript := func(redactRunEvidence *bool) []byte {
		logConfig := map[string]any{"level": "standard"}
		if redactRunEvidence != nil {
			logConfig["redact_run_evidence"] = *redactRunEvidence
		}
		payload, err := json.Marshal(map[string]any{
			"name":     "stage",
			"protocol": "http",
			"config": map[string]any{
				"base_url": "https://example.test",
				"log":      logConfig,
			},
			"metadata": map[string]any{
				"import": map[string]any{
					"source": "openapi",
					"preserved": map[string]any{
						dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					},
				},
			},
			"actions": []any{map[string]any{
				"name":   "request",
				"type":   "request",
				"method": "POST",
				"target": "/verify",
				"header_list": []any{
					map[string]any{"name": "Content-Type", "value": "application/json"},
					map[string]any{"name": "X-Platform", "value": "{{secret.PLATFORM_TOKEN}}"},
				},
				"body": `{"pin":"` + retainedCredential + `"}`,
				"metadata": map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
						map[string]any{"field": "body", "pointer": "/pin"},
					},
				},
			}},
		})
		if err != nil {
			t.Fatalf("marshal staged script: %v", err)
		}
		return payload
	}
	redactOn, redactOff := true, false

	for _, testCase := range []struct {
		name              string
		redactRunEvidence *bool
		redacted          bool
	}{
		{name: "unset_defaults_to_redacting", redactRunEvidence: nil, redacted: true},
		{name: "redaction_on", redactRunEvidence: &redactOn, redacted: true},
		{name: "redaction_off", redactRunEvidence: &redactOff, redacted: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newIsolatedService(t, 10, WithExecution())
			request := stageRequest("run", "script", 1)
			request.ScriptContent = stagedScript(testCase.redactRunEvidence)
			request.SecretBundle = map[string]string{"PLATFORM_TOKEN": platformSecret}
			request.SecretBundleId = "bundle-1"
			validated := validatedStageRequest(t, request)

			service.mu.Lock()
			decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
			service.mu.Unlock()
			if decision.kind != assignmentStagePrepare || decision.plan == nil {
				t.Fatalf("staging decision = %+v", decision)
			}
			prepared, err := service.prepareAssignmentStage(context.Background(), decision.plan)
			if err != nil {
				service.mu.Lock()
				service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
				service.mu.Unlock()
				t.Fatalf("prepareAssignmentStage: %v", err)
			}
			defer func() {
				service.mu.Lock()
				service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
				service.mu.Unlock()
				service.cleanupPreparedAssignmentStage(decision.plan, prepared)
			}()

			config := prepared.execution.redactionConfig
			evidence := config.ScrubText("pin=" + retainedCredential + " token=" + platformSecret)
			if strings.Contains(evidence, platformSecret) {
				t.Fatalf("staged assignment leaked the platform-managed secret bundle value: %q", evidence)
			}
			switch {
			case testCase.redacted && strings.Contains(evidence, retainedCredential):
				t.Fatalf("script-sourced credential survived staged output with redaction on: %q", evidence)
			case !testCase.redacted && !strings.Contains(evidence, retainedCredential):
				t.Fatalf("script-sourced credential was scrubbed from staged output with redaction off: %q", evidence)
			}
		})
	}
}

// ORIGIN, not just scrubbing. prepareAssignmentStage registers the secret bundle
// PLATFORM-MANAGED and the retained import credentials SCRIPT-SOURCED, and the
// distinction lives entirely in which registration function it calls. Both
// origins feed the same exact-value text matcher, so ScrubText is byte-identical
// either way and the sibling test above cannot see the difference: swapping
// engine.WithScriptSourcedSecretValueRedaction for
// engine.WithSecretValueRedactionChecked leaves it green.
//
// HasRuntimeSecretMaterial is the surface that reads origin. Protocols consult it
// to suppress screenshots and browser HAR/trace capture, which are binary
// artifacts no text matcher can prove clean. Registering an imported script's own
// literals as platform-managed would silently drop that evidence from distributed
// runs while the identical local run kept it — so this assertion belongs in the
// package that makes the call, not only in its engine-side twin.
func TestStagedAssignmentKeepsScriptSourcedCredentialsOutOfPlatformMaterial(t *testing.T) {
	const (
		retainedCredential = "staged-origin-credential-6178"
		bundleSecret       = "staged-origin-bundle-2245"
	)
	// The {{secret.*}} reference is present only when a bundle is staged: the
	// staging boundary rejects a bundle whose logical secret nothing references.
	// It is the only platform-managed material in play, which is what the cases
	// vary.
	stagedScript := func(referenceSecret bool) []byte {
		headers := []any{map[string]any{"name": "Content-Type", "value": "application/json"}}
		if referenceSecret {
			headers = append(headers, map[string]any{
				"name": "X-Platform", "value": "{{secret.PLATFORM_TOKEN}}",
			})
		}
		payload, err := json.Marshal(map[string]any{
			"name":     "stage",
			"protocol": "http",
			"config": map[string]any{
				"base_url": "https://example.test",
				// The setting is left UNSET, which is the default and reads as
				// redacting. The script-sourced half is therefore registered.
				"log": map[string]any{"level": "standard"},
			},
			"metadata": map[string]any{
				"import": map[string]any{
					"source": "openapi",
					"preserved": map[string]any{
						dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					},
				},
			},
			"actions": []any{map[string]any{
				"name":        "request",
				"type":        "request",
				"method":      "POST",
				"target":      "/verify",
				"header_list": headers,
				"body":        `{"pin":"` + retainedCredential + `"}`,
				"metadata": map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
						map[string]any{"field": "body", "pointer": "/pin"},
					},
				},
			}},
		})
		if err != nil {
			t.Fatalf("marshal staged script: %v", err)
		}
		return payload
	}

	for _, testCase := range []struct {
		name            string
		bundle          map[string]string
		platformManaged bool
	}{
		{name: "script_sourced_only", platformManaged: false},
		{
			name:            "secret_bundle_is_platform_managed",
			bundle:          map[string]string{"PLATFORM_TOKEN": bundleSecret},
			platformManaged: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newIsolatedService(t, 10, WithExecution())
			request := stageRequest("run", "script", 1)
			request.ScriptContent = stagedScript(testCase.bundle != nil)
			if testCase.bundle != nil {
				request.SecretBundle = testCase.bundle
				request.SecretBundleId = "bundle-1"
			}
			validated := validatedStageRequest(t, request)

			service.mu.Lock()
			decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
			service.mu.Unlock()
			if decision.kind != assignmentStagePrepare || decision.plan == nil {
				t.Fatalf("staging decision = %+v", decision)
			}
			prepared, err := service.prepareAssignmentStage(context.Background(), decision.plan)
			if err != nil {
				service.mu.Lock()
				service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
				service.mu.Unlock()
				t.Fatalf("prepareAssignmentStage: %v", err)
			}
			defer func() {
				service.mu.Lock()
				service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
				service.mu.Unlock()
				service.cleanupPreparedAssignmentStage(decision.plan, prepared)
			}()

			config := prepared.execution.redactionConfig
			// Premise: the script-sourced half really was registered. Without it the
			// origin assertion below would hold vacuously.
			if evidence := config.ScrubText("pin=" + retainedCredential); strings.Contains(evidence, retainedCredential) {
				t.Fatalf("premise failed: the retained import credential was never registered: %q", evidence)
			}
			if got := config.HasRuntimeSecretMaterial(); got != testCase.platformManaged {
				t.Fatalf("HasRuntimeSecretMaterial() = %v, want %v: staging registered values under the wrong origin",
					got, testCase.platformManaged)
			}
		})
	}
}

// The setting rides on scriptContent, so two otherwise identical assignments that
// disagree about it must not share an assignment fingerprint — otherwise a
// generator could keep serving a cached script whose run-output policy changed.
func TestAssignmentFingerprintTracksRunEvidenceSetting(t *testing.T) {
	scriptWith := func(redactRunEvidence bool) []byte {
		payload, err := json.Marshal(map[string]any{
			"name":     "stage",
			"protocol": "http",
			"config": map[string]any{
				"base_url": "https://example.test",
				"log": map[string]any{
					"level":               "standard",
					"redact_run_evidence": redactRunEvidence,
				},
			},
			"actions": []any{map[string]any{
				"name": "request", "type": "request", "target": "/",
			}},
		})
		if err != nil {
			t.Fatalf("marshal script: %v", err)
		}
		return payload
	}
	fingerprint := func(content []byte) [sha256.Size]byte {
		request := stageRequest("run", "script", 1)
		request.ScriptContent = content
		return workAssignmentFingerprint(request)
	}
	if on, off := fingerprint(scriptWith(true)), fingerprint(scriptWith(false)); on == off {
		t.Fatalf("assignment fingerprint %x is identical across run-evidence settings", on)
	}
}

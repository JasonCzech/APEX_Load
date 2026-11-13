package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		{name: "unset_defaults_to_visible", redactRunEvidence: nil, redacted: false},
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
				// Explicitly enable script-sourced redaction so this test can
				// verify that its origin stays separate from platform material.
				"log": map[string]any{"level": "standard", "redact_run_evidence": true},
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

func TestValidateWorkAssignmentAndStagingAllowLiteralDSLSecretsAndJavaVMPropertiesWithoutSecretDeliveryTransport(t *testing.T) {
	const (
		javaVMSecret = "synth-java-vm-pwd-9601"
		headerSecret = "synth-http-bearer-9602"
		bundleSecret = "synth-bundle-secret-9603"
	)

	javaPayload, err := json.Marshal(map[string]any{
		"name":     "java-literal-vm",
		"protocol": "http",
		"config": map[string]any{
			"base_url": "https://example.test",
			"java_vuser": map[string]any{
				"additional_vm_parameters": []string{"-Dapp.password=" + javaVMSecret},
			},
		},
		"actions": []any{map[string]any{
			"name":   "req",
			"type":   "request",
			"method": "GET",
			"target": "/orders",
			"headers": map[string]string{
				"Authorization": "Bearer " + headerSecret,
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal java/http script: %v", err)
	}

	// Guard 3 (#14): AllowsSecretDelivery()==false must succeed when len(secretBundle)==0
	// even if the script carries literal DSL credentials.
	reqNoBundle := stageRequest("run-no-bundle", "script-lit", 1)
	reqNoBundle.ScriptContent = javaPayload
	validated, err := validateWorkAssignment(reqNoBundle, 1<<20, false)
	if err != nil {
		t.Fatalf("validateWorkAssignment rejected literal-only script when allowSecretDelivery=false: %v", err)
	}

	// Conversely, len(secretBundle)>0 still requires allowSecretDelivery=true.
	reqWithBundle := stageRequest("run-with-bundle", "script-bundle", 1)
	reqWithBundle.ScriptContent = javaPayload
	reqWithBundle.SecretBundle = map[string]string{"PLATFORM_TOKEN": bundleSecret}
	reqWithBundle.SecretBundleId = "bundle-9603"
	if _, err := validateWorkAssignment(reqWithBundle, 1<<20, false); err == nil {
		t.Fatalf("validateWorkAssignment accepted non-empty SecretBundle when allowSecretDelivery=false")
	}

	service := newIsolatedService(t, 10, WithExecution())
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	service.mu.Unlock()
	if decision.kind != assignmentStagePrepare || decision.plan == nil {
		t.Fatalf("staging decision = %+v", decision)
	}
	prepared, err := service.prepareAssignmentStage(context.Background(), decision.plan)
	if err != nil {
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked("run-no-bundle", decision.plan.token)
		service.mu.Unlock()
		t.Fatalf("prepareAssignmentStage rejected literal Java VM property / header credential: %v", err)
	}
	defer func() {
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked("run-no-bundle", decision.plan.token)
		service.mu.Unlock()
		service.cleanupPreparedAssignmentStage(decision.plan, prepared)
	}()

	cfg := prepared.execution.redactionConfig
	if cfg.HasRuntimeSecretMaterial() {
		t.Fatalf("HasRuntimeSecretMaterial() = true for literal-only staged assignment")
	}
	// redact_run_evidence is unset, so the script's own literals stay visible.
	for _, secret := range []string{javaVMSecret, headerSecret} {
		if got := cfg.ScrubText("value=" + secret); !strings.Contains(got, secret) {
			t.Fatalf("staged RedactionConfig scrubbed literal %q with redact_run_evidence unset: %q", secret, got)
		}
	}

	// Finding 1 regression: when a bundle secret backs HTTP Basic auth, the
	// staged groupExecution.redactionConfig shares MultiGroupEngine's auth
	// composites and dynamicSecretRegistry.
	basicPayload, err := json.Marshal(map[string]any{
		"name":     "bundle-basic-composite",
		"protocol": "http",
		"config": map[string]any{
			"base_url": "https://example.test",
			"http": map[string]any{
				"auth": map[string]any{
					"type":     "basic",
					"username": "svc-operator",
					"password": "{{secret.PLATFORM_TOKEN}}",
				},
			},
		},
		"actions": []any{map[string]any{
			"name": "req", "type": "request", "method": "GET", "target": "/orders",
		}},
	})
	if err != nil {
		t.Fatalf("marshal basic script: %v", err)
	}
	reqComposite := stageRequest("run-composite", "script-composite", 1)
	reqComposite.ScriptContent = basicPayload
	reqComposite.SecretBundle = map[string]string{"PLATFORM_TOKEN": bundleSecret}
	reqComposite.SecretBundleId = "bundle-9604"
	valComposite, err := validateWorkAssignment(reqComposite, 1<<20, true)
	if err != nil {
		t.Fatalf("validateWorkAssignment composite: %v", err)
	}
	service.mu.Lock()
	decComposite := service.lifecycleCoordinator.reserveStageLocked(service, valComposite)
	service.mu.Unlock()
	prepComposite, err := service.prepareAssignmentStage(context.Background(), decComposite.plan)
	if err != nil {
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked("run-composite", decComposite.plan.token)
		service.mu.Unlock()
		t.Fatalf("prepareAssignmentStage composite: %v", err)
	}
	defer func() {
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked("run-composite", decComposite.plan.token)
		service.mu.Unlock()
		service.cleanupPreparedAssignmentStage(decComposite.plan, prepComposite)
	}()

	basicWire := base64.StdEncoding.EncodeToString([]byte("svc-operator:" + bundleSecret))
	compCfg := prepComposite.execution.redactionConfig
	if got := compCfg.ScrubText("Authorization: Basic " + basicWire); strings.Contains(got, basicWire) {
		t.Fatalf("staged groupExecution.redactionConfig missed HTTP Basic composite: %q", got)
	}
	const dynToken = "synth-staged-dyn-token-9605"
	prepComposite.execution.engine.GroupRedactionConfig(0).RegisterDynamicSecrets("oauth:http", []string{dynToken})
	if got := compCfg.ScrubText("Bearer " + dynToken); strings.Contains(got, dynToken) {
		t.Fatalf("staged groupExecution.redactionConfig disconnected from engine dynamicSecretRegistry: %q", got)
	}

	// Finding 6 regression: prepareAssignmentStage fails closed with InvalidArgument
	// when ImportedCredentialValues encounters an unsupported credential locator field.
	badLocatorPayload, err := json.Marshal(map[string]any{
		"name":     "bad-locator-stage",
		"protocol": "http",
		"config":   map[string]any{"base_url": "https://example.test"},
		"actions": []any{map[string]any{
			"name":   "req",
			"type":   "request",
			"method": "GET",
			"target": "/orders",
			"metadata": map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "unsupported_future_secret_surface"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal bad-locator script: %v", err)
	}
	reqBadLocator := stageRequest("run-bad-locator", "script-bad-locator", 1)
	reqBadLocator.ScriptContent = badLocatorPayload
	valBadLocator := validatedStageRequest(t, reqBadLocator)
	service.mu.Lock()
	decBadLocator := service.lifecycleCoordinator.reserveStageLocked(service, valBadLocator)
	service.mu.Unlock()
	if _, stageErr := service.prepareAssignmentStage(context.Background(), decBadLocator.plan); status.Code(stageErr) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument on unsupported credential locator in prepareAssignmentStage, got %v", stageErr)
	}
	service.mu.Lock()
	service.lifecycleCoordinator.rollbackStageLocked("run-bad-locator", decBadLocator.plan.token)
	service.mu.Unlock()

	// Round-4 Finding 4a regression: prepareAssignmentStage scrubs literal credentials
	// embedded in validation issues (e.g. actions[0].Name) before returning
	// InvalidArgument — only when the script sets redact_run_evidence; with it
	// off the operator's own literal stays readable in the error.
	for _, mode := range []struct {
		name   string
		redact bool
	}{{name: "on", redact: true}, {name: "off", redact: false}} {
		badValidationPayload, err := json.Marshal(map[string]any{
			"name":     "bad-validation-stage",
			"protocol": "http",
			"config": map[string]any{
				"base_url": "https://example.test",
				"log":      map[string]any{"redact_run_evidence": mode.redact},
			},
			"actions": []any{map[string]any{
				"name":    "step-" + headerSecret,
				"type":    "unsupported_action_type",
				"headers": map[string]any{"Authorization": "Bearer " + headerSecret},
			}},
		})
		if err != nil {
			t.Fatalf("marshal bad-validation script: %v", err)
		}
		runID := "run-bad-validation-" + mode.name
		reqBadValidation := stageRequest(runID, "script-bad-validation-"+mode.name, 1)
		reqBadValidation.ScriptContent = badValidationPayload
		valBadValidation := validatedStageRequest(t, reqBadValidation)
		service.mu.Lock()
		decBadValidation := service.lifecycleCoordinator.reserveStageLocked(service, valBadValidation)
		service.mu.Unlock()
		_, valStageErr := service.prepareAssignmentStage(context.Background(), decBadValidation.plan)
		if status.Code(valStageErr) != codes.InvalidArgument {
			t.Fatalf("%s: expected InvalidArgument on invalid script in prepareAssignmentStage, got %v", mode.name, valStageErr)
		}
		if leaked := strings.Contains(valStageErr.Error(), headerSecret); leaked == mode.redact {
			t.Fatalf("%s: validation error literal %q leaked=%t: %v", mode.name, headerSecret, leaked, valStageErr)
		}
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked(runID, decBadValidation.plan.token)
		service.mu.Unlock()
	}

	// Round-8 Finding 2 regression: when script-sourced credentials exceed the
	// runtime matcher value budget (> 101,024 distinct CSV values), distributed
	// staging degrades best-effort without failing the assignment or disturbing
	// platform-managed SecretBundle redaction.
	var csvBuilder strings.Builder
	csvBuilder.WriteString("password\n")
	for i := 0; i < 101_030; i++ {
		csvBuilder.WriteString("v")
		csvBuilder.WriteString(strconv.Itoa(i))
		csvBuilder.WriteByte('\n')
	}
	overflowPayload, err := json.Marshal(map[string]any{
		"name":     "script-sourced-overflow",
		"protocol": "http",
		// Redaction on, so the script-sourced half is actually attempted.
		"config": map[string]any{
			"base_url": "https://example.test",
			"log":      map[string]any{"redact_run_evidence": true},
		},
		"data_sources": []any{map[string]any{
			"name":        "creds",
			"type":        "csv",
			"inline_data": csvBuilder.String(),
		}},
		"actions": []any{map[string]any{
			"name":   "req",
			"type":   "request",
			"method": "GET",
			"target": "/orders",
			"headers": map[string]string{
				"Authorization": "Bearer {{data.creds.password}}",
				"X-Platform":    "{{secret.PLATFORM_TOKEN}}",
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal overflow script: %v", err)
	}
	reqOverflow := stageRequest("run-overflow", "script-overflow", 1)
	reqOverflow.ScriptContent = overflowPayload
	reqOverflow.SecretBundle = map[string]string{"PLATFORM_TOKEN": bundleSecret}
	reqOverflow.SecretBundleId = "bundle-overflow"
	valOverflow, err := validateWorkAssignment(reqOverflow, 4<<20, true)
	if err != nil {
		t.Fatalf("validateWorkAssignment overflow: %v", err)
	}
	service.mu.Lock()
	decOverflow := service.lifecycleCoordinator.reserveStageLocked(service, valOverflow)
	service.mu.Unlock()
	prepOverflow, overflowErr := service.prepareAssignmentStage(context.Background(), decOverflow.plan)
	if overflowErr != nil {
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked("run-overflow", decOverflow.plan.token)
		service.mu.Unlock()
		t.Fatalf("prepareAssignmentStage failed instead of degrading best-effort on script-sourced matcher overflow: %v", overflowErr)
	}
	defer func() {
		service.mu.Lock()
		service.lifecycleCoordinator.rollbackStageLocked("run-overflow", decOverflow.plan.token)
		service.mu.Unlock()
		service.cleanupPreparedAssignmentStage(decOverflow.plan, prepOverflow)
	}()
	overflowCfg := prepOverflow.execution.redactionConfig
	if !overflowCfg.HasRuntimeSecretMaterial() {
		t.Fatalf("expected HasRuntimeSecretMaterial()=true when SecretBundle is present on overflow fallback")
	}
	if got := overflowCfg.ScrubText("platform=" + bundleSecret); strings.Contains(got, bundleSecret) {
		t.Fatalf("platform-managed SecretBundle secret leaked after script-sourced overflow fallback: %q", got)
	}
}

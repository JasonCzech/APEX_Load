package api

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// gateCoverageScript builds a script whose single action carries the supplied
// gate metadata. The action type follows the protocol so the draft is
// schema-valid and the test measures gating rather than validation.
func gateCoverageScript(protocol string, metadata map[string]any) *dsl.Script {
	action := dsl.Action{
		Name:     "request",
		Type:     "GET",
		Method:   "GET",
		Target:   "/orders",
		Metadata: metadata,
	}
	if protocol == "browser" {
		action = dsl.Action{
			Name:     "navigate",
			Type:     "browser_navigate",
			Target:   "https://api.example.test/orders",
			Metadata: metadata,
		}
	}
	return &dsl.Script{
		Name:     "gate coverage",
		Protocol: protocol,
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:  []dsl.Action{action},
	}
}

// TestReviewGateDraftSavesButCannotReplayOrLaunch parameterizes the invariant
// that previously held in exactly one test for one gate kind on one surface.
//
// The contract an importer relies on is three-part and each part is a different
// code path: a gated script must still SAVE (so an operator can review it),
// must not REPLAY, and must not LAUNCH. Asserting them together, per gate kind,
// is what stops a future change from making a gate saveable-and-runnable or
// unsaveable-and-unreviewable.
//
// The final row is a negative: a non-browser script carrying plain
// review_required is deliberately NOT gated today. trueclient, enterprisearchive
// and recorderjson all emit that marker, and gating it would retroactively block
// their existing imports. Pinning it here makes any future widening a conscious
// decision rather than a side effect.
func TestReviewGateDraftSavesButCannotReplayOrLaunch(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		metadata map[string]any
		gated    bool
	}{
		{
			name:     "generic execution blocker on http",
			protocol: "http",
			metadata: map[string]any{"execution_blocker": "import_missing_variables"},
			gated:    true,
		},
		{
			name:     "generic execution blockers list on http",
			protocol: "http",
			metadata: map[string]any{"execution_blockers": []any{"import_missing_variables"}},
			gated:    true,
		},
		{
			name:     "bruno javascript review gate on http",
			protocol: "http",
			metadata: map[string]any{"review_gate": "bruno_javascript"},
			gated:    true,
		},
		{
			name:     "bruno native profile review required on http",
			protocol: "http",
			metadata: map[string]any{"source_profile": "bruno_native", "review_required": true},
			gated:    true,
		},
		{
			name:     "execution blocker on browser",
			protocol: "browser",
			metadata: map[string]any{"execution_blocker": "import_missing_variables"},
			gated:    true,
		},
		{
			name:     "plain review_required on non-browser is not gated",
			protocol: "http",
			metadata: map[string]any{"review_required": true},
			gated:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := gateCoverageScript(test.protocol, test.metadata)

			// (1) The draft must always remain saveable: gate metadata is review
			// state, not a schema error.
			if issues := validateScript(script); len(issues) != 0 {
				t.Fatalf("gated draft failed plain validation and could not be saved: %v", issues)
			}

			replayIssues := replayReviewGateIssues(script)
			launchIssues := launchReviewGateIssues(script)
			storedErr := storedScriptReplayGateError(script)

			if !test.gated {
				if len(replayIssues) != 0 || len(launchIssues) != 0 || storedErr != nil {
					t.Fatalf("ungated metadata was blocked: replay=%v launch=%v stored=%v",
						replayIssues, launchIssues, storedErr)
				}
				return
			}

			// (2) Replay must be blocked, at both the definition gate and the
			// stored-script gate that runs before a caller-supplied draft can
			// replace the stored one.
			if len(replayIssues) == 0 {
				t.Fatal("gated script was allowed to replay")
			}
			if storedErr == nil {
				t.Fatal("gated stored script accepted a draft replacement")
			}
			// (3) Launch must be blocked, so the script cannot be pulled into a
			// managed test group or started.
			if len(launchIssues) == 0 {
				t.Fatal("gated script was allowed to launch")
			}
		})
	}
}

// TestReplayManagersEnforceGateAndReleaseExecutionSlot covers the two
// enforcement points that are only reachable through their HTTP handlers today.
//
// Both managers gate at entry and must release the caller's execution context
// on the reject path. A gate that returned early without releasing would leak a
// concurrency slot on every blocked replay — a leak no handler-level test can
// observe, because the handler never sees the release callback.
func TestReplayManagersEnforceGateAndReleaseExecutionSlot(t *testing.T) {
	gated := gateCoverageScript("browser", map[string]any{
		"execution_blocker": "import_missing_variables",
	})

	t.Run("browser replay manager", func(t *testing.T) {
		released, reserved := 0, 0
		manager := NewBrowserReplayManager(nil)
		t.Cleanup(manager.Close)
		manager.setSessionIDReserver(func(context.Context, string, string) (string, func(), *replaySessionLeaseSignal, error) {
			reserved++
			return "", nil, nil, errors.New("reservation must not run for a gated script")
		})
		session, err := manager.Start("script-1", gated, BrowserReplayStartRequest{
			releaseExecutionContext: func() { released++ },
		})
		if !errors.Is(err, errScriptReplayBlocked) {
			t.Fatalf("gated script did not return its review refusal: %v", err)
		}
		if session != nil {
			t.Fatalf("gated start returned a session: %+v", session)
		}
		if released != 1 || reserved != 0 {
			t.Fatalf("gated start released execution %d times and reserved %d sessions", released, reserved)
		}
	})

	t.Run("script debug manager", func(t *testing.T) {
		released := false
		manager := &ScriptDebugManager{}
		session, err := manager.StartChecked("script-1", gated, ScriptDebugStartRequest{
			releaseExecutionContext: func() { released = true },
		})
		if err == nil {
			t.Fatal("gated script started a debug session")
		}
		if session != nil {
			t.Fatalf("gated start returned a session: %+v", session)
		}
		if !released {
			t.Fatal("gated start leaked the caller's execution slot")
		}
	})
}

// TestDurableReviewGateRegistryScope pins which importers get a durable,
// policy-unoverridable gate — and, more importantly, which do not.
//
// The registry replaced a hard-coded strings.HasPrefix(gate, "bruno_"). The
// obvious "cleanup" is to treat any non-empty review_gate as durable, and that
// would be a regression: TrueClient writes review_gate="javascript" and every
// converter going through importreview.ApplyToAction writes the first review
// finding's code. Those are review markers an operator can knowingly override
// under config.browser.review_policy. Hardening them would retroactively make
// existing TrueClient and enterprise-archive imports unrunnable with no way to
// clear the block.
func TestDurableReviewGateRegistryScope(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		durable  bool
	}{
		// Registered: Bruno native.
		{
			name:     "bruno gate prefix",
			metadata: map[string]any{"review_gate": "bruno_javascript"},
			durable:  true,
		},
		{
			name:     "bruno auth gate",
			metadata: map[string]any{"review_gate": "bruno_auth"},
			durable:  true,
		},
		{
			name:     "bruno native profile with review_required",
			metadata: map[string]any{"source_profile": "bruno_native", "review_required": true},
			durable:  true,
		},
		{
			name:     "bruno native profile with promotion_blocker",
			metadata: map[string]any{"source_profile": "bruno_native", "promotion_blocker": true},
			durable:  true,
		},

		// Not registered: these must stay policy-overridable.
		{
			name:     "trueclient javascript gate",
			metadata: map[string]any{"review_gate": "javascript"},
		},
		{
			name:     "importreview finding code as gate",
			metadata: map[string]any{"review_gate": "import_review_required"},
		},
		{
			name:     "credential redaction gate",
			metadata: map[string]any{"review_gate": "credential_parameter_source_redacted"},
		},
		{
			name:     "plain review_required with no profile",
			metadata: map[string]any{"review_required": true},
		},
		{
			name:     "unregistered profile with review_required",
			metadata: map[string]any{"source_profile": "postman", "review_required": true},
		},
		{
			name:     "bruno native profile without any review flag",
			metadata: map[string]any{"source_profile": "bruno_native"},
		},
		{name: "no metadata"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := dsl.Action{Name: "request", Type: "GET", Metadata: test.metadata}
			if got := durableImportReviewGateAction(action); got != test.durable {
				t.Fatalf("durableImportReviewGateAction = %v, want %v", got, test.durable)
			}
		})
	}
}

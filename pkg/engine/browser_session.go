package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// A browser script with config.browser.session gives each observer its own
// identity for its whole life: the observer whose logical ordinal is n (the
// same dense ordinal unique parameters use, disjoint across generators) is
// identity n. Its storage state seeds a storage_state_pool session; its
// username and password secret spell {{identity.username}} and
// {{identity.password}} in its copy of the script.

// browserSessionStorageStatesKey carries every identity's resolved storage
// state in the shared protocol config, so redaction knows them all; each
// observer's own config keeps only its identity's.
const browserSessionStorageStatesKey = "browser_session_storage_states"

// browserIdentityBinding is an observer's identity, and whether its
// iteration signs in itself (dsl.BrowserSessionSignsInPerIteration).
type browserIdentityBinding struct {
	ordinal             int
	identity            dsl.BrowserIdentity
	signsInPerIteration bool
}

// browserIdentityForOrdinal is the identity the observer with the given
// logical ordinal runs as, or nil for a script without session identities.
func browserIdentityForOrdinal(script *dsl.Script, ordinal int) (*browserIdentityBinding, error) {
	identities := dsl.BrowserSessionIdentities(script)
	if len(identities) == 0 {
		return nil, nil
	}
	if ordinal < 0 || ordinal >= len(identities) {
		return nil, fmt.Errorf("observer %d has no identity: config.browser.session lists %d, one per observer", ordinal+1, len(identities))
	}
	return &browserIdentityBinding{ordinal: ordinal, identity: identities[ordinal], signsInPerIteration: dsl.BrowserSessionSignsInPerIteration(script)}, nil
}

// checkBrowserSessionPopulation fails a group that would run more observers
// than its script lists identities for.
func checkBrowserSessionPopulation(script *dsl.Script, population int) error {
	identities := dsl.BrowserSessionIdentities(script)
	if len(identities) == 0 || population <= len(identities) {
		return nil
	}
	return fmt.Errorf("%d observers need %d identities, but config.browser.session lists %d: each observer runs as its own user", population, population, len(identities))
}

// withBrowserIdentityConfig is an observer's protocol config: its identity
// names it in evidence and, for a storage_state_pool session, its storage
// state seeds the browser.
func withBrowserIdentityConfig(base map[string]any, binding *browserIdentityBinding, script *dsl.Script) (map[string]any, error) {
	states, hasStates := base[browserSessionStorageStatesKey].([]string)
	if binding == nil && !hasStates {
		return base, nil
	}
	cfg := make(map[string]any, len(base)+3)
	for key, value := range base {
		if key != browserSessionStorageStatesKey {
			cfg[key] = value
		}
	}
	if binding == nil {
		return cfg, nil
	}
	cfg["browser_identity_index"] = float64(binding.ordinal + 1)
	cfg["browser_identity_username"] = binding.identity.Username
	if session := script.Config.Browser.Session; session != nil && session.Strategy == dsl.BrowserSessionStorageStatePool {
		if binding.ordinal >= len(states) {
			return nil, fmt.Errorf("observer %d (%s): its storage state is unavailable", binding.ordinal+1, binding.identity.Username)
		}
		state := states[binding.ordinal]
		if state == "" || containsPlatformSecretReference(state) {
			return nil, fmt.Errorf("observer %d (%s): configure the project secret its storage_state names", binding.ordinal+1, binding.identity.Username)
		}
		cfg["browser_storage_state"] = state
	}
	return cfg, nil
}

// scriptForBrowserIdentity is the observer's copy of the script, with
// {{identity.*}} spelled for its identity.
func scriptForBrowserIdentity(script *dsl.Script, binding *browserIdentityBinding) (*dsl.Script, error) {
	if binding == nil {
		return script, nil
	}
	return script.WithBrowserIdentity(binding.identity)
}

// sessionLostPrefix starts the error of an iteration whose observer lost its
// session (ErrorClassSessionLost).
const sessionLostPrefix = "session_lost:"

// browserSessionStart is when an observer may start: its logical ordinal's
// share of the session's start gap after the run started.
func browserSessionStart(runStarted time.Time, binding *browserIdentityBinding, script *dsl.Script) time.Time {
	if binding == nil || runStarted.IsZero() || script == nil || script.Config.Browser.Session == nil || script.Config.Browser.Session.StartGapMs <= 0 {
		return time.Time{}
	}
	return runStarted.Add(time.Duration(binding.ordinal) * time.Duration(script.Config.Browser.Session.StartGapMs) * time.Millisecond)
}

// waitForSessionStart holds an observer until its start, reporting false when
// it was stopped first.
func (vu *VUser) waitForSessionStart(ctx context.Context) bool {
	wait := time.Until(vu.sessionStartNotBefore)
	if vu.sessionStartNotBefore.IsZero() || wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// markBrowserSessionLost fails an iteration step whose observer was sent to
// sign in, or whose Aura session was rejected, as session_lost, and ends the
// iteration whatever its continue-on-error setting: its later steps would
// only measure the sign-in page. An iteration that signs in itself reaches
// the sign-in page on purpose. A salesforce_jwt observer signs in again at
// its next iteration: a new browser context mints a new session, which a
// storage_state_pool observer's exported state cannot.
func (vu *VUser) markBrowserSessionLost(result *protocol.Result) {
	if vu.browserIdentity == nil || result == nil {
		return
	}
	reason := browserSessionLossReason(result, vu.browserIdentity.signsInPerIteration)
	if reason == "" {
		return
	}
	result.Success = false
	result.ErrorMessage = fmt.Sprintf("%s observer %d (%s) %s", sessionLostPrefix, vu.browserIdentity.ordinal+1, vu.browserIdentity.identity.Username, reason)
	result.LifecycleControl = &protocol.LifecycleControl{EndIteration: true, Failed: true}
	if vu.Script != nil && vu.Script.Config.Browser.Session != nil && vu.Script.Config.Browser.Session.Strategy == dsl.BrowserSessionSalesforceJWT {
		vu.browserSessionRenewal = true
	}
}

// takeBrowserSessionRenewal reports, once, that the observer's lost
// salesforce_jwt session needs a new browser context.
func (vu *VUser) takeBrowserSessionRenewal() bool {
	renew := vu.browserSessionRenewal
	vu.browserSessionRenewal = false
	return renew
}

func browserSessionLossReason(result *protocol.Result, signsInPerIteration bool) string {
	if !signsInPerIteration && browserprotocol.SalesforceSignInURL(result.PageURL) {
		return "was sent to sign in"
	}
	evidence, _ := result.ProtocolEvidence["aura_actions"].(map[string]any)
	actions, _ := evidence["actions"].([]any)
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		if state, _ := action["state"].(string); state == string(aura.InvalidSession) {
			return "had its Aura session rejected (invalid_session)"
		}
	}
	return ""
}

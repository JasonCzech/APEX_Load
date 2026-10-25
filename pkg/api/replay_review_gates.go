package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"encoding/json"
	"errors"

	"net/http"

	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func validateScriptForReplay(script *dsl.Script) []string {
	return validateScriptForReplayWithImportTrust(script, false)
}

// validateScriptForReplayWithImportTrust permits credential-bearing Java VM
// arguments only when the caller has already established that script is the
// unchanged server-owned durable snapshot produced by an import. Client-sent
// draft metadata is not provenance and must stay on the strict path.
func validateScriptForReplayWithImportTrust(
	script *dsl.Script,
	trustedPreservedImport bool,
) []string {
	issues := dsl.ValidateScriptIssues(script)
	if trustedPreservedImport {
		issues = validateStoredExecutionScript(script)
	}
	return appendUniqueStrings(issues, scriptReviewGateIssues(script, "replay")...)
}

// transientStaticIssues are a debug or replay script's static findings,
// judged on the copy the session executes. That copy masks a sensitive JVM
// argument with a sentinel, which would read as a different finding than every
// other entry point reports (must_start_with_dash, not sensitive_property), so
// an untrusted script's submitted JVM arguments are judged in its place; every
// other masked value is judged as executed (a masked recorded resource URL
// cannot be executed). A trusted import snapshot is executed unmasked.
func (s *Server) transientStaticIssues(submitted, prepared *dsl.Script, trusted bool) []string {
	if trusted || submitted == nil || prepared == nil {
		return validateScriptForReplayWithImportTrust(prepared, trusted)
	}
	judged := cloneScriptDefinition(prepared)
	if judged == nil {
		return validateScriptForReplayWithImportTrust(prepared, false)
	}
	judged.Config.JavaVuser.AdditionalVMParameters = append([]string(nil), submitted.Config.JavaVuser.AdditionalVMParameters...)
	return validateScriptForReplayWithImportTrust(judged, false)
}

var replayReviewGateIssues = importreview.ReplayReviewGateIssues

// errScriptReplayBlocked marks a replay a script's own review gates refuse:
// the caller must resolve them, so it is not a server failure.
var errScriptReplayBlocked = importreview.ErrScriptReplayBlocked

// scriptReplayBlockedError is errScriptReplayBlocked with the gates that
// refused the replay, so a response can list them instead of one long sentence.
type scriptReplayBlockedError = importreview.ScriptReplayBlockedError

var newScriptReplayBlockedError = importreview.NewScriptReplayBlockedError

// writeScriptReplayBlocked answers a replay its review gates refused: 409 with
// the message older clients show plus each gate in issues. It reports whether
// err was such a refusal.
func writeScriptReplayBlocked(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, errScriptReplayBlocked) {
		return false
	}
	body := map[string]any{"error": err.Error(), "code": "script_replay_blocked"}
	var blocked *scriptReplayBlockedError
	if errors.As(err, &blocked) {
		body["issues"] = blocked.Issues()
	}
	writeJSON(w, http.StatusConflict, body)
	return true
}

// writeStoredScriptReplayGate answers a stored-script start that the saved
// copy's own review gates refuse (storedScriptReplayGateError). It keeps the
// 400 and the message older clients show, and adds each gate plus the saved
// copy's replay preflight, so an editor can list what to resolve instead of one
// long sentence. gate_scope says the gates describe the saved script, not the
// draft the request carried. Any other error is answered as before.
//
// The gates and the preflight are worded from storedReplayGateDisplayScript,
// so each step name they quote is scrubbed with the message's policy.
func (s *Server) writeStoredScriptReplayGate(w http.ResponseWriter, stored *dsl.Script, err error) {
	var blocked *scriptReplayBlockedError
	if !errors.As(err, &blocked) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	display, ok := storedReplayGateDisplayScript(stored)
	var shown *scriptReplayBlockedError
	if !ok || !errors.As(storedScriptReplayGateError(display), &shown) {
		// Names decide no gate, so this is unreachable; the plain message is
		// still never sent with unscrubbed gates.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{
		// Scrubbed exactly as writeError sent it, so the text is unchanged.
		"error":      scrub.Value(err.Error(), scrub.Policy{}),
		"code":       "script_replay_blocked",
		"gate_scope": "stored_script",
		"issues":     shown.Issues(),
		"preflight":  buildReplayPreflight(display, nil, nil, nil, s.runner.secretResolverForScript(stored)),
	})
}

// storedReplayGateDisplayScript is a copy of a stored script whose step names
// are scrubbed as writeError scrubs a message. A gate quotes its step's name,
// and a name is never a secret binding, so a script that keeps no credentials
// can still hold one there as recorded. Gates depend on metadata, types and
// configuration, never on names, so the copy reports the same gates in the
// same order, quoting the scrubbed names. Each name is scrubbed alone: the
// whole gate reads to the scrubber as a quoted field label and its value, so
// scrubbing it could redact the blocker codes an editor reads after the name.
func storedReplayGateDisplayScript(stored *dsl.Script) (*dsl.Script, bool) {
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, false
	}
	var display dsl.Script
	if json.Unmarshal(raw, &display) != nil {
		return nil, false
	}
	_ = dslwalk.Walk(&display, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			ref.Action.Name = scrub.Value(ref.Action.Name, scrub.Policy{})
		} else if name, named := ref.Request["name"].(string); named {
			ref.Request["name"] = scrub.Value(name, scrub.Policy{})
		}
		return nil
	})
	return &display, true
}

var replayReviewGateError = importreview.ReplayReviewGateError

// storedScriptReplayGateError protects immutable import gates before a
// stored-script endpoint applies a caller-supplied draft replacement. Historical
// browser review-policy gates remain subject to their existing per-replay
// overrides and are revalidated after those overrides are applied, but durable
// execution_blocker(s) and native Bruno gates may never be removed by replacing
// the request-time draft.
func storedScriptReplayGateError(script *dsl.Script) error {
	if script != nil &&
		strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		issues := scriptReviewGateIssuesWithBrowserPolicy(script, "replay", false)
		if len(issues) == 0 {
			return nil
		}
		return newScriptReplayBlockedError(issues)
	}
	return replayReviewGateError(script)
}

func launchReviewGateIssues(script *dsl.Script) []string {
	return scriptReviewGateIssues(script, "launch")
}

var scriptReviewGateIssues = importreview.ScriptReviewGateIssues

var scriptReviewGateIssuesWithBrowserPolicy = importreview.ScriptReviewGateIssuesWithBrowserPolicy

var reviewGateSurfaceAction = importreview.ReviewGateSurfaceAction

var executionReviewBlockerCodes = importreview.ExecutionReviewBlockerCodes

func scriptHasNativeBrunoReviewGate(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	found := false
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if durableImportReviewGateAction(reviewGateSurfaceAction(ref)) {
			found = true
		}
		return nil
	})
	return found || err != nil
}

var reviewGatedJavaScriptAction = importreview.ReviewGatedJavaScriptAction

var reviewRequiredAction = importreview.ReviewRequiredAction

// durableReviewGateSource declares an importer whose review gates are durable:
// the block survives browser review policy and cannot be waived per replay.
//
// This is a registry rather than a hard-coded prefix so a second importer can
// opt in by adding an entry. It is deliberately NOT "any non-empty
// review_gate": TrueClient writes review_gate="javascript"
// (pkg/convert/trueclient/javascript.go) and converters going through
// importreview.ApplyToAction write the first review finding's code. Those are
// review markers under the browser policy, which an operator can knowingly
// override. Promoting them to permanent blocks would retroactively make
// existing TrueClient and enterprise-archive imports unrunnable.
type durableReviewGateSource = importreview.DurableReviewGateSource

var durableReviewGateSources = importreview.DurableReviewGateSources

// durableImportReviewGateAction reports whether an action carries a gate whose
// block is durable.
//
// The gate itself is the enforcement marker. Requiring its companion flags
// would let an author bypass the blocker by deleting only review_required or
// promotion_blocker while leaving the untranslated source behavior in place.
var durableImportReviewGateAction = importreview.DurableImportReviewGateAction

// metadataBool, metadataString and metadataStringSlice adapt the shared
// dslwalk extractors to this package's comparison semantics: gate names are
// lowercased because they are matched against fixed literals, and blocker lists
// keep source order while dropping duplicates so a review message reads the way
// the importer wrote it.
var metadataBool = importreview.MetadataBool

var metadataString = importreview.MetadataString

// executionBlockerDetail is a blocker code with what it is about, when the
// action says: the variables a Bruno request leaves unset, or the headers an
// import left out. A hold for left-out headers counts them, and says when the
// same hold also covers something else (an HTTP version, multipart fields),
// so the editor does not present the headers as the only fix. The script
// editor shows the names (dashboard/src/components/replayReadinessIssues.ts).
var executionBlockerDetail = importreview.ExecutionBlockerDetail

// importReviewOnlyForHeaders reports whether an import holds the action only
// for headers it left out.
var importReviewOnlyForHeaders = importreview.ImportReviewOnlyForHeaders

// omittedImportHeaderNames lists the headers an import left out of the
// request, from its header fidelity marker. Markers written before headers
// were named hold a placeholder, which names nothing.
var omittedImportHeaderNames = importreview.OmittedImportHeaderNames

var metadataStringSlice = importreview.MetadataStringSlice

var appendUniqueStrings = importreview.AppendUniqueStrings

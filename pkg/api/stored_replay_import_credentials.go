package api

import (
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	convertreview "github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/contentcontract"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

var serverOwnedPreservedImportMetadataKeys = []string{
	dsl.ImportPreservedRuntimeArtifactKey,
	dsl.ImportPreservedCredentialParameterNamesKey,
	dsl.ImportPreservedCredentialTargetsMetadataKey,
	"jsr223_sources",
	"k6_gated_source",
	"postman_event_sources",
	"credential_traversal_source_snapshot_base64",
	"devweb_gated_source",
	dsl.ImportPreservedJMeterJDBCSourcesKey,
	dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey,
	dsl.ImportPreservedIgnoredProxyCredentialsKey,
	dsl.ImportPreservedEnterpriseJavaVMSourceKey,
	dsl.ImportPreservedEnterpriseJavaJDBCSourceKey,
	contentcontract.ImportSourceCommitFenceMetadataKey,
	"recorded_input_secret_columns",
	"recorded_resource_urls",
	"effective_settings",
}

// resolveStoredReplayImportScript decides which executable script a saved-script
// replay, debugger, or browser-replay start actually runs.
//
// Legacy or sentinel-free disclosure projections (such as [REDACTED] or
// correlation {{fn.uuid()}} placeholders) first attempt durable reconciliation
// under the prior-context checks. When the caller submits an unmasked DSL draft
// carrying literal credentials or {{secret.*}} edits without [REDACTED]
// sentinels, and durable reconciliation declines it, it is prepared directly
// with server-owned security metadata and aligned credential locators restored,
// while reporting trusted=false because the draft is no longer an unchanged or
// reconciled server-owned import snapshot.
func resolveStoredReplayImportScript(candidate, stored *dsl.Script, options importreview.PreservedImportEditOptions) (*dsl.Script, bool, error) {
	if candidate == nil {
		return stored, isCredentialPreservingImport(stored), nil
	}
	if !isCredentialPreservingImport(stored) {
		return candidate, false, nil
	}
	if matchesStoredScriptDisclosureProjection(candidate, stored) {
		return stored, true, nil
	}
	restored, err := restoreDurableImportCredentialsForExecution(candidate, stored, options)
	if err == nil {
		return restored, true, nil
	}
	if candidateHasExecutableMaskedSecret(candidate) {
		return candidate, false, err
	}
	unmasked, unmaskedErr := prepareUnmaskedStoredImportCandidateForExecution(candidate, stored)
	if unmaskedErr != nil {
		return candidate, false, unmaskedErr
	}
	return unmasked, false, nil
}

func candidateHasExecutableMaskedSecret(candidate *dsl.Script) bool {
	if candidate == nil {
		return false
	}
	hasRedactedRuntimeData := importreview.ScriptHasRedactedRuntimeCredentialSource(candidate)
	allowRedactedRuntimeData := hasRedactedRuntimeData &&
		importreview.ScriptHasExecutionBlockerCode(candidate, importreview.CredentialParameterSourceRedactedBlocker)
	if hasRedactedRuntimeData && !allowRedactedRuntimeData {
		return true
	}
	raw, err := importreview.ScriptAsJSONValue(candidate)
	if err != nil {
		return false
	}
	return importreview.JsonValueContainsExecutableMaskedSecretWithOptions(raw, "", allowRedactedRuntimeData)
}

func syncServerOwnedPreservedMetadataKeys(candidate, prior *dsl.Script, sourceProfile string) error {
	if candidate == nil || prior == nil {
		return nil
	}
	for _, key := range serverOwnedPreservedImportMetadataKeys {
		var priorValue any
		hasPriorValue := false
		if prior.Metadata != nil && prior.Metadata.Import != nil && prior.Metadata.Import.Preserved != nil {
			priorValue, hasPriorValue = prior.Metadata.Import.Preserved[key]
		}
		if hasPriorValue {
			cloned, err := importreview.CloneImportSecurityMetadata(priorValue)
			if err != nil {
				return err
			}
			importreview.EnsureScriptImportPreserved(candidate, sourceProfile)[key] = cloned
		} else if candidate.Metadata != nil &&
			candidate.Metadata.Import != nil &&
			candidate.Metadata.Import.Preserved != nil {
			delete(candidate.Metadata.Import.Preserved, key)
		}
	}
	return nil
}

func restoreUIDAlignedStepCredentialTargets(candidate, prior *dsl.Script) error {
	const key = dsl.ImportPreservedCredentialTargetsMetadataKey
	const uidKey = dsl.ImportActionUIDMetadataKey
	priorByUID := make(map[string]any)
	duplicatePriorUID := make(map[string]bool)
	if err := dslwalk.Walk(prior, func(ref dslwalk.SurfaceRef) error {
		meta := ref.Metadata()
		uid, ok := meta[uidKey].(string)
		if !ok || !dsl.IsValidImportActionUID(uid) {
			return nil
		}
		if _, seen := priorByUID[uid]; seen {
			duplicatePriorUID[uid] = true
			return nil
		}
		if raw, exists := meta[key]; exists {
			priorByUID[uid] = raw
		} else {
			priorByUID[uid] = nil
		}
		return nil
	}); err != nil {
		return err
	}
	candidateUIDCount := make(map[string]int)
	if err := dslwalk.Walk(candidate, func(ref dslwalk.SurfaceRef) error {
		if uid, ok := ref.Metadata()[uidKey].(string); ok && dsl.IsValidImportActionUID(uid) {
			candidateUIDCount[uid]++
		}
		return nil
	}); err != nil {
		return err
	}
	return dslwalk.Walk(candidate, func(ref dslwalk.SurfaceRef) error {
		meta := ref.Metadata()
		if meta != nil {
			delete(meta, key)
		}
		uid, ok := meta[uidKey].(string)
		if !ok || !dsl.IsValidImportActionUID(uid) || duplicatePriorUID[uid] || candidateUIDCount[uid] != 1 {
			return nil
		}
		raw := priorByUID[uid]
		if raw == nil {
			return nil
		}
		cloned, err := importreview.CloneImportSecurityMetadata(raw)
		if err != nil {
			return err
		}
		if meta == nil {
			meta = make(map[string]any)
		}
		meta[key] = cloned
		ref.SetMetadata(meta)
		return nil
	})
}

func prepareUnmaskedStoredImportCandidateForExecution(candidate, prior *dsl.Script) (*dsl.Script, error) {
	if candidate == nil || prior == nil {
		return nil, fmt.Errorf("credential-preserving replay requires a durable prior script")
	}
	restored := cloneScriptDefinition(candidate)
	if restored == nil {
		return nil, fmt.Errorf("failed to prepare imported script for replay")
	}
	stripImportDraftAttestation(restored)
	if err := validateJavaVMParameterSafety(restored, true); err != nil {
		return nil, err
	}
	sourceProfile := ""
	if prior.Metadata != nil && prior.Metadata.Import != nil {
		sourceProfile = strings.TrimSpace(prior.Metadata.Import.Source)
	}
	ensureCredentialPreservingImportMetadata(restored, sourceProfile)
	if restored.Metadata != nil && restored.Metadata.Import != nil && sourceProfile != "" {
		restored.Metadata.Import.Source = sourceProfile
	}
	if aligned := cloneScriptDefinition(restored); aligned != nil {
		if alignErr := restoreServerOwnedImportSecurityMetadata(aligned, prior); alignErr == nil {
			restored = aligned
		} else if uidErr := restoreUIDAlignedStepCredentialTargets(restored, prior); uidErr != nil {
			return nil, uidErr
		}
	}
	if err := syncServerOwnedPreservedMetadataKeys(restored, prior, sourceProfile); err != nil {
		return nil, err
	}
	if err := sanitizePersistentImportReviewSurfaces(restored); err != nil {
		return nil, err
	}
	return restored, nil
}

// restoreDurableImportCredentialsForExecution reconciles legacy [REDACTED] or
// sentinel-free disclosure projections against the durable prior script.
func restoreDurableImportCredentialsForExecution(candidate, prior *dsl.Script, options importreview.PreservedImportEditOptions) (*dsl.Script, error) {
	if candidate == nil || prior == nil {
		return nil, fmt.Errorf("credential-preserving replay requires a durable prior script")
	}
	restored := cloneScriptDefinition(candidate)
	if restored == nil {
		return nil, fmt.Errorf("failed to prepare imported script for replay")
	}
	// A save attestation is a short-lived capability for one durable write. It
	// is never execution provenance and must not reach a runtime session.
	stripImportDraftAttestation(restored)
	if err := validateJavaVMParameterSafety(restored, true); err != nil {
		return nil, err
	}
	// Adapter provenance is durable server-owned state. An ordinary editor may
	// omit it but cannot relabel the script to a different adapter profile,
	// which would also change source-specific replay-gate interpretation.
	sourceProfile := ""
	if prior.Metadata != nil && prior.Metadata.Import != nil {
		sourceProfile = strings.TrimSpace(prior.Metadata.Import.Source)
	}
	ensureCredentialPreservingImportMetadata(restored, sourceProfile)
	if restored.Metadata != nil && restored.Metadata.Import != nil && sourceProfile != "" {
		restored.Metadata.Import.Source = sourceProfile
	}
	if err := reconcileMaskedScriptSecretsWithTrust(restored, prior, false); err != nil {
		return nil, err
	}
	if err := restoreServerOwnedImportSecurityMetadata(restored, prior); err != nil {
		return nil, err
	}
	if err := syncServerOwnedPreservedMetadataKeys(restored, prior, sourceProfile); err != nil {
		return nil, err
	}
	if err := sanitizePersistentImportReviewSurfaces(restored); err != nil {
		return nil, err
	}
	if err := importreview.ValidatePreservedImportCredentialEditsWithOptions(restored, prior, options); err != nil {
		return nil, err
	}
	if err := validateReconciledReplayImportEdits(restored, prior, options); err != nil {
		return nil, err
	}
	return restored, nil
}

var replayReviewEvidencePreservedPaths = func() map[string]struct{} {
	keys := []string{
		"source_review",
		"review_artifact",
		"trueclient_archive_inspection",
		"project_settings",
		"salesforce_review",
		"mulesoft_review",
		"agentforce_review",
		"warnings",
		"fidelity_report",
		"replay_preflight",
	}
	paths := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		paths[importreview.AppendImportContextPath("", "metadata", "import", "preserved", key)] = struct{}{}
	}
	return paths
}()

var replayStepReviewMetadataKeys = map[string]struct{}{
	"review_gate":        {},
	"review_reasons":     {},
	"execution_blocker":  {},
	"execution_blockers": {},
	"advisory_callouts":  {},
	"import_review":      {},
	"review_required":    {},
	"promotion_blocker":  {},
	"migration_change":   {},
}

var replayBrowserInertStepTypes = map[string]struct{}{
	"browser_wait":          {},
	"browser_wait_response": {},
	"browser_ws_wait":       {},
	"browser_ws_assert":     {},
	"browser_screenshot":    {},
	"source_transaction":    {},
	"source_timer":          {},
	"source_message":        {},
	"error_message":         {},
	"rendezvous":            {},
}

var replayBrowserDroppableStepTypes = map[string]struct{}{
	"browser_hover":  {},
	"browser_scroll": {},
	"browser_fill":   {},
}

// validateReconciledReplayImportEdits enforces that durable reconciliation only
// restores masked or projected credentials when no new executable credentials
// were injected alongside them, no kept credential was quoted in plain text,
// and no transport, step target, interpolated reference, or browser page flow
// changed around a kept credential. Unmasked drafts (where the caller already
// holds full literal values without [REDACTED] sentinels) decline trusted
// reconciliation here and proceed via prepareUnmaskedStoredImportCandidateForExecution
// with trusted=false.
func validateReconciledReplayImportEdits(
	candidate, prior *dsl.Script,
	options importreview.PreservedImportEditOptions,
) error {
	if candidate == nil || prior == nil {
		return fmt.Errorf("credential-preserving replay requires a durable prior script")
	}
	candidateClone := cloneScriptDefinition(candidate)
	priorClone := cloneScriptDefinition(prior)
	if candidateClone == nil || priorClone == nil {
		return fmt.Errorf("failed to validate credential-preserving replay script")
	}
	for _, script := range []*dsl.Script{candidateClone, priorClone} {
		if script.Metadata != nil && script.Metadata.Import != nil {
			delete(script.Metadata.Import.Preserved, "recorder_session_id")
			delete(script.Metadata.Import.Preserved, contentcontract.ImportSourceCommitFenceMetadataKey)
			delete(script.Metadata.Import.Preserved, "effective_settings")
		}
	}
	projectedCandidate := maskScriptSecrets(candidateClone)
	projectedPrior := maskScriptSecrets(priorClone)
	if projectedCandidate == nil || projectedPrior == nil {
		return fmt.Errorf("failed to validate credential-preserving replay script")
	}
	rawCandidate, err := importreview.ScriptAsJSONValue(candidateClone)
	if err != nil {
		return err
	}
	safeCandidate, err := importreview.ScriptAsJSONValue(projectedCandidate)
	if err != nil {
		return err
	}
	rawPrior, err := importreview.ScriptAsJSONValue(priorClone)
	if err != nil {
		return err
	}
	safePrior, err := importreview.ScriptAsJSONValue(projectedPrior)
	if err != nil {
		return err
	}
	if importreview.HasNewProjectedCredential(rawCandidate, safeCandidate, rawPrior, safePrior) {
		return fmt.Errorf("script introduces new executable credentials; bind a runtime secret or run the source import again")
	}
	if disclosed := replayRetainedCredentialDisclosedAtPath(rawCandidate, safeCandidate, rawPrior, safePrior, ""); disclosed != "" {
		return fmt.Errorf(
			"edit would unmask the imported credential kept at %s (redacted credentials cannot have their secret mark cleared)",
			importreview.ImportContextDisplayPath(disclosed),
		)
	}
	if quoted := replayKeptValueQuotedInPlainText(safeCandidate, rawPrior, safePrior); quoted != "" {
		return fmt.Errorf(
			"edit would disclose an imported credential quoted at %s in plain text (redacted credentials cannot be unmasked in sibling fields)",
			importreview.ImportContextDisplayPath(quoted),
		)
	}
	if changedAt, changed := replayRetainedCredentialContextChanged(
		candidateClone,
		priorClone,
		rawCandidate,
		safeCandidate,
		rawPrior,
		safePrior,
		options,
	); changed {
		return fmt.Errorf(
			"script changes the execution context for retained imported credentials at %s (redacted credentials cannot be sent to a modified destination)",
			changedAt,
		)
	}
	return nil
}

func replayRetainedCredentialDisclosedAtPath(candidate, projectedCandidate, prior, projectedPrior any, path string) string {
	switch typed := candidate.(type) {
	case map[string]any:
		projectedMap, _ := projectedCandidate.(map[string]any)
		priorMap, _ := prior.(map[string]any)
		projectedPriorMap, _ := projectedPrior.(map[string]any)
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if disclosed := replayRetainedCredentialDisclosedAtPath(
				typed[key],
				projectedMap[key],
				priorMap[key],
				projectedPriorMap[key],
				importreview.AppendImportContextPath(path, key),
			); disclosed != "" {
				return disclosed
			}
		}
		return ""
	case []any:
		projectedSlice, _ := projectedCandidate.([]any)
		priorSlice, _ := prior.([]any)
		projectedPriorSlice, projectedPriorOK := projectedPrior.([]any)
		var actionMatches []int
		hasActionMatches := false
		if projectedPriorOK && importreview.IsActionCollectionContextPath(path) && len(projectedSlice) == len(typed) {
			actionMatches, hasActionMatches = importreview.MatchCredentialPreservingActionCollection(projectedSlice, projectedPriorSlice)
		}
		for index, child := range typed {
			var safeChild, priorChild, safePriorChild any
			if index < len(projectedSlice) {
				safeChild = projectedSlice[index]
			}
			priorIdx := index
			if hasActionMatches {
				priorIdx = actionMatches[index]
			}
			if priorIdx >= 0 && priorIdx < len(priorSlice) {
				priorChild = priorSlice[priorIdx]
			}
			if priorIdx >= 0 && priorIdx < len(projectedPriorSlice) {
				safePriorChild = projectedPriorSlice[priorIdx]
			}
			if disclosed := replayRetainedCredentialDisclosedAtPath(
				child,
				safeChild,
				priorChild,
				safePriorChild,
				importreview.AppendImportContextPath(path, strconv.Itoa(index)),
			); disclosed != "" {
				return disclosed
			}
		}
		return ""
	default:
		if candidate == nil || reflect.DeepEqual(prior, projectedPrior) || !reflect.DeepEqual(candidate, prior) {
			return ""
		}
		if reflect.DeepEqual(projectedCandidate, projectedPrior) || projectedCandidate == importreview.MaskedScriptSecret {
			return ""
		}
		return path
	}
}

func replayKeptValueQuotedInPlainText(safeCandidate, rawPrior, safePrior any) string {
	spans := map[string]struct{}{}
	collectReplayMaskedSpans(rawPrior, safePrior, spans)
	for span := range spans {
		if firstReplayPathQuotingSpan(safePrior, map[string]struct{}{span: {}}, "") != "" {
			delete(spans, span)
		}
	}
	if len(spans) == 0 {
		return ""
	}
	return firstReplayPathQuotingSpan(safeCandidate, spans, "")
}

func collectReplayMaskedSpans(raw, projected any, out map[string]struct{}) {
	const minSpan = 4
	switch typed := raw.(type) {
	case map[string]any:
		projectedMap, _ := projected.(map[string]any)
		for key, child := range typed {
			collectReplayMaskedSpans(child, projectedMap[key], out)
		}
	case []any:
		projectedSlice, _ := projected.([]any)
		for index, child := range typed {
			var projectedChild any
			if index < len(projectedSlice) {
				projectedChild = projectedSlice[index]
			}
			collectReplayMaskedSpans(child, projectedChild, out)
		}
	case string:
		masked, ok := projected.(string)
		if !ok || !strings.Contains(masked, importreview.MaskedScriptSecret) {
			return
		}
		pieces := strings.Split(masked, importreview.MaskedScriptSecret)
		rest := typed
		if !strings.HasPrefix(rest, pieces[0]) {
			return
		}
		rest = rest[len(pieces[0]):]
		for index, piece := range pieces[1:] {
			var span string
			if index == len(pieces)-2 {
				if !strings.HasSuffix(rest, piece) {
					return
				}
				span = rest[:len(rest)-len(piece)]
			} else {
				at := strings.Index(rest, piece)
				if piece == "" || at < 0 {
					return
				}
				span, rest = rest[:at], rest[at+len(piece):]
			}
			if len(span) >= minSpan {
				out[span] = struct{}{}
			}
		}
	}
}

func firstReplayPathQuotingSpan(value any, spans map[string]struct{}, path string) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if found := firstReplayPathQuotingSpan(typed[key], spans, importreview.AppendImportContextPath(path, key)); found != "" {
				return found
			}
		}
	case []any:
		for index, child := range typed {
			if found := firstReplayPathQuotingSpan(child, spans, importreview.AppendImportContextPath(path, strconv.Itoa(index))); found != "" {
				return found
			}
		}
	case string:
		for span := range spans {
			if strings.Contains(typed, span) {
				return path
			}
		}
	}
	return ""
}

func replayRetainedCredentialContextChanged(
	candidateClone,
	priorClone *dsl.Script,
	rawCandidate,
	safeCandidate,
	rawPrior,
	safePrior any,
	options importreview.PreservedImportEditOptions,
) (string, bool) {
	candidateMap, candidateOK := safeCandidate.(map[string]any)
	priorMap, priorOK := safePrior.(map[string]any)
	if !candidateOK || !priorOK {
		return "script", true
	}
	rawCandidateMap, _ := rawCandidate.(map[string]any)
	rawPriorMap, _ := rawPrior.(map[string]any)
	rawActions, _ := importreview.ImportActionContexts(rawCandidateMap)
	rawTransport := importreview.ImportTransportContext(rawCandidateMap)
	candidateActions, candidateOrder := importreview.ImportActionContexts(candidateMap)
	priorActions, priorOrder := importreview.ImportActionContexts(priorMap)
	candidateTransport := importreview.ImportTransportContext(candidateMap)
	priorTransport := importreview.ImportTransportContext(priorMap)
	proxyAuthKept := replayHasRetainedProxyAuthorization(priorMap) || replayHasRetainedProxyAuthorization(candidateMap)
	relaxReplayTransportRoute(candidateTransport, priorTransport, options, proxyAuthKept)
	candidateGlobal := importreview.ImportGlobalCredentialContext(
		candidateMap,
		candidateTransport,
		candidateActions,
		candidateOrder,
	)
	priorGlobal := importreview.ImportGlobalCredentialContext(
		priorMap,
		priorTransport,
		priorActions,
		priorOrder,
	)
	inputsChanged := !reflect.DeepEqual(candidateMap["parameters"], priorMap["parameters"]) ||
		!reflect.DeepEqual(candidateMap["data_sources"], priorMap["data_sources"]) ||
		!reflect.DeepEqual(collectReplayActionExtractors(candidateMap), collectReplayActionExtractors(priorMap))
	varContextChanged := inputsChanged ||
		!replayRunLogicEqual(candidateMap, priorMap) ||
		!reflect.DeepEqual(collectReplayVariableProducerContext(candidateMap, candidateClone), collectReplayVariableProducerContext(priorMap, priorClone))
	transportUsesInputs := replayValueContainsRuntimeInputTemplate(candidateTransport) ||
		replayValueContainsRuntimeInputTemplate(rawTransport)
	transportUsesVars := replayValueContainsRuntimeVarReference(candidateTransport) ||
		replayValueContainsRuntimeVarReference(rawTransport)
	browserScript := importreview.NormalizeScriptFieldName(importreview.StringJSONValue(candidateMap["protocol"])) == "browser"
	seededBrowserSession := replayBrowserSessionSeeds(candidateMap) || replayBrowserSessionSeeds(priorMap)
	browserChecked, browserChanged := false, false

	var visit func(raw, safe, rPrior, sPrior any, path, priorPath, actionPath, priorActionPath string) (string, bool)
	visit = func(raw, safe, rPrior, sPrior any, path, priorPath, actionPath, priorActionPath string) (string, bool) {
		if _, evidence := replayReviewEvidencePreservedPaths[path]; evidence {
			return "", false
		}
		if _, isAction := candidateActions[path]; isAction {
			actionPath = path
			priorActionPath = priorPath
		}
		switch typed := raw.(type) {
		case map[string]any:
			safeMap, _ := safe.(map[string]any)
			priorMapNode, _ := rPrior.(map[string]any)
			safePriorMapNode, _ := sPrior.(map[string]any)
			for key, child := range typed {
				var safeChild, priorChild, safePriorChild any
				if safeMap != nil {
					safeChild = safeMap[key]
				}
				if priorMapNode != nil {
					priorChild = priorMapNode[key]
				}
				if safePriorMapNode != nil {
					safePriorChild = safePriorMapNode[key]
				}
				nextPriorPath := ""
				if path == "" || priorPath != "" {
					nextPriorPath = importreview.AppendImportContextPath(priorPath, key)
				}
				if ctxPath, changed := visit(
					child,
					safeChild,
					priorChild,
					safePriorChild,
					importreview.AppendImportContextPath(path, key),
					nextPriorPath,
					actionPath,
					priorActionPath,
				); changed {
					return ctxPath, true
				}
			}
		case []any:
			safeSlice, _ := safe.([]any)
			priorSlice, _ := rPrior.([]any)
			safePriorSlice, safePriorOK := sPrior.([]any)
			var actionMatches []int
			hasActionMatches := false
			if safePriorOK && importreview.IsActionCollectionContextPath(path) && len(safeSlice) == len(typed) {
				actionMatches, hasActionMatches = importreview.MatchCredentialPreservingActionCollection(safeSlice, safePriorSlice)
			}
			for index, child := range typed {
				var safeChild, priorChild, safePriorChild any
				if index < len(safeSlice) {
					safeChild = safeSlice[index]
				}
				priorIdx := index
				if hasActionMatches {
					priorIdx = actionMatches[index]
				}
				nextPriorPath := ""
				if priorIdx >= 0 && (path == "" || priorPath != "") {
					if priorIdx < len(priorSlice) {
						priorChild = priorSlice[priorIdx]
					}
					if priorIdx < len(safePriorSlice) {
						safePriorChild = safePriorSlice[priorIdx]
					}
					nextPriorPath = importreview.AppendImportContextPath(priorPath, strconv.Itoa(priorIdx))
				}
				if ctxPath, changed := visit(
					child,
					safeChild,
					priorChild,
					safePriorChild,
					importreview.AppendImportContextPath(path, strconv.Itoa(index)),
					nextPriorPath,
					actionPath,
					priorActionPath,
				); changed {
					return ctxPath, true
				}
			}
		default:
			if reflect.DeepEqual(raw, safe) ||
				!reflect.DeepEqual(raw, rPrior) ||
				reflect.DeepEqual(rPrior, sPrior) {
				return "", false
			}
			if action, _ := candidateActions[actionPath].(map[string]any); replayUnsentStepReviewMetadataLeaf(path, actionPath, action, raw) ||
				replayStepDependencyCodeLeaf(path, actionPath, raw) {
				return "", false
			}
			if actionPath == "" {
				if !reflect.DeepEqual(candidateGlobal, priorGlobal) || inputsChanged || varContextChanged {
					return "script", true
				}
				return "", false
			}
			candidateAction, candidateExists := candidateActions[actionPath]
			priorAction, priorExists := priorActions[priorActionPath]
			if !candidateExists || !priorExists ||
				!reflect.DeepEqual(candidateTransport, priorTransport) ||
				!reflect.DeepEqual(candidateAction, priorAction) {
				return importreview.ImportContextDisplayPath(actionPath), true
			}
			rawAction := rawActions[actionPath]
			if inputsChanged && (transportUsesInputs ||
				replayValueContainsRuntimeInputTemplate(candidateAction) ||
				replayValueContainsRuntimeInputTemplate(rawAction)) {
				return importreview.ImportContextDisplayPath(actionPath), true
			}
			if varContextChanged && (transportUsesVars ||
				replayValueContainsRuntimeVarReference(candidateAction) ||
				replayValueContainsRuntimeVarReference(rawAction)) {
				return importreview.ImportContextDisplayPath(actionPath), true
			}
			if browserScript && replayBrowserLeafPinsPageFlow(path, actionPath, candidateAction, seededBrowserSession) {
				if !browserChecked {
					browserChecked = true
					browserChanged = replayBrowserPageFlowChanged(
						candidateClone,
						priorClone,
						rawCandidateMap,
						candidateMap,
						rawPriorMap,
						priorMap,
						seededBrowserSession,
						inputsChanged,
						varContextChanged,
					)
				}
				if browserChanged {
					return importreview.ImportContextDisplayPath(actionPath), true
				}
			}
		}
		return "", false
	}
	return visit(rawCandidate, safeCandidate, rawPrior, safePrior, "", "", "", "")
}

func replayUnsentStepReviewMetadataLeaf(path, actionPath string, action map[string]any, leaf any) bool {
	prefix := actionPath + "/metadata/"
	if actionPath == "" || !strings.HasPrefix(path, prefix) {
		return false
	}
	key, _, nested := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	if _, review := replayStepReviewMetadataKeys[key]; review {
		if nested {
			return true
		}
		return importreview.UnsentStepReviewMetadata(dsl.Action{
			Type:     importreview.StringJSONValue(action["type"]),
			Metadata: map[string]any{key: leaf},
		}, key)
	}
	if importreview.IsHTTPImportProvenanceMetadataKey(key) {
		sent, _ := varflow.MetadataReads(dsl.Action{
			Type:     importreview.StringJSONValue(action["type"]),
			Metadata: map[string]any{key: leaf},
		}, key)
		return !sent
	}
	return false
}

func replayStepDependencyCodeLeaf(path, actionPath string, leaf any) bool {
	prefix := actionPath + "/metadata/"
	if actionPath == "" || !strings.HasPrefix(path, prefix) {
		return false
	}
	if _, review := replayStepReviewMetadataKeys[strings.TrimPrefix(path, prefix)]; !review {
		return false
	}
	code, _ := leaf.(string)
	return convertreview.IsDependencyCode(code)
}

func replayStringHasTemplateNamespace(s string, namespaces ...string) bool {
	rest := s
	for {
		idx := strings.Index(rest, "{{")
		if idx < 0 {
			break
		}
		rest = rest[idx+2:]
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		lower := strings.ToLower(trimmed)
		for _, ns := range namespaces {
			if strings.HasPrefix(lower, ns) {
				return true
			}
		}
	}
	return false
}

func replayStringContainsCELVarOrData(s string) bool {
	return strings.Contains(s, "vars.") || strings.Contains(s, "vars[") ||
		strings.Contains(s, "var.") || strings.Contains(s, "var[") ||
		strings.Contains(s, "data.") || strings.Contains(s, "data[")
}

func replayDecodedBodyVariants(node map[string]any) []string {
	bodyStr, ok := node["body"].(string)
	if !ok || bodyStr == "" {
		return nil
	}
	var out []string
	if bodyEnc := strings.TrimSpace(importreview.StringJSONValue(node["body_encoding"])); bodyEnc != "" {
		if decoded, didDecode, err := dsl.DecodeBodyEncoding(bodyStr, bodyEnc, 0); err == nil && didDecode && len(decoded) > 0 {
			out = append(out, string(decoded))
		}
	}
	if strings.Contains(bodyStr, "%") {
		if unescaped, err := url.QueryUnescape(bodyStr); err == nil && unescaped != bodyStr {
			out = append(out, unescaped)
		}
	}
	return out
}

func replayValueContainsRuntimeInputTemplate(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, decoded := range replayDecodedBodyVariants(typed) {
			if replayValueContainsRuntimeInputTemplate(decoded) {
				return true
			}
		}
		for _, child := range typed {
			if replayValueContainsRuntimeInputTemplate(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if replayValueContainsRuntimeInputTemplate(child) {
				return true
			}
		}
	case string:
		if replayStringHasTemplateNamespace(typed, "param.", "data.", "var.", "env.", "secret.", "file.") {
			return true
		}
		if strings.Contains(typed, "%") {
			if unescaped, err := url.QueryUnescape(typed); err == nil && unescaped != typed &&
				replayStringHasTemplateNamespace(unescaped, "param.", "data.", "var.", "env.", "secret.", "file.") {
				return true
			}
		}
		return replayStringContainsCELVarOrData(typed)
	}
	return false
}

func replayValueContainsRuntimeVarReference(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, decoded := range replayDecodedBodyVariants(typed) {
			if replayValueContainsRuntimeVarReference(decoded) {
				return true
			}
		}
		for _, child := range typed {
			if replayValueContainsRuntimeVarReference(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if replayValueContainsRuntimeVarReference(child) {
				return true
			}
		}
	case string:
		if replayStringHasTemplateNamespace(typed, "var.", "data.") {
			return true
		}
		if strings.Contains(typed, "%") {
			if unescaped, err := url.QueryUnescape(typed); err == nil && unescaped != typed &&
				replayStringHasTemplateNamespace(unescaped, "var.", "data.") {
				return true
			}
		}
		return replayStringContainsCELVarOrData(typed)
	}
	return false
}

func walkReplayScriptActions(script map[string]any, visit func(containerPath string, index int, action map[string]any)) {
	if script == nil {
		return
	}
	var walkSlice func(containerPath string, raw any)
	walkSlice = func(containerPath string, raw any) {
		items, ok := raw.([]any)
		if !ok {
			return
		}
		for idx, item := range items {
			action, _ := item.(map[string]any)
			if action == nil {
				continue
			}
			visit(containerPath, idx, action)
			stepPath := importreview.AppendImportContextPath(containerPath, strconv.Itoa(idx))
			if params, _ := action["params"].(map[string]any); params != nil {
				if reqs, ok := params["requests"].([]any); ok {
					walkSlice(importreview.AppendImportContextPath(stepPath, "params", "requests"), reqs)
				}
				if reqMap, ok := params["request"].(map[string]any); ok && reqMap != nil {
					walkSlice(importreview.AppendImportContextPath(stepPath, "params", "request"), []any{reqMap})
				}
			}
			if ctrl, _ := action["control"].(map[string]any); ctrl != nil {
				for _, branch := range []string{"steps", "else", "default", "actions", "then"} {
					if childSlice, ok := ctrl[branch].([]any); ok {
						walkSlice(importreview.AppendImportContextPath(stepPath, "control", branch), childSlice)
					}
				}
				for _, groupKey := range []string{"cases", "branches"} {
					groups, _ := ctrl[groupKey].([]any)
					for gIdx, gItem := range groups {
						gMap, _ := gItem.(map[string]any)
						if gMap == nil {
							continue
						}
						if childSlice, ok := gMap["steps"].([]any); ok {
							walkSlice(importreview.AppendImportContextPath(stepPath, "control", groupKey, strconv.Itoa(gIdx), "steps"), childSlice)
						}
					}
				}
			}
		}
	}
	for _, phase := range []string{"init", "actions", "teardown"} {
		walkSlice("/"+phase, script[phase])
	}
}

func collectReplayActionExtractors(script map[string]any) []any {
	var out []any
	walkReplayScriptActions(script, func(containerPath string, _ int, action map[string]any) {
		if ext := action["extractors"]; ext != nil {
			out = append(out, map[string]any{
				"container":  normalizeReplayBrowserContainerPath(containerPath),
				"extractors": ext,
			})
		}
	})
	return out
}

func replaySourceMessageWritesStatus(action map[string]any) bool {
	params, _ := action["params"].(map[string]any)
	if params == nil {
		return false
	}
	switch typed := params["status_message"].(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	default:
		return false
	}
}

func replayActionIsInert(kind string, action map[string]any) bool {
	if kind == "source_message" && replaySourceMessageWritesStatus(action) {
		return false
	}
	_, inert := replayBrowserInertStepTypes[kind]
	return inert
}

func replayActionStepMeta(action map[string]any) map[string]any {
	meta, _ := action["metadata"].(map[string]any)
	if len(meta) == 0 {
		return nil
	}
	var picked map[string]any
	for _, k := range []string{"source_function", "support_mode", dsl.RunLogicActionMetadataKey} {
		if v, ok := meta[k]; ok && v != nil {
			if picked == nil {
				picked = make(map[string]any, 3)
			}
			picked[k] = v
		}
	}
	return picked
}

func replayStepControlHeader(raw any) map[string]any {
	ctrl, _ := raw.(map[string]any)
	if len(ctrl) == 0 {
		return nil
	}
	header := make(map[string]any, len(ctrl))
	for k, v := range ctrl {
		switch importreview.NormalizeScriptFieldName(k) {
		case "steps", "else", "default", "actions", "then":
			continue
		case "cases", "branches":
			items, _ := v.([]any)
			summaries := make([]any, 0, len(items))
			for _, item := range items {
				itemMap, _ := item.(map[string]any)
				if itemMap == nil {
					continue
				}
				summary := make(map[string]any, len(itemMap))
				for ik, iv := range itemMap {
					if importreview.NormalizeScriptFieldName(ik) == "steps" {
						continue
					}
					summary[ik] = iv
				}
				summaries = append(summaries, summary)
			}
			header[k] = summaries
		default:
			header[k] = v
		}
	}
	if len(header) == 0 {
		return nil
	}
	return header
}

func collectReplayVariableProducerContext(script map[string]any, typedScript *dsl.Script) []any {
	var out []any
	if config, _ := script["config"].(map[string]any); config != nil {
		if jh, _ := config["java_http"].(map[string]any); jh != nil && jh["response_mapping"] != nil {
			out = append(out, map[string]any{"java_http_response_mapping": jh["response_mapping"]})
		}
	}
	if typedScript != nil && len(typedScript.Config.RunLogic.Blocks) > 0 {
		out = append(out, map[string]any{"run_logic_action_groups": dsl.RunLogicActionGroups(typedScript.Actions)})
	}
	walkReplayScriptActions(script, func(containerPath string, _ int, action map[string]any) {
		kind := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(importreview.StringJSONValue(action["type"]))))
		ctrl := replayStepControlHeader(action["control"])
		_, droppable := replayBrowserDroppableStepTypes[kind]
		if (replayActionIsInert(kind, action) || droppable) && ctrl == nil && action["extractors"] == nil {
			return
		}
		out = append(out, map[string]any{
			"container":  normalizeReplayBrowserContainerPath(containerPath),
			"type":       kind,
			"wire":       importreview.ImportActionWireContext(action),
			"extractors": action["extractors"],
			"control":    ctrl,
			"meta":       replayActionStepMeta(action),
		})
	})
	return out
}

func replayRunLogic(script map[string]any) any {
	config, _ := script["config"].(map[string]any)
	for key, val := range config {
		if importreview.NormalizeScriptFieldName(key) == "runlogic" {
			return val
		}
	}
	return nil
}

func replayRunLogicEqual(candidate, prior map[string]any) bool {
	return reflect.DeepEqual(replayRunLogic(candidate), replayRunLogic(prior))
}

func replayBrowserSessionSeeds(script map[string]any) bool {
	config, _ := script["config"].(map[string]any)
	browser, _ := config["browser"].(map[string]any)
	session, _ := browser["session"].(map[string]any)
	strategy := strings.TrimSpace(importreview.StringJSONValue(session["strategy"]))
	return dsl.BrowserSessionSeedsContext(strategy)
}

func replayBrowserLeafPinsPageFlow(leafPath, actionPath string, actionContext any, seededSession bool) bool {
	if strings.HasPrefix(leafPath, actionPath+"/metadata") {
		return seededSession && strings.HasPrefix(leafPath, actionPath+"/metadata/source_url")
	}
	action, _ := actionContext.(map[string]any)
	kind := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(importreview.StringJSONValue(action["type"]))))
	switch kind {
	case "browser_switch_window", "browser_close_window":
		return false
	}
	_, inert := replayBrowserInertStepTypes[kind]
	return !inert
}

type replayBrowserFlowStep struct {
	container         string
	kind              string
	group             string
	wire              map[string]any
	controlMeta       map[string]any
	hasKeptCredential bool
	usesInputs        bool
	usesVars          bool
}

func normalizeReplayBrowserContainerPath(containerPath string) string {
	parts := strings.Split(containerPath, "/")
	for i := 1; i < len(parts); i++ {
		if _, err := strconv.Atoi(parts[i]); err != nil {
			continue
		}
		prev := parts[i-1]
		switch prev {
		case "init", "actions", "teardown", "steps", "else", "default", "requests", "request", "then":
			parts[i] = "*"
		}
	}
	return strings.Join(parts, "/")
}

func collectReplayBrowserFlowSteps(rawScript, safeScript map[string]any) []replayBrowserFlowStep {
	rawByKey := make(map[string]map[string]any)
	walkReplayScriptActions(rawScript, func(containerPath string, index int, action map[string]any) {
		rawByKey[containerPath+"#"+strconv.Itoa(index)] = action
	})
	var steps []replayBrowserFlowStep
	walkReplayScriptActions(safeScript, func(containerPath string, index int, safeAct map[string]any) {
		rawAct := rawByKey[containerPath+"#"+strconv.Itoa(index)]
		kind := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(importreview.StringJSONValue(safeAct["type"]))))
		safeWire := importreview.ImportActionWireContext(safeAct)
		var rawWire map[string]any
		if rawAct != nil {
			rawWire = importreview.ImportActionWireContext(rawAct)
		} else {
			rawWire = safeWire
		}
		hasKept := !reflect.DeepEqual(rawWire, safeWire)
		ctrlMeta := replayStepControlHeader(safeAct["control"])
		if replayActionIsInert(kind, safeAct) && !hasKept && ctrlMeta == nil {
			return
		}
		group := ""
		if meta, _ := safeAct["metadata"].(map[string]any); meta != nil {
			group = strings.TrimSpace(importreview.StringJSONValue(meta[dsl.RunLogicActionMetadataKey]))
		}
		steps = append(steps, replayBrowserFlowStep{
			container:         normalizeReplayBrowserContainerPath(containerPath),
			kind:              kind,
			group:             group,
			wire:              safeWire,
			controlMeta:       ctrlMeta,
			hasKeptCredential: hasKept,
			usesInputs:        replayValueContainsRuntimeInputTemplate(safeAct) || replayValueContainsRuntimeInputTemplate(rawAct),
			usesVars:          replayValueContainsRuntimeVarReference(safeAct) || replayValueContainsRuntimeVarReference(rawAct),
		})
	})
	return steps
}

func replayBrowserPageFlowChanged(
	candidateClone,
	priorClone *dsl.Script,
	rawCandidate,
	safeCandidate,
	rawPrior,
	safePrior map[string]any,
	seededBrowserSession,
	inputsChanged,
	varContextChanged bool,
) bool {
	if !replayRunLogicEqual(safeCandidate, safePrior) {
		return true
	}
	if candidateClone != nil && priorClone != nil {
		if seededBrowserSession && !reflect.DeepEqual(engine.RecordedSignInSummary(candidateClone), engine.RecordedSignInSummary(priorClone)) {
			return true
		}
		if (len(candidateClone.Config.RunLogic.Blocks) > 0 || len(priorClone.Config.RunLogic.Blocks) > 0) &&
			!reflect.DeepEqual(dsl.RunLogicActionGroups(candidateClone.Actions), dsl.RunLogicActionGroups(priorClone.Actions)) {
			return true
		}
	}
	candSteps := collectReplayBrowserFlowSteps(rawCandidate, safeCandidate)
	priorSteps := collectReplayBrowserFlowSteps(rawPrior, safePrior)
	for _, st := range candSteps {
		if inputsChanged && st.usesInputs {
			return true
		}
		if varContextChanged && st.usesVars {
			return true
		}
	}
	pi := 0
	for ci := 0; ci < len(candSteps); ci++ {
		matched := false
		for pi < len(priorSteps) {
			if candSteps[ci].container == priorSteps[pi].container &&
				candSteps[ci].kind == priorSteps[pi].kind &&
				candSteps[ci].group == priorSteps[pi].group &&
				reflect.DeepEqual(candSteps[ci].controlMeta, priorSteps[pi].controlMeta) &&
				reflect.DeepEqual(candSteps[ci].wire, priorSteps[pi].wire) {
				pi++
				matched = true
				break
			}
			if priorSteps[pi].hasKeptCredential || priorSteps[pi].controlMeta != nil {
				return true
			}
			if _, droppable := replayBrowserDroppableStepTypes[priorSteps[pi].kind]; !droppable {
				return true
			}
			pi++
		}
		if !matched {
			return true
		}
	}
	for ; pi < len(priorSteps); pi++ {
		if priorSteps[pi].hasKeptCredential || priorSteps[pi].controlMeta != nil {
			return true
		}
		if _, droppable := replayBrowserDroppableStepTypes[priorSteps[pi].kind]; !droppable {
			return true
		}
	}
	return false
}

func relaxReplayTransportRoute(
	candidate, prior map[string]any,
	options importreview.PreservedImportEditOptions,
	proxyAuthorizationKept bool,
) {
	direct := !proxyAuthorizationKept && replayDirectProxyMode(candidate)
	if options.AllowTransportRouteEdits {
		withoutReplayHTTPRoute(candidate, "tls")
		withoutReplayHTTPRoute(prior, "tls")
		if !proxyAuthorizationKept {
			withoutReplayHTTPRoute(candidate, "proxymode")
			withoutReplayHTTPRoute(prior, "proxymode")
		}
	}
	if direct {
		withoutReplayHTTPRoute(candidate, "proxymode")
		withoutReplayHTTPRoute(prior, "proxymode")
	}
	releaseProxy := !proxyAuthorizationKept && (direct || options.AllowTransportRouteEdits) &&
		replayProxyOnlyLostFields(candidate, prior)
	if options.AllowTransportRouteEdits && !proxyAuthorizationKept {
		releaseProxy = releaseProxy ||
			(!replayHasRetainedProxyCredential(candidate) && !replayHasRetainedProxyCredential(prior))
	}
	if releaseProxy {
		withoutReplayHTTPRoute(candidate, "proxy")
		withoutReplayHTTPRoute(prior, "proxy")
	}
}

func replayTransportHTTP(transport map[string]any) map[string]any {
	config, _ := transport["config"].(map[string]any)
	for key, value := range config {
		if importreview.NormalizeScriptFieldName(key) == "http" {
			httpMap, _ := value.(map[string]any)
			return httpMap
		}
	}
	return nil
}

func replayTransportProxy(transport map[string]any) map[string]any {
	for key, value := range replayTransportHTTP(transport) {
		if importreview.NormalizeScriptFieldName(key) == "proxy" {
			proxy, _ := value.(map[string]any)
			return proxy
		}
	}
	return nil
}

func replayDirectProxyMode(transport map[string]any) bool {
	mode, _ := replayTransportHTTP(transport)["proxy_mode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "none")
}

func replayProxyOnlyLostFields(candidate, prior map[string]any) bool {
	priorProxy := replayTransportProxy(prior)
	for key, value := range replayTransportProxy(candidate) {
		priorValue, exists := priorProxy[key]
		if !exists || !reflect.DeepEqual(value, priorValue) {
			return false
		}
	}
	return true
}

func replayHasRetainedProxyCredential(transport map[string]any) bool {
	for _, value := range replayTransportProxy(transport) {
		if text, _ := value.(string); strings.Contains(text, importreview.MaskedScriptSecret) {
			return true
		}
	}
	return false
}

func replayHasRetainedProxyAuthorization(value any) bool {
	masked := func(text any) bool {
		str, _ := text.(string)
		return strings.Contains(str, importreview.MaskedScriptSecret)
	}
	switch typed := value.(type) {
	case map[string]any:
		if name, _ := typed["name"].(string); importreview.NormalizeScriptFieldName(name) == "proxyauthorization" &&
			masked(typed["value"]) {
			return true
		}
		for key, child := range typed {
			if importreview.NormalizeScriptFieldName(key) == "proxyauthorization" && masked(child) {
				return true
			}
			if replayHasRetainedProxyAuthorization(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if replayHasRetainedProxyAuthorization(child) {
				return true
			}
		}
	}
	return false
}

func withoutReplayHTTPRoute(transport map[string]any, fields ...string) {
	httpMap := replayTransportHTTP(transport)
	for key := range httpMap {
		if slices.Contains(fields, importreview.NormalizeScriptFieldName(key)) {
			delete(httpMap, key)
		}
	}
}

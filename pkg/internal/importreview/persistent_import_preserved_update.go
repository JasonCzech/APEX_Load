package importreview

import (
	"encoding/json"
	"fmt"
	convertreview "github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/contentcontract"
	"github.com/jasonczech/apexload/pkg/selector"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func RestoreServerOwnedImportSecurityMetadata(candidate, prior *dsl.Script) error {
	if candidate == nil || prior == nil {
		return nil
	}
	for _, key := range []string{
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
		"effective_settings",
	} {
		var priorValue any
		hasPriorValue := false
		if prior.Metadata != nil && prior.Metadata.Import != nil &&
			prior.Metadata.Import.Preserved != nil {
			priorValue, hasPriorValue = prior.Metadata.Import.Preserved[key]
		}
		if hasPriorValue {
			cloned, err := CloneImportSecurityMetadata(priorValue)
			if err != nil {
				return err
			}
			EnsureScriptImportPreserved(
				candidate,
				prior.Metadata.Import.Source,
			)[key] = cloned
		} else if candidate.Metadata != nil &&
			candidate.Metadata.Import != nil &&
			candidate.Metadata.Import.Preserved != nil {
			delete(candidate.Metadata.Import.Preserved, key)
		}
	}

	const key = dsl.ImportPreservedCredentialTargetsMetadataKey
	const uidKey = dsl.ImportActionUIDMetadataKey
	type priorSurfaceTargets struct {
		raw  any
		path string
	}
	priorTargetsByPath := make(map[string]priorSurfaceTargets)
	priorUIDByPath := make(map[string]string)
	if err := dslwalk.Walk(prior, func(ref dslwalk.SurfaceRef) error {
		meta := ref.Metadata()
		if raw, exists := meta[key]; exists {
			priorTargetsByPath[ref.Path] = priorSurfaceTargets{raw: raw, path: ref.Path}
		}
		if uid, ok := meta[uidKey]; ok && dsl.IsValidImportActionUID(uid) {
			priorUIDByPath[ref.Path] = uid.(string)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("inspect durable import credential locators: %w", err)
	}

	alignedDisplayPaths, err := buildAlignedCandidateToPriorDisplayPaths(candidate, prior)
	if err != nil {
		// Without an alignment there is no safe way to tell which candidate
		// surface a durable locator belongs to: restoring by array position
		// after a deletion or insertion would strip a retained credential's
		// locator, and the next masked read would show that credential.
		if len(priorTargetsByPath) > 0 {
			return fmt.Errorf("restore durable import credential locators: %w", err)
		}
	}
	// The durable locators follow the step alignment alone. It is the same
	// per-collection, identity-checked matching that reconciliation used to
	// restore each step's stored values, so a step receives the locators of
	// exactly the stored step whose credential it now holds. The client-supplied
	// import_action_uid is not trusted on its own: a forged copy of a
	// credential step's UID on an earlier step must neither take that step's
	// locators nor strip them from the step that reconciliation restored.
	//
	// When several candidate steps carry the same UID, the one aligned with the
	// stored step of that UID keeps it (otherwise the first one walked), and
	// every other copy is renamed so later updates stay unambiguous.
	uidOwners := make(map[string]string)
	if err := dslwalk.Walk(candidate, func(ref dslwalk.SurfaceRef) error {
		uidVal, hasUID := ref.Metadata()[uidKey]
		if !hasUID || !dsl.IsValidImportActionUID(uidVal) {
			return nil
		}
		uidStr := uidVal.(string)
		owner, claimed := uidOwners[uidStr]
		if !claimed || (uidOwnerAligned(ref.Path, uidStr, alignedDisplayPaths, priorUIDByPath) &&
			!uidOwnerAligned(owner, uidStr, alignedDisplayPaths, priorUIDByPath)) {
			uidOwners[uidStr] = ref.Path
		}
		return nil
	}); err != nil {
		return fmt.Errorf("inspect import action identifiers: %w", err)
	}
	// A renamed copy must take a UID no other step uses, in the candidate or
	// in the stored script. Reusing one would store two steps with the same
	// UID, and step matching then gives up on every UID-stamped step of that
	// collection, so the next save would strip the durable credential locators
	// of the steps it can no longer match.
	usedUIDs := make(map[string]struct{})
	collectUIDs := func(script *dsl.Script) error {
		return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
			if uid, ok := ref.Metadata()[uidKey].(string); ok && uid != "" {
				usedUIDs[uid] = struct{}{}
			}
			return nil
		})
	}
	if err := collectUIDs(candidate); err != nil {
		return fmt.Errorf("inspect import action identifiers: %w", err)
	}
	if err := collectUIDs(prior); err != nil {
		return fmt.Errorf("inspect import action identifiers: %w", err)
	}
	uidCopies := make(map[string]int)
	mintCopyUID := func(uid string) string {
		for {
			uidCopies[uid]++
			next := fmt.Sprintf("%s-copy-%d", uid, uidCopies[uid]+1)
			if _, taken := usedUIDs[next]; !taken {
				usedUIDs[next] = struct{}{}
				return next
			}
		}
	}

	if err := dslwalk.Walk(candidate, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		var matched priorSurfaceTargets
		exists := false

		if uidVal, hasUID := metadata[uidKey]; hasUID && dsl.IsValidImportActionUID(uidVal) {
			uidStr := uidVal.(string)
			if uidOwners[uidStr] != ref.Path {
				// A duplicated step received a copied UID in the editor; mint
				// a unique deterministic UID so later updates stay unambiguous.
				if copyUID := mintCopyUID(uidStr); dsl.IsValidImportActionUID(copyUID) {
					metadata[uidKey] = copyUID
				} else {
					// The suffixed name would exceed the UID limit; the copy
					// keeps no UID, as a copy made in the editor does.
					delete(metadata, uidKey)
				}
				ref.SetMetadata(metadata)
			}
		}
		if alignedPriorPath, aligned := alignedDisplayPaths[ref.Path]; aligned {
			matched, exists = priorTargetsByPath[alignedPriorPath]
		}

		if !exists {
			if metadata != nil {
				delete(metadata, key)
			}
			return nil
		}
		cloned, err := CloneImportSecurityMetadata(matched.raw)
		if err != nil {
			return err
		}
		cloned = remapRestoredCredentialTargetPaths(cloned, matched.path, ref.Path)
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata[key] = cloned
		ref.SetMetadata(metadata)
		return nil
	}); err != nil {
		return fmt.Errorf("restore durable import credential locators: %w", err)
	}
	return nil
}

// uidOwnerAligned reports whether the candidate surface at path is aligned
// with the stored surface that carries uid.
func uidOwnerAligned(path, uid string, alignedDisplayPaths, priorUIDByPath map[string]string) bool {
	alignedPriorPath, aligned := alignedDisplayPaths[path]
	return aligned && priorUIDByPath[alignedPriorPath] == uid
}

func remapRestoredCredentialTargetPaths(cloned any, priorPath, currentPath string) any {
	if priorPath == "" || currentPath == "" || priorPath == currentPath {
		return cloned
	}
	items, ok := cloned.([]any)
	if !ok {
		return cloned
	}
	priorPointer := DslWalkPathJSONPointer(priorPath)
	currentPointer := DslWalkPathJSONPointer(currentPath)
	priorFieldPrefix := priorPath + "."
	currentFieldPrefix := currentPath + "."
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if reqKey, ok := item["request_key"].(string); ok {
			switch reqKey {
			case priorPath:
				item["request_key"] = currentPath
			case priorPointer:
				if priorPointer != "" && currentPointer != "" {
					item["request_key"] = currentPointer
				}
			}
		}
		if field, ok := item["field"].(string); ok && strings.HasPrefix(field, priorFieldPrefix) {
			item["field"] = currentFieldPrefix + strings.TrimPrefix(field, priorFieldPrefix)
		}
	}
	return items
}

// buildAlignedCandidateToPriorDisplayPaths maps each candidate surface's
// display path to the prior surface it was reconciled from. It compares the
// executable scripts: reconciliation has already restored every stored value
// into the step it matched, so a matched step's identity equals its stored
// step's. Projecting the candidate instead would read its client-supplied
// credential locators, which are about to be replaced: a dropped locator
// changes how a located identity field projects, and one the engine cannot
// resolve fails the projection. It fails rather than leave the caller to fall
// back to positional alignment.
func buildAlignedCandidateToPriorDisplayPaths(candidate, prior *dsl.Script) (map[string]string, error) {
	candidateValue, err := ScriptAsJSONValue(candidate)
	if err != nil {
		return nil, err
	}
	priorValue, err := ScriptAsJSONValue(prior)
	if err != nil {
		return nil, err
	}
	candidateMap, ok1 := candidateValue.(map[string]any)
	priorMap, ok2 := priorValue.(map[string]any)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("could not align the script's steps with the stored version")
	}
	aligned := make(map[string]string)
	unmatchedCollection := ""
	var walk func(candVal, priorVal any, candPath, priorPath string)
	walk = func(candVal, priorVal any, candPath, priorPath string) {
		switch typed := candVal.(type) {
		case map[string]any:
			pMap, _ := priorVal.(map[string]any)
			if candPath != "" && priorPath != "" {
				candDisplay := ImportContextDisplayPath(candPath)
				priorDisplay := ImportContextDisplayPath(priorPath)
				aligned[candDisplay] = priorDisplay
				if strings.EqualFold(strings.TrimSpace(StringJSONValue(typed["type"])), "http_async_start") {
					aligned[candDisplay+".params.request"] = priorDisplay + ".params.request"
				}
			}
			for k, child := range typed {
				var pChild any
				nextPriorPath := ""
				if pMap != nil && priorPath != "" || (pMap != nil && candPath == "") {
					pChild = pMap[k]
					nextPriorPath = AppendImportContextPath(priorPath, k)
				}
				walk(child, pChild, AppendImportContextPath(candPath, k), nextPriorPath)
			}
		case []any:
			pSlice, pOK := priorVal.([]any)
			if !pOK || priorPath == "" {
				return
			}
			if IsActionCollectionContextPath(candPath) {
				matches, ok := MatchCredentialPreservingActionCollection(typed, pSlice)
				if !ok {
					if unmatchedCollection == "" {
						unmatchedCollection = ImportContextDisplayPath(candPath)
					}
					return
				}
				for i, child := range typed {
					priorIdx := matches[i]
					if priorIdx < 0 || priorIdx >= len(pSlice) {
						continue
					}
					walk(
						child,
						pSlice[priorIdx],
						AppendImportContextPath(candPath, strconv.Itoa(i)),
						AppendImportContextPath(priorPath, strconv.Itoa(priorIdx)),
					)
				}
				return
			}
			for i, child := range typed {
				if i >= len(pSlice) {
					break
				}
				walk(
					child,
					pSlice[i],
					AppendImportContextPath(candPath, strconv.Itoa(i)),
					AppendImportContextPath(priorPath, strconv.Itoa(i)),
				)
			}
		}
	}
	walk(candidateMap, priorMap, "", "")
	if unmatchedCollection != "" {
		return nil, fmt.Errorf("could not match the steps of %s with the stored version", unmatchedCollection)
	}
	return aligned, nil
}

func CloneImportSecurityMetadata(value any) (any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode durable import credential locators: %w", err)
	}
	var cloned any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("decode durable import credential locators: %w", err)
	}
	return cloned, nil
}

// PreservedImportEditOptions carries system-admin policy into the retained
// credential checks.
type PreservedImportEditOptions struct {
	// AllowTransportRouteEdits lets an author change the proxy and TLS
	// settings of a script that keeps imported credentials. By default those
	// settings pin the context a retained credential is sent in, because an
	// editor who cannot read the credential could otherwise route it through a
	// proxy they control. A new credential must still be a runtime secret.
	AllowTransportRouteEdits bool
}

func ValidatePreservedImportCredentialEdits(candidate, prior *dsl.Script) error {
	return ValidatePreservedImportCredentialEditsWithOptions(candidate, prior, PreservedImportEditOptions{})
}

func ValidatePreservedImportCredentialEditsWithOptions(
	candidate, prior *dsl.Script,
	options PreservedImportEditOptions,
) error {
	if candidate == nil || prior == nil {
		return fmt.Errorf("credential-preserving update requires a durable prior script")
	}
	// Provenance, not application credentials: the recorder session and the
	// protected-source commit fence (owner digest, source hash, script
	// fingerprint; server-owned and restored on every update) are masked on
	// disclosure, but no request sends them, so they must not lock every
	// action, parameter and base URL in an otherwise credential-free draft.
	// Work on copies so validation never alters persisted provenance.
	candidate = CloneScriptDefinition(candidate)
	prior = CloneScriptDefinition(prior)
	for _, script := range []*dsl.Script{candidate, prior} {
		if script.Metadata != nil && script.Metadata.Import != nil {
			delete(script.Metadata.Import.Preserved, "recorder_session_id")
			delete(script.Metadata.Import.Preserved, contentcontract.ImportSourceCommitFenceMetadataKey)
			delete(script.Metadata.Import.Preserved, "effective_settings")
		}
	}
	projectedCandidate := MaskScriptSecrets(candidate)
	projectedPrior := MaskScriptSecrets(prior)
	if projectedCandidate == nil || projectedPrior == nil {
		return fmt.Errorf("failed to validate credential-preserving update")
	}
	rawCandidate, err := ScriptAsJSONValue(candidate)
	if err != nil {
		return err
	}
	safeCandidate, err := ScriptAsJSONValue(projectedCandidate)
	if err != nil {
		return err
	}
	rawPrior, err := ScriptAsJSONValue(prior)
	if err != nil {
		return err
	}
	safePrior, err := ScriptAsJSONValue(projectedPrior)
	if err != nil {
		return err
	}
	if found, unmatched := newProjectedCredentialAtPath(rawCandidate, safeCandidate, rawPrior, safePrior, ""); found {
		if unmatched != "" {
			// The steps could not be matched to the stored ones, so they were
			// compared by position, and the value found is one the stored
			// script keeps: a kept credential that moved, not a new one.
			return fmt.Errorf(
				"could not match the edited steps of %s with the stored version, so the imported credentials they keep "+
					"cannot be checked: undo the step deletion, insertion or reordering there, bind the kept credentials "+
					"to runtime secrets, or run the source import again",
				ImportContextDisplayPath(unmatched),
			)
		}
		return fmt.Errorf("script introduces new executable credentials; bind a runtime secret or run the source import again")
	}
	if disclosed := retainedCredentialDisclosedAtPath(rawCandidate, safeCandidate, rawPrior, safePrior, ""); disclosed != "" {
		if selectorLocatorPath(disclosed) {
			// A recorded locator quotes a kept value; binding that value to a
			// runtime secret unmasks the quote, so the locator has to go with it.
			return fmt.Errorf(
				"this edit would show an imported credential that the recorded selector locator at %s quotes, in plain "+
					"text: when you bind or remove a kept value, remove the recorded selector candidates and fallbacks "+
					"that quote it in the same save (if that step keeps a value of its own, bind it first), or run the "+
					"source import again",
				ImportContextDisplayPath(disclosed),
			)
		}
		return fmt.Errorf(
			"this edit would show the imported credential kept at %s in plain text: restore what kept it masked "+
				"(such as a removed or cleared source_marked_secret mark), bind it to a runtime secret, or run the "+
				"source import again",
			ImportContextDisplayPath(disclosed),
		)
	}
	// However the steps align, no value the stored version kept masked may
	// show in plain text after the edit: a binding that unmasks a quote of it
	// (a click's text= selector, a title) is refused like a cleared mark.
	if quoted := keptValueQuotedInPlainText(safeCandidate, rawPrior, safePrior); quoted != "" {
		return fmt.Errorf(
			"this edit would show an imported credential that %s quotes, in plain text: when you bind or remove a kept "+
				"value, rewrite or remove every step field that quotes it in the same save, or run the source import again",
			ImportContextDisplayPath(quoted),
		)
	}
	if change, changed := retainedImportCredentialContextChangeDetail(
		rawCandidate,
		safeCandidate,
		rawPrior,
		safePrior,
		options,
	); changed {
		if change.Reason == "browser_page" {
			return fmt.Errorf(
				"this edit changes the pages or page scripts around the imported credential kept at %s "+
					"(first change at %s): a step could type the credential into another page or read it from "+
					"the page. While a recording keeps a captured value you can only delete hovers, scrolls and "+
					"fills, add or remove waits and screenshots, rename steps (unless run logic runs them by that name), and change think time or browser "+
					"display settings; the parameters, data and run logic the steps use are held too. Bind the kept values to runtime secrets first (a typed value can be bound on its own; one kept in a URL, locator or page script is bound in the same save as every other kept value), or record again",
				change.Path,
				change.ChangedAt,
			)
		}
		return fmt.Errorf(
			"script changes the execution context for retained imported credentials at %s: "+
				"this edit could send an imported credential somewhere new. Bind the imported credential "+
				"to a runtime secret or run the source import again; a system administrator can allow some "+
				"proxy and TLS edits (Access > Import credential policy)",
			change.Path,
		)
	}
	return nil
}

func HasNewProjectedCredential(candidate, projectedCandidate, prior, projectedPrior any) bool {
	found, _ := newProjectedCredentialAtPath(candidate, projectedCandidate, prior, projectedPrior, "")
	return found
}

// projectedCredentialFinding records where hasNewProjectedCredentialAtPath
// found a credential prior does not hold at the matching place.
type projectedCredentialFinding struct {
	// value is the candidate's executable value there.
	value any
	// positional names the innermost step list the finding was compared in by
	// position, because its steps could not be matched to the stored ones.
	positional string
}

// newProjectedCredentialAtPath reports a projected credential in candidate that
// prior does not hold at the matching place. unmatched names the step list
// whose steps could not be matched when the value found is one prior already
// keeps masked: a kept credential compared against a step that now holds its
// position, not a new one. It is empty for a value prior does not keep.
func newProjectedCredentialAtPath(candidate, projectedCandidate, prior, projectedPrior any, path string) (found bool, unmatched string) {
	var finding projectedCredentialFinding
	if !hasNewProjectedCredentialAtPath(candidate, projectedCandidate, prior, projectedPrior, path, &finding) {
		return false, ""
	}
	if finding.positional == "" {
		return true, ""
	}
	kept := make(map[string]struct{})
	collectMaskedLeafValues(prior, projectedPrior, kept)
	if _, isKept := kept[maskedLeafKey(finding.value)]; isKept {
		return true, finding.positional
	}
	return true, ""
}

// keptValueQuotedInPlainText returns the first path in the candidate's
// projection whose text holds, unmasked, a value the prior's projection kept
// masked, or "".
func keptValueQuotedInPlainText(safeCandidate, rawPrior, safePrior any) string {
	spans := map[string]struct{}{}
	collectMaskedSpans(rawPrior, safePrior, spans)
	// A value the stored read already shows somewhere (an unsent step left
	// for review) is not disclosed by the edit.
	for span := range spans {
		if firstPathQuotingSpan(safePrior, map[string]struct{}{span: {}}, "") != "" {
			delete(spans, span)
		}
	}
	if len(spans) == 0 {
		return ""
	}
	return firstPathQuotingSpan(safeCandidate, spans, "")
}

// minQuotedKeptSpan is the shortest kept value looked for as a quote: a
// shorter one (a PIN digit) would match ordinary text.
const minQuotedKeptSpan = 4

// collectMaskedSpans adds the raw text behind each masked span of prior's
// projection: a whole leaf masked, or the part of a leaf a sentinel replaced.
func collectMaskedSpans(raw, projected any, out map[string]struct{}) {
	switch typed := raw.(type) {
	case map[string]any:
		projectedMap, _ := projected.(map[string]any)
		for key, child := range typed {
			collectMaskedSpans(child, projectedMap[key], out)
		}
	case []any:
		projectedSlice, _ := projected.([]any)
		for index, child := range typed {
			var projectedChild any
			if index < len(projectedSlice) {
				projectedChild = projectedSlice[index]
			}
			collectMaskedSpans(child, projectedChild, out)
		}
	case string:
		masked, ok := projected.(string)
		if !ok || !strings.Contains(masked, MaskedScriptSecret) {
			return
		}
		pieces := strings.Split(masked, MaskedScriptSecret)
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
			if len(span) >= minQuotedKeptSpan {
				out[span] = struct{}{}
			}
		}
	}
}

func firstPathQuotingSpan(value any, spans map[string]struct{}, path string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedMapKeys(typed) {
			if found := firstPathQuotingSpan(typed[key], spans, AppendImportContextPath(path, key)); found != "" {
				return found
			}
		}
	case []any:
		for index, child := range typed {
			if found := firstPathQuotingSpan(child, spans, AppendImportContextPath(path, strconv.Itoa(index))); found != "" {
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

// collectMaskedLeafValues adds every scalar of raw that projected changes.
func collectMaskedLeafValues(raw, projected any, out map[string]struct{}) {
	switch typed := raw.(type) {
	case map[string]any:
		projectedMap, _ := projected.(map[string]any)
		for key, child := range typed {
			collectMaskedLeafValues(child, projectedMap[key], out)
		}
	case []any:
		projectedSlice, _ := projected.([]any)
		for index, child := range typed {
			var projectedChild any
			if index < len(projectedSlice) {
				projectedChild = projectedSlice[index]
			}
			collectMaskedLeafValues(child, projectedChild, out)
		}
	default:
		if typed != nil && !reflect.DeepEqual(raw, projected) {
			out[maskedLeafKey(raw)] = struct{}{}
		}
	}
}

func maskedLeafKey(value any) string {
	return fmt.Sprintf("%T\x00%v", value, value)
}

func hasNewProjectedCredentialAtPath(candidate, projectedCandidate, prior, projectedPrior any, path string, finding *projectedCredentialFinding) bool {
	switch typed := candidate.(type) {
	case map[string]any:
		projectedMap, _ := projectedCandidate.(map[string]any)
		priorMap, _ := prior.(map[string]any)
		projectedPriorMap, _ := projectedPrior.(map[string]any)
		// In key order, so the same edit always reports the same place.
		for _, key := range sortedMapKeys(typed) {
			child := typed[key]
			var safeChild, priorChild, safePriorChild any
			if projectedMap != nil {
				safeChild = projectedMap[key]
			}
			if priorMap != nil {
				priorChild = priorMap[key]
			}
			if projectedPriorMap != nil {
				safePriorChild = projectedPriorMap[key]
			}
			if hasNewProjectedCredentialAtPath(
				child,
				safeChild,
				priorChild,
				safePriorChild,
				AppendImportContextPath(path, key),
				finding,
			) {
				return true
			}
		}
		return false
	case []any:
		projectedSlice, _ := projectedCandidate.([]any)
		priorSlice, _ := prior.([]any)
		projectedPriorSlice, projectedPriorOK := projectedPrior.([]any)
		var actionMatches []int
		hasActionMatches := false
		actionCollection := projectedPriorOK && IsActionCollectionContextPath(path)
		if actionCollection && len(projectedSlice) == len(typed) {
			actionMatches, hasActionMatches = MatchCredentialPreservingActionCollection(projectedSlice, projectedPriorSlice)
		}
		// Unmatched steps fall back to the positions they now hold, which after
		// a deletion, insertion or reorder can be other steps'.
		positional := actionCollection && !hasActionMatches
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
			if hasNewProjectedCredentialAtPath(
				child,
				safeChild,
				priorChild,
				safePriorChild,
				AppendImportContextPath(path, strconv.Itoa(index)),
				finding,
			) {
				if positional && finding != nil && finding.positional == "" {
					finding.positional = path
				}
				return true
			}
		}
		return false
	default:
		if reflect.DeepEqual(candidate, projectedCandidate) {
			return false
		}
		if reflect.DeepEqual(prior, projectedPrior) || !reflect.DeepEqual(candidate, prior) {
			if finding != nil {
				finding.value = candidate
			}
			return true
		}
		return false
	}
}

// retainedCredentialDisclosedAtPath returns the first place where candidate
// still holds the exact value prior keeps there masked, but candidate's own
// projection no longer hides it as prior's did. Restoring a masked read puts
// a kept value back only where it was, so the only way to reveal one is to
// change what decides its masking around it: clear the mark on a recorded
// fill, for example, and the next read would show the value. A value the
// candidate masks whole, or exactly as prior did, discloses nothing.
// selectorLocatorPath reports a path inside a step's recorded selector
// candidates or fallbacks.
func selectorLocatorPath(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case selector.MetadataCandidatesKey, selector.MetadataFallbacksKey, "source_target_selector_candidates":
			return true
		}
	}
	return false
}

func retainedCredentialDisclosedAtPath(candidate, projectedCandidate, prior, projectedPrior any, path string) string {
	switch typed := candidate.(type) {
	case map[string]any:
		projectedMap, _ := projectedCandidate.(map[string]any)
		priorMap, _ := prior.(map[string]any)
		projectedPriorMap, _ := projectedPrior.(map[string]any)
		for _, key := range sortedMapKeys(typed) {
			child := typed[key]
			if disclosed := retainedCredentialDisclosedAtPath(
				child,
				projectedMap[key],
				priorMap[key],
				projectedPriorMap[key],
				AppendImportContextPath(path, key),
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
		if projectedPriorOK && IsActionCollectionContextPath(path) && len(projectedSlice) == len(typed) {
			actionMatches, hasActionMatches = MatchCredentialPreservingActionCollection(projectedSlice, projectedPriorSlice)
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
			if disclosed := retainedCredentialDisclosedAtPath(
				child,
				safeChild,
				priorChild,
				safePriorChild,
				AppendImportContextPath(path, strconv.Itoa(index)),
			); disclosed != "" {
				return disclosed
			}
		}
		return ""
	default:
		if candidate == nil || reflect.DeepEqual(prior, projectedPrior) || !reflect.DeepEqual(candidate, prior) {
			return ""
		}
		if reflect.DeepEqual(projectedCandidate, projectedPrior) || projectedCandidate == MaskedScriptSecret {
			return ""
		}
		return path
	}
}

// importReviewEvidencePreservedKeys are the metadata.import.preserved keys
// that hold an import's review evidence and evaluation output. Execution never
// sends or applies them: no request, runtime setting, credential locator or
// redaction plan is built from them, and launch admission reads only the
// verdicts in them (promotion gating). Disclosure still masks a credential
// they quote (an archive's source text quotes every literal header it
// converts), and the context comparison would read that masked copy as a kept
// credential whose context is the whole script, refusing every parameter, data
// source, action and transport edit. HasNewProjectedCredential still covers
// these subtrees, so a quoted credential cannot be added, changed or moved out
// of them into an executable field.
var importReviewEvidencePreservedKeys = []string{
	// The review's copy of the source files and their line mappings. Read by
	// the source text view, promotion gating (file counts) and credential
	// binding, which takes only the file paths.
	"source_review",
	// The reviewed artifact, whose source manifest promotion gating and the
	// Java classpath check compare. The compatibility runtime runs
	// runtime_source_artifact instead.
	"review_artifact",
	// The TrueClient archive inspection the import summary shows.
	"trueclient_archive_inspection",
	// The archive's project file (.usr) sections as recorded. The import
	// summary lists only the section names; settings a run applies are
	// converted into the script. Disclosure scrubs this copy as free text, so a
	// recorded value such as the transaction order list can read as masked.
	"project_settings",
	// Importer review payloads the import summary shows. A Salesforce native
	// preview is a copy for review; the script itself is what runs.
	"salesforce_review",
	"mulesoft_review",
	"agentforce_review",
	// Converter warning text the review lists.
	"warnings",
	// Evaluation output that promotion gating and repairs read and every
	// repair rebuilds from the repaired script.
	"fidelity_report",
	"replay_preflight",
}

// importReviewEvidencePreservedPaths are the context paths of
// importReviewEvidencePreservedKeys.
var importReviewEvidencePreservedPaths = func() map[string]struct{} {
	paths := make(map[string]struct{}, len(importReviewEvidencePreservedKeys))
	for _, key := range importReviewEvidencePreservedKeys {
		paths[AppendImportContextPath("", "metadata", "import", "preserved", key)] = struct{}{}
	}
	return paths
}()

// stepReviewMetadataKeys are the step metadata entries import review stamps
// (importreview.ApplyToAction) and guided repairs write: gate codes such as
// missing_producer:<name>, which a save or a repair rebuilds from the script,
// the review flags stamped beside them, and a repair's migration note. Replay
// reads them only to gate the step. Disclosure scrubs a code as free text, so
// a long variable name in one can read as masked, and the context comparison
// would then pin the step's request to a kept credential it does not carry.
// HasNewProjectedCredential still covers these entries.
var stepReviewMetadataKeys = map[string]struct{}{
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

// UnsentStepReviewMetadata reports whether key is a review entry
// (stepReviewMetadataKeys) in the metadata of action that the step does not
// send. A Kafka or JMS send delivers each string metadata entry as a record
// header or message property (varflow.MetadataReads), so its review_gate and
// execution_blocker are executable values like any other header, except for
// a dependency code in one, which is server output (stepDependencyCodeLeaf).
func UnsentStepReviewMetadata(action dsl.Action, key string) bool {
	if _, review := stepReviewMetadataKeys[key]; !review {
		return false
	}
	sent, _ := varflow.MetadataReads(action, key)
	return !sent
}

// unsentStepReviewMetadataLeaf reports whether path is a leaf of a review
// entry in the metadata of the step at actionPath that the step does not send
// (UnsentStepReviewMetadata). A send delivers only a top-level string entry,
// so a nested leaf is never sent.
func unsentStepReviewMetadataLeaf(path, actionPath string, action map[string]any, leaf any) bool {
	prefix := actionPath + "/metadata/"
	if actionPath == "" || !strings.HasPrefix(path, prefix) {
		return false
	}
	key, _, nested := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	if _, review := stepReviewMetadataKeys[key]; review {
		if nested {
			return true
		}
		return UnsentStepReviewMetadata(dsl.Action{
			Type:     StringJSONValue(action["type"]),
			Metadata: map[string]any{key: leaf},
		}, key)
	}
	if IsHTTPImportProvenanceMetadataKey(key) {
		sent, _ := varflow.MetadataReads(dsl.Action{
			Type:     StringJSONValue(action["type"]),
			Metadata: map[string]any{key: leaf},
		}, key)
		return !sent
	}
	return false
}

// stepDependencyCodeLeaf reports whether path is a top-level review entry
// (stepReviewMetadataKeys) in the metadata of the step at actionPath that
// holds a dependency review code, missing_producer:<name> or
// conditional_producer:<name>. Import review stamps those codes from the
// variables the script reads, and every save or repair of an evaluated import
// stamps them again (importreview.RefreshDependencies), so an unchanged code
// names a variable, not a kept credential, even where a Kafka or JMS send
// delivers it as a header or property. It pins no request; the new-credential
// check still refuses a code that changes or gains masked text.
func stepDependencyCodeLeaf(path, actionPath string, leaf any) bool {
	prefix := actionPath + "/metadata/"
	if actionPath == "" || !strings.HasPrefix(path, prefix) {
		return false
	}
	if _, review := stepReviewMetadataKeys[strings.TrimPrefix(path, prefix)]; !review {
		return false
	}
	code, _ := leaf.(string)
	return convertreview.IsDependencyCode(code)
}

func RetainedImportCredentialContextChange(
	candidate,
	projectedCandidate,
	prior,
	projectedPrior any,
) (string, bool) {
	return retainedImportCredentialContextChange(
		candidate,
		projectedCandidate,
		prior,
		projectedPrior,
		PreservedImportEditOptions{},
	)
}

func retainedImportCredentialContextChange(
	candidate,
	projectedCandidate,
	prior,
	projectedPrior any,
	options PreservedImportEditOptions,
) (string, bool) {
	change, changed := retainedImportCredentialContextChangeDetail(candidate, projectedCandidate, prior, projectedPrior, options)
	return change.Path, changed
}

// importCredentialContextChange says where and why an edit changes the
// context of a kept credential.
type importCredentialContextChange struct {
	// Path is where the kept credential is (ImportContextDisplayPath form).
	Path string
	// Reason is "script" (a credential kept at script level), "transport"
	// (proxy, TLS and other routing), "step" (the step that holds it or what
	// it reads), or "browser_page" (the page flow around a credential a
	// browser step keeps).
	Reason string
	// ChangedAt is the first changed step of a browser_page change.
	ChangedAt string
}

func retainedImportCredentialContextChangeDetail(
	candidate,
	projectedCandidate,
	prior,
	projectedPrior any,
	options PreservedImportEditOptions,
) (importCredentialContextChange, bool) {
	candidateMap, candidateOK := projectedCandidate.(map[string]any)
	priorMap, priorOK := projectedPrior.(map[string]any)
	if !candidateOK || !priorOK {
		return importCredentialContextChange{Path: "script", Reason: "script"}, true
	}
	candidateActions, candidateOrder := ImportActionContexts(candidateMap)
	priorActions, priorOrder := ImportActionContexts(priorMap)
	candidateTransport := ImportTransportContext(candidateMap)
	priorTransport := ImportTransportContext(priorMap)
	proxyAuthorizationKept := hasRetainedProxyAuthorization(priorMap) || hasRetainedProxyAuthorization(candidateMap)
	relaxTransportRoute(candidateTransport, priorTransport, options, proxyAuthorizationKept)
	candidateGlobal := ImportGlobalCredentialContext(
		candidateMap,
		candidateTransport,
		candidateActions,
		candidateOrder,
	)
	priorGlobal := ImportGlobalCredentialContext(
		priorMap,
		priorTransport,
		priorActions,
		priorOrder,
	)
	// A request that carries a kept credential is also pinned to the
	// parameters, data sources and variable-writing steps its wire fields
	// interpolate (importReferenceGuard).
	references := newImportReferenceGuard(candidate, candidateMap, prior, priorMap)

	// The page flow of a browser script (persistent_import_preserved_browser_page.go),
	// built once, and only when a credential a browser step keeps needs it.
	browserScript := NormalizeScriptFieldName(StringJSONValue(candidateMap["protocol"])) == "browser"
	seededSession := browserSessionSeeds(candidateMap) || browserSessionSeeds(priorMap)
	pageChecked, pageChanged, pageChangedAt := false, false, ""
	browserPageChange := func() (string, bool) {
		if pageChecked {
			return pageChangedAt, pageChanged
		}
		pageChecked = true
		candidateSteps, candidateGroups, candidateErr := importBrowserPageSteps(candidateMap)
		priorSteps, priorGroups, priorErr := importBrowserPageSteps(priorMap)
		if candidateErr != nil || priorErr != nil {
			pageChanged, pageChangedAt = true, "script"
			return pageChangedAt, pageChanged
		}
		pairs, at, changed := alignBrowserPageSteps(candidateSteps, priorSteps)
		// Each run-logic group holds its steps the way the walk does: only
		// droppable ones may go, and none may move between groups. (The
		// walk's run_logic entry already pins which groups there are.)
		for group, members := range priorGroups {
			if changed {
				break
			}
			if _, groupAt, groupChanged := alignBrowserPageSteps(candidateGroups[group], members); groupChanged {
				at, changed = groupAt, true
			}
		}
		for _, pair := range pairs {
			if changed {
				break
			}
			// The synthetic sign-in entry has no step, and so nothing it reads.
			if pair[0] != "" && references.requestChangedBetween(pair[0], pair[1]) {
				at, changed = pair[0], true
			}
		}
		if at == "" {
			at = "script"
		}
		pageChanged, pageChangedAt = changed, ImportContextDisplayPath(at)
		return pageChangedAt, pageChanged
	}
	reason, changedAt := "", ""

	var visit func(
		raw,
		safe,
		rawPrior,
		safePrior any,
		path,
		priorPath,
		actionPath,
		priorActionPath string,
	) (string, bool)
	visit = func(
		raw,
		safe,
		rawPrior,
		safePrior any,
		path,
		priorPath,
		actionPath,
		priorActionPath string,
	) (string, bool) {
		// Review evidence has no execution context to change. The new-credential
		// check has already compared it leaf by leaf.
		if _, evidence := importReviewEvidencePreservedPaths[path]; evidence {
			return "", false
		}
		if _, isAction := candidateActions[path]; isAction {
			actionPath = path
			priorActionPath = priorPath
		}
		switch typed := raw.(type) {
		case map[string]any:
			safeMap, _ := safe.(map[string]any)
			priorMap, _ := rawPrior.(map[string]any)
			safePriorMap, _ := safePrior.(map[string]any)
			for key, child := range typed {
				var safeChild, priorChild, safePriorChild any
				if safeMap != nil {
					safeChild = safeMap[key]
				}
				if priorMap != nil {
					priorChild = priorMap[key]
				}
				if safePriorMap != nil {
					safePriorChild = safePriorMap[key]
				}
				nextPriorPath := ""
				if path == "" || priorPath != "" {
					nextPriorPath = AppendImportContextPath(priorPath, key)
				}
				if context, changed := visit(
					child,
					safeChild,
					priorChild,
					safePriorChild,
					AppendImportContextPath(path, key),
					nextPriorPath,
					actionPath,
					priorActionPath,
				); changed {
					return context, true
				}
			}
		case []any:
			safeSlice, _ := safe.([]any)
			priorSlice, _ := rawPrior.([]any)
			safePriorSlice, safePriorOK := safePrior.([]any)
			var actionMatches []int
			hasActionMatches := false
			if safePriorOK && IsActionCollectionContextPath(path) && len(safeSlice) == len(typed) {
				actionMatches, hasActionMatches = MatchCredentialPreservingActionCollection(safeSlice, safePriorSlice)
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
					nextPriorPath = AppendImportContextPath(priorPath, strconv.Itoa(priorIdx))
				}
				if context, changed := visit(
					child,
					safeChild,
					priorChild,
					safePriorChild,
					AppendImportContextPath(path, strconv.Itoa(index)),
					nextPriorPath,
					actionPath,
					priorActionPath,
				); changed {
					return context, true
				}
			}
		default:
			// The preceding new-credential check proves this is the exact
			// durable credential at the same structural location. Values not
			// changed by response projection carry no retained credential.
			if reflect.DeepEqual(raw, safe) ||
				!reflect.DeepEqual(raw, rawPrior) ||
				reflect.DeepEqual(rawPrior, safePrior) {
				return "", false
			}
			if action, _ := candidateActions[actionPath].(map[string]any); unsentStepReviewMetadataLeaf(path, actionPath, action, raw) ||
				stepDependencyCodeLeaf(path, actionPath, raw) {
				return "", false
			}
			if actionPath == "" {
				// A credential kept at script level is sent with every request,
				// so it also holds what every request reads.
				if !reflect.DeepEqual(candidateGlobal, priorGlobal) || references.scriptChanged() {
					reason = "script"
					return "script", true
				}
				return "", false
			}
			candidateAction, candidateExists := candidateActions[actionPath]
			priorAction, priorExists := priorActions[priorActionPath]
			switch {
			case !candidateExists || !priorExists:
				reason = "step"
			case !reflect.DeepEqual(candidateTransport, priorTransport):
				reason = "transport"
			default:
				if browserScript && browserPageValueLeaf(path, actionPath, candidateAction, seededSession) {
					if at, changed := browserPageChange(); changed {
						reason, changedAt = "browser_page", at
						return ImportContextDisplayPath(actionPath), true
					}
				}
				if reflect.DeepEqual(candidateAction, priorAction) &&
					!references.requestChangedBetween(actionPath, priorActionPath) {
					return "", false
				}
				reason = "step"
			}
			return ImportContextDisplayPath(actionPath), true
		}
		return "", false
	}

	path, changed := visit(candidate, projectedCandidate, prior, projectedPrior, "", "", "", "")
	if !changed {
		return importCredentialContextChange{}, false
	}
	return importCredentialContextChange{Path: path, Reason: reason, ChangedAt: changedAt}, true
}

func ImportTransportContext(script map[string]any) map[string]any {
	context := map[string]any{
		"protocol": script["protocol"],
	}
	config, _ := script["config"].(map[string]any)
	if config == nil {
		return context
	}
	transport := make(map[string]any, len(config))
	for key, value := range config {
		switch NormalizeScriptFieldName(key) {
		case "thinktime", "pacing", "errorhandling", "runlogic", "observability", "log", "results":
			continue
		case "http":
			transport[key] = withoutFields(value, httpTuningFields)
		case "webruntime":
			transport[key] = withoutFields(value, webRuntimeBehaviourFields)
		case "soap":
			transport[key] = withoutFields(value, soapBehaviourFields)
		case "browser":
			transport[key] = withoutFields(value, browserBehaviourFields)
		case "browseremulation":
			// Browser emulation picks a user agent and cache behaviour; it never
			// changes where a request goes or what credential it carries.
			continue
		default:
			transport[key] = value
		}
	}
	context["config"] = transport
	return context
}

// httpTuningFields are HTTP settings that size buffers, timeouts and
// connection reuse, judge responses, cache, handle cookies, or set ordinary
// header text. They change neither where nor how a retained credential is
// delivered, so editing them must not pin the request context. Fields that do
// (base URL, proxy, TLS, HTTP version and QUIC, redirects, default headers and
// authentication) stay pinned.
var httpTuningFields = map[string]struct{}{
	"receivebufferbytes":       {},
	"maxmetarefresh":           {},
	"contentcheckerrorlimit":   {},
	"pagetimeoutms":            {},
	"challengedelayms":         {},
	"cachemaxunusediterations": {},
	"keepalive":                {},
	"maxconnsperhost":          {},
	"maxidleconns":             {},
	"connecttimeoutms":         {},
	"requesttimeoutms":         {},
	"tlstimeoutms":             {},
	"idletimeoutms":            {},
	"httperrorsaswarnings":     {},
	"failnoncriticalresources": {},
	"resourcetimeoutaswarning": {},
	"legacyhtmlchecks":         {},
	"dnscache":                 {},
	"ipversion":                {},
	"cacheexcludedmimetypes":   {},
	"htmlparsemode":            {},
	"htmlexcludedmimetypes":    {},
	"aurarecordedfailures":     {},
	"cookiesenabled":           {},
	"clearcookiesperiteration": {},
	"cacheenabled":             {},
	"traceparent":              {},
	"traceparentsampled":       {},
	"useragent":                {},
	"acceptencoding":           {},
	"acceptlanguage":           {},
}

// webRuntimeBehaviourFields are web runtime settings that shape pacing,
// checks, downloads and local addressing. The free-form additional attributes
// stay pinned because their meaning is open-ended.
var webRuntimeBehaviourFields = map[string]struct{}{
	"downloadfilters":   {},
	"contentchecks":     {},
	"networksimulation": {},
	"streaming":         {},
	"dataformatting":    {},
	"sourceips":         {},
	"networkprofiles":   {},
}

// soapBehaviourFields pick the envelope version and judge faults. WS-Security,
// which carries credentials, and the namespaces, which decide which operation
// a kept token authorises, stay pinned.
var soapBehaviourFields = map[string]struct{}{
	"version":       {},
	"faulthandling": {},
}

// browserBehaviourFields shape how the browser runs and what evidence it
// keeps: a window or none, its size and device, pacing, metrics, screenshots
// and HAR. Like the log and results settings, which are never pinned, they
// change neither where a page sends a retained credential nor how. The proxy,
// client certificates, HTTP credentials, storage state and session, which do,
// stay pinned, as do the engine, QUIC, traceparent, per-iteration identity and
// the directory HAR files are written to, and the review policy, which can
// let a reviewed page script run.
var browserBehaviourFields = map[string]struct{}{
	"headless":               {},
	"slowmoms":               {},
	"viewport":               {},
	"availablescreenwidth":   {},
	"availablescreenheight":  {},
	"deviceprofile":          {},
	"touchmode":              {},
	"orientation":            {},
	"useragent":              {},
	"locale":                 {},
	"timezone":               {},
	"systemlanguage":         {},
	"geolocation":            {},
	"clientmetrics":          {},
	"capturedocumentmetrics": {},
	"captureinteractions":    {},
	"experiencecanary":       {},
	"eptcalibration":         {},
	"capturescreenshots":     {},
	"harcapture":             {},
	"harcapturepolicy":       {},
	"harslathresholdms":      {},
	"cacheenabled":           {},
	"cachecontenturls":       {},
	"checknewerversions":     {},
}

// withoutFields returns a copy of a settings map without the named
// (normalized) fields.
func withoutFields(value any, fields map[string]struct{}) any {
	settings, _ := value.(map[string]any)
	if settings == nil {
		return value
	}
	kept := make(map[string]any, len(settings))
	for key, child := range settings {
		if _, drop := fields[NormalizeScriptFieldName(key)]; drop {
			continue
		}
		kept[key] = child
	}
	return kept
}

func transportHTTP(transport map[string]any) map[string]any {
	config, _ := transport["config"].(map[string]any)
	for key, value := range config {
		if NormalizeScriptFieldName(key) == "http" {
			http, _ := value.(map[string]any)
			return http
		}
	}
	return nil
}

func directProxyMode(transport map[string]any) bool {
	mode, _ := transportHTTP(transport)["proxy_mode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "none")
}

// relaxTransportRoute removes from the compared contexts the transport
// settings an edit may change without redirecting a retained credential.
//
// Choosing No Proxy sends the credential straight to the host its request
// already names, so it narrows the route rather than redirecting it. The proxy
// block it leaves behind is dormant but could be re-activated by a later
// trusted flip back to a proxy, so it may only lose fields (stay as it was,
// lose some, or be cleared); it may not gain or change one.
//
// With the admin policy on, proxy mode and TLS may change freely. The proxy
// block is released too, unless a retained proxy credential (a masked proxy
// username, password or URL userinfo, or a masked Proxy-Authorization header)
// would follow a new proxy URL: then it may still only lose fields, and the
// credential must be bound to a runtime secret before the proxy can change.
// A kept Proxy-Authorization header also pins the proxy mode, under either
// policy, because changing the mode changes who receives the header.
func relaxTransportRoute(
	candidate, prior map[string]any,
	options PreservedImportEditOptions,
	proxyAuthorizationKept bool,
) {
	// A kept Proxy-Authorization header is sent to whichever proxy the mode
	// picks, or to the origin when there is none, so it pins the mode itself.
	direct := !proxyAuthorizationKept && directProxyMode(candidate)
	if options.AllowTransportRouteEdits {
		withoutHTTPRoute(candidate, "tls")
		withoutHTTPRoute(prior, "tls")
		if !proxyAuthorizationKept {
			withoutHTTPRoute(candidate, "proxymode")
			withoutHTTPRoute(prior, "proxymode")
		}
	}
	if direct {
		withoutHTTPRoute(candidate, "proxymode")
		withoutHTTPRoute(prior, "proxymode")
	}
	// A block that only lost fields cannot send anything anywhere new.
	// A kept Proxy-Authorization header pins the whole block: blanking the
	// URL under the legacy empty mode would connect directly and hand the
	// header to the origin.
	releaseProxy := !proxyAuthorizationKept && (direct || options.AllowTransportRouteEdits) &&
		proxyOnlyLostFields(candidate, prior)
	if options.AllowTransportRouteEdits && !proxyAuthorizationKept {
		releaseProxy = releaseProxy ||
			(!hasRetainedProxyCredential(candidate) && !hasRetainedProxyCredential(prior))
	}
	if releaseProxy {
		withoutHTTPRoute(candidate, "proxy")
		withoutHTTPRoute(prior, "proxy")
	}
}

// proxyOnlyLostFields reports whether every field of the candidate's proxy
// block is present, unchanged, in the prior's. An empty candidate block
// qualifies.
func proxyOnlyLostFields(candidate, prior map[string]any) bool {
	priorProxy := transportProxy(prior)
	for key, value := range transportProxy(candidate) {
		priorValue, exists := priorProxy[key]
		if !exists || !reflect.DeepEqual(value, priorValue) {
			return false
		}
	}
	return true
}

func transportProxy(transport map[string]any) map[string]any {
	for key, value := range transportHTTP(transport) {
		if NormalizeScriptFieldName(key) == "proxy" {
			proxy, _ := value.(map[string]any)
			return proxy
		}
	}
	return nil
}

// hasRetainedProxyCredential reports whether the projected proxy block carries
// a masked value: a kept proxy username, password or URL userinfo.
func hasRetainedProxyCredential(transport map[string]any) bool {
	for _, value := range transportProxy(transport) {
		if text, _ := value.(string); strings.Contains(text, MaskedScriptSecret) {
			return true
		}
	}
	return false
}

// hasRetainedProxyAuthorization reports whether a masked Proxy-Authorization
// header survives anywhere in the projected script, as a header map entry or as
// a name/value header pair. Such a header is a proxy credential that follows
// the proxy URL like a proxy password does.
func hasRetainedProxyAuthorization(value any) bool {
	masked := func(text any) bool {
		str, _ := text.(string)
		return strings.Contains(str, MaskedScriptSecret)
	}
	switch typed := value.(type) {
	case map[string]any:
		if name, _ := typed["name"].(string); NormalizeScriptFieldName(name) == "proxyauthorization" &&
			masked(typed["value"]) {
			return true
		}
		for key, child := range typed {
			if NormalizeScriptFieldName(key) == "proxyauthorization" && masked(child) {
				return true
			}
			if hasRetainedProxyAuthorization(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if hasRetainedProxyAuthorization(child) {
				return true
			}
		}
	}
	return false
}

// withoutHTTPRoute drops the named (normalized) HTTP fields from a transport
// context. The http map is a fresh copy (withoutFields), so this never
// alters the projected script.
func withoutHTTPRoute(transport map[string]any, fields ...string) {
	http := transportHTTP(transport)
	for key := range http {
		if slices.Contains(fields, NormalizeScriptFieldName(key)) {
			delete(http, key)
		}
	}
}

func ImportGlobalCredentialContext(
	script map[string]any,
	transport map[string]any,
	actions map[string]any,
	order []string,
) map[string]any {
	orderedActions := make([]any, 0, len(order))
	for _, path := range order {
		orderedActions = append(orderedActions, map[string]any{
			"path":    path,
			"context": actions[path],
		})
	}
	return map[string]any{
		"transport":    transport,
		"actions":      orderedActions,
		"parameters":   importClockZoneFreeParameters(script["parameters"]),
		"data_sources": script["data_sources"],
	}
}

// importClockZoneFreeParameters is the parameter list without the zone of each
// clock parameter (importClockTimezone), which a script-level credential does
// not need to pin. Every other field, and every other parameter, is kept.
func importClockZoneFreeParameters(raw any) any {
	parameters, ok := raw.([]any)
	if !ok {
		return raw
	}
	out := make([]any, len(parameters))
	for index, value := range parameters {
		out[index] = value
		parameter, isMap := value.(map[string]any)
		if !isMap || !importClockParameter(parameter) || !importClockTimezone(parameter["timezone"]) {
			continue
		}
		held := make(map[string]any, len(parameter))
		for field, fieldValue := range parameter {
			if field != "timezone" {
				held[field] = fieldValue
			}
		}
		out[index] = held
	}
	return out
}

func ImportActionContexts(script map[string]any) (map[string]any, []string) {
	contexts := make(map[string]any)
	order := make([]string, 0)
	walkImportActions(script, func(path string, action map[string]any) {
		contexts[path] = ImportActionWireContext(action)
		order = append(order, path)
	})
	return contexts, order
}

// walkImportActions calls visit for every action of a projected script, in
// execution-phase order, by structural path: the top-level steps of each phase,
// the steps of every control container (the if branches, switch cases and
// default, and parallel or async-callback branches that
// dsl.ControlFlow.ActionBranches lists) and the children of an HTTP sequence.
func walkImportActions(script map[string]any, visit func(path string, action map[string]any)) {
	var walkActions func(any, string)
	var walkAction func(map[string]any, string)

	walkActions = func(raw any, path string) {
		actions, _ := raw.([]any)
		for index, value := range actions {
			action, _ := value.(map[string]any)
			if action == nil {
				continue
			}
			walkAction(action, AppendImportContextPath(path, strconv.Itoa(index)))
		}
	}
	walkAction = func(action map[string]any, path string) {
		visit(path, action)

		control, _ := action["control"].(map[string]any)
		if control != nil {
			walkActions(control["steps"], AppendImportContextPath(path, "control", "steps"))
			walkActions(control["else"], AppendImportContextPath(path, "control", "else"))
			walkActions(control["default"], AppendImportContextPath(path, "control", "default"))
			for _, container := range []string{"cases", "branches"} {
				entries, _ := control[container].([]any)
				for index, entry := range entries {
					entryMap, _ := entry.(map[string]any)
					walkActions(entryMap["steps"], AppendImportContextPath(path, "control", container, strconv.Itoa(index), "steps"))
				}
			}
		}
		if !strings.EqualFold(strings.TrimSpace(StringJSONValue(action["type"])), "http_sequence") {
			return
		}
		params, _ := action["params"].(map[string]any)
		if params != nil {
			walkActions(params["requests"], AppendImportContextPath(path, "params", "requests"))
		}
	}

	for _, phase := range []string{"init", "actions", "teardown"} {
		walkActions(script[phase], "/"+phase)
	}
}

func ImportActionWireContext(action map[string]any) map[string]any {
	context := make(map[string]any, len(action))
	for key, value := range action {
		switch NormalizeScriptFieldName(key) {
		case "name", "assertions", "extractors", "thinktime", "thinktimeafter",
			"waitfor", "sync", "capture", "control":
			continue
		case "metadata":
			// A browser step's runtime reads part of its metadata to choose
			// its element, frame and page (browserRuntimeMetadataKeys): a
			// self-heal fallback can retarget a kept fill. Other metadata is
			// provenance no step sends.
			if held := browserRuntimeMetadata(action); held != nil {
				context[key] = held
			}
			continue
		case "params":
			params, _ := value.(map[string]any)
			if params == nil {
				context[key] = value
				continue
			}
			projected := make(map[string]any, len(params))
			for paramKey, paramValue := range params {
				if strings.EqualFold(strings.TrimSpace(StringJSONValue(action["type"])), "http_sequence") &&
					NormalizeScriptFieldName(paramKey) == "requests" {
					continue
				}
				projected[paramKey] = paramValue
			}
			context[key] = projected
		default:
			context[key] = value
		}
	}
	return context
}

func AppendImportContextPath(path string, segments ...string) string {
	for _, segment := range segments {
		if path == "" {
			path = "/" + segment
		} else {
			path += "/" + segment
		}
	}
	return path
}

func ImportContextDisplayPath(path string) string {
	if path == "" {
		return "script"
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var display strings.Builder
	for index, segment := range segments {
		if _, err := strconv.Atoi(segment); err == nil {
			display.WriteString("[")
			display.WriteString(segment)
			display.WriteString("]")
			continue
		}
		if index > 0 {
			display.WriteString(".")
		}
		display.WriteString(segment)
	}
	if display.Len() == 0 {
		return "script"
	}
	return display.String()
}

func StringJSONValue(value any) string {
	text, _ := value.(string)
	return text
}

func sortedMapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

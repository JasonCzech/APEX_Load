package importreview

import (
	"encoding/json"
	"fmt"
	convertreview "github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/contentcontract"
	"reflect"
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
	priorTargetsByPath := make(map[string]any)
	if err := dslwalk.Walk(prior, func(ref dslwalk.SurfaceRef) error {
		if raw, exists := ref.Metadata()[key]; exists {
			priorTargetsByPath[ref.Path] = raw
		}
		return nil
	}); err != nil {
		return fmt.Errorf("inspect durable import credential locators: %w", err)
	}
	if err := dslwalk.Walk(candidate, func(ref dslwalk.SurfaceRef) error {
		raw, exists := priorTargetsByPath[ref.Path]
		metadata := ref.Metadata()
		if !exists {
			if metadata != nil {
				delete(metadata, key)
			}
			return nil
		}
		cloned, err := CloneImportSecurityMetadata(raw)
		if err != nil {
			return err
		}
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

func ValidatePreservedImportCredentialEdits(candidate, prior *dsl.Script) error {
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
	if HasNewProjectedCredential(rawCandidate, safeCandidate, rawPrior, safePrior) {
		return fmt.Errorf("script introduces new executable credentials; bind a runtime secret or run the source import again")
	}
	if context, changed := RetainedImportCredentialContextChange(
		rawCandidate,
		safeCandidate,
		rawPrior,
		safePrior,
	); changed {
		return fmt.Errorf(
			"script changes the execution context for retained imported credentials at %s; bind a runtime secret or run the source import again",
			context,
		)
	}
	return nil
}

func HasNewProjectedCredential(candidate, projectedCandidate, prior, projectedPrior any) bool {
	switch typed := candidate.(type) {
	case map[string]any:
		projectedMap, _ := projectedCandidate.(map[string]any)
		priorMap, _ := prior.(map[string]any)
		projectedPriorMap, _ := projectedPrior.(map[string]any)
		for key, child := range typed {
			if HasNewProjectedCredential(
				child,
				projectedMap[key],
				priorMap[key],
				projectedPriorMap[key],
			) {
				return true
			}
		}
		return false
	case []any:
		projectedSlice, _ := projectedCandidate.([]any)
		priorSlice, _ := prior.([]any)
		projectedPriorSlice, _ := projectedPrior.([]any)
		for index, child := range typed {
			var safeChild, priorChild, safePriorChild any
			if index < len(projectedSlice) {
				safeChild = projectedSlice[index]
			}
			if index < len(priorSlice) {
				priorChild = priorSlice[index]
			}
			if index < len(projectedPriorSlice) {
				safePriorChild = projectedPriorSlice[index]
			}
			if HasNewProjectedCredential(child, safeChild, priorChild, safePriorChild) {
				return true
			}
		}
		return false
	default:
		if reflect.DeepEqual(candidate, projectedCandidate) {
			return false
		}
		return reflect.DeepEqual(prior, projectedPrior) || !reflect.DeepEqual(candidate, prior)
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
	if _, review := stepReviewMetadataKeys[key]; !review {
		return false
	}
	if nested {
		return true
	}
	return UnsentStepReviewMetadata(dsl.Action{
		Type:     StringJSONValue(action["type"]),
		Metadata: map[string]any{key: leaf},
	}, key)
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
	candidateMap, candidateOK := projectedCandidate.(map[string]any)
	priorMap, priorOK := projectedPrior.(map[string]any)
	if !candidateOK || !priorOK {
		return "script", true
	}
	candidateActions, candidateOrder := ImportActionContexts(candidateMap)
	priorActions, priorOrder := ImportActionContexts(priorMap)
	candidateTransport := ImportTransportContext(candidateMap)
	priorTransport := ImportTransportContext(priorMap)
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

	var visit func(
		raw,
		safe,
		rawPrior,
		safePrior any,
		path,
		actionPath string,
	) (string, bool)
	visit = func(
		raw,
		safe,
		rawPrior,
		safePrior any,
		path,
		actionPath string,
	) (string, bool) {
		// Review evidence has no execution context to change. The new-credential
		// check has already compared it leaf by leaf.
		if _, evidence := importReviewEvidencePreservedPaths[path]; evidence {
			return "", false
		}
		if _, isAction := candidateActions[path]; isAction {
			actionPath = path
		}
		switch typed := raw.(type) {
		case map[string]any:
			safeMap, _ := safe.(map[string]any)
			priorMap, _ := rawPrior.(map[string]any)
			safePriorMap, _ := safePrior.(map[string]any)
			for key, child := range typed {
				if context, changed := visit(
					child,
					safeMap[key],
					priorMap[key],
					safePriorMap[key],
					AppendImportContextPath(path, key),
					actionPath,
				); changed {
					return context, true
				}
			}
		case []any:
			safeSlice, _ := safe.([]any)
			priorSlice, _ := rawPrior.([]any)
			safePriorSlice, _ := safePrior.([]any)
			for index, child := range typed {
				var safeChild, priorChild, safePriorChild any
				if index < len(safeSlice) {
					safeChild = safeSlice[index]
				}
				if index < len(priorSlice) {
					priorChild = priorSlice[index]
				}
				if index < len(safePriorSlice) {
					safePriorChild = safePriorSlice[index]
				}
				if context, changed := visit(
					child,
					safeChild,
					priorChild,
					safePriorChild,
					AppendImportContextPath(path, strconv.Itoa(index)),
					actionPath,
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
					return "script", true
				}
				return "", false
			}
			candidateAction, candidateExists := candidateActions[actionPath]
			priorAction, priorExists := priorActions[actionPath]
			if !candidateExists || !priorExists ||
				!reflect.DeepEqual(candidateTransport, priorTransport) ||
				!reflect.DeepEqual(candidateAction, priorAction) ||
				references.requestChanged(actionPath) {
				return ImportContextDisplayPath(actionPath), true
			}
		}
		return "", false
	}

	return visit(candidate, projectedCandidate, prior, projectedPrior, "", "")
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
		default:
			transport[key] = value
		}
	}
	context["config"] = transport
	return context
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
		"parameters":   script["parameters"],
		"data_sources": script["data_sources"],
	}
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
			"waitfor", "sync", "capture", "metadata", "control":
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

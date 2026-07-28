package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/storage"
)

// restoreServerOwnedImportSecurityMetadata keeps importer-owned runtime
// artifacts, opaque repair payloads, and disclosure/redaction locators across
// an ordinary masked read-modify-write. They are deliberately visible (but
// value-free) in a disclosure projection so editors can round-trip them, yet
// clients must not be able to delete or rewrite them and thereby expose or
// destroy a raw artifact, repair source, parameter table, or retained request
// credential.
//
// Trusted importer updates bypass this helper and may replace the locator set
// with a newly projected source revision.
func restoreServerOwnedImportSecurityMetadata(candidate, prior *dsl.Script) error {
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
		dsl.ImportPreservedEnterpriseJavaVMSourceKey,
		dsl.ImportPreservedEnterpriseJavaJDBCSourceKey,
		storage.ImportSourceCommitFenceMetadataKey,
		"recorded_input_secret_columns",
	} {
		var priorValue any
		hasPriorValue := false
		if prior.Metadata != nil && prior.Metadata.Import != nil &&
			prior.Metadata.Import.Preserved != nil {
			priorValue, hasPriorValue = prior.Metadata.Import.Preserved[key]
		}
		if hasPriorValue {
			cloned, err := cloneImportSecurityMetadata(priorValue)
			if err != nil {
				return err
			}
			ensureScriptImportPreserved(
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
		cloned, err := cloneImportSecurityMetadata(raw)
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

func cloneImportSecurityMetadata(value any) (any, error) {
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

// validatePreservedImportCredentialEdits permits ordinary edits to keep only
// credential values already present in the durable imported script. A client
// may return either the exact raw value it received at import time or the
// disclosure-safe stored-script projection. Introducing a new literal requires
// a trusted import commit/session or an explicit runtime binding.
func validatePreservedImportCredentialEdits(candidate, prior *dsl.Script) error {
	if candidate == nil || prior == nil {
		return fmt.Errorf("credential-preserving update requires a durable prior script")
	}
	projectedCandidate := maskScriptSecrets(candidate)
	projectedPrior := maskScriptSecrets(prior)
	if projectedCandidate == nil || projectedPrior == nil {
		return fmt.Errorf("failed to validate credential-preserving update")
	}
	rawCandidate, err := scriptAsJSONValue(candidate)
	if err != nil {
		return err
	}
	safeCandidate, err := scriptAsJSONValue(projectedCandidate)
	if err != nil {
		return err
	}
	rawPrior, err := scriptAsJSONValue(prior)
	if err != nil {
		return err
	}
	safePrior, err := scriptAsJSONValue(projectedPrior)
	if err != nil {
		return err
	}
	if hasNewProjectedCredential(rawCandidate, safeCandidate, rawPrior, safePrior) {
		return fmt.Errorf("script introduces new executable credentials; bind a runtime secret or run the source import again")
	}
	if context, changed := retainedImportCredentialContextChange(
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

func hasNewProjectedCredential(candidate, projectedCandidate, prior, projectedPrior any) bool {
	switch typed := candidate.(type) {
	case map[string]any:
		projectedMap, _ := projectedCandidate.(map[string]any)
		priorMap, _ := prior.(map[string]any)
		projectedPriorMap, _ := projectedPrior.(map[string]any)
		for key, child := range typed {
			if hasNewProjectedCredential(
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
			if hasNewProjectedCredential(child, safeChild, priorChild, safePriorChild) {
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

// retainedImportCredentialContextChange prevents a masked read-modify-write
// from turning an already-authorized imported credential into a bearer
// capability for a different destination or request. A credential restored
// from the prior durable script remains usable only in the request-shaped
// surface where it was imported and only while the shared transport context is
// unchanged.
//
// Credential material outside a single request (for example default auth,
// proxy auth, or a credential-bearing data source) is necessarily script-wide,
// so it is bound to every request context in the script. The comparison uses
// disclosure-safe projections exclusively; errors therefore never include a
// source credential.
func retainedImportCredentialContextChange(
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
	candidateActions, candidateOrder := importActionContexts(candidateMap)
	priorActions, priorOrder := importActionContexts(priorMap)
	candidateTransport := importTransportContext(candidateMap)
	priorTransport := importTransportContext(priorMap)
	candidateGlobal := importGlobalCredentialContext(
		candidateMap,
		candidateTransport,
		candidateActions,
		candidateOrder,
	)
	priorGlobal := importGlobalCredentialContext(
		priorMap,
		priorTransport,
		priorActions,
		priorOrder,
	)

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
					appendImportContextPath(path, key),
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
					appendImportContextPath(path, strconv.Itoa(index)),
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
			if actionPath == "" {
				if !reflect.DeepEqual(candidateGlobal, priorGlobal) {
					return "script", true
				}
				return "", false
			}
			candidateAction, candidateExists := candidateActions[actionPath]
			priorAction, priorExists := priorActions[actionPath]
			if !candidateExists || !priorExists ||
				!reflect.DeepEqual(candidateTransport, priorTransport) ||
				!reflect.DeepEqual(candidateAction, priorAction) {
				return importContextDisplayPath(actionPath), true
			}
		}
		return "", false
	}

	return visit(candidate, projectedCandidate, prior, projectedPrior, "", "")
}

// importTransportContext returns request-affecting script configuration while
// leaving ordinary scheduling, pacing, logging, and result-policy edits free
// to round-trip. Values come from maskScriptSecrets, so credential bytes never
// enter the context or an error.
func importTransportContext(script map[string]any) map[string]any {
	context := map[string]any{
		"protocol": script["protocol"],
	}
	config, _ := script["config"].(map[string]any)
	if config == nil {
		return context
	}
	transport := make(map[string]any, len(config))
	for key, value := range config {
		switch normalizeScriptFieldName(key) {
		case "thinktime", "pacing", "errorhandling", "runlogic", "observability", "log":
			continue
		default:
			transport[key] = value
		}
	}
	context["config"] = transport
	return context
}

// importGlobalCredentialContext binds script-wide credentials to their data
// bindings and to every request-shaped surface that could consume them.
func importGlobalCredentialContext(
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

// importActionContexts indexes every regular action and HTTP-sequence child by
// its structural DSL path. The action context contains outbound/request
// semantics, excluding authoring labels, response processing, review metadata,
// and timing-only controls.
func importActionContexts(script map[string]any) (map[string]any, []string) {
	contexts := make(map[string]any)
	order := make([]string, 0)
	var walkActions func(any, string)
	var walkAction func(map[string]any, string)

	walkActions = func(raw any, path string) {
		actions, _ := raw.([]any)
		for index, value := range actions {
			action, _ := value.(map[string]any)
			if action == nil {
				continue
			}
			walkAction(action, appendImportContextPath(path, strconv.Itoa(index)))
		}
	}
	walkAction = func(action map[string]any, path string) {
		contexts[path] = importActionWireContext(action)
		order = append(order, path)

		control, _ := action["control"].(map[string]any)
		if control != nil {
			walkActions(control["steps"], appendImportContextPath(path, "control", "steps"))
			walkActions(control["else"], appendImportContextPath(path, "control", "else"))
		}
		if !strings.EqualFold(strings.TrimSpace(stringJSONValue(action["type"])), "http_sequence") {
			return
		}
		params, _ := action["params"].(map[string]any)
		if params != nil {
			walkActions(params["requests"], appendImportContextPath(path, "params", "requests"))
		}
	}

	for _, phase := range []string{"init", "actions", "teardown"} {
		walkActions(script[phase], "/"+phase)
	}
	return contexts, order
}

func importActionWireContext(action map[string]any) map[string]any {
	context := make(map[string]any, len(action))
	for key, value := range action {
		switch normalizeScriptFieldName(key) {
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
				if strings.EqualFold(strings.TrimSpace(stringJSONValue(action["type"])), "http_sequence") &&
					normalizeScriptFieldName(paramKey) == "requests" {
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

func appendImportContextPath(path string, segments ...string) string {
	for _, segment := range segments {
		if path == "" {
			path = "/" + segment
		} else {
			path += "/" + segment
		}
	}
	return path
}

func importContextDisplayPath(path string) string {
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

func stringJSONValue(value any) string {
	text, _ := value.(string)
	return text
}

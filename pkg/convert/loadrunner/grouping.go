package loadrunner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type loadRunnerPhaseCompaction struct {
	actions           []dsl.Action
	pathMap           map[string]string
	collapsedRequests int
	sequenceActions   int
}

func compactLoadRunnerHTTPActions(result *ConvertResult) {
	if result == nil || result.Script == nil {
		return
	}
	pathMap := make(map[string]string)
	init := compactLoadRunnerHTTPPhase(result.Script.Init, "init")
	main := compactLoadRunnerHTTPPhase(result.Script.Actions, "action")
	teardown := compactLoadRunnerHTTPPhase(result.Script.Teardown, "teardown")
	result.Script.Init = init.actions
	result.Script.Actions = main.actions
	result.Script.Teardown = teardown.actions
	mergePathMap(pathMap, init.pathMap)
	mergePathMap(pathMap, main.pathMap)
	mergePathMap(pathMap, teardown.pathMap)
	remapSourceReviewTargets(result, pathMap)

	result.Stats.CollapsedRequests += init.collapsedRequests + main.collapsedRequests + teardown.collapsedRequests
	result.Stats.LogicalActions = len(result.Script.Init) + len(result.Script.Actions) + len(result.Script.Teardown)
	result.Stats.ConvertedActions = result.Stats.LogicalActions
	if result.Stats.CollapsedRequests > 0 || result.Stats.TransactionMarkers > 0 {
		preserved := ensureLoadRunnerImportPreserved(result.Script)
		preserved["loadrunner_grouping"] = map[string]any{
			"mode":                "transaction_sequence",
			"logical_actions":     result.Stats.LogicalActions,
			"raw_request_actions": result.Stats.RawRequestActions,
			"collapsed_requests":  result.Stats.CollapsedRequests,
			"sequence_actions":    init.sequenceActions + main.sequenceActions + teardown.sequenceActions,
			"transaction_markers": result.Stats.TransactionMarkers,
		}
	}
}

func compactLoadRunnerHTTPPhase(actions []dsl.Action, phase string) loadRunnerPhaseCompaction {
	out := make([]dsl.Action, 0, len(actions))
	pathMap := make(map[string]string, len(actions))
	collapsed := 0
	sequences := 0
	for i := 0; i < len(actions); {
		action := actions[i]
		transaction := actionTransactionName(action)
		if transaction == "" || !isHTTPProtocolAction(action) {
			newIndex := len(out)
			out = append(out, action)
			pathMap[sourceReviewActionPath(phase, i)] = sourceReviewActionPath(phase, newIndex)
			i++
			continue
		}

		start := i
		group := []dsl.Action{action}
		i++
		for i < len(actions) && isHTTPProtocolAction(actions[i]) && actionTransactionName(actions[i]) == transaction {
			group = append(group, actions[i])
			i++
		}

		newIndex := len(out)
		if len(group) == 1 {
			out = append(out, group[0])
			pathMap[sourceReviewActionPath(phase, start)] = sourceReviewActionPath(phase, newIndex)
			continue
		}

		sequence := buildLoadRunnerHTTPSequence(transaction, group)
		out = append(out, sequence)
		sequencePath := sourceReviewActionPath(phase, newIndex)
		for offset := range group {
			oldPath := sourceReviewActionPath(phase, start+offset)
			pathMap[oldPath] = fmt.Sprintf("%s.params.requests[%d]", sequencePath, offset)
		}
		collapsed += len(group) - 1
		sequences++
	}
	return loadRunnerPhaseCompaction{
		actions:           out,
		pathMap:           pathMap,
		collapsedRequests: collapsed,
		sequenceActions:   sequences,
	}
}

func buildLoadRunnerHTTPSequence(transaction string, requests []dsl.Action) dsl.Action {
	first := requests[0]
	metadata := cloneLoadRunnerAnyMap(first.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["loadrunner_transaction"] = transaction
	metadata["source_transaction"] = transaction
	metadata["loadrunner_request_count"] = len(requests)
	if subtransactions := loadRunnerSubtransactions(requests); len(subtransactions) > 0 {
		metadata["loadrunner_subtransactions"] = subtransactions
	}

	children := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		children = append(children, loadRunnerSequenceChild(request))
	}

	return dsl.Action{
		Name:           sanitizeName(firstNonEmpty(transaction, first.Name, "loadrunner_transaction")),
		Type:           "http_sequence",
		Method:         "SEQUENCE",
		Target:         first.Target,
		Params:         map[string]any{"requests": children},
		Metadata:       metadata,
		ThinkTime:      first.ThinkTime,
		ThinkTimeAfter: first.ThinkTimeAfter,
	}
}

func loadRunnerSequenceChild(action dsl.Action) map[string]any {
	child := map[string]any{
		"name":   action.Name,
		"type":   action.Type,
		"method": action.Method,
		"target": action.Target,
	}
	if len(action.Headers) > 0 {
		child["headers"] = cloneLoadRunnerStringMap(action.Headers)
	}
	if action.Body != "" {
		child["body"] = action.Body
	}
	if action.BodyEncoding != "" {
		child["body_encoding"] = action.BodyEncoding
	}
	if len(action.Params) > 0 {
		child["params"] = cloneLoadRunnerAnyMap(action.Params)
	}
	if len(action.Metadata) > 0 {
		child["metadata"] = cloneLoadRunnerAnyMap(action.Metadata)
	}
	if len(action.Extractors) > 0 {
		child["extractors"] = action.Extractors
	}
	if len(action.Assertions) > 0 {
		child["assertions"] = action.Assertions
	}
	if action.TimeoutMs > 0 {
		child["timeout_ms"] = action.TimeoutMs
	}
	if action.MaxRedirects != nil {
		child["max_redirects"] = *action.MaxRedirects
	}
	if action.Auth != nil {
		child["auth"] = action.Auth
	}
	if action.SOAP != nil {
		child["soap"] = action.SOAP
	}
	return child
}

func actionTransactionName(action dsl.Action) string {
	if value := metadataString(action.Metadata, "loadrunner_transaction"); value != "" {
		return value
	}
	return metadataString(action.Metadata, "source_transaction")
}

func isHTTPProtocolAction(action dsl.Action) bool {
	actionType := strings.ToUpper(strings.TrimSpace(firstNonEmpty(action.Method, action.Type)))
	switch actionType {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return strings.TrimSpace(action.Target) != ""
	default:
		return false
	}
}

func loadRunnerSubtransactions(actions []dsl.Action) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, action := range actions {
		value := metadataString(action.Metadata, "loadrunner_subtransaction")
		if value == "" {
			value = metadataString(action.Metadata, "source_subtransaction")
		}
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func remapSourceReviewTargets(result *ConvertResult, pathMap map[string]string) {
	if result == nil || result.SourceReview == nil || len(pathMap) == 0 {
		return
	}
	keys := make([]string, 0, len(pathMap))
	for key := range pathMap {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})
	for index := range result.SourceReview.Mappings {
		target := result.SourceReview.Mappings[index].TargetPath
		if target == "" {
			continue
		}
		for _, oldPath := range keys {
			newPath := pathMap[oldPath]
			if target == oldPath {
				result.SourceReview.Mappings[index].TargetPath = newPath
				break
			}
			if strings.HasPrefix(target, oldPath+".") {
				result.SourceReview.Mappings[index].TargetPath = newPath + strings.TrimPrefix(target, oldPath)
				break
			}
		}
	}
}

func mergePathMap(target map[string]string, source map[string]string) {
	for key, value := range source {
		target[key] = value
	}
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func cloneLoadRunnerStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneLoadRunnerAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

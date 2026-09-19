package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type sourcePhaseCompaction struct {
	actions           []dsl.Action
	pathMap           map[string]string
	collapsedRequests int
	sequenceActions   int
}

func compactSourceHTTPActions(result *ConvertResult) {
	if result == nil || result.Script == nil {
		return
	}
	pathMap := make(map[string]string)
	init := compactSourceHTTPPhase(result.Script.Init, "init")
	main := compactSourceHTTPPhase(result.Script.Actions, "action")
	teardown := compactSourceHTTPPhase(result.Script.Teardown, "teardown")
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
		preserved := ensureEnterpriseArchiveImportPreserved(result.Script)
		preserved["source_grouping"] = map[string]any{
			"mode":                "transaction_sequence",
			"logical_actions":     result.Stats.LogicalActions,
			"raw_request_actions": result.Stats.RawRequestActions,
			"collapsed_requests":  result.Stats.CollapsedRequests,
			"sequence_actions":    init.sequenceActions + main.sequenceActions + teardown.sequenceActions,
			"transaction_markers": result.Stats.TransactionMarkers,
		}
	}
}

func compactSourceHTTPPhase(actions []dsl.Action, phase string) sourcePhaseCompaction {
	return compactSourceHTTPList(actions, func(i int) string { return sourceReviewActionPath(phase, i) })
}

// compactSourceHTTPList collapses consecutive same-transaction requests in one
// action list. pathFor maps a list index to its source-review path, which lets
// the compaction recurse into control_if / control_loop bodies produced by the
// control-flow structuring pass.
func compactSourceHTTPList(actions []dsl.Action, pathFor func(int) string) sourcePhaseCompaction {
	out := make([]dsl.Action, 0, len(actions))
	pathMap := make(map[string]string, len(actions))
	collapsed := 0
	sequences := 0
	for i := 0; i < len(actions); {
		action := actions[i]
		transaction := actionTransactionName(action)
		if transaction == "" || !isHTTPProtocolAction(action) {
			newIndex := len(out)
			oldPath := pathFor(i)
			newPath := pathFor(newIndex)
			pathMap[oldPath] = newPath
			if action.Control != nil && (action.Type == "control_if" || action.Type == "control_loop") {
				action = compactSourceControlAction(action, oldPath, newPath, pathMap, &collapsed, &sequences)
			}
			out = append(out, action)
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
			pathMap[pathFor(start)] = pathFor(newIndex)
			continue
		}

		sequence := buildSourceHTTPSequence(transaction, group)
		out = append(out, sequence)
		sequencePath := pathFor(newIndex)
		for offset := range group {
			oldPath := pathFor(start + offset)
			pathMap[oldPath] = fmt.Sprintf("%s.params.requests[%d]", sequencePath, offset)
		}
		collapsed += len(group) - 1
		sequences++
	}
	return sourcePhaseCompaction{
		actions:           out,
		pathMap:           pathMap,
		collapsedRequests: collapsed,
		sequenceActions:   sequences,
	}
}

// compactSourceControlAction compacts the request runs nested inside a control
// block's steps/else bodies, composing review paths through the block.
func compactSourceControlAction(action dsl.Action, oldPath, newPath string, pathMap map[string]string, collapsed *int, sequences *int) dsl.Action {
	control := *action.Control
	if len(control.Steps) > 0 {
		inner := compactSourceHTTPList(control.Steps, func(i int) string {
			return fmt.Sprintf("%s.control.steps[%d]", oldPath, i)
		})
		control.Steps = inner.actions
		rebaseCompactionPathMap(pathMap, inner.pathMap, oldPath, newPath)
		*collapsed += inner.collapsedRequests
		*sequences += inner.sequenceActions
	}
	if len(control.Else) > 0 {
		inner := compactSourceHTTPList(control.Else, func(i int) string {
			return fmt.Sprintf("%s.control.else[%d]", oldPath, i)
		})
		control.Else = inner.actions
		rebaseCompactionPathMap(pathMap, inner.pathMap, oldPath, newPath)
		*collapsed += inner.collapsedRequests
		*sequences += inner.sequenceActions
	}
	action.Control = &control
	return action
}

// rebaseCompactionPathMap merges a nested compaction's path map, rewriting the
// new-path side onto the parent's final position.
func rebaseCompactionPathMap(target map[string]string, nested map[string]string, oldBase, newBase string) {
	for from, to := range nested {
		if oldBase != newBase && strings.HasPrefix(to, oldBase) {
			to = newBase + strings.TrimPrefix(to, oldBase)
		}
		target[from] = to
	}
}

func buildSourceHTTPSequence(transaction string, requests []dsl.Action) dsl.Action {
	first := requests[0]
	metadata := cloneSourceAnyMap(first.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	for _, key := range []string{"execution_blocker", "execution_blockers", "import_review", "review_required", "promotion_blocker", "review_gate", "review_reasons", "masked_body", "body_gated_by_secret", "source_secret_ref", "source_secret_function", "restored_body_protected"} {
		delete(metadata, key)
	}
	metadata["source_transaction"] = transaction
	metadata["source_request_count"] = len(requests)
	if subtransactions := sourceSubtransactions(requests); len(subtransactions) > 0 {
		metadata["source_subtransactions"] = subtransactions
	}

	children := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		children = append(children, sourceSequenceChild(request))
	}

	return dsl.Action{
		Name:     sanitizeName(convertutil.FirstNonEmpty(transaction, first.Name, "source_transaction")),
		Type:     "http_sequence",
		Method:   "SEQUENCE",
		Target:   first.Target,
		Params:   map[string]any{"requests": children},
		Metadata: metadata,
	}
}

func sourceSequenceChild(action dsl.Action) map[string]any {
	child := map[string]any{
		"name":   action.Name,
		"type":   action.Type,
		"method": action.Method,
		"target": action.Target,
	}
	if len(action.HeaderList) > 0 {
		child["header_list"] = append(dsl.HeaderList(nil), action.HeaderList...)
	}
	if headers := sourceSequenceCompatibilityHeaders(action.HeaderList, action.Headers); len(headers) > 0 {
		child["headers"] = headers
	}
	if action.Body != "" {
		child["body"] = action.Body
	}
	if action.BodyEncoding != "" {
		child["body_encoding"] = action.BodyEncoding
	}
	if len(action.Params) > 0 {
		child["params"] = cloneSourceAnyMap(action.Params)
	}
	if len(action.Metadata) > 0 {
		child["metadata"] = cloneSourceAnyMap(action.Metadata)
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
	if action.ThinkTime.Type != "" {
		child["think_time"] = action.ThinkTime
	}
	if action.ThinkTimeAfter {
		child["think_time_after"] = true
	}
	if action.SOAP != nil {
		child["soap"] = action.SOAP
	}
	return child
}

// sourceSequenceCompatibilityHeaders derives the legacy single-value map from
// the authoritative ordered list. Map-only names are retained for older source
// adapters, but they cannot shadow an identity represented by HeaderList.
func sourceSequenceCompatibilityHeaders(list dsl.HeaderList, legacy map[string]string) map[string]string {
	out := list.ToMap()
	if out == nil && len(legacy) > 0 {
		out = make(map[string]string, len(legacy))
	}
	listNames := make(map[string]struct{}, len(list))
	for _, header := range list {
		name := strings.TrimSpace(header.Name)
		if name != "" {
			listNames[strings.ToLower(name)] = struct{}{}
		}
	}
	for name, value := range legacy {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, authoritative := listNames[strings.ToLower(trimmed)]; authoritative {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func actionTransactionName(action dsl.Action) string {
	return metadataString(action.Metadata, "source_transaction")
}

func isHTTPProtocolAction(action dsl.Action) bool {
	actionType := strings.ToUpper(strings.TrimSpace(convertutil.FirstNonEmpty(action.Method, action.Type)))
	switch actionType {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return strings.TrimSpace(action.Target) != ""
	default:
		return false
	}
}

func sourceSubtransactions(actions []dsl.Action) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, action := range actions {
		value := metadataString(action.Metadata, "source_subtransaction")
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
	for index := range result.SourceReview.Mappings {
		target := result.SourceReview.Mappings[index].TargetPath
		if target == "" {
			continue
		}
		if newPath, ok := pathMap[target]; ok {
			result.SourceReview.Mappings[index].TargetPath = newPath
			continue
		}
		for dot := strings.LastIndexByte(target, '.'); dot > 0; dot = strings.LastIndexByte(target[:dot], '.') {
			oldPath := target[:dot]
			if newPath, ok := pathMap[oldPath]; ok {
				result.SourceReview.Mappings[index].TargetPath = newPath + target[dot:]
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

func metadataBool(metadata map[string]any, key string) bool {
	value, _ := metadata[key].(bool)
	return value
}

func cloneSourceStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSourceAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

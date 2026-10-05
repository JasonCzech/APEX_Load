package enterprisearchive

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

const sourceConcurrentMarkerKey = "__source_concurrent_marker"
const sourceConcurrentTransactionKey = "__source_concurrent_transaction"
const maxSourceConcurrencyWork = 64 << 20
const maxSourceConcurrencyDetails = 1024

var sourceAuraTemplatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// Plans are private source identities. No timestamps, action names, or inferred
// transaction membership can create a concurrent group.
type sourceConcurrentMarker struct {
	fn       ParsedFunction
	group    int
	start    bool
	requests int
	reason   string
	// A start marker tracks per-request transaction wrappers: VuGen can wrap
	// each request of a group in its own lr_start/lr_end_transaction pair.
	wrapper         string // open wrapper transaction name
	wrapperOpen     bool
	wrapperRequests int
	wrapped         int   // requests inside a closed wrapper
	wrapperMarkers  []int // function indices of the wrapper pairs
	// requestTransactions marks a group whose every request has its own
	// transaction; it records those transactions instead of an aggregate.
	requestTransactions bool
}
type sourceConcurrentPlan struct {
	markers        map[int]*sourceConcurrentMarker
	wrapperMarkers map[int]bool // folded per-request transaction calls
	// requestTransactionGroups counts converted groups that record a
	// transaction per request.
	requestTransactionGroups int
	groups                   int
	native                   int
	review                   int
	markerCount              int
	omittedDetails           int
	limited                  bool
	workRemaining            int
}

func planSourceConcurrentGroups(functions []ParsedFunction, actions []*ParsedAction) *sourceConcurrentPlan {
	plan := &sourceConcurrentPlan{markers: make(map[int]*sourceConcurrentMarker), wrapperMarkers: make(map[int]bool), workRemaining: maxSourceConcurrencyWork}
	unsafeFiles := map[string]bool{}
	for _, action := range actions {
		if action != nil && len(action.ParseFailures) > 0 {
			unsafeFiles[action.Filename] = true
		}
	}
	active := -1
	depth := 0
	work := 0
	for index, fn := range functions {
		marker := fn.Name == "web_concurrent_start" || fn.Name == "web_concurrent_end"
		if marker {
			plan.markerCount++
			if fn.Name == "web_concurrent_start" {
				plan.groups++
			}
		}
		if index >= dsl.MaxScriptActionDefinitions || work >= maxSourceConcurrencyWork {
			plan.limited = true
			continue
		}
		cost := sourceConcurrentScopeCost(fn)
		if cost < 0 || cost > maxSourceConcurrencyWork-work {
			plan.limited = true
			cost = 0
		}
		work += cost
		if marker {
			info := &sourceConcurrentMarker{fn: fn, start: fn.Name == "web_concurrent_start", reason: "unmatched_marker"}
			plan.markers[index] = info
			if info.start {
				if active >= 0 {
					plan.markers[active].reason = "nested_group"
					info.reason = "nested_group"
					depth++
					continue
				}
				active = index
				depth = 1
				info.group = plan.groups
				info.reason = ""
				if plan.limited {
					info.reason = "inspection_limit"
				} else if unsafeFiles[fn.Filename] {
					info.reason = "source_parse_review"
				} else if !sourceConcurrentMarkerArgs(fn) {
					info.reason = "marker_arguments"
				} else if !sourceConcurrentScopeSupported(fn) {
					info.reason = "source_scope_review"
				}
				continue
			}
			if active < 0 {
				continue
			}
			start := plan.markers[active]
			if depth > 1 {
				info.reason = "nested_group"
				depth--
				continue
			}
			info.group = start.group
			info.requests = start.requests
			if start.reason == "" {
				if plan.limited {
					start.reason = "inspection_limit"
				} else if !sourceConcurrentSameScope(start.fn, fn) {
					start.reason = "cross_source_scope"
				} else if !sourceConcurrentMarkerArgs(fn) {
					start.reason = "marker_arguments"
				} else if start.requests == 0 {
					start.reason = "empty_group"
				} else if start.requests > dsl.MaxHTTPConcurrentChildren {
					start.reason = "child_limit"
				} else if start.wrapperOpen || start.wrapped != 0 && start.wrapped != start.requests {
					start.reason = "transaction_wrapper_review"
				}
			}
			if start.reason == "" && start.wrapped != 0 {
				start.requestTransactions = true
				for _, marker := range start.wrapperMarkers {
					plan.wrapperMarkers[marker] = true
				}
			}
			info.reason = start.reason
			active = -1
			depth = 0
			continue
		}
		if active < 0 {
			continue
		}
		start := plan.markers[active]
		if sourceConcurrentRequestFunction(fn.Name) {
			start.requests++
		}
		if start.reason != "" {
			continue
		}
		if plan.limited {
			start.reason = "inspection_limit"
		} else if unsafeFiles[fn.Filename] || sourceFunctionHasUnsupportedTransactionDependency(fn) {
			start.reason = "source_parse_review"
		} else if !sourceConcurrentSameScope(start.fn, fn) {
			start.reason = "cross_source_scope"
		} else if fn.Name == "lr_start_transaction" || fn.Name == "lr_end_transaction" {
			start.reason = trackSourceConcurrentTransactionWrapper(start, fn, index)
		} else if sourceConcurrentRequestFunction(fn.Name) {
			if start.wrapperOpen {
				start.wrapperRequests++
			}
		} else if !sourceConcurrentContributorFunction(fn.Name) {
			start.reason = "source_operation_barrier"
		} else if start.wrapperOpen && start.wrapperRequests > 0 {
			// A registration after the wrapped request belongs to the next one.
			start.reason = "transaction_wrapper_review"
		}
		if start.reason == "" && start.wrapperRequests > 1 {
			start.reason = "transaction_wrapper_review"
		}
	}
	if active >= 0 {
		plan.markers[active].reason = "missing_end"
	}
	plan.workRemaining = max(0, maxSourceConcurrencyWork-work)
	// A paired start needs its final count; unsupported nested starts/endpoints
	// remain review even when another group's end happens to be nearby.
	return plan
}

// trackSourceConcurrentTransactionWrapper accepts a transaction pair that
// wraps exactly one request of the group and ends with LR_AUTO, so its status
// is the request's own outcome. Nested, unnamed, mismatched or explicitly
// statused pairs remain review cases.
func trackSourceConcurrentTransactionWrapper(start *sourceConcurrentMarker, fn ParsedFunction, index int) string {
	name := convertutil.FirstNonEmpty(sourceControlArgAt(fn, 0), fn.StepName)
	if strings.TrimSpace(name) == "" || len(name) > 4096 {
		return "transaction_wrapper_review"
	}
	if fn.Name == "lr_start_transaction" {
		if start.wrapperOpen || len(sourceControlArgs(fn)) > 1 {
			return "transaction_wrapper_review"
		}
		start.wrapper, start.wrapperOpen, start.wrapperRequests = name, true, 0
		start.wrapperMarkers = append(start.wrapperMarkers, index)
		return ""
	}
	if !start.wrapperOpen || name != start.wrapper || start.wrapperRequests != 1 || len(sourceControlArgs(fn)) != 2 || strings.TrimSpace(sourceControlArgAt(fn, 1)) != "LR_AUTO" {
		return "transaction_wrapper_review"
	}
	start.wrapper, start.wrapperOpen, start.wrapperRequests = "", false, 0
	start.wrapped++
	start.wrapperMarkers = append(start.wrapperMarkers, index)
	return ""
}

func sourceConcurrentScopeCost(fn ParsedFunction) int {
	cost := len(fn.Filename) + len(fn.sourceFunction)
	if fn.Name == "web_concurrent_start" || fn.Name == "web_concurrent_end" {
		if len(fn.CallArgs) > 1 || len(fn.RawArgs) > 1 || len(fn.Args) > 2 || len(fn.StepName) > 4096 {
			return -1
		}
		cost += len(fn.StepName)
		for key, value := range fn.Args {
			if len(key) > 32 || len(value) > 4096 {
				return -1
			}
			cost += len(key) + len(value)
		}
		for _, arg := range fn.CallArgs {
			if len(arg.Raw) > 4096 {
				return -1
			}
			cost += len(arg.Raw)
		}
		for _, arg := range fn.RawArgs {
			if len(arg) > 4096 {
				return -1
			}
			cost += len(arg)
		}
	}
	if len(fn.Filename) > 4096 || len(fn.sourceFunction) > 256 || len(fn.ConditionIDs) > 32 || len(fn.LoopIDs) > 32 || len(fn.Conditions) > 32 || len(fn.Loops) > 32 {
		return -1
	}
	for _, list := range [][]string{fn.ConditionIDs, fn.LoopIDs, fn.Conditions, fn.Loops} {
		for _, value := range list {
			if len(value) > 4096 {
				return -1
			}
			cost += len(value)
		}
	}
	return cost
}
func sourceConcurrentSameScope(a, b ParsedFunction) bool {
	return a.Filename == b.Filename && normalizeSourcePhase(a.Phase) == normalizeSourcePhase(b.Phase) && a.sourceFunction != "" && a.sourceFunction == b.sourceFunction && slices.Equal(a.ConditionIDs, b.ConditionIDs) && slices.Equal(a.LoopIDs, b.LoopIDs)
}
func sourceConcurrentScopeSupported(fn ParsedFunction) bool {
	if fn.sourceFunction == "" || sourceFunctionHasUnsupportedTransactionDependency(fn) || len(fn.ConditionIDs) != len(fn.Conditions) || len(fn.LoopIDs) != len(fn.Loops) {
		return false
	}
	if len(fn.Conditions) > 0 {
		expr, ok := translateSourceConditionChain(expandCLocalFrames(fn.Conditions, fn.cLocals))
		if !ok || dsl.ValidateConditionExpression(expr) != nil {
			return false
		}
	}
	if len(fn.Loops) > 1 {
		return false
	}
	if len(fn.Loops) == 1 {
		_, ok := translateSourceLoopHeader(expandCLocalReferences(fn.Loops[0], fn.cLocals))
		return ok
	}
	return true
}
func sourceConcurrentMarkerArgs(fn ParsedFunction) bool {
	if len(fn.CallArgs) == 1 {
		if len(fn.CallArgs[0].Raw) > 4096 {
			return false
		}
		return strings.TrimSpace(fn.CallArgs[0].Raw) == "NULL" || strings.TrimSpace(fn.CallArgs[0].Raw) == "0"
	}
	if len(fn.CallArgs) > 1 || len(fn.RawArgs) > 1 || len(fn.Args) > 2 {
		return false
	}
	for key, value := range fn.Args {
		if len(key) > 32 || len(value) > 4096 {
			return false
		}
	}
	for _, arg := range fn.RawArgs {
		if len(arg) > 4096 {
			return false
		}
	}
	if len(fn.StepName) > 4096 {
		return false
	}
	args := sourceControlArgs(fn)
	return len(args) == 1 && (args[0] == "NULL" || args[0] == "0")
}
func sourceConcurrentRequestFunction(name string) bool {
	switch name {
	case "web_url", "web_custom_request", "web_submit_data", "web_submit_form", "web_rest":
		return true
	}
	return false
}
func sourceConcurrentContributorFunction(name string) bool {
	switch name {
	case "web_reg_save_param", "web_reg_save_param_regexp", "web_reg_save_param_json", "web_reg_save_param_xpath", "web_reg_save_param_ex", "web_reg_save_param_attrib", "web_reg_find", "web_add_header":
		return true
	}
	return false
}

func appendSourceConcurrentMarker(result *ConvertResult, fn ParsedFunction, index int, plan *sourceConcurrentPlan) {
	info := plan.markers[index]
	reason := "inspection_limit"
	if info != nil {
		reason = info.reason
	}
	mode := SourceFunctionSupportExecutable
	action := dsl.Action{Name: fmt.Sprintf("source_concurrent_marker_%d", index+1), Type: "source_review", Params: map[string]any{"function": fn.Name}}
	notes := "Explicit source concurrent-group boundary; converted only after exact child and scope validation."
	if info == nil || reason != "" {
		mode = SourceFunctionSupportReviewOnly
		notes = "Concurrent marker requires review; no concurrent execution parity is claimed."
		gateSourceConcurrency(&action, fn, reason)
	}
	appendSourceAction(result, fn, action, mode, notes)
	emitted := actionAtPhaseIndex(result.Script, fn.Phase, actionCountForPhase(result.Script, fn.Phase)-1)
	if emitted != nil {
		if emitted.Metadata == nil {
			emitted.Metadata = map[string]any{}
		}
		emitted.Metadata[sourceConcurrentMarkerKey] = index
	}
	result.Stats.ContributorFunctions++
	if plan.markerCount <= maxSourceConcurrencyDetails || lenSourceConcurrencyDetails(result.Script) < maxSourceConcurrencyDetails {
		preserved := ensureEnterpriseArchiveImportPreserved(result.Script)
		entry := map[string]any{"function": fn.Name, "line": fn.LineNumber, "marker": fn.Name}
		values, _ := preserved["source_concurrent_groups"].([]map[string]any)
		preserved["source_concurrent_groups"] = append(values, entry)
	} else {
		plan.omittedDetails++
	}
}

// tagSourceConcurrentTransaction marks the action just emitted for a planned
// per-request transaction wrapper, so the group fold can turn the pair into its
// request's own transaction.
func tagSourceConcurrentTransaction(result *ConvertResult, fn ParsedFunction, index int, plan *sourceConcurrentPlan) {
	if plan == nil || !plan.wrapperMarkers[index] {
		return
	}
	emitted := actionAtPhaseIndex(result.Script, fn.Phase, actionCountForPhase(result.Script, fn.Phase)-1)
	if emitted == nil {
		return
	}
	if emitted.Metadata == nil {
		emitted.Metadata = map[string]any{}
	}
	emitted.Metadata[sourceConcurrentTransactionKey] = index
}

func sourceConcurrentTransactionAction(action dsl.Action) bool {
	_, ok := action.Metadata[sourceConcurrentTransactionKey].(int)
	return ok
}

func lenSourceConcurrencyDetails(script *dsl.Script) int {
	preserved := ensureEnterpriseArchiveImportPreserved(script)
	switch values := preserved["source_concurrent_groups"].(type) {
	case []map[string]any:
		return len(values)
	case []any:
		return len(values)
	}
	return 0
}
func gateSourceConcurrency(action *dsl.Action, fn ParsedFunction, reason string) {
	importreview.ApplyToAction(action, []importreview.Finding{{Code: "source_concurrent_group_requires_review", ReasonCode: reason, Element: fn.Name, Source: "enterprise_script_archive", Detail: "The source concurrent group cannot be reproduced safely; review its boundaries and request semantics.", Resolution: "repair_concurrent_source_group_and_reimport", Gate: importreview.GateExecution}})
}

func structureSourceConcurrentGroups(result *ConvertResult, plan *sourceConcurrentPlan) {
	if result == nil || result.Script == nil || plan.markerCount == 0 {
		return
	}
	paths := map[string]string{}
	foldedTransactions := map[string]bool{} // pre-fold paths of folded wrapper calls
	fold := func(actions []dsl.Action, phase string) []dsl.Action {
		out := make([]dsl.Action, 0, len(actions))
		for i := 0; i < len(actions); {
			markerIndex, marker := sourceConcurrentActionMarker(actions[i])
			info := plan.markers[markerIndex]
			if !marker || info == nil || !info.start || info.reason != "" {
				old := sourceReviewActionPath(phase, i)
				next := sourceReviewActionPath(phase, len(out))
				paths[old] = next
				action := actions[i]
				delete(action.Metadata, sourceConcurrentTransactionKey)
				if marker {
					delete(action.Metadata, sourceConcurrentMarkerKey)
					if info != nil && info.reason == "" {
						downgradeSourceConcurrentMarker(result, &action, info, "missing_start", old)
					}
				}
				out = append(out, action)
				i++
				continue
			}
			end := i + 1
			limit := dsl.MaxHTTPConcurrentChildren + 1
			if info.requestTransactions {
				limit = 3*dsl.MaxHTTPConcurrentChildren + 1 // each request between its wrapper pair
			}
			for end < len(actions) && end-i <= limit {
				if otherIndex, isMarker := sourceConcurrentActionMarker(actions[end]); isMarker {
					other := plan.markers[otherIndex]
					if other != nil && !other.start && other.group == info.group {
						break
					}
				}
				end++
			}
			reason := "missing_end"
			// Per-request transaction wrappers fold into their requests: each
			// child keeps its transaction name and records it at runtime.
			var children []dsl.Action
			childSourceIndex := make([]int, 0, end-i)
			if end < len(actions) && end-i <= limit {
				transaction := ""
				for index := i + 1; index < end; index++ {
					if sourceConcurrentTransactionAction(actions[index]) {
						continue
					}
					child := actions[index]
					if info.requestTransactions {
						transaction, _ = child.Metadata["source_transaction"].(string)
						if transaction == "" {
							reason = "transaction_wrapper_review"
							break
						}
					}
					children = append(children, child)
					childSourceIndex = append(childSourceIndex, index)
				}
				if reason == "missing_end" {
					reason = sourceConcurrentChildrenReason(result.Script, children, info.requests, plan)
				}
			}
			if reason != "" {
				old := sourceReviewActionPath(phase, i)
				paths[old] = sourceReviewActionPath(phase, len(out))
				action := actions[i]
				downgradeSourceConcurrentMarker(result, &action, info, reason, old)
				delete(action.Metadata, sourceConcurrentMarkerKey)
				out = append(out, action)
				i++
				continue
			}
			wrapperPath := sourceReviewActionPath(phase, len(out))
			children = append([]dsl.Action(nil), children...)
			for childIndex := range children {
				child := &children[childIndex]
				child.Metadata = cloneSourceAnyMap(child.Metadata)
				for _, key := range []string{sourceFlowConditionKey, sourceFlowConditionExprKey, sourceFlowConditionIDKey, sourceFlowLoopKey, sourceFlowLoopExprKey, sourceFlowLoopCountKey, sourceFlowLoopIDKey} {
					delete(child.Metadata, key)
				}
				if info.requestTransactions {
					child.Metadata[dsl.HTTPConcurrentRequestTransactionMetadataKey] = child.Metadata["source_transaction"]
				}
				paths[sourceReviewActionPath(phase, childSourceIndex[childIndex])] = fmt.Sprintf("%s.params.requests[%d]", wrapperPath, childIndex)
			}
			// A folded wrapper pair maps to the request it wraps: registrations
			// emit no action, so each pair is start, request, end in order.
			childAt := make(map[int]int, len(childSourceIndex))
			for childIndex, sourceIndex := range childSourceIndex {
				childAt[sourceIndex] = childIndex
			}
			for index := i + 1; index < end; index++ {
				if !sourceConcurrentTransactionAction(actions[index]) {
					continue
				}
				childIndex, wrapsNext := childAt[index+1]
				if !wrapsNext {
					childIndex = childAt[index-1]
				}
				old := sourceReviewActionPath(phase, index)
				paths[old] = fmt.Sprintf("%s.params.requests[%d]", wrapperPath, childIndex)
				foldedTransactions[old] = true
			}
			metadata := cloneSourceAnyMap(children[0].Metadata)
			if metadata == nil {
				metadata = map[string]any{}
			}
			if info.requestTransactions {
				// The group is not the first request's transaction.
				for _, key := range []string{"source_transaction", "source_subtransaction", "source_action_name", dsl.HTTPConcurrentRequestTransactionMetadataKey} {
					delete(metadata, key)
				}
			}
			for key, value := range actions[i].Metadata {
				if key != sourceConcurrentMarkerKey {
					metadata[key] = value
				}
			}
			metadata["source_concurrent_group"] = info.group
			metadata["source_request_count"] = len(children)
			wrapper := dsl.Action{Name: fmt.Sprintf("source_concurrent_group_%d", info.group), Type: "http_sequence", Method: "SEQUENCE", Target: children[0].Target, Params: map[string]any{"requests": children, "execution_mode": dsl.HTTPSequenceConcurrent}, Metadata: metadata, Attribution: children[0].Attribution}
			_, work, admissionErr := dsl.HTTPConcurrentPreparationSourceSizeWithWorkLimit(plan.workRemaining, wrapper, result.Script.Config.HTTP.DefaultHeaders, result.Script.Config.HTTP.DefaultHeaderList)
			plan.workRemaining = max(0, plan.workRemaining-work)
			if admissionErr != nil {
				plan.limited = true
				plan.workRemaining = 0
				old := sourceReviewActionPath(phase, i)
				paths[old] = sourceReviewActionPath(phase, len(out))
				action := actions[i]
				downgradeSourceConcurrentMarker(result, &action, info, "preparation_limit", old)
				delete(action.Metadata, sourceConcurrentMarkerKey)
				out = append(out, action)
				i++
				continue
			}
			paths[sourceReviewActionPath(phase, i)] = wrapperPath
			paths[sourceReviewActionPath(phase, end)] = wrapperPath
			for mappingIndex := range result.SourceReview.Mappings {
				mapping := &result.SourceReview.Mappings[mappingIndex]
				if mapping.TargetPath == sourceReviewActionPath(phase, i) || mapping.TargetPath == sourceReviewActionPath(phase, end) {
					mapping.TargetName = wrapper.Name
				}
			}
			out = append(out, wrapper)
			plan.native++
			if info.requestTransactions {
				plan.requestTransactionGroups++
			}
			i = end + 1
		}
		return out
	}
	result.Script.Init = fold(result.Script.Init, "init")
	result.Script.Actions = fold(result.Script.Actions, "action")
	result.Script.Teardown = fold(result.Script.Teardown, "teardown")
	if len(foldedTransactions) > 0 {
		for index := range result.SourceReview.Mappings {
			if mapping := &result.SourceReview.Mappings[index]; foldedTransactions[mapping.TargetPath] {
				mapping.Notes = "Folded into the concurrent request's own transaction, timed by that request."
			}
		}
	}
	remapSourceReviewTargets(result, paths)
	plan.review = plan.groups - plan.native
	preserved := ensureEnterpriseArchiveImportPreserved(result.Script)
	preserved["source_concurrency"] = map[string]any{"groups": plan.groups, "native_groups": plan.native, "review_groups": plan.review, "markers": plan.markerCount, "details_omitted": plan.omittedDetails, "inspection_complete": !plan.limited}
	// Historical marker details describe the boundaries only; this summary is the
	// authoritative outcome. Never mark every preserved marker native wholesale.
	if plan.review > 0 || plan.markerCount != plan.native*2 || plan.limited {
		result.Warnings = append(result.Warnings, "Source concurrent groups include explicit review cases; no concurrent timing parity is claimed for those cases.")
	}
	if plan.requestTransactionGroups > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d source concurrent group(s) wrap each request in its own transaction. APEX times each transaction by its own request; LoadRunner runs a group's requests together at web_concurrent_end, so compare these transaction times with care.", plan.requestTransactionGroups))
	}
}
func sourceConcurrentActionMarker(action dsl.Action) (int, bool) {
	index, ok := action.Metadata[sourceConcurrentMarkerKey].(int)
	return index, ok
}
func downgradeSourceConcurrentMarker(result *ConvertResult, action *dsl.Action, info *sourceConcurrentMarker, reason, path string) {
	makeSourceReviewActionDisclosureSafe(action, info.fn, SourceFunctionSupportReviewOnly)
	gateSourceConcurrency(action, info.fn, reason)
	result.Stats.NativeFunctions--
	result.Stats.ReviewOnlyFunctions++
	for index := range result.SourceReview.Mappings {
		mapping := &result.SourceReview.Mappings[index]
		if mapping.TargetPath == path && mapping.Function == info.fn.Name {
			mapping.SupportMode = string(SourceFunctionSupportReviewOnly)
			mapping.Status = "review_only"
			mapping.Notes = "Concurrent marker requires review; no concurrent execution parity is claimed."
		}
	}
}
func sourceConcurrentChildrenReason(script *dsl.Script, children []dsl.Action, want int, plan *sourceConcurrentPlan) string {
	if script.Protocol != "http" {
		return "non_http_protocol"
	}
	if len(children) == 0 || len(children) != want {
		return "request_count_mismatch"
	}
	if len(children) > dsl.MaxHTTPConcurrentChildren {
		return "child_limit"
	}
	if plan.workRemaining <= 0 {
		plan.limited = true
		return "inspection_limit"
	}
	_, sizeWork, err := dsl.HTTPConcurrentPreparationSourceSizeWithWorkLimit(plan.workRemaining, children, children[0].Metadata, children[0].Target, script.Config.HTTP.DefaultHeaders, script.Config.HTTP.DefaultHeaderList)
	plan.workRemaining = max(0, plan.workRemaining-sizeWork)
	// A failed admission cannot recycle the attempted group allowance.
	if err != nil {
		plan.workRemaining = 0
		plan.limited = true
		return "preparation_limit"
	}
	work, err := dsl.ValidateHTTPConcurrentChildrenWithWorkLimit(children, plan.workRemaining)
	plan.workRemaining = max(0, plan.workRemaining-work)
	if err != nil {
		if plan.workRemaining == 0 {
			plan.limited = true
		}
		return "child_semantics_review"
	}
	if reason := sourceConcurrentStructuredChildrenReason(children, plan); reason != "" {
		return reason
	}
	names := map[string]bool{}
	for _, child := range children {
		if dsl.ValidateHTTPConcurrentChild(child) != nil {
			return "non_http_child"
		}
		for _, key := range []string{"execution_blocker", "execution_blockers", "import_review", "masked_body", "body_gated_by_secret", "review_required"} {
			if value, exists := child.Metadata[key]; exists && value != nil && value != false && value != "" {
				return "request_review_gate"
			}
		}
		think := child.ThinkTime
		if think.MeanMs != 0 || think.StdMs != 0 || think.MinMs != 0 || think.MaxMs != 0 || think.MinPercent != 0 || think.MaxPercent != 0 {
			return "child_think_time"
		}
		for _, extractor := range child.Extractors {
			name := strings.TrimSpace(extractor.Name)
			if name == "" {
				continue
			}
			if names[name] {
				return "extractor_conflict"
			}
			names[name] = true
		}
	}
	return ""
}

// Only an authored runtime codec annotation enables structural inspection.
// Ordinary JSON-looking bodies retain their established literal semantics.
func sourceConcurrentStructuredChildrenReason(children []dsl.Action, plan *sourceConcurrentPlan) string {
	outputs := make(map[string]string)
	for _, child := range children {
		for _, extractor := range child.Extractors {
			outputs[extractor.Name] = extractor.Mode
		}
	}
	remainingTemplates := aurapayload.MaxTemplateOccurrences
	for _, child := range children {
		policy, _ := child.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey].(string)
		if strings.TrimSpace(policy) != dsl.ImportAuraJSONTemplateValueEncoding {
			continue
		}
		codecAllowance := min(aurapayload.MaxWorkBytes, plan.workRemaining/2)
		if codecAllowance <= 0 {
			plan.limited = true
			return "inspection_limit"
		}
		scannerAllowance := plan.workRemaining - codecAllowance
		scanWork := 0
		var scanErr error
		_, _, codecWork, codecErr := aurapayload.ResolveContextualWithWorkAccounting(child.Body, child.BodyEncoding, child.Metadata[dsl.ImportAuraTemplateContractMetadataKey], func(value string, templateContext aurapayload.TemplateContext) (string, string) {
			if scanErr == nil {
				if len(value) > scannerAllowance-scanWork {
					scanErr = &aurapayload.Error{Code: aurapayload.WorkLimit}
					return value, value
				}
				scanWork += len(value)
				matches := sourceAuraTemplatePattern.FindAllStringIndex(value, remainingTemplates+1)
				if len(matches) > remainingTemplates {
					scanErr = &aurapayload.Error{Code: aurapayload.TokenLimit}
					return value, value
				}
				var scanBytes []byte
				for index, match := range matches {
					if !templateContext.IsLiteralToken(index) {
						continue
					}
					remainingTemplates--
					if scanBytes == nil {
						if len(value) > (scannerAllowance-scanWork)/2 {
							scanErr = &aurapayload.Error{Code: aurapayload.WorkLimit}
							return value, value
						}
						scanWork += 2 * len(value)
						scanBytes = []byte(value)
					}
					for i := match[0]; i < match[1]; i++ {
						scanBytes[i] = ' '
					}
				}
				scanValue := value
				if scanBytes != nil {
					scanValue = string(scanBytes)
				}
				var charged int
				charged, scanErr = dsl.HTTPConcurrentTemplateDependency(scanValue, outputs, &remainingTemplates, scannerAllowance-scanWork)
				scanWork += charged
			}
			return value, value
		}, codecAllowance)
		plan.workRemaining = max(0, plan.workRemaining-codecWork-scanWork)
		if codecErr != nil || scanErr != nil {
			if plan.workRemaining == 0 {
				plan.limited = true
			}
			return "structured_body_review"
		}
	}
	return ""
}

// Final naming is positional even when nested action names share a namespace.
// Both source boundaries refer to the same exact imported group wrapper.
func reconcileSourceConcurrentMarkerNames(result *ConvertResult) {
	if result == nil || result.Script == nil || result.SourceReview == nil {
		return
	}
	names := map[string]string{}
	err := dslwalk.WalkActions(result.Script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Type == "http_sequence" && ref.Action.Metadata["source_concurrent_group"] != nil {
			names[ref.Path] = ref.Action.Name
		}
		return nil
	})
	if err != nil {
		result.Warnings = append(result.Warnings, "Source concurrent-group destination naming is incomplete because script traversal exceeded its bound.")
		return
	}
	for index := range result.SourceReview.Mappings {
		mapping := &result.SourceReview.Mappings[index]
		if mapping.Function != "web_concurrent_start" && mapping.Function != "web_concurrent_end" {
			continue
		}
		if name, exists := names[mapping.TargetPath]; exists && mapping.SupportMode == string(SourceFunctionSupportExecutable) {
			mapping.TargetName = name
		}
	}
}

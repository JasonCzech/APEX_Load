package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Asynchronous conversations (web_reg_async_attributes before a step,
// web_stop_async, web_sync) become the engine's background HTTP
// conversations: the step the attributes apply to becomes an
// http_async_start whose params.request is the converted step.
const (
	// asyncCallbacksReasonCode: the conversation runs, but its C callbacks
	// (RequestCB, ResponseCB, ...) are not executed.
	asyncCallbacksReasonCode = "async_callbacks_not_executed"
	// defaultAsyncPollIntervalMs is used when a Poll conversation has no
	// PollIntervalMs.
	defaultAsyncPollIntervalMs = 1000
	// defaultWebSyncTimeoutMs is VuGen's default step timeout, used when
	// web_sync has no Timeout.
	defaultWebSyncTimeoutMs = 120000
)

var sourceAsyncPatterns = map[string]string{"poll": "poll", "longpoll": "long_poll", "long_poll": "long_poll", "push": "push"}

// sourceAsyncCallbackArgs are the callback attributes of
// web_reg_async_attributes; VuGen generates them into AsyncCallbacks.c.
var sourceAsyncCallbackArgs = []string{"RequestCB", "RequestBodyBufferCB", "ResponseCB", "ResponseBodyBufferCB", "ResponseHeadersCB"}

// sourceAsync is a web_reg_async_attributes registration waiting for the
// step it applies to.
type sourceAsync struct {
	fn        ParsedFunction
	id        string
	pattern   string
	url       string
	interval  int
	callbacks []string
}

// asyncCallArg returns a key=value argument of an async call (the key is
// matched case-insensitively).
func asyncCallArg(fn ParsedFunction, key string) string {
	for _, arg := range fn.CallArgs {
		if arg.Kind == "key_value" && strings.EqualFold(arg.Name, key) {
			return strings.TrimSpace(arg.Value)
		}
	}
	return ""
}

// parseSourceAsync reads web_reg_async_attributes. It returns a reason when
// the registration cannot become a conversation.
func parseSourceAsync(fn ParsedFunction) (*sourceAsync, string) {
	id := asyncCallArg(fn, "ID")
	if id == "" {
		return nil, "the registration has no ID"
	}
	rawPattern := asyncCallArg(fn, "Pattern")
	pattern, ok := sourceAsyncPatterns[strings.ToLower(rawPattern)]
	if !ok {
		return nil, fmt.Sprintf("Pattern=%s is not Poll, LongPoll or Push", rawPattern)
	}
	async := &sourceAsync{fn: fn, id: id, pattern: pattern, url: asyncCallArg(fn, "URL")}
	if pattern == "poll" {
		async.interval = defaultAsyncPollIntervalMs
		if raw := asyncCallArg(fn, "PollIntervalMs"); raw != "" {
			interval, err := strconv.Atoi(raw)
			if err != nil || interval <= 0 {
				return nil, fmt.Sprintf("PollIntervalMs=%s is not a positive number", raw)
			}
			async.interval = interval
		}
	}
	for _, name := range sourceAsyncCallbackArgs {
		if callback := asyncCallArg(fn, name); callback != "" {
			async.callbacks = append(async.callbacks, name+"="+callback)
		}
	}
	return async, ""
}

// appliesTo reports whether the registration's URL (when it names one) is the
// step's own URL; a URL naming one of the step's resources cannot be split
// out of the step.
func (a *sourceAsync) appliesTo(fn ParsedFunction, action *dsl.Action) bool {
	if a.url == "" {
		return true
	}
	want := convertParamReferences(a.url)
	if want == action.Target || want == convertParamReferences(fn.URL) {
		return true
	}
	// A target relative to the base URL matches the registration's path and
	// query (templates stay as written, so the URL is not parsed).
	_, rest, absolute := strings.Cut(want, "://")
	if !absolute || !strings.HasPrefix(action.Target, "/") {
		return false
	}
	path := "/"
	if slash := strings.Index(rest, "/"); slash >= 0 {
		path = rest[slash:]
	}
	return path == action.Target
}

// wrapAsyncConversation turns the converted step into the http_async_start
// that runs it in the background, with each translated callback as a branch.
func wrapAsyncConversation(async *sourceAsync, action dsl.Action, callbacks []sourceCallbackTranslation) (dsl.Action, error) {
	payload, err := json.Marshal(action)
	if err != nil {
		return dsl.Action{}, err
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		return dsl.Action{}, err
	}
	params := map[string]any{"id": async.id, "pattern": async.pattern, "request": request}
	if async.interval > 0 {
		params["interval_ms"] = async.interval
	}
	if len(async.callbacks) > 0 {
		params["source_callbacks"] = append([]string(nil), async.callbacks...)
	}
	metadata := make(map[string]any, len(action.Metadata))
	for key, value := range action.Metadata {
		metadata[key] = value
	}
	wrapped := dsl.Action{Name: action.Name, Type: "http_async_start", Params: params, Metadata: metadata, Attribution: action.Attribution}
	var branches []dsl.ParallelBranch
	var untranslated []string
	for _, callback := range callbacks {
		switch {
		case callback.reason != "":
			untranslated = append(untranslated, fmt.Sprintf("%s=%s (%s)", callback.attribute, callback.name, callback.reason))
		case len(callback.steps) > 0:
			branches = append(branches, dsl.ParallelBranch{Name: callback.branch, Steps: callback.steps})
		}
	}
	if len(branches) > 0 {
		wrapped.Control = &dsl.ControlFlow{Branches: branches}
	}
	if len(untranslated) > 0 {
		finding := promotionFinding(async.fn, asyncCallbacksReasonCode, fmt.Sprintf(
			"Async conversation %q runs, but these callbacks do not: %s. Rebuild what they do as http_async_start callback branches.",
			async.id, strings.Join(untranslated, "; ")))
		finding.Resolution = "rebuild_async_callbacks"
		importreview.ApplyToAction(&wrapped, []importreview.Finding{finding})
	}
	return wrapped, nil
}

// sourceStopAsyncID reads web_stop_async's conversation ID ("ID=x" or a bare
// x).
func sourceStopAsyncID(fn ParsedFunction) string {
	if id := asyncCallArg(fn, "ID"); id != "" {
		return id
	}
	for _, arg := range fn.CallArgs {
		if arg.Kind == "string" {
			return strings.TrimSpace(arg.Value)
		}
	}
	return ""
}

// sourceWebSyncAction maps web_sync("ParamExists=p" | "ParamValueEquals=p=v",
// ["Timeout=s"]) to http_sync. Other forms stay review-only.
func sourceWebSyncAction(fn ParsedFunction) (dsl.Action, bool) {
	params := map[string]any{}
	timeoutMs := defaultWebSyncTimeoutMs
	for _, arg := range fn.CallArgs {
		if arg.Kind == "macro" && arg.Value == "LAST" {
			continue
		}
		if arg.Kind != "key_value" {
			return dsl.Action{}, false
		}
		value := arg.Value
		switch strings.ToLower(strings.TrimSpace(arg.Name)) {
		case "paramexists":
			params["variable"] = strings.ToLower(strings.TrimSpace(value))
		case "paramvalueequals":
			name, want, ok := strings.Cut(value, "=")
			if !ok {
				return dsl.Action{}, false
			}
			params["variable"] = strings.ToLower(strings.TrimSpace(name))
			params["value"] = convertParamReferences(want)
		case "timeout":
			seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || seconds < 0 {
				return dsl.Action{}, false
			}
			timeoutMs = int(seconds * 1000)
		default:
			return dsl.Action{}, false
		}
	}
	if name, _ := params["variable"].(string); name == "" {
		return dsl.Action{}, false
	}
	params["timeout_ms"] = timeoutMs
	return dsl.Action{Name: "Sync " + params["variable"].(string), Type: "http_sync", Params: params}, true
}

// asyncUnsupportedReasonCode: an async call the importer cannot turn into a
// background conversation; it is preserved and not executed.
const asyncUnsupportedReasonCode = "async_conversation_unsupported"

// asyncReviewAction preserves an async call that cannot run as an
// execution-gated review action.
func asyncReviewAction(fn ParsedFunction, detail string) dsl.Action {
	return sourceReviewActionWithCode(fn, asyncUnsupportedReasonCode, detail)
}

// sourceReviewActionWithCode preserves a call that cannot run as an
// execution-gated review action carrying the reason code.
func sourceReviewActionWithCode(fn ParsedFunction, code, detail string) dsl.Action {
	def, _ := lookupSourceFunction(fn.Name)
	action := convertGenericSourceAction(fn, "source_review", def)
	action.Type = "source_review"
	makeSourceReviewActionDisclosureSafe(&action, fn, SourceFunctionSupportReviewOnly)
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code: code, ReasonCode: code, Severity: diag.Warning,
		Element: fn.Name, Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber),
		Detail: detail, Gate: importreview.GateExecution,
	}})
	return action
}

// asyncCallbackBranches maps a callback attribute to the http_async_start
// branch that runs it.
var asyncCallbackBranches = map[string]string{
	"requestcb":            "on_request",
	"responsecb":           "on_response",
	"responsebodybuffercb": "on_body_chunk",
}

// asyncCallbackArguments are the callback arguments with an APEX value, by
// position: ResponseCB's response body and ResponseBodyBufferCB's latest
// chunk.
var asyncCallbackArguments = map[string]map[int]string{
	"responsecb":           {2: `lr_eval_string("{async_response_body}")`},
	"responsebodybuffercb": {0: `lr_eval_string("{async_chunk}")`},
}

// translateAsyncCallbacks finds each callback the registration names in the
// archive's C files (VuGen writes them to AsyncCallbacks.c) and translates
// it (see translateSourceCallback).
func translateAsyncCallbacks(async *sourceAsync, files map[string]string) []sourceCallbackTranslation {
	var out []sourceCallbackTranslation
	for _, entry := range async.callbacks {
		attribute, name, _ := strings.Cut(entry, "=")
		key := strings.ToLower(attribute)
		out = append(out, translateSourceCallback(files, attribute, name, asyncCallbackBranches[key], asyncCallbackArguments[key]))
	}
	return out
}

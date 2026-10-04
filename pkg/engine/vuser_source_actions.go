package engine

// vuser_source_actions.go — split from vuser.go (same package, behavior-preserving).

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"math/rand/v2"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	protocolhttp "github.com/jasonczech/apexload/pkg/protocol/http"
	"go.opentelemetry.io/otel/trace"
)

func (vu *VUser) handleEngineAction(ctx context.Context, action dsl.Action) (bool, error) {
	switch dsl.CanonicalActionType(action.Type) {
	case "rendezvous":
		return true, vu.waitAtRendezvous(ctx, action)
	case "error_message":
		vu.logErrorMessageAction(ctx, action)
		return true, nil
	case "source_message":
		vu.handleSourceMessageAction(ctx, action)
		return true, nil
	case "source_runtime":
		return true, vu.handleSourceRuntimeAction(ctx, action)
	case "cookie_clear":
		return true, vu.clearProtocolCookies(ctx)
	case "cache_clear":
		return true, vu.clearProtocolCache(ctx)
	case "http_connections_close":
		return true, vu.closeProtocolIdleConnections(ctx)
	case "http_oauth2_token":
		return true, vu.handleHTTPOAuth2TokenAction(ctx, action)
	case "source_verification":
		controller, ok := vu.Proto.(protocol.VerificationController)
		if !ok {
			return true, fmt.Errorf("protocol does not support positional verification")
		}
		var v protocol.Verification
		b, err := json.Marshal(vu.interpolateAnyMap(action.Params))
		if err != nil {
			return true, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return true, err
		}
		return true, controller.ConfigureVerification(v)
	case "source_transaction":
		return true, vu.handleSourceTransactionActionContext(ctx, action)
	case "source_timer":
		vu.handleSourceTimerAction(action)
		return true, nil
	case "source_metric":
		vu.handleSourceMetricAction(ctx, action)
		return true, nil
	case "source_parameter", "source_json", "source_xml", "source_database", "source_secret":
		vu.handleSourceStateAction(ctx, action)
		return true, nil
	case "source_external", "source_review", "custom_call":
		vu.logSourceReviewAction(ctx, action)
		return true, nil
	case "control_break", "control_continue":
		// Loop control transfers are intercepted by the runner switch (load) and
		// expandReplayAction (replay) to produce a flow signal; reaching here
		// means the action is being executed as a plain leaf (replay event
		// emission or a stray break/continue outside a loop) — treat it as a
		// successful no-op so it surfaces in the timeline without erroring.
		return true, nil
	default:
		return false, nil
	}
}

// clearProtocolCookies dispatches a cookie_clear action to the active protocol
// when it exposes the protocol.CookieController capability (HTTP does). It is a
// safe no-op when the VUser has no protocol bound yet (e.g. the catalog
// dispatch cross-check) or the protocol keeps no cookie store.
func (vu *VUser) clearProtocolCookies(ctx context.Context) error {
	if vu == nil || vu.Proto == nil {
		return nil
	}
	controller, ok := vu.Proto.(protocol.CookieController)
	if !ok {
		return nil
	}
	return controller.ClearCookies(ctx)
}

// browserEmulationNewUserEachIteration reports whether browser emulation's
// "simulate a new user on each iteration" is enabled — the flag that clears
// cookies at iteration start, with cache clearing controlled separately.
func browserEmulationNewUserEachIteration(cfg dsl.ScriptConfig) bool {
	emu := cfg.BrowserEmulation
	return emu != nil && emu.Cache != nil && emu.Cache.NewUserEachIteration != nil && *emu.Cache.NewUserEachIteration
}

// clearProtocolCache dispatches to the protocol.CacheController capability
// (HTTP's simulated browser cache). Safe no-op when the protocol keeps no
// response cache.
func (vu *VUser) clearProtocolCache(ctx context.Context) error {
	if vu == nil || vu.Proto == nil {
		return nil
	}
	controller, ok := vu.Proto.(protocol.CacheController)
	if !ok {
		return nil
	}
	return controller.ClearCache(ctx)
}

// closeProtocolIdleConnections dispatches to protocols with reusable
// connection pools. It is intentionally a safe no-op for all other protocols.
func (vu *VUser) closeProtocolIdleConnections(ctx context.Context) error {
	if vu == nil || vu.Proto == nil {
		return nil
	}
	controller, ok := vu.Proto.(protocol.ConnectionController)
	if !ok {
		return nil
	}
	return controller.CloseIdleConnections(ctx)
}

func (vu *VUser) handleHTTPOAuth2TokenAction(ctx context.Context, action dsl.Action) error {
	params := vu.interpolateAnyMap(action.Params)
	saveAs, _ := actionParamString(params, "save_as")
	if saveAs == "" {
		saveAs = "access_token"
	}
	paramString := func(key string) string {
		value, _ := actionParamString(params, key)
		return value
	}
	tokenReq := protocolhttp.OAuthTokenRequest{
		TokenURL:     paramString("token_url"),
		GrantType:    paramString("grant_type"),
		ClientID:     paramString("client_id"),
		ClientSecret: paramString("client_secret"),
		Scope:        paramString("scope"),
		Audience:     paramString("audience"),
		Issuer:       paramString("issuer"),
		Subject:      paramString("subject"),
		PrivateKey:   paramString("private_key"),
		RefreshToken: paramString("refresh_token"),
		Username:     paramString("username"),
		Password:     paramString("password"),
	}
	var allowedTargetHosts []string
	if vu.Script != nil && vu.Script.Metadata != nil && vu.Script.Metadata.RuntimePolicy != nil {
		allowedTargetHosts = vu.Script.Metadata.RuntimePolicy.AllowedTargetHosts
	}
	if err := protocol.ValidateTargetURL(tokenReq.TokenURL, allowedTargetHosts); err != nil {
		return fmt.Errorf("http_oauth2_token %q: %w", action.Name, err)
	}
	// The third per-action credential surface. This handler interpolates its own
	// params and never produces a protocol.Action, so
	// extendRuntimeRedactionForResolvedAction never sees it — but net/http
	// synthesises the same "Basic base64(user:password)" from token_url's
	// userinfo that it does for any other target. Origin is read on the authored
	// param, exactly as actionCredentialCompositeSurfaces reads it on
	// action.Target.
	//
	// Registered before the fetch so an error echoing the request cannot carry
	// the composite out, and replaced wholesale — a token_url without userinfo
	// clears whatever an earlier http_oauth2_token action installed.
	var tokenURLComposites []string
	authoredTokenURL, _ := actionParamString(action.Params, "token_url")
	if surface, ok := urlUserinfoCredentialSurface(
		"action.params.token_url", authoredTokenURL, tokenReq.TokenURL,
	); ok {
		tokenURLComposites = appendPlatformManagedCompositeValues(nil, surface)
	}
	if err := vu.replaceRuntimeCredentialScope(
		runtimeCredentialOAuthURLScope, tokenURLComposites,
	); err != nil {
		return fmt.Errorf("http_oauth2_token %q: prepare token URL redaction: %s",
			action.Name, scrubSensitiveText(err.Error()))
	}
	if httpProto, ok := vu.Proto.(*protocolhttp.HTTP); ok {
		tokenReq.HTTPClient = httpProto.OAuthHTTPClient()
	} else if len(allowedTargetHosts) > 0 {
		// Other protocol wrappers do not expose an HTTP client whose proxy and
		// redirect legs carry the CI policy. Fail closed rather than falling back
		// to the process-global client and an ungoverned environment proxy.
		return fmt.Errorf("http_oauth2_token %q cannot enforce the CI runtime target policy for protocol %T", action.Name, vu.Proto)
	}
	token, err := protocolhttp.FetchOAuth2Token(ctx, tokenReq)
	if err != nil {
		return fmt.Errorf("http_oauth2_token %q: %w", action.Name, err)
	}
	if err := vu.replaceRuntimeCredentialScope(
		"oauth:"+strings.ToLower(strings.TrimSpace(saveAs)),
		[]string{token.AccessToken},
	); err != nil {
		return fmt.Errorf("http_oauth2_token %q: prepare token redaction: %s", action.Name, scrubSensitiveText(err.Error()))
	}
	// Install the exact matcher before the token becomes visible to templates,
	// logs, protocol actions, or retained evidence.
	vu.ensureVariables()
	writeVariableAlias := func(key, value string) {
		vu.Variables[key] = value
		vu.Variables[strings.ToLower(key)] = value
	}
	writeVariableAlias(saveAs, token.AccessToken)
	writeVariableAlias(saveAs+"_token_type", token.TokenType)
	writeVariableAlias(saveAs+"_instance_url", token.InstanceURL)
	if !token.ExpiresAt.IsZero() {
		writeVariableAlias(saveAs+"_expires_at", strconv.FormatInt(token.ExpiresAt.Unix(), 10))
	}
	return nil
}

func (vu *VUser) handleSourceMessageAction(ctx context.Context, action dsl.Action) {
	message := vu.renderErrorMessage(action)
	level, _ := actionParamString(action.Params, "level")
	attrs := []any{
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"message", message,
	}
	if status, _ := actionParamBool(action.Params, "status_message"); status {
		vu.ensureVariables()
		vu.Variables["vuser_status"] = message
	}
	switch level {
	case "debug":
		vu.runtimeLogger().DebugContext(ctx, "source message", attrs...)
	case "error":
		vu.runtimeLogger().ErrorContext(ctx, "source message", attrs...)
	default:
		vu.runtimeLogger().InfoContext(ctx, "source message", attrs...)
	}
}

func (vu *VUser) handleSourceRuntimeAction(ctx context.Context, action dsl.Action) error {
	function := sourceActionFunction(action)
	vu.ensureVariables()
	switch function {
	case "lr_abort":
		return fmt.Errorf("source runtime abort requested by %s", action.Name)
	case "lr_exit":
		vu.Variables["__source_exit_requested"] = strings.Join(sourceActionArgs(action), ",")
	case "web_cache_cleanup":
		return vu.clearProtocolCache(ctx)
	case "lr_disable_ip_spoofing":
		// Import-compatibility marker only — see lr_enable_ip_spoofing below.
		vu.Variables["__lr_ip_spoofing"] = "disabled"
	case "lr_enable_ip_spoofing":
		// Import-compatibility markers only. APEX Load binds source IPs at VUser init
		// from config.web_runtime.source_ips (round-robin per VUser); there is no
		// runtime toggle, so these record intent but do not change binding behavior.
		// The __lr_ip_spoofing variable is intentionally not read anywhere.
		vu.Variables["__lr_ip_spoofing"] = "enabled"
	case "lr_load_dll":
		vu.logSourceReviewAction(ctx, action)
	default:
		vu.runtimeLogger().DebugContext(ctx, "source runtime action",
			"vuser", vu.ID,
			"group", vu.Group,
			"action", action.Name,
			"function", function,
		)
	}
	return nil
}

func sourceTransactionStarts(action dsl.Action) bool {
	if dsl.CanonicalActionType(action.Type) != "source_transaction" {
		return false
	}
	switch normalizeSourceTransactionFunction(action) {
	case "lr_start_transaction", "lr_start_sub_transaction":
		return true
	default:
		return false
	}
}

// sourceMainIterationExit recognizes the LoadRunner control transfer supported
// by the native runner. The first return value reports that the action requests
// "end this main iteration and continue with the next"; the second carries the
// LR_FAIL status independently from that flow transfer.
func sourceMainIterationExit(action dsl.Action) (bool, bool) {
	if dsl.CanonicalActionType(action.Type) != "source_runtime" ||
		!strings.EqualFold(strings.TrimSpace(sourceActionFunction(action)), "lr_exit") {
		return false, false
	}
	args := sourceActionArgs(action)
	if len(args) == 0 || !strings.EqualFold(strings.TrimSpace(args[0]), "LR_EXIT_MAIN_ITERATION_AND_CONTINUE") {
		return false, false
	}
	failed := len(args) > 1 && strings.EqualFold(strings.TrimSpace(args[1]), "LR_FAIL")
	return true, failed
}

type sourceTransactionRuntimeState struct {
	name             string
	startedAt        time.Time
	thinkTime        time.Duration
	status           string
	stoppedAt        time.Time
	stoppedThinkTime time.Duration
	parent           string
	// goalClaimed distinguishes a transaction started in the main action phase,
	// where its MaxTransactions slot is claimed at start, from one opened in
	// init and recorded by a later main/teardown end marker.
	goalClaimed bool
	// goalReserved carries an uncommitted MaxTransactions reservation from a
	// lifecycle start across init/main/teardown. It prevents peer VUsers from
	// entering the enclosed SUT work without soft-cancelling this owner before
	// the matching end marker can record the logical transaction.
	goalReserved bool
	// trace is this transaction's own trace when it opened with no other
	// transaction open and the run enabled trace context. It is a pointer so
	// the state stays comparable.
	trace *sourceTransactionTrace
}

type sourceTransactionTrace struct {
	root trace.SpanContext
	// end ends an exported root span; it runs at most once.
	end func()
}

// sourceTransactionEndNeedsGoalClaim reports whether an end marker will record
// a transaction whose MaxTransactions slot has not already been claimed by its
// matching main-phase start marker. An unmatched or malformed end is an action
// error, not a zero-length business transaction, and therefore consumes no slot.
func (vu *VUser) sourceTransactionEndNeedsGoalClaim(action dsl.Action) bool {
	if vu == nil || !sourceTransactionEnds(action) {
		return false
	}
	if !vu.sourceTransactionEndWillClose(action) {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
	state := vu.sourceTransactions[key]
	return state != nil && !state.goalClaimed && !state.goalReserved
}

func (vu *VUser) markSourceTransactionGoalClaimed(action dsl.Action) {
	if vu == nil || !sourceTransactionStarts(action) {
		return
	}
	key := strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
	if state := vu.sourceTransactions[key]; state != nil {
		state.goalClaimed = true
	}
}

func (vu *VUser) markSourceTransactionGoalReserved(action dsl.Action) {
	if vu == nil || !sourceTransactionStarts(action) {
		return
	}
	key := strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
	if state := vu.sourceTransactions[key]; state != nil {
		state.goalReserved = true
	}
}

func (vu *VUser) sourceTransactionStartWillOpen(action dsl.Action) bool {
	if vu == nil || !sourceTransactionStarts(action) {
		return false
	}
	name, parent, ok := sourceTransactionStartArguments(action)
	if !ok {
		return false
	}
	key := strings.ToLower(name)
	if vu.sourceTransactions[key] != nil {
		return false
	}
	return parent == "" || vu.sourceTransactions[strings.ToLower(parent)] != nil
}

func (vu *VUser) hasOpenSourceTransactionGoalOwnership() bool {
	if vu == nil {
		return false
	}
	for _, state := range vu.sourceTransactions {
		if state != nil && (state.goalClaimed || state.goalReserved) {
			return true
		}
	}
	return false
}

func (vu *VUser) finalizeSourceTransaction(key, reason string) *protocol.Result {
	if vu == nil {
		return nil
	}
	state := vu.sourceTransactions[key]
	if state == nil {
		return nil
	}
	delete(vu.sourceTransactions, key)
	state.endTrace()
	if state.goalReserved && vu.ExecutionGoals != nil {
		vu.ExecutionGoals.CommitTransactionReservation()
		state.goalReserved = false
		state.goalClaimed = true
	}
	duration := sourceTransactionDuration(state, time.Now())
	name := strings.TrimSpace(state.name)
	if name == "" {
		name = key
	}
	vu.ensureVariables()
	// The map above is the authoritative open-transaction state. Its legacy
	// timestamp mirror exists only for compatibility with older execution
	// paths, so it must not outlive the authoritative transaction. Otherwise a
	// later orphan end marker can reuse the stale start and synthesize a second
	// long-running transaction.
	delete(vu.Variables, sourceTransactionStartKey(name))
	source := &protocol.Result{
		ActionName:   name,
		StartTime:    state.startedAt,
		Success:      false,
		ErrorMessage: reason,
	}
	result := vu.recordTransaction(name, duration, false, source)
	vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(false)
	return result
}

func (vu *VUser) finalizeOpenSourceTransactions(reason string) []*protocol.Result {
	if vu == nil || len(vu.sourceTransactions) == 0 {
		return nil
	}
	keys := make([]string, 0, len(vu.sourceTransactions))
	for key := range vu.sourceTransactions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	results := make([]*protocol.Result, 0, len(keys))
	for _, key := range keys {
		if result := vu.finalizeSourceTransaction(key, reason); result != nil {
			results = append(results, result)
		}
	}
	return results
}

// activeSourceTransactionOwnsHTTPSequence reports whether the sequence is
// currently enclosed by a matching, successfully executed source transaction
// start marker. Script-wide marker presence is insufficient: a marker in an
// untaken branch, a later statement, or another completed phase must not
// suppress the sequence's standalone aggregate transaction.
func (vu *VUser) activeSourceTransactionOwnsHTTPSequence(action dsl.Action) bool {
	if vu == nil || !strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(sourceTransactionName(action.Metadata)))
	if name == "" {
		return false
	}
	state, open := vu.sourceTransactions[name]
	return open && state != nil
}

func sourceTransactionActionName(action dsl.Action) string {
	return firstEngineValue(action.Name, sourceActionArgs(action)...)
}

func sourceTransactionEnds(action dsl.Action) bool {
	if dsl.CanonicalActionType(action.Type) != "source_transaction" {
		return false
	}
	switch normalizeSourceTransactionFunction(action) {
	case "lr_end_transaction", "lr_end_sub_transaction", "lr_end_sub_trans":
		return true
	default:
		return false
	}
}

func sourceTransactionRecordsImmediate(action dsl.Action) bool {
	if dsl.CanonicalActionType(action.Type) != "source_transaction" ||
		normalizeSourceTransactionFunction(action) != "lr_set_transaction" {
		return false
	}
	_, _, _, ok := sourceSetTransactionArguments(action)
	return ok
}

func sourceTransactionCompletes(action dsl.Action) bool {
	return sourceTransactionEnds(action) || sourceTransactionRecordsImmediate(action)
}

func (vu *VUser) sourceTransactionCompletionWillExecute(action dsl.Action) bool {
	if sourceTransactionRecordsImmediate(action) {
		return true
	}
	return vu != nil && vu.sourceTransactionEndWillClose(action)
}

func normalizeSourceTransactionFunction(action dsl.Action) string {
	return strings.ToLower(strings.TrimSpace(sourceActionFunction(action)))
}

const (
	sourceTransactionPass = "pass"
	sourceTransactionFail = "fail"
	sourceTransactionStop = "stop"
	sourceTransactionAuto = "auto"
)

func normalizeSourceTransactionStatus(raw string, allowAuto bool) (string, bool) {
	status := strings.ToUpper(strings.TrimSpace(raw))
	status = strings.TrimPrefix(status, "LR.")
	status = strings.TrimPrefix(status, "LR_")
	switch status {
	case "PASS":
		return sourceTransactionPass, true
	case "FAIL":
		return sourceTransactionFail, true
	case "STOP":
		return sourceTransactionStop, true
	case "AUTO":
		return sourceTransactionAuto, allowAuto
	default:
		return "", false
	}
}

func sourceTransactionStartArguments(action dsl.Action) (name, parent string, ok bool) {
	function := normalizeSourceTransactionFunction(action)
	args := sourceActionArgs(action)
	switch function {
	case "lr_start_transaction":
		if len(args) > 1 {
			return "", "", false
		}
		name = strings.TrimSpace(action.Name)
		if len(args) == 1 {
			name = strings.TrimSpace(args[0])
		}
		return name, "", name != ""
	case "lr_start_sub_transaction":
		if len(args) != 2 {
			return "", "", false
		}
		name = strings.TrimSpace(args[0])
		parent = strings.TrimSpace(args[1])
		return name, parent, name != "" && parent != ""
	default:
		return "", "", false
	}
}

func sourceTransactionEndArguments(action dsl.Action) (name, status string, ok bool) {
	if !sourceTransactionEnds(action) {
		return "", "", false
	}
	args := sourceActionArgs(action)
	if len(args) > 2 {
		return "", "", false
	}
	name = strings.TrimSpace(action.Name)
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	if name == "" {
		return "", "", false
	}
	// Preserve the historical hand-authored DSL shorthand where an end marker
	// omitted args and used Name as its identity. Imported calls always carry the
	// explicit status argument.
	status = sourceTransactionPass
	if len(args) == 2 {
		status, ok = normalizeSourceTransactionStatus(args[1], true)
		if !ok {
			return "", "", false
		}
	}
	return name, status, true
}

func (vu *VUser) sourceTransactionEndWillClose(action dsl.Action) bool {
	if vu == nil {
		return false
	}
	name, _, ok := sourceTransactionEndArguments(action)
	if !ok {
		return false
	}
	key := strings.ToLower(name)
	state := vu.sourceTransactions[key]
	if state == nil {
		return false
	}
	function := normalizeSourceTransactionFunction(action)
	if function == "lr_end_transaction" && state.parent != "" {
		return false
	}
	if (function == "lr_end_sub_transaction" || function == "lr_end_sub_trans") &&
		(state.parent == "" || vu.sourceTransactions[state.parent] == nil) {
		return false
	}
	for _, child := range vu.sourceTransactions {
		if child != nil && child.parent == key {
			return false
		}
	}
	return true
}

func sourceSetTransactionArguments(action dsl.Action) (name string, duration time.Duration, status string, ok bool) {
	if normalizeSourceTransactionFunction(action) != "lr_set_transaction" {
		return "", 0, "", false
	}
	args := sourceActionArgs(action)
	if len(args) != 3 {
		return "", 0, "", false
	}
	name = strings.TrimSpace(args[0])
	seconds, err := strconv.ParseFloat(strings.TrimSpace(args[1]), 64)
	if name == "" || err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
		seconds < 0 || seconds > float64((1<<63)-1)/float64(time.Second) {
		return "", 0, "", false
	}
	status, ok = normalizeSourceTransactionStatus(args[2], false)
	if !ok {
		return "", 0, "", false
	}
	return name, time.Duration(seconds * float64(time.Second)), status, true
}

func sourceTransactionDuration(state *sourceTransactionRuntimeState, now time.Time) time.Duration {
	if state == nil {
		return 0
	}
	end := now
	thinkTime := state.thinkTime
	if !state.stoppedAt.IsZero() {
		end = state.stoppedAt
		thinkTime = state.stoppedThinkTime
	}
	duration := end.Sub(state.startedAt) - thinkTime
	if duration < 0 {
		return 0
	}
	return duration
}

func sourceTransactionStatusSuccess(status string) bool {
	return status == sourceTransactionPass
}

func sourceTransactionFailureResult(name, status string, startedAt time.Time) *protocol.Result {
	if sourceTransactionStatusSuccess(status) {
		return nil
	}
	message := "transaction failed"
	if status == sourceTransactionStop {
		message = "transaction stopped"
	}
	return &protocol.Result{
		ActionName:   name,
		StartTime:    startedAt,
		Success:      false,
		ErrorMessage: message,
	}
}

func (vu *VUser) setSourceTransactionStatus(state *sourceTransactionRuntimeState, status string) {
	if vu == nil || state == nil {
		return
	}
	state.status = status
	name := strings.TrimSpace(state.name)
	if name == "" {
		return
	}
	vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(sourceTransactionStatusSuccess(status))
}

func (vu *VUser) logSourceTransactionError(ctx context.Context, action dsl.Action, args []string) {
	messageAction := action
	messageAction.Params = make(map[string]any, len(action.Params)+1)
	for key, value := range action.Params {
		messageAction.Params[key] = value
	}
	if len(args) > 0 {
		messageAction.Value = args[0]
		messageArgs := make([]any, 0, len(args)-1)
		for _, arg := range args[1:] {
			messageArgs = append(messageArgs, arg)
		}
		messageAction.Params["args"] = messageArgs
	}
	vu.runtimeLogger().ErrorContext(ctx, "source transaction error",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", normalizeSourceTransactionFunction(action),
		"message", vu.renderErrorMessage(messageAction),
	)
}

func unsupportedSourceTransactionOwnershipFunction(function string) bool {
	switch function {
	case "lr_start_cross_vuser_transaction", "lr_end_cross_vuser_transaction",
		"lr_start_transaction_instance", "lr_end_transaction_instance",
		"lr_stop_transaction_instance", "lr_resume_transaction_instance",
		"lr_set_transaction_instance_status",
		"lr_get_trans_instance_duration", "lr_get_trans_instance_status",
		"lr_get_trans_instance_think_time", "lr_get_trans_instance_wasted_time":
		return true
	default:
		return false
	}
}

func unsupportedSourceTransactionValueFunction(function string) bool {
	switch function {
	case "lr_get_transaction_duration", "lr_get_transaction_status",
		"lr_get_transaction_think_time", "lr_get_transaction_wasted_time",
		"lr_wasted_time":
		return true
	default:
		return false
	}
}

// handleSourceTransactionAction retains the context-free helper used by focused
// engine tests and older in-package callers.
func (vu *VUser) handleSourceTransactionAction(action dsl.Action) error {
	return vu.handleSourceTransactionActionContext(context.Background(), action)
}

func (vu *VUser) handleSourceTransactionActionContext(ctx context.Context, action dsl.Action) error {
	function := normalizeSourceTransactionFunction(action)
	args := sourceActionArgs(action)
	// Marker names are display labels ("Start … transaction"/"End …
	// transaction"). LoadRunner's first call argument is the authoritative
	// business-transaction identity; fall back to Name for legacy scripts.
	name := firstEngineValue(action.Name, args...)
	key := strings.ToLower(strings.TrimSpace(name))
	vu.ensureVariables()
	switch function {
	case "lr_start_transaction", "lr_start_sub_transaction":
		startName, parentName, ok := sourceTransactionStartArguments(action)
		if !ok {
			if function == "lr_start_sub_transaction" {
				return fmt.Errorf("lr_start_sub_transaction requires subtransaction and parent transaction names")
			}
			return fmt.Errorf("lr_start_transaction requires a transaction name")
		}
		name = startName
		key = strings.ToLower(name)
		if key == "" {
			return fmt.Errorf("%s requires a transaction name", function)
		}
		if vu.sourceTransactions[key] != nil {
			return fmt.Errorf("%s cannot restart already-open transaction %q", function, name)
		}
		parentKey := strings.ToLower(parentName)
		if parentKey != "" && vu.sourceTransactions[parentKey] == nil {
			return fmt.Errorf("lr_start_sub_transaction references parent transaction %q with no matching open transaction", parentName)
		}
		startedAt := time.Now()
		vu.Variables[sourceTransactionStartKey(name)] = strconv.FormatInt(startedAt.UnixNano(), 10)
		// LR_AUTO derives its final status from failures observed while the
		// transaction is open. Start optimistic; any enclosed failed result
		// flips this state to false.
		vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(true)
		if vu.sourceTransactions == nil {
			vu.sourceTransactions = make(map[string]*sourceTransactionRuntimeState)
		}
		vu.sourceTransactions[key] = &sourceTransactionRuntimeState{
			name:      name,
			startedAt: startedAt,
			status:    sourceTransactionPass,
			parent:    parentKey,
		}
		vu.startSourceTransactionTraceRoot(ctx, vu.sourceTransactions[key])
		return nil
	case "lr_end_transaction", "lr_end_sub_transaction", "lr_end_sub_trans":
		endName, requestedStatus, ok := sourceTransactionEndArguments(action)
		if !ok {
			return fmt.Errorf("%s requires a transaction name and PASS, FAIL, STOP, or AUTO status", function)
		}
		key = strings.ToLower(strings.TrimSpace(endName))
		state := vu.sourceTransactions[key]
		if state == nil {
			return fmt.Errorf("%s references transaction %q with no matching open transaction", function, endName)
		}
		if !vu.sourceTransactionEndWillClose(action) {
			return fmt.Errorf("%s cannot close transaction %q while its parent/child transaction relationship is incomplete or mismatched", function, endName)
		}
		status := requestedStatus
		if status == sourceTransactionAuto {
			status = state.status
			if status == "" {
				status = sourceTransactionPass
			}
		}
		if state.goalReserved && vu.ExecutionGoals != nil {
			vu.ExecutionGoals.CommitTransactionReservation()
			state.goalReserved = false
			state.goalClaimed = true
		}
		delete(vu.sourceTransactions, key)
		state.endTrace()
		// The end marker consumes both authoritative state and its legacy mirror.
		// Keeping the mirror would let a later orphan end reuse this start time.
		delete(vu.Variables, sourceTransactionStartKey(endName))
		duration := sourceTransactionDuration(state, time.Now())
		success := sourceTransactionStatusSuccess(status)
		vu.recordTransaction(endName, duration, success, sourceTransactionFailureResult(endName, status, state.startedAt))
		vu.Variables[sourceTransactionStatusKey(endName)] = strconv.FormatBool(success)
		return nil
	case "lr_set_transaction_status":
		if len(args) != 1 {
			return fmt.Errorf("lr_set_transaction_status requires exactly one status argument")
		}
		status, ok := normalizeSourceTransactionStatus(args[0], false)
		if !ok {
			return fmt.Errorf("lr_set_transaction_status status %q is not PASS, FAIL, or STOP", args[0])
		}
		if len(vu.sourceTransactions) == 0 {
			return fmt.Errorf("lr_set_transaction_status requires at least one open transaction")
		}
		for _, state := range vu.sourceTransactions {
			vu.setSourceTransactionStatus(state, status)
		}
		return nil
	case "lr_set_transaction_status_by_name":
		if len(args) != 2 {
			return fmt.Errorf("lr_set_transaction_status_by_name requires status and transaction name arguments")
		}
		status, ok := normalizeSourceTransactionStatus(args[0], false)
		if !ok {
			return fmt.Errorf("lr_set_transaction_status_by_name status %q is not PASS, FAIL, or STOP", args[0])
		}
		target := strings.TrimSpace(args[1])
		state := vu.sourceTransactions[strings.ToLower(target)]
		if target == "" || state == nil {
			return fmt.Errorf("lr_set_transaction_status_by_name references transaction %q with no matching open transaction", target)
		}
		vu.setSourceTransactionStatus(state, status)
		return nil
	case "lr_fail_trans_with_error":
		for _, state := range vu.sourceTransactions {
			vu.setSourceTransactionStatus(state, sourceTransactionFail)
		}
		vu.logSourceTransactionError(ctx, action, args)
		return nil
	case "lr_stop_transaction":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return fmt.Errorf("lr_stop_transaction requires exactly one transaction name")
		}
		target := strings.TrimSpace(args[0])
		state := vu.sourceTransactions[strings.ToLower(strings.TrimSpace(target))]
		if state == nil {
			return fmt.Errorf("lr_stop_transaction references transaction %q with no matching open transaction", target)
		}
		if !state.stoppedAt.IsZero() {
			return fmt.Errorf("lr_stop_transaction references already-stopped transaction %q", target)
		}
		state.stoppedAt = time.Now()
		state.stoppedThinkTime = state.thinkTime
		return nil
	case "lr_resume_transaction":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return fmt.Errorf("lr_resume_transaction requires exactly one transaction name")
		}
		target := strings.TrimSpace(args[0])
		state := vu.sourceTransactions[strings.ToLower(strings.TrimSpace(target))]
		if state == nil {
			return fmt.Errorf("lr_resume_transaction references transaction %q with no matching open transaction", target)
		}
		if state.stoppedAt.IsZero() {
			return fmt.Errorf("lr_resume_transaction references transaction %q that is not stopped", target)
		}
		state.stoppedAt = time.Time{}
		state.stoppedThinkTime = 0
		return nil
	case "lr_set_transaction":
		transactionName, duration, status, ok := sourceSetTransactionArguments(action)
		if !ok {
			return fmt.Errorf("lr_set_transaction requires name, a finite non-negative duration in seconds, and PASS, FAIL, or STOP status")
		}
		success := sourceTransactionStatusSuccess(status)
		vu.recordTransaction(transactionName, duration, success, sourceTransactionFailureResult(transactionName, status, time.Now().Add(-duration)))
		vu.Variables[sourceTransactionStatusKey(transactionName)] = strconv.FormatBool(success)
		return nil
	default:
		if unsupportedSourceTransactionOwnershipFunction(function) {
			return fmt.Errorf("%s requires transaction instance or cross-VUser ownership semantics and is not executable in the native source-transaction runtime", function)
		}
		if unsupportedSourceTransactionValueFunction(function) {
			return fmt.Errorf("%s requires a source-language return value or wasted-time bookkeeping and is not executable in the native source-transaction runtime", function)
		}
		_, hasParamFunction := actionParamString(action.Params, "function")
		_, hasMetadataFunction := action.Metadata["source_function"].(string)
		if function == "source_transaction" && !hasParamFunction && !hasMetadataFunction {
			// Retain the catalog/legacy metadata-only marker as an intentional
			// no-op. Imported and hand-authored function calls carry
			// params.function; every non-empty unsupported function fails closed.
			return nil
		}
		if function == "" {
			function = "<missing>"
		}
		return fmt.Errorf("%s is not a supported native source-transaction function", function)
	}
}

func (vu *VUser) sourceTransactionActionOutcome(action dsl.Action) (ended bool, name string, success bool) {
	if vu == nil || !sourceTransactionCompletes(action) {
		return false, "", true
	}
	name = sourceTransactionActionName(action)
	success = true
	if recorded, err := strconv.ParseBool(vu.Variables[sourceTransactionStatusKey(name)]); err == nil {
		success = recorded
	}
	return true, name, success
}

func (vu *VUser) noteSourceTransactionThinkTime(delay time.Duration) {
	if vu == nil || delay <= 0 {
		return
	}
	for _, state := range vu.sourceTransactions {
		if state != nil {
			state.thinkTime += delay
		}
	}
}

func (vu *VUser) noteSourceTransactionResult(action dsl.Action, success bool) {
	if vu == nil || success {
		return
	}
	name := sourceTransactionName(action.Metadata)
	vu.ensureVariables()
	// Every runtime-open scope encloses the failed leaf, including a parent whose
	// import metadata names only the innermost transaction. LR_AUTO must therefore
	// fail in every enclosing scope.
	for key, state := range vu.sourceTransactions {
		transactionName := key
		if state != nil && strings.TrimSpace(state.name) != "" {
			transactionName = state.name
			state.status = sourceTransactionFail
		}
		vu.Variables[sourceTransactionStatusKey(transactionName)] = strconv.FormatBool(false)
	}
	if len(vu.sourceTransactions) == 0 && name != "" {
		// Preserve compatibility for metadata-only imported actions that predate
		// executable transaction boundary actions.
		vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(false)
	}
}

func (vu *VUser) handleSourceTimerAction(action dsl.Action) {
	function := sourceActionFunction(action)
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	vu.ensureVariables()
	switch function {
	case "lr_start_timer":
		vu.Variables[sourceTimerStartKey(name)] = strconv.FormatInt(time.Now().UnixNano(), 10)
	case "lr_end_timer":
		if raw := vu.Variables[sourceTimerStartKey(name)]; raw != "" {
			if nanos, err := strconv.ParseInt(raw, 10, 64); err == nil {
				durationMs := time.Since(time.Unix(0, nanos)).Milliseconds()
				vu.Variables[sourceTimerDurationKey(name)] = strconv.FormatInt(durationMs, 10)
			}
		}
	}
}

func (vu *VUser) handleSourceMetricAction(ctx context.Context, action dsl.Action) {
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	value := ""
	if len(args) > 1 {
		value = args[1]
	}
	vu.runtimeLogger().InfoContext(ctx, "source metric",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"name", name,
		"value", value,
	)
}

func (vu *VUser) handleSourceStateAction(ctx context.Context, action dsl.Action) {
	function := sourceActionFunction(action)
	switch action.Type {
	case "source_parameter":
		vu.handleSourceParameterAction(function, action)
	case "source_json", "source_xml":
		vu.handleSourceDocumentAction(function, action)
	case "source_secret":
		vu.handleSourceSecretAction(function, action)
	default:
		vu.logSourceReviewAction(ctx, action)
	}
}

func (vu *VUser) handleSourceParameterAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	vu.ensureVariables()
	switch function {
	case "lr_save_string", "lr_save_int":
		if len(args) >= 2 {
			if array, ok := action.Params["source_array"].(map[string]any); ok {
				// Array entries are already runtime values. Interpolating the selected
				// entry again would treat template-looking response bytes as source.
				vu.setResolvedSourceVariable(args[1], vu.sourceParameterArrayValue(array))
			} else {
				vu.setSourceVariable(args[1], args[0])
			}
		}
	case "lr_save_var":
		if len(args) >= 4 {
			vu.setSourceVariable(args[3], args[0])
		}
	case "lr_param_sprintf":
		if len(args) >= 2 {
			values := make([]any, 0, len(args)-2)
			for _, arg := range args[2:] {
				values = append(values, arg)
			}
			vu.setSourceVariable(args[0], renderPrintfLikeMessage(args[1], values))
		}
	case "lr_param_increment":
		if len(args) >= 1 {
			delta := 1
			if len(args) > 1 {
				if parsed, err := strconv.Atoi(strings.TrimSpace(args[1])); err == nil {
					delta = parsed
				}
			}
			current, _ := strconv.Atoi(strings.TrimSpace(vu.Variables[strings.ToLower(args[0])]))
			vu.setSourceVariable(args[0], strconv.Itoa(current+delta))
		}
	case "lr_free_parameter":
		if len(args) >= 1 {
			delete(vu.Variables, args[0])
			delete(vu.Variables, strings.ToLower(args[0]))
		}
	case "lr_save_timestamp":
		name := firstEngineValue("timestamp", args...)
		options := args
		if len(options) > 0 {
			options = options[1:]
		}
		vu.setSourceVariable(name, sourceTimestampValue(time.Now(), options))
	case "lr_save_datetime":
		name := firstEngineValue("datetime", args...)
		format := defaultDateTimeLayout
		if len(args) > 1 {
			format = sourceDateTimeLayout(args[0])
			name = args[len(args)-1]
		}
		vu.setSourceVariable(name, time.Now().Format(format))
	case "lr_whoami":
		vu.setSourceVariable("vuser_id", strconv.Itoa(vu.ID))
		vu.setSourceVariable("vuser_group", vu.Group)
	case "lr_get_host_name", "lr_get_master_host_name":
		if host, err := os.Hostname(); err == nil {
			vu.setSourceVariable(firstEngineValue("host_name", args...), host)
		}
	case "lr_get_vuser_ip":
		vu.setSourceVariable(firstEngineValue("vuser_ip", args...), "127.0.0.1")
	case "web_convert_param":
		vu.handleWebConvertParamAction(action, args)
	default:
		if saveName := saveParamNameFromAction(action); saveName != "" {
			vu.setSourceVariable(saveName, firstEngineValue("", args...))
		}
	}
}

func (vu *VUser) sourceParameterArrayValue(array map[string]any) string {
	name, _ := array["name"].(string)
	selection, _ := array["selection"].(string)
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || selection != "random" {
		return ""
	}
	count, err := strconv.Atoi(vu.Variables[name+"_count"])
	if err != nil || count <= 0 {
		return ""
	}
	return vu.Variables[fmt.Sprintf("%s_%d", name, rand.IntN(count)+1)]
}

// LoadRunner defaults to thirteen epoch digits (milliseconds), with seconds
// and microseconds at ten and sixteen digits respectively. Other valid widths
// take the leading digits; invalid widths use the documented default.
func sourceTimestampValue(now time.Time, args []string) string {
	digits := 13
	for _, arg := range args {
		key, raw, ok := strings.Cut(arg, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "DIGITS") {
			continue
		}
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && value >= 1 && value <= 16 {
			digits = value
		}
		break
	}
	value := strconv.FormatInt(now.UnixMicro(), 10)
	if digits < len(value) {
		value = value[:digits]
	}
	return value
}

func (vu *VUser) handleWebConvertParamAction(action dsl.Action, args []string) {
	saveName := ""
	if value, ok := actionParamString(action.Params, "save_param"); ok {
		saveName = value
	} else if len(args) > 0 {
		saveName = args[0]
	}
	if strings.TrimSpace(saveName) == "" {
		return
	}
	source := ""
	if value, ok := actionParamString(action.Params, "source_string"); ok {
		source = value
	} else {
		for _, arg := range args[1:] {
			key, value, ok := strings.Cut(arg, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "SourceString") {
				source = value
				break
			}
		}
	}
	sourceEncoding := sourceActionParamString(action, "source_encoding", args)
	targetEncoding := sourceActionParamString(action, "target_encoding", args)
	value := vu.interpolate(source)
	switch strings.ToLower(strings.TrimSpace(sourceEncoding)) {
	case "html":
		value = html.UnescapeString(value)
	}
	switch strings.ToLower(strings.TrimSpace(targetEncoding)) {
	case "url":
		value = url.QueryEscape(value)
	case "html":
		value = html.EscapeString(value)
	}
	vu.setSourceVariable(saveName, value)
}

func sourceActionParamString(action dsl.Action, name string, args []string) string {
	if value, ok := actionParamString(action.Params, name); ok {
		return value
	}
	sourceKey := strings.ReplaceAll(name, "_", "")
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if ok && strings.EqualFold(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), sourceKey) {
			return value
		}
	}
	return ""
}

func (vu *VUser) handleSourceDocumentAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	vu.ensureVariables()
	saveName := saveParamNameFromAction(action)
	if saveName == "" && len(args) > 0 {
		saveName = args[len(args)-1]
	}
	if saveName == "" {
		return
	}
	switch {
	case strings.Contains(function, "find"):
		vu.setSourceVariable(saveName, "0")
	case strings.Contains(function, "get_values"), strings.Contains(function, "extract"), strings.Contains(function, "stringify"), strings.Contains(function, "eval"):
		vu.setSourceVariable(saveName, firstEngineValue("", args...))
	}
}

func (vu *VUser) handleSourceSecretAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	saveName := saveParamNameFromAction(action)
	if saveName == "" && len(args) > 0 {
		saveName = args[len(args)-1]
	}
	if saveName == "" {
		saveName = strings.TrimPrefix(function, "lr_")
	}
	vu.setSourceVariable(saveName, "{{secret."+strings.ToLower(saveName)+"}}")
}

func (vu *VUser) logSourceReviewAction(ctx context.Context, action dsl.Action) {
	vu.runtimeLogger().InfoContext(ctx, "source review action",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"support_mode", sourceActionSupportMode(action),
	)
}

func (vu *VUser) ensureVariables() {
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
}

func (vu *VUser) setSourceVariable(name string, value string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	vu.ensureVariables()
	vu.setResolvedSourceVariable(name, vu.interpolate(value))
}

func (vu *VUser) setResolvedSourceVariable(name string, resolved string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	vu.ensureVariables()
	vu.Variables[name] = resolved
	vu.Variables[strings.ToLower(name)] = resolved
	vu.registerSourceVariableRunEvidenceValue(name, resolved)
}

// registerSourceVariableRunEvidenceValue registers a source-action assignment
// whose receiving variable an import reads from a credential-governed field.
//
// The value is SCRIPT-SOURCED: the assignment interpolates an authored literal
// out of the operator's own script, so it is filed under a script-sourced scope
// and the operator's script.Config.Log.RedactRunEvidence setting governs it.
//
// Origin is re-established from the namespace, not inherited from the template
// plan: the plan records "secret.NAME" too, and only {{var.*}}/{{param.*}} name a
// script-sourced variable that a source action can assign. A {{secret.*}}
// reference interpolated into the value is already covered by the base matcher
// installed from snapshotReferencedSecrets, and the funnel drops anything the
// base matcher already covers, so nothing platform-managed can be demoted here.
//
// Errors are not propagated. Callers of setSourceVariable have no error channel,
// and a script-sourced registration must never fail a run (the funnel drops the
// script-sourced half on overflow). A platform-managed failure is recorded on the
// VUser by the funnel and is checked at the next action boundary.
func (vu *VUser) registerSourceVariableRunEvidenceValue(name, resolved string) {
	if vu == nil || resolved == "" || !scriptRedactsRunEvidence(vu.Script) {
		return
	}
	vu.ensureImportedCredentialInspection()
	plan := vu.importedCredentialInspection.templatePlan()
	if plan == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(name))
	referenced := false
	for _, expression := range scriptSourcedTemplateExpressions(plan) {
		namespace, reference, found := strings.Cut(expression, ".")
		if !found || (namespace != "var" && namespace != "param") {
			continue
		}
		// Runtime variable lookup is case-insensitive; match it.
		if strings.ToLower(strings.TrimSpace(reference)) == key {
			referenced = true
			break
		}
	}
	if !referenced {
		return
	}
	_ = vu.replaceRuntimeCredentialScope(
		runtimeCredentialSourceVariablePrefix+key,
		[]string{resolved},
	)
}

func sourceActionFunction(action dsl.Action) string {
	if value, ok := actionParamString(action.Params, "function"); ok {
		return value
	}
	if value, ok := action.Metadata["source_function"].(string); ok {
		return value
	}
	return action.Type
}

func sourceActionSupportMode(action dsl.Action) string {
	if value, ok := actionParamString(action.Params, "support_mode"); ok {
		return value
	}
	if value, ok := action.Metadata["support_mode"].(string); ok {
		return value
	}
	return ""
}

func sourceActionArgs(action dsl.Action) []string {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["args"]
	if !ok {
		return nil
	}
	values := anySlice(raw)
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, strings.TrimSpace(fmt.Sprint(value)))
	}
	return args
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = value
		}
		return out
	case nil:
		return nil
	default:
		return []any{typed}
	}
}

func firstEngineValue(fallback string, values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}

func sourceTransactionStartKey(name string) string {
	return "__lr_transaction." + strings.ToLower(strings.TrimSpace(name)) + ".start"
}

func sourceTransactionStatusKey(name string) string {
	return "__lr_transaction." + strings.ToLower(strings.TrimSpace(name)) + ".status"
}

func sourceTimerStartKey(name string) string {
	return "__lr_timer." + strings.ToLower(strings.TrimSpace(name)) + ".start"
}

func sourceTimerDurationKey(name string) string {
	return "__lr_timer." + strings.ToLower(strings.TrimSpace(name)) + ".duration_ms"
}

func saveParamNameFromAction(action dsl.Action) string {
	for _, key := range []string{"save_param", "saveparam", "save_param_name", "param", "parameter", "name"} {
		if value, ok := actionParamString(action.Params, key); ok {
			return value
		}
	}
	for _, entry := range structuredCallArgs(action) {
		name, _ := entry["name"].(string)
		value, _ := entry["value"].(string)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "saveparam", "save_param", "param", "parameter", "name":
			return value
		}
	}
	return ""
}

func structuredCallArgs(action dsl.Action) []map[string]any {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["call_args"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}

func sourceDateTimeLayout(format string) string {
	switch strings.TrimSpace(format) {
	case "", "%Y-%m-%d %H:%M:%S":
		return defaultDateTimeLayout
	case "%H:%M:%S":
		return defaultTimeLayout
	default:
		layout := strings.TrimSpace(format)
		replacements := []struct {
			old string
			new string
		}{
			{"%Y", "2006"},
			{"%m", "01"},
			{"%d", "02"},
			{"%H", "15"},
			{"%M", "04"},
			{"%S", "05"},
		}
		for _, replacement := range replacements {
			layout = strings.ReplaceAll(layout, replacement.old, replacement.new)
		}
		return layout
	}
}

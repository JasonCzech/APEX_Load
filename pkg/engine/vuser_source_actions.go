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

	"golang.org/x/text/encoding/charmap"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/strftime"
	"github.com/jasonczech/apexload/pkg/protocol"
	protocolhttp "github.com/jasonczech/apexload/pkg/protocol/http"
	"go.opentelemetry.io/otel/trace"
)

func (vu *VUser) handleEngineAction(ctx context.Context, action dsl.Action) (bool, error) {
	if err := vu.liveTableLeaseError(); err != nil {
		return true, err
	}
	switch dsl.CanonicalActionType(action.Type) {
	case "native_javascript":
		return true, vu.executeNativeJavaScript(ctx, action)
	case "live_table":
		return true, vu.executeLiveTableAction(ctx, action)
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
	case "cookie_set":
		return true, vu.setProtocolCookie(ctx, action)
	case "http_async_start":
		return true, vu.startAsyncConversation(ctx, action)
	case "http_async_stop":
		return true, vu.stopAsyncConversation(ctx, vu.interpolate(asyncParamString(action, "id")))
	case "http_sync":
		return true, vu.waitForAsyncCondition(ctx, action)
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
	case "source_json":
		return true, vu.handleSourceJSONAction(ctx, action)
	case "source_xml":
		return true, sourceDocumentActionUnsupported(action)
	case "source_parameter", "source_database", "source_secret":
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

// setProtocolCookie dispatches a cookie_set action (imported web_add_cookie /
// web_remove_cookie) to the protocol's cookie store, resolving templates in
// its parameters first. Protocols without a cookie store treat it as a no-op.
func (vu *VUser) setProtocolCookie(ctx context.Context, action dsl.Action) error {
	if vu == nil || vu.Proto == nil {
		return nil
	}
	setter, ok := vu.Proto.(protocol.CookieSetter)
	if !ok {
		return nil
	}
	params := vu.interpolateAnyMap(action.Params)
	text := func(key string) string {
		value, _ := params[key].(string)
		return value
	}
	flag := func(key string) bool {
		value, _ := params[key].(bool)
		return value
	}
	return setter.SetCookie(ctx, protocol.ScriptCookie{
		Name: text("name"), Value: text("value"), Domain: text("domain"), Path: text("path"),
		Secure: flag("secure"), Remove: flag("remove"),
	})
}

// browserEmulationNewUserEachIteration reports whether browser emulation's
// "simulate a new user on each iteration" is enabled — the flag that clears
// cookies at iteration start, with cache clearing controlled separately.
func browserEmulationNewUserEachIteration(cfg *dsl.ScriptConfig) bool {
	emu := cfg.BrowserEmulation
	return emu != nil && emu.Cache != nil && emu.Cache.NewUserEachIteration != nil && *emu.Cache.NewUserEachIteration
}

// clearIterationUserState applies the cookie and cache resets a new
// iteration's simulated user starts with (browser_emulation new user each
// iteration, http.clear_cookies_per_iteration). It returns the failed step.
func (vu *VUser) clearIterationUserState(ctx context.Context, cfg *dsl.ScriptConfig) (string, error) {
	if vu == nil || cfg == nil {
		return "", nil
	}
	newUser := browserEmulationNewUserEachIteration(cfg)
	if newUser || (vu.Script != nil && vu.Script.Protocol == "http" && cfg.HTTP.ClearCookiesPerIter != nil && *cfg.HTTP.ClearCookiesPerIter) {
		if err := vu.clearProtocolCookies(ctx); err != nil {
			return "cookie clear", err
		}
	}
	if newUser && (cfg.BrowserEmulation.Cache.ClearCacheForNewUser == nil || *cfg.BrowserEmulation.Cache.ClearCacheForNewUser) {
		if err := vu.clearProtocolCache(ctx); err != nil {
			return "cache clear", err
		}
	}
	return "", nil
}

// clearReplayIterationUserState is the debug replay's iteration boundary
// reset. A load run replaces a browser's whole context for
// browser.new_user_per_iteration; a replay keeps its debug-instrumented
// protocol instance, so it clears that browser's cookies and cache instead.
func (vu *VUser) clearReplayIterationUserState(ctx context.Context, cfg *dsl.ScriptConfig) (string, error) {
	if step, err := vu.clearIterationUserState(ctx, cfg); err != nil {
		return step, err
	}
	if vu == nil || cfg == nil || vu.Script == nil || vu.Script.Protocol != "browser" || cfg.Browser.NewUserPerIter == nil || !*cfg.Browser.NewUserPerIter {
		return "", nil
	}
	if err := vu.clearProtocolCookies(ctx); err != nil {
		return "cookie clear", err
	}
	if err := vu.clearProtocolCache(ctx); err != nil {
		return "cache clear", err
	}
	return "", nil
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
		TokenURL:              paramString("token_url"),
		GrantType:             paramString("grant_type"),
		ClientID:              paramString("client_id"),
		ClientSecret:          paramString("client_secret"),
		Scope:                 paramString("scope"),
		Audience:              paramString("audience"),
		Issuer:                paramString("issuer"),
		Subject:               paramString("subject"),
		PrivateKey:            paramString("private_key"),
		RefreshToken:          paramString("refresh_token"),
		Username:              paramString("username"),
		Password:              paramString("password"),
		UnredactedDebugErrors: vu.unredactedDebug,
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
	if stream, _ := actionParamString(action.Params, "output_stream"); stream != "" {
		// An imported fprintf line names the file stream it was written to.
		attrs = append(attrs, "output_stream", stream)
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
	name         string
	startedAt    time.Time
	authoredStep *protocol.BrowserAuthoredStep
	// iteration is the last iteration it was open in (the one that opened
	// it, carried forward while it stays open): a transaction left open is
	// recorded in that iteration even when it is closed after teardown.
	iteration        int64
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
	trace     *sourceTransactionTrace
	llmRollup *protocol.LLMDiagnostics
	// phaseRollup sums the connection-setup and TTFB phases of the requests
	// recorded while the transaction was open, so a multi-request authored
	// transaction reports where its time went instead of zeros.
	phaseRollup *sourceTransactionPhaseRollup
	// stoppedPhaseRollup and stoppedLLMRollup hold what requests add while
	// the transaction is stopped (lr_stop_transaction). A resume puts the
	// stopped interval back in the duration (LoadRunner reports start to end),
	// so it moves them into the rollups; an end while still stopped caps the
	// duration at the stop and drops them.
	stoppedPhaseRollup *sourceTransactionPhaseRollup
	stoppedLLMRollup   *protocol.LLMDiagnostics
	// traceExemplars are the trace contexts of the child requests that sent
	// one while the transaction was open: the slowest, and the first that
	// failed. The synthesized transaction result never goes on the wire
	// itself, so without them a marker-based transaction (lr_start/lr_end)
	// has no trace for live exemplars or the archive's trace_id.
	// stoppedTraceExemplars holds them aside while stopped, like the rollups
	// above.
	traceExemplars        sourceTransactionTraceExemplars
	stoppedTraceExemplars sourceTransactionTraceExemplars
	// asyncDispatchEpoch is the VUser's dispatch epoch when the transaction
	// opened inside an async dispatch (0 outside one): only then does the
	// dispatch's own work (callback requests) roll into it.
	asyncDispatchEpoch uint64
}

type sourceTransactionPhaseRollup struct {
	dns, connect, tls, ttfb time.Duration
	// unsplit counts the child requests that took time but recorded no first
	// byte: a timeout or connection failure, or a protocol that records no
	// TTFB (gRPC, JDBC, WebSocket, ...). Their time after setup is neither
	// server nor download time.
	unsplit int64
}

func (r *sourceTransactionPhaseRollup) add(dns, connect, tls, ttfb time.Duration) {
	if r == nil {
		return
	}
	r.dns += max(0, dns)
	r.connect += max(0, connect)
	r.tls += max(0, tls)
	r.ttfb += max(0, ttfb)
}

// merge adds another rollup (or one child's sample) into r.
func (r *sourceTransactionPhaseRollup) merge(other sourceTransactionPhaseRollup) {
	if r == nil {
		return
	}
	r.add(other.dns, other.connect, other.tls, other.ttfb)
	r.unsplit += max(0, other.unsplit)
}

// sourceTransactionPhaseSample is one child result's contribution to the open
// authored transactions' phase rollups; ok is false when it contributes
// nothing (no phase time, and not a timed protocol step without a first
// byte). A protocol step that took time without a first byte (DependencyType
// names the protocol, so engine bookkeeping rows are left out) marks the
// rollup unsplit, unless it never went to the network
// (resultResolvedLocally): a cache hit or a locally resolved step has no
// first byte to miss, so its time stays in the transaction without
// discarding the TTFB of the requests that were answered.
func sourceTransactionPhaseSample(result *protocol.Result) (sample sourceTransactionPhaseRollup, ok bool) {
	if result == nil {
		return sample, false
	}
	sample.add(result.DNSTime, result.ConnectTime, result.TLSTime, result.TTFB)
	if result.TTFB <= 0 && result.Duration > 0 && strings.TrimSpace(result.DependencyType) != "" && !resultResolvedLocally(result) {
		sample.unsplit = 1
	}
	return sample, sample.unsplit > 0 || sample.dns > 0 || sample.connect > 0 || sample.tls > 0 || sample.ttfb > 0
}

// resultResolvedLocally reports a result that made no network exchange, so
// its missing TTFB is not a missing response: a response served from the
// browser-cache simulation (HTTPVersionSource or
// protocol_evidence.response_content_source "cache"), or an HTTP step that
// finished before dispatching a request (an HTML step resolved against the
// page context, a refused action), which keeps ByteEvidenceNone because
// the HTTP adapter sets body evidence only once it dispatches or serves from
// the cache. Other protocols' ByteEvidenceNone steps (WebSocket handshakes,
// pings, closes) are network exchanges without a TTFB and stay unsplit.
func resultResolvedLocally(result *protocol.Result) bool {
	if result == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(result.HTTPVersionSource), "cache") {
		return true
	}
	if source, ok := result.ProtocolEvidence["response_content_source"].(string); ok && strings.EqualFold(strings.TrimSpace(source), "cache") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(result.DependencyType), "http") && result.ByteEvidence == protocol.ByteEvidenceNone
}

// boundedTo returns the rollup bounded to the transaction's own duration so
// that setup (DNS+connect+TLS) <= TTFB <= duration (setup <= duration without
// a first byte). Then setup + server + download + no-response never exceeds
// the duration in the live phase stack or the report.
//
// A transaction sample has one TTFB, so its time after the TTFB is all
// download. When any child request took time without a first byte (unsplit:
// a timeout, or a protocol with no TTFB), that time would be charted as
// download, so the rollup reports no TTFB: the transaction keeps its setup
// phases and the rest of its time goes to the unsplit (no-TTFB) bucket, never
// to server or download.
//
// The sums are serial time, so overlapping requests (web_concurrent groups,
// parallel branches) can add up to more than the transaction took. The rule
// is to scale, never to clamp in phase order: when the TTFB sum exceeds the
// duration, all four phases scale by duration/TTFB-sum, so each keeps its
// share (setup vs server time) of the overlapped work; when the setup sum
// still exceeds the bounded TTFB (requests that set up but got no first
// byte), the three setup phases scale together to fit it. Clamping in order
// would hand the whole budget to DNS and connect and zero TLS and server
// time. The request rows keep the unscaled detail.
func (r *sourceTransactionPhaseRollup) boundedTo(duration time.Duration) (dns, connect, tls, ttfb time.Duration) {
	if r == nil {
		return 0, 0, 0, 0
	}
	duration = max(0, duration)
	dns, connect, tls, ttfb = r.dns, r.connect, r.tls, r.ttfb
	if r.unsplit > 0 {
		ttfb = 0
	}
	if ttfb > duration {
		dns = scaleDuration(dns, duration, ttfb)
		connect = scaleDuration(connect, duration, ttfb)
		tls = scaleDuration(tls, duration, ttfb)
		ttfb = duration
	}
	limit := duration
	if ttfb > 0 {
		limit = ttfb
	}
	if setup := dns + connect + tls; setup > limit {
		dns = scaleDuration(dns, limit, setup)
		connect = scaleDuration(connect, limit, setup)
		tls = scaleDuration(tls, limit, setup)
		// Rounding can leave the scaled sum a nanosecond over the limit.
		if over := dns + connect + tls - limit; over > 0 {
			dns = max(0, dns-over)
		}
	}
	return dns, connect, tls, ttfb
}

// hasPhases reports a rollup with phase time to put on a transaction (one
// holding only unsplit children reports what a transaction without a rollup
// does).
func (r *sourceTransactionPhaseRollup) hasPhases() bool {
	return r != nil && (r.dns > 0 || r.connect > 0 || r.tls > 0 || r.ttfb > 0)
}

// scaleDuration returns value * num / den (den > 0), rounded down.
func scaleDuration(value, num, den time.Duration) time.Duration {
	if value <= 0 || num <= 0 || den <= 0 {
		return 0
	}
	return time.Duration(float64(value) * (float64(num) / float64(den)))
}

// applySourceTransactionRollups puts the transaction's child phase and LLM
// rollups on its synthesized source result (creating one from fallback when
// source is nil and there is a rollup), bounded to duration. The end marker
// and the failure close (finalizeSourceTransaction) both use it, so a
// transaction closed either way reports the same anatomy.
func applySourceTransactionRollups(source *protocol.Result, state *sourceTransactionRuntimeState, duration time.Duration, fallback func() *protocol.Result) *protocol.Result {
	if state == nil || (!state.phaseRollup.hasPhases() && state.llmRollup == nil && state.traceExemplars.empty()) {
		return source
	}
	if source == nil {
		// recordTransaction keeps the VUser's region, network profile and
		// phase for fields this synthesized source leaves empty.
		source = fallback()
	}
	if state.phaseRollup.hasPhases() {
		source.DNSTime, source.ConnectTime, source.TLSTime, source.TTFB = state.phaseRollup.boundedTo(duration)
	}
	if state.llmRollup != nil {
		source.ServerTelemetry = &protocol.ServerTelemetry{LLM: state.llmRollup}
	}
	// A failed transaction's trace is its error evidence (first_error
	// exemplar, archive error exemplar, error-priority provider lookups), so
	// it points at the child that failed when one sent a trace.
	if exemplar := state.traceExemplars.pick(source.Success); exemplar != nil && source.TraceContext == nil {
		sent := exemplar.context
		source.TraceContext = &sent
	}
	return source
}

type sourceTransactionTrace struct {
	root trace.SpanContext
	// end ends an exported root span; it runs at most once.
	end func()
}

// sourceTransactionTraceExemplar is a child request's sent trace context,
// its duration and whether it failed, so a transaction keeps the slowest one
// and the first failed one.
type sourceTransactionTraceExemplar struct {
	context  protocol.SentTraceContext
	duration time.Duration
	failed   bool
}

// sourceTransactionTraceExemplars holds a transaction's candidate traces: the
// slowest child that sent one, and the first failed child that sent one. A
// failed transaction takes the failed child's trace (its error evidence must
// show the error); a passing one, or a failed one whose failing children
// sent none, takes the slowest.
type sourceTransactionTraceExemplars struct {
	slowest     *sourceTransactionTraceExemplar
	firstFailed *sourceTransactionTraceExemplar
}

func (s sourceTransactionTraceExemplars) empty() bool {
	return s.slowest == nil && s.firstFailed == nil
}

// offer adds one child's exemplar.
func (s *sourceTransactionTraceExemplars) offer(candidate sourceTransactionTraceExemplar) {
	keepSlowerTraceExemplar(&s.slowest, candidate)
	if candidate.failed && s.firstFailed == nil {
		next := candidate
		s.firstFailed = &next
	}
}

// merge adds exemplars collected later (a parallel branch, or the stopped
// interval on resume): the earlier failure stays first.
func (s *sourceTransactionTraceExemplars) merge(later sourceTransactionTraceExemplars) {
	if later.slowest != nil {
		keepSlowerTraceExemplar(&s.slowest, *later.slowest)
	}
	if s.firstFailed == nil && later.firstFailed != nil {
		next := *later.firstFailed
		s.firstFailed = &next
	}
}

// pick returns the exemplar for a transaction with the given outcome.
func (s sourceTransactionTraceExemplars) pick(success bool) *sourceTransactionTraceExemplar {
	if !success && s.firstFailed != nil {
		return s.firstFailed
	}
	return s.slowest
}

// sourceTransactionTraceExemplarOf returns the trace context a request really
// sent (a cache hit or a malformed header has none to look up).
func sourceTransactionTraceExemplarOf(result *protocol.Result) (sourceTransactionTraceExemplar, bool) {
	if result == nil || result.TraceContext.LookupTraceID() == "" {
		return sourceTransactionTraceExemplar{}, false
	}
	return sourceTransactionTraceExemplar{context: *result.TraceContext, duration: result.Duration, failed: !result.Success}, true
}

// keepSlowerTraceExemplar keeps the slower of *target and candidate; on a tie
// the earlier one stays, so the pick does not depend on map order.
func keepSlowerTraceExemplar(target **sourceTransactionTraceExemplar, candidate sourceTransactionTraceExemplar) {
	if *target == nil || candidate.duration > (*target).duration {
		next := candidate
		*target = &next
	}
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
	if current := vu.currentIteration.Load(); current != state.iteration {
		vu.currentIteration.Store(state.iteration)
		defer vu.currentIteration.Store(current)
	}
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
	vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(false)
	if vu.interruptedByStop() {
		// The run stopped before the end marker could run: the transaction is
		// incomplete, not failed, so it is left out of the results.
		return nil
	}
	source := &protocol.Result{
		ActionName:   name,
		StartTime:    state.startedAt,
		Success:      false,
		ErrorMessage: reason,
		AuthoredStep: state.authoredStep.Clone(),
	}
	source = applySourceTransactionRollups(source, state, duration, nil)
	// The wall-clock span keeps think time inside the transaction in its
	// interval (journey accounting reads it).
	prevAuthoredAction := vu.currentAuthoredAction
	vu.currentAuthoredAction = nil
	defer func() { vu.currentAuthoredAction = prevAuthoredAction }()
	defer vu.recordingSourceTransaction(state)()
	return vu.recordTransactionSpan(name, duration, false, source, state.startedAt)
}

// recordingSourceTransaction makes the transaction row recorded next carry
// the dispatch epoch the authored transaction opened in (none when it opened
// outside a dispatch, even if it closes inside one), and returns the restore.
func (vu *VUser) recordingSourceTransaction(state *sourceTransactionRuntimeState) func() {
	prevEpoch, prevSet := vu.recordingTransactionEpoch, vu.recordingTransactionEpochSet
	vu.recordingTransactionEpoch, vu.recordingTransactionEpochSet = 0, true
	if state != nil {
		vu.recordingTransactionEpoch = state.asyncDispatchEpoch
	}
	return func() { vu.recordingTransactionEpoch, vu.recordingTransactionEpochSet = prevEpoch, prevSet }
}

// transactionDispatchEpoch is the async_dispatch_epoch a transaction row
// carries: an authored transaction's own (recordingSourceTransaction), else
// the current dispatch's for one recorded during a dispatch (an http_sequence
// aggregate run from a callback), else 0 (none).
func (vu *VUser) transactionDispatchEpoch() uint64 {
	if vu.recordingTransactionEpochSet {
		return vu.recordingTransactionEpoch
	}
	if vu.asyncDispatchActive() {
		return vu.asyncDispatchEpoch
	}
	return 0
}

// carryOpenSourceTransactions moves the transactions still open when a new
// iteration starts into that iteration (an lr_exit that continues with the
// next iteration leaves them open).
func (vu *VUser) carryOpenSourceTransactions(iteration int64) {
	for _, state := range vu.sourceTransactions {
		if state != nil && state.iteration > 0 && state.iteration < iteration {
			state.iteration = iteration
		}
	}
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

// renamedTransactionEndHint explains an end marker that import
// de-duplication renamed. Recorded transactions used to name their start and
// end markers alike without arguments, so a save renamed the end "<name>_2"
// and it closed nothing; such a script needs the transaction in the end's
// first argument.
func (vu *VUser) renamedTransactionEndHint(action dsl.Action, endName string) string {
	if len(sourceActionArgs(action)) > 0 {
		return ""
	}
	cut := strings.LastIndexByte(endName, '_')
	if cut <= 0 || strings.Trim(endName[cut+1:], "0123456789") != "" || cut == len(endName)-1 {
		return ""
	}
	state := vu.sourceTransactions[strings.ToLower(strings.TrimSpace(endName[:cut]))]
	if state == nil {
		return ""
	}
	return fmt.Sprintf(" (an import renamed this step from %q: set its first argument to %q, or record the script again)", state.name, state.name)
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

func sourceTransactionSourceResult(name, status string, startedAt time.Time, authoredStep *protocol.BrowserAuthoredStep) *protocol.Result {
	result := sourceTransactionFailureResult(name, status, startedAt)
	if !authoredStep.Valid() {
		return result
	}
	if result == nil {
		return &protocol.Result{
			ActionName:   name,
			StartTime:    startedAt,
			Success:      true,
			AuthoredStep: authoredStep.Clone(),
		}
	}
	result.AuthoredStep = authoredStep.Clone()
	return result
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
		// The identity is LoadRunner's first call argument; Name, a display
		// label, is only the fallback for a marker without arguments.
		name := startName
		key := strings.ToLower(name)
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
			name:         name,
			startedAt:    startedAt,
			authoredStep: vu.currentBrowserAuthoredStep(),
			status:       sourceTransactionPass,
			parent:       parentKey,
			iteration:    vu.currentIteration.Load(),
		}
		if vu.asyncDispatchActive() {
			vu.sourceTransactions[key].asyncDispatchEpoch = vu.asyncDispatchEpoch
		}
		vu.startSourceTransactionTraceRoot(ctx, vu.sourceTransactions[key])
		return nil
	case "lr_end_transaction", "lr_end_sub_transaction", "lr_end_sub_trans":
		endName, requestedStatus, ok := sourceTransactionEndArguments(action)
		if !ok {
			return fmt.Errorf("%s requires a transaction name and PASS, FAIL, STOP, or AUTO status", function)
		}
		key := strings.ToLower(strings.TrimSpace(endName))
		state := vu.sourceTransactions[key]
		if state == nil {
			return fmt.Errorf("%s references transaction %q with no matching open transaction%s", function, endName, vu.renamedTransactionEndHint(action, endName))
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
		prevAuthoredAction := vu.currentAuthoredAction
		vu.currentAuthoredAction = nil
		defer func() { vu.currentAuthoredAction = prevAuthoredAction }()
		txSource := sourceTransactionSourceResult(endName, status, state.startedAt, state.authoredStep)
		txSource = applySourceTransactionRollups(txSource, state, duration, func() *protocol.Result {
			return &protocol.Result{
				ActionName: nameWithoutSpaces(endName),
				StartTime:  state.startedAt,
				Success:    success,
			}
		})
		restoreEpoch := vu.recordingSourceTransaction(state)
		vu.recordTransactionSpan(endName, duration, success, txSource, state.startedAt)
		restoreEpoch()
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
		state.resumeRollups()
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

func nameWithoutSpaces(s string) string {
	return strings.TrimSpace(s)
}

// noteSourceTransactionPhases adds a request's phases to the open authored
// transactions (nested ones included) that take it (sourceTransactionTakes).
// The sums are serial time: requests that overlap (concurrent resources) can
// add up to more than the elapsed time.
//
// Async-conversation responses and background requests are never part of an
// authored transaction (see applyAsyncResults), so they are left out. A
// parallel branch's requests reach the parent's transactions through the
// branch's result sink (see parallelResultSink).
func (vu *VUser) noteSourceTransactionPhases(result *protocol.Result) {
	if vu == nil || result == nil || result.BusinessTransaction || resultOutsideSourceTransactions(result) {
		return
	}
	if exemplar, ok := sourceTransactionTraceExemplarOf(result); ok {
		var offered sourceTransactionTraceExemplars
		offered.offer(exemplar)
		vu.offerSourceTransactionTraces(offered, vu.resultFromAsyncDispatch(result))
	}
	sample, ok := sourceTransactionPhaseSample(result)
	if !ok {
		return
	}
	vu.addSourceTransactionPhases(sample, vu.resultFromAsyncDispatch(result))
}

// addSourceTransactionPhases adds one request's (or a parallel branch's)
// phases to every open authored transaction that takes them; dispatch marks
// work recorded during an async dispatch. A stopped transaction
// (lr_stop_transaction) holds them aside: its duration ends at the stop
// unless it is resumed, and a resume puts the stopped interval back in the
// duration, so the held phases join the rollup only then (see resumeRollups).
func (vu *VUser) addSourceTransactionPhases(sample sourceTransactionPhaseRollup, dispatch bool) {
	if vu == nil {
		return
	}
	if sample.unsplit <= 0 && sample.dns <= 0 && sample.connect <= 0 && sample.tls <= 0 && sample.ttfb <= 0 {
		return
	}
	for _, state := range vu.sourceTransactions {
		if !vu.sourceTransactionTakes(state, dispatch) {
			continue
		}
		target := &state.phaseRollup
		if !state.stoppedAt.IsZero() {
			target = &state.stoppedPhaseRollup
		}
		if *target == nil {
			*target = &sourceTransactionPhaseRollup{}
		}
		(*target).merge(sample)
	}
}

// offerSourceTransactionTraces offers a child request's (or a parallel
// branch's) sent trace contexts to every open authored transaction that takes
// them (the same rule as phases).
func (vu *VUser) offerSourceTransactionTraces(exemplars sourceTransactionTraceExemplars, dispatch bool) {
	if vu == nil || exemplars.empty() {
		return
	}
	for _, state := range vu.sourceTransactions {
		if !vu.sourceTransactionTakes(state, dispatch) {
			continue
		}
		if state.stoppedAt.IsZero() {
			state.traceExemplars.merge(exemplars)
		} else {
			state.stoppedTraceExemplars.merge(exemplars)
		}
	}
}

// resumeRollups moves what requests added while the transaction was stopped
// into its rollups: the resumed duration includes the stopped interval.
func (state *sourceTransactionRuntimeState) resumeRollups() {
	if state == nil {
		return
	}
	if held := state.stoppedPhaseRollup; held != nil {
		if state.phaseRollup == nil {
			state.phaseRollup = &sourceTransactionPhaseRollup{}
		}
		state.phaseRollup.merge(*held)
	}
	if state.stoppedLLMRollup != nil {
		protocol.MergeLLMHopDiagnostics(&state.llmRollup, state.stoppedLLMRollup)
	}
	state.traceExemplars.merge(state.stoppedTraceExemplars)
	state.stoppedPhaseRollup, state.stoppedLLMRollup, state.stoppedTraceExemplars = nil, nil, sourceTransactionTraceExemplars{}
}

// resultOutsideSourceTransactions reports a result that never rolls into an
// authored transaction: an async-conversation response or a background
// request. They run beside the script's flow, not inside the transaction
// that happened to be open when they were dispatched.
func resultOutsideSourceTransactions(result *protocol.Result) bool {
	if result == nil || result.ProtocolEvidence == nil {
		return false
	}
	if _, ok := result.ProtocolEvidence["async_conversation"]; ok {
		return true
	}
	if background, ok := result.ProtocolEvidence["background_request"].(bool); ok && background {
		return true
	}
	return false
}

// asyncDispatchEpochEvidence is the protocol_evidence key (int64, > 0)
// naming the async dispatch a result was recorded in, stamped beside
// async_dispatch on every dispatch result; a business-transaction row carries
// the epoch its authored transaction opened in (or, for another transaction
// row recorded during a dispatch, that dispatch's). The report lets a
// dispatch request row be claimed only by a transaction row with the same
// epoch, mirroring sourceTransactionTakes.
const asyncDispatchEpochEvidence = "async_dispatch_epoch"

// asyncDispatchActive reports whether what the VUser records now is async
// dispatch work: it is dispatching conversation results (callbacks
// included), or it is a parallel branch whose block started from a callback.
func (vu *VUser) asyncDispatchActive() bool {
	return vu != nil && (vu.inAsyncDispatch || vu.asyncDispatchBranch)
}

// resultFromAsyncDispatch reports a result recorded during an async dispatch
// (recordResult stamps protocol_evidence.async_dispatch on those).
func (vu *VUser) resultFromAsyncDispatch(result *protocol.Result) bool {
	if vu.asyncDispatchActive() {
		return true
	}
	if result == nil || result.ProtocolEvidence == nil {
		return false
	}
	dispatch, ok := result.ProtocolEvidence["async_dispatch"].(bool)
	return ok && dispatch
}

// sourceTransactionTakes reports whether an open authored transaction takes
// a request's rollup. Work recorded outside an async dispatch joins every
// open transaction. Dispatch work (callback steps, and parallel branches
// started from a callback) joins only the transactions opened during that
// same dispatch: a callback's lr_start/end pair rolls up its own requests,
// while a transaction that was already open when the dispatch began never
// takes them. Every transaction a dispatch branch VUser holds opened inside
// the branch, so inside the dispatch.
func (vu *VUser) sourceTransactionTakes(state *sourceTransactionRuntimeState, dispatch bool) bool {
	if state == nil {
		return false
	}
	if !dispatch || vu.asyncDispatchBranch {
		return true
	}
	return vu.inAsyncDispatch && state.asyncDispatchEpoch != 0 && state.asyncDispatchEpoch == vu.asyncDispatchEpoch
}

func (vu *VUser) noteSourceTransactionLLM(result *protocol.Result) {
	if vu == nil || result == nil || result.BusinessTransaction || result.ServerTelemetry == nil || result.ServerTelemetry.LLM == nil || resultOutsideSourceTransactions(result) {
		return
	}
	dispatch := vu.resultFromAsyncDispatch(result)
	for _, state := range vu.sourceTransactions {
		if !vu.sourceTransactionTakes(state, dispatch) {
			continue
		}
		if state.stoppedAt.IsZero() {
			protocol.MergeLLMHopDiagnostics(&state.llmRollup, result.ServerTelemetry.LLM)
		} else {
			protocol.MergeLLMHopDiagnostics(&state.stoppedLLMRollup, result.ServerTelemetry.LLM)
		}
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
	// Dispatch on the canonical type, as handleEngineAction does: a legacy
	// loadrunner_parameter or loadrunner_secret step must save its value, which
	// replay readiness (pkg/dsl/varflow) already assumes.
	switch dsl.CanonicalActionType(action.Type) {
	case "source_parameter":
		vu.handleSourceParameterAction(function, action)
	case "source_secret":
		vu.handleSourceSecretAction(function, action)
	default:
		vu.logSourceReviewAction(ctx, action)
	}
}

func (vu *VUser) handleSourceParameterAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	// Values keep their exact text (a saved " " or a "%s " format); names and
	// numbers use the trimmed args.
	values := sourceActionValueArgs(action)
	vu.ensureVariables()
	switch function {
	case "lr_save_string", "lr_save_int":
		if len(args) >= 2 {
			if array, ok := action.Params["source_array"].(map[string]any); ok {
				// Array entries are already runtime values. Interpolating the selected
				// entry again would treat template-looking response bytes as source.
				vu.setResolvedSourceVariable(args[1], vu.sourceParameterArrayValue(array))
			} else {
				vu.setSourceVariable(args[1], values[0])
			}
		}
	case "lr_save_var":
		if len(args) >= 4 {
			vu.setResolvedSourceVariable(args[3], sourceSaveVarValue(vu.interpolate(values[0]), action.Params["source_offset"], args[1]))
		}
	case "lr_param_sprintf":
		if len(args) >= 2 {
			formatted := make([]any, 0, len(values)-2)
			for _, arg := range values[2:] {
				formatted = append(formatted, arg)
			}
			vu.setSourceVariable(args[0], renderPrintfLikeMessage(values[1], formatted))
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
	case "web_save_timestamp_param":
		// Milliseconds since the Unix epoch, as LoadRunner saves it.
		if len(args) >= 1 {
			vu.setSourceVariable(args[0], strconv.FormatInt(time.Now().UnixMilli(), 10))
		}
	case "web_save_param_length":
		// The length of an existing parameter's value, in decimal, saved as
		// <Param>_Length. The importer keeps Base=Hexadecimal for review.
		if len(args) >= 1 {
			value := vu.Variables[strings.ToLower(strings.TrimSpace(args[0]))]
			length := len(value)
			if codePage, _ := actionParamString(action.Params, "code_page"); strings.EqualFold(strings.TrimSpace(codePage), "windows-1252") {
				// A code-page script counts the value's Windows-1252 bytes.
				if encoded, err := charmap.Windows1252.NewEncoder().String(value); err == nil {
					length = len(encoded)
				}
			}
			vu.setSourceVariable(strings.TrimSpace(args[0])+"_Length", strconv.Itoa(length))
		}
	case "web_convert_date_param":
		vu.handleWebConvertDateParamAction(args)
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

// handleWebConvertDateParamAction rewrites a date parameter in place from
// SourceFormat= to TargetFormat= (strftime specifiers). A value that does not
// match the source format is left unchanged and logged.
func (vu *VUser) handleWebConvertDateParamAction(args []string) {
	if len(args) < 1 {
		return
	}
	name := strings.TrimSpace(args[0])
	sourceFormat, targetFormat := "", ""
	for _, arg := range args[1:] {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "sourceformat":
			sourceFormat = value
		case "targetformat":
			targetFormat = value
		}
	}
	current := vu.Variables[strings.ToLower(name)]
	// The conversion is field for field, so parse in UTC: a local time zone
	// would move a time inside a daylight-saving gap.
	parsed, err := strftime.Parse(current, sourceFormat, time.UTC)
	if err == nil {
		var converted string
		if converted, err = strftime.Format(parsed, targetFormat); err == nil {
			vu.setSourceVariable(name, converted)
			return
		}
	}
	vu.runtimeLogger().Warn("web_convert_date_param left the parameter unchanged", "vuser", vu.ID, "parameter", name, "error", err)
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
	} else if len(args) > 1 {
		// A save_param step may carry no args; args[1:] would then panic.
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

// sourceDocumentActionUnsupported fails LoadRunner JSON/XML document helpers
// (lr_eval_json, lr_json_get_values, lr_xml_find, ...). APEX does not evaluate
// their queries yet; the former handler stored an unrelated argument literal
// under a guessed variable name and reported success, so dependent requests
// silently sent wrong values. Current imports convert these calls to gated
// source_review steps; this covers drafts saved before that change.
func sourceDocumentActionUnsupported(action dsl.Action) error {
	function := strings.TrimSpace(sourceActionFunction(action))
	if function == "" {
		function = action.Type
	}
	return fmt.Errorf("LoadRunner %s is not executable in APEX yet; replace this step with an extractor on the producing request, or remove it", function)
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

// sourceSaveVarValue applies lr_save_var's byte semantics to a resolved value:
// the source pointer may be offset into the string (lr_eval_string("{p}") + 3,
// as a translated strncpy produces), and at most length bytes are saved.
func sourceSaveVarValue(value string, offset any, length string) string {
	if start, ok := sourceIntegerParam(offset); ok && start > 0 {
		if start >= len(value) {
			return ""
		}
		value = value[start:]
	}
	if limit, err := strconv.Atoi(strings.TrimSpace(length)); err == nil && limit >= 0 && limit < len(value) {
		value = value[:limit]
	}
	return value
}

func sourceIntegerParam(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed == math.Trunc(typed) {
			return int(typed), true
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	}
	return 0, false
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

// sourceActionValueArgs returns the action's arguments without trimming.
func sourceActionValueArgs(action dsl.Action) []string {
	if action.Params == nil {
		return nil
	}
	values := anySlice(action.Params["args"])
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, fmt.Sprint(value))
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

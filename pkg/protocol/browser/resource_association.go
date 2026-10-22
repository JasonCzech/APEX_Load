package browser

import (
	"crypto/sha256"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// Resource ↔ document association. Nothing here guesses a document from a
// frame's current URL: a request joins a document only through evidence that
// names it, and anything ambiguous stays unavailable with its reason.
//
// Chromium: a dedicated CDP session per page observes requests, responses,
// memory-cache hits, frame commits and execution contexts. A Playwright
// request joins the CDP request of the same page, method and URL at the same
// ordinal, checked against its start time and the frame mapping learned so
// far. The join gives the issuing document's loader, cache and initiator
// provenance. A loader becomes a document through the document itself: the
// bootstrap reports its document token to a binding of the evidence session,
// and the binding call names the execution context it ran in. Commits,
// context creation and binding calls arrive in order on that one session, so
// the context belongs to the loader its frame had committed when the context
// was created; a context that predates its frame's latest commit (a late call
// from a replaced document) or the binding itself proves nothing. No clock is
// compared across the renderer and browser processes. Enabling Runtime on
// this session also reports console calls and exceptions to it, an inspector
// cost that only opted-in document capture pays.
//
// WebKit: a request joins the one document of its frame holding exactly one
// Resource Timing entry for its URL (page_resource_timing_join); its document
// offset is that entry's startTime. Navigation
// requests never join: a document has no entry for its own navigation, and
// an iframe navigation's entry sits in the parent document. The join runs
// when the request completes, and an entry that reaches the controller later
// leaves the request resource_timing_unobserved. Cache and initiator remain
// not_exposed.

const (
	resourceAssociationLimit = 4096
	// The CDP start is reproduced with Playwright's own formula, so the two
	// agree to rounding; a larger gap means a misaligned ordinal.
	cdpJoinToleranceMS        = 5.0
	resourceTimingPerDocument = 512
	// cdpCommitFrameLimit bounds the frames whose commits a page keeps.
	cdpCommitFrameLimit = 256
)

type resourceJoinKey [32]byte

func resourceKey(method, url string) resourceJoinKey {
	return sha256.Sum256([]byte(method + " " + url))
}

// resourceJoin is what a completed request needs to find its CDP record or
// Resource Timing entry; the URL itself is never retained.
type resourceJoin struct {
	page    playwright.Page
	frame   playwright.Frame
	key     resourceJoinKey
	ordinal int // 1-based within page, method and URL; 0 when not joinable
	urlHash string
}

type cdpRequestRecord struct {
	requestID, loaderID, frameID string
	wallMS                       float64
	// timestampS is the monotonic request time; startMS is Playwright's
	// start (wall time shifted by the response's requestTime) once known.
	timestampS                     float64
	startMS                        float64
	initiatorType, initiatorOrigin string
	cacheKnown, cached             bool
	// protocol is CDP's Response.protocol, bounded; responded records that
	// a responseReceived event arrived at all.
	protocol  string
	responded bool
}

// maxBrowserProtocolBytes bounds an engine-reported protocol string before it
// is normalized; real values are a few bytes.
const maxBrowserProtocolBytes = 32

// browserResourceProtocol decides the negotiated protocol of one request from
// what the engine reported. A cached or service-worker response never claims
// a network negotiation, and an unreported protocol is never inferred.
func browserResourceProtocol(raw string, responded, cached bool, serviceWorker *bool) (string, string) {
	switch {
	case !responded:
		return "", "no_response"
	case cached:
		return "", "cached"
	case serviceWorker != nil && *serviceWorker:
		return "", "service_worker"
	}
	if len(raw) > maxBrowserProtocolBytes {
		return "", "unsupported"
	}
	return protocol.NormalizeBrowserResourceProtocol(raw)
}

// cdpFrameCommit is a frame's latest committed loader; commit counts the
// frame's commits, so a context created before the latest one is stale.
type cdpFrameCommit struct {
	loaderID string
	commit   uint64
}

// cdpContextLoader is the loader a frame had committed when one of its
// default execution contexts was created.
type cdpContextLoader struct {
	frameID, loaderID string
	commit, created   uint64
}

type cdpEvidencePage struct {
	expectedLoaders     map[string]string // Native loader -> frame, retained independently of JS receipts.
	discoveryIncomplete bool
	records             map[resourceJoinKey][]*cdpRequestRecord
	latest              map[string]*cdpRequestRecord
	served              map[string]bool
	frames              map[string]cdpFrameCommit
	contexts            map[int64]cdpContextLoader
	// loaderTokens is the document token each loader's document reported
	// from its own context; a loader reporting two different tokens is
	// ambiguous.
	loaderTokens     map[string]string
	ambiguousLoaders map[string]bool
	// contextsSeen numbers default contexts in creation order. Only contexts
	// created after the binding was installed (beyond provableAfter) can
	// prove a document: an older context never ran the bootstrap's report
	// and leaves the binding to page scripts.
	contextsSeen  uint64
	provableAfter uint64
	bindingReady  bool
	ordinals      map[resourceJoinKey]int
	count         int
	truncated     bool
}

type loaderDocument struct {
	id         string
	timeOrigin float64
}

// cdpDocumentTokenLength is the bootstrap's document token: 16 random bytes
// as lowercase hex.
const cdpDocumentTokenLength = 32

type resourceTimingEntry struct {
	count int
	start float64
	// protocol is the entry's nextHopProtocol when the page exposed one;
	// cached marks a zero transferSize.
	protocol              string
	protocolKnown, cached bool
}

// resourceTimingKey counts a frame's requests for one URL hash: a URL
// requested twice can never be told apart by its Resource Timing entries.
type resourceTimingKey struct {
	frame playwright.Frame
	hash  string
}

func newCDPEvidencePage() *cdpEvidencePage {
	return &cdpEvidencePage{expectedLoaders: map[string]string{}, records: map[resourceJoinKey][]*cdpRequestRecord{}, latest: map[string]*cdpRequestRecord{}, served: map[string]bool{}, frames: map[string]cdpFrameCommit{}, contexts: map[int64]cdpContextLoader{}, loaderTokens: map[string]string{}, ambiguousLoaders: map[string]bool{}, ordinals: map[resourceJoinKey]int{}}
}

// installResourceAssociation attaches the evidence CDP session to a newly
// registered page. Only opted-in document evidence on Chromium needs it.
func (b *Browser) installResourceAssociation(browserContext playwright.BrowserContext, page playwright.Page, generation string) {
	if browserContext == nil || page == nil || !b.documentMetricsEnabled || !b.supportsCDP() {
		return
	}
	b.mu.Lock()
	_, attempted := b.evidence.cdpPages[page]
	if attempted || b.evidence.cdpPages == nil || b.evidence.generation != generation {
		b.mu.Unlock()
		return
	}
	// A nil entry reserves the page against a concurrent second install.
	b.evidence.cdpPages[page] = nil
	loaderBinding := b.evidence.loaderBinding
	b.mu.Unlock()
	var cdp playwright.CDPSession
	var err error
	if b.createCDPSession != nil {
		cdp, err = b.createCDPSession(browserContext, page)
	} else {
		cdp, err = browserContext.NewCDPSession(page)
	}
	if err != nil || cdp == nil {
		b.mu.Lock()
		b.evidence.nativeDiscoveryIncomplete = true
		b.mu.Unlock()
		return
	}
	state := newCDPEvidencePage()
	cdp.On("Page.frameAttached", func(map[string]any) { b.markNativeDiscoveryIncomplete(generation) })
	cdp.On("Page.frameDetached", func(params map[string]any) {
		if cdpText(params, "reason") == "swap" {
			b.markNativeDiscoveryIncomplete(generation)
		}
	})
	cdp.On("Network.requestWillBeSent", func(params map[string]any) { b.observeCDPRequest(state, params, generation) })
	cdp.On("Network.responseReceived", func(params map[string]any) { b.observeCDPResponse(state, params, generation) })
	cdp.On("Network.requestServedFromCache", func(params map[string]any) { b.observeCDPCacheHit(state, params, generation) })
	cdp.On("Page.frameNavigated", func(params map[string]any) { b.observeCDPCommit(state, params, generation) })
	cdp.On("Runtime.executionContextCreated", func(params map[string]any) { b.observeCDPContextCreated(state, params, generation) })
	cdp.On("Runtime.executionContextDestroyed", func(params map[string]any) { b.observeCDPContextDestroyed(state, params, generation) })
	cdp.On("Runtime.executionContextsCleared", func(map[string]any) { b.observeCDPContextsCleared(state, generation) })
	cdp.On("Runtime.bindingCalled", func(params map[string]any) { b.observeCDPDocumentReport(state, params, loaderBinding, generation) })
	b.mu.Lock()
	if b.closing || b.evidence.generation != generation {
		b.mu.Unlock()
		_ = cdp.Detach()
		return
	}
	b.evidence.cdpPages[page] = state
	// Additional targets can start before their CDP session attaches. Their
	// pre-attachment history cannot be independently closed by this census.
	if len(b.evidence.nativeSources) > 0 {
		b.evidence.nativeDiscoveryIncomplete = true
	}
	if len(b.evidence.nativeSources) >= browserDocumentLimit {
		b.evidence.nativeDiscoveryIncomplete = true
	} else {
		b.evidence.nativeSources[state] = page
	}
	b.mu.Unlock()
	// This session only reads request metadata: zero buffers keep a second
	// network agent from retaining bodies in the renderer.
	_, err = cdp.Send("Page.enable", map[string]any{})
	if err == nil {
		b.seedNativeInitialDocument(cdp, state, generation)
	}
	if err == nil {
		_, err = cdp.Send("Network.enable", map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0})
	}
	// Runtime reports contexts and this session's own binding, which every
	// later document's bootstrap calls once with its token.
	if err == nil {
		_, err = cdp.Send("Runtime.enable", map[string]any{})
	}
	if err == nil {
		_, err = cdp.Send("Runtime.addBinding", map[string]any{"name": loaderBinding})
	}
	if err == nil {
		// Contexts reported so far, including the replay of existing ones,
		// predate the binding; only later ones can prove a document.
		b.mu.Lock()
		state.provableAfter, state.bindingReady = state.contextsSeen, true
		b.mu.Unlock()
	}
	if err == nil {
		b.joinNativeInitialDocument(cdp, state, generation)
	}
	if err != nil {
		b.mu.Lock()
		b.evidence.nativeDiscoveryIncomplete = true
		delete(b.evidence.cdpPages, page)
		b.mu.Unlock()
		_ = cdp.Detach()
	}
}

func cdpText(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func cdpInitiatorType(raw string) string {
	switch raw {
	case "parser", "script", "preload", "preflight", "other":
		return raw
	case "SignedExchange":
		return "signed_exchange"
	case "":
		return ""
	}
	return "other"
}

func (b *Browser) observeCDPRequest(state *cdpEvidencePage, params map[string]any, generation string) {
	request, _ := params["request"].(map[string]any)
	wall, _ := params["wallTime"].(float64)
	timestamp, _ := params["timestamp"].(float64)
	requestID, url, method := cdpText(params, "requestId"), cdpText(request, "url"), cdpText(request, "method")
	if requestID == "" || url == "" || method == "" {
		return
	}
	record := &cdpRequestRecord{requestID: requestID, loaderID: cdpText(params, "loaderId"), frameID: cdpText(params, "frameId"), wallMS: wall * 1000, timestampS: timestamp}
	if initiator, ok := params["initiator"].(map[string]any); ok {
		record.initiatorType = cdpInitiatorType(cdpText(initiator, "type"))
		record.initiatorOrigin = browserResourceOrigin(cdpText(initiator, "url"))
	}
	redirect, _ := params["redirectResponse"].(map[string]any)
	key := resourceKey(method, url)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	// A redirect ends the previous hop of the same CDP request.
	if previous := state.latest[requestID]; previous != nil && redirect != nil {
		previous.cacheKnown, previous.cached = true, cdpCached(redirect)
		previous.startMS = cdpStartMS(previous, redirect, state.served[requestID])
		recordCDPProtocol(previous, redirect)
	}
	if state.count >= resourceAssociationLimit {
		state.truncated = true
		return
	}
	state.count++
	state.records[key] = append(state.records[key], record)
	state.latest[requestID] = record
}

func cdpCached(response map[string]any) bool {
	disk, _ := response["fromDiskCache"].(bool)
	prefetch, _ := response["fromPrefetchCache"].(bool)
	return disk || prefetch
}

// cdpStartMS reproduces Playwright's request start: the wall time shifted
// by the network timing's requestTime relative to the request's monotonic
// timestamp, so time spent queued in the browser is included. A request
// served from the memory cache, or one without network timing, starts at its
// wall time.
func cdpStartMS(record *cdpRequestRecord, response map[string]any, servedFromCache bool) float64 {
	timing, _ := response["timing"].(map[string]any)
	requestTime, _ := timing["requestTime"].(float64)
	if servedFromCache || requestTime <= 0 || record.timestampS <= 0 {
		return record.wallMS
	}
	return (requestTime - record.timestampS + record.wallMS/1000) * 1000
}

func (b *Browser) observeCDPResponse(state *cdpEvidencePage, params map[string]any, generation string) {
	response, _ := params["response"].(map[string]any)
	requestID := cdpText(params, "requestId")
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	if record := state.latest[requestID]; record != nil && response != nil {
		record.cacheKnown, record.cached = true, cdpCached(response) || state.served[requestID]
		record.startMS = cdpStartMS(record, response, state.served[requestID])
		recordCDPProtocol(record, response)
	}
}

// recordCDPProtocol keeps the protocol a hop's response reported: a final
// response's, or a redirect hop's, which CDP delivers only as the next hop's
// redirectResponse. An oversized value keeps one byte past the bound, so the
// decision reads it as unsupported rather than as a shorter protocol.
func recordCDPProtocol(record *cdpRequestRecord, response map[string]any) {
	record.responded = true
	raw := cdpText(response, "protocol")
	if len(raw) > maxBrowserProtocolBytes {
		raw = raw[:maxBrowserProtocolBytes+1]
	}
	record.protocol = raw
}

func (b *Browser) observeCDPCacheHit(state *cdpEvidencePage, params map[string]any, generation string) {
	requestID := cdpText(params, "requestId")
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation || requestID == "" || len(state.served) >= resourceAssociationLimit {
		return
	}
	state.served[requestID] = true
	if record := state.latest[requestID]; record != nil {
		record.cacheKnown, record.cached = true, true
		record.startMS = record.wallMS
	}
}

func (b *Browser) observeCDPCommit(state *cdpEvidencePage, params map[string]any, generation string) {
	frame, _ := params["frame"].(map[string]any)
	frameID, loaderID := cdpText(frame, "id"), cdpText(frame, "loaderId")
	if frameID == "" || loaderID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	if cdpText(frame, "parentId") != "" {
		state.discoveryIncomplete = true
	}
	if state.expectedLoaders == nil {
		state.expectedLoaders = map[string]string{}
	}
	if _, duplicate := state.expectedLoaders[loaderID]; duplicate {
		state.discoveryIncomplete = true
	} else if len(state.expectedLoaders) >= browserDocumentLimit {
		state.discoveryIncomplete = true
	} else {
		state.expectedLoaders[loaderID] = frameID
	}
	previous, known := state.frames[frameID]
	if !known && len(state.frames) >= cdpCommitFrameLimit {
		state.truncated = true
		return
	}
	state.frames[frameID] = cdpFrameCommit{loaderID: loaderID, commit: previous.commit + 1}
}

func cdpContextID(value any) (int64, bool) {
	id, ok := value.(float64)
	if !ok || id != math.Trunc(id) || math.Abs(id) > 1<<53 {
		return 0, false
	}
	return int64(id), true
}

// observeCDPContextCreated binds a frame's new default context to the loader
// the frame committed last.
func (b *Browser) observeCDPContextCreated(state *cdpEvidencePage, params map[string]any, generation string) {
	context, _ := params["context"].(map[string]any)
	aux, _ := context["auxData"].(map[string]any)
	id, valid := cdpContextID(context["id"])
	isDefault, _ := aux["isDefault"].(bool)
	frameID := cdpText(aux, "frameId")
	if !valid || !isDefault || frameID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	frame, known := state.frames[frameID]
	if !known {
		// A document from before this session observed its frame's commit.
		return
	}
	if len(state.contexts) >= resourceAssociationLimit {
		state.truncated = true
		return
	}
	state.contextsSeen++
	state.contexts[id] = cdpContextLoader{frameID: frameID, loaderID: frame.loaderID, commit: frame.commit, created: state.contextsSeen}
}

func (b *Browser) observeCDPContextDestroyed(state *cdpEvidencePage, params map[string]any, generation string) {
	id, valid := cdpContextID(params["executionContextId"])
	if !valid {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation == generation {
		delete(state.contexts, id)
	}
}

func (b *Browser) observeCDPContextsCleared(state *cdpEvidencePage, generation string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation == generation {
		clear(state.contexts)
	}
}

// observeCDPDocumentReport records which document a loader produced: the
// token the bootstrap reported from a context created after the loader's
// commit, while that commit is still its frame's latest.
func (b *Browser) observeCDPDocumentReport(state *cdpEvidencePage, params map[string]any, binding, generation string) {
	token := cdpText(params, "payload")
	id, valid := cdpContextID(params["executionContextId"])
	if binding == "" || cdpText(params, "name") != binding || !valid || len(token) != cdpDocumentTokenLength || strings.Trim(token, "0123456789abcdef") != "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	context, known := state.contexts[id]
	if !known || !state.bindingReady || context.created <= state.provableAfter || state.frames[context.frameID].commit != context.commit {
		return
	}
	if previous, reported := state.loaderTokens[context.loaderID]; reported {
		if previous != token {
			state.ambiguousLoaders[context.loaderID] = true
		}
		return
	}
	if len(state.loaderTokens) >= resourceAssociationLimit {
		state.truncated = true
		return
	}
	state.loaderTokens[context.loaderID] = token
}

// resourceJoinLocked assigns a starting request its join identity: the page
// ordinal of its method and URL, and its URL hash for Resource Timing.
func (b *Browser) resourceJoinLocked(request playwright.Request, frame playwright.Frame) resourceJoin {
	join := resourceJoin{frame: frame}
	if !b.documentMetricsEnabled || frame == nil || request == nil {
		return join
	}
	page := browserFramePage(frame)
	if page == nil {
		return join
	}
	join.page = page
	url := request.URL()
	if b.engine == "webkit" {
		if request.IsNavigationRequest() {
			return join
		}
		join.urlHash = resourceTimingHash(url)
		key := resourceTimingKey{frame: frame, hash: join.urlHash}
		if _, known := b.evidence.timingRequests[key]; known || len(b.evidence.timingRequests) < resourceAssociationLimit {
			b.evidence.timingRequests[key]++
		}
	}
	state := b.evidence.cdpPages[page]
	if state == nil {
		return join
	}
	join.key = resourceKey(request.Method(), url)
	if _, known := state.ordinals[join.key]; !known && len(state.ordinals) >= resourceAssociationLimit {
		return join
	}
	state.ordinals[join.key]++
	join.ordinal = state.ordinals[join.key]
	return join
}

func browserFramePage(frame playwright.Frame) (page playwright.Page) {
	defer func() {
		if recover() != nil {
			page = nil
		}
	}()
	return frame.Page()
}

// associateResourceEvidence adds document, cache and initiator provenance to a
// completed request's evidence when the join proves them.
func (b *Browser) associateResourceEvidence(v *protocol.BrowserResourceEvidence, join resourceJoin, generation string) *protocol.BrowserResourceEvidence {
	if v == nil || join.page == nil {
		return v
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	if s.generation != generation {
		return v
	}
	out := *v
	// Association fields and reasons exist only in version 3 records, so only
	// a record an association pass examined is raised to it.
	if join.urlHash != "" {
		out.Version = protocol.BrowserResourceEvidenceVersion
		b.associateResourceTimingLocked(&out, join)
	} else if state := s.cdpPages[join.page]; state != nil && join.ordinal > 0 {
		out.Version = protocol.BrowserResourceEvidenceVersion
		b.associateCDPLocked(&out, join, state)
	} else {
		return v
	}
	if cloned := out.Clone(); cloned != nil {
		return cloned
	}
	return v
}

func (b *Browser) associateCDPLocked(v *protocol.BrowserResourceEvidence, join resourceJoin, state *cdpEvidencePage) {
	s := &b.evidence
	records := state.records[join.key]
	// Until the join proves which CDP record this request is, its protocol is
	// unobserved.
	v.Protocol, v.ProtocolUnavailableReason = "", "no_observation"
	if join.ordinal > len(records) {
		v.IdentityUnavailableReason = "cdp_unobserved"
		if state.truncated {
			v.IdentityUnavailableReason = "identity_capacity"
		}
		return
	}
	record := records[join.ordinal-1]
	mapped, pwKnown := s.pwFrameCDP[join.frame]
	reverse, cdpKnown := s.cdpFramePW[record.frameID]
	if v.EpochStartMS == nil {
		// A request that failed before any response has no Playwright start.
		v.IdentityUnavailableReason = "request_start_unavailable"
		return
	}
	start := record.startMS
	if start == 0 {
		start = record.wallMS
	}
	if math.Abs(*v.EpochStartMS-start) > cdpJoinToleranceMS || pwKnown && mapped != record.frameID || cdpKnown && reverse != join.frame {
		v.IdentityUnavailableReason = "ambiguous_cdp_join"
		return
	}
	if !pwKnown && len(s.pwFrameCDP) < browserFrameIdentityLimit {
		s.pwFrameCDP[join.frame], s.cdpFramePW[record.frameID] = record.frameID, join.frame
	}
	v.Protocol, v.ProtocolUnavailableReason = browserResourceProtocol(record.protocol, record.responded, record.cacheKnown && record.cached, v.ServiceWorker)
	if record.cacheKnown {
		cached := record.cached
		v.Cache, v.CacheUnavailableReason = &cached, ""
	}
	if record.initiatorType != "" {
		v.InitiatorType, v.InitiatorOrigin, v.InitiatorUnavailableReason = record.initiatorType, record.initiatorOrigin, ""
	}
	document, reason := b.loaderDocumentLocked(state, record, join.frame)
	if reason != "" {
		v.IdentityUnavailableReason = reason
		return
	}
	v.DocumentID, v.DocumentAssociation, v.IdentityUnavailableReason = document.id, "cdp_loader_join", ""
	v.DocumentOffsetMS = nil
	if offset := *v.EpochStartMS - document.timeOrigin; document.timeOrigin > 0 && math.Abs(offset) <= 86400000 {
		v.DocumentOffsetMS = &offset
	}
}

// loaderDocumentLocked proves which document a loader produced: the one
// document of the frame whose token the loader's own context reported.
func (b *Browser) loaderDocumentLocked(state *cdpEvidencePage, record *cdpRequestRecord, frame playwright.Frame) (loaderDocument, string) {
	s := &b.evidence
	if document, ok := s.loaderDocuments[record.loaderID]; ok {
		return document, ""
	}
	if state.ambiguousLoaders[record.loaderID] {
		return loaderDocument{}, "ambiguous_document_commit"
	}
	token, reported := state.loaderTokens[record.loaderID]
	if !reported {
		return loaderDocument{}, "document_not_observed"
	}
	var found loaderDocument
	matches := 0
	for _, d := range s.documents {
		if d.frame != frame || d.token != token || d.restored {
			continue
		}
		if matches == 0 || found.id != d.documentID {
			matches++
			found = loaderDocument{id: d.documentID, timeOrigin: d.timeOrigin}
		}
	}
	switch {
	case matches == 0:
		// The begin reaches the controller on another channel and may
		// still arrive, so this result is not cached.
		return loaderDocument{}, "document_not_observed"
	case matches > 1:
		return loaderDocument{}, "ambiguous_document_commit"
	}
	if len(s.loaderDocuments) < resourceAssociationLimit {
		s.loaderDocuments[record.loaderID] = found
	}
	return found, ""
}

func (b *Browser) associateResourceTimingLocked(v *protocol.BrowserResourceEvidence, join resourceJoin) {
	v.Protocol, v.ProtocolUnavailableReason = "", "no_observation"
	if b.evidence.timingRequests[resourceTimingKey{frame: join.frame, hash: join.urlHash}] != 1 {
		v.IdentityUnavailableReason = "ambiguous_resource_timing"
		return
	}
	var found *browserDocumentState
	matches := 0
	for _, d := range b.evidence.documents {
		if d.frame != join.frame || d.resources[join.urlHash].count != 1 {
			continue
		}
		if matches == 0 || found.documentID != d.documentID {
			matches++
			found = d
		}
	}
	switch {
	case matches == 0:
		v.IdentityUnavailableReason = "resource_timing_unobserved"
	case matches > 1:
		v.IdentityUnavailableReason = "ambiguous_resource_timing"
	default:
		entry := found.resources[join.urlHash]
		start := entry.start
		v.DocumentID, v.DocumentAssociation, v.IdentityUnavailableReason = found.documentID, "page_resource_timing_join", ""
		v.DocumentOffsetMS = &start
		// Resource Timing exposes nextHopProtocol only for same-origin or
		// Timing-Allow-Origin responses, and a zero transfer size is a
		// cache hit, which never claims a negotiation.
		v.Protocol, v.ProtocolUnavailableReason = "", "not_exposed"
		if entry.protocolKnown {
			v.Protocol, v.ProtocolUnavailableReason = browserResourceProtocol(entry.protocol, true, entry.cached, v.ServiceWorker)
		}
	}
}

// resourceTimingHash is the cyrb53 hash the document bootstrap computes over
// a Resource Timing entry name, over the URL's UTF-16 code units.
func resourceTimingHash(value string) string {
	h1, h2 := uint32(0xdeadbeef), uint32(0x41c6ce57)
	for _, unit := range utf16.Encode([]rune(value)) {
		h1 = (h1 ^ uint32(unit)) * 2654435761
		h2 = (h2 ^ uint32(unit)) * 1597334677
	}
	h1 = (h1^(h1>>16))*2246822507 ^ (h2^(h2>>13))*3266489909
	h2 = (h2^(h2>>16))*2246822507 ^ (h1^(h1>>13))*3266489909
	return strconv.FormatUint(uint64(h2&0x1fffff)<<32|uint64(h1), 16)
}

// validResourceTimingHash admits the bootstrap's hash text.
func validResourceTimingHash(v string) bool {
	return len(v) > 0 && len(v) <= 14 && strings.Trim(v, "0123456789abcdef") == ""
}

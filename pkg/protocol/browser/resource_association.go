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
// memory-cache hits and frame commits. A Playwright request joins the CDP
// request of the same page, method and URL at the same ordinal, checked
// against its start time and the frame mapping learned so far. The join gives
// the issuing document's loader, cache and initiator provenance. A loader
// becomes a document when exactly one document of that frame began between
// the loader's commit and the frame's next commit and its time origin lies
// after the previous loader's navigation request and no later than this
// loader's own. Begins are stamped on binding goroutines, so the clock check
// keeps a late-stamped earlier document out of a newer loader's window, and a
// result from a window still open to later begins is never cached.
//
// WebKit: a request joins the one document of its frame holding exactly one
// Resource Timing entry for its URL (page_resource_timing_join). Navigation
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
	// documentClockToleranceMS absorbs rounding between the renderer's time
	// origin and the network wall time; both come from the system clock.
	documentClockToleranceMS = 1.0
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
}

type cdpCommit struct {
	loaderID string
	seq      uint64
}

type cdpEvidencePage struct {
	records  map[resourceJoinKey][]*cdpRequestRecord
	latest   map[string]*cdpRequestRecord
	served   map[string]bool
	commits  map[string][]cdpCommit
	ordinals map[resourceJoinKey]int
	// navigationWallMS is each loader's navigation request wall time (its
	// first hop), which bounds the time origin of the document it commits.
	navigationWallMS map[string]float64
	count            int
	truncated        bool
}

type loaderDocument struct {
	id         string
	timeOrigin float64
}

type resourceTimingEntry struct {
	count int
	start float64
}

// resourceTimingKey counts a frame's requests for one URL hash: a URL
// requested twice can never be told apart by its Resource Timing entries.
type resourceTimingKey struct {
	frame playwright.Frame
	hash  string
}

func newCDPEvidencePage() *cdpEvidencePage {
	return &cdpEvidencePage{records: map[resourceJoinKey][]*cdpRequestRecord{}, latest: map[string]*cdpRequestRecord{}, served: map[string]bool{}, commits: map[string][]cdpCommit{}, ordinals: map[resourceJoinKey]int{}, navigationWallMS: map[string]float64{}}
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
	b.mu.Unlock()
	var cdp playwright.CDPSession
	var err error
	if b.createCDPSession != nil {
		cdp, err = b.createCDPSession(browserContext, page)
	} else {
		cdp, err = browserContext.NewCDPSession(page)
	}
	if err != nil || cdp == nil {
		return
	}
	state := newCDPEvidencePage()
	cdp.On("Network.requestWillBeSent", func(params map[string]any) { b.observeCDPRequest(state, params, generation) })
	cdp.On("Network.responseReceived", func(params map[string]any) { b.observeCDPResponse(state, params, generation) })
	cdp.On("Network.requestServedFromCache", func(params map[string]any) { b.observeCDPCacheHit(state, params, generation) })
	cdp.On("Page.frameNavigated", func(params map[string]any) { b.observeCDPCommit(state, params, generation) })
	b.mu.Lock()
	if b.closing || b.evidence.generation != generation {
		b.mu.Unlock()
		_ = cdp.Detach()
		return
	}
	b.evidence.cdpPages[page] = state
	b.mu.Unlock()
	// This session only reads request metadata: zero buffers keep a second
	// network agent from retaining bodies in the renderer.
	if _, err := cdp.Send("Page.enable", map[string]any{}); err == nil {
		_, err = cdp.Send("Network.enable", map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0})
	}
	if err != nil {
		b.mu.Lock()
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
	}
	// A document's navigation request carries its loader's ID.
	if requestID == record.loaderID && wall > 0 {
		if _, known := state.navigationWallMS[requestID]; !known && len(state.navigationWallMS) < resourceAssociationLimit {
			state.navigationWallMS[requestID] = record.wallMS
		}
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
	}
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
	s := &b.evidence
	if s.generation != generation {
		return
	}
	commits, known := state.commits[frameID]
	if !known && len(state.commits) >= cdpCommitFrameLimit {
		state.truncated = true
		return
	}
	if len(commits) >= 64 {
		// Keep the most recent commits of a frame that navigates repeatedly.
		commits = append(commits[:0], commits[len(commits)-63:]...)
	}
	s.associationSeq++
	state.commits[frameID] = append(commits, cdpCommit{loaderID: loaderID, seq: s.associationSeq})
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
	// Association fields and reasons exist only in version 3 records.
	out.Version = protocol.BrowserResourceEvidenceVersion
	if join.urlHash != "" {
		b.associateResourceTimingLocked(&out, join)
	} else if state := s.cdpPages[join.page]; state != nil && join.ordinal > 0 {
		b.associateCDPLocked(&out, join, state)
	}
	if cloned := out.Clone(); cloned != nil {
		return cloned
	}
	return v
}

func (b *Browser) associateCDPLocked(v *protocol.BrowserResourceEvidence, join resourceJoin, state *cdpEvidencePage) {
	s := &b.evidence
	records := state.records[join.key]
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

// loaderDocumentLocked proves which document a loader produced: exactly one
// document of the frame began between that commit and the frame's next one.
func (b *Browser) loaderDocumentLocked(state *cdpEvidencePage, record *cdpRequestRecord, frame playwright.Frame) (loaderDocument, string) {
	s := &b.evidence
	if document, ok := s.loaderDocuments[record.loaderID]; ok {
		return document, ""
	}
	commits := state.commits[record.frameID]
	index := -1
	for i, commit := range commits {
		if commit.loaderID == record.loaderID {
			index = i
		}
	}
	if index < 0 {
		return loaderDocument{}, "document_not_observed"
	}
	low, high := commits[index].seq, uint64(math.MaxUint64)
	open := index+1 == len(commits)
	if !open {
		high = commits[index+1].seq
	}
	// A loader's document began after the previous loader's navigation
	// request (that loader had committed before this navigation started) and
	// no later than this loader's own navigation request.
	lowerWall, hasLower := 0.0, false
	if index > 0 {
		lowerWall, hasLower = state.navigationWallMS[commits[index-1].loaderID]
	}
	upperWall, hasUpper := state.navigationWallMS[record.loaderID]
	var found loaderDocument
	matches := 0
	for _, d := range s.documents {
		if d.frame != frame || d.beginSeq <= low || d.beginSeq >= high || d.restored {
			continue
		}
		if hasLower && d.timeOrigin <= lowerWall-documentClockToleranceMS || hasUpper && d.timeOrigin > upperWall+documentClockToleranceMS {
			continue
		}
		if matches == 0 || found.id != d.documentID {
			matches++
			found = loaderDocument{id: d.documentID, timeOrigin: d.timeOrigin}
		}
	}
	switch {
	case matches == 0:
		return loaderDocument{}, "document_not_observed"
	case matches > 1:
		return loaderDocument{}, "ambiguous_document_commit"
	}
	// A window still open to later begins may yet gain the true document.
	if !open && len(s.loaderDocuments) < resourceAssociationLimit {
		s.loaderDocuments[record.loaderID] = found
	}
	return found, ""
}

func (b *Browser) associateResourceTimingLocked(v *protocol.BrowserResourceEvidence, join resourceJoin) {
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
		start := found.resources[join.urlHash].start
		v.DocumentID, v.DocumentAssociation, v.IdentityUnavailableReason = found.documentID, "page_resource_timing_join", ""
		v.DocumentOffsetMS = &start
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

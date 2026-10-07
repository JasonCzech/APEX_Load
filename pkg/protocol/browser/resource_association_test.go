package browser

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestResourceTimingHashMatchesTheBootstrap(t *testing.T) {
	// Vectors computed by the bootstrap's JavaScript over UTF-16 code units.
	for input, want := range map[string]string{
		"":                            "bdcb81aee8d83",
		"http://127.0.0.1:8080/a.css": "7efba6041caca",
		"https://example.com/ü?q=1#x": "9351cd03d2d17",
		"https://例え.jp/😀":             "1f775beda0f6ce",
	} {
		if got := resourceTimingHash(input); got != want || !validResourceTimingHash(got) {
			t.Errorf("hash(%q) = %s, want %s", input, got, want)
		}
	}
}

type associationFrame struct{ playwright.Frame }

func associationEvidence(t *testing.T, b *Browser, epoch float64) *protocol.BrowserResourceEvidence {
	t.Helper()
	s := &b.evidence
	start := epoch
	return &protocol.BrowserResourceEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: s.generation, RequestID: s.nextID("r"), RedirectChainID: s.generation + "-r1", EpochStartMS: &start, ObservedAt: time.Now().UTC(), TimingSource: "playwright", State: "finished", IdentityUnavailableReason: "document_unavailable", CacheUnavailableReason: "not_exposed", InitiatorUnavailableReason: "not_exposed"}
}

func associationBrowser(t *testing.T) (*Browser, *cdpEvidencePage, playwright.Page, playwright.Frame, playwright.Frame) {
	t.Helper()
	b := &Browser{}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	page := &browserFinalityCurrentPage{}
	main, child := &associationFrame{}, &associationFrame{}
	state := newCDPEvidencePage()
	b.evidence.cdpPages[page] = state
	bindingInstalled(b, state)
	return b, state, page, main, child
}

func cdpRequest(b *Browser, state *cdpEvidencePage, id, loader, frame, method, url string, wallMS float64, initiator map[string]any) {
	params := map[string]any{"requestId": id, "loaderId": loader, "frameId": frame, "wallTime": wallMS / 1000, "request": map[string]any{"url": url, "method": method}}
	if initiator != nil {
		params["initiator"] = initiator
	}
	b.observeCDPRequest(state, params, b.evidence.generation)
}

func beginDocument(b *Browser, frame playwright.Frame, page playwright.Page, timeOrigin float64, token string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	id := s.nextID("d")
	s.documents[id] = &browserDocumentState{frame: frame, page: page, token: token, documentID: id, timeOrigin: timeOrigin, observations: map[string]protocol.BrowserDocumentObservation{}}
	return id
}

func commitFrame(b *Browser, state *cdpEvidencePage, frame, loader string) {
	b.observeCDPCommit(state, map[string]any{"frame": map[string]any{"id": frame, "loaderId": loader}}, b.evidence.generation)
}

// bindingInstalled marks the session's binding ready, as the addBinding
// reply does; contexts created before it prove nothing.
func bindingInstalled(b *Browser, state *cdpEvidencePage) {
	b.mu.Lock()
	state.provableAfter, state.bindingReady = state.contextsSeen, true
	b.mu.Unlock()
}

func createContext(b *Browser, state *cdpEvidencePage, id int, frame string) {
	b.observeCDPContextCreated(state, map[string]any{"context": map[string]any{"id": float64(id), "auxData": map[string]any{"frameId": frame, "isDefault": true, "type": "default"}}}, b.evidence.generation)
}

func documentToken(n int) string { return fmt.Sprintf("%032x", n) }

func reportDocument(b *Browser, state *cdpEvidencePage, context int, token string) {
	b.observeCDPDocumentReport(state, map[string]any{"name": b.evidence.loaderBinding, "payload": token, "executionContextId": float64(context)}, b.evidence.loaderBinding, b.evidence.generation)
}

// loadDocument replays one document load in the order the evidence session
// delivers it: commit, the new default context, then the document's report.
func loadDocument(b *Browser, state *cdpEvidencePage, frameID, loader string, context int, frame playwright.Frame, page playwright.Page, timeOrigin float64) string {
	commitFrame(b, state, frameID, loader)
	createContext(b, state, context, frameID)
	reportDocument(b, state, context, documentToken(context))
	return beginDocument(b, frame, page, timeOrigin, documentToken(context))
}

func TestCDPJoinAssociatesDocumentsCacheAndInitiators(t *testing.T) {
	b, state, page, main, child := associationBrowser(t)
	origin := 1_800_000_000_000.0
	cdpRequest(b, state, "L1", "L1", "F", "GET", "http://a.test/", origin+1, map[string]any{"type": "other"})
	mainDoc := loadDocument(b, state, "F", "L1", 1, main, page, origin)
	cdpRequest(b, state, "R2", "L1", "F", "GET", "http://a.test/app.css", origin+10, map[string]any{"type": "parser", "url": "http://a.test/index?token=secret", "stack": "never"})
	b.observeCDPResponse(state, map[string]any{"requestId": "R2", "response": map[string]any{"fromDiskCache": true}}, b.evidence.generation)
	cdpRequest(b, state, "L3", "L3", "C", "GET", "http://b.test/frame", origin+20, map[string]any{"type": "parser", "url": "http://a.test/"})
	childDoc := loadDocument(b, state, "C", "L3", 2, child, page, origin+21)
	cdpRequest(b, state, "R4", "L3", "C", "GET", "http://b.test/data", origin+30, map[string]any{"type": "script"})
	b.observeCDPResponse(state, map[string]any{"requestId": "R4", "response": map[string]any{}}, b.evidence.generation)

	join := func(frame playwright.Frame, method, url string, ordinal int) resourceJoin {
		return resourceJoin{page: page, frame: frame, key: resourceKey(method, url), ordinal: ordinal}
	}
	gen := b.evidence.generation
	css := b.associateResourceEvidence(associationEvidence(t, b, origin+10.4), join(main, "GET", "http://a.test/app.css", 1), gen)
	if css.Version != protocol.BrowserResourceEvidenceVersion {
		t.Fatalf("an associated record kept version %d", css.Version)
	}
	if css.DocumentID != mainDoc || css.DocumentAssociation != "cdp_loader_join" || css.DocumentOffsetMS == nil || math.Abs(*css.DocumentOffsetMS-10.4) > 1e-3 || css.Cache == nil || !*css.Cache || css.InitiatorType != "parser" || css.InitiatorOrigin != "http://a.test" || css.IdentityUnavailableReason != "" {
		t.Fatalf("stylesheet %+v", css)
	}
	data := b.associateResourceEvidence(associationEvidence(t, b, origin+30.2), join(child, "GET", "http://b.test/data", 1), gen)
	if data.DocumentID != childDoc || data.Cache == nil || *data.Cache || data.InitiatorType != "script" || data.InitiatorOrigin != "" {
		t.Fatalf("iframe fetch %+v", data)
	}
	// The navigation request belongs to the document it loaded.
	navigation := b.associateResourceEvidence(associationEvidence(t, b, origin+1.3), join(main, "GET", "http://a.test/", 1), gen)
	if navigation.DocumentID != mainDoc {
		t.Fatalf("navigation %+v", navigation)
	}

	// A start time far from the CDP record means the ordinals are misaligned.
	misaligned := b.associateResourceEvidence(associationEvidence(t, b, origin+40), join(main, "GET", "http://a.test/app.css", 1), gen)
	if misaligned.DocumentID != "" || misaligned.IdentityUnavailableReason != "ambiguous_cdp_join" {
		t.Fatalf("misaligned join %+v", misaligned)
	}
	// A request the CDP session never saw stays unobserved.
	unseen := b.associateResourceEvidence(associationEvidence(t, b, origin+50), join(main, "GET", "http://a.test/late.js", 1), gen)
	if unseen.IdentityUnavailableReason != "cdp_unobserved" {
		t.Fatalf("unobserved %+v", unseen)
	}
	// A CDP frame already mapped to another Playwright frame refuses the join.
	cdpRequest(b, state, "R5", "L1", "F", "GET", "http://a.test/other.js", origin+60, nil)
	swapped := b.associateResourceEvidence(associationEvidence(t, b, origin+60), join(child, "GET", "http://a.test/other.js", 1), gen)
	if swapped.IdentityUnavailableReason != "ambiguous_cdp_join" || swapped.DocumentID != "" {
		t.Fatalf("frame conflict %+v", swapped)
	}
}

func TestLoaderBindingRefusesUnprovenDocuments(t *testing.T) {
	b, state, page, main, _ := associationBrowser(t)
	origin := 1_800_000_000_000.0
	gen := b.evidence.generation
	request := func(id, loader, url string) resourceJoin {
		cdpRequest(b, state, id, loader, "F", "GET", url, origin, nil)
		return resourceJoin{page: page, frame: main, key: resourceKey("GET", url), ordinal: 1}
	}
	reason := func(join resourceJoin) (string, string) {
		v := b.associateResourceEvidence(associationEvidence(t, b, origin), join, gen)
		return v.DocumentID, v.IdentityUnavailableReason
	}
	// A loader committed, but no document reported from one of its contexts.
	commitFrame(b, state, "F", "L1")
	createContext(b, state, 1, "F")
	if id, why := reason(request("R1", "L1", "http://a.test/one.js")); id != "" || why != "document_not_observed" {
		t.Fatalf("no report: %q %s", id, why)
	}
	// The report arrived but the document's begin has not: not observed yet,
	// and not cached, so the begin's later arrival is still found.
	reportDocument(b, state, 1, documentToken(1))
	if id, why := reason(request("R2", "L1", "http://a.test/two.js")); id != "" || why != "document_not_observed" {
		t.Fatalf("report before begin: %q %s", id, why)
	}
	first := beginDocument(b, main, page, origin, documentToken(1))
	if id, why := reason(request("R3", "L1", "http://a.test/three.js")); id != first || why != "" {
		t.Fatalf("after the begin: %q %s", id, why)
	}
	// A late report from the replaced document's context, after the next
	// commit, proves nothing about the new loader.
	commitFrame(b, state, "F", "L2")
	reportDocument(b, state, 1, documentToken(99))
	beginDocument(b, main, page, origin, documentToken(99))
	if id, why := reason(request("R4", "L2", "http://a.test/four.js")); id != "" || why != "document_not_observed" {
		t.Fatalf("stale context report: %q %s", id, why)
	}
	// A context created before its frame's latest commit is stale too.
	createContext(b, state, 2, "F")
	commitFrame(b, state, "F", "L3")
	reportDocument(b, state, 2, documentToken(2))
	if id, why := reason(request("R5", "L3", "http://a.test/five.js")); id != "" || why != "document_not_observed" {
		t.Fatalf("context from before the commit: %q %s", id, why)
	}
	// One loader reporting two documents is ambiguous.
	createContext(b, state, 3, "F")
	reportDocument(b, state, 3, documentToken(3))
	createContext(b, state, 4, "F")
	reportDocument(b, state, 4, documentToken(4))
	beginDocument(b, main, page, origin, documentToken(3))
	if id, why := reason(request("R6", "L3", "http://a.test/six.js")); id != "" || why != "ambiguous_document_commit" {
		t.Fatalf("two reports: %q %s", id, why)
	}
	// A destroyed context and a malformed or foreign report record nothing.
	commitFrame(b, state, "F", "L4")
	createContext(b, state, 5, "F")
	b.observeCDPContextDestroyed(state, map[string]any{"executionContextId": float64(5)}, gen)
	reportDocument(b, state, 5, documentToken(5))
	createContext(b, state, 6, "F")
	reportDocument(b, state, 6, "not-a-token")
	b.observeCDPDocumentReport(state, map[string]any{"name": "pageBinding", "payload": documentToken(6), "executionContextId": float64(6)}, b.evidence.loaderBinding, gen)
	if _, reported := state.loaderTokens["L4"]; reported {
		t.Fatalf("an unproven report bound loader L4 to %s", state.loaderTokens["L4"])
	}
	// A loader that never committed (an aborted navigation) proves nothing.
	if id, why := reason(request("L9", "L9", "http://a.test/aborted")); id != "" || why != "document_not_observed" {
		t.Fatalf("uncommitted loader: %q %s", id, why)
	}
}

func TestResourceTimingJoinNeedsOneUniqueEntry(t *testing.T) {
	b := &Browser{engine: "webkit"}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	page, frame, other := &browserFinalityCurrentPage{}, &associationFrame{}, &associationFrame{}
	unique, repeated := resourceTimingHash("http://a.test/a.css"), resourceTimingHash("http://a.test/poll")
	first := beginDocument(b, frame, page, 1, "")
	b.evidence.documents[first].resources = map[string]resourceTimingEntry{unique: {count: 1, start: 12.5}, repeated: {count: 2, start: 30}}
	second := beginDocument(b, other, page, 2, "")
	b.evidence.documents[second].resources = map[string]resourceTimingEntry{unique: {count: 1, start: 3}}
	gen := b.evidence.generation
	b.evidence.timingRequests[resourceTimingKey{frame: frame, hash: unique}] = 1
	b.evidence.timingRequests[resourceTimingKey{frame: frame, hash: repeated}] = 1
	v := b.associateResourceEvidence(associationEvidence(t, b, 100), resourceJoin{page: page, frame: frame, urlHash: unique}, gen)
	if v.DocumentID != first || v.DocumentAssociation != "page_resource_timing_join" || v.DocumentOffsetMS == nil || *v.DocumentOffsetMS != 12.5 || v.Cache != nil || v.InitiatorType != "" {
		t.Fatalf("unique entry %+v", v)
	}
	if v := b.associateResourceEvidence(associationEvidence(t, b, 100), resourceJoin{page: page, frame: frame, urlHash: repeated}, gen); v.DocumentID != "" || v.IdentityUnavailableReason != "resource_timing_unobserved" {
		t.Fatalf("repeated URL %+v", v)
	}
	// A URL the frame requested twice is ambiguous even with one entry so far.
	b.evidence.timingRequests[resourceTimingKey{frame: frame, hash: unique}] = 2
	if v := b.associateResourceEvidence(associationEvidence(t, b, 100), resourceJoin{page: page, frame: frame, urlHash: unique}, gen); v.IdentityUnavailableReason != "ambiguous_resource_timing" {
		t.Fatalf("twice-requested URL %+v", v)
	}
	b.evidence.timingRequests[resourceTimingKey{frame: frame, hash: unique}] = 1
	third := beginDocument(b, frame, page, 3, "")
	b.evidence.documents[third].resources = map[string]resourceTimingEntry{unique: {count: 1, start: 7}}
	if v := b.associateResourceEvidence(associationEvidence(t, b, 100), resourceJoin{page: page, frame: frame, urlHash: unique}, gen); v.IdentityUnavailableReason != "ambiguous_resource_timing" {
		t.Fatalf("two documents %+v", v)
	}
}

func TestResourceEvidenceRefusesUnprovenAssociation(t *testing.T) {
	b := &Browser{}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	v := associationEvidence(t, b, 1)
	for name, change := range map[string]func(*protocol.BrowserResourceEvidence){
		"association without document": func(e *protocol.BrowserResourceEvidence) { e.DocumentAssociation = "cdp_loader_join" },
		"unknown association": func(e *protocol.BrowserResourceEvidence) {
			e.DocumentID, e.DocumentAssociation = e.SourceGeneration+"-d1", "frame_guess"
		},
		"unknown initiator":   func(e *protocol.BrowserResourceEvidence) { e.InitiatorType = "stack" },
		"origin without type": func(e *protocol.BrowserResourceEvidence) { e.InitiatorOrigin = "http://a.test" },
		"initiator path": func(e *protocol.BrowserResourceEvidence) {
			e.InitiatorType, e.InitiatorOrigin = "parser", "http://a.test/index"
		},
	} {
		bad := *v
		change(&bad)
		if bad.Clone() != nil {
			t.Errorf("%s admitted", name)
		}
	}
}

// The Go join hash must equal the hash the embedded bootstrap computes in the
// page, for the same URL, including non-ASCII and surrogate-pair URLs.
func TestResourceTimingHashMatchesTheEmbeddedBootstrap(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := browserDocumentBootstrap("__binding", "__finalizer", "__loader", true, true)
	start := strings.Index(script, "const resourceHash=")
	if start < 0 {
		t.Fatal("bootstrap no longer defines resourceHash")
	}
	end := strings.Index(script[start:], "};")
	if end < 0 {
		t.Fatal("bootstrap resourceHash has no end")
	}
	inputs := []string{"https://example.test/app.css", "https://example.test/ä/ñ?q=ü", "https://example.test/😀/x", "", strings.Repeat("a/", 300)}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	program := script[start:start+end+2] + "\nconsole.log(JSON.stringify(" + string(encoded) + ".map(resourceHash)))"
	output, err := exec.Command(node, "-e", program).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, output)
	}
	var hashes []string
	if err := json.Unmarshal(output, &hashes); err != nil {
		t.Fatalf("node output %s: %v", output, err)
	}
	for i, input := range inputs {
		if got := resourceTimingHash(input); got != hashes[i] {
			t.Fatalf("hash of %q: Go %s, bootstrap %s", input, got, hashes[i])
		}
	}
}

// recordingCDPSession records the evidence session's subscriptions and
// commands in order.
type recordingCDPSession struct {
	playwright.CDPSession
	mu    sync.Mutex
	calls []string
	sends []map[string]any
}

func (s *recordingCDPSession) On(event string, _ interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "on "+event)
}

func (s *recordingCDPSession) Send(method string, params map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "send "+method)
	s.sends = append(s.sends, params)
	return nil, nil
}

func (s *recordingCDPSession) Detach() error { return nil }

// The evidence session subscribes before it enables anything, keeps no
// network buffers, and installs its own document binding.
func TestResourceAssociationSessionSubscribesFirstAndKeepsNoBuffers(t *testing.T) {
	session := &recordingCDPSession{}
	b := &Browser{engine: "chromium", documentMetricsEnabled: true, createCDPSession: func(playwright.BrowserContext, playwright.Page) (playwright.CDPSession, error) { return session, nil }}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	page := &browserFinalityCurrentPage{}
	b.installResourceAssociation(&cdpTestContext{}, page, b.evidence.generation)
	if b.evidence.cdpPages[page] == nil {
		t.Fatal("the evidence session was not kept")
	}
	want := []string{"on Network.requestWillBeSent", "on Network.responseReceived", "on Network.requestServedFromCache", "on Page.frameNavigated", "on Runtime.executionContextCreated", "on Runtime.executionContextDestroyed", "on Runtime.executionContextsCleared", "on Runtime.bindingCalled", "send Page.enable", "send Network.enable", "send Runtime.enable", "send Runtime.addBinding"}
	if !slices.Equal(session.calls, want) {
		t.Fatalf("session calls %v, want %v", session.calls, want)
	}
	if network := session.sends[1]; network["maxTotalBufferSize"] != 0 || network["maxResourceBufferSize"] != 0 || network["maxPostDataSize"] != 0 {
		t.Fatalf("Network.enable kept buffers: %v", network)
	}
	if binding := session.sends[3]; binding["name"] != b.evidence.loaderBinding || !strings.HasPrefix(b.evidence.loaderBinding, "__apexLoader_") {
		t.Fatalf("binding %v", binding)
	}
}

// A closed page drops its evidence session state.
func TestRemovedPageDropsItsEvidenceSession(t *testing.T) {
	b, _, page, _, _ := associationBrowser(t)
	b.removePage(page)
	if _, kept := b.evidence.cdpPages[page]; kept {
		t.Fatal("a closed page kept its evidence session state")
	}
}

// A page keeps commits for a bounded number of frames.
func TestEvidenceSessionBoundsItsFrames(t *testing.T) {
	b, state, _, _, _ := associationBrowser(t)
	for i := range cdpCommitFrameLimit + 1 {
		commitFrame(b, state, fmt.Sprintf("F%d", i), "L")
	}
	if len(state.frames) != cdpCommitFrameLimit || !state.truncated {
		t.Fatalf("frames %d truncated %v", len(state.frames), state.truncated)
	}
}

// The CDP start reproduces Playwright's: queueing time counts, and a memory
// cache hit or a response without timing starts at the wall time.
func TestCDPStartMatchesPlaywright(t *testing.T) {
	record := &cdpRequestRecord{wallMS: 1_000_000, timestampS: 50}
	queued := map[string]any{"timing": map[string]any{"requestTime": 50.25}}
	if got := cdpStartMS(record, queued, false); math.Abs(got-1_000_250) > 1e-6 {
		t.Fatalf("queued start %f", got)
	}
	if got := cdpStartMS(record, queued, true); got != record.wallMS {
		t.Fatalf("memory cache start %f", got)
	}
	if got := cdpStartMS(record, map[string]any{}, false); got != record.wallMS {
		t.Fatalf("no timing start %f", got)
	}
	// A request that failed before its response has no Playwright start.
	b, state, page, main, _ := associationBrowser(t)
	cdpRequest(b, state, "R1", "L1", "F", "GET", "http://a.test/x", 1_000_000, nil)
	evidence := associationEvidence(t, b, 1_000_000)
	evidence.EpochStartMS = nil
	v := b.associateResourceEvidence(evidence, resourceJoin{page: page, frame: main, key: resourceKey("GET", "http://a.test/x"), ordinal: 1}, b.evidence.generation)
	if v.IdentityUnavailableReason != "request_start_unavailable" || v.DocumentID != "" {
		t.Fatalf("failed request %+v", v)
	}
}

// orderingPage records which page handlers were registered, in order.
type orderingPage struct {
	playwright.Page
	log *[]string
}

func (p *orderingPage) OnClose(func(playwright.Page))          { *p.log = append(*p.log, "on close") }
func (p *orderingPage) OnCrash(func(playwright.Page))          { *p.log = append(*p.log, "on crash") }
func (p *orderingPage) OnWebSocket(func(playwright.WebSocket)) {}

// Close and crash handlers are registered before the evidence session is
// installed: installing makes blocking calls, and a popup can close meanwhile.
func TestPageCloseHandlersPrecedeTheEvidenceSession(t *testing.T) {
	var log []string
	b := &Browser{engine: "chromium", documentMetricsEnabled: true, pwContext: &cdpTestContext{}, createCDPSession: func(playwright.BrowserContext, playwright.Page) (playwright.CDPSession, error) {
		log = append(log, "install")
		return &recordingCDPSession{}, nil
	}}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	b.registerPage(&orderingPage{log: &log}, false)
	if !slices.Equal(log, []string{"on close", "on crash", "install"}) {
		t.Fatalf("registration order %v", log)
	}
}

type joinTestFrame struct {
	playwright.Frame
	page playwright.Page
}

func (f *joinTestFrame) Page() playwright.Page { return f.page }

type joinTestRequest struct {
	playwright.Request
	url        string
	navigation bool
}

func (r *joinTestRequest) URL() string               { return r.url }
func (r *joinTestRequest) Method() string            { return "GET" }
func (r *joinTestRequest) IsNavigationRequest() bool { return r.navigation }

// On WebKit a navigation request never takes a Resource Timing join: the
// document it loads has no entry for it, so a main-frame navigation to a URL
// the frame already loaded as an iframe cannot borrow the iframe's entry.
func TestWebKitNavigationRequestsNeverTakeAResourceTimingJoin(t *testing.T) {
	b := &Browser{engine: "webkit", documentMetricsEnabled: true}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	frame := &joinTestFrame{page: &browserFinalityCurrentPage{}}
	b.mu.Lock()
	navigation := b.resourceJoinLocked(&joinTestRequest{url: "http://a.test/frame", navigation: true}, frame)
	subresource := b.resourceJoinLocked(&joinTestRequest{url: "http://a.test/frame"}, frame)
	b.mu.Unlock()
	if navigation.urlHash != "" {
		t.Fatal("a WebKit navigation request took a Resource Timing join")
	}
	if subresource.urlHash == "" || b.evidence.timingRequests[resourceTimingKey{frame: frame, hash: subresource.urlHash}] != 1 {
		t.Fatalf("a subresource was not counted for its join: %+v", subresource)
	}
}

// A context that existed before the session installed its binding never ran
// the bootstrap's report, so a report from it (a page script calling the
// still-global binding) proves nothing; neither does one from another frame
// or after the contexts were cleared.
func TestLoaderReportsNeedAContextCreatedAfterTheBinding(t *testing.T) {
	b := &Browser{}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	state := newCDPEvidencePage()
	commitFrame(b, state, "F", "L1")
	createContext(b, state, 1, "F")
	reportDocument(b, state, 1, documentToken(1))
	if _, reported := state.loaderTokens["L1"]; reported {
		t.Fatal("a report before the binding was installed proved a document")
	}
	bindingInstalled(b, state)
	reportDocument(b, state, 1, documentToken(1))
	if _, reported := state.loaderTokens["L1"]; reported {
		t.Fatal("a context from before the binding proved a document")
	}
	commitFrame(b, state, "F", "L2")
	createContext(b, state, 2, "F")
	b.observeCDPContextsCleared(state, b.evidence.generation)
	reportDocument(b, state, 2, documentToken(2))
	if _, reported := state.loaderTokens["L2"]; reported {
		t.Fatal("a cleared context proved a document")
	}
	createContext(b, state, 3, "F")
	reportDocument(b, state, 3, documentToken(3))
	if state.loaderTokens["L2"] != documentToken(3) {
		t.Fatalf("a context created after the binding was not admitted: %v", state.loaderTokens)
	}
}

// A loader's token proves only a document of the loader's own frame: a
// token whose document began in another frame leaves the request unproven.
func TestLoaderTokenFromAnotherFrameProvesNothing(t *testing.T) {
	b, state, page, main, child := associationBrowser(t)
	origin := 1_800_000_000_000.0
	cdpRequest(b, state, "R1", "L1", "F", "GET", "http://a.test/app.js", origin+5, nil)
	commitFrame(b, state, "F", "L1")
	createContext(b, state, 1, "F")
	reportDocument(b, state, 1, documentToken(1))
	beginDocument(b, child, page, origin, documentToken(1))
	join := resourceJoin{page: page, frame: main, key: resourceKey("GET", "http://a.test/app.js"), ordinal: 1}
	got := b.associateResourceEvidence(associationEvidence(t, b, origin+5), join, b.evidence.generation)
	if got.DocumentID != "" || got.IdentityUnavailableReason != "document_not_observed" {
		t.Fatalf("a token from another frame associated a document: %+v", got)
	}
}

// handlerCDPSession keeps the evidence session's handlers and lets a test
// deliver events while a command is in flight, as Playwright does.
type handlerCDPSession struct {
	playwright.CDPSession
	handlers map[string]func(map[string]any)
	during   map[string]func()
}

func (s *handlerCDPSession) On(event string, handler interface{}) {
	s.handlers[event] = handler.(func(map[string]any))
}

func (s *handlerCDPSession) Send(method string, _ map[string]interface{}) (interface{}, error) {
	if during := s.during[method]; during != nil {
		during()
	}
	return nil, nil
}

func (s *handlerCDPSession) Detach() error { return nil }

func (s *handlerCDPSession) emit(event string, params map[string]any) { s.handlers[event](params) }

// Contexts the session reports before the addBinding reply cannot prove a
// document, however the install interleaves with them; later ones can.
func TestInstallAdmitsOnlyContextsCreatedAfterTheBindingReply(t *testing.T) {
	session := &handlerCDPSession{handlers: map[string]func(map[string]any){}}
	b := &Browser{engine: "chromium", documentMetricsEnabled: true, createCDPSession: func(playwright.BrowserContext, playwright.Page) (playwright.CDPSession, error) { return session, nil }}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	context := func(id int) map[string]any {
		return map[string]any{"context": map[string]any{"id": float64(id), "auxData": map[string]any{"frameId": "F", "isDefault": true, "type": "default"}}}
	}
	report := func(id, token int) {
		session.emit("Runtime.bindingCalled", map[string]any{"name": b.evidence.loaderBinding, "payload": documentToken(token), "executionContextId": float64(id)})
	}
	session.during = map[string]func(){
		// Runtime.enable replays the existing context.
		"Runtime.enable": func() {
			session.emit("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "F", "loaderId": "L1"}})
			session.emit("Runtime.executionContextCreated", context(1))
		},
		// A document committing while the binding is being added.
		"Runtime.addBinding": func() {
			report(1, 1)
			session.emit("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "F", "loaderId": "L2"}})
			session.emit("Runtime.executionContextCreated", context(2))
			report(2, 2)
		},
	}
	page := &browserFinalityCurrentPage{}
	b.installResourceAssociation(&cdpTestContext{}, page, b.evidence.generation)
	state := b.evidence.cdpPages[page]
	if state == nil {
		t.Fatal("the evidence session was not kept")
	}
	report(1, 1)
	report(2, 2)
	if len(state.loaderTokens) != 0 {
		t.Fatalf("a context from before the binding reply proved a document: %v", state.loaderTokens)
	}
	session.emit("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "F", "loaderId": "L3"}})
	session.emit("Runtime.executionContextCreated", context(3))
	report(3, 3)
	if state.loaderTokens["L3"] != documentToken(3) || len(state.loaderTokens) != 1 {
		t.Fatalf("a context created after the binding reply was not admitted: %v", state.loaderTokens)
	}
}

// A record no association pass examined keeps version 2, so older readers
// accept it.
func TestUnexaminedRequestEvidenceKeepsVersionTwo(t *testing.T) {
	b, _, page, main, _ := associationBrowser(t)
	evidence := associationEvidence(t, b, 1_800_000_000_000)
	evidence.Version = protocol.BrowserEvidenceVersion
	for name, join := range map[string]resourceJoin{
		"no page":    {frame: main},
		"no ordinal": {page: page, frame: main},
		"no session": {page: &browserFinalityCurrentPage{}, frame: main, ordinal: 1},
	} {
		if v := b.associateResourceEvidence(evidence, join, b.evidence.generation); v.Version != protocol.BrowserEvidenceVersion {
			t.Fatalf("%s: an unexamined record became version %d", name, v.Version)
		}
	}
}

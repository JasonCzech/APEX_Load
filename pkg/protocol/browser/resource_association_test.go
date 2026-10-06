package browser

import (
	"math"
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
	return b, state, page, main, child
}

func cdpRequest(b *Browser, state *cdpEvidencePage, id, loader, frame, method, url string, wallMS float64, initiator map[string]any) {
	params := map[string]any{"requestId": id, "loaderId": loader, "frameId": frame, "wallTime": wallMS / 1000, "request": map[string]any{"url": url, "method": method}}
	if initiator != nil {
		params["initiator"] = initiator
	}
	b.observeCDPRequest(state, params, b.evidence.generation)
}

func beginDocument(b *Browser, frame playwright.Frame, page playwright.Page, timeOrigin float64) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	s.associationSeq++
	id := s.nextID("d")
	s.documents[id] = &browserDocumentState{frame: frame, page: page, documentID: id, timeOrigin: timeOrigin, beginSeq: s.associationSeq, observations: map[string]protocol.BrowserDocumentObservation{}}
	return id
}

func commitFrame(b *Browser, state *cdpEvidencePage, frame, loader string) {
	b.observeCDPCommit(state, map[string]any{"frame": map[string]any{"id": frame, "loaderId": loader}}, b.evidence.generation)
}

func TestCDPJoinAssociatesDocumentsCacheAndInitiators(t *testing.T) {
	b, state, page, main, child := associationBrowser(t)
	origin := 1_800_000_000_000.0
	cdpRequest(b, state, "L1", "L1", "F", "GET", "http://a.test/", origin+1, map[string]any{"type": "other"})
	commitFrame(b, state, "F", "L1")
	mainDoc := beginDocument(b, main, page, origin)
	cdpRequest(b, state, "R2", "L1", "F", "GET", "http://a.test/app.css", origin+10, map[string]any{"type": "parser", "url": "http://a.test/index?token=secret", "stack": "never"})
	b.observeCDPResponse(state, map[string]any{"requestId": "R2", "response": map[string]any{"fromDiskCache": true}}, b.evidence.generation)
	cdpRequest(b, state, "L3", "L3", "C", "GET", "http://b.test/frame", origin+20, map[string]any{"type": "parser", "url": "http://a.test/"})
	commitFrame(b, state, "C", "L3")
	childDoc := beginDocument(b, child, page, origin+21)
	cdpRequest(b, state, "R4", "L3", "C", "GET", "http://b.test/data", origin+30, map[string]any{"type": "script"})
	b.observeCDPResponse(state, map[string]any{"requestId": "R4", "response": map[string]any{}}, b.evidence.generation)

	join := func(frame playwright.Frame, method, url string, ordinal int) resourceJoin {
		return resourceJoin{page: page, frame: frame, key: resourceKey(method, url), ordinal: ordinal}
	}
	gen := b.evidence.generation
	css := b.associateResourceEvidence(associationEvidence(t, b, origin+10.4), join(main, "GET", "http://a.test/app.css", 1), gen)
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
	join := resourceJoin{page: page, frame: main, key: resourceKey("GET", "http://a.test/x.js"), ordinal: 1}
	// A loader committed but no document began after it.
	commitFrame(b, state, "F", "L1")
	cdpRequest(b, state, "R1", "L1", "F", "GET", "http://a.test/x.js", origin, nil)
	if v := b.associateResourceEvidence(associationEvidence(t, b, origin), join, gen); v.IdentityUnavailableReason != "document_not_observed" || v.DocumentID != "" {
		t.Fatalf("no document %+v", v)
	}
	// Two documents began in the frame between this commit and the next.
	beginDocument(b, main, page, origin)
	beginDocument(b, main, page, origin+5)
	join.ordinal = 1
	if v := b.associateResourceEvidence(associationEvidence(t, b, origin), join, gen); v.IdentityUnavailableReason != "ambiguous_document_commit" {
		t.Fatalf("two documents %+v", v)
	}
	// A loader that never committed (an aborted navigation) proves nothing.
	cdpRequest(b, state, "L9", "L9", "F", "GET", "http://a.test/aborted", origin+70, nil)
	aborted := resourceJoin{page: page, frame: main, key: resourceKey("GET", "http://a.test/aborted"), ordinal: 1}
	if v := b.associateResourceEvidence(associationEvidence(t, b, origin+70), aborted, gen); v.IdentityUnavailableReason != "document_not_observed" {
		t.Fatalf("uncommitted loader %+v", v)
	}
}

func TestResourceTimingJoinNeedsOneUniqueEntry(t *testing.T) {
	b := &Browser{engine: "webkit"}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	page, frame, other := &browserFinalityCurrentPage{}, &associationFrame{}, &associationFrame{}
	unique, repeated := resourceTimingHash("http://a.test/a.css"), resourceTimingHash("http://a.test/poll")
	first := beginDocument(b, frame, page, 1)
	b.evidence.documents[first].resources = map[string]resourceTimingEntry{unique: {count: 1, start: 12.5}, repeated: {count: 2, start: 30}}
	second := beginDocument(b, other, page, 2)
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
	third := beginDocument(b, frame, page, 3)
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

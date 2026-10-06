package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// associationFixture serves a page with a cacheable stylesheet, an image, a
// cross-origin iframe (no Timing-Allow-Origin), two identical fetches, a
// redirect chain and a page that navigates away while a script is loading.
func associationFixture(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/frame" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<!doctype html><html><body><script>fetch('/inner.json')</script></body></html>`)
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(inner.Close)
	frameURL := strings.Replace(inner.URL, "127.0.0.1", "localhost", 1) + "/frame"
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, `<!doctype html><html><head><link rel=stylesheet href="/app.css"></head><body><img src="/logo.png"><iframe src="%s"></iframe><script>fetch('/data.json');fetch('/data.json');fetch('/r1')</script></body></html>`, frameURL)
		case "/race":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<!doctype html><html><body><script src="/slow.js"></script></body></html><script>setTimeout(()=>{location.href='/'},0)</script>`)
		case "/slow.js":
			time.Sleep(300 * time.Millisecond)
			_, _ = fmt.Fprint(w, `void 0`)
		case "/app.css":
			w.Header().Set("Cache-Control", "max-age=600")
			w.Header().Set("Content-Type", "text/css")
			_, _ = fmt.Fprint(w, `body{margin:0}`)
		case "/r1":
			http.Redirect(w, r, "/final.json", http.StatusFound)
		default:
			_, _ = fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(outer.Close)
	return outer, inner
}

func associationSession(t *testing.T, engine, baseURL string) *Browser {
	t.Helper()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": baseURL, "browser_headless": true, "browser_engine": engine, "browser_capture_document_metrics": true}); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	return b
}

func associationVisit(t *testing.T, b *Browser, target string) []protocol.NetworkEntry {
	t.Helper()
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open " + target, Type: "browser_navigate", Target: target})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("navigate %s: %v %s", target, err, result.ErrorMessage)
	}
	settle, err := b.Execute(context.Background(), &protocol.Action{Name: "settle", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "duration", Value: "700ms"}})
	if err != nil || settle.ErrorMessage != "" {
		t.Fatalf("settle: %v %s", err, settle.ErrorMessage)
	}
	return append(append([]protocol.NetworkEntry{}, result.NetworkEntries...), settle.NetworkEntries...)
}

// documentsByScope lists document IDs by frame scope, oldest first.
func documentsByScope(b *Browser) map[string][]string {
	evidence := b.BrowserDocumentEvidence()
	starts := map[string]time.Time{}
	scopes := map[string]string{}
	for _, o := range evidence.Observations {
		if at, seen := starts[o.DocumentID]; !seen || o.WindowStart.Before(at) {
			starts[o.DocumentID] = o.WindowStart
		}
		scopes[o.DocumentID] = o.FrameScope
	}
	out := map[string][]string{}
	for id, scope := range scopes {
		out[scope] = append(out[scope], id)
	}
	for scope := range out {
		sort.Slice(out[scope], func(i, j int) bool { return starts[out[scope][i]].Before(starts[out[scope][j]]) })
	}
	return out
}

func entriesFor(entries []protocol.NetworkEntry, suffix string) []protocol.NetworkEntry {
	var out []protocol.NetworkEntry
	for _, e := range entries {
		if strings.HasSuffix(e.URL, suffix) && e.BrowserEvidence != nil {
			out = append(out, e)
		}
	}
	return out
}

func TestChromiumResourcesJoinTheDocumentsThatIssuedThem(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	outer, _ := associationFixture(t)
	b := associationSession(t, "chromium", outer.URL)
	first := associationVisit(t, b, "/")
	// A new page first holds an about:blank document; the visit is the last.
	docs := documentsByScope(b)
	if len(docs["main_frame"]) != 2 || len(docs["child_frame"]) != 1 {
		t.Fatalf("documents %v", docs)
	}
	mainDoc, frameDoc := docs["main_frame"][1], docs["child_frame"][0]
	expect := func(suffix, document, initiator string, count int) {
		t.Helper()
		matches := entriesFor(first, suffix)
		if len(matches) != count {
			t.Fatalf("%s: %d evidence rows, want %d", suffix, len(matches), count)
		}
		for _, e := range matches {
			v := e.BrowserEvidence
			if v.DocumentID != document || v.DocumentAssociation != "cdp_loader_join" || v.IdentityUnavailableReason != "" {
				t.Fatalf("%s associated with %q (%s, %s), want %s", suffix, v.DocumentID, v.DocumentAssociation, v.IdentityUnavailableReason, document)
			}
			if initiator != "" && v.InitiatorType != initiator {
				t.Fatalf("%s initiator %q, want %s", suffix, v.InitiatorType, initiator)
			}
			if v.DocumentOffsetMS == nil || *v.DocumentOffsetMS < -1000 || *v.DocumentOffsetMS > 60000 {
				t.Fatalf("%s offset %v", suffix, v.DocumentOffsetMS)
			}
			if strings.Contains(v.InitiatorOrigin, "/") && strings.Count(v.InitiatorOrigin, "/") > 2 {
				t.Fatalf("%s initiator origin kept a path: %s", suffix, v.InitiatorOrigin)
			}
		}
	}
	expect("/", mainDoc, "", 1)
	expect("/app.css", mainDoc, "parser", 1)
	expect("/logo.png", mainDoc, "parser", 1)
	expect("/data.json", mainDoc, "script", 2)
	expect("/r1", mainDoc, "script", 1)
	expect("/final.json", mainDoc, "", 1)
	expect("/frame", frameDoc, "parser", 1)
	expect("/inner.json", frameDoc, "script", 1)
	if css := entriesFor(first, "/app.css")[0].BrowserEvidence; css.Cache == nil || *css.Cache {
		t.Fatalf("first stylesheet load cache %v", css.Cache)
	}

	// A revisit is a new document and serves the stylesheet from cache.
	second := associationVisit(t, b, "/")
	docs = documentsByScope(b)
	if len(docs["main_frame"]) != 3 {
		t.Fatalf("documents after revisit %v", docs)
	}
	revisit := docs["main_frame"][2]
	if css := entriesFor(second, "/app.css"); len(css) != 1 || css[0].BrowserEvidence.DocumentID != revisit || css[0].BrowserEvidence.Cache == nil || !*css[0].BrowserEvidence.Cache {
		t.Fatalf("revisited stylesheet %+v", css)
	}
}

func TestChromiumNavigationRaceKeepsResourcesWithTheirDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	outer, _ := associationFixture(t)
	b := associationSession(t, "chromium", outer.URL)
	entries := associationVisit(t, b, "/race")
	docs := documentsByScope(b)
	if len(docs["main_frame"]) < 3 {
		t.Fatalf("race documents %v", docs)
	}
	raceDoc, landing := docs["main_frame"][len(docs["main_frame"])-2], docs["main_frame"][len(docs["main_frame"])-1]
	for _, e := range entriesFor(entries, "/slow.js") {
		if v := e.BrowserEvidence; v.DocumentID != "" && v.DocumentID != raceDoc {
			t.Fatalf("a script of the abandoned document joined %s", v.DocumentID)
		}
	}
	for _, e := range entriesFor(entries, "/app.css") {
		if v := e.BrowserEvidence; v.DocumentID != landing {
			t.Fatalf("the landing page's stylesheet joined %q (%s)", v.DocumentID, v.IdentityUnavailableReason)
		}
	}
}

func TestWebKitAssociatesOnlyThroughUniqueResourceTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	outer, _ := associationFixture(t)
	b := associationSession(t, "webkit", outer.URL)
	entries := associationVisit(t, b, "/")
	associated := 0
	for _, e := range entries {
		v := e.BrowserEvidence
		if v == nil {
			continue
		}
		if v.Cache != nil || v.InitiatorType != "" || v.DocumentAssociation == "cdp_loader_join" {
			t.Fatalf("WebKit exposed CDP provenance: %+v", v)
		}
		if v.DocumentAssociation == "page_resource_timing_join" {
			associated++
			if v.DocumentOffsetMS == nil {
				t.Fatalf("Resource Timing join without its offset: %+v", v)
			}
		}
		// Two identical fetches can never join by Resource Timing.
		if strings.HasSuffix(e.URL, "/data.json") && v.DocumentID != "" {
			t.Fatalf("repeated URL joined a document: %+v", v)
		}
	}
	if associated == 0 {
		t.Fatal("no WebKit resource joined its document")
	}
}

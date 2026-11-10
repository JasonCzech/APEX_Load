package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/deeplink"
	"github.com/jasonczech/apexload/pkg/storage"
)

const waterfallTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

// tracedWaterfallFixture is waterfallFixture with a sent trace on request B
// and an unsent one on C (an id the request never propagated).
func tracedWaterfallFixture() []TransactionEvent {
	events := waterfallFixture(0)
	sent, unsent := true, false
	for index := range events {
		switch events[index].ActionName {
		case "B":
			events[index].TraceID, events[index].TraceSent = waterfallTraceID, &sent
		case "C":
			events[index].TraceID, events[index].TraceSent = "ffffffffffffffffffffffffffffffff", &unsent
		}
	}
	return events
}

func serveTracedWaterfall(t *testing.T, server *Server, report *RunArchiveReport) ArchiveTransactionWaterfall {
	t.Helper()
	representative := waterfallRepresentative(t, report, "Checkout")
	rec := serveWaterfallForTest(server, representative.EventID, fmt.Sprintf("ordinal=%d&first_ordinal=%d", representative.Ordinal, representative.FirstOrdinal))
	var waterfall ArchiveTransactionWaterfall
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &waterfall) != nil {
		t.Fatalf("waterfall: %d %s", rec.Code, rec.Body.String())
	}
	return waterfall
}

func waterfallRequestByName(t *testing.T, waterfall ArchiveTransactionWaterfall, name string) ArchiveWaterfallRow {
	t.Helper()
	for _, row := range waterfall.Requests {
		if row.ActionName == name {
			return row
		}
	}
	t.Fatalf("no request %s in %+v", name, waterfall.Requests)
	return ArchiveWaterfallRow{}
}

func TestTransactionWaterfallLinksTracedRequestsToTheRunsAPM(t *testing.T) {
	server, report := waterfallHandlerServer(t, tracedWaterfallFixture())
	// No trace link template yet: the trace id is shown, with no links.
	unlinked := serveTracedWaterfall(t, server, report)
	if b := waterfallRequestByName(t, unlinked, "B"); b.TraceID != waterfallTraceID || b.TraceLinks != nil || unlinked.TraceLinksUnavailable {
		t.Fatalf("without a template: %+v unavailable=%v", b, unlinked.TraceLinksUnavailable)
	}

	store := server.store.(*storage.SQLiteStore)
	if err := store.CreateDynatraceConnection(&storage.DynatraceConnection{ID: "dt-waterfall", ProjectID: "proj-a", Name: "Dynatrace", Enabled: true, EnvironmentURL: "https://abc123.live.dynatrace.com", EnvironmentTokenSecret: "token", ProblemsEnabled: true, PollIntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests["test-query"] = &ManagedTest{ID: "test-query", ProjectID: "proj-a", Status: "COMPLETE", DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-waterfall"}}
	server.runner.mu.Unlock()

	// The cached drill-down is shared: linking must not leak into it.
	waterfall := serveTracedWaterfall(t, server, report)
	b := waterfallRequestByName(t, waterfall, "B")
	if len(b.TraceLinks) != 1 {
		t.Fatalf("B links = %+v", b.TraceLinks)
	}
	link := b.TraceLinks[0]
	parsed, err := url.Parse(link.URL)
	if err != nil || link.Provider != "dynatrace" || link.ConnectionID != "dt-waterfall" || parsed.Scheme != "https" || parsed.Host != "abc123.live.dynatrace.com" || parsed.Query().Get("traceId") != waterfallTraceID {
		t.Fatalf("B link = %+v (%v)", link, err)
	}
	// No secret leaves in the link: the connection's token is not in it.
	if strings.Contains(link.URL, "token") {
		t.Fatalf("link carries a secret: %s", link.URL)
	}
	// A trace that was never sent has no id, so no link; untraced rows and the root have none.
	for _, name := range []string{"A", "C"} {
		if row := waterfallRequestByName(t, waterfall, name); row.TraceID != "" || row.TraceLinks != nil {
			t.Fatalf("%s = %+v", name, row)
		}
	}
	if waterfall.Root.TraceLinks != nil || waterfall.TraceLinksUnavailable {
		t.Fatalf("root = %+v unavailable=%v", waterfall.Root, waterfall.TraceLinksUnavailable)
	}
	if again := serveTracedWaterfall(t, server, report); len(waterfallRequestByName(t, again, "B").TraceLinks) != 1 {
		t.Fatalf("a repeat lost or doubled the links: %+v", again)
	}
}

func TestTransactionWaterfallWithoutTracesSkipsLinkSettings(t *testing.T) {
	server, report := waterfallHandlerServer(t, waterfallFixture(0))
	// A test the runner does not know would fail a link lookup; with no traced
	// row the drill-down never asks.
	waterfall := serveTracedWaterfall(t, server, report)
	if waterfall.TraceLinksUnavailable {
		t.Fatal("an untraced drill-down reported its links unavailable")
	}
	for _, row := range waterfall.Requests {
		if row.TraceLinks != nil {
			t.Fatalf("untraced row linked: %+v", row)
		}
	}
}

func TestArchiveWaterfallTraceLinksCopyAndPadTheWindow(t *testing.T) {
	compiled, err := deeplink.Compile("https://kibana.example.test/app/discover#/?_g=(time:(from:'{from_utc}',to:'{to_utc}'))&q={trace_id}", []string{"kibana.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	needsTrace, err := deeplink.Compile("https://abc123.live.dynatrace.com/trace?traceId={trace_id}", []string{"abc123.live.dynatrace.com"})
	if err != nil {
		t.Fatal(err)
	}
	targets := []traceLinkTarget{{provider: "elasticsearch", connectionID: "es", template: compiled}, {provider: "dynatrace", connectionID: "dt", template: needsTrace}}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	original := &archiveWaterfallResult{
		Root:     ArchiveWaterfallRow{EventID: "root", Timestamp: at, DurationMs: 1000},
		Requests: []ArchiveWaterfallRow{{EventID: "a", Timestamp: at, DurationMs: 60_000, TraceID: "t1"}, {EventID: "b", Timestamp: at}},
	}
	linked := withArchiveWaterfallTraceLinks(original, targets)
	if linked == original || original.Requests[0].TraceLinks != nil {
		t.Fatal("linking changed the shared value")
	}
	links := linked.Requests[0].TraceLinks
	if len(links) != 2 || links[0].Provider != "elasticsearch" || links[1].Provider != "dynatrace" {
		t.Fatalf("links = %+v", links)
	}
	// The window covers the request's own time, padded on both sides.
	if !strings.Contains(links[0].URL, "from:'2026-11-01T09%3A55%3A00Z'") || !strings.Contains(links[0].URL, "to:'2026-11-01T10%3A06%3A00Z'") {
		t.Fatalf("window = %s", links[0].URL)
	}
	if linked.Requests[1].TraceLinks != nil || linked.Root.TraceLinks != nil {
		t.Fatalf("untraced rows linked: %+v", linked)
	}
	// Nothing to link: the shared value is returned as is.
	if same := withArchiveWaterfallTraceLinks(original, nil); same != original {
		t.Fatal("no targets copied the value")
	}
	untraced := &archiveWaterfallResult{Root: original.Root, Requests: original.Requests[1:]}
	if same := withArchiveWaterfallTraceLinks(untraced, targets); same != untraced {
		t.Fatal("no traced row copied the value")
	}
}

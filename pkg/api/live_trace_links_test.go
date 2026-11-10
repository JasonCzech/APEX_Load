package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func liveTraceLinksReply(t *testing.T, server *Server, testID string, body any) (int, LiveTraceLinksResponse, string) {
	t.Helper()
	reply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/"+testID+"/live/trace-links", body)
	var out LiveTraceLinksResponse
	if reply.Code == http.StatusOK {
		if err := json.Unmarshal(reply.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, reply.Body.String())
		}
	}
	return reply.Code, out, reply.Body.String()
}

// distinctTraceIDs returns n distinct valid W3C trace ids.
func distinctTraceIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%032x", i+1)
	}
	return ids
}

func TestLiveTraceLinksExpandTheRunsProvidersWhileRunning(t *testing.T) {
	server, store := baselineTestServer(t)
	linkFixtureConnections(t, store)
	project := storage.DefaultProjectID
	elkPath := "/api/v1/projects/" + project + "/monitoring/connection-links/elk/elk-links"
	if saved := timeslotRequest(t, server, http.MethodPut, elkPath, map[string]any{"revision": 0, "ui_url": "https://kibana.example.test"}); saved.Code != http.StatusOK {
		t.Fatalf("save Kibana UI: %d %s", saved.Code, saved.Body.String())
	}
	server.runner.mu.Lock()
	// A running test with no archive: links come from its bindings alone.
	server.runner.tests["live-run"] = &ManagedTest{ID: "live-run", ProjectID: project, Status: "RUNNING",
		DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-links"}, ELKCapture: &ELKCaptureConfig{ConnectionID: "elk-links"}}
	server.runner.tests["bare-run"] = &ManagedTest{ID: "bare-run", ProjectID: project, Status: "RUNNING"}
	// Bound to a connection of another project: never resolved.
	server.runner.tests["foreign-run"] = &ManagedTest{ID: "foreign-run", ProjectID: "other-project", Status: "RUNNING",
		DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-links"}}
	server.runner.mu.Unlock()

	first, second := "0af7651916cd43dd8448eb211c80319c", "4bf92f3577b34da6a3ce929d0e0e4736"
	window := map[string]any{"from": "2026-11-10T10:00:00Z", "to": "2026-11-10T10:10:00Z"}
	body := func(ids ...string) map[string]any {
		out := map[string]any{"trace_ids": ids}
		for k, v := range window {
			out[k] = v
		}
		return out
	}
	code, out, raw := liveTraceLinksReply(t, server, "live-run", body(first, second, first))
	if code != http.StatusOK || !out.Configured || out.LinksUnavailable || len(out.Traces) != 2 || out.Traces[0].TraceID != first || out.Traces[1].TraceID != second {
		t.Fatalf("live links: %d %s", code, raw)
	}
	for _, trace := range out.Traces {
		providers := map[string]*url.URL{}
		for _, link := range trace.Links {
			u, err := url.Parse(link.URL)
			if err != nil {
				t.Fatal(err)
			}
			providers[link.Provider] = u
			if link.ConnectionID == "" {
				t.Fatalf("link without a connection: %+v", link)
			}
		}
		if dt := providers["dynatrace"]; dt == nil || dt.Host != "abc123.live.dynatrace.com" || dt.Query().Get("traceId") != trace.TraceID {
			t.Fatalf("Dynatrace link %v for %s", dt, trace.TraceID)
		}
		if kb := providers["elasticsearch"]; kb == nil || kb.Host != "kibana.example.test" || !strings.Contains(kb.Fragment, trace.TraceID) || !strings.Contains(kb.Fragment, "2026-11-10T10:00:00Z") {
			t.Fatalf("Kibana link %v for %s", kb, trace.TraceID)
		}
	}
	if strings.Contains(raw, "token") || strings.Contains(raw, "elk-key") {
		t.Fatalf("response carries secret names: %s", raw)
	}

	// No trace provider: every trace is listed, unlinked, and not configured.
	for _, id := range []string{"bare-run", "foreign-run"} {
		code, out, raw := liveTraceLinksReply(t, server, id, body(first))
		if code != http.StatusOK || out.Configured || len(out.Traces) != 1 || len(out.Traces[0].Links) != 0 {
			t.Fatalf("%s: %d %s", id, code, raw)
		}
	}

	for name, bad := range map[string]any{
		"no ids":        body(),
		"too many ids":  body(distinctTraceIDs(maxLiveTraceLinkIDs + 1)...),
		"uppercase id":  body(strings.ToUpper(first)),
		"short id":      body("abc"),
		"all-zero id":   body(strings.Repeat("0", 32)),
		"no window":     map[string]any{"trace_ids": []string{first}},
		"reverse range": map[string]any{"trace_ids": []string{first}, "from": "2026-11-10T11:00:00Z", "to": "2026-11-10T10:00:00Z"},
		"too wide":      map[string]any{"trace_ids": []string{first}, "from": "2026-10-01T10:00:00Z", "to": "2026-11-10T10:00:00Z"},
	} {
		if code, _, raw := liveTraceLinksReply(t, server, "live-run", bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	// A whole window's exemplars fit one request, through the route's body limit.
	if code, out, raw := liveTraceLinksReply(t, server, "live-run", body(distinctTraceIDs(maxLiveTraceLinkIDs)...)); code != http.StatusOK || len(out.Traces) != maxLiveTraceLinkIDs || !out.Configured {
		t.Fatalf("full window: %d %.300s", code, raw)
	}
	if code, _, _ := liveTraceLinksReply(t, server, "missing", body(first)); code != http.StatusNotFound {
		t.Fatalf("missing run: %d", code)
	}
}

func TestLiveTraceLinksRouteIsViewerScopedWithABodyLimit(t *testing.T) {
	got, ok := accessRequirementFromRouteCatalog(http.MethodPost, "/api/v1/tests/{id}/live/trace-links")
	if !ok {
		t.Fatal("route is absent from the authorization catalog")
	}
	want := viewerProjectAccessWithBodyLimit(resolveTestProjectID, liveTraceLinksMaxBodyBytes)
	// A read: service accounts need results:read, not tests:run.
	if got.MaxBodyBytes != want.MaxBodyBytes || got.ProjectLookup == nil || strings.Join(got.ProjectRoles, ",") != strings.Join(want.ProjectRoles, ",") || got.ServiceAccountScope != "results:read" {
		t.Fatalf("access = %+v, want %+v", got, want)
	}
	// A full window of ids (the request cap) must fit the body limit.
	full, err := json.Marshal(LiveTraceLinksRequest{TraceIDs: distinctTraceIDs(maxLiveTraceLinkIDs), From: "2026-11-10T10:00:00.000000000Z", To: "2026-11-10T10:10:00.000000000Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) > liveTraceLinksMaxBodyBytes {
		t.Fatalf("a %d-id request is %d bytes, over the %d-byte body limit", maxLiveTraceLinkIDs, len(full), liveTraceLinksMaxBodyBytes)
	}
}

func TestLiveTraceLinksReportUnreadableSettingsAsUnavailable(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	wrapped := &failingJobsStore{SQLiteStore: store}
	server := NewServer(ServerConfig{Store: sqliteOverlay(wrapped)})
	if server.providerMonitoringCancel != nil {
		server.providerMonitoringCancel()
	}
	linkFixtureConnections(t, store)
	server.runner.mu.Lock()
	server.runner.tests["live-run"] = &ManagedTest{ID: "live-run", ProjectID: storage.DefaultProjectID, Status: "RUNNING",
		DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-links"}}
	server.runner.mu.Unlock()
	id := "0af7651916cd43dd8448eb211c80319c"
	body := map[string]any{"trace_ids": []string{id}, "from": "2026-11-10T10:00:00Z", "to": "2026-11-10T10:10:00Z"}
	if code, out, raw := liveTraceLinksReply(t, server, "live-run", body); code != http.StatusOK || !out.Configured || out.LinksUnavailable {
		t.Fatalf("healthy: %d %s", code, raw)
	}
	// Unreadable link settings: the trace ids still come back, unlinked and flagged.
	wrapped.fail.Store(true)
	code, out, raw := liveTraceLinksReply(t, server, "live-run", body)
	if code != http.StatusOK || !out.LinksUnavailable || out.Configured || len(out.Traces) != 1 || out.Traces[0].TraceID != id || len(out.Traces[0].Links) != 0 {
		t.Fatalf("degraded: %d %s", code, raw)
	}
	if !strings.Contains(raw, `"links":[]`) {
		t.Fatalf("degraded traces must carry an empty link list: %s", raw)
	}
}

// TestLiveTraceLinksClientCapMatchesServer pins the dashboard's id cap (the
// drawer's batch size and the request type's documented bound) to
// maxLiveTraceLinkIDs: the drawer tests mock the API, so a drift would
// otherwise surface only as 400s (cap lowered here) or a flood of small
// requests (a caller following a stale, smaller bound).
func TestLiveTraceLinksClientCapMatchesServer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "src", "types", "rca.ts"))
	if err != nil {
		t.Fatalf("read rca.ts: %v", err)
	}
	source := string(raw)
	match := regexp.MustCompile(`export const LIVE_TRACE_LINKS_MAX_IDS = (\d+)`).FindStringSubmatch(source)
	if match == nil {
		t.Fatal("rca.ts no longer exports LIVE_TRACE_LINKS_MAX_IDS")
	}
	if got, _ := strconv.Atoi(match[1]); got != maxLiveTraceLinkIDs {
		t.Fatalf("LIVE_TRACE_LINKS_MAX_IDS = %d, server maxLiveTraceLinkIDs = %d", got, maxLiveTraceLinkIDs)
	}
	if want := fmt.Sprintf("1 to LIVE_TRACE_LINKS_MAX_IDS (%d) W3C trace ids", maxLiveTraceLinkIDs); !strings.Contains(source, want) {
		t.Fatalf("LiveTraceLinksRequest.trace_ids comment does not state the server bound %q", want)
	}
	drawer, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "src", "components", "LiveExemplarDrawer.tsx"))
	if err != nil {
		t.Fatalf("read LiveExemplarDrawer.tsx: %v", err)
	}
	if !strings.Contains(string(drawer), "const LIVE_TRACE_LINK_BATCH = LIVE_TRACE_LINKS_MAX_IDS") {
		t.Fatal("LiveExemplarDrawer batches trace ids by something other than LIVE_TRACE_LINKS_MAX_IDS")
	}
}

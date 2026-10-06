package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func linkFixtureConnections(t *testing.T, store *storage.SQLiteStore) {
	t.Helper()
	project := storage.DefaultProjectID
	if err := store.CreateDynatraceConnection(&storage.DynatraceConnection{ID: "dt-links", ProjectID: project, Name: "Dynatrace", Enabled: true, EnvironmentURL: "https://abc123.live.dynatrace.com", EnvironmentTokenSecret: "token", ProblemsEnabled: true, PollIntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateELKConnection(&storage.ELKConnection{ID: "elk-links", ProjectID: project, Name: "Logs", Enabled: true, Endpoint: "https://es.example.test:9200", IndexPattern: "logs-*", TimestampField: "@timestamp", TraceIDField: "trace.id", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "elk-key"}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionLinkSettingsRejectUnsafeTemplates(t *testing.T) {
	server, store := baselineTestServer(t)
	linkFixtureConnections(t, store)
	path := "/api/v1/projects/" + storage.DefaultProjectID + "/monitoring/connection-links/dynatrace/dt-links"
	for name, template := range map[string]string{
		"host mismatch":           "https://evil.example.com/trace/{trace_id}",
		"non-https scheme":        "http://abc123.live.dynatrace.com/trace/{trace_id}",
		"injected into the host":  "https://{trace_id}.live.dynatrace.com/",
		"unknown placeholder":     "https://abc123.live.dynatrace.com/{secret}",
		"credentials in the link": "https://user@abc123.live.dynatrace.com/{trace_id}",
	} {
		if reply := timeslotRequest(t, server, http.MethodPut, path, map[string]any{"revision": 0, "trace_template": template}); reply.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, reply.Code, reply.Body.String())
		}
	}
	// Kibana is not the search endpoint, so its host must be declared first.
	elkPath := "/api/v1/projects/" + storage.DefaultProjectID + "/monitoring/connection-links/elk/elk-links"
	if reply := timeslotRequest(t, server, http.MethodPut, elkPath, map[string]any{"revision": 0, "trace_template": "https://kibana.example.test/app/discover#/?q={trace_id}"}); reply.Code != http.StatusBadRequest {
		t.Fatalf("undeclared Kibana host: %d %s", reply.Code, reply.Body.String())
	}
	for name, uiURL := range map[string]string{
		"plain http":             "http://kibana.example.test",
		"brace breaks default":   "https://kibana.example.test/a}b",
		"placeholder in the URL": "https://kibana.example.test/{to_utc}",
		"default link too long":  "https://kibana.example.test/" + strings.Repeat("a", 1950),
	} {
		if reply := timeslotRequest(t, server, http.MethodPut, elkPath, map[string]any{"revision": 0, "ui_url": uiURL}); reply.Code != http.StatusBadRequest {
			t.Fatalf("%s UI URL: %d %s", name, reply.Code, reply.Body.String())
		}
	}
	// Dynatrace links stay on the connection's own environment and platform.
	if reply := timeslotRequest(t, server, http.MethodPut, path, map[string]any{"revision": 0, "ui_url": "https://evil.example.com", "trace_template": "https://evil.example.com/{trace_id}"}); reply.Code != http.StatusBadRequest {
		t.Fatalf("Dynatrace UI URL: %d %s", reply.Code, reply.Body.String())
	}
	if reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/projects/"+storage.DefaultProjectID+"/monitoring/connection-links/elk/missing", nil); reply.Code != http.StatusNotFound {
		t.Fatalf("missing connection: %d", reply.Code)
	}
}

// failingJobsStore fails run monitor job listing on demand.
type failingJobsStore struct {
	*storage.SQLiteStore
	fail atomic.Bool
}

func (s *failingJobsStore) ListRunMonitorJobs(project, run string) ([]storage.RunMonitorJob, error) {
	if s.fail.Load() {
		return nil, errors.New("storage offline")
	}
	return s.SQLiteStore.ListRunMonitorJobs(project, run)
}

func TestExemplarAPIReturnsWorkingDeepLinks(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	wrapped := &failingJobsStore{SQLiteStore: store}
	server := NewServer(ServerConfig{Store: wrapped})
	// The run gets a live direct-mode monitor job; keep the poller off it.
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	linkFixtureConnections(t, store)
	record := exemplarArchiveFixture(t, server, store, "linked-run", nil)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	admitArchiveForTest(t, record)
	if err := server.runner.persistExemplarArchive(context.Background(), record, manifest); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(record.ManifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Dynatrace comes from the legacy capture, Elasticsearch from a
	// trace-lookup monitor source.
	server.runner.mu.Lock()
	server.runner.tests["linked-run"] = &ManagedTest{ID: "linked-run", ProjectID: storage.DefaultProjectID, Status: "COMPLETE",
		DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-links"}}
	server.runner.mu.Unlock()
	source := storage.MonitorSource{ID: "elastic-links", ProjectID: storage.DefaultProjectID, Name: "Log buckets", Enabled: true, Pack: "elastic-query", ExecutorMode: "direct", ConnectionID: "elk-links", IntervalSeconds: 30, ElasticQuery: &storage.ElasticQuerySelection{ProjectID: storage.DefaultProjectID}}
	if _, err := store.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetTest("linked-run")
	if err != nil {
		t.Fatal(err)
	}
	run.MonitorBindings = &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}
	if err := store.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.StartRunMonitors(storage.DefaultProjectID, "linked-run", "", exemplarFixtureStart, exemplarFixtureStart.Add(time.Hour)); err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}

	elkPath := "/api/v1/projects/" + storage.DefaultProjectID + "/monitoring/connection-links/elk/elk-links"
	saved := timeslotRequest(t, server, http.MethodPut, elkPath, map[string]any{"revision": 0, "ui_url": "https://kibana.example.test"})
	var links ConnectionLinksResponse
	if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), &links) != nil || links.Revision != 1 || !strings.HasPrefix(links.TraceTemplate, "https://kibana.example.test/app/discover#/") {
		t.Fatalf("save Kibana UI: %d %s", saved.Code, saved.Body.String())
	}

	readPage := func(query string) ExemplarPage {
		t.Helper()
		reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/linked-run/archive/exemplars?"+query, nil)
		var page ExemplarPage
		if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &page) != nil {
			t.Fatalf("exemplars %s: %d %s", query, reply.Code, reply.Body.String())
		}
		return page
	}
	page := readPage("limit=5")
	if len(page.Exemplars) != 5 || page.Total <= 5 || page.Total != len(archivedTraceIDs(t, record, manifest)) {
		t.Fatalf("page of %d from %d", len(page.Exemplars), page.Total)
	}
	// Offsets continue the same order; the last page is short.
	next := readPage("limit=5&offset=5")
	if next.Total != page.Total || len(next.Exemplars) != 5 || next.Exemplars[0].TraceID == page.Exemplars[0].TraceID || next.Exemplars[0].TraceID != archivedTraceIDs(t, record, manifest)[5] {
		t.Fatalf("second page %v", next.Exemplars)
	}
	if last := readPage(fmt.Sprintf("limit=5&offset=%d", page.Total-2)); len(last.Exemplars) != 2 {
		t.Fatalf("last page has %d", len(last.Exemplars))
	}
	for _, exemplar := range page.Exemplars {
		providers := map[string]*url.URL{}
		for _, link := range exemplar.Links {
			u, err := url.Parse(link.URL)
			if err != nil {
				t.Fatal(err)
			}
			providers[link.Provider] = u
		}
		dynatrace, kibana := providers["dynatrace"], providers["elasticsearch"]
		if dynatrace == nil || dynatrace.Host != "abc123.live.dynatrace.com" || dynatrace.Query().Get("traceId") != exemplar.TraceID {
			t.Fatalf("Dynatrace link %v for trace %s", dynatrace, exemplar.TraceID)
		}
		if kibana == nil || kibana.Host != "kibana.example.test" || !strings.Contains(kibana.Fragment, `query:'trace.id:"`+exemplar.TraceID+`"'`) || !strings.Contains(kibana.Fragment, "from:'"+exemplar.Timestamp.Add(-exemplarLinkWindow).UTC().Format("2006-01-02T15:04:05Z")+"'") {
			t.Fatalf("Kibana link %v for trace %s at %s", kibana, exemplar.TraceID, exemplar.Timestamp)
		}
	}
	// Filtering by group and transaction keeps paging consistent.
	if filtered := readPage("name=missing"); filtered.Total != 0 || len(filtered.Exemplars) != 0 {
		t.Fatalf("filtered: %+v", filtered)
	}
	if grouped := readPage("group=shop&name=GET%20/cart&limit=1"); grouped.Total != page.Total || len(grouped.Exemplars) != 1 {
		t.Fatalf("group filter: %d of %d", len(grouped.Exemplars), grouped.Total)
	}
	if other := readPage("group=admin&name=GET%20/cart"); other.Total != 0 || other.LinksUnavailable {
		t.Fatalf("another group: %+v", other)
	}
	// Population and a time window narrow the same scan, so total still
	// counts every match.
	archived := archivedExemplars(t, record, manifest)
	at := archived[len(archived)/2].Timestamp.UTC()
	inWindow, ofPopulation := 0, 0
	for _, exemplar := range archived {
		if !exemplar.Timestamp.Before(at) && !exemplar.Timestamp.After(at.Add(10*time.Second)) {
			inWindow++
		}
		if exemplar.Population == archived[0].Population {
			ofPopulation++
		}
	}
	windowed := readPage("from=" + url.QueryEscape(at.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(at.Add(10*time.Second).Format(time.RFC3339Nano)))
	if windowed.Total != inWindow || inWindow == 0 || inWindow == len(archived) {
		t.Fatalf("window: %d of %d, want %d", windowed.Total, len(archived), inWindow)
	}
	for _, exemplar := range windowed.Exemplars {
		if exemplar.Timestamp.Before(at) || exemplar.Timestamp.After(at.Add(10*time.Second)) {
			t.Fatalf("exemplar at %s outside the window", exemplar.Timestamp)
		}
	}
	if populated := readPage("population=" + url.QueryEscape(archived[0].Population)); populated.Total != ofPopulation {
		t.Fatalf("population: %d, want %d", populated.Total, ofPopulation)
	}
	if none := readPage("population=no-such-population"); none.Total != 0 {
		t.Fatalf("unknown population: %+v", none)
	}
	if bad := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/linked-run/archive/exemplars?from=yesterday", nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad from: %d %s", bad.Code, bad.Body.String())
	}
	// Unreadable link settings leave the exemplars readable, unlinked.
	wrapped.fail.Store(true)
	if degraded := readPage("limit=5"); !degraded.LinksUnavailable || len(degraded.Exemplars) != 5 || degraded.Total != page.Total || len(degraded.Exemplars[0].Links) != 0 {
		t.Fatalf("degraded page: %+v", degraded)
	}
}

// archivedTraceIDs reads the archived exemplars' trace IDs in file order.
func archivedTraceIDs(t *testing.T, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) []string {
	t.Helper()
	var ids []string
	for _, exemplar := range archivedExemplars(t, record, manifest) {
		ids = append(ids, exemplar.TraceID)
	}
	return ids
}

// archivedExemplars reads the archived exemplars in file order.
func archivedExemplars(t *testing.T, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) []ArchiveExemplar {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.ExemplarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var out []ArchiveExemplar
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var exemplar ArchiveExemplar
		if err := json.Unmarshal([]byte(line), &exemplar); err != nil {
			t.Fatal(err)
		}
		out = append(out, exemplar)
	}
	return out
}

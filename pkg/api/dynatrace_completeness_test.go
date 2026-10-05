package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/storage"
)

// documentedDynatraceTenant answers the Classic Environment API v2 with bodies
// shaped like the API reference examples. Every selected operation must reach
// complete coverage on these shapes, or the selected-coverage gate can never
// advance the watermark and required monitoring always ends incomplete.
func documentedDynatraceTenant(t *testing.T, windowStart time.Time) dynatraceRoundTripFunc {
	t.Helper()
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join("..", "dynatrace", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		// Keep fixture diagnostics inside the run window.
		return strings.ReplaceAll(string(body), "1791028800000", strconv.FormatInt(windowStart.UnixMilli(), 10))
	}
	reply := func(body string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v2/metrics/builtin:service.requestCount.total":
			return reply(`{"metricId":"builtin:service.requestCount.total","displayName":"Request count","description":"","unit":"Count","dduBillable":false,"created":0,"lastWritten":0,"entityType":["SERVICE"],"aggregationTypes":["auto","value","count","sum","min","max","avg"],"transformations":["filter","fold","limit","merge","names","parents","timeshift","sort","last","splitBy","lastReal","setUnit","rate","default","delta","smooth","rollup","partition","toUnit"],"defaultAggregation":{"type":"value"},"dimensionDefinitions":[{"key":"dt.entity.service","name":"Service","displayName":"Service","index":0,"type":"ENTITY"}],"tags":[],"metricValueType":{"type":"unknown"},"scalar":false,"resolutionInfSupported":true}`)
		case "/api/v2/metrics/query":
			from, err1 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("from"))
			to, err2 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("to"))
			if err1 != nil || err2 != nil {
				t.Errorf("metric window %s", r.URL.RawQuery)
			}
			timestamps, values := []int64{}, []any{}
			for end := from.Truncate(time.Minute).Add(time.Minute); !end.After(to.Truncate(time.Minute).Add(time.Minute)); end = end.Add(time.Minute) {
				timestamps = append(timestamps, end.UnixMilli())
				values = append(values, float64(len(values)))
			}
			body, _ := json.Marshal(map[string]any{"totalCount": 1, "nextPageKey": nil, "resolution": "1m", "result": []any{map[string]any{"metricId": "builtin:service.requestCount.total:sum", "dataPointCountRatio": 0.0001, "dimensionCountRatio": 0.0001, "data": []any{map[string]any{"dimensions": []string{"SERVICE-0000000000000001"}, "dimensionMap": map[string]string{"dt.entity.service": "SERVICE-0000000000000001"}, "timestamps": timestamps, "values": values}}}}})
			return reply(string(body))
		case "/api/v2/problems":
			return reply(strings.Replace(read("problems.json"), `"nextPageKey": "page-two",`, "", 1))
		case "/api/v2/events":
			return reply(read("events.json"))
		case "/api/v2/entities":
			if r.URL.Query().Get("fields") != "+fromRelationships,+toRelationships" {
				t.Errorf("entity relationships requested as %q", r.URL.Query().Get("fields"))
			}
			return reply(read("entities.json"))
		}
		t.Errorf("unexpected Dynatrace request %s", r.URL.Path)
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
}

func TestDynatraceDirectCollectionCompletesSelectedCoverageOnDocumentedResponses(t *testing.T) {
	server, db := newTimeslotServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", "")
	c := &storage.DynatraceConnection{ID: "dt-complete", ProjectID: "proj-a", Name: "SaaS", Enabled: true, CollectionMode: "direct", EnvironmentURL: "https://fixture.live.dynatrace.com", EnvironmentTokenSecret: "environment-token-ref", MetricsEnabled: true, ProblemsEnabled: true, EventsEnabled: true, TopologyMode: "classic"}
	connections := db.(storage.DynatraceConnectionStore)
	if err := connections.CreateDynatraceConnection(c); err != nil {
		t.Fatal(err)
	}
	health := db.(interface {
		UpdateDynatraceConnectionHealthIfRevision(string, string, int64, string, string, dynatrace.Capabilities, time.Time) (bool, error)
	})
	if updated, err := health.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "connected", "", dynatrace.Capabilities{Metrics: true, Problems: true, Events: true, ClassicTopology: true}, time.Now().UTC()); err != nil || !updated {
		t.Fatalf("health %v %v", updated, err)
	}
	metric := dynatrace.MetricSelection{Name: "calls", MetricSelector: "builtin:service.requestCount.total", Aggregation: "sum", Unit: "count"}
	source := storage.MonitorSource{ID: "dt-complete-source", ProjectID: c.ProjectID, Name: "SaaS", Enabled: true, Pack: "dynatrace", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 60, Dynatrace: &dynatrace.Selection{ProjectID: c.ProjectID, Metrics: []dynatrace.MetricSelection{metric}, Problems: true, Events: true, TopologyMode: "classic"}}
	store := db.(providerMonitorStore)
	if _, err := store.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	record := &storage.TestRecord{ID: "dt-complete-run", ProjectID: c.ProjectID, Name: "SaaS run", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}}
	if err := db.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(c.ProjectID, record.ID, "", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	server.runner.projectSecretStore = &devSecretStore{value: "fixture-token"}
	server.dynatraceClient = func(cfg dynatrace.Config, credentials dynatrace.Credentials) (*dynatrace.Client, error) {
		if credentials.EnvironmentToken != "fixture-token" {
			t.Error("environment credential did not resolve in the direct executor")
		}
		return dynatrace.NewClientWithHTTPClient(cfg, credentials, &http.Client{Transport: documentedDynatraceTenant(t, start)})
	}

	server.collectDirectDynatrace(context.Background(), jobs[0])
	collected, err := store.GetRunMonitorJob(c.ProjectID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if collected.QueryWatermark.IsZero() || collected.NextSequence == 0 {
		t.Fatalf("selected coverage never completed; watermark did not advance: %+v", collected.QueryCoverage)
	}
	points, err := loadCanonicalMonitorData(context.Background(), store, c.ProjectID, record.ID, source.ID, "point", "", 100)
	if err != nil || len(points.Points) == 0 {
		t.Fatalf("metric observations %+v %v", points, err)
	}
	evidence, err := loadCanonicalMonitorData(context.Background(), store, c.ProjectID, record.ID, source.ID, "diagnostic", "", 100)
	if err != nil || len(evidence.Evidence) != 2 {
		t.Fatalf("diagnostics %+v %v", evidence, err)
	}
	for _, d := range evidence.Evidence {
		if len(d.EntityIDs) == 0 {
			t.Fatalf("%s lost its documented entity reference: %+v", d.Kind, d)
		}
	}

	// End the load far enough in the past that the final pass is settled.
	ended := time.Now().UTC().Add(-4 * time.Minute)
	record.Status, record.EndTime = "COMPLETED", &ended
	if _, err := db.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	}).SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	reconciling, err := store.GetRunMonitorJob(c.ProjectID, jobs[0].ID)
	if err != nil || reconciling.State != "reconciling" {
		t.Fatalf("reconciling %+v %v", reconciling, err)
	}
	server.collectDirectDynatrace(context.Background(), *reconciling)
	final, err := store.GetRunMonitorJob(c.ProjectID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.StablePasses < 1 || final.FinalDigest == "" || !final.QueryWatermark.Equal(ended) {
		t.Fatalf("settled final pass did not satisfy selected coverage: passes=%d watermark=%s coverage=%+v", final.StablePasses, final.QueryWatermark, final.QueryCoverage)
	}
}

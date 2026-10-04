package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunMonitorCollectorPageProtocol(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "page-collector", Name: "collector", ProjectID: "proj-a", TokenHash: hashAPIKey("page-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"go-runtime"}}); err != nil {
		t.Fatal(err)
	}
	monitors := store.(storage.RunMonitorStore)
	if err := monitors.SaveCollectorRunReadiness("proj-a", "page-collector", []string{"go-runtime"}, nil, now); err != nil {
		t.Fatal(err)
	}
	bindings := []storage.MonitorBinding{}
	for i := 0; i < 20; i++ {
		sourceID := fmt.Sprintf("page-source-%d", i)
		if i == 0 {
			sourceID = strings.Repeat("a", 256)
		}
		source, err := monitors.SaveMonitorSource(storage.MonitorSource{ID: sourceID, Name: "runtime", ProjectID: "proj-a", CollectorID: "page-collector", Pack: "go-runtime", IntervalSeconds: 15, Enabled: true}, 0)
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, storage.MonitorBinding{SourceID: source.ID, Required: true})
	}
	for _, run := range []string{"page-run-a", "page-run-b"} {
		if err := store.SaveTest(&storage.TestRecord{ID: run, Name: run, ProjectID: "proj-a", Status: "RUNNING", StartTime: now, StartedAt: &now, MonitorBindings: &storage.MonitorBindings{Sources: bindings}}); err != nil {
			t.Fatal(err)
		}
		if _, err := monitors.StartRunMonitors("proj-a", run, "", now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/monitoring/collectors/page-collector/jobs"
	longCursor := collectorRequest(t, server, http.MethodGet, path+"?page_size=1", "page-token", nil).Header().Get("X-APEX-Monitor-Next-Key")
	if len(longCursor) <= 256 {
		t.Fatal("fixture did not retain the long provider source identity")
	}
	if response := collectorRequest(t, server, http.MethodGet, path+"?page_size=1&after_key="+url.QueryEscape(longCursor), "page-token", nil); response.Code != 200 {
		t.Fatalf("long job cursor %d %s", response.Code, response.Body.String())
	}
	first := collectorRequest(t, server, http.MethodGet, path+"?page_size=32", "page-token", nil)
	var signed []collectoragent.SignedRunJob
	if first.Code != 200 || first.Header().Get("X-APEX-Monitor-Page") != "1" || first.Header().Get("X-APEX-Monitor-Next-Key") == "" || json.Unmarshal(first.Body.Bytes(), &signed) != nil || len(signed) != 32 {
		t.Fatalf("first page %d %s", first.Code, first.Body.String())
	}
	after := first.Header().Get("X-APEX-Monitor-Next-Key")
	second := collectorRequest(t, server, http.MethodGet, path+"?page_size=32&after_key="+url.QueryEscape(after), "page-token", nil)
	if second.Code != 200 || second.Header().Get("X-APEX-Monitor-Next-Key") != "" || json.Unmarshal(second.Body.Bytes(), &signed) != nil || len(signed) != 8 {
		t.Fatalf("second page %d %s", second.Code, second.Body.String())
	}
	for _, query := range []string{"?page_size=33", "?page_size=0", "?page_size=32&after_key=%0A"} {
		response := collectorRequest(t, server, http.MethodGet, path+query, "page-token", nil)
		if response.Code != 400 {
			t.Fatalf("invalid page %d %s", response.Code, response.Body.String())
		}
	}
	legacy := collectorRequest(t, server, http.MethodGet, path, "page-token", nil)
	if legacy.Code != 200 || json.Unmarshal(legacy.Body.Bytes(), &signed) != nil || len(signed) > 32 {
		t.Fatalf("legacy poll failed for concurrent runs: %d %s", legacy.Code, legacy.Body.String())
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDependencyEvidenceWindowAndCapability(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC().Add(-time.Second)
	if err := store.SaveTest(&storage.TestRecord{ID: "dependency-run", Name: "run", ProjectID: "proj-a", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	point := &LiveMetrics{Timestamp: now, SampleWindowSeconds: 1, DependencyMap: buildLiveDependencyMap(map[string]*TxMetrics{"service": {Count: 5, DependencyType: "grpc", P95Ms: 25}})}
	server.runner.mu.Lock()
	server.runner.tests["dependency-run"] = &ManagedTest{ID: "dependency-run", ProjectID: "proj-a", Status: "RUNNING", LiveMetrics: point, History: []*LiveMetrics{point}}
	server.runner.mu.Unlock()
	path := "/api/v1/tests/dependency-run/dependency-evidence"
	if reply := timeslotRequest(t, server, http.MethodGet, path, nil); reply.Code != 409 {
		t.Fatalf("disabled: %d", reply.Code)
	}
	t.Setenv("APEXLOAD_DIAGNOSTICS_PROJECTS", "proj-a")
	reply := timeslotRequest(t, server, http.MethodGet, path, nil)
	if reply.Code != 200 {
		t.Fatalf("evidence: %d %s", reply.Code, reply.Body.String())
	}
	var evidence DependencyEvidenceResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Graph == nil || len(evidence.Graph.Nodes) != 2 || !evidence.Advisory || evidence.IntervalSeconds != 1 {
		t.Fatalf("evidence: %+v", evidence)
	}
	if reply := timeslotRequest(t, server, http.MethodGet, path+"?at=invalid", nil); reply.Code != 400 {
		t.Fatal("invalid timestamp accepted")
	}
}

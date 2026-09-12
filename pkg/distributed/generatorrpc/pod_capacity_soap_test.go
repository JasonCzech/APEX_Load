package generatorrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
)

func TestPodCapacityConcurrentSOAP(t *testing.T) {
	var first, second atomic.Int64
	response := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><ok>` + strings.Repeat("x", 2048) + `</ok></s:Body></s:Envelope>`
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			first.Add(1)
		} else {
			second.Add(1)
		}
		time.Sleep(3 * time.Millisecond)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(response))
	}))
	defer target.Close()
	s := newIsolatedService(t, 10000, WithExecution())
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{"first", "second"} {
		claim := podClaimFixture(s, id, .5)
		ack, err := s.ReservePodCapacity(context.Background(), claim)
		if err != nil || !ack.Accepted {
			t.Fatalf("reserve %s: %v %+v", id, err, ack)
		}
		script := dsl.Script{Name: id, Protocol: "soap", Config: dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 0}}, Actions: []dsl.Action{{Name: "request", Type: "POST", Target: "/" + id, Body: "<Ping/>", SOAP: &dsl.SOAPActionConfig{Action: "urn:Ping", Operation: "Ping", BodyMode: "generated"}}}}
		body, err := json.Marshal(script)
		if err != nil {
			t.Fatal(err)
		}
		work, err := s.AssignWork(context.Background(), &pb.WorkAssignment{TestId: id, GroupName: "soap", ScriptId: id, ScriptContent: body, VuserCount: 8, TargetDurationSeconds: 120, InitStrategy: "at_once"})
		if err != nil || !work.Accepted {
			t.Fatalf("assign %s: %v %+v", id, err, work)
		}
		commitTestWork(t, s, id, 0)
	}
	duration := 3 * time.Second
	if os.Getenv("APEXLOAD_POD_CAPACITY_SOAK") == "1" {
		duration = 30 * time.Second
	}
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		s.mu.RLock()
		snapshot := PodCapacityFromProto(s.podCapacitySnapshotLocked())
		s.mu.RUnlock()
		if snapshot.ReservedPct != 100 {
			t.Fatalf("peak reservation changed during load: %+v", snapshot)
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		t.Logf("SOAP requests=%d heap_alloc_mb=%.1f heap_sys_mb=%.1f gc=%d reserved=%.0f%%", first.Load()+second.Load(), float64(mem.HeapAlloc)/(1024*1024), float64(mem.HeapSys)/(1024*1024), mem.NumGC, snapshot.ReservedPct)
	}
	if first.Load() == 0 || second.Load() == 0 {
		t.Fatalf("both runs must emit SOAP traffic: first=%d second=%d", first.Load(), second.Load())
	}
	paused, err := s.SetPaused(context.Background(), &pb.PauseRequest{TestId: "first", Pause: true, OperationId: "pause", OperationSequence: 1})
	if err != nil || !paused.Accepted {
		t.Fatalf("pause: %v %+v", err, paused)
	}
	s.mu.RLock()
	snapshot := PodCapacityFromProto(s.podCapacitySnapshotLocked())
	s.mu.RUnlock()
	if snapshot.ReservedPct != 100 {
		t.Fatal("pause released peak capacity")
	}
}

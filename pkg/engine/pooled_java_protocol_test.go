package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	javaadapter "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

// Exercise the actual managed HTTP client and pooled protocol through VUser's
// result/parameter/control paths. The peer supplies wire fixtures, not a JVM;
// this test does not claim process containment or delegated-Linux qualification.
func TestPooledJavaProtocolThroughVUser(t *testing.T) {
	for _, option := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(option), func(t *testing.T) {
			d := javasidecar.PooledControlDecision{TargetClass: "Actions", InvocationEnabled: true, PreparationEnabled: true, CommitEnabled: true, Mode: "pooled_pilot", ProfileID: "pilot", ResourceProfile: "resource", ConfigurationSHA256: strings.Repeat("a", 64), ContainmentBackend: "delegated_linux", SlotsPerHost: 2, HostMemoryBytes: 1024, MaxHosts: 1, MaxVUsers: 2, MaxMemoryBytes: 1024}
			var mu sync.Mutex
			var calls []javasidecar.PooledControlRequest
			deadline := time.Now().Add(time.Minute).UnixMilli()
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				write := func(value any) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(value)
				}
				if r.URL.Path == "/v1/owner/claim" {
					write(javasidecar.ClaimOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: "owner", Epoch: "epoch", Claimed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()})
					return
				}
				var q javasidecar.PooledControlRequest
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				response := javasidecar.PooledControlResponse{Contract: javasidecar.ContractVersion, PooledContract: javasidecar.PooledControlContract, Operation: q.Operation, RequestID: q.RequestID, RunID: "run", LeaseTTLMS: javasidecar.RuntimeLeaseTTL.Milliseconds(), Decision: d}
				switch q.Operation {
				case "claim":
					response.RunToken = strings.Repeat("b", 64)
				case "reserve":
					response.ReservationID = "reservation"
					response.PrepareDeadlineUnixMS = deadline
					response.Placements = []javasidecar.PooledPlacement{{HostID: "host", SlotIDs: []string{"slot"}, MemoryBytes: 1024}}
				case "prepare":
					response.ReservationID = q.ReservationID
					response.Ready = true
					response.PrepareDeadlineUnixMS = deadline
				case "commit":
					response.ReservationID = q.ReservationID
					response.Committed = true
				case "invoke":
					mu.Lock()
					calls = append(calls, q)
					mu.Unlock()
					response.ReservationID, response.HostID, response.SlotID = q.ReservationID, q.HostID, q.SlotID
					id := strconv.FormatUint(q.Sequence, 10)
					response.Invocation = &javasidecar.PooledInvocationResult{Sequence: q.Sequence, Method: q.Method, EventRequestID: id}
					if q.Method == "action" {
						now := time.Now().UnixMilli()
						response.Invocation.Events = fmt.Sprintf(`{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":%q,"sequence":1,"type":"transaction_start","name":"order","transaction_id":"tx","timestamp_ms":%d}`+"\n"+`{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":%q,"sequence":2,"type":"transaction_end","name":"order","transaction_id":"tx","timestamp_ms":%d,"duration_ms":7,"raw_duration_ms":10,"think_time_ms":3,"message":"0"}`+"\n", id, now, id, now+10)
						if option != 0 {
							response.Invocation.Exit = &javasidecar.PooledExit{Option: option, Status: 1}
						}
					}
				case "close_slot":
					response.ReservationID, response.HostID, response.SlotID = q.ReservationID, q.HostID, q.SlotID
					response.SlotClosed = true
				case "close":
					response.Reaped = true
				case "renew":
				default:
					t.Errorf("unexpected operation %q", q.Operation)
				}
				write(response)
			}))
			defer h.Close()
			c, err := javasidecar.NewClient(h.URL, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.ClaimOwner(context.Background(), "owner"); err != nil {
				t.Fatal(err)
			}
			run, err := c.OpenPooledClientRun(context.Background(), javasidecar.PooledClientOptions{ExecutionID: "execution", GroupID: "group", Decision: d})
			if err != nil {
				t.Fatal(err)
			}
			defer run.Close(context.Background())
			slots, err := run.Prepare(context.Background(), []javasidecar.PooledSlotContext{{VUserID: 1}}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := javaadapter.NewPooledSlotProtocol(slots[0], time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := adapter.Init(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if err := run.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			action := func(name, method string) dsl.Action {
				return dsl.Action{Name: name, Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method}}
			}
			script := &dsl.Script{Name: "pooled", Protocol: "java_vuser", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 2, AutoTransactions: "per_iteration"}, ErrorHandling: dsl.ErrorHandlingConfig{MaxRetries: 3, ContinueOnError: dsl.ContinueOnErrorValue(false)}}, Parameters: []dsl.Parameter{{Name: "Iteration", Type: "current_iteration", Update: "each_iteration", Start: 1, Step: 1}}, Init: []dsl.Action{action("init", "init")}, Actions: []dsl.Action{{Name: "nested", Type: "control_loop", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{action("first", "action"), action("nested-after", "action")}}}, action("after", "action")}, Teardown: []dsl.Action{action("end", "end")}}
			sink := &sequenceCaptureSink{}
			vu := &VUser{ID: 0, TotalVUsers: 1, Group: "java", Proto: adapter, Script: script, Metrics: NewMetricsCollector(128), ResultSink: sink, Variables: map[string]string{}}
			vu.Run(context.Background())
			if err := adapter.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actual := append([]javasidecar.PooledControlRequest(nil), calls...)
			mu.Unlock()
			expected := 8 // init + three actions per iteration + end
			if option == 2 {
				expected = 4
			}
			if len(actual) != expected || actual[0].Method != "init" || actual[len(actual)-1].Method != "end" {
				t.Fatalf("lifecycle/continuation calls=%+v", actual)
			}
			for i, q := range actual {
				if q.Sequence != uint64(i+1) {
					t.Fatal("invocation replay", q.Sequence)
				}
			}
			actionCalls := 0
			for _, q := range actual {
				if q.Method == "action" {
					actionCalls++
					iteration := "1"
					if actionCalls > (expected-2)/2 {
						iteration = "2"
					}
					if q.Params["Iteration"] != iteration {
						t.Fatal("parameter binding changed", q.Params, iteration)
					}
				}
			}
			children, exitFailures, iterations := 0, 0, 0
			for _, r := range sink.results {
				if r.SampleKind == "child" && r.BusinessTransaction && r.ActionName == "order" {
					children++
					if r.Duration != 7*time.Millisecond || r.ProtocolEvidence["runner_slot_id"] != "slot" {
						t.Fatal("transaction timing/attribution", r)
					}
				}
				if r.ActionName == "lr.exit" {
					exitFailures++
					if r.Success {
						t.Fatal("exit status lost")
					}
				}

			}
			for _, r := range sink.transactions {
				if r.ActionName == vu.iterationTransactionName() {
					iterations++
					if r.Success != (option == 0) {
						t.Fatal("iteration status lost", r)
					}
				}
			}
			if children != expected-2 || iterations != 2 || (option != 0 && exitFailures != expected-2) {
				t.Fatal("incorrect metrics", children, exitFailures, iterations)
			}
		})
	}
}

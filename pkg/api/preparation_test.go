package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPreparationLocalZeroTrafficUntilStart(t *testing.T) {
	for _, action := range []string{"start", "renew", "cancel", "cancel_disabled", "revision"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
			var requests, connections atomic.Int64
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
			target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			target.Start()
			defer target.Close()
			server, durable := newTimeslotServer(t)
			backend := &joinedLocalExecutionBackend{done: make(chan struct{})}
			WithExecutionBackend(backend)(server.runner)
			script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "prepare-script", Name: "prepare", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: target.URL}, Init: []dsl.Action{{Name: "init", Type: "request", Method: "GET", Target: "/init"}}, Actions: []dsl.Action{{Name: "load", Type: "request", Method: "GET", Target: "/load"}}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := server.runner.CreateTest(CreateTestRequest{Name: "prepare", ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/tests/" + run.ID + "/preparation"
			reply := timeslotRequest(t, server, "POST", path, nil)
			if reply.Code != 202 {
				t.Fatalf("prepare: %d %s", reply.Code, reply.Body.String())
			}
			t.Cleanup(func() { _ = server.runner.StopTest(run.ID); backend.finish(t, server.runner) })
			var p storage.Preparation
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				r := timeslotRequest(t, server, "GET", path, nil)
				if r.Code != 200 {
					t.Fatalf("get: %s", r.Body.String())
				}
				if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				if p.State == "ready" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if p.State != "ready" {
				current, _ := server.runner.GetTest(run.ID)
				t.Fatalf("never ready: %+v run=%+v", p, current)
			}
			time.Sleep(100 * time.Millisecond)
			if requests.Load() != 0 || connections.Load() != 0 {
				t.Fatalf("prepare contacted target: requests=%d connections=%d", requests.Load(), connections.Load())
			}
			opts := StartTestOptions{PreparationID: p.ID, ExpectedPreparationRevision: p.ExecutionRevision}
			switch action {
			case "start", "renew":
				if action == "renew" {
					old := p
					response := timeslotRequest(t, server, "POST", path, map[string]any{"preparation_id": p.ID, "revision": p.Revision, "ttl_seconds": 3600})
					if response.Code != 200 {
						t.Fatalf("renew: %d %s", response.Code, response.Body.String())
					}
					if err := json.Unmarshal(response.Body.Bytes(), &p); err != nil {
						t.Fatal(err)
					}
					if !p.ExpiresAt.After(old.ExpiresAt) || p.Revision != old.Revision+1 || requests.Load() != 0 || connections.Load() != 0 {
						t.Fatalf("renewal: %+v", p)
					}
					if r := timeslotRequest(t, server, "POST", path, map[string]any{"preparation_id": p.ID, "revision": old.Revision, "ttl_seconds": 3600}); r.Code != 409 {
						t.Fatal("stale renewal accepted")
					}
				}
				r := timeslotRequest(t, server, "POST", "/api/v1/tests/"+run.ID+"/start", opts)
				if r.Code != 200 {
					t.Fatalf("start: %d %s", r.Code, r.Body.String())
				}
				if err := server.runner.StartTestWithOptions(run.ID, opts); err == nil {
					t.Fatal("preparation consumed twice")
				}
				backend.wait(t)
				if requests.Load() < 2 || connections.Load() < 1 {
					t.Fatal("start did not execute init and load")
				}
			case "cancel", "cancel_disabled":
				if action == "cancel_disabled" {
					t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "")
					if r := timeslotRequest(t, server, "GET", path, nil); r.Code != 200 {
						t.Fatalf("disabled project hid preparation: %d %s", r.Code, r.Body.String())
					}
					if r := timeslotRequest(t, server, "POST", path, nil); r.Code != 409 {
						t.Fatalf("disabled project allowed new preparation: %d", r.Code)
					}
				}
				r := timeslotRequest(t, server, "DELETE", path, nil)
				if r.Code != 200 {
					t.Fatalf("cancel: %d %s", r.Code, r.Body.String())
				}
				if err := server.runner.StartTestWithOptions(run.ID, opts); err == nil {
					t.Fatal("cancelled preparation consumed")
				}
				backend.wait(t)
			case "revision":
				server.runner.mu.Lock()
				server.runner.tests[run.ID].DurationStr = "2s"
				server.runner.mu.Unlock()
				if err := server.runner.StartTestWithOptions(run.ID, opts); err == nil {
					t.Fatal("changed revision consumed")
				}
				backend.wait(t)
			}
			stored, err := durable.(storage.PreparationStore).GetPreparation(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (action == "start" || action == "renew") && stored.State != "consumed" {
				t.Fatalf("state=%s", stored.State)
			}
			if action != "start" && action != "renew" && (requests.Load() != 0 || connections.Load() != 0) {
				t.Fatal("cancelled or stale preparation contacted target")
			}
		})
	}
}

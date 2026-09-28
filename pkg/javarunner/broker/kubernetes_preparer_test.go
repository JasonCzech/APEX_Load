package broker

import (
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestKubernetesWaveUsesDurablePodThenTLSAndHarnessBarriers(t *testing.T) {
	s, epoch, _ := newStore(t)
	var creates, observes, invokes atomic.Int32
	var saved map[string]any
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "POST":
			creates.Add(1)
			if e := json.NewDecoder(req.Body).Decode(&saved); e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			saved["metadata"].(map[string]any)["uid"] = "uid-1"
			w.WriteHeader(201)
		case "GET":
			observes.Add(1)
			saved["status"] = map[string]any{"containerStatuses": []any{map[string]string{"name": "java-host", "containerID": "containerd://test"}}}
		default:
			t.Error("unexpected method", req.Method)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(saved)
	}, &testAttestor{})
	p := &KubernetesHostPreparer{Backend: k, Store: s, Epoch: epoch, PollInterval: time.Millisecond,
		PodConfig: func(_ context.Context, r Record) (ExecutionPodConfig, error) {
			cfg := podConfig()
			cfg.Context = &javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: r.SessionID, VUserID: 1, GroupName: "group", ScenarioID: "scenario"}
			return cfg, nil
		},
		Connect: func(ctx context.Context, r Record, receipt PodReceipt) (*SupervisorClient, error) {
			bound := record(t, s, epoch, r.ID)
			if bound.PodUID != receipt.UID || bound.ContainerID != receipt.ContainerID || bound.PodPolicySHA256 == "" {
				t.Error("connect preceded durable receipt")
			}
			cfg := controlConfig()
			cfg.HostID = r.ID
			cfg.BrokerEpoch = epoch
			cfg.ConfigurationSHA256 = r.Binding.ConfigurationSHA256
			c, e := NewSupervisorControl(t.Context(), cfg, &countedWaveRuntime{&invokes})
			if e != nil {
				return nil, e
			}
			t.Cleanup(c.Close)
			client, _ := supervisorClientFixture(t, cfg, c)
			return client, nil
		},
	}
	wave, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"vuser-1"}, 100, time.Second, time.Second, p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = wave.Abort("test_complete") })
	if e = wave.Prepare(t.Context()); e != nil {
		t.Fatal(e)
	}
	if invokes.Load() != 0 || creates.Load() != 1 || observes.Load() != 1 {
		t.Fatal(creates.Load(), observes.Load(), invokes.Load())
	}
	if e = wave.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if invokes.Load() != 0 {
		t.Fatal("commit initialized user")
	}
	if _, e = wave.Invoke(t.Context(), wave.Records()[0].ID, "init", nil); e != nil {
		t.Fatal(e)
	}
	if invokes.Load() != 1 || creates.Load() != 1 {
		t.Fatal("unexpected replay")
	}
}

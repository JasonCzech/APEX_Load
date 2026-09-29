package broker

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func TestBrokerCapabilitiesAndPolicyDrift(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	out, status := f.request(t, BrokerRequest{Operation: "capabilities"})
	if status != 200 || !validCapabilities(out.Capabilities) || len(out.Capabilities.Profiles) != 1 || out.Capabilities.Profiles[0].ID != "approved" {
		t.Fatal(status, out)
	}
	p := out.Capabilities.Profiles[0]
	if p.MaxRunMS != (time.Hour - 12*time.Second).Milliseconds() {
		t.Fatal("certificate budget", p)
	}
	owner := f.claim(t)
	q := owner
	q.Operation = "reserve"
	q.ExecutionID = "run"
	q.GroupID = "group"
	q.Profile = p.ID
	q.ConfigurationSHA256 = p.ConfigurationSHA256
	q.PolicySHA256 = strings.Repeat("b", 64)
	q.RunDurationMS = 1000
	q.Sessions = []javarunner.DedicatedHostContext{managedContext()}
	if _, status = f.request(t, q); status != 409 {
		t.Fatal("changed policy reserved", status)
	}
	records, _, e := f.store.Snapshot(t.Context(), f.epoch)
	if e != nil || len(records) != 0 {
		t.Fatal("rejected policy consumed durable capacity", e)
	}
	q.PolicySHA256 = p.PolicySHA256
	q.RunDurationMS = p.MaxRunMS + 1
	if _, status = f.request(t, q); status != 409 {
		t.Fatal("transport lifetime exceeded", status)
	}
}
func TestBrokerAbsoluteRunDeadlineAndCommitReplay(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	owner := f.claim(t)
	w := f.wave(t, owner)
	q := owner
	q.Operation = "commit"
	q.WaveID = w.WaveID
	if _, status := f.request(t, q); status != 200 {
		t.Fatal(status)
	}
	f.service.mu.Lock()
	o := f.service.owners["generator"]
	f.service.mu.Unlock()
	o.mu.Lock()
	wave := o.waves[w.WaveID]
	o.mu.Unlock()
	wave.mu.Lock()
	deadline := wave.deadline
	wave.mu.Unlock()
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("commit replay extended run")
	}
	wave.mu.Lock()
	unchanged := wave.deadline.Equal(deadline)
	wave.deadline = time.Now().Add(-time.Second)
	wave.mu.Unlock()
	if !unchanged {
		t.Fatal("commit replay changed deadline")
	}
	q.Operation = "renew_host"
	q.HostID = w.HostID
	q.RenewSequence = 1
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("renewed expired run")
	}
	q.Operation = "invoke"
	q.RenewSequence = 0
	q.Sequence = 1
	q.Method = "init"
	if _, status := f.request(t, q); status != 409 || f.calls.Load() != 0 {
		t.Fatal("expired run executed", status)
	}
}

func TestBrokerRunDeadlineCancelsInFlightInvocation(t *testing.T) {
	entered := make(chan struct{})
	f := newBrokerServiceFixture(t, func(s *Store, epoch string, calls *atomic.Int32) func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
		return func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
			return hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) {
				if e := s.BeginPrepare(ctx, epoch, r.ID); e != nil {
					return nil, e
				}
				if e := s.BindPod(ctx, epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
					return nil, e
				}
				cfg := controlConfig()
				cfg.HostID = r.ID
				cfg.BrokerEpoch = epoch
				cfg.PreparationTimeout = 10 * time.Second
				control := newControl(t, cfg, &controlRuntime{invoke: func(ctx context.Context, _, _ string, _ map[string]string) ([]byte, error) {
					close(entered)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(150 * time.Millisecond):
						return []byte(`{"ok":true}`), nil
					}
				}})
				c, _ := supervisorClientFixture(t, cfg, control)
				return c, nil
			}), nil
		}
	})
	owner := f.claim(t)
	w := f.wave(t, owner)
	q := owner
	q.Operation = "commit"
	q.WaveID = w.WaveID
	if _, status := f.request(t, q); status != 200 {
		t.Fatal(status)
	}
	f.service.mu.Lock()
	o := f.service.owners["generator"]
	f.service.mu.Unlock()
	o.mu.Lock()
	wave := o.waves[w.WaveID]
	o.mu.Unlock()
	wave.mu.Lock()
	wave.deadline = time.Now().Add(40 * time.Millisecond)
	wave.mu.Unlock()
	q.Operation = "invoke"
	q.HostID = w.HostID
	q.Method = "init"
	q.Sequence = 1
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("late invocation result accepted", status)
	}
	select {
	case <-entered:
	default:
		t.Fatal("did not exercise in-flight expiry")
	}
}

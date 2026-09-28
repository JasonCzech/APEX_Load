package broker

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"testing"
	"time"
)

func attachGroupHost(t *testing.T, g *DedicatedGroup, s *Store, epoch string, r Record) (*SupervisorControl, *controlRuntime) {
	t.Helper()
	if e := s.BeginPrepare(t.Context(), epoch, r.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.BindPod(t.Context(), epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
		t.Fatal(e)
	}
	runtime := &controlRuntime{}
	cfg := controlConfig()
	cfg.HostID = r.ID
	cfg.BrokerEpoch = epoch
	cfg.ConfigurationSHA256 = r.Binding.ConfigurationSHA256
	cfg.LeaseTTL = 10 * time.Second
	cfg.PreparationTimeout = 10 * time.Second
	c := newControl(t, cfg, runtime)
	waitReady(t, c)
	client, _ := supervisorClientFixture(t, cfg, c)
	if e := g.Attach(t.Context(), r.ID, client); e != nil {
		t.Fatal(e)
	}
	if ready, e := g.PollReady(t.Context(), r.ID); e != nil || !ready {
		t.Fatal(ready, e)
	}
	return c, runtime
}
func TestDedicatedGroupAllHostCommitAndNoEarlyUserCode(t *testing.T) {
	s, epoch, _ := newStore(t)
	a, b := reserve(t, s, epoch), reserve(t, s, epoch)
	g, e := NewDedicatedGroup(s, epoch, binding())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = g.Abort("test_complete") })
	_, ra := attachGroupHost(t, g, s, epoch, a)
	_, rb := attachGroupHost(t, g, s, epoch, b)
	if _, e := g.Invoke(t.Context(), a.ID, "init", nil); !errors.Is(e, ErrFence) {
		t.Fatal("precommit invocation", e)
	}
	if e := g.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if ra.calls.Load() != 0 || rb.calls.Load() != 0 {
		t.Fatal("commit ran user code")
	}
	if _, e := g.Invoke(t.Context(), a.ID, "init", nil); e != nil {
		t.Fatal(e)
	}
	if e := g.Renew(t.Context(), 10*time.Second); e != nil {
		t.Fatal(e)
	}
	if _, e := g.Invoke(t.Context(), a.ID, "end", nil); e != nil {
		t.Fatal(e)
	}
	_, usage, e := s.Snapshot(t.Context(), epoch)
	if e != nil || usage.Hosts != 2 || usage.MemoryBytes != 200 || usage.VUsers != 2 {
		t.Fatal("close released unproven pod", usage, e)
	}
	if _, e := g.Invoke(t.Context(), a.ID, "action", nil); !errors.Is(e, ErrFence) {
		t.Fatal("closed vuser restarted", e)
	}
	if r := record(t, s, epoch, a.ID); r.State != javarunner.HostQuarantined {
		t.Fatal(r)
	}
}
func TestDedicatedGroupPartialCommitNeverInitializesOrReleases(t *testing.T) {
	for _, failure := range []string{"peer_retired", "unattached_reservation"} {
		t.Run(failure, func(t *testing.T) {
			s, epoch, _ := newStore(t)
			a, b := reserve(t, s, epoch), reserve(t, s, epoch)
			g, _ := NewDedicatedGroup(s, epoch, binding())
			ca, ra := attachGroupHost(t, g, s, epoch, a)
			var rb *controlRuntime
			if failure == "peer_retired" {
				cb, runtime := attachGroupHost(t, g, s, epoch, b)
				rb = runtime
				cb.Close()
			}
			if e := g.Commit(t.Context()); e == nil {
				t.Fatal("partial barrier accepted")
			}
			if ra.calls.Load() != 0 || rb != nil && rb.calls.Load() != 0 {
				t.Fatal("failed commit ran user code")
			}
			select {
			case <-ca.Done():
			default:
				t.Fatal("partially committed host remained live")
			}
			_, usage, e := s.Snapshot(t.Context(), epoch)
			wantHosts := 2
			if failure == "unattached_reservation" {
				wantHosts = 1
			}
			if e != nil || usage.Hosts != wantHosts {
				t.Fatal("unproven capacity released", usage, e)
			}
			if _, e := g.Invoke(t.Context(), a.ID, "init", nil); !errors.Is(e, ErrFence) {
				t.Fatal(e)
			}
		})
	}
}
func TestDedicatedGroupBrokerRestartFencesInvocations(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	g, _ := NewDedicatedGroup(s, epoch, binding())
	c, runtime := attachGroupHost(t, g, s, epoch, r)
	if e := g.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	next, e := s.StartEpoch(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e := g.Invoke(context.Background(), r.ID, "init", nil); !errors.Is(e, ErrFence) {
		t.Fatal(e)
	}
	if runtime.calls.Load() != 0 {
		t.Fatal("old broker invoked")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("old broker host not closed")
	}
	_, usage, e := s.Snapshot(t.Context(), next)
	if e != nil || usage.Hosts != 1 {
		t.Fatal("restart released live reservation", usage, e)
	}
}

func TestDedicatedGroupRenewSurvivorAfterPeerClosed(t *testing.T) {
	s, epoch, _ := newStore(t)
	a, b := reserve(t, s, epoch), reserve(t, s, epoch)
	g, _ := NewDedicatedGroup(s, epoch, binding())
	t.Cleanup(func() { _ = g.Abort("test_complete") })
	_, ra := attachGroupHost(t, g, s, epoch, a)
	_, rb := attachGroupHost(t, g, s, epoch, b)
	if e := g.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{a.ID, b.ID} {
		if _, e := g.Invoke(t.Context(), id, "init", nil); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := g.Invoke(t.Context(), a.ID, "end", nil); e != nil {
		t.Fatal(e)
	}
	if e := g.Renew(t.Context(), 10*time.Second); e != nil {
		t.Fatal("closed peer prevented renewal", e)
	}
	if _, e := g.Invoke(t.Context(), b.ID, "action", nil); e != nil {
		t.Fatal(e)
	}
	if ra.calls.Load() != 2 || rb.calls.Load() != 2 {
		t.Fatal("unexpected replay", ra.calls.Load(), rb.calls.Load())
	}
}

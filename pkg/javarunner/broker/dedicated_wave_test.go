package broker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

type hostPrepareFunc func(context.Context, Record) (*SupervisorClient, error)

func (f hostPrepareFunc) PrepareHost(ctx context.Context, r Record) (*SupervisorClient, error) {
	return f(ctx, r)
}

func waveHostFactory(t *testing.T, s *Store, epoch string, calls *atomic.Int32) hostPrepareFunc {
	t.Helper()
	return func(ctx context.Context, r Record) (*SupervisorClient, error) {
		if e := s.BeginPrepare(ctx, epoch, r.ID); e != nil {
			return nil, e
		}
		if e := s.BindPod(ctx, epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
			return nil, e
		}
		cfg := controlConfig()
		cfg.HostID = r.ID
		cfg.BrokerEpoch = epoch
		cfg.ConfigurationSHA256 = r.Binding.ConfigurationSHA256
		cfg.LeaseTTL = 10 * time.Second
		cfg.PreparationTimeout = 10 * time.Second
		runtime := &countedWaveRuntime{calls: calls}
		c, e := NewSupervisorControl(t.Context(), cfg, runtime)
		if e != nil {
			return nil, e
		}
		t.Cleanup(c.Close)
		client, _ := supervisorClientFixture(t, cfg, c)
		return client, nil
	}
}

type countedWaveRuntime struct{ calls *atomic.Int32 }

func (r *countedWaveRuntime) Ready(context.Context) error { return nil }
func (r *countedWaveRuntime) Invoke(context.Context, string, string, map[string]string) ([]byte, error) {
	r.calls.Add(1)
	return []byte(`{"ok":true}`), nil
}
func TestDedicatedWavePreparationBarrierAndPersistentSessions(t *testing.T) {
	s, epoch, _ := newStore(t)
	var calls atomic.Int32
	w, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"vuser-1", "vuser-2"}, 100, 10*time.Second, 10*time.Second, waveHostFactory(t, s, epoch, &calls))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = w.Abort("test_complete") })
	records := w.Records()
	records[0].ID = "mutated"
	if w.Records()[0].ID == "mutated" {
		t.Fatal("mutable inventory")
	}
	if e = w.Prepare(t.Context()); e != nil {
		t.Fatal(e)
	}
	for _, r := range w.Records() {
		if _, e = w.Invoke(t.Context(), r.ID, "init", nil); !errors.Is(e, ErrFence) {
			t.Fatal("early initialization", e)
		}
	}
	if e = w.Renew(t.Context(), 10*time.Second); e != nil {
		t.Fatal(e)
	}
	if e = w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("prewarm ran user code")
	}
	for _, r := range w.Records() {
		if _, e = w.Invoke(t.Context(), r.ID, "init", nil); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	if e = w.Prepare(t.Context()); !errors.Is(e, ErrState) {
		t.Fatal("repeated preparation", e)
	}
	if e = w.Commit(t.Context()); !errors.Is(e, ErrState) {
		t.Fatal("repeated commit", e)
	}
}
func TestDedicatedWaveCancelledPreparationKeepsCreatedCharge(t *testing.T) {
	s, epoch, _ := newStore(t)
	entered := make(chan struct{})
	factory := hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) {
		if e := s.BeginPrepare(ctx, epoch, r.ID); e != nil {
			return nil, e
		}
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	w, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"a"}, 100, time.Second, time.Second, factory)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Prepare(ctx) }()
	<-entered
	cancel()
	if e = <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	r := record(t, s, epoch, w.Records()[0].ID)
	if r.State != javarunner.HostQuarantined {
		t.Fatal(r)
	}
	_, u, e := s.Snapshot(t.Context(), epoch)
	if e != nil || u.Hosts != 1 || u.MemoryBytes != 100 {
		t.Fatal(u, e)
	}
	if e = w.Commit(t.Context()); e == nil {
		t.Fatal("canceled preparation committed")
	}
}
func TestDedicatedWaveDeadlineIndependentOfCallerAndRenewal(t *testing.T) {
	s, epoch, _ := newStore(t)
	factory := hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) { <-ctx.Done(); return nil, ctx.Err() })
	w, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"a"}, 100, time.Second, 40*time.Millisecond, factory)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- w.Prepare(t.Context()) }()
	// Renewing owner authority cannot move the fixed preparation deadline.
	if e = w.Renew(t.Context(), time.Second); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("preparation deadline ignored")
	}
	if r := record(t, s, epoch, w.Records()[0].ID); r.State != javarunner.HostReaped {
		t.Fatal("uncreated reservation leaked", r)
	}
}
func TestDedicatedWaveDelayedAbortDoesNotCaptureLaterWave(t *testing.T) {
	s, epoch, _ := newStore(t)
	factory := hostPrepareFunc(func(context.Context, Record) (*SupervisorClient, error) { return nil, ErrState })
	first, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"a"}, 100, time.Second, time.Second, factory)
	if e != nil {
		t.Fatal(e)
	}
	if e = first.Abort("first_aborted"); e != nil {
		t.Fatal(e)
	}
	second, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"b"}, 100, time.Second, time.Second, factory)
	if e != nil {
		t.Fatal(e)
	}
	if e = first.Abort("delayed_abort"); e != nil {
		t.Fatal(e)
	}
	if r := record(t, s, epoch, second.Records()[0].ID); r.State != javarunner.HostReserved {
		t.Fatal("later wave captured", r)
	}
}

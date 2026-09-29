package broker

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func TestReserveDedicatedWaveAtomicCapacityAndSessionTombstone(t *testing.T) {
	s, epoch, _ := newStore(t)
	for _, tc := range []struct {
		ids    []string
		memory int64
		want   error
	}{
		{nil, 100, ErrState}, {[]string{"a", "a"}, 100, ErrState}, {[]string{"bad/id"}, 100, ErrState}, {[]string{"a", "b"}, math.MaxInt64, ErrCapacity},
	} {
		if _, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), tc.ids, tc.memory, time.Second, time.Second); !errors.Is(e, tc.want) {
			t.Fatal(e)
		}
		rs, u, e := s.Snapshot(t.Context(), epoch)
		if e != nil || len(rs) != 0 || u.Hosts != 0 {
			t.Fatal(rs, u, e)
		}
	}
	rs, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"a", "b"}, 100, time.Second, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"c"}, 100, time.Second, time.Second); !errors.Is(e, ErrState) {
		t.Fatal("overlapping wave", e)
	}
	for _, r := range rs {
		if e = s.Abort(t.Context(), epoch, r.ID, "test_abort"); e != nil {
			t.Fatal(e)
		}
	}
	// Never-provisioned charges release, but identities remain consumed.
	_, u, e := s.Snapshot(t.Context(), epoch)
	if e != nil || u.Hosts != 0 {
		t.Fatal(u, e)
	}
	next, e := s.StartEpoch(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	b := binding()
	b.OwnerEpoch = "new-epoch"
	b.OwnerID = "different-owner"
	if _, e = s.ReserveDedicatedWave(t.Context(), next, b, []string{"a"}, 100, time.Second, time.Second); !errors.Is(e, ErrState) {
		t.Fatal("replayed session after restart", e)
	}
	if _, e = s.ReserveDedicatedWave(t.Context(), next, b, []string{"c"}, 100, time.Second, time.Second); e != nil {
		t.Fatal(e)
	}
}
func TestReserveDedicatedWaveConcurrentAdmission(t *testing.T) {
	s, epoch, _ := newStore(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"a", "b"}, 100, time.Second, time.Second)
			results <- e
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrState) {
			t.Fatal(e)
		}
	}
	rs, u, e := s.Snapshot(t.Context(), epoch)
	if e != nil || successes != 1 || len(rs) != 2 || u.Hosts != 2 || u.VUsers != 2 || u.MemoryBytes != 200 {
		t.Fatal(successes, rs, u, e)
	}
}
func TestReserveDedicatedWavePreservesCommittedIncrease(t *testing.T) {
	s, epoch, _ := newStore(t)
	rs, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"a"}, 100, time.Second, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	r := rs[0]
	for _, e := range []error{s.BeginPrepare(t.Context(), epoch, r.ID), s.BindPod(t.Context(), epoch, r.ID, "uid", "containerd://id"), s.Ready(t.Context(), epoch, r.ID), s.Commit(t.Context(), epoch, []string{r.ID})} {
		if e != nil {
			t.Fatal(e)
		}
	}
	next, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"b"}, 100, time.Second, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AbortWave(t.Context(), epoch, binding(), []string{next[0].ID}, "wave_failed"); e != nil {
		t.Fatal(e)
	}
	if got := record(t, s, epoch, r.ID); got.State != javarunner.HostCommitted {
		t.Fatal(got)
	}
}

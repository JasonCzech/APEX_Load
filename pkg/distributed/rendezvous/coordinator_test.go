package rendezvous

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func coordinatorFor(t *testing.T, target int, save SaveFunc) *Coordinator {
	t.Helper()
	c, err := New([]MemberRange{{First: 10, Count: 2}, {First: 100, Count: 2}}, []Contract{{Key: "checkout", Target: target, TimeoutMS: 1000}}, save)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDistinctArrivalsDurableRestoreAndLateOutcome(t *testing.T) {
	var saved Snapshot
	c := coordinatorFor(t, 4, func(_ context.Context, previous uint64, next Snapshot) error {
		if next.Revision != previous+1 {
			t.Fatal("revision mismatch")
		}
		saved = next
		return nil
	})
	ctx := context.Background()
	out, err := c.Exchange(ctx, Batch{Key: "checkout", Round: 1, Arrivals: []uint64{10, 10, 100}})
	if err != nil || out.State != "waiting" || out.Arrived != 2 {
		t.Fatalf("duplicate arrival: %+v %v", out, err)
	}
	restored, err := Restore(saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err = restored.Exchange(ctx, Batch{Key: "checkout", Round: 1, Arrivals: []uint64{11, 101}})
	if err != nil || out.State != "released" || out.Arrived != 4 {
		t.Fatalf("restored barrier: %+v %v", out, err)
	}
	late, err := restored.Exchange(ctx, Batch{Key: "checkout", Round: 1, Arrivals: []uint64{10}})
	if err != nil || late != out {
		t.Fatalf("late arrival: %+v %v", late, err)
	}
	if _, err = restored.Exchange(ctx, Batch{Key: "checkout", Round: 1, Arrivals: []uint64{99}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("forged participant accepted")
	}
}

func TestTimeoutDoesNotShrinkAfterMemberLoss(t *testing.T) {
	c := coordinatorFor(t, 4, nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	out, err := c.Exchange(context.Background(), Batch{Key: "checkout", Round: 1, Arrivals: []uint64{10, 11}})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	out, err = c.Exchange(context.Background(), Batch{Key: "checkout", Round: 1, Arrivals: []uint64{100}})
	if err != nil || out.State != "timed_out" || out.Target != 4 || out.Arrived != 2 {
		t.Fatalf("lost member changed contract: %+v %v", out, err)
	}
}

func TestCommitFailureFreezesServingWithoutPublishing(t *testing.T) {
	c := coordinatorFor(t, 1, func(context.Context, uint64, Snapshot) error { return errors.New("uncertain database commit") })
	if _, err := c.Exchange(context.Background(), Batch{Key: "checkout", Round: 1, Arrivals: []uint64{10}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if c.Snapshot().Revision != 0 {
		t.Fatal("uncommitted release published")
	}
	if _, err := c.Exchange(context.Background(), Batch{Key: "checkout", Round: 1, Arrivals: []uint64{11}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("serving after uncertain commit")
	}
}

func TestRetiredRoundCannotBecomeNewBarrier(t *testing.T) {
	c := coordinatorFor(t, 1, nil)
	for i := uint64(1); i < 100; i++ {
		if _, err := c.Exchange(context.Background(), Batch{Key: "checkout", Round: i, Arrivals: []uint64{10}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.Snapshot().Rounds) > RetainedRounds {
		t.Fatal("unbounded history")
	}
	if _, err := c.Exchange(context.Background(), Batch{Key: "checkout", Round: 1, Arrivals: []uint64{10}}); !errors.Is(err, ErrRetired) {
		t.Fatal("retired round re-executed")
	}
}

func TestTwoGeneratorAggregationBeyondTableChannelCapacity(t *testing.T) {
	const population = 512
	c, err := New([]MemberRange{{First: 0, Count: population}}, []Contract{{Key: "checkout", Target: population, TimeoutMS: 5000}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := NewAggregator(c, population/2), NewAggregator(c, population/2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < population; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := a
			if id >= population/2 {
				client = b
			}
			out, err := client.Wait(ctx, "checkout", 1, uint64(id))
			if err != nil || out.State != "released" {
				t.Errorf("participant %d: %+v %v", id, out, err)
			}
		}(i)
	}
	wg.Wait()
	a.mu.Lock()
	left := len(a.rounds)
	a.mu.Unlock()
	b.mu.Lock()
	left += len(b.rounds)
	b.mu.Unlock()
	if left != 0 {
		t.Fatal("local waiter state retained")
	}
}

func TestRestoreRejectsCorruptMembership(t *testing.T) {
	c := coordinatorFor(t, 4, nil)
	_, _ = c.Exchange(context.Background(), Batch{Key: "checkout", Round: 1, Arrivals: []uint64{10}})
	s := c.Snapshot()
	r := s.Rounds["checkout/1"]
	r.Members[0] |= 128
	s.Rounds["checkout/1"] = r
	if _, err := Restore(s, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("out-of-range membership bit accepted")
	}
}

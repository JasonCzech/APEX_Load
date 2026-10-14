package pwshutdown

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The driver stops only after every browser has closed.
func TestCloseThenStopClosesBrowsersBeforeTheDriver(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(name string) func() error {
		return func() error {
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}
	}
	stop := func() error {
		mu.Lock()
		order = append(order, "driver")
		mu.Unlock()
		return nil
	}
	if !CloseThenStop(time.Second, stop, record("a"), nil, record("b")) {
		t.Fatal("CloseThenStop reported a timeout")
	}
	if len(order) != 3 || order[2] != "driver" {
		t.Fatalf("order = %v, want both browsers before the driver", order)
	}
}

// A browser that does not close in time still has its driver stopped, once.
func TestCloseThenStopStopsTheDriverAfterTheWait(t *testing.T) {
	release := make(chan struct{})
	var stops atomic.Int32
	stopped := make(chan struct{}, 2)
	stop := func() error { stops.Add(1); stopped <- struct{}{}; return nil }
	hung := func() error { <-release; return nil }
	if CloseThenStop(30*time.Millisecond, stop, hung) {
		t.Fatal("CloseThenStop reported success for a browser that never closed")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the driver was not stopped after the wait")
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if got := stops.Load(); got != 1 {
		t.Fatalf("driver stopped %d times, want once", got)
	}
}

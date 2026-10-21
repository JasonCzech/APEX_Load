package playwright

import (
	"sync"
	"testing"
)

func TestAPEXRequestTimingDetachedSnapshot(t *testing.T) {
	r := &requestImpl{timing: &RequestTiming{StartTime: 10, ResponseStart: -1, ResponseEnd: -1}}
	early := r.Timing()
	r.setResponseEndTiming(20)
	if early.ResponseEnd != -1 || early.ResponseStart != -1 {
		t.Fatal("native update mutated returned snapshot")
	}
	late := r.Timing()
	if late.ResponseStart != 20 || late.ResponseEnd != 20 {
		t.Fatal(late)
	}
	late.StartTime = 100
	if r.Timing().StartTime != 10 {
		t.Fatal("caller mutated SDK timing")
	}
	r.applyResponseTiming(map[string]any{"startTime": 30.0, "requestStart": 5.0})
	if r.Timing().StartTime != 30 || r.Timing().ResponseEnd != 20 || r.Timing().RequestStart != 5 {
		t.Fatal("partial initialization lost fields")
	}
	if (&requestImpl{}).Timing() != nil {
		t.Fatal("missing timing invented")
	}
}
func TestAPEXRequestTimingConcurrentNativeUpdates(t *testing.T) {
	r := &requestImpl{timing: &RequestTiming{ResponseStart: -1, ResponseEnd: -1}}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Go(func() {
			for i := 0; i < 10000; i++ {
				r.applyResponseTiming(map[string]any{"startTime": float64(i), "requestStart": float64(i)})
				r.setResponseEndTiming(float64(i))
				snapshot := r.Timing()
				if snapshot.StartTime != snapshot.RequestStart {
					t.Error("torn initializer snapshot")
					return
				}
				snapshot.ResponseEnd = -100
			}
		})
	}
	wg.Wait()
	if r.Timing().ResponseEnd < 0 {
		t.Fatal("caller snapshot escaped")
	}
}

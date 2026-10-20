package playwright

import (
	"errors"
	"sync"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
)

// Arrange a native transition precisely after a negative state snapshot. If
// registration holds the registration lock, the native callback waits for that owner;
// otherwise it completes before the waiter can subscribe, reproducing the gap.
type apexTransitionAtSnapshot struct {
	mapset.Set[string]
	frame  *frameImpl
	once   sync.Once
	joined chan struct{}
}

func (s *apexTransitionAtSnapshot) ContainsOne(state string) bool {
	found := s.Set.ContainsOne(state)
	s.once.Do(func() {
		publish := func() { s.frame.onLoadState(map[string]any{"add": state}); close(s.joined) }
		if s.frame.loadStateMu.TryLock() {
			s.frame.loadStateMu.Unlock()
			publish()
		} else {
			go publish()
		}
	})
	return found
}
func apexLoadStateFrame() *frameImpl {
	return &frameImpl{loadStates: mapset.NewSet[string](), page: &pageImpl{browserContext: &browserContextImpl{}}}
}
func assertAPEXLoadWaiterJoined(t *testing.T, f *frameImpl) {
	t.Helper()
	if n := f.ListenerCount("loadstate"); n != 0 {
		t.Fatalf("retained %d load listeners", n)
	}
	for _, event := range []string{"close", "crash", "framedetached"} {
		if n := f.page.ListenerCount(event); n != 0 {
			t.Fatalf("retained %d %s listeners", n, event)
		}
	}
}
func TestAPEXLoadStateRegistrationDoesNotLoseTransition(t *testing.T) {
	for _, state := range []string{"load", "domcontentloaded", "networkidle"} {
		t.Run(state, func(t *testing.T) {
			f := apexLoadStateFrame()
			transition := &apexTransitionAtSnapshot{Set: f.loadStates, frame: f, joined: make(chan struct{})}
			f.loadStates = transition
			err := f.waitForLoadStateImpl(state, Float(100), nil)
			<-transition.joined
			if err != nil {
				t.Fatalf("completed native transition was lost: %v", err)
			}
			assertAPEXLoadWaiterJoined(t, f)
		})
	}
}
func TestAPEXLoadStateWaitPreservesReadyTimeoutAndClosedPage(t *testing.T) {
	t.Run("detached frame releases registration", func(t *testing.T) {
		f := apexLoadStateFrame()
		f.page = nil
		if err := f.waitForLoadStateImpl("load", Float(100), nil); err == nil {
			t.Fatal("detached frame succeeded")
		}
		if !f.loadStateMu.TryLock() {
			t.Fatal("error retained registration lock")
		}
		f.loadStateMu.Unlock()
	})
	t.Run("callback can publish state", func(t *testing.T) {
		f := apexLoadStateFrame()
		err := f.waitForLoadStateImpl("load", Float(100), func() error {
			if !f.loadStateMu.TryLock() {
				return errors.New("callback retained registration lock")
			}
			f.loadStateMu.Unlock()
			f.onLoadState(map[string]any{"add": "load"})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertAPEXLoadWaiterJoined(t, f)
	})
	t.Run("already ready", func(t *testing.T) {
		f := apexLoadStateFrame()
		f.loadStates.Add("load")
		if err := f.waitForLoadStateImpl("load", Float(100), nil); err != nil {
			t.Fatal(err)
		}
		assertAPEXLoadWaiterJoined(t, f)
	})
	t.Run("missing state times out", func(t *testing.T) {
		f := apexLoadStateFrame()
		if err := f.waitForLoadStateImpl("load", Float(10), nil); !errors.Is(err, ErrTimeout) {
			t.Fatalf("expected timeout: %v", err)
		}
		assertAPEXLoadWaiterJoined(t, f)
	})
	t.Run("closed page rejected", func(t *testing.T) {
		f := apexLoadStateFrame()
		f.page.isClosed = true
		if err := f.waitForLoadStateImpl("load", Float(100), nil); err == nil {
			t.Fatal("closed page succeeded")
		}
		assertAPEXLoadWaiterJoined(t, f)
	})
}

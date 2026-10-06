package playwright

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitAPEXOwnedHAR(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("owned close did not physically join")
	}
}

func TestAPEXOwnedHARRepeatCallersJoinExactResult(t *testing.T) {
	b := &browserContextImpl{}
	entered, gate := make(chan struct{}), make(chan struct{})
	var release sync.Once
	var calls atomic.Int32
	first, second := make(chan struct{}), make(chan struct{})
	secondEntered := make(chan struct{})
	var firstErr, secondErr error
	secondStarted := false
	defer func() {
		release.Do(func() { close(gate) })
		waitAPEXOwnedHAR(t, first)
		if secondStarted {
			waitAPEXOwnedHAR(t, second)
		}
	}()
	go func() {
		defer close(first)
		firstErr = b.closeHAROwned(func() error { calls.Add(1); close(entered); <-gate; return nil })
	}()
	waitAPEXOwnedHAR(t, entered)
	// The ordinary API retains its early-nil behavior but is no ownership proof.
	if err := b.Close(BrowserContextCloseOptions{Reason: String("not winner")}); err != nil {
		t.Fatal(err)
	}
	secondStarted = true
	go func() {
		defer close(second)
		close(secondEntered)
		secondErr = b.closeHAROwned(func() error { calls.Add(1); return errors.New("duplicate body") })
	}()
	waitAPEXOwnedHAR(t, secondEntered)
	select {
	case <-first:
		t.Fatal("first owner returned before body joined")
	case <-second:
		t.Fatal("repeat owner returned before body joined")
	case <-time.After(25 * time.Millisecond):
	}
	release.Do(func() { close(gate) })
	waitAPEXOwnedHAR(t, first)
	waitAPEXOwnedHAR(t, second)
	if firstErr != nil || secondErr != nil || calls.Load() != 1 || b.closeReason != nil {
		t.Fatal("close did not retain one exact result/winner", firstErr, secondErr, calls.Load())
	}
	if err := b.closeHAROwned(func() error { t.Fatal("completed owner repeated work"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestAPEXOwnedHARErrorPanicAndGoexitNeverBecomeSuccess(t *testing.T) {
	failure := errors.New("synthetic close failure")
	for _, mode := range []string{"error", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			b := &browserContextImpl{}
			done := make(chan struct{})
			var calls atomic.Int32
			go func() {
				defer close(done)
				defer func() { _ = recover() }()
				_ = b.closeHAROwned(func() error {
					calls.Add(1)
					switch mode {
					case "error":
						return failure
					case "panic":
						panic("synthetic panic")
					case "goexit":
						runtime.Goexit()
					}
					return nil
				})
			}()
			waitAPEXOwnedHAR(t, done)
			err := b.closeHAROwned(func() error { calls.Add(1); return nil })
			if !errors.Is(err, ErrHARCloseOwnershipUnproven) || calls.Load() != 1 {
				t.Fatal("abnormal physical result was erased/repeated", err, calls.Load())
			}
			if mode == "error" && !errors.Is(err, failure) {
				t.Fatal("original error lost", err)
			}
		})
	}
}

func TestAPEXOwnedHARPriorClientAndServerEntriesFailClosed(t *testing.T) {
	for _, mode := range []string{"ordinary_inflight", "ordinary_closed", "server_closed"} {
		t.Run(mode, func(t *testing.T) {
			b := &browserContextImpl{}
			if mode != "server_closed" {
				b.closeWasCalled.Store(true)
			}
			if mode != "ordinary_inflight" {
				b.isClosedFlag.Store(true)
			}
			var calls atomic.Int32
			err := b.closeHAROwned(func() error { calls.Add(1); return nil })
			if !errors.Is(err, ErrHARCloseOwnershipUnproven) || calls.Load() != 0 || b.ownedHARClose.done != nil {
				t.Fatal("unowned prior entry was adopted", err, calls.Load())
			}
			if err := b.Close(); err != nil {
				t.Fatal("ordinary prior-close compatibility changed", err)
			}
		})
	}
}

func TestAPEXOwnedHARServerCloseDuringBodyRetainsFailure(t *testing.T) {
	b := &browserContextImpl{}
	failure := errors.New("synthetic native acknowledgment unavailable")
	err := b.closeHAROwned(func() error { b.isClosedFlag.Store(true); return failure })
	if !errors.Is(err, failure) || !errors.Is(err, ErrHARCloseOwnershipUnproven) {
		t.Fatal("server close lost physical acknowledgment failure", err)
	}
	if got := b.closeHAROwned(func() error { t.Fatal("failed owner repeated body"); return nil }); !errors.Is(got, failure) {
		t.Fatal("failed result was not retained", got)
	}
}

func TestAPEXOwnedHARBroadcastCloseNotification(t *testing.T) {
	// Construct the real SDK context/Once notification without a native driver.
	parent := &channelOwner{objectType: "synthetic", objects: map[string]*channelOwner{}}
	tracing := &tracingImpl{}
	tracing.channel = &channel{object: tracing}
	request := &apiRequestContextImpl{}
	request.channel = &channel{object: request}
	b := newBrowserContext(parent, "BrowserContext", "synthetic", map[string]any{"tracing": tracing.channel, "requestContext": request.channel})
	b.onClose()
	// The channel is completion, not a one-consumer receipt. No caller adopts
	// that server-close event as fresh HAR proof.
	for i := 0; i < 2; i++ {
		select {
		case <-b.closed:
		default:
			t.Fatal("close completion was consumed by an earlier waiter")
		}
	}
	if err := b.closeHAROwned(func() error { t.Fatal("server-close event became writer proof"); return nil }); !errors.Is(err, ErrHARCloseOwnershipUnproven) {
		t.Fatal(err)
	}
}

func TestAPEXOwnedHARDispatcherRepeatCannotBlockFirstAcknowledgment(t *testing.T) {
	connection := &connection{}
	b := &browserContextImpl{channelOwner: channelOwner{connection: connection}}
	bodyEntered, gate, firstDone, callbackEntered, callbackDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(gate) }) }
	var firstErr, callbackErr error
	defer func() { release(); waitAPEXOwnedHAR(t, firstDone); waitAPEXOwnedHAR(t, callbackDone) }()
	go func() {
		defer close(firstDone)
		firstErr = b.closeHAROwned(func() error { close(bodyEntered); <-gate; return nil })
	}()
	waitAPEXOwnedHAR(t, bodyEntered)
	b.OnClose(func(BrowserContext) { close(callbackEntered); callbackErr = b.CloseHAROwned(); release() })
	go func() { defer close(callbackDone); connection.dispatchGID.Store(currentGoroutineID()); b.onClose() }()
	waitAPEXOwnedHAR(t, callbackEntered)
	select {
	case <-callbackDone:
	case <-time.After(25 * time.Millisecond):
		t.Error("unfinished dispatcher repeat blocks first owner's acknowledgment")
	}
	release()
	waitAPEXOwnedHAR(t, callbackDone)
	waitAPEXOwnedHAR(t, firstDone)
	if !errors.Is(callbackErr, ErrHARCloseOwnershipUnproven) || firstErr != nil {
		t.Fatal("dispatcher repeat claimed completion or poisoned first owner", callbackErr, firstErr)
	}
	if err := b.CloseHAROwned(); err != nil {
		t.Fatal("completed exact owned result was lost", err)
	}
}

func TestAPEXOwnedHARUnknownDispatcherRepeatFailsClosed(t *testing.T) {
	b := &browserContextImpl{channelOwner: channelOwner{connection: &connection{}}}
	entered, gate, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	defer func() { release(); waitAPEXOwnedHAR(t, done) }()
	go func() { defer close(done); _ = b.closeHAROwned(func() error { close(entered); <-gate; return nil }) }()
	waitAPEXOwnedHAR(t, entered)
	if err := b.CloseHAROwned(); !errors.Is(err, ErrHARCloseOwnershipUnproven) {
		t.Fatal("unknown dispatch identity accepted unfinished wait", err)
	}
	release()
	waitAPEXOwnedHAR(t, done)
	if err := b.CloseHAROwned(); err != nil {
		t.Fatal("unknown identity erased completed result", err)
	}
}

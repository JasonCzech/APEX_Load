package api

import (
	"testing"
	"time"
)

func TestPanicRecoveryAllowsCompletionToSettleAfterDurableWrite(t *testing.T) {
	previousTimeout, previousExtensions := runTestPanicRecoveryLockTimeout, runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryLockTimeout, runTestPanicRecoveryDurableWriteExtensions = 50*time.Millisecond, 3
	defer func() {
		runTestPanicRecoveryLockTimeout, runTestPanicRecoveryDurableWriteExtensions = previousTimeout, previousExtensions
	}()
	runner := &TestRunner{}
	runner.durableWritesInFlight.Store(1)
	done := make(chan struct{})
	result := make(chan bool, 1)
	go func() { result <- runner.awaitPanicRecoveryStage("test", "settling", done) }()
	// At least the first deadline sees in-flight I/O. Then simulate the caller's
	// post-write work spanning the next deadline before publishing completion.
	time.Sleep(70 * time.Millisecond)
	runner.durableWritesInFlight.Store(0)
	time.Sleep(60 * time.Millisecond)
	close(done)
	if !<-result {
		t.Fatal("completed write was mistaken for an orphaned gate")
	}
}

func TestPanicRecoverySettlingStillHonorsMaximumBudget(t *testing.T) {
	previousTimeout, previousExtensions := runTestPanicRecoveryLockTimeout, runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryLockTimeout, runTestPanicRecoveryDurableWriteExtensions = 10*time.Millisecond, 2
	defer func() {
		runTestPanicRecoveryLockTimeout, runTestPanicRecoveryDurableWriteExtensions = previousTimeout, previousExtensions
	}()
	runner := &TestRunner{}
	runner.durableWritesInFlight.Store(1)
	result := make(chan bool, 1)
	go func() { result <- runner.awaitPanicRecoveryStage("test", "wedged", make(chan struct{})) }()
	select {
	case ok := <-result:
		if ok {
			t.Fatal("unfinished stage reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("continuous writes defeated the bounded watchdog")
	}
}

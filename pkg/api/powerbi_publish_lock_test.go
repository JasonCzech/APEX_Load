package api

import (
	"fmt"
	"testing"
)

func TestPowerBIPublishLockRejectsConcurrentPublishAndEvictsOnRelease(t *testing.T) {
	baseline := powerBIPublishLockCount()
	unlock, acquired := tryLockPowerBIPublish("lock-test")
	if !acquired {
		t.Fatal("first lock was not acquired")
	}
	if secondUnlock, secondAcquired := tryLockPowerBIPublish("lock-test"); secondAcquired || secondUnlock != nil {
		t.Fatal("concurrent publish acquired the same test lock")
	}
	unlock()
	if got := powerBIPublishLockCount(); got != baseline {
		t.Fatalf("in-flight lock count = %d after release, want %d", got, baseline)
	}
	replacementUnlock, acquired := tryLockPowerBIPublish("lock-test")
	if !acquired {
		t.Fatal("replacement lock was not acquired")
	}
	unlock() // A stale duplicate release must not erase the replacement owner.
	if thirdUnlock, thirdAcquired := tryLockPowerBIPublish("lock-test"); thirdAcquired || thirdUnlock != nil {
		t.Fatal("duplicate stale release erased the replacement lock owner")
	}
	replacementUnlock()

	for index := 0; index < 1000; index++ {
		unlock, acquired := tryLockPowerBIPublish(fmt.Sprintf("churn-%d", index))
		if !acquired {
			t.Fatalf("lock %d was not acquired", index)
		}
		unlock()
	}
	if got := powerBIPublishLockCount(); got != baseline {
		t.Fatalf("lock churn retained %d entries, want baseline %d", got, baseline)
	}
}

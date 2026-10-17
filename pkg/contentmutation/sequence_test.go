package contentmutation

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

type memorySequenceStore struct {
	mu                  sync.Mutex
	value               int
	failRead, failWrite bool
}

func (s *memorySequenceStore) GetSetting(string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failRead {
		return "", errors.New("read unavailable")
	}
	return strconv.Itoa(s.value), nil
}
func (s *memorySequenceStore) SaveSetting(_ string, raw string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrite {
		return errors.New("write unavailable")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return err
	}
	s.value = n
	return nil
}

type atomicSequenceMemory struct{ memorySequenceStore }

func (s *atomicSequenceMemory) NextSettingSequence(string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrite {
		return 0, errors.New("write unavailable")
	}
	s.value++
	return s.value, nil
}
func (s *atomicSequenceMemory) EnsureSettingSequenceAtLeast(_ string, floor int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrite {
		return 0, errors.New("floor unavailable")
	}
	s.value = max(s.value, floor)
	return s.value, nil
}

func TestSequenceObservedFloorSurvivesFailedAllocation(t *testing.T) {
	for _, atomic := range []bool{false, true} {
		t.Run(strconv.FormatBool(atomic), func(t *testing.T) {
			base := &memorySequenceStore{}
			var store SequenceStore = base
			if atomic {
				a := &atomicSequenceMemory{}
				base = &a.memorySequenceStore
				store = a
			}
			allocator := NewSequenceAllocator("scripts", 0, store)
			allocator.Observe(1_234_567)
			base.failWrite = true
			if _, err := allocator.Next(); err == nil {
				t.Fatal("failed allocation reported success")
			}
			base.failWrite = false
			if err := allocator.PersistFloor(0); err != nil {
				t.Fatal(err)
			}
			if base.value != 1_234_567 {
				t.Fatalf("observed floor lost after failed write: %d", base.value)
			}
			next, err := allocator.Next()
			if err != nil || next != 1_234_568 {
				t.Fatalf("next=%d err=%v", next, err)
			}
		})
	}
}

func TestSequenceConcurrentAllocatorsShareAtomicOwnership(t *testing.T) {
	store := &atomicSequenceMemory{}
	first, second := NewSequenceAllocator("scripts", 0, store), NewSequenceAllocator("scripts", 0, store)
	first.Observe(1000)
	if err := first.PersistFloor(1000); err != nil {
		t.Fatal(err)
	}
	values := make(chan int, 200)
	var done sync.WaitGroup
	for _, allocator := range []*SequenceAllocator{first, second} {
		done.Add(1)
		go func() {
			defer done.Done()
			for range 100 {
				value, err := allocator.Next()
				if err != nil {
					t.Error(err)
					return
				}
				values <- value
			}
		}()
	}
	done.Wait()
	close(values)
	seen := map[int]bool{}
	for value := range values {
		if seen[value] || value <= 1000 {
			t.Fatalf("duplicate or stale allocation %d", value)
		}
		seen[value] = true
	}
	if len(seen) != 200 {
		t.Fatalf("allocated %d unique values", len(seen))
	}
}

func TestSequenceCompatibilityStoreSerializesOneOwnerAndHonorsExternalFloor(t *testing.T) {
	store := &memorySequenceStore{value: 50}
	allocator := NewSequenceAllocator("scenarios", 0, store)
	var done sync.WaitGroup
	values := make(chan int, 100)
	for range 100 {
		done.Add(1)
		go func() {
			defer done.Done()
			value, err := allocator.Next()
			if err != nil {
				t.Error(err)
				return
			}
			values <- value
		}()
	}
	done.Wait()
	close(values)
	seen := map[int]bool{}
	for value := range values {
		if value <= 50 || seen[value] {
			t.Fatalf("duplicate/stale %d", value)
		}
		seen[value] = true
	}
	if len(seen) != 100 {
		t.Fatalf("got %d values", len(seen))
	}
	store.mu.Lock()
	store.value = 500
	store.mu.Unlock()
	if value, err := allocator.Next(); err != nil || value != 501 {
		t.Fatalf("external floor ignored: %d %v", value, err)
	}
}

func TestSequenceMemoryOnlyAndReadFailureDoNotConsumeIdentity(t *testing.T) {
	allocator := NewSequenceAllocator("scripts", 9, nil)
	if n, err := allocator.Next(); err != nil || n != 10 {
		t.Fatalf("memory next: %d %v", n, err)
	}
	store := &memorySequenceStore{failRead: true}
	allocator = NewSequenceAllocator("scripts", 0, store)
	if _, err := allocator.Next(); err == nil {
		t.Fatal("read failure ignored")
	}
	store.failRead = false
	if n, err := allocator.Next(); err != nil || n != 1 {
		t.Fatalf("failed read consumed identity: %d %v", n, err)
	}
}

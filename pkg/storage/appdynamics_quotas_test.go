package storage

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func checkAppDynamicsQuotaLeases(t *testing.T, s *SQLiteStore) {
	t.Helper()
	key := strings.Repeat("a", 64)
	now := time.Now().UTC()
	for _, id := range []string{"direct-leader", "collector-a"} {
		if ok, err := s.AllocateAppDynamicsQuota(key, id, now); err != nil || !ok {
			t.Fatal(id, ok, err)
		}
	}
	if ok, err := s.AllocateAppDynamicsQuota(key, "collector-b", now); err != nil || ok {
		t.Fatal("third executor", ok, err)
	}
	if ok, err := s.AllocateAppDynamicsQuota(key, "direct-leader", now.Add(30*time.Second)); err != nil || !ok {
		t.Fatal("renewal", ok, err)
	}
	if ok, err := s.AllocateAppDynamicsQuota(key, "collector-b", now.Add(time.Minute)); err != nil || !ok {
		t.Fatal("expiry", ok, err)
	}
	if ok, err := s.AllocateAppDynamicsQuota(key, "collector-c", now.Add(time.Minute)); err != nil || ok {
		t.Fatal("renewed executor should remain", ok, err)
	}
	if ok, err := s.AllocateAppDynamicsQuota(strings.Repeat("b", 64), "collector-c", now); err != nil || !ok {
		t.Fatal("independent account", ok, err)
	}
}
func TestAppDynamicsQuotaLeasesAndConcurrency(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/quotas.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkAppDynamicsQuotaLeases(t, s)
	key := strings.Repeat("c", 64)
	now := time.Now().UTC()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			ok, err := s.AllocateAppDynamicsQuota(key, string(rune('a'+i)), now)
			if err != nil {
				t.Error(err)
			}
			if ok {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 2 {
		t.Fatalf("admitted=%d", admitted.Load())
	}
	if _, err := s.AllocateAppDynamicsQuota("plain-secret-value", "executor", now); err == nil {
		t.Fatal("nonopaque resource identity accepted")
	}
}

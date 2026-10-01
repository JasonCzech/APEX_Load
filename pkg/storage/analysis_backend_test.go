package storage

import (
	"strings"
	"sync"
	"testing"
)

func TestAnalysisStorageIdentityFencesDifferentReplicas(t *testing.T) {
	s, _, _ := monitorFixture(t)
	if err := s.EnsureAnalysisStorageIdentity("invalid"); err == nil {
		t.Fatal("invalid identity accepted")
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, id := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		wg.Go(func() {
			if s.EnsureAnalysisStorageIdentity(id) == nil {
				results <- id
			}
		})
	}
	wg.Wait()
	close(results)
	var winner string
	count := 0
	for id := range results {
		winner = id
		count++
	}
	if count != 1 {
		t.Fatalf("expected one namespace, got %d", count)
	}
	if err := s.EnsureAnalysisStorageIdentity(winner); err != nil {
		t.Fatal(err)
	}
}

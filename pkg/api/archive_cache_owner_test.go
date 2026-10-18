package api

import (
	"context"
	"testing"
)

func TestArchiveReadCachesAreIsolatedBetweenServers(t *testing.T) {
	first := NewServer(ServerConfig{MaxConcurrent: 1})
	second := NewServer(ServerConfig{MaxConcurrent: 1})
	t.Cleanup(func() { _ = first.Shutdown(context.Background()) })
	t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
	left, right := first.archiveReadCaches(), second.archiveReadCaches()
	if left != first.runner.archiveReadCaches() || right != second.runner.archiveReadCaches() {
		t.Fatal("server reads and runner invalidation must use the same caches")
	}
	if left == right || left.catalogReadSlots == right.catalogReadSlots {
		t.Fatal("independent controllers share archive cache ownership")
	}
	checkArchiveCacheOwnerIsolation(t, left, right, left.catalogIdentities, right.catalogIdentities, &archiveCatalogIdentity{}, &archiveCatalogIdentity{})
	checkArchiveCacheOwnerIsolation(t, left, right, left.reports, right.reports, &RunArchiveReport{}, &RunArchiveReport{})
	checkArchiveCacheOwnerIsolation(t, left, right, left.manifests, right.manifests, &RunArchiveManifest{}, &RunArchiveManifest{})
	checkArchiveCacheOwnerIsolation(t, left, right, left.sanitizedPlans, right.sanitizedPlans, &sanitizedRunArchivePlan{}, &sanitizedRunArchivePlan{})
}

func checkArchiveCacheOwnerIsolation[T any](t *testing.T, first, second *archiveReadCaches, left, right *boundedArchiveCache[T], leftValue, rightValue *T) {
	t.Helper()
	const testID, key = "same-run", "same-run|same-manifest"
	t.Cleanup(func() { first.forgetRunArchiveCache(testID) })
	t.Cleanup(func() { second.forgetRunArchiveCache(testID) })
	leftLoads, rightLoads := 0, 0
	leftLoad := func() (*T, error) { leftLoads++; return leftValue, nil }
	rightLoad := func() (*T, error) { rightLoads++; return rightValue, nil }
	if got, err := left.getOrLoad(key, leftLoad); err != nil || got != leftValue {
		t.Fatalf("first controller cache=%p err=%v", got, err)
	}
	if got, err := right.getOrLoad(key, rightLoad); err != nil || got != rightValue {
		t.Fatalf("second controller reused first's content: %p err=%v", got, err)
	}
	first.forgetRunArchiveCache(testID)
	if _, err := left.getOrLoad(key, leftLoad); err != nil {
		t.Fatal(err)
	}
	if _, err := right.getOrLoad(key, rightLoad); err != nil {
		t.Fatal(err)
	}
	if leftLoads != 2 || rightLoads != 1 {
		t.Fatalf("cross-controller invalidation: first loads=%d second loads=%d", leftLoads, rightLoads)
	}
}

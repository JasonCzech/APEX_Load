package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSanitizedArchivePlanCacheUsesManifestIdentityAndInvalidatesByTest(t *testing.T) {
	record := &storage.RunArchiveRecord{TestID: "cache-run", ManifestSHA256: "ABC123"}
	key := sanitizedArchivePlanCacheKey(record)
	if key != "cache-run|sanitized|abc123" {
		t.Fatalf("cache key = %q", key)
	}
	caches := newArchiveReadCaches()
	t.Cleanup(func() { caches.forgetRunArchiveCache(record.TestID) })
	plan := &sanitizedRunArchivePlan{replacements: map[string][]byte{"manifest.json": []byte("safe")}}
	loads := 0
	load := func() (*sanitizedRunArchivePlan, error) {
		loads++
		return plan, nil
	}
	for range 2 {
		if got, err := caches.sanitizedPlans.getOrLoad(key, load); err != nil || got != plan {
			t.Fatalf("cached plan = %p, %v; want %p", got, err, plan)
		}
	}
	if loads != 1 {
		t.Fatalf("cache miss for unchanged manifest: %d loads", loads)
	}
	otherKey := "unrelated-run|sanitized|other"
	t.Cleanup(func() { caches.forgetRunArchiveCache("unrelated-run") })
	otherLoads := 0
	otherLoad := func() (*sanitizedRunArchivePlan, error) {
		otherLoads++
		return &sanitizedRunArchivePlan{}, nil
	}
	if _, err := caches.sanitizedPlans.getOrLoad(otherKey, otherLoad); err != nil {
		t.Fatal(err)
	}
	caches.forgetRunArchiveCache(record.TestID)
	if _, err := caches.sanitizedPlans.getOrLoad(key, load); err != nil {
		t.Fatal(err)
	}
	if _, err := caches.sanitizedPlans.getOrLoad(otherKey, otherLoad); err != nil {
		t.Fatal(err)
	}
	if loads != 2 || otherLoads != 1 {
		t.Fatalf("invalidation loads: run=%d unrelated=%d; want 2 and 1", loads, otherLoads)
	}
}

func TestSanitizedArchivePlanCacheSkipsUnanchoredRecords(t *testing.T) {
	for _, record := range []*storage.RunArchiveRecord{
		nil,
		{TestID: "run"},
		{ManifestSHA256: "hash"},
	} {
		if key := sanitizedArchivePlanCacheKey(record); key != "" {
			t.Fatalf("unanchored record cache key = %q", key)
		}
	}
}

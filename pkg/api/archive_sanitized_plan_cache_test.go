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
	t.Cleanup(func() { forgetRunArchiveCache(record.TestID) })
	// The cache is process-global and other tests leave their own runs' plans in
	// it, so count loads of this key rather than asserting on the cache size.
	plan := &sanitizedRunArchivePlan{replacements: map[string][]byte{"manifest.json": []byte("safe")}}
	loads := 0
	load := func() (*sanitizedRunArchivePlan, error) {
		loads++
		return plan, nil
	}
	for range 2 {
		if got, err := runArchiveSanitizedPlanCache.getOrLoad(key, load); err != nil || got != plan {
			t.Fatalf("getOrLoad = %p, %v; want the loaded plan", got, err)
		}
	}
	if loads != 1 {
		t.Fatalf("loads = %d before invalidation, want 1: sanitized plan was not cached", loads)
	}
	forgetRunArchiveCache(record.TestID)
	if _, err := runArchiveSanitizedPlanCache.getOrLoad(key, load); err != nil {
		t.Fatalf("reload after invalidation: %v", err)
	}
	if loads != 2 {
		t.Fatalf("loads = %d after invalidation, want 2: sanitized plan survived archive cache invalidation", loads)
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

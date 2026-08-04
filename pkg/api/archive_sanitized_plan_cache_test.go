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
	plan := &sanitizedRunArchivePlan{replacements: map[string][]byte{"manifest.json": []byte("safe")}}
	if _, err := runArchiveSanitizedPlanCache.getOrLoad(key, func() (*sanitizedRunArchivePlan, error) { return plan, nil }); err != nil {
		t.Fatalf("populate cache: %v", err)
	}
	if runArchiveSanitizedPlanCache.len() == 0 {
		t.Fatal("sanitized plan was not cached")
	}
	forgetRunArchiveCache(record.TestID)
	if runArchiveSanitizedPlanCache.len() != 0 {
		t.Fatal("sanitized plan survived archive cache invalidation")
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

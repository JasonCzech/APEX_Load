package boundedcache

import "testing"

func TestStringCacheBoundsEntriesAndKeyBytes(t *testing.T) {
	cache := NewStringCache[int](2, 4, 6)
	cache.LoadOrStore("aa", 1)
	cache.LoadOrStore("bb", 2)
	cache.LoadOrStore("ccc", 3)
	if cache.Len() != 2 {
		t.Fatalf("cache length = %d, want 2", cache.Len())
	}
	if _, ok := cache.Load("aa"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if actual, loaded := cache.LoadOrStore("oversized", 4); loaded || actual != 4 {
		t.Fatalf("oversized admission = (%d, %v), want (4, false)", actual, loaded)
	}
	if cache.Len() != 2 {
		t.Fatalf("oversized key changed cache length to %d", cache.Len())
	}
}

// The oversized check above runs against a full cache, so an eviction can mask
// a lost admission guard: a victim leaves as the oversized key arrives and
// Len() is unchanged either way. These caches have room to spare, so retaining
// an oversized key is directly visible in Len().
func TestStringCacheNeverRetainsOversizedKey(t *testing.T) {
	perKey := NewStringCache[int](8, 4, 64)
	if actual, loaded := perKey.LoadOrStore("oversized-key", 7); loaded || actual != 7 {
		t.Fatalf("oversized admission = (%d, %v), want (7, false)", actual, loaded)
	}
	if perKey.Len() != 0 {
		t.Fatalf("key over the per-key bound was retained: length = %d, want 0", perKey.Len())
	}

	total := NewStringCache[int](8, 16, 3)
	if actual, loaded := total.LoadOrStore("abcd", 9); loaded || actual != 9 {
		t.Fatalf("over-total admission = (%d, %v), want (9, false)", actual, loaded)
	}
	if total.Len() != 0 {
		t.Fatalf("key over the total-bytes bound was retained: length = %d, want 0", total.Len())
	}
}

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

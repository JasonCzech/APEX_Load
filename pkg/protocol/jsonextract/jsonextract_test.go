package jsonextract

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
)

func TestJSONPath(t *testing.T) {
	body := []byte(`{"data":{"items":[{"id":"a"},{"id":"b"}]},"count":2}`)
	cases := []struct {
		expr string
		want string
	}{
		{"$.data.items[1].id", "b"},
		{"data.items[0].id", "a"},
		{"$.count", "2"},
		{"$", `{"count":2,"data":{"items":[{"id":"a"},{"id":"b"}]}}`},
	}
	for _, tc := range cases {
		got, err := JSONPath(tc.expr, body)
		if err != nil {
			t.Fatalf("JSONPath(%q) error: %v", tc.expr, err)
		}
		if got != tc.want {
			t.Fatalf("JSONPath(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestJSONPathErrors(t *testing.T) {
	body := []byte(`{"data":{"items":[]}}`)
	for _, expr := range []string{"", "$.data.items[5]", "$.missing"} {
		if _, err := JSONPath(expr, body); err == nil {
			t.Fatalf("JSONPath(%q) expected error, got nil", expr)
		}
	}
	if _, err := JSONPath("$.x", []byte("not json")); err == nil {
		t.Fatalf("JSONPath on invalid JSON expected error")
	}
	for _, expr := range []string{"$.items[abc]", "$.items[]", "$.items[1", "$.items[1]suffix"} {
		if _, err := JSONPath(expr, []byte(`{"items":["a","b"]}`)); err == nil {
			t.Fatalf("JSONPath(%q) accepted malformed index", expr)
		}
	}
}

func TestJSONPathRejectsEmptySegmentsAsConfigurationErrors(t *testing.T) {
	body := []byte(`{"items":[{"id":"one"}]}`)
	for _, expr := range []string{
		"$.",
		".items",
		"$.items..id",
		"items..id",
		"items.",
		"$items",
		"$.[0]",
		"$.items.[0]",
		"items.[0]",
	} {
		t.Run(expr, func(t *testing.T) {
			_, err := JSONPath(expr, body)
			if err == nil {
				t.Fatalf("JSONPath(%q) accepted an empty or malformed path segment", expr)
			}
			if errors.Is(err, ErrNotFound) {
				t.Fatalf("JSONPath(%q) error = %v, want configuration error", expr, err)
			}
		})
	}
}

func TestJSONPathAllowsRootArrayIndex(t *testing.T) {
	got, err := JSONPath("$[1].id", []byte(`[{"id":"first"},{"id":"second"}]`))
	if err != nil {
		t.Fatalf("JSONPath root array index error: %v", err)
	}
	if got != "second" {
		t.Fatalf("JSONPath root array index = %q, want second", got)
	}
}

func TestJMESPath(t *testing.T) {
	body := []byte(`{"users":[{"name":"ann"},{"name":"bob"}]}`)
	got, err := JMESPath("users[1].name", body)
	if err != nil {
		t.Fatalf("JMESPath error: %v", err)
	}
	if got != "bob" {
		t.Fatalf("JMESPath = %q, want %q", got, "bob")
	}
}

func TestJMESPathClassifiesMissingValue(t *testing.T) {
	_, err := JMESPath("users[3].name", []byte(`{"users":[{"name":"alice"}]}`))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("JMESPath error = %v, want ErrNotFound", err)
	}
}

func TestRegex(t *testing.T) {
	body := []byte("token=abc123;")
	got, err := Regex(`token=([a-z0-9]+)`, body)
	if err != nil {
		t.Fatalf("Regex error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("Regex = %q, want %q", got, "abc123")
	}
	if _, err := Regex(`nope=(\d+)`, body); err == nil {
		t.Fatalf("Regex no-match expected error")
	}
	if _, err := Regex(`(`, body); err == nil {
		t.Fatalf("Regex invalid pattern expected error")
	}
}

func TestBoundary(t *testing.T) {
	body := []byte("<<START>>payload<<END>>")
	got, err := Boundary("<<START>>|||<<END>>", body)
	if err != nil {
		t.Fatalf("Boundary error: %v", err)
	}
	if got != "payload" {
		t.Fatalf("Boundary = %q, want %q", got, "payload")
	}
	if _, err := Boundary("no-delim", body); err == nil {
		t.Fatalf("Boundary malformed expr expected error")
	}
	if _, err := Boundary("X|||Y", body); err == nil {
		t.Fatalf("Boundary missing left expected error")
	}
}

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"text", "text"},
		{float64(3), "3"},
		{true, "true"},
		{map[string]any{"a": float64(1)}, `{"a":1}`},
	}
	for _, tc := range cases {
		if got := Stringify(tc.in); got != tc.want {
			t.Fatalf("Stringify(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Reading the cache back cannot prove reuse: boundedcache never overwrites an
// admitted entry, so two Loads of the same key are the same value by
// construction whether or not Regex consulted them. The compile counter is the
// only thing that observes what Regex actually did.
func TestRegexReusesCompiledPattern(t *testing.T) {
	const expr = `reuse=([a-z0-9]+)`
	body := []byte("reuse=abc123")
	if _, err := Regex(expr, body); err != nil {
		t.Fatalf("Regex error: %v", err)
	}
	if _, ok := regexCompileCache.Load(expr); !ok {
		t.Fatal("pattern was not memoized after first use")
	}
	compiles := regexCompileCount.Load()
	const reuses = 16
	for i := 0; i < reuses; i++ {
		if _, err := Regex(expr, body); err != nil {
			t.Fatalf("Regex error: %v", err)
		}
	}
	if extra := regexCompileCount.Load() - compiles; extra != 0 {
		t.Fatalf("Regex recompiled a pattern it had already compiled: %d compiles across %d reuses, want 0", extra, reuses)
	}
	if _, ok := regexCompileCache.Load(expr); !ok {
		t.Fatal("pattern was evicted between uses")
	}
}

func TestRegexCachesCompileFailuresWithoutRecompiling(t *testing.T) {
	const expr = `bad-pattern-(`
	if _, err := Regex(expr, []byte("body")); err == nil {
		t.Fatal("Regex(invalid) expected error")
	}
	cached, ok := regexCompileCache.Load(expr)
	if !ok {
		t.Fatal("invalid pattern was not memoized")
	}
	if cached.compileErr == nil {
		t.Fatal("cached entry for an invalid pattern has no compile error")
	}
	compiles := regexCompileCount.Load()
	_, err := Regex(expr, []byte("body"))
	if err == nil {
		t.Fatal("Regex(invalid) expected error on the cached path")
	}
	if extra := regexCompileCount.Load() - compiles; extra != 0 {
		t.Fatalf("Regex recompiled an expression whose compile failure was already memoized: %d extra compiles, want 0", extra)
	}
	// The memoized failure must be handed back verbatim, not a freshly produced
	// equivalent — a fresh error value means a fresh compile.
	if err != cached.compileErr {
		t.Fatalf("Regex returned a different error value (%v) than the memoized one (%v)", err, cached.compileErr)
	}
}

func TestRegexCompileCacheIsBounded(t *testing.T) {
	for i := 0; i < boundedcache.DefaultMaxEntries*2; i++ {
		expr := fmt.Sprintf("param-%d-([0-9]+)", i)
		if _, err := Regex(expr, []byte("no match here")); err == nil {
			t.Fatalf("Regex(%q) expected a not-found error", expr)
		}
	}
	if size := regexCompileCache.Len(); size > boundedcache.DefaultMaxEntries {
		t.Fatalf("compile cache holds %d entries, want at most %d", size, boundedcache.DefaultMaxEntries)
	}
}

func TestRegexDoesNotRetainOversizedPatterns(t *testing.T) {
	// A hostile or machine-generated expression can be arbitrarily long. It must
	// still evaluate correctly, but it must not be pinned in the process-global
	// cache.
	expr := "(?:" + strings.Repeat("a|", boundedcache.DefaultMaxKeyBytes) + "b)"
	if len(expr) <= boundedcache.DefaultMaxKeyBytes {
		t.Fatalf("test expression is %d bytes, expected larger than the %d-byte key bound", len(expr), boundedcache.DefaultMaxKeyBytes)
	}
	// Retention must be observed through the cache's size, not through
	// Load(expr): Load applies the same key-size guard and returns false for an
	// oversized key before it ever consults the map, so it reports "not
	// retained" even for a cache that retained the entry (or filed it under a
	// surrogate key).
	before := regexCompileCache.Len()
	compiles := regexCompileCount.Load()

	got, err := Regex(expr, []byte("zzbzz"))
	if err != nil {
		t.Fatalf("Regex(oversized) error: %v", err)
	}
	if got != "b" {
		t.Fatalf("Regex(oversized) = %q, want %q", got, "b")
	}
	if after := regexCompileCache.Len(); after != before {
		t.Fatalf("compile cache grew from %d to %d entries: the oversized pattern was retained", before, after)
	}
	if used := regexCompileCount.Load() - compiles; used != 1 {
		t.Fatalf("first Regex(oversized) made %d compiles, want exactly 1", used)
	}

	// A second call must recompile. If it is served from a cache entry — under
	// this key or a surrogate one — the compile count does not move, which is
	// the retention this bound exists to prevent.
	compiles = regexCompileCount.Load()
	if got, err = Regex(expr, []byte("zzbzz")); err != nil || got != "b" {
		t.Fatalf("second Regex(oversized) = %q, %v; want %q, nil", got, err, "b")
	}
	if used := regexCompileCount.Load() - compiles; used != 1 {
		t.Fatalf("second Regex(oversized) made %d compiles, want exactly 1 — the oversized pattern is being served from the cache", used)
	}
	if after := regexCompileCache.Len(); after != before {
		t.Fatalf("compile cache grew from %d to %d entries after the second call", before, after)
	}
}

func TestRegexCompileCacheIsConcurrencySafe(t *testing.T) {
	body := []byte("concurrent=value7")
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 64; i++ {
				if _, err := Regex(`concurrent=([a-z0-9]+)`, body); err != nil {
					t.Errorf("shared pattern error: %v", err)
					return
				}
				if _, err := Regex(fmt.Sprintf("worker-%d-iteration-%d-([0-9]+)", worker, i), body); err == nil {
					t.Errorf("unique pattern expected a not-found error")
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

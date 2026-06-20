package api

import (
	"fmt"
	"testing"
	"time"
)

// TestTrimLiveErrorPatternsLockedCapsCardinality verifies the per-run error
// pattern map is bounded and that eviction targets the lowest-value patterns
// (fewest occurrences) — the ones the UI, which sorts by Count desc, never
// surfaces.
func TestTrimLiveErrorPatternsLockedCapsCardinality(t *testing.T) {
	patterns := map[string]*LiveErrorPattern{}
	base := time.Now()
	total := maxLiveErrorPatternsPerRun + 100
	for i := 0; i < total; i++ {
		sig := fmt.Sprintf("error signature id%dx", i)
		patterns[sig] = &LiveErrorPattern{
			Signature:  sig,
			Count:      int64(i + 1), // higher index => higher count
			LatestSeen: base.Add(time.Duration(i) * time.Second),
		}
	}

	trimLiveErrorPatternsLocked(patterns)

	if len(patterns) != maxLiveErrorPatternsPerRun {
		t.Fatalf("expected %d patterns after trim, got %d", maxLiveErrorPatternsPerRun, len(patterns))
	}
	// Lowest-count patterns (the first 100 by index) must be evicted.
	for i := 0; i < total-maxLiveErrorPatternsPerRun; i++ {
		sig := fmt.Sprintf("error signature id%dx", i)
		if _, ok := patterns[sig]; ok {
			t.Errorf("expected low-count pattern %q to be evicted", sig)
		}
	}
	// Highest-count patterns must survive.
	for i := total - maxLiveErrorPatternsPerRun; i < total; i++ {
		sig := fmt.Sprintf("error signature id%dx", i)
		if _, ok := patterns[sig]; !ok {
			t.Errorf("expected high-count pattern %q to survive", sig)
		}
	}
}

// TestTrimLiveErrorPatternsLockedNoOpUnderCap confirms no eviction happens while
// the map is at or below the cap.
func TestTrimLiveErrorPatternsLockedNoOpUnderCap(t *testing.T) {
	patterns := map[string]*LiveErrorPattern{}
	for i := 0; i < maxLiveErrorPatternsPerRun; i++ {
		sig := fmt.Sprintf("sig%d", i)
		patterns[sig] = &LiveErrorPattern{Signature: sig, Count: 1}
	}

	trimLiveErrorPatternsLocked(patterns)

	if len(patterns) != maxLiveErrorPatternsPerRun {
		t.Fatalf("expected no eviction at cap, got %d", len(patterns))
	}
}

// TestUpdateLiveErrorPatternsWithTrimStaysBounded drives the real
// updateLiveErrorPatterns + trim path across many scrapes, each carrying a
// distinct high-cardinality (mixed-alphanumeric) error message that survives
// signature normalization, and asserts the map never grows past the cap — the
// regression guard for the controller memory leak.
func TestUpdateLiveErrorPatternsWithTrimStaysBounded(t *testing.T) {
	patterns := map[string]*LiveErrorPattern{}
	base := time.Now()
	scrapes := maxLiveErrorPatternsPerRun + 200
	for i := 0; i < scrapes; i++ {
		observedAt := base.Add(time.Duration(i) * time.Second)
		live := &LiveMetrics{
			Timestamp: observedAt,
			Transactions: map[string]*TxMetrics{
				"checkout": {
					Errors:           1,
					LastErrorMessage: fmt.Sprintf("order id%dx not found", i),
					LastErrorAt:      observedAt,
					LastStatusCode:   500,
				},
			},
		}
		patterns = updateLiveErrorPatterns(patterns, live, "test-1")
		trimLiveErrorPatternsLocked(patterns)
		if len(patterns) > maxLiveErrorPatternsPerRun {
			t.Fatalf("scrape %d: patterns grew to %d, exceeds cap %d", i, len(patterns), maxLiveErrorPatternsPerRun)
		}
	}
	if len(patterns) != maxLiveErrorPatternsPerRun {
		t.Fatalf("expected map to saturate at cap %d, got %d", maxLiveErrorPatternsPerRun, len(patterns))
	}
}

// TestNormalizeArchiveErrorSignatureKeepsMixedAlnumDistinct documents the root
// cause: messages whose identifiers mix letters and digits are NOT collapsed by
// normalization, so each mints a distinct signature (hence the need for a cap).
func TestNormalizeArchiveErrorSignatureKeepsMixedAlnumDistinct(t *testing.T) {
	a := normalizeArchiveErrorSignature("order ABC123XYZ not found")
	b := normalizeArchiveErrorSignature("order DEF456QQQ not found")
	if a == b {
		t.Fatalf("expected distinct signatures for mixed-alnum ids, both normalized to %q", a)
	}
	// Pure-digit ids, by contrast, DO collapse to a single signature.
	c := normalizeArchiveErrorSignature("order 123 not found")
	d := normalizeArchiveErrorSignature("order 456 not found")
	if c != d {
		t.Fatalf("expected pure-digit ids to collapse, got %q vs %q", c, d)
	}
}

package harsynth

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
)

// The existing HAR importer reads a synthetic HAR and accounts for every
// entry it holds: each is converted, filtered as an asset or skipped.
func TestSyntheticHARRoundTripsThroughImporter(t *testing.T) {
	raw, summary := writeHAR(t, Metadata{CaptureMode: CaptureRetainedNetworkEntries}, fixtureEntries())
	for _, includeAssets := range []bool{true, false} {
		include := includeAssets
		result, err := har.Convert(raw, har.ConvertOptions{ScriptName: "synthetic", IncludeStaticAssets: &include})
		if err != nil {
			t.Fatalf("import (assets %v): %v", include, err)
		}
		stats := result.Stats
		accounted := stats.ConvertedActions + stats.FilteredAssets + stats.SkippedRequests
		if int64(stats.TotalRequests) != summary.Entries || int64(accounted) != summary.Entries {
			t.Fatalf("import (assets %v) accounts for %+v, HAR holds %d entries", include, stats, summary.Entries)
		}
		if int64(accounted) != summary.Entries {
			t.Fatalf("import (assets %v) stats %+v do not add up to %d entries", include, stats, summary.Entries)
		}
		if include && int64(stats.ConvertedActions) != summary.Entries {
			t.Fatalf("import with assets converted %d of %d entries (%+v, %v)", stats.ConvertedActions, summary.Entries, stats, result.Warnings)
		}
	}
}

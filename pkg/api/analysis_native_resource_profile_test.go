package api

import (
	"context"
	"encoding/json"
	"errors"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeAnalysisResourceConfigurationIsExplicitBoundedAndPrecedesCacheAllocation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "3GiB")
	t.Setenv("APEXLOAD_ANALYSIS_SPILL_LIMIT", "128MiB")
	_, p, dir, err := beginNativeAnalysisPipeline(context.Background(), 12, 12)
	if p != nil || dir != "" || !errors.Is(err, analytics.ErrResourceConfiguration) {
		t.Fatal("invalid requested resource profile was admitted", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_analysis-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid profile performed cache allocation", err)
	}
}
func TestNativeAnalysisCapabilitiesReportConfiguredResourcesWithoutChangingBuildCapability(t *testing.T) {
	for _, tc := range []struct {
		name, memory, spill, wantMemory, wantSpill string
		accepted                                   bool
	}{
		{"defaults", "", "", "2GiB", "8GiB", true}, {"smaller", "256MiB", "128MiB", "256MiB", "128MiB", true}, {"invalid", "private-invalid-request", "128MiB", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", tc.memory)
			t.Setenv("APEXLOAD_ANALYSIS_SPILL_LIMIT", tc.spill)
			w := httptest.NewRecorder()
			(&Server{}).handleAnalysisCapabilities(w, httptest.NewRequest("GET", "/api/v1/analysis/capabilities", nil))
			var got AnalysisCapabilitiesResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.MemoryLimit != tc.wantMemory || got.SpillLimit != tc.wantSpill || got.ResourceConfigurationAccepted != tc.accepted || got.NativeParquet != analytics.Available() {
				t.Fatal("declared configured resources changed", got)
			}
		})
	}
	t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "256MiB")
	t.Setenv("APEXLOAD_ANALYSIS_SPILL_LIMIT", "128MiB")
	profile, err := nativeAnalysisResourceProfile()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "1GiB")
	limits, err := profile.Limits()
	if err != nil || limits.MemoryBytes != 256<<20 || limits.SpillBytes != 128<<20 {
		t.Fatal("ambient change mutated captured profile", limits, err)
	}
}

func TestNativeAnalysisResourceAdmissionChecksActualDeadlineBeforeCacheIO(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "256MiB")
	t.Setenv("APEXLOAD_ANALYSIS_SPILL_LIMIT", "128MiB")
	ctx := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	_, p, dir, err := beginNativeAnalysisPipeline(ctx, 12, 12)
	if p != nil || dir != "" || err == nil {
		t.Fatal("expired actual caller admitted resource work", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_analysis-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired authority allocated cache", err)
	}
}

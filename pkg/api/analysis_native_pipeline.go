package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

func nativeAnalysisCacheRoot() (string, error) {
	return nativeAnalysisCacheRootForBackend(currentArchiveBackend())
}
func nativeAnalysisCacheRootForBackend(backend archiveBackendRuntime) (string, error) {
	root := filepath.Join(backend.LocalRoot, "_analysis-cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	// No mtime/lease-based removal. Foreign or unproven old attempt ownership
	// requires an explicit qualified incarnation/reap reconciler.
	return root, nil
}
func beginNativeAnalysisPipeline(ctx context.Context, inputBytes, outputBytes int64) (context.Context, *analytics.NativePipeline, string, error) {
	resources, err := nativeAnalysisResourceProfile()
	if err != nil {
		return ctx, nil, "", err
	}
	limits, err := resources.Limits()
	if err != nil {
		return ctx, nil, "", err
	}
	if !analysisQueryContextLive(ctx) {
		return ctx, nil, "", errors.New("analytical admission authority expired")
	}
	backend := currentArchiveBackend()
	if err := backend.validate(); err != nil {
		return ctx, nil, "", err
	}
	ctx = context.WithValue(ctx, nativeAnalysisArtifactBackendKey{}, backend)
	root, err := nativeAnalysisCacheRootForBackend(backend)
	if err != nil {
		return ctx, nil, "", err
	}
	ctx, p, err := analytics.ReserveNativePipeline(ctx, root, analytics.PipelinePlan{InputBytes: inputBytes, OutputBytes: outputBytes, StagingBytes: inputBytes, MemoryBytes: limits.MemoryBytes, SpillBytes: limits.SpillBytes})
	if err != nil {
		return ctx, nil, "", err
	}
	dir, err := p.CreateStagingDirectory(root)
	if err != nil {
		_ = p.Release()
		return ctx, nil, "", err
	}
	return ctx, p, dir, nil
}
func closeNativeAnalysisPipeline(p *analytics.NativePipeline, dir string) error {
	if p == nil {
		return nil
	}
	// Pipeline release succeeds only when every attempt/reader is physically
	// closed and publication reconciliation is definitive. Unproven ownership
	// retains the private staging directory and the controller-wide reservation.
	// The owned continuation removes only its recorded staging directories before
	// releasing admission. No pathname-based second delete follows that release.
	return p.Release()
}
func nativeNormalizationGeometry(files, sizes int64) (int64, error) {
	if files < 1 || files > 2048 || sizes < 12 || sizes > analysisImportMaxBytes {
		return 0, errors.New("invalid analytical geometry")
	}
	output := int64(20 << 30)
	if sizes <= (20<<30)/4 {
		output = max(files*(1<<20), sizes*4)
	}
	if output > 20<<30 {
		return 0, errors.New("analytical output quota exceeds limit")
	}
	return output, nil
}

func nativeAnalysisResourceProfile() (analytics.NativeResourceProfile, error) {
	return analytics.ApproveNativeResourceProfile(os.Getenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT"), os.Getenv("APEXLOAD_ANALYSIS_SPILL_LIMIT"))
}

func nativeAnalysisResourceCapability() (string, string, bool) {
	profile, err := nativeAnalysisResourceProfile()
	if err != nil {
		return "", "", false
	}
	limits, err := profile.Limits()
	if err != nil {
		return "", "", false
	}
	return nativeAnalysisBudgetDisplay(limits.MemoryBytes), nativeAnalysisBudgetDisplay(limits.SpillBytes), true
}
func nativeAnalysisBudgetDisplay(value int64) string {
	for _, unit := range []struct {
		name  string
		scale int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}} {
		if value%unit.scale == 0 {
			return strconv.FormatInt(value/unit.scale, 10) + unit.name
		}
	}
	return ""
}

package prometheus

import (
	"strings"
	"testing"
)

func TestInferenceMetricPacksValidateAndRenderVariants(t *testing.T) {
	packs := InferenceMetricPacks()
	if len(packs) != 7 {
		t.Fatalf("expected 7 inference metric packs, got %d", len(packs))
	}
	for _, pack := range packs {
		if len(pack.Queries) == 0 || len(pack.Queries) > 20 {
			t.Fatalf("pack %s has %d queries, want 1..20", pack.ID, len(pack.Queries))
		}
		queries, err := pack.BuildQueries(PackRenderOptions{})
		if err != nil {
			t.Fatalf("pack %s BuildQueries failed: %v", pack.ID, err)
		}
		sel := Selection{
			ProjectID: "default",
			Queries:   queries,
		}
		if err := sel.Validate(); err != nil {
			t.Fatalf("pack %s failed Selection.Validate(): %v", pack.ID, err)
		}

		// Verify exported_pod override also validates cleanly.
		overridden, err := pack.BuildQueries(PackRenderOptions{PodLabel: "exported_pod"})
		if err != nil {
			t.Fatalf("pack %s BuildQueries(exported_pod) failed: %v", pack.ID, err)
		}
		selOverridden := Selection{
			ProjectID: "default",
			Queries:   overridden,
		}
		if err := selOverridden.Validate(); err != nil {
			t.Fatalf("pack %s exported_pod failed Selection.Validate(): %v", pack.ID, err)
		}
	}

	// Verify vLLM fallback metric selection when legacy gpu_cache_usage_perc is discovered.
	vllm, ok := GetMetricPack("vllm")
	if !ok {
		t.Fatalf("expected vllm pack")
	}
	legacyQueries, err := vllm.BuildQueries(PackRenderOptions{
		DiscoveredMetrics: []string{"vllm:gpu_cache_usage_perc"},
		PodLabel:          "exported_pod",
	})
	if err != nil {
		t.Fatalf("vllm legacy BuildQueries failed: %v", err)
	}
	if !strings.Contains(legacyQueries[0].Expression, "vllm:gpu_cache_usage_perc") || !strings.Contains(legacyQueries[0].Expression, "exported_pod") {
		t.Fatalf("expected legacy vllm:gpu_cache_usage_perc and exported_pod in expression, got %q", legacyQueries[0].Expression)
	}
}

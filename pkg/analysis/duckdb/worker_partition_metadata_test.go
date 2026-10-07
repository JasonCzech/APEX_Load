package duckdb

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestWorkerPartitionMetadataCannotRewritePhysicalPopulation(t *testing.T) {
	b := workerTestBootstrap(t)
	op := workerTestRequest(b)
	for name, change := range map[string]func(*workerNormalizeResult){
		"missing-proof":   func(r *workerNormalizeResult) { r.Outputs[0].Partition = nil },
		"row-count":       func(r *workerNormalizeResult) { r.Outputs[0].Partition.Rows++ },
		"missing-count":   func(r *workerNormalizeResult) { r.Outputs[0].Partition.Missing++ },
		"mapping-version": func(r *workerNormalizeResult) { r.Outputs[0].Partition.IdentityVersion = 2 },
		"semantic-kind":   func(r *workerNormalizeResult) { r.Outputs[0].Partition.Kind = analysis.KindCounter },
		"canonical-unit":  func(r *workerNormalizeResult) { r.Outputs[0].Partition.Unit = "s" },
		"nanosecond-time": func(r *workerNormalizeResult) {
			v := r.Outputs[0].Partition.MinTime.Add(time.Nanosecond)
			r.Outputs[0].Partition.MinTime = &v
		},
		"rejection-proof": func(r *workerNormalizeResult) { r.Outputs[1].Partition = r.Outputs[0].Partition },
		"legacy-source-rebind": func(r *workerNormalizeResult) {
			r.Outputs[0].Partition.Identity = &analysis.NormalizedPartitionIdentity{Source: "foreign", Dimensions: map[string]string{}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := workerTestNormalizeResult(b)
			change(r.Normalize)
			if r.validate(b, op) == nil {
				t.Fatal("rewritten physical summary accepted")
			}
		})
	}
	r := workerTestNormalizeResult(b)
	frame := workerFrame{Identity: b.Identity, Kind: "result", Result: &r}
	body, err := marshalWorkerJSON(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded workerFrame
	if err = decodeWorkerJSON(body, &decoded); err != nil || decoded.Result.validate(b, op) != nil {
		t.Fatal("typed physical summary round trip", err)
	}
	for _, corrupted := range []string{
		strings.Replace(string(body), `"rows":2,"valid":2`, `"Rows":2,"valid":2`, 1),
		strings.Replace(string(body), `"valid":2`, `"valid":2,"valid":1`, 1),
	} {
		if err := decodeWorkerJSON([]byte(corrupted), &decoded); err == nil {
			t.Fatal("ambiguous proof JSON accepted")
		}
	}
}

func TestWorkerPartitionMetadataCoarsensProofWithoutLosingRows(t *testing.T) {
	b := workerTestBootstrap(t)
	outputs := make([]workerOutput, analysis.MaxNormalizedPartitions)
	for i := range outputs {
		outputs[i] = workerTestNormalizeResult(b).Normalize.Outputs[0]
		outputs[i].Partition.IdentityVersion = 2
		dims := map[string]string{}
		for d := 0; d < 16; d++ {
			dims[string(rune('a'+d))] = strings.Repeat("\n", 128)
		}
		outputs[i].Partition.Identity = &analysis.NormalizedPartitionIdentity{Source: "agent", Host: "host", Dimensions: dims}
	}
	if _, err := analysis.MarshalBoundedAnalysisJSON(outputs, analysis.MaxPartitionManifestBytes); err == nil {
		t.Fatal("fixture did not exceed aggregate proof budget")
	}
	if err := workerBoundPartitionMetadata(outputs, "p", "d", 2); err != nil {
		t.Fatal(err)
	}
	for _, out := range outputs {
		if out.Partition.Identity != nil || out.Partition.Rows != 2 || out.Partition.Valid != 2 || out.Partition.Missing != 0 || out.Partition.MinTime == nil || out.Partition.Validate() != nil {
			t.Fatal("coarsening lost physical population")
		}
	}
	if _, err := analysis.MarshalBoundedAnalysisJSON(outputs, analysis.MaxPartitionManifestBytes); err != nil {
		t.Fatal("coarsened population still excessive", err)
	}
}

// The worker's outputs carry short handles, but the controller's manifest
// keys every partition by its full object key. Metadata that fits the
// worker's own outputs but not that manifest is coarsened until the manifest
// fits, so the import drops identities instead of failing.
func TestWorkerPartitionMetadataFitsTheControllerManifest(t *testing.T) {
	b := workerTestBootstrap(t)
	const project, dataset = "proj-a", "0123abcd-0123-4abc-8abc-0123456789ab"
	build := func(valueBytes int) []workerOutput {
		outputs := make([]workerOutput, analysis.MaxNormalizedPartitions)
		for i := range outputs {
			outputs[i] = workerTestNormalizeResult(b).Normalize.Outputs[0]
			outputs[i].Partition.IdentityVersion = 2
			dims := map[string]string{}
			for d := 0; d < 16; d++ {
				dims[string(rune('a'+d))] = strings.Repeat("v", valueBytes)
			}
			outputs[i].Partition.Identity = &analysis.NormalizedPartitionIdentity{Source: "agent", Host: "host", Dimensions: dims}
		}
		return outputs
	}
	stats := func(outputs []workerOutput) []analysis.NormalizedPartitionStats {
		out := make([]analysis.NormalizedPartitionStats, len(outputs))
		for i := range outputs {
			out[i] = *outputs[i].Partition
		}
		return out
	}
	mapping := workerTestRequest(b).Normalize.Mapping
	mapping.Version = 2
	// Keys shaped like the controller's: hashed project, dataset, owner and part name.
	manifest := func(outputs []workerOutput) error {
		parts := make([]analysis.NormalizedPartition, len(outputs))
		for i, out := range outputs {
			key := fmt.Sprintf("datasets/v1/%064x/%s/normalized-%s-part-%04d-0000.parquet", i, dataset, dataset, i)
			parts[i] = analysis.NormalizedPartition{ObjectKey: key, SHA256: out.SHA256, Bytes: out.Bytes, Stats: *out.Partition}
		}
		_, err := analysis.NewNormalizedPartitionManifest(project, dataset, mapping, parts)
		return err
	}
	// The densest identities the worker's own outputs admit.
	var outputs []workerOutput
	for valueBytes := 128; valueBytes >= 1 && outputs == nil; valueBytes-- {
		candidate := build(valueBytes)
		if _, err := analysis.MarshalBoundedAnalysisJSON(candidate, analysis.MaxPartitionManifestBytes); err == nil {
			outputs = candidate
		}
	}
	if outputs == nil {
		t.Fatal("fixture: no identity size fits the worker's outputs")
	}
	if manifest(outputs) == nil || analysis.PartitionStatsFitManifest(project, dataset, 2, stats(outputs)) {
		t.Fatal("fixture: the controller's manifest admits what the worker's outputs admit")
	}
	if err := workerBoundPartitionMetadata(outputs, project, dataset, 2); err != nil {
		t.Fatal(err)
	}
	for _, out := range outputs {
		if out.Partition.Identity != nil || out.Partition.Rows != 2 {
			t.Fatal("metadata was not coarsened, or lost its population")
		}
	}
	if err := manifest(outputs); err != nil {
		t.Fatalf("the controller's manifest refused metadata the worker accepted: %v", err)
	}
}

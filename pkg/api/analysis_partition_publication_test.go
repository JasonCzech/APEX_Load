package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

func TestNativePartitionPublicationRequiresCompleteAlignedProducerPopulation(t *testing.T) {
	in, _ := analysisPartitionPlanningFixture(t)
	makeResult := func() analytics.NormalizeResult {
		result := analytics.NormalizeResult{Files: []string{"part-left.parquet", "part-right.parquet"}, Accepted: 2}
		for i, p := range in.PartitionManifest.Partitions {
			result.Partitions = append(result.Partitions, analytics.NormalizedFilePartition{File: result.Files[i], Bytes: p.Bytes, SHA256: p.SHA256, Stats: p.Stats.Clone()})
		}
		return result
	}
	for _, tc := range []struct {
		name   string
		change func(*analytics.NormalizeResult)
	}{
		{"missing", func(r *analytics.NormalizeResult) { r.Partitions = nil }},
		{"reordered", func(r *analytics.NormalizeResult) {
			r.Partitions[0], r.Partitions[1] = r.Partitions[1], r.Partitions[0]
		}},
		{"duplicate", func(r *analytics.NormalizeResult) { r.Files[1] = r.Files[0]; r.Partitions[1] = r.Partitions[0] }},
		{"wrong-hash", func(r *analytics.NormalizeResult) { r.Partitions[0].SHA256 = "invalid" }},
		{"wrong-version", func(r *analytics.NormalizeResult) { r.Partitions[0].Stats.IdentityVersion = 1 }},
		{"row-population", func(r *analytics.NormalizeResult) { r.Accepted = 1 }},
		{"extra-population", func(r *analytics.NormalizeResult) { r.Accepted = 3 }},
		{"oversized", func(r *analytics.NormalizeResult) { r.Partitions[0].Bytes = analysisImportMaxBytes }},
		{"rejection-overlap", func(r *analytics.NormalizeResult) { r.RejectionFiles = []string{r.Files[0]} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := makeResult()
			tc.change(&r)
			if _, err := analysisNormalizedOutputProofs(*in, r); err == nil {
				t.Fatal("invalid producer proof admitted")
			}
		})
	}
	r := makeResult()
	proofs, err := analysisNormalizedOutputProofs(*in, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Partitions[0].Stats.Identity.Dimensions["region"] = "caller edit"
	if proofs[r.Files[0]].Stats.Identity.Dimensions["region"] != "left" {
		t.Fatal("producer proof retained mutable metadata")
	}
}

func TestNativePartitionPublicationBindsCompleteDurableCopy(t *testing.T) {
	in, _ := analysisPartitionPlanningFixture(t)
	p := in.PartitionManifest.Partitions[0]
	proof := analytics.NormalizedFilePartition{File: "owned-output", Bytes: p.Bytes, SHA256: p.SHA256, Stats: p.Stats}
	for _, tc := range []struct {
		name  string
		bytes int64
		hash  string
	}{
		{"short", p.Bytes - 1, p.SHA256}, {"long", p.Bytes + 1, p.SHA256}, {"changed", p.Bytes, strings.Repeat("f", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := analysisCopiedPartition(p.ObjectKey, proof, tc.bytes, tc.hash); err == nil {
				t.Fatal("durable artifact mismatch accepted")
			}
		})
	}
	got, err := analysisCopiedPartition(p.ObjectKey, proof, p.Bytes, p.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := analysis.NewNormalizedPartitionManifest(in.ProjectID, in.ID, *in.Mapping, []analysis.NormalizedPartition{got, in.PartitionManifest.Partitions[1]})
	if err != nil {
		t.Fatal(err)
	}
	if err = manifest.MatchesArtifacts(in.ProjectID, in.ID, manifest.MappingSHA256, in.Mapping.Version, in.Normalized, in.NormalizedSHA256, in.Accepted); err != nil {
		t.Fatal("parent-bound proof differs", err)
	}
}

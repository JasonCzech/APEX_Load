package api

import (
	"errors"
	"io"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The child descriptor proof is necessary but insufficient for publication.
// The parent binds each summary to its own complete immutable artifact copy.
func analysisNormalizedOutputProofs(in storage.AnalysisImport, result analytics.NormalizeResult) (map[string]analytics.NormalizedFilePartition, error) {
	if in.Mapping == nil || in.PartitionManifestVersion != 0 && in.PartitionManifestVersion != analysis.PartitionManifestVersion || len(result.Files) < 1 || len(result.Files) > analysis.MaxNormalizedPartitions || len(result.Partitions) != len(result.Files) || result.Accepted < 0 {
		return nil, analysis.ErrPartitionManifest
	}
	if _, err := analysis.MarshalBoundedAnalysisJSON(result, 2<<20); err != nil {
		return nil, err
	}
	proofs := make(map[string]analytics.NormalizedFilePartition, len(result.Files))
	var rows, totalBytes int64
	for i, file := range result.Files {
		p := result.Partitions[i]
		if file == "" || p.File != file || p.Bytes < 12 || p.Bytes > analysisImportMaxBytes-totalBytes || !analysisDatasetArtifactDigestValid(p.SHA256) || p.Stats.Validate() != nil || p.Stats.IdentityVersion != in.Mapping.Version || p.Stats.Rows > result.Accepted-rows {
			return nil, analysis.ErrPartitionManifest
		}
		if _, exists := proofs[file]; exists {
			return nil, analysis.ErrPartitionManifest
		}
		p.Stats = p.Stats.Clone()
		proofs[file] = p
		rows += p.Stats.Rows
		totalBytes += p.Bytes
	}
	if rows != result.Accepted {
		return nil, analysis.ErrPartitionManifest
	}
	for _, file := range result.RejectionFiles {
		if _, exists := proofs[file]; exists {
			return nil, analysis.ErrPartitionManifest
		}
	}
	return proofs, nil
}

type analysisOutputCountingReader struct {
	reader io.Reader
	bytes  int64
}

func (r *analysisOutputCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}
func analysisCopiedPartition(key string, p analytics.NormalizedFilePartition, bytes int64, digest string) (analysis.NormalizedPartition, error) {
	if bytes != p.Bytes || digest != p.SHA256 {
		return analysis.NormalizedPartition{}, errors.New("normalized output copy differs from producer proof")
	}
	return analysis.NormalizedPartition{ObjectKey: key, Bytes: bytes, SHA256: digest, Stats: p.Stats.Clone()}, nil
}

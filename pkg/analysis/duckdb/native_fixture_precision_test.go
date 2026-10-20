//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// These existing numerical/window fixtures contain SQL decimal literals. Their
// purpose is summary/histogram/time semantics, so explicitly consent to their
// known fixture data under the new admission contract. This helper is test-only;
// consent/refusal tests call Normalize directly and never pass through here.
// Inputs with no DECIMAL risk keep their original mapping/identity version.
func normalizeConsentedNativeFixture(t *testing.T, e *Engine, ctx context.Context, request NormalizeRequest) (NormalizeResult, error) {
	t.Helper()
	candidate := request
	candidate.Mapping.Version = 3
	candidate.Mapping.PrecisionPolicy = analysis.PrecisionAllowFloat64Rounding
	inputs := make([]analysis.ParquetPrecisionInput, 0, len(request.Files))
	for _, path := range request.Files {
		schema, err := analysis.InspectParquet(path)
		if err != nil {
			return e.Normalize(ctx, request)
		}
		f, err := os.Open(path)
		if err != nil {
			return e.Normalize(ctx, request)
		}
		digest := sha256.New()
		_, err = io.Copy(digest, f)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("fixture hash: %v / %v", err, closeErr)
		}
		inputs = append(inputs, analysis.ParquetPrecisionInput{SHA256: hex.EncodeToString(digest.Sum(nil)), Schema: schema})
	}
	review, err := analysis.ReviewParquetPrecision(candidate.Mapping, inputs)
	if err != nil || len(review.Risks) == 0 {
		return e.Normalize(ctx, request)
	}
	candidate.PrecisionAcknowledgement = review.SHA256
	return e.Normalize(ctx, candidate)
}

// Query fixtures explicitly select their retained identity schema, just as the
// controller selects it from the immutable import mapping. Mapping v3 retains
// identity schema 2; default/legacy query version 0 must not be relabeled.
func nativeFixtureIdentityVersion(t *testing.T, e *Engine, files []string) int {
	t.Helper()
	version := 0
	for _, path := range files {
		schema, err := analysis.InspectParquet(path)
		if err != nil {
			t.Fatal(err)
		}
		hasIdentity := false
		for _, col := range schema.Columns {
			if len(col.PathComponents) == 1 && col.PathComponents[0] == "identity_version" {
				hasIdentity = true
			}
		}
		found := 1
		if hasIdentity {
			err = e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
				return db.QueryRowContext(ctx, "SELECT identity_version FROM read_parquet("+literal(path)+", hive_partitioning=false) LIMIT 1").Scan(&found)
			})
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if found != 1 && found != 2 {
			t.Fatalf("unexpected fixture identity version %d", found)
		}
		if version != 0 && version != found {
			t.Fatal("query fixture mixes identity schema versions")
		}
		version = found
	}
	return version
}

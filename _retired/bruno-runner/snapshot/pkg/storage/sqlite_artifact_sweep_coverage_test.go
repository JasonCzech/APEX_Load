package storage

import (
	"sort"
	"strings"
	"testing"
)

// TestArtifactSweepCoversEveryArtifactReference derives the set of columns that
// reference bruno_source_artifacts from the schema itself and requires each one
// to appear in the orphan sweep.
//
// deleteUnreferencedImportArtifactsSQL states this as a rule for whoever adds
// the next referencing table, but a comment cannot fail a build. Missing one is
// silent and destructive in the worst direction: the sweep would delete
// encrypted source artifacts that the new table still points at, and nothing
// surfaces until someone tries to read that source or roll back an import --
// long after the delete that caused it, with no evidence connecting the two.
//
// The expectation is derived rather than hardcoded on purpose. A hardcoded list
// would have to be updated by the same person who just forgot to update the
// sweep, so it would fail exactly when it was needed least. postgresForeignKeySpecs
// is reused because it already parses both schemaCreateTableStatements and
// schemaColumnMigrations -- a reference added by a later ALTER TABLE counts too.
func TestArtifactSweepCoversEveryArtifactReference(t *testing.T) {
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatalf("derive foreign keys from the schema: %v", err)
	}

	var referencing []string
	for _, spec := range specs {
		if !strings.EqualFold(spec.references, "bruno_source_artifacts") {
			continue
		}
		referencing = append(referencing, strings.ToLower(spec.table)+"."+strings.ToLower(spec.column))
	}
	if len(referencing) == 0 {
		t.Fatal("no foreign keys onto bruno_source_artifacts were found; the schema or the parser changed " +
			"in a way that makes this test vacuous rather than passing")
	}

	sweep := strings.Join(strings.Fields(strings.ToLower(deleteUnreferencedImportArtifactsSQL)), " ")
	var missing []string
	for _, reference := range referencing {
		table, column, _ := strings.Cut(reference, ".")
		// Match the table and column together. sanitized_artifact_id exists in
		// two different tables, so checking the column alone would pass while one
		// of those tables went entirely unswept.
		if !strings.Contains(sweep, "select "+column+" from "+table) {
			missing = append(missing, reference)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("these columns reference bruno_source_artifacts but are absent from the orphan sweep, "+
			"so deleting a script would remove artifacts they still point at: %s\n\nsweep:\n%s",
			strings.Join(missing, ", "), deleteUnreferencedImportArtifactsSQL)
	}
}

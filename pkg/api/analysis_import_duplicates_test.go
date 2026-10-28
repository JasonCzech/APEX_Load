package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// An import's files with the same content are found by their whole-file
// digest, or else by size and ordered chunk digests; a file whose chunk
// digests are unknown is never called a duplicate.
func TestDuplicateAnalysisImportFiles(t *testing.T) {
	chunk := func(offset, size int64, digest string) storage.AnalysisChunk {
		return storage.AnalysisChunk{Offset: offset, Size: size, SHA256: digest}
	}
	for name, tc := range map[string]struct {
		files       []storage.AnalysisImportFile
		first, then string
	}{
		"distinct": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", Size: 10, Chunks: []storage.AnalysisChunk{chunk(0, 10, "x")}},
			{RelativePath: "b", Size: 10, Chunks: []storage.AnalysisChunk{chunk(0, 10, "y")}}}},
		"same chunks": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", Size: 20, Chunks: []storage.AnalysisChunk{chunk(0, 10, "x"), chunk(10, 10, "y")}},
			{RelativePath: "c", Size: 5, Chunks: []storage.AnalysisChunk{chunk(0, 5, "z")}},
			{RelativePath: "b", Size: 20, Chunks: []storage.AnalysisChunk{chunk(0, 10, "x"), chunk(10, 10, "y")}}}, first: "a", then: "b"},
		"same whole digest": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", SHA256: "d"}, {RelativePath: "b", SHA256: "d"}}, first: "a", then: "b"},
		"single-chunk vs multi-chunk with assembled whole digest": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", Size: 20, Chunks: []storage.AnalysisChunk{chunk(0, 20, "whole-digest")}},
			{RelativePath: "b", Size: 20, SHA256: "whole-digest", Chunks: []storage.AnalysisChunk{chunk(0, 8, "c1"), chunk(8, 12, "c2")}}}, first: "a", then: "b"},
		"different chunk boundaries with assembled whole digest": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", Size: 20, SHA256: "whole-digest", Chunks: []storage.AnalysisChunk{chunk(0, 8, "c1"), chunk(8, 12, "c2")}},
			{RelativePath: "b", Size: 20, SHA256: "whole-digest", Chunks: []storage.AnalysisChunk{chunk(0, 5, "d1"), chunk(5, 8, "d2"), chunk(13, 7, "d3")}}}, first: "a", then: "b"},
		"chunk order differs": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", Size: 20, Chunks: []storage.AnalysisChunk{chunk(0, 10, "x"), chunk(10, 10, "y")}},
			{RelativePath: "b", Size: 20, Chunks: []storage.AnalysisChunk{chunk(0, 10, "y"), chunk(10, 10, "x")}}}},
		"unknown chunk digest": {files: []storage.AnalysisImportFile{
			{RelativePath: "a", Size: 10, Chunks: []storage.AnalysisChunk{chunk(0, 10, "")}},
			{RelativePath: "b", Size: 10, Chunks: []storage.AnalysisChunk{chunk(0, 10, "")}}}},
	} {
		first, then, found := duplicateAnalysisImportFiles(tc.files)
		if found != (tc.first != "") || first != tc.first || then != tc.then {
			t.Errorf("%s: (%q, %q, %v)", name, first, then, found)
		}
	}
}

func TestAnalysisCommitRejectsMultiChunkDuplicatesAcrossChunkBoundaries(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newTimeslotServer(t)
	imports := store.(storage.AnalysisImportStore)
	writers := store.(storage.AnalysisUploadWriterStore)

	content := bytes.Repeat([]byte("PAR1-identical-telemetry-payload-"), 64)
	wantSum := sha256.Sum256(content)
	wantDigest := hex.EncodeToString(wantSum[:])

	uploadSplits := func(t *testing.T, importID string, fileIndex int, data []byte, splits []int) {
		t.Helper()
		offset := int64(0)
		for _, size := range splits {
			current, err := imports.GetAnalysisImport(importID)
			if err != nil {
				t.Fatal(err)
			}
			owner, ok := acquireAnalysisUploadOwner()
			if !ok {
				t.Fatal("acquire upload owner")
			}
			ctx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, owner)
			chunkBytes := data[offset : offset+int64(size)]
			chunkSum := sha256.Sum256(chunkBytes)
			req := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
			res := httptest.NewRecorder()
			server.writeAnalysisUploadChunk(res, req, writers, *current, current.Files[fileIndex], offset, chunkBytes, hex.EncodeToString(chunkSum[:]))
			owner.endRequest()
			if res.Code != 200 {
				t.Fatalf("upload offset %d size %d: %d %s", offset, size, res.Code, res.Body.String())
			}
			offset += int64(size)
		}
	}

	mapping := analysis.ParquetMapping{
		Version:           1,
		Timestamp:         []string{"ts"},
		TimestampEncoding: "unix_s",
		Timezone:          "UTC",
		Measures:          []analysis.ParquetMeasure{{Value: []string{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}},
	}

	for _, tc := range []struct {
		name       string
		firstName  string
		firstSplit []int
		thenName   string
		thenSplit  []int
	}{
		{
			name:       "two-chunks-vs-three-chunks",
			firstName:  "two-chunks.parquet",
			firstSplit: []int{700, len(content) - 700},
			thenName:   "three-chunks.parquet",
			thenSplit:  []int{311, 809, len(content) - 311 - 809},
		},
		{
			name:       "single-chunk-vs-three-chunks",
			firstName:  "single.parquet",
			firstSplit: []int{len(content)},
			thenName:   "three-chunks.parquet",
			thenSplit:  []int{400, 600, len(content) - 1000},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := imports.SaveAnalysisImport(storage.AnalysisImport{
				ID:                  uuid.NewString(),
				ProjectID:           "proj-a",
				Name:                tc.name,
				State:               "uploading",
				UploadWriterVersion: storage.AnalysisUploadWriterVersion,
				Files: []storage.AnalysisImportFile{
					{ID: uuid.NewString(), RelativePath: tc.firstName, Size: int64(len(content))},
					{ID: uuid.NewString(), RelativePath: tc.thenName, Size: int64(len(content))},
				},
			}, 0)
			if err != nil {
				t.Fatal(err)
			}
			uploadSplits(t, in.ID, 0, content, tc.firstSplit)
			uploadSplits(t, in.ID, 1, content, tc.thenSplit)

			saved, err := imports.GetAnalysisImport(in.ID)
			if err != nil {
				t.Fatal(err)
			}
			for i, file := range saved.Files {
				if got := analysisWholeFileDigest(file); got != wantDigest {
					t.Fatalf("file %d (%s) whole-file digest = %q, want %q (file.SHA256=%q, chunks=%d)", i, file.RelativePath, got, wantDigest, file.SHA256, len(file.Chunks))
				}
			}
			if len(tc.thenSplit) > 1 && saved.Files[1].SHA256 != wantDigest {
				t.Fatalf("multi-chunk file SHA256 = %q, want %q", saved.Files[1].SHA256, wantDigest)
			}

			saved.State = "mapped"
			saved.Mapping = &mapping
			saved, err = imports.SaveAnalysisImport(*saved, saved.Revision)
			if err != nil {
				t.Fatal(err)
			}

			first, second, ok := duplicateAnalysisImportFiles(server.resolveAnalysisCommitFiles(t.Context(), saved))
			if !ok || first != tc.firstName || second != tc.thenName {
				t.Fatalf("duplicateAnalysisImportFiles = (%q, %q, %v), want (%q, %q, true)", first, second, ok, tc.firstName, tc.thenName)
			}
			wantMessage := fmt.Sprintf("files %q and %q have the same content; create a new import without one of them", tc.firstName, tc.thenName)
			gotMessage := fmt.Sprintf("files %q and %q have the same content; create a new import without one of them", first, second)
			if gotMessage != wantMessage {
				t.Fatalf("got %q, want %q", gotMessage, wantMessage)
			}

			// Also verify resolveAnalysisCommitFiles backfills whole-file SHA-256 if cleared on a multi-chunk file.
			cleared := *saved
			cleared.Files = append([]storage.AnalysisImportFile(nil), saved.Files...)
			cleared.Files[1].SHA256 = ""
			first2, second2, ok2 := duplicateAnalysisImportFiles(server.resolveAnalysisCommitFiles(t.Context(), &cleared))
			if !ok2 || first2 != tc.firstName || second2 != tc.thenName {
				t.Fatalf("backfilled duplicateAnalysisImportFiles = (%q, %q, %v), want (%q, %q, true)", first2, second2, ok2, tc.firstName, tc.thenName)
			}
		})
	}
}


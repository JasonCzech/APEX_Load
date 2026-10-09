package api

import (
	"testing"

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

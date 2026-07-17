package bruno

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"math"
	"strings"
	"testing"
)

func TestExceedsByteBudgetRejectsOverflowAndBoundaryViolations(t *testing.T) {
	tests := []struct {
		name             string
		total, next, max int64
		want             bool
	}{
		{name: "within", total: 4, next: 6, max: 10},
		{name: "over", total: 5, next: 6, max: 10, want: true},
		{name: "overflow", total: math.MaxInt64 - 1, next: 2, max: math.MaxInt64, want: true},
		{name: "negative total", total: -1, next: 1, max: 10, want: true},
		{name: "negative next", total: 0, next: -1, max: 10, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exceedsByteBudget(test.total, test.next, test.max); got != test.want {
				t.Fatalf("exceedsByteBudget(%d, %d, %d) = %v, want %v", test.total, test.next, test.max, got, test.want)
			}
		})
	}
}

func TestInspectRejectsArchiveBeforeMaterializationCopiesExceedBudget(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	request := []byte("info: {name: Request, type: http}\nhttp: {url: https://example.test}\n")
	fixture := bytes.Repeat([]byte("memory-budget-fixture-"), 8<<10)
	for name, content := range map[string][]byte{"request.yml": request, "fixture.txt": fixture} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := Inspect(Input{
		Filename: "bounded.tar.gz",
		Content:  archive.Bytes(),
		Limits: Limits{
			MaxExpandedBytes:     1 << 20,
			MaxMaterializedBytes: 1 << 20,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "expanded size exceeds 32768 bytes") {
		t.Fatalf("materialization admission error = %v", err)
	}
}

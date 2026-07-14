package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type downsampleFixture struct {
	Index int     `json:"index"`
	Value float64 `json:"value"`
}

func TestReadJSONLDownsampledPreservesEndpointsAndExtrema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for index := 0; index < 100; index++ {
		value := 100 + float64(index%7)
		if index == 37 {
			value = 10_000
		}
		if index == 62 {
			value = -10_000
		}
		if err := encoder.Encode(downsampleFixture{Index: index, Value: value}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	items, total, err := readJSONLDownsampled(path, 100, 10, func(item *downsampleFixture) float64 {
		return item.Value
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 {
		t.Fatalf("total = %d, want 100", total)
	}
	if len(items) > 10 {
		t.Fatalf("returned %d items, limit 10", len(items))
	}
	seen := make(map[int]bool, len(items))
	for _, item := range items {
		seen[item.Index] = true
	}
	for _, required := range []int{0, 37, 62, 99} {
		if !seen[required] {
			t.Fatalf("expected sampled indexes to retain %d; got %#v", required, seen)
		}
	}
}

func TestReadJSONLDownsampledRejectsInvalidLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readJSONLDownsampled[downsampleFixture](path, 1, 1); err == nil {
		t.Fatal("expected a sample-limit error")
	}
}

func TestReadJSONLDownsampledSmallLimitStillPreservesEndpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for index := 0; index < 10; index++ {
		if err := encoder.Encode(downsampleFixture{Index: index, Value: float64(index)}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	items, _, err := readJSONLDownsampled(path, 10, 2, func(item *downsampleFixture) float64 {
		return item.Value
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Index != 0 || items[1].Index != 9 {
		t.Fatalf("items = %#v, want indexes 0 and 9", items)
	}
}

package api

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNumberFromAnyRejectsNonFiniteValues(t *testing.T) {
	for name, value := range map[string]any{
		"float nan":       math.NaN(),
		"float infinity":  math.Inf(1),
		"json infinity":   json.Number("1e9999"),
		"string nan":      "NaN",
		"string infinity": "+Inf",
	} {
		t.Run(name, func(t *testing.T) {
			if parsed, ok := numberFromAny(value); ok {
				t.Fatalf("numberFromAny(%v) = %v, true; want rejection", value, parsed)
			}
		})
	}
}

func TestExternalSummaryMapBoundsInvalidMetrics(t *testing.T) {
	summary := externalSummaryMap(float64(math.MaxInt64)*2, -2, 250, math.NaN(), -1)
	if summary["transactions"] != int64(math.MaxInt64) {
		t.Fatalf("transactions = %#v, want MaxInt64", summary["transactions"])
	}
	if summary["errors"] != int64(0) {
		t.Fatalf("errors = %#v, want 0", summary["errors"])
	}
	if summary["error_pct"] != float64(100) {
		t.Fatalf("error_pct = %#v, want 100", summary["error_pct"])
	}
	if summary["avg_response_ms"] != float64(0) || summary["p95_response_ms"] != float64(0) {
		t.Fatalf("response metrics were not normalized: %#v", summary)
	}
}

func TestParsePositiveIntQueryRejectsTrailingGarbage(t *testing.T) {
	for _, raw := range []string{"10items", "1.5", "7 8"} {
		if value, err := parsePositiveIntQuery(raw); err == nil {
			t.Fatalf("parsePositiveIntQuery(%q) = %d, nil; want error", raw, value)
		}
	}
	if value, err := parsePositiveIntQuery(" 42 "); err != nil || value != 42 {
		t.Fatalf("parsePositiveIntQuery valid value = %d, %v", value, err)
	}
}

func TestInMemoryExternalResultImportBudgetRejectsByteOverflow(t *testing.T) {
	server := &Server{
		resultImports:     make(map[string]*storage.ExternalResultImport),
		resultImportBytes: maxInMemoryExternalResultImportBytes - 1,
	}
	result := &storage.ExternalResultImport{
		ID:        "result-over-budget",
		Provider:  "k6",
		RawResult: map[string]any{"value": "small"},
		CreatedAt: time.Now().UTC(),
	}
	if err := server.saveExternalResultImport(result); !errors.Is(err, errInMemoryExternalResultCapacity) {
		t.Fatalf("saveExternalResultImport error = %v, want capacity error", err)
	}
	if len(server.resultImports) != 0 {
		t.Fatalf("result count = %d, want rejected import to remain absent", len(server.resultImports))
	}
}

func TestInMemoryExternalResultImportBudgetAccountsReplacementAndDelete(t *testing.T) {
	server := &Server{resultImports: make(map[string]*storage.ExternalResultImport)}
	createdAt := time.Now().UTC()
	first := &storage.ExternalResultImport{
		ID:        "result-1",
		Provider:  "k6",
		RawResult: map[string]any{"value": "first"},
		CreatedAt: createdAt,
	}
	if err := server.saveExternalResultImport(first); err != nil {
		t.Fatalf("save first external result: %v", err)
	}
	firstBytes := server.resultImportBytes
	if firstBytes <= 0 {
		t.Fatalf("first result byte accounting = %d, want positive", firstBytes)
	}

	replacement := cloneExternalResultImport(first)
	replacement.RawResult = map[string]any{"value": "a larger replacement payload"}
	if err := server.saveExternalResultImport(replacement); err != nil {
		t.Fatalf("replace external result: %v", err)
	}
	if len(server.resultImports) != 1 {
		t.Fatalf("result count = %d, want one replacement", len(server.resultImports))
	}
	if server.resultImportBytes <= firstBytes {
		t.Fatalf("replacement byte accounting = %d, want greater than %d", server.resultImportBytes, firstBytes)
	}
	deleted, err := server.deleteExternalResultImportIfUnchanged(replacement)
	if err != nil || !deleted {
		t.Fatalf("delete replacement = %t, %v; want true, nil", deleted, err)
	}
	if server.resultImportBytes != 0 || len(server.resultImports) != 0 {
		t.Fatalf("post-delete state = %d bytes, %d results; want empty", server.resultImportBytes, len(server.resultImports))
	}
}

func TestInMemoryExternalResultImportBudgetRejectsCountOverflow(t *testing.T) {
	server := &Server{resultImports: make(map[string]*storage.ExternalResultImport, maxInMemoryExternalResultImports)}
	for index := 0; index < maxInMemoryExternalResultImports; index++ {
		id := strconv.Itoa(index)
		server.resultImports[id] = &storage.ExternalResultImport{ID: id}
	}
	result := &storage.ExternalResultImport{ID: "one-too-many", CreatedAt: time.Now().UTC()}
	if err := server.saveExternalResultImport(result); !errors.Is(err, errInMemoryExternalResultCapacity) {
		t.Fatalf("saveExternalResultImport error = %v, want capacity error", err)
	}
}

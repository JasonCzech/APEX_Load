package database

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDatabaseExtractionPreservesIntegerIdentifiers(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{"database_driver": "sqlite", "database_dsn": ":memory:"}); err != nil {
		t.Fatal(err)
	}
	defer db.Teardown(context.Background())
	result, err := db.Execute(context.Background(), &protocol.Action{
		Name: "exact ID", Type: "db_query", Target: "SELECT 9007199254740993 AS id",
		Extractors: []protocol.Extractor{
			{Name: "body_id", Source: "body", Type: "jsonpath", Expression: "$.rows[0].id"},
			{Name: "scalar_id", Source: "scalar"},
			{Name: "count", Source: "row_count"},
		},
		Assertions: []protocol.Assertion{{Type: "scalar_equals", Expected: "9007199254740993"}, {Type: "row_count", Expected: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s; body=%s", result.ErrorMessage, result.ResponseBody)
	}
	for name, want := range map[string]string{"body_id": "9007199254740993", "scalar_id": "9007199254740993", "count": "1"} {
		if got := result.Extractions[name]; got != want {
			t.Errorf("%s=%q want=%q", name, got, want)
		}
	}
}

func TestDatabaseExtractionFormatsSevenDigitNumbers(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{"database_driver": "sqlite", "database_dsn": ":memory:"}); err != nil {
		t.Fatal(err)
	}
	defer db.Teardown(context.Background())
	for _, raw := range []string{"1234567", "-1234567", "1234567.25"} {
		t.Run(raw, func(t *testing.T) {
			result, err := db.Execute(context.Background(), &protocol.Action{
				Name: "numeric ID", Type: "db_query", Target: "SELECT " + raw + " AS id",
				Extractors: []protocol.Extractor{
					{Name: "jsonpath", Source: "body", Type: "jsonpath", Expression: "$.rows[0].id", OnMiss: "fail"},
					{Name: "jmespath", Source: "body", Type: "jmespath", Expression: "rows[0].id", OnMiss: "fail"},
					{Name: "scalar", Source: "scalar"},
				},
				Assertions: []protocol.Assertion{{Type: "scalar_equals", Expected: raw}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success {
				t.Fatalf("query failed: %s; body=%s", result.ErrorMessage, result.ResponseBody)
			}
			for _, name := range []string{"jsonpath", "jmespath", "scalar"} {
				if got := result.Extractions[name]; got != raw {
					t.Errorf("%s=%q want=%q", name, got, raw)
				}
			}
		})
	}
}

func TestDatabasePayloadRejectsTrailingDocument(t *testing.T) {
	if _, err := decodeDatabasePayload(`{"scalar":1} {"scalar":2}`); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

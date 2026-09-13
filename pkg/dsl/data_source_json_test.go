package dsl

import (
	"encoding/json"
	"testing"
)

func TestDataSourceJSONPreservesExplicitHeaderMode(t *testing.T) {
	for _, header := range []bool{false, true} {
		data, err := json.Marshal(DataSource{Name: "users", Type: "csv", FirstRowHeader: header, Columns: []string{"id"}, InlineData: "value"})
		if err != nil {
			t.Fatal(err)
		}
		var source map[string]any
		if err := json.Unmarshal(data, &source); err != nil {
			t.Fatal(err)
		}
		if got, exists := source["first_row_header"]; !exists || got != header {
			t.Fatalf("header mode lost in API/editor round trip: %s", data)
		}
	}
}

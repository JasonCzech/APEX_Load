package prometheus

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDashboardPresetsValidate checks every dashboard query preset, as the
// dashboard builds it (testdata/dashboard-presets.json, kept current by
// prometheusPresets.fixture.test.js), against the controller's validation.
func TestDashboardPresetsValidate(t *testing.T) {
	data, err := os.ReadFile("testdata/dashboard-presets.json")
	if err != nil {
		t.Fatal(err)
	}
	var queries []Query
	if err := json.Unmarshal(data, &queries); err != nil {
		t.Fatal(err)
	}
	if len(queries) == 0 {
		t.Fatal("no dashboard presets")
	}
	for _, q := range queries {
		if err := (Selection{ProjectID: "presets", Queries: []Query{q}}).Validate(); err != nil {
			t.Errorf("preset %s: %v", q.ID, err)
		}
	}
}

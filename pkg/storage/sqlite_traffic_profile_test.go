package storage

import (
	"testing"
	"time"
)

func artBackedTrafficProfileFixture() map[string]any {
	return map[string]any{
		"version":        float64(1),
		"runtime_target": "tps",
		"mappings": []any{map[string]any{
			"series_key": "orders",
			"art": map[string]any{
				"basis":                 "source_series",
				"source_unit":           "seconds",
				"peak_art_ms":           float64(400),
				"estimated_peak_vusers": float64(32),
				"points":                []any{map[string]any{"offset_ms": float64(0), "art_ms": float64(400)}},
			},
		}},
		"sizing": map[string]any{
			"complete":               true,
			"profile_peak_vusers":    float64(32),
			"static_peak_vusers":     float64(0),
			"estimated_peak_vusers":  float64(32),
			"suggested_min_vusers":   float64(1),
			"suggested_max_vusers":   float64(64),
			"recommended_generators": float64(1),
		},
	}
}

func TestScenarioTrafficProfileRoundTripAndTrueNil(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/traffic-profile.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	profile := artBackedTrafficProfileFixture()
	if err := store.SaveScenario(&Scenario{ID: "traffic", Name: "Traffic", Duration: "1m", TrafficProfile: profile, CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.GetScenario("traffic")
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := got.TrafficProfile.(map[string]any)
	if !ok || decoded["runtime_target"] != "tps" {
		t.Fatalf("traffic profile = %#v", got.TrafficProfile)
	}
	sizing, ok := decoded["sizing"].(map[string]any)
	if !ok || sizing["static_peak_vusers"] != float64(0) || sizing["recommended_generators"] != float64(1) {
		t.Fatalf("ART sizing evidence = %#v", decoded["sizing"])
	}
	if err := store.SaveScenario(&Scenario{ID: "nil-profile", Name: "Nil", Duration: "1m", TrafficProfile: nil, CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	nilScenario, _, err := store.GetScenario("nil-profile")
	if err != nil {
		t.Fatal(err)
	}
	if nilScenario.TrafficProfile != nil {
		t.Fatalf("nil profile decoded as %#v", nilScenario.TrafficProfile)
	}
}

func TestTestTrafficProfileRoundTripAndTrueNil(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/traffic-profile-test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	profile := artBackedTrafficProfileFixture()
	if err := store.SaveTest(&TestRecord{ID: "traffic-run", Name: "Traffic run", Status: "QUEUED", ProjectID: DefaultProjectID, Duration: int64(time.Minute), StartTime: now, Groups: []any{}, TrafficProfile: profile}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTest("traffic-run")
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := got.TrafficProfile.(map[string]any)
	if !ok || decoded["runtime_target"] != "tps" {
		t.Fatalf("traffic profile = %#v", got.TrafficProfile)
	}
	mappings, ok := decoded["mappings"].([]any)
	if !ok || len(mappings) != 1 {
		t.Fatalf("ART mappings = %#v", decoded["mappings"])
	}
	mapping, ok := mappings[0].(map[string]any)
	if !ok {
		t.Fatalf("ART mapping = %#v", mappings[0])
	}
	art, ok := mapping["art"].(map[string]any)
	if !ok || art["source_unit"] != "seconds" || art["peak_art_ms"] != float64(400) {
		t.Fatalf("ART evidence = %#v", mapping["art"])
	}
	if err := store.SaveTest(&TestRecord{ID: "nil-profile-run", Name: "Nil run", Status: "QUEUED", ProjectID: DefaultProjectID, Duration: int64(time.Minute), StartTime: now, Groups: []any{}, TrafficProfile: nil}); err != nil {
		t.Fatal(err)
	}
	nilRun, err := store.GetTest("nil-profile-run")
	if err != nil {
		t.Fatal(err)
	}
	if nilRun.TrafficProfile != nil {
		t.Fatalf("nil profile decoded as %#v", nilRun.TrafficProfile)
	}
}

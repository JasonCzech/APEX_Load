package storage

import (
	"testing"
	"time"
)

func TestScenarioTrafficProfileRoundTripAndTrueNil(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/traffic-profile.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	profile := map[string]any{"version": float64(1), "runtime_target": "tps", "mappings": []any{map[string]any{"series_key": "orders"}}}
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
	profile := map[string]any{"version": float64(1), "runtime_target": "tps", "mappings": []any{map[string]any{"series_key": "orders"}}}
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

package java_vuser

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

func TestExactClasspathAssetsValueTypedSlice(t *testing.T) {
	cfg := map[string]any{
		"java_vuser_classpath_assets": []dsl.JavaClasspathAsset{
			{Entry: "app.jar", SHA256: "abc", StorageRef: "file:///a"},
		},
	}
	assets, err := exactClasspathAssetsValue(cfg, "java_vuser_classpath_assets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets) != 1 || assets[0].Entry != "app.jar" || assets[0].StorageRef != "file:///a" {
		t.Fatalf("unexpected assets: %#v", assets)
	}
}

func TestExactClasspathAssetsValueJSONRoundTrip(t *testing.T) {
	// Simulates the production path where the flattened cfg map has been through
	// a JSON round-trip, so the value is []any of map[string]any.
	raw := []dsl.JavaClasspathAsset{{
		Entry:      "lib/app.jar",
		FileName:   "app.jar",
		SHA256:     "deadbeef",
		SizeBytes:  1234,
		StorageRef: "file:///assets/app.jar",
	}}
	encoded, err := json.Marshal(map[string]any{"java_vuser_classpath_assets": raw})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assets, err := exactClasspathAssetsValue(decoded, "java_vuser_classpath_assets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("len = %d, want 1", len(assets))
	}
	got := assets[0]
	if got.Entry != "lib/app.jar" || got.FileName != "app.jar" || got.SHA256 != "deadbeef" || got.SizeBytes != 1234 || got.StorageRef != "file:///assets/app.jar" {
		t.Fatalf("decoded asset mismatch: %#v", got)
	}
}

func TestExactClasspathAssetsValueAbsentIsNil(t *testing.T) {
	assets, err := exactClasspathAssetsValue(map[string]any{}, "java_vuser_classpath_assets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets != nil {
		t.Fatalf("expected nil, got %#v", assets)
	}
}

func TestExactClasspathAssetsValueWrongType(t *testing.T) {
	cfg := map[string]any{"java_vuser_classpath_assets": "not-an-array"}
	if _, err := exactClasspathAssetsValue(cfg, "java_vuser_classpath_assets"); err == nil {
		t.Fatal("expected error for non-array value")
	}
}

func TestExactClasspathAssetsValueWrongElementShape(t *testing.T) {
	// A well-formed array whose element is not an object (the realistic
	// JSON-round-trip corruption case) must error, not panic or silently drop.
	cfg := map[string]any{"java_vuser_classpath_assets": []any{"just-a-string"}}
	if _, err := exactClasspathAssetsValue(cfg, "java_vuser_classpath_assets"); err == nil {
		t.Fatal("expected error for a non-object array element")
	}
}

func TestExactClasspathAssetsValueTypedSliceEnforcesCap(t *testing.T) {
	// The 256-entry cap must apply to the in-process typed-slice branch too, not
	// only the JSON []any branch.
	big := make([]dsl.JavaClasspathAsset, javarunner.MaxClasspathEntries+1)
	cfg := map[string]any{"java_vuser_classpath_assets": big}
	if _, err := exactClasspathAssetsValue(cfg, "java_vuser_classpath_assets"); err == nil {
		t.Fatal("expected typed-slice length to be capped at MaxClasspathEntries")
	}
}

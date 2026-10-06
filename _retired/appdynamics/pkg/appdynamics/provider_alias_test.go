package appdynamics

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"testing"
	"time"
)

func TestProviderAliasesPreserveAppDynamicsJSON(t *testing.T) {
	stamp := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	var evidence Diagnostic = monitorprovider.Diagnostic{ID: "incident", Kind: "violation", ApplicationID: "1", TimestampUTC: stamp}
	value, _ := json.Marshal(evidence)
	expected := `{"id":"incident","kind":"violation","application_id":"1","timestamp_utc":"2025-01-01T00:00:00Z"}`
	if string(value) != expected {
		t.Fatalf("AppDynamics projection changed: %s", value)
	}
	var coverage Coverage = monitorprovider.Coverage{Status: "complete", From: stamp, To: stamp.Add(time.Minute)}
	value, _ = json.Marshal(coverage)
	expected = `{"status":"complete","from":"2025-01-01T00:00:00Z","to":"2025-01-01T00:01:00Z","points":0,"evidence":0,"truncated":false}`
	if string(value) != expected {
		t.Fatalf("legacy coverage changed: %s", value)
	}
	var progress QueryProgress = monitorprovider.QueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true}
	value, _ = json.Marshal(progress)
	if string(value) != `{"next_query":1,"total_queries":1,"complete":true}` {
		t.Fatalf("legacy progress changed: %s", value)
	}
}

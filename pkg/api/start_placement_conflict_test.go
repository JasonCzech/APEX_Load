package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestStartPlacementConflictError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *StartPlacementConflictError
		want string
	}{
		{"nil", nil, "generator placement conflict"},
		{"empty", &StartPlacementConflictError{}, "generator placement conflict"},
		{"details", &StartPlacementConflictError{Conflicts: []LaunchConflict{
			generatorPlacementLaunchConflict("no compatible generator"),
			generatorPlacementLaunchConflict("second conflict"),
		}}, "generator_placement: no compatible generator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleStartTestPreservesPlacementConflicts(t *testing.T) {
	const projectID = "project-start-placement-conflicts"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"}, time.Second, nil,
	))
	features := governedPersistentJavaRuntimeFeatures()
	setPreflightJavaVuserInventoryEntries(server.runner, GeneratorInventoryEntry{
		Target: "gen-jvm:9090", GeneratorID: "gen-jvm", Status: "connected", DrainState: "active",
		AvailableCapacity: 100, MaxVUsers: 100, SupportedProtocols: []string{"java_vuser"}, CompatibleFeatures: features,
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol: "java_vuser", Available: true, MaxVUsers: 6, AvailableVUsers: 6,
			Features: features, OccupancyMode: generatorrpc.RuntimeOccupancyModeIdle,
		}},
	})
	run, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Aggregate placement conflict", ProjectID: projectID, Duration: "5m",
		Groups: []CreateGroupEntry{
			{Name: "first", ScriptID: approved.Script.ID, VUsers: 4},
			{Name: "second", ScriptID: approved.Script.ID, VUsers: 3},
			{Name: "third", ScriptID: approved.Script.ID, VUsers: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := server.store.GetTest(run.ID)
	if err != nil || before == nil {
		t.Fatalf("load created test: %v", err)
	}
	err = server.runner.StartTest(run.ID)
	var placementErr *StartPlacementConflictError
	if !errors.As(fmt.Errorf("wrapped: %w", err), &placementErr) || len(placementErr.Conflicts) != 2 {
		t.Fatalf("StartTest = %v, want two typed placement conflicts", err)
	}
	for _, override := range []bool{false, true} {
		body, _ := json.Marshal(StartTestOptions{OverrideCapacityShortfall: override})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/start", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("override=%v: status=%d body=%s", override, resp.Code, resp.Body)
		}
		var payload struct {
			Code              string           `json:"code"`
			TestID            string           `json:"test_id"`
			Error             string           `json:"error"`
			OverrideAvailable *bool            `json:"override_available"`
			Conflicts         []LaunchConflict `json:"blocking_conflicts"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Code != "generator_placement_conflict" || payload.TestID != run.ID || payload.OverrideAvailable == nil || *payload.OverrideAvailable || len(payload.Conflicts) != 2 {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		if payload.Error != placementErr.Error() {
			t.Fatalf("error = %q, want %q", payload.Error, placementErr.Error())
		}
		for _, conflict := range payload.Conflicts {
			if conflict.Code != "generator_placement" || conflict.Severity != "blocking" || conflict.Source != "generator_assignment" || !strings.Contains(conflict.Message, "available java_vuser runtime slots") {
				t.Fatalf("lost conflict details: %+v", conflict)
			}
		}
	}
	after, ok := server.runner.GetTest(run.ID)
	if !ok || after == nil {
		t.Fatal("rejected test disappeared")
	}
	persisted, err := server.store.GetTest(run.ID)
	if err != nil || persisted == nil {
		t.Fatalf("load rejected test: %v", err)
	}
	if after.Status != run.Status || after.StartedAt != nil || after.EndedAt != nil || after.Error != "" || persisted.Status != before.Status || persisted.StartedAt != nil || persisted.EndTime != nil || persisted.Error != "" {
		t.Fatalf("rejection mutated lifecycle: memory=%+v durable=%+v", after, persisted)
	}
	events, err := server.store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.placement.reject", Limit: 10})
	if err != nil || len(events) != 2 {
		t.Fatalf("audit events = %+v, err=%v", events, err)
	}
	for _, event := range events {
		if event.Status != "blocked" || event.ResourceID != run.ID || event.ProjectID != projectID || event.Details["reason"] != "generator_placement_conflict" {
			t.Fatalf("unexpected audit event: %+v", event)
		}
	}
}

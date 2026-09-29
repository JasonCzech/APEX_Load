package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecoverySpareSettingsError(t *testing.T) {
	spare := &ManagedTest{Assignment: &GeneratorAssignmentSettings{RecoverySpareVUsers: 100}}
	withoutSpare := &ManagedTest{Assignment: &GeneratorAssignmentSettings{RecoverySpareVUsers: 0}}
	for _, tc := range []struct {
		name    string
		test    *ManagedTest
		enabled bool
		blocked bool
	}{
		{"nil test", nil, false, false},
		{"no spare", withoutSpare, false, false},
		{"spare enabled", spare, true, false},
		{"spare disabled", spare, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := SystemSettings{}
			settings.CapacityPlanning.PodReservationsEnabled = tc.enabled
			err := recoverySpareSettingsError(tc.test, settings)
			if errors.Is(err, errRecoverySpareRequiresPodReservations) != tc.blocked {
				t.Fatalf("error = %v, blocked = %v", err, tc.blocked)
			}
		})
	}
}

func TestPendingRecoverySpareFailsWhenPodReservationsAreDisabled(t *testing.T) {
	for _, updateMode := range []string{"local update", "durable refresh"} {
		t.Run(updateMode, func(t *testing.T) {
			server := newAdmittedHAReplayServer(t)
			store := server.store
			server.runner.StoreScript(&dsl.Script{
				ID: "pending-recovery-spare-script", Name: "Pending recovery spare", Protocol: "http", Version: "1.0.0",
				Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
				Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
			})
			run, err := server.runner.CreateTest(CreateTestRequest{
				Name: "Pending recovery spare", Duration: "1m", ScriptID: "pending-recovery-spare-script", VUsers: 50,
				Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true, RecoverySpareVUsers: 100},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a run queued while reservations were enabled, followed by an
			// administrative setting change before capacity becomes available.
			settings := server.runner.GetSystemSettings()
			settings.CapacityPlanning.PodReservationsEnabled = true
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			server.runner.mu.Lock()
			if err := server.runner.markTestPendingLocked(server.runner.tests[run.ID], "waiting for peak pod capacity"); err != nil {
				server.runner.mu.Unlock()
				t.Fatal(err)
			}
			server.runner.mu.Unlock()
			term, err := server.runner.captureExecutionLeaseTerm()
			if err != nil {
				t.Fatal(err)
			}
			// A stale promotion decision must not fail a queued run after an operator
			// has corrected the setting before the terminal write.
			if err := server.runner.failPendingPromotionGateIfWithTerm(run.ID, "obsolete configuration conflict", term, func(test *ManagedTest) bool {
				return recoverySpareSettingsError(test, server.runner.settings) != nil
			}); err != nil {
				t.Fatal(err)
			}
			stillPending, ok := server.runner.GetTest(run.ID)
			if !ok || stillPending.Status != "PENDING" {
				t.Fatalf("corrected setting failed queued run: %+v", stillPending)
			}
			settings = server.runner.GetSystemSettings()
			settings.CapacityPlanning.PodReservationsEnabled = false
			switch updateMode {
			case "local update":
				if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
					t.Fatal(err)
				}
			case "durable refresh":
				raw, err := json.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SaveSetting(systemSettingsStoreKey, string(raw)); err != nil {
					t.Fatal(err)
				}
				server.runner.refreshSystemSettingsFromStore(true)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				current, ok := server.runner.GetTest(run.ID)
				if ok && current.Status == "FAILED" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			current, ok := server.runner.GetTest(run.ID)
			if !ok || current.Status != "FAILED" || !strings.Contains(current.Error, "enable peak pod reservations") {
				t.Fatalf("pending test did not fail with a configuration reason: %+v", current)
			}
			if current.PendingSince != nil || current.EndedAt == nil {
				t.Fatalf("pending test did not reach a terminal state: %+v", current)
			}
			persisted, err := store.GetTest(run.ID)
			if err != nil || persisted.Status != "FAILED" || !strings.Contains(persisted.Error, "enable peak pod reservations") {
				t.Fatalf("durable pending test did not fail: %+v error=%v", persisted, err)
			}
			if persisted.EndTime == nil || time.Since(*persisted.EndTime) > time.Minute {
				t.Fatalf("missing recent durable end time: %+v", persisted.EndTime)
			}
		})
	}
}

func TestStartRecoverySpareRejectsDisabledPodReservationsBeforeQueue(t *testing.T) {
	server := newAdmittedHAReplayServer(t)
	store := server.store
	server.runner.StoreScript(&dsl.Script{
		ID: "recovery-spare-settings-script", Name: "Recovery spare settings script", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	run, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Recovery spare settings", Duration: "1m", ScriptID: "recovery-spare-settings-script", VUsers: 50,
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true, RecoverySpareVUsers: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.GetTest(run.ID)
	if err != nil || before == nil {
		t.Fatalf("load created test: %v", err)
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
			Code              string `json:"code"`
			Error             string `json:"error"`
			TestID            string `json:"test_id"`
			OverrideAvailable *bool  `json:"override_available"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Code != "recovery_spare_requires_pod_reservations" || payload.TestID != run.ID ||
			payload.OverrideAvailable == nil || *payload.OverrideAvailable || !strings.Contains(payload.Error, "enable peak pod reservations") {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		current, ok := server.runner.GetTest(run.ID)
		persisted, persistErr := store.GetTest(run.ID)
		if !ok || persistErr != nil || current.Status != run.Status || current.StartedAt != nil || current.PendingSince != nil ||
			persisted.Status != before.Status || persisted.StartedAt != nil || persisted.PendingSince != nil {
			t.Fatalf("start conflict queued run: memory=%+v durable=%+v error=%v", current, persisted, persistErr)
		}
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.placement.reject", Limit: 10})
	if err != nil || len(events) != 2 {
		t.Fatalf("audit events=%+v error=%v", events, err)
	}
	for _, event := range events {
		if event.Details["reason"] != "recovery_spare_requires_pod_reservations" {
			t.Fatalf("audit details=%+v", event.Details)
		}
	}
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.PodReservationsEnabled = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/start", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"status":"pending"`) {
		t.Fatalf("enabled reservations did not allow normal capacity queueing: status=%d body=%s", resp.Code, resp.Body)
	}
	queued, ok := server.runner.GetTest(run.ID)
	if !ok || queued.Status != "PENDING" || strings.Contains(queued.PendingReason, "enable peak pod reservations") {
		t.Fatalf("enabled reservations kept configuration conflict: %+v", queued)
	}
}

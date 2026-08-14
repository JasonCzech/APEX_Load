package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleSaveJMeterImportEnforcesBatchScriptLimit(t *testing.T) {
	tests := []struct {
		name        string
		scriptCount int
		wantStatus  int
	}{
		{name: "maximum accepted", scriptCount: jmeterImportSaveMaxScripts, wantStatus: http.StatusCreated},
		{name: "one above maximum rejected", scriptCount: jmeterImportSaveMaxScripts + 1, wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, store := newAuditTestServer(t)
			defer store.Close()

			scripts := make([]dsl.Script, test.scriptCount)
			for index := range scripts {
				scripts[index] = dsl.Script{
					Name:      fmt.Sprintf("JMeter lane %04d", index+1),
					Version:   "1.0",
					Protocol:  "http",
					ProjectID: storage.DefaultProjectID,
					Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
					Actions: []dsl.Action{{
						Name:   "Request",
						Type:   "request",
						Method: http.MethodGet,
						Target: "/health",
					}},
				}
			}
			body, err := json.Marshal(map[string]any{"scripts": scripts})
			if err != nil {
				t.Fatal(err)
			}
			response := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", response.Code, test.wantStatus, response.Body.String())
			}

			stored, err := store.ListScripts(storage.DefaultProjectID, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantStored := test.scriptCount
			if test.wantStatus != http.StatusCreated {
				wantStored = 0
			}
			if len(stored) != wantStored {
				t.Fatalf("stored scripts = %d, want %d", len(stored), wantStored)
			}
		})
	}
}

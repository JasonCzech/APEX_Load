package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCreateTestPersistsCreatorInInitialDurableRecord(t *testing.T) {
	store := &metricsStoreSpy{}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-atomic-creator",
		Name:     "Atomic creator",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Attributed run", Duration: "1m", ScriptID: "script-atomic-creator", VUsers: 1,
		creationActor: testCreationActor{Type: "user", Subject: " user:creator ", DisplayName: " Creator User "},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if testRun.CreatedBySubject != "user:creator" || testRun.CreatedByDisplayName != "Creator User" {
		t.Fatalf("returned attribution = %q/%q", testRun.CreatedBySubject, testRun.CreatedByDisplayName)
	}
	if len(store.savedTests) != 1 {
		t.Fatalf("SaveTest calls = %d, want one atomic create write", len(store.savedTests))
	}
	record := store.savedTests[0]
	if record.CreatedBySubject != "user:creator" || record.CreatedByDisplayName != "Creator User" {
		t.Fatalf("initial durable attribution = %q/%q", record.CreatedBySubject, record.CreatedByDisplayName)
	}
}

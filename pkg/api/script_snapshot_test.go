package api

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestStoredScriptPublicMethodsReturnDeepSnapshots(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	folder := "folder-a"
	original := &dsl.Script{
		ID: "script-snapshot", ProjectID: "project-a", FolderID: &folder, Name: "Before", Protocol: "http", Version: "1",
		Config:  dsl.ScriptConfig{BaseURL: "https://before.example"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/", Headers: map[string]string{"X-Test": "before"}}},
	}
	created := runner.StoreScript(original)
	if created == nil {
		t.Fatal("StoreScript returned nil")
	}

	// Neither the caller-owned input nor the returned value may alias the
	// runner's canonical definition.
	original.Name = "mutated input"
	original.Actions[0].Headers["X-Test"] = "mutated input"
	created.Name = "mutated response"
	created.Script.Config.BaseURL = "https://mutated.example"
	created.Script.Actions[0].Headers["X-Test"] = "mutated response"
	*created.FolderID = "folder-mutated"

	stored, ok := runner.GetScript(original.ID)
	if !ok {
		t.Fatal("GetScript did not find stored script")
	}
	if stored.Name != "Before" || stored.Script.Config.BaseURL != "https://before.example" || stored.Script.Actions[0].Headers["X-Test"] != "before" || *stored.FolderID != "folder-a" {
		t.Fatalf("stored script aliases caller state: %+v", stored)
	}
	stored.Script.Actions[0].Headers["X-Test"] = "mutated get"
	listed := runner.ListScripts()
	listed[0].Script.Actions[0].Headers["X-Test"] = "mutated list"
	again, _ := runner.GetScript(original.ID)
	if again.Script.Actions[0].Headers["X-Test"] != "before" {
		t.Fatalf("public script snapshots alias runner state: %+v", again.Script.Actions[0].Headers)
	}
}

func TestStoredScriptSnapshotConcurrentUpdateAndEncode(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	stored := runner.StoreScript(&dsl.Script{
		ID: "script-snapshot-race", ProjectID: "project-a", Name: "initial", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if stored == nil {
		t.Fatal("StoreScript returned nil")
	}

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		for index := 0; index < 100; index++ {
			_, ok, err := runner.UpdateScriptDurable(stored.ID, &dsl.Script{
				Name: fmt.Sprintf("script-%d", index), Protocol: "http", Version: "1",
				Config:  dsl.ScriptConfig{BaseURL: fmt.Sprintf("https://%d.example.test", index)},
				Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/", Headers: map[string]string{"X-Iteration": fmt.Sprint(index)}}},
			})
			if err != nil || !ok {
				t.Errorf("UpdateScriptDurable(%d) = ok=%v err=%v", index, ok, err)
				return
			}
		}
	}()
	go func() {
		defer wait.Done()
		for index := 0; index < 100; index++ {
			snapshot, ok := runner.GetScript(stored.ID)
			if !ok {
				t.Error("GetScript lost script")
				return
			}
			if _, err := json.Marshal(snapshot); err != nil {
				t.Errorf("marshal snapshot: %v", err)
				return
			}
		}
	}()
	wait.Wait()
}

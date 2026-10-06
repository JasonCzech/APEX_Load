package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRCACritiquesAreKeptApartAndBounded(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "critiques.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", ProjectID: DefaultProjectID, Status: "COMPLETE", StartTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	analysis, _, err := store.CreateRCAAnalysis(DefaultProjectID, "run", strings.Repeat("a", 64), json.RawMessage(`{}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"narrative":"ok"}`)
	if _, err := store.CreateRCACritique(RCACritique{ID: "c-other", ProjectID: DefaultProjectID, TestID: "other-run", AnalysisID: analysis.ID, Payload: payload}); err == nil {
		t.Fatal("a critique named another run")
	}
	for i := 0; i < MaxRCACritiquesPerAnalysis; i++ {
		if _, err := store.CreateRCACritique(RCACritique{ID: fmt.Sprintf("c-%03d", i), ProjectID: DefaultProjectID, TestID: "run", AnalysisID: analysis.ID, CreatedBy: "agent", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateRCACritique(RCACritique{ID: "c-over", ProjectID: DefaultProjectID, TestID: "run", AnalysisID: analysis.ID, Payload: payload}); !errors.Is(err, ErrRCACritiqueLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	list, err := store.ListRCACritiques(DefaultProjectID, "run", analysis.ID)
	if err != nil || len(list) != MaxRCACritiquesPerAnalysis || list[0].CreatedBy != "agent" || string(list[0].Payload) != string(payload) {
		t.Fatalf("list: %d %v", len(list), err)
	}
	// The analysis itself is untouched.
	again, err := store.GetRCAAnalysis(DefaultProjectID, "run", analysis.ID)
	if err != nil || string(again.Payload) != `{}` {
		t.Fatalf("analysis: %+v %v", again, err)
	}
	// Critiques follow their run.
	if err := store.DeleteTest("run"); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.ListRCACritiques(DefaultProjectID, "run", analysis.ID); len(list) != 0 {
		t.Fatalf("%d critiques outlived their run", len(list))
	}
}

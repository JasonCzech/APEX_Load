package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type loadModelDataPoolSpy struct {
	pool      *storage.SharedDataPool
	rowReads  int
	poolReads int
}

func (s *loadModelDataPoolSpy) GetDataPool(string) (*storage.SharedDataPool, error) {
	s.poolReads++
	if s.pool == nil {
		return nil, nil
	}
	clone := *s.pool
	return &clone, nil
}

func (s *loadModelDataPoolSpy) ListDataPoolRows(string) ([]*storage.SharedDataPoolRow, error) {
	s.rowReads++
	return []*storage.SharedDataPoolRow{{ID: "row-foreign", Data: map[string]string{"secret": "must-not-read"}}}, nil
}

func loadModelProjectRunner(t *testing.T, poolStore dataPoolPersistence) *TestRunner {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	runner.dataPoolStore = poolStore
	stored := runner.StoreScript(&dsl.Script{
		ID:        "script-project-a",
		ProjectID: "project-a",
		Name:      "Project A flow",
		Protocol:  "http",
		Version:   "1.0",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	})
	if stored == nil {
		t.Fatal("StoreScript returned nil")
	}
	return runner
}

func TestValidateScenarioLoadModelRejectsForeignPoolBeforeReadingRows(t *testing.T) {
	spy := &loadModelDataPoolSpy{pool: &storage.SharedDataPool{ID: "pool-b", ProjectID: "project-b", Name: "Foreign pool"}}
	runner := loadModelProjectRunner(t, spy)
	_, err := runner.ValidateScenarioLoadModel(CreateScenarioRequest{
		Name: "Foreign pool draft", Duration: "1m",
		Groups:    []CreateGroupEntry{{ScriptID: "script-project-a", VUsers: 1}},
		DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "pool-b", Alias: "customers"}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not belong to project") {
		t.Fatalf("ValidateScenarioLoadModel error = %v, want project ownership rejection", err)
	}
	if spy.poolReads != 1 {
		t.Fatalf("pool metadata reads = %d, want 1", spy.poolReads)
	}
	if spy.rowReads != 0 {
		t.Fatalf("foreign pool row reads = %d, want 0", spy.rowReads)
	}
}

func TestValidateAndSaveUseTheSameDataPoolBindingNormalization(t *testing.T) {
	spy := &loadModelDataPoolSpy{pool: &storage.SharedDataPool{ID: "pool-a", ProjectID: "project-a", Name: "Pool A"}}
	runner := loadModelProjectRunner(t, spy)
	req := CreateScenarioRequest{
		Name: "Duplicate aliases", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "script-project-a", VUsers: 1}},
		DataPools: []storage.ScenarioDataPoolBinding{
			{PoolID: "pool-a", Alias: " Customers "},
			{PoolID: "pool-a", Alias: "customers"},
		},
	}
	_, validationErr := runner.ValidateScenarioLoadModel(req)
	_, saveErr := runner.StoreScenario(req)
	for operation, err := range map[string]error{"validate": validationErr, "save": saveErr} {
		if err == nil || !strings.Contains(err.Error(), "duplicate data pool binding alias") {
			t.Fatalf("%s error = %v, want duplicate alias rejection", operation, err)
		}
	}
	if spy.rowReads != 0 {
		t.Fatalf("row reads = %d, want normalization failure before row access", spy.rowReads)
	}
}

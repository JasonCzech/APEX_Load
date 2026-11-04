package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/llmperf/econ"
)

func historyLLMFixture() json.RawMessage {
	return json.RawMessage(`{"requests":12,"streaming_requests":12,"completed_requests":11,"failed_requests":1,"PromptTokens":1440,"CompletionTokens":384,"TotalTokens":1824,"p50_ttft_ms":85,"p95_ttft_ms":140,"p99_ttft_ms":190,"p50_tpot_ms":11.5,"p95_tpot_ms":18.2,"p99_tpot_ms":24.1,"by_model":{"gpt-4o-mini":{"requests":12,"completed_requests":11,"prompt_tokens":1440,"completion_tokens":384,"p95_ttft_ms":140,"p95_tpot_ms":18.2}}}`)
}

func assertLLMHistoryAndProjectSettings(t *testing.T, store *SQLiteStore, now time.Time) {
	t.Helper()
	if err := store.SaveHistory("run", now, map[string]any{"timestamp": now, "llm": historyLLMFixture()}); err != nil {
		t.Fatal(err)
	}
	history, err := store.GetHistoryLimit("run", 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("GetHistoryLimit: %+v, err=%v", history, err)
	}
	got := history[0].(IntervalSummaryRecord)
	if len(got.LLM) == 0 {
		t.Fatal("expected persisted llm_json on IntervalSummaryRecord, got empty")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.LLM, &decoded); err != nil {
		t.Fatalf("unmarshal got.LLM: %v", err)
	}
	if decoded["requests"] != float64(12) || decoded["p95_ttft_ms"] != float64(140) {
		t.Fatalf("unexpected llm_json payload: %+v", decoded)
	}

	proj := &Project{
		ID:   "llm-proj",
		Name: "LLM Project",
		Settings: ProjectSettings{
			LLMDeployments: []econ.DeploymentSpec{
				{
					ID:                 "dep-vllm-h100",
					Name:               "vLLM H100x2",
					Model:              "llama-3.1-70b",
					BillingMode:        "gpu_hourly",
					GPUCount:           2,
					GPUCostPerHourUSD:  7.50,
					InputCostPer1MUSD:  0.40,
					OutputCostPer1MUSD: 1.20,
					AsOfDate:           "2026-10-01",
				},
			},
		},
		CreatedAt: now,
	}
	if err := store.SaveProject(proj); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	loadedProj, err := store.GetProject("llm-proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(loadedProj.Settings.LLMDeployments) != 1 || loadedProj.Settings.LLMDeployments[0].ID != "dep-vllm-h100" || loadedProj.Settings.LLMDeployments[0].GPUCount != 2 {
		t.Fatalf("expected round-tripped ProjectSettings.LLMDeployments, got %+v", loadedProj.Settings.LLMDeployments)
	}
}

func TestLLMHistorySQLiteMigrationRestartAndRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llm_history.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err = store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveHistory("run", now.Add(-time.Second), map[string]any{"timestamp": now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN llm_json"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, currentSchemaRelease().Version); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	assertLLMHistoryAndProjectSettings(t, store, now)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.GetHistory("run")
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	if len(history[0].(IntervalSummaryRecord).LLM) != 0 || len(history[1].(IntervalSummaryRecord).LLM) == 0 {
		t.Fatalf("legacy absence or restart changed llm_json evidence: row0=%s row1=%s", string(history[0].(IntervalSummaryRecord).LLM), string(history[1].(IntervalSummaryRecord).LLM))
	}
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type partialReimportStore struct {
	*storage.SQLiteStore
	failing   bool
	remaining int
}

func (s *partialReimportStore) UpdateScriptAuthoringIfCurrent(script *dsl.Script, expected storage.ScriptAuthoringPrecondition) (*storage.ScriptAuthoringMutationResult, error) {
	if s.failing {
		if s.remaining == 0 {
			return nil, errors.New("storage outage during re-import")
		}
		s.remaining--
	}
	return s.SQLiteStore.UpdateScriptAuthoringIfCurrent(script, expected)
}

func TestPartialReimportRecoveryAcrossBatchHandlers(t *testing.T) {
	for _, adapter := range []struct{ name, path, profile string }{
		{"asyncapi", "/api/v1/scripts/import/asyncapi/commit-batch", "asyncapi"},
		{"generic", "/api/v1/scripts/import/commit-batch", "raml"},
		{"enterprise", "/api/v1/scripts/import/enterprise-script-archive/save", enterpriseArchiveSaveProfile},
		{"jmeter", "/api/v1/scripts/import/jmeter/save", "jmeter"},
	} {
		for _, outcome := range []string{"resume after repeated failures", "preserve intervening edit", "new operation needs review"} {
			t.Run(adapter.name+"/"+outcome, func(t *testing.T) {
				base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "partial.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = base.Close() })
				store := &partialReimportStore{SQLiteStore: base}
				server := NewServer(ServerConfig{Store: store})
				scripts := func(step string) []any {
					out := make([]any, 0, 2)
					for _, name := range []string{"Orders", "Refunds"} {
						var script map[string]any
						if adapter.name == "asyncapi" {
							script = asyncAPIReimportScript(name, step)
						} else {
							script = genericImportBatchTestScript(name, storage.DefaultProjectID, "")
							script["actions"].([]any)[0].(map[string]any)["name"] = step
							script["metadata"] = map[string]any{"import": map[string]any{"source": adapter.profile, "preserved": map[string]any{
								dsl.ImportSourceDocumentKey: "Orders", dsl.ImportSourceDocumentDeclaredKey: true, dsl.ImportSourceItemKey: name,
							}}}
						}
						out = append(out, script)
					}
					return out
				}
				post := func(operation string, payload map[string]any) *httptest.ResponseRecorder {
					t.Helper()
					payload["operation_id"], payload["source_profile"] = operation, adapter.profile
					body, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					request := httptest.NewRequest(http.MethodPost, adapter.path, bytes.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Idempotency-Key", operation)
					response := httptest.NewRecorder()
					server.mux.ServeHTTP(response, request)
					return response
				}
				first := asyncAPIReimportScripts(t, post("initial", map[string]any{"scripts": scripts("v1")}))
				var targets []any
				for _, item := range first {
					current, _, err := server.runner.GetScriptDurable(item.ID)
					if err != nil {
						t.Fatal(err)
					}
					digest, err := importedScriptContentDigest(current.Script)
					if err != nil {
						t.Fatal(err)
					}
					targets = append(targets, importUpdateTarget{ScriptID: item.ID, ExpectedUpdatedAt: current.Script.UpdatedAt, ExpectedContentDigest: digest})
				}
				payload := map[string]any{"scripts": scripts("v2"), "update_targets": targets}
				store.failing, store.remaining = true, 1
				if failed := post("update", payload); failed.Code != http.StatusInternalServerError {
					t.Fatalf("initial failure = %d %s", failed.Code, failed.Body.String())
				}
				applied, _ := base.GetScript(first[0].ID)
				pending, _ := base.GetScript(first[1].ID)
				if applied.Actions[0].Name != "v2" || pending.Actions[0].Name != "v1" {
					t.Fatalf("fixture did not partially apply: %q / %q", applied.Actions[0].Name, pending.Actions[0].Name)
				}
				store.failing = false
				if outcome == "preserve intervening edit" {
					changed := cloneScriptDefinition(applied)
					changed.Actions[0].Name = "operator edit"
					if _, ok, err := server.runner.UpdateScriptDurable(first[0].ID, changed); err != nil || !ok {
						t.Fatalf("operator edit: %v %v", ok, err)
					}
				}
				// A fresh controller must recover from durable evidence alone.
				server = NewServer(ServerConfig{Store: store})
				if outcome != "resume after repeated failures" {
					operation := "update"
					if outcome == "new operation needs review" {
						operation = "different-update"
					}
					response := post(operation, payload)
					if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), importUpdateTargetStaleCode) {
						t.Fatalf("unreviewed edit accepted: %d %s", response.Code, response.Body.String())
					}
					current, _ := base.GetScript(first[0].ID)
					want := "v2"
					if outcome == "preserve intervening edit" {
						want = "operator edit"
					}
					if current.Actions[0].Name != want {
						t.Fatal("conflicting request changed the first script")
					}
					return
				}
				// Fail again after adopting the first update, before the pending
				// mutation. Clean compensation must still retain the original receipt.
				store.failing, store.remaining = true, 0
				if failed := post("update", payload); failed.Code != http.StatusInternalServerError {
					t.Fatalf("second failure = %d %s", failed.Code, failed.Body.String())
				}
				store.failing = false
				server = NewServer(ServerConfig{Store: store})
				for retry := 0; retry < 2; retry++ {
					resumed := asyncAPIReimportScripts(t, post("update", payload))
					for i, item := range resumed {
						if item.ID != first[i].ID || item.Script.Actions[0].Name != "v2" {
							t.Fatalf("incorrect resumed item: %+v", item)
						}
					}
					if resumed[0].RefKey != applied.RefKey {
						t.Fatal("recovery created a new version of an already-applied update")
					}
				}
			})
		}
	}
}

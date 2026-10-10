package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type scriptUpdateReadHook struct {
	io.Reader
	once       sync.Once
	beforeRead func()
}

func (reader *scriptUpdateReadHook) Read(buffer []byte) (int, error) {
	reader.once.Do(reader.beforeRead)
	return reader.Reader.Read(buffer)
}

func TestUpdateScriptRejectsEditDuringRequestDecoding(t *testing.T) {
	for _, conditioned := range []bool{false, true} {
		name := "legacy unconditioned save"
		if conditioned {
			name = "If-Match save"
		}
		t.Run(name, func(t *testing.T) {
			server := recordingTestServer(t)
			original, err := server.runner.StoreScriptDurable(&dsl.Script{
				Name: "Original", Protocol: "http", ProjectID: "p1",
				Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
				Actions: []dsl.Action{{Name: "Fetch", Type: "request", Method: "GET", Target: "/original"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			update := cloneScriptDefinition(original.Script)
			update.Name = "In-flight update"
			body, err := json.Marshal(map[string]any{"script": update})
			if err != nil {
				t.Fatal(err)
			}
			reader := &scriptUpdateReadHook{Reader: bytes.NewReader(body), beforeRead: func() {
				current, err := server.store.GetScript(original.ID)
				if err != nil {
					t.Fatal(err)
				}
				current.Name = "Concurrent edit"
				current.Actions[0].Target = "/new"
				if err := server.store.SaveScript(current); err != nil {
					t.Fatal(err)
				}
			}}
			request := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+original.ID, reader)
			request.SetPathValue("id", original.ID)
			if conditioned {
				stored, found, err := server.runner.GetScriptAuthoringDurable(original.ID)
				if err != nil || !found {
					t.Fatalf("authoring snapshot: found=%v err=%v", found, err)
				}
				tag, err := scriptAuthoringETag(stored.ContentRevision)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("If-Match", tag)
			}
			response := httptest.NewRecorder()
			server.handleUpdateScript(response, request)
			if response.Code != http.StatusPreconditionFailed {
				t.Fatalf("update status = %d, want 412: %s", response.Code, response.Body.String())
			}
			after, err := server.store.GetScript(original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Name != "Concurrent edit" || after.Actions[0].Target != "/new" {
				t.Fatalf("concurrent edit was overwritten: %#v", after)
			}
		})
	}
}

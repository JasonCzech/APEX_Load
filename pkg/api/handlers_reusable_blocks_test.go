package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newReusableBlockServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewServer(ServerConfig{Store: store})
}

func TestReusableBlockCreateListDelete(t *testing.T) {
	server := newReusableBlockServer(t)
	if err := server.store.SaveProject(&storage.Project{ID: "proj-test", Name: "Reusable blocks"}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	create := map[string]any{
		"project_id": "proj-test",
		"name":       "Checkout Group",
		"kind":       "request_group",
		"protocols":  []string{"http"},
		"summary":    "Add to cart then checkout.",
		"actions": []map[string]any{
			{"name": "Add To Cart", "type": "http_request", "method": "POST", "target": "/cart"},
		},
	}
	body, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/components", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var created AuthoringComponent
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Name != "Checkout Group" {
		t.Fatalf("created component = %+v, want id + name", created)
	}

	// GET (filtered to the project + protocol) must include the saved block alongside built-ins.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/components?protocol=http&project_id=proj-test", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Components []AuthoringComponent `json:"components"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, c := range listed.Components {
		if c.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("saved block %s not present in merged catalog", created.ID)
	}

	// A different project must not see the block.
	otherResp := httptest.NewRecorder()
	server.mux.ServeHTTP(otherResp, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/components?protocol=http&project_id=other-proj", nil))
	var otherListed struct {
		Components []AuthoringComponent `json:"components"`
	}
	_ = json.Unmarshal(otherResp.Body.Bytes(), &otherListed)
	for _, c := range otherListed.Components {
		if c.ID == created.ID {
			t.Errorf("block %s leaked into other project", created.ID)
		}
	}

	// Delete removes it.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/authoring/components/"+created.ID, nil)
	delResp := httptest.NewRecorder()
	server.mux.ServeHTTP(delResp, delReq)
	if delResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", delResp.Code, delResp.Body.String())
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/components?protocol=http&project_id=proj-test", nil)
	afterResp := httptest.NewRecorder()
	server.mux.ServeHTTP(afterResp, afterReq)
	var after struct {
		Components []AuthoringComponent `json:"components"`
	}
	_ = json.Unmarshal(afterResp.Body.Bytes(), &after)
	for _, c := range after.Components {
		if c.ID == created.ID {
			t.Errorf("block %s still present after delete", created.ID)
		}
	}
}

func TestReusableBlockCreateValidation(t *testing.T) {
	server := newReusableBlockServer(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"project_id": "proj-test"}},
		{"missing project", map[string]any{"name": "No Project"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/components", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

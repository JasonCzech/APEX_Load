package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestListExternalResultImportsPagesWithinApplicationScope(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	server := &Server{resultImports: map[string]*storage.ExternalResultImport{}}
	for _, result := range []*storage.ExternalResultImport{
		{ID: "a-1", Provider: "k6", Name: "a-1", ProjectID: "project", ApplicationID: "app-a", CreatedAt: createdAt},
		{ID: "a-3", Provider: "k6", Name: "a-3", ProjectID: "project", ApplicationID: "app-a", CreatedAt: createdAt},
		{ID: "b-9", Provider: "k6", Name: "b-9", ProjectID: "project", ApplicationID: "app-b", CreatedAt: createdAt.Add(time.Hour)},
		{ID: "a-2", Provider: "k6", Name: "a-2", ProjectID: "project", ApplicationID: "app-a", CreatedAt: createdAt},
	} {
		server.resultImports[result.ID] = result
	}

	requestPage := func(offset int) struct {
		Imports []*storage.ExternalResultImport `json:"imports"`
		Count   int                             `json:"count"`
		Limit   int                             `json:"limit"`
		Offset  int                             `json:"offset"`
		HasMore bool                            `json:"has_more"`
	} {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/results/imports?project_id=project&application_id=app-a&limit=2&offset="+strconv.Itoa(offset), nil)
		response := httptest.NewRecorder()
		server.handleListExternalResultImports(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
		}
		var page struct {
			Imports []*storage.ExternalResultImport `json:"imports"`
			Count   int                             `json:"count"`
			Limit   int                             `json:"limit"`
			Offset  int                             `json:"offset"`
			HasMore bool                            `json:"has_more"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	first := requestPage(0)
	if first.Count != 2 || first.Limit != 2 || first.Offset != 0 || !first.HasMore {
		t.Fatalf("first page metadata = count:%d limit:%d offset:%d has_more:%t", first.Count, first.Limit, first.Offset, first.HasMore)
	}
	if got := []string{first.Imports[0].ID, first.Imports[1].ID}; got[0] != "a-3" || got[1] != "a-2" {
		t.Fatalf("first page ids = %v, want [a-3 a-2]", got)
	}

	second := requestPage(2)
	if second.Count != 1 || second.Offset != 2 || second.HasMore {
		t.Fatalf("second page metadata = count:%d offset:%d has_more:%t", second.Count, second.Offset, second.HasMore)
	}
	if len(second.Imports) != 1 || second.Imports[0].ID != "a-1" {
		t.Fatalf("second page imports = %#v, want a-1", second.Imports)
	}
	for _, page := range [][]*storage.ExternalResultImport{first.Imports, second.Imports} {
		for _, imported := range page {
			if imported.ApplicationID != "app-a" {
				t.Fatalf("application scope leaked import %#v", imported)
			}
		}
	}
}

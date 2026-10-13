package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDataPoolColumnOrderFollowsImportsAndEdits(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	importCSV := func(mode, body string) {
		t.Helper()
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"mode": mode}, body))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s import status = %d body=%s", mode, resp.Code, resp.Body.String())
		}
	}
	columns := func() []string {
		t.Helper()
		current, err := store.GetDataPool(pool.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current.Columns
	}
	exportHeader := func() string {
		t.Helper()
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("export status = %d body=%s", resp.Code, resp.Body.String())
		}
		return strings.Join(readDataPoolExportCSV(t, resp.Body)[1], ",")
	}
	put := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		current, err := store.GetDataPool(pool.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := body["updated_at"]; !ok {
			body["updated_at"] = current.UpdatedAt.Format(time.RFC3339Nano)
		}
		raw, _ := json.Marshal(body)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader(raw)))
		return resp
	}

	before, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A replacement keeps the file's order; export writes it back that way.
	importCSV("replace", "zip,customer_id,email\n10001,C-1,a@example.test\n")
	// The order is a display hint: recording it leaves the edit token alone.
	if after, _ := store.GetDataPool(pool.ID); !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("import changed the pool's edit token from %v to %v", before.UpdatedAt, after.UpdatedAt)
	}
	if got := strings.Join(columns(), ","); got != "zip,customer_id,email" {
		t.Fatalf("columns after replace = %q", got)
	}
	if got := exportHeader(); got != "zip,customer_id,email,"+dataPoolExportStatusColumnBase {
		t.Fatalf("export header = %q", got)
	}

	// An append adds only the file's new columns, after the known ones.
	importCSV("append", "region,email\neast,b@example.test\n")
	if got := strings.Join(columns(), ","); got != "zip,customer_id,email,region" {
		t.Fatalf("columns after append = %q", got)
	}

	// The editor can reorder; unlisted columns still export, sorted, after.
	if resp := put(map[string]any{"columns": []string{"email", " zip ", "email"}}); resp.Code != http.StatusOK {
		t.Fatalf("reorder status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := strings.Join(columns(), ","); got != "email,zip" {
		t.Fatalf("columns after reorder = %q", got)
	}
	if got := exportHeader(); got != "email,zip,customer_id,region,"+dataPoolExportStatusColumnBase {
		t.Fatalf("export header after reorder = %q", got)
	}

	// A rename leaves the order alone; null clears it; a non-array is refused.
	if resp := put(map[string]any{"name": "Customer accounts"}); resp.Code != http.StatusOK {
		t.Fatalf("rename status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := strings.Join(columns(), ","); got != "email,zip" {
		t.Fatalf("columns after rename = %q", got)
	}
	if resp := put(map[string]any{"columns": map[string]string{"a": "b"}}); resp.Code != http.StatusBadRequest {
		t.Fatalf("object columns status = %d, want 400", resp.Code)
	}
	if resp := put(map[string]any{"columns": nil}); resp.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := columns(); got != nil {
		t.Fatalf("columns after clear = %#v", got)
	}
	if got := exportHeader(); got != "customer_id,email,region,zip,"+dataPoolExportStatusColumnBase {
		t.Fatalf("export header without an order = %q", got)
	}
}

func TestCreateDataPoolKeepsDeclaredColumns(t *testing.T) {
	server, _, _ := newImportExportTestServer(t)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", strings.NewReader(`{"name":"Orders","project_id":"`+storage.DefaultProjectID+`","columns":["order_id","sku"]}`)))
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.Code, resp.Body.String())
	}
	var created storage.SharedDataPool
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if strings.Join(created.Columns, ",") != "order_id,sku" {
		t.Fatalf("created columns = %#v", created.Columns)
	}
}

func TestOrderDataPoolColumns(t *testing.T) {
	present := map[string]struct{}{"b": {}, "a": {}, "c": {}, "Z": {}}
	got := orderDataPoolColumns([]string{"c", "missing", "a", "c"}, present)
	if strings.Join(got, ",") != "c,a,Z,b" {
		t.Fatalf("ordered columns = %v", got)
	}
}

package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newDataPoolImportRequest(t *testing.T, poolID string, params map[string]string, content string) *http.Request {
	return newDataPoolImportRequestWithFilename(t, poolID, params, "pool.csv", content)
}

func readDataPoolExportCSV(t *testing.T, source io.Reader) [][]string {
	t.Helper()
	reader := csv.NewReader(source)
	// The metadata preamble intentionally has two fields while the following
	// header and data records contain the pool's dynamic column count.
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse data pool export: %v", err)
	}
	return records
}

func newDataPoolImportRequestWithFilename(t *testing.T, poolID string, params map[string]string, filename, content string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range params {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s): %v", key, err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+poolID+"/rows/import", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func newImportExportTestServer(t *testing.T) (*Server, *storage.SQLiteStore, *storage.SharedDataPool) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	pool := &storage.SharedDataPool{ID: "pool-import", Name: "Import Pool", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	return NewServer(ServerConfig{Store: store}), store, pool
}

func TestImportDataPoolRowsDryRunAppendReplaceAndExportRoundTrip(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "row-existing", PoolID: pool.ID, Data: map[string]string{"customer_id": "C-0"}}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	csvBody := "customer_id,region\nC-1,east\nC-2,west\n"

	// Dry run parses and previews without writing.
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"dry_run": "true"}, csvBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("dry-run cache control = %q, want no-store", got)
	}
	var preview struct {
		DryRun     bool                `json:"dry_run"`
		Columns    []string            `json:"columns"`
		RowCount   int                 `json:"row_count"`
		SampleRows []map[string]string `json:"sample_rows"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode dry-run: %v", err)
	}
	if !preview.DryRun || preview.RowCount != 2 || len(preview.SampleRows) != 2 || strings.Join(preview.Columns, "|") != "customer_id|region" {
		t.Fatalf("dry-run preview = %+v", preview)
	}
	if count, err := store.CountDataPoolRows(pool.ID); err != nil || count != 1 {
		t.Fatalf("dry run wrote rows: count=%d err=%v", count, err)
	}

	// Append keeps the existing row.
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"mode": "append"}, csvBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("append status = %d body=%s", resp.Code, resp.Body.String())
	}
	var appendResult struct {
		Imported int `json:"imported"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &appendResult); err != nil {
		t.Fatalf("decode append: %v", err)
	}
	if appendResult.Imported != 2 || appendResult.Total != 3 {
		t.Fatalf("append result = %+v, want imported 2 total 3", appendResult)
	}

	// Replace clears the pool first.
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"mode": "replace"}, csvBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("replace status = %d body=%s", resp.Code, resp.Body.String())
	}
	var replaceResult struct {
		Imported int `json:"imported"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &replaceResult); err != nil {
		t.Fatalf("decode replace: %v", err)
	}
	if replaceResult.Imported != 2 || replaceResult.Total != 2 {
		t.Fatalf("replace result = %+v, want imported 2 total 2", replaceResult)
	}

	// Export round-trips the imported file, preserving row order.
	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil)
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, exportReq)
	if resp.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("export content type = %q", got)
	}
	if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "Import-Pool.apexload.csv") {
		t.Fatalf("export disposition = %q", got)
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("export cache control = %q, want no-store", got)
	}
	if got := resp.Header().Get("X-APEXLoad-Sensitivity"); got != "raw" {
		t.Fatalf("export sensitivity = %q, want raw", got)
	}
	records := readDataPoolExportCSV(t, resp.Body)
	want := [][]string{
		{dataPoolExportMetadataMarker, dataPoolExportStatusColumnBase},
		{"customer_id", "region", dataPoolExportStatusColumnBase},
		{"C-1", "east", "active"},
		{"C-2", "west", "active"},
	}
	if len(records) != len(want) {
		t.Fatalf("export records = %#v, want %#v", records, want)
	}
	for i := range want {
		if strings.Join(records[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("export record %d = %#v, want %#v", i, records[i], want[i])
		}
	}
}

func TestImportDataPoolRowsDetectsTabDelimiter(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, nil, "user\tpass\nalice\tsecret1\nbob\tsecret2\n"))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}
	if rows[0].Data["user"] != "alice" || rows[0].Data["pass"] != "secret1" {
		t.Fatalf("first row = %#v", rows[0].Data)
	}
}

func TestImportDataPoolRowsHeaderFalseRequiresColumns(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"header": "false"}, "C-1,east\n"))
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "columns is required") {
		t.Fatalf("headerless without columns: status = %d body=%s", resp.Code, resp.Body.String())
	}

	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"header": "false", "columns": "customer_id,region"}, "C-1,east\n"))
	if resp.Code != http.StatusOK {
		t.Fatalf("headerless with columns: status = %d body=%s", resp.Code, resp.Body.String())
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil || len(rows) != 1 || rows[0].Data["customer_id"] != "C-1" || rows[0].Data["region"] != "east" {
		t.Fatalf("rows = %#v err=%v", rows, err)
	}
}

func TestImportDataPoolRowsRejectsArchivedPool(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	loaded, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	archivedAt := time.Now().UTC()
	loaded.ArchivedAt = &archivedAt
	if err := store.SaveDataPool(loaded); err != nil {
		t.Fatalf("SaveDataPool(archive): %v", err)
	}
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, nil, "customer_id\nC-1\n"))
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "archived") {
		t.Fatalf("status = %d body=%s, want archived rejection", resp.Code, resp.Body.String())
	}
}

func TestExportDataPoolRowsStatusFilter(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	base := time.Now().UTC().Add(-time.Minute)
	for i, row := range []*storage.SharedDataPoolRow{
		{ID: "row-a", PoolID: pool.ID, Data: map[string]string{"id": "active-1"}, Status: "active"},
		{ID: "row-b", PoolID: pool.ID, Data: map[string]string{"id": "consumed-1"}, Status: "consumed"},
	} {
		row.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := store.SaveDataPoolRow(row); err != nil {
			t.Fatalf("SaveDataPoolRow(%s): %v", row.ID, err)
		}
	}

	fetch := func(query string) [][]string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export"+query, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("export%s status = %d body=%s", query, resp.Code, resp.Body.String())
		}
		return readDataPoolExportCSV(t, resp.Body)
	}

	if all := fetch(""); len(all) != 4 {
		t.Fatalf("default export records = %#v, want metadata + header + 2 rows", all)
	}
	active := fetch("?status=active")
	if len(active) != 3 || active[2][0] != "active-1" {
		t.Fatalf("active export records = %#v, want metadata + header + active row only", active)
	}
}

func TestListDataPoolRowsReportsTotal(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
			ID: "row-total-" + string(rune('a'+i)), PoolID: pool.ID,
			Data: map[string]string{"id": "1"}, CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("SaveDataPoolRow: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows?limit=1", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Count int `json:"count"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Count != 1 || payload.Total != 3 {
		t.Fatalf("payload = %+v, want count 1 total 3", payload)
	}
}

func TestBulkReplaceDataPoolRowsPreservesFileOrder(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &storage.SharedDataPool{ID: "pool-order", Name: "Order", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	rows := make([]*storage.SharedDataPoolRow, 0, 50)
	for i := 0; i < 50; i++ {
		rows = append(rows, &storage.SharedDataPoolRow{ID: uniqueOrderRowID(i), Data: map[string]string{"n": uniqueOrderRowID(i)}})
	}
	if err := store.BulkReplaceDataPoolRows(pool.ID, rows, false); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows: %v", err)
	}
	listed, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows: %v", err)
	}
	if len(listed) != 50 {
		t.Fatalf("listed %d rows, want 50", len(listed))
	}
	for i, row := range listed {
		if row.Data["n"] != uniqueOrderRowID(i) {
			t.Fatalf("row %d = %q, want %q (file order must survive listing)", i, row.Data["n"], uniqueOrderRowID(i))
		}
	}
}

func uniqueOrderRowID(i int) string {
	return "order-row-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

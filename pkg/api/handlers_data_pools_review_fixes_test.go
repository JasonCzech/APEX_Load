package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestImportAndExportDataPoolRowsEnforceProjectRoles(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	project := &storage.Project{ID: "project-import-authz", Name: "Import AuthZ", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	loaded, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	// Recreate the pool inside the scoped project (relocation is refused).
	scoped := &storage.SharedDataPool{ID: "pool-import-authz", Name: loaded.Name, ProjectID: project.ID}
	if err := store.SaveDataPool(scoped); err != nil {
		t.Fatalf("SaveDataPool(scoped): %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "authz-row", PoolID: scoped.ID, Data: map[string]string{"id": "1"}}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	enableTrustedHeaderAuth(t, server)

	do := func(req *http.Request, user, roles string) *httptest.ResponseRecorder {
		req.Header.Set("X-Auth-Request-User", user)
		req.Header.Set("X-APEXLoad-Project-Roles", roles)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	csvBody := "id\n2\n"
	if resp := do(newDataPoolImportRequest(t, scoped.ID, nil, csvBody), "foreign-editor", "some-other-project="+RoleEditor); resp.Code != http.StatusForbidden {
		t.Fatalf("foreign-project import status = %d body=%s, want 403", resp.Code, resp.Body.String())
	}
	if resp := do(newDataPoolImportRequest(t, scoped.ID, nil, csvBody), "project-viewer", project.ID+"="+RoleViewer); resp.Code != http.StatusForbidden {
		t.Fatalf("viewer import status = %d body=%s, want 403", resp.Code, resp.Body.String())
	}
	if resp := do(newDataPoolImportRequest(t, scoped.ID, nil, csvBody), "project-editor", project.ID+"="+RoleEditor); resp.Code != http.StatusOK {
		t.Fatalf("editor import status = %d body=%s, want 200", resp.Code, resp.Body.String())
	}

	if resp := do(httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+scoped.ID+"/rows/export", nil), "foreign-viewer", "some-other-project="+RoleViewer); resp.Code != http.StatusForbidden {
		t.Fatalf("foreign-project export status = %d body=%s, want 403", resp.Code, resp.Body.String())
	}
	if resp := do(httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+scoped.ID+"/rows/export", nil), "project-viewer", project.ID+"="+RoleViewer); resp.Code != http.StatusOK {
		t.Fatalf("viewer export status = %d body=%s, want 200", resp.Code, resp.Body.String())
	}
}

func TestParseDataPoolCSVEdgeCases(t *testing.T) {
	t.Run("bom and blank headers", func(t *testing.T) {
		columns, rows, warnings, err := parseDataPoolCSV("\ufeffcustomer_id,\nC-1,east\n", "", "", "")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if strings.Join(columns, "|") != "customer_id|col2" {
			t.Fatalf("columns = %v", columns)
		}
		if len(rows) != 1 || rows[0]["customer_id"] != "C-1" || rows[0]["col2"] != "east" {
			t.Fatalf("rows = %#v", rows)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "blank header") {
			t.Fatalf("warnings = %v", warnings)
		}
	})
	t.Run("ragged rows warn and drop extras", func(t *testing.T) {
		_, rows, warnings, err := parseDataPoolCSV("a,b\n1,2,3\n4\n", "", "", "")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if rows[0]["a"] != "1" || rows[0]["b"] != "2" || rows[1]["a"] != "4" || rows[1]["b"] != "" {
			t.Fatalf("rows = %#v", rows)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "more fields") {
			t.Fatalf("warnings = %v", warnings)
		}
	})
	t.Run("exact duplicate columns rejected", func(t *testing.T) {
		if _, _, _, err := parseDataPoolCSV("User,User\n1,2\n", "", "", ""); err == nil || !strings.Contains(err.Error(), "duplicate column") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("case-distinct columns remain distinct", func(t *testing.T) {
		columns, rows, _, err := parseDataPoolCSV("User,user\nAlice,alice\n", "", "", "")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !reflect.DeepEqual(columns, []string{"User", "user"}) ||
			len(rows) != 1 ||
			rows[0]["User"] != "Alice" ||
			rows[0]["user"] != "alice" {
			t.Fatalf("case-distinct columns did not round-trip: columns=%#v rows=%#v", columns, rows)
		}
	})
	t.Run("invalid delimiter rejected", func(t *testing.T) {
		if _, _, _, err := parseDataPoolCSV("a\n1\n", "semicolon", "", ""); err == nil || !strings.Contains(err.Error(), "delimiter") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid utf8 rejected rather than rewritten", func(t *testing.T) {
		if _, _, _, err := parseDataPoolCSV("a\n"+string([]byte{0xff})+"\n", "", "", ""); err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("invalid UTF-8 err = %v", err)
		}
	})
	t.Run("invalid booleans and blank explicit columns rejected", func(t *testing.T) {
		if _, _, _, err := parseDataPoolCSV("a\n1\n", "", "sometimes", ""); err == nil || !strings.Contains(err.Error(), "header") {
			t.Fatalf("invalid header err = %v", err)
		}
		if _, _, _, err := parseDataPoolCSV("1,2\n", "", "false", "a,"); err == nil || !strings.Contains(err.Error(), "blank name") {
			t.Fatalf("blank columns err = %v", err)
		}
		server, _, pool := newImportExportTestServer(t)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"dry_run": "sometimes"}, "a\n1\n"))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "dry_run") {
			t.Fatalf("invalid dry_run status = %d body=%s", resp.Code, resp.Body.String())
		}
	})
	t.Run("invalid mode rejected at handler", func(t *testing.T) {
		server, _, pool := newImportExportTestServer(t)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"mode": "merge"}, "a\n1\n"))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "mode") {
			t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
		}
	})
	t.Run("spreadsheet guard unescaped", func(t *testing.T) {
		_, rows, _, err := parseDataPoolCSV("value\n\"'=SUM(A1)\"\n\"'plain\"\n\"'-5\"\n", "", "", "", true)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if rows[0]["value"] != "=SUM(A1)" {
			t.Fatalf("guarded formula = %q, want unescaped", rows[0]["value"])
		}
		if rows[1]["value"] != "'plain" {
			t.Fatalf("plain apostrophe value = %q, want untouched", rows[1]["value"])
		}
		if rows[2]["value"] != "-5" {
			t.Fatalf("guarded negative = %q, want unescaped", rows[2]["value"])
		}
	})
	t.Run("ordinary CSV preserves literal formula apostrophe", func(t *testing.T) {
		_, rows, _, err := parseDataPoolCSV("value\n\"'=SUM(A1)\"\n", "", "", "")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if rows[0]["value"] != "'=SUM(A1)" {
			t.Fatalf("literal apostrophe value = %q, want byte-preserved import", rows[0]["value"])
		}
	})
}

func TestParseDataPoolCSVEnforcesCapsBeforeBuffering(t *testing.T) {
	over := "c\n" + strings.Repeat("v\n", maxDataPoolImportRecords+2)
	if _, _, _, err := parseDataPoolCSV(over, "", "", ""); err == nil || !strings.Contains(err.Error(), "rows") {
		t.Fatalf("record cap err = %v", err)
	}

	wideHeader := make([]string, maxDataPoolImportColumns+1)
	for i := range wideHeader {
		wideHeader[i] = fmt.Sprintf("c%d", i)
	}
	wide := strings.Join(wideHeader, ",") + "\nv\n"
	if _, _, _, err := parseDataPoolCSV(wide, "", "", ""); err == nil || !strings.Contains(err.Error(), "columns") {
		t.Fatalf("column cap err = %v", err)
	}

	// Ragged over-wide data rows must trip the incremental cells guard during
	// parse, before the whole file is materialized in memory.
	fat := "a\n" + strings.Repeat(strings.Repeat("v,", 4999)+"v\n", 500)
	if _, _, _, err := parseDataPoolCSV(fat, "", "", ""); err == nil || !strings.Contains(err.Error(), "cells") {
		t.Fatalf("cells cap err = %v", err)
	}

	t.Run("verified status metadata is outside user dimension caps", func(t *testing.T) {
		columns, rows, _, err := parseDataPoolCSVWithOptions(
			"a,b,__status\n1,2,active\n3,4,consumed\n",
			"comma",
			"true",
			"",
			dataPoolCSVParseOptions{
				metadataColumns: 1,
				maxRecords:      2,
				maxColumns:      2,
				// Header (2) plus two user rows (4); the status header
				// and two status values are transport metadata.
				maxCells: 6,
			},
		)
		if err != nil {
			t.Fatalf("metadata pushed a boundary-valid user CSV over its caps: %v", err)
		}
		if len(columns) != 3 || len(rows) != 2 {
			t.Fatalf("parsed metadata envelope = columns %d rows %d", len(columns), len(rows))
		}
	})

	t.Run("header cells count for ordinary and inactive marked imports", func(t *testing.T) {
		ordinary := "a,b\n1,2\n3,4\n"
		exact := dataPoolCSVParseOptions{maxRecords: 2, maxColumns: 2, maxCells: 6}
		if _, _, _, err := parseDataPoolCSVWithOptions(ordinary, "comma", "true", "", exact); err != nil {
			t.Fatalf("ordinary CSV at exact header-inclusive cell cap: %v", err)
		}
		over := exact
		over.maxCells = 5
		if _, _, _, err := parseDataPoolCSVWithOptions(ordinary, "comma", "true", "", over); err == nil || !strings.Contains(err.Error(), "generated header") {
			t.Fatalf("ordinary CSV omitted header from cell accounting: %v", err)
		}

		markedInactive := dataPoolExportMetadataMarker + "," + dataPoolExportStatusColumnBase + "\n" +
			"a,b," + dataPoolExportStatusColumnBase + "\n" +
			"1,2,consumed\n3,4,disabled\n"
		columns, rows, statuses, _, err := parseDataPoolCSVImportWithOptions(markedInactive, "comma", "true", "", exact)
		if err != nil {
			t.Fatalf("inactive marked export at exact header-inclusive cap: %v", err)
		}
		if len(columns) != 2 || len(rows) != 2 ||
			!reflect.DeepEqual(statuses, []string{"consumed", "disabled"}) ||
			len(activeDataPoolRows([]*storage.SharedDataPoolRow{
				{Data: rows[0], Status: statuses[0]},
				{Data: rows[1], Status: statuses[1]},
			})) != 0 {
			t.Fatalf("inactive marked parse = columns %v rows %#v statuses %#v", columns, rows, statuses)
		}
		if _, _, _, _, err := parseDataPoolCSVImportWithOptions(markedInactive, "comma", "true", "", over); err == nil || !strings.Contains(err.Error(), "cells") {
			t.Fatalf("inactive marked export omitted header from cell accounting: %v", err)
		}
	})

	t.Run("maximum user column export reimports with hidden status column", func(t *testing.T) {
		header := make([]string, 0, maxDataPoolImportColumns+1)
		values := make([]string, 0, maxDataPoolImportColumns+1)
		for index := 0; index < maxDataPoolImportColumns; index++ {
			header = append(header, fmt.Sprintf("c%d", index))
			values = append(values, "v")
		}
		header = append(header, dataPoolExportStatusColumnBase)
		values = append(values, "active")
		envelope := dataPoolExportMetadataMarker + "," + dataPoolExportStatusColumnBase + "\n" +
			strings.Join(header, ",") + "\n" + strings.Join(values, ",") + "\n"
		columns, rows, statuses, _, err := parseDataPoolCSVImport(envelope, "comma", "true", "")
		if err != nil {
			t.Fatalf("parse maximum-column APEX export: %v", err)
		}
		if len(columns) != maxDataPoolImportColumns || len(rows) != 1 || statuses[0] != "active" {
			t.Fatalf("maximum-column envelope = columns %d rows %d statuses %#v", len(columns), len(rows), statuses)
		}
	})
}

func TestDataPoolRuntimeShapeMatchesEngineCaps(t *testing.T) {
	if err := validateDataPoolImportFileSize(fileasset.MaxBytes); err != nil {
		t.Fatalf("maximum upload size rejected: %v", err)
	}
	if err := validateDataPoolImportFileSize(fileasset.MaxBytes + 1); err == nil || !strings.Contains(err.Error(), "data-source limit") {
		t.Fatalf("oversized upload err = %v", err)
	}
	if err := validateDataPoolImportFileSize(dataPoolMaxMarkedCSVBytes, true); err != nil {
		t.Fatalf("maximum marked APEX export rejected: %v", err)
	}
	if err := validateDataPoolImportFileSize(dataPoolMaxMarkedCSVBytes+1, true); err == nil || !strings.Contains(err.Error(), "APEX-export envelope") {
		t.Fatalf("oversized marked export err = %v", err)
	}
	if err := validateDataPoolImportFileSizeAgainst(14, 10, 4, true); err != nil {
		t.Fatalf("bounded marked-envelope overhead rejected: %v", err)
	}
	if err := validateDataPoolImportFileSizeAgainst(11, 10, 4, false); err == nil {
		t.Fatal("ordinary CSV received marked-envelope overhead")
	}

	if err := validateDataPoolRuntimeDimensions(maxDataPoolImportRecords, 2); err != nil {
		t.Fatalf("maximum executable pool rejected: %v", err)
	}
	if err := validateDataPoolRuntimeDimensions(maxDataPoolImportRecords+1, 1); err == nil || !strings.Contains(err.Error(), "data-row") {
		t.Fatalf("generated-header record cap err = %v", err)
	}
	if err := validateDataPoolRuntimeDimensions(maxDataPoolImportRecords, 3); err == nil || !strings.Contains(err.Error(), "cells") {
		t.Fatalf("generated-header cell cap err = %v", err)
	}

	incoming := []map[string]string{{"new": "1"}}
	if err := validateDataPoolRuntimeShape(nil, incoming, 10); err != nil {
		t.Fatalf("small replacement shape rejected: %v", err)
	}
	existing := []*storage.SharedDataPoolRow{{Data: map[string]string{"legacy": "1"}}}
	if err := validateDataPoolRuntimeShape(existing, incoming, 10); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("append union did not participate in flattened byte cap: %v", err)
	}
}

func TestImportDataPoolRowsRejectsAppendPastEngineCap(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "cap-existing", PoolID: pool.ID, Data: map[string]string{"c": "0"}}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	atCap := "c\n" + strings.Repeat("v\n", maxDataPoolImportRecords)

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"dry_run": "true"}, atCap))
	if resp.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d body=%s", resp.Code, resp.Body.String()[:200])
	}
	var preview struct {
		AppendWarning string `json:"append_warning"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(preview.AppendWarning, "engine cap") {
		t.Fatalf("dry-run append_warning = %q, want engine-cap warning", preview.AppendWarning)
	}

	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, map[string]string{"mode": "append"}, atCap))
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "engine cap") {
		t.Fatalf("append-over-cap status = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
}

func TestImportDataPoolRowsEngineShapeExcludesInactiveRows(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	inactiveData := make(map[string]string, maxDataPoolImportColumns+1)
	for index := 0; index < maxDataPoolImportColumns+1; index++ {
		inactiveData[fmt.Sprintf("legacy_%d", index)] = "unused"
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "inactive-wide-row", PoolID: pool.ID, Status: "disabled", Data: inactiveData,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, nil, "id\nactive\n"))
	if resp.Code != http.StatusOK {
		t.Fatalf("append status = %d body=%s; inactive rows must not count against the executable engine shape", resp.Code, resp.Body.String())
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows: %v", err)
	}
	if len(activeDataPoolRows(rows)) != 1 {
		t.Fatalf("active rows = %d, want imported row only", len(activeDataPoolRows(rows)))
	}
}

func TestExportDataPoolRowsEscapesFormulasAndRoundTrips(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	base := time.Now().UTC().Add(-time.Minute)
	for i, row := range []*storage.SharedDataPoolRow{
		{ID: "esc-1", PoolID: pool.ID, Status: "ready", Data: map[string]string{"User": "Alice", "user": "alice", "formula": `=HYPERLINK("https://evil.test")`, "literal_guard": "'=literal", "literal_double_guard": "''=literal", "note": "plain", " spaced ": "kept", dataPoolExportStatusColumnBase: "user-status-one"}},
		{ID: "esc-2", PoolID: pool.ID, Status: "consumed", Data: map[string]string{"User": "Bob", "user": "bob", "formula": "-5", "literal_guard": "plain", "literal_double_guard": "plain", "note": "@user", " spaced ": "also-kept", dataPoolExportStatusColumnBase: "user-status-two"}},
	} {
		row.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := store.SaveDataPoolRow(row); err != nil {
			t.Fatalf("SaveDataPoolRow(%s): %v", row.ID, err)
		}
	}

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", resp.Code, resp.Body.String())
	}
	exported := resp.Body.String()
	for _, want := range []string{`'=HYPERLINK`, "'-5", "'@user", "''=literal", "'''=literal", "kept"} {
		if !strings.Contains(exported, want) {
			t.Fatalf("export missing spreadsheet guard %q: %q", want, exported)
		}
	}
	exportRecords := readDataPoolExportCSV(t, strings.NewReader(exported))
	if len(exportRecords) < 2 || exportRecords[0][1] != dataPoolExportStatusColumnBase+"_2" {
		t.Fatalf("status metadata did not avoid user-column collision: %#v", exportRecords)
	}

	// Re-importing a renamed guarded export restores original values and row
	// statuses; marker detection must not depend on the filename suffix.
	second := &storage.SharedDataPool{ID: "pool-roundtrip", Name: "Roundtrip", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(second); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequestWithFilename(t, second.ID, nil, "renamed-backup.csv", exported))
	if resp.Code != http.StatusOK {
		t.Fatalf("re-import status = %d body=%s", resp.Code, resp.Body.String())
	}
	rows, err := store.ListDataPoolRows(second.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}
	if rows[0].Data["formula"] != `=HYPERLINK("https://evil.test")` ||
		rows[0].Data["literal_guard"] != "'=literal" ||
		rows[0].Data["literal_double_guard"] != "''=literal" ||
		rows[0].Data["spaced"] != "kept" ||
		rows[0].Data["User"] != "Alice" ||
		rows[0].Data["user"] != "alice" ||
		rows[0].Data[dataPoolExportStatusColumnBase] != "user-status-one" ||
		rows[0].Status != "ready" ||
		rows[1].Data["formula"] != "-5" ||
		rows[1].Data["note"] != "@user" ||
		rows[1].Data["User"] != "Bob" ||
		rows[1].Data["user"] != "bob" ||
		rows[1].Data[dataPoolExportStatusColumnBase] != "user-status-two" ||
		rows[1].Status != "consumed" {
		t.Fatalf("round-trip values mutated: %#v / %#v", rows[0].Data, rows[1].Data)
	}
}

func TestImportSpoofedAPEXFilenameDoesNotDecodeOrdinaryCSV(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequestWithFilename(
		t,
		pool.ID,
		nil,
		"spoofed.apexload.csv",
		"value\n\"'=SUM(A1)\"\n",
	))
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%s", resp.Code, resp.Body.String())
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %#v err=%v", rows, err)
	}
	if got := rows[0].Data["value"]; got != "'=SUM(A1)" {
		t.Fatalf("spoofed suffix decoded literal apostrophe: %q", got)
	}
}

func TestParseDataPoolCSVPreservesLeadingValueWhitespace(t *testing.T) {
	columns, rows, _, err := parseDataPoolCSV("id,value\n1,  padded\n", "comma", "true", "")
	if err != nil {
		t.Fatalf("parseDataPoolCSV: %v", err)
	}
	if !reflect.DeepEqual(columns, []string{"id", "value"}) {
		t.Fatalf("columns = %#v", columns)
	}
	if len(rows) != 1 || rows[0]["value"] != "  padded" {
		t.Fatalf("leading value whitespace was not preserved: %#v", rows)
	}
}

type replaceRaceDataPoolStore struct {
	*storage.SQLiteStore
	beforeReplace func() error
}

func (s *replaceRaceDataPoolStore) BulkReplaceDataPoolRowsIfRevision(poolID string, rows []*storage.SharedDataPoolRow, expectedRevision string) (bool, error) {
	if s.beforeReplace != nil {
		before := s.beforeReplace
		s.beforeReplace = nil
		if err := before(); err != nil {
			return false, err
		}
	}
	return s.SQLiteStore.BulkReplaceDataPoolRowsIfRevision(poolID, rows, expectedRevision)
}

func TestReplaceDataPoolImportRejectsConcurrentSameCountEdit(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	existing := &storage.SharedDataPoolRow{
		ID: "replace-race-existing", PoolID: pool.ID, Data: map[string]string{"id": "before"},
	}
	if err := store.SaveDataPoolRow(existing); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	raceStore := &replaceRaceDataPoolStore{SQLiteStore: store}
	raceStore.beforeReplace = func() error {
		current, err := store.GetDataPoolRowFromPool(pool.ID, existing.ID)
		if err != nil {
			return err
		}
		current.Data = map[string]string{"id": "concurrent"}
		updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, current, current.Version)
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("concurrent row update was not applied")
		}
		return nil
	}
	server.store = raceStore

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(
		t,
		pool.ID,
		map[string]string{"mode": "replace"},
		"id\nreplacement\n",
	))
	if resp.Code != http.StatusConflict {
		t.Fatalf("replace status = %d body=%s, want 409", resp.Code, resp.Body.String())
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != existing.ID || rows[0].Data["id"] != "concurrent" {
		t.Fatalf("concurrent row was erased by replace: %#v", rows)
	}
}

type failingDataPoolExportWriter struct {
	header http.Header
}

func (w *failingDataPoolExportWriter) Header() http.Header {
	return w.header
}

func (w *failingDataPoolExportWriter) Write([]byte) (int, error) {
	return 0, errors.New("client write failed")
}

func (w *failingDataPoolExportWriter) WriteHeader(int) {}

func TestExportDataPoolRowsAbortsOnWriterFailure(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "write-failure-row", PoolID: pool.ID, Data: map[string]string{"id": "1"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil)
	req.SetPathValue("id", pool.ID)
	writer := &failingDataPoolExportWriter{header: make(http.Header)}
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("panic = %#v, want http.ErrAbortHandler", recovered)
		}
		events, err := store.ListAuditEvents(storage.AuditEventFilter{
			Action:     "data_pool.rows.export",
			ResourceID: pool.ID,
			Limit:      10,
		})
		if err != nil {
			t.Fatalf("ListAuditEvents: %v", err)
		}
		if len(events) != 2 || events[0].Status != "failed" || events[1].Status != "pending" {
			t.Fatalf("export audit events = %#v, want failed outcome plus preceding pending intent", events)
		}
		if events[0].Details["outcome"] != "stream_failed" {
			t.Fatalf("failure outcome = %#v, want stream_failed", events[0].Details["outcome"])
		}
		if events[0].Details["operation_id"] == "" || events[0].Details["operation_id"] != events[1].Details["operation_id"] {
			t.Fatalf("export audit operation ids do not correlate: %#v / %#v", events[0].Details, events[1].Details)
		}
	}()
	server.handleExportDataPoolRows(writer, req)
}

func TestExportDataPoolRowsRecordsCanceledOutcome(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "canceled-export-row", PoolID: pool.ID, Data: map[string]string{"id": "1"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil).WithContext(ctx)
	req.SetPathValue("id", pool.ID)
	server.handleExportDataPoolRows(httptest.NewRecorder(), req)

	events, err := store.ListAuditEvents(storage.AuditEventFilter{
		Action:     "data_pool.rows.export",
		ResourceID: pool.ID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 2 || events[0].Status != "failed" || events[1].Status != "pending" {
		t.Fatalf("export audit events = %#v, want failed outcome plus preceding pending intent", events)
	}
	if events[0].Details["outcome"] != "client_canceled" {
		t.Fatalf("cancel outcome = %#v, want client_canceled", events[0].Details["outcome"])
	}
	if events[0].Details["operation_id"] == "" || events[0].Details["operation_id"] != events[1].Details["operation_id"] {
		t.Fatalf("export audit operation ids do not correlate: %#v / %#v", events[0].Details, events[1].Details)
	}
}

type mutatingDataPoolExportStore struct {
	*storage.SQLiteStore
	poolID    string
	pageCalls int
}

func (s *mutatingDataPoolExportStore) ListDataPoolRowsPageAfter(poolID, afterKey, afterID string, limit int) ([]*storage.SharedDataPoolRow, string, error) {
	s.pageCalls++
	// Column discovery and the restorable-envelope preflight are both fenced
	// before response headers. Mutate on the actual streaming pass.
	if s.pageCalls == 3 {
		if err := s.SQLiteStore.SaveDataPoolRow(&storage.SharedDataPoolRow{
			ID: s.poolID + "-concurrent", PoolID: s.poolID, Data: map[string]string{"new_column": "must-not-be-dropped"},
		}); err != nil {
			return nil, "", err
		}
	}
	return s.SQLiteStore.ListDataPoolRowsPageAfter(poolID, afterKey, afterID, limit)
}

func TestExportDataPoolRowsAbortsWhenRowsChangeBetweenHeaderAndBodyPasses(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "snapshot-original", PoolID: pool.ID, Data: map[string]string{"original_column": "original"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	server.store = &mutatingDataPoolExportStore{SQLiteStore: store, poolID: pool.ID}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil)
	req.SetPathValue("id", pool.ID)
	resp := httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("panic = %#v, want http.ErrAbortHandler so the cross-revision backup cannot complete", recovered)
		}
	}()
	server.handleExportDataPoolRows(resp, req)
}

func TestDataPoolExportPreflightEnforcesExactRestorableEnvelope(t *testing.T) {
	rows := []*storage.SharedDataPoolRow{{
		ID:     "inactive-status-row",
		Status: "consumed,status-with-commas",
		Data:   map[string]string{"formula": "=1+1", "id": "inactive"},
	}}
	forEachRow := func(fn func(*storage.SharedDataPoolRow) error) error {
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	}
	columns := []string{"formula", "id"}
	statusColumn := dataPoolExportStatusColumnBase
	unbounded := &dataPoolCSVLimitWriter{max: 1 << 20}
	if err := writeDataPoolExportCSV(unbounded, columns, statusColumn, forEachRow); err != nil {
		t.Fatalf("measure export: %v", err)
	}
	exactBytes := unbounded.written
	if exactBytes == 0 {
		t.Fatal("measured export is empty")
	}
	if err := writeDataPoolExportCSV(&dataPoolCSVLimitWriter{max: exactBytes}, columns, statusColumn, forEachRow); err != nil {
		t.Fatalf("exact envelope boundary rejected: %v", err)
	}
	err := writeDataPoolExportCSV(&dataPoolCSVLimitWriter{max: exactBytes - 1}, columns, statusColumn, forEachRow)
	if !errors.Is(err, errDataPoolCSVByteLimit) {
		t.Fatalf("one-byte-oversized inactive/status export err = %v, want byte limit", err)
	}
}

func TestExportDataPoolRowsRejectsNonRestorableInactiveDimensionsBefore200(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	wideInactiveData := make(map[string]string, maxDataPoolImportColumns+1)
	for index := 0; index <= maxDataPoolImportColumns; index++ {
		wideInactiveData[fmt.Sprintf("inactive_%d", index)] = "unused"
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "legacy-inactive-wide-export", PoolID: pool.ID, Status: "disabled", Data: wideInactiveData,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("wide inactive export status = %d body=%s, want 413 before an unusable backup is emitted", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); strings.HasPrefix(got, "text/csv") {
		t.Fatalf("oversized export committed CSV headers: %q", got)
	}
}

func TestExportDataPoolRowsStreamsMultiplePagesInOrder(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	total := dataPoolExportPageSize*2 + 500
	rows := make([]*storage.SharedDataPoolRow, 0, total)
	for i := 0; i < total; i++ {
		rows = append(rows, &storage.SharedDataPoolRow{ID: uuid.NewString(), Data: map[string]string{"n": fmt.Sprintf("%06d", i)}})
	}
	if err := store.BulkReplaceDataPoolRows(pool.ID, rows, false); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows: %v", err)
	}

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("export status = %d", resp.Code)
	}
	records := readDataPoolExportCSV(t, resp.Body)
	if len(records) != total+2 {
		t.Fatalf("export records = %d, want %d", len(records), total+2)
	}
	for i := 2; i < len(records); i++ {
		if want := fmt.Sprintf("%06d", i-2); records[i][0] != want {
			t.Fatalf("record %d = %q, want %q (keyset pagination must preserve order)", i, records[i][0], want)
		}
	}
}

func TestExportDataPoolRowsEmptyPoolProducesRestorableEnvelope(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("empty export status = %d body=%s, want 200", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("content type = %q", got)
	}
	exported := resp.Body.String()
	records := readDataPoolExportCSV(t, strings.NewReader(exported))
	if !reflect.DeepEqual(records, [][]string{
		{dataPoolExportMetadataMarker, dataPoolExportStatusColumnBase},
		{dataPoolExportStatusColumnBase},
	}) {
		t.Fatalf("empty export records = %#v", records)
	}

	target := &storage.SharedDataPool{ID: "pool-empty-restore", Name: "Empty restore", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(target); err != nil {
		t.Fatalf("SaveDataPool(target): %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "row-to-clear", PoolID: target.ID, Data: map[string]string{"id": "remove-me"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow(target): %v", err)
	}
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, newDataPoolImportRequestWithFilename(
		t,
		target.ID,
		map[string]string{"dry_run": "true"},
		"renamed-empty.csv",
		exported,
	))
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview empty export status = %d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview struct {
		DryRun        bool   `json:"dry_run"`
		Mode          string `json:"mode"`
		RowCount      int    `json:"row_count"`
		AppendWarning string `json:"append_warning"`
	}
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode empty preview: %v", err)
	}
	if !preview.DryRun || preview.Mode != "append" || preview.RowCount != 0 ||
		!strings.Contains(preview.AppendWarning, "mode=replace") {
		t.Fatalf("empty preview = %#v, want replace-only append warning", preview)
	}
	rejectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rejectResp, newDataPoolImportRequestWithFilename(
		t,
		target.ID,
		map[string]string{"mode": "append"},
		"renamed-empty.csv",
		exported,
	))
	if rejectResp.Code != http.StatusBadRequest || !strings.Contains(rejectResp.Body.String(), "mode=replace") {
		t.Fatalf("committed empty append status = %d body=%s, want replace-only rejection", rejectResp.Code, rejectResp.Body.String())
	}
	importResp := httptest.NewRecorder()
	server.mux.ServeHTTP(importResp, newDataPoolImportRequestWithFilename(
		t,
		target.ID,
		map[string]string{"mode": "replace"},
		"renamed-empty.csv",
		exported,
	))
	if importResp.Code != http.StatusOK {
		t.Fatalf("restore empty export status = %d body=%s", importResp.Code, importResp.Body.String())
	}
	if count, err := store.CountDataPoolRows(target.ID); err != nil || count != 0 {
		t.Fatalf("restored empty pool count = %d err=%v", count, err)
	}
}

func TestDataPoolTimestampsSurviveClientEchoOnUpdate(t *testing.T) {
	server, store, _ := newImportExportTestServer(t)

	createResp := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", strings.NewReader(`{"name":"Echo Pool","project_id":"`+storage.DefaultProjectID+`"}`))
	createReq.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created struct {
		ID        string    `json:"id"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("updated_at %v is not microsecond-aligned; Postgres would truncate it and the echoed CAS token could never match", created.UpdatedAt)
	}

	// A client echoing the token from the POST response must be able to update.
	payload, _ := json.Marshal(map[string]any{"name": "Echo Pool Renamed", "updated_at": created.UpdatedAt})
	putResp := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+created.ID, strings.NewReader(string(payload)))
	putReq.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("echoed-token PUT status = %d body=%s, want 200", putResp.Code, putResp.Body.String())
	}

	row := &storage.SharedDataPoolRow{ID: "align-row", PoolID: created.ID, Data: map[string]string{"a": "1"}}
	if err := store.SaveDataPoolRow(row); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	if row.UpdatedAt.Nanosecond()%1000 != 0 || row.CreatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("row timestamps not microsecond-aligned: %v / %v", row.CreatedAt, row.UpdatedAt)
	}
}

func TestCreateDataPoolAlwaysStartsActive(t *testing.T) {
	server, store, _ := newImportExportTestServer(t)
	requestedArchive := time.Now().UTC().Add(-time.Hour)
	payload, err := json.Marshal(map[string]any{
		"name":        "Must start active",
		"project_id":  storage.DefaultProjectID,
		"archived_at": requestedArchive,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.Code, resp.Body.String())
	}
	var created storage.SharedDataPool
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ArchivedAt != nil {
		t.Fatalf("create response retained client archived_at: %v", created.ArchivedAt)
	}
	persisted, err := store.GetDataPool(created.ID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	if persisted.ArchivedAt != nil {
		t.Fatalf("created pool persisted as archived: %v", persisted.ArchivedAt)
	}
}

func TestDirectDataPoolRowMutationsRejectInvalidRuntimeShapeTransactionally(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	wide := make(map[string]string, maxDataPoolImportColumns+1)
	for index := 0; index <= maxDataPoolImportColumns; index++ {
		wide[fmt.Sprintf("column_%04d", index)] = "value"
	}

	createBody, err := json.Marshal(map[string]any{"data": wide, "status": "active"})
	if err != nil {
		t.Fatal(err)
	}
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+pool.ID+"/rows", strings.NewReader(string(createBody)))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusBadRequest || !strings.Contains(createResponse.Body.String(), "column engine cap") {
		t.Fatalf("wide create response = %d %s, want transactional cap rejection", createResponse.Code, createResponse.Body.String())
	}
	if revision, count, err := store.DataPoolRowsPageState(pool.ID); err != nil || revision != "0" || count != 0 {
		t.Fatalf("rejected create changed paging state: revision=%q count=%d err=%v", revision, count, err)
	}

	row := &storage.SharedDataPoolRow{ID: "valid-row", PoolID: pool.ID, Data: map[string]string{"id": "before"}, Status: "active"}
	if err := store.SaveDataPoolRow(row); err != nil {
		t.Fatalf("seed valid row: %v", err)
	}
	revisionBefore, countBefore, err := store.DataPoolRowsPageState(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	updateBody, err := json.Marshal(map[string]any{"data": wide, "status": "active", "version": row.Version})
	if err != nil {
		t.Fatal(err)
	}
	updateRequest := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID+"/rows/"+row.ID, strings.NewReader(string(updateBody)))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusBadRequest || !strings.Contains(updateResponse.Body.String(), "column engine cap") {
		t.Fatalf("wide update response = %d %s, want transactional cap rejection", updateResponse.Code, updateResponse.Body.String())
	}
	persisted, err := store.GetDataPoolRowFromPool(pool.ID, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != row.Version || persisted.Data["id"] != "before" || len(persisted.Data) != 1 {
		t.Fatalf("rejected update committed candidate row: %#v", persisted)
	}
	revisionAfter, countAfter, err := store.DataPoolRowsPageState(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revisionAfter != revisionBefore || countAfter != countBefore {
		t.Fatalf("rejected update changed paging state: before=%s/%d after=%s/%d", revisionBefore, countBefore, revisionAfter, countAfter)
	}
}

func TestUpdateDataPoolArchivedAtPresenceSemantics(t *testing.T) {
	server, store, pool := newImportExportTestServer(t)
	loaded, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	archivedAt := time.Now().UTC()
	loaded.ArchivedAt = &archivedAt
	loaded.Description = "retained description"
	loaded.Schema = map[string]string{"id": "string"}
	if err := store.SaveDataPool(loaded); err != nil {
		t.Fatalf("SaveDataPool(archive): %v", err)
	}

	put := func(name string, archivedAt any, expectedUpdatedAt time.Time) (*httptest.ResponseRecorder, storage.SharedDataPool) {
		body := map[string]any{"name": name, "updated_at": expectedUpdatedAt}
		if archivedAt != "absent" {
			body["archived_at"] = archivedAt
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal update: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		var updated storage.SharedDataPool
		if resp.Code == http.StatusOK {
			if err := json.Unmarshal(resp.Body.Bytes(), &updated); err != nil {
				t.Fatalf("json.Unmarshal updated pool: %v", err)
			}
		}
		return resp, updated
	}

	// A rename that omits archived_at must not silently un-archive the pool.
	resp, renamed := put("Renamed While Archived", "absent", loaded.UpdatedAt)
	if resp.Code != http.StatusOK {
		t.Fatalf("rename status = %d body=%s", resp.Code, resp.Body.String())
	}
	afterRename, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool(after rename): %v", err)
	}
	if afterRename.ArchivedAt == nil {
		t.Fatalf("rename with absent archived_at un-archived the pool")
	}
	if afterRename.Description != "retained description" || afterRename.Schema["id"] != "string" {
		t.Fatalf("rename with absent metadata cleared fields: %#v", afterRename)
	}

	// Explicit null restores.
	resp, _ = put("Restored", nil, renamed.UpdatedAt)
	if resp.Code != http.StatusOK {
		t.Fatalf("restore status = %d body=%s", resp.Code, resp.Body.String())
	}
	afterRestore, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool(after restore): %v", err)
	}
	if afterRestore.ArchivedAt != nil {
		t.Fatalf("explicit null did not restore the pool")
	}
	clearPayload, err := json.Marshal(map[string]any{
		"name": "Cleared metadata", "description": nil, "schema": nil, "updated_at": afterRestore.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	clearReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader(clearPayload))
	clearReq.Header.Set("Content-Type", "application/json")
	clearResp := httptest.NewRecorder()
	server.mux.ServeHTTP(clearResp, clearReq)
	if clearResp.Code != http.StatusOK {
		t.Fatalf("clear metadata status = %d body=%s", clearResp.Code, clearResp.Body.String())
	}
	cleared, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Description != "" || cleared.Schema != nil {
		t.Fatalf("explicit null metadata was not cleared: %#v", cleared)
	}
}

func TestValidateScenarioLoadModelReportsArchivedPoolAsBlockedItem(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	pool, err := server.store.GetDataPool(poolID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	archivedAt := time.Now().UTC()
	pool.ArchivedAt = &archivedAt
	if err := server.store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool(archive): %v", err)
	}
	report, err := server.runner.ValidateScenarioLoadModel(CreateScenarioRequest{
		Name: "Archived report", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID: poolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
		}},
	})
	if err != nil {
		t.Fatalf("ValidateScenarioLoadModel must report archived pools, not fail: %v", err)
	}
	found := false
	for _, item := range report.Items {
		if item.Dimension == "data_pool_rows" && item.Status == "blocked" && strings.Contains(item.Summary, "archived") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no archived blocked item in report: %s", mustJSON(report.Items))
	}
}

func TestMaterializationKeepsUnknownRowStatuses(t *testing.T) {
	store := &governanceDataPoolStore{
		pool: &storage.SharedDataPool{ID: "pool-tags", Name: "Tagged rows", ProjectID: storage.DefaultProjectID},
		rows: []*storage.SharedDataPoolRow{
			{ID: "row-ready", Data: map[string]string{"id": "ready-1"}, Status: "ready"},
			{ID: "row-available", Data: map[string]string{"id": "available-1"}, Status: "available"},
			{ID: "row-consumed", Data: map[string]string{"id": "consumed-1"}, Status: "consumed"},
			{ID: "row-used", Data: map[string]string{"id": "used-1"}, Status: "used"},
		},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.dataPoolStore = store
	sources, err := runner.buildSharedDataSourcesLocked([]storage.ScenarioDataPoolBinding{{
		PoolID: "pool-tags", Alias: "tagged", AccessMode: "read_only", SelectionStrategy: "sequential",
	}})
	if err != nil {
		t.Fatalf("buildSharedDataSourcesLocked: %v", err)
	}
	inline := sources[0].InlineData
	for _, want := range []string{"ready-1", "available-1"} {
		if !strings.Contains(inline, want) {
			t.Fatalf("free-text status row %q was excluded from execution: %q", want, inline)
		}
	}
	for _, reject := range []string{"consumed-1", "used-1"} {
		if strings.Contains(inline, reject) {
			t.Fatalf("spent row %q leaked into execution: %q", reject, inline)
		}
	}
}

func TestImportAndExportWriteAuditEvents(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	project := &storage.Project{ID: "project-pool-io-audit", Name: "Pool IO", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	pool := &storage.SharedDataPool{ID: "pool-io-audit", Name: "Audited", ProjectID: project.ID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, newDataPoolImportRequest(t, pool.ID, nil, "id\n1\n"))
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%s", resp.Code, resp.Body.String())
	}
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows/export", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", resp.Code, resp.Body.String())
	}

	exportEvents, err := store.ListAuditEvents(storage.AuditEventFilter{
		Action:     "data_pool.rows.export",
		ResourceID: pool.ID,
		ProjectID:  project.ID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents(export): %v", err)
	}
	if len(exportEvents) != 2 || exportEvents[0].Status != "success" || exportEvents[1].Status != "pending" {
		t.Fatalf("export audit events = %#v, want success outcome plus preceding pending intent", exportEvents)
	}
	if exportEvents[0].Details["outcome"] != "exported" ||
		exportEvents[0].Details["operation_id"] == "" ||
		exportEvents[0].Details["operation_id"] != exportEvents[1].Details["operation_id"] {
		t.Fatalf("export audit details do not correlate: %#v / %#v", exportEvents[0].Details, exportEvents[1].Details)
	}
	importEvents, err := store.ListAuditEvents(storage.AuditEventFilter{
		Action:     "data_pool.rows.import",
		ResourceID: pool.ID,
		ProjectID:  project.ID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents(import): %v", err)
	}
	if len(importEvents) != 1 || importEvents[0].Status != "success" {
		t.Fatalf("import audit events = %#v, want one success", importEvents)
	}
}

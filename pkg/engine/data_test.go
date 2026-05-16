package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func writeCSV(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestNewDataProvider_CSV(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "users.csv", "username,password\nalice,pass1\nbob,pass2\n")

	dp, err := NewDataProvider([]dsl.DataSource{
		{Name: "users", Type: "csv", Path: path, FirstRowHeader: true, Strategy: "sequential"},
	})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}
	if !dp.HasSources() {
		t.Error("HasSources should be true")
	}
	names := dp.SourceNames()
	if len(names) != 1 || names[0] != "users" {
		t.Errorf("SourceNames = %v", names)
	}
}

func TestDataProvider_Sequential(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "id,name\n1,alice\n2,bob\n3,charlie\n")

	dp, err := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: true, Strategy: "sequential"},
	})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	row1 := dp.GetRow("data", 0)
	if row1["data.id"] != "1" || row1["data.name"] != "alice" {
		t.Errorf("row1 = %v", row1)
	}

	row2 := dp.GetRow("data", 0)
	if row2["data.id"] != "2" || row2["data.name"] != "bob" {
		t.Errorf("row2 = %v", row2)
	}

	row3 := dp.GetRow("data", 0)
	if row3["data.id"] != "3" || row3["data.name"] != "charlie" {
		t.Errorf("row3 = %v", row3)
	}

	// Should wrap around
	row4 := dp.GetRow("data", 0)
	if row4["data.id"] != "1" {
		t.Errorf("row4 should wrap to first, got id=%q", row4["data.id"])
	}
}

func TestDataProvider_Unique(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "id\n1\n2\n3\n")

	dp, _ := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: true, Strategy: "unique"},
	})

	row0 := dp.GetRow("data", 0)
	row1 := dp.GetRow("data", 1)
	row2 := dp.GetRow("data", 2)

	if row0["data.id"] != "1" {
		t.Errorf("vuser 0 should get row 0, got id=%q", row0["data.id"])
	}
	if row1["data.id"] != "2" {
		t.Errorf("vuser 1 should get row 1, got id=%q", row1["data.id"])
	}
	if row2["data.id"] != "3" {
		t.Errorf("vuser 2 should get row 2, got id=%q", row2["data.id"])
	}

	// Wraps around
	row3 := dp.GetRow("data", 3)
	if row3["data.id"] != "1" {
		t.Errorf("vuser 3 should wrap to row 0, got id=%q", row3["data.id"])
	}
}

func TestDataProvider_Pop(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "id\n1\n2\n3\n")

	dp, err := NewDataProvider([]dsl.DataSource{{
		Name:           "data",
		Type:           "csv",
		Path:           path,
		FirstRowHeader: true,
		Strategy:       "pop",
	}})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	row1 := dp.GetRow("data", 0)
	row2 := dp.GetRow("data", 99)
	row3 := dp.GetRow("data", 1)
	row4 := dp.GetRow("data", 2)

	if row1["data.id"] != "1" {
		t.Fatalf("first pop row id = %q, want 1", row1["data.id"])
	}
	if row2["data.id"] != "2" {
		t.Fatalf("second pop row id = %q, want 2", row2["data.id"])
	}
	if row3["data.id"] != "3" {
		t.Fatalf("third pop row id = %q, want 3", row3["data.id"])
	}
	if row4 != nil {
		t.Fatalf("expected pop source exhaustion to return nil, got %#v", row4)
	}
}

func TestDataProvider_Random(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "val\na\nb\nc\n")

	dp, _ := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: true, Strategy: "random"},
	})

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		row := dp.GetRow("data", 0)
		seen[row["data.val"]] = true
	}

	// With 100 draws from 3 values, we should see all three
	if len(seen) < 2 {
		t.Errorf("random strategy should produce varied results, only got %v", seen)
	}
}

func TestDataProvider_AutoColumns(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "alice,pass1\nbob,pass2\n")

	dp, err := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: false, Strategy: "sequential"},
	})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	row := dp.GetRow("data", 0)
	if row["data.col_1"] != "alice" {
		t.Errorf("col_1 = %q, want %q", row["data.col_1"], "alice")
	}
	if row["data.col_2"] != "pass1" {
		t.Errorf("col_2 = %q, want %q", row["data.col_2"], "pass1")
	}
}

func TestDataProvider_ExplicitColumns(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "alice,pass1\nbob,pass2\n")

	dp, _ := NewDataProvider([]dsl.DataSource{
		{Name: "users", Type: "csv", Path: path, Columns: []string{"username", "password"}, Strategy: "sequential"},
	})

	row := dp.GetRow("users", 0)
	if row["users.username"] != "alice" {
		t.Errorf("username = %q", row["users.username"])
	}
}

func TestDataProvider_TabDelimited(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.dat", "id\tname\n1\talice\n")

	dp, _ := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "dat", Path: path, FirstRowHeader: true, Strategy: "sequential"},
	})

	row := dp.GetRow("data", 0)
	if row["data.id"] != "1" || row["data.name"] != "alice" {
		t.Errorf("dat row = %v", row)
	}
}

func TestDataProvider_CustomDelimiter(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "id;name\n1;alice\n")

	dp, _ := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: true, Delimiter: ";", Strategy: "sequential"},
	})

	row := dp.GetRow("data", 0)
	if row["data.id"] != "1" || row["data.name"] != "alice" {
		t.Errorf("custom delimiter row = %v", row)
	}
}

func TestDataProvider_FileNotFound(t *testing.T) {
	_, err := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: "/nonexistent/file.csv"},
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDataProvider_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "empty.csv", "")

	_, err := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path},
	})
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestDataProvider_HeaderOnly(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "header.csv", "id,name\n")

	_, err := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: true},
	})
	if err == nil {
		t.Fatal("expected error for header-only file (no data rows)")
	}
}

func TestDataProvider_GetRow_UnknownSource(t *testing.T) {
	dp := &DataProvider{sources: make(map[string]*dataSource)}
	row := dp.GetRow("nonexistent", 0)
	if row != nil {
		t.Errorf("expected nil for unknown source, got %v", row)
	}
}

func TestDataProvider_NoSources(t *testing.T) {
	dp := &DataProvider{sources: make(map[string]*dataSource)}
	if dp.HasSources() {
		t.Error("HasSources should be false for empty provider")
	}
}

func TestDataProvider_DefaultStrategy(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "data.csv", "val\na\nb\n")

	dp, _ := NewDataProvider([]dsl.DataSource{
		{Name: "data", Type: "csv", Path: path, FirstRowHeader: true},
	})

	// Default should be sequential
	row := dp.GetRow("data", 0)
	if row["data.val"] != "a" {
		t.Errorf("default strategy should be sequential, got val=%q", row["data.val"])
	}
}

func TestDataProvider_InlineData(t *testing.T) {
	dp, err := NewDataProvider([]dsl.DataSource{
		{
			Name:           "users",
			Type:           "csv",
			InlineData:     "username,password\nalice,pass1\nbob,pass2\n",
			FirstRowHeader: true,
			Strategy:       "sequential",
		},
	})
	if err != nil {
		t.Fatalf("NewDataProvider inline_data: %v", err)
	}

	row := dp.GetRow("users", 0)
	if row["users.username"] != "alice" || row["users.password"] != "pass1" {
		t.Fatalf("unexpected row: %#v", row)
	}
}

func TestDataProvider_InlineDataPreferredOverPath(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "users.csv", "username,password\nfile,source\n")

	dp, err := NewDataProvider([]dsl.DataSource{
		{
			Name:           "users",
			Type:           "csv",
			Path:           path,
			InlineData:     "username,password\ninline,source\n",
			FirstRowHeader: true,
		},
	})
	if err != nil {
		t.Fatalf("NewDataProvider inline preferred: %v", err)
	}

	row := dp.GetRow("users", 0)
	if row["users.username"] != "inline" {
		t.Fatalf("expected inline data to win, got %#v", row)
	}
}

func TestDataProvider_MissingPathAndInlineData(t *testing.T) {
	_, err := NewDataProvider([]dsl.DataSource{
		{Name: "users", Type: "csv"},
	})
	if err == nil {
		t.Fatal("expected error for missing path and inline_data")
	}
}

func TestDataProvider_RejectsUnsupportedStrategy(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "users.csv", "username\nalice\n")

	_, err := NewDataProvider([]dsl.DataSource{{
		Name:           "users",
		Type:           "csv",
		Path:           path,
		FirstRowHeader: true,
		Strategy:       "mystery",
	}})
	if err == nil {
		t.Fatal("expected unsupported strategy to fail")
	}
}

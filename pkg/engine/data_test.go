package engine

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

func writeCSV(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatalf("register data root: %v", err)
	}
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

func TestNewDataProvider_CSVStorageRef(t *testing.T) {
	dir := t.TempDir()
	path := writeCSV(t, dir, "users.csv", "username,password\nalice,pass1\n")
	ref := (&url.URL{Scheme: "file", Path: path}).String()

	dp, err := NewDataProvider([]dsl.DataSource{{
		Name: "users", Type: "csv", StorageRef: ref, FirstRowHeader: true, Strategy: "sequential",
	}})
	if err != nil {
		t.Fatalf("NewDataProvider CSV storage_ref: %v", err)
	}
	row := dp.GetRow("users", 0)
	if row["users.username"] != "alice" || row["users.password"] != "pass1" {
		t.Fatalf("storage_ref row = %#v", row)
	}
}

func TestNewDataProvider_CSVStorageRefRejectsOutsideAllowedRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.csv")
	if err := os.WriteFile(path, []byte("username\nalice\n"), 0o600); err != nil {
		t.Fatalf("write ungoverned CSV: %v", err)
	}
	ref := (&url.URL{Scheme: "file", Path: path}).String()

	_, err := NewDataProvider([]dsl.DataSource{{
		Name: "users", Type: "csv", StorageRef: ref, FirstRowHeader: true, Strategy: "sequential",
	}})
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("ungoverned CSV storage_ref error = %v", err)
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

func TestDataProvider_DistributedShardPartitionsDoNotOverlap(t *testing.T) {
	const assetName = "sha256-users.csv"
	source := dsl.DataSource{
		Name:           "data",
		Type:           "csv",
		StagedAsset:    assetName,
		FirstRowHeader: true,
	}
	assets := map[string][]byte{assetName: []byte("id\n0\n1\n2\n3\n4\n5\n")}

	for _, strategy := range []string{"sequential", "unique", "pop"} {
		t.Run(strategy, func(t *testing.T) {
			source.Strategy = strategy
			providers := make([]*DataProvider, 2)
			for ordinal := range providers {
				provider, err := NewShardedDataProviderWithAssets(
					[]dsl.DataSource{source},
					DataShardConfig{Ordinal: uint32(ordinal), Count: 2},
					assets,
				)
				if err != nil {
					t.Fatalf("new shard %d: %v", ordinal, err)
				}
				providers[ordinal] = provider
			}

			seen := [2]map[string]struct{}{{}, {}}
			for ordinal, provider := range providers {
				for localID := 0; localID < 3; localID++ {
					// Exercise the large disjoint runtime namespaces used by the
					// controller instead of assuming compact IDs.
					vuserID := ordinal*int(1<<31-1) + localID
					row := provider.GetRow("data", vuserID)
					if row == nil {
						t.Fatalf("shard %d row %d is nil", ordinal, localID)
					}
					seen[ordinal][row["data.id"]] = struct{}{}
				}
			}
			for value := range seen[0] {
				if _, duplicate := seen[1][value]; duplicate {
					t.Fatalf("row %q was selected by both shards: %#v", value, seen)
				}
			}

			if strategy == "pop" {
				if row := providers[0].GetRow("data", 99); row != nil {
					t.Fatalf("pop shard returned row after its partition was exhausted: %#v", row)
				}
			}
			for ordinal, provider := range providers {
				rows := provider.GetAllRows("data")
				values, ok := provider.GetColumnValues("data", "id")
				if !ok || len(rows) != 3 || len(values) != 3 {
					t.Fatalf("shard %d views = rows:%#v values:%#v ok:%v", ordinal, rows, values, ok)
				}
				for index := range rows {
					if rows[index]["id"] != values[index] {
						t.Fatalf("shard %d row/column view mismatch at %d: %#v vs %#v", ordinal, index, rows, values)
					}
				}
			}
		})
	}
}

func TestDataProvider_StagedAssetMustBePresent(t *testing.T) {
	_, err := NewShardedDataProviderWithAssets([]dsl.DataSource{{
		Name:           "data",
		Type:           "csv",
		StagedAsset:    "missing.csv",
		FirstRowHeader: true,
	}}, DataShardConfig{}, nil)
	if err == nil {
		t.Fatal("missing staged asset was accepted")
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

func TestDataProvider_AutoDelimiter(t *testing.T) {
	dp, err := NewDataProvider([]dsl.DataSource{
		{
			Name:           "users",
			Type:           "csv",
			InlineData:     "username\tpassword\nalice\tpass1\nbob\tpass2\n",
			Delimiter:      "auto",
			FirstRowHeader: true,
			Strategy:       "sequential",
		},
	})
	if err != nil {
		t.Fatalf("NewDataProvider auto delimiter: %v", err)
	}

	row := dp.GetRow("users", 0)
	if row["users.username"] != "alice" || row["users.password"] != "pass1" {
		t.Fatalf("auto delimiter row = %#v", row)
	}
}

func TestDataProvider_DelimiterAliases(t *testing.T) {
	dp, err := NewDataProvider([]dsl.DataSource{
		{
			Name:           "users",
			Type:           "csv",
			InlineData:     "username,password\nalice,pass1\n",
			Delimiter:      "comma",
			FirstRowHeader: true,
			Strategy:       "sequential",
		},
	})
	if err != nil {
		t.Fatalf("NewDataProvider comma delimiter: %v", err)
	}

	row := dp.GetRow("users", 0)
	if row["users.username"] != "alice" || row["users.password"] != "pass1" {
		t.Fatalf("comma delimiter row = %#v", row)
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

func TestDataProvider_FileDataSourceInlineBase64(t *testing.T) {
	payload := []byte{0x00, 0x01, 'A', 'P', 'E', 'X'}
	dp, err := NewDataProvider([]dsl.DataSource{{
		Name:       "payload",
		Type:       "file",
		InlineData: base64.StdEncoding.EncodeToString(payload),
		Encoding:   "base64",
		FileName:   "payload.bin",
	}})
	if err != nil {
		t.Fatalf("NewDataProvider file inline base64: %v", err)
	}
	got, ok := dp.GetFileContent("payload")
	if !ok {
		t.Fatal("expected file content")
	}
	if string(payload) != got {
		t.Fatalf("file content = %v, want %v", []byte(got), payload)
	}
	if row := dp.GetRow("payload", 0); row != nil {
		t.Fatalf("file data source should not be exposed as tabular rows: %#v", row)
	}
}

func TestDataProvider_FileDataSourceStorageRef(t *testing.T) {
	dir := t.TempDir()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatalf("RegisterRoot: %v", err)
	}
	path := filepath.Join(dir, "upload.txt")
	if err := os.WriteFile(path, []byte("asset bytes"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	ref := (&url.URL{Scheme: "file", Path: path}).String()
	dp, err := NewDataProvider([]dsl.DataSource{{
		Name:       "upload",
		Type:       "file",
		StorageRef: ref,
		FileName:   "upload.txt",
	}})
	if err != nil {
		t.Fatalf("NewDataProvider file storage_ref: %v", err)
	}
	got, ok := dp.GetFileContent("upload")
	if !ok || got != "asset bytes" {
		t.Fatalf("file content = %q/%v, want asset bytes/true", got, ok)
	}
}

func TestDataProvider_FileDataSourceStorageRefRejectsOutsideAllowedRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("secret bytes"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	ref := (&url.URL{Scheme: "file", Path: path}).String()

	_, err := NewDataProvider([]dsl.DataSource{{
		Name:       "secret",
		Type:       "file",
		StorageRef: ref,
		FileName:   "secret.txt",
	}})
	if err == nil {
		t.Fatal("expected file storage_ref outside allowed roots to fail")
	}
}

func TestDataProvider_FileDataSourceStorageRefRejectsSymlinkEscape(t *testing.T) {
	assetRoot := t.TempDir()
	if err := fileasset.RegisterRoot(assetRoot); err != nil {
		t.Fatalf("RegisterRoot: %v", err)
	}
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsidePath, []byte("secret bytes"), 0o600); err != nil {
		t.Fatalf("write outside asset: %v", err)
	}
	linkPath := filepath.Join(assetRoot, "linked-secret.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	ref := (&url.URL{Scheme: "file", Path: linkPath}).String()

	_, err := NewDataProvider([]dsl.DataSource{{
		Name:       "linked_secret",
		Type:       "file",
		StorageRef: ref,
		FileName:   "linked-secret.txt",
	}})
	if err == nil {
		t.Fatal("expected symlink escaping allowed roots to fail")
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

func TestDataProvider_CSVRejectsPathOutsideAssetRoots(t *testing.T) {
	// A separate unregistered directory is used because roots are process-wide.
	outside := t.TempDir()
	path := filepath.Join(outside, "secret.csv")
	if err := os.WriteFile(path, []byte("value\nsecret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDataProvider([]dsl.DataSource{{Name: "secret", Type: "csv", Path: path, FirstRowHeader: true}}); err == nil {
		t.Fatal("expected CSV path outside registered asset roots to fail")
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

func TestDataProviderRejectsDuplicateAndExcessiveSources(t *testing.T) {
	if _, err := NewDataProvider([]dsl.DataSource{
		{Name: "shared", Type: "csv", InlineData: "value\none\n", FirstRowHeader: true},
		{Name: " shared ", Type: "file", InlineData: "two"},
	}); err == nil || !strings.Contains(err.Error(), "duplicate data source") {
		t.Fatalf("duplicate source error = %v", err)
	}

	sources := make([]dsl.DataSource, maxDataProviderSources+1)
	for index := range sources {
		sources[index] = dsl.DataSource{Name: fmt.Sprintf("source-%d", index), Type: "file", InlineData: "x"}
	}
	if _, err := NewDataProvider(sources); err == nil || !strings.Contains(err.Error(), "source limit") {
		t.Fatalf("source cardinality error = %v", err)
	}
}

func TestDataProviderRejectsCSVColumnExpansion(t *testing.T) {
	header := strings.Repeat("column,", maxDataSourceColumns) + "last\n"
	_, err := NewDataProvider([]dsl.DataSource{{
		Name:           "wide",
		Type:           "csv",
		InlineData:     header,
		FirstRowHeader: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "column limit") {
		t.Fatalf("wide CSV error = %v", err)
	}
}

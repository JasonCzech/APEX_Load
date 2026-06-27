package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFileFilters(t *testing.T) {
	got := parseFileFilters(`[{"displayName":"Scripts (*.json)","pattern":"*.json"},{"displayName":"Archives","pattern":"*.zip"}]`)
	if len(got) != 2 {
		t.Fatalf("got %d filters, want 2", len(got))
	}
	if got[0].DisplayName != "Scripts (*.json)" || got[0].Pattern != "*.json" {
		t.Errorf("filter[0] = %+v", got[0])
	}
	if got[1].Pattern != "*.zip" {
		t.Errorf("filter[1] = %+v", got[1])
	}
}

func TestParseFileFiltersEmptyAndMalformed(t *testing.T) {
	if got := parseFileFilters(""); got != nil {
		t.Errorf("empty input should yield nil, got %+v", got)
	}
	if got := parseFileFilters("{not json"); got != nil {
		t.Errorf("malformed input should yield nil, got %+v", got)
	}
	if got := parseFileFilters(`[{"displayName":"","pattern":""}]`); len(got) != 0 {
		t.Errorf("blank filter should be dropped, got %+v", got)
	}
}

func TestReadWriteFileBase64RoundTrip(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	src := filepath.Join(dir, "script.json")
	content := []byte(`{"name":"order-flow"}`)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	b64, err := app.ReadFileBase64(src)
	if err != nil {
		t.Fatalf("ReadFileBase64: %v", err)
	}
	if want := base64.StdEncoding.EncodeToString(content); b64 != want {
		t.Errorf("encoded mismatch")
	}

	dst := filepath.Join(dir, "export.json")
	if err := app.WriteFileBase64(dst, b64); err != nil {
		t.Fatalf("WriteFileBase64: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("round-trip content = %q, want %q", got, content)
	}
}

func TestReadFileBase64Errors(t *testing.T) {
	app := NewApp()
	if _, err := app.ReadFileBase64(""); err == nil {
		t.Error("expected error for empty path")
	}
	if _, err := app.ReadFileBase64(t.TempDir()); err == nil {
		t.Error("expected error reading a directory")
	}
}

func TestWriteFileBase64RejectsBadBase64(t *testing.T) {
	app := NewApp()
	dst := filepath.Join(t.TempDir(), "out.bin")
	if err := app.WriteFileBase64(dst, "@@not base64@@"); err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestRevealCommand(t *testing.T) {
	tests := []struct {
		goos     string
		path     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "/Users/x/script.json", "open", []string{"-R", "/Users/x/script.json"}},
		{"windows", `C:\Users\x\script.json`, "explorer", []string{`/select,C:\Users\x\script.json`}},
		{"linux", "/home/x/dir/script.json", "xdg-open", []string{"/home/x/dir"}},
		{"plan9", "/x", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			name, args := revealCommand(tc.goos, tc.path)
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tc.wantArgs[i])
				}
			}
		})
	}
}

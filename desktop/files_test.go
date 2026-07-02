package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
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
	app.appDataPath = dir // writes are confined to the app data dir or dialog paths
	src := filepath.Join(dir, "script.json")
	content := []byte(`{"name":"order-flow"}`)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	app.rememberReadPath(src)
	b64, err := app.ReadFileBase64(src)
	if err != nil {
		t.Fatalf("ReadFileBase64: %v", err)
	}
	if want := base64.StdEncoding.EncodeToString(content); b64 != want {
		t.Errorf("encoded mismatch")
	}

	dst := filepath.Join(dir, "export.json")
	app.rememberWritePath(dst)
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

// TestFileBridgeConfinement verifies the bridge refuses arbitrary paths and only
// operates on dialog-vended / importable / app-data paths.
func TestFileBridgeConfinement(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	app.appDataPath = filepath.Join(dir, "appdata")
	if err := os.MkdirAll(app.appDataPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Arbitrary, non-dialog path → read refused before any IO, even if the
	// extension is otherwise importable.
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte("TOKEN=abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFileBase64(secret); err == nil {
		t.Error("expected ReadFileBase64 to refuse an arbitrary non-importable path")
	}
	script := filepath.Join(dir, "script.json")
	if err := os.WriteFile(script, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFileBase64(script); err == nil {
		t.Error("expected ReadFileBase64 to refuse an arbitrary importable path")
	}

	// Once vended by a dialog, the same arbitrary path is permitted.
	app.rememberReadPath(secret)
	if _, err := app.ReadFileBase64(secret); err != nil {
		t.Errorf("dialog-vended path should be readable: %v", err)
	}

	// Writes: arbitrary path and appData paths are refused unless vended by the
	// save dialog. A read grant does not authorize write-back.
	if err := app.WriteFileBase64(filepath.Join(dir, "evil.bin"), ""); err == nil {
		t.Error("expected WriteFileBase64 to refuse a path outside appData / not vended")
	}
	if err := app.WriteFileBase64(filepath.Join(app.appDataPath, "ok.json"), ""); err == nil {
		t.Error("expected WriteFileBase64 to refuse direct appData writes")
	}
	if err := app.WriteFileBase64(secret, ""); err == nil {
		t.Error("expected WriteFileBase64 to refuse read-granted paths")
	}
	savePath := filepath.Join(dir, "saved.json")
	app.rememberWritePath(savePath)
	if err := app.WriteFileBase64(savePath, ""); err != nil {
		t.Errorf("save-dialog write should be allowed: %v", err)
	}
}

func TestWriteFileBase64RejectsBadBase64(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	app.rememberWritePath(dst)
	if err := app.WriteFileBase64(dst, "@@not base64@@"); err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestWriteFileBase64RejectsOversizedEncoded(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	dst := filepath.Join(dir, "big.bin")
	app.rememberWritePath(dst)
	huge := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxBridgeFileBytes)+4)
	if err := app.WriteFileBase64(dst, huge); err == nil {
		t.Error("expected oversized encoded payload to be rejected before decode")
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

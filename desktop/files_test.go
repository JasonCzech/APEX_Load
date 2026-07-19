package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestReadFileBase64RejectsReplacedGrantedFile(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, []byte(`{"selected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app.rememberReadPath(path)
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"replacement":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFileBase64(path); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replacement should be rejected, got %v", err)
	}
}

func TestReadFileBase64UsesGrantedCanonicalTarget(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	link := filepath.Join(dir, "selected.json")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	app.rememberReadPath(link)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	b64, err := app.ReadFileBase64(link)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(b64)
	if string(decoded) != "first" {
		t.Fatalf("read retargeted symlink content %q, want original target", decoded)
	}
}

func TestReadFileBase64RejectsOversizedDescriptor(t *testing.T) {
	app := NewApp()
	path := filepath.Join(t.TempDir(), "large.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxBridgeFileBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	app.rememberReadPath(path)
	if _, err := app.ReadFileBase64(path); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized file should be rejected, got %v", err)
	}
}

func TestReadDroppedFileWaitsForBackendGrant(t *testing.T) {
	app := NewApp()
	app.dropGrantWait = time.Second
	path := filepath.Join(t.TempDir(), "drop.har")
	content := []byte("drop content")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		app.rememberReadPath(path)
	}()
	b64, err := app.ReadDroppedFileBase64(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := base64.StdEncoding.DecodeString(b64)
	if !reflect.DeepEqual(got, content) {
		t.Fatalf("content = %q, want %q", got, content)
	}
}

func TestReadDroppedFileTimesOutWithoutExactGrant(t *testing.T) {
	app := NewApp()
	app.dropGrantWait = 20 * time.Millisecond
	path := filepath.Join(t.TempDir(), "drop.har")
	if _, err := app.ReadDroppedFileBase64(path); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("missing grant should time out, got %v", err)
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

func TestSecureWriteGrantedFileFromEnforcesLimitBeforePublish(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "streamed.json")
	_, grant, err := newWritePathGrant(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFileFrom(grant, strings.NewReader("too large"), 3); err == nil || !strings.Contains(err.Error(), "3-byte limit") {
		t.Fatalf("oversized stream should be rejected, got %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("oversized stream was published: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".apexload-save-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("oversized stream recovery staging files = %v, want one scrubbed file", matches)
	}
	if info, statErr := os.Stat(matches[0]); statErr != nil || info.Size() != 0 {
		t.Fatalf("oversized stream staging file was not descriptor-scrubbed: info=%v err=%v", info, statErr)
	}
}

func TestWriteFileBase64RejectsSaveTargetReplacement(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.json")
	if err := os.WriteFile(dst, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.rememberWritePath(dst)
	if err := os.Rename(dst, dst+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.WriteFileBase64(dst, base64.StdEncoding.EncodeToString([]byte("renderer"))); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replacement should be rejected, got %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replacement" {
		t.Fatalf("replacement was clobbered: %q", got)
	}
}

func TestWriteFileBase64DoesNotClobberTargetCreatedAfterGrant(t *testing.T) {
	app := NewApp()
	dst := filepath.Join(t.TempDir(), "new.json")
	app.rememberWritePath(dst)
	if err := os.WriteFile(dst, []byte("created elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.WriteFileBase64(dst, base64.StdEncoding.EncodeToString([]byte("renderer"))); err == nil {
		t.Fatal("write should reject a target created after the save grant")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "created elsewhere" {
		t.Fatalf("new target was clobbered: %q", got)
	}
}

func TestWriteFileBase64UsesGrantedCanonicalParent(t *testing.T) {
	app := NewApp()
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "selected")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	dst := filepath.Join(link, "export.json")
	app.rememberWritePath(dst)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if err := app.WriteFileBase64(dst, base64.StdEncoding.EncodeToString([]byte("safe"))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(first, "export.json"))
	if err != nil || string(got) != "safe" {
		t.Fatalf("canonical destination content = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(second, "export.json")); !os.IsNotExist(err) {
		t.Fatalf("retargeted directory received output: %v", err)
	}
}

func TestWriteFileBase64RejectsReplacedCanonicalParent(t *testing.T) {
	app := NewApp()
	root := t.TempDir()
	parent := filepath.Join(root, "selected")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "export.json")
	app.rememberWritePath(dst)
	if err := os.Rename(parent, parent+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := app.WriteFileBase64(dst, base64.StdEncoding.EncodeToString([]byte("blocked"))); err == nil || !strings.Contains(err.Error(), "directory was replaced") {
		t.Fatalf("replaced parent should be rejected, got %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("replacement parent received output: %v", err)
	}
}

func TestSaveGrantRejectsSymbolicLinkTarget(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(real, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	app.rememberWritePath(link)
	if err := app.WriteFileBase64(link, base64.StdEncoding.EncodeToString([]byte("blocked"))); err == nil {
		t.Fatal("symbolic-link save target should not receive a grant")
	}
	got, _ := os.ReadFile(real)
	if string(got) != "secret" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

func TestAuthorizedRevealPathRequiresNativeGrant(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	arbitrary := filepath.Join(dir, "probe.json")
	if err := os.WriteFile(arbitrary, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := app.authorizedRevealPath(arbitrary); ok || got != "" {
		t.Fatalf("arbitrary importable path was reveal-authorized: %q", got)
	}
	app.rememberReadPath(arbitrary)
	want, err := filepath.EvalSymlinks(arbitrary)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := app.authorizedRevealPath(arbitrary); !ok || got != want {
		t.Fatalf("picker-granted reveal = %q, %v", got, ok)
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

func TestFolderOpenCommand(t *testing.T) {
	path := "/tmp/APEX Load/logs"
	tests := []struct {
		goos string
		name string
		args []string
	}{
		{goos: "darwin", name: "open", args: []string{path}},
		{goos: "linux", name: "xdg-open", args: []string{path}},
		{goos: "windows", name: "explorer", args: []string{path}},
		{goos: "plan9", name: "", args: nil},
	}
	for _, tc := range tests {
		name, args := folderOpenCommandForOS(tc.goos, path)
		if name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s command = %q %v, want %q %v", tc.goos, name, args, tc.name, tc.args)
		}
	}
}

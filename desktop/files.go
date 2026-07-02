package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxBridgeFileBytes caps files read/written over the base64 bridge. Large
// imports (archives) should keep using the multipart upload to the loopback API
// instead — base64 doubles memory and is unsuitable for big payloads.
const maxBridgeFileBytes = 64 << 20 // 64 MiB

// fileFilter is the dashboard-facing dialog filter, passed as a JSON array so
// the Wails binding stays free of generated model types.
type fileFilter struct {
	DisplayName string `json:"displayName"`
	Pattern     string `json:"pattern"`
}

// parseFileFilters converts the dashboard's JSON filter list to Wails filters.
// An empty or malformed list yields no filters (show all files).
func parseFileFilters(filtersJSON string) []wailsruntime.FileFilter {
	filtersJSON = strings.TrimSpace(filtersJSON)
	if filtersJSON == "" {
		return nil
	}
	var raw []fileFilter
	if err := json.Unmarshal([]byte(filtersJSON), &raw); err != nil {
		return nil
	}
	out := make([]wailsruntime.FileFilter, 0, len(raw))
	for _, f := range raw {
		if strings.TrimSpace(f.Pattern) == "" && strings.TrimSpace(f.DisplayName) == "" {
			continue
		}
		out = append(out, wailsruntime.FileFilter{DisplayName: f.DisplayName, Pattern: f.Pattern})
	}
	return out
}

func cleanBridgePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

// rememberReadPath records a path the OS open-file picker/open-with handoff
// returned so the file bridge may read it. Stored as a cleaned absolute path.
func (a *App) rememberReadPath(path string) {
	path = cleanBridgePath(path)
	if path == "" {
		return
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	if a.readPaths == nil {
		a.readPaths = map[string]bool{}
	}
	a.readPaths[path] = true
}

// rememberWritePath records a path the OS save-file picker returned so the file
// bridge may write it. Stored as a cleaned absolute path.
func (a *App) rememberWritePath(path string) {
	path = cleanBridgePath(path)
	if path == "" {
		return
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	if a.writePaths == nil {
		a.writePaths = map[string]bool{}
	}
	a.writePaths[path] = true
}

func (a *App) isReadPath(path string) bool {
	path = cleanBridgePath(path)
	if path == "" {
		return false
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	return a.readPaths[path]
}

func (a *App) isWritePath(path string) bool {
	path = cleanBridgePath(path)
	if path == "" {
		return false
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	return a.writePaths[path]
}

// isUnderAppData reports whether a path is inside the app data directory.
func (a *App) isUnderAppData(path string) bool {
	if a.appDataPath == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(a.appDataPath, filepath.Clean(abs))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// OpenFileDialogNative shows a native open-file picker and returns the chosen
// path ("" if the user cancels — not an error).
func (a *App) OpenFileDialogNative(title, filtersJSON string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	path, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: parseFileFilters(filtersJSON),
	})
	if err == nil {
		a.rememberReadPath(path)
	}
	return path, err
}

// OpenFilesDialogNative shows a native multi-select open-file picker.
func (a *App) OpenFilesDialogNative(title, filtersJSON string) ([]string, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("desktop runtime is not ready")
	}
	paths, err := wailsruntime.OpenMultipleFilesDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: parseFileFilters(filtersJSON),
	})
	if err == nil {
		for _, p := range paths {
			a.rememberReadPath(p)
		}
	}
	return paths, err
}

// SaveFileDialogNative shows a native save-file picker and returns the chosen
// path ("" if the user cancels).
func (a *App) SaveFileDialogNative(title, defaultName, filtersJSON string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	path, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
		Filters:         parseFileFilters(filtersJSON),
	})
	if err == nil {
		a.rememberWritePath(path)
	}
	return path, err
}

// ReadFileBase64 reads a file the user selected via the OS open dialog/open-with
// handoff and returns it base64-encoded so the dashboard can reconstruct a
// File/Blob for its existing upload code path. Bounded by maxBridgeFileBytes.
func (a *App) ReadFileBase64(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	// Confinement: extensions are not authorization. Only paths that the native
	// app learned from an OS-mediated open action in this session are readable.
	if !a.isReadPath(path) {
		return "", fmt.Errorf("refusing to read a path that was not selected by the user")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > maxBridgeFileBytes {
		return "", fmt.Errorf("file is too large for the bridge (%d bytes > %d); use the upload flow", info.Size(), maxBridgeFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// WriteFileBase64 writes base64-encoded content to a path chosen via the save
// dialog, letting exports skip the browser download flow.
func (a *App) WriteFileBase64(path, b64 string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	// Confinement: only write to a save-dialog destination the user chose. Read
	// grants do not authorize write-back, and app data is never writable via this
	// renderer bridge.
	if !a.isWritePath(path) {
		return fmt.Errorf("refusing to write a path that was not chosen via the save dialog")
	}
	// Enforce the cap on the ENCODED length before allocating the decoded buffer,
	// so a caller can't force a huge allocation that the post-decode check rejects.
	if len(b64) > base64.StdEncoding.EncodedLen(maxBridgeFileBytes) {
		return fmt.Errorf("payload is too large for the bridge (> %d bytes)", maxBridgeFileBytes)
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// RevealInFolder opens the OS file manager with the given file selected (or its
// containing folder where per-file selection is unsupported).
func (a *App) RevealInFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	// Only reveal paths the user chose, app-owned locations, or importable files,
	// and only if they actually exist (avoids handing arbitrary strings to the OS
	// file manager).
	if !a.isReadPath(path) && !a.isWritePath(path) && !a.isUnderAppData(path) && !isOpenableFile(path) {
		return fmt.Errorf("refusing to reveal a path that was not selected by the user")
	}
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	name, args := revealCommand(runtime.GOOS, path)
	if name == "" {
		// Fallback: open the parent directory via the Wails browser opener.
		if a.ctx != nil {
			return a.openFolder(filepath.Dir(path))
		}
		return fmt.Errorf("reveal is not supported on %s", runtime.GOOS)
	}
	// explorer.exe returns a non-zero exit code even on success, so ignore its
	// error; surface errors from the other openers.
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

// revealCommand returns the OS command that reveals a file in the file manager.
// Pure (no exec) so it is unit-testable across platforms. Returns an empty name
// when there is no suitable command.
func revealCommand(goos, path string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{"-R", path}
	case "windows":
		// explorer expects "/select,<path>" as a single argument.
		return "explorer", []string{"/select," + path}
	case "linux":
		// No standard "select file" verb; open the containing directory.
		return "xdg-open", []string{filepath.Dir(path)}
	default:
		return "", nil
	}
}

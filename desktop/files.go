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

// OpenFileDialogNative shows a native open-file picker and returns the chosen
// path ("" if the user cancels — not an error).
func (a *App) OpenFileDialogNative(title, filtersJSON string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: parseFileFilters(filtersJSON),
	})
}

// OpenFilesDialogNative shows a native multi-select open-file picker.
func (a *App) OpenFilesDialogNative(title, filtersJSON string) ([]string, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("desktop runtime is not ready")
	}
	return wailsruntime.OpenMultipleFilesDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: parseFileFilters(filtersJSON),
	})
}

// SaveFileDialogNative shows a native save-file picker and returns the chosen
// path ("" if the user cancels).
func (a *App) SaveFileDialogNative(title, defaultName, filtersJSON string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	return wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
		Filters:         parseFileFilters(filtersJSON),
	})
}

// ReadFileBase64 reads a file the user selected (or dropped) and returns it
// base64-encoded so the dashboard can reconstruct a File/Blob for its existing
// upload code path. Bounded by maxBridgeFileBytes.
func (a *App) ReadFileBase64(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
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
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	if len(data) > maxBridgeFileBytes {
		return fmt.Errorf("payload is too large for the bridge (%d bytes > %d)", len(data), maxBridgeFileBytes)
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

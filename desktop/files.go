package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

// readPathGrant binds a renderer-visible picker/drop path to the canonical file
// identity that the native process observed when granting access. Subsequent
// reads open that canonical path and verify the descriptor still names the same
// file, closing symlink-retarget and stat/read replacement races.
type readPathGrant struct {
	canonical string
	identity  os.FileInfo
}

// writePathGrant binds a save-dialog result to the canonical parent directory
// and the identities observed at grant time. Writes are performed relative to
// a verified parent descriptor and atomically published, so a later symlink or
// directory replacement cannot redirect renderer-controlled bytes elsewhere.
type writePathGrant struct {
	canonicalParent string
	name            string
	parentIdentity  os.FileInfo
	targetIdentity  os.FileInfo
	targetExisted   bool
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
	key, grant, err := newReadPathGrant(path)
	if err != nil {
		return
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	if a.readPaths == nil {
		a.readPaths = make(map[string]readPathGrant)
	}
	if a.readGrantChanged == nil {
		a.readGrantChanged = make(chan struct{})
	}
	a.readPaths[key] = grant
	close(a.readGrantChanged)
	a.readGrantChanged = make(chan struct{})
}

func newReadPathGrant(path string) (string, readPathGrant, error) {
	key := cleanBridgePath(path)
	if key == "" {
		return "", readPathGrant{}, fmt.Errorf("path is required")
	}
	canonical, err := filepath.EvalSymlinks(key)
	if err != nil {
		return "", readPathGrant{}, err
	}
	canonical = cleanBridgePath(canonical)
	f, err := openBridgeReadFile(canonical)
	if err != nil {
		return "", readPathGrant{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", readPathGrant{}, err
	}
	if !info.Mode().IsRegular() {
		return "", readPathGrant{}, fmt.Errorf("%s is not a regular file", path)
	}
	return key, readPathGrant{canonical: canonical, identity: info}, nil
}

// rememberWritePath records a path the OS save-file picker returned so the file
// bridge may write it. Stored as a cleaned absolute path.
func (a *App) rememberWritePath(path string) {
	key, grant, err := newWritePathGrant(path)
	if err != nil {
		return
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	if a.writePaths == nil {
		a.writePaths = map[string]writePathGrant{}
	}
	a.writePaths[key] = grant
}

func newWritePathGrant(path string) (string, writePathGrant, error) {
	key := cleanBridgePath(path)
	if key == "" {
		return "", writePathGrant{}, fmt.Errorf("path is required")
	}
	name := filepath.Base(key)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", writePathGrant{}, fmt.Errorf("invalid save filename")
	}
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(key))
	if err != nil {
		return "", writePathGrant{}, fmt.Errorf("resolve save directory: %w", err)
	}
	canonicalParent = cleanBridgePath(canonicalParent)
	parent, err := os.Open(canonicalParent)
	if err != nil {
		return "", writePathGrant{}, err
	}
	defer parent.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		return "", writePathGrant{}, err
	}
	if !parentInfo.IsDir() {
		return "", writePathGrant{}, fmt.Errorf("save destination parent is not a directory")
	}
	grant := writePathGrant{
		canonicalParent: canonicalParent,
		name:            name,
		parentIdentity:  parentInfo,
	}
	targetInfo, err := os.Lstat(filepath.Join(canonicalParent, name))
	switch {
	case err == nil:
		if targetInfo.Mode()&os.ModeSymlink != 0 {
			return "", writePathGrant{}, fmt.Errorf("refusing a symbolic-link save destination")
		}
		if !targetInfo.Mode().IsRegular() {
			return "", writePathGrant{}, fmt.Errorf("save destination is not a regular file")
		}
		grant.targetExisted = true
		grant.targetIdentity = targetInfo
	case os.IsNotExist(err):
		// A new target is valid; secureWriteGrantedFile publishes it without
		// clobbering a file that appears after the dialog grant.
	default:
		return "", writePathGrant{}, err
	}
	return key, grant, nil
}

func (a *App) isReadPath(path string) bool {
	path = cleanBridgePath(path)
	if path == "" {
		return false
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	_, ok := a.readPaths[path]
	return ok
}

func (a *App) readGrant(path string) (readPathGrant, bool) {
	path = cleanBridgePath(path)
	if path == "" {
		return readPathGrant{}, false
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	grant, ok := a.readPaths[path]
	return grant, ok
}

func (a *App) isWritePath(path string) bool {
	path = cleanBridgePath(path)
	if path == "" {
		return false
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	_, ok := a.writePaths[path]
	return ok
}

func (a *App) writeGrant(path string) (writePathGrant, bool) {
	path = cleanBridgePath(path)
	if path == "" {
		return writePathGrant{}, false
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	grant, ok := a.writePaths[path]
	return grant, ok
}

// isUnderAppData reports whether a path is inside the app data directory.
func (a *App) isUnderAppData(path string) bool {
	_, ok := a.appDataRevealPath(path)
	return ok
}

func (a *App) appDataRevealPath(path string) (string, bool) {
	appDataPath := a.appDataDirectory()
	if appDataPath == "" {
		return "", false
	}
	appData, err := filepath.EvalSymlinks(appDataPath)
	if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(cleanBridgePath(path))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(appData, canonical)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return canonical, true
}

// OpenFileDialogNative shows a native open-file picker and returns the chosen
// path ("" if the user cancels — not an error).
func (a *App) OpenFileDialogNative(title, filtersJSON string) (string, error) {
	ctx := a.runtimeContext()
	if ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	path, err := wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
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
	ctx := a.runtimeContext()
	if ctx == nil {
		return nil, fmt.Errorf("desktop runtime is not ready")
	}
	paths, err := wailsruntime.OpenMultipleFilesDialog(ctx, wailsruntime.OpenDialogOptions{
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
	ctx := a.runtimeContext()
	if ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	path, err := wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
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
	grant, ok := a.readGrant(path)
	if !ok {
		return "", fmt.Errorf("refusing to read a path that was not selected by the user")
	}
	f, err := openBridgeReadFile(grant.canonical)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(grant.identity, info) {
		return "", fmt.Errorf("refusing to read %s because the selected file was replaced", path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxBridgeFileBytes {
		return "", fmt.Errorf("file is too large for the bridge (%d bytes > %d); use the upload flow", info.Size(), maxBridgeFileBytes)
	}
	// The descriptor, rather than a second pathname lookup, is the source of
	// truth. LimitReader enforces the cap even if a concurrently-growing file had
	// a smaller size at Stat time.
	data, err := io.ReadAll(io.LimitReader(f, maxBridgeFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxBridgeFileBytes {
		return "", fmt.Errorf("file grew beyond the bridge limit (%d bytes)", maxBridgeFileBytes)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// ReadDroppedFileBase64 waits for the backend OnFileDrop callback to record the
// exact native path before reading it. The frontend and backend receive Wails'
// drop event independently, so this acknowledgement removes the former fixed
// delay race without expanding renderer access.
func (a *App) ReadDroppedFileBase64(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	key := cleanBridgePath(path)
	wait := a.dropGrantWait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		a.pathMu.Lock()
		_, granted := a.readPaths[key]
		changed := a.readGrantChanged
		if changed == nil {
			changed = make(chan struct{})
			a.readGrantChanged = changed
		}
		a.pathMu.Unlock()
		if granted {
			return a.ReadFileBase64(path)
		}
		select {
		case <-changed:
		case <-timer.C:
			return "", fmt.Errorf("native drop access was not granted for %s", path)
		}
	}
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
	grant, ok := a.writeGrant(path)
	if !ok {
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
	if err := secureWriteGrantedFile(grant, data); err != nil {
		return err
	}
	// Refresh the target identity so a deliberately repeated save through the
	// same dialog grant remains valid, while any external replacement between
	// saves is still detected.
	_, refreshed, err := newWritePathGrant(filepath.Join(grant.canonicalParent, grant.name))
	if err != nil {
		return fmt.Errorf("saved file could not be re-verified: %w", err)
	}
	key := cleanBridgePath(path)
	a.pathMu.Lock()
	current, stillGranted := a.writePaths[key]
	if stillGranted && current.name == grant.name && os.SameFile(current.parentIdentity, grant.parentIdentity) {
		a.writePaths[key] = refreshed
	}
	a.pathMu.Unlock()
	return nil
}

// RevealInFolder opens the OS file manager with the given file selected (or its
// containing folder where per-file selection is unsupported).
func (a *App) RevealInFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	// File extensions are never authorization: revealing an arbitrary *.json or
	// *.har path would let a compromised renderer probe the user's filesystem.
	// Resolve only exact picker grants or app-owned paths, and use the canonical
	// path/identity captured by the native side.
	authorizedPath, ok := a.authorizedRevealPath(path)
	if !ok {
		return fmt.Errorf("refusing to reveal a path that was not selected by the user")
	}
	if _, err := os.Lstat(authorizedPath); err != nil {
		return err
	}
	name, args := revealCommand(runtime.GOOS, authorizedPath)
	if name == "" {
		// Fallback: open the parent directory via the Wails browser opener.
		if a.runtimeContext() != nil {
			return a.openFolder(filepath.Dir(authorizedPath))
		}
		return fmt.Errorf("reveal is not supported on %s", runtime.GOOS)
	}
	// explorer.exe returns a non-zero exit code even on success, so ignore its
	// error; surface errors from the other openers.
	return launchAndReap(name, args)
}

func (a *App) authorizedRevealPath(path string) (string, bool) {
	if grant, ok := a.readGrant(path); ok {
		f, err := openBridgeReadFile(grant.canonical)
		if err != nil {
			return "", false
		}
		info, statErr := f.Stat()
		_ = f.Close()
		if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(grant.identity, info) {
			return "", false
		}
		return grant.canonical, true
	}
	if grant, ok := a.writeGrant(path); ok {
		parent, err := os.Open(grant.canonicalParent)
		if err != nil {
			return "", false
		}
		info, statErr := parent.Stat()
		_ = parent.Close()
		if statErr != nil || !info.IsDir() || !os.SameFile(grant.parentIdentity, info) {
			return "", false
		}
		target := filepath.Join(grant.canonicalParent, grant.name)
		targetInfo, err := os.Lstat(target)
		if err != nil || targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() {
			return "", false
		}
		if grant.targetExisted && !os.SameFile(grant.targetIdentity, targetInfo) {
			return "", false
		}
		return target, true
	}
	return a.appDataRevealPath(path)
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

func folderOpenCommand(path string) (string, []string) {
	return folderOpenCommandForOS(runtime.GOOS, path)
}

// folderOpenCommandForOS returns the platform file-manager invocation used by
// OpenLogsFolder/OpenAppDataFolder. It intentionally avoids file: URLs because
// Wails' browser opener rejects that scheme.
func folderOpenCommandForOS(goos, path string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "explorer", []string{path}
	case "linux":
		return "xdg-open", []string{path}
	default:
		return "", nil
	}
}

// launchAndReap starts a short-lived OS integration helper and always waits for
// it in the background. Calling Start without Wait leaks zombie processes on
// Unix after repeated reveal/open actions.
func launchAndReap(name string, args []string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("native folder opening is not supported on %s", runtime.GOOS)
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			// Explorer is documented to return non-zero for some successful shell
			// activations; after Start there is no synchronous failure to surface.
			slog.Debug("native file manager helper exited", "command", name, "err", err)
		}
	}()
	return nil
}

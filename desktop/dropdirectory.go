package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A folder dropped on the window (a Bruno collection, an unpacked project) is
// granted like a dropped file: only the exact directory the OS reported in the
// drop event may be read, and only below it.

// maxDroppedDirectoryEntries matches the browser's folder drop
// (droppedImportSources.ts MAX_DROPPED_FOLDER_ENTRIES): it stops a mistaken
// drop of a home directory, counting folders as well as files.
const maxDroppedDirectoryEntries = 5000

// maxDroppedDirectoryDepth stops a deep tree the entry count would not.
const maxDroppedDirectoryDepth = 32

// droppedJunkDirectories hold no import content and can hold thousands of
// entries. Mirrors pkg/internal/archiveingest JunkReason's directory rule.
var droppedJunkDirectories = map[string]bool{
	"__macosx": true,
	".git":     true,
	".svn":     true,
	".hg":      true,
}

type readDirectoryGrant struct {
	canonical string
	identity  os.FileInfo
}

type droppedDirectoryFile struct {
	Path   string `json:"path"`
	Base64 string `json:"base64"`
}

type droppedDirectory struct {
	Files []droppedDirectoryFile `json:"files"`
}

// rememberDroppedDirectory grants a directory the OS reported in a drop event.
// Anything else (a file of a type the bridge does not read) is ignored.
func (a *App) rememberDroppedDirectory(path string) {
	key := cleanBridgePath(path)
	if key == "" {
		return
	}
	canonical, err := filepath.EvalSymlinks(key)
	if err != nil {
		return
	}
	canonical = cleanBridgePath(canonical)
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return
	}
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	if a.readDirectories == nil {
		a.readDirectories = make(map[string]readDirectoryGrant)
	}
	if a.readGrantChanged == nil {
		a.readGrantChanged = make(chan struct{})
	}
	a.readDirectories[key] = readDirectoryGrant{canonical: canonical, identity: info}
	close(a.readGrantChanged)
	a.readGrantChanged = make(chan struct{})
}

// ReadDroppedDirectory returns every file below a dropped folder as JSON
// {"files":[{"path":"Folder/sub/file","base64":"..."}]}. Paths start with the
// folder's own name, like a browser folder pick's webkitRelativePath, so the
// files flow through the same upload code. Symbolic links are not followed,
// version-control and macOS bookkeeping folders are skipped, and the folder's
// size is bounded like any other bridge read.
func (a *App) ReadDroppedDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	grant, err := a.waitForDirectoryGrant(cleanBridgePath(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(grant.canonical)
	if err != nil {
		return "", err
	}
	if !os.SameFile(grant.identity, info) {
		return "", fmt.Errorf("refusing to read %s because the dropped folder was replaced", path)
	}
	// One reply carries the whole folder. Its files share a single bridge
	// read's budget, so it carries no more file bytes than one
	// ReadDroppedFileBase64 reply.
	files, err := readDroppedDirectoryFiles(grant.canonical)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(droppedDirectory{Files: files})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *App) waitForDirectoryGrant(key string) (readDirectoryGrant, error) {
	wait := a.dropGrantWait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		a.pathMu.Lock()
		grant, granted := a.readDirectories[key]
		_, dropped := a.droppedPaths[key]
		changed := a.readGrantChanged
		if changed == nil {
			changed = make(chan struct{})
			a.readGrantChanged = changed
		}
		a.pathMu.Unlock()
		if granted {
			return grant, nil
		}
		if dropped {
			return readDirectoryGrant{}, fmt.Errorf("native drop access was not granted for %s", key)
		}
		select {
		case <-changed:
		case <-timer.C:
			return readDirectoryGrant{}, fmt.Errorf("native drop access was not granted for %s", key)
		}
	}
}

var errDroppedDirectoryTooLarge = errors.New("dropped folder is too large")

func readDroppedDirectoryFiles(root string) ([]droppedDirectoryFile, error) {
	rootName := filepath.Base(root)
	files := []droppedDirectoryFile{}
	entries := 0
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.Count(filepath.ToSlash(rel), "/") >= maxDroppedDirectoryDepth {
			return fmt.Errorf("%w: %s is nested more than %d folders deep", errDroppedDirectoryTooLarge, rootName, maxDroppedDirectoryDepth)
		}
		entries++
		if entries > maxDroppedDirectoryEntries {
			return fmt.Errorf("%w: %s has more than %d files and folders; drop the collection's own folder", errDroppedDirectoryTooLarge, rootName, maxDroppedDirectoryEntries)
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			// Never follow a link out of the folder the person dropped.
			return nil
		case entry.IsDir():
			if droppedJunkDirectories[strings.ToLower(entry.Name())] {
				return filepath.SkipDir
			}
			return nil
		case !entry.Type().IsRegular():
			return nil
		}
		data, err := readDroppedDirectoryFile(path, maxBridgeFileBytes-total)
		if err != nil {
			return err
		}
		total += int64(len(data))
		files = append(files, droppedDirectoryFile{
			Path:   rootName + "/" + filepath.ToSlash(rel),
			Base64: base64.StdEncoding.EncodeToString(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// readDroppedDirectoryFile reads one regular file through the same
// no-follow, nonblocking open as every other bridge read, bounded by what is
// left of the folder's budget.
func readDroppedDirectoryFile(path string, remaining int64) ([]byte, error) {
	f, err := openBridgeReadFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, remaining+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > remaining {
		return nil, fmt.Errorf("%w: the folder's files exceed %d MiB together; drop a smaller folder or an archive", errDroppedDirectoryTooLarge, maxBridgeFileBytes>>20)
	}
	return data, nil
}

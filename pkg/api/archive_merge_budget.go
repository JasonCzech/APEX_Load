package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	envArchiveMergeMaxBytes     = "APEXLOAD_ARCHIVE_MERGE_MAX_BYTES"
	envArchiveMergeMinFreeBytes = "APEXLOAD_ARCHIVE_MERGE_MIN_FREE_BYTES"
)

var errArchiveMergeLimit = errors.New("archive merge byte limit exceeded")
var errArchiveMergeFreeSpace = errors.New("insufficient free space for archive merge")

// One budget covers merged JSONL, both interval-summary copies, telemetry
// evidence and the final manifest. Conversion has its own independent budget.
func newArchiveMergeBudget(root string) (*archiveOutputBudget, error) {
	limit, err := collectionCacheByteLimit(envArchiveMergeMaxBytes, 64<<30, 128<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveMergeMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	return &archiveOutputBudget{root: root, remaining: limit, minFree: minFree, limitErr: errArchiveMergeLimit, freeSpaceErr: errArchiveMergeFreeSpace}, nil
}

func mergeBudget(root string, budgets []*archiveOutputBudget) (*archiveOutputBudget, error) {
	if len(budgets) == 0 {
		return newArchiveMergeBudget(root)
	}
	if len(budgets) != 1 || budgets[0] == nil {
		return nil, errors.New("exactly one archive merge budget is required")
	}
	return budgets[0], nil
}

func writeSyncedArchiveMergeFile(path string, data []byte, budget *archiveOutputBudget) error {
	file, err := replaceArchiveArtifactForWrite(filepath.Dir(path), filepath.Base(path), 0o600, false)
	if err != nil {
		return err
	}
	writer := &archiveOutputFile{file: file, budget: budget}
	_, writeErr := writer.Write(data)
	return errors.Join(writeErr, writer.Sync(), writer.Close())
}

// Opening for append does not truncate the target. Before the first write,
// bind the descriptor to the inspected regular inode, just as the reader does.
func openArchiveArtifactForAppend(rootPath, name string) (*os.File, error) {
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := inspectArchiveRelativeParents(root, normalized, false, 0); err != nil {
		return nil, err
	}
	before, err := inspectArchiveArtifactTarget(root, normalized, false)
	if err != nil {
		return nil, err
	}
	file, err := root.OpenFile(filepath.FromSlash(normalized), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	after, err := root.Lstat(filepath.FromSlash(normalized))
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("archive append target changed while it was opened")
	}
	return file, nil
}

func writeArchiveMergeJSONL(path string, budget *archiveOutputBudget, emit func(*json.Encoder) error) error {
	file, err := replaceArchiveArtifactForWrite(filepath.Dir(path), filepath.Base(path), 0o600, false)
	if err != nil {
		return err
	}
	defer file.Close()
	output := newArchiveMergeOutput(file, budget)
	writeErr := emit(json.NewEncoder(output))
	if writeErr == nil {
		writeErr = output.Flush()
	}
	return errors.Join(writeErr, file.Sync(), file.Close())
}

// The staging directory is private until its final rename. Flush artifact data
// and directory entries before the manifest; then flush the new manifest entry.
func writeDurableArchiveMergeManifest(path string, data []byte, budget *archiveOutputBudget) error {
	root := filepath.Dir(path)
	if err := syncArchiveArtifacts(root, path); err != nil {
		return err
	}
	if err := syncArchiveMergeDirectories(root); err != nil {
		return err
	}
	if err := writeSyncedArchiveMergeFile(path, data, budget); err != nil {
		return err
	}
	return syncArchiveMergeDirectory(root)
}

func syncArchiveMergeDirectories(root string) error {
	var directories []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("archive merge tree contains a symbolic link")
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := syncArchiveMergeDirectory(directories[i]); err != nil {
			return err
		}
	}
	return nil
}

func syncArchiveMergeDirectory(path string) error {
	root, err := openArchiveDirectoryNoSymlink(path)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr, closeErr := dir.Sync(), dir.Close()
	if isIgnorableArchiveDirectorySyncError(syncErr) {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

// Keep memory bounded while amortizing the cross-process capacity lock over
// physical writes. Every success path must Flush before syncing or publishing.
func newArchiveMergeOutput(file *os.File, budget *archiveOutputBudget) *bufio.Writer {
	return bufio.NewWriterSize(&archiveOutputFile{file: file, budget: budget}, 64<<10)
}

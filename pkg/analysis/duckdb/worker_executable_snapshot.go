package duckdb

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// A published native executable larger than this explicit snapshot budget is
// rejected before copying. Admission reserves this extra scratch independently
// of source/normalized/spill quotas on every production worker profile.
const workerExecutableMaxBytes int64 = 512 << 20

func workerExecutableSnapshotName() string {
	if runtime.GOOS == "windows" {
		return "approved-native-worker.exe"
	}
	return "approved-native-worker"
}

// The installer-owned original pathname is never handed to exec/CreateProcess.
// Launch reopens only this private, permission-protected immutable copy, hashed
// from an exact retained source descriptor and checked against startup approval.
// Platform DLL/rpath/code-signing behavior still requires packaged acceptance.
func workerSnapshotExecutable(a *workerAttempt, sourcePath, expected string) (string, error) {
	if workerContextError(a.ctx) != nil {
		return "", errWorkerOwnership
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", errWorkerProtocol
	}
	a.trackDescriptor(source)
	defer a.requestDescriptorClose(source)
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > workerExecutableMaxBytes {
		return "", errWorkerResourceLimit
	}
	name := workerExecutableSnapshotName()
	output, err := a.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
	if err != nil {
		return "", errWorkerProtocol
	}
	a.trackDescriptor(output)
	if workerPrivateDescriptor(output) != nil {
		a.requestDescriptorClose(output)
		return "", errWorkerOwnership
	}
	digest := sha256.New()
	n, copyErr := workerCopyContext(a.ctx, io.MultiWriter(output, digest), io.LimitReader(source, before.Size()+1))
	after, statErr := source.Stat()
	syncErr := output.Sync()
	closeErr := a.closeDescriptor(output)
	sourceCloseErr := a.closeDescriptor(source)
	if errors.Join(copyErr, statErr, syncErr, closeErr, sourceCloseErr) != nil || n != before.Size() || after == nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != expected {
		return "", errWorkerOwnership
	}
	if workerContextError(a.ctx) != nil {
		return "", errWorkerOwnership
	}
	return filepath.Join(a.path, name), nil
}

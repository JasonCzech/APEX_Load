package duckdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/procidentity"
)

const (
	workerStagingOwnerMetadataName     = ".staging-owner.json"
	workerStagingOwnerMetadataTempName = ".staging-owner.json.tmp"
	workerOwnerMetadataName            = ".worker-owner.json"
	workerOwnerMetadataTempName        = ".worker-owner.json.tmp"
)

type nativeStagingScopeKey struct{}

type nativeStagingScope struct {
	DatasetID string
	AttemptID string
}

// WithNativeStagingScope binds dataset and attempt identifiers to the staging
// directory created by a native pipeline reservation.
func WithNativeStagingScope(ctx context.Context, datasetID, attemptID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, nativeStagingScopeKey{}, nativeStagingScope{
		DatasetID: strings.TrimSpace(datasetID),
		AttemptID: strings.TrimSpace(attemptID),
	})
}

// BindStagingAttemptScope updates the staging owner metadata file in dir with
// the dataset and attempt identifiers once an attempt claim succeeds.
func BindStagingAttemptScope(dir, datasetID, attemptID string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return writeWorkerStagingOwner(dir, nativeStagingScope{
		DatasetID: strings.TrimSpace(datasetID),
		AttemptID: strings.TrimSpace(attemptID),
	})
}

func nativeStagingScopeFrom(ctx context.Context) nativeStagingScope {
	if ctx == nil {
		return nativeStagingScope{}
	}
	scope, _ := ctx.Value(nativeStagingScopeKey{}).(nativeStagingScope)
	return scope
}

// prefix returns a compact directory prefix so that nested worker scratch
// directories (<cacheRoot>/job-<rand>/native-<rand>) stay well below the
// Win32 CreateProcessW lpCurrentDirectory MAX_PATH (258-char) limit even when
// the archive root lives under a deep temporary directory; DatasetID and
// AttemptID are persisted in .staging-owner.json and .worker-owner.json.
func (s nativeStagingScope) prefix() string {
	return "job-"
}

type workerStagingOwnerMetadata struct {
	Version    int                    `json:"version"`
	Controller *procidentity.Identity `json:"controller,omitempty"`
	DatasetID  string                 `json:"dataset_id,omitempty"`
	AttemptID  string                 `json:"attempt_id,omitempty"`
}

type workerAttemptOwnerMetadata struct {
	Version    int                    `json:"version"`
	Controller *procidentity.Identity `json:"controller,omitempty"`
	AttemptID  string                 `json:"attempt_id,omitempty"`
	Launching  bool                   `json:"launching,omitempty"`
	Worker     *procidentity.Identity `json:"worker,omitempty"`
}

func writeWorkerStagingOwner(dir string, scope nativeStagingScope) error {
	meta := workerStagingOwnerMetadata{
		Version:   1,
		DatasetID: scope.DatasetID,
		AttemptID: scope.AttemptID,
	}
	if self, err := procidentity.Self(); err == nil {
		meta.Controller = &self
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, workerStagingOwnerMetadataTempName)
	dst := filepath.Join(dir, workerStagingOwnerMetadataName)
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func writeWorkerAttemptOwner(dir, attemptID string, launching bool, worker *procidentity.Identity) error {
	meta := workerAttemptOwnerMetadata{
		Version:   1,
		AttemptID: attemptID,
		Launching: launching,
	}
	if self, err := procidentity.Self(); err == nil {
		meta.Controller = &self
	}
	if worker != nil {
		cp := *worker
		meta.Worker = &cp
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, workerOwnerMetadataTempName)
	dst := filepath.Join(dir, workerOwnerMetadataName)
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func isWorkerOwnerMetadataFile(name string) bool {
	base := filepath.Base(name)
	return base == workerStagingOwnerMetadataName ||
		base == workerStagingOwnerMetadataTempName ||
		base == workerOwnerMetadataName ||
		base == workerOwnerMetadataTempName
}

// ReclaimExitedStagingDirectories removes orphaned job-* staging directories
// under root once procidentity proves both the owning controller and every
// spawned worker child incarnation have exited. It never creates root when
// absent and never removes a directory with an active or unjudgeable owner.
func ReclaimExitedStagingDirectories(root string, exited func(procidentity.Identity) bool) (int, error) {
	if exited == nil {
		exited = procidentity.Exited
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errWorkerProtocol
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	reclaimed := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), "job-") {
			continue
		}
		jobDir := filepath.Join(root, entry.Name())
		if !workerStagingDirectoryExited(jobDir, exited) {
			continue
		}
		if err := os.RemoveAll(jobDir); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	return reclaimed, nil
}

func workerStagingDirectoryExited(jobDir string, exited func(procidentity.Identity) bool) bool {
	info, err := os.Lstat(jobDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	ownerPath := filepath.Join(jobDir, workerStagingOwnerMetadataName)
	ownerInfo, err := os.Lstat(ownerPath)
	if err != nil || !ownerInfo.Mode().IsRegular() || ownerInfo.Size() < 2 || ownerInfo.Size() > 4096 {
		return false
	}
	raw, err := os.ReadFile(ownerPath)
	if err != nil {
		return false
	}
	var owner workerStagingOwnerMetadata
	if err := json.Unmarshal(raw, &owner); err != nil || owner.Version != 1 || owner.Controller == nil {
		return false
	}
	if !exited(*owner.Controller) {
		return false
	}
	children, err := os.ReadDir(jobDir)
	if err != nil {
		return false
	}
	for _, child := range children {
		if child.Type()&os.ModeSymlink != 0 {
			return false
		}
		if !child.IsDir() {
			continue
		}
		if !strings.HasPrefix(child.Name(), "native-") {
			return false
		}
		attemptDir := filepath.Join(jobDir, child.Name())
		if !workerAttemptDirectoryExited(attemptDir, exited) {
			return false
		}
	}
	return true
}

func workerAttemptDirectoryExited(attemptDir string, exited func(procidentity.Identity) bool) bool {
	info, err := os.Lstat(attemptDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	metaPath := filepath.Join(attemptDir, workerOwnerMetadataName)
	metaInfo, err := os.Lstat(metaPath)
	if os.IsNotExist(err) {
		// Directory was created by the now-exited controller before .worker-owner.json
		// was written; no child process could have been started yet.
		return true
	}
	if err != nil || !metaInfo.Mode().IsRegular() || metaInfo.Size() < 2 || metaInfo.Size() > 4096 {
		return false
	}
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	var meta workerAttemptOwnerMetadata
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Version != 1 || meta.Controller == nil {
		return false
	}
	if !exited(*meta.Controller) || meta.Launching {
		return false
	}
	if meta.Worker != nil && !exited(*meta.Worker) {
		return false
	}
	return true
}

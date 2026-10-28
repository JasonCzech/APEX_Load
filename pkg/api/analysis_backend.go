package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const analysisVolumeIdentityFileName = "source.v1"

type analysisVolumeIdentityStore interface {
	GetAnalysisVolumeIdentity() (string, error)
	EnsureAnalysisVolumeIdentity(string) (string, error)
}

func ensureAnalysisBackend(store any) error {
	return ensureAnalysisBackendFor(store, currentArchiveBackend())
}

func ensureAnalysisBackendFor(store any, backend archiveBackendRuntime) error {
	binding, ok := store.(interface{ EnsureAnalysisStorageIdentity(string) error })
	if !ok {
		return errors.New("analytical storage identity persistence unavailable")
	}
	if err := backend.validate(); err != nil {
		return err
	}
	// Object cache paths can differ across replicas; only the durable namespace
	// and credential destination identity participate in the binding.
	if backend.Backend == archiveBackendFilesystem {
		root, err := filepath.Abs(backend.LocalRoot)
		if err != nil {
			return err
		}
		backend = archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: root}
	} else {
		backend.LocalRoot = ""
	}
	body, err := json.Marshal(backend)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if err := binding.EnsureAnalysisStorageIdentity(hex.EncodeToString(digest[:])); err != nil {
		return err
	}
	_, err = verifyAnalysisVolumeFile(store, backend)
	return err
}

func readAnalysisVolumeSourceFile(backend archiveBackendRuntime) (string, error) {
	if backend.Backend != archiveBackendFilesystem {
		return "", nil
	}
	path := filepath.Join(analysisArtifactDirectory(backend), analysisVolumeIdentityFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < 36 || info.Size() > 128 {
		return "", errors.New("invalid analytical volume identity file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", errors.New("invalid analytical volume identity value")
	}
	return id, nil
}

// verifyAnalysisVolumeFile checks that a bound filesystem volume identity still
// matches the physical _analysis/source.v1 file without creating the directory.
func verifyAnalysisVolumeFile(store any, backend archiveBackendRuntime) (string, error) {
	if backend.Backend != archiveBackendFilesystem {
		return "", nil
	}
	volStore, ok := store.(analysisVolumeIdentityStore)
	if !ok {
		return "", nil
	}
	boundID, err := volStore.GetAnalysisVolumeIdentity()
	if err != nil {
		return "", err
	}
	diskID, diskErr := readAnalysisVolumeSourceFile(backend)
	if errors.Is(diskErr, fs.ErrNotExist) {
		if boundID == "" {
			return "", nil
		}
		return "", fmt.Errorf("bound analytical volume %s is missing from %s: %w", boundID, analysisArtifactDirectory(backend), diskErr)
	}
	if diskErr != nil {
		return "", diskErr
	}
	if boundID != "" && diskID != boundID {
		return "", fmt.Errorf("analytical volume identity mismatch: disk %s != store %s", diskID, boundID)
	}
	return diskID, nil
}

// ensureAnalysisVolumeFile materializes _analysis/source.v1 on a fresh
// filesystem backend at writer-mint time and binds its UUID in app_settings.
// Once bound in app_settings, a missing or mismatched source.v1 fails closed.
func ensureAnalysisVolumeFile(store any, backend archiveBackendRuntime) (string, error) {
	if backend.Backend != archiveBackendFilesystem {
		return "", nil
	}
	volStore, ok := store.(analysisVolumeIdentityStore)
	if !ok {
		return "", nil
	}
	diskID, diskErr := readAnalysisVolumeSourceFile(backend)
	if diskErr == nil {
		return volStore.EnsureAnalysisVolumeIdentity(diskID)
	}
	if !errors.Is(diskErr, fs.ErrNotExist) {
		return "", diskErr
	}
	boundID, err := volStore.GetAnalysisVolumeIdentity()
	if err != nil {
		return "", err
	}
	dir := analysisArtifactDirectory(backend)
	if boundID != "" {
		return "", fmt.Errorf("bound analytical volume %s is missing from %s: %w", boundID, dir, fs.ErrNotExist)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	boundID, err = volStore.EnsureAnalysisVolumeIdentity(uuid.NewString())
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, analysisVolumeIdentityFileName)
	tmp := dst + ".tmp-" + uuid.NewString()
	if err := os.WriteFile(tmp, []byte(boundID+"\n"), 0600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return readAnalysisVolumeSourceFile(backend)
}

func (s *Server) requireAnalysisBackend(w http.ResponseWriter) bool {
	if err := ensureAnalysisBackend(s.store); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return false
	}
	return true
}

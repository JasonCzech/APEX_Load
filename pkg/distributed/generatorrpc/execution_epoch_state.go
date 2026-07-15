package generatorrpc

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	executionEpochStateDirEnv   = "APEXLOAD_GENERATOR_STATE_DIR"
	executionEpochStateVersion  = 2
	maxExecutionEpochStateBytes = 4096
	maxExecutionLeaseOwnerBytes = 512
)

type executionEpochStateRecord struct {
	Version      int    `json:"version"`
	Generation   uint64 `json:"generation"`
	LeaseOwnerID string `json:"lease_owner_id,omitempty"`
}

type executionEpochStateStore struct {
	root       *os.Root
	name       string
	ownsRoot   bool
	generation uint64
	leaseOwner string
}

func (s *Service) initializeExecutionEpochState() {
	state, generation, leaseOwner, err := openExecutionEpochState(s)
	if err != nil {
		s.executionEpochStateErr = err
		s.notifyReadinessChangedLocked()
		return
	}
	s.executionEpochState = state
	s.executionGeneration = generation
	s.executionLeaseOwner = leaseOwner
	// A durable high-water loaded without an in-memory expiry means this is a
	// replacement process, not an ordinarily expired live service. Permit one
	// exact-owner revalidation; the flag is consumed as soon as a lease session
	// is installed and is never restored by normal expiry.
	s.executionRestartRevalidation = generation > 0
}

func openExecutionEpochState(service *Service) (*executionEpochStateStore, uint64, string, error) {
	if service == nil {
		return nil, 0, "", errors.New("generator service is required")
	}
	if service.executionEpochStatePath != "" {
		return openExecutionEpochStateAtPath(service.executionEpochStatePath)
	}
	stateDir := strings.TrimSpace(os.Getenv(executionEpochStateDirEnv))
	if stateDir != "" {
		_, root, err := openCanonicalArchiveDirectory(stateDir, true)
		if err != nil {
			return nil, 0, "", fmt.Errorf("open %s: %w", executionEpochStateDirEnv, err)
		}
		name := sanitizeArchiveNamespace(service.generatorID) + ".execution-epoch.json"
		return loadExecutionEpochState(&executionEpochStateStore{root: root, name: name, ownsRoot: true})
	}
	if isProductionEnvironment(os.Getenv("APEXLOAD_ENV")) {
		return nil, 0, "", fmt.Errorf("%s is required in production and must be backed by storage that survives generator replacement", executionEpochStateDirEnv)
	}
	if service.archiveNamespaceErr != nil {
		return nil, 0, "", fmt.Errorf("default execution state directory: %w", service.archiveNamespaceErr)
	}
	if service.archiveNamespace == nil || service.archiveNamespace.root == nil {
		return nil, 0, "", errors.New("default execution state directory is unavailable")
	}
	return loadExecutionEpochState(&executionEpochStateStore{
		root: service.archiveNamespace.root,
		name: ".execution-epoch.json",
	})
}

func openExecutionEpochStateAtPath(path string) (*executionEpochStateStore, uint64, string, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, 0, "", err
	}
	name := filepath.Base(absPath)
	if err := validateArchiveNamespace(name); err != nil {
		return nil, 0, "", fmt.Errorf("invalid execution epoch state filename: %w", err)
	}
	_, root, err := openCanonicalArchiveDirectory(filepath.Dir(absPath), true)
	if err != nil {
		return nil, 0, "", err
	}
	return loadExecutionEpochState(&executionEpochStateStore{root: root, name: name, ownsRoot: true})
}

func loadExecutionEpochState(state *executionEpochStateStore) (*executionEpochStateStore, uint64, string, error) {
	if state == nil || state.root == nil || state.name == "" {
		return nil, 0, "", errors.New("execution epoch state location is invalid")
	}
	info, err := state.root.Lstat(state.name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := state.persist(0, ""); err != nil {
			_ = state.close()
			return nil, 0, "", fmt.Errorf("initialize execution epoch state: %w", err)
		}
		return state, 0, "", nil
	}
	if err != nil {
		_ = state.close()
		return nil, 0, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		_ = state.close()
		return nil, 0, "", errors.New("execution epoch state is not a regular non-symlink file")
	}
	if info.Size() > maxExecutionEpochStateBytes {
		_ = state.close()
		return nil, 0, "", fmt.Errorf("execution epoch state exceeds %d bytes", maxExecutionEpochStateBytes)
	}
	file, err := state.root.Open(state.name)
	if err != nil {
		_ = state.close()
		return nil, 0, "", err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxExecutionEpochStateBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		_ = state.close()
		return nil, 0, "", errors.Join(readErr, closeErr)
	}
	if len(raw) > maxExecutionEpochStateBytes {
		_ = state.close()
		return nil, 0, "", fmt.Errorf("execution epoch state exceeds %d bytes", maxExecutionEpochStateBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record executionEpochStateRecord
	if err := decoder.Decode(&record); err != nil {
		_ = state.close()
		return nil, 0, "", fmt.Errorf("decode execution epoch state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		_ = state.close()
		return nil, 0, "", fmt.Errorf("decode execution epoch state: %w", err)
	}
	if record.Version != 1 && record.Version != executionEpochStateVersion {
		_ = state.close()
		return nil, 0, "", fmt.Errorf("unsupported execution epoch state version %d", record.Version)
	}
	if err := validateExecutionLeaseOwner(record.Generation, record.LeaseOwnerID, record.Version == executionEpochStateVersion); err != nil {
		_ = state.close()
		return nil, 0, "", fmt.Errorf("decode execution epoch state: %w", err)
	}
	state.generation = record.Generation
	state.leaseOwner = strings.TrimSpace(record.LeaseOwnerID)
	return state, record.Generation, state.leaseOwner, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON value")
	}
	return err
}

func (s *executionEpochStateStore) persist(generation uint64, leaseOwner string) (retErr error) {
	if s == nil || s.root == nil {
		return errors.New("execution epoch state store is unavailable")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if err := validateExecutionLeaseOwner(generation, leaseOwner, true); err != nil {
		return err
	}
	if generation < s.generation {
		return fmt.Errorf("refusing to lower execution generation from %d to %d", s.generation, generation)
	}
	if generation == s.generation && s.leaseOwner != "" && leaseOwner != s.leaseOwner {
		return fmt.Errorf("refusing to change execution generation %d lease owner", generation)
	}
	if info, err := s.root.Lstat(s.name); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("execution epoch state path is not a regular non-symlink file")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	raw, err := json.Marshal(executionEpochStateRecord{Version: executionEpochStateVersion, Generation: generation, LeaseOwnerID: leaseOwner})
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	tempName := s.name + ".tmp-" + hex.EncodeToString(random)
	file, err := s.root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = s.root.Remove(tempName)
		}
	}()
	if err := writeExecutionEpochState(file, raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := s.root.Rename(tempName, s.name); err != nil {
		return err
	}
	directory, err := s.root.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(syncErr, closeErr)
	}
	s.generation = generation
	s.leaseOwner = leaseOwner
	return nil
}

func writeExecutionEpochState(destination io.Writer, content []byte) error {
	if destination == nil {
		return errors.New("execution epoch state writer is unavailable")
	}
	written, err := destination.Write(content)
	if err != nil {
		return err
	}
	if written != len(content) {
		return io.ErrShortWrite
	}
	return nil
}

func validateExecutionLeaseOwner(generation uint64, leaseOwner string, requireOwner bool) error {
	leaseOwner = strings.TrimSpace(leaseOwner)
	if generation == 0 {
		if leaseOwner != "" {
			return errors.New("generation zero cannot have a lease owner")
		}
		return nil
	}
	if requireOwner && leaseOwner == "" {
		return errors.New("positive execution generation requires a lease owner")
	}
	if len(leaseOwner) > maxExecutionLeaseOwnerBytes {
		return fmt.Errorf("execution lease owner exceeds %d bytes", maxExecutionLeaseOwnerBytes)
	}
	for _, char := range leaseOwner {
		if char < 0x21 || char == 0x7f {
			return errors.New("execution lease owner contains control or whitespace characters")
		}
	}
	return nil
}

func (s *executionEpochStateStore) close() error {
	if s == nil || !s.ownsRoot || s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}

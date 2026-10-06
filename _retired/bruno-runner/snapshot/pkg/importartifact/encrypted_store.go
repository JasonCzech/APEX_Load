package importartifact

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

const (
	artifactMagic          = "APEXBRUNO1\n"
	artifactFormatVersion  = 1
	defaultChunkSize       = 1 << 20
	maxHeaderBytes         = 1 << 20
	defaultSessionCapacity = 128
	staleReservationGrace  = 5 * time.Minute
	sessionClaimLease      = 30 * time.Second
)

var (
	ErrProjectDeleted        = importsession.ErrProjectDeleted
	ErrSessionCapacity       = importsession.ErrSessionCapacity
	ErrSessionClaimed        = importsession.ErrSessionClaimed
	ErrSessionClaimLost      = importsession.ErrSessionClaimLost
	ErrUnsupportedFilesystem = errors.New("protected import artifact filesystem is unsupported")
)

type EncryptedStore struct {
	root            string
	wrapper         KeyWrapper
	chunkSize       int
	readOnly        bool
	sessionCapacity int
	// syncDirectoryFn is injectable only for focused durability-ordering tests.
	// Production stores always use the OS-specific syncDirectory implementation.
	syncDirectoryFn func(string) error
}

type StoreOptions struct {
	RootDir   string
	Wrapper   KeyWrapper
	ChunkSize int
	ReadOnly  bool
	// SessionCapacity is enforced through atomic files under the shared store
	// root, so all controller replicas observe the same admission limit. Zero
	// uses the secure default.
	SessionCapacity int
}

type StoredObject struct {
	BackendKey         string
	SHA256             string
	SizeBytes          int64
	EncryptionMetadata map[string]any
}

// These aliases preserve the original Bruno package API and persisted JSON
// while exposing the same types through the importer-neutral session package.
type OpenRequest = importsession.ObjectRef
type SessionDescriptor = importsession.SessionDescriptor
type PublicationDescriptor = importsession.PublicationDescriptor

var _ importsession.ProtectedStore = (*EncryptedStore)(nil)

type sessionCapacityReservation struct {
	SessionID string       `json:"session_id"`
	ProjectID string       `json:"project_id"`
	ExpiresAt time.Time    `json:"expires_at"`
	Payload   *OpenRequest `json:"payload,omitempty"`
}

type artifactHeader struct {
	Version       int            `json:"version"`
	Algorithm     string         `json:"algorithm"`
	ProjectDigest string         `json:"project_digest"`
	Kind          string         `json:"kind"`
	SHA256        string         `json:"sha256"`
	SizeBytes     int64          `json:"size_bytes"`
	ChunkSize     int            `json:"chunk_size"`
	WrappedKey    WrappedKey     `json:"wrapped_key"`
	CreatedAt     time.Time      `json:"created_at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

func NewEncryptedStore(options StoreOptions) (*EncryptedStore, error) {
	root := strings.TrimSpace(options.RootDir)
	if root == "" {
		return nil, errors.New("Bruno artifact root directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if options.Wrapper == nil {
		return nil, errors.New("Bruno artifact key wrapper is required")
	}
	chunkSize := options.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if chunkSize < 64<<10 || chunkSize > 16<<20 {
		return nil, errors.New("Bruno artifact chunk size must be between 64 KiB and 16 MiB")
	}
	sessionCapacity := options.SessionCapacity
	if sessionCapacity == 0 {
		sessionCapacity = defaultSessionCapacity
	}
	if sessionCapacity < 0 {
		return nil, errors.New("Bruno import session capacity must not be negative")
	}
	if options.ReadOnly {
		info, statErr := os.Stat(absolute)
		if statErr != nil {
			return nil, fmt.Errorf("open read-only Bruno artifact root: %w", statErr)
		}
		if !info.IsDir() {
			return nil, errors.New("read-only Bruno artifact root is not a directory")
		}
	} else {
		if err := initializeWritableStoreRoot(absolute, syncDirectory); err != nil {
			return nil, err
		}
		if err := validateDurableFilesystem(absolute); err != nil {
			return nil, err
		}
		if err := pruneLegacyPlaintextStaging(absolute); err != nil {
			return nil, err
		}
		if err := cleanupDurableTombstones(absolute); err != nil {
			return nil, err
		}
	}
	store := &EncryptedStore{
		root: absolute, wrapper: options.Wrapper, chunkSize: chunkSize,
		readOnly: options.ReadOnly, sessionCapacity: sessionCapacity,
		syncDirectoryFn: syncDirectory,
	}
	if !options.ReadOnly {
		// Every encrypted-object writer takes the same cross-process lock, so
		// initialization can remove only temporaries left by a terminated writer.
		unlock, lockErr := store.lockEncryptedObjectWrites()
		if lockErr != nil {
			return nil, lockErr
		}
		cleanupErr := store.pruneStaleEncryptedTemps()
		unlockErr := unlock()
		if err := errors.Join(cleanupErr, unlockErr); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// initializeWritableStoreRoot prepares every ancestor used by the store. Unix
// persists directory entries through fsync. Windows validates every ancestor's
// local fixed NTFS/ReFS volume policy here; subsequent namespace publication is
// persisted by MoveFileExW with MOVEFILE_WRITE_THROUGH.
func initializeWritableStoreRoot(root string, syncFn func(string) error) error {
	root = filepath.Clean(root)
	if syncFn == nil {
		return errors.New("Bruno artifact directory sync is required")
	}
	if err := mkdirAllDurably(root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return err
	}
	for current := root; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("Bruno artifact root ancestor %q is not a directory", current)
		}
		if err := syncFn(current); err != nil {
			return fmt.Errorf("sync Bruno artifact root directory %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func (s *EncryptedStore) Put(ctx context.Context, projectID, kind string, reader io.Reader, maxBytes int64) (*StoredObject, error) {
	if s.readOnly {
		return nil, errors.New("Bruno artifact store is read-only")
	}
	projectID = strings.TrimSpace(projectID)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if projectID == "" || !validArtifactKind(kind) {
		return nil, errors.New("Bruno artifact project and valid kind are required")
	}
	if reader == nil {
		return nil, errors.New("Bruno artifact reader is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("Bruno artifact maximum size must be positive")
	}
	deleted, err := s.projectDeleted(projectID)
	if err != nil {
		return nil, err
	}
	if deleted {
		return nil, ErrProjectDeleted
	}
	stagingDir := filepath.Join(s.root, ".staging")
	if err := mkdirAllDurably(stagingDir, 0o700); err != nil {
		return nil, err
	}
	plaintext, err := os.CreateTemp(stagingDir, "plain-*")
	if err != nil {
		return nil, err
	}
	plaintextPath := plaintext.Name()
	defer os.Remove(plaintextPath)
	if err := plaintext.Chmod(0o600); err != nil {
		plaintext.Close()
		return nil, err
	}
	digest := sha256.New()
	size, err := copyBounded(ctx, io.MultiWriter(plaintext, digest), reader, maxBytes)
	closeErr := plaintext.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	sha := hex.EncodeToString(digest.Sum(nil))
	source, err := os.Open(plaintextPath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return s.putKnown(ctx, projectID, kind, source, size, sha)
}

// PutBytes encrypts a caller-owned byte slice directly into the content-
// addressed object. Unlike the streaming Put compatibility path, it never
// writes plaintext to a staging file. Protected Bruno uploads and import
// sessions use this method because their payload is already bounded in memory.
func (s *EncryptedStore) PutBytes(ctx context.Context, projectID, kind string, payload []byte, maxBytes int64) (*StoredObject, error) {
	if s.readOnly {
		return nil, errors.New("Bruno artifact store is read-only")
	}
	projectID = strings.TrimSpace(projectID)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if projectID == "" || !validArtifactKind(kind) {
		return nil, errors.New("Bruno artifact project and valid kind are required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("Bruno artifact maximum size must be positive")
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("Bruno artifact exceeds maximum size %d", maxBytes)
	}
	deleted, err := s.projectDeleted(projectID)
	if err != nil {
		return nil, err
	}
	if deleted {
		return nil, ErrProjectDeleted
	}
	digest := sha256.Sum256(payload)
	return s.putKnown(ctx, projectID, kind, bytes.NewReader(payload), int64(len(payload)), hex.EncodeToString(digest[:]))
}

// PlanBytes returns the immutable object identity PutBytes will use. Callers use
// it to durably journal a publication before the encrypted object is created.
func (s *EncryptedStore) PlanBytes(projectID, kind string, payload []byte, maxBytes int64) (OpenRequest, error) {
	projectID = strings.TrimSpace(projectID)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if projectID == "" || !validArtifactKind(kind) {
		return OpenRequest{}, errors.New("Bruno artifact project and valid kind are required")
	}
	if maxBytes <= 0 {
		return OpenRequest{}, errors.New("Bruno artifact maximum size must be positive")
	}
	if int64(len(payload)) > maxBytes {
		return OpenRequest{}, fmt.Errorf("Bruno artifact exceeds maximum size %d", maxBytes)
	}
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	return OpenRequest{
		BackendKey: artifactBackendKey(projectID, kind, sha),
		ProjectID:  projectID,
		Kind:       kind,
		SHA256:     sha,
		SizeBytes:  int64(len(payload)),
	}, nil
}

func (s *EncryptedStore) putKnown(ctx context.Context, projectID, kind string, source io.Reader, size int64, sha string) (*StoredObject, error) {
	backendKey := artifactBackendKey(projectID, kind, sha)
	target, err := s.pathForBackendKey(backendKey)
	if err != nil {
		return nil, err
	}
	if header, err := s.readExistingHeader(target); err == nil {
		request := OpenRequest{
			BackendKey: backendKey,
			ProjectID:  projectID,
			Kind:       kind,
			SHA256:     sha,
			SizeBytes:  size,
		}
		if err := validateArtifactHeader(header, request); err != nil {
			return nil, err
		}
		if err := s.verifyStoredObject(ctx, request); err != nil {
			return nil, err
		}
		if err := s.rejectObjectWrittenAfterProjectDeletion(ctx, projectID, OpenRequest{
			BackendKey: backendKey,
			ProjectID:  projectID,
			Kind:       kind,
			SHA256:     sha,
			SizeBytes:  size,
		}); err != nil {
			return nil, err
		}
		return storedObjectFromHeader(backendKey, header), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := mkdirAllDurably(filepath.Dir(target), 0o700); err != nil {
		return nil, s.projectWriteError(projectID, err)
	}
	unlockEncryptedWrites, err := s.lockEncryptedObjectWrites()
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlockEncryptedWrites() }()
	dek := make([]byte, 32)
	defer clear(dek)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, err
	}
	wrapAAD := artifactWrapAAD(projectID, kind, sha)
	wrapped, err := s.wrapper.WrapKey(ctx, dek, wrapAAD)
	if err != nil {
		return nil, fmt.Errorf("wrap Bruno artifact data key: %w", err)
	}
	header := artifactHeader{
		Version:       artifactFormatVersion,
		Algorithm:     "AES-256-GCM-CHUNKED",
		ProjectDigest: projectDigest(projectID),
		Kind:          kind,
		SHA256:        sha,
		SizeBytes:     size,
		ChunkSize:     s.chunkSize,
		WrappedKey:    wrapped,
		CreatedAt:     time.Now().UTC(),
		Metadata: map[string]any{
			"content_addressed": true,
		},
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".encrypted-*")
	if err != nil {
		return nil, s.projectWriteError(projectID, err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return nil, err
	}
	if err := writeEncryptedArtifact(ctx, temp, source, dek, header); err != nil {
		temp.Close()
		return nil, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	if err := s.linkDurably(tempPath, target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, s.projectWriteError(projectID, err)
		}
		existing, readErr := s.readExistingHeader(target)
		if readErr != nil {
			return nil, s.projectWriteError(projectID, readErr)
		}
		request := OpenRequest{
			BackendKey: backendKey,
			ProjectID:  projectID,
			Kind:       kind,
			SHA256:     sha,
			SizeBytes:  size,
		}
		if err := validateArtifactHeader(existing, request); err != nil {
			return nil, err
		}
		if err := s.verifyStoredObject(ctx, request); err != nil {
			return nil, err
		}
		header = existing
	}
	if err := s.rejectObjectWrittenAfterProjectDeletion(ctx, projectID, OpenRequest{
		BackendKey: backendKey,
		ProjectID:  projectID,
		Kind:       kind,
		SHA256:     sha,
		SizeBytes:  size,
	}); err != nil {
		return nil, err
	}
	return storedObjectFromHeader(backendKey, header), nil
}

func (s *EncryptedStore) Open(ctx context.Context, request OpenRequest) (io.ReadCloser, int64, error) {
	target, err := s.pathForBackendKey(request.BackendKey)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, 0, errors.New("Bruno artifact backend object is not a regular file")
	}
	header, reader, err := readArtifactHeader(file)
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	if err := validateArtifactHeader(header, request); err != nil {
		file.Close()
		return nil, 0, err
	}
	dek, err := s.wrapper.UnwrapKey(ctx, header.WrappedKey, artifactWrapAAD(request.ProjectID, header.Kind, header.SHA256))
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("unwrap Bruno artifact data key: %w", err)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		defer file.Close()
		err := decryptArtifact(ctx, reader, pipeWriter, aead, header)
		_ = pipeWriter.CloseWithError(err)
	}()
	return pipeReader, header.SizeBytes, nil
}

// Delete removes an encrypted object after callers have established that no
// durable revision references it. Missing objects are treated as already
// deleted.
func (s *EncryptedStore) Delete(_ context.Context, request OpenRequest) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	target, err := s.pathForBackendKey(request.BackendKey)
	if err != nil {
		return err
	}
	header, err := s.readExistingHeader(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateArtifactHeader(header, request); err != nil {
		return err
	}
	if err := s.removeDurably(target); err != nil {
		return err
	}
	return nil
}

func (s *EncryptedStore) SavePublication(descriptor PublicationDescriptor) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	descriptor.ProjectID = strings.TrimSpace(descriptor.ProjectID)
	if descriptor.ID == "" || strings.ContainsAny(descriptor.ID, `/\`) || strings.Contains(descriptor.ID, "..") {
		return errors.New("invalid Bruno publication id")
	}
	if descriptor.ProjectID == "" || descriptor.ExpiresAt.IsZero() || len(descriptor.Objects) == 0 {
		return errors.New("Bruno publication project, expiry, and objects are required")
	}
	for _, request := range descriptor.Objects {
		kind := strings.ToLower(strings.TrimSpace(request.Kind))
		if strings.TrimSpace(request.ProjectID) != descriptor.ProjectID ||
			!validArtifactKind(kind) ||
			!validArtifactSHA256(request.SHA256) ||
			request.BackendKey != artifactBackendKey(descriptor.ProjectID, kind, request.SHA256) ||
			request.SizeBytes < 0 {
			return errors.New("Bruno publication contains an invalid object identity")
		}
	}
	if deleted, err := s.projectDeleted(descriptor.ProjectID); err != nil {
		return err
	} else if deleted {
		return ErrProjectDeleted
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	directory := filepath.Join(s.root, ".publications")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".publication-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	target := filepath.Join(directory, descriptor.ID+".json")
	if err := s.linkDurably(tempPath, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("Bruno publication already exists")
		}
		return err
	}
	return nil
}

func (s *EncryptedStore) DeletePublication(id, projectID string) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return errors.New("invalid Bruno publication id")
	}
	target := filepath.Join(s.root, ".publications", id+".json")
	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var descriptor PublicationDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return err
	}
	if descriptor.ID != id || strings.TrimSpace(descriptor.ProjectID) != projectID {
		return errors.New("Bruno publication belongs to another project")
	}
	if err := s.removeDurably(target); err != nil {
		return err
	}
	return nil
}

func (s *EncryptedStore) ListExpiredPublications(now time.Time) ([]PublicationDescriptor, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, ".publications"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var descriptors []PublicationDescriptor
	var readErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		target := filepath.Join(s.root, ".publications", entry.Name())
		data, readErr := os.ReadFile(target)
		if readErr != nil {
			readErrors = append(readErrors, readErr)
			continue
		}
		var descriptor PublicationDescriptor
		if decodeErr := json.Unmarshal(data, &descriptor); decodeErr != nil {
			readErrors = append(readErrors, decodeErr)
			continue
		}
		if !descriptor.ExpiresAt.After(now.UTC()) {
			descriptors = append(descriptors, descriptor)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	return descriptors, errors.Join(readErrors...)
}

func (s *EncryptedStore) SaveSession(ctx context.Context, descriptor SessionDescriptor, reader io.Reader, maxBytes int64) error {
	return s.saveSession(ctx, descriptor, func() (*StoredObject, error) {
		return s.Put(ctx, descriptor.ProjectID, "session", reader, maxBytes)
	})
}

// SaveSessionBytes is the protected-session counterpart to PutBytes. Capacity
// is reserved before the encrypted payload is persisted, and plaintext never
// touches the artifact filesystem.
func (s *EncryptedStore) SaveSessionBytes(ctx context.Context, descriptor SessionDescriptor, payload []byte, maxBytes int64) error {
	planned, err := s.PlanBytes(descriptor.ProjectID, "session", payload, maxBytes)
	if err != nil {
		return err
	}
	descriptor.BackendKey = planned.BackendKey
	descriptor.SHA256 = planned.SHA256
	descriptor.SizeBytes = planned.SizeBytes
	return s.saveSession(ctx, descriptor, func() (*StoredObject, error) {
		return s.PutBytes(ctx, descriptor.ProjectID, "session", payload, maxBytes)
	})
}

func (s *EncryptedStore) saveSession(ctx context.Context, descriptor SessionDescriptor, persistPayload func() (*StoredObject, error)) error {
	// Session payloads are content-addressed. Keep publication under the same
	// cross-process lock as claim cleanup so a concurrent finalizer cannot
	// delete a deduplicated payload between Put and descriptor publication.
	unlockClaims, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlockClaims() }()
	return s.saveSessionLocked(ctx, descriptor, persistPayload)
}

func (s *EncryptedStore) saveSessionLocked(ctx context.Context, descriptor SessionDescriptor, persistPayload func() (*StoredObject, error)) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	descriptor.ProjectID = strings.TrimSpace(descriptor.ProjectID)
	descriptor.Actor = strings.TrimSpace(descriptor.Actor)
	if !validSessionID(descriptor.ID) {
		return errors.New("invalid Bruno import session id")
	}
	if descriptor.ProjectID == "" || descriptor.Actor == "" || descriptor.ExpiresAt.IsZero() {
		return errors.New("Bruno import session id, project, actor, and expiry are required")
	}
	deleted, err := s.projectDeleted(descriptor.ProjectID)
	if err != nil {
		return err
	}
	if deleted {
		return ErrProjectDeleted
	}
	slot, err := s.reserveSessionCapacity(descriptor)
	if err != nil {
		deleted, deletedErr := s.projectDeleted(descriptor.ProjectID)
		if deletedErr != nil {
			return errors.Join(err, deletedErr)
		}
		if deleted {
			return ErrProjectDeleted
		}
		return err
	}
	descriptor.CapacitySlot = &slot
	reservationPublished := false
	defer func() {
		if !reservationPublished {
			_ = s.releaseSessionCapacity(descriptor)
		}
	}()
	object, err := persistPayload()
	if err != nil {
		return err
	}
	persisted := false
	defer func() {
		if !persisted {
			referenced, referenceErr := s.sessionPayloadReferenced(object.BackendKey, "")
			if referenceErr != nil {
				// Leave the capacity reservation as a durable GC locator.
				reservationPublished = true
				return
			}
			if referenced {
				return
			}
			if deleteErr := s.Delete(context.Background(), OpenRequest{
				BackendKey: object.BackendKey,
				ProjectID:  descriptor.ProjectID,
				Kind:       "session",
				SHA256:     object.SHA256,
				SizeBytes:  object.SizeBytes,
			}); deleteErr != nil {
				// Leave the capacity reservation as a durable GC locator.
				reservationPublished = true
			}
		}
	}()
	descriptor.BackendKey = object.BackendKey
	descriptor.SHA256 = object.SHA256
	descriptor.SizeBytes = object.SizeBytes
	data, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	directory := filepath.Join(s.root, ".sessions")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return err
	}
	target := filepath.Join(directory, descriptor.ID+".json")
	temp, err := os.CreateTemp(directory, ".session-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Publish create-only. Replacing an existing descriptor would leak its
	// capacity reservation and could turn a one-shot session into a replayable
	// session after a controller race.
	if err := s.linkDurably(tempPath, target); err != nil {
		return err
	}
	if deleted, err := s.projectDeleted(descriptor.ProjectID); err != nil {
		_ = s.deleteSessionLocked(context.Background(), descriptor.ID, descriptor.ProjectID)
		return err
	} else if deleted {
		_ = s.deleteSessionLocked(context.Background(), descriptor.ID, descriptor.ProjectID)
		return ErrProjectDeleted
	}
	persisted = true
	reservationPublished = true
	return nil
}

func (s *EncryptedStore) reserveSessionCapacity(descriptor SessionDescriptor) (int, error) {
	directory := filepath.Join(s.root, ".sessions", ".capacity")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return 0, err
	}
	reservation := sessionCapacityReservation{
		SessionID: descriptor.ID,
		ProjectID: descriptor.ProjectID,
		ExpiresAt: descriptor.ExpiresAt.UTC(),
	}
	if descriptor.BackendKey != "" && descriptor.SHA256 != "" {
		reservation.Payload = &OpenRequest{
			BackendKey: descriptor.BackendKey,
			ProjectID:  descriptor.ProjectID,
			Kind:       "session",
			SHA256:     descriptor.SHA256,
			SizeBytes:  descriptor.SizeBytes,
		}
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return 0, err
	}
	temp, err := os.CreateTemp(directory, ".capacity-*")
	if err != nil {
		return 0, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return 0, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return 0, err
	}
	if err := temp.Close(); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for slot := 0; slot < s.sessionCapacity; slot++ {
		path := s.sessionCapacityPath(slot)
		for attempt := 0; attempt < 2; attempt++ {
			linkErr := s.linkDurably(tempPath, path)
			if linkErr == nil {
				return slot, nil
			}
			if !errors.Is(linkErr, os.ErrExist) {
				return 0, linkErr
			}
			reclaimed, reclaimErr := s.reclaimSessionCapacityReservation(path, now)
			if reclaimErr != nil {
				return 0, reclaimErr
			}
			if !reclaimed {
				break
			}
		}
	}
	return 0, ErrSessionCapacity
}

func (s *EncryptedStore) reclaimSessionCapacityReservation(path string, now time.Time) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var reservation sessionCapacityReservation
	if err := json.Unmarshal(data, &reservation); err == nil && reservation.SessionID != "" && reservation.ProjectID != "" && !reservation.ExpiresAt.IsZero() {
		if reservation.ExpiresAt.After(now) {
			return false, nil
		}
	} else {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return true, nil
		}
		if statErr != nil {
			return false, statErr
		}
		if info.ModTime().Add(staleReservationGrace).After(now) {
			return false, nil
		}
	}
	if reservation.Payload != nil {
		referenced, referenceErr := s.sessionPayloadReferenced(reservation.Payload.BackendKey, "")
		if referenceErr != nil {
			return false, referenceErr
		}
		if !referenced {
			if deleteErr := s.Delete(context.Background(), *reservation.Payload); deleteErr != nil {
				return false, deleteErr
			}
		}
	}
	if err := s.removeDurably(path); err != nil {
		return false, err
	}
	return true, nil
}

func (s *EncryptedStore) pruneExpiredSessionCapacityReservations(now time.Time) error {
	directory := filepath.Join(s.root, ".sessions", ".capacity")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "slot-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if _, reclaimErr := s.reclaimSessionCapacityReservation(filepath.Join(directory, entry.Name()), now.UTC()); reclaimErr != nil {
			cleanupErrors = append(cleanupErrors, reclaimErr)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *EncryptedStore) releaseSessionCapacity(descriptor SessionDescriptor) error {
	if descriptor.CapacitySlot == nil {
		return nil
	}
	slot := *descriptor.CapacitySlot
	if slot < 0 || slot >= s.sessionCapacity {
		return errors.New("Bruno import session capacity slot is invalid")
	}
	path := s.sessionCapacityPath(slot)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var reservation sessionCapacityReservation
	if err := json.Unmarshal(data, &reservation); err != nil {
		return err
	}
	if reservation.SessionID != descriptor.ID || strings.TrimSpace(reservation.ProjectID) != strings.TrimSpace(descriptor.ProjectID) {
		// The original reservation expired and this slot has already been
		// reassigned. Never let late cleanup remove the new owner's admission.
		return nil
	}
	if err := s.removeDurably(path); err != nil {
		return err
	}
	return nil
}

func (s *EncryptedStore) sessionCapacityPath(slot int) string {
	return filepath.Join(s.root, ".sessions", ".capacity", fmt.Sprintf("slot-%06d.json", slot))
}

func (s *EncryptedStore) lockSessionClaims() (func() error, error) {
	directory := filepath.Join(s.root, ".project-tombstones")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return nil, err
	}
	return filelock.Acquire(filepath.Join(directory, ".session-claims.lock"))
}

func (s *EncryptedStore) lockEncryptedObjectWrites() (func() error, error) {
	directory := filepath.Join(s.root, ".project-tombstones")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return nil, err
	}
	return filelock.Acquire(filepath.Join(directory, ".encrypted-objects.lock"))
}

func (s *EncryptedStore) syncDirectoryChain(directory string) error {
	directory = filepath.Clean(directory)
	root := filepath.Clean(s.root)
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("Bruno artifact durability path escapes its store root")
	}
	syncFn := s.syncDirectoryFn
	if syncFn == nil {
		syncFn = syncDirectory
	}
	for current := directory; ; current = filepath.Dir(current) {
		if err := syncFn(current); err != nil {
			return fmt.Errorf("sync Bruno artifact directory %q: %w", current, err)
		}
		if current == root {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("Bruno artifact durability path did not reach its store root")
		}
	}
}

func (s *EncryptedStore) linkDurably(existingPath, targetPath string) error {
	if err := publishCreateOnly(existingPath, targetPath); err != nil {
		return err
	}
	if err := s.syncDirectoryChain(filepath.Dir(targetPath)); err != nil {
		// The create-only caller has not accepted ownership yet. Roll the link
		// back when durability cannot be established so a retry does not mistake
		// an uncertain publication for a completed one.
		return errors.Join(err, s.removeDurably(targetPath))
	}
	return nil
}

func (s *EncryptedStore) renameDurably(oldPath, newPath string) error {
	if err := renameWriteThrough(oldPath, newPath); err != nil {
		return err
	}
	newDirectory := filepath.Dir(newPath)
	if err := s.syncDirectoryChain(newDirectory); err != nil {
		return err
	}
	oldDirectory := filepath.Dir(oldPath)
	if filepath.Clean(oldDirectory) != filepath.Clean(newDirectory) {
		return s.syncDirectoryChain(oldDirectory)
	}
	return nil
}

// removeDurably also syncs an existing parent when the target is already
// absent. That makes a retry complete an earlier unlink whose directory sync
// may have failed after the namespace mutation succeeded.
func (s *EncryptedStore) removeDurably(path string) error {
	if err := removeWriteThrough(s.root, path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(path)
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return s.syncDirectoryChain(directory)
}

// LockSourcePublication serializes source-object publication with reference
// checks and deletion across every controller sharing this artifact root.
// Callers must hold this lock from the first raw/sanitized Put through durable
// session descriptor publication. Cleanup paths hold the same lock while
// checking references and deleting, closing the content-addressed Put-to-
// descriptor publication gap.
func (s *EncryptedStore) LockSourcePublication() (func() error, error) {
	if s.readOnly {
		return nil, errors.New("Bruno artifact store is read-only")
	}
	directory := filepath.Join(s.root, ".project-tombstones")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return nil, err
	}
	return filelock.Acquire(filepath.Join(directory, ".source-publication.lock"))
}

func (s *EncryptedStore) OpenSession(ctx context.Context, id, projectID, actor string) (io.ReadCloser, SessionDescriptor, error) {
	return s.openSession(ctx, id, projectID, actor, "", false)
}

func (s *EncryptedStore) OpenSessionScoped(
	ctx context.Context,
	scope importsession.Scope,
) (io.ReadCloser, SessionDescriptor, error) {
	return s.openSession(ctx, scope.ID, scope.ProjectID, scope.Actor, scope.Profile, true)
}

func (s *EncryptedStore) openSession(
	ctx context.Context,
	id, projectID, actor, profile string,
	enforceProfile bool,
) (io.ReadCloser, SessionDescriptor, error) {
	id = strings.TrimSpace(id)
	if !validSessionID(id) {
		return nil, SessionDescriptor{}, errors.New("invalid Bruno import session id")
	}
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	if projectID == "" || actor == "" {
		return nil, SessionDescriptor{}, errors.New("Bruno import session project and actor are required")
	}
	target := filepath.Join(s.root, ".sessions", id+".json")
	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		if recoverErr := s.recoverStaleSessionClaimScoped(
			id, projectID, actor, profile, enforceProfile, time.Now().UTC(),
		); recoverErr == nil {
			data, err = os.ReadFile(target)
		} else if !errors.Is(recoverErr, os.ErrNotExist) {
			return nil, SessionDescriptor{}, recoverErr
		}
	}
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	var descriptor SessionDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, SessionDescriptor{}, err
	}
	if descriptor.ID != id || strings.TrimSpace(descriptor.ProjectID) != projectID || strings.TrimSpace(descriptor.Actor) != actor {
		return nil, SessionDescriptor{}, os.ErrNotExist
	}
	if enforceProfile && descriptor.EffectiveProfile() != effectiveSessionProfile(profile) {
		return nil, SessionDescriptor{}, os.ErrNotExist
	}
	if time.Now().UTC().After(descriptor.ExpiresAt) {
		// Preserve the descriptor until the janitor can use RelatedObjects to
		// clean raw and sanitized sources safely. Deleting it here would release
		// capacity but permanently orphan every uncommitted source object.
		return nil, descriptor, errors.New("Bruno import session expired")
	}
	reader, _, err := s.Open(ctx, OpenRequest{
		BackendKey: descriptor.BackendKey,
		ProjectID:  descriptor.ProjectID,
		Kind:       "session",
		SHA256:     descriptor.SHA256,
		SizeBytes:  descriptor.SizeBytes,
	})
	return reader, descriptor, err
}

// ClaimSession atomically leases a persisted import session while retaining its
// encrypted payload. The caller must release the claim after failure or
// complete it after the durable database transaction succeeds.
func (s *EncryptedStore) ClaimSession(ctx context.Context, id, projectID, actor string, maxBytes int64) ([]byte, SessionDescriptor, error) {
	return s.claimSession(ctx, id, projectID, actor, "", false, maxBytes)
}

func (s *EncryptedStore) ClaimSessionScoped(
	ctx context.Context,
	scope importsession.Scope,
	maxBytes int64,
) ([]byte, SessionDescriptor, error) {
	return s.claimSession(ctx, scope.ID, scope.ProjectID, scope.Actor, scope.Profile, true, maxBytes)
}

func (s *EncryptedStore) claimSession(
	ctx context.Context,
	id, projectID, actor, profile string,
	enforceProfile bool,
	maxBytes int64,
) ([]byte, SessionDescriptor, error) {
	if s.readOnly {
		return nil, SessionDescriptor{}, errors.New("Bruno artifact store is read-only")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	if !validSessionID(id) {
		return nil, SessionDescriptor{}, errors.New("invalid Bruno import session id")
	}
	if projectID == "" || actor == "" {
		return nil, SessionDescriptor{}, errors.New("Bruno import session project and actor are required")
	}
	if maxBytes <= 0 {
		return nil, SessionDescriptor{}, errors.New("Bruno import session maximum size must be positive")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	defer func() { _ = unlock() }()
	directory := filepath.Join(s.root, ".sessions")
	target := filepath.Join(directory, id+".json")
	_, descriptor, err := readSessionDescriptorFile(target)
	if errors.Is(err, os.ErrNotExist) {
		if recoverErr := s.recoverStaleSessionClaimScopedLocked(
			id, projectID, actor, profile, enforceProfile, time.Now().UTC(),
		); recoverErr == nil {
			_, descriptor, err = readSessionDescriptorFile(target)
		} else if !errors.Is(recoverErr, os.ErrNotExist) {
			return nil, SessionDescriptor{}, recoverErr
		}
	}
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	if descriptor.ID != id || strings.TrimSpace(descriptor.ProjectID) != projectID || strings.TrimSpace(descriptor.Actor) != actor {
		return nil, SessionDescriptor{}, os.ErrNotExist
	}
	if enforceProfile && descriptor.EffectiveProfile() != effectiveSessionProfile(profile) {
		return nil, SessionDescriptor{}, os.ErrNotExist
	}
	claimToken, err := newSessionClaimToken()
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	descriptor.ClaimToken = claimToken
	claimPath := filepath.Join(directory, id+".claim.json")
	if _, err := os.Stat(claimPath); err == nil {
		return nil, SessionDescriptor{}, ErrSessionClaimed
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, SessionDescriptor{}, err
	}
	// Publish the epoch token in the normal descriptor first, then atomically
	// rename that descriptor into the claim state. A crash between these two
	// operations leaves a retryable normal descriptor rather than both normal
	// and claim descriptors.
	if err := s.replaceSessionDescriptorFile(target, descriptor); err != nil {
		return nil, SessionDescriptor{}, err
	}
	if err := s.renameDurably(target, claimPath); err != nil {
		return nil, SessionDescriptor{}, err
	}

	request := OpenRequest{
		BackendKey: descriptor.BackendKey,
		ProjectID:  descriptor.ProjectID,
		Kind:       "session",
		SHA256:     descriptor.SHA256,
		SizeBytes:  descriptor.SizeBytes,
	}
	payload, readErr := s.ReadAll(ctx, request, maxBytes)
	if readErr != nil {
		clear(payload)
		restoreErr := s.releaseSessionClaimLocked(id, projectID, actor, claimToken)
		return nil, SessionDescriptor{}, errors.Join(readErr, restoreErr)
	}
	return payload, descriptor, nil
}

// TouchSessionClaim refreshes an active lease so a slow live commit cannot be
// reclaimed as though its controller had crashed.
func (s *EncryptedStore) TouchSessionClaim(id, projectID, actor, claimToken string, now time.Time) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	claimPath := filepath.Join(s.root, ".sessions", strings.TrimSpace(id)+".claim.json")
	_, descriptor, err := readSessionDescriptorFile(claimPath)
	if err != nil {
		return err
	}
	if err := validateSessionClaimDescriptor(descriptor, id, projectID, actor, claimToken); err != nil {
		return err
	}
	now = now.UTC()
	return os.Chtimes(claimPath, now, now)
}

// ReleaseSessionClaim restores the durable descriptor without re-encrypting
// the retained payload.
func (s *EncryptedStore) ReleaseSessionClaim(id, projectID, actor, claimToken string) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	return s.releaseSessionClaimLocked(id, projectID, actor, claimToken)
}

// RestoreSessionClaim releases an unexpired claim for retry. If the session
// has reached its original security expiry, it is consumed instead and false
// is returned so callers never advertise retained credential material that is
// no longer retryable.
func (s *EncryptedStore) RestoreSessionClaim(
	ctx context.Context,
	id, projectID, actor, claimToken string,
	now time.Time,
) (bool, error) {
	if s.readOnly {
		return false, errors.New("Bruno artifact store is read-only")
	}
	if !validSessionID(id) {
		return false, errors.New("invalid Bruno import session id")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()
	claimPath := filepath.Join(s.root, ".sessions", strings.TrimSpace(id)+".claim.json")
	_, descriptor, err := readSessionDescriptorFile(claimPath)
	if err != nil {
		return false, err
	}
	if err := validateSessionClaimDescriptor(descriptor, id, projectID, actor, claimToken); err != nil {
		return false, err
	}
	if !descriptor.ExpiresAt.After(now.UTC()) {
		if err := s.consumeExpiredSessionClaimLocked(ctx, descriptor, claimPath); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := s.releaseSessionClaimLocked(id, projectID, actor, claimToken); err != nil {
		return false, err
	}
	return true, nil
}

func (s *EncryptedStore) releaseSessionClaimLocked(id, projectID, actor, claimToken string) error {
	directory := filepath.Join(s.root, ".sessions")
	claimPath := filepath.Join(directory, strings.TrimSpace(id)+".claim.json")
	target := filepath.Join(directory, strings.TrimSpace(id)+".json")
	_, descriptor, err := readSessionDescriptorFile(claimPath)
	if err != nil {
		return err
	}
	if err := validateSessionClaimDescriptor(descriptor, id, projectID, actor, claimToken); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		return errors.New("Bruno import session descriptor already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.renameDurably(claimPath, target); err != nil {
		return err
	}
	// The rename is the durable state transition. Clearing the now-inert epoch
	// token is best effort: if this metadata rewrite fails, a future ClaimSession
	// replaces it before publishing a new claim.
	descriptor.ClaimToken = ""
	_ = s.replaceSessionDescriptorFile(target, descriptor)
	return nil
}

// CompleteSessionClaim removes the claim and encrypted session payload only
// after the caller has committed its database transaction.
func (s *EncryptedStore) CompleteSessionClaim(ctx context.Context, id, projectID, actor, claimToken string) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	directory := filepath.Join(s.root, ".sessions")
	claimPath := filepath.Join(directory, strings.TrimSpace(id)+".claim.json")
	_, descriptor, err := readSessionDescriptorFile(claimPath)
	if err != nil {
		return err
	}
	if err := validateSessionClaimDescriptor(descriptor, id, projectID, actor, claimToken); err != nil {
		return err
	}
	return s.completeSessionClaimLocked(ctx, descriptor, claimPath)
}

// consumeExpiredSessionClaimLocked keeps the descriptor—the only durable
// locator for RelatedObjects—until payload and capacity cleanup have both
// succeeded. If the final unlink has an uncertain durability result, restore
// the descriptor in the live namespace so the janitor can finish cleanup.
func (s *EncryptedStore) consumeExpiredSessionClaimLocked(
	ctx context.Context,
	descriptor SessionDescriptor,
	claimPath string,
) error {
	if err := s.deleteSessionPayloadIfUnreferenced(ctx, descriptor, claimPath); err != nil {
		return err
	}
	if releaseErr := s.releaseSessionCapacity(descriptor); releaseErr != nil {
		return fmt.Errorf("release expired Bruno import session capacity: %w", releaseErr)
	}
	if err := s.removeDurably(claimPath); err != nil {
		restoreErr := s.restoreSessionCleanupLocator(claimPath, descriptor)
		return errors.Join(err, restoreErr)
	}
	return nil
}

func (s *EncryptedStore) completeSessionClaimLocked(
	ctx context.Context,
	descriptor SessionDescriptor,
	claimPath string,
) error {
	if err := s.deleteSessionPayloadIfUnreferenced(ctx, descriptor, claimPath); err != nil {
		return err
	}
	// Delete the credential-bearing payload before its final recovery pointer.
	// A crash after payload deletion leaves a harmless claim descriptor that a
	// receipt retry or stale-claim janitor can remove idempotently.
	if err := s.removeDurably(claimPath); err != nil {
		return err
	}
	if releaseErr := s.releaseSessionCapacity(descriptor); releaseErr != nil {
		return fmt.Errorf("release consumed Bruno import session capacity: %w", releaseErr)
	}
	return nil
}

func (s *EncryptedStore) deleteSessionPayloadIfUnreferenced(
	ctx context.Context,
	descriptor SessionDescriptor,
	excludedPath string,
) error {
	request := OpenRequest{
		BackendKey: descriptor.BackendKey,
		ProjectID:  descriptor.ProjectID,
		Kind:       "session",
		SHA256:     descriptor.SHA256,
		SizeBytes:  descriptor.SizeBytes,
	}
	referenced, referenceErr := s.sessionPayloadReferenced(request.BackendKey, excludedPath)
	if referenceErr != nil {
		return referenceErr
	}
	if !referenced {
		if deleteErr := s.Delete(ctx, request); deleteErr != nil {
			return deleteErr
		}
	}
	return nil
}

func (s *EncryptedStore) restoreSessionCleanupLocator(path string, descriptor SessionDescriptor) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect expired Bruno import session cleanup locator: %w", err)
	}
	if err := s.replaceSessionDescriptorFile(path, descriptor); err != nil {
		// renameDurably can report a directory-sync failure after publishing the
		// replacement. If it is visible, the janitor still has its locator.
		if _, statErr := os.Stat(path); statErr == nil {
			return nil
		}
		return fmt.Errorf("restore expired Bruno import session cleanup locator: %w", err)
	}
	return nil
}

// FinalizeCommittedSession removes either a normal or claimed session after a
// durable idempotency receipt proves that no retry needs its credential-bearing
// payload. Actor validation prevents a receipt replay from cleaning up a
// different principal's session if storage has been corrupted.
func (s *EncryptedStore) FinalizeCommittedSession(ctx context.Context, id, projectID, actor string) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	if projectID == "" || actor == "" {
		return errors.New("Bruno import session project and actor are required")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	directory := filepath.Join(s.root, ".sessions")
	found := false
	for _, path := range []string{
		filepath.Join(directory, id+".claim.json"),
		filepath.Join(directory, id+".json"),
	} {
		_, descriptor, err := readSessionDescriptorFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		found = true
		if descriptor.ID != id ||
			strings.TrimSpace(descriptor.ProjectID) != projectID ||
			strings.TrimSpace(descriptor.Actor) != actor {
			return os.ErrNotExist
		}
	}
	if !found {
		return nil
	}
	return s.deleteSessionLocked(ctx, id, projectID)
}

// FinalizeCommittedSessionScoped is the importer-neutral, profile-fenced
// finalizer. The legacy method above remains unchanged for Bruno callers that
// predate descriptor profiles.
func (s *EncryptedStore) FinalizeCommittedSessionScoped(ctx context.Context, scope importsession.Scope) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	id := strings.TrimSpace(scope.ID)
	projectID := strings.TrimSpace(scope.ProjectID)
	actor := strings.TrimSpace(scope.Actor)
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	if projectID == "" || actor == "" {
		return errors.New("Bruno import session project and actor are required")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	found, err := s.validateScopedSessionDescriptorsLocked(id, projectID, actor, scope.Profile)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return s.deleteSessionLocked(ctx, id, projectID)
}

func (s *EncryptedStore) recoverStaleSessionClaim(
	id, projectID, actor string,
	now time.Time,
) error {
	return s.recoverStaleSessionClaimScoped(id, projectID, actor, "", false, now)
}

func (s *EncryptedStore) recoverStaleSessionClaimScoped(
	id, projectID, actor, profile string,
	enforceProfile bool,
	now time.Time,
) error {
	if s.readOnly {
		_, descriptor, err := readSessionDescriptorFile(
			filepath.Join(s.root, ".sessions", strings.TrimSpace(id)+".claim.json"),
		)
		if err != nil {
			return err
		}
		if strings.TrimSpace(descriptor.ProjectID) != strings.TrimSpace(projectID) ||
			strings.TrimSpace(descriptor.Actor) != strings.TrimSpace(actor) ||
			(enforceProfile && descriptor.EffectiveProfile() != effectiveSessionProfile(profile)) {
			return os.ErrNotExist
		}
		return ErrSessionClaimed
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	return s.recoverStaleSessionClaimScopedLocked(
		id, projectID, actor, profile, enforceProfile, now,
	)
}

func (s *EncryptedStore) recoverStaleSessionClaimLocked(
	id, projectID, actor string,
	now time.Time,
) error {
	return s.recoverStaleSessionClaimScopedLocked(id, projectID, actor, "", false, now)
}

func (s *EncryptedStore) recoverStaleSessionClaimScopedLocked(
	id, projectID, actor, profile string,
	enforceProfile bool,
	now time.Time,
) error {
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	directory := filepath.Join(s.root, ".sessions")
	claimPath := filepath.Join(directory, strings.TrimSpace(id)+".claim.json")
	target := filepath.Join(directory, strings.TrimSpace(id)+".json")
	info, err := os.Stat(claimPath)
	if err != nil {
		return err
	}
	_, descriptor, err := readSessionDescriptorFile(claimPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(descriptor.ProjectID) != strings.TrimSpace(projectID) ||
		strings.TrimSpace(descriptor.Actor) != strings.TrimSpace(actor) ||
		(enforceProfile && descriptor.EffectiveProfile() != effectiveSessionProfile(profile)) {
		return os.ErrNotExist
	}
	if now.UTC().Sub(info.ModTime().UTC()) <= sessionClaimLease {
		return ErrSessionClaimed
	}
	if _, err := os.Stat(target); err == nil {
		return errors.New("Bruno import session descriptor already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.renameDurably(claimPath, target); err != nil {
		return err
	}
	// As in ReleaseSessionClaim, the token is inert once the claim path is gone.
	// Keep recovery atomic and clear it opportunistically afterward.
	descriptor.ClaimToken = ""
	_ = s.replaceSessionDescriptorFile(target, descriptor)
	return nil
}

// ConsumeSession preserves the canonical import's existing one-shot behavior.
// Native batches use the leased lifecycle directly.
func (s *EncryptedStore) ConsumeSession(ctx context.Context, id, projectID, actor string, maxBytes int64) ([]byte, SessionDescriptor, error) {
	payload, descriptor, err := s.ClaimSession(ctx, id, projectID, actor, maxBytes)
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	if err := s.CompleteSessionClaim(ctx, id, projectID, actor, descriptor.ClaimToken); err != nil {
		clear(payload)
		_ = s.ReleaseSessionClaim(id, projectID, actor, descriptor.ClaimToken)
		return nil, SessionDescriptor{}, err
	}
	return payload, descriptor, nil
}

func (s *EncryptedStore) DeleteSession(ctx context.Context, id, projectID string) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	id = strings.TrimSpace(id)
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	return s.deleteSessionLocked(ctx, id, projectID)
}

// DeleteSessionScoped removes a session only when all persisted scope fields,
// including its importer profile, match. Missing sessions remain idempotent.
func (s *EncryptedStore) DeleteSessionScoped(ctx context.Context, scope importsession.Scope) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	id := strings.TrimSpace(scope.ID)
	projectID := strings.TrimSpace(scope.ProjectID)
	actor := strings.TrimSpace(scope.Actor)
	if !validSessionID(id) {
		return errors.New("invalid Bruno import session id")
	}
	if projectID == "" || actor == "" {
		return errors.New("Bruno import session project and actor are required")
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	found, err := s.validateScopedSessionDescriptorsLocked(id, projectID, actor, scope.Profile)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return s.deleteSessionLocked(ctx, id, projectID)
}

func (s *EncryptedStore) validateScopedSessionDescriptorsLocked(
	id, projectID, actor, profile string,
) (bool, error) {
	directory := filepath.Join(s.root, ".sessions")
	found := false
	for _, path := range []string{
		filepath.Join(directory, id+".claim.json"),
		filepath.Join(directory, id+".json"),
	} {
		_, descriptor, err := readSessionDescriptorFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		found = true
		if descriptor.ID != id ||
			strings.TrimSpace(descriptor.ProjectID) != projectID ||
			strings.TrimSpace(descriptor.Actor) != actor ||
			descriptor.EffectiveProfile() != effectiveSessionProfile(profile) {
			return false, os.ErrNotExist
		}
	}
	return found, nil
}

func (s *EncryptedStore) deleteSessionLocked(ctx context.Context, id, projectID string) error {
	directory := filepath.Join(s.root, ".sessions")
	paths := []string{
		filepath.Join(directory, id+".json"),
		filepath.Join(directory, id+".claim.json"),
	}
	found := false
	var cleanupErrors []error
	for _, target := range paths {
		_, descriptor, err := readSessionDescriptorFile(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		found = true
		if strings.TrimSpace(descriptor.ProjectID) != strings.TrimSpace(projectID) {
			cleanupErrors = append(cleanupErrors, errors.New("Bruno import session belongs to another project"))
			continue
		}
		referenced, referenceErr := s.sessionPayloadReferenced(descriptor.BackendKey, target)
		if referenceErr != nil {
			cleanupErrors = append(cleanupErrors, referenceErr)
			continue
		}
		if !referenced {
			if deleteErr := s.Delete(ctx, OpenRequest{
				BackendKey: descriptor.BackendKey,
				ProjectID:  descriptor.ProjectID,
				Kind:       "session",
				SHA256:     descriptor.SHA256,
				SizeBytes:  descriptor.SizeBytes,
			}); deleteErr != nil {
				cleanupErrors = append(cleanupErrors, deleteErr)
				continue
			}
		}
		if err := s.removeDurably(target); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		if releaseErr := s.releaseSessionCapacity(descriptor); releaseErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("release Bruno import session capacity: %w", releaseErr))
		}
	}
	if !found && len(cleanupErrors) == 0 {
		return nil
	}
	return errors.Join(cleanupErrors...)
}

// DeleteProject removes every encrypted Bruno object and persisted import
// session belonging to projectID. It validates each object's full project
// digest before deletion because backend directories use a shortened digest.
func (s *EncryptedStore) DeleteProject(ctx context.Context, projectID string) error {
	if s.readOnly {
		return errors.New("Bruno artifact store is read-only")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return errors.New("Bruno artifact project is required")
	}
	if err := s.markProjectDeleted(projectID); err != nil {
		return err
	}
	// Project tombstoning stops new writes; serialize the sweep with claim
	// transitions so a concurrent release cannot recreate a session descriptor
	// after the project has been removed.
	unlockClaims, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlockClaims() }()
	// Serialize the sweep with temp creation as well. This makes every
	// .encrypted-* file under this project stale by construction.
	unlockEncryptedWrites, err := s.lockEncryptedObjectWrites()
	if err != nil {
		return err
	}
	defer func() { _ = unlockEncryptedWrites() }()
	var cleanupErrors []error
	sessionsDir := filepath.Join(s.root, ".sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("enumerate Bruno import sessions: %w", err))
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			break
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(sessionsDir, entry.Name())
		_, descriptor, readErr := readSessionDescriptorFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			cleanupErrors = append(cleanupErrors, fmt.Errorf("read Bruno import session %q: %w", entry.Name(), readErr))
			continue
		}
		if strings.TrimSpace(descriptor.ProjectID) != projectID {
			continue
		}
		if removeErr := s.removeDurably(path); removeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Bruno import session %q: %w", descriptor.ID, removeErr))
			continue
		}
		if deleteErr := s.Delete(ctx, OpenRequest{
			BackendKey: descriptor.BackendKey,
			ProjectID:  descriptor.ProjectID,
			Kind:       "session",
			SHA256:     descriptor.SHA256,
			SizeBytes:  descriptor.SizeBytes,
		}); deleteErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Bruno import session payload %q: %w", descriptor.ID, deleteErr))
		}
		if releaseErr := s.releaseSessionCapacity(descriptor); releaseErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("release Bruno import session capacity %q: %w", descriptor.ID, releaseErr))
		}
	}
	if releaseErr := s.releaseProjectSessionCapacity(projectID); releaseErr != nil {
		cleanupErrors = append(cleanupErrors, releaseErr)
	}
	publicationsDir := filepath.Join(s.root, ".publications")
	publications, publicationErr := os.ReadDir(publicationsDir)
	if publicationErr != nil && !errors.Is(publicationErr, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("enumerate Bruno publications: %w", publicationErr))
	}
	for _, entry := range publications {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(publicationsDir, entry.Name())
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("read Bruno publication %q: %w", entry.Name(), readErr))
			continue
		}
		var descriptor PublicationDescriptor
		if decodeErr := json.Unmarshal(data, &descriptor); decodeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("decode Bruno publication %q: %w", entry.Name(), decodeErr))
			continue
		}
		if strings.TrimSpace(descriptor.ProjectID) != projectID {
			continue
		}
		if removeErr := s.removeDurably(path); removeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Bruno publication %q: %w", descriptor.ID, removeErr))
		}
	}

	digest := projectDigest(projectID)
	projectRoot := filepath.Join(s.root, "bruno", digest[:16])
	walkErr := filepath.WalkDir(projectRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			cleanupErrors = append(cleanupErrors, fmt.Errorf("visit Bruno artifact path %q: %w", path, walkErr))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".encrypted-") {
			if removeErr := s.removeDurably(path); removeErr != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("delete stale Bruno encrypted temporary %q: %w", path, removeErr))
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("Bruno artifact path %q is a symbolic link", path))
			return nil
		}
		header, headerErr := s.readExistingHeader(path)
		if headerErr != nil {
			if errors.Is(headerErr, os.ErrNotExist) {
				return nil
			}
			cleanupErrors = append(cleanupErrors, fmt.Errorf("read Bruno artifact header %q: %w", path, headerErr))
			return nil
		}
		if header.ProjectDigest != digest {
			return nil
		}
		if removeErr := s.removeDurably(path); removeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Bruno artifact %q: %w", path, removeErr))
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Bruno project artifacts: %w", walkErr))
	}
	s.removeEmptyTreeDurably(projectRoot, filepath.Join(s.root, "bruno"))
	_ = s.removeDurably(filepath.Join(sessionsDir, ".capacity"))
	_ = s.removeDurably(sessionsDir)
	return errors.Join(cleanupErrors...)
}

func (s *EncryptedStore) releaseProjectSessionCapacity(projectID string) error {
	directory := filepath.Join(s.root, ".sessions", ".capacity")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enumerate Bruno import session capacity: %w", err)
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "slot-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			cleanupErrors = append(cleanupErrors, readErr)
			continue
		}
		var reservation sessionCapacityReservation
		if unmarshalErr := json.Unmarshal(data, &reservation); unmarshalErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("read Bruno import session capacity %q: %w", entry.Name(), unmarshalErr))
			continue
		}
		if strings.TrimSpace(reservation.ProjectID) != projectID {
			continue
		}
		if removeErr := s.removeDurably(path); removeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Bruno import session capacity %q: %w", entry.Name(), removeErr))
		}
	}
	return errors.Join(cleanupErrors...)
}

// DeleteExpiredSessions removes normal descriptors whose durable expiry has
// passed and recovers expired claim descriptors only after their heartbeat
// lease is stale. Fresh in-flight claims are left alone. RelatedObjects are
// returned so the API can apply
// its database reference check before deleting uncommitted source artifacts.
func (s *EncryptedStore) DeleteExpiredSessions(ctx context.Context, now time.Time) ([]SessionDescriptor, error) {
	if s.readOnly {
		return nil, errors.New("Bruno artifact store is read-only")
	}
	unlockClaims, err := s.lockSessionClaims()
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlockClaims() }()
	capacityErr := s.pruneExpiredSessionCapacityReservations(now.UTC())
	entries, err := os.ReadDir(filepath.Join(s.root, ".sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, capacityErr
	}
	if err != nil {
		return nil, errors.Join(capacityErr, err)
	}
	expired := make([]SessionDescriptor, 0)
	var cleanupErrors []error
	if capacityErr != nil {
		cleanupErrors = append(cleanupErrors, capacityErr)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return expired, errors.Join(append(cleanupErrors, err)...)
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.root, ".sessions", entry.Name())
		_, descriptor, readErr := readSessionDescriptorFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("read Bruno import session %q: %w", entry.Name(), readErr))
			continue
		}
		if descriptor.ExpirationDisabled {
			if !strings.HasSuffix(entry.Name(), ".claim.json") {
				continue
			}
			// Non-expiring sessions still need abandoned claim recovery. Without
			// this path, a controller crash after Claim would strand the draft
			// forever because its durable expiry intentionally never arrives.
			recoverErr := s.recoverStaleSessionClaimLocked(
				descriptor.ID, descriptor.ProjectID, descriptor.Actor, now.UTC(),
			)
			if errors.Is(recoverErr, ErrSessionClaimed) || errors.Is(recoverErr, os.ErrNotExist) {
				continue
			}
			if recoverErr != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("recover non-expiring Bruno import session claim %q: %w", descriptor.ID, recoverErr))
			}
			continue
		}
		if descriptor.ExpiresAt.After(now) {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".claim.json") {
			sessionID := descriptor.ID
			recoverErr := s.recoverStaleSessionClaimLocked(
				descriptor.ID, descriptor.ProjectID, descriptor.Actor, now.UTC(),
			)
			if errors.Is(recoverErr, ErrSessionClaimed) || errors.Is(recoverErr, os.ErrNotExist) {
				continue
			}
			if recoverErr != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("recover expired Bruno import session claim %q: %w", descriptor.ID, recoverErr))
				continue
			}
			path = filepath.Join(s.root, ".sessions", descriptor.ID+".json")
			_, descriptor, readErr = readSessionDescriptorFile(path)
			if readErr != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("read recovered Bruno import session %q: %w", sessionID, readErr))
				continue
			}
		}
		referenced, referenceErr := s.sessionPayloadReferenced(descriptor.BackendKey, path)
		if referenceErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("check expired Bruno import session payload %q: %w", descriptor.ID, referenceErr))
			continue
		}
		if !referenced {
			if deleteErr := s.Delete(ctx, OpenRequest{
				BackendKey: descriptor.BackendKey,
				ProjectID:  descriptor.ProjectID,
				Kind:       "session",
				SHA256:     descriptor.SHA256,
				SizeBytes:  descriptor.SizeBytes,
			}); deleteErr != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("delete expired Bruno import session payload %q: %w", descriptor.ID, deleteErr))
				continue
			}
		}
		expired = append(expired, descriptor)
	}
	return expired, errors.Join(cleanupErrors...)
}

func (s *EncryptedStore) IsSessionRelatedObjectReferenced(backendKey, excludedSessionID string) (bool, error) {
	backendKey = strings.TrimSpace(backendKey)
	if backendKey == "" {
		return false, nil
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()
	return s.isSessionRelatedObjectReferencedLocked(backendKey, excludedSessionID)
}

// isSessionRelatedObjectReferencedLocked exists for code that already owns the
// claim-transition lock. The exported scanner acquires that lock exactly once,
// preventing a descriptor rename from falling between its snapshot and reads.
func (s *EncryptedStore) isSessionRelatedObjectReferencedLocked(backendKey, excludedSessionID string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, ".sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		_, descriptor, readErr := readSessionDescriptorFile(filepath.Join(s.root, ".sessions", entry.Name()))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return false, readErr
		}
		if descriptor.ID == excludedSessionID {
			continue
		}
		for _, request := range descriptor.RelatedObjects {
			if strings.TrimSpace(request.BackendKey) == backendKey {
				return true, nil
			}
		}
	}
	return false, nil
}

func readSessionDescriptorFile(path string) ([]byte, SessionDescriptor, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	if !info.Mode().IsRegular() {
		return nil, SessionDescriptor{}, errors.New("Bruno import session descriptor is not a regular file")
	}
	if info.Size() < 0 || info.Size() > maxHeaderBytes {
		return nil, SessionDescriptor{}, errors.New("Bruno import session descriptor exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxHeaderBytes+1))
	if err != nil {
		return nil, SessionDescriptor{}, err
	}
	if len(data) > maxHeaderBytes {
		return nil, SessionDescriptor{}, errors.New("Bruno import session descriptor exceeds size limit")
	}
	var descriptor SessionDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, SessionDescriptor{}, err
	}
	return data, descriptor, nil
}

func newSessionClaimToken() (string, error) {
	token := make([]byte, 32)
	defer clear(token)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return "", fmt.Errorf("generate Bruno import session claim token: %w", err)
	}
	return hex.EncodeToString(token), nil
}

func validateSessionClaimDescriptor(descriptor SessionDescriptor, id, projectID, actor, claimToken string) error {
	if descriptor.ID != strings.TrimSpace(id) ||
		strings.TrimSpace(descriptor.ProjectID) != strings.TrimSpace(projectID) ||
		strings.TrimSpace(descriptor.Actor) != strings.TrimSpace(actor) {
		return os.ErrNotExist
	}
	expected := strings.TrimSpace(descriptor.ClaimToken)
	actual := strings.TrimSpace(claimToken)
	if expected == "" || actual == "" || len(expected) != len(actual) ||
		subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		return ErrSessionClaimLost
	}
	return nil
}

func validSessionID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

func effectiveSessionProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return importsession.LegacyBrunoProfile
	}
	return profile
}

func (s *EncryptedStore) replaceSessionDescriptorFile(path string, descriptor SessionDescriptor) error {
	data, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".session-descriptor-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return s.renameDurably(tempPath, path)
}

func (s *EncryptedStore) sessionPayloadReferenced(backendKey, excludedPath string) (bool, error) {
	backendKey = strings.TrimSpace(backendKey)
	if backendKey == "" {
		return false, nil
	}
	entries, err := os.ReadDir(filepath.Join(s.root, ".sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	excludedPath = filepath.Clean(excludedPath)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.root, ".sessions", entry.Name())
		if excludedPath != "." && filepath.Clean(path) == excludedPath {
			continue
		}
		_, descriptor, readErr := readSessionDescriptorFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return false, readErr
		}
		if strings.TrimSpace(descriptor.BackendKey) == backendKey {
			return true, nil
		}
	}
	return false, nil
}

func (s *EncryptedStore) projectDeleted(projectID string) (bool, error) {
	_, err := os.Stat(s.projectTombstonePath(projectID))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (s *EncryptedStore) markProjectDeleted(projectID string) error {
	directory := filepath.Join(s.root, ".project-tombstones")
	if err := mkdirAllDurably(directory, 0o700); err != nil {
		return err
	}
	path := s.projectTombstonePath(projectID)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// A previous attempt may have created the entry but failed while syncing
		// its directory. Retrying the tombstone operation completes durability.
		return s.syncDirectoryChain(directory)
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString(time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = file.Close()
		_ = s.removeDurably(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = s.removeDurably(path)
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return s.syncDirectoryChain(directory)
}

func (s *EncryptedStore) projectTombstonePath(projectID string) string {
	return filepath.Join(s.root, ".project-tombstones", projectDigest(strings.TrimSpace(projectID))+".deleted")
}

func (s *EncryptedStore) rejectObjectWrittenAfterProjectDeletion(ctx context.Context, projectID string, request OpenRequest) error {
	deleted, err := s.projectDeleted(projectID)
	if err != nil {
		return err
	}
	if !deleted {
		return nil
	}
	if deleteErr := s.Delete(ctx, request); deleteErr != nil {
		return errors.Join(ErrProjectDeleted, deleteErr)
	}
	return ErrProjectDeleted
}

func (s *EncryptedStore) projectWriteError(projectID string, writeErr error) error {
	deleted, err := s.projectDeleted(projectID)
	if err != nil {
		return errors.Join(writeErr, err)
	}
	if deleted {
		return errors.Join(ErrProjectDeleted, writeErr)
	}
	return writeErr
}

func (s *EncryptedStore) removeEmptyTreeDurably(path, stop string) {
	path = filepath.Clean(path)
	stop = filepath.Clean(stop)
	for path != stop && strings.HasPrefix(path, stop+string(filepath.Separator)) {
		if err := s.removeDurably(path); err != nil {
			return
		}
		path = filepath.Dir(path)
	}
}

// Older releases staged plaintext before encrypting content-addressed objects.
// A crash could leave plain-* files behind, so writable store initialization
// removes those legacy artifacts before accepting any new work.
func pruneLegacyPlaintextStaging(root string) error {
	directory := filepath.Join(root, ".staging")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy Bruno plaintext staging: %w", err)
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "plain-") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		// RemoveAll does not follow a top-level symlink and also handles an
		// interrupted implementation that accidentally left a directory.
		if removeErr := os.RemoveAll(path); removeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove legacy Bruno plaintext staging %q: %w", entry.Name(), removeErr))
		}
	}
	_ = os.Remove(directory)
	return errors.Join(cleanupErrors...)
}

// pruneStaleEncryptedTemps runs only while the encrypted-object writer lock is
// held. Consequently every matching file is from a terminated writer; active
// writers cannot create or retain a temporary concurrently with this sweep.
func (s *EncryptedStore) pruneStaleEncryptedTemps() error {
	artifactRoot := filepath.Join(s.root, "bruno")
	var cleanupErrors []error
	walkErr := filepath.WalkDir(artifactRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".encrypted-") {
			return nil
		}
		if removeErr := s.removeDurably(path); removeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove stale Bruno encrypted temporary %q: %w", path, removeErr))
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect stale Bruno encrypted temporaries: %w", walkErr))
	}
	return errors.Join(cleanupErrors...)
}

func (s *EncryptedStore) ReadAll(ctx context.Context, request OpenRequest, maxBytes int64) ([]byte, error) {
	reader, size, err := s.Open(ctx, request)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if maxBytes <= 0 || size > maxBytes {
		return nil, fmt.Errorf("Bruno artifact size %d exceeds read limit %d", size, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("Bruno artifact exceeds read limit %d", maxBytes)
	}
	return data, nil
}

func (s *EncryptedStore) verifyStoredObject(ctx context.Context, request OpenRequest) error {
	reader, _, err := s.Open(ctx, request)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, reader)
	closeErr := reader.Close()
	return errors.Join(copyErr, closeErr)
}

func writeEncryptedArtifact(ctx context.Context, destination io.Writer, source io.Reader, dek []byte, header artifactHeader) error {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(encodedHeader) > maxHeaderBytes {
		return errors.New("Bruno artifact encryption header is too large")
	}
	if _, err := io.WriteString(destination, artifactMagic); err != nil {
		return err
	}
	if err := binary.Write(destination, binary.BigEndian, uint32(len(encodedHeader))); err != nil {
		return err
	}
	if _, err := destination.Write(encodedHeader); err != nil {
		return err
	}
	buffer := make([]byte, header.ChunkSize)
	var counter uint64
	var written int64
	for written < header.SizeBytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := int64(len(buffer))
		if remaining := header.SizeBytes - written; remaining < want {
			want = remaining
		}
		n, err := io.ReadFull(source, buffer[:want])
		if err != nil {
			return err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return err
		}
		sealed := aead.Seal(nil, nonce, buffer[:n], artifactChunkAAD(header, counter))
		payloadLength := len(nonce) + len(sealed)
		if err := binary.Write(destination, binary.BigEndian, uint32(payloadLength)); err != nil {
			return err
		}
		if _, err := destination.Write(nonce); err != nil {
			return err
		}
		if _, err := destination.Write(sealed); err != nil {
			return err
		}
		written += int64(n)
		counter++
	}
	return nil
}

func decryptArtifact(ctx context.Context, reader *bufio.Reader, destination io.Writer, aead cipher.AEAD, header artifactHeader) error {
	digest := sha256.New()
	writer := io.MultiWriter(destination, digest)
	var counter uint64
	var written int64
	for written < header.SizeBytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		var payloadLength uint32
		if err := binary.Read(reader, binary.BigEndian, &payloadLength); err != nil {
			return fmt.Errorf("read Bruno artifact chunk length: %w", err)
		}
		maxLength := header.ChunkSize + aead.NonceSize() + aead.Overhead()
		if payloadLength < uint32(aead.NonceSize()+aead.Overhead()) || payloadLength > uint32(maxLength) {
			return errors.New("Bruno artifact chunk length is invalid")
		}
		payload := make([]byte, payloadLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return fmt.Errorf("read Bruno artifact chunk: %w", err)
		}
		nonce := payload[:aead.NonceSize()]
		plaintext, err := aead.Open(nil, nonce, payload[aead.NonceSize():], artifactChunkAAD(header, counter))
		if err != nil {
			return fmt.Errorf("decrypt Bruno artifact chunk %d: %w", counter, err)
		}
		if written+int64(len(plaintext)) > header.SizeBytes {
			return errors.New("Bruno artifact plaintext exceeds declared size")
		}
		if _, err := writer.Write(plaintext); err != nil {
			return err
		}
		written += int64(len(plaintext))
		counter++
	}
	if written != header.SizeBytes {
		return errors.New("Bruno artifact plaintext size mismatch")
	}
	if actual := hex.EncodeToString(digest.Sum(nil)); actual != header.SHA256 {
		return fmt.Errorf("Bruno artifact plaintext digest mismatch: got %s", actual)
	}
	if trailing, err := reader.ReadByte(); err == nil || !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("Bruno artifact has trailing byte 0x%02x", trailing)
		}
		return err
	}
	return nil
}

func readArtifactHeader(source io.Reader) (artifactHeader, *bufio.Reader, error) {
	reader := bufio.NewReader(source)
	magic := make([]byte, len(artifactMagic))
	if _, err := io.ReadFull(reader, magic); err != nil {
		return artifactHeader{}, nil, err
	}
	if string(magic) != artifactMagic {
		return artifactHeader{}, nil, errors.New("unsupported Bruno artifact object format")
	}
	var headerLength uint32
	if err := binary.Read(reader, binary.BigEndian, &headerLength); err != nil {
		return artifactHeader{}, nil, err
	}
	if headerLength == 0 || headerLength > maxHeaderBytes {
		return artifactHeader{}, nil, errors.New("Bruno artifact header length is invalid")
	}
	encoded := make([]byte, headerLength)
	if _, err := io.ReadFull(reader, encoded); err != nil {
		return artifactHeader{}, nil, err
	}
	var header artifactHeader
	if err := json.Unmarshal(encoded, &header); err != nil {
		return artifactHeader{}, nil, err
	}
	return header, reader, nil
}

func (s *EncryptedStore) readExistingHeader(target string) (artifactHeader, error) {
	file, err := os.Open(target)
	if err != nil {
		return artifactHeader{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return artifactHeader{}, err
	}
	if !info.Mode().IsRegular() {
		return artifactHeader{}, errors.New("Bruno artifact backend object is not a regular file")
	}
	header, _, err := readArtifactHeader(file)
	return header, err
}

func validateArtifactHeader(header artifactHeader, request OpenRequest) error {
	if header.Version != artifactFormatVersion || header.Algorithm != "AES-256-GCM-CHUNKED" {
		return errors.New("unsupported Bruno artifact encryption format")
	}
	if header.ProjectDigest != projectDigest(strings.TrimSpace(request.ProjectID)) {
		return errors.New("Bruno artifact belongs to another project")
	}
	if kind := strings.ToLower(strings.TrimSpace(request.Kind)); kind != "" && header.Kind != kind {
		return errors.New("Bruno artifact kind mismatch")
	}
	if digest := strings.ToLower(strings.TrimSpace(request.SHA256)); digest != "" && header.SHA256 != digest {
		return errors.New("Bruno artifact digest metadata mismatch")
	}
	if request.SizeBytes >= 0 && request.SizeBytes != 0 && header.SizeBytes != request.SizeBytes {
		return errors.New("Bruno artifact size metadata mismatch")
	}
	if header.SizeBytes < 0 || header.ChunkSize < 64<<10 || header.ChunkSize > 16<<20 {
		return errors.New("Bruno artifact header contains invalid bounds")
	}
	return nil
}

func storedObjectFromHeader(backendKey string, header artifactHeader) *StoredObject {
	metadata := map[string]any{
		"format_version": header.Version,
		"algorithm":      header.Algorithm,
		"chunk_size":     header.ChunkSize,
	}
	for key, value := range header.WrappedKey.Metadata {
		metadata["wrap_"+key] = value
	}
	return &StoredObject{
		BackendKey:         backendKey,
		SHA256:             header.SHA256,
		SizeBytes:          header.SizeBytes,
		EncryptionMetadata: metadata,
	}
}

func (s *EncryptedStore) pathForBackendKey(backendKey string) (string, error) {
	backendKey = filepath.ToSlash(strings.TrimSpace(backendKey))
	if backendKey == "" || strings.HasPrefix(backendKey, "/") || strings.Contains(backendKey, "..") {
		return "", errors.New("invalid Bruno artifact backend key")
	}
	target := filepath.Join(s.root, filepath.FromSlash(backendKey))
	relative, err := filepath.Rel(s.root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("Bruno artifact backend key escapes the configured root")
	}
	return target, nil
}

func artifactBackendKey(projectID, kind, digest string) string {
	return filepath.ToSlash(filepath.Join("bruno", projectDigest(projectID)[:16], kind, digest[:2], digest+".abx"))
}

func projectDigest(projectID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(projectID)))
	return hex.EncodeToString(sum[:])
}

func artifactWrapAAD(projectID, kind, digest string) []byte {
	return []byte("apexload/bruno-artifact/v1\x00" + projectDigest(projectID) + "\x00" + kind + "\x00" + digest)
}

func artifactChunkAAD(header artifactHeader, counter uint64) []byte {
	value := fmt.Sprintf("apexload/bruno-artifact/chunk/v1\x00%s\x00%s\x00%s\x00%d\x00%d",
		header.ProjectDigest, header.Kind, header.SHA256, header.SizeBytes, counter)
	return []byte(value)
}

func validArtifactKind(kind string) bool {
	switch kind {
	case "raw", "sanitized", "dependency", "manifest", "session":
		return true
	default:
		return false
	}
}

func validArtifactSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func copyBounded(ctx context.Context, destination io.Writer, source io.Reader, maximum int64) (int64, error) {
	buffer := make([]byte, 256<<10)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		remaining := maximum - written
		if remaining < 0 {
			return written, fmt.Errorf("Bruno artifact exceeds %d bytes", maximum)
		}
		readBuffer := buffer
		if int64(len(readBuffer)) > remaining+1 {
			readBuffer = readBuffer[:remaining+1]
		}
		n, readErr := source.Read(readBuffer)
		if n > 0 {
			if written+int64(n) > maximum {
				return written, fmt.Errorf("Bruno artifact exceeds %d bytes", maximum)
			}
			if _, err := destination.Write(readBuffer[:n]); err != nil {
				return written, err
			}
			written += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
}

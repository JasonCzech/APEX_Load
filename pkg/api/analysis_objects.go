package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/google/uuid"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

var analysisObjectPattern = regexp.MustCompile(`^datasets/v1/[a-f0-9]{64}/[a-f0-9-]{36}/[a-z0-9][a-z0-9_.-]{0,159}$`)

func analysisObjectKey(project, id, name string) string {
	hash := sha256.Sum256([]byte(project))
	return "datasets/v1/" + hex.EncodeToString(hash[:]) + "/" + id + "/" + name
}

func analysisObjectWrite(ctx context.Context, key string, input io.Reader) (retDigest string, retErr error) {
	if !analysisObjectPattern.MatchString(key) {
		return "", errors.New("invalid analytical object")
	}
	owner, joined, err := analysisObjectIO(ctx)
	if err != nil {
		return "", err
	}
	defer joined()
	backend, err := nativeAnalysisArtifactBackend(ctx)
	if err != nil {
		return "", err
	}
	if err := backend.validate(); err != nil {
		return "", err
	}
	hash := sha256.New()
	input = io.TeeReader(input, hash)
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return "", err
		}
		owner.TrackOwner(bucket)
		defer func() { retErr = errors.Join(retErr, owner.CloseOwner(bucket)) }()
		writer, err := bucket.NewWriter(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)), nil)
		if err != nil {
			return "", err
		}
		owner.TrackOwner(writer)
		_, copyErr := io.Copy(writer, input)
		closeErr := owner.CloseOwner(writer)
		if err = errors.Join(copyErr, closeErr); err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	rootPath := filepath.Join(backend.LocalRoot, "_analysis")
	if err := os.MkdirAll(rootPath, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", err
	}
	owner.TrackOwner(root)
	defer func() { retErr = errors.Join(retErr, owner.CloseOwner(root)) }()
	if err = root.MkdirAll(filepath.Dir(key), 0700); err != nil {
		return "", err
	}
	tmp := analysisUploadTemporaryKey(ctx, key)
	if tmp == "" {
		tmp = key + "." + uuid.NewString() + ".tmp"
	}
	file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	owner.TrackOwner(file)
	tempClosed := false
	defer func() {
		if tempClosed {
			_ = root.Remove(tmp)
		}
	}()
	_, copyErr := io.Copy(file, &contextReader{ctx: ctx, reader: input})
	syncErr := file.Sync()
	closeErr := owner.CloseOwner(file)
	tempClosed = closeErr == nil
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = root.Rename(tmp, key); err != nil {
		return "", err
	}
	dir, err := root.Open(filepath.Dir(key))
	if err != nil {
		return "", err
	}
	owner.TrackOwner(dir)
	syncErr = syncArchiveDirectoryHandle(dir)
	closeErr = owner.CloseOwner(dir)
	if isIgnorableArchiveDirectorySyncError(syncErr) {
		syncErr = nil
	}
	if err = errors.Join(syncErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func analysisObjectRead(ctx context.Context, key string, output io.Writer, maxBytes int64) (retDigest string, retErr error) {
	if !analysisObjectPattern.MatchString(key) || maxBytes < 0 {
		return "", errors.New("invalid analytical object read")
	}
	owner, joined, err := analysisObjectIO(ctx)
	if err != nil {
		return "", err
	}
	defer joined()
	backend, err := nativeAnalysisArtifactBackend(ctx)
	if err != nil {
		return "", err
	}
	var reader io.ReadCloser
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return "", err
		}
		owner.TrackOwner(bucket)
		defer func() { retErr = errors.Join(retErr, owner.CloseOwner(bucket)) }()
		reader, err = bucket.NewReader(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)), nil)
		if err != nil {
			return "", err
		}
	} else {
		root, err := os.OpenRoot(filepath.Join(backend.LocalRoot, "_analysis"))
		if err != nil {
			return "", err
		}
		owner.TrackOwner(root)
		defer func() { retErr = errors.Join(retErr, owner.CloseOwner(root)) }()
		reader, err = root.Open(key)
		if err != nil {
			return "", err
		}
	}
	owner.TrackOwner(reader)
	defer func() { retErr = errors.Join(retErr, owner.CloseOwner(reader)) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(&contextReader{ctx: ctx, reader: reader}, maxBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxBytes {
		return "", errors.New("analytical object exceeds declared size")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func analysisObjectDelete(ctx context.Context, key string) error {
	if !analysisObjectPattern.MatchString(key) {
		return errors.New("invalid analytical object deletion")
	}
	return deleteAnalysisObjectPhysical(ctx, currentArchiveBackend(), key)
}

func analysisCloseNativeOwner(lease *analytics.NativeIOLease, descriptor io.Closer) error {
	if lease == nil {
		return descriptor.Close()
	}
	return lease.CloseOwner(descriptor)
}

// Shared helper ownership selects one opaque admitted parent before Open.
// A standalone non-native read preserves the historical synchronous contract.
type analysisObjectPhysicalIO interface {
	TrackOwner(io.Closer)
	CloseOwner(io.Closer) error
}
type analysisPlainObjectIO struct{}

func (analysisPlainObjectIO) TrackOwner(io.Closer)         {}
func (analysisPlainObjectIO) CloseOwner(c io.Closer) error { return c.Close() }
func analysisObjectIO(ctx context.Context) (analysisObjectPhysicalIO, func(), error) {
	lease, err := analytics.NativePipelineIO(ctx)
	if err != nil {
		return nil, nil, err
	}
	upload, _ := ctx.Value(analysisUploadOwnerKey{}).(*analysisUploadPhysicalOwner)
	if upload != nil {
		if err := analysisUploadContextError(ctx); err != nil {
			return nil, nil, err
		}
		if lease != nil {
			return nil, nil, errAnalysisUploadPhysical
		}
		end, err := upload.beginOperation()
		return upload, end, err
	}
	if lease != nil {
		return lease, lease.Joined, nil
	}
	return analysisPlainObjectIO{}, func() {}, nil
}
func analysisUploadTemporaryKey(ctx context.Context, key string) string {
	owner, _ := ctx.Value(analysisUploadOwnerKey{}).(*analysisUploadPhysicalOwner)
	if owner == nil {
		return ""
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.intent == nil || owner.intent.ObjectKey != key {
		return ""
	}
	return owner.intent.TemporaryKey
}

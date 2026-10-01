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
	"gocloud.dev/gcerrors"
)

var analysisObjectPattern = regexp.MustCompile(`^datasets/v1/[a-f0-9]{64}/[a-f0-9-]{36}/[a-z0-9][a-z0-9_.-]{0,159}$`)

func analysisObjectKey(project, id, name string) string {
	hash := sha256.Sum256([]byte(project))
	return "datasets/v1/" + hex.EncodeToString(hash[:]) + "/" + id + "/" + name
}

func analysisObjectWrite(ctx context.Context, key string, input io.Reader) (string, error) {
	if !analysisObjectPattern.MatchString(key) {
		return "", errors.New("invalid analytical object")
	}
	backend := currentArchiveBackend()
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
		defer bucket.Close()
		writer, err := bucket.NewWriter(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)), nil)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := writer.Close()
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
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(key), 0700); err != nil {
		return "", err
	}
	tmp := key + "." + uuid.NewString() + ".tmp"
	file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(tmp)
	_, copyErr := io.Copy(file, &contextReader{ctx: ctx, reader: input})
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", err
	}
	if err = root.Rename(tmp, key); err != nil {
		return "", err
	}
	dir, err := root.Open(filepath.Dir(key))
	if err != nil {
		return "", err
	}
	syncErr = dir.Sync()
	closeErr = dir.Close()
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

func analysisObjectRead(ctx context.Context, key string, output io.Writer, maxBytes int64) (string, error) {
	if !analysisObjectPattern.MatchString(key) || maxBytes < 0 {
		return "", errors.New("invalid analytical object read")
	}
	backend := currentArchiveBackend()
	var reader io.ReadCloser
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return "", err
		}
		defer bucket.Close()
		reader, err = bucket.NewReader(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)), nil)
		if err != nil {
			return "", err
		}
	} else {
		root, err := os.OpenRoot(filepath.Join(backend.LocalRoot, "_analysis"))
		if err != nil {
			return "", err
		}
		defer root.Close()
		reader, err = root.Open(key)
		if err != nil {
			return "", err
		}
	}
	defer reader.Close()
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
	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return err
		}
		defer bucket.Close()
		err = bucket.Delete(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)))
		if gcerrors.Code(err) == gcerrors.NotFound {
			return nil
		}
		return err
	}
	root, err := os.OpenRoot(filepath.Join(backend.LocalRoot, "_analysis"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(key)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

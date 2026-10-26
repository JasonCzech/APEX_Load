package api

import (
	"context"
	"errors"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"os"
	"path/filepath"
)

func analysisObjectSize(ctx context.Context, key string, limit int64) (retSize int64, retErr error) {
	if !analysisObjectPattern.MatchString(key) || limit < 12 {
		return 0, errors.New("invalid analytical object size request")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	lease, err := analytics.NativePipelineIO(ctx)
	if err != nil {
		return 0, err
	}
	if lease == nil {
		return 0, errors.New("native metadata admission unavailable")
	}
	defer lease.Joined()
	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return 0, err
		}
		lease.TrackOwner(bucket)
		defer func() { retErr = errors.Join(retErr, lease.CloseOwner(bucket)) }()
		attributes, err := bucket.Attributes(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)))
		if err != nil {
			return 0, err
		}
		if attributes.Size < 12 || attributes.Size > limit {
			return 0, errors.New("analytical object exceeds declared geometry")
		}
		return attributes.Size, nil
	}
	root, err := os.OpenRoot(analysisArtifactDirectory(backend))
	if err != nil {
		return 0, err
	}
	lease.TrackOwner(root)
	defer func() { retErr = errors.Join(retErr, lease.CloseOwner(root)) }()
	info, err := root.Stat(key)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() < 12 || info.Size() > limit {
		return 0, errors.New("analytical object exceeds declared geometry")
	}
	return info.Size(), nil
}

package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
)

func (s *Server) retryAnalysisCleanup(ctx context.Context) error {
	store, ok := s.store.(storage.AnalysisCleanupStore)
	if !ok {
		return nil
	}
	jobs, err := store.ListAnalysisCleanupJobs(time.Now().UTC(), 50)
	if err != nil {
		return err
	}
	if len(jobs) > 0 {
		if err := ensureAnalysisBackend(s.store); err != nil {
			return err
		}
	}
	for _, j := range jobs {
		if err := deleteAnalysisNamespace(ctx, j); err != nil {
			return err
		}
		if j.Kind == "dataset" {
			if imports, ok := s.store.(storage.AnalysisImportStore); ok {
				in, err := imports.GetAnalysisImport(j.ResourceID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err == nil && in.State == "deleting" {
					in.State = "deleted"
					if _, err = imports.SaveAnalysisImport(*in, in.Revision); err != nil {
						return err
					}
				}
			}
		}
		if err := store.CompleteAnalysisCleanupJob(j); err != nil {
			return err
		}
	}
	return nil
}

func deleteAnalysisNamespace(ctx context.Context, j storage.AnalysisCleanupJob) error {
	var prefix string
	switch j.Kind {
	case "dataset":
		key := analysisObjectKey(j.ProjectID, j.ResourceID, "validation")
		if !analysisObjectPattern.MatchString(key) {
			return errors.New("invalid dataset cleanup namespace")
		}
		prefix = filepath.Dir(key)
	case "monitor":
		key, _ := monitorArtifactKey(j.ProjectID, j.ResourceID, nil)
		prefix = filepath.Dir(key)
	default:
		return errors.New("invalid analytical cleanup kind")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backend := currentArchiveBackend()
	if backend.Backend == archiveBackendFilesystem {
		root, err := os.OpenRoot(filepath.Join(backend.LocalRoot, "_analysis"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer root.Close()
		return root.RemoveAll(prefix)
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	prefix = filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", prefix)) + "/"
	iterator := bucket.List(&blob.ListOptions{Prefix: prefix})
	for i := 0; i < 40000; i++ {
		item, err := iterator.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if item.IsDir {
			continue
		}
		if err = bucket.Delete(ctx, item.Key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
			return err
		}
	}
	return errors.New("analytical cleanup will continue on next sweep")
}

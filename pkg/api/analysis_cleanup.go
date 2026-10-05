package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
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
		err := s.retryAnalysisCleanupJob(ctx, store, j)
		if errors.Is(err, errAnalysisCleanupPhysical) {
			if err = deferBlockedAnalysisCleanup(s.store, j); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Logical cleanup jobs retire only after the exact deletion operation and
// every parent descriptor supply actual nil closure proof. An unjoined owner
// remains queued and is moved out of the bounded first page for fair retries.
func (s *Server) retryAnalysisCleanupJob(ctx context.Context, store storage.AnalysisCleanupStore, j storage.AnalysisCleanupJob) error {
	if j.Kind == "monitor-object" {
		management, ok := s.store.(storage.ManagementMonitorStore)
		if !ok {
			return errors.New("management object reference storage unavailable")
		}
		if len(j.ResourceID) > 4096 {
			return errors.New("invalid monitor object cleanup reference")
		}
		var reference storage.MonitorObjectCleanupReference
		if json.Unmarshal([]byte(j.ResourceID), &reference) != nil || !validMonitorArtifactKey(reference.ArtifactKey) {
			return errors.New("invalid monitor object cleanup reference")
		}
		owner, _ := monitorArtifactKey(j.ProjectID, reference.RunID, nil)
		if reference.RunID == "" || filepath.Dir(owner) != filepath.Dir(reference.ArtifactKey) {
			return errors.New("monitor object cleanup ownership mismatch")
		}
		referenced, err := management.IsMonitorArtifactReferenced(j.ProjectID, reference.ArtifactKey)
		if err != nil {
			return err
		}
		if !referenced {
			if err = deleteMonitorObject(ctx, reference.ArtifactKey); err != nil {
				return err
			}
		}
		if err = store.CompleteAnalysisCleanupJob(j); err != nil {
			return err
		}
		return nil
	}
	if j.Kind == "upload-writer" {
		writers, ok := s.store.(storage.AnalysisUploadWriterStore)
		if !ok {
			return errors.New("upload writer cleanup proof unavailable")
		}
		plan, err := writers.AnalysisUploadWriterCleanupPlan(ctx, j)
		if err != nil {
			return err
		}
		if plan.Blocked {
			return deferBlockedAnalysisCleanup(s.store, j)
		}
		for _, key := range plan.Keys {
			if err = analysisObjectDelete(ctx, key); err != nil {
				return err
			}
		}
		return store.CompleteAnalysisCleanupJob(j)
	}
	if j.Kind == "attempt" {
		attempts, ok := s.store.(interface {
			AnalysisAttemptCleanupPlan(context.Context, storage.AnalysisCleanupJob) (storage.AnalysisAttemptCleanupPlan, error)
		})
		if !ok {
			return errors.New("analytical attempt cleanup proof unavailable")
		}
		plan, err := attempts.AnalysisAttemptCleanupPlan(ctx, j)
		if err != nil {
			return err
		}
		for _, key := range plan.Keys {
			if err = analysisObjectDelete(ctx, key); err != nil {
				return err
			}
		}
		if plan.Blocked {
			if err = deferBlockedAnalysisCleanup(s.store, j); err != nil {
				return err
			}
			return nil
		}
		if err = store.CompleteAnalysisCleanupJob(j); err != nil {
			return err
		}
		return nil
	}
	if j.Kind == "dataset" {
		attempts, ok := s.store.(interface {
			AnalysisDatasetCleanupReady(context.Context, string, string) (bool, error)
		})
		if !ok {
			return errors.New("dataset physical writer cleanup proof unavailable")
		}
		ready, err := attempts.AnalysisDatasetCleanupReady(ctx, j.ProjectID, j.ResourceID)
		if err != nil {
			return err
		}
		if !ready {
			if err = deferBlockedAnalysisCleanup(s.store, j); err != nil {
				return err
			}
			return nil
		}
	}
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
		if !validMonitorArtifactKey(key) {
			return errors.New("invalid monitor cleanup namespace")
		}
		prefix = filepath.Dir(key)
	default:
		return errors.New("invalid analytical cleanup kind")
	}
	return deleteAnalysisNamespacePhysical(ctx, currentArchiveBackend(), prefix)
}

// A single orphan is reclaimed after the durable writer grace. A receipt
// reference check above protects all accepted objects in still-live runs.
func deleteMonitorObject(ctx context.Context, key string) error {
	if !validMonitorArtifactKey(key) || strings.Contains(key, "\\") {
		return errors.New("invalid monitor cleanup key")
	}
	return deleteAnalysisObjectPhysical(ctx, currentArchiveBackend(), key)
}

func deferBlockedAnalysisCleanup(store any, j storage.AnalysisCleanupJob) error {
	deferred, ok := store.(interface {
		DeferAnalysisCleanupJob(storage.AnalysisCleanupJob, time.Time) error
	})
	if !ok {
		return errors.New("analysis cleanup bounded retry unavailable")
	}
	return deferred.DeferAnalysisCleanupJob(j, time.Now().UTC().Add(time.Minute))
}

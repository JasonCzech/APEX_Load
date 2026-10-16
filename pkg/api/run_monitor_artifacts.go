package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Analytical objects use the configured archive backend and an isolated,
// content-addressed namespace. Database receipts are published only after the
// object writer closes successfully (and local writes have been fsynced).
func monitorArtifactKey(project, run string, payload []byte) (string, string) {
	owner := sha256.Sum256([]byte(project + "\x00" + run))
	digest := sha256.Sum256(payload)
	d := hex.EncodeToString(digest[:])
	return "monitor-evidence/v1/" + hex.EncodeToString(owner[:]) + "/" + d + ".json", d
}

func validMonitorArtifactKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "monitor-evidence" || parts[1] != "v1" || !strings.HasSuffix(parts[3], ".json") {
		return false
	}
	for _, p := range []string{parts[2], strings.TrimSuffix(parts[3], ".json")} {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 32 || strings.ToLower(p) != p {
			return false
		}
	}
	return true
}

func writeMonitorArtifact(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validMonitorArtifactKey(key) || len(data) > 1<<20 {
		return errors.New("invalid monitor artifact")
	}
	backend := currentArchiveBackend()
	if err := backend.validate(); err != nil {
		return err
	}
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return err
		}
		defer bucket.Close()
		return bucket.WriteAll(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)), data, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rootPath := filepath.Join(backend.LocalRoot, "_analysis")
	if err := os.MkdirAll(rootPath, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(key), 0700); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	temp := key + "." + uuid.NewString() + ".tmp"
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	syncErr := ctx.Err()
	if writeErr == nil && syncErr == nil {
		syncErr = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	// Filesystem calls cannot be interrupted by a context. Recheck after the
	// actual write/sync/close return so an expired writer never publishes its
	// temporary object after deletion cleanup has completed.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = root.Rename(temp, key); err != nil {
		return err
	}
	dir, err := root.Open(filepath.Dir(key))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := syncArchiveDirectoryHandle(dir); !isIgnorableArchiveDirectorySyncError(err) {
		return err
	}
	return nil
}

func readMonitorArtifact(ctx context.Context, key, digest string) ([]byte, error) {
	if !validMonitorArtifactKey(key) {
		return nil, errors.New("invalid monitor artifact key")
	}
	backend := currentArchiveBackend()
	var reader io.ReadCloser
	if backend.Backend != archiveBackendFilesystem {
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return nil, err
		}
		defer bucket.Close()
		reader, err = bucket.NewReader(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)), nil)
		if err != nil {
			return nil, err
		}
	} else {
		root, err := os.OpenRoot(filepath.Join(backend.LocalRoot, "_analysis"))
		if err != nil {
			return nil, err
		}
		defer root.Close()
		reader, err = root.Open(key)
		if err != nil {
			return nil, err
		}
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("monitor artifact exceeds limit")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != digest {
		return nil, fmt.Errorf("monitor artifact integrity failure")
	}
	return data, nil
}

// Every monitor reader shares version/count/ownership validation. The storage
// layout version stays v1 while new payloads use the v2 evidence envelope.
func readMonitorEvidence(ctx context.Context, project, run string, receipt storage.MonitorBatchReceipt) (analysis.MonitorEvidenceEnvelope, error) {
	body, err := readMonitorArtifact(ctx, receipt.ArtifactKey, receipt.Digest)
	if err != nil {
		return analysis.MonitorEvidenceEnvelope{}, err
	}
	return decodeMonitorEvidenceBody(body, project, run, receipt)
}

func decodeMonitorEvidenceBody(body []byte, project, run string, receipt storage.MonitorBatchReceipt) (analysis.MonitorEvidenceEnvelope, error) {
	key, digest := monitorArtifactKey(project, run, body)
	if key != receipt.ArtifactKey || digest != receipt.Digest {
		return analysis.MonitorEvidenceEnvelope{}, errors.New("monitor evidence ownership mismatch")
	}
	evidence, err := analysis.DecodeMonitorEvidence(body)
	if err != nil {
		return analysis.MonitorEvidenceEnvelope{}, err
	}
	if len(evidence.Points) != receipt.SampleCount {
		return analysis.MonitorEvidenceEnvelope{}, errors.New("monitor receipt sample count mismatch")
	}
	version := int64(1)
	if receipt.EvidenceVersion != nil {
		version = *receipt.EvidenceVersion
	}
	if int64(evidence.Version) != version {
		return analysis.MonitorEvidenceEnvelope{}, errors.New("monitor receipt version mismatch")
	}
	if evidence.Version == 1 {
		if receipt.ManagementSnapshotCount != nil || receipt.ManagementRowCount != nil || receipt.CollectionGeneration != nil || receipt.ManagementPollID != nil {
			return analysis.MonitorEvidenceEnvelope{}, errors.New("legacy monitor receipt has management metadata")
		}
		for _, point := range evidence.Points {
			if point.ProjectID != project || point.RunID != run {
				return analysis.MonitorEvidenceEnvelope{}, errors.New("legacy monitor point ownership mismatch")
			}
		}
		return evidence, nil
	}
	a := evidence.Attribution
	snapshot := evidence.MessagingManagement
	if a == nil || a.ProjectID != project || a.RunID != run || a.JobID != receipt.JobID || !a.ReceivedAt.Equal(receipt.ReceivedAt) || snapshot == nil {
		return analysis.MonitorEvidenceEnvelope{}, errors.New("monitor evidence attribution mismatch")
	}
	generation, err := analysis.ParseManagementInteger(snapshot.Generation)
	if err != nil || receipt.ManagementSnapshotCount == nil || *receipt.ManagementSnapshotCount != 1 || receipt.ManagementRowCount == nil || *receipt.ManagementRowCount != int64(len(snapshot.Rows)) || receipt.CollectionGeneration == nil || *receipt.CollectionGeneration != generation || receipt.ManagementPollID == nil || *receipt.ManagementPollID != snapshot.PollID {
		return analysis.MonitorEvidenceEnvelope{}, errors.New("monitor management receipt mismatch")
	}
	return evidence, nil
}

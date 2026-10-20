package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The handler's dedicated claim has already minted the owner/revision and exact
// output plan. The pipeline lease predates downloads and spans publication.
func (s *Server) normalizeAnalysisImportOwned(parent context.Context, store storage.AnalysisImportStore, attempts storage.AnalysisAttemptStore, in storage.AnalysisImport, pipeline *analytics.NativePipeline, dir string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer closeNativeAnalysisPipeline(pipeline, dir)
	var quiescence *nativeWriterQuiescenceRecord
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(ctx, time.Second)
				_, err := attempts.RenewAnalysisImportAttempt(renewCtx, in.ID, in.ProjectID, in.Owner, in.Revision, time.Minute)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	engine, err := analytics.NewForNativePipeline(ctx, dir)
	if err != nil {
		failureCtx, stop := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
		defer stop()
		quiescence, _ = s.retainNativeWriterQuiescence(ctx, in, pipeline, dir)
		if quiescence != nil {
			_ = s.confirmNativeWriterQuiescence(failureCtx, attempts, quiescence)
		}
		_ = attempts.FailAnalysisImportAttempt(failureCtx, in.ID, in.ProjectID, in.Owner, in.Revision)
		return
	}
	// On a close failure ReleaseOutputs leaves physical admission quarantined.
	defer engine.ReleaseOutputs()
	run := func() error {
		paths := []string{}
		seen := map[string]bool{}
		for i, file := range in.Files {
			local, digest, err := materializeAnalysisOriginal(ctx, &in, file, dir)
			if err != nil {
				return err
			}
			if seen[digest] {
				return errors.New("duplicate original files in import")
			}
			seen[digest] = true
			in.Files[i].SHA256 = digest
			sourceIO, err := pipeline.AcquireIO()
			if err != nil {
				return err
			}
			reader, err := os.Open(local)
			if err != nil {
				sourceIO.Joined()
				return err
			}
			sourceIO.TrackOwner(reader)
			_, writeErr := analysisObjectWrite(ctx, analysisObjectKey(in.ProjectID, in.ID, "original-"+file.ID+".parquet"), reader)
			closeErr := sourceIO.CloseOwner(reader)
			sourceIO.Joined()
			if err = errors.Join(writeErr, closeErr); err != nil {
				return err
			}
			paths = append(paths, local)
		}
		result, err := engine.Normalize(ctx, analytics.NormalizeRequest{ProjectID: in.ProjectID, DatasetID: in.ID, Files: paths, Mapping: *in.Mapping, AllowRejected: in.AllowRejected, PrecisionAcknowledgement: in.PrecisionReviewSHA256})
		if err != nil {
			return err
		}
		proofs, err := analysisNormalizedOutputProofs(in, result)
		if err != nil {
			return err
		}
		partitions := make([]analysis.NormalizedPartition, 0, len(result.Files))
		in.Accepted, in.Rejected, in.Rejections = result.Accepted, result.Rejected, result.Rejections
		in.NormalizedSHA256 = map[string]string{}
		in.RejectionSHA256 = map[string]string{}
		in.RejectionArtifacts = nil
		for _, file := range append(append([]string{}, result.Files...), result.RejectionFiles...) {
			key := analysisObjectKey(in.ProjectID, in.ID, "normalized-"+in.Owner+"-"+filepath.Base(file))
			reader, err := engine.OpenOutput(file)
			if err != nil {
				return err
			}
			proof, normalized := proofs[file]
			copyReader := io.Reader(reader)
			if normalized {
				copyReader = io.LimitReader(reader, proof.Bytes+1)
			}
			counted := &analysisOutputCountingReader{reader: copyReader}
			digest, writeErr := analysisObjectWrite(ctx, key, counted)
			closeErr := reader.Close()
			if err = errors.Join(writeErr, closeErr); err != nil {
				return err
			}
			if normalized {
				partition, err := analysisCopiedPartition(key, proof, counted.bytes, digest)
				if err != nil {
					return err
				}
				partitions = append(partitions, partition)
			}
			if strings.HasPrefix(filepath.Base(file), "reject-") {
				in.RejectionArtifacts = append(in.RejectionArtifacts, key)
				in.RejectionSHA256[key] = digest
			} else {
				in.NormalizedSHA256[key] = digest
			}
		}
		if in.PartitionManifestVersion == analysis.PartitionManifestVersion {
			in.PartitionManifest, err = analysis.NewNormalizedPartitionManifest(in.ProjectID, in.ID, *in.Mapping, partitions)
			if err != nil {
				return err
			}
			if err = validateAnalysisQueryManifest(&in); err != nil {
				return err
			}
		} else if in.PartitionManifest != nil {
			return analysis.ErrPartitionManifest
		}
		// No SQL receipt invents OS proof. These trusted calls are reachable only
		// after the exact supervisor/result and every synchronous copy have closed.
		if err = engine.ReleaseOutputs(); err != nil {
			return err
		}
		quiescence, err = s.retainNativeWriterQuiescence(ctx, in, pipeline, dir)
		if err != nil {
			return err
		}
		sealCtx, sealStop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		err = s.confirmNativeWriterQuiescence(sealCtx, attempts, quiescence)
		sealStop()
		if err != nil {
			return err
		}
		digest, err := storage.AnalysisAttemptResultDigest(in)
		if err != nil {
			return err
		}
		publicationLease, retainErr := pipeline.RetainPublication()
		if retainErr != nil {
			return retainErr
		}
		_, err = attempts.PublishAnalysisImportAttempt(ctx, storage.AnalysisAttemptPublication{Import: in, AttemptID: in.Owner, Digest: digest})
		if !errors.Is(err, storage.ErrAnalysisPublicationUnknown) {
			publicationLease.Resolve()
			return err
		}
		// Ambiguous commit never authorizes inline object deletion. Resolve under
		// the same durable project/import locks; retain admission on store outage.
		reconcileCtx, stop := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
		defer stop()
		resolution, resolveErr := attempts.ResolveAnalysisAttemptPublication(reconcileCtx, in.ID, in.ProjectID, in.Owner, digest)
		if resolveErr != nil {
			s.retainNativePublicationReconciliation(ctx, in, pipeline, publicationLease, dir, digest)
			return storage.ErrAnalysisPublicationUnknown
		}
		publicationLease.Resolve()
		if resolution.Published {
			return nil
		}
		return err
	}
	if err = run(); err != nil && !errors.Is(err, storage.ErrAnalysisPublicationUnknown) {
		// The stored error is a generic, user-facing message; keep the cause.
		slog.Warn("analysis normalization failed", "dataset_id", in.ID, "project_id", in.ProjectID, "error", err)
		failureCtx, stop := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
		defer stop()
		// Failure never overwrites cancellation/replacement/ready. Exact output
		// cleanup remains durable and separately gated on writer/process proof.
		_ = engine.ReleaseOutputs()
		// Reuse the initial exact hold after a failed durable Confirm. Reacquiring
		// a hold, waiting for pipeline.Done, or Release is not physical proof.
		if quiescence == nil {
			quiescence, _ = s.retainNativeWriterQuiescence(ctx, in, pipeline, dir)
		}
		if quiescence != nil {
			_ = s.confirmNativeWriterQuiescence(failureCtx, attempts, quiescence)
		}
		_ = attempts.FailAnalysisImportAttempt(failureCtx, in.ID, in.ProjectID, in.Owner, in.Revision)
	}
}

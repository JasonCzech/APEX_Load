package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Native HAR artifacts in a generator run archive (D5). A session's
// published, scrubbed HAR moves from its HAR directory into the run archive,
// where the controller fetches it with the run's other artifacts, records a
// durable receipt and acknowledges it; only then does the generator delete
// its copy. HAR is optional evidence: an artifact that cannot be retained is
// counted under an omission reason and never fails the run.
const (
	maxArchiveHARBytes    = int64(100 << 20)
	maxArchiveHARRunBytes = int64(1 << 30)
	archiveHARKindNative  = "har_native"
	archiveHARFidelity    = "native"
	archiveHARTakeTimeout = 30 * time.Second
)

// archiveHARArtifact describes one HAR in the run archive manifest.
type archiveHARArtifact struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Fidelity         string `json:"fidelity"`
	SourceGeneration string `json:"source_generation"`
	Sequence         int64  `json:"sequence"`
	SHA256           string `json:"sha256"`
	Bytes            int64  `json:"bytes"`
}

// archiveHARName is the run archive name of a working generation's HAR.
func archiveHARName(generation string) string {
	return "browser_har_" + generation + ".har"
}

// archiveHARGeneration parses a run archive HAR name.
func archiveHARGeneration(name string) (string, bool) {
	generation, found := strings.CutPrefix(name, "browser_har_")
	generation, suffixed := strings.CutSuffix(generation, ".har")
	if !found || !suffixed || len(generation) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(generation); err != nil || strings.ToLower(generation) != generation {
		return "", false
	}
	return generation, true
}

// Omission reasons a manifest counts.
const (
	harOmittedArtifactLimit = "artifact_limit"
	harOmittedTooLarge      = "artifact_too_large"
	harOmittedRunBytes      = "run_byte_limit"
	harOmittedArchiveQuota  = "archive_quota"
	harOmittedTransfer      = "transfer_failed"
	harOmittedInvalid       = "invalid_artifact"
)

var errArchiveHAROmitted = errors.New("browser HAR artifact omitted from the run archive")

// omitHARLocked records an omission; w.mu is held.
func (w *archiveRecorder) omitHARLocked(reason string) error {
	w.manifest.BrowserHARIncomplete = true
	if w.manifest.BrowserHAROmitted == nil {
		w.manifest.BrowserHAROmitted = map[string]int64{}
	}
	w.manifest.BrowserHAROmitted[reason]++
	return fmt.Errorf("%w: %s", errArchiveHAROmitted, reason)
}

func (w *archiveRecorder) omitHAR(reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.omitHARLocked(reason)
}

// RecordBrowserHARArtifact moves a published HAR into the run archive. The
// copy is written outside w.mu so result recording never waits for it;
// harMu keeps Finalize and Discard from closing the run while it runs.
func (w *archiveRecorder) RecordBrowserHARArtifact(artifact protocol.BrowserHARArtifact) error {
	if w == nil {
		return fmt.Errorf("generator archive recorder is unavailable")
	}
	w.harMu.Lock()
	defer w.harMu.Unlock()
	w.mu.Lock()
	if w.closed || w.runRoot == nil {
		w.mu.Unlock()
		return fmt.Errorf("generator archive recorder is closed")
	}
	name := archiveHARName(artifact.Generation)
	if _, valid := archiveHARGeneration(name); !valid {
		defer w.mu.Unlock()
		return w.omitHARLocked(harOmittedInvalid)
	}
	if len(w.manifest.BrowserHARArtifacts) >= protocol.MaxBrowserHARArtifactsPerRun {
		defer w.mu.Unlock()
		return w.omitHARLocked(harOmittedArtifactLimit)
	}
	for _, existing := range w.manifest.BrowserHARArtifacts {
		if existing.Name == name {
			defer w.mu.Unlock()
			return w.omitHARLocked(harOmittedInvalid)
		}
	}
	runRoot, budget, runBytes := w.runRoot, w.byteBudget, w.harBytes
	w.mu.Unlock()

	reason := harOmittedTransfer
	var sum string
	var size int64
	ctx, cancel := context.WithTimeout(context.Background(), archiveHARTakeTimeout)
	defer cancel()
	err := browser.TakeRuntimeHARArtifact(ctx, artifact.Path, func(r io.Reader, length int64) error {
		switch {
		case length > maxArchiveHARBytes:
			reason = harOmittedTooLarge
			return errArchiveHAROmitted
		case length > maxArchiveHARRunBytes-runBytes:
			reason = harOmittedRunBytes
			return errArchiveHAROmitted
		case !budget.admitOptional(length):
			reason = harOmittedArchiveQuota
			return errArchiveHAROmitted
		}
		file, err := replaceGeneratorArchiveFile(runRoot, name, 0o644)
		if err != nil {
			return err
		}
		digest := sha256.New()
		copied, copyErr := io.Copy(io.MultiWriter(&generatorArchiveQuotaWriter{file: file, budget: budget}, digest), io.LimitReader(r, length+1))
		if copyErr == nil && copied != length {
			copyErr = io.ErrUnexpectedEOF
		}
		if copyErr == nil {
			copyErr = file.Sync()
		}
		if err := errors.Join(copyErr, file.Close()); err != nil {
			if strings.Contains(err.Error(), "exceeds byte limit") || strings.Contains(err.Error(), "free space") {
				reason = harOmittedArchiveQuota
			}
			_ = runRoot.Remove(name)
			return err
		}
		sum, size = hex.EncodeToString(digest.Sum(nil)), length
		return nil
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		return errors.Join(w.omitHARLocked(reason), err)
	}
	w.harBytes += size
	w.manifest.BrowserHARArtifacts = append(w.manifest.BrowserHARArtifacts, archiveHARArtifact{Name: name, Kind: archiveHARKindNative, Fidelity: archiveHARFidelity, SourceGeneration: artifact.Generation, Sequence: int64(len(w.manifest.BrowserHARArtifacts) + 1), SHA256: sum, Bytes: size})
	return nil
}

// admitOptional reports whether optional evidence of length bytes leaves a
// quarter of the run's byte limit for required evidence.
func (b *generatorArchiveByteBudget) admitOptional(length int64) bool {
	if b == nil || length < 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written <= b.maxBytes-b.maxBytes/4 && length <= b.maxBytes-b.maxBytes/4-b.written
}

// AcknowledgeEvidence deletes the acknowledged HAR copies of a finalized run
// archive once the controller durably holds them. Each must still be the
// artifact the controller hashed; an unknown name or a changed file is
// retained. Deleting an absent artifact succeeds, so a repeated
// acknowledgement is harmless.
func (s *Service) AcknowledgeEvidence(_ context.Context, request *pb.EvidenceAcknowledgement) (*pb.EvidenceAck, error) {
	testID := request.GetTestId()
	if err := validateArchiveTestID(testID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid test_id: %v", err)
	}
	if identity := request.GetSourceIdentity(); identity != "" && !validArchiveSourceIdentity(identity) {
		return nil, status.Error(codes.InvalidArgument, "invalid archive source identity")
	}
	if len(request.GetArtifacts()) > protocol.MaxBrowserHARArtifactsPerRun {
		return nil, status.Error(codes.InvalidArgument, "too many acknowledged artifacts")
	}
	for _, artifact := range request.GetArtifacts() {
		if _, ok := archiveHARGeneration(artifact.GetName()); !ok || !validSHA256Hex(artifact.GetSha256()) {
			return nil, status.Error(codes.InvalidArgument, "acknowledged artifact is not a run archive HAR")
		}
	}
	manager := s.archiveManager
	lease, retained := manager.acquireCatalogLease(testID)
	if !retained {
		return nil, status.Error(codes.NotFound, "archive not found")
	}
	defer s.removeGeneratorArchiveRecords(manager.releaseCatalogLease(testID, time.Now()))
	if lease.namespaceErr != nil || lease.namespace == nil {
		return nil, status.Error(codes.Internal, "generator archive namespace is unavailable")
	}
	runRoot, runIdentity, err := openGeneratorArchiveRun(lease.namespace, testID, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Error(codes.NotFound, "archive not found")
		}
		return nil, status.Errorf(codes.FailedPrecondition, "open archive run: %v", err)
	}
	defer runRoot.Close()
	if lease.expectedIdentity == nil || !os.SameFile(lease.expectedIdentity, runIdentity) {
		return nil, status.Error(codes.FailedPrecondition, "archive run was replaced")
	}
	if identity := request.GetSourceIdentity(); identity != "" {
		if err := verifyArchiveSourceIdentity(runRoot, testID, identity); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "verify archive source identity: %v", err)
		}
	}
	ack := &pb.EvidenceAck{}
	for _, artifact := range request.GetArtifacts() {
		name := artifact.GetName()
		file, info, err := openGeneratorArchiveRegularFile(runRoot, name)
		if os.IsNotExist(err) {
			ack.Deleted = append(ack.Deleted, name)
			continue
		}
		if err != nil {
			ack.Retained = append(ack.Retained, name)
			continue
		}
		digest := sha256.New()
		_, copyErr := io.Copy(digest, io.LimitReader(file, maxArchiveHARBytes+1))
		closeErr := file.Close()
		current, statErr := runRoot.Lstat(name)
		if copyErr != nil || closeErr != nil || statErr != nil || !os.SameFile(info, current) || hex.EncodeToString(digest.Sum(nil)) != artifact.GetSha256() {
			ack.Retained = append(ack.Retained, name)
			continue
		}
		if err := runRoot.Remove(name); err != nil && !os.IsNotExist(err) {
			ack.Retained = append(ack.Retained, name)
			continue
		}
		ack.Deleted = append(ack.Deleted, name)
	}
	return ack, nil
}

func validSHA256Hex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

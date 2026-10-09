package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// archiveEvidenceArtifact describes one transferable evidence artifact (a
// native HAR or the interactions sidecar) in the run archive manifest.
type archiveEvidenceArtifact struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Fidelity         string `json:"fidelity"`
	SourceGeneration string `json:"source_generation"`
	Sequence         int64  `json:"sequence"`
	SHA256           string `json:"sha256"`
	Bytes            int64  `json:"bytes"`
	// EvidenceGeneration is the browser evidence generation of the context
	// the HAR recorded; absent when unknown.
	EvidenceGeneration string `json:"evidence_generation,omitempty"`
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
	harOmittedArtifactLimit = protocol.BrowserHAROmittedArtifactLimit
	harOmittedTooLarge      = protocol.BrowserHAROmittedTooLarge
	harOmittedRunBytes      = protocol.BrowserHAROmittedRunBytes
	harOmittedArchiveQuota  = protocol.BrowserHAROmittedArchiveQuota
	harOmittedTransfer      = protocol.BrowserHAROmittedTransfer
	harOmittedInvalid       = protocol.BrowserHAROmittedInvalid
	harOmittedDuplicate     = protocol.BrowserHAROmittedDuplicate
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
			return w.omitHARLocked(harOmittedDuplicate)
		}
	}
	runRoot, budget, runBytes := w.runRoot, w.byteBudget, w.harBytes
	w.mu.Unlock()

	reason := harOmittedTransfer
	var sum string
	var size int64
	ctx, cancel := context.WithTimeout(context.Background(), archiveHARTakeTimeout)
	defer cancel()
	// The HAR directory keeps its copy until this archive is finalized: a
	// run archive that fails is discarded whole, and the HAR then stays
	// under its directory's retention instead of being lost with it.
	identity, err := browser.ReadRuntimeHARArtifact(ctx, artifact.Path, func(r io.Reader, length int64) error {
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
			budget.releaseOptional(length)
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
			budget.releaseOptional(length)
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
	w.harSources = append(w.harSources, archivedHARSource{path: artifact.Path, identity: identity})
	evidenceGeneration := artifact.EvidenceGeneration
	if !protocol.BrowserEvidenceGeneration(evidenceGeneration) {
		evidenceGeneration = ""
	}
	w.manifest.BrowserHARArtifacts = append(w.manifest.BrowserHARArtifacts, archiveEvidenceArtifact{Name: name, Kind: archiveHARKindNative, Fidelity: archiveHARFidelity, SourceGeneration: artifact.Generation, Sequence: int64(len(w.manifest.BrowserHARArtifacts) + 1), SHA256: sum, Bytes: size, EvidenceGeneration: evidenceGeneration})
	return nil
}

// optionalEvidenceFreeBytes reads the free space the optional-evidence
// allowance is fixed against (tests replace it).
var optionalEvidenceFreeBytes = generatorArchiveFreeBytes

// admitOptional admits optional evidence of length bytes while all optional
// evidence stays within one allowance, fixed at the first admission: a
// quarter of the run's byte limit and of the free space then above the
// floor. Required evidence written later therefore keeps three quarters of
// both, on a volume the byte limit alone does not bound. An admitted length
// that is then not written is released.
func (b *generatorArchiveByteBudget) admitOptional(length int64) bool {
	if b == nil || length < 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.optionalSet {
		free, err := optionalEvidenceFreeBytes(b.root)
		if err != nil {
			return false
		}
		b.optionalCap, b.optionalSet = min(b.maxBytes, max(free-b.minFreeBytes, 0))/4, true
	}
	if length > b.optionalCap-b.optional || length > b.maxBytes-b.written {
		return false
	}
	b.optional += length
	return true
}

// releaseOptional returns an admitted optional length that was not written.
func (b *generatorArchiveByteBudget) releaseOptional(length int64) {
	if b == nil || length <= 0 {
		return
	}
	b.mu.Lock()
	b.optional = max(b.optional-length, 0)
	b.mu.Unlock()
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
	// A run archive holds at most its HAR limit plus the interactions
	// sidecar.
	if len(request.GetArtifacts()) > protocol.MaxBrowserHARArtifactsPerRun+1 {
		return nil, status.Error(codes.InvalidArgument, "too many acknowledged artifacts")
	}
	for _, artifact := range request.GetArtifacts() {
		if _, ok := archiveHARGeneration(artifact.GetName()); !ok && artifact.GetName() != archiveInteractionsName || !validSHA256Hex(artifact.GetSha256()) {
			return nil, status.Error(codes.InvalidArgument, "acknowledged artifact is not run archive evidence")
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
		_, copyErr := io.Copy(digest, io.LimitReader(file, max(maxArchiveHARBytes, maxArchiveInteractionsRunBytes)+1))
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

// archivedHARSource is a HAR directory copy a run archive holds, retired
// from its directory once the archive is finalized.
type archivedHARSource struct {
	path     string
	identity os.FileInfo
}

// retireRegisteredHARSources retires the HAR directory copies once the
// finalized archive is registered: until then a failed registration (which
// deletes the archive) must leave them.
func (w *archiveRecorder) retireRegisteredHARSources() {
	if w == nil {
		return
	}
	w.harMu.Lock()
	defer w.harMu.Unlock()
	w.retireArchivedHARSources()
}

// retireArchivedHARSources retires the HAR directory copies of a finalized
// archive's HARs. A copy that cannot be retired stays under its directory's
// retention. The caller holds harMu.
func (w *archiveRecorder) retireArchivedHARSources() {
	for _, source := range w.harSources {
		ctx, cancel := context.WithTimeout(context.Background(), archiveHARTakeTimeout)
		if err := browser.RetireRuntimeHARArtifact(ctx, source.path, source.identity); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("archived HAR left in its HAR directory until retention", "path", source.path, "error", err)
		}
		cancel()
	}
	w.harSources = nil
}

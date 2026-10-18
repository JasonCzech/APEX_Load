package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const ciTerminalEvidenceFailureReason = "durable terminal evidence is unavailable or failed integrity validation"

// validatedCIRunArchiveManifest resolves a CI run only through the durable
// archive catalog and then verifies the persisted manifest trust anchor and
// every declared artifact hash. A COMPLETE CI result is authoritative only
// after this succeeds; an in-memory writer or parseable manifest is not enough.
type ciValidatedArchiveEvidence struct {
	Manifest     *RunArchiveManifest
	FinalSummary *IntervalSummaryRecord
	// Validity is the run's validity verdict, nil when no report could be
	// built over the archive, which ValidityUnavailable then says.
	Validity            *ciRunValidity
	ValidityUnavailable bool
	// TelemetryVerdict is the run's telemetry acceptance verdict, nil when
	// it pinned no acceptance policy.
	TelemetryVerdict *TelemetryAcceptanceVerdict
}

func (s *Server) validatedCIRunArchiveEvidence(ctx context.Context, test *ManagedTest) (*ciValidatedArchiveEvidence, error) {
	if s == nil || s.runner == nil || test == nil || strings.TrimSpace(test.ID) == "" {
		return nil, errors.New("CI run archive identity is unavailable")
	}
	if s.runner.archiveStore == nil {
		return nil, errors.New("durable archive catalog is unavailable")
	}
	if err := s.runner.ensureRunMonitorFinalization(test.ProjectID, test.ID); err != nil {
		return nil, err
	}
	record, err := s.runner.archiveStore.GetRunArchive(test.ID)
	if errors.Is(err, sql.ErrNoRows) || record == nil {
		return nil, errors.New("durable archive record was not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load durable archive record: %w", err)
	}
	if strings.TrimSpace(record.TestID) != strings.TrimSpace(test.ID) {
		return nil, errors.New("durable archive record belongs to another run")
	}
	if strings.TrimSpace(record.ManifestSHA256) == "" {
		return nil, errors.New("durable archive manifest trust anchor is missing")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("materialize durable archive: %w", err)
	}
	defer lease.Release()

	// Bypass the report cache: terminal authorization must re-read the durable
	// manifest so a previously cached clean value cannot conceal later tampering.
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, fmt.Errorf("load durable archive manifest: %w", err)
	}
	if strings.TrimSpace(manifest.TestID) != strings.TrimSpace(test.ID) {
		return nil, errors.New("durable archive manifest belongs to another run")
	}
	if normalizeProjectID(manifest.Provenance.ProjectID) != normalizeProjectID(test.ProjectID) {
		return nil, errors.New("durable archive manifest belongs to another project")
	}
	if manifest.Provenance.RequestedAt.IsZero() || test.CreatedAt.IsZero() || !archiveRunTimestampMatches(manifest.Provenance.RequestedAt, test.CreatedAt, s.runner.testStore) {
		return nil, errors.New("durable archive manifest belongs to a stale run identity")
	}
	incarnation, err := archiveManifestRunIncarnation(manifest)
	if err != nil || incarnation != managedTestRunIncarnation(test) {
		return nil, errors.New("durable archive manifest belongs to a different run incarnation")
	}
	if status := strings.ToUpper(strings.TrimSpace(manifest.Status)); status != strings.ToUpper(strings.TrimSpace(test.Status)) {
		return nil, errors.New("durable archive status does not match the terminal run")
	}

	names, err := materializationArtifactNames(manifest)
	if err != nil {
		return nil, fmt.Errorf("validate durable archive inventory: %w", err)
	}
	descriptors := make(map[string]RunArchiveArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		name, nameErr := requiredArchiveArtifactName("artifacts.name", artifact.Name)
		if nameErr != nil {
			return nil, fmt.Errorf("validate durable archive inventory: %w", nameErr)
		}
		if _, duplicate := descriptors[name]; duplicate {
			return nil, fmt.Errorf("durable archive contains duplicate artifact %q", name)
		}
		if strings.TrimSpace(artifact.SHA256) == "" {
			return nil, fmt.Errorf("durable archive artifact %q has no persisted SHA-256", name)
		}
		artifact.Name = name
		descriptors[name] = artifact
	}
	for _, name := range names {
		if name == runArchiveManifestFileName {
			continue
		}
		if _, ok := descriptors[name]; !ok {
			return nil, fmt.Errorf("durable archive artifact %q has no persisted SHA-256 descriptor", name)
		}
	}
	if err := validateMaterializedArchiveWithManifestContext(ctx, record, record.ArchivePath, record.ManifestPath, manifest); err != nil {
		return nil, fmt.Errorf("verify durable archive integrity: %w", err)
	}
	finalSummary, err := loadCIFinalIntervalSummary(ctx, record, manifest)
	if err != nil {
		return nil, fmt.Errorf("load durable archive terminal metrics: %w", err)
	}
	validity, validityErr := s.archiveReadCaches().cachedRunArchiveValidity(record)
	return &ciValidatedArchiveEvidence{Manifest: manifest, FinalSummary: finalSummary, Validity: validity, ValidityUnavailable: validityErr != nil,
		TelemetryVerdict: telemetryAcceptanceForManifest(manifest)}, nil
}

// cachedRunArchiveValidity is the validity verdict of the report built over
// an archive, whose cache key includes the manifest digest. It reads the
// shared cached report without cloning all of it.
func (c *archiveReadCaches) cachedRunArchiveValidity(record *storage.RunArchiveRecord) (*ciRunValidity, error) {
	report, err := c.reports.getOrLoad(runArchiveReportCacheKey(record), func() (*RunArchiveReport, error) {
		return buildRunArchiveReport(record)
	})
	if err != nil {
		return nil, err
	}
	if report == nil || report.Validity == nil {
		return nil, errors.New("the archive report has no validity verdict")
	}
	return &ciRunValidity{Verdict: report.Validity.Verdict, Reasons: append([]string{}, report.Validity.Reasons...)}, nil
}

func loadCIFinalIntervalSummary(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*IntervalSummaryRecord, error) {
	if manifest == nil || manifest.IntervalSummaryCount == 0 {
		return nil, nil
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.IntervalSummariesFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := newArchiveJSONLScanner(file)
	var final IntervalSummaryRecord
	var count int64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count++
		if count > manifest.IntervalSummaryCount {
			return nil, fmt.Errorf("interval summary count exceeds manifest")
		}
		if err := json.Unmarshal(scanner.Bytes(), &final); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if count != manifest.IntervalSummaryCount {
		return nil, fmt.Errorf("interval summary count is %d, want %d", count, manifest.IntervalSummaryCount)
	}
	return &final, nil
}

func mergeCIRunArchiveEvidence(response *ciRunResponse, evidence *ciValidatedArchiveEvidence) {
	if response == nil || evidence == nil || evidence.Manifest == nil {
		return
	}
	manifest := evidence.Manifest
	response.MonitoringStatus = manifest.MonitoringStatus
	response.MonitorRuleResults = manifest.MonitorRuleResults
	response.MonitoringIncomplete = monitoringAcceptanceIncomplete(manifest)
	response.Transactions = manifest.TransactionCount
	if evidence.FinalSummary != nil {
		response.Errors = evidence.FinalSummary.TotalErrors
		response.TPS = evidence.FinalSummary.TPS
	}
	// Live polling records cumulative evidence. Archive finalization can race a
	// final metrics update, so never let an older manifest erase an evaluation or
	// breach already observed by the controller.
	response.SLAEvaluated = response.SLAEvaluated || manifest.SLAEvaluated
	response.SLABreached = response.SLABreached || manifest.SLABreached
	response.SLADetails = compactNonEmptyStrings(append(response.SLADetails, manifest.SLADetails...)...)
	response.validityUnavailable = evidence.ValidityUnavailable
	if evidence.Validity != nil {
		validity := *evidence.Validity
		validity.Reasons = append([]string{}, validity.Reasons...)
		response.Validity = &validity
	}
	if evidence.TelemetryVerdict != nil {
		response.TelemetryVerdict = &ciTelemetryVerdict{TelemetryAcceptanceVerdict: *cloneTelemetryAcceptanceVerdict(evidence.TelemetryVerdict)}
	}
}

func applyCITerminalEvidence(response *ciRunResponse, evidence *ciValidatedArchiveEvidence, evidenceErr error) {
	if response == nil {
		return
	}
	response.terminalEvidenceChecked = true
	response.terminalEvidenceVerified = evidenceErr == nil && evidence != nil && evidence.Manifest != nil
	if response.terminalEvidenceVerified {
		mergeCIRunArchiveEvidence(response, evidence)
		response.ReportURL = ciPublicURL("/tests/" + url.PathEscape(response.TestID))
		return
	}
	response.ReportURL = ""
	if errors.Is(evidenceErr, errMonitoringPending) {
		response.MonitoringStatus = "pending"
		response.Outcome = "monitoring_pending"
		return
	}
	if strings.EqualFold(strings.TrimSpace(response.Status), "COMPLETE") {
		response.Error = ciTerminalEvidenceFailureReason
		response.Outcome = "execution_error"
	}
}

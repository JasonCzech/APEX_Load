package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Root-cause v2 schemas and limits.
const (
	rcaEvidenceSchema = "apexload.rca_evidence.v1"
	rcaBundleSchema   = "apexload.rca_bundle.v2"
	rcaMaxBundleBytes = 256 << 10
	// An analysis lease outlives the engine's 120 s wall-time budget plus
	// archive reading, so a live owner is not overtaken.
	rcaAnalysisLease = 5 * time.Minute
	// At most two analyses run at once on one controller.
	rcaMaxConcurrentAnalyses = 2
	// rcaMaxEvidenceReadTime bounds reading the archive before the engine's
	// budget starts: materializing it, its manifest, summaries and resources
	// and one pass over its rows. It is the archive's size that sets this,
	// not the analysis, so it is kept apart from the engine's wall time.
	rcaMaxEvidenceReadTime = 10 * time.Minute
)

// rcaArchiveAdapterVersion names how this package reads an archive into the
// engine's inputs: which series, load levels and evidence it offers. It
// changes whenever that reading changes, as the engine's version does for
// the engine itself.
//
// archive.2: plateaus tolerate timestamp jitter, and actions are judged
// beside automatic per-iteration transactions.
// archive.3: provider series are keyed by their measurement series identity,
// every series carries one, joint (version 3) interval evidence becomes series
// on its exact action-dimension tuple, associated analysis datasets
// contribute imported series and browser document measurement tuples
// contribute document series (joint slices ranking last for series and
// anomalies), and unscored telemetry reads as unknown.
// archive.4: the archive input digest covers the manifest fields and
// artifacts the analysis reads, not the adjunct sidecars, telemetry accounts
// and test notes a finalize refresh rewrites; a request made while the
// archive is still being finalized is refused instead of failing later.
const rcaArchiveAdapterVersion = "archive.4"

// rcaEngineVersion changes whenever the engine's output for the same frozen
// inputs could change, so it is part of every input digest.
var rcaEngineVersion = rca.EngineVersion() + "+" + rcaArchiveAdapterVersion

var errRCAArchiveChanged = errors.New("the run archive changed after the analysis was requested")

func projectListed(variable, project string) bool {
	for _, listed := range strings.Split(os.Getenv(variable), ",") {
		if project != "" && strings.TrimSpace(listed) == project {
			return true
		}
	}
	return false
}

// rcaV2ProjectEnabled gates computing v2 analyses; saved ones stay readable.
func rcaV2ProjectEnabled(project string) bool {
	return projectListed("APEXLOAD_RCA_V2_PROJECTS", project)
}

// rcaV2PresentEnabled says whether Summary, Diagnostics and reports present
// v2 for the project.
func rcaV2PresentEnabled(project string) bool {
	return projectListed("APEXLOAD_RCA_V2_PRESENT_PROJECTS", project)
}

// rcaAutoEnrichEnabled starts enrichment when a run's archive is finalized.
func rcaAutoEnrichEnabled(project string) bool {
	return projectListed("APEXLOAD_RCA_AUTO_ENRICH_PROJECTS", project)
}

// rcaAnalysisRequest is the POST body.
type rcaAnalysisRequest struct {
	Enrich         bool   `json:"enrich"`
	BaselineTestID string `json:"baseline_test_id,omitempty"`
}

// rcaEvidence is one frozen evidence revision: what the run's archive and
// monitoring held when it was captured.
type rcaEvidence struct {
	Schema        string    `json:"schema"`
	ArchiveDigest string    `json:"archive_digest"`
	CapturedAt    time.Time `json:"captured_at"`
	Enriched      bool      `json:"enriched"`
	// Sidecars maps each archive evidence sidecar to present, truncated or
	// absent.
	Sidecars       map[string]string   `json:"sidecars"`
	MonitorSources []rcaEvidenceSource `json:"monitor_sources"`
	// Mappings are the project's dependency-to-entity decisions when the
	// evidence was captured; MappingDigest identifies them.
	Mappings      []rca.GraphMapping `json:"mappings"`
	MappingDigest string             `json:"mapping_digest,omitempty"`
	// Datasets are the project's ready analysis datasets associated with the
	// run when the evidence was captured, each at the revision, mapping and
	// partition digest an analysis reads; DatasetsTruncated says more existed.
	Datasets          []rcaEvidenceDataset `json:"datasets,omitempty"`
	DatasetsTruncated bool                 `json:"datasets_truncated,omitempty"`
}

type rcaEvidenceSource struct {
	SourceID       string `json:"source_id"`
	Pack           string `json:"pack"`
	State          string `json:"state"`
	CoverageReason string `json:"coverage_reason,omitempty"`
	Required       bool   `json:"required"`
}

// rcaAnalysisPayload is what an analysis row holds.
type rcaAnalysisPayload struct {
	Request    rcaAnalysisRequest `json:"request"`
	Inputs     rca.InputDigests   `json:"inputs"`
	Assessment *rca.Assessment    `json:"assessment,omitempty"`
	// Excerpts are the series windows the assessment's evidence references
	// name, frozen with it.
	Excerpts []RCAEvidenceExcerpt `json:"excerpts,omitempty"`
	Error    string               `json:"error,omitempty"`
	// ArchiveManifest is the digest of the whole archive manifest when the
	// inputs were frozen. Inputs.Archive covers only what the analysis reads,
	// so an unchanged manifest digest says the archive is unchanged without
	// reading it again.
	ArchiveManifest string `json:"archive_manifest,omitempty"`
	// Attempt counts the analyses of these inputs that a budget cut short
	// before this one; it tells identical requests apart from the cut ones.
	Attempt int `json:"attempt,omitempty"`
}

// RCAAnalysisResponse reports an analysis and, once completed, its
// assessment.
type RCAAnalysisResponse struct {
	AnalysisID  string            `json:"analysis_id,omitempty"`
	Revision    int64             `json:"revision,omitempty"`
	State       string            `json:"state"`
	CreatedAt   *time.Time        `json:"created_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	Inputs      *rca.InputDigests `json:"inputs,omitempty"`
	Assessment  *rca.Assessment   `json:"assessment,omitempty"`
	Error       string            `json:"error,omitempty"`
	// Presented says whether the project presents v2 in Summary,
	// Diagnostics and reports.
	Presented bool `json:"presented"`
	// AnalysisEnabled says whether the project computes new analyses;
	// saved ones stay readable when it does not.
	AnalysisEnabled bool `json:"analysis_enabled"`

	// ArchiveChanged says the run's archive changed after the analysis read
	// it, as when late monitoring evidence arrives; it is not in the analysis.
	ArchiveChanged bool `json:"archive_changed,omitempty"`
}

// RCABundle is an analysis with its evidence, bounded for agents and export
// (apexload.rca_bundle.v2): the assessment, with its coverage and
// limitations, the evidence excerpts its reference IDs name, and the frozen
// evidence revision's inventory.
type RCABundle struct {
	Schema           string               `json:"schema"`
	AnalysisID       string               `json:"analysis_id"`
	Revision         int64                `json:"revision"`
	Assessment       rca.Assessment       `json:"assessment"`
	Excerpts         []RCAEvidenceExcerpt `json:"excerpts"`
	EvidenceRevision int64                `json:"evidence_revision"`
	Evidence         *json.RawMessage     `json:"evidence,omitempty"`
	// Truncated means windows, excerpts or evidence were left out to stay
	// within 256 KiB.
	Truncated bool `json:"truncated"`
}

// rcaInputDigest identifies a request's frozen inputs and which attempt at
// them it is: the first, or one after an assessment the budget cut short.
func rcaInputDigest(inputs rca.InputDigests, attempt int) string {
	body, _ := json.Marshal(struct {
		Inputs  rca.InputDigests `json:"inputs"`
		Attempt int              `json:"attempt,omitempty"`
	}{inputs, attempt})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// rcaAnalysisCutByBudget reports whether a completed analysis's assessment
// was cut short by the engine's wall-time or heap budget. Such an assessment
// describes the controller's load when it ran, not the run, so it does not
// answer an identical request.
func rcaAnalysisCutByBudget(analysis *storage.RCAAnalysis) bool {
	if analysis == nil || analysis.State != storage.RCAStateCompleted {
		return false
	}
	payload := rcaAnalysisPayloadOf(analysis)
	return payload.Assessment != nil && rca.CutByBudget(payload.Assessment.Limitations)
}

// rcaManifestDigest identifies an archive publication by its whole manifest.
func rcaManifestDigest(record *storage.RunArchiveRecord) (string, error) {
	if record == nil {
		return "", errors.New("run archive unavailable")
	}
	if digest := strings.TrimSpace(record.ManifestSHA256); digest != "" {
		return digest, nil
	}
	data, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		return "", fmt.Errorf("read archive manifest: %w", err)
	}
	return sha256BytesHex(data), nil
}

// rcaEvidenceDigest identifies the archive evidence an analysis reads: the
// manifest without the adjunct sidecars, telemetry accounts and test notes
// that finalize writes after publication and a later refresh rewrites, and
// only the artifacts the adapters open. An adjunct-only rewrite leaves it
// unchanged, so a completed analysis stays current and a queued one still
// runs; a field this package does not know about still changes it, which
// errs towards re-analysis.
func rcaEvidenceDigest(manifest *RunArchiveManifest) (string, error) {
	if manifest == nil {
		return "", errors.New("run archive manifest unavailable")
	}
	read := *manifest
	read.TestManifestFile, read.TestNotesFile = "", ""
	read.ProviderLookupsFile, read.ProviderLookupCount, read.ProviderLookupsTruncated = "", 0, false
	read.ExemplarsFile, read.ExemplarCount, read.ExemplarsTruncated = "", 0, false
	read.ClockObservationsFile, read.ClockObservationCount, read.ClockObservationsTruncated = "", 0, false
	read.TelemetrySourcesFile, read.TelemetrySourceEpochCount, read.TelemetrySourcesTruncated = "", 0, false
	read.JointIntervalsFile, read.JointIntervalsParquetFile, read.JointIntervalCount, read.JointIntervalsTruncated = "", "", 0, false
	read.TelemetryCoverage, read.TelemetryStageDistributions, read.TelemetryStageDigests, read.TelemetryLoss = nil, nil, nil, nil
	read.PhaseEventsFile, read.PhaseEventCount, read.PhaseEventsTruncated = "", 0, false
	read.BrowserHARArtifacts, read.BrowserHARIncomplete, read.BrowserHAROmitted = nil, false, nil
	read.BrowserInteractionsFile, read.BrowserInteractionRecordCount, read.BrowserInteractionsIncomplete, read.BrowserInteractionsDropped, read.BrowserInteractionsArtifact = "", 0, false, 0, nil
	inputs := map[string]bool{}
	for _, name := range []string{manifest.TransactionsFile, manifest.IntervalSummariesFile, manifest.ResourceSamplesFile, manifest.IntervalDistributionsFile, manifest.MonitorEvidenceFile, manifest.BrowserDocumentsFile} {
		if strings.TrimSpace(name) != "" {
			inputs[name] = true
		}
	}
	read.Artifacts = nil
	for _, artifact := range manifest.Artifacts {
		if inputs[artifact.Name] {
			read.Artifacts = append(read.Artifacts, artifact)
		}
	}
	body, err := json.Marshal(read)
	if err != nil {
		return "", fmt.Errorf("encode archive manifest: %w", err)
	}
	return sha256BytesHex(body), nil
}

// rcaArchiveDigest is the evidence digest of an archive record's current
// manifest, read from its materialized path.
func rcaArchiveDigest(record *storage.RunArchiveRecord) (string, error) {
	if record == nil {
		return "", errors.New("run archive unavailable")
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return "", err
	}
	return rcaEvidenceDigest(manifest)
}

// rcaArchiveFinalizing reports whether the run's archive is still being
// finalized or refreshed. The sidecars and manifest that work publishes would
// change the evidence an analysis requested now reads, so the request waits
// instead of failing when it runs.
func (s *Server) rcaArchiveFinalizing(testID string) bool {
	if s.runner == nil {
		return false
	}
	if outbox := s.runner.archiveFinalizationOutbox(); outbox != nil {
		if job, err := outbox.GetArchiveFinalizationJob(testID); err == nil && job != nil {
			return true
		}
	}
	if refresh := s.runner.archiveRefreshOutbox(); refresh != nil {
		if job, err := refresh.GetArchiveRefreshJob(testID); err == nil && job != nil {
			return true
		}
	}
	return false
}

func sidecarState(file string, truncated bool) string {
	switch {
	case strings.TrimSpace(file) == "":
		return "absent"
	case truncated:
		return "truncated"
	}
	return "present"
}

// captureRCAEvidence freezes a new evidence revision for a run: its archive
// sidecars, its monitor sources' outcomes and the project's mapping
// decisions. Enrichment captures the same inventory; provider evidence pulls
// arrive with the signatures. When reuse accepts the run's latest revision
// against the one being captured, that revision is returned instead; the
// check runs under the run's lock.
func captureRCAEvidence(store any, project, testID string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, enriched bool, now time.Time, reuse func(latest, current rcaEvidence) bool) (*storage.RCAEvidenceRevision, error) {
	rcaStore, ok := store.(storage.RCAStore)
	if !ok {
		return nil, errors.New("root-cause storage unavailable")
	}
	if record == nil {
		return nil, errors.New("run archive unavailable")
	}
	digest, err := rcaEvidenceDigest(manifest)
	if err != nil {
		return nil, err
	}
	evidence := rcaEvidence{Schema: rcaEvidenceSchema, ArchiveDigest: digest, CapturedAt: now.UTC(), Enriched: enriched, MonitorSources: []rcaEvidenceSource{},
		Sidecars: map[string]string{
			"exemplars":              sidecarState(manifest.ExemplarsFile, manifest.ExemplarsTruncated),
			"provider_lookups":       sidecarState(manifest.ProviderLookupsFile, manifest.ProviderLookupsTruncated),
			"interval_distributions": sidecarState(manifest.IntervalDistributionsFile, manifest.IntervalDistributionsTruncated),
			"clock_observations":     sidecarState(manifest.ClockObservationsFile, manifest.ClockObservationsTruncated),
			"telemetry_sources":      sidecarState(manifest.TelemetrySourcesFile, manifest.TelemetrySourcesTruncated),
			"phase_events":           sidecarState(manifest.PhaseEventsFile, manifest.PhaseEventsTruncated),
		}}
	if monitors, ok := store.(storage.RunMonitorStore); ok {
		jobs, err := monitors.ListRunMonitorJobs(project, testID)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			evidence.MonitorSources = append(evidence.MonitorSources, rcaEvidenceSource{SourceID: job.Source.ID, Pack: job.Source.Pack, State: job.State, CoverageReason: job.CoverageReason, Required: job.Required})
		}
		sort.Slice(evidence.MonitorSources, func(i, j int) bool { return evidence.MonitorSources[i].SourceID < evidence.MonitorSources[j].SourceID })
	}
	if evidence.Mappings, evidence.MappingDigest, err = loadRCAMappings(store, project); err != nil {
		return nil, err
	}
	if evidence.Datasets, evidence.DatasetsTruncated, err = loadRCADatasets(store, project, testID); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	var accept func(*storage.RCAEvidenceRevision) bool
	if reuse != nil {
		accept = func(latest *storage.RCAEvidenceRevision) bool {
			var frozen rcaEvidence
			return json.Unmarshal(latest.Payload, &frozen) == nil && reuse(frozen, evidence)
		}
	}
	return rcaStore.AppendRCAEvidenceRevision(project, testID, payload, now, accept)
}

// sameEvidence reuses a revision captured from the same archive evidence
// under the same mapping decisions and associated dataset revisions. The
// sidecar inventory is not compared: a sidecar the assessment reads is in
// the evidence digest, and one it does not read (exemplars, phase events)
// must not make identical requests two analyses.
func sameEvidence(latest, current rcaEvidence) bool {
	return latest.ArchiveDigest == current.ArchiveDigest && latest.MappingDigest == current.MappingDigest &&
		rcaDatasetsDigest(latest.Datasets) == rcaDatasetsDigest(current.Datasets) && latest.DatasetsTruncated == current.DatasetsTruncated
}

// autoEnrichRCAEvidence captures an enriched evidence revision when a
// gated project's run archive is finalized.
func (tr *TestRunner) autoEnrichRCAEvidence(test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) {
	if test == nil || tr.testStore == nil {
		return
	}
	project := normalizeProjectID(test.ProjectID)
	if !rcaAutoEnrichEnabled(project) {
		return
	}
	// A refresh of the same archive reuses its enrichment.
	reuse := func(latest, current rcaEvidence) bool { return latest.Enriched && sameEvidence(latest, current) }
	if _, err := captureRCAEvidence(tr.testStore, project, test.ID, record, manifest, true, time.Now(), reuse); err != nil {
		slog.Warn("failed to enrich root-cause evidence", "test_id", test.ID, "error", err)
	}
}

// rcaAnalysisPayloadOf decodes an analysis row's payload; a row that cannot
// be decoded reads as empty.
func rcaAnalysisPayloadOf(analysis *storage.RCAAnalysis) rcaAnalysisPayload {
	var payload rcaAnalysisPayload
	_ = json.Unmarshal(analysis.Payload, &payload)
	return payload
}

func rcaAnalysisResponse(analysis *storage.RCAAnalysis) RCAAnalysisResponse {
	payload := rcaAnalysisPayloadOf(analysis)
	created := analysis.CreatedAt
	out := RCAAnalysisResponse{AnalysisID: analysis.ID, Revision: analysis.Revision, State: analysis.State, CreatedAt: &created, CompletedAt: analysis.CompletedAt,
		Inputs: &payload.Inputs, Error: payload.Error, Presented: rcaV2PresentEnabled(analysis.ProjectID), AnalysisEnabled: rcaV2ProjectEnabled(analysis.ProjectID)}
	if analysis.State == storage.RCAStateCompleted {
		out.Assessment = payload.Assessment
	}
	return out
}

func (s *Server) rcaStore(w http.ResponseWriter) (storage.RCAStore, bool) {
	store, ok := s.store.(storage.RCAStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "root-cause analysis storage unavailable")
	}
	return store, ok
}

// handleCreateRCAAnalysis queues an analysis of the run's archive, or returns
// the existing one for identical frozen inputs.
func (s *Server) handleCreateRCAAnalysis(w http.ResponseWriter, r *http.Request) {
	test, record, lease, ok := s.requireRunArchiveTest(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	// Runs stored before project scoping belong to the default project.
	project := normalizeProjectID(test.ProjectID)
	if !rcaV2ProjectEnabled(project) {
		writeError(w, http.StatusConflict, "root-cause v2 is not enabled for this project")
		return
	}
	store, ok := s.rcaStore(w)
	if !ok {
		return
	}
	var request rcaAnalysisRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decodeJSONBody(w, r, &request) {
		return
	}
	request.BaselineTestID = strings.TrimSpace(request.BaselineTestID)
	// The archive is published before finalize writes its sidecars and the
	// final manifest; an analysis frozen in that window would read a changed
	// archive when it runs, so it waits for the archive to settle.
	if s.rcaArchiveFinalizing(test.ID) {
		writeError(w, http.StatusConflict, "the run archive is still being finalized; request the analysis once it has settled")
		return
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive manifest")
		return
	}
	archiveDigest, err := rcaEvidenceDigest(manifest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	manifestDigest, err := rcaManifestDigest(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	inputs := rca.InputDigests{Archive: archiveDigest, Engine: rcaEngineVersion, SignatureSet: signatures.Version}
	if request.BaselineTestID != "" {
		baseline, status, message := s.rcaBaselineArchive(r.Context(), test.ID, project, request.BaselineTestID)
		if status != 0 {
			writeError(w, status, message)
			return
		}
		inputs.Baseline = baseline
	}
	// Evidence is captured afresh when asked, when the run has none, or when
	// the archive or the project's mapping decisions changed since the latest
	// capture; otherwise the latest is reused, so identical requests share
	// one evidence revision.
	var reuse func(latest, current rcaEvidence) bool
	if !request.Enrich {
		reuse = sameEvidence
	}
	evidence, err := captureRCAEvidence(s.store, project, test.ID, record, manifest, request.Enrich, time.Now(), reuse)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, storage.ErrRCALimit) {
			status = http.StatusConflict
		}
		writeError(w, status, "failed to capture root-cause evidence: "+err.Error())
		return
	}
	inputs.EvidenceRevision = evidence.Revision
	var frozen rcaEvidence
	if json.Unmarshal(evidence.Payload, &frozen) == nil {
		inputs.Mapping = frozen.MappingDigest
	}
	var analysis *storage.RCAAnalysis
	var created bool
	for attempt := 0; ; attempt++ {
		payload, _ := json.Marshal(rcaAnalysisPayload{Request: request, Inputs: inputs, ArchiveManifest: manifestDigest, Attempt: attempt})
		analysis, created, err = store.CreateRCAAnalysis(project, test.ID, rcaInputDigest(inputs, attempt), payload, time.Now())
		// An identical request answered by an assessment the budget cut
		// short is analyzed again: that assessment describes the controller's
		// load when it ran, not the run.
		if err != nil || created || !rcaAnalysisCutByBudget(analysis) || attempt >= storage.MaxRCARevisionsPerRun {
			break
		}
	}
	if errors.Is(err, storage.ErrRCALimit) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to queue root-cause analysis")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
		s.recordAuditEvent(r, "rca.analysis.create", "success", "rca_analysis", analysis.ID, project, map[string]any{"test_id": test.ID, "revision": analysis.Revision, "enrich": request.Enrich})
		s.wakeRCAWorker()
	}
	writeJSON(w, status, rcaAnalysisResponse(analysis))
}

// rcaBaselineArchive returns the digest of a baseline run's archive in the
// same project, or an HTTP status and message.
func (s *Server) rcaBaselineArchive(ctx context.Context, currentID, project, baselineID string) (string, int, string) {
	if baselineID == currentID {
		return "", http.StatusBadRequest, "a run cannot be its own baseline"
	}
	archives := s.archiveDomainService()
	baseline, ok := archives.GetTestSummary(baselineID)
	if !ok || baseline == nil || normalizeProjectID(baseline.ProjectID) != project {
		return "", http.StatusNotFound, "baseline run not found in project"
	}
	record, ok := archives.GetRunArchiveContext(ctx, baselineID)
	if !ok {
		return "", http.StatusNotFound, "baseline run has no archive"
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return "", http.StatusServiceUnavailable, "baseline archive unavailable"
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return "", http.StatusServiceUnavailable, "baseline archive unavailable"
	}
	digest, err := rcaEvidenceDigest(manifest)
	if err != nil {
		return "", http.StatusServiceUnavailable, "baseline archive unavailable"
	}
	return digest, 0, ""
}

// rcaAnalysisForRequest loads the analysis a request names.
func (s *Server) rcaAnalysisForRequest(w http.ResponseWriter, r *http.Request) (*storage.RCAAnalysis, bool) {
	test, ok := s.archiveDomainService().GetTestSummary(r.PathValue("id"))
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return nil, false
	}
	store, ok := s.rcaStore(w)
	if !ok {
		return nil, false
	}
	analysis, err := store.GetRCAAnalysis(normalizeProjectID(test.ProjectID), test.ID, r.PathValue("analysis_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "root-cause analysis not found")
		return nil, false
	}
	return analysis, true
}

// handleGetRCAAnalysis reports an analysis's state and, once completed, its
// assessment. Saved analyses stay readable when the project gate is off.
func (s *Server) handleGetRCAAnalysis(w http.ResponseWriter, r *http.Request) {
	if analysis, ok := s.rcaAnalysisForRequest(w, r); ok {
		writeJSON(w, http.StatusOK, s.currentRCAAnalysisResponse(r.Context(), analysis))
	}
}

// currentRCAAnalysisResponse is rcaAnalysisResponse that also says whether
// a completed analysis predates the run's current archive.
func (s *Server) currentRCAAnalysisResponse(ctx context.Context, analysis *storage.RCAAnalysis) RCAAnalysisResponse {
	out := rcaAnalysisResponse(analysis)
	if out.Assessment != nil {
		out.ArchiveChanged = s.rcaArchiveChanged(ctx, analysis.TestID, rcaAnalysisPayloadOf(analysis))
	}
	return out
}

// rcaArchiveChanged reports whether the archive evidence an analysis read is
// no longer the run's: a field or artifact the analysis reads changed. A
// manifest rewrite that only refreshed adjunct sidecars or test notes is not
// a change. A run without an archive is not judged; an archive that cannot
// be read is judged changed, so the caller re-analyzes rather than trusts.
func (s *Server) rcaArchiveChanged(ctx context.Context, testID string, payload rcaAnalysisPayload) bool {
	if payload.Inputs.Archive == "" {
		return false
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok || record == nil {
		return false
	}
	if current := strings.TrimSpace(record.ManifestSHA256); current != "" && current == payload.ArchiveManifest {
		return false
	}
	digest, err := rcaCurrentEvidenceDigest(ctx, record)
	if err != nil {
		return true
	}
	return digest != payload.Inputs.Archive
}

// rcaEvidenceDigests caches each archive manifest's evidence digest by the
// manifest's own digest, so a run's analyses are judged current without
// re-reading its manifest on every request. Each archive publication adds
// one entry; the cache is emptied once it holds rcaEvidenceDigestCacheLimit.
var (
	rcaEvidenceDigests          sync.Map
	rcaEvidenceDigestCount      atomic.Int64
	rcaEvidenceDigestCacheLimit = int64(4096)
)

// rcaCurrentEvidenceDigest is the evidence digest of the run's current
// archive manifest.
func rcaCurrentEvidenceDigest(ctx context.Context, record *storage.RunArchiveRecord) (string, error) {
	key := strings.TrimSpace(record.ManifestSHA256)
	if key != "" {
		if cached, ok := rcaEvidenceDigests.Load(key); ok {
			return cached.(string), nil
		}
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return "", err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return "", err
	}
	digest, err := rcaEvidenceDigest(manifest)
	if err != nil {
		return "", err
	}
	if key != "" {
		if rcaEvidenceDigestCount.Add(1) > rcaEvidenceDigestCacheLimit {
			rcaEvidenceDigests.Range(func(k, _ any) bool { rcaEvidenceDigests.Delete(k); return true })
			rcaEvidenceDigestCount.Store(1)
		}
		rcaEvidenceDigests.Store(key, digest)
	}
	return digest, nil
}

// handleGetRCAAssessmentV2 returns the run's latest completed assessment.
func (s *Server) handleGetRCAAssessmentV2(w http.ResponseWriter, r *http.Request) {
	test, ok := s.archiveDomainService().GetTestSummary(r.PathValue("id"))
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	store, ok := s.rcaStore(w)
	if !ok {
		return
	}
	project := normalizeProjectID(test.ProjectID)
	latest, err := store.LatestCompletedRCAAnalysis(project, test.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "root-cause analyses unavailable")
		return
	}
	if latest == nil {
		writeJSON(w, http.StatusOK, RCAAnalysisResponse{State: "none", Presented: rcaV2PresentEnabled(project), AnalysisEnabled: rcaV2ProjectEnabled(project)})
		return
	}
	writeJSON(w, http.StatusOK, s.currentRCAAnalysisResponse(r.Context(), latest))
}

// handleGetRCABundle returns a completed analysis with its evidence, within
// 256 KiB.
func (s *Server) handleGetRCABundle(w http.ResponseWriter, r *http.Request) {
	if bundle, _, ok := s.rcaBundleForRequest(w, r); ok {
		writeJSON(w, http.StatusOK, bundle)
	}
}

// rcaBundleForRequest builds the bounded bundle of the completed analysis a
// request names, or writes the error.
func (s *Server) rcaBundleForRequest(w http.ResponseWriter, r *http.Request) (RCABundle, *storage.RCAAnalysis, bool) {
	analysis, ok := s.rcaAnalysisForRequest(w, r)
	if !ok {
		return RCABundle{}, nil, false
	}
	var payload rcaAnalysisPayload
	if analysis.State != storage.RCAStateCompleted || json.Unmarshal(analysis.Payload, &payload) != nil || payload.Assessment == nil {
		writeError(w, http.StatusConflict, "root-cause analysis has not completed")
		return RCABundle{}, nil, false
	}
	bundle := RCABundle{Schema: rcaBundleSchema, AnalysisID: analysis.ID, Revision: analysis.Revision, Assessment: *payload.Assessment, Excerpts: append([]RCAEvidenceExcerpt{}, payload.Excerpts...), EvidenceRevision: payload.Inputs.EvidenceRevision}
	if store, ok := s.store.(storage.RCAStore); ok {
		if evidence, err := store.GetRCAEvidenceRevision(analysis.ProjectID, analysis.TestID, payload.Inputs.EvidenceRevision); err == nil {
			bundle.Evidence = &evidence.Payload
		}
	}
	return boundRCABundle(bundle), analysis, true
}

// rcaBundleInventoryReserve is the largest evidence inventory the bundle
// keeps room for; a larger one goes before the assessment is cut for it.
const rcaBundleInventoryReserve = 16 << 10

// boundRCABundle cuts the bundle until it fits 256 KiB: an oversized
// evidence inventory, excerpts no hypothesis cites, then the assessment as
// the stored one is cut (with the excerpts of the anomalies it drops), then
// the inventory, the remaining excerpts and last the SLA details. It marks
// what it cut.
func boundRCABundle(bundle RCABundle) RCABundle {
	fits := func() bool {
		body, err := json.Marshal(bundle)
		return err == nil && len(body) <= rcaMaxBundleBytes
	}
	if bundle.Evidence != nil && len(*bundle.Evidence) > rcaBundleInventoryReserve && !fits() {
		bundle.Evidence, bundle.Truncated = nil, true
	}
	if dropUncitedRCAExcerpts(&bundle.Assessment, &bundle.Excerpts, fits) {
		bundle.Truncated = true
	}
	if trimRCAAssessment(&bundle.Assessment, fits) {
		bundle.Truncated = true
	}
	bundle.Excerpts = citedExcerpts(&bundle.Assessment, bundle.Excerpts)
	if !fits() && bundle.Evidence != nil {
		bundle.Evidence, bundle.Truncated = nil, true
	}
	if halveRCAExcerpts(&bundle.Excerpts, fits) {
		bundle.Truncated = true
	}
	if trimRCADetails(&bundle.Assessment, fits) {
		bundle.Truncated = true
	}
	return bundle
}

// handleCancelRCAAnalysis cancels a queued or running analysis.
func (s *Server) handleCancelRCAAnalysis(w http.ResponseWriter, r *http.Request) {
	analysis, ok := s.rcaAnalysisForRequest(w, r)
	if !ok {
		return
	}
	store := s.store.(storage.RCAStore)
	cancelled, err := store.CancelRCAAnalysis(analysis.ProjectID, analysis.TestID, analysis.ID, time.Now())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to cancel root-cause analysis")
		return
	}
	if !cancelled {
		writeError(w, http.StatusConflict, "root-cause analysis has already ended")
		return
	}
	if jobs := s.rcaJobRunner(); jobs != nil {
		jobs.cancelRunning(analysis.ID)
	}
	s.recordAuditEvent(r, "rca.analysis.cancel", "success", "rca_analysis", analysis.ID, analysis.ProjectID, map[string]any{"test_id": analysis.TestID})
	if current, err := store.GetRCAAnalysis(analysis.ProjectID, analysis.TestID, analysis.ID); err == nil {
		analysis = current
	}
	writeJSON(w, http.StatusOK, rcaAnalysisResponse(analysis))
}

// rcaJobRunner returns the shared leased-job runner for root-cause analyses,
// or nil when the store keeps no analyses.
func (s *Server) rcaJobRunner() *leasedJobRunner[storage.RCAAnalysis] {
	store, ok := s.store.(storage.RCAStore)
	if !ok {
		return nil
	}
	s.rcaJobsOnce.Do(func() {
		s.rcaJobs = &leasedJobRunner[storage.RCAAnalysis]{
			name: "root-cause analysis", lease: rcaAnalysisLease, slots: rcaMaxConcurrentAnalyses,
			idOf:  func(analysis *storage.RCAAnalysis) string { return analysis.ID },
			claim: store.ClaimRCAAnalysis,
			renew: store.RenewRCAAnalysis,
			run: func(ctx context.Context, analysis *storage.RCAAnalysis, owner string, term executionLeaseTerm) error {
				return s.runRCAAnalysis(ctx, store, analysis, owner, term)
			},
			fail: func(analysis *storage.RCAAnalysis, owner string, term executionLeaseTerm, message string) error {
				var payload rcaAnalysisPayload
				_ = json.Unmarshal(analysis.Payload, &payload)
				payload.Assessment, payload.Excerpts, payload.Error = nil, nil, message
				body, _ := json.Marshal(payload)
				leaseName, leaseOwner, generation := s.fencedLease(term)
				_, err := store.CompleteRCAAnalysisFenced(analysis.ID, owner, storage.RCAStateFailed, body, time.Now(), leaseName, leaseOwner, generation)
				return err
			},
		}
	})
	return s.rcaJobs
}

func (s *Server) wakeRCAWorker() {
	if jobs := s.rcaJobRunner(); jobs != nil {
		jobs.wakeUp()
	}
}

// startRCAAnalysisWorker runs queued analyses on the execution leader, at
// most two at a time.
func (s *Server) startRCAAnalysisWorker() {
	jobs := s.rcaJobRunner()
	if jobs == nil || s.runner == nil {
		return
	}
	s.rcaCancel = jobs.start(s)
}

// runNextRCAAnalysis claims the next queued analysis and runs it. claimed is
// called once an analysis is held. It reports whether one ran.
func (s *Server) runNextRCAAnalysis(ctx context.Context, claimed func()) (bool, error) {
	jobs := s.rcaJobRunner()
	if jobs == nil {
		return false, nil
	}
	return jobs.runNext(s, ctx, claimed)
}

// runRCAAnalysis assesses one claimed analysis and records the outcome. A
// cancelled, overtaken or shut-down job records nothing.
func (s *Server) runRCAAnalysis(ctx context.Context, store storage.RCAStore, analysis *storage.RCAAnalysis, owner string, term executionLeaseTerm) error {
	var payload rcaAnalysisPayload
	_ = json.Unmarshal(analysis.Payload, &payload)
	state := storage.RCAStateCompleted
	var excerpts []RCAEvidenceExcerpt
	assessment, runErr := recoverRCAAssessment(analysis.ID, func() (*rca.Assessment, error) {
		assessment, taken, err := s.assessRCAAnalysisWithExcerpts(ctx, analysis, payload)
		excerpts = taken
		return assessment, err
	})
	if runErr != nil && ctx.Err() != nil {
		// Interrupted by shutdown, cancellation or a lost lease: leave the
		// analysis to be re-claimed when its lease ends, or to stay cancelled.
		return nil
	}
	if runErr != nil {
		state, payload.Error = storage.RCAStateFailed, runErr.Error()
	} else {
		payload.Assessment, payload.Excerpts = assessment, excerpts
	}
	body, err := boundStoredRCAPayload(&payload)
	if err != nil {
		state, payload.Assessment, payload.Error = storage.RCAStateFailed, nil, "assessment exceeds the stored size limit"
		body, _ = json.Marshal(payload)
	}
	leaseName, leaseOwner, generation := s.fencedLease(term)
	// A cancelled analysis, an overtaken owner or a lost term records nothing.
	_, err = store.CompleteRCAAnalysisFenced(analysis.ID, owner, state, body, time.Now(), leaseName, leaseOwner, generation)
	return err
}

// recoverRCAAssessment turns a panic into a failure, so an analysis that
// cannot be assessed fails once instead of being re-claimed after every lease.
func recoverRCAAssessment(analysisID string, assess func() (*rca.Assessment, error)) (assessment *rca.Assessment, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("root-cause analysis panicked", "analysis_id", analysisID, "panic", recovered)
			assessment, err = nil, errors.New("the analysis stopped unexpectedly")
		}
	}()
	return assess()
}

// assessRCAAnalysis computes an analysis's assessment from its frozen inputs.
func (s *Server) assessRCAAnalysis(ctx context.Context, analysis *storage.RCAAnalysis, payload rcaAnalysisPayload) (*rca.Assessment, error) {
	assessment, _, err := s.assessRCAAnalysisWithExcerpts(ctx, analysis, payload)
	return assessment, err
}

// assessRCAAnalysisWithExcerpts also takes the evidence excerpts the
// assessment's references name, from the series it judged.
func (s *Server) assessRCAAnalysisWithExcerpts(ctx context.Context, analysis *storage.RCAAnalysis, payload rcaAnalysisPayload) (*rca.Assessment, []RCAEvidenceExcerpt, error) {
	// Reading the archive is bounded by its size, not by the engine's
	// budget, which starts once the evidence is in hand: a slow download or
	// a long pass over the rows must not spend the time the engine's stages
	// need and turn a sound archive into an insufficient-evidence verdict
	// that identical requests would then be answered with.
	readCtx, cancelRead := context.WithTimeout(ctx, rcaMaxEvidenceReadTime)
	defer cancelRead()
	readErr := func(err error) error {
		if readCtx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("reading the run's archive evidence took longer than %s; retry the analysis when the controller is less busy", rcaMaxEvidenceReadTime)
		}
		return err
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(readCtx, analysis.TestID)
	if !ok {
		return nil, nil, errors.New("run archive not found")
	}
	lease, err := acquireArchiveMaterialized(readCtx, record)
	if err != nil {
		return nil, nil, readErr(fmt.Errorf("materialize archive: %w", err))
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, nil, err
	}
	digest, err := rcaEvidenceDigest(manifest)
	if err != nil {
		return nil, nil, err
	}
	if digest != payload.Inputs.Archive {
		return nil, nil, errRCAArchiveChanged
	}
	summaries, err := readAllArchiveSummariesContext(readCtx, record)
	if err != nil {
		return nil, nil, readErr(err)
	}
	resources, err := readAllArchiveRecordsContext[ResourceSample](readCtx, record, func(m *RunArchiveManifest) string { return m.ResourceSamplesFile })
	if err != nil {
		return nil, nil, readErr(err)
	}
	evidence, frozen := s.frozenRCAEvidence(analysis, payload)
	// The evidence revision is a frozen input: the mapping decisions and
	// associated datasets it holds decide what the analysis judges, so an
	// analysis whose frozen revision cannot be read fails, and can be
	// requested again, rather than judging less and being reused for that.
	// An analysis that froze no revision has none to read.
	if !frozen && payload.Inputs.EvidenceRevision != 0 {
		return nil, nil, errors.New("the analysis's frozen evidence revision could not be read")
	}
	if payload.Inputs.Mapping != "" && (!frozen || evidence.MappingDigest != payload.Inputs.Mapping) {
		return nil, nil, errors.New("the evidence revision holding the analysis's mapping decisions could not be read")
	}
	// One pass over the rows counts results for validity and collects the
	// calls the graph is built from.
	counter := newValidityResultCounter(archiveValidityIntervals(manifest, summaries))
	calls := newGraphCallCollector()
	phases := newNetworkPhaseCollector(summaries, manifest.TransactionsFile)
	if err := forEachArchiveTransactionContext(readCtx, record, func(tx *TransactionEvent) error {
		counter.add(tx.Timestamp)
		calls.add(tx)
		phases.add(tx)
		return readCtx.Err()
	}); err != nil {
		return nil, nil, readErr(err)
	}
	// The engine's budget starts here, with the evidence in hand; the
	// detection reads below check it, as the stages do.
	budget := rca.DefaultBudget
	budgeter := rca.NewBudgeter(budget, nil, rca.LiveHeapBytes)
	input := rca.AnalysisInput{
		Acceptance: archiveAcceptance(manifest),
		Validity:   archiveValidityInput(manifest, counter.counted(), summaries, resources),
	}
	observations := newGraphObservationCollector()
	if input.Detection, err = archiveDetectionInput(ctx, record, manifest, summaries, input.Validity.ArrivalModel, budgeter, budget, observations.observe); err != nil {
		return nil, nil, err
	}
	// The run's own connection timing per dependency joins the detected
	// series, for the network-phase signature.
	input.Detection.Series = append(input.Detection.Series, phases.series()...)
	if phases.truncated {
		input.Detection.Limitations = append(input.Detection.Limitations, rca.Limitation{Code: limitationPhaseEvidence, Detail: fmt.Sprintf("network timing was kept for the first %d dependencies", maxPhaseDependencies)})
	}
	// Associated analysis datasets join as imported series, at the revisions
	// the evidence froze, within what the series budget leaves.
	// Joint action-dimension slices rank last (no hypothesis uses them): the
	// series kinds read below may take the room they hold, and they give way
	// at the end.
	if frozen && len(evidence.Datasets) > 0 {
		limit := -1
		if budget.MaxSeries > 0 {
			limit = max(0, budget.MaxSeries-rcaNonJointSeries(input.Detection.Series))
		}
		imported, limitations, err := s.rcaImportedSeries(ctx, normalizeProjectID(analysis.ProjectID), analysis.TestID, evidence.Datasets, summaries, limit, budgeter, budget.MaxWallTime/rcaImportedReadShare)
		if err != nil {
			return nil, nil, err
		}
		input.Detection.Series = append(input.Detection.Series, imported...)
		input.Detection.Limitations = append(input.Detection.Limitations, limitations...)
	}
	// Browser document metrics join as their measurement tuples' final
	// document p75 series, within what the series budget leaves.
	documentLimit := -1
	if budget.MaxSeries > 0 {
		documentLimit = max(0, budget.MaxSeries-rcaNonJointSeries(input.Detection.Series))
	}
	documents, limitations, err := rcaBrowserDocumentSeries(ctx, record, manifest, summaries, documentLimit, budget.MaxSeriesPoints)
	if err != nil {
		return nil, nil, err
	}
	input.Detection.Series = append(input.Detection.Series, documents...)
	input.Detection.Limitations = append(input.Detection.Limitations, limitations...)
	input.Detection.DocumentsIncomplete, input.Detection.DocumentsUnreadable = rcaDocumentEvidenceLoss(limitations)
	// The run's pinned browser policy sets the browser baselines' effect
	// floors and baseline mode; a run without document series has no
	// browser baseline to apply it to.
	if len(documents) > 0 {
		if input.Detection.BrowserPolicy, err = s.rcaBrowserBaselinePolicy(analysis.ProjectID, manifest); err != nil {
			return nil, nil, err
		}
	}
	if budget.MaxSeries > 0 {
		var gaveWay int
		if input.Detection.Series, gaveWay = trimRCAJointSeries(input.Detection.Series, budget.MaxSeries); gaveWay > 0 {
			input.Detection.Limitations = append(input.Detection.Limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("%d joint action-dimension series gave way to imported and document series within the series budget", gaveWay)})
		}
	}
	if frozen && evidence.DatasetsTruncated {
		input.Detection.Limitations = append(input.Detection.Limitations, rca.Limitation{Code: limitationImportedDatasetsCut, Detail: fmt.Sprintf("more than %d analysis datasets are associated with the run; the first %d by ID were judged", rcaMaxImportedDatasets, rcaMaxImportedDatasets)})
	}
	input.Graph = archiveGraphInput(manifest, calls, observations, evidence.Mappings)
	input.Coverage, input.Detection.Baseline = s.rcaCoverage(ctx, payload, manifest, input, evidence, frozen, budgeter, budget)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	assessment := rca.Analyze(input, budgeter)
	assessment.AnalysisID, assessment.Revision, assessment.Inputs = analysis.ID, analysis.Revision, payload.Inputs
	return &assessment, rcaEvidenceExcerpts(&assessment, input.Detection.Series), nil
}

// archiveAcceptance is the run's SLA verdict, apart from its validity.
func archiveAcceptance(manifest *RunArchiveManifest) rca.Acceptance {
	acceptance := rca.Acceptance{Status: rca.AcceptanceUnknown, Details: append([]string{}, manifest.SLADetails...)}
	switch archiveSLAState(manifest) {
	case archiveSLAPassed:
		acceptance.Status = rca.AcceptancePassed
	case archiveSLABreached:
		acceptance.Status = rca.AcceptanceBreached
	case archiveSLANotConfigured:
		acceptance.Status = rca.AcceptanceNotConfigured
	case archiveSLANotEvaluated:
		acceptance.Details = append(acceptance.Details, "SLA criteria are configured but could not be evaluated from the archive")
	}
	return acceptance
}

// frozenRCAEvidence reads the evidence revision an analysis froze, and
// whether it could.
func (s *Server) frozenRCAEvidence(analysis *storage.RCAAnalysis, payload rcaAnalysisPayload) (rcaEvidence, bool) {
	var evidence rcaEvidence
	store, ok := s.store.(storage.RCAStore)
	if !ok {
		return evidence, false
	}
	revision, err := store.GetRCAEvidenceRevision(analysis.ProjectID, analysis.TestID, payload.Inputs.EvidenceRevision)
	if err != nil || json.Unmarshal(revision.Payload, &evidence) != nil {
		return rcaEvidence{}, false
	}
	return evidence, true
}

// rcaCoverage says how much of each kind of evidence the analysis had, and
// returns the requested baseline's detection evidence when it qualifies.
func (s *Server) rcaCoverage(ctx context.Context, payload rcaAnalysisPayload, manifest *RunArchiveManifest, input rca.AnalysisInput, evidence rcaEvidence, frozen bool, budgeter *rca.Budgeter, budget rca.Budget) ([]rca.Coverage, *rca.DetectionBaseline) {
	validity, detection, graph := input.Validity, input.Detection, input.Graph
	coverage := []rca.Coverage{}
	status := rca.CoverageComplete
	switch {
	case len(validity.Samples) == 0:
		status = rca.CoverageMissing
	case !validity.GeneratorIdentityRecorded:
		status = rca.CoveragePartial
	}
	coverage = append(coverage, rca.Coverage{Evidence: "generator_stats", Status: status, Detail: fmt.Sprintf("%d resource samples", len(validity.Samples))})
	intervalStatus := rca.CoverageComplete
	if len(validity.Intervals) == 0 {
		intervalStatus = rca.CoverageMissing
	}
	coverage = append(coverage, rca.Coverage{Evidence: "intervals", Status: intervalStatus, Detail: fmt.Sprintf("%d intervals", len(validity.Intervals))})
	transactions, timing, providers, imported, joint, documents := 0, 0, 0, 0, 0, 0
	for _, series := range detection.Series {
		switch {
		case series.Kind == rca.SeriesProvider:
			providers++
		case series.Kind == rca.SeriesImported:
			imported++
		case series.Kind == rca.SeriesBrowserDocument:
			documents++
		case strings.HasPrefix(series.ID, rca.SeriesJointPrefix):
			joint++
		case strings.HasPrefix(series.ID, rca.NodeDependency+"/"):
			timing++
		default:
			transactions++
		}
	}
	seriesStatus := rca.CoverageComplete
	switch {
	case len(detection.Series) == 0 || len(detection.Levels) == 0:
		seriesStatus = rca.CoverageMissing
	case rcaDetectionEvidenceLimited(detection.Limitations):
		seriesStatus = rca.CoveragePartial
	}
	seriesDetail := fmt.Sprintf("%d transaction series, %d dependency timing series, %d provider series, %d load levels", transactions, timing, providers, len(detection.Levels))
	if imported > 0 || joint > 0 {
		seriesDetail += fmt.Sprintf(", %d imported series, %d joint action-dimension series", imported, joint)
	}
	if documents > 0 {
		seriesDetail += fmt.Sprintf(", %d browser document series", documents)
	}
	coverage = append(coverage, rca.Coverage{Evidence: "detection_series", Status: seriesStatus, Detail: seriesDetail})
	topology := rca.CoverageComplete
	switch {
	case len(graph.Entities)+len(graph.Relations) == 0:
		topology = rca.CoverageMissing
	case len(graph.Limitations) > 0:
		topology = rca.CoveragePartial
	}
	coverage = append(coverage, rca.Coverage{Evidence: "topology", Status: topology, Detail: fmt.Sprintf("%d calls, %d provider entities, %d provider relations, %d mapping decisions", len(graph.Calls), len(graph.Entities), len(graph.Relations), len(graph.Mappings))})
	if frozen {
		names := make([]string, 0, len(evidence.Sidecars))
		for name := range evidence.Sidecars {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			coverage = append(coverage, rca.Coverage{Evidence: name, Status: map[string]string{"present": rca.CoverageComplete, "truncated": rca.CoveragePartial, "absent": rca.CoverageMissing}[evidence.Sidecars[name]]})
		}
		monitor := rca.CoverageComplete
		incomplete := 0
		for _, source := range evidence.MonitorSources {
			if source.CoverageReason != "" {
				incomplete++
			}
		}
		switch {
		case len(evidence.MonitorSources) == 0:
			monitor = rca.CoverageMissing
		case incomplete > 0:
			monitor = rca.CoveragePartial
		}
		coverage = append(coverage, rca.Coverage{Evidence: "monitor_sources", Status: monitor, Detail: fmt.Sprintf("%d sources, %d incomplete", len(evidence.MonitorSources), incomplete)})
	}
	var baseline *rca.DetectionBaseline
	if baselineID := payload.Request.BaselineTestID; baselineID != "" {
		var baselineCoverage rca.Coverage
		baselineCoverage, baseline = s.rcaBaselineEvidence(ctx, manifest, baselineID, payload.Inputs.Baseline, budgeter, budget)
		coverage = append(coverage, baselineCoverage)
	}
	return coverage, baseline
}

// rcaBaselineEvidence qualifies the requested baseline's workload against
// the run's and, unless it is unqualified, reads its load levels and series
// so detection can compare each level with the baseline's same level.
func (s *Server) rcaBaselineEvidence(ctx context.Context, manifest *RunArchiveManifest, baselineID, digest string, budgeter *rca.Budgeter, budget rca.Budget) (rca.Coverage, *rca.DetectionBaseline) {
	missing := rca.Coverage{Evidence: "baseline", Status: rca.CoverageMissing, Detail: "baseline archive unavailable"}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, baselineID)
	if !ok {
		return missing, nil
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return missing, nil
	}
	defer lease.Release()
	baselineManifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return missing, nil
	}
	if current, err := rcaEvidenceDigest(baselineManifest); err != nil || current != digest {
		missing.Detail = "baseline archive changed after the analysis was requested"
		return missing, nil
	}
	baselineKey := comparisonWorkloadKey(&baselineComparisonRun{manifest: baselineManifest}, nil, "")
	currentKey := comparisonWorkloadKey(&baselineComparisonRun{manifest: manifest}, nil, "")
	qualification := rca.QualifyBaseline(currentKey, baselineKey)
	status := rca.CoverageComplete
	if qualification.Status != rca.BaselineQualified {
		status = rca.CoveragePartial
	}
	detail := qualification.Status
	if len(qualification.Reasons) > 0 {
		detail += ": " + strings.Join(qualification.Reasons, ", ")
	}
	coverage := rca.Coverage{Evidence: "baseline", Status: status, Detail: detail}
	if qualification.Status == rca.BaselineUnqualified {
		return coverage, nil
	}
	summaries, err := readAllArchiveSummaries(record)
	if err == nil {
		var evidence rca.DetectionInput
		if evidence, err = archiveDetectionInput(ctx, record, baselineManifest, summaries, archiveArrivalModel(&baselineKey), budgeter, budget, nil); err == nil {
			// The baseline's own connection timing, so network phases can be
			// compared at the same level too.
			phases := newNetworkPhaseCollector(summaries, baselineManifest.TransactionsFile)
			if scanErr := forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
				phases.add(tx)
				return ctx.Err()
			}); scanErr == nil {
				evidence.Series = append(evidence.Series, phases.series()...)
			} else {
				evidence.Limitations = append(evidence.Limitations, rca.Limitation{Code: limitationPhaseEvidence, Detail: "the baseline's rows could not be read: " + scanErr.Error()})
			}
			// The baseline's browser document tuples, so a document series
			// is judged against the same tuple of the baseline run.
			// As for the run itself, joint slices give way to them.
			documentLimit := -1
			if budget.MaxSeries > 0 {
				documentLimit = max(0, budget.MaxSeries-rcaNonJointSeries(evidence.Series))
			}
			documentsIncomplete := false
			if documents, limitations, docErr := rcaBrowserDocumentSeries(ctx, record, baselineManifest, summaries, documentLimit, budget.MaxSeriesPoints); docErr == nil {
				evidence.Series = append(evidence.Series, documents...)
				evidence.Limitations = append(evidence.Limitations, limitations...)
				// Each limitation here says document evidence is missing:
				// dropped observations or series the budget left out.
				documentsIncomplete = len(limitations) > 0
			} else if ctx.Err() == nil {
				documentsIncomplete = true
				evidence.Limitations = append(evidence.Limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: "the baseline's browser document evidence could not be read: " + docErr.Error()})
			}
			if budget.MaxSeries > 0 {
				var gaveWay int
				if evidence.Series, gaveWay = trimRCAJointSeries(evidence.Series, budget.MaxSeries); gaveWay > 0 {
					evidence.Limitations = append(evidence.Limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("%d of the baseline's joint action-dimension series gave way to its document series within the series budget", gaveWay)})
				}
			}
			// What limited the baseline's own evidence limits every
			// comparison with it.
			if len(evidence.Limitations) > 0 {
				codes := make([]string, 0, len(evidence.Limitations))
				for _, l := range evidence.Limitations {
					codes = append(codes, l.Code)
				}
				coverage.Status = rca.CoveragePartial
				coverage.Detail += "; baseline evidence: " + strings.Join(slices.Compact(slices.Sorted(slices.Values(codes))), ", ")
			}
			return coverage, &rca.DetectionBaseline{TestID: baselineID, Qualification: qualification.Status, QualificationReasons: qualification.Reasons,
				ScriptChangedGroups: rca.ScriptChangedGroups(currentKey, baselineKey), DocumentsIncomplete: documentsIncomplete, Levels: evidence.Levels, Series: evidence.Series}
		}
	}
	coverage.Status, coverage.Detail = rca.CoveragePartial, detail+"; its series could not be read"
	return coverage, nil
}

// rcaInformationalDetectionLimitations say how the series were read, not
// that evidence was missing or cut, so they do not make the detection
// coverage partial.
var rcaInformationalDetectionLimitations = map[string]bool{limitationActionsJudged: true, limitationCumulativeSeriesRated: true}

// rcaDetectionEvidenceLimited reports whether any detection limitation says
// evidence was missing, cut or unreadable.
func rcaDetectionEvidenceLimited(limitations []rca.Limitation) bool {
	for _, l := range limitations {
		if !rcaInformationalDetectionLimitations[l.Code] {
			return true
		}
	}
	return false
}

// rcaNonJointSeries counts the series that are not joint action-dimension
// slices.
func rcaNonJointSeries(series []rca.Series) int {
	count := 0
	for _, s := range series {
		if !strings.HasPrefix(s.ID, rca.SeriesJointPrefix) {
			count++
		}
	}
	return count
}

// trimRCAJointSeries drops joint action-dimension slices, the last first,
// until at most limit series remain. It returns how many it dropped.
func trimRCAJointSeries(series []rca.Series, limit int) ([]rca.Series, int) {
	excess := len(series) - limit
	if excess <= 0 {
		return series, 0
	}
	out := make([]rca.Series, 0, len(series))
	dropped := 0
	for i := len(series) - 1; i >= 0; i-- {
		if dropped < excess && strings.HasPrefix(series[i].ID, rca.SeriesJointPrefix) {
			dropped++
			continue
		}
		out = append(out, series[i])
	}
	slices.Reverse(out)
	return out, dropped
}

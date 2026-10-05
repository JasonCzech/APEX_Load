package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Root-cause v2 schemas and limits.
const (
	rcaEvidenceSchema = "apexload.rca_evidence.v1"
	rcaBundleSchema   = "apexload.rca_bundle.v1"
	// rcaEngineVersion changes whenever the engine's output for the same
	// frozen inputs could change, so it is part of every input digest.
	rcaEngineVersion  = "apexload.rca.engine.v1+" + rca.ValidityRulesVersion + "+" + rca.DetectParamsVersion + "+" + rca.GraphSchemaVersion + "+" + signatures.Version
	rcaMaxBundleBytes = 256 << 10
	// An analysis lease outlives the engine's 120 s wall-time budget plus
	// archive reading, so a live owner is not overtaken.
	rcaAnalysisLease = 5 * time.Minute
	// At most two analyses run at once on one controller.
	rcaMaxConcurrentAnalyses = 2
)

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
	Error      string             `json:"error,omitempty"`
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
}

// RCABundle is an analysis with its evidence, bounded for agents and export.
type RCABundle struct {
	Schema           string           `json:"schema"`
	AnalysisID       string           `json:"analysis_id"`
	Revision         int64            `json:"revision"`
	Assessment       rca.Assessment   `json:"assessment"`
	EvidenceRevision int64            `json:"evidence_revision"`
	Evidence         *json.RawMessage `json:"evidence,omitempty"`
	// Truncated means windows or evidence were left out to stay within
	// 256 KiB.
	Truncated bool `json:"truncated"`
}

func rcaInputDigest(inputs rca.InputDigests) string {
	body, _ := json.Marshal(inputs)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// rcaArchiveDigest identifies an archive by its manifest.
func rcaArchiveDigest(record *storage.RunArchiveRecord) (string, error) {
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
	digest, err := rcaArchiveDigest(record)
	if err != nil {
		return nil, err
	}
	evidence := rcaEvidence{Schema: rcaEvidenceSchema, ArchiveDigest: digest, CapturedAt: now.UTC(), Enriched: enriched, MonitorSources: []rcaEvidenceSource{},
		Sidecars: map[string]string{
			"exemplars":              sidecarState(manifest.ExemplarsFile, manifest.ExemplarsTruncated),
			"provider_lookups":       sidecarState(manifest.ProviderLookupsFile, manifest.ProviderLookupsTruncated),
			"interval_distributions": sidecarState(manifest.IntervalDistributionsFile, manifest.IntervalDistributionsTruncated),
			"clock_observations":     sidecarState(manifest.ClockObservationsFile, manifest.ClockObservationsTruncated),
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

// sameEvidence reuses a revision captured from the same archive under the
// same mapping decisions.
func sameEvidence(latest, current rcaEvidence) bool {
	return latest.ArchiveDigest == current.ArchiveDigest && latest.MappingDigest == current.MappingDigest
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

func rcaAnalysisResponse(analysis *storage.RCAAnalysis) RCAAnalysisResponse {
	var payload rcaAnalysisPayload
	_ = json.Unmarshal(analysis.Payload, &payload)
	created := analysis.CreatedAt
	out := RCAAnalysisResponse{AnalysisID: analysis.ID, Revision: analysis.Revision, State: analysis.State, CreatedAt: &created, CompletedAt: analysis.CompletedAt,
		Inputs: &payload.Inputs, Error: payload.Error, Presented: rcaV2PresentEnabled(analysis.ProjectID)}
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
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive manifest")
		return
	}
	archiveDigest, err := rcaArchiveDigest(record)
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
	payload, _ := json.Marshal(rcaAnalysisPayload{Request: request, Inputs: inputs})
	analysis, created, err := store.CreateRCAAnalysis(project, test.ID, rcaInputDigest(inputs), payload, time.Now())
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
	digest, err := rcaArchiveDigest(record)
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
		writeJSON(w, http.StatusOK, rcaAnalysisResponse(analysis))
	}
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
		writeJSON(w, http.StatusOK, RCAAnalysisResponse{State: "none", Presented: rcaV2PresentEnabled(project)})
		return
	}
	writeJSON(w, http.StatusOK, rcaAnalysisResponse(latest))
}

// handleGetRCABundle returns a completed analysis with its evidence, within
// 256 KiB.
func (s *Server) handleGetRCABundle(w http.ResponseWriter, r *http.Request) {
	analysis, ok := s.rcaAnalysisForRequest(w, r)
	if !ok {
		return
	}
	var payload rcaAnalysisPayload
	if analysis.State != storage.RCAStateCompleted || json.Unmarshal(analysis.Payload, &payload) != nil || payload.Assessment == nil {
		writeError(w, http.StatusConflict, "root-cause analysis has not completed")
		return
	}
	bundle := RCABundle{Schema: rcaBundleSchema, AnalysisID: analysis.ID, Revision: analysis.Revision, Assessment: *payload.Assessment, EvidenceRevision: payload.Inputs.EvidenceRevision}
	if store, ok := s.store.(storage.RCAStore); ok {
		if evidence, err := store.GetRCAEvidenceRevision(analysis.ProjectID, analysis.TestID, payload.Inputs.EvidenceRevision); err == nil {
			bundle.Evidence = &evidence.Payload
		}
	}
	writeJSON(w, http.StatusOK, boundRCABundle(bundle))
}

// boundRCABundle trims the validity windows, the spikes, the capacity
// results, the weakest anomalies, the graph's unused and then
// lowest-confidence edges and its nodes, the lowest-ranked hypotheses, the
// evidence and then the SLA details until the bundle fits 256 KiB, and
// marks what it cut.
func boundRCABundle(bundle RCABundle) RCABundle {
	fits := func() bool {
		body, err := json.Marshal(bundle)
		return err == nil && len(body) <= rcaMaxBundleBytes
	}
	for !fits() && len(bundle.Assessment.Validity.Windows) > 0 {
		bundle.Assessment.Validity.Windows = bundle.Assessment.Validity.Windows[:len(bundle.Assessment.Validity.Windows)/2]
		bundle.Truncated = true
	}
	for !fits() && len(bundle.Assessment.Detection.Spikes) > 0 {
		bundle.Assessment.Detection.Spikes = bundle.Assessment.Detection.Spikes[:len(bundle.Assessment.Detection.Spikes)/2]
		bundle.Truncated = true
	}
	for !fits() && len(bundle.Assessment.LittlesLaw)+len(bundle.Assessment.Capacity) > 0 {
		bundle.Assessment.LittlesLaw = bundle.Assessment.LittlesLaw[:len(bundle.Assessment.LittlesLaw)/2]
		bundle.Assessment.Capacity = bundle.Assessment.Capacity[:len(bundle.Assessment.Capacity)/2]
		bundle.Truncated = true
	}
	if !fits() && len(bundle.Assessment.Anomalies) > 0 {
		// Keep the strongest anomalies, still in onset order.
		anomalies := slices.Clone(bundle.Assessment.Anomalies)
		slices.SortStableFunc(anomalies, func(a, b rca.Anomaly) int { return cmp.Compare(math.Abs(b.PeakZ), math.Abs(a.PeakZ)) })
		for !fits() && len(anomalies) > 0 {
			anomalies = anomalies[:len(anomalies)/2]
			kept := slices.Clone(anomalies)
			slices.SortStableFunc(kept, func(a, b rca.Anomaly) int { return a.Onset.Compare(b.Onset) })
			bundle.Assessment.Anomalies = kept
			bundle.Truncated = true
		}
	}
	if graph := &bundle.Assessment.Graph; !fits() && len(graph.Edges) > 0 {
		used := graph.Edges[:0:0]
		for _, e := range graph.Edges {
			if e.Used {
				used = append(used, e)
			}
		}
		graph.Edges, bundle.Truncated = used, true
		// Edges are stored strongest first.
		for !fits() && len(graph.Edges) > 0 {
			graph.Edges = graph.Edges[:len(graph.Edges)/2]
		}
		for !fits() && len(graph.Nodes) > 0 {
			graph.Nodes = graph.Nodes[:len(graph.Nodes)/2]
		}
	}
	// Hypotheses are stored best first.
	for !fits() && len(bundle.Assessment.Hypotheses) > 0 {
		bundle.Assessment.Hypotheses = bundle.Assessment.Hypotheses[:len(bundle.Assessment.Hypotheses)/2]
		bundle.Truncated = true
	}
	if !fits() && bundle.Evidence != nil {
		bundle.Evidence = nil
		bundle.Truncated = true
	}
	for !fits() && len(bundle.Assessment.Acceptance.Details) > 0 {
		bundle.Assessment.Acceptance.Details = bundle.Assessment.Acceptance.Details[:len(bundle.Assessment.Acceptance.Details)/2]
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
	if running, ok := s.rcaRunning.Load(analysis.ID); ok {
		running.(context.CancelFunc)()
	}
	s.recordAuditEvent(r, "rca.analysis.cancel", "success", "rca_analysis", analysis.ID, analysis.ProjectID, map[string]any{"test_id": analysis.TestID})
	if current, err := store.GetRCAAnalysis(analysis.ProjectID, analysis.TestID, analysis.ID); err == nil {
		analysis = current
	}
	writeJSON(w, http.StatusOK, rcaAnalysisResponse(analysis))
}

// rcaWorker holds the controller's analysis slots and wake signal.
type rcaWorker struct {
	once  sync.Once
	wake  chan struct{}
	slots chan struct{}
}

func (s *Server) rcaWorkerState() *rcaWorker {
	s.rcaWorker.once.Do(func() {
		s.rcaWorker.wake = make(chan struct{}, 1)
		s.rcaWorker.slots = make(chan struct{}, rcaMaxConcurrentAnalyses)
	})
	return &s.rcaWorker
}

func (s *Server) wakeRCAWorker() {
	select {
	case s.rcaWorkerState().wake <- struct{}{}:
	default:
	}
}

// startRCAAnalysisWorker runs queued analyses on the execution leader, at
// most two at a time.
func (s *Server) startRCAAnalysisWorker() {
	if _, ok := s.store.(storage.RCAStore); !ok || s.runner == nil {
		return
	}
	worker := s.rcaWorkerState()
	ctx, cancel := context.WithCancel(context.Background())
	s.rcaCancel = cancel
	s.launchBackground(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-worker.wake:
			}
			s.fillRCASlots(ctx, worker)
		}
	})
}

// fillRCASlots starts analyses while slots are free and work is queued.
func (s *Server) fillRCASlots(ctx context.Context, worker *rcaWorker) {
	for ctx.Err() == nil {
		select {
		case worker.slots <- struct{}{}:
		default:
			return
		}
		claimed := make(chan bool, 1)
		started := s.launchBackground(func() {
			signalled := false
			defer func() {
				// Even a panicking job must release the fill loop.
				if !signalled {
					claimed <- false
				}
				<-worker.slots
			}()
			ran, err := s.runNextRCAAnalysis(ctx, func() { signalled = true; claimed <- true })
			if err != nil {
				slog.Warn("root-cause analysis failed", "error", err)
			}
			_ = ran
		})
		if !started {
			<-worker.slots
			return
		}
		if !<-claimed {
			return
		}
	}
}

// runNextRCAAnalysis claims the next queued analysis and runs it. claimed is
// called once an analysis is held. It reports whether one ran.
func (s *Server) runNextRCAAnalysis(ctx context.Context, claimed func()) (bool, error) {
	store, ok := s.store.(storage.RCAStore)
	if !ok || s.runner == nil || !s.runner.isExecutionLeader() {
		return false, nil
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		return false, nil
	}
	owner := uuid.NewString()
	analysis, err := store.ClaimRCAAnalysis(owner, time.Now(), rcaAnalysisLease)
	if err != nil || analysis == nil {
		return false, err
	}
	if claimed != nil {
		claimed()
	}
	jobCtx, cancel := context.WithCancel(ctx)
	s.rcaRunning.Store(analysis.ID, cancel)
	// Reading a large archive can outlast one lease; renew it while the job
	// runs so another controller does not start over.
	renewed := make(chan struct{})
	go func() {
		ticker := time.NewTicker(rcaAnalysisLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-renewed:
				return
			case <-ticker.C:
				ok, err := store.RenewRCAAnalysis(analysis.ID, owner, time.Now(), rcaAnalysisLease)
				if err != nil {
					// The lease survives a missed renewal; retry at the next tick.
					slog.Warn("failed to renew root-cause analysis lease", "analysis_id", analysis.ID, "error", err)
					continue
				}
				if !ok {
					// Cancelled or overtaken: nothing this job records would stand.
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(renewed)
		s.rcaRunning.Delete(analysis.ID)
		cancel()
	}()
	var payload rcaAnalysisPayload
	_ = json.Unmarshal(analysis.Payload, &payload)
	state := storage.RCAStateCompleted
	assessment, runErr := recoverRCAAssessment(analysis.ID, func() (*rca.Assessment, error) {
		return s.assessRCAAnalysis(jobCtx, analysis, payload)
	})
	if runErr != nil && ctx.Err() != nil {
		// Interrupted by shutdown: leave the analysis to be re-claimed when
		// its lease ends.
		return true, nil
	}
	if runErr != nil {
		state, payload.Error = storage.RCAStateFailed, runErr.Error()
	} else {
		payload.Assessment = assessment
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > storage.MaxRCAPayloadBytes {
		state, payload.Assessment, payload.Error = storage.RCAStateFailed, nil, "assessment exceeds the stored size limit"
		body, _ = json.Marshal(payload)
	}
	leaseName, leaseOwner, generation := "", "", uint64(0)
	if s.runner.durableExecutionFence {
		leaseName, leaseOwner, generation = executionLeaseName, term.ownerID, term.generation
	}
	// A cancelled analysis, an overtaken owner or a lost term records nothing.
	if _, err := store.CompleteRCAAnalysisFenced(analysis.ID, owner, state, body, time.Now(), leaseName, leaseOwner, generation); err != nil {
		return true, err
	}
	return true, nil
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
	budget := rca.DefaultBudget
	budgeter := rca.NewBudgeter(budget, nil, rca.LiveHeapBytes)
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, analysis.TestID)
	if !ok {
		return nil, errors.New("run archive not found")
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("materialize archive: %w", err)
	}
	defer lease.Release()
	digest, err := rcaArchiveDigest(record)
	if err != nil {
		return nil, err
	}
	if digest != payload.Inputs.Archive {
		return nil, errRCAArchiveChanged
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, err
	}
	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		return nil, err
	}
	resources, err := readAllArchiveResources(record)
	if err != nil {
		return nil, err
	}
	evidence, frozen := s.frozenRCAEvidence(analysis, payload)
	// Mapping decisions are frozen inputs: without the revision that holds
	// them, the analysis cannot judge what it was asked to.
	if payload.Inputs.Mapping != "" && (!frozen || evidence.MappingDigest != payload.Inputs.Mapping) {
		return nil, errors.New("the evidence revision holding the analysis's mapping decisions could not be read")
	}
	// One pass over the rows counts results for validity and collects the
	// calls the graph is built from.
	counter := newValidityResultCounter(archiveValidityIntervals(manifest, summaries))
	calls := newGraphCallCollector()
	phases := newNetworkPhaseCollector(summaries, manifest.TransactionsFile)
	if err := forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		counter.add(tx.Timestamp)
		calls.add(tx)
		phases.add(tx)
		return ctx.Err()
	}); err != nil {
		return nil, err
	}
	input := rca.AnalysisInput{
		Acceptance: archiveAcceptance(manifest),
		Validity:   archiveValidityInput(manifest, counter.counted(), summaries, resources),
	}
	observations := newGraphObservationCollector()
	if input.Detection, err = archiveDetectionInput(ctx, record, manifest, summaries, input.Validity.ArrivalModel, budgeter, budget, observations.observe); err != nil {
		return nil, err
	}
	// The run's own connection timing per dependency joins the detected
	// series, for the network-phase signature.
	input.Detection.Series = append(input.Detection.Series, phases.series()...)
	if phases.truncated {
		input.Detection.Limitations = append(input.Detection.Limitations, rca.Limitation{Code: limitationPhaseEvidence, Detail: fmt.Sprintf("network timing was kept for the first %d dependencies", maxPhaseDependencies)})
	}
	input.Graph = archiveGraphInput(manifest, calls, observations, evidence.Mappings)
	input.Coverage, input.Detection.Baseline = s.rcaCoverage(ctx, payload, manifest, input, evidence, frozen, budgeter, budget)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	assessment := rca.Analyze(input, budgeter)
	assessment.AnalysisID, assessment.Revision, assessment.Inputs = analysis.ID, analysis.Revision, payload.Inputs
	return &assessment, nil
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
	transactions, providers := 0, 0
	for _, series := range detection.Series {
		if series.Kind == rca.SeriesProvider {
			providers++
		} else {
			transactions++
		}
	}
	seriesStatus := rca.CoverageComplete
	switch {
	case len(detection.Series) == 0 || len(detection.Levels) == 0:
		seriesStatus = rca.CoverageMissing
	case len(detection.Limitations) > 0:
		seriesStatus = rca.CoveragePartial
	}
	coverage = append(coverage, rca.Coverage{Evidence: "detection_series", Status: seriesStatus, Detail: fmt.Sprintf("%d transaction series, %d provider series, %d load levels", transactions, providers, len(detection.Levels))})
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
	if current, err := rcaArchiveDigest(record); err != nil || current != digest {
		missing.Detail = "baseline archive changed after the analysis was requested"
		return missing, nil
	}
	baselineManifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return missing, nil
	}
	baselineKey := comparisonWorkloadKey(&baselineComparisonRun{manifest: baselineManifest}, nil, "")
	qualification := rca.QualifyBaseline(comparisonWorkloadKey(&baselineComparisonRun{manifest: manifest}, nil, ""), baselineKey)
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
			return coverage, &rca.DetectionBaseline{TestID: baselineID, Qualification: qualification.Status, Levels: evidence.Levels, Series: evidence.Series}
		}
	}
	coverage.Status, coverage.Detail = rca.CoveragePartial, detail+"; its series could not be read"
	return coverage, nil
}

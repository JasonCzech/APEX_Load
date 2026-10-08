package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Delivery of browser findings (E1): automatic evaluation when a run's
// archive is attached, the run's latest result, and webhook transitions.
// A run's own result is always an evaluation under its pinned policy; one
// under a named revision is an ordinary evaluation job and never stands in
// for it.

const (
	browserIntelligenceSystemActor = "system:browser-intelligence"
	// maxBrowserIntelligenceSubjectJobs bounds the evaluations read for one
	// run (found by index, newest first).
	maxBrowserIntelligenceSubjectJobs = 200
	// maxBrowserFindingEventKeys bounds the finding keys one webhook event
	// lists.
	maxBrowserFindingEventKeys = 20
)

// pinnedBrowserEvaluations returns a run's evaluations under its pinned
// policy, newest first (summaries without bodies).
func pinnedBrowserEvaluations(store storage.EvaluationJobStore, projectID, runID string) ([]*storage.EvaluationJob, error) {
	return store.ListEvaluationJobsForSubject(projectID, browserIntelligenceKind, runID, maxBrowserIntelligenceSubjectJobs)
}

// autoQueueBrowserIntelligence queues a browser_intelligence evaluation for a
// run whose archive was just attached, when the run pinned a browser policy
// and requested document evidence, unless an evaluation of the same inputs
// already exists.
func (s *Server) autoQueueBrowserIntelligence(testID string) {
	if s == nil || s.runner == nil {
		return
	}
	store, ok := s.store.(storage.EvaluationJobStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	test, ok := s.runner.GetTestSummary(testID)
	if !ok || test == nil {
		return
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok || record == nil {
		return
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		slog.Warn("browser findings were not queued", "test_id", testID, "error", err)
		return
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	lease.Release()
	if err != nil || (manifest.BrowserDocumentsFile == "" && !manifest.BrowserDocumentCaptureRequested) {
		return
	}
	pinned := false
	for _, pin := range manifest.Provenance.PolicyPins {
		pinned = pinned || pin.Kind == storage.AnalysisDefinitionKindBrowserPolicy
	}
	if !pinned {
		return
	}
	project := normalizeProjectID(test.ProjectID)
	request, _ := json.Marshal(BrowserIntelligenceRequest{Run: test.ID})
	inputs, err := prepareBrowserIntelligence(ctx, s, project, request)
	if err != nil {
		slog.Warn("browser findings were not queued", "test_id", testID, "error", err)
		return
	}
	existing, err := pinnedBrowserEvaluations(store, project, test.ID)
	if err != nil {
		return
	}
	for _, job := range existing {
		if job.InputFingerprint == inputs.fingerprint && (job.State == storage.EvaluationJobQueued || job.State == storage.EvaluationJobRunning || job.State == storage.EvaluationJobReady) {
			return
		}
	}
	if _, _, err := store.CreateEvaluationJob(&storage.EvaluationJob{ID: uuid.NewString(), ProjectID: project, Actor: browserIntelligenceSystemActor, Kind: browserIntelligenceKind,
		Request: string(request), InputFingerprint: inputs.fingerprint, SubjectID: test.ID}, evaluationJobsActivePerProject); err != nil {
		slog.Warn("browser findings were not queued", "test_id", testID, "error", err)
		return
	}
	s.wakeEvaluationWorker()
}

// RunBrowserFindingsResponse is a run's latest browser findings under its
// pinned policy.
type RunBrowserFindingsResponse struct {
	RunID string `json:"run_id"`
	// Status is ready (Result is set), pending (an evaluation is queued or
	// running and none is ready), failed (the newest evaluation failed and
	// none is ready; Failure says why) or not_evaluated.
	Status      string                     `json:"status"`
	JobID       string                     `json:"job_id,omitempty"`
	CompletedAt *time.Time                 `json:"completed_at,omitempty"`
	Pending     bool                       `json:"pending,omitempty"`
	Failure     string                     `json:"failure,omitempty"`
	Result      *BrowserIntelligenceResult `json:"result,omitempty"`
}

// handleGetRunArchiveBrowserFindings returns the run's latest ready browser
// findings under its pinned policy.
func (s *Server) handleGetRunArchiveBrowserFindings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.archiveDomainService().GetTestSummary(id)
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	store, ok := s.store.(storage.EvaluationJobStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "evaluations unavailable")
		return
	}
	project := normalizeProjectID(test.ProjectID)
	jobs, err := pinnedBrowserEvaluations(store, project, test.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot list browser findings")
		return
	}
	response := RunBrowserFindingsResponse{RunID: test.ID, Status: "not_evaluated"}
	for index, summary := range jobs {
		switch summary.State {
		case storage.EvaluationJobQueued, storage.EvaluationJobRunning:
			response.Pending = true
			continue
		case storage.EvaluationJobFailed, storage.EvaluationJobExpired:
			// An expired job was claimed repeatedly without finishing; its
			// failure says so.
			if index == 0 || response.Status == "not_evaluated" && response.Failure == "" {
				response.Failure = summary.Failure
			}
			continue
		case storage.EvaluationJobReady:
		default:
			continue
		}
		job, err := store.GetEvaluationJob(project, summary.ID)
		if err != nil {
			continue
		}
		var result BrowserIntelligenceResult
		if json.Unmarshal([]byte(job.Result), &result) != nil {
			continue
		}
		response.Status, response.JobID, response.CompletedAt, response.Result = "ready", job.ID, job.CompletedAt, &result
		break
	}
	if response.Result == nil {
		switch {
		case response.Pending:
			response.Status = "pending"
		case response.Failure != "":
			response.Status = "failed"
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// notifyBrowserFindingTransitions sends webhook events when an evaluation
// under a run's pinned policy becomes ready: one browser_finding event
// listing the findings active now that were not active in the run's previous
// ready pinned evaluation, and one browser_finding_resolved event listing
// those active before and not now. A finding is identified across
// evaluations by its key, so a rerun of identical inputs sends nothing. An
// evaluation under a named revision sends nothing.
func notifyBrowserFindingTransitions(s *Server, job *storage.EvaluationJob, body []byte) {
	if s == nil || s.runner == nil || s.runner.notifier == nil || job == nil || job.SubjectID == "" || strings.HasSuffix(job.SubjectID, browserRequestedSubjectSuffix) {
		return
	}
	var current BrowserIntelligenceResult
	if json.Unmarshal(body, &current) != nil {
		return
	}
	store, ok := s.store.(storage.EvaluationJobStore)
	if !ok {
		return
	}
	jobs, err := pinnedBrowserEvaluations(store, job.ProjectID, job.SubjectID)
	if err != nil {
		return
	}
	previous := map[string]BrowserFinding{}
	for _, other := range jobs {
		// The previous ready evaluation: jobs are listed newest first, and an
		// evaluation completing late compares with the ones before it.
		if other.ID == job.ID || other.State != storage.EvaluationJobReady || !other.CreatedAt.Before(job.CreatedAt) {
			continue
		}
		full, err := store.GetEvaluationJob(job.ProjectID, other.ID)
		if err != nil {
			continue
		}
		var result BrowserIntelligenceResult
		if json.Unmarshal([]byte(full.Result), &result) != nil {
			continue
		}
		for _, finding := range result.Findings {
			if finding.State == "active" {
				previous[finding.Key] = finding
			}
		}
		break
	}
	var added, resolved []BrowserFinding
	active := map[string]bool{}
	for _, finding := range current.Findings {
		if finding.State != "active" {
			continue
		}
		active[finding.Key] = true
		if _, seen := previous[finding.Key]; !seen {
			added = append(added, finding)
		}
	}
	keys := make([]string, 0, len(previous))
	for key := range previous {
		if !active[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		resolved = append(resolved, previous[key])
	}
	test, _ := s.runner.GetTestSummary(current.RunID)
	name := current.RunID
	if test != nil && test.Name != "" {
		name = test.Name
	}
	settings := s.runner.currentNotificationSettings()
	now := time.Now().UTC()
	send := func(eventType, status string, findings []BrowserFinding) {
		if len(findings) == 0 {
			return
		}
		severity := ""
		conditions := []string{}
		for index, finding := range findings {
			if finding.Severity == "poor" || severity == "" {
				severity = finding.Severity
			}
			if index < maxBrowserFindingEventKeys {
				conditions = append(conditions, "finding:"+finding.Key+" rule:"+finding.RuleID)
			}
		}
		summary := findings[0].Summary
		if len(findings) > 1 {
			summary = fmt.Sprintf("%d browser findings; first: %s", len(findings), findings[0].Summary)
		}
		// One event per transition kind: per-destination delivery admits
		// only a few concurrent events, so a burst would be dropped. The
		// dispatcher logs a delivery it drops; false also means no webhook
		// subscribes to the event, which is not a failure.
		s.runner.notifier.Dispatch(settings, NotificationEvent{Type: eventType, TestID: current.RunID, ProjectID: job.ProjectID, TestName: name, Status: status,
			Severity: severity, Summary: summary, Conditions: conditions, Timestamp: now})
	}
	send("browser_finding", "active", added)
	send("browser_finding_resolved", "resolved", resolved)
}

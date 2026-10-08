package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Delivery of browser findings (E1): automatic evaluation when a run's
// archive is attached, the run's latest result, and webhook transitions.

const (
	browserIntelligenceSystemActor = "system:browser-intelligence"
	// maxBrowserIntelligenceJobScan bounds how many of a project's most recent
	// browser_intelligence jobs are read to find a run's.
	maxBrowserIntelligenceJobScan = 50
)

// browserIntelligenceJobsForRun returns the project's recent
// browser_intelligence jobs for one run, newest first, with their bodies.
func browserIntelligenceJobsForRun(store storage.EvaluationJobStore, projectID, runID string) ([]*storage.EvaluationJob, error) {
	summaries, err := store.ListEvaluationJobs(projectID, browserIntelligenceKind, maxBrowserIntelligenceJobScan, 0)
	if err != nil {
		return nil, err
	}
	var jobs []*storage.EvaluationJob
	for _, summary := range summaries {
		job, err := store.GetEvaluationJob(projectID, summary.ID)
		if err != nil || job == nil {
			continue
		}
		var request BrowserIntelligenceRequest
		if json.Unmarshal([]byte(job.Request), &request) != nil || request.Run != runID {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
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
	manifest, err := loadRunArchiveManifestFromRecord(record)
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
	existing, err := browserIntelligenceJobsForRun(store, project, test.ID)
	if err != nil {
		return
	}
	for _, job := range existing {
		if job.InputFingerprint == inputs.fingerprint && (job.State == storage.EvaluationJobQueued || job.State == storage.EvaluationJobRunning || job.State == storage.EvaluationJobReady) {
			return
		}
	}
	if _, _, err := store.CreateEvaluationJob(&storage.EvaluationJob{ID: uuid.NewString(), ProjectID: project, Actor: browserIntelligenceSystemActor, Kind: browserIntelligenceKind,
		Request: string(request), InputFingerprint: inputs.fingerprint}, evaluationJobsActivePerProject); err != nil {
		slog.Warn("browser findings were not queued", "test_id", testID, "error", err)
		return
	}
	s.wakeEvaluationWorker()
}

// RunBrowserFindingsResponse is a run's latest browser findings.
type RunBrowserFindingsResponse struct {
	RunID string `json:"run_id"`
	// Status is ready (Result is set), pending (an evaluation is queued or
	// running and none is ready) or not_evaluated.
	Status      string                     `json:"status"`
	JobID       string                     `json:"job_id,omitempty"`
	CompletedAt *time.Time                 `json:"completed_at,omitempty"`
	Pending     bool                       `json:"pending,omitempty"`
	Result      *BrowserIntelligenceResult `json:"result,omitempty"`
}

// handleGetRunArchiveBrowserFindings returns the run's latest ready browser
// findings.
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
	jobs, err := browserIntelligenceJobsForRun(store, normalizeProjectID(test.ProjectID), test.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot list browser findings")
		return
	}
	response := RunBrowserFindingsResponse{RunID: test.ID, Status: "not_evaluated"}
	for _, job := range jobs {
		if job.State == storage.EvaluationJobQueued || job.State == storage.EvaluationJobRunning {
			response.Pending = true
			continue
		}
		if job.State != storage.EvaluationJobReady || response.Result != nil {
			continue
		}
		var result BrowserIntelligenceResult
		if json.Unmarshal([]byte(job.Result), &result) != nil {
			continue
		}
		response.Status, response.JobID, response.CompletedAt, response.Result = "ready", job.ID, job.CompletedAt, &result
	}
	if response.Result == nil && response.Pending {
		response.Status = "pending"
	}
	writeJSON(w, http.StatusOK, response)
}

// notifyBrowserFindingTransitions sends webhook events when an evaluation
// becomes ready: browser_finding for a finding active now that was not
// active in the run's previous ready evaluation, browser_finding_resolved for
// one active before and not now. A finding is identified across evaluations
// by its key, so a rerun of identical inputs sends nothing.
func notifyBrowserFindingTransitions(s *Server, job *storage.EvaluationJob, body []byte) {
	if s == nil || s.runner == nil || s.runner.notifier == nil {
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
	previous := map[string]BrowserFinding{}
	jobs, err := browserIntelligenceJobsForRun(store, job.ProjectID, current.RunID)
	if err != nil {
		return
	}
	for _, other := range jobs {
		// The run's previous ready evaluation: jobs are listed newest first.
		if other.ID == job.ID || other.State != storage.EvaluationJobReady || !other.CreatedAt.Before(job.CreatedAt) {
			continue
		}
		var result BrowserIntelligenceResult
		if json.Unmarshal([]byte(other.Result), &result) != nil {
			continue
		}
		for _, finding := range result.Findings {
			if finding.State == "active" {
				previous[finding.Key] = finding
			}
		}
		break
	}
	test, _ := s.runner.GetTestSummary(current.RunID)
	name := current.RunID
	if test != nil && test.Name != "" {
		name = test.Name
	}
	settings := s.runner.currentNotificationSettings()
	now := time.Now().UTC()
	sent := 0
	send := func(eventType, status string, finding BrowserFinding) {
		if sent >= maxBrowserFindingNotifications {
			return
		}
		sent++
		s.runner.notifier.Dispatch(settings, NotificationEvent{Type: eventType, TestID: current.RunID, ProjectID: job.ProjectID, TestName: name, Status: status,
			Severity: finding.Severity, Summary: finding.Summary, Conditions: []string{"finding:" + finding.Key, "rule:" + finding.RuleID}, Timestamp: now})
	}
	active := map[string]bool{}
	for _, finding := range current.Findings {
		if finding.State != "active" {
			continue
		}
		active[finding.Key] = true
		if _, seen := previous[finding.Key]; !seen {
			send("browser_finding", "active", finding)
		}
	}
	resolved := make([]string, 0, len(previous))
	for key := range previous {
		if !active[key] {
			resolved = append(resolved, key)
		}
	}
	sort.Strings(resolved)
	for _, key := range resolved {
		send("browser_finding_resolved", "resolved", previous[key])
	}
	if sent >= maxBrowserFindingNotifications {
		slog.Info("browser finding notifications truncated", "test_id", current.RunID, "job_id", job.ID, "limit", fmt.Sprint(maxBrowserFindingNotifications))
	}
}

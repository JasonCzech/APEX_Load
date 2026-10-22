package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Client render evidence (C5). A dashboard tab viewing a live run reports how
// long each publication took from receipt to render, when the run's project
// opts in (settings.telemetry.client_render_evidence). Reports are
// authenticated and bounded, rate-limited per tab and given a sample budget
// per viewer (so new tab identities buy no more samples); only a visible
// tab's renders enter the run's advisory browser_render distribution, and
// the absence of any report never affects a run.

const (
	clientRenderMaxBodyBytes = 16 << 10
	clientRenderMaxSamples   = 64
	// clientRenderMinInterval is the least time between accepted reports
	// from one viewer's tab.
	clientRenderMinInterval  = 2 * time.Second
	clientRenderMaxReporters = 1024
	// A viewer's samples across all their tabs refill at
	// clientRenderViewerSamplesPerSecond up to one full report.
	clientRenderViewerSamplesPerSecond = 8
	clientRenderViewerBurst            = clientRenderMaxSamples
)

type clientRenderReport struct {
	ClientID string               `json:"client_id"`
	Samples  []clientRenderSample `json:"samples"`
}

// ClientRenderReportResult says how many samples entered the distribution.
type ClientRenderReportResult struct {
	Accepted int              `json:"accepted"`
	Excluded map[string]int64 `json:"excluded,omitempty"`
}

// clientRenderLimiter remembers each reporter's last accepted report and
// each viewer's sample budget.
type clientRenderLimiter struct {
	mu      sync.Mutex
	last    map[string]time.Time
	order   []string
	viewers map[string]*clientRenderBudget
	vorder  []string
}

type clientRenderBudget struct {
	tokens float64
	at     time.Time
}

// spend takes samples from a viewer's budget, or returns how long until it
// holds that many.
func (l *clientRenderLimiter) spend(viewer string, samples int, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.viewers == nil {
		l.viewers = map[string]*clientRenderBudget{}
	}
	budget := l.viewers[viewer]
	if budget == nil {
		budget = &clientRenderBudget{tokens: clientRenderViewerBurst, at: now}
		l.viewers[viewer] = budget
		l.vorder = append(l.vorder, viewer)
		if len(l.vorder) > clientRenderMaxReporters {
			delete(l.viewers, l.vorder[0])
			l.vorder = l.vorder[1:]
		}
	}
	if elapsed := now.Sub(budget.at); elapsed > 0 {
		budget.tokens = min(clientRenderViewerBurst, budget.tokens+elapsed.Seconds()*clientRenderViewerSamplesPerSecond)
		budget.at = now
	}
	if need := float64(samples); budget.tokens < need {
		return false, time.Duration((need - budget.tokens) / clientRenderViewerSamplesPerSecond * float64(time.Second))
	}
	budget.tokens -= float64(samples)
	return true, 0
}

// allow admits one report from key at now, or returns how long to wait.
func (l *clientRenderLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if previous, ok := l.last[key]; ok {
		if wait := clientRenderMinInterval - now.Sub(previous); wait > 0 {
			return false, wait
		}
	} else {
		l.order = append(l.order, key)
		if len(l.order) > clientRenderMaxReporters {
			delete(l.last, l.order[0])
			l.order = l.order[1:]
		}
	}
	l.last[key] = now
	return true, 0
}

// release returns samples to a viewer's budget (a report refused after
// spending).
func (l *clientRenderLimiter) release(viewer string, samples int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if budget := l.viewers[viewer]; budget != nil {
		budget.tokens = min(clientRenderViewerBurst, budget.tokens+float64(samples))
	}
}

func clientRenderEvidenceEnabled(runner *TestRunner, projectID string) bool {
	if runner == nil || runner.projectStore == nil {
		return false
	}
	project, err := runner.projectStore.GetProject(normalizeProjectID(projectID))
	return err == nil && project != nil && project.Settings.Telemetry != nil && project.Settings.Telemetry.ClientRenderEvidence
}

// decodeClientRenderReport strictly decodes one bounded report: unknown
// fields and trailing values are refused.
func decodeClientRenderReport(w http.ResponseWriter, r *http.Request, report *clientRenderReport) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, clientRenderMaxBodyBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(report)
	if err == nil {
		if trailing := decoder.Decode(&struct{}{}); !errors.Is(trailing, io.EOF) {
			err = errors.New("trailing input")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "client render report exceeds 16 KiB")
	default:
		writeError(w, http.StatusBadRequest, "invalid client render report")
	}
	return false
}

// OpenAPI-Responses: 503
func (s *Server) handleReportClientRender(w http.ResponseWriter, r *http.Request) {
	// Only the execution leader publishes a run's live metrics.
	if !s.requireExecutionLeader(w) {
		return
	}
	// A live run is in memory on the leader: resolve only its identity, never
	// a snapshot of its history.
	id := r.PathValue("id")
	recorder, testID, projectID, started, live := s.runner.liveStageRecorder(id)
	if !live {
		if summary, ok := s.runner.GetTestSummary(id); !ok || summary == nil {
			writeError(w, http.StatusNotFound, "test not found")
		} else {
			writeError(w, http.StatusConflict, "client render evidence is accepted only while a run is live")
		}
		return
	}
	var report clientRenderReport
	if !decodeClientRenderReport(w, r, &report) {
		return
	}
	report.ClientID = strings.TrimSpace(report.ClientID)
	if report.ClientID == "" || len(report.ClientID) > 64 || !utf8.ValidString(report.ClientID) || len(report.Samples) == 0 || len(report.Samples) > clientRenderMaxSamples {
		writeError(w, http.StatusBadRequest, "a client render report needs a client_id and 1 to 64 samples")
		return
	}
	// Every sample that can reach the stage spends the viewer's sample budget:
	// a visible render, and any unnamed sample. A named publication's
	// unmeasured outcome (superseded, background) reaches it only from the
	// reference tab, one report per 2 seconds, so it spends none; each report
	// still spends at least one, so rotating tab identifiers buy no reports
	// (TEL-A).
	charged := 0
	for _, sample := range report.Samples {
		if sample.Publication != nil && !sample.Publication.valid() {
			writeError(w, http.StatusBadRequest, "a client render sample names a publication by a non-empty epoch of at most 64 bytes and a positive sequence")
			return
		}
		if sample.State == "visible" || sample.Publication == nil {
			charged++
		}
	}
	charged = max(charged, 1)
	subject := "anonymous:" + requestSourceIP(r).String()
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		subject = "user:" + user.Subject
	}
	now := time.Now()
	viewer := testID + "\x00" + subject
	if allowed, wait := s.clientRenderReports.spend(viewer, charged, now); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		writeError(w, http.StatusTooManyRequests, "client render samples are limited per viewer across all tabs")
		return
	}
	if allowed, wait := s.clientRenderReports.allow(viewer+"\x00"+report.ClientID, now); !allowed {
		s.clientRenderReports.release(viewer, charged)
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		writeError(w, http.StatusTooManyRequests, "client render reports are limited to one every 2 seconds per tab")
		return
	}
	if !clientRenderEvidenceEnabled(s.runner, projectID) {
		writeError(w, http.StatusConflict, "client render evidence is not enabled for this project")
		return
	}
	result := ClientRenderReportResult{}
	var outcomes []clientRenderOutcome
	// A named publication's unmeasured outcome excludes a render from the
	// stage only for the reference client: another tab's would count again
	// publications the reference tab accounts for.
	// unmeasured holds the index, in outcomes, of each named publication's
	// unmeasured outcome: it excludes a render only when the reference tab's
	// ledger recorded it. A visible render that fails admission (stalled,
	// implausibly timed) is excluded for every tab, as an unnamed sample is.
	unmeasured := map[int]string{}
	for _, sample := range report.Samples {
		outcome := telemetryRenderRendered
		if visible, reason := admitClientRenderSample(sample, started, now); visible {
			recorder.observe(telemetryStageBrowserRender, sample.ReceiveToRenderMs)
			result.Accepted++
		} else {
			outcome = reason
			if sample.Publication != nil && sample.State != "visible" {
				unmeasured[len(outcomes)] = reason
			} else {
				recorder.exclude(telemetryStageBrowserRender, reason)
			}
			if result.Excluded == nil {
				result.Excluded = map[string]int64{}
			}
			result.Excluded[reason]++
		}
		if sample.Publication != nil {
			outcomes = append(outcomes, clientRenderOutcome{publication: *sample.Publication, outcome: outcome})
		}
	}
	// The reference client is the viewer's tab, so another viewer cannot
	// report as it by reusing its tab identifier.
	recorded := recorder.renderOutcomes(subject+"\x00"+report.ClientID, outcomes)
	for index, reason := range unmeasured {
		if recorded[index] {
			recorder.exclude(telemetryStageBrowserRender, reason)
		}
	}
	writeJSON(w, http.StatusAccepted, result)
}

// projectTelemetryPolicyRequest sets a project's telemetry opt-ins.
type projectTelemetryPolicyRequest struct {
	ClientRenderEvidence *bool `json:"client_render_evidence"`
}

// handleUpdateProjectTelemetryPolicy changes a project's telemetry opt-ins
// alone. An editor may change them (decided for C5), where the rest of a
// project's settings need a project administrator; a concurrent project
// update is retried against, never overwritten.
func (s *Server) handleUpdateProjectTelemetryPolicy(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	var req projectTelemetryPolicyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.ClientRenderEvidence == nil {
		writeError(w, http.StatusBadRequest, "client_render_evidence is required")
		return
	}
	updater, supported := s.store.(interface {
		UpdateProjectIfUnchanged(project *storage.Project, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic project updates are not supported by this storage backend")
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		project, err := s.store.GetProject(r.PathValue("id"))
		if err != nil || project == nil {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		policy := storage.ProjectTelemetryPolicy{}
		if project.Settings.Telemetry != nil {
			policy = *project.Settings.Telemetry
		}
		before := policy.ClientRenderEvidence
		policy.ClientRenderEvidence = *req.ClientRenderEvidence
		project.Settings.Telemetry = &policy
		updated, err := updater.UpdateProjectIfUnchanged(project, project.UpdatedAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update the project telemetry policy")
			return
		}
		if updated {
			s.recordAuditEvent(r, "project.telemetry_policy.update", "success", "project", project.ID, project.ID, map[string]any{"client_render_evidence_before": before, "client_render_evidence_after": policy.ClientRenderEvidence})
			writeJSON(w, http.StatusOK, project)
			return
		}
	}
	writeError(w, http.StatusConflict, "project changed while the update was in flight; reload and retry")
}

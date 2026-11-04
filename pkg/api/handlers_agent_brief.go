package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/runcontract"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Agent brief error codes: what an agent branches on.
const (
	agentErrRunNotFound        = "run_not_found"
	agentErrRunNotFinished     = "run_not_finished"
	agentErrArchiveUnavailable = "archive_unavailable"
	agentErrInvalidDetail      = "invalid_detail"
	agentErrInvalidFormat      = "invalid_format"
	agentErrInvalidBaseline    = "invalid_baseline"
	agentErrComparisonNotFound = "comparison_not_found"
	agentErrFindingNotFound    = "finding_not_found"
	agentErrForbidden          = "forbidden"
	agentErrComparisonStorage  = "comparison_storage_unavailable"
	agentErrInternal           = "internal"
)

// agentAPIError is a coded failure; the remediation tells an agent what to
// do next.
type agentAPIError struct {
	Status      int
	Code        string
	Message     string
	Remediation string
}

func writeAgentError(w http.ResponseWriter, e *agentAPIError) {
	if e.Code == agentErrInternal || e.Code == "" {
		writeJSON(w, e.Status, map[string]string{
			"error": "internal server error",
			"code":  agentErrInternal,
			"ref":   logServerErrorDetail(w, e.Status, e.Message),
		})
		return
	}
	body := map[string]string{"error": e.Message, "code": e.Code}
	if e.Remediation != "" {
		body["remediation"] = e.Remediation
	}
	writeJSON(w, e.Status, body)
}

// agentBriefRequest is a parsed brief request.
type agentBriefRequest struct {
	Detail       string
	Format       string
	Baseline     string
	ComparisonID string
	// CIClaims, on the CI route, are the caller's token claims: a baseline's
	// source context is shown only when it is the same repository.
	CIClaims *ciBearerClaims
}

func parseAgentBriefRequest(r *http.Request, formats ...string) (agentBriefRequest, *agentAPIError) {
	query := r.URL.Query()
	detail, ok := normalizeAgentDetail(query.Get("detail"))
	if !ok {
		return agentBriefRequest{}, &agentAPIError{Status: 400, Code: agentErrInvalidDetail, Message: "detail must be summary, standard or full"}
	}
	format := strings.ToLower(strings.TrimSpace(query.Get("format")))
	if format == "" {
		format = "json"
	}
	allowed := false
	for _, candidate := range formats {
		allowed = allowed || candidate == format
	}
	if !allowed {
		return agentBriefRequest{}, &agentAPIError{Status: 400, Code: agentErrInvalidFormat, Message: "format must be one of " + strings.Join(formats, ", ")}
	}
	request := agentBriefRequest{Detail: detail, Format: format, ComparisonID: strings.TrimSpace(query.Get("comparison_id"))}
	switch baseline := strings.ToLower(strings.TrimSpace(query.Get("baseline"))); {
	case request.ComparisonID != "":
		if baseline != "" && baseline != agentBaselineSelectionExplicit {
			return agentBriefRequest{}, &agentAPIError{Status: 400, Code: agentErrInvalidBaseline, Message: "comparison_id and baseline cannot both be set"}
		}
		request.Baseline = agentBaselineSelectionExplicit
	case baseline == "", baseline == agentBaselineSelectionLatest:
		request.Baseline = agentBaselineSelectionLatest
	case baseline == agentBaselineSelectionNone:
		request.Baseline = agentBaselineSelectionNone
	default:
		return agentBriefRequest{}, &agentAPIError{Status: 400, Code: agentErrInvalidBaseline, Message: "baseline must be latest or none; pass comparison_id for a specific comparison"}
	}
	return request, nil
}

// handleGetAgentBrief serves GET /api/v1/tests/{id}/archive/agent-brief.
func (s *Server) handleGetAgentBrief(w http.ResponseWriter, r *http.Request) {
	request, apiErr := parseAgentBriefRequest(r, "json", "md", "bundle", "ctrf")
	if apiErr != nil {
		writeAgentError(w, apiErr)
		return
	}
	brief, apiErr := s.buildAgentBriefForRun(r.Context(), r.PathValue("id"), request, request.Format == "ctrf")
	if apiErr != nil {
		writeAgentError(w, apiErr)
		return
	}
	if writeAgentBriefNonJSON(w, r, brief, request.Format) {
		return
	}
	writeJSON(w, http.StatusOK, brief)
}

// handleGetCIRunAgentBrief serves GET /api/v1/ci/runs/{id}/agent-brief:
// the brief under CI credentials, bound to the run's source like
// handleGetCIRun.
func (s *Server) handleGetCIRunAgentBrief(w http.ResponseWriter, r *http.Request) {
	request, apiErr := parseAgentBriefRequest(r, "json", "md", "ctrf")
	if apiErr != nil {
		writeAgentError(w, apiErr)
		return
	}
	test, ok := s.runner.GetTest(r.PathValue("id"))
	if !ok {
		writeAgentError(w, &agentAPIError{Status: 404, Code: agentErrRunNotFound, Message: "CI run not found"})
		return
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil {
		writeAgentError(w, &agentAPIError{Status: 404, Code: agentErrRunNotFound, Message: "CI run not found"})
		return
	}
	if claims, ok := ciClaimsFromContext(r.Context()); ok {
		if err := claims.matchCIRunSource(test.ProjectID, metadata.Source); err != nil {
			writeAgentError(w, &agentAPIError{Status: 403, Code: agentErrForbidden, Message: "CI token is not bound to this run"})
			return
		}
		request.CIClaims = claims
	} else if account, ok := AuthenticatedServiceAccountFromContext(r.Context()); ok {
		if err := s.validateCIRunnerServiceAccount(account, test.ProjectID); err != nil {
			writeAgentError(w, &agentAPIError{Status: 403, Code: agentErrForbidden, Message: err.Error()})
			return
		}
	}
	brief, apiErr := s.buildAgentBriefForRun(r.Context(), test.ID, request, request.Format == "ctrf")
	if apiErr != nil {
		writeAgentError(w, apiErr)
		return
	}
	if writeAgentBriefNonJSON(w, r, brief, request.Format) {
		return
	}
	writeJSON(w, http.StatusOK, brief)
}

// writeAgentBriefNonJSON sets the brief's ETag and answers a conditional
// request or a non-JSON format; it reports whether it wrote the response.
// The JSON brief itself is written by the handler, which keeps its type
// visible to the OpenAPI walker.
func writeAgentBriefNonJSON(w http.ResponseWriter, r *http.Request, brief *AgentBrief, format string) bool {
	etag := `"` + brief.Digest + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	switch format {
	case "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderAgentBriefMarkdown(brief)))
		return true
	case "bundle", "ctrf":
		writeAgentBriefAlternate(w, brief, format)
		return true
	}
	return false
}

// writeAgentBriefAlternate writes the bundle (brief plus Markdown, for the
// MCP server) and CTRF forms. It is apart from writeAgentBrief so the
// OpenAPI walker documents the brief as the route's JSON response.
func writeAgentBriefAlternate(w http.ResponseWriter, brief *AgentBrief, format string) {
	if format == "ctrf" {
		writeJSON(w, http.StatusOK, buildAgentCTRF(brief))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Brief    *AgentBrief `json:"brief"`
		Markdown string      `json:"markdown"`
	}{brief, renderAgentBriefMarkdown(brief)})
}

// buildAgentBriefForRun loads a finished run's archive report and builds
// its brief. Unbounded keeps every finding (finding lookups use it).
func (s *Server) buildAgentBriefForRun(ctx context.Context, testID string, request agentBriefRequest, unbounded bool) (*AgentBrief, *agentAPIError) {
	brief, _, apiErr := s.buildAgentBriefAndReport(ctx, testID, request, unbounded, true)
	return brief, apiErr
}

// buildAgentBriefAndReport is buildAgentBriefForRun that also returns the
// report the brief was built from, for a finding detail's series and
// samples.
func (s *Server) buildAgentBriefAndReport(ctx context.Context, testID string, request agentBriefRequest, unbounded, allowWithoutArchive bool) (*AgentBrief, *RunArchiveReport, *agentAPIError) {
	report, digest, current, apiErr := s.loadAgentBriefReport(ctx, testID)
	if apiErr != nil {
		if allowWithoutArchive && (apiErr.Code == agentErrArchiveUnavailable || apiErr.Code == agentErrInternal) {
			// A finished CI run's gate is decided without its archive; say
			// that much rather than send the agent to retry.
			if brief := s.agentBriefWithoutArchive(ctx, testID, request); brief != nil {
				return brief, &RunArchiveReport{}, nil
			}
		}
		return nil, nil, apiErr
	}
	in := agentBriefInputs{Report: report, ManifestSHA256: digest, BaselineSelection: request.Baseline}
	if request.Baseline != agentBaselineSelectionNone {
		comparison, apiErr := s.selectAgentBriefComparison(current, request)
		if apiErr != nil {
			return nil, nil, apiErr
		}
		in.Comparison = comparison
		if comparison != nil && len(comparison.Baselines) > 0 {
			baselineIDs := make([]string, 0, len(comparison.Baselines))
			for _, baseline := range comparison.Baselines {
				baselineIDs = append(baselineIDs, baseline.ID)
			}
			sort.Strings(baselineIDs)
			var source *ciRunSource
			in.BaselineFindingIDs, source = s.agentBaselineFindings(ctx, baselineIDs[0])
			if source != nil && (request.CIClaims == nil || ciSameRepository(request.CIClaims, *source)) {
				in.BaselineSource = agentSourceContextOf(*source)
			}
		}
	}
	if s.runner != nil {
		if test, ok := s.runner.GetTest(testID); ok && test != nil {
			if metadata, err := decodeCIRunMetadata(test.MaterializationKey); err == nil {
				response, err := s.agentCIEvaluation(ctx, test, digest)
				if err == nil && ciRunTerminal(response.Status) {
					in.CI = response
				}
				in.CIMetadata = &metadata
			}
		}
	}
	brief, err := buildAgentBrief(in, agentBriefOptions{Detail: request.Detail, Unbounded: unbounded})
	if err != nil {
		return nil, nil, &agentAPIError{Status: 500, Code: agentErrInternal, Message: "agent brief: " + err.Error()}
	}
	return brief, report, nil
}

// ciSameRepository says a CI token's repository is the source's, so the
// token may see that run's commit and ref.
func ciSameRepository(claims *ciBearerClaims, source ciRunSource) bool {
	if !strings.EqualFold(strings.TrimSpace(claims.Repository), strings.TrimSpace(source.Repository)) {
		return false
	}
	return claims.RepositoryID == "" || source.RepositoryID == "" || claims.RepositoryID == source.RepositoryID
}

// loadAgentBriefReport loads a finished run's report with the attachments a
// brief reads: verdict and SLA rows, identity names, root cause.
func (s *Server) loadAgentBriefReport(ctx context.Context, testID string) (*RunArchiveReport, string, *ManagedTest, *agentAPIError) {
	archives := s.archiveDomainService()
	test, ok := archives.GetTestSummary(testID)
	if !ok || test == nil {
		return nil, "", nil, &agentAPIError{Status: 404, Code: agentErrRunNotFound, Message: "test not found"}
	}
	if !isTerminalTestStatus(test.Status) {
		return nil, "", nil, &agentAPIError{Status: 409, Code: agentErrRunNotFinished, Message: "the run has not finished",
			Remediation: "wait for the run to finish (apex_wait_for_run), then request the brief again"}
	}
	record, ok := archives.GetRunArchiveContext(ctx, testID)
	if !ok {
		return nil, "", nil, &agentAPIError{Status: 409, Code: agentErrArchiveUnavailable, Message: "the run's archive is not available",
			Remediation: "the archive may still be publishing (retry shortly), or it was not written or retention removed it; the run's status is at /tests/{id}"}
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, "", nil, &agentAPIError{Status: 500, Code: agentErrInternal, Message: "failed to materialize archive: " + err.Error()}
	}
	report, err := s.archiveReadCaches().cachedRunArchiveReport(record)
	// The report is a memory snapshot: release before cross-run reads so a
	// one-archive budget cannot deadlock on the baseline's read.
	lease.Release()
	if err != nil {
		return nil, "", nil, &agentAPIError{Status: 500, Code: agentErrInternal, Message: "failed to build archive report: " + err.Error()}
	}
	attachReportVerdictAndSLA(report)
	s.attachIdentityNames(report)
	s.attachRootCause(report)
	s.attachRootCauseSummary(ctx, report)
	current, _ := archives.GetTest(testID)
	if current == nil {
		current = test
	}
	return report, record.ManifestSHA256, current, nil
}

// selectAgentBriefComparison picks the saved comparison a brief reads. A
// read never creates one: comparing is a write (POST baseline-comparisons).
func (s *Server) selectAgentBriefComparison(current *ManagedTest, request agentBriefRequest) (*BaselineComparisonSnapshot, *agentAPIError) {
	store, ok := s.store.(storage.ComparisonStore)
	if !ok {
		if request.Baseline == agentBaselineSelectionExplicit {
			return nil, &agentAPIError{Status: 503, Code: agentErrComparisonStorage, Message: "durable comparison storage is unavailable",
				Remediation: "use baseline=none, or a controller with durable storage"}
		}
		return nil, nil
	}
	id := request.ComparisonID
	if request.Baseline == agentBaselineSelectionLatest {
		err := store.VisitBaselineComparisons(current.ProjectID, current.ID, 1, func(record storage.BaselineComparisonRecord) error {
			id = record.ID
			return nil
		})
		if err != nil || id == "" {
			return nil, nil
		}
	}
	snapshot, status, message := s.loadBaselineComparisonSnapshot(store, current, id)
	if status != 0 {
		if request.Baseline == agentBaselineSelectionLatest {
			// The newest comparison cannot be opened; the brief goes without.
			return nil, nil
		}
		if status >= 500 {
			return nil, &agentAPIError{Status: status, Code: agentErrInternal, Message: message}
		}
		return nil, &agentAPIError{Status: 404, Code: agentErrComparisonNotFound, Message: message,
			Remediation: "list the run's comparisons, or create one with apex_compare_runs"}
	}
	return snapshot, nil
}

// agentBaselineFindings is a baseline run's finding ids, for new versus
// recurring, and its CI source. A baseline whose archive cannot be read
// yields no ids, so every finding stays unknown.
func (s *Server) agentBaselineFindings(ctx context.Context, baselineID string) ([]string, *ciRunSource) {
	report, digest, _, apiErr := s.loadAgentBriefReport(ctx, baselineID)
	if apiErr != nil {
		return nil, nil
	}
	var source *ciRunSource
	if s.runner != nil {
		if test, ok := s.runner.GetTest(baselineID); ok && test != nil {
			if metadata, err := decodeCIRunMetadata(test.MaterializationKey); err == nil {
				source = &metadata.Source
			}
		}
	}
	brief, err := buildAgentBrief(agentBriefInputs{Report: report, ManifestSHA256: digest, BaselineSelection: agentBaselineSelectionNone}, agentBriefOptions{Detail: agentDetailFull, Unbounded: true})
	if err != nil {
		return nil, source
	}
	ids := make([]string, 0, len(brief.Findings))
	for _, finding := range brief.Findings {
		ids = append(ids, finding.ID)
	}
	sort.Strings(ids)
	return ids, source
}

// handleGetAgentFinding serves GET
// /api/v1/tests/{id}/archive/agent-brief/findings/{finding_id}.
func (s *Server) handleGetAgentFinding(w http.ResponseWriter, r *http.Request) {
	request, apiErr := parseAgentBriefRequest(r, "json", "md", "bundle")
	if apiErr != nil {
		writeAgentError(w, apiErr)
		return
	}
	testID, findingID := r.PathValue("id"), strings.TrimSpace(r.PathValue("finding_id"))
	// The same inputs as the brief (baseline state, CI gate), so a finding
	// reads the same here as there.
	request.Detail = agentDetailFull
	// A finding needs the archive: never the archive-less fallback.
	brief, report, apiErr := s.buildAgentBriefAndReport(r.Context(), testID, request, true, false)
	if apiErr != nil {
		writeAgentError(w, apiErr)
		return
	}
	detail, ok := buildAgentFindingDetail(brief, report, findingID)
	if !ok {
		valid := make([]string, 0, 10)
		for _, finding := range brief.Findings {
			if len(valid) == 10 {
				break
			}
			valid = append(valid, finding.ID)
		}
		writeAgentError(w, &agentAPIError{Status: 404, Code: agentErrFindingNotFound, Message: "finding not found in this run",
			Remediation: "finding ids come from apex_run_brief; this run has: " + strings.Join(valid, ", ")})
		return
	}
	body, _ := json.Marshal(detail)
	etag := `"` + sha256BytesHex(body) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	switch request.Format {
	case "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderAgentFindingMarkdown(detail)))
	case "bundle":
		writeAgentFindingBundle(w, detail)
	default:
		writeJSON(w, http.StatusOK, detail)
	}
}

func writeAgentFindingBundle(w http.ResponseWriter, detail *AgentFindingDetail) {
	writeJSON(w, http.StatusOK, struct {
		Finding  *AgentFindingDetail `json:"finding"`
		Markdown string              `json:"markdown"`
	}{detail, renderAgentFindingMarkdown(detail)})
}

// agentBriefWithoutArchive is a CI run's brief when its archive cannot be
// read: the CI outcome the gate decided without it, and nothing else. It is
// nil for a run that is not a CI run or whose outcome is not settled.
func (s *Server) agentBriefWithoutArchive(ctx context.Context, testID string, request agentBriefRequest) *AgentBrief {
	if s.runner == nil {
		return nil
	}
	test, ok := s.runner.GetTest(testID)
	if !ok || test == nil {
		return nil
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil {
		return nil
	}
	response, err := s.agentCIEvaluation(ctx, test, "")
	if err != nil || !agentCIOutcomeDecidedWithoutArchive(*response) {
		return nil
	}
	brief, err := buildAgentBriefFromCIOutcome(test, *response, metadata, request.Detail)
	if err != nil {
		return nil
	}
	return brief
}

// agentCIOutcomeDecidedWithoutArchive says a CI outcome stands without the
// run's archive: only a failed or aborted run's does. A completed run's
// gate is judged on that evidence, so with the archive missing its outcome
// would be the evidence failure, not the gate.
func agentCIOutcomeDecidedWithoutArchive(response ciRunResponse) bool {
	if !runcontract.IsCIOutcome(response.Outcome) {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(response.Status)) {
	case "FAILED", "ABORTED":
		return true
	}
	return false
}

const (
	// agentCIEvaluationTTL bounds how stale a brief's CI outcome can be. A
	// finished run's outcome only moves when its evidence is re-evaluated.
	agentCIEvaluationTTL = time.Minute
	// agentCIEvaluationTimeout bounds one evaluation, which verifies the
	// archive's integrity by hashing it.
	agentCIEvaluationTimeout = 2 * time.Minute
)

// agentCIEvaluation is a finished CI run's evaluated response for briefs.
// Evaluating re-hashes the run's archive, so concurrent and repeated brief
// and finding reads share one evaluation per run, archive and minute.
func (s *Server) agentCIEvaluation(ctx context.Context, test *ManagedTest, manifestDigest string) (*ciRunResponse, error) {
	s.agentCIEvaluationsOnce.Do(func() {
		s.agentCIEvaluations = newBoundedArchiveCache[ciRunResponse](128)
	})
	key := strings.Join([]string{test.ID, manifestDigest, time.Now().UTC().Truncate(agentCIEvaluationTTL).Format(time.RFC3339)}, "|")
	return s.agentCIEvaluations.getOrLoadContext(ctx, key, func() (*ciRunResponse, error) {
		evaluationCtx, cancel := context.WithTimeout(context.Background(), agentCIEvaluationTimeout)
		defer cancel()
		response, _, err := s.evaluateCIRunResponse(evaluationCtx, test)
		if err != nil {
			return nil, err
		}
		return &response, nil
	})
}

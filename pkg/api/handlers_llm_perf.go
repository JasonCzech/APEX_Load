package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/llmperf/compare"
	"github.com/jasonczech/apexload/pkg/llmperf/dataset"
	"github.com/jasonczech/apexload/pkg/llmperf/econ"
	"github.com/jasonczech/apexload/pkg/llmperf/plan"
	"github.com/jasonczech/apexload/pkg/llmperf/synth"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// LLMPromptDatasetImportRequest is the request payload for POST /api/v1/llm/prompt-datasets/import.
type LLMPromptDatasetImportRequest struct {
	ProjectID     string  `json:"project_id,omitempty"`
	Payload       string  `json:"payload"`
	Format        string  `json:"format,omitempty"`
	MaxTurns      int     `json:"max_turns,omitempty"`
	BytesPerToken float64 `json:"bytes_per_token,omitempty"`
	ScrubPII      *bool   `json:"scrub_pii,omitempty"`
	MaxRecords    int     `json:"max_records,omitempty"`
}

// LLMCalibrationSample is one sample of character/byte length and server-reported token count.
type LLMCalibrationSample struct {
	Text   string `json:"text,omitempty"`
	Chars  int    `json:"chars,omitempty"`
	Tokens int    `json:"tokens"`
}

// LLMPromptDatasetCalibrateRequest is the request payload for POST /api/v1/llm/prompt-datasets/calibrate.
type LLMPromptDatasetCalibrateRequest struct {
	ProjectID string                 `json:"project_id,omitempty"`
	TestID    string                 `json:"test_id,omitempty"`
	Samples   []LLMCalibrationSample `json:"samples,omitempty"`
}

// LLMPromptDatasetCalibrateResponse reports the calibrated bytes-per-token ratio.
type LLMPromptDatasetCalibrateResponse struct {
	BytesPerToken float64 `json:"bytes_per_token"`
	SampleCount   int     `json:"sample_count"`
	TotalChars    int64   `json:"total_chars"`
	TotalTokens   int64   `json:"total_tokens"`
	Source        string  `json:"source"`
}

// LLMEvalSampleRecord is one redacted JSONL row exported for offline evaluation tools
// (DeepEval, promptfoo, Ragas).
type LLMEvalSampleRecord struct {
	EventID          string  `json:"event_id"`
	Timestamp        string  `json:"timestamp"`
	ActionName       string  `json:"action_name"`
	Model            string  `json:"model,omitempty"`
	Dialect          string  `json:"dialect,omitempty"`
	InputBand        string  `json:"input_band,omitempty"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TTFTMs           float64 `json:"ttft_ms,omitempty"`
	TPOTMs           float64 `json:"tpot_ms,omitempty"`
	E2EMs            float64 `json:"e2e_ms,omitempty"`
	FinishReason     string  `json:"finish_reason,omitempty"`
	Success          bool    `json:"success"`
	ErrorCode        string  `json:"error_code,omitempty"`
	OutputHash       string  `json:"output_hash,omitempty"`
	Truncated        bool    `json:"truncated,omitempty"`
	SchemaFailed     bool    `json:"schema_failed,omitempty"`
	Input            string  `json:"input,omitempty"`
	ActualOutput     string  `json:"actual_output,omitempty"`
}

func (s *Server) handleImportLLMPromptDataset(w http.ResponseWriter, r *http.Request) {
	var req LLMPromptDatasetImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scrubEnabled := req.ScrubPII == nil || *req.ScrubPII
	var redactFn func(string) string
	if scrubEnabled {
		redactFn = scrub.BodyText
	}
	res, err := dataset.Normalize([]byte(req.Payload), dataset.ImportOptions{
		Format:        req.Format,
		MaxTurns:      req.MaxTurns,
		BytesPerToken: req.BytesPerToken,
		Redact:        redactFn,
		MaxRecords:    req.MaxRecords,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSynthesizeLLMPromptDataset(w http.ResponseWriter, r *http.Request) {
	var req synth.Config
	if !decodeJSONBody(w, r, &req) {
		return
	}
	res, err := synth.Generate(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCalibrateLLMPromptDataset(w http.ResponseWriter, r *http.Request) {
	var req LLMPromptDatasetCalibrateRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var totalChars, totalTokens int64
	var sampleCount int
	source := "samples"

	for _, sm := range req.Samples {
		chars := sm.Chars
		if chars <= 0 && sm.Text != "" {
			chars = len(sm.Text)
		}
		if chars > 0 && sm.Tokens > 0 {
			totalChars += int64(chars)
			totalTokens += int64(sm.Tokens)
			sampleCount++
		}
	}

	if !s.requireBodyTestsInRouteProject(w, r, req.TestID) {
		return
	}
	if testID := strings.TrimSpace(req.TestID); testID != "" && sampleCount == 0 {
		source = "archive"
		archives := s.archiveDomainService()
		record, ok := archives.GetRunArchiveContext(r.Context(), testID)
		if !ok {
			writeError(w, http.StatusNotFound, "run archive not found")
			return
		}
		lease, err := acquireArchiveMaterialized(r.Context(), record)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
			return
		}
		defer lease.Release()
		_ = forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
			if tx.ServerTelemetry == nil || tx.ServerTelemetry.LLM == nil {
				return nil
			}
			d := tx.ServerTelemetry.LLM
			// The completion count must be the server's: an estimate of it, or
			// of everything, would calibrate the estimator against itself.
			if (d.TokensEstimated && !d.PromptTokensEstimated) || d.ReasoningTokensEstimated || d.OutputChars <= 0 || d.ToolCallsCount > 0 {
				// A tool call's argument tokens are in the count but not in
				// the visible characters, and an estimated reasoning share
				// leaves the visible count unknown.
				return nil
			}
			// The characters are the visible answer's, so are the tokens.
			tokens := d.CompletionTokens - d.ReasoningTokens
			if tokens <= 0 {
				return nil
			}
			totalChars += int64(d.OutputChars)
			totalTokens += int64(tokens)
			sampleCount++
			return nil
		})
	}

	bpt := 4.0
	if totalChars > 0 && totalTokens > 0 {
		bpt = math.Round((float64(totalChars)/float64(totalTokens))*1000) / 1000
	} else {
		source = "default_fallback"
	}

	writeJSON(w, http.StatusOK, LLMPromptDatasetCalibrateResponse{
		BytesPerToken: bpt,
		SampleCount:   sampleCount,
		TotalChars:    totalChars,
		TotalTokens:   totalTokens,
		Source:        source,
	})
}

func (s *Server) handleEvaluateLLMCapacityPlan(w http.ResponseWriter, r *http.Request) {
	var req plan.Request
	if !decodeJSONBody(w, r, &req) {
		return
	}
	res, err := plan.Evaluate(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGetRunArchiveCapacitySweep(w http.ResponseWriter, r *http.Request) {
	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()

	testID := r.PathValue("id")
	testName := testID
	var cfg *CapacitySweepConfig
	var deployments []econ.DeploymentSpec
	archives := s.archiveDomainService()
	if test, found := archives.GetTest(testID); found {
		testName = test.Name
		if test.GoalConfig != nil && test.GoalConfig.WorkloadShapeConfig != nil {
			cfg = test.GoalConfig.WorkloadShapeConfig.CapacitySweep
		}
		if s.store != nil {
			if proj, err := s.store.GetProject(normalizeProjectID(test.ProjectID)); err == nil && proj != nil {
				deployments = proj.Settings.LLMDeployments
			}
		}
	}

	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive summaries: "+err.Error())
		return
	}
	var transactions []TransactionEvent
	_ = forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if tx != nil {
			transactions = append(transactions, *tx)
		}
		return nil
	})
	var monitorPoints []analysis.MetricPoint
	if manifest, err := loadRunArchiveManifestFromRecord(record); err == nil && manifest.MonitorEvidenceFile != "" {
		monitorPoints, _ = readAllArchiveRecords[analysis.MetricPoint](record, func(m *RunArchiveManifest) string { return m.MonitorEvidenceFile })
	}

	report := BuildCapacitySweepReport(testID, testName, cfg, summaries, transactions, monitorPoints, deployments)
	if wantsCSV(r) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-capacity-sweep-"+sanitizeFilename(testID)+".csv\"")
		_, _ = w.Write([]byte(FormatCapacitySweepCSV(report)))
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleGetRunArchiveLLMLatencyDecomposition(w http.ResponseWriter, r *http.Request) {
	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()

	testID := r.PathValue("id")
	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive summaries: "+err.Error())
		return
	}

	var llmSummary *engine.LLMSummary
	_ = forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if tx == nil || tx.BusinessTransaction || tx.Population == "transaction" {
			return nil
		}
		if tx.ServerTelemetry != nil && tx.ServerTelemetry.LLM != nil {
			engine.ObserveLLMAction(&llmSummary, &protocol.Result{
				Success:         tx.Success && tx.StatusCode < 400 && tx.ErrorMessage == "",
				StatusCode:      tx.StatusCode,
				Duration:        time.Duration(tx.DurationMs * float64(time.Millisecond)),
				ServerTelemetry: tx.ServerTelemetry,
			})
		}
		return nil
	})
	var totalTPS float64
	for _, sm := range summaries {
		if sm.TPS > totalTPS {
			totalTPS = sm.TPS
		}
		if llmSummary == nil && sm.LLM != nil {
			if sm.LLM.IntervalSummary != nil {
				engine.MergeLLMSummary(&llmSummary, sm.LLM.IntervalSummary)
			} else if sm.LLM.Summary != nil {
				engine.MergeLLMSummary(&llmSummary, sm.LLM.Summary)
			}
		}
	}

	var monitorPoints []analysis.MetricPoint
	if manifest, err := loadRunArchiveManifestFromRecord(record); err == nil && manifest.MonitorEvidenceFile != "" {
		monitorPoints, _ = readAllArchiveRecords[analysis.MetricPoint](record, func(m *RunArchiveManifest) string { return m.MonitorEvidenceFile })
	}

	decomp := BuildLLMLatencyDecomposition(testID, llmSummary, totalTPS, monitorPoints)
	writeJSON(w, http.StatusOK, decomp)
}

// llmEvalSampleRequestBody is the exported prompt of an archived LLM request.
// The engine marks a request body it scrubbed under a script that left
// config.log.redact_run_evidence off (AuthoredRequestRetained); that body keeps
// its authored values. Any other body gets the body-shape scrub.
func llmEvalSampleRequestBody(snapshot *protocol.ErrorSnapshot) string {
	if snapshot == nil {
		return ""
	}
	if snapshot.AuthoredRequestRetained {
		return snapshot.RequestBody
	}
	return scrub.BodyText(snapshot.RequestBody)
}

func (s *Server) handleExportRunArchiveLLMSamples(w http.ResponseWriter, r *http.Request) {
	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()

	testID := r.PathValue("id")
	var samples []LLMEvalSampleRecord
	_ = forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if tx == nil || tx.BusinessTransaction || tx.ServerTelemetry == nil || tx.ServerTelemetry.LLM == nil || len(samples) >= 1000 {
			return nil
		}
		d := tx.ServerTelemetry.LLM
		if !tx.Success && d.OutputChars == 0 && d.CompletionTokens == 0 && d.ToolCallsCount == 0 {
			// A request that failed before producing output (a transport
			// failure, a refused request) has nothing to evaluate.
			return nil
		}
		var hashStr string
		if d.OutputHash != 0 {
			hashStr = strings.ToLower(strings.TrimSpace(formatUint64Hex(d.OutputHash)))
		}
		var reqBody, respBody string
		if tx.Snapshot != nil {
			reqBody = llmEvalSampleRequestBody(tx.Snapshot)
			respBody = scrub.BodyText(tx.Snapshot.ResponseBody)
		}
		samples = append(samples, LLMEvalSampleRecord{
			EventID:          tx.EventID,
			Timestamp:        tx.Timestamp.UTC().Format(time.RFC3339Nano),
			ActionName:       tx.ActionName,
			Model:            d.Model,
			Dialect:          d.Dialect,
			InputBand:        d.InputLengthBand,
			PromptTokens:     d.PromptTokens,
			CompletionTokens: d.CompletionTokens,
			TTFTMs:           d.TTFTMs,
			TPOTMs:           d.TPOTMs,
			E2EMs:            d.E2EMs,
			FinishReason:     d.FinishReason,
			Success:          tx.Success,
			ErrorCode:        d.ErrorCode,
			OutputHash:       hashStr,
			Truncated:        d.Truncated,
			SchemaFailed:     d.SchemaFailed,
			Input:            reqBody,
			ActualOutput:     respBody,
		})
		return nil
	})

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "jsonl") {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-llm-samples-"+sanitizeFilename(testID)+".jsonl\"")
		enc := json.NewEncoder(w)
		for _, sm := range samples {
			_ = enc.Encode(sm)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": testID,
		"count":   len(samples),
		"samples": samples,
	})
}

func formatUint64Hex(v uint64) string {
	const hexDigits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = hexDigits[v&0xf]
		v >>= 4
	}
	return string(buf[:])
}

func (s *Server) handleGetPrometheusMetricPacks(w http.ResponseWriter, r *http.Request) {
	packs := prometheus.InferenceMetricPacks()
	packID := strings.TrimSpace(r.URL.Query().Get("pack"))
	podLabel := strings.TrimSpace(r.URL.Query().Get("pod_label"))
	if packID != "" {
		pack, ok := prometheus.GetMetricPack(packID)
		if !ok {
			writeError(w, http.StatusNotFound, "unknown prometheus metric pack: "+packID)
			return
		}
		queries, err := pack.BuildQueries(prometheus.PackRenderOptions{PodLabel: podLabel})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"pack":    pack,
			"queries": queries,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"packs": packs,
	})
}

// LLMCompareRequest compares two LLM runs or two groups within one run.
type LLMCompareRequest struct {
	TestID          string              `json:"test_id,omitempty"`
	BaselineGroup   string              `json:"baseline_group,omitempty"`
	CandidateGroup  string              `json:"candidate_group,omitempty"`
	BaselineTestID  string              `json:"baseline_test_id,omitempty"`
	CandidateTestID string              `json:"candidate_test_id,omitempty"`
	Baseline        compare.RunSnapshot `json:"baseline"`
	Candidate       compare.RunSnapshot `json:"candidate"`
	Seed            int64               `json:"seed,omitempty"`
}

func (s *Server) handleCompareLLMRuns(w http.ResponseWriter, r *http.Request) {
	var req LLMCompareRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if !s.requireBodyTestsInRouteProject(w, r, req.TestID, req.BaselineTestID, req.CandidateTestID) {
		return
	}
	baseSnap := req.Baseline
	candSnap := req.Candidate
	if strings.TrimSpace(req.TestID) != "" && req.BaselineGroup != "" && req.CandidateGroup != "" {
		var err error
		baseSnap, err = s.loadArchiveLLMSnapshot(r.Context(), req.TestID, req.BaselineGroup)
		if err != nil {
			writeError(w, http.StatusBadRequest, "baseline group: "+err.Error())
			return
		}
		candSnap, err = s.loadArchiveLLMSnapshot(r.Context(), req.TestID, req.CandidateGroup)
		if err != nil {
			writeError(w, http.StatusBadRequest, "candidate group: "+err.Error())
			return
		}
	} else {
		if strings.TrimSpace(req.BaselineTestID) != "" && baseSnap.Requests == 0 {
			snap, err := s.loadArchiveLLMSnapshot(r.Context(), req.BaselineTestID, req.BaselineGroup)
			if err != nil {
				writeError(w, http.StatusBadRequest, "baseline run: "+err.Error())
				return
			}
			baseSnap = snap
		}
		if strings.TrimSpace(req.CandidateTestID) != "" && candSnap.Requests == 0 {
			snap, err := s.loadArchiveLLMSnapshot(r.Context(), req.CandidateTestID, req.CandidateGroup)
			if err != nil {
				writeError(w, http.StatusBadRequest, "candidate run: "+err.Error())
				return
			}
			candSnap = snap
		}
	}
	res := compare.Compare(baseSnap, candSnap, req.Seed)
	writeJSON(w, http.StatusOK, res)
}

// requireBodyTestsInRouteProject requires every test a request body names to
// belong to the project the route authorized: the RBAC-bound project, or the
// query's project_id for an unscoped caller. These routes authorize only the
// query's project, so a body test ID must not reach another project's archive.
// A test elsewhere is reported as not found, like a missing one. An unscoped
// caller (global administrator, auth bypass) who names no project stays
// unrestricted, as elsewhere.
func (s *Server) requireBodyTestsInRouteProject(w http.ResponseWriter, r *http.Request, testIDs ...string) bool {
	projectID, scoped := authorizedProjectFromRequest(r)
	if !scoped {
		requested := strings.TrimSpace(r.URL.Query().Get("project_id"))
		if requested == "" {
			return true
		}
		projectID = normalizeProjectID(requested)
	}
	for _, id := range testIDs {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		test, ok := s.archiveDomainService().GetTestSummary(id)
		if !ok || test == nil || normalizeProjectID(test.ProjectID) != projectID {
			writeError(w, http.StatusNotFound, "test not found")
			return false
		}
	}
	return true
}

func (s *Server) loadArchiveLLMSnapshot(ctx context.Context, testID, groupFilter string) (compare.RunSnapshot, error) {
	archives := s.archiveDomainService()
	record, ok := archives.GetRunArchiveContext(ctx, testID)
	if !ok {
		return compare.RunSnapshot{}, fmt.Errorf("run archive not found")
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return compare.RunSnapshot{}, err
	}
	defer lease.Release()

	manifest, _ := loadRunArchiveManifestFromRecord(record)
	summaries, _ := readAllArchiveSummariesContext(ctx, record)
	var llmSummary *engine.LLMSummary
	var model string
	_ = forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if tx == nil || tx.BusinessTransaction || tx.Population == "transaction" {
			return nil
		}
		if groupFilter != "" && tx.Group != groupFilter {
			return nil
		}
		if tx.ServerTelemetry != nil && tx.ServerTelemetry.LLM != nil {
			if model == "" && tx.ServerTelemetry.LLM.Model != "" {
				model = tx.ServerTelemetry.LLM.Model
			}
			engine.ObserveLLMAction(&llmSummary, &protocol.Result{
				Success:         tx.Success && tx.StatusCode < 400 && tx.ErrorMessage == "",
				StatusCode:      tx.StatusCode,
				Duration:        time.Duration(tx.DurationMs * float64(time.Millisecond)),
				ServerTelemetry: tx.ServerTelemetry,
			})
		}
		return nil
	})
	snap := compare.RunSnapshot{
		TestID:         testID,
		Model:          model,
		WorkloadDigest: computeLLMWorkloadDigest(manifest),
	}
	if groupFilter != "" {
		snap.TestID = testID + "/" + groupFilter
	}
	for _, sm := range summaries {
		if sm.LLM != nil {
			if sm.LLM.OutputTokensPerSec > snap.SystemOutputTPS {
				snap.SystemOutputTPS = sm.LLM.OutputTokensPerSec
			}
			if ok, streamed, ttftP95, tpotP95, _, _ := extractIntervalLLMValues(sm.LLM, sm.SampleWindowSeconds); ok && streamed {
				snap.IntervalTTFTP95Ms = append(snap.IntervalTTFTP95Ms, ttftP95)
				snap.IntervalTPOTP95Ms = append(snap.IntervalTPOTP95Ms, tpotP95)
			}
		}
	}
	if llmSummary == nil && groupFilter == "" {
		for _, sm := range summaries {
			if sm.LLM == nil {
				continue
			}
			if sm.LLM.IntervalSummary != nil {
				engine.MergeLLMSummary(&llmSummary, sm.LLM.IntervalSummary)
			} else if sm.LLM.Summary != nil {
				engine.MergeLLMSummary(&llmSummary, sm.LLM.Summary)
			}
		}
	}
	if llmSummary != nil {
		engine.FinalizeLLMSummary(llmSummary)
		snap.Requests = llmSummary.Requests
		snap.TTFTP50Ms = llmSummary.P50TTFTMs
		snap.TTFTP95Ms = llmSummary.P95TTFTMs
		snap.TTFTP99Ms = llmSummary.P99TTFTMs
		snap.TPOTP50Ms = llmSummary.P50TPOTMs
		snap.TPOTP95Ms = llmSummary.P95TPOTMs
		snap.TPOTP99Ms = llmSummary.P99TPOTMs
		snap.E2EP50Ms = llmSummary.P50E2EMs
		snap.E2EP95Ms = llmSummary.P95E2EMs
		snap.E2EP99Ms = llmSummary.P99E2EMs
		if llmSummary.P50TPOTMs > 0 {
			snap.OutputTPSP50 = 1000.0 / llmSummary.P50TPOTMs
		}
		snap.PromptTokens = llmSummary.PromptTokens
		snap.OutputTokens = llmSummary.CompletionTokens
		if llmSummary.PromptTokens > 0 {
			snap.CacheHitRatio = float64(llmSummary.CachedTokens) / float64(llmSummary.PromptTokens)
		}
		snap.Truncated = llmSummary.TruncatedRequests
		snap.Throttled429 = llmSummary.ThrottledRequests
	}
	return snap, nil
}

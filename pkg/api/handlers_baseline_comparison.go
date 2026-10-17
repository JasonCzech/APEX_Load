package api

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) comparisonStore(w http.ResponseWriter) (storage.ComparisonStore, bool) {
	store, ok := s.store.(storage.ComparisonStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "durable comparison storage is unavailable")
	}
	return store, ok
}
func (s *Server) comparisonTest(w http.ResponseWriter, r *http.Request) (*ManagedTest, bool) {
	test, ok := s.archiveDomainService().GetTest(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
	}
	return test, ok
}
func baselineSameScope(a, b *ManagedTest) bool {
	return testsShareProject(a, b) && a.ApplicationID == b.ApplicationID
}

func (s *Server) handleBaselineComparisonCandidates(w http.ResponseWriter, r *http.Request) {
	current, ok := s.comparisonTest(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffsetQuery(r, 50)
	if limit > 100 {
		limit = 100
	}
	other := r.URL.Query().Get("other_scenarios") == "true"
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	candidates := []BaselineRunIdentity{}
	for _, test := range s.runner.ListTestSummariesByContext(current.ApplicationID, "", "") {
		if test == nil || test.ID == current.ID || !baselineSameScope(current, test) || !test.BaselinePinned || !canPinComparisonBaseline(test) || (!other && test.ScenarioID != current.ScenarioID) {
			continue
		}
		identity := baselineRunInfo(test)
		if s.store != nil && test.ReleaseID != "" {
			if release, e := s.store.GetRelease(test.ReleaseID); e == nil && release != nil {
				identity.ReleaseName = release.Name
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(identity.Name+" "+identity.ReleaseName+" "+identity.ID+" "+identity.ScenarioID), query) {
			continue
		}
		record, available := s.archiveDomainService().GetRunArchiveContext(r.Context(), test.ID)
		identity.ArchiveAvailable = available
		if available && record.TrendPointJSON != "" {
			var point struct {
				PeakVUsers int64 `json:"peak_active_vusers"`
			}
			if json.Unmarshal([]byte(record.TrendPointJSON), &point) == nil {
				identity.PeakVUsers = point.PeakVUsers
			}
		}
		candidates = append(candidates, identity)
	}
	sortBaselineCandidates(candidates)
	before := current.CreatedAt
	if current.StartedAt != nil {
		before = *current.StartedAt
	}
	suggested := suggestedBaselineCandidate(candidates, current.ScenarioID, before)
	total := len(candidates)
	if offset > total {
		offset = total
	}
	end := min(total, offset+limit)
	writeJSON(w, http.StatusOK, map[string]any{"runs": candidates[offset:end], "total": total, "offset": offset, "limit": limit, "suggested_baseline": suggested})
}

func sortBaselineCandidates(candidates []BaselineRunIdentity) {
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if (a.CompletedAt == nil) != (b.CompletedAt == nil) {
			return a.CompletedAt != nil
		}
		if a.CompletedAt != nil && b.CompletedAt != nil && !a.CompletedAt.Equal(*b.CompletedAt) {
			return a.CompletedAt.After(*b.CompletedAt)
		}
		return a.ID < b.ID
	})
}

// Select from all candidates before pagination, never from only the visible page.
func suggestedBaselineCandidate(candidates []BaselineRunIdentity, scenario string, before time.Time) *BaselineRunIdentity {
	for _, candidate := range candidates {
		if candidate.ScenarioID == scenario && candidate.Status == "COMPLETE" && candidate.ArchiveAvailable && candidate.CompletedAt != nil && candidate.CompletedAt.Before(before) {
			return &candidate
		}
	}
	return nil
}

func (s *Server) readBaselinePolicy(store storage.ComparisonStore, scenarioID, projectID string) (BaselineComparisonPolicy, error) {
	p := defaultBaselineComparisonPolicy()
	if scenarioID == "" {
		return p, nil
	}
	body, revision, err := store.GetComparisonPolicy(scenarioID, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(body, &p); err != nil {
		return p, err
	}
	p.Revision = revision
	return p, nil
}

func (s *Server) handleGetBaselineComparisonPolicy(w http.ResponseWriter, r *http.Request) {
	store, ok := s.comparisonStore(w)
	if !ok {
		return
	}
	scenario, ok := s.runner.GetScenario(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "scenario not found")
		return
	}
	p, err := s.readBaselinePolicy(store, scenario.ID, scenario.ProjectID)
	if err != nil {
		writeError(w, 500, "comparison policy could not be loaded")
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) handlePutBaselineComparisonPolicy(w http.ResponseWriter, r *http.Request) {
	store, ok := s.comparisonStore(w)
	if !ok {
		return
	}
	scenario, ok := s.runner.getScenarioForMutation(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "scenario not found")
		return
	}
	var p BaselineComparisonPolicy
	if !decodeJSONBody(w, r, &p) {
		return
	}
	if err := validateBaselineComparisonPolicy(p); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	payload, _ := json.Marshal(p)
	revision, err := store.SaveComparisonPolicy(scenario.ID, scenario.ProjectID, p.Revision, payload)
	if errors.Is(err, storage.ErrComparisonPolicyConflict) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "comparison policy could not be saved")
		return
	}
	p.Revision = revision
	s.recordAuditEvent(r, "scenario.comparison_policy.update", "success", "scenario", scenario.ID, scenario.ProjectID, map[string]any{"revision": revision})
	writeJSON(w, 200, p)
}

func (s *Server) handleCreateBaselineComparison(w http.ResponseWriter, r *http.Request) {
	extendArtifactWriteDeadline(w)
	store, ok := s.comparisonStore(w)
	if !ok {
		return
	}
	current, ok := s.comparisonTest(w, r)
	if !ok {
		return
	}
	var request BaselineComparisonRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if !finiteComparison(request.MinimumLoad) || !finiteComparison(request.MaximumLoad) || request.MinimumLoad < 0 || request.MaximumLoad < 0 || (request.MaximumLoad > 0 && request.MaximumLoad < request.MinimumLoad) {
		writeError(w, 400, "invalid observed load range")
		return
	}
	if !canPinComparisonBaseline(current) {
		writeError(w, 400, "current run must be terminal")
		return
	}
	if len(request.BaselineIDs) < 1 || len(request.BaselineIDs) > 5 {
		writeError(w, 400, "select between one and five baselines")
		return
	}
	if request.Population == "" {
		request.Population = "transaction"
	}
	if request.Population != "transaction" && request.Population != "action" && request.Population != "unknown" {
		writeError(w, 400, "unsupported population")
		return
	}
	if err := validateBaselineRegressionRequest(request); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, err := s.readBaselinePolicy(store, current.ScenarioID, current.ProjectID)
	if err != nil {
		writeError(w, 500, "comparison policy could not be loaded")
		return
	}
	if request.Policy != nil {
		p = *request.Policy
	}
	if err = validateBaselineComparisonPolicy(p); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	tests := []*ManagedTest{current}
	seen := map[string]bool{current.ID: true}
	for _, id := range request.BaselineIDs {
		if seen[id] {
			writeError(w, 400, "duplicate baseline or current run selected")
			return
		}
		seen[id] = true
		test, found := s.archiveDomainService().GetTest(id)
		if !found || !baselineSameScope(current, test) {
			writeError(w, 404, "baseline test not found in this application and project")
			return
		}
		if !canPinComparisonBaseline(test) {
			writeError(w, 400, "baselines must be terminal runs")
			return
		}
		if !test.BaselinePinned {
			writeError(w, 409, "a selected baseline is no longer pinned")
			return
		}
		if !request.AllowOtherScenarios && test.ScenarioID != current.ScenarioID {
			writeError(w, 400, "explicitly enable other scenarios to compare this baseline")
			return
		}
		tests = append(tests, test)
	}
	for id, window := range request.Windows {
		if !seen[id] || !finiteComparison(window.StartSeconds) || !finiteComparison(window.EndSeconds) || window.StartSeconds < 0 || window.EndSeconds < 0 || (window.EndSeconds > 0 && window.EndSeconds <= window.StartSeconds) {
			writeError(w, 400, "invalid per-run measurement window")
			return
		}
	}
	runs := make([]*baselineComparisonRun, 0, len(tests))
	distributionCells, resourcePoints := 0, 0
	for _, test := range tests {
		run, e := s.readBaselineComparisonRun(r.Context(), test, request, p)
		if e != nil {
			writeError(w, 422, fmt.Sprintf("Cannot compare run %s: %v", test.ID, e))
			return
		}
		for _, cells := range run.transactions {
			distributionCells += len(cells)
		}
		for _, series := range run.resources {
			resourcePoints += len(series.points)
		}
		if distributionCells > maxLoadComparisonDistributionCells || resourcePoints > 250000 {
			writeError(w, 422, "aggregate comparison evidence budget exceeded; select fewer runs or narrower windows")
			return
		}
		runs = append(runs, run)
	}
	result := compareBaselineRuns(runs[0], runs[1:], request, p)
	if result == nil || len(result.Transactions)+len(result.Resources) > maxBaselineComparisonCells {
		writeError(w, 422, "comparison exceeds row budget; narrow the measurement window")
		return
	}
	result.ID = uuid.NewString()
	result.ProjectID = normalizeProjectID(current.ProjectID)
	payload, err := json.Marshal(result)
	if err != nil || len(payload) > storage.MaxComparisonSnapshotBytes {
		writeError(w, 422, "comparison exceeds snapshot size budget")
		return
	}
	if err = store.SaveBaselineComparison(result.ID, current.ProjectID, current.ID, payload); err != nil {
		writeError(w, 500, "comparison could not be saved")
		return
	}
	s.recordAuditEvent(r, "test.baseline_comparison.create", "success", "test", current.ID, current.ProjectID, map[string]any{"comparison_id": result.ID, "baseline_ids": request.BaselineIDs})
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) loadBaselineComparison(w http.ResponseWriter, r *http.Request, id string) (*BaselineComparisonSnapshot, bool) {
	store, ok := s.comparisonStore(w)
	if !ok {
		return nil, false
	}
	current, ok := s.comparisonTest(w, r)
	if !ok {
		return nil, false
	}
	payload, err := store.GetBaselineComparison(id, current.ProjectID, current.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "comparison not found")
		return nil, false
	}
	if err != nil {
		writeError(w, 500, "comparison could not be loaded")
		return nil, false
	}
	var result BaselineComparisonSnapshot
	if json.Unmarshal(payload, &result) != nil {
		writeError(w, 500, "invalid comparison snapshot")
		return nil, false
	}
	for _, baseline := range result.Baselines {
		test, found := s.archiveDomainService().GetTest(baseline.ID)
		if !found || !baselineSameScope(current, test) {
			writeError(w, 404, "a referenced baseline is unavailable in this application and project")
			return nil, false
		}
	}
	return &result, true
}

// BaselineComparisonSummary lists a saved comparison without its full payload.
type BaselineComparisonSummary struct {
	ID        string                `json:"id"`
	CreatedAt time.Time             `json:"created_at"`
	Baselines []BaselineRunIdentity `json:"baselines"`
	Verdict   string                `json:"verdict,omitempty"`
}

type baselineComparisonListResponse struct {
	Comparisons []BaselineComparisonSummary `json:"comparisons"`
}

// handleListBaselineComparisons lists a run's saved comparisons, newest first,
// so the report can reopen them instead of re-running the comparison.
func (s *Server) handleListBaselineComparisons(w http.ResponseWriter, r *http.Request) {
	store, ok := s.comparisonStore(w)
	if !ok {
		return
	}
	current, ok := s.comparisonTest(w, r)
	if !ok {
		return
	}
	// Decode while the rows are open, but resolve baseline runs only after the
	// cursor closes: the lookups may hit the same single-connection store.
	var summaries []BaselineComparisonSummary
	err := store.VisitBaselineComparisons(current.ProjectID, current.ID, 20, func(record storage.BaselineComparisonRecord) error {
		// Decode only what the list shows, not the full comparison rows.
		var snapshot struct {
			CreatedAt time.Time             `json:"created_at"`
			Baselines []BaselineRunIdentity `json:"baselines"`
			Verdict   *struct {
				Status string `json:"status"`
			} `json:"verdict"`
		}
		if json.Unmarshal(record.Payload, &snapshot) != nil {
			return nil //nolint:nilerr // an unreadable saved comparison is left out of the list rather than failing it
		}
		summary := BaselineComparisonSummary{ID: record.ID, CreatedAt: record.CreatedAt, Baselines: snapshot.Baselines}
		if !snapshot.CreatedAt.IsZero() {
			summary.CreatedAt = snapshot.CreatedAt
		}
		if snapshot.Verdict != nil {
			summary.Verdict = snapshot.Verdict.Status
		}
		summaries = append(summaries, summary)
		return nil
	})
	if err != nil {
		writeError(w, 500, "comparisons could not be listed")
		return
	}
	response := baselineComparisonListResponse{Comparisons: []BaselineComparisonSummary{}}
	for _, summary := range summaries {
		// List only comparisons that can still be opened: every baseline must
		// remain visible in this run's application and project (the rule
		// loadBaselineComparison enforces), so no out-of-scope run name leaks.
		inScope := true
		for _, baseline := range summary.Baselines {
			test, found := s.archiveDomainService().GetTest(baseline.ID)
			if !found || !baselineSameScope(current, test) {
				inScope = false
				break
			}
		}
		if inScope {
			response.Comparisons = append(response.Comparisons, summary)
		}
	}
	writeJSON(w, 200, response)
}

func (s *Server) handleGetBaselineComparison(w http.ResponseWriter, r *http.Request) {
	result, ok := s.loadBaselineComparison(w, r, r.PathValue("comparison_id"))
	if !ok {
		return
	}
	if queryFormat(r) == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="baseline-comparison.csv"`)
		writer := csv.NewWriter(w)
		writeRow := func(row []string) { _ = writer.Write(spreadsheetSafeCSVRow(row)) }
		writeRow([]string{"comparison_id", "section", "baseline_id", "identity", "metric", "current", "baseline", "delta", "delta_pct", "qualification"})
		for _, metadata := range []struct {
			name  string
			value any
		}{{"current_run", result.Current}, {"baseline_runs", result.Baselines}, {"effective_policy", result.Policy}, {"request", result.Request}, {"rule_version", result.RuleVersion}, {"created_at", result.CreatedAt}, {"setup_differences", result.Setup}, {"warnings", result.Warnings}, {"verdict", result.Verdict}} {
			data, _ := json.Marshal(metadata.value)
			writeRow([]string{result.ID, "context", "", "", metadata.name, string(data), "", "", "", ""})
		}
		for _, f := range result.Findings {
			writeRow([]string{result.ID, "finding", f.BaselineID, f.RowID, f.Metric, "", "", "", "", f.Status + ": " + f.Message})
		}
		for _, section := range []struct {
			name string
			rows []BaselineComparisonRow
		}{{"transaction", result.Transactions}, {"resource", result.Resources}} {
			for _, row := range section.rows {
				for _, b := range result.Baselines {
					cell := row.Baselines[b.ID]
					a, c := "", ""
					if row.Current != nil {
						data, _ := json.Marshal(row.Current)
						a = string(data)
					}
					if cell.Measurement != nil {
						data, _ := json.Marshal(cell.Measurement)
						c = string(data)
					}
					d, pct := "", ""
					if cell.Delta != nil {
						d = fmt.Sprint(*cell.Delta)
					}
					if cell.DeltaPct != nil {
						pct = fmt.Sprint(*cell.DeltaPct)
					}
					writeRow([]string{result.ID, section.name, b.ID, row.Group + " / " + row.Name, row.Unit, a, c, d, pct, strings.Join(cell.Qualification, "; ")})
				}
			}
		}
		writer.Flush()
		return
	}
	if queryFormat(r) != "" && queryFormat(r) != "json" {
		writeError(w, 400, "unsupported comparison format")
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) attachSelectedBaselineComparison(w http.ResponseWriter, r *http.Request, report *RunArchiveReport, id string) bool {
	if id == "" {
		return true
	}
	snapshot, ok := s.loadBaselineComparison(w, r, id)
	if !ok {
		return false
	}
	report.BaselineComparison = snapshot
	return true
}

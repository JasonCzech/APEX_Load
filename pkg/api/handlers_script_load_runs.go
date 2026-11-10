package api

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recent load runs of one script, for the debugger's "Debug vs load" view.
const (
	defaultScriptLoadRunLimit = 10
	maxScriptLoadRunLimit     = 50
)

// ScriptLoadRun is one finished run whose groups execute the script.
type ScriptLoadRun struct {
	TestID           string     `json:"test_id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	ArchiveAvailable bool       `json:"archive_available"`
	// Groups names the run's groups that execute this script.
	Groups []string `json:"groups"`
	// VUsers sums those groups' configured VUsers.
	VUsers         int  `json:"vusers,omitempty"`
	ScriptShakeout bool `json:"script_shakeout,omitempty"`
}

// ScriptLoadRunsResponse lists a script's recent runs, newest first.
type ScriptLoadRunsResponse struct {
	ScriptID string          `json:"script_id"`
	Runs     []ScriptLoadRun `json:"runs"`
	Count    int             `json:"count"`
	Limit    int             `json:"limit"`
}

func (s *Server) handleListScriptLoadRuns(w http.ResponseWriter, r *http.Request) {
	limit := defaultScriptLoadRunLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxScriptLoadRunLimit {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	stored, ok := s.runner.GetScript(r.PathValue("id"))
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	runs := s.runner.listScriptLoadRuns(stored.ID, stored.RefKey, stored.ProjectID, limit)
	writeJSON(w, http.StatusOK, ScriptLoadRunsResponse{ScriptID: stored.ID, Runs: runs, Count: len(runs), Limit: limit})
}

// scriptLoadRunStatusEligible keeps completed runs, and stopped or failed
// runs only when their archive exists (their report is the comparison).
func scriptLoadRunStatusEligible(status string, archived bool) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "COMPLETED":
		return true
	case "FAILED", "ABORTED", "STOPPED":
		return archived
	}
	return false
}

// listScriptLoadRuns scans the retained runs of the script's own project
// under the read lock without cloning them. Runs in other projects are never
// returned, even when they execute the same script, so the list cannot reveal
// a project the script's reader may not see.
func (tr *TestRunner) listScriptLoadRuns(scriptID, refKey, projectID string, limit int) []ScriptLoadRun {
	tr.refreshTestsForHARead()
	projectID = normalizeProjectID(projectID)
	matches := func(id string) bool {
		id = strings.TrimSpace(id)
		return id != "" && (id == scriptID || refKey != "" && id == refKey)
	}
	tr.mu.RLock()
	runs := make([]ScriptLoadRun, 0, min(len(tr.tests), limit*2))
	for _, test := range tr.tests {
		if test == nil || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		archived := test.Archive != nil
		if !scriptLoadRunStatusEligible(test.Status, archived) {
			continue
		}
		run := ScriptLoadRun{TestID: test.ID, Name: test.Name, Status: test.Status, ArchiveAvailable: archived, ScriptShakeout: test.ScriptShakeout, Groups: []string{}}
		for _, group := range test.Groups {
			if matches(group.ScriptID) {
				run.Groups = append(run.Groups, group.Name)
				run.VUsers += max(group.VUsers, 0)
			}
		}
		if len(run.Groups) == 0 && !matches(test.ScriptID) {
			continue
		}
		run.StartedAt = cloneTimePtr(test.StartedAt)
		run.CompletedAt = cloneTimePtr(test.EndedAt)
		if run.StartedAt == nil && !test.CreatedAt.IsZero() {
			created := test.CreatedAt
			run.StartedAt = &created
		}
		runs = append(runs, run)
	}
	tr.mu.RUnlock()
	slices.SortFunc(runs, func(a, b ScriptLoadRun) int {
		at, bt := scriptLoadRunTime(a), scriptLoadRunTime(b)
		if c := bt.Compare(at); c != 0 {
			return c
		}
		return cmp.Compare(a.TestID, b.TestID)
	})
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return runs
}

func scriptLoadRunTime(run ScriptLoadRun) time.Time {
	if run.StartedAt != nil {
		return *run.StartedAt
	}
	if run.CompletedAt != nil {
		return *run.CompletedAt
	}
	return time.Time{}
}

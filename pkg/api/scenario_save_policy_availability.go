package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// This compatibility guard does not implement scenario If-Match. A strict
// policy seeded outside project HTTP APIs must not silently fall through the
// ordinary timestamp save while the complete conditioned path is unavailable.
// Final transaction admission of a concurrently activated policy remains part
// of the scenario authoring vertical; this read is not advertised as that fence.
func (s *Server) requireScenarioSavePolicyAvailable(w http.ResponseWriter, r *http.Request, selected *StoredScenario) bool {
	if s.store == nil {
		// Historical memory-only operation has no persisted project policy, but
		// an explicitly requested unsupported condition still must be refused.
		if expected, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(selected.ProjectID) != expected {
			writeError(w, http.StatusNotFound, "scenario not found")
			return false
		}
		if scenarioSaveIfMatchPresented(r.Header) {
			writeScenarioSaveCapabilityUnavailable(w, false)
			return false
		}
		return true
	}
	projectID := ""
	if reader, supported := s.store.(storage.ScenarioAuthoringStore); supported {
		snapshot, err := reader.GetScenarioAuthoringSnapshot(selected.ID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScenarioAuthoringNotFound) {
			writeError(w, http.StatusNotFound, "scenario not found")
			return false
		}
		if err != nil || snapshot == nil || snapshot.Scenario == nil || snapshot.Scenario.ID != selected.ID || strings.TrimSpace(snapshot.ProjectID) == "" {
			writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
			return false
		}
		projectID = normalizeProjectID(snapshot.ProjectID)
	} else {
		// Retain optional legacy adapters without treating their display cache as
		// policy authority. No nonce/projection is manufactured from these reads.
		durable, groups, err := getPublishedImportScenario(s.store, selected.ID)
		if err != nil || durable == nil || durable.ID != selected.ID {
			writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
			return false
		}
		var status int
		projectID, status, _ = resolveProjectIDFromScenarioScripts(groups, s.store)
		if status != 0 {
			writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
			return false
		}
	}
	if expected, scoped := authorizedProjectFromRequest(r); scoped && projectID != expected {
		writeError(w, http.StatusNotFound, "scenario not found")
		return false
	}
	if projectID != normalizeProjectID(selected.ProjectID) {
		// The existing cached save builder cannot safely reconcile a differently
		// owned durable resource. Refuse without exposing the replacement owner.
		writeError(w, http.StatusNotFound, "scenario not found")
		return false
	}
	if scenarioSaveIfMatchPresented(r.Header) {
		writeScenarioSaveCapabilityUnavailable(w, false)
		return false
	}
	project, err := s.store.GetProject(projectID)
	if err != nil || project == nil || normalizeProjectID(project.ID) != projectID {
		writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
		return false
	}
	if project.Settings.Authoring != nil && project.Settings.Authoring.RequireScenarioSaveIfMatch {
		writeScenarioSaveCapabilityUnavailable(w, true)
		return false
	}
	return true
}

// Presence matters: empty or repeated conditions cannot be discarded into an
// unconditioned save. Header maps may also contain noncanonical keys in adapters.
func scenarioSaveIfMatchPresented(header http.Header) bool {
	for key := range header {
		if strings.EqualFold(key, "If-Match") {
			return true
		}
	}
	return false
}

// ScenarioSaveCapabilityUnavailableError contains only fixed capability
// guidance and an opaque log reference, never credential or persistence detail.
type ScenarioSaveCapabilityUnavailableError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	Ref   string `json:"ref"`
}

func writeScenarioSaveCapabilityUnavailable(w http.ResponseWriter, strictPolicy bool) {
	message := "scenario If-Match saves are unavailable"
	code := "scenario_save_if_match_unavailable"
	if strictPolicy {
		message = "scenario If-Match saves are unavailable; the configured strict policy prevents this save"
		code = "scenario_save_strict_policy_unavailable"
	}
	reference := newErrorReference()
	slog.Error("scenario save capability unavailable", "ref", reference, "code", code)
	writeJSON(w, http.StatusServiceUnavailable, ScenarioSaveCapabilityUnavailableError{Error: message, Code: code, Ref: reference})
}

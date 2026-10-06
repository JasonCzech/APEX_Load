package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	scenarioSaveModeConditioned = "conditioned"
	scenarioSaveModeLegacy      = "legacy_unconditioned"
)

// admitScenarioSavePrecondition decides how an ordinary scenario PUT is fenced.
//
// Ownership is resolved before any tag is evaluated, so a foreign scenario is
// not found rather than a validator mismatch. With a durable authoring store,
// a presented strong tag becomes the storage precondition; a missing tag uses
// the revision read now with LegacyOrdinarySave, which makes the storage
// transaction re-read the project's strict policy. That in-transaction read is
// the final fence against a policy enabled while the save is in flight.
//
// Stores without durable revisions (memory-only and optional legacy adapters)
// cannot honor a condition, so a presented If-Match or a strict policy is
// refused explicitly instead of falling through to an unconditioned save.
// scenarioSaveAdmission is the fence chosen for one ordinary scenario save.
// durableUpdatedAt is the snapshot's clock; the save builder's cached scenario
// must carry the same clock or the save is refused as a concurrent change.
type scenarioSaveAdmission struct {
	expected         *storage.ScenarioAuthoringPrecondition
	mode             string
	durableUpdatedAt time.Time
}

func (s *Server) admitScenarioSavePrecondition(w http.ResponseWriter, r *http.Request, selected *StoredScenario) (scenarioSaveAdmission, bool) {
	revision, presented, parseErr := parseScenarioAuthoringIfMatch(r.Header)
	if s.store == nil {
		if expected, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(selected.ProjectID) != expected {
			writeError(w, http.StatusNotFound, "scenario not found")
			return scenarioSaveAdmission{}, false
		}
		if presented {
			writeScenarioSaveCapabilityUnavailable(w, false)
			return scenarioSaveAdmission{}, false
		}
		return scenarioSaveAdmission{mode: scenarioSaveModeLegacy}, true
	}
	if reader, supported := s.store.(storage.ScenarioAuthoringStore); supported {
		snapshot, err := reader.GetScenarioAuthoringSnapshot(selected.ID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScenarioAuthoringNotFound) {
			writeError(w, http.StatusNotFound, "scenario not found")
			return scenarioSaveAdmission{}, false
		}
		if err != nil || snapshot == nil || snapshot.Scenario == nil || snapshot.Scenario.ID != selected.ID || strings.TrimSpace(snapshot.ProjectID) == "" {
			writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
			return scenarioSaveAdmission{}, false
		}
		projectID := normalizeProjectID(snapshot.ProjectID)
		if !s.scenarioSaveOwnerAdmitted(w, r, selected, projectID) {
			return scenarioSaveAdmission{}, false
		}
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "If-Match must contain one strong scenario authoring v1 tag")
			return scenarioSaveAdmission{}, false
		}
		expected := storage.ScenarioAuthoringPrecondition{ProjectID: projectID, CreatedAt: snapshot.Scenario.CreatedAt}
		if presented {
			if revision != snapshot.ContentRevision {
				writeError(w, http.StatusPreconditionFailed, "scenario authored content changed; reload before applying changes")
				return scenarioSaveAdmission{}, false
			}
			expected.ContentRevision = revision
			return scenarioSaveAdmission{expected: &expected, mode: scenarioSaveModeConditioned, durableUpdatedAt: snapshot.Scenario.UpdatedAt}, true
		}
		// An early read gives a prompt 428; the storage transaction re-reads the
		// policy, so a concurrently enabled strict policy still refuses the save.
		project, err := s.store.GetProject(projectID)
		if err != nil || project == nil || normalizeProjectID(project.ID) != projectID {
			writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
			return scenarioSaveAdmission{}, false
		}
		if project.Settings.Authoring != nil && project.Settings.Authoring.RequireScenarioSaveIfMatch {
			writeError(w, http.StatusPreconditionRequired, "If-Match is required for scenario saves in this project")
			return scenarioSaveAdmission{}, false
		}
		expected.ContentRevision = snapshot.ContentRevision
		expected.LegacyOrdinarySave = true
		return scenarioSaveAdmission{expected: &expected, mode: scenarioSaveModeLegacy, durableUpdatedAt: snapshot.Scenario.UpdatedAt}, true
	}
	// Retain optional legacy adapters without treating their display cache as
	// policy authority. No nonce/projection is manufactured from these reads.
	durable, groups, err := getPublishedImportScenario(s.store, selected.ID)
	if err != nil || durable == nil || durable.ID != selected.ID {
		writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
		return scenarioSaveAdmission{}, false
	}
	projectID, status, _ := resolveProjectIDFromScenarioScripts(groups, s.store)
	if status != 0 {
		writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
		return scenarioSaveAdmission{}, false
	}
	if !s.scenarioSaveOwnerAdmitted(w, r, selected, projectID) {
		return scenarioSaveAdmission{}, false
	}
	if presented {
		writeScenarioSaveCapabilityUnavailable(w, false)
		return scenarioSaveAdmission{}, false
	}
	project, err := s.store.GetProject(projectID)
	if err != nil || project == nil || normalizeProjectID(project.ID) != projectID {
		writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
		return scenarioSaveAdmission{}, false
	}
	if project.Settings.Authoring != nil && project.Settings.Authoring.RequireScenarioSaveIfMatch {
		writeScenarioSaveCapabilityUnavailable(w, true)
		return scenarioSaveAdmission{}, false
	}
	return scenarioSaveAdmission{mode: scenarioSaveModeLegacy}, true
}

// scenarioSaveOwnerAdmitted hides a scenario whose durable owner differs from
// the request scope or from the cached save builder's owner.
func (s *Server) scenarioSaveOwnerAdmitted(w http.ResponseWriter, r *http.Request, selected *StoredScenario, durableProjectID string) bool {
	if expected, scoped := authorizedProjectFromRequest(r); scoped && durableProjectID != expected {
		writeError(w, http.StatusNotFound, "scenario not found")
		return false
	}
	if durableProjectID != normalizeProjectID(selected.ProjectID) {
		// The cached save builder cannot safely reconcile a differently owned
		// durable resource. Refuse without exposing the replacement owner.
		writeError(w, http.StatusNotFound, "scenario not found")
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

// refuseUnconditionedScenarioWriter guards scenario writers that cannot yet
// compare an authored-content validator inside their mutation. A presented
// If-Match is refused rather than silently ignored; callers needing
// stale-edit protection save the whole scenario through PUT /scenarios/{id}.
func refuseUnconditionedScenarioWriter(w http.ResponseWriter, r *http.Request) bool {
	if !scenarioSaveIfMatchPresented(r.Header) {
		return false
	}
	reference := newErrorReference()
	slog.Error("scenario writer condition unavailable", "ref", reference, "path", r.URL.Path)
	writeJSON(w, http.StatusServiceUnavailable, ScenarioSaveCapabilityUnavailableError{
		Error: "this scenario endpoint does not accept If-Match; save the scenario with PUT /api/v1/scenarios/{id} for stale-edit protection",
		Code:  "scenario_writer_if_match_unavailable", Ref: reference,
	})
	return true
}

// ScenarioSaveCapabilityUnavailableError contains only fixed capability
// guidance and an opaque log reference, never credential or persistence detail.
type ScenarioSaveCapabilityUnavailableError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	Ref   string `json:"ref"`
}

func writeScenarioSaveCapabilityUnavailable(w http.ResponseWriter, strictPolicy bool) {
	message := "scenario If-Match saves are unavailable without durable scenario revisions"
	code := "scenario_save_if_match_unavailable"
	if strictPolicy {
		message = "scenario If-Match saves are unavailable without durable scenario revisions; the configured strict policy prevents this save"
		code = "scenario_save_strict_policy_unavailable"
	}
	reference := newErrorReference()
	slog.Error("scenario save capability unavailable", "ref", reference, "code", code)
	writeJSON(w, http.StatusServiceUnavailable, ScenarioSaveCapabilityUnavailableError{Error: message, Code: code, Ref: reference})
}

func writeScenarioAuthoringMutationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, storage.ErrScenarioAuthoringNotFound):
		writeError(w, http.StatusNotFound, "scenario not found")
	case errors.Is(err, storage.ErrScenarioAuthoringPreconditionRequired):
		writeError(w, http.StatusPreconditionRequired, "If-Match is required for scenario saves in this project")
	case errors.Is(err, storage.ErrScenarioAuthoringConflict):
		writeError(w, http.StatusPreconditionFailed, "scenario authored content changed; reload before applying changes")
	case errors.Is(err, storage.ErrScenarioAuthoringInvalidRevision):
		writeError(w, http.StatusBadRequest, "invalid scenario authoring precondition")
	case errors.Is(err, storage.ErrScenarioAuthoringUnavailable), errors.Is(err, errScenarioAuthoringUnsupported):
		writeError(w, http.StatusServiceUnavailable, "durable scenario content revisions unavailable")
	default:
		return false
	}
	return true
}

// ScenarioAuthoringSaveOutcomeError carries fixed workflow guidance after a
// save whose durability could not be confirmed. Clients keep their draft and
// reload; they never retry automatically.
type ScenarioAuthoringSaveOutcomeError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	Ref   string `json:"ref"`
}

func writeScenarioAuthoringSaveOutcomeUnavailable(w http.ResponseWriter, unknown bool) {
	code := "scenario_save_outcome_audit_unavailable"
	if unknown {
		code = "scenario_save_outcome_unknown"
	}
	reference := newErrorReference()
	slog.Error("scenario save outcome needs verification", "ref", reference, "code", code)
	writeJSON(w, http.StatusServiceUnavailable, ScenarioAuthoringSaveOutcomeError{
		Error: "scenario save outcome requires verification; reload the scenario before retrying",
		Code:  code, Ref: reference,
	})
}

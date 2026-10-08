package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// scenarioWriterAdmission fences one secondary writer of scenario-authored
// state: DELETE, data-pool bindings, library placement, runtime
// apply-to-groups and monitor bindings. With durable content revisions the
// guard always applies. A presented strong tag becomes its precondition (412
// when stale); without one the write is unconditioned, and the project's
// strict save policy refuses it (428) inside the writer's own transaction.
// Stores without revisions keep their legacy writer, refuse a presented
// If-Match rather than ignoring it, and refuse every write while the owning
// project's strict policy is on, exactly like an ordinary scenario save.
type scenarioWriterAdmission struct {
	store storage.ScenarioGuardedWriterStore
	guard storage.ScenarioWriterGuard
}

func (a scenarioWriterAdmission) guarded() bool { return a.store != nil }

// admitScenarioWriter is called after the scenario was resolved and
// authorized; projectID is the resolved owner, empty when unknown.
func (s *Server) admitScenarioWriter(w http.ResponseWriter, r *http.Request, scenarioID, projectID string) (scenarioWriterAdmission, bool) {
	guardedStore, guarded := s.store.(storage.ScenarioGuardedWriterStore)
	reader, readable := s.store.(storage.ScenarioAuthoringStore)
	if !guarded || !readable {
		if scenarioSaveIfMatchPresented(r.Header) {
			reference := errorReferenceFor(w)
			slog.Error("scenario writer condition unavailable", "ref", reference, "path", r.URL.Path)
			writeJSON(w, http.StatusServiceUnavailable, ScenarioSaveCapabilityUnavailableError{
				Error: "scenario If-Match writes are unavailable without durable scenario revisions",
				Code:  "scenario_writer_if_match_unavailable", Ref: reference,
			})
			return scenarioWriterAdmission{}, false
		}
		if s.store != nil && strings.TrimSpace(projectID) != "" {
			project, err := s.store.GetProject(normalizeProjectID(projectID))
			if err != nil || project == nil {
				writeError(w, http.StatusServiceUnavailable, "scenario save policy unavailable")
				return scenarioWriterAdmission{}, false
			}
			if project.Settings.Authoring != nil && project.Settings.Authoring.RequireScenarioSaveIfMatch {
				writeScenarioSaveCapabilityUnavailable(w, true)
				return scenarioWriterAdmission{}, false
			}
		}
		return scenarioWriterAdmission{}, true
	}
	revision, presented, parseErr := parseScenarioAuthoringIfMatch(r.Header)
	if parseErr != nil {
		writeError(w, http.StatusBadRequest, "If-Match must contain one strong scenario authoring v1 tag")
		return scenarioWriterAdmission{}, false
	}
	admission := scenarioWriterAdmission{store: guardedStore}
	if !presented {
		return admission, true
	}
	snapshot, err := reader.GetScenarioAuthoringSnapshot(scenarioID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScenarioAuthoringNotFound) {
		writeError(w, http.StatusNotFound, "scenario not found")
		return scenarioWriterAdmission{}, false
	}
	if err != nil || snapshot == nil || snapshot.Scenario == nil || strings.TrimSpace(snapshot.ProjectID) == "" {
		writeError(w, http.StatusServiceUnavailable, "durable scenario content revisions unavailable")
		return scenarioWriterAdmission{}, false
	}
	owner := normalizeProjectID(snapshot.ProjectID)
	if expected, scoped := authorizedProjectFromRequest(r); scoped && owner != expected {
		writeError(w, http.StatusNotFound, "scenario not found")
		return scenarioWriterAdmission{}, false
	}
	if revision != snapshot.ContentRevision {
		writeError(w, http.StatusPreconditionFailed, "scenario authored content changed; reload before applying changes")
		return scenarioWriterAdmission{}, false
	}
	admission.guard.Expected = &storage.ScenarioAuthoringPrecondition{ProjectID: owner, ContentRevision: revision, CreatedAt: snapshot.Scenario.CreatedAt}
	return admission, true
}

// writeScenarioWriterError maps the guard's refusals; other errors are left
// to the writer's own mapping.
func writeScenarioWriterError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, storage.ErrScenarioAuthoringOutcomeUnknown) {
		writeScenarioAuthoringSaveOutcomeUnavailable(w, true)
		return true
	}
	if errors.Is(err, storage.ErrScenarioAuthoringPreconditionRequired) {
		writeError(w, http.StatusPreconditionRequired, "If-Match is required for scenario changes in this project")
		return true
	}
	return writeScenarioAuthoringMutationError(w, err)
}

// setScenarioWriterETag returns the revision the writer's own transaction
// committed, so the next conditioned change starts from exactly this state.
func setScenarioWriterETag(w http.ResponseWriter, revision string) {
	if revision == "" {
		return
	}
	if tag, err := scenarioAuthoringETag(revision); err == nil {
		w.Header().Set("ETag", tag)
	}
}

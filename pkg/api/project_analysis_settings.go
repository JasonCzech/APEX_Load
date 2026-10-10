package api

import (
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Deployment-wide analysis gates. A project named in one of these lists has
// the flag on whatever its own settings say; the lists predate the project
// settings and stay as overrides for operators who manage gates in the
// deployment.
const (
	envRCAV2Projects          = "APEXLOAD_RCA_V2_PROJECTS"
	envRCAV2PresentProjects   = "APEXLOAD_RCA_V2_PRESENT_PROJECTS"
	envRCAAutoAnalyzeProjects = "APEXLOAD_RCA_AUTO_ANALYZE_PROJECTS"
	envRCAAutoEnrichProjects  = "APEXLOAD_RCA_AUTO_ENRICH_PROJECTS"
	envBusinessKeyProjects    = "APEXLOAD_BUSINESS_KEY_PROJECTS"
)

// projectGetter is the part of a store the gates read.
type projectGetter interface {
	GetProject(id string) (*storage.Project, error)
}

// AnalysisRootCauseGates are root-cause v2's gates for one project.
type AnalysisRootCauseGates struct {
	// Enabled computes new analyses; saved ones stay readable without it.
	Enabled bool `json:"enabled"`
	// Present shows v2 in summaries, Diagnostics and reports.
	Present bool `json:"present"`
	// AutoAnalyze queues an analysis for every archived run. As an effective
	// gate it is on only with Enabled.
	AutoAnalyze bool `json:"auto_analyze"`
	// AutoEnrich freezes an enriched evidence revision at archive finalize.
	AutoEnrich bool `json:"auto_enrich"`
}

// AnalysisBusinessKeyGates are the business-key gates for one project.
type AnalysisBusinessKeyGates struct {
	Enabled bool `json:"enabled"`
}

// AnalysisGates are a project's analysis gates.
type AnalysisGates struct {
	RootCause    AnalysisRootCauseGates   `json:"root_cause"`
	BusinessKeys AnalysisBusinessKeyGates `json:"business_keys"`
}

// analysisGatesFromEnv are the gates the deployment's lists turn on.
func analysisGatesFromEnv(project string) AnalysisGates {
	return AnalysisGates{
		RootCause: AnalysisRootCauseGates{
			Enabled:     projectListed(envRCAV2Projects, project),
			Present:     projectListed(envRCAV2PresentProjects, project),
			AutoAnalyze: projectListed(envRCAAutoAnalyzeProjects, project),
			AutoEnrich:  projectListed(envRCAAutoEnrichProjects, project),
		},
		BusinessKeys: AnalysisBusinessKeyGates{Enabled: projectListed(envBusinessKeyProjects, project)},
	}
}

// analysisGatesFromSettings are the gates a project's own settings turn on.
func analysisGatesFromSettings(settings *storage.ProjectAnalysisSettings) AnalysisGates {
	if settings == nil {
		return AnalysisGates{}
	}
	return AnalysisGates{
		RootCause: AnalysisRootCauseGates{
			Enabled: settings.RootCause.Enabled, Present: settings.RootCause.Present,
			AutoAnalyze: settings.RootCause.AutoAnalyze, AutoEnrich: settings.RootCause.AutoEnrich,
		},
		BusinessKeys: AnalysisBusinessKeyGates{Enabled: settings.BusinessKeys.Enabled},
	}
}

// combineAnalysisGates turns a flag on when either source does. Automatic
// analysis needs analyses to be computed at all.
func combineAnalysisGates(env, settings AnalysisGates) AnalysisGates {
	gates := AnalysisGates{
		RootCause: AnalysisRootCauseGates{
			Enabled:     env.RootCause.Enabled || settings.RootCause.Enabled,
			Present:     env.RootCause.Present || settings.RootCause.Present,
			AutoAnalyze: env.RootCause.AutoAnalyze || settings.RootCause.AutoAnalyze,
			AutoEnrich:  env.RootCause.AutoEnrich || settings.RootCause.AutoEnrich,
		},
		BusinessKeys: AnalysisBusinessKeyGates{Enabled: env.BusinessKeys.Enabled || settings.BusinessKeys.Enabled},
	}
	gates.RootCause.AutoAnalyze = gates.RootCause.AutoAnalyze && gates.RootCause.Enabled
	return gates
}

// projectAnalysisSettingsOf reads a project's stored analysis settings; a
// store that cannot read projects, or a project that cannot be read, has
// none, so only the deployment's lists apply.
func projectAnalysisSettingsOf(store any, project string) *storage.ProjectAnalysisSettings {
	getter, ok := store.(projectGetter)
	if !ok || getter == nil || project == "" {
		return nil
	}
	stored, err := getter.GetProject(project)
	if err != nil || stored == nil {
		return nil
	}
	return stored.Settings.Analysis
}

// projectAnalysisGates are a project's effective analysis gates: each flag
// is on when the deployment's list names the project or the project's own
// settings set it. Every analysis gate reads through here.
func projectAnalysisGates(store any, project string) AnalysisGates {
	return combineAnalysisGates(analysisGatesFromEnv(project), analysisGatesFromSettings(projectAnalysisSettingsOf(store, project)))
}

// rcaV2ProjectEnabled gates computing v2 analyses; saved ones stay readable.
func rcaV2ProjectEnabled(store any, project string) bool {
	return projectAnalysisGates(store, project).RootCause.Enabled
}

// rcaV2PresentEnabled says whether Summary, Diagnostics and reports present
// v2 for the project.
func rcaV2PresentEnabled(store any, project string) bool {
	return projectAnalysisGates(store, project).RootCause.Present
}

// businessKeyProjectEnabled says whether a project uses business keys
// (approved mappings, digests and lookups).
func businessKeyProjectEnabled(store any, project string) bool {
	return projectAnalysisGates(store, project).BusinessKeys.Enabled
}

// ProjectAnalysisSettingsResponse is a project's analysis settings: what the
// project set, what the deployment forces on, and the gates in effect.
type ProjectAnalysisSettingsResponse struct {
	ProjectID string                          `json:"project_id"`
	Settings  storage.ProjectAnalysisSettings `json:"settings"`
	// ForcedByEnv are the flags the deployment's environment lists turn on
	// for this project; the project cannot turn them off.
	ForcedByEnv AnalysisGates `json:"forced_by_env"`
	// Effective are the gates the controller applies.
	Effective AnalysisGates `json:"effective"`
}

// projectAnalysisSettingsRequest is the PUT body. ExpectedRevision is the
// settings revision the editor read; another save since is refused.
type projectAnalysisSettingsRequest struct {
	ExpectedRevision *int64                             `json:"expected_revision"`
	RootCause        storage.ProjectRootCauseSettings   `json:"root_cause"`
	BusinessKeys     storage.ProjectBusinessKeySettings `json:"business_keys"`
}

func projectAnalysisSettingsResponse(project *storage.Project) ProjectAnalysisSettingsResponse {
	settings := storage.ProjectAnalysisSettings{}
	if project.Settings.Analysis != nil {
		settings = *project.Settings.Analysis
	}
	env := analysisGatesFromEnv(project.ID)
	return ProjectAnalysisSettingsResponse{ProjectID: project.ID, Settings: settings, ForcedByEnv: env,
		Effective: combineAnalysisGates(env, analysisGatesFromSettings(&settings))}
}

// handleGetProjectAnalysisSettings returns a project's analysis settings.
func (s *Server) handleGetProjectAnalysisSettings(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, projectAnalysisSettingsResponse(project))
}

// handleUpdateProjectAnalysisSettings replaces a project's analysis settings.
// It is a project administrator's change, stamped and audited; an editor who
// read an older revision is refused rather than overwriting a newer save,
// and an unrelated concurrent project update is retried against.
func (s *Server) handleUpdateProjectAnalysisSettings(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	var req projectAnalysisSettingsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "expected_revision is required")
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
		prior := storage.ProjectAnalysisSettings{}
		if project.Settings.Analysis != nil {
			prior = *project.Settings.Analysis
		}
		if prior.Revision != *req.ExpectedRevision {
			writeError(w, http.StatusConflict, "the project's analysis settings changed since they were read; reload and retry")
			return
		}
		next := storage.ProjectAnalysisSettings{RootCause: req.RootCause, BusinessKeys: req.BusinessKeys, Revision: prior.Revision, UpdatedAt: prior.UpdatedAt, UpdatedBy: prior.UpdatedBy}
		changed := next.RootCause != prior.RootCause || next.BusinessKeys != prior.BusinessKeys
		if changed {
			next.Revision = prior.Revision + 1
			next.UpdatedAt = time.Now().UTC()
			_, subject, displayName := s.resolveAuditActor(r)
			next.UpdatedBy = firstNonEmpty(displayName, subject)
		}
		if !changed {
			writeJSON(w, http.StatusOK, projectAnalysisSettingsResponse(project))
			return
		}
		project.Settings.Analysis = &next
		updated, err := updater.UpdateProjectIfUnchanged(project, project.UpdatedAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save the project's analysis settings")
			return
		}
		if updated {
			s.recordAuditEvent(r, "project.analysis_settings.update", "success", "project", project.ID, project.ID, map[string]any{
				"revision": next.Revision, "root_cause_before": prior.RootCause, "root_cause_after": next.RootCause,
				"business_keys_before": prior.BusinessKeys.Enabled, "business_keys_after": next.BusinessKeys.Enabled,
			})
			writeJSON(w, http.StatusOK, projectAnalysisSettingsResponse(project))
			return
		}
	}
	writeError(w, http.StatusConflict, "project changed while the update was in flight; reload and retry")
}

// stampCreatedProjectAnalysisSettings stamps the analysis settings a new
// project is created with, as its first revision.
func (s *Server) stampCreatedProjectAnalysisSettings(r *http.Request, settings *storage.ProjectAnalysisSettings) *storage.ProjectAnalysisSettings {
	if settings == nil {
		return nil
	}
	stamped := storage.ProjectAnalysisSettings{RootCause: settings.RootCause, BusinessKeys: settings.BusinessKeys}
	if stamped.RootCause == (storage.ProjectRootCauseSettings{}) && stamped.BusinessKeys == (storage.ProjectBusinessKeySettings{}) {
		return nil
	}
	stamped.Revision = 1
	stamped.UpdatedAt = time.Now().UTC()
	_, subject, displayName := s.resolveAuditActor(r)
	stamped.UpdatedBy = firstNonEmpty(displayName, subject)
	return &stamped
}

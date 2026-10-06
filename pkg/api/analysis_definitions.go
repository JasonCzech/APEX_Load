package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) handleAnalysisDefinitions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project_id")
	kind := "mapping"
	switch {
	case strings.Contains(r.URL.Path, "/comparisons"):
		kind = "comparison"
	case strings.Contains(r.URL.Path, "/rca-mappings"):
		kind = storage.AnalysisDefinitionKindRCAMapping
	case strings.Contains(r.URL.Path, "/business-key-mappings"):
		kind = storage.AnalysisDefinitionKindBusinessKeyMapping
	}
	// Root-cause mappings follow the root-cause v2 gate and business-key
	// mappings the business-key gate.
	var enabled bool
	switch kind {
	case storage.AnalysisDefinitionKindRCAMapping:
		enabled = rcaV2ProjectEnabled(project)
	case storage.AnalysisDefinitionKindBusinessKeyMapping:
		enabled = businessKeyProjectEnabled(project)
	default:
		enabled = analysisProjectEnabled(project)
	}
	if !enabled {
		writeError(w, 409, "analysis is not enabled for project")
		return
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		writeError(w, 503, "analysis definitions unavailable")
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodGet {
		if id != "" {
			revision := int64(0)
			if raw := r.URL.Query().Get("revision"); raw != "" {
				var err error
				revision, err = strconv.ParseInt(raw, 10, 64)
				if err != nil || revision < 1 {
					writeError(w, 400, "revision must be a positive integer")
					return
				}
			}
			in, err := store.GetAnalysisDefinition(project, id, revision)
			if err != nil || in.Kind != kind || in.Deleted {
				writeError(w, 404, "analytical definition not found")
				return
			}
			writeJSON(w, 200, in)
		} else {
			limit, offset := parseLimitOffsetQuery(r, 50)
			list, err := store.ListAnalysisDefinitions(project, kind, min(limit, 100), offset)
			if err != nil {
				writeError(w, 500, "cannot list analytical definitions")
				return
			}
			writeJSON(w, 200, list)
		}
		return
	}
	var in storage.AnalysisDefinition
	if r.Method == http.MethodDelete {
		revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if err != nil || revision < 1 {
			writeError(w, 400, "revision required")
			return
		}
		current, err := store.GetAnalysisDefinition(project, id, 0)
		if err != nil || current.Kind != kind {
			writeError(w, 404, "analytical definition not found")
			return
		}
		in = *current
		in.Revision = revision
		in.Deleted = true
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				writeError(w, 413, "analytical definition exceeds request byte limit")
			} else {
				writeError(w, 400, "invalid analytical definition request")
			}
			return
		}
		if err := decodeAnalysisDefinitionJSON(body, &in); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if in.ProjectID != "" && in.ProjectID != project || in.ID != "" && in.ID != id {
			writeError(w, 400, "analytical definition identity mismatch")
			return
		}
		if r.Method == http.MethodPost {
			if in.Revision != 0 || in.ID != "" {
				writeError(w, 400, "new definition must omit revision and id")
				return
			}
			id = uuid.NewString()
		} else if in.Revision < 1 {
			writeError(w, 400, "revision required")
			return
		}
		in.ID, in.ProjectID, in.Kind, in.Deleted = id, project, kind, false
		if kind == storage.AnalysisDefinitionKindRCAMapping {
			if err := validateRCAMappingDefinition(in.Definition); err != nil {
				writeError(w, 400, err.Error())
				return
			}
		} else if kind == storage.AnalysisDefinitionKindBusinessKeyMapping {
			if !s.validBusinessKeyMapping(w, project, in.Definition) {
				return
			}
		} else if kind == "mapping" {
			if _, err := analysis.DecodeParquetMappingDefinition(in.Definition); err != nil {
				writeError(w, 400, err.Error())
				return
			}
		} else {
			var comparison AnalysisComparisonRequest
			if decodeAnalysisDefinitionJSON(in.Definition, &comparison) != nil || comparison.validate() != nil {
				writeError(w, 400, "invalid saved comparison")
				return
			}
			// Recheck ownership on save and again at execution; saved definitions
			// never grant access to datasets/runs that have moved or been deleted.
			for _, selection := range []AnalysisSeriesSelection{comparison.Left, comparison.Right} {
				if selection.DatasetID != "" {
					imports, ok := s.store.(storage.AnalysisImportStore)
					if !ok {
						writeError(w, 503, "dataset storage unavailable")
						return
					}
					dataset, err := imports.GetAnalysisImport(selection.DatasetID)
					if err != nil || dataset.ProjectID != project || dataset.State != "ready" {
						writeError(w, 404, "dataset not found in project")
						return
					}
				} else {
					run, err := s.store.GetTest(selection.RunID)
					if err != nil || run.ProjectID != project {
						writeError(w, 404, "run not found in project")
						return
					}
				}
			}
		}
	}
	saved, err := store.SaveAnalysisDefinition(in, in.Revision)
	if err != nil {
		writeError(w, 409, "analytical definition revision conflict or resource limit")
		return
	}
	s.recordAuditEvent(r, "analysis."+kind+".save", "success", "analysis_definition", in.ID, project, map[string]any{"revision": saved.Revision, "deleted": saved.Deleted})
	writeJSON(w, 200, saved)
}

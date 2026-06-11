package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/recorderjson"
)

type recorderImportRequest struct {
	Data      []byte
	ProjectID string
	Opts      recorderjson.ConvertOptions
}

// handleImportRecorder handles POST /api/v1/scripts/import/recorder.
// Accepts Chrome DevTools Recorder JSON exports or Playwright codegen scripts
// (JSON body, raw upload, or multipart) and returns a generated
// browser-protocol script preview with ranked selector candidates.
func (s *Server) handleImportRecorder(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readRecorderImportRequest(w, r)
	if !ok {
		return
	}

	result, err := recorderjson.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		Parameterization:    buildImportParameterization(result.Script, nil),
		Summary: map[string]any{
			"import_mode":     result.Stats.Format,
			"execution_mode":  "apexload_browser",
			"selector_review": "ranked candidates preserved in action metadata",
		},
	}))
}

func readRecorderImportRequest(w http.ResponseWriter, r *http.Request) (*recorderImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, recorderImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req struct {
			Name      string                      `json:"name,omitempty"`
			ProjectID string                      `json:"project_id,omitempty"`
			Content   string                      `json:"content"`
			Options   recorderjson.ConvertOptions `json:"options,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" {
			writeError(w, http.StatusBadRequest, "content field is required (the recording JSON or Playwright script text)")
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		return &recorderImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(recorderImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		name := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
		return &recorderImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Opts: recorderjson.ConvertOptions{
				ScriptName: firstNonEmpty(r.FormValue("name"), name),
				BaseURL:    r.FormValue("base_url"),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &recorderImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Opts: recorderjson.ConvertOptions{
			ScriptName: firstNonEmpty(r.URL.Query().Get("name"), strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))),
			BaseURL:    r.URL.Query().Get("base_url"),
		},
	}, true
}

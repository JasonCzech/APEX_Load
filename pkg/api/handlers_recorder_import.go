package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/recorderjson"
	"github.com/jasonczech/apexload/pkg/dsl"
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

	result, preflight, err := s.convertRecorderImport(importReq)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildRecorderImportResponse(result, preflight))
}

func (s *Server) handleValidateRecorderImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readRecorderImportRequest(w, r)
	if !ok {
		return
	}
	result, preflight, err := s.convertRecorderImport(importReq)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildRecorderImportResponse(result, preflight))
}

func (s *Server) handleReplayRecorderImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readRecorderImportRequest(w, r)
	if !ok {
		return
	}
	result, preflight, err := s.convertRecorderImport(importReq)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script == nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion produced no script")
		return
	}
	issues := validateScriptForReplay(result.Script)
	if len(issues) > 0 {
		response := buildRecorderImportResponse(result, preflight)
		response["valid"] = false
		response["issues"] = issues
		writeJSON(w, http.StatusBadRequest, response)
		return
	}
	ignoreThink := true
	replay := s.browserReplay.Start(result.Script.ID, result.Script, BrowserReplayStartRequest{
		GroupName:       result.Script.Name + " — import replay",
		IgnoreThinkTime: &ignoreThink,
		Preflight:       preflight,
	})
	response := buildRecorderImportResponse(result, preflight)
	response["replay_session_id"] = replay.ID
	response["replay_session"] = replay
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) convertRecorderImport(importReq *recorderImportRequest) (*recorderjson.ConvertResult, *ReplayPreflight, error) {
	result, err := recorderjson.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		return nil, nil, err
	}
	if result.Script != nil {
		if result.Script.ID == "" {
			result.Script.ID = uuid.New().String()
		}
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
		issues := validateScript(result.Script)
		result.Warnings = append(result.Warnings, issues...)
		preflight := buildReplayPreflight(result.Script, nil, validateScriptForReplay(result.Script), result.Warnings)
		attachImportReplayPreflight(result.Script, "recorder_export", preflight)
		preserved := ensureScriptImportPreserved(result.Script, "recorder_export")
		preserved["source_format"] = result.Stats.Format
		preserved["replay_required"] = true
		preserved["replay_supported"] = result.Script.Protocol == "browser"
		preserved["conversion_confidence"] = recorderConversionConfidence(result.Stats)
		preserved["step_mapping"] = recorderStepMapping(result)
		return result, preflight, nil
	}
	return result, nil, nil
}

func buildRecorderImportResponse(result *recorderjson.ConvertResult, preflight *ReplayPreflight) map[string]any {
	return newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		Parameterization:    buildImportParameterization(result.Script, nil),
		Preflight:           preflight,
		Summary: map[string]any{
			"import_mode":           result.Stats.Format,
			"source_format":         result.Stats.Format,
			"execution_mode":        "apexload_browser",
			"selector_review":       "ranked candidates preserved in action metadata",
			"replay_required":       true,
			"replay_supported":      result.Script != nil && result.Script.Protocol == "browser",
			"conversion_confidence": recorderConversionConfidence(result.Stats),
			"step_mapping":          recorderStepMapping(result),
		},
	})
}

func recorderConversionConfidence(stats recorderjson.ConvertStats) float64 {
	if stats.TotalSteps <= 0 {
		return 1
	}
	exactSteps := stats.TotalSteps - stats.SkippedSteps - stats.DegradedSteps
	if exactSteps < 0 {
		exactSteps = 0
	}
	confidence := (float64(exactSteps) + float64(stats.DegradedSteps)*0.5) / float64(stats.TotalSteps)
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

func recorderStepMapping(result *recorderjson.ConvertResult) []map[string]any {
	if result == nil {
		return []map[string]any{}
	}
	if len(result.StepMapping) > 0 {
		out := make([]map[string]any, 0, len(result.StepMapping))
		for _, mapping := range result.StepMapping {
			entry := map[string]any{
				"disposition": mapping.Disposition,
			}
			if mapping.SourceIndex > 0 {
				entry["source_index"] = mapping.SourceIndex
			}
			if mapping.SourceLocation != "" {
				entry["source_location"] = mapping.SourceLocation
			}
			if mapping.SourceStepType != "" {
				entry["source_step_type"] = mapping.SourceStepType
			}
			if mapping.ActionIndex != nil {
				entry["index"] = *mapping.ActionIndex
				entry["action_index"] = *mapping.ActionIndex
			}
			if mapping.Name != "" {
				entry["name"] = mapping.Name
			}
			if mapping.Type != "" {
				entry["type"] = mapping.Type
			}
			if mapping.TargetPath != "" {
				entry["target_path"] = mapping.TargetPath
			}
			if mapping.RecorderStepSeq > 0 {
				entry["recorder_step_seq"] = mapping.RecorderStepSeq
			}
			if mapping.Reason != "" {
				entry["reason"] = mapping.Reason
			}
			out = append(out, entry)
		}
		return out
	}
	script := result.Script
	if script == nil {
		return []map[string]any{}
	}
	actions := append([]dsl.Action{}, script.Init...)
	actions = append(actions, script.Actions...)
	actions = append(actions, script.Teardown...)
	out := make([]map[string]any, 0, len(actions))
	for index, action := range actions {
		entry := map[string]any{
			"index":       index,
			"name":        action.Name,
			"type":        action.Type,
			"disposition": "mapped",
		}
		if action.Metadata != nil {
			if sourceType, ok := action.Metadata["source_step_type"]; ok {
				entry["source_step_type"] = sourceType
			}
			if sourceSeq, ok := action.Metadata["recorder_step_seq"]; ok {
				entry["recorder_step_seq"] = sourceSeq
			}
		}
		out = append(out, entry)
	}
	return out
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
		return &recorderImportRequest{Data: []byte(req.Content), ProjectID: jsonImportProjectID(r, req.ProjectID), Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, uploadName, ok := readImportMultipartFile(w, r, recorderImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		name := strings.TrimSuffix(uploadName, filepath.Ext(uploadName))
		return &recorderImportRequest{
			Data:      data,
			ProjectID: authorizedImportProjectID(r),
			Opts: recorderjson.ConvertOptions{
				ScriptName: firstNonEmpty(r.FormValue("name"), name),
				BaseURL:    r.FormValue("base_url"),
			},
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
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

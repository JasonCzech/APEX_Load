package api

import (
	"context"
	"encoding/json"
	"net/http"
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

	result, augmented, err := s.convertRecorderImport(r, importReq)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	writeScriptImportResponse(w, http.StatusOK, s.buildRecorderImportResponse(r, result, augmented))
}

func (s *Server) handleValidateRecorderImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readRecorderImportRequest(w, r)
	if !ok {
		return
	}
	result, augmented, err := s.convertRecorderImport(r, importReq)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	writeScriptImportResponse(w, http.StatusOK, s.buildRecorderImportResponse(r, result, augmented))
}

func (s *Server) handleReplayRecorderImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	importReq, ok := readRecorderImportRequest(w, r)
	if !ok {
		return
	}
	result, augmented, err := s.convertRecorderImport(r, importReq)
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
		response := s.buildRecorderImportResponse(r, result, augmented)
		response["valid"] = false
		response["issues"] = issues
		writeScriptImportResponse(w, http.StatusBadRequest, response)
		return
	}
	// Build the response BEFORE handing the script to the replay manager.
	//
	// buildRecorderImportResponse mutates the script — markCredentialPreserving
	// ImportScript writes into script.Metadata.Import.Preserved — while Start
	// hands that same *dsl.Script to a goroutine that immediately serializes it
	// (ReplayScript → snapshotReferencedSecretsContext → ReferencedSecretNames).
	// Doing it the other way round was a concurrent map write against a map
	// read, which in Go is not merely a race: it is a fatal runtime throw that
	// takes the whole controller down mid-import.
	//
	// Ordering it this way is also the more correct behaviour — the replay now
	// runs against exactly the script the response describes, credential policy
	// included, rather than a pre-mutation version of it.
	response := s.buildRecorderImportResponse(r, result, augmented)

	ignoreThink := true
	executionCtx, releaseExecutionCtx := s.runner.executionOperationContext(context.Background())
	replay, replayErr := s.browserReplay.Start(result.Script.ID, result.Script, BrowserReplayStartRequest{
		GroupName:               result.Script.Name + " — import replay",
		IgnoreThinkTime:         &ignoreThink,
		Preflight:               augmented.Preflight,
		executionContext:        executionCtx,
		releaseExecutionContext: releaseExecutionCtx,
	})
	if replayErr != nil {
		releaseExecutionCtx()
		writeBrowserReplayStartError(w, replayErr)
		return
	}
	response["replay_session_id"] = replay.ID
	response["replay_session"] = replay
	writeScriptImportResponse(w, http.StatusCreated, response)
}

func (s *Server) convertRecorderImport(r *http.Request, importReq *recorderImportRequest) (*recorderjson.ConvertResult, importAugmentation, error) {
	result, err := recorderjson.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		return nil, importAugmentation{}, err
	}
	if result.Script != nil {
		if result.Script.ID == "" {
			result.Script.ID = uuid.New().String()
		}
		preserved := ensureScriptImportPreserved(result.Script, "recorder_export")
		preserved["source_format"] = result.Stats.Format
		preserved["replay_required"] = true
		preserved["replay_supported"] = result.Script.Protocol == "browser"
		preserved["conversion_confidence"] = recorderConversionConfidence(result.Stats)
		preserved["step_mapping"] = recorderStepMapping(result)
	}
	draft := importDraft{
		Source:           "recorder_export",
		SourceScriptName: importReq.Opts.ScriptName,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		Unsupported:      result.Unsupported,
		Warnings:         result.Warnings,
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	return result, augmented, nil
}

func (s *Server) buildRecorderImportResponse(r *http.Request, result *recorderjson.ConvertResult, augmented importAugmentation) map[string]any {
	return s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      augmented.Sections,
		Parameterization:    buildImportParameterization(result.Script, nil),
		Preflight:           augmented.Preflight,
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
	upload, ok := decodeImportUpload(w, r, recorderImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req struct {
			Name      string                      `json:"name,omitempty"`
			ProjectID string                      `json:"project_id,omitempty"`
			Content   string                      `json:"content"`
			Options   recorderjson.ConvertOptions `json:"options,omitempty"`
		}
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" {
			writeError(w, http.StatusBadRequest, "content field is required (the recording JSON or Playwright script text)")
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		return &recorderImportRequest{
			Data:      []byte(req.Content),
			ProjectID: upload.jsonProjectID(req.ProjectID),
			Opts:      req.Options,
		}, true
	}
	return &recorderImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Opts: recorderjson.ConvertOptions{
			ScriptName: firstNonEmpty(upload.Value("name"), upload.Name),
			BaseURL:    upload.Value("base_url"),
		},
	}, true
}

package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func diffAsyncAPIImport(existing *dsl.Script, imported *dsl.Script, replaceMode string) openAPIImportDiff {
	return diffImportByKey(existing, imported, replaceMode, asyncAPIActionKey)
}

func mergeAsyncAPIImportScript(existing *dsl.Script, imported *dsl.Script) *dsl.Script {
	return mergeImportScriptByKey(existing, imported, asyncAPIActionKey)
}

func asyncAPIActionKey(action dsl.Action) string {
	if action.Metadata == nil {
		return ""
	}
	channel := strings.TrimSpace(anyString(action.Metadata["asyncapi_channel"]))
	if channel == "" {
		return ""
	}
	correlation := strings.TrimSpace(anyString(action.Metadata["asyncapi_correlation_id"]))
	parts := []string{strings.ToLower(strings.TrimSpace(action.Type)), strings.TrimSpace(action.Target), channel, strings.TrimSpace(action.Name), correlation}
	return strings.Join(parts, "\x00")
}

func (s *Server) handleCommitAsyncAPIImport(w http.ResponseWriter, r *http.Request) {
	var req openAPIImportCommitRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	script := &req.Script
	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}
	preflight := buildReplayPreflight(script, nil, nil, validationWarnings(script))
	attachImportReplayPreflight(script, "asyncapi", preflight)

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "create"
	}
	replaceMode := strings.ToLower(strings.TrimSpace(req.ReplaceMode))
	if replaceMode == "" {
		replaceMode = "full"
	}
	if mode != "replace" && strings.TrimSpace(req.TargetScriptID) != "" {
		mode = "replace"
	}
	if mode != "create" && mode != "replace" {
		writeError(w, http.StatusBadRequest, "mode must be create or replace")
		return
	}
	if replaceMode != "full" && replaceMode != "merge_by_operation_id" {
		writeError(w, http.StatusBadRequest, "replace_mode must be full or merge_by_operation_id")
		return
	}

	if mode == "create" {
		diff := openAPIImportDiff{Mode: "create", Added: len(script.Actions)}
		if req.DryRun {
			writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "diff": diff, "preflight": preflight})
			return
		}
		stored, err := s.runner.StoreScriptDurable(script)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
		s.recordAuditEvent(r, "script.import.asyncapi.create", "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": stored.Name})
		writeJSON(w, http.StatusCreated, map[string]any{
			"scripts":       maskStoredScripts([]*StoredScript{stored}),
			"script":        maskScriptSecrets(stored.Script),
			"stored_script": maskStoredScript(stored),
			"replaced":      false,
			"dry_run":       false,
			"diff":          diff,
			"preflight":     preflight,
		})
		return
	}

	targetID := strings.TrimSpace(req.TargetScriptID)
	if targetID == "" {
		targetID = strings.TrimSpace(script.ID)
	}
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "target_script_id is required when replacing an existing AsyncAPI import")
		return
	}
	existing, ok := s.runner.GetScript(targetID)
	if !ok || existing == nil || existing.Script == nil {
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, existing.ProjectID) {
		return
	}
	next := script
	diff := diffAsyncAPIImport(existing.Script, script, replaceMode)
	diff.Mode = "replace"
	diff.ReplaceMode = replaceMode
	if replaceMode == "merge_by_operation_id" {
		next = mergeAsyncAPIImportScript(existing.Script, script)
	}
	preserveReplacedScriptIdentity(next, existing.Script)
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "replaced": true, "diff": diff, "preflight": preflight})
		return
	}
	updated, ok, err := s.runner.UpdateScriptDurableInProject(targetID, next, authorizedProjectOr(r, existing.ProjectID))
	if err != nil {
		if errors.Is(err, errScriptConcurrentChange) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	s.recordAuditEvent(r, "script.import.asyncapi.replace", "success", "script", updated.ID, updated.ProjectID, map[string]any{"name": updated.Name, "replace_mode": replaceMode})
	writeJSON(w, http.StatusOK, map[string]any{
		"scripts":       maskStoredScripts([]*StoredScript{updated}),
		"script":        maskScriptSecrets(updated.Script),
		"stored_script": maskStoredScript(updated),
		"replaced":      true,
		"dry_run":       false,
		"diff":          diff,
		"preflight":     preflight,
	})
}

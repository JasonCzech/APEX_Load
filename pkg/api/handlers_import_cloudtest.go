package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/cloudtest"
)

func (s *Server) handleCloudTestSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "cloudtest")
}

// handleImportCloudTest inspects a dependency-complete XML/ZIP export and
// returns the standard import envelope. Until real golden fixtures prove the
// wire shape, every emitted action is a durable execution-blocked review asset.
func (s *Server) handleImportCloudTest(w http.ResponseWriter, r *http.Request) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return
	}
	data := upload.Data
	projectID := upload.ProjectID
	fileName := upload.Filename
	if upload.Transport == importTransportJSON {
		var request struct {
			Content   string `json:"content"`
			ProjectID string `json:"project_id,omitempty"`
			FileName  string `json:"file_name,omitempty"`
			Filename  string `json:"filename,omitempty"`
		}
		if err := json.Unmarshal(upload.Data, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		data = []byte(request.Content)
		projectID = upload.jsonProjectID(request.ProjectID)
		fileName = firstNonEmpty(request.FileName, request.Filename, upload.Filename)
	}

	result, err := cloudtest.Convert(data, cloudtest.ConvertOptions{
		ProjectID: projectID,
		FileName:  fileName,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}

	draft := importDraft{
		Source:           "cloudtest",
		SourceScriptName: firstNonEmpty(fileName, "CloudTest export"),
		ProjectID:        projectID,
		Script:           result.Script,
		Scripts:          result.Scripts,
		Unsupported:      result.Unsupported,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting,
		Sections: []scriptImportReviewSection{{
			Key:      "cloudtest_fixture_gate",
			Title:    "CloudTest Fixture Gate",
			Severity: "warning",
			Summary:  "CloudTest mappings remain review-only until sanitized dependency-complete exports prove their wire shape.",
			Items: []string{
				fmt.Sprintf("%d XML element(s) classified; %d recognized and %d unknown", result.Stats.Elements, result.Stats.RecognizedElements, result.Stats.UnknownElements),
				fmt.Sprintf("%d Clip script draft(s) and %d Composition scenario draft(s) produced", result.Stats.Clips, result.Stats.Compositions),
				"Every source element has executable_exact, executable_bounded, blocked_draft, or rejected disposition; this preview uses blocked_draft while fixture_proven is false.",
			},
		}},
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings

	// Reuse the existing encrypted candidate/session path for the primary asset
	// so a committed multi-asset batch can retain the dependency-complete source
	// without writing orphan artifacts for abandoned previews.
	var sessionID string
	var sessionExpiresAt time.Time
	var sessionErr error
	if result.Script != nil {
		sessionID, sessionExpiresAt, sessionErr = s.registerImportCandidateSessionWithRequirements(
			r,
			"cloudtest",
			result.Script,
			nil,
			importCandidateSource{Bytes: data, Format: result.SourceFormat},
		)
	}
	if sessionErr != nil {
		result.Warnings = append(result.Warnings, "The protected CloudTest source candidate could not be created; review remains available but durable source retention is unavailable for this preview.")
	}

	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Result:              result,
		Scripts:             result.Scripts,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Diagnostics:         result.Unsupported,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      augmented.Sections,
		Summary: map[string]any{
			"source_format": result.SourceFormat,
			"fixture_gate":  result.FixtureGate,
			"dispositions":  map[string]any{"allowed": []string{"executable_exact", "executable_bounded", "blocked_draft", "rejected"}},
		},
		Accounting:           result.Accounting,
		Preflight:            augmented.Preflight,
		SkipDraftAttestation: true,
	})
	response["scenario_drafts"] = sanitizeImportEmbeddedReviewEvidence(result.ScenarioDrafts)
	response["data_pool_drafts"] = sanitizeImportReviewEvidence(result.DataPoolDrafts)
	response["environment_drafts"] = sanitizeImportReviewEvidence(result.EnvironmentDrafts)
	response["monitor_drafts"] = sanitizeImportReviewEvidence(result.MonitorDrafts)
	response["preserved_artifacts"] = sanitizeImportReviewEvidence(result.PreservedArtifacts)
	response["dispositions"] = sanitizeImportReviewEvidence(result.Dispositions)
	if sessionErr == nil && strings.TrimSpace(sessionID) != "" {
		importSessionIDs := make([]string, len(result.Scripts))
		importSessionIDs[0] = sessionID
		response["import_session_ids"] = importSessionIDs
		response["import_session_id"] = sessionID
		response["expires_at"] = sessionExpiresAt
		response["import_draft_expires_at"] = sessionExpiresAt
		response["source_retention"] = map[string]any{
			"state":       "protected_candidate",
			"asset_index": 0,
			"commit_path": "/api/v1/scripts/import/commit-batch",
		}
	}
	writeScriptImportResponse(w, http.StatusOK, response)
}

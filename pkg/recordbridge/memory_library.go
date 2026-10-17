package recordbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

// MemoryStoredScript is the saved-script envelope of a storage-free bridge.
// Controller-only relationships and revision projections are absent, as they
// were for a create-only save by the embedded in-memory runner.
type MemoryStoredScript struct {
	ID              string                        `json:"id"`
	RefKey          string                        `json:"ref_key,omitempty"`
	Name            string                        `json:"name"`
	Protocol        string                        `json:"protocol"`
	Version         string                        `json:"version"`
	ProjectID       string                        `json:"project_id,omitempty"`
	FolderID        *string                       `json:"folder_id,omitempty"`
	CreatedAt       time.Time                     `json:"created_at"`
	AuthoringHealth *importreview.AuthoringHealth `json:"authoring_health,omitempty"`
	Script          *dsl.Script                   `json:"script"`
}

// MemoryScriptLibrary retains the bridge's saved conversion identity and exact
// scripts for retries during this process. A batch is published atomically.
type MemoryScriptLibrary struct {
	mu       sync.Mutex
	scripts  map[string]*MemoryStoredScript
	sequence int
}

func (m *MemoryScriptLibrary) save(scripts []*dsl.Script) ([]*MemoryStoredScript, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	staged := make([]*MemoryStoredScript, len(scripts))
	sequence := m.sequence
	for index, script := range scripts {
		if script == nil {
			return nil, errors.New("script is required")
		}
		if prior := m.scripts[script.ID]; prior != nil {
			if !equivalentSavedScript(prior.Script, script) {
				return nil, fmt.Errorf("script changed concurrently; reload and retry: script %s already exists and is not owned by this approval", script.ID)
			}
			staged[index] = prior
			continue
		}
		source := script.Metadata.Import.Source
		prepared, err := importreview.PreparePersistentImportedLibraryScript(script, nil, source)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if prepared.CreatedAt.IsZero() {
			prepared.CreatedAt = now
		}
		prepared.UpdatedAt = now
		sequence++
		prepared.RefKey = refkey.Script(buildinfo.Environment(), sequence, 1)
		staged[index] = &MemoryStoredScript{ID: prepared.ID, RefKey: prepared.RefKey, Name: prepared.Name, Protocol: prepared.Protocol, Version: prepared.Version, ProjectID: prepared.ProjectID, FolderID: prepared.FolderID, CreatedAt: prepared.CreatedAt, AuthoringHealth: importreview.BuildStoredScriptAuthoringHealth(prepared), Script: prepared}
	}
	if m.scripts == nil {
		m.scripts = make(map[string]*MemoryStoredScript)
	}
	for _, stored := range staged {
		m.scripts[stored.ID] = stored
	}
	m.sequence = sequence
	// Never return the retained definitions to response augmentation or callers.
	encoded, err := json.Marshal(staged)
	if err != nil {
		return nil, err
	}
	var detached []*MemoryStoredScript
	if err = json.Unmarshal(encoded, &detached); err != nil {
		return nil, err
	}
	return detached, nil
}

func equivalentSavedScript(existing, candidate *dsl.Script) bool {
	if existing == nil || candidate == nil {
		return false
	}
	left, right := *existing, *candidate
	left.ID, right.ID = "", ""
	left.RefKey, right.RefKey = "", ""
	left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
	left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
	left.Metadata = importreview.WithoutImportedContentDigest(left.Metadata)
	right.Metadata = importreview.WithoutImportedContentDigest(right.Metadata)
	l, le := json.Marshal(left)
	r, re := json.Marshal(right)
	return le == nil && re == nil && string(l) == string(r)
}

// HandleConvertBrowserRecording preserves the standalone saved conversion
// contract. Durable controller saves use their receipt adapter after the same
// shared conversion preparation.
func (s *Service) HandleConvertBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req BrowserRecordingConvertRequest
	if !s.decodeOptionalJSONBody(w, r, &req) || !s.validHTTPVersionMode(w, req.HTTPVersionMode) {
		return
	}
	if req.ExpectedScriptCount < 0 || req.ExpectedScriptCount > 65 {
		s.writeError(w, http.StatusBadRequest, "expected_script_count is outside the supported range")
		return
	}
	if err := ValidateBrowserRecordingParameterizationSelections(req.ParameterizationSelections, -1); err != nil {
		s.writeError(w, http.StatusBadRequest, "browser recording parameterization selections are invalid: "+err.Error())
		return
	}
	artifact, info, ok := s.Recording.Artifact(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil {
		s.writeError(w, http.StatusConflict, "browser recording has no captured session yet")
		return
	}
	if info.Status != BrowserRecordingStopped {
		s.writeError(w, http.StatusConflict, "stop the recording before saving the converted script")
		return
	}
	prepared, err := s.PrepareBrowserRecordingConversion(artifact, info, req, false)
	if err != nil {
		s.writeError(w, err.Status, err.Message)
		return
	}
	project := normalizeProjectID(info.ProjectID)
	operation := importreview.DirectImportOperationHash(project, "browser_recorder", "dev-mode\x00session:"+id)
	actorHash := hashCanonical(struct {
		RecordingID string `json:"recording_id"`
		Actor       string `json:"actor"`
	}{strings.TrimSpace(id), "dev-mode"})
	for index, script := range prepared.TransactionScripts {
		if script.ProjectID != "" && normalizeProjectID(script.ProjectID) != project {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("converted script %d: project_id does not match script.project_id", index+1))
			return
		}
		script.ProjectID = project
		if req.FolderID != nil {
			folder := strings.TrimSpace(*req.FolderID)
			script.FolderID = nil
			if folder != "" {
				s.writeError(w, http.StatusServiceUnavailable, "script folder storage is unavailable")
				return
			}
		}
		if script.FolderID != nil && strings.TrimSpace(*script.FolderID) != "" {
			s.writeError(w, http.StatusServiceUnavailable, "script folder storage is unavailable")
			return
		}
		script.FolderID = nil
		script.ID = importreview.DeterministicDirectImportAssetID(operation, "script", index)
		script.RefKey = ""
		role := browserRecordingSaveRoleBrowser
		if prepared.PreparedHTTPScript != nil && index == len(prepared.TransactionScripts)-1 {
			role = browserRecordingSaveRoleHTTP
		}
		StampBrowserRecordingSaveProvenance(script, id, index, len(prepared.TransactionScripts), actorHash, role, false)
		payload, proofErr := BrowserRecordingSaveProvenanceProofPayload(script)
		if proofErr != nil {
			s.writeError(w, http.StatusInternalServerError, "failed to attest browser recording save provenance: "+proofErr.Error())
			return
		}
		script.Metadata.Import.Preserved[browserRecordingSaveProofKey] = hashCanonical(payload)
	}
	if s.Library == nil {
		s.writeError(w, http.StatusServiceUnavailable, "recording script library is unavailable")
		return
	}
	stored, saveErr := s.Library.save(prepared.TransactionScripts)
	if saveErr != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to save converted script: "+saveErr.Error())
		return
	}
	primary := stored[0]
	s.Recording.NotifyConverted(id, primary.ID, scriptdisclosure.ProjectLabel(primary.Name, primary.Script))
	warnings := append([]string(nil), prepared.Warnings...)
	var sourceHTTP *dsl.Script
	var httpStored *MemoryStoredScript
	if prepared.PreparedHTTPScript != nil {
		httpStored = stored[len(stored)-1]
		sourceHTTP = prepared.HTTPScript
		warnings = append(warnings, prepared.HTTPWarnings...)
		warnings = append(warnings, fmt.Sprintf("also created HTTP script %q (%s) from the recorded HAR", scriptdisclosure.ProjectLabel(httpStored.Name, httpStored.Script), httpStored.ID))
	}
	response := s.importResponse(r, importreview.ScriptImportResponseOptions{Script: prepared.Result.Script, Scripts: AppendBrowserRecordingScripts(prepared.Result.Scripts, sourceHTTP), Warnings: warnings, Stats: prepared.Result.Stats, Parameterization: importreview.BuildImportParameterization(prepared.Result.Script, nil), SkipDraftAttestation: true})
	response["script"] = primary
	response["scripts"] = stored
	if httpStored != nil {
		response["http_script"] = httpStored
	}
	s.writeImportResponse(w, http.StatusCreated, response)
}

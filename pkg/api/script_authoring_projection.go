package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errScriptAuthoringAlreadyExists = errors.New("script ID already exists; use a conditioned update")

var errScriptAuthoringUnsupported = errors.New("durable script authoring store unsupported")

const scriptAuthoringProjectionVersion = "apexload.script_authoring.v1"
const scriptAuthoringTagPrefix = "script-authoring-v1:"

// Only persisted authored fields belong to this representation. Enriched
// usage, activity, joined names and timestamps have independent lifecycles.
type ScriptAuthoringDocument struct {
	ID            string              `json:"id"`
	RefKey        string              `json:"ref_key,omitempty"`
	Name          string              `json:"name"`
	Version       string              `json:"version"`
	Protocol      string              `json:"protocol"`
	GoalConfig    *dsl.GoalConfig     `json:"goal_config,omitempty"`
	ProjectID     string              `json:"project_id,omitempty"`
	FolderID      *string             `json:"folder_id,omitempty"`
	GeneratedBy   string              `json:"generated_by,omitempty"`
	GeneratedFrom string              `json:"generated_from,omitempty"`
	Metadata      *dsl.ScriptMetadata `json:"metadata,omitempty"`
	Config        dsl.ScriptConfig    `json:"config"`
	DataSources   []dsl.DataSource    `json:"data_sources,omitempty"`
	Parameters    []dsl.Parameter     `json:"parameters,omitempty"`
	Init          []dsl.Action        `json:"init,omitempty"`
	Actions       []dsl.Action        `json:"actions"`
	Teardown      []dsl.Action        `json:"teardown,omitempty"`
}

type ScriptAuthoringPlacement struct {
	ApplicationID *string  `json:"application_id"`
	ReleaseIDs    []string `json:"release_ids"`
}

type ScriptAuthoringProjection struct {
	ProjectionVersion string                   `json:"projection_version"`
	ID                string                   `json:"id"`
	ProjectID         string                   `json:"project_id"`
	FolderID          *string                  `json:"folder_id"`
	ContentRevision   string                   `json:"content_revision"`
	Script            ScriptAuthoringDocument  `json:"script"`
	LibraryPlacement  ScriptAuthoringPlacement `json:"library_placement"`
}

func scriptAuthoringETag(revision string) (string, error) {
	if !storage.ValidScriptContentRevision(revision) {
		return "", storage.ErrScriptAuthoringInvalidRevision
	}
	return `"` + scriptAuthoringTagPrefix + revision + `"`, nil
}

// HTTP libraries normalize request header keys, but callers constructing a
// request directly may not. Count every case-insensitive field and value.
func parseScriptAuthoringIfMatch(header http.Header) (string, bool, error) {
	var values []string
	fields := 0
	for key, entries := range header {
		if strings.EqualFold(key, "If-Match") {
			fields++
			values = append(values, entries...)
		}
	}
	if fields == 0 {
		return "", false, nil
	}
	if fields != 1 || len(values) != 1 {
		return "", true, storage.ErrScriptAuthoringInvalidRevision
	}
	value := strings.Trim(values[0], " \t")
	if strings.ContainsAny(value, "\r\n") {
		return "", true, storage.ErrScriptAuthoringInvalidRevision
	}
	prefix := `"` + scriptAuthoringTagPrefix
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, `"`) {
		return "", true, storage.ErrScriptAuthoringInvalidRevision
	}
	revision := strings.TrimSuffix(strings.TrimPrefix(value, prefix), `"`)
	if !storage.ValidScriptContentRevision(revision) {
		return "", true, storage.ErrScriptAuthoringInvalidRevision
	}
	return revision, true, nil
}

func cloneScriptAuthoringProjection(projection *ScriptAuthoringProjection) *ScriptAuthoringProjection {
	if projection == nil {
		return nil
	}
	body, err := json.Marshal(projection)
	if err != nil {
		return nil
	}
	var clone ScriptAuthoringProjection
	if json.Unmarshal(body, &clone) != nil {
		return nil
	}
	return &clone
}

func buildScriptAuthoringProjection(stored *StoredScript) (*ScriptAuthoringProjection, error) {
	if stored == nil || stored.Script == nil || !storage.ValidScriptContentRevision(stored.ContentRevision) {
		return nil, storage.ErrScriptAuthoringUnavailable
	}
	// Keep the established authoring disclosure contract, including protected
	// imports and exact CSV/DAT source bytes. Avoid projecting transient context.
	base := *stored
	base.AuthoringProjection = nil
	view := viewStoredScript(&base)
	if view == nil || view.Script == nil {
		return nil, storage.ErrScriptAuthoringUnavailable
	}
	script := view.Script
	placement := ScriptAuthoringPlacement{ReleaseIDs: []string{}}
	if assignment := stored.LibraryAssignment; assignment != nil {
		if assignment.Application != nil {
			id := assignment.Application.ID
			placement.ApplicationID = &id
		}
		for _, release := range assignment.Releases {
			placement.ReleaseIDs = append(placement.ReleaseIDs, release.ID)
		}
		sort.Strings(placement.ReleaseIDs)
	}
	return &ScriptAuthoringProjection{
		ProjectionVersion: scriptAuthoringProjectionVersion,
		ID:                stored.ID, ProjectID: stored.ProjectID, FolderID: cloneStringPtr(stored.FolderID),
		ContentRevision: stored.ContentRevision, LibraryPlacement: placement,
		Script: scriptAuthoringDocumentFromScript(script),
	}, nil
}

// Pure draft helpers use exactly the same authored-field boundary as reads.
// They must not introduce zero clock fields, context, authority or a new tag.
func scriptAuthoringDocumentFromScript(script *dsl.Script) ScriptAuthoringDocument {
	return ScriptAuthoringDocument{
		ID: script.ID, RefKey: script.RefKey, Name: script.Name, Version: script.Version,
		Protocol: script.Protocol, GoalConfig: script.GoalConfig, ProjectID: script.ProjectID,
		FolderID: cloneStringPtr(script.FolderID), GeneratedBy: script.GeneratedBy,
		GeneratedFrom: script.GeneratedFrom, Metadata: script.Metadata, Config: script.Config,
		DataSources: script.DataSources, Parameters: script.Parameters, Init: script.Init,
		Actions: script.Actions, Teardown: script.Teardown,
	}
}

func storedScriptFromAuthoringSnapshot(snapshot *storage.ScriptAuthoringSnapshot) (*StoredScript, error) {
	if snapshot == nil || snapshot.Script == nil || !storage.ValidScriptContentRevision(snapshot.ContentRevision) {
		return nil, storage.ErrScriptAuthoringUnavailable
	}
	script := cloneScriptDefinition(snapshot.Script)
	stored := &StoredScript{
		ID: script.ID, RefKey: script.RefKey, Name: script.Name, Protocol: script.Protocol,
		Version: script.Version, ProjectID: script.ProjectID, FolderID: cloneStringPtr(script.FolderID),
		CreatedAt: script.CreatedAt, ArchivedAt: cloneTimePtr(script.ArchivedAt), Script: script,
		ContentRevision: snapshot.ContentRevision, LibraryAssignment: snapshot.LibraryAssignment,
		AuthoringHealth: buildStoredScriptAuthoringHealth(script),
	}
	projection, err := buildScriptAuthoringProjection(stored)
	if err != nil {
		return nil, err
	}
	stored.AuthoringProjection = projection
	return stored, nil
}

// Read the script, nonce and authored placement from one durable snapshot,
// bypassing the HA display cache. Private CI and pending publication remain
// hidden. A caller cannot reconstruct a revision from a timestamp or cache.
func (tr *TestRunner) GetScriptAuthoringDurable(id string) (*StoredScript, bool, error) {
	requestedID := strings.TrimSpace(id)
	parts, reference := refkey.Parse(normalizeRefKeyCandidate(requestedID))
	reference = reference && parts.Type == refkey.TypeScript
	tr.mu.RLock()
	if reference {
		id = requestedID
	} else {
		id = tr.resolveRefKeyLocked(id)
	}
	store, supported := tr.scriptStore.(storage.ScriptAuthoringStore)
	tr.mu.RUnlock()
	if !supported {
		return nil, false, errScriptAuthoringUnsupported
	}
	snapshot, err := store.GetScriptAuthoringSnapshot(id)
	if reference && (errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScriptAuthoringNotFound)) {
		if reader, supported := store.(interface {
			GetScriptAuthoringSnapshotByRefKey(string) (*storage.ScriptAuthoringSnapshot, error)
		}); supported {
			// Cache aliases are entirely uppercase; durable script keys keep the
			// lowercase version marker. Preserve numeric spelling and normalize
			// only the documented case-insensitive HTTP alias components.
			canonicalKey := normalizeRefKeyCandidate(requestedID)
			versionStart := strings.LastIndexByte(canonicalKey, '-') + 1
			canonicalKey = canonicalKey[:versionStart] + strings.ToLower(canonicalKey[versionStart:])
			snapshot, err = reader.GetScriptAuthoringSnapshotByRefKey(canonicalKey)
		} else {
			// Preserve the historical optional-store alias behavior without pairing
			// a cached nonce with content. Production resolves aliases durably.
			tr.refreshScriptsForHARead(true)
			tr.mu.RLock()
			resolved := tr.resolveRefKeyLocked(requestedID)
			tr.mu.RUnlock()
			if resolved != requestedID {
				snapshot, err = store.GetScriptAuthoringSnapshot(resolved)
			}
		}
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScriptAuthoringNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if snapshot == nil || isCIScriptSnapshot(snapshot.Script) {
		return nil, false, nil
	}
	stored, err := storedScriptFromAuthoringSnapshot(snapshot)
	if err != nil {
		return nil, false, err
	}
	if _, published := tr.publishedScriptSnapshot(stored); !published {
		return nil, false, nil
	}
	return stored, true, nil
}

func scriptAuthoringPrecondition(stored *StoredScript) storage.ScriptAuthoringPrecondition {
	return storage.ScriptAuthoringPrecondition{ProjectID: stored.ProjectID, ContentRevision: stored.ContentRevision, CreatedAt: stored.Script.CreatedAt}
}

// UpdateScriptAuthoringDurable retains every existing execution, secret and
// approval guard while binding the final write to the caller's durable nonce.
func (tr *TestRunner) UpdateScriptAuthoringDurable(id string, script *dsl.Script, expected storage.ScriptAuthoringPrecondition) (*StoredScript, bool, error) {
	if !storage.ValidScriptContentRevision(expected.ContentRevision) {
		return nil, false, storage.ErrScriptAuthoringInvalidRevision
	}
	return tr.updateScriptDurableInProject(id, script, expected.ProjectID, time.Time{}, "", "", time.Time{}, false, "", &expected)
}

// Ordinary creation accepts a primary ID, never a display alias. This early
// check avoids credential/source work; the final create-only insert remains
// authoritative when another writer races it.
func (tr *TestRunner) scriptPrimaryIDForCreate(id string) (*dsl.Script, bool, error) {
	id = strings.TrimSpace(id)
	tr.mu.RLock()
	store := tr.scriptStore
	cached := tr.scripts[id]
	var local *dsl.Script
	if store == nil && cached != nil {
		local = cloneScriptDefinition(cached.Script)
	}
	tr.mu.RUnlock()
	if store != nil {
		reader, supported := store.(interface {
			GetScript(string) (*dsl.Script, error)
		})
		if !supported {
			return nil, false, errScriptAuthoringUnsupported
		}
		script, err := reader.GetScript(id)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrScriptAuthoringNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if script == nil {
			return nil, false, storage.ErrScriptAuthoringUnavailable
		}
		return script, true, nil
	}
	return local, local != nil, nil
}

// Authorization precedes capability/precondition disclosure, including a
// malformed durable nonce. Unsupported stores remain an explicit legacy read.
func (s *Server) loadScriptAuthoringRequest(w http.ResponseWriter, r *http.Request, id string) (*StoredScript, bool, bool) {
	stored, found, err := s.runner.GetScriptAuthoringDurable(id)
	supported := !errors.Is(err, errScriptAuthoringUnsupported)
	if err != nil {
		fallback, exists, readErr := s.runner.GetScriptDurable(id)
		if errors.Is(readErr, sql.ErrNoRows) || errors.Is(readErr, storage.ErrScriptAuthoringNotFound) || (readErr == nil && !exists) {
			writeError(w, http.StatusNotFound, "script not found")
			return nil, supported, false
		}
		if readErr == nil && fallback != nil && !requireScriptAuthoringProjectMatch(w, r, fallback.ProjectID) {
			return nil, supported, false
		}
		if supported || readErr != nil {
			writeError(w, http.StatusServiceUnavailable, "script authoring snapshot unavailable")
			return nil, supported, false
		}
		stored, found = fallback, exists
	}
	if !found || stored == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return nil, supported, false
	}
	if !requireScriptAuthoringProjectMatch(w, r, stored.ProjectID) {
		return nil, supported, false
	}
	return stored, supported, true
}

// Only ordinary PUT compatibility uses this policy. Internal/import writers
// keep their existing explicit fences and cannot opt into legacy admission.
func (s *Server) admitScriptSavePrecondition(w http.ResponseWriter, r *http.Request, stored *StoredScript, supported bool) (*storage.ScriptAuthoringPrecondition, string, bool) {
	revision, present, err := parseScriptAuthoringIfMatch(r.Header)
	if err != nil {
		writeError(w, http.StatusBadRequest, "If-Match must contain one strong script authoring v1 tag")
		return nil, "", false
	}
	if present {
		if !supported {
			writeError(w, http.StatusServiceUnavailable, "durable script content revisions unavailable")
			return nil, "", false
		}
		if revision != stored.ContentRevision {
			writeError(w, http.StatusPreconditionFailed, "script authored content changed; reload before applying changes")
			return nil, "", false
		}
		expected := scriptAuthoringPrecondition(stored)
		return &expected, "conditioned", true
	}
	// A missing capability cannot bypass an explicitly configured strict policy.
	// Store-backed compatibility is audited; historical memory-only mode is
	// unqualified and cannot advertise a durable authoring projection.
	if s.store != nil {
		project, err := s.store.GetProject(stored.ProjectID)
		if err != nil || project == nil {
			writeError(w, http.StatusServiceUnavailable, "script save policy unavailable")
			return nil, "", false
		}
		if project.Settings.Authoring != nil && project.Settings.Authoring.RequireScriptSaveIfMatch {
			writeError(w, http.StatusPreconditionRequired, "If-Match is required for script saves in this project")
			return nil, "", false
		}
	} else if supported {
		writeError(w, http.StatusServiceUnavailable, "script save policy unavailable")
		return nil, "", false
	}
	if !supported {
		return nil, "legacy_unconditioned", true
	}
	expected := scriptAuthoringPrecondition(stored)
	expected.LegacyOrdinarySave = true
	return &expected, "legacy_unconditioned", true
}

func requireScriptAuthoringProjectMatch(w http.ResponseWriter, r *http.Request, projectID string) bool {
	if expected, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(projectID) != expected {
		writeError(w, http.StatusNotFound, "script not found")
		return false
	}
	return true
}

func writeScriptAuthoringMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrScriptAuthoringNotFound), errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "script not found")
	case errors.Is(err, storage.ErrScriptAuthoringPreconditionRequired):
		writeError(w, http.StatusPreconditionRequired, "If-Match is required for script saves in this project")
	case errors.Is(err, storage.ErrScriptAuthoringConflict):
		writeError(w, http.StatusPreconditionFailed, "script authored content changed; reload before applying changes")
	case errors.Is(err, storage.ErrScriptAuthoringInvalidRevision):
		writeError(w, http.StatusBadRequest, "invalid script authoring precondition")
	case errors.Is(err, storage.ErrScriptAuthoringUnavailable), errors.Is(err, errScriptAuthoringUnsupported):
		writeError(w, http.StatusServiceUnavailable, "durable script content revisions unavailable")
	case errors.Is(err, errScriptConcurrentChange):
		writeError(w, http.StatusConflict, "script changed; reload before applying changes")
	default:
		writeError(w, http.StatusInternalServerError, "failed to update script")
	}
}

// Ordinary POST creates only. Approval/import receipt replay retains its
// separately governed create-only/equivalence contract.
func (tr *TestRunner) CreateScriptAuthoringDurable(script *dsl.Script) (*StoredScript, error) {
	stored, _, err := tr.createScriptDurable(script, false, false, "", true)
	return stored, err
}

// Fixed workflow guidance is safe to disclose; persistence errors and request
// details still use the standard redacted internal-error path.
type ScriptAuthoringSaveOutcomeError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	Ref   string `json:"ref"`
}

func writeScriptAuthoringSaveOutcomeUnavailable(w http.ResponseWriter, unknown bool) {
	code := "script_save_outcome_audit_unavailable"
	if unknown {
		code = "script_save_outcome_unknown"
	}
	reference := errorReferenceFor(w)
	slog.Error("script save outcome needs verification", "ref", reference, "code", code)
	writeJSON(w, http.StatusServiceUnavailable, ScriptAuthoringSaveOutcomeError{
		Error: "script save outcome requires verification; reload the script before retrying",
		Code:  code, Ref: reference,
	})
}

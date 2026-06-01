package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const libraryBundleSchemaVersion = "apexload.library_bundle.v1"

type LibraryCapabilitiesResponse struct {
	SchemaVersion string    `json:"schema_version"`
	InstanceID    string    `json:"instance_id"`
	GeneratedAt   time.Time `json:"generated_at"`
	ItemKinds     []string  `json:"item_kinds"`
	Operations    []string  `json:"operations"`
}

type LibraryCatalogResponse struct {
	SchemaVersion string               `json:"schema_version"`
	InstanceID    string               `json:"instance_id"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Items         []LibraryCatalogItem `json:"items"`
	Count         int                  `json:"count"`
}

type LibraryCatalogItem struct {
	Kind             string              `json:"kind"`
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	ProjectID        string              `json:"project_id,omitempty"`
	Protocol         string              `json:"protocol,omitempty"`
	Version          string              `json:"version,omitempty"`
	Status           string              `json:"status,omitempty"`
	ContentHash      string              `json:"content_hash"`
	SourceInstanceID string              `json:"source_instance_id"`
	SourceItemID     string              `json:"source_item_id"`
	UpdatedAt        time.Time           `json:"updated_at"`
	HasArchive       bool                `json:"has_archive,omitempty"`
	Dependencies     []LibraryDependency `json:"dependencies,omitempty"`
	SyncStatus       string              `json:"sync_status,omitempty"`
	LocalItemID      string              `json:"local_item_id,omitempty"`
	RemoteItemID     string              `json:"remote_item_id,omitempty"`
}

type LibraryDependency struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type LibraryBundle struct {
	SchemaVersion string              `json:"schema_version"`
	BundleID      string              `json:"bundle_id"`
	CreatedAt     time.Time           `json:"created_at"`
	Source        LibraryBundleSource `json:"source"`
	Items         []LibraryBundleItem `json:"items"`
	Provenance    map[string]any      `json:"provenance,omitempty"`
}

type LibraryBundleSource struct {
	InstanceID string `json:"instance_id"`
	ProjectID  string `json:"project_id,omitempty"`
}

type LibraryBundleItem struct {
	Kind             string                  `json:"kind"`
	SourceInstanceID string                  `json:"source_instance_id"`
	SourceItemID     string                  `json:"source_item_id"`
	ProjectID        string                  `json:"project_id,omitempty"`
	Name             string                  `json:"name"`
	ContentHash      string                  `json:"content_hash"`
	CreatedAt        time.Time               `json:"created_at,omitempty"`
	UpdatedAt        time.Time               `json:"updated_at,omitempty"`
	Script           *dsl.Script             `json:"script,omitempty"`
	Scenario         *LibraryScenarioPayload `json:"scenario,omitempty"`
	Result           *LibraryResultPayload   `json:"result,omitempty"`
	Dependencies     []LibraryDependency     `json:"dependencies,omitempty"`
}

type LibraryScenarioPayload struct {
	ID      string                `json:"id"`
	Request CreateScenarioRequest `json:"request"`
}

type LibraryResultPayload struct {
	Test          *ManagedTest              `json:"test,omitempty"`
	Archive       *storage.RunArchiveRecord `json:"archive,omitempty"`
	ArchiveZipB64 string                    `json:"archive_zip_b64,omitempty"`
	Summary       map[string]any            `json:"summary,omitempty"`
}

type LibraryImportRequest struct {
	Bundle    LibraryBundle `json:"bundle"`
	ProjectID string        `json:"project_id,omitempty"`
	Mode      string        `json:"mode,omitempty"`
}

type LibraryImportResponse struct {
	SchemaVersion string                    `json:"schema_version"`
	ImportedAt    time.Time                 `json:"imported_at"`
	Items         []LibraryImportResultItem `json:"items"`
	Conflicts     []LibraryConflict         `json:"conflicts,omitempty"`
}

type LibraryImportResultItem struct {
	Kind             string `json:"kind"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceItemID     string `json:"source_item_id"`
	LocalItemID      string `json:"local_item_id"`
	ContentHash      string `json:"content_hash"`
	Action           string `json:"action"`
}

type LibraryConflict struct {
	Kind             string `json:"kind"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceItemID     string `json:"source_item_id"`
	LocalItemID      string `json:"local_item_id,omitempty"`
	Reason           string `json:"reason"`
}

type LibraryRemote struct {
	ID               string                       `json:"id"`
	Name             string                       `json:"name"`
	BaseURL          string                       `json:"base_url"`
	DefaultProjectID string                       `json:"default_project_id,omitempty"`
	AuthMethod       string                       `json:"auth_method"`
	Status           string                       `json:"status,omitempty"`
	LastHealthCheck  *time.Time                   `json:"last_health_check,omitempty"`
	LastError        string                       `json:"last_error,omitempty"`
	Capabilities     *LibraryCapabilitiesResponse `json:"capabilities,omitempty"`
	APIKey           string                       `json:"-"`
	CreatedAt        time.Time                    `json:"created_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
}

type LibraryRemoteRequest struct {
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	DefaultProjectID string `json:"default_project_id,omitempty"`
	AuthMethod       string `json:"auth_method,omitempty"`
	APIKey           string `json:"api_key,omitempty"`
}

type LibraryRemoteListResponse struct {
	Remotes []LibraryRemote `json:"remotes"`
	Count   int             `json:"count"`
}

type LibraryRemoteCatalogResponse struct {
	Remote  LibraryRemote          `json:"remote"`
	Catalog LibraryCatalogResponse `json:"catalog"`
}

type LibraryTransferRequest struct {
	Kind                string `json:"kind"`
	ID                  string `json:"id"`
	LocalID             string `json:"local_id,omitempty"`
	RemoteID            string `json:"remote_id,omitempty"`
	ProjectID           string `json:"project_id,omitempty"`
	IncludeDependencies bool   `json:"include_dependencies,omitempty"`
}

type LibraryRemoteTransferResponse struct {
	Remote LibraryRemote          `json:"remote"`
	Bundle *LibraryBundle         `json:"bundle,omitempty"`
	Import *LibraryImportResponse `json:"import,omitempty"`
}

type librarySyncLink struct {
	RemoteID          string
	Kind              string
	SourceInstanceID  string
	SourceItemID      string
	LocalItemID       string
	RemoteItemID      string
	LocalContentHash  string
	RemoteContentHash string
	LastSyncedAt      time.Time
}

func (s *Server) handleLibraryCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, LibraryCapabilitiesResponse{
		SchemaVersion: libraryBundleSchemaVersion,
		InstanceID:    s.controllerID,
		GeneratedAt:   time.Now().UTC(),
		ItemKinds:     []string{"script", "scenario", "result"},
		Operations:    []string{"catalog", "export", "import", "remote_catalog", "pull", "push", "diff"},
	})
}

func (s *Server) handleLibraryCatalog(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	kind := normalizeLibraryKind(r.URL.Query().Get("kind"))
	if kind == "" && strings.TrimSpace(r.URL.Query().Get("kind")) != "" {
		writeError(w, http.StatusBadRequest, "kind must be one of script, scenario, or result")
		return
	}
	items := s.libraryCatalogItems(projectID, kind)
	writeJSON(w, http.StatusOK, LibraryCatalogResponse{
		SchemaVersion: libraryBundleSchemaVersion,
		InstanceID:    s.controllerID,
		GeneratedAt:   time.Now().UTC(),
		Items:         items,
		Count:         len(items),
	})
}

func (s *Server) handleLibraryExport(w http.ResponseWriter, r *http.Request) {
	var req LibraryTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	bundle, err := s.buildLibraryBundle(req.Kind, req.ID, req.ProjectID, req.IncludeDependencies)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "library.export", "success", "library_bundle", bundle.BundleID, bundle.Source.ProjectID, map[string]any{"kind": normalizeLibraryKind(req.Kind), "source_item_id": req.ID})
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) handleLibraryImport(w http.ResponseWriter, r *http.Request) {
	var req LibraryImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	result, err := s.importLibraryBundle(req, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if len(result.Conflicts) > 0 {
		status = http.StatusConflict
	}
	s.recordAuditEvent(r, "library.import", "success", "library_bundle", req.Bundle.BundleID, normalizeProjectID(req.ProjectID), map[string]any{"items": len(result.Items), "conflicts": len(result.Conflicts)})
	writeJSON(w, status, result)
}

func (s *Server) handleLibraryListRemotes(w http.ResponseWriter, r *http.Request) {
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	remotes := make([]LibraryRemote, 0, len(s.libraryRemotes))
	for _, remote := range s.libraryRemotes {
		remotes = append(remotes, sanitizeLibraryRemote(remote))
	}
	sort.SliceStable(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i].Name) < strings.ToLower(remotes[j].Name)
	})
	writeJSON(w, http.StatusOK, LibraryRemoteListResponse{Remotes: remotes, Count: len(remotes)})
}

func (s *Server) handleLibraryCreateRemote(w http.ResponseWriter, r *http.Request) {
	var req LibraryRemoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	remote, err := buildLibraryRemote(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.libraryMu.Lock()
	s.libraryRemotes[remote.ID] = remote
	s.libraryMu.Unlock()
	writeJSON(w, http.StatusCreated, sanitizeLibraryRemote(remote))
}

func (s *Server) handleLibraryProbeRemote(w http.ResponseWriter, r *http.Request) {
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	caps, err := s.remoteLibraryCapabilities(remote)
	now := time.Now().UTC()
	s.libraryMu.Lock()
	if stored := s.libraryRemotes[remote.ID]; stored != nil {
		stored.LastHealthCheck = &now
		stored.UpdatedAt = now
		if err != nil {
			stored.Status = "unreachable"
			stored.LastError = err.Error()
		} else {
			stored.Status = "healthy"
			stored.LastError = ""
			stored.Capabilities = caps
		}
		remote = sanitizeLibraryRemote(stored)
	}
	s.libraryMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "unreachable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "healthy", "capabilities": caps})
}

func (s *Server) handleLibraryRemoteCatalog(w http.ResponseWriter, r *http.Request) {
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	projectID := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("project_id"), remote.DefaultProjectID))
	kind := normalizeLibraryKind(r.URL.Query().Get("kind"))
	catalog, err := s.remoteLibraryCatalog(remote, projectID, kind)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	for i := range catalog.Items {
		catalog.Items[i] = s.annotateRemoteCatalogItem(remote.ID, catalog.Items[i])
	}
	writeJSON(w, http.StatusOK, LibraryRemoteCatalogResponse{Remote: sanitizeLibraryRemote(&remote), Catalog: *catalog})
}

func (s *Server) handleLibraryRemotePull(w http.ResponseWriter, r *http.Request) {
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		req.ProjectID = remote.DefaultProjectID
	}
	bundle, err := s.remoteLibraryExport(remote, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	imported, err := s.importLibraryBundle(LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "pull"}, remote.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if len(imported.Conflicts) > 0 {
		status = http.StatusConflict
	}
	s.recordAuditEvent(r, "library.pull", "success", "library_bundle", bundle.BundleID, req.ProjectID, map[string]any{
		"remote_id":      remote.ID,
		"remote_name":    remote.Name,
		"kind":           normalizeLibraryKind(req.Kind),
		"source_item_id": req.ID,
		"items":          len(imported.Items),
		"conflicts":      len(imported.Conflicts),
	})
	writeJSON(w, status, LibraryRemoteTransferResponse{Remote: sanitizeLibraryRemote(&remote), Bundle: bundle, Import: imported})
}

func (s *Server) handleLibraryRemotePush(w http.ResponseWriter, r *http.Request) {
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		req.ProjectID = remote.DefaultProjectID
	}
	bundle, err := s.buildLibraryBundle(req.Kind, req.ID, req.ProjectID, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imported, err := s.remoteLibraryImport(remote, LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "push"})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.rememberRemoteImportLinks(remote.ID, bundle, imported, req.Kind, req.ID)
	status := http.StatusOK
	if len(imported.Conflicts) > 0 {
		status = http.StatusConflict
	}
	s.recordAuditEvent(r, "library.push", "success", "library_bundle", bundle.BundleID, req.ProjectID, map[string]any{
		"remote_id":      remote.ID,
		"remote_name":    remote.Name,
		"kind":           normalizeLibraryKind(req.Kind),
		"source_item_id": req.ID,
		"items":          len(imported.Items),
		"conflicts":      len(imported.Conflicts),
	})
	writeJSON(w, status, LibraryRemoteTransferResponse{Remote: sanitizeLibraryRemote(&remote), Bundle: bundle, Import: imported})
}

func (s *Server) handleLibraryRemoteDiff(w http.ResponseWriter, r *http.Request) {
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	localID := firstNonEmpty(req.LocalID, req.ID)
	remoteItemID := firstNonEmpty(req.RemoteID, req.ID)
	localHash, err := s.localLibraryItemHash(req.Kind, localID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		req.ProjectID = remote.DefaultProjectID
	}
	req.ID = remoteItemID
	bundle, err := s.remoteLibraryExport(remote, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	remoteHash := ""
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) == normalizeLibraryKind(req.Kind) {
			remoteHash = item.ContentHash
			break
		}
	}
	status := "changed"
	if localHash == remoteHash {
		status = "synced"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"remote":      sanitizeLibraryRemote(&remote),
		"kind":        normalizeLibraryKind(req.Kind),
		"local_id":    localID,
		"remote_id":   remoteItemID,
		"local_hash":  localHash,
		"remote_hash": remoteHash,
		"status":      status,
	})
}

func (s *Server) libraryCatalogItems(projectID string, kind string) []LibraryCatalogItem {
	projectID = strings.TrimSpace(projectID)
	items := []LibraryCatalogItem{}
	if kind == "" || kind == "script" {
		scripts := s.runner.ListScripts()
		if projectID != "" {
			scripts = s.runner.ListScriptsFiltered(projectID, nil)
		}
		for _, script := range scripts {
			if script == nil || script.Script == nil {
				continue
			}
			items = append(items, LibraryCatalogItem{
				Kind:             "script",
				ID:               script.ID,
				Name:             script.Name,
				ProjectID:        script.ProjectID,
				Protocol:         script.Protocol,
				Version:          script.Version,
				ContentHash:      hashLibraryScript(script.Script),
				SourceInstanceID: s.controllerID,
				SourceItemID:     script.ID,
				UpdatedAt:        script.CreatedAt,
			})
		}
	}
	if kind == "" || kind == "scenario" {
		scenarios := s.runner.ListScenarios()
		if projectID != "" {
			scenarios = s.runner.ListScenariosFiltered(projectID)
		}
		for _, scenario := range scenarios {
			if scenario == nil {
				continue
			}
			items = append(items, LibraryCatalogItem{
				Kind:             "scenario",
				ID:               scenario.ID,
				Name:             scenario.Name,
				ProjectID:        scenario.ProjectID,
				ContentHash:      hashLibraryScenario(scenario),
				SourceInstanceID: s.controllerID,
				SourceItemID:     scenario.ID,
				UpdatedAt:        scenario.UpdatedAt,
				Dependencies:     scenarioDependencies(scenario, s),
			})
		}
	}
	if kind == "" || kind == "result" {
		tests := s.runner.ListTestsFiltered(projectID, nil)
		if projectID == "" {
			tests = s.runner.ListTests()
		}
		for _, test := range tests {
			if test == nil || test.Status == "QUEUED" || test.Status == "RUNNING" || test.Status == "PENDING" {
				continue
			}
			items = append(items, LibraryCatalogItem{
				Kind:             "result",
				ID:               test.ID,
				Name:             test.Name,
				ProjectID:        test.ProjectID,
				Status:           test.Status,
				ContentHash:      hashLibraryResult(test),
				SourceInstanceID: s.controllerID,
				SourceItemID:     test.ID,
				UpdatedAt:        managedTestCompletedAt(test),
				HasArchive:       test.Archive != nil,
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

func (s *Server) buildLibraryBundle(kind string, id string, projectID string, includeDependencies bool) (*LibraryBundle, error) {
	kind = normalizeLibraryKind(kind)
	if kind == "" {
		return nil, fmt.Errorf("kind must be one of script, scenario, or result")
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("id is required")
	}
	bundle := &LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		BundleID:      uuid.New().String(),
		CreatedAt:     time.Now().UTC(),
		Source:        LibraryBundleSource{InstanceID: s.controllerID, ProjectID: normalizeProjectID(projectID)},
		Provenance: map[string]any{
			"exported_by": "apexload",
			"mode":        "explicit",
		},
	}
	added := map[string]struct{}{}
	addItem := func(item LibraryBundleItem) {
		key := item.Kind + ":" + item.SourceItemID
		if _, ok := added[key]; ok {
			return
		}
		added[key] = struct{}{}
		if item.SourceInstanceID == "" {
			item.SourceInstanceID = s.controllerID
		}
		bundle.Items = append(bundle.Items, item)
	}
	switch kind {
	case "script":
		item, err := s.libraryScriptBundleItem(id)
		if err != nil {
			return nil, err
		}
		bundle.Source.ProjectID = item.ProjectID
		addItem(item)
	case "scenario":
		item, err := s.libraryScenarioBundleItem(id)
		if err != nil {
			return nil, err
		}
		bundle.Source.ProjectID = item.ProjectID
		if includeDependencies {
			for _, dep := range item.Dependencies {
				if dep.Kind == "script" {
					if scriptItem, err := s.libraryScriptBundleItem(dep.ID); err == nil {
						addItem(scriptItem)
					}
				}
			}
		}
		addItem(item)
	case "result":
		item, err := s.libraryResultBundleItem(id)
		if err != nil {
			return nil, err
		}
		bundle.Source.ProjectID = item.ProjectID
		addItem(item)
	}
	return bundle, nil
}

func (s *Server) libraryScriptBundleItem(id string) (LibraryBundleItem, error) {
	stored, ok := s.runner.GetScript(id)
	if !ok || stored == nil || stored.Script == nil {
		return LibraryBundleItem{}, fmt.Errorf("script not found")
	}
	script := cloneScriptForLibrary(stored.Script)
	return LibraryBundleItem{
		Kind:             "script",
		SourceInstanceID: s.controllerID,
		SourceItemID:     stored.ID,
		ProjectID:        stored.ProjectID,
		Name:             stored.Name,
		ContentHash:      hashLibraryScript(script),
		CreatedAt:        stored.CreatedAt,
		UpdatedAt:        stored.CreatedAt,
		Script:           script,
	}, nil
}

func (s *Server) libraryScenarioBundleItem(id string) (LibraryBundleItem, error) {
	scenario, ok := s.runner.GetScenario(id)
	if !ok || scenario == nil {
		return LibraryBundleItem{}, fmt.Errorf("scenario not found")
	}
	req := createScenarioRequestFromStored(scenario)
	return LibraryBundleItem{
		Kind:             "scenario",
		SourceInstanceID: s.controllerID,
		SourceItemID:     scenario.ID,
		ProjectID:        scenario.ProjectID,
		Name:             scenario.Name,
		ContentHash:      hashLibraryScenario(scenario),
		CreatedAt:        scenario.CreatedAt,
		UpdatedAt:        scenario.UpdatedAt,
		Scenario:         &LibraryScenarioPayload{ID: scenario.ID, Request: req},
		Dependencies:     scenarioDependencies(scenario, s),
	}, nil
}

func (s *Server) libraryResultBundleItem(id string) (LibraryBundleItem, error) {
	test, ok := s.runner.GetTest(id)
	if !ok || test == nil {
		return LibraryBundleItem{}, fmt.Errorf("result not found")
	}
	if test.Status == "QUEUED" || test.Status == "RUNNING" || test.Status == "PENDING" {
		return LibraryBundleItem{}, fmt.Errorf("only completed, failed, stopped, or aborted results can be exported")
	}
	result := &LibraryResultPayload{
		Test:    test,
		Archive: test.Archive,
		Summary: map[string]any{
			"status":       test.Status,
			"total_vusers": test.TotalVUsers,
			"duration":     test.DurationStr,
		},
	}
	if record, ok := s.runner.GetRunArchive(id); ok && record != nil {
		if err := ensureArchiveMaterialized(record); err == nil {
			var buf bytes.Buffer
			if err := zipDirectory(record.ArchivePath, &buf); err == nil {
				result.Archive = record
				result.ArchiveZipB64 = base64.StdEncoding.EncodeToString(buf.Bytes())
			}
		}
	}
	return LibraryBundleItem{
		Kind:             "result",
		SourceInstanceID: s.controllerID,
		SourceItemID:     test.ID,
		ProjectID:        test.ProjectID,
		Name:             test.Name,
		ContentHash:      hashLibraryResult(test),
		CreatedAt:        test.CreatedAt,
		UpdatedAt:        managedTestCompletedAt(test),
		Result:           result,
	}, nil
}

func (s *Server) importLibraryBundle(req LibraryImportRequest, remoteID string) (*LibraryImportResponse, error) {
	bundle := req.Bundle
	if bundle.SchemaVersion != libraryBundleSchemaVersion {
		return nil, fmt.Errorf("unsupported library bundle schema %q", bundle.SchemaVersion)
	}
	response := &LibraryImportResponse{SchemaVersion: libraryBundleSchemaVersion, ImportedAt: time.Now().UTC()}
	projectID := normalizeProjectID(req.ProjectID)
	idMap := map[string]string{}
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) != "script" || item.Script == nil {
			continue
		}
		localID, action, conflict := s.importLibraryScript(item, projectID, remoteID)
		if conflict != nil {
			response.Conflicts = append(response.Conflicts, *conflict)
			continue
		}
		idMap[item.SourceItemID] = localID
		response.Items = append(response.Items, LibraryImportResultItem{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, ContentHash: item.ContentHash, Action: action})
	}
	for _, item := range bundle.Items {
		switch normalizeLibraryKind(item.Kind) {
		case "script":
			continue
		case "scenario":
			localID, action, conflict := s.importLibraryScenario(item, projectID, remoteID, idMap)
			if conflict != nil {
				response.Conflicts = append(response.Conflicts, *conflict)
				continue
			}
			response.Items = append(response.Items, LibraryImportResultItem{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, ContentHash: item.ContentHash, Action: action})
		case "result":
			localID, action, conflict := s.importLibraryResult(item, projectID, remoteID)
			if conflict != nil {
				response.Conflicts = append(response.Conflicts, *conflict)
				continue
			}
			response.Items = append(response.Items, LibraryImportResultItem{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, ContentHash: item.ContentHash, Action: action})
		}
	}
	return response, nil
}

func (s *Server) importLibraryScript(item LibraryBundleItem, projectID string, remoteID string) (string, string, *LibraryConflict) {
	link := s.libraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID)
	localID := ""
	action := "created"
	if link != nil {
		localID = link.LocalItemID
		if local, ok := s.runner.GetScript(localID); ok && local != nil && hashLibraryScript(local.Script) != link.LocalContentHash && item.ContentHash != link.RemoteContentHash {
			return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "local and remote script changed since last sync"}
		}
		action = "updated"
	}
	script := cloneScriptForLibrary(item.Script)
	if projectID != "" {
		script.ProjectID = projectID
	}
	if localID == "" {
		script.ID = uuid.New().String()
	} else {
		script.ID = localID
	}
	stored := s.runner.StoreScript(script)
	s.rememberLibraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID, stored.ID, item.SourceItemID, hashLibraryScript(stored.Script), item.ContentHash)
	return stored.ID, action, nil
}

func (s *Server) importLibraryScenario(item LibraryBundleItem, projectID string, remoteID string, idMap map[string]string) (string, string, *LibraryConflict) {
	if item.Scenario == nil {
		return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "scenario payload is missing"}
	}
	link := s.libraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID)
	localID := ""
	action := "created"
	if link != nil {
		localID = link.LocalItemID
		if local, ok := s.runner.GetScenario(localID); ok && local != nil && hashLibraryScenario(local) != link.LocalContentHash && item.ContentHash != link.RemoteContentHash {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "local and remote scenario changed since last sync"}
		}
		action = "updated"
	}
	req := item.Scenario.Request
	for i := range req.Groups {
		if mapped := idMap[req.Groups[i].ScriptID]; mapped != "" {
			req.Groups[i].ScriptID = mapped
		}
	}
	if localID == "" {
		stored, err := s.runner.StoreScenario(req)
		if err != nil {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		if projectID != "" && stored.ProjectID != projectID {
			// Scenario project comes from referenced scripts. The imported scripts above carry the mapped project.
			stored.ProjectID = projectID
		}
		s.rememberLibraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID, stored.ID, item.SourceItemID, hashLibraryScenario(stored), item.ContentHash)
		return stored.ID, action, nil
	}
	stored, ok, err := s.runner.UpdateScenario(localID, req)
	if err != nil {
		return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: err.Error()}
	}
	if !ok {
		stored, err = s.runner.StoreScenario(req)
		if err != nil {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		action = "created"
	}
	s.rememberLibraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID, stored.ID, item.SourceItemID, hashLibraryScenario(stored), item.ContentHash)
	return stored.ID, action, nil
}

func (s *Server) importLibraryResult(item LibraryBundleItem, projectID string, remoteID string) (string, string, *LibraryConflict) {
	if item.Result == nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "result payload is missing"}
	}
	resultID := uuid.New().String()
	if item.Result.Test != nil {
		resultID = item.Result.Test.ID
	}
	imported := &storage.ExternalResultImport{
		ID:         uuid.New().String(),
		Provider:   "apexload",
		Name:       firstNonEmpty(strings.TrimSpace(item.Name), "APEX Load result import"),
		ProjectID:  normalizeProjectID(firstNonEmpty(projectID, item.ProjectID)),
		ScenarioID: "",
		Source:     "library://" + item.SourceInstanceID + "/" + item.SourceItemID,
		Summary:    item.Result.Summary,
		RawResult: map[string]any{
			"schema_version":  libraryBundleSchemaVersion,
			"source_instance": item.SourceInstanceID,
			"source_item_id":  item.SourceItemID,
			"content_hash":    item.ContentHash,
			"test":            item.Result.Test,
			"archive":         item.Result.Archive,
			"archive_zip_b64": item.Result.ArchiveZipB64,
		},
		CreatedAt: time.Now().UTC(),
	}
	if imported.Summary == nil {
		imported.Summary = map[string]any{"status": "imported", "provider": "apexload"}
	}
	imported.Summary["provider"] = "apexload"
	if err := s.saveExternalResultImport(imported); err != nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
	}
	s.rememberLibraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID, imported.ID, resultID, item.ContentHash, item.ContentHash)
	return imported.ID, "created", nil
}

func (s *Server) remoteLibraryCapabilities(remote LibraryRemote) (*LibraryCapabilitiesResponse, error) {
	var caps LibraryCapabilitiesResponse
	if err := s.remoteJSON(remote, http.MethodGet, "/api/v1/library/capabilities", nil, &caps); err != nil {
		return nil, err
	}
	return &caps, nil
}

func (s *Server) remoteLibraryCatalog(remote LibraryRemote, projectID string, kind string) (*LibraryCatalogResponse, error) {
	values := url.Values{}
	if projectID != "" {
		values.Set("project_id", projectID)
	}
	if kind != "" {
		values.Set("kind", kind)
	}
	path := "/api/v1/library/catalog"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var catalog LibraryCatalogResponse
	if err := s.remoteJSON(remote, http.MethodGet, path, nil, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (s *Server) remoteLibraryExport(remote LibraryRemote, req LibraryTransferRequest) (*LibraryBundle, error) {
	var bundle LibraryBundle
	if err := s.remoteJSON(remote, http.MethodPost, "/api/v1/library/export", req, &bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (s *Server) remoteLibraryImport(remote LibraryRemote, req LibraryImportRequest) (*LibraryImportResponse, error) {
	var imported LibraryImportResponse
	if _, err := s.remoteJSONAccepted(remote, http.MethodPost, "/api/v1/library/import", req, &imported, map[int]bool{http.StatusConflict: true}); err != nil {
		return nil, err
	}
	return &imported, nil
}

func (s *Server) remoteJSON(remote LibraryRemote, method string, path string, body any, out any) error {
	_, err := s.remoteJSONAccepted(remote, method, path, body, out, nil)
	return err
}

func (s *Server) remoteJSONAccepted(remote LibraryRemote, method string, path string, body any, out any, accepted map[int]bool) (int, error) {
	base, err := url.Parse(remote.BaseURL)
	if err != nil {
		return 0, err
	}
	rel, err := url.Parse(path)
	if err != nil {
		return 0, err
	}
	endpoint := base.ResolveReference(rel)
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, endpoint.String(), reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if remote.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+remote.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && !accepted[resp.StatusCode] {
		return resp.StatusCode, fmt.Errorf("remote library returned %s", resp.Status)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

func buildLibraryRemote(req LibraryRemoteRequest) (*LibraryRemote, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("remote name is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("remote base_url must be an absolute URL")
	}
	authMethod := strings.TrimSpace(req.AuthMethod)
	if authMethod == "" {
		authMethod = "service_account"
	}
	now := time.Now().UTC()
	return &LibraryRemote{
		ID:               uuid.New().String(),
		Name:             name,
		BaseURL:          baseURL,
		DefaultProjectID: normalizeProjectID(req.DefaultProjectID),
		AuthMethod:       authMethod,
		Status:           "unknown",
		APIKey:           strings.TrimSpace(req.APIKey),
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

func (s *Server) libraryRemote(id string) (LibraryRemote, bool) {
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	remote, ok := s.libraryRemotes[id]
	if !ok || remote == nil {
		return LibraryRemote{}, false
	}
	copied := *remote
	return copied, true
}

func sanitizeLibraryRemote(remote *LibraryRemote) LibraryRemote {
	if remote == nil {
		return LibraryRemote{}
	}
	copied := *remote
	copied.APIKey = ""
	return copied
}

func normalizeLibraryKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "script", "scripts":
		return "script"
	case "scenario", "scenarios":
		return "scenario"
	case "result", "results":
		return "result"
	default:
		return ""
	}
}

func hashLibraryScript(script *dsl.Script) string {
	if script == nil {
		return hashCanonical(nil)
	}
	copied := cloneScriptForLibrary(script)
	copied.ID = ""
	copied.ProjectID = ""
	copied.FolderID = nil
	return hashCanonical(copied)
}

func hashLibraryScenario(scenario *StoredScenario) string {
	if scenario == nil {
		return hashCanonical(nil)
	}
	req := createScenarioRequestFromStored(scenario)
	return hashCanonical(req)
}

func hashLibraryResult(test *ManagedTest) string {
	if test == nil {
		return hashCanonical(nil)
	}
	payload := map[string]any{
		"name":         test.Name,
		"status":       test.Status,
		"project_id":   test.ProjectID,
		"scenario_id":  test.ScenarioID,
		"total_vusers": test.TotalVUsers,
		"duration":     test.DurationStr,
		"started_at":   test.StartedAt,
		"ended_at":     test.EndedAt,
		"archive":      test.Archive,
		"error":        test.Error,
	}
	return hashCanonical(payload)
}

func hashCanonical(value any) string {
	payload, _ := json.Marshal(value)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func cloneScriptForLibrary(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	payload, _ := json.Marshal(script)
	var cloned dsl.Script
	_ = json.Unmarshal(payload, &cloned)
	return &cloned
}

func createScenarioRequestFromStored(scenario *StoredScenario) CreateScenarioRequest {
	req := CreateScenarioRequest{
		Name:             scenario.Name,
		Duration:         scenario.Duration,
		StartDelay:       scenario.StartDelay,
		ScenarioMode:     scenario.ScenarioMode,
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		Assignment:       scenario.Assignment,
		GoalConfig:       scenario.GoalConfig,
		RuntimeSettings:  scenario.RuntimeSettings,
		DataPools:        scenario.DataPools,
	}
	for _, script := range scenario.Scripts {
		req.Groups = append(req.Groups, CreateGroupEntry{
			Name:         script.Name,
			ScriptID:     script.ScriptID,
			VUsers:       script.VUsers,
			RampUp:       script.RampUp,
			RampDown:     script.RampDown,
			Stages:       script.Stages,
			Iterations:   script.Iterations,
			InitStrategy: script.Initialization,
		})
	}
	return req
}

func scenarioDependencies(scenario *StoredScenario, s *Server) []LibraryDependency {
	deps := []LibraryDependency{}
	if scenario == nil {
		return deps
	}
	seen := map[string]struct{}{}
	for _, ref := range scenario.Scripts {
		if ref.ScriptID == "" {
			continue
		}
		if _, ok := seen[ref.ScriptID]; ok {
			continue
		}
		seen[ref.ScriptID] = struct{}{}
		dep := LibraryDependency{Kind: "script", ID: ref.ScriptID, Name: ref.ScriptName}
		if stored, ok := s.runner.GetScript(ref.ScriptID); ok && stored != nil {
			dep.Name = firstNonEmpty(dep.Name, stored.Name)
			dep.ContentHash = hashLibraryScript(stored.Script)
		}
		deps = append(deps, dep)
	}
	return deps
}

func (s *Server) libraryLink(remoteID string, kind string, sourceInstanceID string, sourceItemID string) *librarySyncLink {
	if remoteID == "" {
		remoteID = "direct"
	}
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	link := s.libraryLinks[libraryLinkKey(remoteID, kind, sourceInstanceID, sourceItemID)]
	if link == nil {
		return nil
	}
	copied := *link
	return &copied
}

func (s *Server) rememberLibraryLink(remoteID string, kind string, sourceInstanceID string, sourceItemID string, localItemID string, remoteItemID string, localHash string, remoteHash string) {
	if remoteID == "" {
		remoteID = "direct"
	}
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	s.libraryLinks[libraryLinkKey(remoteID, kind, sourceInstanceID, sourceItemID)] = &librarySyncLink{
		RemoteID:          remoteID,
		Kind:              normalizeLibraryKind(kind),
		SourceInstanceID:  sourceInstanceID,
		SourceItemID:      sourceItemID,
		LocalItemID:       localItemID,
		RemoteItemID:      remoteItemID,
		LocalContentHash:  localHash,
		RemoteContentHash: remoteHash,
		LastSyncedAt:      time.Now().UTC(),
	}
}

func libraryLinkKey(remoteID string, kind string, sourceInstanceID string, sourceItemID string) string {
	return strings.Join([]string{strings.TrimSpace(remoteID), normalizeLibraryKind(kind), strings.TrimSpace(sourceInstanceID), strings.TrimSpace(sourceItemID)}, "\x00")
}

func (s *Server) annotateRemoteCatalogItem(remoteID string, item LibraryCatalogItem) LibraryCatalogItem {
	link := s.libraryLink(remoteID, item.Kind, item.SourceInstanceID, item.SourceItemID)
	if link == nil {
		item.SyncStatus = "host only"
		return item
	}
	item.LocalItemID = link.LocalItemID
	item.RemoteItemID = firstNonEmpty(link.RemoteItemID, item.ID)
	item.SyncStatus = "synced"
	if item.ContentHash != "" && item.ContentHash != link.RemoteContentHash {
		item.SyncStatus = "stale"
	}
	if localHash, err := s.localLibraryItemHash(item.Kind, link.LocalItemID); err == nil && localHash != link.LocalContentHash {
		if item.SyncStatus == "stale" {
			item.SyncStatus = "conflict"
		} else {
			item.SyncStatus = "local only"
		}
	}
	return item
}

func (s *Server) localLibraryItemHash(kind string, id string) (string, error) {
	switch normalizeLibraryKind(kind) {
	case "script":
		script, ok := s.runner.GetScript(id)
		if !ok {
			return "", fmt.Errorf("script not found")
		}
		return hashLibraryScript(script.Script), nil
	case "scenario":
		scenario, ok := s.runner.GetScenario(id)
		if !ok {
			return "", fmt.Errorf("scenario not found")
		}
		return hashLibraryScenario(scenario), nil
	case "result":
		test, ok := s.runner.GetTest(id)
		if !ok {
			return "", fmt.Errorf("result not found")
		}
		return hashLibraryResult(test), nil
	default:
		return "", fmt.Errorf("kind must be one of script, scenario, or result")
	}
}

func (s *Server) rememberRemoteImportLinks(remoteID string, bundle *LibraryBundle, imported *LibraryImportResponse, requestedKind string, requestedLocalID string) {
	if bundle == nil || imported == nil {
		return
	}
	localBySource := map[string]string{}
	for _, item := range bundle.Items {
		localBySource[item.SourceItemID] = item.SourceItemID
	}
	for _, item := range imported.Items {
		localID := localBySource[item.SourceItemID]
		if localID == "" && normalizeLibraryKind(item.Kind) == normalizeLibraryKind(requestedKind) {
			localID = requestedLocalID
		}
		if localID == "" {
			continue
		}
		localHash, _ := s.localLibraryItemHash(item.Kind, localID)
		s.rememberLibraryLink(remoteID, item.Kind, s.controllerID, localID, localID, item.LocalItemID, localHash, item.ContentHash)
	}
}

func resolveLibraryTransferProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req LibraryTransferRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if strings.HasSuffix(r.URL.Path, "/pull") {
		return "", 0, ""
	}
	if store == nil || strings.TrimSpace(req.ID) == "" {
		return "", 0, ""
	}
	lookupID := firstNonEmpty(req.LocalID, req.ID)
	switch normalizeLibraryKind(req.Kind) {
	case "script":
		script, err := store.GetScript(lookupID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		return normalizeProjectID(script.ProjectID), 0, ""
	case "scenario":
		_, scripts, err := store.GetScenario(lookupID)
		if err != nil {
			return "", http.StatusNotFound, "scenario not found"
		}
		return resolveProjectIDFromScenarioScripts(scripts, store)
	case "result":
		test, err := store.GetTest(lookupID)
		if err != nil {
			return "", http.StatusNotFound, "result not found"
		}
		return normalizeProjectID(test.ProjectID), 0, ""
	default:
		return "", http.StatusBadRequest, "kind must be one of script, scenario, or result"
	}
}

func resolveLibraryImportProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req LibraryImportRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if strings.TrimSpace(req.Bundle.Source.ProjectID) != "" {
		return normalizeProjectID(req.Bundle.Source.ProjectID), 0, ""
	}
	for _, item := range req.Bundle.Items {
		if strings.TrimSpace(item.ProjectID) != "" {
			return normalizeProjectID(item.ProjectID), 0, ""
		}
	}
	return "", 0, ""
}

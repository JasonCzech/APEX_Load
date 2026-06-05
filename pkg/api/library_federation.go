package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const libraryBundleSchemaVersion = "apexload.library_bundle.v1"
const libraryRemotesSettingKey = "library.remotes.v1"
const librarySyncLinksSettingKey = "library.sync_links.v1"
const defaultLibraryArchiveMaxBytes int64 = 512 * 1024 * 1024

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
	ReleaseID        string              `json:"release_id,omitempty"`
	ReleaseName      string              `json:"release_name,omitempty"`
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
	Test                *ManagedTest              `json:"test,omitempty"`
	Archive             *storage.RunArchiveRecord `json:"archive,omitempty"`
	ArchiveZipB64       string                    `json:"archive_zip_b64,omitempty"`
	ArchiveTransfer     string                    `json:"archive_transfer,omitempty"`
	ArchiveManifestHash string                    `json:"archive_manifest_hash,omitempty"`
	ArchiveSizeBytes    int64                     `json:"archive_size_bytes,omitempty"`
	Summary             map[string]any            `json:"summary,omitempty"`
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
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	BaseURL              string                       `json:"base_url"`
	DefaultProjectID     string                       `json:"default_project_id,omitempty"`
	AuthMethod           string                       `json:"auth_method"`
	CredentialRef        string                       `json:"credential_ref,omitempty"`
	CredentialConfigured bool                         `json:"credential_configured"`
	Status               string                       `json:"status,omitempty"`
	LastHealthCheck      *time.Time                   `json:"last_health_check,omitempty"`
	LastError            string                       `json:"last_error,omitempty"`
	Capabilities         *LibraryCapabilitiesResponse `json:"capabilities,omitempty"`
	APIKey               string                       `json:"-"`
	CreatedAt            time.Time                    `json:"created_at"`
	UpdatedAt            time.Time                    `json:"updated_at"`
}

type LibraryRemoteRequest struct {
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	DefaultProjectID string `json:"default_project_id,omitempty"`
	AuthMethod       string `json:"auth_method,omitempty"`
	CredentialRef    string `json:"credential_ref,omitempty"`
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

func (s *Server) loadLibraryState() {
	if s == nil || s.store == nil {
		return
	}
	if raw, err := s.store.GetSetting(libraryRemotesSettingKey); err != nil {
		slog.Warn("failed to load library remotes", "err", err)
	} else if strings.TrimSpace(raw) != "" {
		var remotes []LibraryRemote
		if err := json.Unmarshal([]byte(raw), &remotes); err != nil {
			slog.Warn("failed to decode library remotes", "err", err)
		} else {
			s.libraryMu.Lock()
			for i := range remotes {
				remote := remotes[i]
				remote.APIKey = ""
				remote.CredentialConfigured = false
				if remote.ID != "" {
					copied := remote
					s.libraryRemotes[remote.ID] = &copied
				}
			}
			s.libraryMu.Unlock()
		}
	}
	if raw, err := s.store.GetSetting(librarySyncLinksSettingKey); err != nil {
		slog.Warn("failed to load library sync links", "err", err)
	} else if strings.TrimSpace(raw) != "" {
		var links []librarySyncLink
		if err := json.Unmarshal([]byte(raw), &links); err != nil {
			slog.Warn("failed to decode library sync links", "err", err)
		} else {
			s.libraryMu.Lock()
			for i := range links {
				link := links[i]
				if link.Kind == "" || link.SourceItemID == "" {
					continue
				}
				copied := link
				s.libraryLinks[libraryLinkKey(link.RemoteID, link.Kind, link.SourceInstanceID, link.SourceItemID)] = &copied
			}
			s.libraryMu.Unlock()
		}
	}
}

func (s *Server) persistLibraryRemotes() {
	if s == nil || s.store == nil {
		return
	}
	s.libraryMu.Lock()
	remotes := make([]LibraryRemote, 0, len(s.libraryRemotes))
	for _, remote := range s.libraryRemotes {
		if remote == nil {
			continue
		}
		copied := *remote
		copied.APIKey = ""
		copied.CredentialConfigured = false
		remotes = append(remotes, copied)
	}
	sort.SliceStable(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i].Name) < strings.ToLower(remotes[j].Name)
	})
	s.libraryMu.Unlock()
	payload, err := json.Marshal(remotes)
	if err != nil {
		slog.Warn("failed to encode library remotes", "err", err)
		return
	}
	if err := s.store.SaveSetting(libraryRemotesSettingKey, string(payload)); err != nil {
		slog.Warn("failed to persist library remotes", "err", err)
	}
}

func (s *Server) persistLibraryLinks() {
	if s == nil || s.store == nil {
		return
	}
	s.libraryMu.Lock()
	links := make([]librarySyncLink, 0, len(s.libraryLinks))
	for _, link := range s.libraryLinks {
		if link == nil {
			continue
		}
		links = append(links, *link)
	}
	sort.SliceStable(links, func(i, j int) bool {
		return libraryLinkKey(links[i].RemoteID, links[i].Kind, links[i].SourceInstanceID, links[i].SourceItemID) <
			libraryLinkKey(links[j].RemoteID, links[j].Kind, links[j].SourceInstanceID, links[j].SourceItemID)
	})
	s.libraryMu.Unlock()
	payload, err := json.Marshal(links)
	if err != nil {
		slog.Warn("failed to encode library sync links", "err", err)
		return
	}
	if err := s.store.SaveSetting(librarySyncLinksSettingKey, string(payload)); err != nil {
		slog.Warn("failed to persist library sync links", "err", err)
	}
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

func (s *Server) handleLibraryImportArchive(w http.ResponseWriter, r *http.Request) {
	req, archiveImport, cleanup, err := readLibraryArchiveImportRequest(r)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.importLibraryBundleWithArchive(req, "", archiveImport)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if len(result.Conflicts) > 0 {
		status = http.StatusConflict
	}
	s.recordAuditEvent(r, "library.import_archive", "success", "library_bundle", req.Bundle.BundleID, normalizeProjectID(req.ProjectID), map[string]any{"items": len(result.Items), "conflicts": len(result.Conflicts), "archive_size_bytes": archiveImportSize(archiveImport)})
	writeJSON(w, status, result)
}

func (s *Server) handleLibraryResultArchive(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	test, ok := s.runner.GetTest(id)
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "result not found")
		return
	}
	record, ok := s.runner.GetRunArchive(id)
	if !ok || record == nil {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(test.Name)+"-"+id+".zip\"")
	w.Header().Set("Last-Modified", record.CompletedAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	if err := zipDirectory(record.ArchivePath, w); err != nil {
		slog.Warn("failed to stream library result archive", "test_id", id, "err", err)
	}
}

func (s *Server) handleLibraryListRemotes(w http.ResponseWriter, r *http.Request) {
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	remotes := make([]LibraryRemote, 0, len(s.libraryRemotes))
	for _, remote := range s.libraryRemotes {
		remotes = append(remotes, s.sanitizeLibraryRemote(remote))
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
	if err := s.storeRemoteCredential(remote, req.APIKey, req.CredentialRef); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.libraryMu.Lock()
	s.libraryRemotes[remote.ID] = remote
	s.libraryMu.Unlock()
	s.persistLibraryRemotes()
	s.recordAuditEvent(r, "library.remote.create", "success", "library_remote", remote.ID, "", map[string]any{"name": remote.Name, "base_url": remote.BaseURL})
	writeJSON(w, http.StatusCreated, s.sanitizeLibraryRemote(remote))
}

func (s *Server) handleLibraryUpdateRemote(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var req LibraryRemoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	s.libraryMu.Lock()
	remote := s.libraryRemotes[id]
	if remote == nil {
		s.libraryMu.Unlock()
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	updated := *remote
	s.libraryMu.Unlock()
	if strings.TrimSpace(req.Name) != "" {
		updated.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.BaseURL) != "" {
		baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			writeError(w, http.StatusBadRequest, "remote base_url must be an absolute URL")
			return
		}
		updated.BaseURL = baseURL
	}
	if strings.TrimSpace(req.AuthMethod) != "" {
		updated.AuthMethod = strings.TrimSpace(req.AuthMethod)
	}
	if req.DefaultProjectID != "" {
		updated.DefaultProjectID = normalizeProjectID(req.DefaultProjectID)
	}
	updated.UpdatedAt = time.Now().UTC()
	if err := s.storeRemoteCredential(&updated, req.APIKey, req.CredentialRef); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.libraryMu.Lock()
	s.libraryRemotes[id] = &updated
	s.libraryMu.Unlock()
	s.persistLibraryRemotes()
	s.recordAuditEvent(r, "library.remote.update", "success", "library_remote", id, "", map[string]any{"name": updated.Name, "base_url": updated.BaseURL})
	writeJSON(w, http.StatusOK, s.sanitizeLibraryRemote(&updated))
}

func (s *Server) handleLibraryDeleteRemote(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	s.libraryMu.Lock()
	remote := s.libraryRemotes[id]
	if remote == nil {
		s.libraryMu.Unlock()
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	delete(s.libraryRemotes, id)
	for key, link := range s.libraryLinks {
		if link != nil && link.RemoteID == id {
			delete(s.libraryLinks, key)
		}
	}
	s.libraryMu.Unlock()
	if s.libraryCreds != nil {
		if err := s.libraryCreds.DeleteRemoteAPIKey(id); err != nil {
			slog.Warn("failed to delete remote library credential", "remote_id", id, "err", err)
		}
	}
	s.persistLibraryRemotes()
	s.persistLibraryLinks()
	s.recordAuditEvent(r, "library.remote.delete", "success", "library_remote", id, "", map[string]any{"name": remote.Name})
	w.WriteHeader(http.StatusNoContent)
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
		remote = s.sanitizeLibraryRemote(stored)
	}
	s.libraryMu.Unlock()
	s.persistLibraryRemotes()
	if err != nil {
		s.recordAuditEvent(r, "library.remote.probe", "failure", "library_remote", remote.ID, "", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "unreachable", "error": err.Error()})
		return
	}
	s.recordAuditEvent(r, "library.remote.probe", "success", "library_remote", remote.ID, "", nil)
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
	writeJSON(w, http.StatusOK, LibraryRemoteCatalogResponse{Remote: s.sanitizeLibraryRemote(&remote), Catalog: *catalog})
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
	var archiveImport *libraryArchiveImport
	var cleanup func()
	if normalizeLibraryKind(req.Kind) == "result" && libraryBundleHasStreamedArchive(bundle) {
		archiveImport, cleanup, err = s.remoteLibraryDownloadArchive(remote, firstNonEmpty(req.RemoteID, req.ID))
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	imported, err := s.importLibraryBundleWithArchive(LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "pull"}, remote.ID, archiveImport)
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
	writeJSON(w, status, LibraryRemoteTransferResponse{Remote: s.sanitizeLibraryRemote(&remote), Bundle: bundle, Import: imported})
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
	var imported *LibraryImportResponse
	if normalizeLibraryKind(req.Kind) == "result" && libraryBundleHasStreamedArchive(bundle) {
		imported, err = s.remoteLibraryImportArchive(remote, LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "push"}, req.ID)
	} else {
		imported, err = s.remoteLibraryImport(remote, LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "push"})
	}
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
	writeJSON(w, status, LibraryRemoteTransferResponse{Remote: s.sanitizeLibraryRemote(&remote), Bundle: bundle, Import: imported})
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
		"remote":      s.sanitizeLibraryRemote(&remote),
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
			releaseRef := ReleaseRef{}
			if test.ReleaseID != "" {
				releaseRef = s.runner.releaseRefLocked(test.ReleaseID)
			}
			items = append(items, LibraryCatalogItem{
				Kind:             "result",
				ID:               test.ID,
				Name:             test.Name,
				ProjectID:        test.ProjectID,
				ReleaseID:        test.ReleaseID,
				ReleaseName:      releaseRef.Name,
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
		result.Archive = record
		if err := ensureArchiveMaterialized(record); err == nil {
			result.ArchiveTransfer = "stream"
			result.ArchiveManifestHash = hashArchiveManifest(record)
			result.ArchiveSizeBytes = archiveDirectorySize(record.ArchivePath)
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
	return s.importLibraryBundleWithArchive(req, remoteID, nil)
}

type libraryArchiveImport struct {
	ZipPath      string
	SizeBytes    int64
	ManifestHash string
}

func (s *Server) importLibraryBundleWithArchive(req LibraryImportRequest, remoteID string, archiveImport *libraryArchiveImport) (*LibraryImportResponse, error) {
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
			localID, action, conflict := s.importLibraryResult(item, projectID, remoteID, archiveImport)
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

func (s *Server) importLibraryResult(item LibraryBundleItem, projectID string, remoteID string, archiveImport *libraryArchiveImport) (string, string, *LibraryConflict) {
	if item.Result == nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "result payload is missing"}
	}
	resultID := uuid.New().String()
	if item.Result.Test != nil {
		resultID = item.Result.Test.ID
	}
	importID := uuid.New().String()
	rawResult := map[string]any{
		"schema_version":        libraryBundleSchemaVersion,
		"source_instance":       item.SourceInstanceID,
		"source_item_id":        item.SourceItemID,
		"content_hash":          item.ContentHash,
		"test":                  item.Result.Test,
		"archive":               item.Result.Archive,
		"archive_transfer":      item.Result.ArchiveTransfer,
		"archive_manifest_hash": item.Result.ArchiveManifestHash,
		"archive_size_bytes":    item.Result.ArchiveSizeBytes,
	}
	if archiveImport != nil && strings.TrimSpace(archiveImport.ZipPath) != "" {
		dir := libraryImportedArchiveDir(importID)
		if err := os.RemoveAll(dir); err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		if err := unzipArchiveFile(archiveImport.ZipPath, dir, libraryArchiveMaxBytes()); err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		manifestHash := hashArchiveManifestPath(filepath.Join(dir, "manifest.json"))
		sizeBytes := archiveDirectorySize(dir)
		if item.Result.ArchiveManifestHash != "" && manifestHash != "" && manifestHash != item.Result.ArchiveManifestHash {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "archive manifest hash does not match bundle metadata"}
		}
		if item.Result.ArchiveSizeBytes > 0 && sizeBytes != item.Result.ArchiveSizeBytes {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "archive size does not match bundle metadata"}
		}
		rawResult["archive_path"] = dir
		rawResult["imported_archive_manifest_hash"] = manifestHash
		rawResult["imported_archive_size_bytes"] = sizeBytes
		if item.Result.Archive != nil {
			copied := *item.Result.Archive
			copied.ArchivePath = dir
			copied.ManifestPath = filepath.Join(dir, "manifest.json")
			rawResult["archive"] = &copied
		}
	} else if strings.TrimSpace(item.Result.ArchiveZipB64) != "" {
		dir := libraryImportedArchiveDir(importID)
		if err := os.RemoveAll(dir); err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		if err := importLegacyArchiveZipB64(item.Result.ArchiveZipB64, dir, libraryArchiveMaxBytes()); err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		rawResult["archive_path"] = dir
		rawResult["imported_archive_manifest_hash"] = hashArchiveManifestPath(filepath.Join(dir, "manifest.json"))
		rawResult["imported_archive_size_bytes"] = archiveDirectorySize(dir)
	}
	imported := &storage.ExternalResultImport{
		ID:         importID,
		Provider:   "apexload",
		Name:       firstNonEmpty(strings.TrimSpace(item.Name), "APEX Load result import"),
		ProjectID:  normalizeProjectID(firstNonEmpty(projectID, item.ProjectID)),
		ScenarioID: "",
		Source:     "library://" + item.SourceInstanceID + "/" + item.SourceItemID,
		Summary:    item.Result.Summary,
		RawResult:  rawResult,
		CreatedAt:  time.Now().UTC(),
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

func (s *Server) remoteLibraryImportArchive(remote LibraryRemote, req LibraryImportRequest, resultID string) (*LibraryImportResponse, error) {
	record, ok := s.runner.GetRunArchive(resultID)
	if !ok || record == nil {
		return s.remoteLibraryImport(remote, req)
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, fmt.Errorf("materialize local archive: %w", err)
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	errCh := make(chan error, 1)
	go func() {
		defer writer.Close()
		bundlePart, err := multipartWriter.CreateFormField("bundle")
		if err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if err := json.NewEncoder(bundlePart).Encode(req.Bundle); err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if strings.TrimSpace(req.ProjectID) != "" {
			if err := multipartWriter.WriteField("project_id", req.ProjectID); err != nil {
				errCh <- err
				_ = multipartWriter.Close()
				return
			}
		}
		archivePart, err := multipartWriter.CreateFormFile("archive", sanitizeFilename(firstNonEmpty(resultID, "result"))+".zip")
		if err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if err := zipDirectory(record.ArchivePath, archivePart); err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if err := multipartWriter.Close(); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	base, err := url.Parse(remote.BaseURL)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	values := url.Values{}
	if strings.TrimSpace(req.ProjectID) != "" {
		values.Set("project_id", req.ProjectID)
	}
	endpoint := base.ResolveReference(&url.URL{Path: "/api/v1/library/import/archive", RawQuery: values.Encode()})
	httpReq, err := http.NewRequest(http.MethodPost, endpoint.String(), reader)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	httpReq.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if err := s.authorizeRemoteRequest(httpReq, remote); err != nil {
		_ = reader.Close()
		return nil, err
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		_ = reader.Close()
		if writeErr := <-errCh; writeErr != nil {
			return nil, writeErr
		}
		return nil, err
	}
	defer resp.Body.Close()
	writeErr := <-errCh
	if writeErr != nil {
		return nil, writeErr
	}
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && resp.StatusCode != http.StatusConflict {
		return nil, fmt.Errorf("remote library returned %s", resp.Status)
	}
	var imported LibraryImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&imported); err != nil {
		return nil, err
	}
	return &imported, nil
}

func (s *Server) remoteLibraryDownloadArchive(remote LibraryRemote, resultID string) (*libraryArchiveImport, func(), error) {
	base, err := url.Parse(remote.BaseURL)
	if err != nil {
		return nil, nil, err
	}
	path := "/api/v1/library/results/" + url.PathEscape(strings.TrimSpace(resultID)) + "/archive"
	endpoint := base.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	if err := s.authorizeRemoteRequest(req, remote); err != nil {
		return nil, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("remote library archive returned %s", resp.Status)
	}
	tmp, err := os.CreateTemp("", "apexload-library-archive-*.zip")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	written, err := copyWithMaxBytes(tmp, resp.Body, libraryArchiveMaxBytes())
	closeErr := tmp.Close()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if closeErr != nil {
		cleanup()
		return nil, nil, closeErr
	}
	return &libraryArchiveImport{ZipPath: tmp.Name(), SizeBytes: written}, cleanup, nil
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
	if err := s.authorizeRemoteRequest(req, remote); err != nil {
		return 0, err
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

func (s *Server) authorizeRemoteRequest(req *http.Request, remote LibraryRemote) error {
	apiKey, err := s.remoteLibraryAPIKey(remote)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return nil
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
		CredentialRef:    strings.TrimSpace(req.CredentialRef),
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

func (s *Server) storeRemoteCredential(remote *LibraryRemote, apiKey string, credentialRef string) error {
	if remote == nil {
		return fmt.Errorf("remote library is required")
	}
	apiKey = strings.TrimSpace(apiKey)
	credentialRef = strings.TrimSpace(credentialRef)
	if strings.EqualFold(remote.AuthMethod, "none") {
		remote.CredentialRef = ""
		remote.APIKey = ""
		remote.CredentialConfigured = true
		return nil
	}
	if apiKey != "" {
		if s.libraryCreds == nil {
			return fmt.Errorf("remote credential store is not configured")
		}
		ref, err := s.libraryCreds.PutRemoteAPIKey(remote.ID, apiKey)
		if err != nil {
			return err
		}
		remote.CredentialRef = strings.TrimSpace(ref)
		remote.APIKey = ""
		remote.CredentialConfigured = true
		return nil
	}
	if credentialRef != "" {
		remote.CredentialRef = credentialRef
	}
	remote.APIKey = ""
	remote.CredentialConfigured = s.remoteCredentialConfigured(*remote)
	return nil
}

func (s *Server) remoteLibraryAPIKey(remote LibraryRemote) (string, error) {
	if strings.EqualFold(remote.AuthMethod, "none") {
		return "", nil
	}
	if strings.TrimSpace(remote.APIKey) != "" {
		return strings.TrimSpace(remote.APIKey), nil
	}
	if s.libraryCreds == nil {
		return "", fmt.Errorf("remote library credential store is not configured")
	}
	apiKey, err := s.libraryCreds.GetRemoteAPIKey(remote)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("remote library credential is not configured")
	}
	return strings.TrimSpace(apiKey), nil
}

func (s *Server) remoteCredentialConfigured(remote LibraryRemote) bool {
	if strings.EqualFold(remote.AuthMethod, "none") {
		return true
	}
	if strings.TrimSpace(remote.APIKey) != "" {
		return true
	}
	if strings.TrimSpace(remote.CredentialRef) == "" || s.libraryCreds == nil {
		return false
	}
	secret, err := s.libraryCreds.GetRemoteAPIKey(remote)
	return err == nil && strings.TrimSpace(secret) != ""
}

func (s *Server) sanitizeLibraryRemote(remote *LibraryRemote) LibraryRemote {
	if remote == nil {
		return LibraryRemote{}
	}
	copied := *remote
	copied.APIKey = ""
	copied.CredentialConfigured = s.remoteCredentialConfigured(copied)
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
	s.libraryMu.Unlock()
	s.persistLibraryLinks()
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

func readLibraryArchiveImportRequest(r *http.Request) (LibraryImportRequest, *libraryArchiveImport, func(), error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("invalid multipart archive import: %w", err)
	}
	var req LibraryImportRequest
	req.ProjectID = strings.TrimSpace(r.URL.Query().Get("project_id"))
	var bundleSeen bool
	var archivePath string
	var archiveSize int64
	cleanup := func() {
		if archivePath != "" {
			_ = os.Remove(archivePath)
		}
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return LibraryImportRequest{}, nil, nil, err
		}
		switch part.FormName() {
		case "bundle":
			if err := json.NewDecoder(io.LimitReader(part, 16*1024*1024)).Decode(&req.Bundle); err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, fmt.Errorf("invalid bundle part: %w", err)
			}
			bundleSeen = true
		case "project_id":
			data, err := io.ReadAll(io.LimitReader(part, 4096))
			if err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, err
			}
			req.ProjectID = strings.TrimSpace(string(data))
		case "archive":
			tmp, err := os.CreateTemp("", "apexload-library-import-*.zip")
			if err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, err
			}
			archivePath = tmp.Name()
			archiveSize, err = copyWithMaxBytes(tmp, part, libraryArchiveMaxBytes())
			closeErr := tmp.Close()
			if err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, err
			}
			if closeErr != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, closeErr
			}
		}
	}
	if !bundleSeen {
		cleanup()
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("bundle part is required")
	}
	if libraryBundleHasStreamedArchive(&req.Bundle) && archivePath == "" {
		cleanup()
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("archive part is required for streamed result imports")
	}
	if archivePath == "" {
		return req, nil, cleanup, nil
	}
	return req, &libraryArchiveImport{ZipPath: archivePath, SizeBytes: archiveSize}, cleanup, nil
}

func archiveImportSize(archiveImport *libraryArchiveImport) int64 {
	if archiveImport == nil {
		return 0
	}
	return archiveImport.SizeBytes
}

func libraryBundleHasStreamedArchive(bundle *LibraryBundle) bool {
	if bundle == nil {
		return false
	}
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) == "result" && item.Result != nil && strings.EqualFold(item.Result.ArchiveTransfer, "stream") {
			return true
		}
	}
	return false
}

func libraryArchiveMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_LIBRARY_ARCHIVE_MAX_BYTES"))
	if raw == "" {
		return defaultLibraryArchiveMaxBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultLibraryArchiveMaxBytes
	}
	return value
}

func copyWithMaxBytes(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		maxBytes = defaultLibraryArchiveMaxBytes
	}
	limited := &io.LimitedReader{R: src, N: maxBytes + 1}
	written, err := io.Copy(dst, limited)
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, fmt.Errorf("archive exceeds maximum size of %d bytes", maxBytes)
	}
	return written, nil
}

func unzipArchiveFile(zipPath string, destination string, maxBytes int64) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open archive zip: %w", err)
	}
	defer reader.Close()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	var total int64
	for _, file := range reader.File {
		cleanName := filepath.Clean(file.Name)
		if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanName) {
			return fmt.Errorf("archive contains unsafe path %q", file.Name)
		}
		target := filepath.Join(destination, cleanName)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, file.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			_ = src.Close()
			return err
		}
		remaining := maxBytes - total
		if remaining <= 0 {
			_ = dst.Close()
			_ = src.Close()
			return fmt.Errorf("archive exceeds maximum size of %d bytes", maxBytes)
		}
		written, copyErr := copyWithMaxBytes(dst, src, remaining)
		closeDstErr := dst.Close()
		closeSrcErr := src.Close()
		total += written
		if copyErr != nil {
			return copyErr
		}
		if closeDstErr != nil {
			return closeDstErr
		}
		if closeSrcErr != nil {
			return closeSrcErr
		}
		if total > maxBytes {
			return fmt.Errorf("archive exceeds maximum size of %d bytes", maxBytes)
		}
	}
	return nil
}

func importLegacyArchiveZipB64(encoded string, destination string, maxBytes int64) error {
	tmp, err := os.CreateTemp("", "apexload-library-legacy-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, err = copyWithMaxBytes(tmp, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), maxBytes)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return unzipArchiveFile(tmpPath, destination, maxBytes)
}

func libraryImportedArchiveDir(importID string) string {
	return filepath.Join(resolveArchiveRoot(), "library-imports", strings.TrimSpace(importID))
}

func archiveDirectorySize(dir string) int64 {
	var total int64
	if strings.TrimSpace(dir) == "" {
		return 0
	}
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		} else if info, statErr := os.Stat(path); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func hashArchiveManifest(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	if strings.TrimSpace(record.ManifestPath) != "" {
		return hashArchiveManifestPath(record.ManifestPath)
	}
	if strings.TrimSpace(record.ArchivePath) != "" {
		return hashArchiveManifestPath(filepath.Join(record.ArchivePath, "manifest.json"))
	}
	return ""
}

func hashArchiveManifestPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum[:])
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

func resolveLibraryResultArchiveProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", 0, ""
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		return "", http.StatusBadRequest, "result id is required"
	}
	test, err := store.GetTest(id)
	if err != nil {
		return "", http.StatusNotFound, "result not found"
	}
	return normalizeProjectID(test.ProjectID), 0, ""
}

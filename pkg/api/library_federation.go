package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const libraryBundleSchemaVersion = "apexload.library_bundle.v1"
const libraryRemotesSettingKey = "library.remotes.v1"
const librarySyncLinksSettingKey = "library.sync_links.v1"
const defaultLibraryArchiveMaxBytes int64 = 512 * 1024 * 1024
const defaultLibraryRemoteJSONTimeout = 30 * time.Second
const defaultLibraryRemoteArchiveTimeout = 5 * time.Minute

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
	ExecutionRunDate time.Time           `json:"execution_run_date,omitempty"`
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

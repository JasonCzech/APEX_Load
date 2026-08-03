package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const libraryBundleSchemaVersion = "apexload.library_bundle.v1"
const libraryResultHashVersionCurrent = "apexload.library_result_hash.v2"
const libraryResultHashVersionLegacyV1 = "apexload.library_result_hash.v1"
const libraryRemotesSettingKey = "library.remotes.v1"
const librarySyncLinksSettingKey = "library.sync_links.v1"

type librarySettingsCASStore interface {
	CompareAndSwapSetting(key, expected, value string) (bool, error)
}

const defaultLibraryArchiveMaxBytes int64 = 512 * 1024 * 1024
const defaultLibraryRemoteJSONTimeout = 30 * time.Second
const defaultLibraryRemoteArchiveTimeout = 5 * time.Minute

// Remote bundle responses are intentionally allowed to be larger than ordinary
// controller CRUD requests. Keep this outbound trust boundary independent of
// the inbound global request-body backstop.
const libraryRemoteJSONMaxBytes int64 = 128 << 20
const libraryRemoteCapabilitiesMaxBytes int64 = 64 << 10

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
	Total         int                  `json:"total"`
	Limit         int                  `json:"limit"`
	Offset        int                  `json:"offset"`
	HasMore       bool                 `json:"has_more"`
}

type LibraryCatalogItem struct {
	Kind             string              `json:"kind"`
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	ProjectID        string              `json:"project_id,omitempty"`
	ApplicationID    string              `json:"application_id,omitempty"`
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
	ProjectID   string `json:"project_id,omitempty"`
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
	Kind               string                  `json:"kind"`
	SourceInstanceID   string                  `json:"source_instance_id"`
	SourceItemID       string                  `json:"source_item_id"`
	ProjectID          string                  `json:"project_id,omitempty"`
	Name               string                  `json:"name"`
	ContentHash        string                  `json:"content_hash"`
	ContentHashVersion string                  `json:"content_hash_version,omitempty"`
	CreatedAt          time.Time               `json:"created_at,omitempty"`
	UpdatedAt          time.Time               `json:"updated_at,omitempty"`
	Script             *dsl.Script             `json:"script,omitempty"`
	Scenario           *LibraryScenarioPayload `json:"scenario,omitempty"`
	Result             *LibraryResultPayload   `json:"result,omitempty"`
	Dependencies       []LibraryDependency     `json:"dependencies,omitempty"`
	importMutationID   string
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
	Bundle        LibraryBundle `json:"bundle"`
	ProjectID     string        `json:"project_id,omitempty"`
	ApplicationID string        `json:"application_id,omitempty"`
	ReleaseID     string        `json:"release_id,omitempty"`
	Mode          string        `json:"mode,omitempty"`
}

type LibraryImportResponse struct {
	SchemaVersion string                    `json:"schema_version"`
	ImportedAt    time.Time                 `json:"imported_at"`
	Items         []LibraryImportResultItem `json:"items"`
	Conflicts     []LibraryConflict         `json:"conflicts,omitempty"`
	Warnings      []string                  `json:"warnings,omitempty"`
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
	ProjectMappings      map[string]string            `json:"project_mappings,omitempty"`
	ApplicationMappings  map[string]string            `json:"application_mappings,omitempty"`
	ReleaseMappings      map[string]string            `json:"release_mappings,omitempty"`
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
	Name                string            `json:"name"`
	BaseURL             string            `json:"base_url"`
	DefaultProjectID    string            `json:"default_project_id,omitempty"`
	ProjectMappings     map[string]string `json:"project_mappings,omitempty"`
	ApplicationMappings map[string]string `json:"application_mappings,omitempty"`
	ReleaseMappings     map[string]string `json:"release_mappings,omitempty"`
	AuthMethod          string            `json:"auth_method,omitempty"`
	CredentialRef       string            `json:"credential_ref,omitempty"`
	APIKey              string            `json:"api_key,omitempty"`
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
	ProjectID           string `json:"project_id,omitempty"`        // local authorized project (pull: local destination)
	ApplicationID       string `json:"application_id,omitempty"`    // local destination application for result pulls
	ReleaseID           string `json:"release_id,omitempty"`        // local destination release for result pulls
	RemoteProjectID     string `json:"remote_project_id,omitempty"` // optional source/destination project on the remote controller
	IncludeDependencies bool   `json:"include_dependencies,omitempty"`
}

type LibraryRemoteTransferResponse struct {
	Remote LibraryRemote          `json:"remote"`
	Bundle *LibraryBundle         `json:"bundle,omitempty"`
	Import *LibraryImportResponse `json:"import,omitempty"`
}

type librarySyncLink struct {
	RemoteID          string
	ProjectID         string
	Kind              string
	SourceInstanceID  string
	SourceItemID      string
	LocalItemID       string
	RemoteItemID      string
	LocalContentHash  string
	RemoteContentHash string
	LastSyncedAt      time.Time
	MutationID        string `json:",omitempty"`
}

func (s *Server) loadLibraryState() {
	if s == nil || s.store == nil {
		return
	}
	if raw, err := s.store.GetSetting(libraryRemotesSettingKey); err != nil {
		slog.Warn("failed to load library remotes", "err", err)
	} else {
		loaded := make(map[string]*LibraryRemote)
		valid := true
		if strings.TrimSpace(raw) != "" {
			var remotes []LibraryRemote
			if err := json.Unmarshal([]byte(raw), &remotes); err != nil {
				slog.Warn("failed to decode library remotes", "err", err)
				valid = false
			} else {
				for i := range remotes {
					remote := remotes[i]
					remote.APIKey = ""
					remote.CredentialConfigured = false
					projectMappings, mappingErr := normalizeLibraryRemoteProjectMappings(remote.ProjectMappings)
					if mappingErr != nil {
						slog.Warn("discarding invalid library remote project mappings", "remote_id", remote.ID, "err", mappingErr)
						projectMappings = nil
					}
					remote.ProjectMappings = projectMappings
					applicationMappings, mappingErr := normalizeLibraryRemoteApplicationMappings(remote.ApplicationMappings)
					if mappingErr != nil {
						slog.Warn("discarding invalid library remote application mappings", "remote_id", remote.ID, "err", mappingErr)
						applicationMappings = nil
					}
					remote.ApplicationMappings = applicationMappings
					releaseMappings, mappingErr := normalizeLibraryRemoteReleaseMappings(remote.ReleaseMappings)
					if mappingErr != nil {
						slog.Warn("discarding invalid library remote release mappings", "remote_id", remote.ID, "err", mappingErr)
						releaseMappings = nil
					}
					remote.ReleaseMappings = releaseMappings
					if remote.ID != "" {
						copied := remote
						loaded[remote.ID] = &copied
					}
				}
			}
		}
		if valid {
			s.libraryMu.Lock()
			s.libraryRemotes = loaded
			s.libraryRemotesRaw = raw
			s.libraryMu.Unlock()
		}
	}
	if raw, err := s.store.GetSetting(librarySyncLinksSettingKey); err != nil {
		slog.Warn("failed to load library sync links", "err", err)
	} else {
		loaded := make(map[string]*librarySyncLink)
		valid := true
		if strings.TrimSpace(raw) != "" {
			var links []librarySyncLink
			if err := json.Unmarshal([]byte(raw), &links); err != nil {
				slog.Warn("failed to decode library sync links", "err", err)
				valid = false
			} else {
				for i := range links {
					link := links[i]
					if link.Kind == "" || link.SourceItemID == "" {
						continue
					}
					copied := link
					loaded[libraryLinkKey(link.RemoteID, link.ProjectID, link.Kind, link.SourceInstanceID, link.SourceItemID)] = &copied
				}
			}
		}
		if valid {
			s.libraryMu.Lock()
			s.libraryLinks = loaded
			s.libraryLinksRaw = raw
			s.libraryMu.Unlock()
		}
	}
}

func (s *Server) loadLibraryLinksState() {
	if s == nil || s.store == nil {
		return
	}
	raw, err := s.store.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		slog.Warn("failed to load library sync links", "err", err)
		return
	}
	loaded := make(map[string]*librarySyncLink)
	if strings.TrimSpace(raw) != "" {
		var links []librarySyncLink
		if err := json.Unmarshal([]byte(raw), &links); err != nil {
			slog.Warn("failed to decode library sync links", "err", err)
			return
		}
		for i := range links {
			link := links[i]
			if link.Kind == "" || link.SourceItemID == "" {
				continue
			}
			copied := link
			loaded[libraryLinkKey(link.RemoteID, link.ProjectID, link.Kind, link.SourceInstanceID, link.SourceItemID)] = &copied
		}
	}
	s.libraryMu.Lock()
	s.libraryLinks = loaded
	s.libraryLinksRaw = raw
	s.libraryMu.Unlock()
}

func (s *Server) persistLibraryRemotes() error {
	if s == nil || s.store == nil {
		return nil
	}
	s.libraryMu.Lock()
	expected := s.libraryRemotesRaw
	remotes := make([]LibraryRemote, 0, len(s.libraryRemotes))
	for _, remote := range s.libraryRemotes {
		if remote == nil {
			continue
		}
		copied := *remote
		copied.APIKey = ""
		copied.CredentialConfigured = false
		copied.ProjectMappings = cloneLibraryRemoteProjectMappings(remote.ProjectMappings)
		copied.ApplicationMappings = cloneLibraryRemoteApplicationMappings(remote.ApplicationMappings)
		copied.ReleaseMappings = cloneLibraryRemoteReleaseMappings(remote.ReleaseMappings)
		remotes = append(remotes, copied)
	}
	sort.SliceStable(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i].Name) < strings.ToLower(remotes[j].Name)
	})
	s.libraryMu.Unlock()
	payload, err := json.Marshal(remotes)
	if err != nil {
		return fmt.Errorf("encode library remotes: %w", err)
	}
	encoded := string(payload)
	if cas, ok := s.store.(librarySettingsCASStore); ok {
		updated, err := cas.CompareAndSwapSetting(libraryRemotesSettingKey, expected, encoded)
		if err != nil {
			return fmt.Errorf("persist library remotes: %w", err)
		}
		if !updated {
			return fmt.Errorf("persist library remotes: shared settings changed concurrently; retry the request")
		}
	} else if err := s.store.SaveSetting(libraryRemotesSettingKey, encoded); err != nil {
		return fmt.Errorf("persist library remotes: %w", err)
	}
	s.libraryMu.Lock()
	s.libraryRemotesRaw = encoded
	s.libraryMu.Unlock()
	return nil
}

func (s *Server) persistLibraryLinks() error {
	if s == nil || s.store == nil {
		return nil
	}
	s.libraryMu.Lock()
	expected := s.libraryLinksRaw
	links := make([]librarySyncLink, 0, len(s.libraryLinks))
	for _, link := range s.libraryLinks {
		if link == nil {
			continue
		}
		links = append(links, *link)
	}
	sort.SliceStable(links, func(i, j int) bool {
		return libraryLinkKey(links[i].RemoteID, links[i].ProjectID, links[i].Kind, links[i].SourceInstanceID, links[i].SourceItemID) <
			libraryLinkKey(links[j].RemoteID, links[j].ProjectID, links[j].Kind, links[j].SourceInstanceID, links[j].SourceItemID)
	})
	s.libraryMu.Unlock()
	payload, err := json.Marshal(links)
	if err != nil {
		return fmt.Errorf("encode library sync links: %w", err)
	}
	encoded := string(payload)
	if cas, ok := s.store.(librarySettingsCASStore); ok {
		updated, err := cas.CompareAndSwapSetting(librarySyncLinksSettingKey, expected, encoded)
		if err != nil {
			return fmt.Errorf("persist library sync links: %w", err)
		}
		if !updated {
			return fmt.Errorf("persist library sync links: shared settings changed concurrently; retry the request")
		}
	} else if err := s.store.SaveSetting(librarySyncLinksSettingKey, encoded); err != nil {
		return fmt.Errorf("persist library sync links: %w", err)
	}
	s.libraryMu.Lock()
	s.libraryLinksRaw = encoded
	s.libraryMu.Unlock()
	return nil
}

func resolveLibraryTransferProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req LibraryTransferRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	return resolveLibraryTransferRequestProjectID(req, r.URL.Path, store)
}

// resolveLibraryTransferRequestProjectID binds a decoded transfer to the
// object's durable project identity. Handlers call this again after decoding so
// the exact identity used for authorization is also supplied to cache-backed
// bundle construction; otherwise an HA replica with a stale object cache could
// authorize the current durable project and export an older object with the
// same ID from another project.
func resolveLibraryTransferRequestProjectID(req LibraryTransferRequest, path string, store storage.Store) (string, int, string) {
	if strings.HasSuffix(path, "/pull") {
		if strings.TrimSpace(req.ProjectID) == "" {
			return "", http.StatusBadRequest, "project_id is required as the library pull destination"
		}
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	lookupID := strings.TrimSpace(req.ID)
	if strings.HasSuffix(path, "/diff") {
		lookupID = strings.TrimSpace(firstNonEmpty(req.LocalID, req.ID))
	}
	if store == nil || lookupID == "" {
		return "", 0, ""
	}
	var actualProjectID string
	switch normalizeLibraryKind(req.Kind) {
	case "script":
		script, err := store.GetScript(lookupID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		actualProjectID = normalizeProjectID(script.ProjectID)
	case "scenario":
		_, scripts, err := store.GetScenario(lookupID)
		if err != nil {
			return "", http.StatusNotFound, "scenario not found"
		}
		resolved, status, message := resolveProjectIDFromScenarioScripts(scripts, store)
		if status != 0 {
			return "", status, message
		}
		actualProjectID = resolved
	case "result":
		test, err := store.GetTest(lookupID)
		if err == nil && test != nil {
			actualProjectID = normalizeProjectID(test.ProjectID)
			break
		}
		imports, ok := store.(interface {
			GetExternalResultImport(string) (*storage.ExternalResultImport, error)
		})
		if !ok {
			return "", http.StatusNotFound, "result not found"
		}
		imported, importErr := imports.GetExternalResultImport(lookupID)
		if importErr != nil || imported == nil {
			return "", http.StatusNotFound, "result not found"
		}
		actualProjectID = normalizeProjectID(imported.ProjectID)
	default:
		return "", http.StatusBadRequest, "kind must be one of script, scenario, or result"
	}
	if strings.TrimSpace(req.ProjectID) != "" && normalizeProjectID(req.ProjectID) != actualProjectID {
		return "", http.StatusBadRequest, "project_id does not match the requested object's project"
	}
	return actualProjectID, 0, ""
}

func resolveLibraryImportProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req LibraryImportRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID, err := validateLibraryImportDestination(req)
	if err != nil {
		return "", http.StatusBadRequest, err.Error()
	}
	return projectID, 0, ""
}

// validateLibraryImportDestination establishes one explicit local write scope.
// Bundle project fields describe provenance, never an implicit destination.
// They must also agree with each other so a bundle cannot conceal assets from
// several tenants behind the first project encountered by the auth resolver.
func validateLibraryImportDestination(req LibraryImportRequest) (string, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", fmt.Errorf("project_id is required as the library import destination")
	}
	destination := normalizeProjectID(strings.TrimSpace(req.ProjectID))
	sourceProjects := map[string]struct{}{}
	addSourceProject := func(value string) {
		if strings.TrimSpace(value) != "" {
			sourceProjects[normalizeProjectID(strings.TrimSpace(value))] = struct{}{}
		}
	}
	addSourceProject(req.Bundle.Source.ProjectID)
	for _, item := range req.Bundle.Items {
		addSourceProject(item.ProjectID)
		for _, dependency := range item.Dependencies {
			addSourceProject(dependency.ProjectID)
		}
		if item.Script != nil {
			addSourceProject(item.Script.ProjectID)
		}
		if item.Result != nil && item.Result.Test != nil {
			addSourceProject(item.Result.Test.ProjectID)
		}
	}
	if len(sourceProjects) > 1 {
		return "", fmt.Errorf("library bundle items span multiple source projects")
	}
	return destination, nil
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
	if err == nil && test != nil {
		return normalizeProjectID(test.ProjectID), 0, ""
	}
	if imports, ok := store.(interface {
		GetExternalResultImport(string) (*storage.ExternalResultImport, error)
	}); ok {
		imported, importErr := imports.GetExternalResultImport(id)
		if importErr == nil && imported != nil {
			return normalizeProjectID(imported.ProjectID), 0, ""
		}
	}
	return "", http.StatusNotFound, "result not found"
}

package api

// library_handlers.go — split from library_federation.go (same package, behavior-preserving).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
	limit, offset, ok := parseLibraryCatalogPage(w, r)
	if !ok {
		return
	}
	items, total := s.libraryCatalogPage(projectID, kind, offset, limit)
	writeJSON(w, http.StatusOK, LibraryCatalogResponse{
		SchemaVersion: libraryBundleSchemaVersion,
		InstanceID:    s.controllerID,
		GeneratedAt:   time.Now().UTC(),
		Items:         items,
		Count:         len(items),
		Total:         total,
		Limit:         limit,
		Offset:        offset,
		HasMore:       offset+len(items) < total,
	})
}

func parseLibraryCatalogPage(w http.ResponseWriter, r *http.Request) (limit int, offset int, ok bool) {
	limit = defaultLibraryCatalogPageSize
	query := r.URL.Query()
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxLibraryCatalogPageSize {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and "+strconv.Itoa(maxLibraryCatalogPageSize))
			return 0, 0, false
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > maxLibraryCatalogOffset {
			writeError(w, http.StatusBadRequest, "offset must be an integer between 0 and "+strconv.Itoa(maxLibraryCatalogOffset))
			return 0, 0, false
		}
		offset = parsed
	}
	return limit, offset, true
}

func (s *Server) handleLibraryExport(w http.ResponseWriter, r *http.Request) {
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID, status, message := s.resolveLibraryTransferHandlerProject(req, r.URL.Path)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	bundle, err := s.buildLibraryBundle(req.Kind, req.ID, projectID, req.IncludeDependencies)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !requireServiceAccountLibraryBundleScopes(w, r, bundle) {
		return
	}
	s.recordAuditEvent(r, "library.export", "success", "library_bundle", bundle.BundleID, bundle.Source.ProjectID, map[string]any{"kind": normalizeLibraryKind(req.Kind), "source_item_id": req.ID})
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) handleLibraryImport(w http.ResponseWriter, r *http.Request) {
	var req LibraryImportRequest
	if !decodeJSONBody(w, r, &req) {
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
	for _, item := range req.Bundle.Items {
		if normalizeLibraryKind(item.Kind) != "result" {
			writeError(w, http.StatusBadRequest, "archive imports may contain result items only; import scripts and scenarios through /api/v1/library/import")
			return
		}
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
	clearArchiveWriteDeadline(w)
	prepCtx, cancelPrep := context.WithTimeout(r.Context(), configuredArchiveMaterializationTimeout())
	defer cancelPrep()
	id := strings.TrimSpace(r.PathValue("id"))
	var (
		testName  string
		projectID string
		record    *storage.RunArchiveRecord
	)
	if s.store != nil {
		// The route resolver authorizes against the durable test row. Read the
		// archive from that same source of truth instead of trusting this replica's
		// in-memory test/archive cache: another replica may have deleted and
		// recreated the ID in a different project while this process still holds
		// the prior project's archive pointer.
		durableTest, err := s.store.GetTest(id)
		if err != nil || durableTest == nil {
			writeError(w, http.StatusNotFound, "result not found")
			return
		}
		projectID = normalizeProjectID(durableTest.ProjectID)
		if !requireAuthorizedProjectMatch(w, r, projectID) {
			return
		}
		testName = durableTest.Name
		durableArchive, err := s.store.GetRunArchive(id)
		if err != nil || durableArchive == nil {
			writeError(w, http.StatusNotFound, "archive not found")
			return
		}
		record = cloneRunArchiveRecord(durableArchive)
	} else {
		test, ok := s.runner.GetTest(id)
		if !ok || test == nil {
			writeError(w, http.StatusNotFound, "result not found")
			return
		}
		projectID = normalizeProjectID(test.ProjectID)
		if !requireAuthorizedProjectMatch(w, r, projectID) {
			return
		}
		testName = test.Name
		var archiveOK bool
		record, archiveOK = s.runner.GetRunArchiveContext(r.Context(), id)
		if !archiveOK || record == nil {
			writeError(w, http.StatusNotFound, "archive not found")
			return
		}
	}
	lease, err := acquireArchiveMaterialized(prepCtx, record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	defer lease.Release()
	manifest, err := cachedRunArchiveManifest(record)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if manifest.TestID != id {
		writeError(w, http.StatusNotFound, "archive does not belong to result")
		return
	}
	if manifestProjectID := strings.TrimSpace(manifest.Provenance.ProjectID); manifestProjectID != "" && normalizeProjectID(manifestProjectID) != projectID {
		writeError(w, http.StatusNotFound, "archive does not belong to result project")
		return
	}
	staged, err := stageArchiveDownload(prepCtx, func(destination io.Writer) error {
		return zipSanitizedRunArchiveContext(prepCtx, record, manifest, destination)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive bundle: "+err.Error())
		return
	}
	defer removeStagedArchiveDownload(staged)
	filename := sanitizeFilename(testName) + "-" + id + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	clearArchiveWriteDeadline(w)
	http.ServeContent(w, r, filename, record.CompletedAt.UTC(), staged.File)
}

func (s *Server) handleLibraryListRemotes(w http.ResponseWriter, r *http.Request) {
	s.libraryStateMutationMu.Lock()
	s.loadLibraryState()
	s.libraryMu.Lock()
	remotes := make([]LibraryRemote, 0, len(s.libraryRemotes))
	for _, remote := range s.libraryRemotes {
		remotes = append(remotes, s.sanitizeLibraryRemote(remote))
	}
	sort.SliceStable(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i].Name) < strings.ToLower(remotes[j].Name)
	})
	s.libraryMu.Unlock()
	s.libraryStateMutationMu.Unlock()
	writeJSON(w, http.StatusOK, LibraryRemoteListResponse{Remotes: remotes, Count: len(remotes)})
}

func (s *Server) handleLibraryCreateRemote(w http.ResponseWriter, r *http.Request) {
	var req LibraryRemoteRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	remote, err := buildLibraryRemote(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	s.loadLibraryState()
	if err := s.storeRemoteCredential(remote, req.APIKey, req.CredentialRef); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.libraryMu.Lock()
	s.libraryRemotes[remote.ID] = remote
	s.libraryMu.Unlock()
	if err := s.persistLibraryRemotes(); err != nil {
		_ = s.deleteRemoteCredential(*remote)
		s.libraryMu.Lock()
		delete(s.libraryRemotes, remote.ID)
		s.libraryMu.Unlock()
		s.loadLibraryState()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "library.remote.create", "success", "library_remote", remote.ID, "", map[string]any{"name": remote.Name, "base_url": remote.BaseURL})
	writeJSON(w, http.StatusCreated, s.sanitizeLibraryRemote(remote))
}

func (s *Server) handleLibraryUpdateRemote(w http.ResponseWriter, r *http.Request) {
	s.remoteConfigMu.Lock()
	defer s.remoteConfigMu.Unlock()
	id := strings.TrimSpace(r.PathValue("id"))
	var req LibraryRemoteRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	s.loadLibraryState()
	s.libraryMu.Lock()
	remote := s.libraryRemotes[id]
	if remote == nil {
		s.libraryMu.Unlock()
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	previous := *remote
	previous.ProjectMappings = cloneLibraryRemoteProjectMappings(remote.ProjectMappings)
	updated := previous
	s.libraryMu.Unlock()
	if strings.TrimSpace(req.BaseURL) != "" || strings.TrimSpace(req.AuthMethod) != "" || strings.TrimSpace(req.CredentialRef) != "" || strings.TrimSpace(req.APIKey) != "" {
		pending, pendingErr := s.hasPendingRemoteRunCleanup(id)
		if pendingErr != nil {
			writeError(w, http.StatusServiceUnavailable, "remote cleanup state is unavailable: "+pendingErr.Error())
			return
		}
		if pending {
			writeError(w, http.StatusConflict, "remote transport and credentials cannot change while run cleanup is pending")
			return
		}
	}
	if strings.TrimSpace(req.Name) != "" {
		updated.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.BaseURL) != "" {
		baseURL, err := normalizeLibraryRemoteBaseURL(req.BaseURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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
	if req.ProjectMappings != nil {
		projectMappings, err := normalizeLibraryRemoteProjectMappings(req.ProjectMappings)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.ProjectMappings = projectMappings
	}
	updated.UpdatedAt = time.Now().UTC()
	if err := validateLibraryRemoteTransport(updated); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.storeRemoteCredential(&updated, req.APIKey, req.CredentialRef); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.libraryMu.Lock()
	s.libraryRemotes[id] = &updated
	s.libraryMu.Unlock()
	if err := s.persistLibraryRemotes(); err != nil {
		if updated.CredentialRef != previous.CredentialRef {
			_ = s.deleteRemoteCredential(updated)
		}
		s.libraryMu.Lock()
		s.libraryRemotes[id] = &previous
		s.libraryMu.Unlock()
		s.loadLibraryState()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated.CredentialRef != previous.CredentialRef {
		if err := s.deleteRemoteCredential(previous); err != nil {
			slog.Warn("failed to delete superseded remote library credential", "remote_id", id, "err", err)
		}
	}
	s.recordAuditEvent(r, "library.remote.update", "success", "library_remote", id, "", map[string]any{"name": updated.Name, "base_url": updated.BaseURL})
	writeJSON(w, http.StatusOK, s.sanitizeLibraryRemote(&updated))
}

func (s *Server) handleLibraryDeleteRemote(w http.ResponseWriter, r *http.Request) {
	s.remoteConfigMu.Lock()
	defer s.remoteConfigMu.Unlock()
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	s.loadLibraryState()
	id := strings.TrimSpace(r.PathValue("id"))
	s.libraryMu.Lock()
	remote := s.libraryRemotes[id]
	if remote == nil {
		s.libraryMu.Unlock()
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	previousRemote := *remote
	previousRemote.ProjectMappings = cloneLibraryRemoteProjectMappings(remote.ProjectMappings)
	pending, pendingErr := s.hasPendingRemoteRunCleanup(id)
	if pendingErr != nil {
		s.libraryMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "remote cleanup state is unavailable: "+pendingErr.Error())
		return
	}
	if pending {
		s.libraryMu.Unlock()
		writeError(w, http.StatusConflict, "remote workspace cannot be deleted while run cleanup is pending")
		return
	}
	removedLinks := make(map[string]*librarySyncLink)
	delete(s.libraryRemotes, id)
	for key, link := range s.libraryLinks {
		if link != nil && link.RemoteID == id {
			copied := *link
			removedLinks[key] = &copied
			delete(s.libraryLinks, key)
		}
	}
	s.libraryMu.Unlock()
	if err := s.persistLibraryRemotes(); err != nil {
		s.libraryMu.Lock()
		s.libraryRemotes[id] = &previousRemote
		for key, link := range removedLinks {
			s.libraryLinks[key] = link
		}
		s.libraryMu.Unlock()
		s.loadLibraryState()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.persistLibraryLinks(); err != nil {
		s.loadLibraryState()
		slog.Warn("remote deleted but stale library links could not be cleaned up", "remote_id", id, "error", err)
	}
	if err := s.deleteRemoteCredential(*remote); err != nil {
		slog.Warn("failed to delete remote library credential", "remote_id", id, "err", err)
	}
	s.recordAuditEvent(r, "library.remote.delete", "success", "library_remote", id, "", map[string]any{"name": remote.Name})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLibraryProbeRemote(w http.ResponseWriter, r *http.Request) {
	s.remoteConfigMu.RLock()
	defer s.remoteConfigMu.RUnlock()
	s.libraryStateMutationMu.Lock()
	s.loadLibraryState()
	remote, ok := s.libraryRemote(r.PathValue("id"))
	s.libraryStateMutationMu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	caps, err := s.remoteLibraryCapabilities(r.Context(), remote)
	now := time.Now().UTC()
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	s.loadLibraryState()
	s.libraryMu.Lock()
	stored := s.libraryRemotes[remote.ID]
	if stored == nil {
		s.libraryMu.Unlock()
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	copied := *stored
	previous := &copied
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
	s.libraryMu.Unlock()
	if persistErr := s.persistLibraryRemotes(); persistErr != nil {
		s.libraryMu.Lock()
		s.libraryRemotes[remote.ID] = previous
		s.libraryMu.Unlock()
		s.loadLibraryState()
		writeError(w, http.StatusInternalServerError, persistErr.Error())
		return
	}
	if err != nil {
		s.recordAuditEvent(r, "library.remote.probe", "failure", "library_remote", remote.ID, "", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "unreachable", "error": err.Error()})
		return
	}
	s.recordAuditEvent(r, "library.remote.probe", "success", "library_remote", remote.ID, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "healthy", "capabilities": caps})
}

func (s *Server) handleLibraryRemoteCatalog(w http.ResponseWriter, r *http.Request) {
	s.remoteConfigMu.RLock()
	defer s.remoteConfigMu.RUnlock()
	remote, ok := s.freshLibraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	localProjectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	projectID, err := allowedRemoteProjectID(remote, localProjectID, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := normalizeLibraryKind(r.URL.Query().Get("kind"))
	limit, offset, pageOK := parseLibraryCatalogPage(w, r)
	if !pageOK {
		return
	}
	catalog, err := s.remoteLibraryCatalog(r.Context(), remote, projectID, kind, offset, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	for i := range catalog.Items {
		catalog.Items[i] = s.annotateRemoteCatalogItem(remote.ID, projectID, catalog.Items[i])
	}
	writeJSON(w, http.StatusOK, LibraryRemoteCatalogResponse{Remote: s.sanitizeLibraryRemote(&remote), Catalog: *catalog})
}

func (s *Server) handleLibraryRemotePull(w http.ResponseWriter, r *http.Request) {
	s.remoteConfigMu.RLock()
	defer s.remoteConfigMu.RUnlock()
	remote, ok := s.freshLibraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeError(w, http.StatusBadRequest, "project_id is required as the library pull destination")
		return
	}
	remoteProjectID, err := allowedRemoteProjectID(remote, req.ProjectID, req.RemoteProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	remoteReq := req
	remoteReq.ProjectID = remoteProjectID
	bundle, err := s.remoteLibraryExport(r.Context(), remote, remoteReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := validateLibraryBundleProject(bundle, remoteReq.ProjectID); err != nil {
		writeError(w, http.StatusBadGateway, "remote library returned a cross-project bundle")
		return
	}
	if !requireServiceAccountLibraryBundleScopes(w, r, bundle) {
		return
	}
	var archiveImport *libraryArchiveImport
	var cleanup func()
	if normalizeLibraryKind(req.Kind) == "result" && libraryBundleHasStreamedArchive(bundle) {
		archiveImport, cleanup, err = s.remoteLibraryDownloadArchive(r.Context(), remote, firstNonEmpty(req.RemoteID, req.ID))
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
	s.remoteConfigMu.RLock()
	defer s.remoteConfigMu.RUnlock()
	remote, ok := s.freshLibraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	localProjectID, resolveStatus, message := s.resolveLibraryTransferHandlerProject(req, r.URL.Path)
	if resolveStatus != 0 {
		writeError(w, resolveStatus, message)
		return
	}
	req.ProjectID = localProjectID
	remoteProjectID, err := allowedRemoteProjectID(remote, localProjectID, req.RemoteProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bundle, err := s.buildLibraryBundle(req.Kind, req.ID, localProjectID, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !requireServiceAccountLibraryBundleScopes(w, r, bundle) {
		return
	}
	var imported *LibraryImportResponse
	if normalizeLibraryKind(req.Kind) == "result" && libraryBundleHasStreamedArchive(bundle) {
		imported, err = s.remoteLibraryImportArchive(r.Context(), remote, LibraryImportRequest{Bundle: *bundle, ProjectID: remoteProjectID, Mode: "push"}, req.ID)
	} else {
		imported, err = s.remoteLibraryImport(r.Context(), remote, LibraryImportRequest{Bundle: *bundle, ProjectID: remoteProjectID, Mode: "push"})
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

func requireServiceAccountLibraryBundleScopes(w http.ResponseWriter, r *http.Request, bundle *LibraryBundle) bool {
	if err := requireServiceAccountLibraryBundleScopesContext(r.Context(), bundle); err == nil {
		return true
	}
	writeError(w, http.StatusForbidden, errServiceAccountLibraryBundleScope.Error())
	return false
}

var errServiceAccountLibraryBundleScope = errors.New("service account token scope does not allow every item in the expanded library bundle")

func requireServiceAccountLibraryBundleScopesContext(ctx context.Context, bundle *LibraryBundle) error {
	account, ok := AuthenticatedServiceAccountFromContext(ctx)
	if !ok || account == nil {
		return nil
	}
	if bundle != nil && serviceAccountAllowsScopes(account, serviceAccountScopesForLibraryItems(bundle.Items)) {
		return nil
	}
	return errServiceAccountLibraryBundleScope
}

func (s *Server) handleLibraryRemoteDiff(w http.ResponseWriter, r *http.Request) {
	s.remoteConfigMu.RLock()
	defer s.remoteConfigMu.RUnlock()
	remote, ok := s.freshLibraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	localID := firstNonEmpty(req.LocalID, req.ID)
	remoteItemID := firstNonEmpty(req.RemoteID, req.ID)
	localProjectID, resolveStatus, message := s.resolveLibraryTransferHandlerProject(req, r.URL.Path)
	if resolveStatus != 0 {
		writeError(w, resolveStatus, message)
		return
	}
	req.ProjectID = localProjectID
	localBundle, err := s.buildLibraryBundle(req.Kind, localID, localProjectID, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	localHash := libraryBundleItemHash(localBundle, req.Kind, localID)
	if localHash == "" {
		writeError(w, http.StatusBadRequest, "local library item hash is unavailable")
		return
	}
	remoteReq := req
	remoteReq.ID = remoteItemID
	remoteProjectID, err := allowedRemoteProjectID(remote, localProjectID, req.RemoteProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	remoteReq.ProjectID = remoteProjectID
	bundle, err := s.remoteLibraryExport(r.Context(), remote, remoteReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := validateLibraryBundleProject(bundle, remoteReq.ProjectID); err != nil {
		writeError(w, http.StatusBadGateway, "remote library returned a cross-project bundle")
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

func (s *Server) resolveLibraryTransferHandlerProject(req LibraryTransferRequest, path string) (string, int, string) {
	if normalizeLibraryKind(req.Kind) == "" {
		return "", http.StatusBadRequest, "kind must be one of script, scenario, or result"
	}
	if s != nil && s.store != nil {
		return resolveLibraryTransferRequestProjectID(req, path, s.store)
	}
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
	projectID := s.localAssetProjectID(req.Kind, lookupID)
	if projectID == "" {
		return "", http.StatusNotFound, "local library item not found"
	}
	if strings.TrimSpace(req.ProjectID) != "" && normalizeProjectID(req.ProjectID) != projectID {
		return "", http.StatusBadRequest, "project_id does not match the requested object's project"
	}
	return projectID, 0, ""
}

func libraryBundleItemHash(bundle *LibraryBundle, kind, id string) string {
	if bundle == nil {
		return ""
	}
	kind = normalizeLibraryKind(kind)
	id = strings.TrimSpace(id)
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) == kind && strings.TrimSpace(item.SourceItemID) == id {
			return strings.TrimSpace(item.ContentHash)
		}
	}
	return ""
}

// allowedRemoteProjectID resolves the only remote project that a non-admin
// transfer request may target. The mapping is configured by a system
// administrator on the same remote object as its credential. There is no
// implicit/default mapping: a broad controller credential must never turn
// possession of an unrelated local project role into remote-project access.
func allowedRemoteProjectID(remote LibraryRemote, localProjectID, requestedRemoteProjectID string) (string, error) {
	if strings.TrimSpace(localProjectID) == "" {
		return "", fmt.Errorf("local project_id is required for remote workspace access")
	}
	localProjectID = normalizeProjectID(localProjectID)
	remoteProjectID, mapped := remote.ProjectMappings[localProjectID]
	remoteProjectID = strings.TrimSpace(remoteProjectID)
	if !mapped || remoteProjectID == "" {
		return "", fmt.Errorf("local project %q has no administrator-configured mapping for this remote workspace", localProjectID)
	}
	remoteProjectID = normalizeProjectID(remoteProjectID)
	if requested := strings.TrimSpace(requestedRemoteProjectID); requested != "" && normalizeProjectID(requested) != remoteProjectID {
		return "", fmt.Errorf("remote_project_id is not allowed by the remote workspace project mapping")
	}
	return remoteProjectID, nil
}

func normalizeLibraryRemoteProjectMappings(mappings map[string]string) (map[string]string, error) {
	if mappings == nil {
		return nil, nil
	}
	if len(mappings) > 1000 {
		return nil, fmt.Errorf("project_mappings exceeds the 1000-entry limit")
	}
	normalized := make(map[string]string, len(mappings))
	for localProjectID, remoteProjectID := range mappings {
		if strings.TrimSpace(localProjectID) == "" || strings.TrimSpace(remoteProjectID) == "" {
			return nil, fmt.Errorf("project_mappings keys and values must be non-empty")
		}
		localProjectID = normalizeProjectID(localProjectID)
		remoteProjectID = normalizeProjectID(remoteProjectID)
		if existing, ok := normalized[localProjectID]; ok && existing != remoteProjectID {
			return nil, fmt.Errorf("project_mappings contains conflicting normalized local project %q", localProjectID)
		}
		normalized[localProjectID] = remoteProjectID
	}
	return normalized, nil
}

func cloneLibraryRemoteProjectMappings(mappings map[string]string) map[string]string {
	if mappings == nil {
		return nil
	}
	cloned := make(map[string]string, len(mappings))
	for localProjectID, remoteProjectID := range mappings {
		cloned[localProjectID] = remoteProjectID
	}
	return cloned
}

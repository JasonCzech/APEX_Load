package api

// library_handlers.go — split from library_federation.go (same package, behavior-preserving).

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
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
	if !decodeJSONBody(w, r, &req) {
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
	test, ok := s.runner.GetTest(id)
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "result not found")
		return
	}
	record, ok := s.runner.GetRunArchiveContext(r.Context(), id)
	if !ok || record == nil {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	lease, err := acquireArchiveMaterialized(prepCtx, record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	defer lease.Release()
	staged, err := stageArchiveDownload(prepCtx, func(destination io.Writer) error {
		return zipDirectoryContext(prepCtx, record.ArchivePath, destination)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive bundle: "+err.Error())
		return
	}
	defer removeStagedArchiveDownload(staged)
	filename := sanitizeFilename(test.Name) + "-" + id + ".zip"
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
	updated := previous
	s.libraryMu.Unlock()
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
	updated.UpdatedAt = time.Now().UTC()
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
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	projectID := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("project_id"), remote.DefaultProjectID))
	kind := normalizeLibraryKind(r.URL.Query().Get("kind"))
	catalog, err := s.remoteLibraryCatalog(r.Context(), remote, projectID, kind)
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
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		req.ProjectID = remote.DefaultProjectID
	}
	bundle, err := s.remoteLibraryExport(r.Context(), remote, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
	remote, ok := s.libraryRemote(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
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
		imported, err = s.remoteLibraryImportArchive(r.Context(), remote, LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "push"}, req.ID)
	} else {
		imported, err = s.remoteLibraryImport(r.Context(), remote, LibraryImportRequest{Bundle: *bundle, ProjectID: req.ProjectID, Mode: "push"})
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
	if !decodeJSONBody(w, r, &req) {
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
	bundle, err := s.remoteLibraryExport(r.Context(), remote, req)
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

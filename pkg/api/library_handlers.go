package api

// library_handlers.go — split from library_federation.go (same package, behavior-preserving).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
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
		testName                    string
		projectID                   string
		expectedManifestTestID      string
		expectedRunIncarnation      string
		expectedExecutionGeneration uint64
		importedEvidence            bool
		record                      *storage.RunArchiveRecord
	)
	if s.store != nil {
		// The route resolver authorizes against the durable test row. Read the
		// archive from that same source of truth instead of trusting this replica's
		// in-memory test/archive cache: another replica may have deleted and
		// recreated the ID in a different project while this process still holds
		// the prior project's archive pointer.
		durableTest, testErr := s.store.GetTest(id)
		if testErr == nil && durableTest != nil {
			projectID = normalizeProjectID(durableTest.ProjectID)
			testName = durableTest.Name
			expectedManifestTestID = id
			expectedExecutionGeneration = durableTest.ExecutionGeneration
			var identityErr error
			expectedRunIncarnation, identityErr = storage.TestRunIncarnation(durableTest.InitialProvenance)
			if identityErr != nil {
				writeError(w, http.StatusNotFound, "archive run identity is invalid")
				return
			}
			if !requireAuthorizedProjectMatch(w, r, projectID) {
				return
			}
			durableArchive, err := s.store.GetRunArchive(id)
			if err != nil || durableArchive == nil {
				writeError(w, http.StatusNotFound, "archive not found")
				return
			}
			record = cloneRunArchiveRecord(durableArchive)
		} else {
			imported, importedRecord, sourceTestID, err := s.importedLibraryResultArchive(id)
			if err != nil || imported == nil || importedRecord == nil {
				writeError(w, http.StatusNotFound, "result archive not found")
				return
			}
			projectID = normalizeProjectID(imported.ProjectID)
			testName = imported.Name
			expectedManifestTestID = sourceTestID
			importedEvidence = true
			if !requireAuthorizedProjectMatch(w, r, projectID) {
				return
			}
			record = importedRecord
		}
	} else {
		test, ok := s.runner.GetTest(id)
		if ok && test != nil {
			projectID = normalizeProjectID(test.ProjectID)
			testName = test.Name
			expectedManifestTestID = id
			expectedRunIncarnation = managedTestRunIncarnation(test)
			expectedExecutionGeneration = test.durableExecutionGeneration
			if !requireAuthorizedProjectMatch(w, r, projectID) {
				return
			}
			var archiveOK bool
			record, archiveOK = s.runner.GetRunArchiveContext(r.Context(), id)
			if !archiveOK || record == nil {
				writeError(w, http.StatusNotFound, "archive not found")
				return
			}
		} else {
			imported, importedRecord, sourceTestID, err := s.importedLibraryResultArchive(id)
			if err != nil || imported == nil || importedRecord == nil {
				writeError(w, http.StatusNotFound, "result archive not found")
				return
			}
			projectID = normalizeProjectID(imported.ProjectID)
			testName = imported.Name
			expectedManifestTestID = sourceTestID
			importedEvidence = true
			if !requireAuthorizedProjectMatch(w, r, projectID) {
				return
			}
			record = importedRecord
		}
	}
	lease, err := acquireArchiveMaterialized(prepCtx, record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	defer lease.Release()
	manifest, err := s.archiveReadCaches().cachedRunArchiveManifest(record)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if manifest.TestID != expectedManifestTestID {
		writeError(w, http.StatusNotFound, "archive does not belong to result")
		return
	}
	if !importedEvidence {
		incarnation, err := archiveManifestRunIncarnation(manifest)
		if err != nil || incarnation != expectedRunIncarnation || manifest.Provenance.ExecutionGeneration != expectedExecutionGeneration {
			writeError(w, http.StatusNotFound, "archive does not belong to run incarnation")
			return
		}
	}
	if manifestProjectID := strings.TrimSpace(manifest.Provenance.ProjectID); !importedEvidence && manifestProjectID != "" && normalizeProjectID(manifestProjectID) != projectID {
		writeError(w, http.StatusNotFound, "archive does not belong to result project")
		return
	}
	staged, err := stageArchiveDownload(prepCtx, func(destination io.Writer) error {
		return s.archiveReadCaches().zipSanitizedRunArchiveContext(prepCtx, record, manifest, destination)
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

func (s *Server) importedLibraryResultArchive(id string) (*storage.ExternalResultImport, *storage.RunArchiveRecord, string, error) {
	imported, ok, err := s.getExternalResultImport(id)
	if err != nil || !ok || imported == nil {
		return nil, nil, "", err
	}
	if !strings.EqualFold(strings.TrimSpace(imported.Provider), "apexload") {
		return nil, nil, "", fmt.Errorf("result is not an APEX Load library import")
	}
	sourceTestID, _ := imported.RawResult["source_item_id"].(string)
	sourceTestID = strings.TrimSpace(sourceTestID)
	if sourceTestID == "" {
		return nil, nil, "", fmt.Errorf("imported archive source identity does not match its evidence row")
	}
	if imported.ArchiveRecord != nil {
		record := *imported.ArchiveRecord
		if record.TestID != sourceTestID {
			return nil, nil, "", fmt.Errorf("imported archive source identity does not match its evidence row")
		}
		return imported, &record, sourceTestID, nil
	}

	// Backward compatibility for filesystem imports created before archive
	// backend metadata was persisted alongside the evidence row.
	archiveDir := libraryImportedArchiveDir(imported.ID)
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	manifest, err := loadRunArchiveManifest(manifestPath)
	if err != nil {
		return nil, nil, "", err
	}
	if manifest.TestID != sourceTestID {
		return nil, nil, "", fmt.Errorf("imported archive source identity does not match its evidence row")
	}
	manifestHash, _ := imported.RawResult["imported_archive_manifest_hash"].(string)
	record := &storage.RunArchiveRecord{
		TestID:               sourceTestID,
		SchemaVersion:        manifest.SchemaVersion,
		ArchiveBackend:       archiveBackendFilesystem,
		ArchivePath:          archiveDir,
		ManifestPath:         manifestPath,
		ManifestSHA256:       strings.TrimPrefix(strings.TrimSpace(manifestHash), "sha256:"),
		TransactionCount:     manifest.TransactionCount,
		ResourceSampleCount:  manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount,
		CreatedAt:            manifest.CreatedAt,
		CompletedAt:          manifest.CompletedAt,
	}
	return imported, record, sourceTestID, nil
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
	s.remoteConfigMu.Lock()
	defer s.remoteConfigMu.Unlock()
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
	previous.ApplicationMappings = cloneLibraryRemoteApplicationMappings(remote.ApplicationMappings)
	previous.ReleaseMappings = cloneLibraryRemoteReleaseMappings(remote.ReleaseMappings)
	updated := previous
	s.libraryMu.Unlock()
	if strings.TrimSpace(req.BaseURL) != "" || strings.TrimSpace(req.AuthMethod) != "" || strings.TrimSpace(req.CredentialRef) != "" || strings.TrimSpace(req.APIKey) != "" {
		if s.remoteRunCreateInFlightLocked(id) {
			writeError(w, http.StatusConflict, "remote transport and credentials cannot change while a run create is preparing")
			return
		}
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
	if req.ApplicationMappings != nil {
		applicationMappings, err := normalizeLibraryRemoteApplicationMappings(req.ApplicationMappings)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.ApplicationMappings = applicationMappings
	}
	if req.ReleaseMappings != nil {
		releaseMappings, err := normalizeLibraryRemoteReleaseMappings(req.ReleaseMappings)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.ReleaseMappings = releaseMappings
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
	previousRemote.ApplicationMappings = cloneLibraryRemoteApplicationMappings(remote.ApplicationMappings)
	previousRemote.ReleaseMappings = cloneLibraryRemoteReleaseMappings(remote.ReleaseMappings)
	if s.remoteRunCreateInFlightLocked(id) {
		s.libraryMu.Unlock()
		writeError(w, http.StatusConflict, "remote workspace cannot be deleted while a run create is preparing")
		return
	}
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
	remote, ok, probeErr := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	var caps *LibraryCapabilitiesResponse
	if probeErr == nil {
		caps, probeErr = s.remoteLibraryCapabilities(r.Context(), remote)
	}
	now := time.Now().UTC()
	s.remoteConfigMu.Lock()
	defer s.remoteConfigMu.Unlock()
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
	if !sameLibraryRemoteTransport(remote, *stored) {
		s.libraryMu.Unlock()
		writeError(w, http.StatusConflict, "remote transport changed while the probe was in progress; retry")
		return
	}
	copied := *stored
	previous := &copied
	stored.LastHealthCheck = &now
	stored.UpdatedAt = now
	if probeErr != nil {
		stored.Status = "unreachable"
		stored.LastError = probeErr.Error()
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
	if probeErr != nil {
		s.recordAuditEvent(r, "library.remote.probe", "failure", "library_remote", remote.ID, "", map[string]any{"error": probeErr.Error()})
		writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "unreachable", "error": probeErr.Error()})
		return
	}
	s.recordAuditEvent(r, "library.remote.probe", "success", "library_remote", remote.ID, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "status": "healthy", "capabilities": caps})
}

func (s *Server) handleLibraryRemoteCatalog(w http.ResponseWriter, r *http.Request) {
	remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
		// Synchronization links are owned by the local destination project. The
		// remote project ID is only the outbound catalog scope and may differ.
		catalog.Items[i] = s.annotateRemoteCatalogItem(remote.ID, localProjectID, catalog.Items[i])
	}
	writeJSON(w, http.StatusOK, LibraryRemoteCatalogResponse{Remote: s.sanitizeLibraryRemote(&remote), Catalog: *catalog})
}

func (s *Server) handleLibraryRemotePull(w http.ResponseWriter, r *http.Request) {
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
	if err := s.validateLibraryResultPullDestination(remote, req, bundle); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	imported, err := s.importLibraryBundleWithArchive(LibraryImportRequest{
		Bundle:        *bundle,
		ProjectID:     req.ProjectID,
		ApplicationID: req.ApplicationID,
		ReleaseID:     req.ReleaseID,
		Mode:          "pull",
	}, remote.ID, archiveImport)
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

func (s *Server) validateLibraryResultPullDestination(remote LibraryRemote, req LibraryTransferRequest, bundle *LibraryBundle) error {
	if normalizeLibraryKind(req.Kind) != "result" || bundle == nil {
		return nil
	}
	localApplicationID := strings.TrimSpace(req.ApplicationID)
	localReleaseID := strings.TrimSpace(req.ReleaseID)
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) != "result" || item.Result == nil || item.Result.Test == nil {
			continue
		}
		remoteApplicationID := strings.TrimSpace(item.Result.Test.ApplicationID)
		remoteReleaseID := strings.TrimSpace(item.Result.Test.ReleaseID)
		// Legacy project-only results have no application/release identity. Keep
		// them importable, but never infer a destination from display names.
		if remoteApplicationID == "" && remoteReleaseID == "" && localApplicationID == "" && localReleaseID == "" {
			continue
		}
		if localApplicationID == "" || localReleaseID == "" {
			return fmt.Errorf("application_id and release_id are required for an application-scoped result pull")
		}
		if strings.TrimSpace(remote.ApplicationMappings[localApplicationID]) != remoteApplicationID {
			return fmt.Errorf("application_id is not mapped to the remote result application")
		}
		if strings.TrimSpace(remote.ReleaseMappings[localReleaseID]) != remoteReleaseID {
			return fmt.Errorf("release_id is not mapped to the remote result release")
		}
		if s.store != nil {
			projectID, _, releaseApplicationID, err := s.releaseContext(localReleaseID)
			if err != nil {
				return err
			}
			if normalizeProjectID(projectID) != normalizeProjectID(req.ProjectID) || strings.TrimSpace(releaseApplicationID) != localApplicationID {
				return fmt.Errorf("release_id does not belong to the destination application and project")
			}
		}
	}
	return nil
}

func (s *Server) handleLibraryRemotePush(w http.ResponseWriter, r *http.Request) {
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
	bundle, err := s.buildLibraryBundle(req.Kind, req.ID, localProjectID, req.IncludeDependencies)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	remoteApplicationID, remoteReleaseID, err := libraryResultPushDestination(remote, bundle)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !requireServiceAccountLibraryBundleScopes(w, r, bundle) {
		return
	}
	remoteImport := LibraryImportRequest{
		Bundle:        *bundle,
		ProjectID:     remoteProjectID,
		ApplicationID: remoteApplicationID,
		ReleaseID:     remoteReleaseID,
		Mode:          "push",
	}
	var imported *LibraryImportResponse
	if normalizeLibraryKind(req.Kind) == "result" && libraryBundleHasStreamedArchive(bundle) {
		imported, err = s.remoteLibraryImportArchive(r.Context(), remote, remoteImport, req.ID)
	} else {
		imported, err = s.remoteLibraryImport(r.Context(), remote, remoteImport)
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

func libraryResultPushDestination(remote LibraryRemote, bundle *LibraryBundle) (string, string, error) {
	if bundle == nil {
		return "", "", nil
	}
	remoteApplicationID := ""
	remoteReleaseID := ""
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) != "result" || item.Result == nil || item.Result.Test == nil {
			continue
		}
		localApplicationID := strings.TrimSpace(item.Result.Test.ApplicationID)
		localReleaseID := strings.TrimSpace(item.Result.Test.ReleaseID)
		if localApplicationID == "" && localReleaseID == "" {
			continue
		}
		if localApplicationID == "" || localReleaseID == "" {
			return "", "", fmt.Errorf("application_id and release_id are required together for an application-scoped result push")
		}
		mappedApplicationID := strings.TrimSpace(remote.ApplicationMappings[localApplicationID])
		if mappedApplicationID == "" {
			return "", "", fmt.Errorf("local result application %q has no mapping for this remote workspace", localApplicationID)
		}
		mappedReleaseID := strings.TrimSpace(remote.ReleaseMappings[localReleaseID])
		if mappedReleaseID == "" {
			return "", "", fmt.Errorf("local result release %q has no mapping for this remote workspace", localReleaseID)
		}
		if (remoteApplicationID != "" && remoteApplicationID != mappedApplicationID) || (remoteReleaseID != "" && remoteReleaseID != mappedReleaseID) {
			return "", "", fmt.Errorf("result push bundle maps to multiple destination applications or releases")
		}
		remoteApplicationID = mappedApplicationID
		remoteReleaseID = mappedReleaseID
	}
	return remoteApplicationID, remoteReleaseID, nil
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
	var req LibraryTransferRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
	// Validate and hash the local side before any outbound request. Live assets
	// go through bundle construction so a stale replica cache cannot escape the
	// durable project identity resolved above. Imported results are immutable
	// evidence rows rather than runnable tests, so their already-validated
	// portable hash is read from the destination-scoped durable record.
	localBundle, localHash, err := s.localLibraryDiffEvidence(req.Kind, localID, localProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
	} else if normalizeLibraryKind(req.Kind) == "script" {
		localItem := libraryBundleItem(localBundle, req.Kind, localID)
		remoteItem := libraryBundleItem(bundle, req.Kind, remoteItemID)
		if localItem != nil && remoteItem != nil &&
			libraryScriptHashesEquivalent(localItem.Script, remoteItem.Script) &&
			libraryScriptHashMatches(localItem.Script, localHash) &&
			libraryScriptHashMatches(remoteItem.Script, remoteHash) {
			status = "synced"
		}
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

func (s *Server) localLibraryDiffEvidence(kind, id, projectID string) (*LibraryBundle, string, error) {
	kind = normalizeLibraryKind(kind)
	id = strings.TrimSpace(id)
	projectID = normalizeProjectID(projectID)
	if kind == "result" {
		if _, live := s.runner.GetTest(id); !live {
			imported, found, err := s.getExternalResultImport(id)
			if err != nil {
				return nil, "", err
			}
			if !found || imported == nil {
				return nil, "", fmt.Errorf("result not found")
			}
			if normalizeProjectID(imported.ProjectID) != projectID {
				return nil, "", fmt.Errorf("result belongs to project %s, not authorized request project %s", normalizeProjectID(imported.ProjectID), projectID)
			}
			contentHash, _ := imported.RawResult["content_hash"].(string)
			contentHash = strings.TrimSpace(contentHash)
			if contentHash == "" {
				return nil, "", fmt.Errorf("imported result content hash is missing")
			}
			return nil, contentHash, nil
		}
	}
	bundle, err := s.buildLibraryBundle(kind, id, projectID, false)
	if err != nil {
		return nil, "", err
	}
	return bundle, libraryBundleItemHash(bundle, kind, id), nil
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
	item := libraryBundleItem(bundle, kind, id)
	if item == nil {
		return ""
	}
	return strings.TrimSpace(item.ContentHash)
}

func libraryBundleItem(bundle *LibraryBundle, kind, id string) *LibraryBundleItem {
	if bundle == nil {
		return nil
	}
	kind = normalizeLibraryKind(kind)
	id = strings.TrimSpace(id)
	for index := range bundle.Items {
		item := &bundle.Items[index]
		if normalizeLibraryKind(item.Kind) == kind && strings.TrimSpace(item.SourceItemID) == id {
			return item
		}
	}
	return nil
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

func normalizeLibraryRemoteApplicationMappings(mappings map[string]string) (map[string]string, error) {
	if mappings == nil {
		return nil, nil
	}
	if len(mappings) > 1000 {
		return nil, fmt.Errorf("application_mappings exceeds the 1000-entry limit")
	}
	normalized := make(map[string]string, len(mappings))
	for localApplicationID, remoteApplicationID := range mappings {
		localApplicationID = strings.TrimSpace(localApplicationID)
		remoteApplicationID = strings.TrimSpace(remoteApplicationID)
		if localApplicationID == "" || remoteApplicationID == "" {
			return nil, fmt.Errorf("application_mappings keys and values must be non-empty")
		}
		if existing, ok := normalized[localApplicationID]; ok && existing != remoteApplicationID {
			return nil, fmt.Errorf("application_mappings contains conflicting normalized local application %q", localApplicationID)
		}
		normalized[localApplicationID] = remoteApplicationID
	}
	return normalized, nil
}

func cloneLibraryRemoteApplicationMappings(mappings map[string]string) map[string]string {
	if mappings == nil {
		return nil
	}
	cloned := make(map[string]string, len(mappings))
	for localApplicationID, remoteApplicationID := range mappings {
		cloned[localApplicationID] = remoteApplicationID
	}
	return cloned
}

func normalizeLibraryRemoteReleaseMappings(mappings map[string]string) (map[string]string, error) {
	if mappings == nil {
		return nil, nil
	}
	if len(mappings) > 1000 {
		return nil, fmt.Errorf("release_mappings exceeds the 1000-entry limit")
	}
	normalized := make(map[string]string, len(mappings))
	for localReleaseID, remoteReleaseID := range mappings {
		localReleaseID = strings.TrimSpace(localReleaseID)
		remoteReleaseID = strings.TrimSpace(remoteReleaseID)
		if localReleaseID == "" || remoteReleaseID == "" {
			return nil, fmt.Errorf("release_mappings keys and values must be non-empty")
		}
		if existing, ok := normalized[localReleaseID]; ok && existing != remoteReleaseID {
			return nil, fmt.Errorf("release_mappings contains conflicting normalized local release %q", localReleaseID)
		}
		normalized[localReleaseID] = remoteReleaseID
	}
	return normalized, nil
}

func cloneLibraryRemoteReleaseMappings(mappings map[string]string) map[string]string {
	if mappings == nil {
		return nil
	}
	cloned := make(map[string]string, len(mappings))
	for localReleaseID, remoteReleaseID := range mappings {
		cloned[localReleaseID] = remoteReleaseID
	}
	return cloned
}

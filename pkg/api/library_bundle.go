package api

// library_bundle.go — split from library_federation.go (same package, behavior-preserving).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

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
			if test == nil || test.Status == "QUEUED" || test.Status == "RESERVED" || test.Status == "RUNNING" || test.Status == "PENDING" {
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
				ExecutionRunDate: managedTestExecutionRunDate(test),
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

func managedTestExecutionRunDate(test *ManagedTest) time.Time {
	if test == nil {
		return time.Time{}
	}
	if test.StartedAt != nil {
		return *test.StartedAt
	}
	return test.CreatedAt
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
	if test.Status == "QUEUED" || test.Status == "RESERVED" || test.Status == "RUNNING" || test.Status == "PENDING" {
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
		if lease, err := acquireArchiveMaterialized(context.Background(), record); err == nil {
			defer lease.Release()
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

func (s *Server) importLibraryBundleWithArchive(req LibraryImportRequest, remoteID string, archiveImport *libraryArchiveImport) (*LibraryImportResponse, error) {
	bundle := req.Bundle
	if bundle.SchemaVersion != libraryBundleSchemaVersion {
		return nil, fmt.Errorf("unsupported library bundle schema %q", bundle.SchemaVersion)
	}
	projectID, err := validateLibraryImportDestination(req)
	if err != nil {
		return nil, err
	}
	response := &LibraryImportResponse{SchemaVersion: libraryBundleSchemaVersion, ImportedAt: time.Now().UTC()}
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
		if local, ok := s.runner.GetScript(localID); ok && local != nil {
			if normalizeProjectID(local.ProjectID) != projectID {
				return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "existing sync link belongs to a different destination project"}
			}
			if hashLibraryScript(local.Script) != link.LocalContentHash && item.ContentHash != link.RemoteContentHash {
				return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "local and remote script changed since last sync"}
			}
		}
		action = "updated"
	}
	script := cloneScriptForLibrary(item.Script)
	script.ProjectID = projectID
	if localID == "" {
		script.ID = uuid.New().String()
	} else {
		script.ID = localID
	}
	stored, err := s.runner.StoreScriptDurable(script)
	if err != nil {
		return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "failed to persist imported script: " + err.Error()}
	}
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
		if local, ok := s.runner.GetScenario(localID); ok && local != nil {
			if normalizeProjectID(local.ProjectID) != projectID {
				return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "existing sync link belongs to a different destination project"}
			}
			if hashLibraryScenario(local) != link.LocalContentHash && item.ContentHash != link.RemoteContentHash {
				return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "local and remote scenario changed since last sync"}
			}
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
		ProjectID:  projectID,
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
			Name:            script.Name,
			ScriptID:        script.ScriptID,
			Role:            script.Role,
			VUsers:          script.VUsers,
			RampUp:          script.RampUp,
			RampDown:        script.RampDown,
			Stages:          script.Stages,
			Iterations:      script.Iterations,
			RuntimeSettings: script.RuntimeSettings,
			Assignment:      script.Assignment,
			InitStrategy:    script.Initialization,
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

package api

// library_links.go — split from library_federation.go (same package, behavior-preserving).

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

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
	req := createScenarioRequestFromStored(maskStoredScenario(scenario))
	return hashCanonical(req)
}

func hashLibraryResult(test *ManagedTest) string {
	return hashLibraryResultWithSummary(test, libraryResultSummary(test))
}

func hashLibraryResultWithSummary(test *ManagedTest, summary map[string]any) string {
	if test == nil {
		return hashCanonical(nil)
	}
	payload := map[string]any{"test": libraryResultTestHashPayload(test), "summary": summary}
	return hashCanonical(payload)
}

func hashLibraryResultTestOnly(test *ManagedTest) string {
	if test == nil {
		return hashCanonical(nil)
	}
	return hashCanonical(libraryResultCompactHashPayload(test))
}

func hashLibraryResultPreviousV1WithSummary(test *ManagedTest, summary map[string]any) string {
	if test == nil {
		return hashCanonical(nil)
	}
	return hashCanonical(map[string]any{"test": libraryResultCompactHashPayload(test), "summary": summary})
}

func libraryResultCompactHashPayload(test *ManagedTest) map[string]any {
	return map[string]any{
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
}

func libraryResultTestHashPayload(test *ManagedTest) any {
	return maskManagedTestWithOptions(test, managedTestMaskOptions{IncludeHistory: true, IncludeSparkline: true})
}

func libraryResultSummary(test *ManagedTest) map[string]any {
	if test == nil {
		return nil
	}
	return map[string]any{
		"status":       test.Status,
		"total_vusers": test.TotalVUsers,
		"duration":     test.DurationStr,
	}
}

func hashCanonical(value any) string {
	payload, _ := json.Marshal(value)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func (s *Server) libraryLink(remoteID string, projectID string, kind string, sourceInstanceID string, sourceItemID string) *librarySyncLink {
	if remoteID == "" {
		remoteID = "direct"
	}
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	link := s.libraryLinks[libraryLinkKey(remoteID, projectID, kind, sourceInstanceID, sourceItemID)]
	if link == nil && strings.TrimSpace(projectID) != "" {
		// Links written before project-aware synchronization are still readable.
		link = s.libraryLinks[libraryLinkKey(remoteID, "", kind, sourceInstanceID, sourceItemID)]
	}
	if link == nil {
		return nil
	}
	copied := *link
	return &copied
}

func (s *Server) rememberLibraryLink(remoteID string, projectID string, kind string, sourceInstanceID string, sourceItemID string, localItemID string, remoteItemID string, localHash string, remoteHash string) error {
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	if remoteID == "" {
		remoteID = "direct"
	}
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	key := libraryLinkKey(remoteID, projectID, kind, sourceInstanceID, sourceItemID)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		s.loadLibraryLinksState()
		s.libraryMu.Lock()
		previous, hadPrevious := s.libraryLinks[key]
		s.libraryLinks[key] = &librarySyncLink{
			RemoteID: remoteID, ProjectID: projectID, Kind: normalizeLibraryKind(kind), SourceInstanceID: sourceInstanceID,
			SourceItemID: sourceItemID, LocalItemID: localItemID, RemoteItemID: remoteItemID,
			LocalContentHash: localHash, RemoteContentHash: remoteHash, LastSyncedAt: time.Now().UTC(),
		}
		s.libraryMu.Unlock()
		if err := s.persistLibraryLinks(); err == nil {
			return nil
		} else {
			lastErr = err
			s.libraryMu.Lock()
			if hadPrevious {
				s.libraryLinks[key] = previous
			} else {
				delete(s.libraryLinks, key)
			}
			s.libraryMu.Unlock()
		}
	}
	s.loadLibraryLinksState()
	return lastErr
}

func libraryLinkKey(remoteID string, projectID string, kind string, sourceInstanceID string, sourceItemID string) string {
	return strings.Join([]string{strings.TrimSpace(remoteID), normalizeProjectID(strings.TrimSpace(projectID)), normalizeLibraryKind(kind), strings.TrimSpace(sourceInstanceID), strings.TrimSpace(sourceItemID)}, "\x00")
}

func (s *Server) annotateRemoteCatalogItem(remoteID string, projectID string, item LibraryCatalogItem) LibraryCatalogItem {
	link := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
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
		if localHash == item.ContentHash {
			item.SyncStatus = "synced"
			return item
		}
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
		if err := s.rememberLibraryLink(remoteID, bundle.Source.ProjectID, item.Kind, s.controllerID, localID, localID, item.LocalItemID, localHash, item.ContentHash); err != nil {
			slog.Warn("failed to persist remote import link", "remote_id", remoteID, "error", err)
		}
	}
}

package api

// library_links.go — split from library_federation.go (same package, behavior-preserving).

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
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
	copied := maskScriptSecrets(script)
	if copied == nil {
		return hashCanonical(nil)
	}
	copied.ID = ""
	copied.RefKey = ""
	copied.ProjectID = ""
	copied.FolderID = nil
	copied.CreatedAt = time.Time{}
	copied.UpdatedAt = time.Time{}
	// Legacy importer rows gain this server-owned marker when read from durable
	// storage. Canonicalize the pre-upgrade view by adding the marker to the hash
	// copy. Do not remove it generally: marker-bearing modern scripts must retain
	// their established hash.
	if source, legacy := storage.LegacyCredentialPreservingImportSource(copied); legacy {
		if copied.Metadata == nil {
			copied.Metadata = &dsl.ScriptMetadata{}
		}
		if copied.Metadata.Import == nil {
			copied.Metadata.Import = &dsl.ImportMetadata{Source: source}
		} else if strings.TrimSpace(copied.Metadata.Import.Source) == "" {
			copied.Metadata.Import.Source = source
		}
		if copied.Metadata.Import.Preserved == nil {
			copied.Metadata.Import.Preserved = make(map[string]any)
		}
		if _, exists := copied.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey]; !exists {
			copied.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] =
				dsl.ImportPreservedCredentialPolicyPreserve
		}
	}
	if copied.Metadata != nil && copied.Metadata.Import != nil && copied.Metadata.Import.Preserved != nil {
		delete(copied.Metadata.Import.Preserved, javaVuserApprovalEvidenceKey)
	}
	return hashCanonical(copied)
}

// libraryScriptHashMatches accepts the current canonical hash and the bounded
// pre-import-value-policy hash for known legacy importer output. Older
// library.sync_links.v1 rows captured the latter before durable reads began
// injecting the server-owned preservation marker. The compatibility candidate
// is used only for comparisons; newly written links and modern marker-bearing
// scripts keep the current canonical hash.
func libraryScriptHashMatches(script *dsl.Script, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	if hashLibraryScript(script) == expected {
		return true
	}
	for _, candidate := range legacyImportLibraryScriptHashCandidates(script) {
		if candidate == expected {
			return true
		}
	}
	return false
}

func libraryScriptHashesEquivalent(left, right *dsl.Script) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftHash := hashLibraryScript(left)
	rightHash := hashLibraryScript(right)
	return leftHash == rightHash ||
		libraryScriptHashMatches(left, rightHash) ||
		libraryScriptHashMatches(right, leftHash)
}

func legacyImportLibraryScriptHashCandidates(script *dsl.Script) []string {
	source, legacy := storage.LegacyCredentialPreservingImportSource(script)
	if !legacy {
		return nil
	}
	copied := canonicalLibraryScriptHashCopy(script)
	if copied == nil {
		return nil
	}
	// The oldest recorder rows had exact source-less GeneratedBy provenance and
	// no metadata container at all. For an inbound rolling-version bundle this
	// is already the complete pre-policy hash shape; durable recognition has
	// not yet had a chance to allocate metadata.import.
	if copied.Metadata == nil || copied.Metadata.Import == nil {
		return []string{hashCanonical(copied)}
	}
	delete(
		copied.Metadata.Import.Preserved,
		dsl.ImportPreservedCredentialPolicyKey,
	)
	candidates := []string{hashCanonical(copied)}

	// Some historical rows did not carry metadata.import.source. Recognition
	// fills it from the bounded GeneratedBy/GeneratedFrom classifier. Accept
	// that exact pre-recognition shape as a second legacy candidate.
	if strings.EqualFold(
		strings.TrimSpace(copied.Metadata.Import.Source),
		strings.TrimSpace(source),
	) {
		withoutSource := cloneScriptDefinition(copied)
		withoutSource.Metadata.Import.Source = ""
		candidates = append(candidates, hashCanonical(withoutSource))

		// The recorder's oldest rows omitted import metadata entirely. Only
		// remove empty compatibility-created containers; authored import
		// fields and runtime policy remain part of the hash.
		if withoutSource.Metadata.Import.SourceScriptName == "" &&
			len(withoutSource.Metadata.Import.Preserved) == 0 {
			withoutImport := cloneScriptDefinition(withoutSource)
			withoutImport.Metadata.Import = nil
			candidates = append(candidates, hashCanonical(withoutImport))
			if withoutImport.Metadata.RuntimePolicy == nil {
				withoutMetadata := cloneScriptDefinition(withoutImport)
				withoutMetadata.Metadata = nil
				candidates = append(candidates, hashCanonical(withoutMetadata))
			}
		}
	}
	return candidates
}

func canonicalLibraryScriptHashCopy(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	copied := maskScriptSecrets(script)
	if copied == nil {
		return nil
	}
	copied.ID = ""
	copied.RefKey = ""
	copied.ProjectID = ""
	copied.FolderID = nil
	copied.CreatedAt = time.Time{}
	copied.UpdatedAt = time.Time{}
	if copied.Metadata != nil && copied.Metadata.Import != nil &&
		copied.Metadata.Import.Preserved != nil {
		delete(copied.Metadata.Import.Preserved, javaVuserApprovalEvidenceKey)
	}
	return copied
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
	portable := maskManagedTestWithOptions(test, managedTestMaskOptions{IncludeHistory: true, IncludeSparkline: true})
	if portable != nil {
		// Archive locations and backend trust anchors are instance-specific. They
		// must neither leak nor make the same portable result hash differently in
		// catalog, export, diff, and import paths.
		portable.Archive = portableLibraryArchiveRecord(portable.Archive, "")
	}
	return portable
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
	return s.rememberLibraryLinkWithMutation(remoteID, projectID, kind, sourceInstanceID, sourceItemID, localItemID, remoteItemID, localHash, remoteHash, "")
}

func (s *Server) rememberLibraryLinkWithMutation(remoteID string, projectID string, kind string, sourceInstanceID string, sourceItemID string, localItemID string, remoteItemID string, localHash string, remoteHash string, mutationID string) error {
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	if remoteID == "" {
		remoteID = "direct"
	}
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	mutationID = strings.TrimSpace(mutationID)
	if mutationID == "" {
		mutationID = uuid.NewString()
	}
	key := libraryLinkKey(remoteID, projectID, kind, sourceInstanceID, sourceItemID)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		s.loadLibraryLinksState()
		s.libraryMu.Lock()
		previous, hadPrevious := s.libraryLinks[key]
		s.libraryLinks[key] = &librarySyncLink{
			RemoteID: remoteID, ProjectID: projectID, Kind: normalizeLibraryKind(kind), SourceInstanceID: sourceInstanceID,
			SourceItemID: sourceItemID, LocalItemID: localItemID, RemoteItemID: remoteItemID,
			LocalContentHash: localHash, RemoteContentHash: remoteHash, LastSyncedAt: time.Now().UTC(), MutationID: mutationID,
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
	remoteChanged := item.ContentHash != "" && item.ContentHash != link.RemoteContentHash
	var localScript *dsl.Script
	if normalizeLibraryKind(item.Kind) == "script" {
		if stored, ok := s.runner.GetScript(link.LocalItemID); ok && stored != nil {
			localScript = stored.Script
		}
		if remoteChanged && localScript != nil &&
			libraryScriptHashMatches(localScript, item.ContentHash) &&
			libraryScriptHashMatches(localScript, link.RemoteContentHash) {
			remoteChanged = false
		}
	}
	if remoteChanged {
		item.SyncStatus = "stale"
	}
	if localHash, err := s.localLibraryItemHash(item.Kind, link.LocalItemID); err == nil &&
		localHash != link.LocalContentHash &&
		!s.localLibraryItemHashMatches(item.Kind, link.LocalItemID, link.LocalContentHash) {
		localMatchesRemote := localHash == item.ContentHash
		if localScript != nil && libraryScriptHashMatches(localScript, item.ContentHash) {
			localMatchesRemote = true
		}
		if localMatchesRemote {
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

func (s *Server) localLibraryItemHashMatches(kind, id, expected string) bool {
	if normalizeLibraryKind(kind) == "script" {
		script, ok := s.runner.GetScript(id)
		return ok && script != nil && libraryScriptHashMatches(script.Script, expected)
	}
	hash, err := s.localLibraryItemHash(kind, id)
	return err == nil && hash == strings.TrimSpace(expected)
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

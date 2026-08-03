package api

// library_bundle.go — split from library_federation.go (same package, behavior-preserving).

import (
	"container/heap"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultLibraryCatalogPageSize = 50
	maxLibraryCatalogPageSize     = 100
	maxLibraryCatalogOffset       = 10_000
	maxLibraryImportItems         = 10_000
	libraryResultMutationIDKey    = "library_import_mutation_id"
)

type libraryCatalogCandidate struct {
	item     LibraryCatalogItem
	script   *StoredScript
	scenario *StoredScenario
	result   *ManagedTest
}

type libraryCatalogMaxHeap []libraryCatalogCandidate

func (h libraryCatalogMaxHeap) Len() int { return len(h) }
func (h libraryCatalogMaxHeap) Less(i, j int) bool {
	// A max heap keeps the worst retained candidate at index 0.
	return libraryCatalogCandidateLess(h[j], h[i])
}
func (h libraryCatalogMaxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *libraryCatalogMaxHeap) Push(value any) {
	*h = append(*h, value.(libraryCatalogCandidate))
}
func (h *libraryCatalogMaxHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func libraryCatalogCandidateLess(left, right libraryCatalogCandidate) bool {
	if left.item.Kind != right.item.Kind {
		return left.item.Kind < right.item.Kind
	}
	leftName := strings.ToLower(left.item.Name)
	rightName := strings.ToLower(right.item.Name)
	if leftName != rightName {
		return leftName < rightName
	}
	return left.item.ID < right.item.ID
}

// libraryCatalogItems retains the internal convenience API while enforcing the
// same bounded default used by the HTTP handler.
func (s *Server) libraryCatalogItems(projectID string, kind string) []LibraryCatalogItem {
	items, _ := s.libraryCatalogPage(projectID, kind, 0, defaultLibraryCatalogPageSize)
	return items
}

// libraryCatalogPage first builds and sorts lightweight metadata, then clones
// and hashes only the requested page. In particular it never clones every
// retained result history merely to return compact catalog rows.
func (s *Server) libraryCatalogPage(projectID string, kind string, offset int, limit int) ([]LibraryCatalogItem, int) {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxLibraryCatalogOffset {
		offset = maxLibraryCatalogOffset
	}
	if limit <= 0 || limit > maxLibraryCatalogPageSize {
		limit = defaultLibraryCatalogPageSize
	}

	s.runner.refreshScriptsForHARead()
	s.runner.refreshScenariosForHARead()
	s.runner.refreshTestsForHARead()
	s.runner.refreshReadMetadataForTestList()

	selected, total := func() ([]libraryCatalogCandidate, int) {
		s.runner.mu.RLock()
		defer s.runner.mu.RUnlock()

		window := offset + limit
		if window > maxLibraryCatalogOffset+maxLibraryCatalogPageSize {
			window = maxLibraryCatalogOffset + maxLibraryCatalogPageSize
		}
		candidates := make(libraryCatalogMaxHeap, 0, window)
		total := 0
		retain := func(candidate libraryCatalogCandidate) {
			total++
			if window == 0 {
				return
			}
			if len(candidates) < window {
				heap.Push(&candidates, candidate)
				return
			}
			if libraryCatalogCandidateLess(candidate, candidates[0]) {
				heap.Pop(&candidates)
				heap.Push(&candidates, candidate)
			}
		}
		if kind == "" || kind == "script" {
			for _, script := range s.runner.scripts {
				if script == nil || script.Script == nil || normalizeProjectID(script.Script.ProjectID) != normalizeProjectID(script.ProjectID) || (projectID != "" && normalizeProjectID(script.ProjectID) != projectID) {
					continue
				}
				retain(libraryCatalogCandidate{item: LibraryCatalogItem{
					Kind: "script", ID: script.ID, Name: script.Name, ProjectID: script.ProjectID,
					Protocol: script.Protocol, Version: script.Version, SourceInstanceID: s.controllerID,
					SourceItemID: script.ID, UpdatedAt: script.CreatedAt,
				}, script: script})
			}
		}
		if kind == "" || kind == "scenario" {
			for _, scenario := range s.runner.scenarios {
				if scenario == nil || (projectID != "" && scenario.ProjectID != projectID) {
					continue
				}
				retain(libraryCatalogCandidate{item: LibraryCatalogItem{
					Kind: "scenario", ID: scenario.ID, Name: scenario.Name, ProjectID: scenario.ProjectID,
					SourceInstanceID: s.controllerID, SourceItemID: scenario.ID, UpdatedAt: scenario.UpdatedAt,
				}, scenario: scenario})
			}
		}
		if kind == "" || kind == "result" {
			for _, test := range s.runner.tests {
				if test == nil || !isTerminalTestStatus(test.Status) || (projectID != "" && test.ProjectID != projectID) {
					continue
				}
				releaseRef := ReleaseRef{}
				if test.ReleaseID != "" {
					releaseRef = s.runner.releaseRefLocked(test.ReleaseID)
				}
				retain(libraryCatalogCandidate{item: LibraryCatalogItem{
					Kind: "result", ID: test.ID, Name: test.Name, ProjectID: test.ProjectID,
					ApplicationID: test.ApplicationID, ReleaseID: test.ReleaseID, ReleaseName: releaseRef.Name, Status: test.Status,
					SourceInstanceID: s.controllerID, SourceItemID: test.ID,
					ExecutionRunDate: managedTestExecutionRunDate(test), UpdatedAt: managedTestCompletedAt(test),
					HasArchive: test.Archive != nil,
				}, result: test})
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return libraryCatalogCandidateLess(candidates[i], candidates[j]) })
		start := min(offset, len(candidates))
		end := min(start+limit, len(candidates))
		page := make([]libraryCatalogCandidate, 0, end-start)
		for _, candidate := range candidates[start:end] {
			copy := libraryCatalogCandidate{item: candidate.item}
			switch candidate.item.Kind {
			case "script":
				copy.script = cloneStoredScriptForReadLocked(candidate.script)
			case "scenario":
				copy.scenario = cloneStoredScenarioForReadLocked(candidate.scenario)
			case "result":
				copy.result = cloneManagedTestForReadLocked(candidate.result)
			}
			page = append(page, copy)
		}
		return page, total
	}()

	items := make([]LibraryCatalogItem, 0, len(selected))
	for _, candidate := range selected {
		item := candidate.item
		switch item.Kind {
		case "script":
			item.ContentHash = hashLibraryScript(candidate.script.Script)
		case "scenario":
			item.ContentHash = hashLibraryScenario(candidate.scenario)
			item.Dependencies = scenarioDependencies(candidate.scenario, s)
		case "result":
			item.ContentHash = hashLibraryResult(candidate.result)
		}
		items = append(items, item)
	}
	return items, total
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
	expectedProjectID := ""
	if strings.TrimSpace(projectID) != "" {
		expectedProjectID = normalizeProjectID(projectID)
	}
	bundle := &LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		BundleID:      uuid.New().String(),
		CreatedAt:     time.Now().UTC(),
		Source:        LibraryBundleSource{InstanceID: s.controllerID},
		Provenance: map[string]any{
			"exported_by": "apexload",
			"mode":        "explicit",
		},
	}
	added := map[string]struct{}{}
	addItem := func(item LibraryBundleItem) error {
		if err := validateLibraryBundleItemProject(item, expectedProjectID); err != nil {
			return err
		}
		key := item.Kind + ":" + item.SourceItemID
		if _, ok := added[key]; ok {
			return nil
		}
		added[key] = struct{}{}
		if item.SourceInstanceID == "" {
			item.SourceInstanceID = s.controllerID
		}
		bundle.Items = append(bundle.Items, item)
		return nil
	}
	setPrimaryProject := func(item LibraryBundleItem) error {
		actual := normalizeProjectID(item.ProjectID)
		if expectedProjectID != "" && actual != expectedProjectID {
			return fmt.Errorf("%s belongs to project %s, not authorized request project %s", item.Kind, actual, expectedProjectID)
		}
		if expectedProjectID == "" {
			expectedProjectID = actual
		}
		bundle.Source.ProjectID = expectedProjectID
		return nil
	}
	switch kind {
	case "script":
		item, err := s.libraryScriptBundleItem(id)
		if err != nil {
			return nil, err
		}
		if err := setPrimaryProject(item); err != nil {
			return nil, err
		}
		if err := addItem(item); err != nil {
			return nil, err
		}
	case "scenario":
		item, err := s.libraryScenarioBundleItem(id)
		if err != nil {
			return nil, err
		}
		if err := setPrimaryProject(item); err != nil {
			return nil, err
		}
		if err := s.validateScenarioLibraryDependencies(id, expectedProjectID); err != nil {
			return nil, err
		}
		if includeDependencies {
			for _, dep := range item.Dependencies {
				if dep.Kind == "script" {
					scriptItem, err := s.libraryScriptBundleItem(dep.ID)
					if err != nil {
						return nil, fmt.Errorf("scenario dependency %s: %w", dep.ID, err)
					}
					if err := addItem(scriptItem); err != nil {
						return nil, fmt.Errorf("scenario dependency %s: %w", dep.ID, err)
					}
				}
			}
		}
		if err := addItem(item); err != nil {
			return nil, err
		}
	case "result":
		item, err := s.libraryResultBundleItem(id)
		if err != nil {
			return nil, err
		}
		if err := setPrimaryProject(item); err != nil {
			return nil, err
		}
		if err := addItem(item); err != nil {
			return nil, err
		}
	}
	return bundle, nil
}

func validateLibraryBundleItemProject(item LibraryBundleItem, expectedProjectID string) error {
	actual := normalizeProjectID(item.ProjectID)
	if expectedProjectID != "" && actual != expectedProjectID {
		return fmt.Errorf("%s %s belongs to project %s, not authorized request project %s", normalizeLibraryKind(item.Kind), item.SourceItemID, actual, expectedProjectID)
	}
	if item.Script != nil && normalizeProjectID(item.Script.ProjectID) != actual {
		return fmt.Errorf("script %s has inconsistent project ownership", item.SourceItemID)
	}
	if item.Result != nil && item.Result.Test != nil && normalizeProjectID(item.Result.Test.ProjectID) != actual {
		return fmt.Errorf("result %s has inconsistent project ownership", item.SourceItemID)
	}
	for _, dependency := range item.Dependencies {
		if strings.TrimSpace(dependency.ProjectID) != "" && normalizeProjectID(dependency.ProjectID) != actual {
			return fmt.Errorf("%s %s dependency %s belongs to a different project", normalizeLibraryKind(item.Kind), item.SourceItemID, dependency.ID)
		}
	}
	return nil
}

func validateLibraryBundleProject(bundle *LibraryBundle, expectedProjectID string) error {
	if bundle == nil {
		return fmt.Errorf("library bundle is missing")
	}
	expectedProjectID = normalizeProjectID(expectedProjectID)
	if normalizeProjectID(bundle.Source.ProjectID) != expectedProjectID {
		return fmt.Errorf("library bundle source belongs to a different project")
	}
	for _, item := range bundle.Items {
		if err := validateLibraryBundleItemProject(item, expectedProjectID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) validateScenarioLibraryDependencies(id string, expectedProjectID string) error {
	scenario, ok := s.runner.GetScenario(id)
	if !ok || scenario == nil {
		return fmt.Errorf("scenario not found")
	}
	if normalizeProjectID(scenario.ProjectID) != expectedProjectID {
		return fmt.Errorf("scenario %s belongs to project %s, not authorized request project %s", id, normalizeProjectID(scenario.ProjectID), expectedProjectID)
	}
	for _, ref := range scenario.Scripts {
		stored, ok := s.runner.GetScript(strings.TrimSpace(ref.ScriptID))
		if !ok || stored == nil || stored.Script == nil {
			return fmt.Errorf("scenario dependency %s: script not found", ref.ScriptID)
		}
		if normalizeProjectID(stored.ProjectID) != expectedProjectID || normalizeProjectID(stored.Script.ProjectID) != expectedProjectID {
			return fmt.Errorf("scenario dependency %s belongs to a different project", ref.ScriptID)
		}
	}
	return nil
}

func (s *Server) libraryScriptBundleItem(id string) (LibraryBundleItem, error) {
	stored, ok := s.runner.GetScript(id)
	if !ok || stored == nil || stored.Script == nil {
		return LibraryBundleItem{}, fmt.Errorf("script not found")
	}
	if normalizeProjectID(stored.ProjectID) != normalizeProjectID(stored.Script.ProjectID) {
		return LibraryBundleItem{}, fmt.Errorf("script has inconsistent project ownership")
	}
	script := maskScriptSecrets(stored.Script)
	return LibraryBundleItem{
		Kind:             "script",
		SourceInstanceID: s.controllerID,
		SourceItemID:     stored.ID,
		ProjectID:        stored.ProjectID,
		Name:             disclosureSafeScriptName(stored.Name, stored.Script),
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
	req := createScenarioRequestFromStored(maskStoredScenario(scenario))
	return LibraryBundleItem{
		Kind:             "scenario",
		SourceInstanceID: s.controllerID,
		SourceItemID:     scenario.ID,
		ProjectID:        scenario.ProjectID,
		Name:             scenario.Name,
		ContentHash:      hashCanonical(req),
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
	if !isTerminalTestStatus(test.Status) {
		return LibraryBundleItem{}, fmt.Errorf("only terminal results (complete, failed, or aborted) can be exported")
	}
	portableTest := maskManagedTestWithOptions(test, managedTestMaskOptions{IncludeHistory: true, IncludeSparkline: true})
	portableTest.Archive = portableLibraryArchiveRecord(portableTest.Archive, "")
	result := &LibraryResultPayload{
		Test:    portableTest,
		Archive: portableTest.Archive,
		Summary: libraryResultSummary(portableTest),
	}
	if record, ok := s.runner.GetRunArchive(id); ok && record != nil {
		result.Archive = portableLibraryArchiveRecord(record, "")
		if lease, err := acquireArchiveMaterialized(context.Background(), record); err == nil {
			defer lease.Release()
			manifest, err := loadRunArchiveManifestFromRecord(record)
			if err != nil {
				return LibraryBundleItem{}, fmt.Errorf("load result archive manifest: %w", err)
			}
			manifestHash, archiveSize, err := sanitizedRunArchiveMetadata(context.Background(), record, manifest)
			if err != nil {
				return LibraryBundleItem{}, fmt.Errorf("prepare safe result archive: %w", err)
			}
			result.ArchiveTransfer = "stream"
			result.ArchiveManifestHash = manifestHash
			result.ArchiveSizeBytes = archiveSize
			result.Archive = portableLibraryArchiveRecord(record, manifestHash)
		}
		portableTest.Archive = result.Archive
	}
	return LibraryBundleItem{
		Kind:               "result",
		SourceInstanceID:   s.controllerID,
		SourceItemID:       test.ID,
		ProjectID:          test.ProjectID,
		Name:               test.Name,
		ContentHash:        hashLibraryResult(portableTest),
		ContentHashVersion: libraryResultHashVersionCurrent,
		CreatedAt:          test.CreatedAt,
		UpdatedAt:          managedTestCompletedAt(test),
		Result:             result,
	}, nil
}

func portableLibraryArchiveRecord(record *storage.RunArchiveRecord, manifestHash string) *storage.RunArchiveRecord {
	portable := maskRunArchiveRecord(record)
	if portable != nil {
		portable.ManifestSHA256 = strings.TrimPrefix(strings.TrimSpace(manifestHash), "sha256:")
	}
	return portable
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
	applicationID, releaseID, err := s.validateLibraryImportResultPlacement(req, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.recoverExpiredLibraryImportClaims(); err != nil {
		return nil, fmt.Errorf("recover expired library import before preflight: %w", err)
	}
	// Synchronization links are shared authorization/conflict metadata. Refresh
	// them under the same mutation fence used by link writers so this read can
	// never replace an in-flight local mutation with stale durable state.
	s.refreshLibraryLinksForImport()
	response := &LibraryImportResponse{SchemaVersion: libraryBundleSchemaVersion, ImportedAt: time.Now().UTC()}
	response.Conflicts = s.preflightLibraryImport(req, remoteID, projectID, archiveImport)
	if len(response.Conflicts) > 0 {
		return response, nil
	}
	claims, err := s.acquireLibraryImportClaims(bundle, remoteID, projectID)
	if err != nil {
		if errors.Is(err, errLibraryImportClaimBusy) {
			response.Conflicts = []LibraryConflict{{Reason: err.Error()}}
			return response, nil
		}
		return nil, fmt.Errorf("claim library import identities: %w", err)
	}
	// A peer may have completed between the optimistic preflight and our claim.
	// Refresh and validate again while same-identity importers are excluded.
	s.refreshLibraryLinksForImport()
	response.Conflicts = s.preflightLibraryImport(req, remoteID, projectID, archiveImport)
	if len(response.Conflicts) > 0 {
		if err := claims.complete(); err != nil {
			return nil, fmt.Errorf("release rejected library import claims: %w", err)
		}
		return response, nil
	}
	journal := newLibraryImportJournal(s)
	abort := func(conflict LibraryConflict) (*LibraryImportResponse, error) {
		response.Items = nil
		response.Warnings = nil
		response.Conflicts = []LibraryConflict{conflict}
		rollbackErr := journal.rollback()
		// A prepared item can fail after its asset write but before the caller
		// has enough outcome data to append an in-memory journal entry (for
		// example, result evidence followed by archive activation failure).
		// Therefore every abort retains and immediately reconciles the durable
		// pre-mutation intents, even when the fast in-memory rollback succeeded.
		claimErr := claims.abandon()
		var recoveryErr error
		if claimErr == nil {
			recoveryErr = s.recoverExpiredLibraryImportClaims()
		}
		if claimErr == nil && recoveryErr == nil {
			if rollbackErr != nil {
				slog.Warn("library import rollback required durable recovery", "reason", conflict.Reason, "error", rollbackErr)
			}
			return response, nil
		}
		if combined := errors.Join(rollbackErr, claimErr, recoveryErr); combined != nil {
			return nil, fmt.Errorf("library bundle import failed (%s) and could not be rolled back safely: %w", conflict.Reason, combined)
		}
		return response, nil
	}
	idMap := map[string]string{}
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) != "script" {
			continue
		}
		if item.Script == nil {
			return abort(LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "script payload is missing"})
		}
		item.importMutationID = claims.mutationID(item)
		linkBefore := s.exactLibraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
		effectiveLink := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
		var scriptBefore *StoredScript
		if effectiveLink != nil {
			scriptBefore, _ = s.runner.GetScript(effectiveLink.LocalItemID)
		} else if importID, idErr := deterministicLibraryImportID(remoteID, projectID, item); idErr == nil {
			scriptBefore, _ = s.runner.GetScript(importID)
		}
		plannedLocalID := ""
		if effectiveLink != nil {
			plannedLocalID = effectiveLink.LocalItemID
		} else {
			plannedLocalID, _ = deterministicLibraryImportID(remoteID, projectID, item)
		}
		if err := claims.prepare(item, plannedLocalID, linkBefore, scriptBefore, nil, nil, false, false, ""); err != nil {
			return abort(LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: plannedLocalID, Reason: "persist script import intent: " + err.Error()})
		}
		localID, action, conflict := s.importLibraryScript(item, projectID, remoteID)
		if conflict != nil {
			return abort(*conflict)
		}
		scriptAfter, ok := s.runner.GetScript(localID)
		if !ok || scriptAfter == nil {
			return abort(LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "imported script disappeared before commit"})
		}
		linkAfter := s.exactLibraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
		var ownedLinkAfter *librarySyncLink
		if linkAfter != nil && linkAfter.MutationID == item.importMutationID {
			ownedLinkAfter = linkAfter
		}
		journal.recordScript(scriptBefore, scriptAfter, remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, linkBefore, ownedLinkAfter)
		if err := claims.markApplied(item, false); err != nil {
			return abort(LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "persist applied script import intent: " + err.Error()})
		}
		if strings.HasSuffix(action, "_link_pending") || linkAfter == nil || linkAfter.MutationID != item.importMutationID {
			return abort(LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "failed to commit the script synchronization link"})
		}
		idMap[item.SourceItemID] = localID
		response.Items = append(response.Items, LibraryImportResultItem{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, ContentHash: item.ContentHash, Action: action})
	}
	for _, item := range bundle.Items {
		switch normalizeLibraryKind(item.Kind) {
		case "script":
			continue
		case "scenario":
			item.importMutationID = claims.mutationID(item)
			linkBefore := s.exactLibraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
			effectiveLink := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
			var scenarioBefore *StoredScenario
			if effectiveLink != nil {
				scenarioBefore, _ = s.runner.GetScenario(effectiveLink.LocalItemID)
			} else if importID, idErr := deterministicLibraryImportID(remoteID, projectID, item); idErr == nil {
				scenarioBefore, _ = s.runner.GetScenario(importID)
			}
			plannedLocalID := ""
			if effectiveLink != nil {
				plannedLocalID = effectiveLink.LocalItemID
			} else {
				plannedLocalID, _ = deterministicLibraryImportID(remoteID, projectID, item)
			}
			intendedRequest := cloneScenarioRequestForLibrary(item.Scenario.Request)
			for index := range intendedRequest.Groups {
				if mapped := idMap[intendedRequest.Groups[index].ScriptID]; mapped != "" {
					intendedRequest.Groups[index].ScriptID = mapped
				}
			}
			intendedLocalHash := s.libraryScenarioRequestRecoveryHash(intendedRequest)
			if err := claims.prepare(item, plannedLocalID, linkBefore, nil, scenarioBefore, nil, false, false, "", intendedLocalHash); err != nil {
				return abort(LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: plannedLocalID, Reason: "persist scenario import intent: " + err.Error()})
			}
			localID, action, conflict := s.importLibraryScenario(item, projectID, remoteID, idMap)
			if conflict != nil {
				return abort(*conflict)
			}
			scenarioAfter, ok := s.runner.GetScenario(localID)
			if !ok || scenarioAfter == nil {
				return abort(LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "imported scenario disappeared before commit"})
			}
			linkAfter := s.exactLibraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
			var ownedLinkAfter *librarySyncLink
			if linkAfter != nil && linkAfter.MutationID == item.importMutationID {
				ownedLinkAfter = linkAfter
			}
			journal.recordScenario(scenarioBefore, scenarioAfter, remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, linkBefore, ownedLinkAfter)
			if err := claims.markApplied(item, false); err != nil {
				return abort(LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "persist applied scenario import intent: " + err.Error()})
			}
			if strings.HasSuffix(action, "_link_pending") || linkAfter == nil || linkAfter.MutationID != item.importMutationID {
				return abort(LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "failed to commit the scenario synchronization link"})
			}
			response.Items = append(response.Items, LibraryImportResultItem{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, ContentHash: item.ContentHash, Action: action})
		case "result":
			item.importMutationID = claims.mutationID(item)
			linkBefore := s.exactLibraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
			var itemArchive *libraryArchiveImport
			if item.Result != nil && strings.EqualFold(item.Result.ArchiveTransfer, "stream") {
				itemArchive = archiveImport
			}
			importID, idErr := deterministicLibraryImportID(remoteID, projectID, item)
			if idErr != nil {
				return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: idErr.Error()})
			}
			resultBefore, _, lookupErr := s.getExternalResultImport(importID)
			if lookupErr != nil {
				return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: importID, Reason: "read result before import: " + lookupErr.Error()})
			}
			archiveExpected := item.Result != nil && ((itemArchive != nil && strings.TrimSpace(itemArchive.ZipPath) != "") || strings.TrimSpace(item.Result.ArchiveZipB64) != "")
			archivePath := ""
			archiveExisted := false
			if archiveExpected {
				archiveExisted = resultBefore != nil && resultBefore.ArchiveRecord != nil
				if currentArchiveBackend().Backend == archiveBackendFilesystem {
					archivePath = libraryImportedArchiveDir(importID)
					if _, statErr := os.Stat(archivePath); statErr == nil {
						archiveExisted = true
					} else if !os.IsNotExist(statErr) {
						return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: importID, Reason: "inspect result archive before import: " + statErr.Error()})
					}
				}
			}
			if err := claims.prepare(item, importID, linkBefore, nil, nil, resultBefore, archiveExpected, archiveExisted, archivePath); err != nil {
				return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: importID, Reason: "persist result import intent: " + err.Error()})
			}
			localID, action, archiveActivated, conflict := s.importLibraryResultWithDestinationOutcome(item, projectID, applicationID, releaseID, remoteID, itemArchive)
			if conflict != nil {
				return abort(*conflict)
			}
			resultAfter, ok, lookupErr := s.getExternalResultImport(localID)
			if lookupErr != nil || !ok || resultAfter == nil {
				reason := "imported result disappeared before commit"
				if lookupErr != nil {
					reason = "read imported result before commit: " + lookupErr.Error()
				}
				return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: reason})
			}
			linkAfter := s.exactLibraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
			var ownedLinkAfter *librarySyncLink
			if linkAfter != nil && linkAfter.MutationID == item.importMutationID {
				ownedLinkAfter = linkAfter
			}
			if archiveActivated && currentArchiveBackend().Backend == archiveBackendFilesystem {
				archivePath = libraryImportedArchiveDir(importID)
			}
			journal.recordResult(strings.HasPrefix(action, "created"), resultAfter, archiveActivated, archivePath, remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, linkBefore, ownedLinkAfter)
			if err := claims.markApplied(item, archiveActivated); err != nil {
				return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "persist applied result import intent: " + err.Error()})
			}
			if strings.HasSuffix(action, "_link_pending") || linkAfter == nil || linkAfter.MutationID != item.importMutationID {
				return abort(LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "failed to commit the result synchronization link"})
			}
			response.Items = append(response.Items, LibraryImportResultItem{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, ContentHash: item.ContentHash, Action: action})
		}
	}
	if err := claims.complete(); err != nil {
		return abort(LibraryConflict{Reason: "commit library import transaction: " + err.Error()})
	}
	return response, nil
}

type preflightLibraryScript struct {
	script *dsl.Script
}

// preflightLibraryImport validates the complete dependency graph and every
// payload before the first durable mutation. Import used to validate and write
// item-by-item, so a malformed final item could leave all earlier items and
// links committed despite a conflict response.
func (s *Server) preflightLibraryImport(req LibraryImportRequest, remoteID string, projectID string, archiveImport *libraryArchiveImport) []LibraryConflict {
	if len(req.Bundle.Items) > maxLibraryImportItems {
		return []LibraryConflict{{Reason: fmt.Sprintf("library bundle exceeds the %d item limit", maxLibraryImportItems)}}
	}
	conflicts := make([]LibraryConflict, 0)
	seen := make(map[string]struct{}, len(req.Bundle.Items))
	seenScriptIDs := make(map[string]string)
	plannedScripts := make(map[string]preflightLibraryScript)

	conflict := func(item LibraryBundleItem, reason string) {
		conflicts = append(conflicts, LibraryConflict{Kind: normalizeLibraryKind(item.Kind), SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: reason})
	}
	for _, item := range req.Bundle.Items {
		kind := normalizeLibraryKind(item.Kind)
		key := kind + "\x00" + strings.TrimSpace(item.SourceInstanceID) + "\x00" + strings.TrimSpace(item.SourceItemID)
		if kind == "" {
			conflict(item, "kind must be one of script, scenario, or result")
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			conflict(item, "bundle contains a duplicate source item")
			continue
		}
		seen[key] = struct{}{}
		if kind == "script" {
			sourceItemID := strings.TrimSpace(item.SourceItemID)
			if priorSource, duplicate := seenScriptIDs[sourceItemID]; duplicate {
				conflict(item, "bundle contains an ambiguous script source_item_id also used by source instance "+priorSource)
				continue
			}
			seenScriptIDs[sourceItemID] = strings.TrimSpace(item.SourceInstanceID)
		}
		if _, err := deterministicLibraryImportID(remoteID, projectID, item); err != nil {
			conflict(item, err.Error())
		}
	}

	for _, item := range req.Bundle.Items {
		if normalizeLibraryKind(item.Kind) != "script" {
			continue
		}
		if item.Script == nil {
			conflict(item, "script payload is missing")
			continue
		}
		if strings.TrimSpace(item.ProjectID) != "" && normalizeProjectID(item.Script.ProjectID) != normalizeProjectID(item.ProjectID) {
			conflict(item, "script payload has inconsistent source project ownership")
			continue
		}
		if strings.TrimSpace(item.ContentHash) == "" ||
			!libraryScriptHashMatches(item.Script, item.ContentHash) {
			conflict(item, "declared content hash does not match script payload")
			continue
		}
		link := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
		localID := ""
		var prior *dsl.Script
		if link != nil {
			localID = link.LocalItemID
			if local, ok := s.runner.GetScript(localID); ok && local != nil {
				if normalizeProjectID(local.ProjectID) != projectID {
					conflict(item, "existing sync link belongs to a different destination project")
					continue
				}
				prior = local.Script
				localChanged := !libraryScriptHashMatches(local.Script, link.LocalContentHash)
				remoteChanged := !libraryScriptHashMatches(item.Script, link.RemoteContentHash)
				converged := libraryScriptHashesEquivalent(local.Script, item.Script) ||
					libraryScriptHashMatches(local.Script, item.ContentHash)
				if localChanged && remoteChanged && !converged {
					conflict(item, "local and remote script changed since last sync")
					continue
				}
			}
		}
		if localID == "" {
			localID, _ = deterministicLibraryImportID(remoteID, projectID, item)
			if existing, ok := s.runner.GetScript(localID); ok && existing != nil {
				conflict(item, "deterministic import id already belongs to an unlinked local script")
				continue
			}
		}
		projected := cloneScriptForLibrary(item.Script)
		if err := reconcileMaskedScriptSecrets(projected, prior); err != nil {
			conflict(item, err.Error())
			continue
		}
		projected.ProjectID = projectID
		projected.ID = localID
		if issues := validateScript(projected); len(issues) > 0 {
			conflict(item, strings.Join(issues, "; "))
			continue
		}
		plannedScripts[item.SourceItemID] = preflightLibraryScript{script: item.Script}
	}

	for _, item := range req.Bundle.Items {
		if normalizeLibraryKind(item.Kind) != "scenario" {
			continue
		}
		if item.Scenario == nil {
			conflict(item, "scenario payload is missing")
			continue
		}
		if strings.TrimSpace(item.ContentHash) == "" || item.ContentHash != hashCanonical(item.Scenario.Request) {
			conflict(item, "declared content hash does not match scenario payload")
			continue
		}
		dependencies := make(map[string]LibraryDependency, len(item.Dependencies))
		invalidDependencyProject := false
		for _, dependency := range item.Dependencies {
			dependencies[dependency.ID] = dependency
			if strings.TrimSpace(item.ProjectID) != "" && strings.TrimSpace(dependency.ProjectID) != "" && normalizeProjectID(dependency.ProjectID) != normalizeProjectID(item.ProjectID) {
				invalidDependencyProject = true
			}
		}
		if invalidDependencyProject {
			conflict(item, "scenario dependency belongs to a different source project")
			continue
		}
		valid := true
		for _, group := range item.Scenario.Request.Groups {
			sourceID := strings.TrimSpace(group.ScriptID)
			if planned, ok := plannedScripts[sourceID]; ok {
				if dependency, exists := dependencies[sourceID]; exists &&
					strings.TrimSpace(dependency.ContentHash) != "" &&
					!libraryScriptHashMatches(planned.script, dependency.ContentHash) {
					conflict(item, "referenced script content does not match dependency hash: "+sourceID)
					valid = false
					break
				}
				continue
			}
			stored, ok := s.runner.GetScript(sourceID)
			if !ok || stored == nil || stored.Script == nil {
				conflict(item, "referenced script not found: "+sourceID)
				valid = false
				break
			}
			if normalizeProjectID(stored.ProjectID) != projectID || normalizeProjectID(stored.Script.ProjectID) != projectID {
				conflict(item, "referenced script belongs to a different destination project")
				valid = false
				break
			}
			if dependency, exists := dependencies[sourceID]; exists &&
				strings.TrimSpace(dependency.ContentHash) != "" &&
				!libraryScriptHashMatches(stored.Script, dependency.ContentHash) {
				conflict(item, "referenced script content does not match dependency hash: "+sourceID)
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		link := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
		if link != nil {
			if local, ok := s.runner.GetScenario(link.LocalItemID); ok && local != nil {
				if normalizeProjectID(local.ProjectID) != projectID {
					conflict(item, "existing sync link belongs to a different destination project")
					continue
				}
				localHash := hashLibraryScenario(local)
				if localHash != link.LocalContentHash && item.ContentHash != link.RemoteContentHash && localHash != item.ContentHash {
					conflict(item, "local and remote scenario changed since last sync")
				}
			}
		} else if importID, err := deterministicLibraryImportID(remoteID, projectID, item); err == nil {
			if existing, ok := s.runner.GetScenario(importID); ok && existing != nil {
				conflict(item, "deterministic import id already belongs to an unlinked local scenario")
			}
		}
	}

	for _, item := range req.Bundle.Items {
		if normalizeLibraryKind(item.Kind) != "result" {
			continue
		}
		if reason := validateLibraryResultItem(item); reason != "" {
			conflict(item, reason)
			continue
		}
		if reason := preflightLibraryResultArchive(item.Result, archiveImport); reason != "" {
			conflict(item, reason)
		}
	}
	return conflicts
}

func validateLibraryResultItem(item LibraryBundleItem) string {
	if item.Result == nil {
		return "result payload is missing"
	}
	if item.Result.Test == nil {
		return "result test payload is missing"
	}
	if strings.TrimSpace(item.ProjectID) != "" && normalizeProjectID(item.Result.Test.ProjectID) != normalizeProjectID(item.ProjectID) {
		return "result payload has inconsistent source project ownership"
	}
	contentHash := strings.TrimSpace(item.ContentHash)
	fullHash := hashLibraryResultWithSummary(item.Result.Test, item.Result.Summary)
	previousV1Hash := hashLibraryResultPreviousV1WithSummary(item.Result.Test, item.Result.Summary)
	legacyHash := contentHash == hashLibraryResultTestOnly(item.Result.Test) && hashCanonical(item.Result.Summary) == hashCanonical(libraryResultSummary(item.Result.Test))
	switch strings.TrimSpace(item.ContentHashVersion) {
	case "", libraryResultHashVersionCurrent:
		if contentHash == "" || contentHash != fullHash {
			return "declared content hash does not match result payload"
		}
	case libraryResultHashVersionLegacyV1:
		if contentHash == "" || (contentHash != previousV1Hash && !legacyHash) {
			return "declared content hash does not match result payload"
		}
	default:
		return "unsupported result content_hash_version " + strings.TrimSpace(item.ContentHashVersion)
	}
	return ""
}

func preflightLibraryResultArchive(result *LibraryResultPayload, archiveImport *libraryArchiveImport) string {
	if result == nil {
		return "result payload is missing"
	}
	hasStream := strings.EqualFold(strings.TrimSpace(result.ArchiveTransfer), "stream")
	hasLegacy := strings.TrimSpace(result.ArchiveZipB64) != ""
	if !hasStream && !hasLegacy {
		if result.ArchiveManifestHash != "" || result.ArchiveSizeBytes > 0 {
			return "archive payload is missing"
		}
		return ""
	}
	root, err := os.MkdirTemp("", "apexload-library-preflight-*")
	if err != nil {
		return err.Error()
	}
	defer os.RemoveAll(root)
	dir := filepath.Join(root, "archive")
	if hasStream {
		if archiveImport == nil || strings.TrimSpace(archiveImport.ZipPath) == "" {
			return "archive payload is missing"
		}
		err = unzipArchiveFile(archiveImport.ZipPath, dir, libraryArchiveMaxBytes())
	} else {
		err = importLegacyArchiveZipB64(result.ArchiveZipB64, dir, libraryArchiveMaxBytes())
	}
	if err != nil {
		return err.Error()
	}
	manifestHash := hashArchiveManifestPath(filepath.Join(dir, "manifest.json"))
	sizeBytes := archiveDirectorySize(dir)
	if reason := validateLibraryArchiveMetadata(result, manifestHash, sizeBytes); reason != "" {
		return reason
	}
	return verifyImportedLibraryArchive(result, dir)
}

func (s *Server) importLibraryScript(item LibraryBundleItem, projectID string, remoteID string) (string, string, *LibraryConflict) {
	if item.Script == nil || (strings.TrimSpace(item.ProjectID) != "" && normalizeProjectID(item.Script.ProjectID) != normalizeProjectID(item.ProjectID)) {
		return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "script payload has inconsistent source project ownership"}
	}
	if strings.TrimSpace(item.ContentHash) == "" ||
		!libraryScriptHashMatches(item.Script, item.ContentHash) {
		return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "declared content hash does not match script payload"}
	}
	link := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
	localID := ""
	action := "created"
	var prior *StoredScript
	if link != nil {
		localID = link.LocalItemID
		if local, ok := s.runner.GetScript(localID); ok && local != nil {
			if normalizeProjectID(local.ProjectID) != projectID {
				return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "existing sync link belongs to a different destination project"}
			}
			localChanged := !libraryScriptHashMatches(local.Script, link.LocalContentHash)
			remoteChanged := !libraryScriptHashMatches(item.Script, link.RemoteContentHash)
			converged := libraryScriptHashesEquivalent(local.Script, item.Script) ||
				libraryScriptHashMatches(local.Script, item.ContentHash)
			if localChanged && remoteChanged && !converged {
				return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "local and remote script changed since last sync"}
			}
			prior = local
		}
		action = "updated"
	}
	script := cloneScriptForLibrary(item.Script)
	script.ProjectID = projectID
	// These fields identify the destination workspace, not portable content.
	// Updates restore them from the conditionally-observed local row below;
	// creates allocate fresh local metadata.
	script.RefKey = ""
	script.FolderID = nil
	script.CreatedAt = time.Time{}
	script.UpdatedAt = time.Time{}
	if localID == "" {
		var err error
		script.ID, err = deterministicLibraryImportID(remoteID, projectID, item)
		if err != nil {
			return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
	} else {
		script.ID = localID
	}
	var stored *StoredScript
	var err error
	if prior != nil {
		var updated bool
		stored, updated, err = s.runner.UpdateScriptDurableIfUnchanged(script, prior.Script)
		if err == nil && !updated {
			err = fmt.Errorf("local script changed concurrently after conflict preflight")
		}
	} else {
		stored, err = s.runner.CreateScriptDurable(script)
	}
	if err != nil || stored == nil {
		return "", "", &LibraryConflict{Kind: "script", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "failed to persist imported script: " + err.Error()}
	}
	if err := s.rememberLibraryLinkWithMutation(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, stored.ID, item.SourceItemID, hashLibraryScript(stored.Script), item.ContentHash, item.importMutationID); err != nil {
		slog.Warn("imported script but failed to persist library link", "script_id", stored.ID, "error", err)
		action += "_link_pending"
	}
	return stored.ID, action, nil
}

func (s *Server) importLibraryScenario(item LibraryBundleItem, projectID string, remoteID string, idMap map[string]string) (string, string, *LibraryConflict) {
	if item.Scenario == nil {
		return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "scenario payload is missing"}
	}
	if strings.TrimSpace(item.ContentHash) == "" || item.ContentHash != hashCanonical(item.Scenario.Request) {
		return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "declared content hash does not match scenario payload"}
	}
	for _, dependency := range item.Dependencies {
		if strings.TrimSpace(item.ProjectID) != "" && strings.TrimSpace(dependency.ProjectID) != "" && normalizeProjectID(dependency.ProjectID) != normalizeProjectID(item.ProjectID) {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "scenario dependency belongs to a different source project"}
		}
	}
	link := s.libraryLink(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
	localID := ""
	action := "created"
	var prior *StoredScenario
	if link != nil {
		localID = link.LocalItemID
		if local, ok := s.runner.GetScenario(localID); ok && local != nil {
			if normalizeProjectID(local.ProjectID) != projectID {
				return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "existing sync link belongs to a different destination project"}
			}
			localHash := hashLibraryScenario(local)
			if localHash != link.LocalContentHash && item.ContentHash != link.RemoteContentHash && localHash != item.ContentHash {
				return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: "local and remote scenario changed since last sync"}
			}
			prior = local
		}
		action = "updated"
	}
	req := cloneScenarioRequestForLibrary(item.Scenario.Request)
	req.creationActor = testCreationActor{
		Type:    "legacy",
		Subject: "controller:library-import-unattributed",
	}
	dependencies := make(map[string]LibraryDependency, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		if normalizeLibraryKind(dependency.Kind) == "script" && strings.TrimSpace(dependency.ID) != "" {
			dependencies[dependency.ID] = dependency
		}
	}
	for i := range req.Groups {
		sourceScriptID := req.Groups[i].ScriptID
		mappedDependency := false
		if mapped := idMap[sourceScriptID]; mapped != "" {
			req.Groups[i].ScriptID = mapped
			mappedDependency = true
		}
		stored, ok := s.runner.GetScript(req.Groups[i].ScriptID)
		if !ok || stored == nil {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "referenced script not found: " + req.Groups[i].ScriptID}
		}
		if normalizeProjectID(stored.ProjectID) != projectID {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "referenced script belongs to a different destination project"}
		}
		if dependency, ok := dependencies[sourceScriptID]; !mappedDependency && ok &&
			strings.TrimSpace(dependency.ContentHash) != "" &&
			!libraryScriptHashMatches(stored.Script, dependency.ContentHash) {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "referenced script content does not match dependency hash: " + sourceScriptID}
		}
	}
	if localID == "" {
		importID, err := deterministicLibraryImportID(remoteID, projectID, item)
		if err != nil {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		stored, err := s.runner.StoreScenarioWithIDCreateOnly(importID, req)
		if err != nil {
			return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		if err := s.rememberLibraryLinkWithMutation(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, stored.ID, item.SourceItemID, hashLibraryScenario(stored), item.ContentHash, item.importMutationID); err != nil {
			slog.Warn("imported scenario but failed to persist library link", "scenario_id", stored.ID, "error", err)
			action += "_link_pending"
		}
		return stored.ID, action, nil
	}
	var stored *StoredScenario
	var err error
	if prior != nil {
		stored, err = s.runner.UpdateScenarioIfUnchanged(localID, req, prior.UpdatedAt)
	} else {
		stored, err = s.runner.StoreScenarioWithIDCreateOnly(localID, req)
		action = "created"
	}
	if err != nil {
		return "", "", &LibraryConflict{Kind: "scenario", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: localID, Reason: err.Error()}
	}
	if err := s.rememberLibraryLinkWithMutation(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, stored.ID, item.SourceItemID, hashLibraryScenario(stored), item.ContentHash, item.importMutationID); err != nil {
		slog.Warn("imported scenario but failed to persist library link", "scenario_id", stored.ID, "error", err)
		action += "_link_pending"
	}
	return stored.ID, action, nil
}

func (s *Server) importLibraryResult(item LibraryBundleItem, projectID string, remoteID string, archiveImport *libraryArchiveImport) (string, string, *LibraryConflict) {
	return s.importLibraryResultTrackingArchive(item, projectID, "", "", remoteID, archiveImport, nil)
}

func (s *Server) importLibraryResultWithOutcome(item LibraryBundleItem, projectID string, remoteID string, archiveImport *libraryArchiveImport) (string, string, bool, *LibraryConflict) {
	return s.importLibraryResultWithDestinationOutcome(item, projectID, "", "", remoteID, archiveImport)
}

func (s *Server) importLibraryResultWithDestinationOutcome(item LibraryBundleItem, projectID string, applicationID string, releaseID string, remoteID string, archiveImport *libraryArchiveImport) (string, string, bool, *LibraryConflict) {
	archiveActivated := false
	localID, action, conflict := s.importLibraryResultTrackingArchive(item, projectID, applicationID, releaseID, remoteID, archiveImport, &archiveActivated)
	return localID, action, archiveActivated, conflict
}

func (s *Server) importLibraryResultTrackingArchive(item LibraryBundleItem, projectID string, applicationID string, releaseID string, remoteID string, archiveImport *libraryArchiveImport, archiveActivated *bool) (string, string, *LibraryConflict) {
	if archiveActivated != nil {
		*archiveActivated = false
	}
	if item.Result == nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "result payload is missing"}
	}
	if item.Result.Test == nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "result test payload is missing"}
	}
	if strings.TrimSpace(item.ProjectID) != "" && normalizeProjectID(item.Result.Test.ProjectID) != normalizeProjectID(item.ProjectID) {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "result payload has inconsistent source project ownership"}
	}
	contentHash := strings.TrimSpace(item.ContentHash)
	fullHash := hashLibraryResultWithSummary(item.Result.Test, item.Result.Summary)
	previousV1Hash := hashLibraryResultPreviousV1WithSummary(item.Result.Test, item.Result.Summary)
	legacyHashWithExpectedSummary := contentHash == hashLibraryResultTestOnly(item.Result.Test) && hashCanonical(item.Result.Summary) == hashCanonical(libraryResultSummary(item.Result.Test))
	hashVersion := strings.TrimSpace(item.ContentHashVersion)
	validHash := contentHash == fullHash
	switch hashVersion {
	case "", libraryResultHashVersionCurrent:
		// Unversioned bundles remain compatible only when they carry the current,
		// complete hash. Compact legacy hashes must opt into the explicit v1 gate.
	case libraryResultHashVersionLegacyV1:
		validHash = contentHash == previousV1Hash || legacyHashWithExpectedSummary
	default:
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "unsupported result content_hash_version " + hashVersion}
	}
	if contentHash == "" || !validHash {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "declared content hash does not match result payload"}
	}
	resultID := uuid.New().String()
	if item.Result.Test != nil {
		resultID = item.Result.Test.ID
	}
	importID, err := deterministicLibraryImportID(remoteID, projectID, item)
	if err != nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
	}
	archiveDir := ""
	finalArchiveDir := ""
	var archiveRecord *storage.RunArchiveRecord
	defer func() {
		if archiveDir != "" {
			_ = os.RemoveAll(archiveDir)
		}
	}()
	maskedTest := maskManagedTest(item.Result.Test)
	if maskedTest != nil {
		// Imported-result listings are evidence views, not executable archive
		// locators. The separately verified manifest hash/size/transfer fields are
		// sufficient and avoid serializing required storage path keys as empty
		// strings through storage.RunArchiveRecord.
		maskedTest.Archive = nil
	}
	rawResult := map[string]any{
		"schema_version":        libraryBundleSchemaVersion,
		"source_instance":       item.SourceInstanceID,
		"source_item_id":        item.SourceItemID,
		"content_hash":          item.ContentHash,
		"content_hash_version":  firstNonEmpty(hashVersion, libraryResultHashVersionCurrent),
		"test":                  maskedTest,
		"archive_transfer":      item.Result.ArchiveTransfer,
		"archive_manifest_hash": item.Result.ArchiveManifestHash,
		"archive_size_bytes":    item.Result.ArchiveSizeBytes,
	}
	if mutationID := strings.TrimSpace(item.importMutationID); mutationID != "" {
		rawResult[libraryResultMutationIDKey] = mutationID
	}
	if archiveImport != nil && strings.TrimSpace(archiveImport.ZipPath) != "" {
		finalDir := libraryImportedArchiveDir(importID)
		dir := finalDir + ".staging-" + uuid.NewString()
		archiveDir = dir
		finalArchiveDir = finalDir
		if err := unzipArchiveFile(archiveImport.ZipPath, dir, libraryArchiveMaxBytes()); err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		manifestHash := hashArchiveManifestPath(filepath.Join(dir, "manifest.json"))
		sizeBytes := archiveDirectorySize(dir)
		if reason := validateLibraryArchiveMetadata(item.Result, manifestHash, sizeBytes); reason != "" {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: reason}
		}
		if reason := verifyImportedLibraryArchive(item.Result, dir); reason != "" {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: reason}
		}
		rawResult["imported_archive_manifest_hash"] = manifestHash
		rawResult["imported_archive_size_bytes"] = sizeBytes
	} else if strings.TrimSpace(item.Result.ArchiveZipB64) != "" {
		finalDir := libraryImportedArchiveDir(importID)
		dir := finalDir + ".staging-" + uuid.NewString()
		archiveDir = dir
		finalArchiveDir = finalDir
		if err := importLegacyArchiveZipB64(item.Result.ArchiveZipB64, dir, libraryArchiveMaxBytes()); err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: err.Error()}
		}
		manifestHash := hashArchiveManifestPath(filepath.Join(dir, "manifest.json"))
		sizeBytes := archiveDirectorySize(dir)
		if reason := validateLibraryArchiveMetadata(item.Result, manifestHash, sizeBytes); reason != "" {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: reason}
		}
		if reason := verifyImportedLibraryArchive(item.Result, dir); reason != "" {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: reason}
		}
		rawResult["imported_archive_manifest_hash"] = manifestHash
		rawResult["imported_archive_size_bytes"] = sizeBytes
	} else if item.Result.ArchiveManifestHash != "" || item.Result.ArchiveSizeBytes > 0 {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "archive payload is missing"}
	}
	if archiveDir != "" {
		archiveRecord, err = buildImportedLibraryArchiveRecord(importID, archiveDir, finalArchiveDir, rawResult["imported_archive_manifest_hash"].(string))
		if err != nil {
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: "build imported archive record: " + err.Error()}
		}
	}
	imported := &storage.ExternalResultImport{
		ID:            importID,
		Provider:      "apexload",
		Name:          firstNonEmpty(strings.TrimSpace(item.Name), "APEX Load result import"),
		ProjectID:     projectID,
		ApplicationID: strings.TrimSpace(applicationID),
		ReleaseID:     strings.TrimSpace(releaseID),
		ScenarioID:    "",
		Source:        "library://" + item.SourceInstanceID + "/" + item.SourceItemID,
		Summary:       cloneAnyMap(item.Result.Summary),
		RawResult:     rawResult,
		ArchiveRecord: archiveRecord,
		CreatedAt:     time.Now().UTC(),
	}
	if imported.Summary == nil {
		imported.Summary = map[string]any{"status": "imported", "provider": "apexload"}
	}
	imported.Summary["provider"] = "apexload"
	created, existing, saveErr := s.createExternalResultImportIfAbsent(imported)
	if saveErr != nil {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, Reason: saveErr.Error()}
	}
	if !created && !equivalentExternalResultImport(existing, imported) {
		return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: importID, Reason: "immutable result evidence already exists with different content: " + externalResultImportDifference(existing, imported)}
	}
	if archiveDir != "" {
		activated, err := s.activateImmutableLibraryResultArchive(item.Result, archiveDir, finalArchiveDir, archiveRecord, created)
		if err != nil {
			if created {
				deleted, deleteErr := s.deleteExternalResultImportIfUnchanged(imported)
				if deleteErr != nil {
					err = fmt.Errorf("%w; rollback result evidence: %v", err, deleteErr)
				} else if !deleted {
					err = fmt.Errorf("%w; rollback result evidence lost its version fence", err)
				} else if archiveRecord != nil && archiveRecordBackend(archiveRecord) != archiveBackendFilesystem {
					if cleanupErr := deleteArchiveBackendObjects(context.Background(), archiveRecord); cleanupErr != nil {
						err = fmt.Errorf("%w; rollback imported archive objects: %v", err, cleanupErr)
					}
				}
			}
			return "", "", &LibraryConflict{Kind: "result", SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: importID, Reason: err.Error()}
		}
		if archiveActivated != nil {
			*archiveActivated = activated
		}
		archiveDir = ""
	}
	action := "unchanged"
	if created {
		action = "created"
	}
	if err := s.rememberLibraryLinkWithMutation(remoteID, projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, imported.ID, resultID, item.ContentHash, item.ContentHash, item.importMutationID); err != nil {
		slog.Warn("imported result but failed to persist library link", "import_id", imported.ID, "error", err)
		return imported.ID, action + "_link_pending", nil
	}
	return imported.ID, action, nil
}

func (s *Server) validateLibraryImportResultPlacement(req LibraryImportRequest, projectID string) (string, string, error) {
	applicationID := strings.TrimSpace(req.ApplicationID)
	releaseID := strings.TrimSpace(req.ReleaseID)
	if applicationID == "" && releaseID == "" {
		return "", "", nil
	}
	if applicationID == "" || releaseID == "" {
		return "", "", fmt.Errorf("application_id and release_id must be provided together for result imports")
	}
	if s.store != nil {
		releaseProjectID, _, releaseApplicationID, err := s.releaseContext(releaseID)
		if err != nil {
			return "", "", err
		}
		if normalizeProjectID(releaseProjectID) != normalizeProjectID(projectID) || strings.TrimSpace(releaseApplicationID) != applicationID {
			return "", "", fmt.Errorf("release_id does not belong to the destination application and project")
		}
	}
	return applicationID, releaseID, nil
}

func equivalentExternalResultImport(left, right *storage.ExternalResultImport) bool {
	if left == nil || right == nil {
		return left == right
	}
	return strings.EqualFold(strings.TrimSpace(left.Provider), strings.TrimSpace(right.Provider)) &&
		strings.TrimSpace(left.Name) == strings.TrimSpace(right.Name) &&
		normalizeProjectID(left.ProjectID) == normalizeProjectID(right.ProjectID) &&
		strings.TrimSpace(left.ApplicationID) == strings.TrimSpace(right.ApplicationID) &&
		strings.TrimSpace(left.ReleaseID) == strings.TrimSpace(right.ReleaseID) &&
		strings.TrimSpace(left.ScenarioID) == strings.TrimSpace(right.ScenarioID) &&
		strings.TrimSpace(left.Source) == strings.TrimSpace(right.Source) &&
		hashCanonical(canonicalJSONValue(left.Summary)) == hashCanonical(canonicalJSONValue(right.Summary)) &&
		hashCanonical(canonicalExternalResultRawEvidence(left.RawResult)) == hashCanonical(canonicalExternalResultRawEvidence(right.RawResult)) &&
		hashCanonical(left.ArchiveRecord) == hashCanonical(right.ArchiveRecord)
}

func externalResultImportDifference(left, right *storage.ExternalResultImport) string {
	if left == nil || right == nil {
		return "one evidence row is missing"
	}
	switch {
	case !strings.EqualFold(strings.TrimSpace(left.Provider), strings.TrimSpace(right.Provider)):
		return "provider differs"
	case strings.TrimSpace(left.Name) != strings.TrimSpace(right.Name):
		return "name differs"
	case normalizeProjectID(left.ProjectID) != normalizeProjectID(right.ProjectID):
		return "project differs"
	case strings.TrimSpace(left.ApplicationID) != strings.TrimSpace(right.ApplicationID):
		return "application differs"
	case strings.TrimSpace(left.ReleaseID) != strings.TrimSpace(right.ReleaseID):
		return "release differs"
	case strings.TrimSpace(left.ScenarioID) != strings.TrimSpace(right.ScenarioID):
		return "scenario differs"
	case strings.TrimSpace(left.Source) != strings.TrimSpace(right.Source):
		return "source differs"
	case hashCanonical(canonicalJSONValue(left.Summary)) != hashCanonical(canonicalJSONValue(right.Summary)):
		return "summary differs"
	case hashCanonical(canonicalExternalResultRawEvidence(left.RawResult)) != hashCanonical(canonicalExternalResultRawEvidence(right.RawResult)):
		return "raw evidence differs"
	case hashCanonical(left.ArchiveRecord) != hashCanonical(right.ArchiveRecord):
		return "archive storage metadata differs"
	default:
		return "evidence differs"
	}
}

func canonicalExternalResultRawEvidence(raw map[string]any) any {
	cloned := cloneExternalResultJSONMap(raw)
	delete(cloned, libraryResultMutationIDKey)
	return canonicalJSONValue(cloned)
}

// activateImmutableLibraryResultArchive installs a verified archive through
// the configured archive backend. Filesystem deployments retain the atomic
// rename behavior; object-backed deployments publish artifacts first and the
// manifest commit marker last through the shared archive abstraction.
func (s *Server) activateImmutableLibraryResultArchive(result *LibraryResultPayload, stagedDir string, finalDir string, record *storage.RunArchiveRecord, created bool) (bool, error) {
	s.libraryArchiveImportMu.Lock()
	defer s.libraryArchiveImportMu.Unlock()
	if record == nil {
		return false, fmt.Errorf("imported archive record is required")
	}
	if archiveRecordBackend(record) != archiveBackendFilesystem {
		publication := *record
		publication.ArchivePath = stagedDir
		publication.ManifestPath = filepath.Join(stagedDir, runArchiveManifestFileName)
		if err := publishRunArchive(context.Background(), &publication); err != nil {
			return false, fmt.Errorf("publish imported archive: %w", err)
		}
		if err := os.RemoveAll(stagedDir); err != nil {
			return false, fmt.Errorf("remove imported archive staging directory: %w", err)
		}
		return created, nil
	}
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o750); err != nil {
		return false, err
	}
	if _, err := os.Stat(finalDir); err == nil {
		if reason := validateExistingLibraryResultArchive(result, finalDir); reason != "" {
			return false, fmt.Errorf("immutable result archive already exists with different content: %s", reason)
		}
		return false, os.RemoveAll(stagedDir)
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect prior imported archive: %w", err)
	}
	if err := os.Rename(stagedDir, finalDir); err != nil {
		// Another replica may have won the filesystem race after the stat.
		// Accept only an independently verified identical winner.
		if _, statErr := os.Stat(finalDir); statErr == nil {
			if reason := validateExistingLibraryResultArchive(result, finalDir); reason == "" {
				_ = os.RemoveAll(stagedDir)
				return false, nil
			}
		}
		return false, fmt.Errorf("activate imported archive: %w", err)
	}
	return true, nil
}

func validateExistingLibraryResultArchive(result *LibraryResultPayload, dir string) string {
	manifestHash := hashArchiveManifestPath(filepath.Join(dir, "manifest.json"))
	sizeBytes := archiveDirectorySize(dir)
	if reason := validateLibraryArchiveMetadata(result, manifestHash, sizeBytes); reason != "" {
		return reason
	}
	return verifyImportedLibraryArchive(result, dir)
}

func deterministicLibraryImportID(remoteID string, projectID string, item LibraryBundleItem) (string, error) {
	kind := normalizeLibraryKind(item.Kind)
	sourceInstanceID := strings.TrimSpace(item.SourceInstanceID)
	sourceItemID := strings.TrimSpace(item.SourceItemID)
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	if kind == "" || sourceInstanceID == "" || sourceItemID == "" || projectID == "" {
		return "", fmt.Errorf("library import identity requires kind, source_instance_id, source_item_id, and destination project_id")
	}
	identity, err := json.Marshal(struct {
		RemoteID         string `json:"remote_id"`
		ProjectID        string `json:"project_id"`
		Kind             string `json:"kind"`
		SourceInstanceID string `json:"source_instance_id"`
		SourceItemID     string `json:"source_item_id"`
	}{firstNonEmpty(strings.TrimSpace(remoteID), "direct"), projectID, kind, sourceInstanceID, sourceItemID})
	if err != nil {
		return "", fmt.Errorf("encode library import identity: %w", err)
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, identity).String(), nil
}

func validateLibraryArchiveMetadata(result *LibraryResultPayload, manifestHash string, sizeBytes int64) string {
	if result == nil {
		return "archive metadata is missing"
	}
	if strings.TrimSpace(result.ArchiveManifestHash) == "" {
		return "archive_manifest_hash is required for verifiable archive imports"
	}
	declaredManifestHash, err := normalizeLibraryArchiveManifestHash(result.ArchiveManifestHash)
	if err != nil {
		return err.Error()
	}
	actualManifestHash, err := normalizeLibraryArchiveManifestHash(manifestHash)
	if err != nil || actualManifestHash != declaredManifestHash {
		return "archive manifest hash does not match bundle metadata"
	}
	if result.ArchiveSizeBytes <= 0 {
		return "archive_size_bytes is required for verifiable archive imports"
	}
	if sizeBytes != result.ArchiveSizeBytes {
		return "archive size does not match bundle metadata"
	}
	return ""
}

func normalizeLibraryArchiveManifestHash(raw string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(raw), ":", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "sha256") || len(parts[1]) != 64 {
		return "", fmt.Errorf("archive_manifest_hash must use the sha256:<64 hex characters> format")
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", fmt.Errorf("archive_manifest_hash must use the sha256:<64 hex characters> format")
	}
	return "sha256:" + strings.ToLower(parts[1]), nil
}

func verifyImportedLibraryArchive(result *LibraryResultPayload, archiveDir string) string {
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	manifest, err := loadRunArchiveManifest(manifestPath)
	if err != nil {
		return "archive manifest is invalid or missing: " + err.Error()
	}
	if len(manifest.Artifacts) == 0 {
		return "archive manifest contains no per-artifact SHA-256 entries"
	}
	recorded := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		name, err := normalizeArchiveArtifactName(artifact.Name)
		if err != nil {
			return "archive manifest contains an invalid artifact entry: " + err.Error()
		}
		if strings.TrimSpace(artifact.SHA256) == "" {
			return "archive manifest artifact " + name + " has no SHA-256"
		}
		if _, duplicate := recorded[name]; duplicate {
			return "archive manifest contains duplicate artifact entry " + name
		}
		recorded[name] = struct{}{}
	}
	referenced, err := archiveManifestArtifactNames(manifest)
	if err != nil {
		return "archive manifest artifact list is invalid: " + err.Error()
	}
	for name := range referenced {
		if name == runArchiveManifestFileName {
			continue
		}
		if _, ok := recorded[name]; !ok {
			return "archive manifest does not provide a SHA-256 entry for " + name
		}
	}
	files, err := importedArchiveRegularFiles(archiveDir)
	if err != nil {
		return "archive contents are invalid: " + err.Error()
	}
	for _, name := range files {
		if name == runArchiveManifestFileName || name == archiveMaterializationCacheMarkerSuffix {
			continue
		}
		if _, ok := recorded[name]; !ok {
			return "archive contains undeclared or unhashed artifact " + name
		}
	}
	expectedManifestHash, err := normalizeLibraryArchiveManifestHash(result.ArchiveManifestHash)
	if err != nil {
		return err.Error()
	}
	expectedManifestHash = strings.TrimPrefix(expectedManifestHash, "sha256:")
	report := verifyRunArchiveIntegrity(&storage.RunArchiveRecord{
		ArchivePath:    archiveDir,
		ManifestPath:   manifestPath,
		ManifestSHA256: expectedManifestHash,
	}, manifest)
	if !report.Verified {
		return "archive artifact integrity verification failed: " + report.Summary
	}
	return ""
}

func importedArchiveRegularFiles(archiveDir string) ([]string, error) {
	root, err := openArchiveDirectoryNoSymlink(archiveDir)
	if err != nil {
		return nil, err
	}
	files, listErr := importedArchiveRegularFilesAt(root, "")
	closeErr := root.Close()
	if listErr != nil {
		return nil, listErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return files, nil
}

func importedArchiveRegularFilesAt(root *os.Root, prefix string) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		info, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		archiveName := name
		if prefix != "" {
			archiveName = prefix + "/" + name
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("archive contains symbolic link %s", archiveName)
		}
		if info.IsDir() {
			child, err := openArchiveChildDirectoryAt(root, name, false, 0)
			if err != nil {
				return nil, err
			}
			childFiles, listErr := importedArchiveRegularFilesAt(child, archiveName)
			childCloseErr := child.Close()
			if listErr != nil {
				return nil, listErr
			}
			if childCloseErr != nil {
				return nil, childCloseErr
			}
			files = append(files, childFiles...)
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("archive contains non-regular artifact %s", archiveName)
		}
		files = append(files, archiveName)
	}
	return files, nil
}

func cloneScenarioRequestForLibrary(req CreateScenarioRequest) CreateScenarioRequest {
	payload, err := json.Marshal(req)
	if err != nil {
		return req
	}
	var cloned CreateScenarioRequest
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return req
	}
	return cloned
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
		dep := LibraryDependency{Kind: "script", ID: ref.ScriptID, Name: ref.ScriptName, ProjectID: normalizeProjectID(scenario.ProjectID)}
		if stored, ok := s.runner.GetScript(ref.ScriptID); ok && stored != nil && normalizeProjectID(stored.ProjectID) == normalizeProjectID(scenario.ProjectID) && stored.Script != nil && normalizeProjectID(stored.Script.ProjectID) == normalizeProjectID(scenario.ProjectID) {
			dep.Name = firstNonEmpty(dep.Name, disclosureSafeScriptName(stored.Name, stored.Script))
			dep.ContentHash = hashLibraryScript(stored.Script)
		}
		deps = append(deps, dep)
	}
	return deps
}

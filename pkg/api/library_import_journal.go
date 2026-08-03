package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type libraryImportUndo func() error

// libraryImportJournal records one dependency-ordered compensation per item.
// Link ownership is always restored or verified first; an asset is never
// removed after a peer replica has replaced the importing mutation's link.
type libraryImportJournal struct {
	server *Server
	undo   []libraryImportUndo
}

func newLibraryImportJournal(server *Server) *libraryImportJournal {
	return &libraryImportJournal{server: server}
}

func (j *libraryImportJournal) add(undo libraryImportUndo) {
	if j != nil && undo != nil {
		j.undo = append(j.undo, undo)
	}
}

func (j *libraryImportJournal) rollback() error {
	if j == nil {
		return nil
	}
	var errs []error
	for i := len(j.undo) - 1; i >= 0; i-- {
		if err := j.undo[i](); err != nil {
			errs = append(errs, err)
		}
	}
	j.undo = nil
	return errors.Join(errs...)
}

func (j *libraryImportJournal) recordItem(remoteID, projectID, kind, sourceInstanceID, sourceItemID string, beforeLink, ownedAfterLink *librarySyncLink, compensateAsset libraryImportUndo) {
	if j == nil || j.server == nil {
		return
	}
	beforeLink = cloneLibrarySyncLink(beforeLink)
	ownedAfterLink = cloneLibrarySyncLink(ownedAfterLink)
	j.add(func() error {
		if ownedAfterLink != nil {
			restored, err := j.server.restoreLibraryLinkIfCurrent(remoteID, projectID, kind, sourceInstanceID, sourceItemID, ownedAfterLink, beforeLink)
			if err != nil {
				return fmt.Errorf("rollback %s synchronization link %s: %w", normalizeLibraryKind(kind), sourceItemID, err)
			}
			if !restored {
				return fmt.Errorf("rollback %s synchronization link %s lost its ownership fence; asset compensation skipped", normalizeLibraryKind(kind), sourceItemID)
			}
		} else {
			unchanged, err := j.server.durableLibraryLinkEquals(remoteID, projectID, kind, sourceInstanceID, sourceItemID, beforeLink)
			if err != nil {
				return fmt.Errorf("verify %s synchronization link %s before rollback: %w", normalizeLibraryKind(kind), sourceItemID, err)
			}
			if !unchanged {
				return fmt.Errorf("rollback %s synchronization link %s was claimed by another mutation; asset compensation skipped", normalizeLibraryKind(kind), sourceItemID)
			}
		}
		if compensateAsset != nil {
			return compensateAsset()
		}
		return nil
	})
}

func (j *libraryImportJournal) recordScript(before, after *StoredScript, remoteID, projectID, kind, sourceInstanceID, sourceItemID string, beforeLink, ownedAfterLink *librarySyncLink) {
	if j == nil || j.server == nil || after == nil || after.Script == nil {
		return
	}
	after = cloneStoredScriptForRead(after)
	var compensate libraryImportUndo
	if before == nil {
		compensate = func() error {
			deleted, err := j.server.runner.DeleteScriptDurableIfUnchanged(after.ID, after.Script.UpdatedAt)
			if err != nil {
				return fmt.Errorf("rollback created script %s: %w", after.ID, err)
			}
			if !deleted {
				return fmt.Errorf("rollback created script %s lost its version fence", after.ID)
			}
			return nil
		}
	} else {
		before = cloneStoredScriptForRead(before)
		compensate = func() error {
			restored := cloneScriptDefinition(before.Script)
			_, updated, err := j.server.runner.UpdateScriptDurableIfUnchanged(restored, after.Script)
			if err != nil {
				return fmt.Errorf("rollback updated script %s: %w", after.ID, err)
			}
			if !updated {
				return fmt.Errorf("rollback updated script %s lost its version fence", after.ID)
			}
			return nil
		}
	}
	j.recordItem(remoteID, projectID, kind, sourceInstanceID, sourceItemID, beforeLink, ownedAfterLink, compensate)
}

func cloneStoredScriptForRead(script *StoredScript) *StoredScript {
	if script == nil {
		return nil
	}
	copy := *script
	copy.FolderID = cloneStringPtr(script.FolderID)
	copy.Script = cloneScriptDefinition(script.Script)
	return &copy
}

func (j *libraryImportJournal) recordScenario(before, after *StoredScenario, remoteID, projectID, kind, sourceInstanceID, sourceItemID string, beforeLink, ownedAfterLink *librarySyncLink) {
	if j == nil || j.server == nil || after == nil {
		return
	}
	after = cloneStoredScenarioForRead(after)
	var compensate libraryImportUndo
	if before == nil {
		compensate = func() error {
			deleted, err := j.server.runner.DeleteScenarioIfUnchanged(after.ID, after.UpdatedAt)
			if err != nil {
				return fmt.Errorf("rollback created scenario %s: %w", after.ID, err)
			}
			if !deleted {
				return fmt.Errorf("rollback created scenario %s lost its version fence", after.ID)
			}
			return nil
		}
	} else {
		before = cloneStoredScenarioForRead(before)
		compensate = func() error {
			_, err := j.server.runner.UpdateScenarioIfUnchanged(before.ID, createScenarioRequestFromStored(before), after.UpdatedAt)
			if err != nil {
				return fmt.Errorf("rollback updated scenario %s: %w", after.ID, err)
			}
			return nil
		}
	}
	j.recordItem(remoteID, projectID, kind, sourceInstanceID, sourceItemID, beforeLink, ownedAfterLink, compensate)
}

func cloneStoredScenarioForRead(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	return cloneStoredScenarioForReadLocked(scenario)
}

func (j *libraryImportJournal) recordResult(created bool, after *storage.ExternalResultImport, archiveActivated bool, archivePath string, remoteID, projectID, kind, sourceInstanceID, sourceItemID string, beforeLink, ownedAfterLink *librarySyncLink) {
	if j == nil || j.server == nil || after == nil {
		return
	}
	afterCopy := cloneExternalResultImport(after)
	archiveActivated = archiveActivated && (strings.TrimSpace(archivePath) != "" || afterCopy.ArchiveRecord != nil)
	compensate := func() error {
		if created {
			deleted, err := j.server.deleteExternalResultImportIfUnchanged(afterCopy)
			if err != nil {
				return fmt.Errorf("rollback created result %s: %w", afterCopy.ID, err)
			}
			if !deleted {
				return fmt.Errorf("rollback created result %s lost its version fence", afterCopy.ID)
			}
		}
		if archiveActivated {
			if afterCopy.ArchiveRecord != nil && archiveRecordBackend(afterCopy.ArchiveRecord) != archiveBackendFilesystem {
				if err := deleteArchiveBackendObjects(context.Background(), afterCopy.ArchiveRecord); err != nil {
					return fmt.Errorf("rollback imported result archive %s: %w", afterCopy.ID, err)
				}
			} else if err := os.RemoveAll(archivePath); err != nil {
				return fmt.Errorf("rollback imported result archive %s: %w", afterCopy.ID, err)
			}
		}
		return nil
	}
	j.recordItem(remoteID, projectID, kind, sourceInstanceID, sourceItemID, beforeLink, ownedAfterLink, compensate)
}

func cloneExternalResultImport(result *storage.ExternalResultImport) *storage.ExternalResultImport {
	if result == nil {
		return nil
	}
	copy := *result
	copy.Summary = cloneExternalResultJSONMap(result.Summary)
	copy.RawResult = cloneExternalResultJSONMap(result.RawResult)
	if result.ArchiveRecord != nil {
		archiveRecord := *result.ArchiveRecord
		copy.ArchiveRecord = &archiveRecord
	}
	return &copy
}

func cloneExternalResultJSONMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	payload, err := json.Marshal(input)
	if err == nil {
		var cloned map[string]any
		if err := json.Unmarshal(payload, &cloned); err == nil {
			return cloned
		}
	}
	// Persistence will reject non-JSON evidence later; preserve the caller's
	// values here while still isolating the top-level map on that error path.
	return cloneAnyMap(input)
}

func cloneLibrarySyncLink(link *librarySyncLink) *librarySyncLink {
	if link == nil {
		return nil
	}
	copy := *link
	return &copy
}

func librarySyncLinksEqual(left, right *librarySyncLink) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func (s *Server) refreshLibraryLinksForImport() {
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	s.loadLibraryLinksState()
}

func (s *Server) exactLibraryLink(remoteID, projectID, kind, sourceInstanceID, sourceItemID string) *librarySyncLink {
	if strings.TrimSpace(remoteID) == "" {
		remoteID = "direct"
	}
	key := libraryLinkKey(remoteID, projectID, kind, sourceInstanceID, sourceItemID)
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	return cloneLibrarySyncLink(s.libraryLinks[key])
}

func (s *Server) durableLibraryLinkEquals(remoteID, projectID, kind, sourceInstanceID, sourceItemID string, expected *librarySyncLink) (bool, error) {
	if strings.TrimSpace(remoteID) == "" {
		remoteID = "direct"
	}
	key := libraryLinkKey(remoteID, projectID, kind, sourceInstanceID, sourceItemID)
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	if s.store == nil {
		s.libraryMu.Lock()
		current := cloneLibrarySyncLink(s.libraryLinks[key])
		s.libraryMu.Unlock()
		return librarySyncLinksEqual(current, expected), nil
	}
	raw, err := s.store.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		return false, err
	}
	var links []librarySyncLink
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &links); err != nil {
			return false, fmt.Errorf("decode durable library links: %w", err)
		}
	}
	var current *librarySyncLink
	for i := range links {
		link := &links[i]
		if libraryLinkKey(link.RemoteID, link.ProjectID, link.Kind, link.SourceInstanceID, link.SourceItemID) == key {
			current = link
			break
		}
	}
	return librarySyncLinksEqual(current, expected), nil
}

// restoreLibraryLinkIfCurrent conditionally compensates one link while merging
// unrelated links written by other replicas through the existing settings CAS.
func (s *Server) restoreLibraryLinkIfCurrent(remoteID, projectID, kind, sourceInstanceID, sourceItemID string, expected, replacement *librarySyncLink) (bool, error) {
	if strings.TrimSpace(remoteID) == "" {
		remoteID = "direct"
	}
	key := libraryLinkKey(remoteID, projectID, kind, sourceInstanceID, sourceItemID)
	s.libraryStateMutationMu.Lock()
	defer s.libraryStateMutationMu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		s.loadLibraryLinksState()
		s.libraryMu.Lock()
		current := cloneLibrarySyncLink(s.libraryLinks[key])
		if !librarySyncLinksEqual(current, expected) {
			s.libraryMu.Unlock()
			return false, nil
		}
		if replacement == nil {
			delete(s.libraryLinks, key)
		} else {
			s.libraryLinks[key] = cloneLibrarySyncLink(replacement)
		}
		s.libraryMu.Unlock()
		if err := s.persistLibraryLinks(); err == nil {
			return true, nil
		} else {
			lastErr = err
		}
	}
	s.loadLibraryLinksState()
	return false, lastErr
}

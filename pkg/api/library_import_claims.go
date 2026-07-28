package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	libraryImportClaimsSettingKey  = "library.import_claims.v1"
	maxLibraryImportClaims         = 10_000
	maxLibraryImportClaimsRawBytes = 64 << 20
	libraryImportClaimCASRetries   = 8
)

var (
	libraryImportClaimsMu     sync.Mutex
	memoryLibraryImportClaims = make(map[*Server][]libraryImportClaim)
	errLibraryImportClaimBusy = errors.New("library import identity is already being mutated")
)

type libraryImportClaim struct {
	Identity         string                        `json:"identity"`
	OwnerID          string                        `json:"owner_id"`
	Sequence         int                           `json:"sequence"`
	RemoteID         string                        `json:"remote_id"`
	ProjectID        string                        `json:"project_id"`
	Kind             string                        `json:"kind"`
	SourceInstanceID string                        `json:"source_instance_id"`
	SourceItemID     string                        `json:"source_item_id"`
	MutationID       string                        `json:"mutation_id"`
	LocalItemID      string                        `json:"local_item_id,omitempty"`
	IntendedHash     string                        `json:"intended_hash"`
	BeforeLink       *librarySyncLink              `json:"before_link,omitempty"`
	BeforeScript     *StoredScript                 `json:"before_script,omitempty"`
	BeforeScenario   *StoredScenario               `json:"before_scenario,omitempty"`
	BeforeResult     *storage.ExternalResultImport `json:"before_result,omitempty"`
	ArchiveExpected  bool                          `json:"archive_expected,omitempty"`
	ArchiveExisted   bool                          `json:"archive_existed,omitempty"`
	ArchiveActivated bool                          `json:"archive_activated,omitempty"`
	ArchivePath      string                        `json:"archive_path,omitempty"`
	ArchiveManifest  string                        `json:"archive_manifest,omitempty"`
	ArchiveSize      int64                         `json:"archive_size,omitempty"`
	Prepared         bool                          `json:"prepared,omitempty"`
	Applied          bool                          `json:"applied,omitempty"`
	Committed        bool                          `json:"committed,omitempty"`
	CreatedAt        time.Time                     `json:"created_at"`
	ExpiresAt        time.Time                     `json:"expires_at"`
}

type libraryImportClaimHandle struct {
	server *Server
	owner  string
	ttl    time.Duration
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once

	mu       sync.RWMutex
	claims   map[string]libraryImportClaim
	identity map[string]string
}

func libraryImportClaimTTL() time.Duration {
	const defaultTTL = 5 * time.Minute
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL"))
	if raw == "" {
		return defaultTTL
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < 100*time.Millisecond || parsed > time.Hour {
		return defaultTTL
	}
	return parsed
}

func libraryImportIdentity(remoteID, projectID string, item LibraryBundleItem) string {
	return libraryLinkKey(firstNonEmpty(strings.TrimSpace(remoteID), "direct"), normalizeProjectID(projectID), item.Kind, item.SourceInstanceID, item.SourceItemID)
}

func (s *Server) acquireLibraryImportClaims(bundle LibraryBundle, remoteID, projectID string) (*libraryImportClaimHandle, error) {
	if len(bundle.Items) == 0 {
		return &libraryImportClaimHandle{server: s, claims: map[string]libraryImportClaim{}, identity: map[string]string{}}, nil
	}
	if err := s.recoverExpiredLibraryImportClaims(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	ttl := libraryImportClaimTTL()
	owner := uuid.NewString()
	claims := make([]libraryImportClaim, 0, len(bundle.Items))
	seen := make(map[string]struct{}, len(bundle.Items))
	orderedItems := make([]LibraryBundleItem, 0, len(bundle.Items))
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) == "script" {
			orderedItems = append(orderedItems, item)
		}
	}
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) != "script" {
			orderedItems = append(orderedItems, item)
		}
	}
	for sequence, item := range orderedItems {
		identity := libraryImportIdentity(remoteID, projectID, item)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("duplicate library import identity for %s %s", normalizeLibraryKind(item.Kind), strings.TrimSpace(item.SourceItemID))
		}
		seen[identity] = struct{}{}
		claims = append(claims, libraryImportClaim{
			Identity: identity, OwnerID: owner, Sequence: sequence, RemoteID: firstNonEmpty(strings.TrimSpace(remoteID), "direct"),
			ProjectID: normalizeProjectID(projectID), Kind: normalizeLibraryKind(item.Kind),
			SourceInstanceID: strings.TrimSpace(item.SourceInstanceID), SourceItemID: strings.TrimSpace(item.SourceItemID),
			MutationID: uuid.NewString(), IntendedHash: strings.TrimSpace(item.ContentHash), CreatedAt: now, ExpiresAt: now.Add(ttl),
		})
	}
	err := s.mutateLibraryImportClaims(func(existing *[]libraryImportClaim) (bool, error) {
		active := make(map[string]libraryImportClaim, len(*existing))
		for _, claim := range *existing {
			if claim.Committed {
				continue
			}
			active[claim.Identity] = claim
		}
		for _, claim := range claims {
			if prior, found := active[claim.Identity]; found {
				return false, fmt.Errorf("%w: %s %s is held until %s", errLibraryImportClaimBusy, claim.Kind, claim.SourceItemID, prior.ExpiresAt.UTC().Format(time.RFC3339))
			}
		}
		if len(active)+len(claims) > maxLibraryImportClaims {
			return false, fmt.Errorf("library import claim limit of %d reached", maxLibraryImportClaims)
		}
		retained := (*existing)[:0]
		for _, claim := range *existing {
			if !claim.Committed {
				retained = append(retained, claim)
			}
		}
		*existing = append(retained, claims...)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	handle := &libraryImportClaimHandle{
		server: s, owner: owner, ttl: ttl, stop: make(chan struct{}), done: make(chan struct{}),
		claims: make(map[string]libraryImportClaim, len(claims)), identity: make(map[string]string, len(claims)),
	}
	for _, claim := range claims {
		handle.claims[claim.Identity] = claim
		handle.identity[claim.Kind+"\x00"+claim.SourceInstanceID+"\x00"+claim.SourceItemID] = claim.Identity
	}
	go handle.heartbeat()
	return handle, nil
}

func (h *libraryImportClaimHandle) key(item LibraryBundleItem) string {
	if h == nil {
		return ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.identity[normalizeLibraryKind(item.Kind)+"\x00"+strings.TrimSpace(item.SourceInstanceID)+"\x00"+strings.TrimSpace(item.SourceItemID)]
}

func (h *libraryImportClaimHandle) mutationID(item LibraryBundleItem) string {
	key := h.key(item)
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.claims[key].MutationID
}

func (h *libraryImportClaimHandle) claimCount() int {
	if h == nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.claims)
}

func (h *libraryImportClaimHandle) prepare(item LibraryBundleItem, localItemID string, beforeLink *librarySyncLink, beforeScript *StoredScript, beforeScenario *StoredScenario, beforeResult *storage.ExternalResultImport, archiveExpected, archiveExisted bool, archivePath string, intendedHashOverride ...string) error {
	if h == nil || h.server == nil {
		return nil
	}
	key := h.key(item)
	return h.update(key, func(claim *libraryImportClaim) {
		claim.LocalItemID = strings.TrimSpace(localItemID)
		claim.BeforeLink = cloneLibrarySyncLink(beforeLink)
		// Claims live in shared settings and must never become a second secret
		// store. Rollback write paths reconcile redaction sentinels against the
		// still-fenced current asset, preserving values without persisting them
		// in the recovery journal.
		claim.BeforeScript = maskStoredScript(beforeScript)
		claim.BeforeScenario = maskStoredScenario(cloneStoredScenarioForRead(beforeScenario))
		claim.BeforeResult = cloneExternalResultImport(beforeResult)
		claim.ArchiveExpected = archiveExpected
		claim.ArchiveExisted = archiveExisted
		claim.ArchivePath = strings.TrimSpace(archivePath)
		if item.Result != nil {
			claim.ArchiveManifest = strings.TrimSpace(item.Result.ArchiveManifestHash)
			claim.ArchiveSize = item.Result.ArchiveSizeBytes
		}
		if len(intendedHashOverride) > 0 && strings.TrimSpace(intendedHashOverride[0]) != "" {
			claim.IntendedHash = strings.TrimSpace(intendedHashOverride[0])
		}
		claim.Prepared = true
	})
}

func (h *libraryImportClaimHandle) markApplied(item LibraryBundleItem, archiveActivated bool) error {
	if h == nil || h.server == nil {
		return nil
	}
	return h.update(h.key(item), func(claim *libraryImportClaim) {
		claim.Applied = true
		claim.ArchiveActivated = archiveActivated
	})
}

func (h *libraryImportClaimHandle) update(identity string, mutate func(*libraryImportClaim)) error {
	if strings.TrimSpace(identity) == "" {
		return fmt.Errorf("library import claim identity is missing")
	}
	now := time.Now().UTC()
	expectedClaims := h.claimCount()
	err := h.server.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		owned := 0
		var target *libraryImportClaim
		for index := range *claims {
			claim := &(*claims)[index]
			if claim.OwnerID != h.owner || claim.Committed {
				continue
			}
			owned++
			if claim.Identity == identity {
				target = claim
			}
		}
		if owned != expectedClaims || target == nil {
			return false, h.ownerFenceError(identity)
		}
		mutate(target)
		for index := range *claims {
			claim := &(*claims)[index]
			if claim.OwnerID == h.owner && !claim.Committed {
				// All item claims form one transaction lease. Renewing only the
				// checkpointed item could let a replacement replica take over the
				// rest of an import that is still actively progressing.
				claim.ExpiresAt = now.Add(h.ttl)
			}
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	h.mu.Lock()
	for key, claim := range h.claims {
		if key == identity {
			mutate(&claim)
		}
		claim.ExpiresAt = now.Add(h.ttl)
		h.claims[key] = claim
	}
	h.mu.Unlock()
	return nil
}

func (h *libraryImportClaimHandle) ownerFenceError(identity string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return fmt.Errorf("library import transaction %s lost its owner fence", h.owner)
	}
	return fmt.Errorf("library import claim %s lost its owner fence", identity)
}

func (h *libraryImportClaimHandle) heartbeat() {
	if h == nil || h.server == nil || h.stop == nil {
		return
	}
	defer close(h.done)
	interval := h.ttl / 3
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			expectedClaims := h.claimCount()
			if err := h.server.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
				owned := 0
				for index := range *claims {
					if (*claims)[index].OwnerID == h.owner && !(*claims)[index].Committed {
						owned++
						(*claims)[index].ExpiresAt = now.Add(h.ttl)
					}
				}
				if owned != expectedClaims {
					return false, h.ownerFenceError("")
				}
				return owned > 0, nil
			}); err != nil {
				slog.Warn("failed to renew library import claims", "owner_id", h.owner, "error", err)
			}
		}
	}
}

func (h *libraryImportClaimHandle) stopHeartbeat() {
	if h == nil || h.stop == nil {
		return
	}
	h.once.Do(func() { close(h.stop) })
	if h.done != nil {
		<-h.done
	}
}

// complete is the durable commit/abort marker. Once Committed is persisted,
// restart reconciliation may remove the records but must never compensate.
func (h *libraryImportClaimHandle) complete() error {
	if h == nil || h.server == nil || strings.TrimSpace(h.owner) == "" {
		return nil
	}
	h.stopHeartbeat()
	expectedClaims := h.claimCount()
	if err := h.server.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		owned := 0
		for index := range *claims {
			if (*claims)[index].OwnerID == h.owner {
				owned++
				(*claims)[index].Committed = true
			}
		}
		if owned != expectedClaims {
			return false, h.ownerFenceError("")
		}
		return owned > 0, nil
	}); err != nil {
		return err
	}
	// Cleanup is best effort after the durable terminal marker.
	if err := h.server.removeLibraryImportClaims(h.owner); err != nil {
		slog.Warn("committed library import claims remain for cleanup", "owner_id", h.owner, "error", err)
	}
	return nil
}

// abandon stops an unsuccessful import without destroying its recovery
// evidence. Expiring the still-uncommitted lease makes the transaction
// immediately eligible for safe compensation by this or another replica.
func (h *libraryImportClaimHandle) abandon() error {
	if h == nil || h.server == nil || strings.TrimSpace(h.owner) == "" {
		return nil
	}
	h.stopHeartbeat()
	now := time.Now().UTC()
	expectedClaims := h.claimCount()
	return h.server.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		owned := 0
		for index := range *claims {
			if (*claims)[index].OwnerID == h.owner && !(*claims)[index].Committed {
				owned++
				(*claims)[index].ExpiresAt = now
			}
		}
		if owned != expectedClaims {
			return false, h.ownerFenceError("")
		}
		return owned > 0, nil
	})
}

func (s *Server) removeLibraryImportClaims(owner string) error {
	return s.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		filtered := (*claims)[:0]
		changed := false
		for _, claim := range *claims {
			if claim.OwnerID == owner {
				changed = true
				continue
			}
			filtered = append(filtered, claim)
		}
		*claims = filtered
		return changed, nil
	})
}

func (s *Server) expireLibraryImportClaims(owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil
	}
	now := time.Now().UTC()
	return s.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		changed := false
		for index := range *claims {
			if (*claims)[index].OwnerID == owner && !(*claims)[index].Committed {
				(*claims)[index].ExpiresAt = now
				changed = true
			}
		}
		return changed, nil
	})
}

func (s *Server) mutateLibraryImportClaims(mutate func(*[]libraryImportClaim) (bool, error)) error {
	if s == nil {
		return fmt.Errorf("server is required")
	}
	libraryImportClaimsMu.Lock()
	defer libraryImportClaimsMu.Unlock()
	if s.store == nil {
		claims := cloneLibraryImportClaims(memoryLibraryImportClaims[s])
		changed, err := mutate(&claims)
		if err != nil || !changed {
			return err
		}
		if len(claims) == 0 {
			delete(memoryLibraryImportClaims, s)
		} else {
			memoryLibraryImportClaims[s] = cloneLibraryImportClaims(claims)
		}
		return nil
	}
	for attempt := 0; attempt < libraryImportClaimCASRetries; attempt++ {
		raw, err := s.store.GetSetting(libraryImportClaimsSettingKey)
		if err != nil {
			return err
		}
		claims, err := decodeLibraryImportClaims(raw)
		if err != nil {
			return err
		}
		changed, err := mutate(&claims)
		if err != nil || !changed {
			return err
		}
		payload, err := json.Marshal(claims)
		if err != nil {
			return err
		}
		if len(payload) > maxLibraryImportClaimsRawBytes {
			return fmt.Errorf("library import claims exceed the %d-byte limit", maxLibraryImportClaimsRawBytes)
		}
		if cas, ok := s.store.(librarySettingsCASStore); ok {
			updated, err := cas.CompareAndSwapSetting(libraryImportClaimsSettingKey, raw, string(payload))
			if err != nil {
				return err
			}
			if updated {
				return nil
			}
			continue
		}
		if s.runner != nil && s.runner.leadership != nil {
			return fmt.Errorf("HA storage must support compare-and-swap library import claims")
		}
		return s.store.SaveSetting(libraryImportClaimsSettingKey, string(payload))
	}
	return fmt.Errorf("library import claims changed concurrently; retry")
}

func decodeLibraryImportClaims(raw string) ([]libraryImportClaim, error) {
	if len(raw) > maxLibraryImportClaimsRawBytes {
		return nil, fmt.Errorf("library import claims exceed the %d-byte limit", maxLibraryImportClaimsRawBytes)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var claims []libraryImportClaim
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		return nil, fmt.Errorf("decode library import claims: %w", err)
	}
	if len(claims) > maxLibraryImportClaims {
		return nil, fmt.Errorf("library import claims exceed the %d-entry limit", maxLibraryImportClaims)
	}
	identities := make(map[string]struct{}, len(claims))
	ownerCommitted := make(map[string]bool, len(claims))
	ownerSeen := make(map[string]bool, len(claims))
	ownerSequences := make(map[string]map[int]struct{}, len(claims))
	for index := range claims {
		claim := &claims[index]
		claim.Identity = strings.TrimSpace(claim.Identity)
		claim.OwnerID = strings.TrimSpace(claim.OwnerID)
		claim.RemoteID = firstNonEmpty(strings.TrimSpace(claim.RemoteID), "direct")
		claim.ProjectID = normalizeProjectID(claim.ProjectID)
		claim.Kind = normalizeLibraryKind(claim.Kind)
		claim.SourceInstanceID = strings.TrimSpace(claim.SourceInstanceID)
		claim.SourceItemID = strings.TrimSpace(claim.SourceItemID)
		claim.MutationID = strings.TrimSpace(claim.MutationID)
		claim.LocalItemID = strings.TrimSpace(claim.LocalItemID)
		claim.IntendedHash = strings.TrimSpace(claim.IntendedHash)
		claim.ArchivePath = strings.TrimSpace(claim.ArchivePath)
		if claim.Identity == "" || claim.OwnerID == "" || claim.Kind == "" || claim.SourceInstanceID == "" || claim.SourceItemID == "" || claim.MutationID == "" || claim.IntendedHash == "" {
			return nil, fmt.Errorf("library import claim %d is missing required identity metadata", index)
		}
		expectedIdentity := libraryLinkKey(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID)
		if claim.Identity != expectedIdentity {
			return nil, fmt.Errorf("library import claim %d identity does not match its metadata", index)
		}
		if claim.Sequence < 0 || claim.CreatedAt.IsZero() || claim.ExpiresAt.IsZero() {
			return nil, fmt.Errorf("library import claim %d has invalid sequence or lease timestamps", index)
		}
		if claim.Prepared && claim.LocalItemID == "" {
			return nil, fmt.Errorf("prepared library import claim %d has no local item id", index)
		}
		if claim.Applied && !claim.Prepared {
			return nil, fmt.Errorf("applied library import claim %d was not prepared", index)
		}
		if claim.ArchiveActivated && (!claim.ArchiveExpected || claim.ArchivePath == "") {
			return nil, fmt.Errorf("library import claim %d has invalid archive activation metadata", index)
		}
		if _, duplicate := identities[claim.Identity]; duplicate {
			return nil, fmt.Errorf("duplicate library import claim identity %q", claim.Identity)
		}
		identities[claim.Identity] = struct{}{}
		if ownerSeen[claim.OwnerID] && ownerCommitted[claim.OwnerID] != claim.Committed {
			return nil, fmt.Errorf("library import transaction %q has mixed terminal state", claim.OwnerID)
		}
		ownerSeen[claim.OwnerID] = true
		ownerCommitted[claim.OwnerID] = claim.Committed
		if ownerSequences[claim.OwnerID] == nil {
			ownerSequences[claim.OwnerID] = make(map[int]struct{})
		}
		if _, duplicate := ownerSequences[claim.OwnerID][claim.Sequence]; duplicate {
			return nil, fmt.Errorf("library import transaction %q has duplicate sequence %d", claim.OwnerID, claim.Sequence)
		}
		ownerSequences[claim.OwnerID][claim.Sequence] = struct{}{}
	}
	return claims, nil
}

func cloneLibraryImportClaims(input []libraryImportClaim) []libraryImportClaim {
	if len(input) == 0 {
		return nil
	}
	out := make([]libraryImportClaim, len(input))
	for index, claim := range input {
		out[index] = claim
		out[index].BeforeLink = cloneLibrarySyncLink(claim.BeforeLink)
		out[index].BeforeScript = cloneStoredScriptForRead(claim.BeforeScript)
		out[index].BeforeScenario = cloneStoredScenarioForRead(claim.BeforeScenario)
		out[index].BeforeResult = cloneExternalResultImport(claim.BeforeResult)
	}
	return out
}

func (s *Server) loadLibraryImportClaims() ([]libraryImportClaim, error) {
	if s.store == nil {
		libraryImportClaimsMu.Lock()
		defer libraryImportClaimsMu.Unlock()
		return cloneLibraryImportClaims(memoryLibraryImportClaims[s]), nil
	}
	raw, err := s.store.GetSetting(libraryImportClaimsSettingKey)
	if err != nil {
		return nil, err
	}
	return decodeLibraryImportClaims(raw)
}

// recoverExpiredLibraryImportClaims takes over one expired transaction at a
// time. A CAS lease prevents two replacement replicas from compensating it.
func (s *Server) recoverExpiredLibraryImportClaims() error {
	for {
		now := time.Now().UTC()
		recoveryOwner := uuid.NewString()
		var taken []libraryImportClaim
		cleanedCommitted := false
		err := s.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
			// This closure can run again when a settings CAS loses. Do not carry
			// ownership decisions forward from an attempt that never committed.
			taken = taken[:0]
			cleanedCommitted = false
			for index := range *claims {
				if (*claims)[index].Committed {
					owner := (*claims)[index].OwnerID
					filtered := (*claims)[:0]
					for _, claim := range *claims {
						if claim.OwnerID != owner {
							filtered = append(filtered, claim)
						}
					}
					*claims = filtered
					cleanedCommitted = true
					return true, nil
				}
			}
			owner := ""
			for _, claim := range *claims {
				if !claim.ExpiresAt.After(now) {
					owner = claim.OwnerID
					break
				}
			}
			if owner == "" {
				return false, nil
			}
			for index := range *claims {
				if (*claims)[index].OwnerID == owner {
					(*claims)[index].OwnerID = recoveryOwner
					(*claims)[index].ExpiresAt = now.Add(libraryImportClaimTTL())
					taken = append(taken, (*claims)[index])
				}
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		if len(taken) == 0 {
			if cleanedCommitted {
				continue
			}
			return nil
		}
		if err := s.refreshLibraryImportAssetsForRecovery(taken); err != nil {
			return errors.Join(err, s.expireLibraryImportClaims(recoveryOwner))
		}
		sort.SliceStable(taken, func(i, j int) bool { return taken[i].Sequence > taken[j].Sequence })
		for _, claim := range taken {
			if err := s.recoverLibraryImportClaim(claim); err != nil {
				recoveryErr := fmt.Errorf("recover expired library import %s: %w", claim.Identity, err)
				return errors.Join(recoveryErr, s.expireLibraryImportClaims(recoveryOwner))
			}
		}
		if err := s.removeLibraryImportClaims(recoveryOwner); err != nil {
			return err
		}
	}
}

func (s *Server) refreshLibraryImportAssetsForRecovery(claims []libraryImportClaim) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("library import recovery requires a test runner")
	}
	needScripts := false
	needScenarios := false
	for _, claim := range claims {
		switch claim.Kind {
		case "script":
			needScripts = true
		case "scenario":
			needScripts = true
			needScenarios = true
		}
	}
	if needScripts {
		if err := s.runner.reloadStoredScripts(); err != nil {
			return fmt.Errorf("refresh durable scripts for library recovery: %w", err)
		}
	}
	if needScenarios {
		if err := s.runner.reloadStoredScenarios(); err != nil {
			return fmt.Errorf("refresh durable scenarios for library recovery: %w", err)
		}
	}
	return nil
}

func (s *Server) libraryRecoveryScript(id string) (*StoredScript, bool, error) {
	if s == nil || s.runner == nil {
		return nil, false, fmt.Errorf("library import recovery requires a test runner")
	}
	current, exists, err := s.runner.GetScriptDurable(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return current, exists, err
}

func (s *Server) libraryRecoveryScenario(id string) (*StoredScenario, bool, error) {
	if s == nil || s.runner == nil {
		return nil, false, fmt.Errorf("library import recovery requires a test runner")
	}
	if s.store == nil {
		current, exists := s.runner.GetScenario(id)
		return current, exists, nil
	}
	scenario, scripts, err := s.store.GetScenario(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	s.runner.mu.RLock()
	current := s.runner.buildStoredScenario(scenario, scripts)
	s.runner.mu.RUnlock()
	if current == nil {
		return nil, false, nil
	}
	bindings, err := s.store.ListScenarioDataPoolBindings(id)
	if err != nil {
		return nil, false, err
	}
	schedules, err := s.store.ListScenarioSchedules(id)
	if err != nil {
		return nil, false, err
	}
	current.DataPools = append([]storage.ScenarioDataPoolBinding(nil), bindings...)
	current.Schedules = make([]*storage.ScenarioSchedule, 0, len(schedules))
	for _, schedule := range schedules {
		current.Schedules = append(current.Schedules, cloneScenarioSchedule(schedule))
	}
	return current, true, nil
}

// libraryScenarioRecoveryHash fingerprints the user-owned scenario content
// while excluding destination-generated binding identities and timestamps.
// That gives a prepared crash intent a stable value even when imported script
// IDs are remapped and the process dies before the synchronization link exists.
func (s *Server) libraryScenarioRecoveryHash(scenario *StoredScenario) string {
	if scenario == nil {
		return hashCanonical(nil)
	}
	return s.libraryScenarioRequestRecoveryHash(createScenarioRequestFromStored(maskStoredScenario(scenario)))
}

func (s *Server) libraryScenarioRequestRecoveryHash(req CreateScenarioRequest) string {
	req = cloneScenarioRequestForLibrary(req)
	req.ScenarioMode = normalizeScenarioMode(req.ScenarioMode)
	req.RuntimeSettings = maskRuntimeSettings(req.RuntimeSettings)
	for index := range req.Groups {
		group := &req.Groups[index]
		group.RuntimeSettings = maskRuntimeSettings(group.RuntimeSettings)
		protocol := ""
		if s != nil && s.runner != nil {
			if script, exists := s.runner.GetScript(group.ScriptID); exists && script != nil {
				protocol = script.Protocol
			}
		}
		if role, err := normalizeGroupRole(group.Role, protocol, index); err == nil {
			group.Role = role
		} else {
			group.Role = strings.ToLower(strings.TrimSpace(group.Role))
		}
	}
	for index := range req.DataPools {
		binding := &req.DataPools[index]
		binding.ID = ""
		binding.ScenarioID = ""
		binding.PoolID = strings.TrimSpace(binding.PoolID)
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.AccessMode = firstNonEmpty(strings.TrimSpace(binding.AccessMode), "read_only")
		binding.SelectionStrategy = firstNonEmpty(strings.TrimSpace(binding.SelectionStrategy), "sequential")
		binding.CreatedAt = time.Time{}
		binding.UpdatedAt = time.Time{}
	}
	return hashCanonical(req)
}

func (s *Server) recoverLibraryImportClaim(claim libraryImportClaim) error {
	if !claim.Prepared {
		return nil
	}
	s.refreshLibraryLinksForImport()
	currentLink := s.exactLibraryLink(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID)
	var ownedLink *librarySyncLink
	if currentLink != nil && currentLink.MutationID == claim.MutationID {
		ownedLink = currentLink
	} else if !librarySyncLinksEqual(currentLink, claim.BeforeLink) {
		// A later owner superseded the expired mutation. Its asset is authoritative.
		return nil
	}
	journal := newLibraryImportJournal(s)
	switch claim.Kind {
	case "script":
		current, exists, err := s.libraryRecoveryScript(claim.LocalItemID)
		if err != nil {
			return err
		}
		if !exists || current == nil {
			journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
		} else if claim.BeforeScript == nil {
			matchesImport, err := s.libraryImportClaimScriptHashMatches(
				claim,
				current.Script,
				claim.IntendedHash,
			)
			if err != nil {
				return err
			}
			if ownedLink != nil && strings.TrimSpace(ownedLink.LocalContentHash) != "" {
				matchesLink, err := s.libraryImportClaimScriptHashMatches(
					claim,
					current.Script,
					ownedLink.LocalContentHash,
				)
				if err != nil {
					return err
				}
				matchesImport = matchesImport || matchesLink
			}
			if !matchesImport {
				// A later editor won without adopting this claim's link token.
				// Restore only the link and preserve their asset.
				journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
				break
			}
			journal.recordScript(nil, current, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
		} else if matchesBefore, err := s.libraryImportClaimScriptsEquivalent(
			claim,
			current.Script,
			claim.BeforeScript.Script,
		); err != nil {
			return err
		} else if matchesBefore {
			journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
		} else {
			matchesIntended, err := s.libraryImportClaimScriptHashMatches(
				claim,
				current.Script,
				claim.IntendedHash,
			)
			if err != nil {
				return err
			}
			if !matchesIntended && ownedLink != nil {
				matchesIntended, err = s.libraryImportClaimScriptHashMatches(
					claim,
					current.Script,
					ownedLink.LocalContentHash,
				)
				if err != nil {
					return err
				}
			}
			if matchesIntended {
				journal.recordScript(claim.BeforeScript, current, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
			} else {
				journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
			}
		}
	case "scenario":
		current, exists, err := s.libraryRecoveryScenario(claim.LocalItemID)
		if err != nil {
			return err
		}
		if !exists || current == nil {
			journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
		} else if claim.BeforeScenario == nil {
			matchesImport := s.libraryScenarioRecoveryHash(current) == claim.IntendedHash
			if ownedLink != nil && strings.TrimSpace(ownedLink.LocalContentHash) != "" {
				matchesImport = hashLibraryScenario(current) == ownedLink.LocalContentHash
			}
			if !matchesImport {
				journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
				break
			}
			journal.recordScenario(nil, current, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
		} else if s.libraryScenarioRecoveryHash(current) == s.libraryScenarioRecoveryHash(claim.BeforeScenario) {
			journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
		} else if s.libraryScenarioRecoveryHash(current) == claim.IntendedHash || (ownedLink != nil && strings.TrimSpace(ownedLink.LocalContentHash) != "" && hashLibraryScenario(current) == ownedLink.LocalContentHash) {
			journal.recordScenario(claim.BeforeScenario, current, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
		} else {
			journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
		}
	case "result":
		current, exists, err := s.getExternalResultImport(claim.LocalItemID)
		if err != nil {
			return err
		}
		archiveActivated := claim.ArchiveActivated
		if !claim.Applied && claim.ArchiveExpected && !claim.ArchiveExisted && strings.TrimSpace(claim.ArchivePath) != "" {
			if _, statErr := os.Stat(claim.ArchivePath); statErr == nil {
				archiveActivated = hashArchiveManifestPath(filepath.Join(claim.ArchivePath, "manifest.json")) == claim.ArchiveManifest && archiveDirectorySize(claim.ArchivePath) == claim.ArchiveSize
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
		}
		if !exists || current == nil {
			current = &storage.ExternalResultImport{ID: claim.LocalItemID}
			journal.recordResult(false, current, archiveActivated, claim.ArchivePath, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
		} else if claim.BeforeResult == nil {
			mutationID, _ := current.RawResult[libraryResultMutationIDKey].(string)
			if strings.TrimSpace(mutationID) != claim.MutationID {
				// Immutable evidence without this claim's durable marker belongs
				// to a later writer even if its public content hash is identical.
				journal.recordItem(claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink, nil)
				break
			}
			journal.recordResult(true, current, archiveActivated, claim.ArchivePath, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
		} else {
			journal.recordResult(false, current, archiveActivated, claim.ArchivePath, claim.RemoteID, claim.ProjectID, claim.Kind, claim.SourceInstanceID, claim.SourceItemID, claim.BeforeLink, ownedLink)
		}
	default:
		return fmt.Errorf("unsupported claim kind %q", claim.Kind)
	}
	return journal.rollback()
}

func (s *Server) libraryImportClaimScriptHashMatches(
	claim libraryImportClaim,
	script *dsl.Script,
	expected string,
) (bool, error) {
	if strings.HasPrefix(strings.TrimSpace(claim.RemoteID), "direct-import:") {
		digest, err := s.directImportExactScriptDigest(script)
		if err != nil {
			return false, fmt.Errorf("fingerprint direct import recovery script: %w", err)
		}
		return digest == strings.TrimSpace(expected), nil
	}
	return libraryScriptHashMatches(script, expected), nil
}

func (s *Server) libraryImportClaimScriptsEquivalent(
	claim libraryImportClaim,
	left, right *dsl.Script,
) (bool, error) {
	if strings.HasPrefix(strings.TrimSpace(claim.RemoteID), "direct-import:") {
		leftDigest, err := s.directImportExactScriptDigest(left)
		if err != nil {
			return false, fmt.Errorf("fingerprint current direct import recovery script: %w", err)
		}
		rightDigest, err := s.directImportExactScriptDigest(right)
		if err != nil {
			return false, fmt.Errorf("fingerprint prior direct import recovery script: %w", err)
		}
		return leftDigest == rightDigest, nil
	}
	return libraryScriptHashesEquivalent(left, right), nil
}

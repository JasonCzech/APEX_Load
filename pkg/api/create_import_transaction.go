package api

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// createImportTransaction gives direct, multi-asset imports the same durable
// write-ahead recovery and version-fenced compensation used by federated
// library imports. Every local ID and intended content hash is journaled before
// the first asset is created. A process restart can therefore remove an
// incomplete import, while a concurrent edit changes the hash/version and is
// preserved.
type createImportTransaction struct {
	server           *Server
	claims           *libraryImportClaimHandle
	journal          *libraryImportJournal
	remoteID         string
	projectID        string
	sourceInstanceID string
	items            map[string]LibraryBundleItem
	applied          map[string]LibraryBundleItem
	// updates holds, by script ID, the earlier imported script a re-import
	// updates as a new version. An update is atomic on its own and is not
	// journaled for crash recovery: a restart leaves an applied update
	// applied, which is the version the operator asked for. A failure later in
	// the same request puts its prior content back as a further version.
	updates map[string]*StoredScript
	// reconciledUpdates were applied by an earlier attempt of this operation.
	// Verify and return them without a new version or a compensation entry.
	reconciledUpdates map[string]*StoredScript
	appliedUpdates    map[string]bool
	mutationStarted   bool
	closed            bool
}

var errCreateImportAssetCompensation = errors.New("direct import asset compensation failed")

func newCreateImportTransaction(server *Server, source string, scripts []*dsl.Script, scenarioID string, scenarioRequest *CreateScenarioRequest) (*createImportTransaction, error) {
	return newImportTransaction(server, source, scripts, nil, scenarioID, scenarioRequest)
}

// newImportTransaction is newCreateImportTransaction where each script whose
// ID is in updates replaces that earlier imported script as a new version
// instead of being created.
func newImportTransaction(server *Server, source string, scripts []*dsl.Script, updates map[string]*StoredScript, scenarioID string, scenarioRequest *CreateScenarioRequest) (*createImportTransaction, error) {
	if server == nil || server.runner == nil {
		return nil, fmt.Errorf("import transaction requires a test runner")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "multi-script"
	}
	projectID := ""
	for _, script := range scripts {
		if script != nil && strings.TrimSpace(script.ProjectID) != "" {
			projectID = script.ProjectID
			break
		}
	}
	projectID = normalizeProjectID(projectID)
	remoteID := "direct-import:" + source
	identityParts := []string{projectID, strings.TrimSpace(scenarioID)}
	for _, script := range scripts {
		if script == nil {
			identityParts = append(identityParts, "")
			continue
		}
		identityParts = append(identityParts, strings.TrimSpace(script.ID))
	}
	sourceInstanceID := source + ":" + directImportSHA256(identityParts...)
	bundle := LibraryBundle{
		BundleID: uuid.NewString(),
		Source:   LibraryBundleSource{InstanceID: sourceInstanceID, ProjectID: projectID},
	}
	items := make(map[string]LibraryBundleItem, len(scripts)+1)
	for index, script := range scripts {
		if script == nil {
			return nil, fmt.Errorf("planned import script %d is nil", index+1)
		}
		id := strings.TrimSpace(script.ID)
		if id == "" {
			return nil, fmt.Errorf("planned import script %d has no reserved id", index+1)
		}
		if updates[id] != nil {
			continue
		}
		// Direct imports are local creates, so source timestamps are not ownership
		// evidence. Give even the in-memory store a non-zero version fence; SQL
		// stores may advance UpdatedAt again during the atomic insert.
		now := time.Now().UTC()
		script.CreatedAt = now
		script.UpdatedAt = now
		key := createImportItemKey("script", id)
		if _, duplicate := items[key]; duplicate {
			return nil, fmt.Errorf("planned import contains duplicate script id %s", id)
		}
		exactDigest, err := server.directImportExactScriptDigest(script)
		if err != nil {
			return nil, fmt.Errorf("fingerprint planned import script %d: %w", index+1, err)
		}
		item := LibraryBundleItem{
			Kind:             "script",
			SourceInstanceID: sourceInstanceID,
			SourceItemID:     id,
			ProjectID:        normalizeProjectID(script.ProjectID),
			Name:             script.Name,
			ContentHash:      exactDigest,
			Script:           script,
		}
		items[key] = item
		bundle.Items = append(bundle.Items, item)
	}
	if scenarioRequest != nil {
		scenarioID = strings.TrimSpace(scenarioID)
		if scenarioID == "" {
			return nil, fmt.Errorf("planned import scenario has no reserved id")
		}
		item := LibraryBundleItem{
			Kind:             "scenario",
			SourceInstanceID: sourceInstanceID,
			SourceItemID:     scenarioID,
			ProjectID:        projectID,
			Name:             scenarioRequest.Name,
			ContentHash:      server.libraryScenarioRequestRecoveryHash(*scenarioRequest),
			Scenario:         &LibraryScenarioPayload{ID: scenarioID, Request: cloneScenarioRequestForLibrary(*scenarioRequest)},
		}
		items[createImportItemKey("scenario", scenarioID)] = item
		bundle.Items = append(bundle.Items, item)
	}
	if len(bundle.Items) == 0 && len(updates) == 0 {
		return nil, fmt.Errorf("import transaction requires at least one asset")
	}

	tx := &createImportTransaction{
		server: server, journal: newLibraryImportJournal(server),
		remoteID: remoteID, projectID: projectID, sourceInstanceID: sourceInstanceID,
		items: items, applied: make(map[string]LibraryBundleItem, len(items)),
		updates: updates, appliedUpdates: make(map[string]bool, len(updates)),
	}
	if len(bundle.Items) == 0 {
		return tx, nil
	}
	claims, err := server.acquireLibraryImportClaims(bundle, remoteID, projectID)
	if err != nil {
		return nil, fmt.Errorf("start durable import transaction: %w", err)
	}
	tx.claims = claims
	if err := claims.prepareDirectCreates(bundle.Items); err != nil {
		return nil, errors.Join(fmt.Errorf("prepare durable import transaction: %w", err), tx.rollback())
	}
	return tx, nil
}

// updateImportedScript replaces the earlier imported script reserved for
// script.ID with script, as a new version, if it is still the version the
// operator reviewed. Its folder and creation time stay.
func (tx *createImportTransaction) updateImportedScript(script *dsl.Script, sourceProfile string) (*StoredScript, error) {
	if tx == nil || tx.closed {
		return nil, fmt.Errorf("import transaction is not active")
	}
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	before := tx.updates[strings.TrimSpace(script.ID)]
	if before == nil || before.Script == nil {
		return nil, fmt.Errorf("script %s was not reserved for update by the import transaction", strings.TrimSpace(script.ID))
	}
	sourceProfile = strings.TrimSpace(sourceProfile)
	if sourceProfile == "" {
		return nil, fmt.Errorf("import source profile is required")
	}
	beforeDigest, err := scriptCorrelationActionGraphDigest(before.Script)
	if err != nil {
		return nil, err
	}
	tx.mutationStarted = true
	stored, updated, err := tx.server.runner.UpdateImportedScriptDurableInProjectIfUnchanged(
		before.ID, script, before.ProjectID, before.Script.UpdatedAt, beforeDigest, sourceProfile,
	)
	if errors.Is(err, errScriptConcurrentChange) {
		return nil, staleImportUpdate("the script to update changed after review")
	}
	if err != nil {
		return nil, err
	}
	if !updated || stored == nil || stored.Script == nil {
		return nil, staleImportUpdate("the script to update no longer exists")
	}
	prior := cloneScriptDefinition(before.Script)
	after := cloneScriptDefinition(stored.Script)
	tx.journal.add(func() error {
		afterDigest, err := scriptCorrelationActionGraphDigest(after)
		if err != nil {
			return fmt.Errorf("rollback updated script %s: %w", after.ID, err)
		}
		_, restored, err := tx.server.runner.UpdateImportedScriptDurableInProjectIfUnchanged(
			after.ID, prior, after.ProjectID, after.UpdatedAt, afterDigest, sourceProfile,
		)
		if err != nil {
			return fmt.Errorf("rollback updated script %s: %w", after.ID, err)
		}
		if !restored {
			return fmt.Errorf("rollback updated script %s lost its version fence", after.ID)
		}
		return nil
	})
	tx.appliedUpdates[before.ID] = true
	return stored, nil
}

func createImportItemKey(kind, id string) string {
	return normalizeLibraryKind(kind) + "\x00" + strings.TrimSpace(id)
}

func (tx *createImportTransaction) item(kind, id string) (LibraryBundleItem, error) {
	if tx == nil || tx.closed {
		return LibraryBundleItem{}, fmt.Errorf("import transaction is not active")
	}
	item, ok := tx.items[createImportItemKey(kind, id)]
	if !ok {
		return LibraryBundleItem{}, fmt.Errorf("%s %s was not reserved by the import transaction", normalizeLibraryKind(kind), strings.TrimSpace(id))
	}
	return item, nil
}

func (tx *createImportTransaction) createScript(script *dsl.Script) (*StoredScript, error) {
	return tx.createScriptWithImportProfile(script, "")
}

func (tx *createImportTransaction) createImportedScript(
	script *dsl.Script,
	sourceProfile string,
) (*StoredScript, error) {
	sourceProfile = strings.TrimSpace(sourceProfile)
	if sourceProfile == "" {
		return nil, fmt.Errorf("import source profile is required")
	}
	return tx.createScriptWithImportProfile(script, sourceProfile)
}

func (tx *createImportTransaction) createScriptWithImportProfile(
	script *dsl.Script,
	sourceProfile string,
) (*StoredScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	item, err := tx.item("script", script.ID)
	if err != nil {
		return nil, err
	}
	var stored *StoredScript
	created := false
	tx.mutationStarted = true
	if sourceProfile == "" {
		stored, created, err = tx.server.runner.CreateScriptDurableReportingReplay(script)
	} else {
		stored, created, err = tx.server.runner.CreateImportedScriptDurableReportingReplay(
			script,
			sourceProfile,
		)
	}
	if err != nil {
		return nil, err
	}
	if created {
		tx.journal.recordScript(nil, stored, tx.remoteID, tx.projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, nil, nil)
	}
	tx.recordApplied(item)
	return stored, nil
}

func (tx *createImportTransaction) createScenario(id string, request CreateScenarioRequest) (*StoredScenario, error) {
	item, err := tx.item("scenario", id)
	if err != nil {
		return nil, err
	}
	tx.mutationStarted = true
	stored, err := tx.server.runner.StoreScenarioWithIDCreateOnly(id, request)
	created := err == nil
	if errors.Is(err, errScenarioConcurrentChange) {
		// A crash can leave the deterministic scenario durable after its direct
		// import transaction committed but before the idempotency receipt was
		// finalized. Adopt only an exact request-equivalent row.
		_ = tx.server.runner.reloadStoredScenarios()
		if existing, ok := tx.server.runner.GetScenario(id); ok &&
			tx.server.libraryScenarioRequestRecoveryHash(
				createScenarioRequestFromStored(existing),
			) == item.ContentHash {
			stored = existing
			created = false
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	if created {
		tx.journal.recordScenario(nil, stored, tx.remoteID, tx.projectID, item.Kind, item.SourceInstanceID, item.SourceItemID, nil, nil)
	}
	tx.recordApplied(item)
	return stored, nil
}

func (tx *createImportTransaction) recordApplied(item LibraryBundleItem) {
	if tx == nil {
		return
	}
	if tx.applied == nil {
		tx.applied = make(map[string]LibraryBundleItem)
	}
	tx.applied[createImportItemKey(item.Kind, item.SourceItemID)] = item
}

func (tx *createImportTransaction) appliedItems() []LibraryBundleItem {
	if tx == nil || len(tx.applied) == 0 {
		return nil
	}
	items := make([]LibraryBundleItem, 0, len(tx.applied))
	for _, item := range tx.applied {
		items = append(items, item)
	}
	return items
}

func (tx *createImportTransaction) validateAllItemsApplied() error {
	if tx == nil {
		return fmt.Errorf("import transaction is required")
	}
	if len(tx.applied) != len(tx.items) {
		return fmt.Errorf("cannot commit direct import: %d of %d reserved assets were applied", len(tx.applied), len(tx.items))
	}
	for id := range tx.updates {
		if !tx.appliedUpdates[id] {
			return fmt.Errorf("cannot commit direct import: reserved update of script %s was not applied", id)
		}
	}
	for key := range tx.items {
		if _, ok := tx.applied[key]; !ok {
			return fmt.Errorf("cannot commit direct import: reserved asset %q was not applied", key)
		}
	}
	return nil
}

func (tx *createImportTransaction) commit() error {
	if err := tx.commitRetainingClaims(); err != nil {
		return err
	}
	// Cleanup is best effort after the durable terminal marker. Retained claims
	// are safe publication evidence and generic recovery removes them later.
	_ = tx.cleanupCommittedClaims()
	return nil
}

func (tx *createImportTransaction) commitRetainingClaims() error {
	if tx == nil || tx.closed {
		return fmt.Errorf("import transaction is not active")
	}
	if err := tx.validateAllItemsApplied(); err != nil {
		return err
	}
	if tx.claims != nil {
		if err := tx.claims.markDirectCreatesApplied(tx.appliedItems()); err != nil {
			return fmt.Errorf("checkpoint durable import transaction: %w", err)
		}
		if err := tx.claims.markCommittedRetainingClaims(); err != nil {
			return fmt.Errorf("commit durable import transaction: %w", err)
		}
	}
	tx.closed = true
	tx.journal.undo = nil
	return nil
}

func (tx *createImportTransaction) cleanupCommittedClaims() error {
	if tx == nil || tx.claims == nil {
		return nil
	}
	return tx.claims.cleanupCommittedClaims()
}

func (tx *createImportTransaction) rollback() error {
	if tx == nil || tx.closed {
		return nil
	}
	tx.closed = true
	var errs []error
	if tx.journal != nil {
		if err := tx.journal.rollback(); err != nil {
			errs = append(errs, fmt.Errorf("%w: %v", errCreateImportAssetCompensation, err))
		}
	}
	if tx.claims != nil {
		if err := tx.releaseClaims(); err != nil {
			errs = append(errs, fmt.Errorf("abandon durable import transaction: %w", err))
		}
	}
	if tx.server != nil {
		if err := tx.server.recoverExpiredLibraryImportClaims(); err != nil {
			errs = append(errs, fmt.Errorf("recover abandoned import transaction: %w", err))
		}
	}
	return errors.Join(errs...)
}

// stopClaimHeartbeat is deliberately independent from durable claim expiry.
// Direct-import rollback must first stop process-local renewal, then establish
// whether it still owns the receipt fence before exposing claims to recovery.
func (tx *createImportTransaction) stopClaimHeartbeat() {
	if tx == nil || tx.claims == nil {
		return
	}
	tx.claims.stopHeartbeat()
}

// releaseClaims expires the durable recovery evidence after receipt ownership
// has been resolved or local asset compensation has finished.
func (tx *createImportTransaction) releaseClaims() error {
	if tx == nil || tx.claims == nil {
		return nil
	}
	claims := tx.claims
	tx.claims = nil
	return claims.abandon()
}

// commitUnmutatedClaims records a terminal no-op transaction. It is safe only
// before any durable asset mutation has started. Unlike expiring the claims,
// this prevents recovery from mistaking an identical asset owned by a newer
// receipt holder for work performed by this losing request.
func (tx *createImportTransaction) commitUnmutatedClaims() error {
	if tx == nil || tx.claims == nil {
		return nil
	}
	if tx.mutationStarted {
		return errors.New("cannot commit direct import claims after asset mutation started")
	}
	claims := tx.claims
	tx.claims = nil
	return claims.complete()
}

func (tx *createImportTransaction) closeWithoutAssetCompensation() {
	if tx == nil {
		return
	}
	tx.closed = true
	if tx.journal != nil {
		tx.journal.undo = nil
	}
	if tx.claims != nil {
		activeLibraryImportOwners.Delete(tx.claims.owner)
	}
}

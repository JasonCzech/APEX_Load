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
	closed           bool
}

func newCreateImportTransaction(server *Server, source string, scripts []*dsl.Script, scenarioID string, scenarioRequest *CreateScenarioRequest) (*createImportTransaction, error) {
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
	if len(bundle.Items) == 0 {
		return nil, fmt.Errorf("import transaction requires at least one asset")
	}

	claims, err := server.acquireLibraryImportClaims(bundle, remoteID, projectID)
	if err != nil {
		return nil, fmt.Errorf("start durable import transaction: %w", err)
	}
	tx := &createImportTransaction{
		server: server, claims: claims, journal: newLibraryImportJournal(server),
		remoteID: remoteID, projectID: projectID, sourceInstanceID: sourceInstanceID, items: items,
	}
	for _, item := range bundle.Items {
		if err := claims.prepare(item, item.SourceItemID, nil, nil, nil, nil, false, false, ""); err != nil {
			return nil, errors.Join(fmt.Errorf("prepare durable import transaction: %w", err), tx.rollback())
		}
	}
	return tx, nil
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
	if err := tx.claims.markApplied(item, false); err != nil {
		return stored, fmt.Errorf("checkpoint imported script %s: %w", stored.ID, err)
	}
	return stored, nil
}

func (tx *createImportTransaction) createScenario(id string, request CreateScenarioRequest) (*StoredScenario, error) {
	item, err := tx.item("scenario", id)
	if err != nil {
		return nil, err
	}
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
	if err := tx.claims.markApplied(item, false); err != nil {
		return stored, fmt.Errorf("checkpoint imported scenario %s: %w", stored.ID, err)
	}
	return stored, nil
}

func (tx *createImportTransaction) commit() error {
	if tx == nil || tx.closed {
		return fmt.Errorf("import transaction is not active")
	}
	if err := tx.claims.complete(); err != nil {
		return fmt.Errorf("commit durable import transaction: %w", err)
	}
	tx.closed = true
	tx.journal.undo = nil
	return nil
}

func (tx *createImportTransaction) rollback() error {
	if tx == nil || tx.closed {
		return nil
	}
	tx.closed = true
	var errs []error
	if tx.journal != nil {
		if err := tx.journal.rollback(); err != nil {
			errs = append(errs, err)
		}
	}
	if tx.claims != nil {
		if err := tx.claims.abandon(); err != nil {
			errs = append(errs, fmt.Errorf("abandon durable import transaction: %w", err))
		} else if err := tx.server.recoverExpiredLibraryImportClaims(); err != nil {
			errs = append(errs, fmt.Errorf("recover abandoned import transaction: %w", err))
		}
	}
	return errors.Join(errs...)
}

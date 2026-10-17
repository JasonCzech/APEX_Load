package api

import (
	"errors"
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// preparedImportBatch begins after each importer has restored and validated its
// drafts. Conversion, source retention and recording evidence remain adapter
// decisions; receipt ownership and the durable transaction have one owner.
type preparedImportBatch struct {
	Source, DefaultProfile string
	Scripts                []*dsl.Script
	Profiles               []string
	Updates                map[string]*StoredScript
	Targets                []*importUpdateTarget
	Claims                 []*importDraftAttestationClaim
	ScenarioID             string
	Scenario               *CreateScenarioRequest
	RenewBeforeCommit      bool
}

type importBatchStage uint8

const (
	importBatchReadUpdate importBatchStage = iota
	importBatchStaleUpdate
	importBatchPrepare
	importBatchReceipt
	importBatchDraft
	importBatchScript
	importBatchScenario
	importBatchCommitStage
)

// importBatchFailure carries a domain outcome, not an HTTP response. Index is
// zero based and relevant only to an item failure.
type importBatchFailure struct {
	Stage importBatchStage
	Index int
	Err   error
}

func batchFailure(stage importBatchStage, index int, err error) *importBatchFailure {
	return &importBatchFailure{Stage: stage, Index: index, Err: err}
}

func (f *importBatchFailure) Error() string { return f.Err.Error() }
func (f *importBatchFailure) Unwrap() error { return f.Err }

type importBatchCommit struct {
	server          *Server
	attempt         *directImportAttempt
	transaction     *createImportTransaction
	assetsCommitted bool
	finished        bool
}

func newImportBatchCommit(server *Server, attempt *directImportAttempt, targets []*importUpdateTarget) *importBatchCommit {
	attempt.retainUpdateRecovery(targets)
	return &importBatchCommit{server: server, attempt: attempt}
}

// close compensates only an unfinished transaction. Once assets are committed
// or adopted, a failed receipt/evidence finalization must leave them available
// to the same operation's recovery pass.
func (b *importBatchCommit) close() error {
	if b.finished || b.assetsCommitted {
		return nil
	}
	return b.attempt.rollback(b.transaction)
}

func (b *importBatchCommit) complete(scripts []*StoredScript, scenario *StoredScenario) error {
	if err := b.attempt.complete(scripts, scenario); err != nil {
		return err
	}
	b.finished = true
	return nil
}

// loadRecovery distinguishes a fully written prior batch from a partial update.
// Finding all assets transfers compensation ownership before content/evidence
// verification: a mismatch must preserve the receipt-bound assets for recovery.
// The adapter supplies its own identity/evidence checks after this returns.
func (b *importBatchCommit) loadRecovery(projectID string, ids []string, scenarioID string, prepared []*dsl.Script, targets []*importUpdateTarget) ([]*StoredScript, *StoredScenario, error) {
	scripts, scenario, err := b.server.loadDirectImportAssetsByIDs(projectID, ids, scenarioID)
	if err != nil {
		return nil, nil, err
	}
	if importUpdatesPending(scripts, prepared, targets) {
		return nil, nil, errImportUpdateTargetStale
	}
	b.assetsCommitted = true
	return scripts, scenario, nil
}

func (b *importBatchCommit) commitPrepared(batch preparedImportBatch) ([]*StoredScript, *StoredScenario, *importBatchFailure) {
	if len(batch.Profiles) != 0 && len(batch.Profiles) != len(batch.Scripts) {
		return nil, nil, batchFailure(importBatchPrepare, -1, fmt.Errorf("script profiles do not match prepared scripts"))
	}
	reconciled, failure := b.server.reconcileImportUpdates(b.attempt, batch.Scripts, batch.Targets, batch.Updates)
	if failure != nil {
		return nil, nil, failure
	}
	transaction, err := newImportTransaction(b.server, batch.Source, batch.Scripts, batch.Updates, batch.ScenarioID, batch.Scenario)
	if err != nil {
		return nil, nil, batchFailure(importBatchPrepare, -1, err)
	}
	b.transaction = transaction
	transaction.reconciledUpdates = reconciled
	stored := make([]*StoredScript, 0, len(batch.Scripts))
	for index, script := range batch.Scripts {
		if err := b.attempt.renew(); err != nil {
			return nil, nil, batchFailure(importBatchReceipt, index, err)
		}
		if index < len(batch.Claims) && batch.Claims[index] != nil {
			if err := b.server.assertImportDraftAttestationOwned(batch.Claims[index]); err != nil {
				return nil, nil, batchFailure(importBatchDraft, index, err)
			}
		}
		profile := batch.DefaultProfile
		if len(batch.Profiles) > 0 {
			profile = batch.Profiles[index]
		}
		created, err := transaction.saveImportedScript(script, profile)
		if errors.Is(err, errImportUpdateTargetStale) {
			return nil, nil, batchFailure(importBatchStaleUpdate, index, err)
		}
		if err != nil {
			return nil, nil, batchFailure(importBatchScript, index, err)
		}
		stored = append(stored, created)
	}
	var scenario *StoredScenario
	if batch.Scenario != nil {
		if err := b.attempt.renew(); err != nil {
			return nil, nil, batchFailure(importBatchReceipt, -1, err)
		}
		scenario, err = transaction.createScenario(batch.ScenarioID, *batch.Scenario)
		if err != nil {
			return nil, nil, batchFailure(importBatchScenario, -1, err)
		}
	}
	if batch.RenewBeforeCommit {
		if err := b.attempt.renew(); err != nil {
			return nil, nil, batchFailure(importBatchReceipt, -1, err)
		}
	}
	if err := transaction.commit(); err != nil {
		return nil, nil, batchFailure(importBatchCommitStage, -1, err)
	}
	b.assetsCommitted = true
	return stored, scenario, nil
}

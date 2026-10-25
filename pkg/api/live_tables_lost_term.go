package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Live tables a new lease term never restored.
//
// Phase A restores a run's live tables only for an adoption candidate. A run
// the new term holds fail-closed, or whose checkpoint it cannot decode, kept
// the tables a term this process lost had restored or prepared: no writer could
// use them again (every client, checkpoint and finalization checks their term,
// and storage refuses a writer that no longer holds the lease), the new term's
// terminal writer was refused by the owner check, nothing finalized them
// durably, and their memory and budget stayed counted until the process
// restarted.
//
// Now the new term releases them from memory and budget as it acquires the
// lease, before Phase A restores anything, and its terminal writer for such a
// run restores the tables from storage under its own term and finalizes them
// there. The restore claims each table for the new term, so the ended term's
// writes are refused by the table's owner as well as by the lease.

// releaseLostTermLiveTables drops every run's live tables that were restored
// or prepared under another lease term than term. Their durable journal and
// checkpoint stay as that term left them. A summary already released after
// its finalization holds no budget and is kept for the run view.
func (tr *TestRunner) releaseLostTermLiveTables(term executionLeaseTerm) []string {
	if tr == nil || !tr.durableExecutionFence || !term.valid() {
		return nil
	}
	tr.liveTablesMu.Lock()
	lost := make([]*runLiveTables, 0)
	for id, run := range tr.liveTables {
		if run.releasedAt.IsZero() && run.term != term {
			lost = append(lost, run)
			delete(tr.liveTables, id)
		}
	}
	tr.liveTablesMu.Unlock()
	released := make([]string, 0, len(lost))
	// Leaving tr.liveTables drops a run from every budget sum. Its own budget
	// field is left alone: a restore still deducts it from the recovery
	// reservation when it ends.
	for _, run := range lost {
		run.mu.Lock()
		run.closed = true
		run.mu.Unlock()
		released = append(released, run.testID)
		slog.Info("released live tables of a lost lease term", "test_id", run.testID, "lost_owner", run.term.ownerID, "lost_generation", run.term.generation, "generation", term.generation)
	}
	return released
}

// liveTablesReleased reports whether run is a summary released after its
// finalization. releasedAt is guarded by liveTablesMu.
func (tr *TestRunner) liveTablesReleased(run *runLiveTables) bool {
	tr.liveTablesMu.Lock()
	defer tr.liveTablesMu.Unlock()
	return !run.releasedAt.IsZero()
}

// finishUnrestoredLiveTables finalizes, under the current term, the durable
// live tables of a run whose tables this term never restored. Only the
// current term's writer holding the registered run object does so; a writer
// of an ended term, which still holds the run's old object or its old fence,
// does nothing. registered is the run's in-memory tables, if any.
func (tr *TestRunner) finishUnrestoredLiveTables(test *ManagedTest, registered *runLiveTables, finalStatus string) {
	// Only an HA controller changes terms; a run without live pools has none.
	if !tr.durableExecutionFence || !hasLiveDataPools(test.DataPools) {
		return
	}
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	tr.mu.RLock()
	writer := tr.tests[test.ID] == test && test.executionFence != "" && test.executionFence == term.ownerID
	tr.mu.RUnlock()
	if !writer {
		return
	}
	if registered != nil {
		if registered.term == term || tr.liveTablesReleased(registered) {
			return
		}
		// Defence in depth: the acquisition already released any table set
		// of an ended term.
		tr.releaseLostTermLiveTables(term)
	}
	finish := tr.unrestoredLiveTableFinalizer(test, term, finalStatus)
	if err := finish(); err != nil {
		tr.enqueueTerminalRetry(liveTableRetryKey(test.ID, &runLiveTables{term: term}), "live table finalization", finish)
	}
}

// unrestoredLiveTableFinalizer restores the run's durable live tables under
// term and finalizes them with status. It gives up once term ended, and does
// nothing when storage shows nothing left to finalize.
func (tr *TestRunner) unrestoredLiveTableFinalizer(test *ManagedTest, term executionLeaseTerm, status string) func() error {
	return func() error {
		if !tr.executionLeaseTermLocallyCurrent(term) {
			return nil
		}
		if current := tr.liveRun(test.ID); current != nil && !tr.liveTablesReleased(current) {
			if current.term == term {
				// An earlier attempt restored them and its finalization failed.
				return tr.liveTableFinalizer(test, current, status)()
			}
			// Defence in depth, as above.
			tr.releaseLostTermLiveTables(term)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pending, err := tr.liveTablesAwaitingFinalization(ctx, test)
		if err != nil || !pending {
			return err
		}
		if err := tr.restoreRunLiveTables(ctx, test, term, true); err != nil {
			var unrecoverable liveTableRestoreUnrecoverable
			if errors.As(err, &unrecoverable) {
				// No retry can change it, and each attempt would claim and
				// read the tables again. The tables stay as storage has them.
				slog.Error("live tables of a run this term never restored cannot be finalized", "test_id", test.ID, "error", err)
				return nil
			}
			return fmt.Errorf("restore live tables for finalization: %w", err)
		}
		restored := tr.liveRun(test.ID)
		if restored == nil || restored.term != term {
			return errors.New("restored live tables are not registered")
		}
		// Every failure, the restore's included, returns to this attempt's
		// own retry; nothing here queues a retry of its own.
		return tr.finalizeRunLiveTables(ctx, test, restored, status)
	}
}

// collectUnrestoredProjectAttachments gives run the run's project-table
// attachments still active in storage, under this term's project term, so
// finalization detaches them. Unlike attachRun it checks nothing against the
// run's groups: the run will not resume, and its attachments only need to go.
func (tr *TestRunner) collectUnrestoredProjectAttachments(ctx context.Context, run *runLiveTables) error {
	store, ok := tr.dataPoolStore.(storage.ProjectLiveTableStore)
	if !ok {
		// Without project-table storage the run has no attachments.
		return nil
	}
	attachments, err := store.ListProjectLiveTableRunAttachments(ctx, run.projectID, run.testID)
	if err != nil {
		return err
	}
	active := make(map[string]storage.ProjectLiveTableAttachment)
	for _, attachment := range attachments {
		if attachment.State == "active" && attachment.RunIncarnation == run.testID {
			active[attachment.ID] = attachment
		}
	}
	if len(active) == 0 {
		return nil
	}
	projectTerm, err := tr.captureProjectLiveTableTerm()
	if err != nil {
		return err
	}
	run.projectTerm, run.projectAttachments = projectTerm, active
	return nil
}

// liveTablesAwaitingFinalization reports whether any of the run's durable
// live tables is still active, or closed without a finalization result, or a
// project-table attachment of the run is still active. A table kept only in
// memory left nothing in storage to finalize; it cannot be claimed by another
// term, its record stays as its term left it, and it does not keep the run's
// other tables from being finalized.
func (tr *TestRunner) liveTablesAwaitingFinalization(ctx context.Context, test *ManagedTest) (bool, error) {
	state, seedStore, err := tr.liveStore()
	if err != nil {
		return false, err
	}
	records, err := state.ListLiveTablesForRun(ctx, test.ID, test.ProjectID)
	if err != nil {
		return false, err
	}
	pending := false
	if store, ok := tr.dataPoolStore.(storage.ProjectLiveTableStore); ok && hasProjectLiveTables(test.DataPools) {
		attachments, err := store.ListProjectLiveTableRunAttachments(ctx, test.ProjectID, test.ID)
		if err != nil {
			return false, err
		}
		for _, attachment := range attachments {
			pending = pending || (attachment.State == "active" && attachment.RunIncarnation == test.ID)
		}
	}
	for _, record := range records {
		if record.Durability != "durable" {
			if record.State != "closed" {
				slog.Warn("a live table kept only in memory by a lost term cannot be finalized", "test_id", test.ID, "table_id", record.ID)
			}
			continue
		}
		switch record.State {
		case "active":
			pending = true
		case "closed":
			if _, err := seedStore.GetLiveTableFinalization(ctx, record.ID, test.ProjectID); errors.Is(err, sql.ErrNoRows) {
				pending = true
			} else if err != nil {
				return false, err
			}
		}
	}
	return pending, nil
}

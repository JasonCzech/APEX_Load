package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

const liveTableSeedBytes = 512 << 10
const liveTableRunBudget = 32 << 20
const liveTableProjectBudget = 64 << 20
const liveTableControllerBudget = 128 << 20
const liveTableRecoveryBytes = 64 << 20
const liveTableRetainedRuns = 128

var errLiveTableFinalizationInterrupted = errors.New("live table finalization was interrupted; the run cannot resume")

type runLiveTables struct {
	mu                 sync.RWMutex
	tables             map[string]*runLiveTable
	projectAttachments map[string]storage.ProjectLiveTableAttachment
	projectSummaries   []RunArchiveProjectLiveTableAttachment
	groupBindings      map[string][]engine.LiveTableBinding
	bindings           []engine.LiveTableBinding
	term               executionLeaseTerm
	projectTerm        executionLeaseTerm
	testID, projectID  string
	recoveryBytes      int
	budget             int64
	releasedAt         time.Time // guarded by liveTablesMu
	closed             bool
	ready              bool
	lastNotification   atomic.Int64
	notificationSeq    atomic.Uint64
}
type runLiveTable struct {
	table                     *livetable.Table
	binding                   storage.ScenarioDataPoolBinding
	record                    storage.LiveTableRecord
	seed                      storage.LiveDataPoolSeedIdentity
	cipher                    *liveTableCipher
	checkpointMu              sync.Mutex
	lastCheckpoint            uint64
	pendingCheckpointRevision uint64
	pendingCheckpoint         []byte
	final                     *storage.LiveTableFinalizationResult
	summary                   livetable.Stats
}
type liveTableSeedManifest struct {
	Binding  storage.ScenarioDataPoolBinding  `json:"binding"`
	Identity storage.LiveDataPoolSeedIdentity `json:"identity"`
	Columns  []string                         `json:"columns"`
	Rows     []livetable.Row                  `json:"rows"`
}
type liveTablePrivateMetadata struct {
	Version int                 `json:"version"`
	Key     *keywrap.WrappedKey `json:"key,omitempty"`
	Seed    json.RawMessage     `json:"seed"`
}
type liveTableClientFunc func(context.Context, string, livetable.Request) (livetable.Result, error)

func (f liveTableClientFunc) Execute(ctx context.Context, id string, r livetable.Request) (livetable.Result, error) {
	return f(ctx, id, r)
}
func hasLiveDataPools(bs []storage.ScenarioDataPoolBinding) bool {
	for _, b := range bs {
		if b.AccessMode == "live" {
			return true
		}
	}
	return false
}
func hasSnapshotDataPools(bs []storage.ScenarioDataPoolBinding) bool {
	for _, b := range bs {
		if b.AccessMode != "live" {
			return true
		}
	}
	return false
}
func liveTableID(runID, alias string) string {
	h := sha256.Sum256([]byte(runID + "\x00" + strings.ToLower(alias)))
	return "live-" + hex.EncodeToString(h[:16])
}
func liveTableAAD(id, kind string, revision uint64) string {
	return "apexload/live-table/v1/" + id + "/" + kind + "/" + strconv.FormatUint(revision, 10)
}
func liveStorageTerm(term executionLeaseTerm) storage.LiveTableExecutionTerm {
	if !term.valid() {
		return storage.LiveTableExecutionTerm{}
	}
	return storage.LiveTableExecutionTerm{LeaseName: executionLeaseName, OwnerID: term.ownerID, Generation: term.generation}
}
func (tr *TestRunner) liveStore() (storage.LiveTableStore, storage.LiveTableSeedStore, error) {
	state, ok := tr.dataPoolStore.(storage.LiveTableStore)
	seed, seedOK := tr.dataPoolStore.(storage.LiveTableSeedStore)
	if !ok || !seedOK {
		return nil, nil, errors.New("live table storage is unavailable")
	}
	return state, seed, nil
}
func liveTableReservation(o *storage.LiveTableOptions) int64 {
	// Logical state includes data, receipts, and bounded identity/queue metadata.
	return o.MaxBytes + 6<<20
}

// Caller holds liveTablesMu. Recovery reserves before decoding; replacement
// removes the old installed state only after the replacement has fully replayed.
func (tr *TestRunner) liveTableBudgetError(projectID string, additional int64, excludeID string) error {
	total := tr.liveTablesRecoveryReserved + additional
	project := tr.liveTablesRecoveryByProject[projectID] + additional
	for id, run := range tr.liveTables {
		if id == excludeID {
			continue
		}
		total += run.budget
		if run.projectID == projectID {
			project += run.budget
		}
	}
	for _, instance := range tr.projectLiveTables {
		total += instance.budget
		if instance.projectID == projectID {
			project += instance.budget
		}
	}
	if project > liveTableProjectBudget {
		return errors.New("live table project memory budget is exhausted")
	}
	if total > liveTableControllerBudget {
		return errors.New("live table controller memory budget is exhausted")
	}
	return nil
}

func liveCoreLimits(o *storage.LiveTableOptions) livetable.Limits {
	return livetable.Limits{
		MaxBytes: o.MaxBytes, MaxRows: o.MaxRows, MaxFields: 100000, MaxColumns: 128, MaxFieldBytes: 16 << 10, MaxRequestBytes: 256 << 10, MaxEntryBytes: 2 << 20, MaxResultBytes: o.MaxResultBytes,
		MaxReceipts: o.MaxReceipts, MaxReceiptBytes: 2 << 20, MaxClients: 16384, MaxWaiters: 256, MaxScanCells: 100000, MaxSnapshotBytes: int(liveTableReservation(o)),
		ReceiptRetention: time.Duration(o.RetryWindowMS) * time.Millisecond, MaxWait: 60 * time.Second, DefaultLeaseDuration: time.Duration(o.LeaseTTLMS) * time.Millisecond,
		MaxLeaseDuration: time.Duration(o.LeaseTTLMS) * time.Millisecond, MaxLeaseLifetime: time.Duration(o.MaxLeaseLifetimeMS) * time.Millisecond, MaxDeliveryAttempts: uint32(o.MaxDeliveryAttempts),
	}
}
func liveEngineBinding(b storage.ScenarioDataPoolBinding, id string, columns []string) engine.LiveTableBinding {
	o := b.LiveOptions
	return engine.LiveTableBinding{Alias: b.Alias, TableID: id, Strategy: b.SelectionStrategy, Profile: livetable.Profile(o.Profile), Columns: append([]string(nil), columns...), Wait: time.Duration(o.WaitMS) * time.Millisecond, LeaseDuration: time.Duration(o.LeaseTTLMS) * time.Millisecond, EmptyPolicy: o.EmptyPolicy, OpTimeout: time.Duration(o.OperationTimeoutMS) * time.Millisecond}
}

// prepareLiveTables is called once at the launch boundary, after start admission
// and before any engine or generator is allowed to execute. It never holds a runner-wide mutex
// while seeding, wrapping keys, or writing storage.
func (tr *TestRunner) prepareLiveTables(ctx context.Context, test *ManagedTest) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tr.mu.RLock()
	bindings := cloneScenarioDataPoolBindings(test.DataPools)
	testID, projectID := test.ID, test.ProjectID
	assignment := test.Assignment
	tr.mu.RUnlock()
	if !hasLiveDataPools(bindings) {
		return nil
	}
	if err := validateLiveTableContinuation(bindings, assignment); err != nil {
		return err
	}
	normalized, err := normalizeDataPoolBindingIntent(bindings)
	if err != nil {
		return err
	}
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	state, seedStore, err := tr.liveStore()
	if err != nil {
		return err
	}
	if err := tr.validateLiveTablePolicy(projectID); err != nil {
		return err
	}
	tr.liveTablesMu.Lock()
	if existing := tr.liveTables[testID]; existing != nil {
		tr.liveTablesMu.Unlock()
		if !existing.mu.TryRLock() {
			return errors.New("live table initialization is in progress")
		}
		defer existing.mu.RUnlock()
		if !existing.ready || existing.closed {
			return errors.New("live table initialization is incomplete")
		}
		if existing.term != term {
			return errors.New("live table execution owner changed")
		}
		return nil
	}
	run := &runLiveTables{tables: map[string]*runLiveTable{}, term: term, testID: testID, projectID: projectID}
	for _, b := range normalized {
		if b.AccessMode == "live" && b.Scope != "project" {
			run.budget += liveTableReservation(b.LiveOptions)
		}
	}
	if run.budget > liveTableRunBudget {
		tr.liveTablesMu.Unlock()
		return errors.New("live tables exceed the aggregate run memory budget")
	}
	if err := tr.liveTableBudgetError(projectID, run.budget, ""); err != nil {
		tr.liveTablesMu.Unlock()
		return err
	}
	// Reserve before any allocation or I/O. Only this run's lock remains held
	// while preparing it, so unrelated operations and lease renewals can proceed.
	run.mu.Lock()
	defer run.mu.Unlock()
	if tr.liveTables == nil {
		tr.liveTables = map[string]*runLiveTables{}
	}
	tr.liveTables[testID] = run
	tr.liveTablesMu.Unlock()
	existingRecords, err := state.ListLiveTablesForRun(ctx, testID, projectID)
	if err != nil {
		return err
	}
	if len(existingRecords) > 0 {
		return errors.New("live table launch was already initialized; recovery or a new run is required")
	}
	for _, b := range normalized {
		if b.AccessMode != "live" || b.Scope == "project" {
			continue
		}
		o := b.LiveOptions
		if o.Durability == "durable" && (!term.valid() || tr.continuityKeyWrapper == nil) {
			return errors.New("durable live tables require execution fencing and a configured wrapping key")
		}
		if o.EndMode != "discard" {
			if err := tr.authorizeLiveTablePersistence(test); err != nil {
				return err
			}
		}
		if o.EndMode != "discard" && tr.continuityKeyWrapper == nil {
			return errors.New("saving live table output requires a configured wrapping key")
		}
		manifest := liveTableSeedManifest{Binding: b, Columns: append([]string(nil), o.Columns...)}
		if o.SeedPolicy == "original_seed" {
			original, err := tr.originalLiveSeed(ctx, o.SeedRunID, projectID, b.Alias)
			if err != nil {
				return err
			}
			manifest.Identity = original.Identity
			manifest.Columns = original.Columns
			manifest.Rows = original.Rows
		} else if o.SeedPolicy != "empty" {
			seed, err := seedStore.ReadLiveDataPoolSeed(ctx, b.PoolID, projectID, o.MaxRows, liveTableSeedBytes)
			if err != nil {
				return fmt.Errorf("seed live table %q: %w", b.Alias, err)
			}
			manifest.Identity = seed.Identity
			known := map[string]bool{}
			for _, c := range manifest.Columns {
				known[c] = true
			}
			extra := map[string]bool{}
			for _, row := range seed.Rows {
				values := map[string]string{}
				for k, v := range row.Data {
					values[k] = fmt.Sprint(v)
					if !known[k] {
						extra[k] = true
					}
				}
				manifest.Rows = append(manifest.Rows, livetable.Row{ID: row.ID, Version: uint64(max(1, row.Version)), Values: values})
			}
			for c := range seed.Identity.Schema {
				if !known[c] {
					extra[c] = true
				}
			}
			// The pool's own column order first, then anything else sorted.
			for _, c := range seed.Columns {
				if extra[c] {
					manifest.Columns = append(manifest.Columns, c)
					delete(extra, c)
				}
			}
			keys := make([]string, 0, len(extra))
			for k := range extra {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			manifest.Columns = append(manifest.Columns, keys...)
		}
		if o.EndMode == "write_back" && manifest.Identity.PoolID != b.PoolID {
			return errors.New("live table writeback must use its original source pool")
		}
		id := liveTableID(testID, b.Alias)
		cipher, err := newLiveTableCipher(ctx, tr.continuityKeyWrapper, id)
		if err != nil {
			return errors.New("live table recovery key could not be prepared")
		}
		seedBody, err := json.Marshal(manifest)
		if err != nil || len(seedBody) > liveTableSeedBytes {
			return errors.New("live table seed exceeds the recovery seed budget")
		}
		digest := sha256.Sum256(seedBody)
		clearRunSnapshotBytes(seedBody)
		encryptedSeed, err := cipher.seal(manifest, liveTableAAD(id, "seed", 0))
		if err != nil {
			return err
		}
		metadata, err := json.Marshal(liveTablePrivateMetadata{Version: 1, Key: cipher.wrapped, Seed: encryptedSeed})
		if err != nil {
			return err
		}
		rt := &runLiveTable{binding: b, seed: manifest.Identity, cipher: cipher, record: storage.LiveTableRecord{ID: id, TestID: testID, ProjectID: projectID, RunIncarnation: testID, Alias: b.Alias, Profile: o.Profile, Durability: o.Durability, State: "active", SeedDigest: firstNonEmpty(manifest.Identity.Digest, hex.EncodeToString(digest[:])), SeedMetadata: string(metadata)}}
		var committer livetable.Committer
		if o.Durability == "durable" {
			committer = tr.liveCommitter(rt, term)
		}
		table, err := livetable.New(livetable.Config{ID: id, RunID: testID, Profile: livetable.Profile(o.Profile), Columns: manifest.Columns, Rows: manifest.Rows, Limits: liveCoreLimits(o), Committer: committer})
		if err != nil {
			return fmt.Errorf("initialize live table %q: %w", b.Alias, err)
		}
		rt.table = table
		snapshot, err := table.Snapshot()
		if err != nil {
			return err
		}
		sealed, err := cipher.seal(snapshot, liveTableAAD(id, "checkpoint", 0))
		if err != nil {
			return err
		}
		record, err := state.InitializeLiveTable(ctx, rt.record, sealed, liveStorageTerm(term))
		if err != nil {
			return err
		}
		rt.record = *record
		run.tables[id] = rt
		run.bindings = append(run.bindings, liveEngineBinding(b, id, manifest.Columns))
	}
	if err := tr.projectLiveTableService().attachRun(ctx, test, run, normalized, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	run.ready = true
	return nil
}
func (tr *TestRunner) liveCommitter(rt *runLiveTable, term executionLeaseTerm) livetable.Committer {
	return livetable.CommitFunc(func(ctx context.Context, e livetable.Entry) error {
		if !tr.executionLeaseTermLocallyCurrent(term) {
			return errors.New("live table execution ownership expired")
		}
		state, _, err := tr.liveStore()
		if err != nil {
			return err
		}
		payload, err := rt.cipher.seal(e, liveTableAAD(e.TableID, "entry", e.Revision))
		if err != nil {
			return err
		}
		return state.AppendLiveTableEntry(ctx, storage.LiveTableEntry{TableID: e.TableID, Revision: e.Revision, PreviousRevision: e.PreviousRevision, At: e.At, Payload: payload}, liveStorageTerm(term))
	})
}
func (tr *TestRunner) liveRun(id string) *runLiveTables {
	tr.liveTablesMu.Lock()
	defer tr.liveTablesMu.Unlock()
	return tr.liveTables[id]
}
func (tr *TestRunner) liveBindings(id string) []engine.LiveTableBinding {
	run := tr.liveRun(id)
	if run == nil {
		return nil
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	if !run.ready {
		return nil
	}
	return append([]engine.LiveTableBinding(nil), run.bindings...)
}
func (tr *TestRunner) liveBindingJSON(id string) []byte {
	bindings := tr.liveBindings(id)
	if len(bindings) == 0 {
		return nil
	}
	b, _ := json.Marshal(bindings)
	return b
}
func (tr *TestRunner) liveClient(testID, scope string) livetable.Client {
	return liveTableClientFunc(func(ctx context.Context, id string, req livetable.Request) (livetable.Result, error) {
		run := tr.liveRun(testID)
		if run == nil {
			return livetable.Result{}, errors.New("live tables are unavailable")
		}
		run.mu.RLock()
		rt := run.tables[id]
		attachment, projectBound := run.projectAttachments[id]
		var table *livetable.Table
		if rt != nil {
			table = rt.table
		}
		closed := run.closed || !run.ready
		run.mu.RUnlock()
		if closed || !tr.executionLeaseTermLocallyCurrent(run.term) {
			return livetable.Result{}, errors.New("live table execution owner is no longer serving")
		}
		if projectBound {
			return tr.projectLiveTableService().executeAttachment(ctx, run, attachment, scope, req)
		}
		if rt == nil || table == nil {
			return livetable.Result{}, errors.New("live table is outside this run")
		}
		if len(req.ClientID) > 256 || len(req.OwnerID) > 256 {
			return livetable.Result{}, errors.New("invalid live table client identity")
		}
		prefix := func(value string) string {
			h := sha256.Sum256([]byte(scope + "\x00" + value))
			return hex.EncodeToString(h[:])
		}
		if req.ClientID != "" {
			req.ClientID = prefix(req.ClientID)
		}
		if req.OwnerID != "" {
			req.OwnerID = prefix(req.OwnerID)
		}
		result, err := table.Execute(ctx, req)
		if !tr.executionLeaseTermLocallyCurrent(run.term) {
			return livetable.Result{}, &livetable.Error{Code: livetable.CodeUnavailable, Message: "live table owner changed before its response", OutcomeUnknown: req.Operation != livetable.OpRead && req.Operation != livetable.OpReadColumn}
		}
		if rt.binding.LiveOptions.Durability == "durable" {
			tr.checkpointLiveTable(ctx, run, rt, false)
		}
		tr.notifyLiveTableChange(run, time.Now())
		return result, err
	})
}
func (tr *TestRunner) checkpointLiveTable(ctx context.Context, run *runLiveTables, rt *runLiveTable, force bool) error {
	if !rt.checkpointMu.TryLock() {
		if !force {
			return nil
		}
		rt.checkpointMu.Lock()
	}
	defer rt.checkpointMu.Unlock()
	stats := rt.table.Stats()
	if stats.Revision == rt.lastCheckpoint {
		return nil
	}
	if !force && stats.Revision-rt.lastCheckpoint < 8 {
		return nil
	}
	snapshot, err := rt.table.Snapshot()
	if err != nil {
		return err
	}
	payload := rt.pendingCheckpoint
	if payload == nil || rt.pendingCheckpointRevision != snapshot.Revision {
		payload, err = rt.cipher.seal(snapshot, liveTableAAD(rt.record.ID, "checkpoint", snapshot.Revision))
		if err != nil {
			return err
		}
		rt.pendingCheckpointRevision, rt.pendingCheckpoint = snapshot.Revision, payload
	}
	state, _, err := tr.liveStore()
	if err != nil {
		return err
	}
	err = state.PublishLiveTableCheckpoint(ctx, rt.record.ID, snapshot.Revision, payload, liveStorageTerm(run.term))
	if err == nil {
		rt.lastCheckpoint = snapshot.Revision
		rt.pendingCheckpoint = nil
	}
	return err
}

func (tr *TestRunner) restoreLiveTables(ctx context.Context, test *ManagedTest, term executionLeaseTerm) error {
	if !hasLiveDataPools(test.DataPools) {
		return nil
	}
	state, seedStore, err := tr.liveStore()
	if err != nil {
		return err
	}
	records, err := state.ListLiveTablesForRun(ctx, test.ID, test.ProjectID)
	if err != nil {
		return err
	}
	expected := 0
	for _, b := range test.DataPools {
		if b.AccessMode == "live" && b.Scope != "project" {
			expected++
		}
	}
	if len(records) != expected {
		return errors.New("live table recovery manifest is incomplete")
	}
	run := &runLiveTables{tables: map[string]*runLiveTable{}, term: term, testID: test.ID, projectID: test.ProjectID}
	normalized, err := normalizeDataPoolBindingIntent(test.DataPools)
	if err != nil {
		return err
	}
	for _, binding := range normalized {
		if binding.AccessMode == "live" && binding.Scope != "project" {
			run.budget += liveTableReservation(binding.LiveOptions)
		}
	}
	if run.budget > liveTableRunBudget {
		return errors.New("live table recovery run budget exceeded")
	}
	tr.liveTablesMu.Lock()
	if err := tr.liveTableBudgetError(test.ProjectID, run.budget, ""); err != nil {
		tr.liveTablesMu.Unlock()
		return err
	}
	tr.liveTablesRecoveryReserved += run.budget
	if tr.liveTablesRecoveryByProject == nil {
		tr.liveTablesRecoveryByProject = make(map[string]int64)
	}
	tr.liveTablesRecoveryByProject[test.ProjectID] += run.budget
	tr.liveTablesMu.Unlock()
	defer func() {
		tr.liveTablesMu.Lock()
		tr.liveTablesRecoveryReserved -= run.budget
		tr.liveTablesRecoveryByProject[test.ProjectID] -= run.budget
		if tr.liveTablesRecoveryByProject[test.ProjectID] == 0 {
			delete(tr.liveTablesRecoveryByProject, test.ProjectID)
		}
		tr.liveTablesMu.Unlock()
	}()
	var actualBudget int64
	var recoveryBytes int
	interruptedFinalization := false
	continuationErr := validateLiveTableContinuation(test.DataPools, test.Assignment)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if record.Durability != "durable" || (record.State != "active" && record.State != "closed") {
			return errors.New("live table is not recoverable")
		}
		if err := state.ClaimLiveTable(ctx, record.ID, liveStorageTerm(term)); err != nil {
			return err
		}
		recovery, err := state.ReadLiveTableRecovery(ctx, record.ID, test.ProjectID)
		if err != nil {
			return err
		}
		recoveryBytes += len(recovery.Checkpoint) + len(record.SeedMetadata)
		for _, entry := range recovery.Entries {
			recoveryBytes += len(entry.Payload)
		}
		if recoveryBytes > liveTableRecoveryBytes {
			return errors.New("live table aggregate recovery byte budget exceeded")
		}
		var metadata liveTablePrivateMetadata
		if err := json.Unmarshal([]byte(record.SeedMetadata), &metadata); err != nil || metadata.Version != 1 {
			return errors.New("invalid live table recovery metadata")
		}
		cipher, err := restoreLiveTableCipher(ctx, tr.continuityKeyWrapper, record.ID, metadata.Key)
		if err != nil {
			return fmt.Errorf("%w: live table recovery key is unavailable", errRunAdoptionKeyAccess)
		}
		var manifest liveTableSeedManifest
		if err := cipher.open(metadata.Seed, liveTableAAD(record.ID, "seed", 0), &manifest); err != nil {
			return err
		}
		if manifest.Binding.LiveOptions == nil || manifest.Binding.AccessMode != "live" || liveTableID(test.ID, manifest.Binding.Alias) != record.ID {
			return errors.New("live table recovery binding mismatch")
		}
		actualBudget += liveTableReservation(manifest.Binding.LiveOptions)
		if actualBudget > run.budget {
			return errors.New("live table recovery exceeds its reservation")
		}
		rt := &runLiveTable{binding: manifest.Binding, seed: manifest.Identity, cipher: cipher, record: recovery.Table, lastCheckpoint: recovery.Table.CheckpointRevision}
		var snapshot livetable.Snapshot
		if err := cipher.open(recovery.Checkpoint, liveTableAAD(record.ID, "checkpoint", recovery.Table.CheckpointRevision), &snapshot); err != nil {
			return err
		}
		table, err := livetable.Restore(snapshot, tr.liveCommitter(rt, term))
		if err != nil {
			return err
		}
		rt.table = table
		for _, stored := range recovery.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			var entry livetable.Entry
			if err := cipher.open(stored.Payload, liveTableAAD(record.ID, "entry", stored.Revision), &entry); err != nil {
				return err
			}
			if err := table.Replay(entry); err != nil {
				return err
			}
		}
		closed := table.Stats().Closed
		if !closed {
			table.RecordRecoveryGap()
		}
		if recovery.Table.State == "closed" && !closed {
			return errors.New("closed live table recovery checkpoint is not closed")
		}
		if closed {
			// Close commits before the storage lifecycle marker. Either durable
			// witness means this run must never resume target work.
			interruptedFinalization = true
			final, err := seedStore.GetLiveTableFinalization(ctx, record.ID, test.ProjectID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			rt.final = final
		}
		run.tables[record.ID] = rt
		run.bindings = append(run.bindings, liveEngineBinding(manifest.Binding, record.ID, manifest.Columns))

	}
	run.recoveryBytes = recoveryBytes
	if err := tr.projectLiveTableService().attachRun(ctx, test, run, normalized, true); err != nil {
		return err
	}
	if run.budget > liveTableRunBudget {
		return errors.New("live table recovery run budget exceeded")
	}
	tr.liveTablesMu.Lock()
	if err := tr.liveTableBudgetError(test.ProjectID, 0, test.ID); err != nil {
		tr.liveTablesMu.Unlock()
		return err
	}
	if tr.liveTables == nil {
		tr.liveTables = map[string]*runLiveTables{}
	}
	run.ready = true
	run.closed = interruptedFinalization || continuationErr != nil
	tr.liveTables[test.ID] = run
	tr.liveTablesMu.Unlock()
	if run.closed {
		// Reconciliation closes only table data. It neither resumes generators
		// nor bypasses the existing positive generator-stop proof. FAILED keeps
		// reusable pools unchanged while preserving output for explicit recovery.
		cleanupErr := tr.finalizeLiveTables(ctx, test, "FAILED")
		if cleanupErr != nil {
			tr.enqueueTerminalRetry("live-tables:"+test.ID, "live table recovery finalization", func() error {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				return tr.finalizeLiveTables(cleanupCtx, test, "FAILED")
			})
		}
		if interruptedFinalization {
			return errors.Join(errLiveTableFinalizationInterrupted, continuationErr, cleanupErr)
		}
		return errors.Join(continuationErr, cleanupErr)
	}
	return nil
}

func (tr *TestRunner) finalizeLiveTables(ctx context.Context, test *ManagedTest, terminalStatuses ...string) error {
	terminalStatus := test.Status
	if len(terminalStatuses) > 0 {
		terminalStatus = terminalStatuses[0]
	}
	run := tr.liveRun(test.ID)
	if run == nil {
		return nil
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	state, seedStore, err := tr.liveStore()
	if err != nil {
		return err
	}
	if !tr.executionLeaseTermLocallyCurrent(run.term) {
		return errors.New("live table finalization owner expired")
	}
	run.closed = true
	if err := tr.projectLiveTableService().detachRun(ctx, run); err != nil {
		return err
	}
	// Close and checkpoint every table before applying any reusable-pool policy.
	// A permission/conflict on one output cannot leave another table accepting work.
	for _, rt := range run.tables {
		if rt.table == nil {
			if rt.final != nil {
				continue
			}
			return errors.New("live table finalization snapshot is unavailable")
		}
		if !run.ready {
			rt.binding.LiveOptions.EndMode = "discard"
		}
		if rt.record.State != "closed" {
			if err := rt.table.Close(ctx, rt.binding.LiveOptions.EndMode == "discard"); err != nil {
				return err
			}
			if err := tr.checkpointLiveTable(ctx, run, rt, true); err != nil {
				return err
			}
			if err := state.CloseLiveTable(ctx, rt.record.ID, rt.table.Stats().Revision, liveStorageTerm(run.term)); err != nil {
				return err
			}
			rt.record.State = "closed"
		}
		rt.summary = rt.table.Stats()
	}
	for _, rt := range run.tables {
		if rt.final != nil && (rt.final.State != "pending" || rt.final.Reason == "run_not_complete" || rt.final.Reason == "output_limits") {
			if err := tr.auditLiveTableFinalization(test, rt, "completed"); err != nil {
				return err
			}
			continue
		}
		rt.final = nil
		if rt.table == nil {
			return errors.New("live table finalization snapshot is unavailable")
		}
		stats := rt.summary
		mode := rt.binding.LiveOptions.EndMode
		pending := func(reason string) {
			rt.final = &storage.LiveTableFinalizationResult{TableID: rt.record.ID, Mode: mode, State: "pending", Reason: reason}
		}
		if mode != "discard" && terminalStatus != "COMPLETE" {
			rt.final = &storage.LiveTableFinalizationResult{TableID: rt.record.ID, Mode: mode, State: "pending", Reason: "run_not_complete"}
			rt.summary = stats
			continue
		}
		if mode != "discard" {
			if err := tr.validateLiveTablePolicy(run.projectID); err != nil {
				pending("policy_disabled")
				return err
			}
			if err := tr.authorizeLiveTablePersistence(test); err != nil {
				pending("permission_unavailable")
				return err
			}
		}
		req := storage.LiveTableFinalizationRequest{TableID: rt.record.ID, ProjectID: run.projectID, Revision: stats.Revision, Mode: mode, OutputPoolName: rt.binding.LiveOptions.OutputPoolName, Seed: rt.seed, Schema: map[string]string{}}
		if mode != "discard" {
			rows, columns, err := liveTableExportRows(rt.table, stats.Revision)
			if err != nil {
				return err
			}
			for _, column := range columns {
				req.Schema[column] = "string"
			}
			req.Columns = columns
			for _, row := range rows {
				status := "available"
				if row.State == "quarantined" {
					status = "disabled"
				}
				req.Rows = append(req.Rows, &storage.SharedDataPoolRow{ID: row.ID, Data: row.Values, Status: status, Version: int(row.Version)})
			}
			// Sparse live rows can fit the table budget while their rectangular
			// snapshot representation exceeds the ordinary pool execution limits.
			// Apply the same admission as pool edits before publishing any output.
			if err := validateExecutableDataPoolRows(req.Rows); err != nil {
				pending("output_limits")
				// The table is closed: retries cannot change this shape. Keep the
				// checkpoint exportable and release its runtime on the normal timer.
				continue
			}
		}
		if err := tr.auditLiveTableFinalization(test, rt, "admitted"); err != nil {
			pending("audit_unavailable")
			return err
		}
		result, err := seedStore.FinalizeLiveTable(ctx, req, liveStorageTerm(run.term))
		if err != nil {
			return err
		}
		rt.final = result
		rt.summary = rt.table.Stats()
		if err := tr.auditLiveTableFinalization(test, rt, "completed"); err != nil {
			return err
		}
	}
	cleanup := func() { tr.releaseLiveTableRuntime(run) }
	// run.mu is held here; cleanup acquires it asynchronously.
	if run.ready {
		time.AfterFunc(5*time.Minute, cleanup)
	} else {
		go cleanup()
	}
	return nil
}

// releaseLiveTableRuntime retains a bounded, value-free recent summary cache.
// Storage remains authoritative after eviction, including finalization results.
func (tr *TestRunner) releaseLiveTableRuntime(run *runLiveTables) {
	run.mu.Lock()
	defer run.mu.Unlock()
	for id, rt := range run.tables {
		// Replace handles so operations already draining after Close remain safe.
		record := rt.record
		record.SeedMetadata = ""
		run.tables[id] = &runLiveTable{binding: rt.binding, record: record, final: rt.final, summary: rt.summary}
	}
	tr.liveTablesMu.Lock()
	defer tr.liveTablesMu.Unlock()
	run.budget = 0
	run.releasedAt = time.Now()
	var released []*runLiveTables
	for _, candidate := range tr.liveTables {
		if !candidate.releasedAt.IsZero() {
			released = append(released, candidate)
		}
	}
	sort.Slice(released, func(i, j int) bool { return released[i].releasedAt.Before(released[j].releasedAt) })
	for len(released) > liveTableRetainedRuns {
		delete(tr.liveTables, released[0].testID)
		released = released[1:]
	}
}

func (tr *TestRunner) originalLiveSeed(ctx context.Context, sourceID, projectID, alias string) (liveTableSeedManifest, error) {
	var manifest liveTableSeedManifest
	state, _, err := tr.liveStore()
	if err != nil {
		return manifest, err
	}
	records, err := state.ListLiveTablesForRun(ctx, sourceID, projectID)
	if err != nil {
		return manifest, err
	}
	for _, record := range records {
		if record.Alias != alias {
			continue
		}
		var metadata liveTablePrivateMetadata
		if err := json.Unmarshal([]byte(record.SeedMetadata), &metadata); err != nil || metadata.Version != 1 {
			return manifest, errors.New("original live seed is invalid")
		}
		var cipher *liveTableCipher
		if run := tr.liveRun(sourceID); run != nil {
			// Never wait on another run while holding the initializing run's
			// lock: mutually referenced seeds must fail instead of deadlocking.
			if !run.mu.TryRLock() {
				return manifest, errors.New("original live seed is busy; retry after the source run settles")
			}
			if rt := run.tables[record.ID]; rt != nil {
				cipher = rt.cipher
			}
			run.mu.RUnlock()
		}
		if cipher == nil {
			cipher, err = restoreLiveTableCipher(ctx, tr.continuityKeyWrapper, record.ID, metadata.Key)
			if err != nil {
				return manifest, errors.New("original live seed cannot be recovered; explicitly reseed from the current pool")
			}
		}
		if err := cipher.open(metadata.Seed, liveTableAAD(record.ID, "seed", 0), &manifest); err != nil {
			return manifest, err
		}
		return manifest, nil
	}
	return manifest, errors.New("original live seed is unavailable in this project")
}

func (tr *TestRunner) finishLiveTables(test *ManagedTest, finalStatus string) {
	if tr.liveRun(test.ID) == nil {
		return
	}
	finish := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return tr.finalizeLiveTables(ctx, test, finalStatus)
	}
	if err := finish(); err != nil {
		// Finalization status remains separately visible and retryable. A pool
		// conflict must not discard the run's performance evidence.
		tr.enqueueTerminalRetry("live-tables:"+test.ID, "live table finalization", finish)
	}
}

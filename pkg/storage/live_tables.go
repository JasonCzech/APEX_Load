package storage

// Live-table persistence deliberately stores opaque, bounded recovery envelopes.
// The runtime owns semantics and encryption; these bytes are never an evidence
// projection and must not be served by ordinary data-pool or archive endpoints.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	MaxLiveTableEntryBytes      = 4 << 20
	MaxLiveTableCheckpointBytes = 32 << 20
	MaxLiveTableRecoveryBytes   = 64 << 20
	MaxLiveTableJournalEntries  = 50000
)

var (
	ErrLiveTableFenced   = errors.New("live table execution owner fenced")
	ErrLiveTableConflict = errors.New("live table state changed")
	ErrLiveTableCorrupt  = errors.New("live table recovery state is corrupt")
	ErrLiveTableLimit    = errors.New("live table recovery limit exceeded")
	ErrLiveTableClosed   = errors.New("live table is closed")
)

// LiveTableOptions is authored configuration. Nil preserves the historical
// static read-only binding contract; legacy consume/read_write modes do not
// acquire these semantics merely because this field exists.
type LiveTableOptions struct {
	Columns             []string `json:"columns,omitempty"`
	Profile             string   `json:"profile,omitempty"`
	SeedPolicy          string   `json:"seed_policy,omitempty"`
	SeedRunID           string   `json:"seed_run_id,omitempty"`
	Durability          string   `json:"durability,omitempty"`
	EndMode             string   `json:"end_mode,omitempty"`
	EmptyPolicy         string   `json:"empty_policy,omitempty"`
	WaitMS              int64    `json:"wait_ms,omitempty"`
	OperationTimeoutMS  int64    `json:"operation_timeout_ms,omitempty"`
	LeaseTTLMS          int64    `json:"lease_ttl_ms,omitempty"`
	MaxLeaseLifetimeMS  int64    `json:"max_lease_lifetime_ms,omitempty"`
	MaxDeliveryAttempts int      `json:"max_delivery_attempts,omitempty"`
	MaxRows             int      `json:"max_rows,omitempty"`
	MaxBytes            int64    `json:"max_bytes,omitempty"`
	MaxResultBytes      int      `json:"max_result_bytes,omitempty"`
	RetryWindowMS       int64    `json:"retry_window_ms,omitempty"`
	MaxReceipts         int      `json:"max_receipts,omitempty"`
	OutputPoolName      string   `json:"output_pool_name,omitempty"`
}

// LiveTableExecutionTerm is checked against the scheduler lease using database
// time in every modifying transaction. The all-zero term is for an explicitly
// memory-only local lifecycle and can never claim a durable table.
type LiveTableExecutionTerm struct {
	LeaseName  string
	OwnerID    string
	Generation uint64
}

type LiveTableRecord struct {
	ID                 string    `json:"id"`
	TestID             string    `json:"test_id"`
	ProjectID          string    `json:"project_id"`
	RunIncarnation     string    `json:"run_incarnation"`
	Alias              string    `json:"alias"`
	Profile            string    `json:"profile"`
	Durability         string    `json:"durability"`
	State              string    `json:"state"`
	SeedDigest         string    `json:"seed_digest"`
	SeedMetadata       string    `json:"-"`
	OwnerLeaseName     string    `json:"-"`
	OwnerID            string    `json:"-"`
	OwnerGeneration    uint64    `json:"owner_generation"`
	Revision           uint64    `json:"revision"`
	CheckpointRevision uint64    `json:"checkpoint_revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// LiveTableEntry has the same shape as the core's Entry, without coupling the
// storage package to runtime semantics. Payload must include the resolved
// outcomes/receipts as well as all nondeterministic mutation decisions.
type LiveTableEntry struct {
	TableID          string
	Revision         uint64
	PreviousRevision uint64
	At               time.Time
	Payload          []byte
}

type LiveTableRecovery struct {
	Table      LiveTableRecord
	Checkpoint []byte
	Entries    []LiveTableEntry
}

type LiveTableStore interface {
	InitializeLiveTable(context.Context, LiveTableRecord, []byte, LiveTableExecutionTerm) (*LiveTableRecord, error)
	ClaimLiveTable(context.Context, string, LiveTableExecutionTerm) error
	AppendLiveTableEntry(context.Context, LiveTableEntry, LiveTableExecutionTerm) error
	PublishLiveTableCheckpoint(context.Context, string, uint64, []byte, LiveTableExecutionTerm) error
	ReadLiveTableRecovery(context.Context, string, string) (*LiveTableRecovery, error)
	ListLiveTablesForRun(context.Context, string, string) ([]LiveTableRecord, error)
	CloseLiveTable(context.Context, string, uint64, LiveTableExecutionTerm) error
}

var _ LiveTableStore = (*SQLiteStore)(nil)

const liveTableColumns = `id,test_id,project_id,run_incarnation,alias,profile,durability,state,seed_digest,seed_metadata,owner_lease_name,owner_id,owner_generation,revision,checkpoint_revision,created_at,updated_at`

func liveTableHash(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func validateLivePayload(payload []byte, maximum int) error {
	if len(payload) == 0 {
		return errors.New("live table recovery payload is required")
	}
	if len(payload) > maximum {
		return ErrLiveTableLimit
	}
	return nil
}

func (s *SQLiteStore) beginLiveTableTx(ctx context.Context, term LiveTableExecutionTerm) (*storeTx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if term == (LiveTableExecutionTerm{}) {
		return tx, nil
	}
	if strings.TrimSpace(term.LeaseName) == "" || strings.TrimSpace(term.OwnerID) == "" || term.Generation == 0 {
		_ = tx.Rollback()
		return nil, ErrLiveTableFenced
	}
	held, err := schedulerLeaseTermHeldInTransactionContext(ctx, tx, tx.dialect, term.LeaseName, term.OwnerID, term.Generation)
	if err != nil || !held {
		_ = tx.Rollback()
		if err != nil {
			return nil, err
		}
		return nil, ErrLiveTableFenced
	}
	return tx, nil
}

type liveTableScanner interface{ Scan(...any) error }

func scanLiveTable(scanner liveTableScanner) (*LiveTableRecord, error) {
	var r LiveTableRecord
	err := scanner.Scan(&r.ID, &r.TestID, &r.ProjectID, &r.RunIncarnation, &r.Alias, &r.Profile, &r.Durability, &r.State, &r.SeedDigest, &r.SeedMetadata, &r.OwnerLeaseName, &r.OwnerID, &r.OwnerGeneration, &r.Revision, &r.CheckpointRevision, &r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func lockLiveTable(ctx context.Context, tx *storeTx, id string, term LiveTableExecutionTerm) (*LiveTableRecord, error) {
	query := `SELECT ` + liveTableColumns + ` FROM live_tables WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	r, err := scanLiveTable(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return nil, err
	}
	if r.OwnerLeaseName != term.LeaseName || r.OwnerID != term.OwnerID || r.OwnerGeneration != term.Generation || (r.Durability == "durable" && term.Generation == 0) {
		return nil, ErrLiveTableFenced
	}
	return r, nil
}

// InitializeLiveTable seeds exactly once. An identical retry returns the same
// lifecycle; a different incarnation, seed or initial checkpoint is refused.
func (s *SQLiteStore) InitializeLiveTable(ctx context.Context, r LiveTableRecord, checkpoint []byte, term LiveTableExecutionTerm) (*LiveTableRecord, error) {
	if err := validateLivePayload(checkpoint, MaxLiveTableCheckpointBytes); err != nil {
		return nil, err
	}
	if r.ID == "" || r.TestID == "" || r.ProjectID == "" || r.RunIncarnation == "" || r.Alias == "" || r.SeedDigest == "" || len(r.SeedMetadata) > 1<<20 || (r.Profile != "native" && r.Profile != "vts" && r.Profile != "native_queue" && r.Profile != "vts_columns") || (r.Durability != "memory" && r.Durability != "durable") || (r.Durability == "durable" && term.Generation == 0) {
		return nil, errors.New("invalid live table initialization")
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, r.ProjectID); err != nil {
		return nil, err
	}
	var projectID, status string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(project_id,''),status FROM tests WHERE id=?`, r.TestID).Scan(&projectID, &status); err != nil {
		return nil, err
	}
	if normalizeProjectID(projectID) != r.ProjectID {
		return nil, ErrLiveTableConflict
	}
	if status != "INITIALIZING" && status != "STARTING" && status != "RUNNING" && status != "PAUSED" && status != "QUEUED" {
		return nil, ErrLiveTableClosed
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO live_tables (`+liveTableColumns+`,initial_checkpoint_hash,checkpoint_payload,checkpoint_hash,journal_bytes,journal_count) VALUES (?,?,?,?,?,?,?,'active',?,?,?,?,?,0,0,?,?,?,?,?,0,0) ON CONFLICT(id) DO NOTHING`, r.ID, r.TestID, r.ProjectID, r.RunIncarnation, r.Alias, r.Profile, r.Durability, r.SeedDigest, r.SeedMetadata, term.LeaseName, term.OwnerID, term.Generation, now, now, liveTableHash(checkpoint), string(checkpoint), liveTableHash(checkpoint))
	if err != nil {
		return nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	stored, err := scanLiveTable(tx.QueryRowContext(ctx, `SELECT `+liveTableColumns+` FROM live_tables WHERE id=?`, r.ID))
	if err != nil {
		return nil, err
	}
	var initialHash string
	if err := tx.QueryRowContext(ctx, `SELECT initial_checkpoint_hash FROM live_tables WHERE id=?`, r.ID).Scan(&initialHash); err != nil {
		return nil, err
	}
	if stored.TestID != r.TestID || stored.ProjectID != r.ProjectID || stored.RunIncarnation != r.RunIncarnation || stored.Alias != r.Alias || stored.Profile != r.Profile || stored.Durability != r.Durability || stored.SeedDigest != r.SeedDigest || stored.SeedMetadata != r.SeedMetadata || initialHash != liveTableHash(checkpoint) {
		return nil, ErrLiveTableConflict
	}
	if inserted == 0 && (stored.OwnerLeaseName != term.LeaseName || stored.OwnerID != term.OwnerID || stored.OwnerGeneration != term.Generation) {
		return nil, ErrLiveTableFenced
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

// ClaimLiveTable changes only ownership. It never reseeds or clears receipts.
// Closed memory tables may be claimed to finish a persisted output, but active
// memory tables can never be adopted for execution.
func (s *SQLiteStore) ClaimLiveTable(ctx context.Context, id string, term LiveTableExecutionTerm) error {
	if term.Generation == 0 {
		return ErrLiveTableFenced
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT ` + liveTableColumns + ` FROM live_tables WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	r, err := scanLiveTable(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return err
	}
	if (r.Durability != "durable" && r.State != "closed") || r.OwnerGeneration > term.Generation || (r.OwnerGeneration == term.Generation && r.OwnerID != term.OwnerID) {
		return ErrLiveTableFenced
	}
	if r.State != "active" && r.State != "closed" {
		return ErrLiveTableClosed
	}
	if r.OwnerLeaseName != term.LeaseName && !(r.Durability == "memory" && r.State == "closed" && r.OwnerLeaseName == "") {
		return ErrLiveTableFenced
	}
	_, err = tx.ExecContext(ctx, `UPDATE live_tables SET owner_lease_name=?,owner_id=?,owner_generation=?,updated_at=? WHERE id=?`, term.LeaseName, term.OwnerID, term.Generation, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// AppendLiveTableEntry is the publication boundary. The caller must not expose
// speculative state or responses until this transaction has committed.
func (s *SQLiteStore) AppendLiveTableEntry(ctx context.Context, e LiveTableEntry, term LiveTableExecutionTerm) error {
	if e.TableID == "" || e.Revision == 0 || e.Revision != e.PreviousRevision+1 || e.At.IsZero() {
		return errors.New("invalid live table journal entry")
	}
	if err := validateLivePayload(e.Payload, MaxLiveTableEntryBytes); err != nil {
		return err
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := lockLiveTable(ctx, tx, e.TableID, term)
	if err != nil {
		return err
	}
	var existingHash string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM live_table_journal WHERE table_id=? AND revision=?`, e.TableID, e.Revision).Scan(&existingHash)
	if err == nil {
		if existingHash != liveTableHash(e.Payload) {
			return ErrLiveTableConflict
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if r.State != "active" {
		return ErrLiveTableClosed
	}
	if r.Revision != e.PreviousRevision {
		return ErrLiveTableConflict
	}
	var bytes, count int64
	if err := tx.QueryRowContext(ctx, `SELECT journal_bytes,journal_count FROM live_tables WHERE id=?`, e.TableID).Scan(&bytes, &count); err != nil {
		return err
	}
	if bytes+int64(len(e.Payload)) > MaxLiveTableRecoveryBytes || count >= MaxLiveTableJournalEntries {
		return ErrLiveTableLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO live_table_journal(table_id,revision,previous_revision,payload,payload_hash,created_at) VALUES(?,?,?,?,?,?)`, e.TableID, e.Revision, e.PreviousRevision, string(e.Payload), liveTableHash(e.Payload), e.At); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_tables SET revision=?,journal_bytes=journal_bytes+?,journal_count=journal_count+1,updated_at=? WHERE id=?`, e.Revision, len(e.Payload), time.Now().UTC(), e.TableID); err != nil {
		return err
	}
	return tx.Commit()
}

// PublishLiveTableCheckpoint atomically swaps a complete verified recovery
// point and removes only its covered journal prefix. Snapshot serialization
// must include retained receipts and retirement markers, not just row values.
func (s *SQLiteStore) PublishLiveTableCheckpoint(ctx context.Context, id string, revision uint64, payload []byte, term LiveTableExecutionTerm) error {
	if err := validateLivePayload(payload, MaxLiveTableCheckpointBytes); err != nil {
		return err
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := lockLiveTable(ctx, tx, id, term)
	if err != nil {
		return err
	}
	if revision < r.CheckpointRevision || (r.Durability != "memory" && revision > r.Revision) || (r.Durability == "memory" && revision < r.Revision) {
		return ErrLiveTableConflict
	}
	if revision == r.CheckpointRevision {
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT checkpoint_hash FROM live_tables WHERE id=?`, id).Scan(&existing); err != nil {
			return err
		}
		if existing != liveTableHash(payload) {
			return ErrLiveTableConflict
		}
		return tx.Commit()
	}
	if r.State != "active" {
		return ErrLiveTableClosed
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM live_table_journal WHERE table_id=? AND revision<=?`, id, revision); err != nil {
		return err
	}
	var bytes, count int64
	byteLength := `LENGTH(CAST(payload AS BLOB))`
	if tx.dialect == storeDialectPostgres {
		byteLength = `OCTET_LENGTH(payload)`
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+byteLength+`),0),COUNT(*) FROM live_table_journal WHERE table_id=?`, id).Scan(&bytes, &count); err != nil {
		return err
	}
	committedRevision := r.Revision
	if r.Durability == "memory" {
		committedRevision = revision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_tables SET revision=?,checkpoint_revision=?,checkpoint_payload=?,checkpoint_hash=?,journal_bytes=?,journal_count=?,updated_at=? WHERE id=?`, committedRevision, revision, string(payload), liveTableHash(payload), bytes, count, time.Now().UTC(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ReadLiveTableRecovery(ctx context.Context, id, project string) (*LiveTableRecovery, error) {
	// A repeatable snapshot keeps the checkpoint and tail coherent while another
	// controller compacts. SQLite already provides snapshot reads in a transaction.
	var opts *sql.TxOptions
	if s.db.dialect == storeDialectPostgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r, err := scanLiveTable(tx.QueryRowContext(ctx, `SELECT `+liveTableColumns+` FROM live_tables WHERE id=? AND project_id=?`, id, project))
	if err != nil {
		return nil, err
	}
	var payload, hash string
	if err := tx.QueryRowContext(ctx, `SELECT checkpoint_payload,checkpoint_hash FROM live_tables WHERE id=?`, id).Scan(&payload, &hash); err != nil {
		return nil, err
	}
	if len(payload) > MaxLiveTableCheckpointBytes || liveTableHash([]byte(payload)) != hash {
		return nil, ErrLiveTableCorrupt
	}
	out := &LiveTableRecovery{Table: *r, Checkpoint: []byte(payload), Entries: []LiveTableEntry{}}
	rows, err := tx.QueryContext(ctx, `SELECT revision,previous_revision,payload,payload_hash,created_at FROM live_table_journal WHERE table_id=? AND revision>? ORDER BY revision`, id, r.CheckpointRevision)
	if err != nil {
		return nil, err
	}
	expected := r.CheckpointRevision
	total := len(payload)
	for rows.Next() {
		var e LiveTableEntry
		var p, h string
		e.TableID = id
		if err := rows.Scan(&e.Revision, &e.PreviousRevision, &p, &h, &e.At); err != nil {
			rows.Close()
			return nil, err
		}
		total += len(p)
		if e.PreviousRevision != expected || e.Revision != expected+1 || liveTableHash([]byte(p)) != h || len(p) > MaxLiveTableEntryBytes {
			rows.Close()
			return nil, ErrLiveTableCorrupt
		}
		if total > MaxLiveTableRecoveryBytes+MaxLiveTableCheckpointBytes || len(out.Entries) >= MaxLiveTableJournalEntries {
			rows.Close()
			return nil, ErrLiveTableLimit
		}
		e.Payload = []byte(p)
		out.Entries = append(out.Entries, e)
		expected = e.Revision
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if expected != r.Revision {
		return nil, ErrLiveTableCorrupt
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) ListLiveTablesForRun(ctx context.Context, testID, projectID string) ([]LiveTableRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+liveTableColumns+` FROM live_tables WHERE test_id=? AND project_id=? ORDER BY alias,id`, testID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveTableRecord{}
	for rows.Next() {
		r, err := scanLiveTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) CloseLiveTable(ctx context.Context, id string, revision uint64, term LiveTableExecutionTerm) error {
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := lockLiveTable(ctx, tx, id, term)
	if err != nil {
		return err
	}
	if r.Revision != revision {
		return ErrLiveTableConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_tables SET state='closed',updated_at=? WHERE id=?`, time.Now().UTC(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func liveTableSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS live_tables(id TEXT PRIMARY KEY,test_id TEXT NOT NULL,project_id TEXT NOT NULL,run_incarnation TEXT NOT NULL,alias TEXT NOT NULL,profile TEXT NOT NULL,durability TEXT NOT NULL,state TEXT NOT NULL,seed_digest TEXT NOT NULL,seed_metadata TEXT NOT NULL,owner_lease_name TEXT NOT NULL,owner_id TEXT NOT NULL,owner_generation BIGINT NOT NULL,revision BIGINT NOT NULL,checkpoint_revision BIGINT NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,initial_checkpoint_hash TEXT NOT NULL,checkpoint_payload TEXT NOT NULL,checkpoint_hash TEXT NOT NULL,journal_bytes BIGINT NOT NULL,journal_count BIGINT NOT NULL,UNIQUE(test_id,run_incarnation,alias))`,
		`CREATE TABLE IF NOT EXISTS live_table_journal(table_id TEXT NOT NULL,revision BIGINT NOT NULL,previous_revision BIGINT NOT NULL,payload TEXT NOT NULL,payload_hash TEXT NOT NULL,created_at DATETIME NOT NULL,PRIMARY KEY(table_id,revision))`,
		`CREATE TABLE IF NOT EXISTS live_table_finalizations(table_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,mode TEXT NOT NULL,request_digest TEXT NOT NULL,output_pool_id TEXT NOT NULL,state TEXT NOT NULL,result_json TEXT NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL)`,
	}
}

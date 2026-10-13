package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxLiveTableSeedRows = 100000

// LiveDataPoolSeedIdentity is a compare-and-swap witness for active-subset
// writeback. CreatedAt prevents an ID deleted and reused from matching; metadata
// time and schema protect edits which do not advance rows_revision.
type LiveDataPoolSeedIdentity struct {
	PoolID       string            `json:"pool_id"`
	ProjectID    string            `json:"project_id"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	RowsRevision int64             `json:"rows_revision"`
	Schema       map[string]string `json:"schema,omitempty"`
	Digest       string            `json:"digest"`
}

type LiveDataPoolSeed struct {
	Identity     LiveDataPoolSeedIdentity `json:"identity"`
	PoolName     string                   `json:"pool_name"`
	Rows         []*SharedDataPoolRow     `json:"rows"`
	ExcludedRows int                      `json:"excluded_rows"`
	// Columns is the pool's column order (SharedDataPool.Columns).
	Columns []string `json:"columns,omitempty"`
}

type LiveTableFinalizationRequest struct {
	TableID        string
	ProjectID      string
	Revision       uint64
	Mode           string // discard, save_as_new, write_back
	OutputPoolName string
	Seed           LiveDataPoolSeedIdentity
	Rows           []*SharedDataPoolRow
	Schema         map[string]string
	// Columns is the table's column order, kept by a save_as_new pool.
	Columns []string
}

type LiveTableFinalizationResult struct {
	TableID      string `json:"table_id"`
	Mode         string `json:"mode"`
	State        string `json:"state"` // complete or conflict
	OutputPoolID string `json:"output_pool_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type LiveTableSeedStore interface {
	ReadLiveDataPoolSeed(context.Context, string, string, int, int) (*LiveDataPoolSeed, error)
	FinalizeLiveTable(context.Context, LiveTableFinalizationRequest, LiveTableExecutionTerm) (*LiveTableFinalizationResult, error)
	GetLiveTableFinalization(context.Context, string, string) (*LiveTableFinalizationResult, error)
}

var _ LiveTableSeedStore = (*SQLiteStore)(nil)

func (s *SQLiteStore) GetLiveTableFinalization(ctx context.Context, id, projectID string) (*LiveTableFinalizationResult, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT result_json FROM live_table_finalizations WHERE table_id=? AND project_id=?`, id, projectID).Scan(&raw); err != nil {
		return nil, err
	}
	var out LiveTableFinalizationResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.TableID != id || (out.State != "complete" && out.State != "conflict") {
		return nil, ErrLiveTableCorrupt
	}
	return &out, nil
}

func liveSeedRowActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "consumed", "used", "disabled", "inactive", "exhausted":
		return false
	default:
		return true
	}
}

func liveFinalRowStatus(status string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "available":
		return "active", nil
	case "disabled":
		return "disabled", nil
	default:
		return "", errors.New("final live table rows must be active, available or disabled")
	}
}

// ReadLiveDataPoolSeed locks parents in the ordinary data-pool writer order,
// then reads metadata, paging generation and rows in one transaction. Empty
// active seeds are valid for producer-first live tables.
func (s *SQLiteStore) ReadLiveDataPoolSeed(ctx context.Context, poolID, projectID string, maxRows, maxBytes int) (*LiveDataPoolSeed, error) {
	if maxRows <= 0 || maxRows > MaxLiveTableSeedRows || maxBytes <= 0 || maxBytes > MaxLiveTableCheckpointBytes {
		return nil, ErrLiveTableLimit
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	owner, err := lockDataPoolParentForMutation(tx, poolID)
	if err != nil {
		return nil, err
	}
	if owner != normalizeProjectID(projectID) {
		return nil, sql.ErrNoRows
	}
	seed := &LiveDataPoolSeed{Identity: LiveDataPoolSeedIdentity{PoolID: poolID, ProjectID: owner}, Rows: []*SharedDataPoolRow{}}
	var schema, columns string
	var archived sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT name,COALESCE(schema_json,''),COALESCE(columns_json,''),rows_revision,created_at,updated_at,archived_at FROM shared_data_pools WHERE id=?`, poolID).Scan(&seed.PoolName, &schema, &columns, &seed.Identity.RowsRevision, &seed.Identity.CreatedAt, &seed.Identity.UpdatedAt, &archived); err != nil {
		return nil, err
	}
	if archived.Valid {
		return nil, errors.New("live table seed pool is archived")
	}
	if schema != "" {
		if err := json.Unmarshal([]byte(schema), &seed.Identity.Schema); err != nil {
			return nil, errors.New("invalid live table seed schema")
		}
	}
	if columns != "" && columns != "null" {
		if err := json.Unmarshal([]byte(columns), &seed.Columns); err != nil {
			return nil, errors.New("invalid live table seed column order")
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,pool_id,data_json,COALESCE(status,''),COALESCE(version,1),created_at,updated_at FROM shared_data_pool_rows WHERE pool_id=? ORDER BY created_at,id`, poolID)
	if err != nil {
		return nil, err
	}
	bytes, total := len(schema), 0
	for rows.Next() {
		var row SharedDataPoolRow
		var raw string
		if err := rows.Scan(&row.ID, &row.PoolID, &raw, &row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		total++
		bytes += len(raw) + len(row.ID) + len(row.Status) + 96
		if total > maxRows || bytes > maxBytes {
			rows.Close()
			return nil, ErrLiveTableLimit
		}
		if !liveSeedRowActive(row.Status) {
			seed.ExcludedRows++
			continue
		}
		if err := json.Unmarshal([]byte(raw), &row.Data); err != nil {
			rows.Close()
			return nil, errors.New("invalid live table seed row")
		}
		if row.Data == nil {
			row.Data = map[string]string{}
		}
		seed.Rows = append(seed.Rows, &row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	canonical, err := json.Marshal(seed)
	if err != nil {
		return nil, err
	}
	if len(canonical) > maxBytes {
		return nil, ErrLiveTableLimit
	}
	seed.Identity.Digest = liveTableHash(canonical)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return seed, nil
}

func validateLiveFinalization(in LiveTableFinalizationRequest) (string, error) {
	if in.TableID == "" || in.ProjectID == "" {
		return "", errors.New("live table finalization identity is required")
	}
	if in.Mode != "discard" && in.Mode != "save_as_new" && in.Mode != "write_back" {
		return "", errors.New("invalid live table end mode")
	}
	if len(in.Rows) > MaxLiveTableSeedRows {
		return "", ErrLiveTableLimit
	}
	seen := map[string]bool{}
	for _, row := range in.Rows {
		if row == nil {
			return "", errors.New("nil final live table row")
		}
		if row.ID != "" && seen[row.ID] {
			return "", errors.New("duplicate final live table row identity")
		}
		seen[row.ID] = true
		if _, err := liveFinalRowStatus(row.Status); err != nil {
			return "", err
		}
	}
	if in.Mode == "save_as_new" && strings.TrimSpace(in.OutputPoolName) == "" {
		return "", errors.New("output pool name is required")
	}
	if in.Mode == "write_back" && (in.Seed.PoolID == "" || in.Seed.ProjectID != in.ProjectID || in.Seed.CreatedAt.IsZero() || in.Seed.UpdatedAt.IsZero() || in.Seed.Digest == "") {
		return "", errors.New("live table writeback requires its original seed witness")
	}
	canonical, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	if len(canonical) > MaxLiveTableCheckpointBytes {
		return "", ErrLiveTableLimit
	}
	return liveTableHash(canonical), nil
}

// FinalizeLiveTable applies one frozen final snapshot and records its result in
// the same transaction. Permissions, quotas and required audit must be admitted
// by the API before this method. Revision conflicts are durable outcomes; they
// never overwrite concurrent pool edits or silently create a fallback pool.
func (s *SQLiteStore) FinalizeLiveTable(ctx context.Context, in LiveTableFinalizationRequest, term LiveTableExecutionTerm) (*LiveTableFinalizationResult, error) {
	digest, err := validateLiveFinalization(in)
	if err != nil {
		return nil, err
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, in.ProjectID); err != nil {
		return nil, err
	}
	var sourceMissing bool
	if in.Mode == "write_back" {
		owner, err := lockDataPoolParentForMutation(tx, in.Seed.PoolID)
		if errors.Is(err, sql.ErrNoRows) {
			sourceMissing = true
		} else if err != nil {
			return nil, err
		} else if owner != in.ProjectID {
			sourceMissing = true
		}
	}
	table, err := lockLiveTable(ctx, tx, in.TableID, term)
	if err != nil {
		return nil, err
	}
	if table.ProjectID != in.ProjectID {
		return nil, sql.ErrNoRows
	}
	var oldDigest, oldResult string
	resolveConflict := false
	err = tx.QueryRowContext(ctx, `SELECT request_digest,result_json FROM live_table_finalizations WHERE table_id=?`, in.TableID).Scan(&oldDigest, &oldResult)
	if err == nil {
		var result LiveTableFinalizationResult
		if err := json.Unmarshal([]byte(oldResult), &result); err != nil || result.TableID != in.TableID {
			return nil, ErrLiveTableCorrupt
		}
		if oldDigest == digest {
			return &result, tx.Commit()
		}
		// An explicit recovery may preserve the same closed checkpoint in a new
		// pool after writeback refused a concurrent source edit. Completed results
		// remain immutable, and a second recovery returns the same output identity.
		resolveConflict = result.Mode == "write_back" && result.State == "conflict" && in.Mode == "save_as_new"
		if !resolveConflict {
			return nil, ErrLiveTableConflict
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if table.State != "closed" || table.Revision != in.Revision || table.CheckpointRevision != in.Revision {
		return nil, errors.New("live table finalization requires a closed checkpointed revision")
	}
	out := &LiveTableFinalizationResult{TableID: in.TableID, Mode: in.Mode, State: "complete"}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	if in.Mode == "write_back" {
		out.OutputPoolID = in.Seed.PoolID
		if sourceMissing {
			out.State = "conflict"
			out.Reason = "source_pool_unavailable"
		} else if table.SeedDigest != in.Seed.Digest {
			return nil, ErrLiveTableConflict
		} else {
			var revision int64
			var created, updated time.Time
			var schema string
			var archived sql.NullTime
			if err := tx.QueryRowContext(ctx, `SELECT rows_revision,created_at,updated_at,COALESCE(schema_json,''),archived_at FROM shared_data_pools WHERE id=?`, in.Seed.PoolID).Scan(&revision, &created, &updated, &schema, &archived); err != nil {
				return nil, err
			}
			var shape map[string]string
			if schema != "" {
				if err := json.Unmarshal([]byte(schema), &shape); err != nil {
					return nil, err
				}
			}
			currentSchema, _ := json.Marshal(shape)
			seedSchema, _ := json.Marshal(in.Seed.Schema)
			if archived.Valid || revision != in.Seed.RowsRevision || !created.Equal(in.Seed.CreatedAt) || !updated.Equal(in.Seed.UpdatedAt) || string(currentSchema) != string(seedSchema) {
				out.State = "conflict"
				out.Reason = "source_pool_changed"
			} else {
				if err := replaceLiveActiveRowsTx(ctx, tx, in.Seed.PoolID, in.Rows, now); err != nil {
					return nil, err
				}
			}
		}
	} else if in.Mode == "save_as_new" {
		out.OutputPoolID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:live-table-output:"+in.TableID)).String()
		schema, err := json.Marshal(in.Schema)
		if err != nil {
			return nil, err
		}
		columns, err := encodeDataPoolColumns(&SharedDataPool{ID: out.OutputPoolID, Columns: in.Columns})
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO shared_data_pools(id,name,description,schema_json,columns_json,project_id,rows_revision,row_count,created_at,updated_at) VALUES(?,?,'Created from a live data table',?,?,?,0,0,?,?)`, out.OutputPoolID, strings.TrimSpace(in.OutputPoolName), string(schema), columns, in.ProjectID, now, now); err != nil {
			return nil, err
		}
		if err := replaceLiveActiveRowsTx(ctx, tx, out.OutputPoolID, in.Rows, now); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if resolveConflict {
		if _, err := tx.ExecContext(ctx, `UPDATE live_table_finalizations SET mode=?,request_digest=?,output_pool_id=?,state=?,result_json=?,updated_at=? WHERE table_id=?`, in.Mode, digest, out.OutputPoolID, out.State, string(raw), now, in.TableID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO live_table_finalizations(table_id,project_id,mode,request_digest,output_pool_id,state,result_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, in.TableID, in.ProjectID, in.Mode, digest, out.OutputPoolID, out.State, string(raw), now, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func replaceLiveActiveRowsTx(ctx context.Context, tx *storeTx, poolID string, replacements []*SharedDataPoolRow, now time.Time) error {
	// Match the legacy free-text exclusion policy exactly. The original witness
	// protects the complete pool, including these excluded rows.
	rows, err := listDataPoolRowsTx(tx, poolID)
	if err != nil {
		return err
	}
	excluded := map[string]bool{}
	active := map[string]*SharedDataPoolRow{}
	for _, row := range rows {
		if liveSeedRowActive(row.Status) {
			active[row.ID] = row
		} else {
			excluded[row.ID] = true
		}
	}
	for id := range active {
		if _, err := tx.ExecContext(ctx, `DELETE FROM shared_data_pool_rows WHERE id=? AND pool_id=?`, id, poolID); err != nil {
			return err
		}
	}
	for index, row := range replacements {
		id := row.ID
		if id == "" {
			id = uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("apexload:live-row:%s:%d", poolID, index))).String()
		}
		if excluded[id] {
			return errors.New("live table output collides with an excluded source row")
		}
		// New output pools must not reuse globally unique IDs from their seed.
		var existingPool string
		err := tx.QueryRowContext(ctx, `SELECT pool_id FROM shared_data_pool_rows WHERE id=?`, id).Scan(&existingPool)
		if err == nil && existingPool != poolID {
			id = uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:live-row:"+poolID+":"+id)).String()
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		data, err := json.Marshal(row.Data)
		if err != nil {
			return err
		}
		status, err := liveFinalRowStatus(row.Status)
		if err != nil {
			return err
		}
		version := 1
		if previous := active[id]; previous != nil {
			version = previous.Version + 1
		}
		at := now.Add(time.Duration(index) * time.Microsecond)
		if _, err := tx.ExecContext(ctx, `INSERT INTO shared_data_pool_rows(id,pool_id,data_json,status,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, poolID, string(data), status, version, at, now); err != nil {
			return err
		}
	}
	return advanceDataPoolRowsPageState(tx, poolID, len(replacements)-len(active))
}

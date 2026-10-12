package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

var _ ProjectLiveTableStore = (*SQLiteStore)(nil)

const projectTableColumns = `id,project_id,name,revision,state,current_instance_id,config_json,retention_days,created_by_subject,created_at,updated_at`
const projectInstanceColumns = `id,table_id,project_id,state,seed_digest,seed_metadata,owner_lease_name,owner_id,owner_generation,revision,checkpoint_revision,lease_count,created_at,updated_at,retire_after`
const projectAttachmentColumns = `id,incarnation,project_id,table_id,instance_id,test_id,run_incarnation,group_name,alias,allowed_operations_json,principal,state,created_at,detached_at,retire_after,settled_revision`

func scanProjectTable(row liveTableScanner) (*ProjectLiveTableDefinition, error) {
	var r ProjectLiveTableDefinition
	var config string
	if err := row.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Revision, &r.State, &r.CurrentInstanceID, &config, &r.RetentionDays, &r.CreatedBySubject, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(config), &r.Config); err != nil {
		return nil, ErrLiveTableCorrupt
	}
	return &r, nil
}
func scanProjectInstance(row liveTableScanner) (*ProjectLiveTableInstance, error) {
	var r ProjectLiveTableInstance
	var retired sql.NullTime
	err := row.Scan(&r.ID, &r.TableID, &r.ProjectID, &r.State, &r.SeedDigest, &r.SeedMetadata, &r.OwnerLeaseName, &r.OwnerID, &r.OwnerGeneration, &r.Revision, &r.CheckpointRevision, &r.LeaseCount, &r.CreatedAt, &r.UpdatedAt, &retired)
	if retired.Valid {
		r.RetireAfter = &retired.Time
	}
	return &r, err
}
func scanProjectAttachment(row liveTableScanner) (*ProjectLiveTableAttachment, error) {
	var r ProjectLiveTableAttachment
	var ops string
	var detached, retire sql.NullTime
	var settled sql.NullInt64
	if err := row.Scan(&r.ID, &r.Incarnation, &r.ProjectID, &r.TableID, &r.InstanceID, &r.TestID, &r.RunIncarnation, &r.Group, &r.Alias, &ops, &r.Principal, &r.State, &r.CreatedAt, &detached, &retire, &settled); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(ops), &r.AllowedOperations); err != nil {
		return nil, ErrLiveTableCorrupt
	}
	if detached.Valid {
		r.DetachedAt = &detached.Time
	}
	if retire.Valid {
		r.RetireAfter = &retire.Time
	}
	if settled.Valid {
		v := uint64(settled.Int64)
		r.SettledRevision = &v
	}
	return &r, nil
}
func (s *SQLiteStore) beginProjectTableTx(ctx context.Context, term LiveTableExecutionTerm) (*storeTx, error) {
	if term.Generation == 0 {
		return nil, ErrLiveTableFenced
	}
	return s.beginLiveTableTx(ctx, term)
}
func lockProjectTable(ctx context.Context, tx *storeTx, project, id string) (*ProjectLiveTableDefinition, error) {
	if _, err := lockProjectForOwnedMutationContext(ctx, tx, project); err != nil {
		return nil, err
	}
	query := `SELECT ` + projectTableColumns + ` FROM project_live_tables WHERE project_id=? AND id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	return scanProjectTable(tx.QueryRowContext(ctx, query, project, id))
}
func lockProjectInstance(ctx context.Context, tx *storeTx, id string, term LiveTableExecutionTerm) (*ProjectLiveTableDefinition, *ProjectLiveTableInstance, error) {
	var project, table string
	if err := tx.QueryRowContext(ctx, `SELECT project_id,table_id FROM project_live_table_instances WHERE id=?`, id).Scan(&project, &table); err != nil {
		return nil, nil, err
	}
	d, err := lockProjectTable(ctx, tx, project, table)
	if err != nil {
		return nil, nil, err
	}
	query := `SELECT ` + projectInstanceColumns + ` FROM project_live_table_instances WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	i, err := scanProjectInstance(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return nil, nil, err
	}
	if i.OwnerLeaseName != term.LeaseName || i.OwnerID != term.OwnerID || i.OwnerGeneration != term.Generation || term.Generation == 0 {
		return nil, nil, ErrLiveTableFenced
	}
	return d, i, nil
}
func insertProjectLiveTableAuditTx(ctx context.Context, tx *storeTx, event *AuditEvent) error {
	if event == nil {
		return nil
	}
	if event.ID == "" || event.ActorSubject == "" || event.Action == "" {
		return errors.New("project live table audit identity is required")
	}
	details, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	if len(details) > 64<<10 {
		return ErrLiveTableLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,action,actor_type,actor_subject,actor_display_name,project_id,domain_id,resource_type,resource_id,status,details_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, event.ID, event.Action, event.ActorType, event.ActorSubject, event.ActorDisplayName, event.ProjectID, event.DomainID, event.ResourceType, event.ResourceID, event.Status, string(details), event.CreatedAt)
	return err
}
func insertProjectInstanceTx(ctx context.Context, tx *storeTx, i ProjectLiveTableInstance, payload []byte, term LiveTableExecutionTerm, now time.Time) error {
	if i.ID == "" || i.TableID == "" || i.ProjectID == "" || i.SeedDigest == "" || len(i.SeedMetadata) > 1<<20 {
		return errors.New("invalid project table instance")
	}
	if err := validateLivePayload(payload, MaxLiveTableCheckpointBytes); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO project_live_table_instances(`+projectInstanceColumns+`,initial_checkpoint_hash,checkpoint_payload,checkpoint_hash,journal_bytes,journal_count) VALUES(?,?,?,'active',?,?,?,?,?,0,0,0,?,?,NULL,?,?,?,0,0)`, i.ID, i.TableID, i.ProjectID, i.SeedDigest, i.SeedMetadata, term.LeaseName, term.OwnerID, term.Generation, now, now, liveTableHash(payload), string(payload), liveTableHash(payload))
	return err
}
func (s *SQLiteStore) CreateProjectLiveTable(ctx context.Context, input ProjectLiveTableCreate, term LiveTableExecutionTerm) (*ProjectLiveTableDefinition, error) {
	d := input.Definition
	i := input.Instance
	if d.ID == "" || d.ProjectID == "" || strings.TrimSpace(d.Name) == "" || len(d.Name) > 256 || i.TableID != d.ID || i.ProjectID != d.ProjectID || d.CurrentInstanceID != i.ID || d.RetentionDays < 1 || d.RetentionDays > 365 {
		return nil, errors.New("invalid project live table definition")
	}
	if d.Config.Profile != "native_queue" && d.Config.Profile != "vts_columns" {
		return nil, errors.New("invalid project table profile")
	}
	config, err := json.Marshal(d.Config)
	if err != nil {
		return nil, err
	}
	if len(config) > 64<<10 {
		return nil, ErrLiveTableLimit
	}
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutationContext(ctx, tx, d.ProjectID); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_live_tables WHERE project_id=?`, d.ProjectID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 256 {
		return nil, ErrLiveTableLimit
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	// Definitions are explicitly created, never upserted or implicitly reseeded.
	_, err = tx.ExecContext(ctx, `INSERT INTO project_live_tables(`+projectTableColumns+`) VALUES(?,?,?,1,'active',?,?,?,?,?,?)`, d.ID, d.ProjectID, d.Name, i.ID, string(config), d.RetentionDays, d.CreatedBySubject, now, now)
	if err != nil {
		return nil, err
	}
	if err := insertProjectInstanceTx(ctx, tx, i, input.Checkpoint, term, now); err != nil {
		return nil, err
	}
	if err := insertProjectLiveTableAuditTx(ctx, tx, input.Audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	d.Revision = 1
	d.State = "active"
	d.CreatedAt = now
	d.UpdatedAt = now
	return &d, nil
}
func (s *SQLiteStore) GetProjectLiveTable(ctx context.Context, project, id string) (*ProjectLiveTableDefinition, error) {
	return scanProjectTable(s.db.QueryRowContext(ctx, `SELECT `+projectTableColumns+` FROM project_live_tables WHERE project_id=? AND id=?`, project, id))
}
func (s *SQLiteStore) ListProjectLiveTables(ctx context.Context, project string) ([]ProjectLiveTableDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectTableColumns+` FROM project_live_tables WHERE project_id=? ORDER BY created_at,id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectLiveTableDefinition{}
	for rows.Next() {
		r, err := scanProjectTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) GetProjectLiveTableInstance(ctx context.Context, project, id string) (*ProjectLiveTableInstance, error) {
	return scanProjectInstance(s.db.QueryRowContext(ctx, `SELECT `+projectInstanceColumns+` FROM project_live_table_instances WHERE project_id=? AND id=?`, project, id))
}
func (s *SQLiteStore) ClaimProjectLiveTableInstance(ctx context.Context, project, id string, term LiveTableExecutionTerm) error {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var table string
	if err := tx.QueryRowContext(ctx, `SELECT table_id FROM project_live_table_instances WHERE project_id=? AND id=?`, project, id).Scan(&table); err != nil {
		return err
	}
	d, err := lockProjectTable(ctx, tx, project, table)
	if err != nil {
		return err
	}
	query := `SELECT ` + projectInstanceColumns + ` FROM project_live_table_instances WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	i, err := scanProjectInstance(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return err
	}
	if d.CurrentInstanceID != id || d.State == "archived" || i.State != "active" {
		return ErrLiveTableClosed
	}
	if i.OwnerLeaseName != term.LeaseName || i.OwnerGeneration > term.Generation || (i.OwnerGeneration == term.Generation && i.OwnerID != term.OwnerID) {
		return ErrLiveTableFenced
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_live_table_instances SET owner_id=?,owner_generation=?,updated_at=? WHERE id=?`, term.OwnerID, term.Generation, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// appendProjectLiveTableEntryTx is also used by the operator receipt/audit
// transaction. It must be called after beginProjectTableTx, never on an unfenced
// transaction. A supplied attachment closes the revoke/publication race.
func appendProjectLiveTableEntryTx(ctx context.Context, tx *storeTx, e ProjectLiveTableEntry, term LiveTableExecutionTerm) error {
	if e.TableID == "" || e.Revision == 0 || e.Revision != e.PreviousRevision+1 || e.At.IsZero() {
		return errors.New("invalid project table entry")
	}
	if err := validateLivePayload(e.Payload, MaxLiveTableEntryBytes); err != nil {
		return err
	}
	d, i, err := lockProjectInstance(ctx, tx, e.TableID, term)
	if err != nil {
		return err
	}
	if d.CurrentInstanceID != i.ID || d.State == "archived" || i.State != "active" {
		return ErrLiveTableClosed
	}
	if e.AttachmentID != "" {
		var state, instance string
		if err := tx.QueryRowContext(ctx, `SELECT state,instance_id FROM project_live_table_attachments WHERE id=?`, e.AttachmentID).Scan(&state, &instance); err != nil {
			return err
		}
		if state != "active" || instance != i.ID {
			return ErrLiveTableClosed
		}
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM project_live_table_journal WHERE instance_id=? AND revision=?`, i.ID, e.Revision).Scan(&existing)
	if err == nil {
		if existing != liveTableHash(e.Payload) {
			return ErrLiveTableConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if i.Revision != e.PreviousRevision {
		return ErrLiveTableConflict
	}
	var bytes, count int64
	if err := tx.QueryRowContext(ctx, `SELECT journal_bytes,journal_count FROM project_live_table_instances WHERE id=?`, i.ID).Scan(&bytes, &count); err != nil {
		return err
	}
	if bytes+int64(len(e.Payload)) > MaxLiveTableRecoveryBytes || count >= MaxLiveTableJournalEntries {
		return ErrLiveTableLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_live_table_journal(instance_id,revision,previous_revision,payload,payload_hash,created_at) VALUES(?,?,?,?,?,?)`, i.ID, e.Revision, e.PreviousRevision, string(e.Payload), liveTableHash(e.Payload), e.At); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_live_table_instances SET revision=?,lease_count=?,journal_bytes=journal_bytes+?,journal_count=journal_count+1,updated_at=? WHERE id=?`, e.Revision, e.LeasedCount, len(e.Payload), time.Now().UTC(), i.ID)
	return err
}
func (s *SQLiteStore) AppendProjectLiveTableEntry(ctx context.Context, e ProjectLiveTableEntry, term LiveTableExecutionTerm) error {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendProjectLiveTableEntryTx(ctx, tx, e, term); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLiteStore) PublishProjectLiveTableCheckpoint(ctx context.Context, id string, revision uint64, payload []byte, term LiveTableExecutionTerm) error {
	if err := validateLivePayload(payload, MaxLiveTableCheckpointBytes); err != nil {
		return err
	}
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, i, err := lockProjectInstance(ctx, tx, id, term)
	if err != nil {
		return err
	}
	if d.CurrentInstanceID != id || i.State != "active" {
		return ErrLiveTableClosed
	}
	if revision < i.CheckpointRevision || revision > i.Revision {
		return ErrLiveTableConflict
	}
	if revision == i.CheckpointRevision {
		var hash string
		if err := tx.QueryRowContext(ctx, `SELECT checkpoint_hash FROM project_live_table_instances WHERE id=?`, id).Scan(&hash); err != nil {
			return err
		}
		if hash != liveTableHash(payload) {
			return ErrLiveTableConflict
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_live_table_journal WHERE instance_id=? AND revision<=?`, id, revision); err != nil {
		return err
	}
	var bytes, count int64
	length := `LENGTH(CAST(payload AS BLOB))`
	if tx.dialect == storeDialectPostgres {
		length = `OCTET_LENGTH(payload)`
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+length+`),0),COUNT(*) FROM project_live_table_journal WHERE instance_id=?`, id).Scan(&bytes, &count); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_live_table_instances SET checkpoint_revision=?,checkpoint_payload=?,checkpoint_hash=?,journal_bytes=?,journal_count=?,updated_at=? WHERE id=?`, revision, string(payload), liveTableHash(payload), bytes, count, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLiteStore) ReadProjectLiveTableRecovery(ctx context.Context, project, id string) (*ProjectLiveTableRecovery, error) {
	var opts *sql.TxOptions
	if s.db.dialect == storeDialectPostgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	i, err := scanProjectInstance(tx.QueryRowContext(ctx, `SELECT `+projectInstanceColumns+` FROM project_live_table_instances WHERE project_id=? AND id=?`, project, id))
	if err != nil {
		return nil, err
	}
	d, err := scanProjectTable(tx.QueryRowContext(ctx, `SELECT `+projectTableColumns+` FROM project_live_tables WHERE project_id=? AND id=?`, project, i.TableID))
	if err != nil {
		return nil, err
	}
	var payload, hash string
	if err := tx.QueryRowContext(ctx, `SELECT checkpoint_payload,checkpoint_hash FROM project_live_table_instances WHERE id=?`, id).Scan(&payload, &hash); err != nil {
		return nil, err
	}
	if len(payload) > MaxLiveTableCheckpointBytes || liveTableHash([]byte(payload)) != hash {
		return nil, ErrLiveTableCorrupt
	}
	out := &ProjectLiveTableRecovery{Definition: *d, Instance: *i, Checkpoint: []byte(payload), Entries: []LiveTableEntry{}}
	rows, err := tx.QueryContext(ctx, `SELECT revision,previous_revision,payload,payload_hash,created_at FROM project_live_table_journal WHERE instance_id=? AND revision>? ORDER BY revision`, id, i.CheckpointRevision)
	if err != nil {
		return nil, err
	}
	expected := i.CheckpointRevision
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
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if expected != i.Revision {
		return nil, ErrLiveTableCorrupt
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) CreateProjectLiveTableAttachments(ctx context.Context, input []ProjectLiveTableAttachmentCreate, term LiveTableExecutionTerm) ([]ProjectLiveTableAttachment, error) {
	if len(input) == 0 {
		return []ProjectLiveTableAttachment{}, nil
	}
	if len(input) > 256 {
		return nil, ErrLiveTableLimit
	}
	input = append([]ProjectLiveTableAttachmentCreate(nil), input...)
	sort.Slice(input, func(i, j int) bool {
		a, b := input[i].Attachment, input[j].Attachment
		return a.ProjectID+"/"+a.TableID+"/"+a.ID < b.ProjectID+"/"+b.TableID+"/"+b.ID
	})
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectLiveTableAttachment, 0, len(input))
	for _, item := range input {
		a := item.Attachment
		if a.ID == "" || a.Incarnation == "" || a.TestID == "" || a.RunIncarnation == "" || a.Group == "" || a.Alias == "" || a.Principal == "" || len(a.AllowedOperations) == 0 || len(a.AllowedOperations) > 32 {
			return nil, errors.New("invalid project table attachment")
		}
		d, i, err := lockProjectInstance(ctx, tx, a.InstanceID, term)
		if err != nil {
			return nil, err
		}
		if d.ProjectID != a.ProjectID || d.ID != a.TableID || d.CurrentInstanceID != a.InstanceID || d.Revision != item.ExpectedDefinitionRevision || d.State != "active" || i.State != "active" {
			return nil, ErrLiveTableConflict
		}
		var project, status string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(project_id,''),status FROM tests WHERE id=?`, a.TestID).Scan(&project, &status); err != nil {
			return nil, err
		}
		if normalizeProjectID(project) != a.ProjectID {
			return nil, ErrLiveTableConflict
		}
		switch status {
		case "INITIALIZING", "STARTING", "RUNNING", "PAUSED", "QUEUED":
		default:
			return nil, ErrLiveTableClosed
		}
		// Retrying preparation cannot change an attachment's policy or incarnation.
		existing, err := scanProjectAttachment(tx.QueryRowContext(ctx, `SELECT `+projectAttachmentColumns+` FROM project_live_table_attachments WHERE test_id=? AND run_incarnation=? AND group_name=? AND alias=?`, a.TestID, a.RunIncarnation, a.Group, a.Alias))
		ops, _ := json.Marshal(a.AllowedOperations)
		if err == nil {
			existingOps, _ := json.Marshal(existing.AllowedOperations)
			if existing.State != "active" || existing.InstanceID != a.InstanceID || existing.Principal != a.Principal || string(existingOps) != string(ops) {
				return nil, ErrLiveTableConflict
			}
			out = append(out, *existing)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_live_table_attachments WHERE instance_id=?`, a.InstanceID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 4096 {
			return nil, ErrLiveTableLimit
		}
		a.State = "active"
		a.CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO project_live_table_attachments(`+projectAttachmentColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,'active',?,NULL,NULL,NULL)`, a.ID, a.Incarnation, a.ProjectID, a.TableID, a.InstanceID, a.TestID, a.RunIncarnation, a.Group, a.Alias, string(ops), a.Principal, now)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) listProjectAttachments(ctx context.Context, where string, args ...any) ([]ProjectLiveTableAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectAttachmentColumns+` FROM project_live_table_attachments WHERE `+where+` ORDER BY instance_id,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectLiveTableAttachment{}
	for rows.Next() {
		r, err := scanProjectAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) ListProjectLiveTableAttachments(ctx context.Context, project, table string) ([]ProjectLiveTableAttachment, error) {
	return s.listProjectAttachments(ctx, `project_id=? AND table_id=?`, project, table)
}
func (s *SQLiteStore) ListProjectLiveTableRunAttachments(ctx context.Context, project, test string) ([]ProjectLiveTableAttachment, error) {
	return s.listProjectAttachments(ctx, `project_id=? AND test_id=?`, project, test)
}
func (s *SQLiteStore) DetachProjectLiveTableAttachments(ctx context.Context, project, test string, retireAfter time.Time, term LiveTableExecutionTerm) error {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutationContext(ctx, tx, project); err != nil {
		return err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return err
	}
	if retireAfter.Before(now) {
		return errors.New("attachment retirement must preserve retry window")
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_live_table_attachments SET state='detached',detached_at=?,retire_after=? WHERE project_id=? AND test_id=? AND state='active'`, now, retireAfter, project, test)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLiteStore) MarkProjectLiveTableAttachmentSettled(ctx context.Context, id string, revision uint64, term LiveTableExecutionTerm) error {
	return s.changeProjectAttachment(ctx, id, revision, term, false)
}
func (s *SQLiteStore) RetireProjectLiveTableAttachment(ctx context.Context, id string, revision uint64, term LiveTableExecutionTerm) error {
	return s.changeProjectAttachment(ctx, id, revision, term, true)
}
func (s *SQLiteStore) changeProjectAttachment(ctx context.Context, id string, revision uint64, term LiveTableExecutionTerm, retire bool) error {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var instance string
	if err := tx.QueryRowContext(ctx, `SELECT instance_id FROM project_live_table_attachments WHERE id=?`, id).Scan(&instance); err != nil {
		return err
	}
	_, i, err := lockProjectInstance(ctx, tx, instance, term)
	if err != nil {
		return err
	}
	a, err := scanProjectAttachment(tx.QueryRowContext(ctx, `SELECT `+projectAttachmentColumns+` FROM project_live_table_attachments WHERE id=?`, id))
	if err != nil {
		return err
	}
	if a.State != "detached" || revision > i.Revision {
		return ErrLiveTableConflict
	}
	if retire {
		now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
		if err != nil {
			return err
		}
		if a.SettledRevision == nil || a.RetireAfter == nil || now.Before(*a.RetireAfter) {
			return ErrLiveTableConflict
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM project_live_table_attachments WHERE id=?`, id)
		if err != nil {
			return err
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE project_live_table_attachments SET settled_revision=? WHERE id=?`, revision, id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func validateProjectLifecycle(ctx context.Context, tx *storeTx, input ProjectLiveTableLifecycle, term LiveTableExecutionTerm) (*ProjectLiveTableDefinition, *ProjectLiveTableInstance, error) {
	d, i, err := lockProjectInstance(ctx, tx, input.ExpectedInstanceID, term)
	if err != nil {
		return nil, nil, err
	}
	if d.ProjectID != input.ProjectID || d.ID != input.TableID || d.CurrentInstanceID != input.ExpectedInstanceID || d.Revision != input.ExpectedDefinitionRevision || i.Revision != input.ExpectedRevision {
		return nil, nil, ErrLiveTableConflict
	}
	if strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 1024 {
		return nil, nil, errors.New("a bounded lifecycle reason is required")
	}
	return d, i, nil
}
func (s *SQLiteStore) TransitionProjectLiveTable(ctx context.Context, input ProjectLiveTableLifecycle, term LiveTableExecutionTerm) (*ProjectLiveTableDefinition, error) {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	d, i, err := validateProjectLifecycle(ctx, tx, input, term)
	if err != nil {
		return nil, err
	}
	allowed := (d.State == "active" && input.State == "quiescing") || ((d.State == "quiescing" || d.State == "quiesced") && input.State == "active") || (d.State == "quiescing" && input.State == "quiesced") || (d.State == "quiesced" && input.State == "archived")
	if !allowed {
		return nil, ErrLiveTableConflict
	}
	if input.State == "quiesced" || input.State == "archived" {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_live_table_attachments WHERE instance_id=? AND state='active'`, i.ID).Scan(&active); err != nil {
			return nil, err
		}
		if input.State == "archived" {
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_live_table_attachments WHERE instance_id=?`, i.ID).Scan(&active); err != nil {
				return nil, err
			}
		}
		if active != 0 || i.LeaseCount != 0 {
			return nil, ErrLiveTableConflict
		}
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_live_tables SET state=?,revision=revision+1,updated_at=? WHERE id=?`, input.State, now, d.ID); err != nil {
		return nil, err
	}
	if input.State == "archived" {
		if _, err := tx.ExecContext(ctx, `UPDATE project_live_table_instances SET state='retired',retire_after=?,updated_at=? WHERE id=?`, now.Add(time.Duration(d.RetentionDays)*24*time.Hour), now, i.ID); err != nil {
			return nil, err
		}
	}
	if err := insertProjectLiveTableAuditTx(ctx, tx, input.Audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	d.State = input.State
	d.Revision++
	d.UpdatedAt = now
	return d, nil
}
func (s *SQLiteStore) ReplaceProjectLiveTableInstance(ctx context.Context, input ProjectLiveTableReset, term LiveTableExecutionTerm) (*ProjectLiveTableDefinition, error) {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	d, i, err := validateProjectLifecycle(ctx, tx, input.ProjectLiveTableLifecycle, term)
	if err != nil {
		return nil, err
	}
	if d.State != "quiesced" || i.LeaseCount != 0 || input.Instance.ID == i.ID || input.Instance.TableID != d.ID || input.Instance.ProjectID != d.ProjectID {
		return nil, ErrLiveTableConflict
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_live_table_attachments WHERE instance_id=? `, i.ID).Scan(&active); err != nil {
		return nil, err
	}
	if active != 0 {
		return nil, ErrLiveTableConflict
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	if err := insertProjectInstanceTx(ctx, tx, input.Instance, input.Checkpoint, term, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_live_table_instances SET state='retired',retire_after=?,updated_at=? WHERE id=?`, now.Add(time.Duration(d.RetentionDays)*24*time.Hour), now, i.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_live_tables SET current_instance_id=?,revision=revision+1,updated_at=? WHERE id=?`, input.Instance.ID, now, d.ID); err != nil {
		return nil, err
	}
	if err := insertProjectLiveTableAuditTx(ctx, tx, input.Audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	d.CurrentInstanceID = input.Instance.ID
	d.Revision++
	d.UpdatedAt = now
	return d, nil
}

// PruneProjectLiveTableInstances removes only expired retired instances that no
// longer have cleanup tombstones. The definition and audit remain discoverable.
func (s *SQLiteStore) PruneProjectLiveTableInstances(ctx context.Context, project string, term LiveTableExecutionTerm) (int, error) {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutationContext(ctx, tx, project); err != nil {
		return 0, err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM project_live_table_instances WHERE project_id=? AND state='retired' AND retire_after<=? AND NOT EXISTS(SELECT 1 FROM project_live_table_attachments a WHERE a.instance_id=project_live_table_instances.id) ORDER BY id LIMIT 64`, project, now)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		for _, table := range []string{"project_live_table_operator_receipts", "project_live_table_journal"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE instance_id=?`, id); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_live_table_instances WHERE id=?`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

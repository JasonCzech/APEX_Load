package storage

import (
	"context"
	"time"
)

// ListProjectLiveTableMaintenance bounds restart reconciliation independently
// from the number of projects or historical runs. The caller carries the cursor
// forward and wraps only after reaching the end.
func (s *SQLiteStore) ListProjectLiveTableMaintenance(ctx context.Context, after string, limit int) ([]ProjectLiveTableDefinition, error) {
	if limit < 1 || limit > 64 {
		limit = 16
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectTableColumns+` FROM project_live_tables WHERE id>? AND (EXISTS(SELECT 1 FROM project_live_table_attachments a WHERE a.table_id=project_live_tables.id AND (a.state='detached' OR NOT EXISTS(SELECT 1 FROM tests t WHERE t.id=a.test_id AND t.status IN ('QUEUED','INITIALIZING','STARTING','RUNNING','PAUSED','STOPPING','SHAKEOUT')))) OR EXISTS(SELECT 1 FROM project_live_table_instances i WHERE i.table_id=project_live_tables.id AND i.state='retired' AND i.retire_after<=?)) ORDER BY id LIMIT ?`, after, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectLiveTableDefinition{}
	for rows.Next() {
		d, err := scanProjectTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DetachOrphanProjectLiveTableAttachments reconciles terminal/deleted runs under
// the same project lock as attachment creation. A prepared/running run cannot
// disappear between the orphan check and the durable admission revocation.
func (s *SQLiteStore) DetachOrphanProjectLiveTableAttachments(ctx context.Context, project, table string, term LiveTableExecutionTerm) error {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := lockProjectTable(ctx, tx, project, table)
	if err != nil {
		return err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_live_table_attachments SET state='detached',detached_at=?,retire_after=? WHERE project_id=? AND table_id=? AND state='active' AND NOT EXISTS(SELECT 1 FROM tests t WHERE t.id=project_live_table_attachments.test_id AND t.status IN ('QUEUED','INITIALIZING','STARTING','RUNNING','PAUSED','STOPPING','SHAKEOUT'))`, now, now.Add(time.Duration(d.Config.RetryWindowMS)*time.Millisecond), project, table)
	if err != nil {
		return err
	}
	return tx.Commit()
}

package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type ProjectLiveTableOperatorReceipt struct {
	InstanceID   string
	ActorSubject string
	KeyHash      string
	Digest       string
	ClientScope  string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	Outcome      []byte // Encrypted; never expose through metadata or audit APIs.
}

type ProjectLiveTableOperatorStore interface {
	GetProjectLiveTableOperatorReceipt(context.Context, string, string, string) (*ProjectLiveTableOperatorReceipt, error)
	AppendProjectLiveTableOperatorEntry(context.Context, ProjectLiveTableEntry, ProjectLiveTableOperatorReceipt, *AuditEvent, LiveTableExecutionTerm) error
	ListExpiredProjectLiveTableOperatorReceipts(context.Context, string, time.Time) ([]ProjectLiveTableOperatorReceipt, error)
	DeleteProjectLiveTableOperatorReceipt(context.Context, ProjectLiveTableOperatorReceipt, uint64, LiveTableExecutionTerm) error
}

const projectOperatorReceiptColumns = `instance_id,actor_subject,key_hash,digest,client_scope,issued_at,expires_at,outcome`

func scanProjectOperatorReceipt(row liveTableScanner) (*ProjectLiveTableOperatorReceipt, error) {
	var receipt ProjectLiveTableOperatorReceipt
	err := row.Scan(&receipt.InstanceID, &receipt.ActorSubject, &receipt.KeyHash, &receipt.Digest, &receipt.ClientScope, &receipt.IssuedAt, &receipt.ExpiresAt, &receipt.Outcome)
	if err == nil && (len(receipt.Outcome) == 0 || len(receipt.Outcome) > 3<<20) {
		return nil, ErrLiveTableCorrupt
	}
	return &receipt, err
}

func (s *SQLiteStore) GetProjectLiveTableOperatorReceipt(ctx context.Context, instance, actor, key string) (*ProjectLiveTableOperatorReceipt, error) {
	return scanProjectOperatorReceipt(s.db.QueryRowContext(ctx, `SELECT `+projectOperatorReceiptColumns+` FROM project_live_table_operator_receipts WHERE instance_id=? AND actor_subject=? AND key_hash=?`, instance, actor, key))
}

// The journal transition, exact encrypted operator outcome and value-free audit
// have a single commit point. A failed audit insertion rolls the mutation back.
func (s *SQLiteStore) AppendProjectLiveTableOperatorEntry(ctx context.Context, entry ProjectLiveTableEntry, receipt ProjectLiveTableOperatorReceipt, audit *AuditEvent, term LiveTableExecutionTerm) error {
	if receipt.InstanceID != entry.TableID || receipt.ActorSubject == "" || len(receipt.ActorSubject) > 512 || len(receipt.KeyHash) != 64 || len(receipt.Digest) != 64 || len(receipt.ClientScope) > 192 || !strings.HasPrefix(receipt.ClientScope, "operator/") || !strings.HasSuffix(receipt.ClientScope, "/") || receipt.IssuedAt.IsZero() || !receipt.ExpiresAt.After(receipt.IssuedAt) || len(receipt.Outcome) == 0 || len(receipt.Outcome) > 3<<20 || audit == nil {
		return errors.New("invalid project table operator receipt")
	}
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	definition, instance, err := lockProjectInstance(ctx, tx, entry.TableID, term)
	if err != nil {
		return err
	}
	if audit.ProjectID != definition.ProjectID || audit.ResourceID != definition.ID || audit.ActorSubject != receipt.ActorSubject || audit.ResourceType != "project_live_table" {
		return errors.New("operator audit scope mismatch")
	}
	existing, err := scanProjectOperatorReceipt(tx.QueryRowContext(ctx, `SELECT `+projectOperatorReceiptColumns+` FROM project_live_table_operator_receipts WHERE instance_id=? AND actor_subject=? AND key_hash=?`, receipt.InstanceID, receipt.ActorSubject, receipt.KeyHash))
	if err == nil {
		if existing.Digest != receipt.Digest || existing.ClientScope != receipt.ClientScope || !existing.IssuedAt.Equal(receipt.IssuedAt) || !existing.ExpiresAt.Equal(receipt.ExpiresAt) || !bytes.Equal(existing.Outcome, receipt.Outcome) {
			return ErrLiveTableConflict
		}
		if err = appendProjectLiveTableEntryTx(ctx, tx, entry, term); err != nil {
			return err
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if definition.CurrentInstanceID != instance.ID || definition.State != "active" {
		return ErrLiveTableClosed
	}
	var count, retainedBytes int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(LENGTH(outcome)),0) FROM project_live_table_operator_receipts WHERE instance_id=?`, receipt.InstanceID).Scan(&count, &retainedBytes); err != nil {
		return err
	}
	if count >= int64(definition.Config.MaxReceipts) || retainedBytes+int64(len(receipt.Outcome)) > MaxLiveTableRecoveryBytes {
		return ErrLiveTableLimit
	}
	if err = appendProjectLiveTableEntryTx(ctx, tx, entry, term); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_live_table_operator_receipts(`+projectOperatorReceiptColumns+`) VALUES(?,?,?,?,?,?,?,?)`, receipt.InstanceID, receipt.ActorSubject, receipt.KeyHash, receipt.Digest, receipt.ClientScope, receipt.IssuedAt, receipt.ExpiresAt, receipt.Outcome); err != nil {
		return err
	}
	if err = insertProjectLiveTableAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListExpiredProjectLiveTableOperatorReceipts(ctx context.Context, instance string, before time.Time) ([]ProjectLiveTableOperatorReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,actor_subject,key_hash,digest,client_scope,issued_at,expires_at FROM project_live_table_operator_receipts WHERE instance_id=? AND expires_at<=? ORDER BY expires_at,key_hash LIMIT 64`, instance, before.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectLiveTableOperatorReceipt{}
	for rows.Next() {
		var receipt ProjectLiveTableOperatorReceipt
		err := rows.Scan(&receipt.InstanceID, &receipt.ActorSubject, &receipt.KeyHash, &receipt.Digest, &receipt.ClientScope, &receipt.IssuedAt, &receipt.ExpiresAt)
		if err != nil {
			return nil, err
		}
		out = append(out, receipt)
	}
	return out, rows.Err()
}

// Call only after the core has journaled retirement of this rejected-at-ingress
// identity. The state revision witness prevents deleting a receipt before that
// retirement is durable, including when a leader changes between these steps.
func (s *SQLiteStore) DeleteProjectLiveTableOperatorReceipt(ctx context.Context, receipt ProjectLiveTableOperatorReceipt, revision uint64, term LiveTableExecutionTerm) error {
	tx, err := s.beginProjectTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, instance, err := lockProjectInstance(ctx, tx, receipt.InstanceID, term)
	if err != nil {
		return err
	}
	if instance.Revision != revision || receipt.ExpiresAt.After(time.Now().UTC()) {
		return ErrLiveTableConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM project_live_table_operator_receipts WHERE instance_id=? AND actor_subject=? AND key_hash=? AND digest=? AND expires_at=?`, receipt.InstanceID, receipt.ActorSubject, receipt.KeyHash, receipt.Digest, receipt.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

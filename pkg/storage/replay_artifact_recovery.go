package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

// ReplayArtifactScope is internal authorization metadata, not permission to
// read artifacts. The HTTP caller must enforce the current project role and
// subsequently obtain an exact project-scoped ready receipt before any bytes.
type ReplayArtifactScope struct {
	SessionID    string
	ProjectID    string
	Generation   string
	State        string
	WriterJoined bool
	Expired      bool
}

func (s *SQLiteStore) LookupReplayArtifactScope(ctx context.Context, session string) (*ReplayArtifactScope, error) {
	if session == "" || len(session) > 128 || !utf8.ValidString(session) || strings.TrimSpace(session) != session {
		return nil, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return nil, err
	}
	receipt, err := scanReplayArtifactReceipt(tx.QueryRowContext(ctx, `SELECT session_id,project_id,generation,spec_json,reserved_bytes,state,writer_joined,expires_at FROM replay_artifact_receipts WHERE session_id=? AND EXISTS(SELECT 1 FROM projects p WHERE p.id=replay_artifact_receipts.project_id)`, session))
	if err != nil {
		return nil, err
	}
	if receipt.Spec.SessionID != session {
		return nil, errors.New("replay artifact scope identity mismatch")
	}
	switch receipt.State {
	case "staging", "ready", "cleanup":
	default:
		return nil, errors.New("invalid replay artifact scope state")
	}
	return &ReplayArtifactScope{SessionID: session, ProjectID: receipt.Spec.ProjectID, Generation: receipt.Spec.Generation, State: receipt.State, WriterJoined: receipt.WriterJoined, Expired: !now.Before(receipt.ExpiresAt)}, nil
}

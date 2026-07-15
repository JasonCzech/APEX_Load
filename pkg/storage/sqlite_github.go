package storage

// SQLite store — github. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveGitHubConnection(conn *GitHubConnection) error {
	if conn == nil || strings.TrimSpace(conn.UserID) == "" {
		return fmt.Errorf("github connection requires a user id")
	}
	now := time.Now()
	if conn.ConnectedAt.IsZero() {
		conn.ConnectedAt = now
	}
	conn.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO github_connections
		(user_id, github_login, github_user_id, access_token_enc, refresh_token_enc, token_type, scopes, expires_at, connected_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			github_login=excluded.github_login,
			github_user_id=excluded.github_user_id,
			access_token_enc=excluded.access_token_enc,
			refresh_token_enc=excluded.refresh_token_enc,
			token_type=excluded.token_type,
			scopes=excluded.scopes,
			expires_at=excluded.expires_at,
			updated_at=excluded.updated_at`,
		conn.UserID, conn.GitHubLogin, conn.GitHubUserID, conn.AccessTokenEnc, conn.RefreshTokenEnc,
		conn.TokenType, conn.Scopes, conn.ExpiresAt, conn.ConnectedAt, conn.UpdatedAt)
	return err
}

// SaveGitHubConnectionIfNotRevokedSince atomically completes an OAuth link only
// if no disconnect/offboarding revocation happened after that OAuth flow began.
// The lifecycle row is created before it is locked, which also serializes the
// previously-missing-row case on PostgreSQL rather than relying on gap locks.
func (s *SQLiteStore) SaveGitHubConnectionIfNotRevokedSince(conn *GitHubConnection, flowStartedAt time.Time) (bool, error) {
	if conn == nil || strings.TrimSpace(conn.UserID) == "" || flowStartedAt.IsZero() {
		return false, fmt.Errorf("github connection, user id, and oauth flow start are required")
	}
	conn.UserID = strings.TrimSpace(conn.UserID)
	now := time.Now()
	if conn.ConnectedAt.IsZero() {
		conn.ConnectedAt = now
	}
	conn.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO github_connection_revocations (user_id, revoked_at) VALUES (?, NULL)
		ON CONFLICT(user_id) DO NOTHING`, conn.UserID); err != nil {
		return false, err
	}
	query := `SELECT revoked_at FROM github_connection_revocations WHERE user_id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var revokedAt sql.NullTime
	if err := tx.QueryRow(query, conn.UserID).Scan(&revokedAt); err != nil {
		return false, err
	}
	if revokedAt.Valid && !revokedAt.Time.Before(flowStartedAt) {
		return false, nil
	}
	if _, err := tx.Exec(`INSERT INTO github_connections
		(user_id, github_login, github_user_id, access_token_enc, refresh_token_enc, token_type, scopes, expires_at, connected_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			github_login=excluded.github_login,
			github_user_id=excluded.github_user_id,
			access_token_enc=excluded.access_token_enc,
			refresh_token_enc=excluded.refresh_token_enc,
			token_type=excluded.token_type,
			scopes=excluded.scopes,
			expires_at=excluded.expires_at,
			updated_at=excluded.updated_at`,
		conn.UserID, conn.GitHubLogin, conn.GitHubUserID, conn.AccessTokenEnc, conn.RefreshTokenEnc,
		conn.TokenType, conn.Scopes, conn.ExpiresAt, conn.ConnectedAt, conn.UpdatedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateGitHubConnectionIfUnchanged persists a token refresh only while the
// connection row still matches the snapshot that initiated it. In particular, a
// slow OAuth refresh can never recreate a connection the user disconnected.
func (s *SQLiteStore) UpdateGitHubConnectionIfUnchanged(conn *GitHubConnection, expectedUpdatedAt time.Time) (bool, error) {
	if conn == nil || strings.TrimSpace(conn.UserID) == "" || expectedUpdatedAt.IsZero() {
		return false, fmt.Errorf("github connection and expected updated_at are required")
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := s.db.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM github_connections WHERE user_id = ?`, conn.UserID).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	conn.UpdatedAt = time.Now()
	result, err := s.db.Exec(`UPDATE github_connections SET github_login = ?, github_user_id = ?, access_token_enc = ?, refresh_token_enc = ?, token_type = ?, scopes = ?, expires_at = ?, updated_at = ?
		WHERE user_id = ? AND CAST(updated_at AS TEXT) = ?`, conn.GitHubLogin, conn.GitHubUserID, conn.AccessTokenEnc, conn.RefreshTokenEnc,
		conn.TokenType, conn.Scopes, conn.ExpiresAt, conn.UpdatedAt, conn.UserID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) GetGitHubConnection(userID string) (*GitHubConnection, error) {
	conn := &GitHubConnection{}
	err := s.db.QueryRow(`SELECT user_id, github_login, github_user_id, access_token_enc, refresh_token_enc,
		token_type, scopes, expires_at, connected_at, updated_at FROM github_connections WHERE user_id = ?`, userID).
		Scan(&conn.UserID, &conn.GitHubLogin, &conn.GitHubUserID, &conn.AccessTokenEnc, &conn.RefreshTokenEnc,
			&conn.TokenType, &conn.Scopes, &conn.ExpiresAt, &conn.ConnectedAt, &conn.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return conn, nil
}

func (s *SQLiteStore) DeleteGitHubConnection(userID string) error {
	return s.RevokeGitHubConnection(userID, time.Now().UTC())
}

// RevokeGitHubConnection records revocation before deleting the credential in
// one transaction. A callback that started earlier will observe this fence and
// cannot recreate the connection after disconnect returned success.
func (s *SQLiteStore) RevokeGitHubConnection(userID string, revokedAt time.Time) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || revokedAt.IsZero() {
		return fmt.Errorf("github connection user id and revocation time are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revokeGitHubConnectionTx(tx, userID, revokedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func revokeGitHubConnectionTx(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, userID string, revokedAt time.Time) error {
	if _, err := tx.Exec(`INSERT INTO github_connection_revocations (user_id, revoked_at) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET revoked_at = CASE
			WHEN github_connection_revocations.revoked_at IS NULL OR github_connection_revocations.revoked_at < excluded.revoked_at
			THEN excluded.revoked_at ELSE github_connection_revocations.revoked_at END`, userID, revokedAt); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM github_connections WHERE user_id = ?`, userID)
	return err
}

func (s *SQLiteStore) SaveGitHubScriptLink(link *GitHubScriptLink) error {
	if link == nil || strings.TrimSpace(link.ScriptID) == "" {
		return fmt.Errorf("github script link requires a script id")
	}
	link.ScriptID = strings.TrimSpace(link.ScriptID)
	now := time.Now()
	if link.SyncedAt.IsZero() {
		link.SyncedAt = now
	}
	link.UpdatedAt = now
	if strings.TrimSpace(link.RemoteBlobSHA) == "" {
		link.RemoteBlobSHA = link.LastCommitSHA
	}
	if strings.TrimSpace(link.LastCommitSHA) == "" {
		link.LastCommitSHA = link.RemoteBlobSHA
	}
	projectID, err := s.githubAnchorProject("scripts", link.ScriptID)
	if err != nil {
		return fmt.Errorf("load github script link anchor: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return err
	}
	if err := lockGitHubAnchor(tx, "scripts", link.ScriptID, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO github_script_links
		(script_id, ref_key, owner, repo, branch, path, last_commit_sha, remote_blob_sha, remote_commit_sha, content_hash, synced_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(script_id) DO UPDATE SET
			ref_key=excluded.ref_key,
			owner=excluded.owner,
			repo=excluded.repo,
			branch=excluded.branch,
			path=excluded.path,
			last_commit_sha=excluded.last_commit_sha,
			remote_blob_sha=excluded.remote_blob_sha,
			remote_commit_sha=excluded.remote_commit_sha,
			content_hash=excluded.content_hash,
			synced_at=excluded.synced_at,
			updated_at=excluded.updated_at`,
		link.ScriptID, link.RefKey, link.Owner, link.Repo, link.Branch, link.Path,
		link.LastCommitSHA, link.RemoteBlobSHA, link.RemoteCommitSHA, link.ContentHash, link.SyncedAt, link.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) githubAnchorProject(table, id string) (string, error) {
	query := fmt.Sprintf(`SELECT COALESCE(NULLIF(project_id, ''), ?) FROM %s WHERE id = ?`, table)
	var projectID string
	if err := s.db.QueryRow(query, DefaultProjectID, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%s anchor not found: %s", strings.TrimSuffix(table, "s"), id)
		}
		return "", err
	}
	return normalizeProjectID(projectID), nil
}

func lockGitHubAnchor(tx *storeTx, table, id, expectedProjectID string) error {
	query := fmt.Sprintf(`SELECT COALESCE(NULLIF(project_id, ''), ?) FROM %s WHERE id = ?`, table)
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var projectID string
	if err := tx.QueryRow(query, DefaultProjectID, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%s anchor not found: %s", strings.TrimSuffix(table, "s"), id)
		}
		return err
	}
	if normalizeProjectID(projectID) != normalizeProjectID(expectedProjectID) {
		return fmt.Errorf("%s anchor changed projects: %s", strings.TrimSuffix(table, "s"), id)
	}
	return nil
}

func (s *SQLiteStore) GetGitHubScriptLink(scriptID string) (*GitHubScriptLink, error) {
	link := &GitHubScriptLink{}
	err := s.db.QueryRow(`SELECT script_id, ref_key, owner, repo, branch, path, last_commit_sha,
		COALESCE(remote_blob_sha, ''), COALESCE(remote_commit_sha, ''), content_hash, synced_at, updated_at FROM github_script_links WHERE script_id = ?`, scriptID).
		Scan(&link.ScriptID, &link.RefKey, &link.Owner, &link.Repo, &link.Branch, &link.Path,
			&link.LastCommitSHA, &link.RemoteBlobSHA, &link.RemoteCommitSHA, &link.ContentHash, &link.SyncedAt, &link.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if link.RemoteBlobSHA == "" {
		link.RemoteBlobSHA = link.LastCommitSHA
	}
	return link, nil
}

// UpdateGitHubScriptLinksRemoteCommit records a webhook-observed branch head
// without rewriting link coordinates, blob state, or content hashes from a
// stale read. This is safe to race with push/import link updates.
func (s *SQLiteStore) UpdateGitHubScriptLinksRemoteCommit(owner, repo, branch, commitSHA string, updatedAt time.Time) (int64, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	branch = strings.TrimSpace(branch)
	if owner == "" || repo == "" {
		return 0, fmt.Errorf("github link owner and repo are required")
	}
	query := `UPDATE github_script_links SET remote_commit_sha = ?, updated_at = ? WHERE LOWER(owner) = LOWER(?) AND LOWER(repo) = LOWER(?)`
	args := []any{strings.TrimSpace(commitSHA), updatedAt, owner, repo}
	if branch != "" {
		query += ` AND branch = ?`
		args = append(args, branch)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *SQLiteStore) DeleteGitHubScriptLink(scriptID string) error {
	_, err := s.db.Exec(`DELETE FROM github_script_links WHERE script_id = ?`, scriptID)
	return err
}

func (s *SQLiteStore) SaveGitHubAppInstallation(installation *GitHubAppInstallation) error {
	if installation == nil || installation.InstallationID <= 0 {
		return fmt.Errorf("github app installation requires an installation id")
	}
	now := time.Now()
	if installation.CreatedAt.IsZero() {
		installation.CreatedAt = now
	}
	installation.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO github_app_installations
		(installation_id, account_login, account_type, repository_selection, permissions_json, events_json, html_url, suspended_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(installation_id) DO UPDATE SET
			account_login=excluded.account_login,
			account_type=excluded.account_type,
			repository_selection=excluded.repository_selection,
			permissions_json=excluded.permissions_json,
			events_json=excluded.events_json,
			html_url=excluded.html_url,
			suspended_at=excluded.suspended_at,
			updated_at=excluded.updated_at`,
		installation.InstallationID, installation.AccountLogin, installation.AccountType, installation.RepositorySelection,
		installation.PermissionsJSON, installation.EventsJSON, installation.HTMLURL, installation.SuspendedAt,
		installation.CreatedAt, installation.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetGitHubAppInstallation(installationID int64) (*GitHubAppInstallation, error) {
	installation := &GitHubAppInstallation{}
	err := s.db.QueryRow(`SELECT installation_id, account_login, account_type, repository_selection, permissions_json, events_json, html_url, suspended_at, created_at, updated_at
		FROM github_app_installations WHERE installation_id = ?`, installationID).
		Scan(&installation.InstallationID, &installation.AccountLogin, &installation.AccountType, &installation.RepositorySelection,
			&installation.PermissionsJSON, &installation.EventsJSON, &installation.HTMLURL, &installation.SuspendedAt,
			&installation.CreatedAt, &installation.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return installation, nil
}

func (s *SQLiteStore) ListGitHubAppInstallations() ([]*GitHubAppInstallation, error) {
	rows, err := s.db.Query(`SELECT installation_id, account_login, account_type, repository_selection, permissions_json, events_json, html_url, suspended_at, created_at, updated_at
		FROM github_app_installations ORDER BY account_login ASC, installation_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*GitHubAppInstallation{}
	for rows.Next() {
		installation := &GitHubAppInstallation{}
		if err := rows.Scan(&installation.InstallationID, &installation.AccountLogin, &installation.AccountType, &installation.RepositorySelection,
			&installation.PermissionsJSON, &installation.EventsJSON, &installation.HTMLURL, &installation.SuspendedAt,
			&installation.CreatedAt, &installation.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, installation)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) SaveGitHubSyncJob(job *GitHubSyncJob) error {
	if job == nil || strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("github sync job requires an id")
	}
	job.ID = strings.TrimSpace(job.ID)
	job.ProjectID = normalizeProjectID(job.ProjectID)
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if job.ProjectID, err = lockProjectForOwnedMutation(tx, job.ProjectID); err != nil {
		return err
	}
	var currentProjectID string
	err = tx.QueryRow(`SELECT COALESCE(project_id, '') FROM github_sync_jobs WHERE id = ?`, job.ID).Scan(&currentProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != job.ProjectID {
		return fmt.Errorf("github sync job %q belongs to another project", job.ID)
	}
	result, err := tx.Exec(`INSERT INTO github_sync_jobs
		(id, project_id, kind, status, owner, repo, branch, path, total, processed, created, updated, conflicts, errors, summary_json, started_by, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind=excluded.kind,
			status=excluded.status,
			owner=excluded.owner,
			repo=excluded.repo,
			branch=excluded.branch,
			path=excluded.path,
			total=excluded.total,
			processed=excluded.processed,
			created=excluded.created,
			updated=excluded.updated,
			conflicts=excluded.conflicts,
			errors=excluded.errors,
			summary_json=excluded.summary_json,
			started_by=excluded.started_by,
			updated_at=excluded.updated_at,
			completed_at=excluded.completed_at
		WHERE COALESCE(NULLIF(github_sync_jobs.project_id, ''), ?) = excluded.project_id`,
		job.ID, job.ProjectID, job.Kind, job.Status, job.Owner, job.Repo, job.Branch, job.Path,
		job.Total, job.Processed, job.Created, job.Updated, job.Conflicts, job.Errors, job.SummaryJSON, job.StartedBy,
		job.CreatedAt, job.UpdatedAt, job.CompletedAt, DefaultProjectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("github sync job %q belongs to another project", job.ID)
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetGitHubSyncJob(id string) (*GitHubSyncJob, error) {
	job := &GitHubSyncJob{}
	err := s.db.QueryRow(`SELECT id, project_id, kind, status, owner, repo, branch, path, total, processed, created, updated, conflicts, errors, summary_json, started_by, created_at, updated_at, completed_at
		FROM github_sync_jobs WHERE id = ?`, id).
		Scan(&job.ID, &job.ProjectID, &job.Kind, &job.Status, &job.Owner, &job.Repo, &job.Branch, &job.Path,
			&job.Total, &job.Processed, &job.Created, &job.Updated, &job.Conflicts, &job.Errors, &job.SummaryJSON, &job.StartedBy,
			&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return job, nil
}

func (s *SQLiteStore) ListGitHubSyncJobs(projectID string, limit int) ([]*GitHubSyncJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT id, project_id, kind, status, owner, repo, branch, path, total, processed, created, updated, conflicts, errors, summary_json, started_by, created_at, updated_at, completed_at FROM github_sync_jobs`
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*GitHubSyncJob{}
	for rows.Next() {
		job := &GitHubSyncJob{}
		if err := rows.Scan(&job.ID, &job.ProjectID, &job.Kind, &job.Status, &job.Owner, &job.Repo, &job.Branch, &job.Path,
			&job.Total, &job.Processed, &job.Created, &job.Updated, &job.Conflicts, &job.Errors, &job.SummaryJSON, &job.StartedBy,
			&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) SaveGitHubWebhookDelivery(delivery *GitHubWebhookDelivery) error {
	if delivery == nil || strings.TrimSpace(delivery.DeliveryID) == "" {
		return fmt.Errorf("github webhook delivery requires a delivery id")
	}
	if delivery.ReceivedAt.IsZero() {
		delivery.ReceivedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO github_webhook_deliveries
		(delivery_id, event, action, owner, repo, ref, commit_sha, status, summary_json, received_at, processed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(delivery_id) DO UPDATE SET
			event=excluded.event,
			action=excluded.action,
			owner=excluded.owner,
			repo=excluded.repo,
			ref=excluded.ref,
			commit_sha=excluded.commit_sha,
			status=excluded.status,
			summary_json=excluded.summary_json,
			processed_at=excluded.processed_at`,
		delivery.DeliveryID, delivery.Event, delivery.Action, delivery.Owner, delivery.Repo, delivery.Ref, delivery.CommitSHA,
		delivery.Status, delivery.SummaryJSON, delivery.ReceivedAt, delivery.ProcessedAt)
	return err
}

func (s *SQLiteStore) GetGitHubWebhookDelivery(deliveryID string) (*GitHubWebhookDelivery, error) {
	delivery := &GitHubWebhookDelivery{}
	err := s.db.QueryRow(`SELECT delivery_id, event, action, owner, repo, ref, commit_sha, status, summary_json, received_at, processed_at
		FROM github_webhook_deliveries WHERE delivery_id = ?`, deliveryID).
		Scan(&delivery.DeliveryID, &delivery.Event, &delivery.Action, &delivery.Owner, &delivery.Repo, &delivery.Ref,
			&delivery.CommitSHA, &delivery.Status, &delivery.SummaryJSON, &delivery.ReceivedAt, &delivery.ProcessedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return delivery, nil
}

func (s *SQLiteStore) ListGitHubWebhookDeliveries(limit int) ([]*GitHubWebhookDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT delivery_id, event, action, owner, repo, ref, commit_sha, status, summary_json, received_at, processed_at
		FROM github_webhook_deliveries ORDER BY received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*GitHubWebhookDelivery{}
	for rows.Next() {
		delivery := &GitHubWebhookDelivery{}
		if err := rows.Scan(&delivery.DeliveryID, &delivery.Event, &delivery.Action, &delivery.Owner, &delivery.Repo, &delivery.Ref,
			&delivery.CommitSHA, &delivery.Status, &delivery.SummaryJSON, &delivery.ReceivedAt, &delivery.ProcessedAt); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) SaveGitHubCheckLink(link *GitHubCheckLink) error {
	if link == nil || strings.TrimSpace(link.ID) == "" {
		return fmt.Errorf("github check link requires an id")
	}
	link.ID = strings.TrimSpace(link.ID)
	link.TestID = strings.TrimSpace(link.TestID)
	link.ScriptID = strings.TrimSpace(link.ScriptID)
	if link.TestID == "" && link.ScriptID == "" {
		return fmt.Errorf("github check link requires a test or script anchor")
	}
	projectID := ""
	if link.TestID != "" {
		var err error
		projectID, err = s.githubAnchorProject("tests", link.TestID)
		if err != nil {
			return fmt.Errorf("load github check test anchor: %w", err)
		}
	}
	if link.ScriptID != "" {
		scriptProjectID, err := s.githubAnchorProject("scripts", link.ScriptID)
		if err != nil {
			return fmt.Errorf("load github check script anchor: %w", err)
		}
		if projectID != "" && projectID != scriptProjectID {
			return fmt.Errorf("github check test and script anchors belong to different projects")
		}
		projectID = scriptProjectID
	}
	now := time.Now()
	if link.PublishedAt.IsZero() {
		link.PublishedAt = now
	}
	link.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return err
	}
	if link.TestID != "" {
		if err := lockGitHubAnchor(tx, "tests", link.TestID, projectID); err != nil {
			return err
		}
	}
	if link.ScriptID != "" {
		if err := lockGitHubAnchor(tx, "scripts", link.ScriptID, projectID); err != nil {
			return err
		}
	}
	var currentTestID, currentScriptID string
	err = tx.QueryRow(`SELECT COALESCE(test_id, ''), COALESCE(script_id, '') FROM github_check_links WHERE id = ?`, link.ID).Scan(&currentTestID, &currentScriptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (currentTestID != link.TestID || currentScriptID != link.ScriptID) {
		return fmt.Errorf("github check link %q belongs to different anchors", link.ID)
	}
	result, err := tx.Exec(`INSERT INTO github_check_links
		(id, test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, published_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			owner=excluded.owner,
			repo=excluded.repo,
			commit_sha=excluded.commit_sha,
			check_run_id=excluded.check_run_id,
			check_run_url=excluded.check_run_url,
			status=excluded.status,
			conclusion=excluded.conclusion,
			updated_at=excluded.updated_at
		WHERE COALESCE(github_check_links.test_id, '') = excluded.test_id
			AND COALESCE(github_check_links.script_id, '') = excluded.script_id`,
		link.ID, link.TestID, link.ScriptID, link.Owner, link.Repo, link.CommitSHA, link.CheckRunID, link.CheckRunURL,
		link.Status, link.Conclusion, link.PublishedAt, link.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("github check link %q belongs to different anchors", link.ID)
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetGitHubCheckLink(id string) (*GitHubCheckLink, error) {
	link := &GitHubCheckLink{}
	err := s.db.QueryRow(`SELECT id, test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, published_at, updated_at
		FROM github_check_links WHERE id = ?`, id).
		Scan(&link.ID, &link.TestID, &link.ScriptID, &link.Owner, &link.Repo, &link.CommitSHA, &link.CheckRunID,
			&link.CheckRunURL, &link.Status, &link.Conclusion, &link.PublishedAt, &link.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return link, nil
}

func (s *SQLiteStore) ListGitHubCheckLinks(testID string, scriptID string, limit int) ([]*GitHubCheckLink, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT id, test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, published_at, updated_at FROM github_check_links`
	args := []any{}
	filters := []string{}
	if strings.TrimSpace(testID) != "" {
		filters = append(filters, "test_id = ?")
		args = append(args, testID)
	}
	if strings.TrimSpace(scriptID) != "" {
		filters = append(filters, "script_id = ?")
		args = append(args, scriptID)
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += " ORDER BY published_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*GitHubCheckLink{}
	for rows.Next() {
		link := &GitHubCheckLink{}
		if err := rows.Scan(&link.ID, &link.TestID, &link.ScriptID, &link.Owner, &link.Repo, &link.CommitSHA, &link.CheckRunID,
			&link.CheckRunURL, &link.Status, &link.Conclusion, &link.PublishedAt, &link.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

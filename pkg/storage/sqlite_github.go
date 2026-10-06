package storage

// SQLite store — github. Split from sqlite.go (same package, behavior-preserving).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
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

type normalizedGitHubScriptSyncTarget struct {
	targetKey string
	ownerNorm string
	repoNorm  string
	branch    string
	path      string
}

func normalizeGitHubScriptSyncTargets(targets []GitHubScriptSyncTarget) ([]normalizedGitHubScriptSyncTarget, error) {
	byKey := make(map[string]normalizedGitHubScriptSyncTarget, len(targets))
	for _, target := range targets {
		normalized := normalizedGitHubScriptSyncTarget{
			ownerNorm: strings.ToLower(strings.TrimSpace(target.Owner)),
			repoNorm:  strings.ToLower(strings.TrimSpace(target.Repo)),
			branch:    strings.TrimSpace(target.Branch),
			path:      strings.TrimSpace(target.Path),
		}
		if normalized.ownerNorm == "" || normalized.repoNorm == "" || normalized.branch == "" || normalized.path == "" {
			return nil, fmt.Errorf("github script sync targets require owner, repo, branch, and path")
		}
		digest := sha256.Sum256([]byte(normalized.ownerNorm + "\x00" + normalized.repoNorm + "\x00" + normalized.branch + "\x00" + normalized.path))
		normalized.targetKey = "v1:" + hex.EncodeToString(digest[:])
		if previous, exists := byKey[normalized.targetKey]; exists && previous != normalized {
			return nil, fmt.Errorf("github script sync target key collision")
		}
		byKey[normalized.targetKey] = normalized
	}
	if len(byKey) == 0 {
		return nil, fmt.Errorf("github script sync claim requires at least one target")
	}
	normalized := make([]normalizedGitHubScriptSyncTarget, 0, len(byKey))
	for _, target := range byKey {
		normalized = append(normalized, target)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].targetKey < normalized[j].targetKey })
	return normalized, nil
}

func githubScriptSyncTargetsForLink(link *GitHubScriptLink) ([]normalizedGitHubScriptSyncTarget, error) {
	if link == nil {
		return nil, fmt.Errorf("github script link is required")
	}
	targets := []GitHubScriptSyncTarget{{Owner: link.Owner, Repo: link.Repo, Branch: link.Branch, Path: link.Path}}
	if strings.TrimSpace(link.BaseBranch) != "" {
		targets = append(targets, GitHubScriptSyncTarget{Owner: link.Owner, Repo: link.Repo, Branch: link.BaseBranch, Path: link.Path})
	} else if strings.TrimSpace(link.BaseBlobSHA) != "" || link.BaseMissing {
		return nil, fmt.Errorf("github script link base witness requires a base branch")
	}
	return normalizeGitHubScriptSyncTargets(targets)
}

func mergeNormalizedGitHubSyncTargets(groups ...[]normalizedGitHubScriptSyncTarget) []normalizedGitHubScriptSyncTarget {
	byKey := make(map[string]normalizedGitHubScriptSyncTarget)
	for _, group := range groups {
		for _, target := range group {
			byKey[target.targetKey] = target
		}
	}
	merged := make([]normalizedGitHubScriptSyncTarget, 0, len(byKey))
	for _, target := range byKey {
		merged = append(merged, target)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].targetKey < merged[j].targetKey })
	return merged
}

func (s *SQLiteStore) lockGitHubSyncTargetsTx(tx *storeTx, targets []normalizedGitHubScriptSyncTarget, now time.Time) error {
	for _, target := range targets {
		if _, err := tx.Exec(`INSERT INTO github_script_sync_targets
			(target_key, owner_norm, repo_norm, branch, path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(target_key) DO NOTHING`,
			target.targetKey, target.ownerNorm, target.repoNorm, target.branch, target.path, now, now); err != nil {
			return err
		}
		query := `SELECT owner_norm, repo_norm, branch, path FROM github_script_sync_targets WHERE target_key = ?`
		if s.db.dialect == storeDialectPostgres {
			query += ` FOR UPDATE`
		}
		var ownerNorm, repoNorm, branch, path string
		if err := tx.QueryRow(query, target.targetKey).Scan(&ownerNorm, &repoNorm, &branch, &path); err != nil {
			return err
		}
		if ownerNorm != target.ownerNorm || repoNorm != target.repoNorm || branch != target.branch || path != target.path {
			return fmt.Errorf("github script sync target key collision")
		}
	}
	return nil
}

func githubSyncTargetRowsTx(tx *storeTx, table, scriptID, claimID string) ([]normalizedGitHubScriptSyncTarget, []time.Time, error) {
	query := fmt.Sprintf(`SELECT target_key, owner_norm, repo_norm, branch, path, expires_at FROM %s WHERE script_id = ? AND claim_id = ? ORDER BY target_key`, table)
	rows, err := tx.Query(query, scriptID, claimID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	targets := make([]normalizedGitHubScriptSyncTarget, 0)
	expires := make([]time.Time, 0)
	for rows.Next() {
		var target normalizedGitHubScriptSyncTarget
		var expiry time.Time
		if err := rows.Scan(&target.targetKey, &target.ownerNorm, &target.repoNorm, &target.branch, &target.path, &expiry); err != nil {
			return nil, nil, err
		}
		targets = append(targets, target)
		expires = append(expires, expiry)
	}
	return targets, expires, rows.Err()
}

func githubOwnedSyncTargetsTx(tx *storeTx, scriptID string) ([]normalizedGitHubScriptSyncTarget, error) {
	rows, err := tx.Query(`SELECT target_key, owner_norm, repo_norm, branch, path
		FROM github_script_sync_target_owners WHERE script_id = ? ORDER BY target_key`, scriptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]normalizedGitHubScriptSyncTarget, 0)
	for rows.Next() {
		var target normalizedGitHubScriptSyncTarget
		if err := rows.Scan(&target.targetKey, &target.ownerNorm, &target.repoNorm, &target.branch, &target.path); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func githubSyncTargetConflictsWithOtherScriptTx(tx *storeTx, scriptID string, target normalizedGitHubScriptSyncTarget) (bool, error) {
	var ownerScriptID string
	err := tx.QueryRow(`SELECT script_id FROM github_script_sync_target_owners WHERE target_key = ?`, target.targetKey).Scan(&ownerScriptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && ownerScriptID != scriptID {
		return true, nil
	}
	var linkedScriptID string
	err = tx.QueryRow(`SELECT script_id FROM github_script_links
		WHERE script_id <> ?
			AND LOWER(TRIM(COALESCE(owner, ''))) = ?
			AND LOWER(TRIM(COALESCE(repo, ''))) = ?
			AND TRIM(COALESCE(path, '')) = ?
			AND (TRIM(COALESCE(branch, '')) = ? OR TRIM(COALESCE(base_branch, '')) = ?)
		LIMIT 1`, scriptID, target.ownerNorm, target.repoNorm, target.path, target.branch, target.branch).Scan(&linkedScriptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return err == nil, nil
}

func (s *SQLiteStore) persistGitHubSyncTargetOwnersTx(tx *storeTx, scriptID, projectID string, targets []normalizedGitHubScriptSyncTarget, now time.Time) error {
	for _, target := range targets {
		conflict, err := githubSyncTargetConflictsWithOtherScriptTx(tx, scriptID, target)
		if err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("github target %s/%s:%s:%s is owned by another script", target.ownerNorm, target.repoNorm, target.branch, target.path)
		}
	}
	if _, err := tx.Exec(`DELETE FROM github_script_sync_target_owners WHERE script_id = ?`, scriptID); err != nil {
		return err
	}
	for _, target := range targets {
		result, err := tx.Exec(`INSERT INTO github_script_sync_target_owners
			(target_key, project_id, script_id, owner_norm, repo_norm, branch, path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(target_key) DO UPDATE SET
				project_id=excluded.project_id,
				owner_norm=excluded.owner_norm,
				repo_norm=excluded.repo_norm,
				branch=excluded.branch,
				path=excluded.path,
				updated_at=excluded.updated_at
			WHERE github_script_sync_target_owners.script_id=excluded.script_id`,
			target.targetKey, projectID, scriptID, target.ownerNorm, target.repoNorm, target.branch, target.path, now, now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("github target %s/%s:%s:%s is owned by another script", target.ownerNorm, target.repoNorm, target.branch, target.path)
		}
	}
	return nil
}

// backfillGitHubScriptSyncTargetOwners turns pre-reservation script links into
// durable target ownership. Legacy schemas allowed two scripts to name the same
// normalized target. For those databases, the oldest synced link wins (with
// script ID as the stable tie-breaker); losing links are removed transactionally
// and an audit repair event is retained. An active legacy claim defers repair of
// that collision without failing startup, leaving both links as a conservative
// fence until a later startup can repair them safely.
func (s *SQLiteStore) backfillGitHubScriptSyncTargetOwners() error {
	type linkedScript struct {
		projectID string
		scriptID  string
	}
	rows, err := s.db.Query(`SELECT COALESCE(NULLIF(s.project_id, ''), ?), l.script_id
		FROM github_script_links l
		JOIN scripts s ON s.id = l.script_id`, DefaultProjectID)
	if err != nil {
		return fmt.Errorf("list github script links for target-owner backfill: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	var linked []linkedScript
	for rows.Next() {
		var item linkedScript
		if err := rows.Scan(&item.projectID, &item.scriptID); err != nil {
			rows.Close()
			return fmt.Errorf("scan github script link for target-owner backfill: %w", err)
		}
		item.projectID = normalizeProjectID(item.projectID)
		linked = append(linked, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close github script target-owner backfill scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list github script links for target-owner backfill: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projects := make(map[string]struct{})
	projectByScript := make(map[string]string, len(linked))
	for _, item := range linked {
		projects[item.projectID] = struct{}{}
		projectByScript[item.scriptID] = item.projectID
	}
	projectIDs := make([]string, 0, len(projects))
	for projectID := range projects {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Strings(projectIDs)
	for _, projectID := range projectIDs {
		if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
			return err
		}
	}
	scriptIDs := make([]string, 0, len(projectByScript))
	for scriptID := range projectByScript {
		scriptIDs = append(scriptIDs, scriptID)
	}
	sort.Strings(scriptIDs)
	for _, scriptID := range scriptIDs {
		if err := lockGitHubAnchor(tx, "scripts", scriptID, projectByScript[scriptID]); err != nil {
			return err
		}
	}

	type candidate struct {
		projectID string
		link      GitHubScriptLink
		targets   []normalizedGitHubScriptSyncTarget
	}
	candidates := make([]candidate, 0, len(scriptIDs))
	for _, scriptID := range scriptIDs {
		liveLink := GitHubScriptLink{ScriptID: scriptID}
		var baseMissing any
		if err := tx.QueryRow(`SELECT COALESCE(owner, ''), COALESCE(repo, ''),
			COALESCE(branch, ''), COALESCE(path, ''), COALESCE(base_branch, ''),
			COALESCE(base_blob_sha, ''), COALESCE(base_missing, 0), synced_at
			FROM github_script_links WHERE script_id = ?`, scriptID).Scan(
			&liveLink.Owner, &liveLink.Repo, &liveLink.Branch, &liveLink.Path,
			&liveLink.BaseBranch, &liveLink.BaseBlobSHA, &baseMissing, &liveLink.SyncedAt,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("reload github script link %q for target-owner backfill: %w", scriptID, err)
		}
		liveLink.BaseMissing = sqlBool(baseMissing)
		// Older builds permitted incomplete link rows. They do not name a remote
		// target and therefore cannot reserve one; preserve the row so a later
		// pull/relink workflow can repair it.
		if strings.TrimSpace(liveLink.Owner) == "" || strings.TrimSpace(liveLink.Repo) == "" ||
			strings.TrimSpace(liveLink.Branch) == "" || strings.TrimSpace(liveLink.Path) == "" {
			continue
		}
		targets, err := githubScriptSyncTargetsForLink(&liveLink)
		if err != nil {
			return fmt.Errorf("derive github targets for linked script %q: %w", scriptID, err)
		}
		candidates = append(candidates, candidate{projectID: projectByScript[scriptID], link: liveLink, targets: targets})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].link.SyncedAt.Equal(candidates[j].link.SyncedAt) {
			return candidates[i].link.SyncedAt.Before(candidates[j].link.SyncedAt)
		}
		return candidates[i].link.ScriptID < candidates[j].link.ScriptID
	})

	targetWinner := make(map[string]string)
	winners := make([]candidate, 0, len(candidates))
	losers := make(map[string][]string)
	for _, item := range candidates {
		conflictingWinners := make(map[string]struct{})
		for _, target := range item.targets {
			if winnerID := targetWinner[target.targetKey]; winnerID != "" && winnerID != item.link.ScriptID {
				conflictingWinners[winnerID] = struct{}{}
			}
		}
		if len(conflictingWinners) != 0 {
			winnerIDs := make([]string, 0, len(conflictingWinners))
			for winnerID := range conflictingWinners {
				winnerIDs = append(winnerIDs, winnerID)
			}
			sort.Strings(winnerIDs)
			losers[item.link.ScriptID] = winnerIDs
			continue
		}
		for _, target := range item.targets {
			targetWinner[target.targetKey] = item.link.ScriptID
		}
		winners = append(winners, item)
	}

	for _, item := range candidates {
		winnerIDs, loses := losers[item.link.ScriptID]
		if !loses {
			continue
		}
		if err := rejectActiveGitHubScriptSyncClaimTx(tx, item.link.ScriptID); err != nil {
			if IsGitHubScriptSyncBusy(err) {
				continue
			}
			return err
		}
		ownedTargets, err := githubOwnedSyncTargetsTx(tx, item.link.ScriptID)
		if err != nil {
			return err
		}
		now, err := preciseDatabaseTime(tx, s.db.dialect)
		if err != nil {
			return fmt.Errorf("read github target-owner backfill database clock: %w", err)
		}
		if err := s.lockGitHubSyncTargetsTx(tx, mergeNormalizedGitHubSyncTargets(item.targets, ownedTargets), now); err != nil {
			return err
		}
		for _, query := range []string{
			`DELETE FROM github_script_sync_target_claims WHERE script_id = ?`,
			`DELETE FROM github_script_sync_claims WHERE script_id = ?`,
			`DELETE FROM github_script_sync_target_owners WHERE script_id = ?`,
			`DELETE FROM github_script_links WHERE script_id = ?`,
		} {
			if _, err := tx.Exec(query, item.link.ScriptID); err != nil {
				return err
			}
		}
		detailsJSON, err := json.Marshal(map[string]any{
			"reason":            "duplicate_normalized_github_target",
			"winner_script_ids": winnerIDs,
			"loser_script_id":   item.link.ScriptID,
		})
		if err != nil {
			return err
		}
		repairID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:github-target-owner-repair:v1\x00"+item.link.ScriptID+"\x00"+strings.Join(winnerIDs, "\x00"))).String()
		if _, err := tx.Exec(`INSERT INTO audit_events
			(id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`, repairID, "github.script_link.target_conflict_repaired", "system", "storage-migration", "",
			item.projectID, "", "script", item.link.ScriptID, "repaired", string(detailsJSON), now); err != nil {
			return fmt.Errorf("record github target-owner migration repair for script %q: %w", item.link.ScriptID, err)
		}
	}

	for _, item := range winners {
		blocked := false
		for _, target := range item.targets {
			conflict, err := githubSyncTargetConflictsWithOtherScriptTx(tx, item.link.ScriptID, target)
			if err != nil {
				return err
			}
			if conflict {
				blocked = true
				break
			}
		}
		if blocked {
			// A losing legacy link can remain only while its old per-script claim
			// is active. Leaving both links live causes the compatibility conflict
			// check to fence both scripts rather than selecting an unsafe winner.
			continue
		}
		ownedTargets, err := githubOwnedSyncTargetsTx(tx, item.link.ScriptID)
		if err != nil {
			return err
		}
		now, err := preciseDatabaseTime(tx, s.db.dialect)
		if err != nil {
			return fmt.Errorf("read github target-owner backfill database clock: %w", err)
		}
		if err := s.lockGitHubSyncTargetsTx(tx, mergeNormalizedGitHubSyncTargets(item.targets, ownedTargets), now); err != nil {
			return err
		}
		if err := s.persistGitHubSyncTargetOwnersTx(tx, item.link.ScriptID, item.projectID, item.targets, now); err != nil {
			return fmt.Errorf("backfill github target owners for script %q: %w", item.link.ScriptID, err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveGitHubScriptLink(link *GitHubScriptLink) error {
	if err := prepareGitHubScriptLink(link, time.Now()); err != nil {
		return err
	}
	targets, err := githubScriptSyncTargetsForLink(link)
	if err != nil {
		return err
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
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return fmt.Errorf("read github script sync database clock: %w", err)
	}
	ownedTargets, err := githubOwnedSyncTargetsTx(tx, link.ScriptID)
	if err != nil {
		return err
	}
	if err := s.lockGitHubSyncTargetsTx(tx, mergeNormalizedGitHubSyncTargets(targets, ownedTargets), now); err != nil {
		return err
	}
	if err := rejectActiveGitHubScriptSyncClaimTx(tx, link.ScriptID); err != nil {
		return err
	}
	for _, target := range targets {
		conflict, err := githubSyncTargetConflictsWithOtherScriptTx(tx, link.ScriptID, target)
		if err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("github target %s/%s:%s:%s is owned by another script", target.ownerNorm, target.repoNorm, target.branch, target.path)
		}
		var targetClaims int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM github_script_sync_target_claims
			WHERE target_key = ? AND expires_at > ?`, target.targetKey, now).Scan(&targetClaims); err != nil {
			return err
		}
		if targetClaims != 0 {
			return fmt.Errorf("%w for github target %s/%s:%s:%s", ErrGitHubScriptSyncBusy, target.ownerNorm, target.repoNorm, target.branch, target.path)
		}
	}
	if err := s.upsertGitHubScriptLinkTx(tx, link); err != nil {
		return err
	}
	if err := s.persistGitHubSyncTargetOwnersTx(tx, link.ScriptID, projectID, targets, now); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareGitHubScriptLink(link *GitHubScriptLink, now time.Time) error {
	if link == nil || strings.TrimSpace(link.ScriptID) == "" {
		return fmt.Errorf("github script link requires a script id")
	}
	link.ScriptID = strings.TrimSpace(link.ScriptID)
	link.Owner = strings.TrimSpace(link.Owner)
	link.Repo = strings.TrimSpace(link.Repo)
	link.Branch = strings.TrimSpace(link.Branch)
	link.Path = strings.TrimSpace(link.Path)
	link.BaseBranch = strings.TrimSpace(link.BaseBranch)
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
	return nil
}

func (s *SQLiteStore) upsertGitHubScriptLinkTx(tx *storeTx, link *GitHubScriptLink) error {
	_, err := tx.Exec(`INSERT INTO github_script_links
		(script_id, ref_key, owner, repo, branch, path, last_commit_sha, remote_blob_sha, remote_commit_sha, base_branch, base_blob_sha, base_missing, content_hash, synced_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(script_id) DO UPDATE SET
			ref_key=excluded.ref_key,
			owner=excluded.owner,
			repo=excluded.repo,
			branch=excluded.branch,
			path=excluded.path,
			last_commit_sha=excluded.last_commit_sha,
			remote_blob_sha=excluded.remote_blob_sha,
			remote_commit_sha=excluded.remote_commit_sha,
			base_branch=excluded.base_branch,
			base_blob_sha=excluded.base_blob_sha,
			base_missing=excluded.base_missing,
			content_hash=excluded.content_hash,
			synced_at=excluded.synced_at,
			updated_at=excluded.updated_at`,
		link.ScriptID, link.RefKey, link.Owner, link.Repo, link.Branch, link.Path,
		link.LastCommitSHA, link.RemoteBlobSHA, link.RemoteCommitSHA,
		link.BaseBranch, link.BaseBlobSHA, s.boolValue(link.BaseMissing),
		link.ContentHash, link.SyncedAt, link.UpdatedAt)
	return err
}

func loadGitHubScriptLinkTx(tx *storeTx, scriptID string) (*GitHubScriptLink, error) {
	link := &GitHubScriptLink{}
	var baseMissing any
	err := tx.QueryRow(`SELECT script_id, COALESCE(ref_key, ''), COALESCE(owner, ''), COALESCE(repo, ''),
		COALESCE(branch, ''), COALESCE(path, ''), COALESCE(last_commit_sha, ''),
		COALESCE(remote_blob_sha, ''), COALESCE(remote_commit_sha, ''), COALESCE(base_branch, ''),
		COALESCE(base_blob_sha, ''), COALESCE(base_missing, 0), COALESCE(content_hash, ''), synced_at, updated_at
		FROM github_script_links WHERE script_id = ?`, scriptID).Scan(
		&link.ScriptID, &link.RefKey, &link.Owner, &link.Repo, &link.Branch, &link.Path,
		&link.LastCommitSHA, &link.RemoteBlobSHA, &link.RemoteCommitSHA,
		&link.BaseBranch, &link.BaseBlobSHA, &baseMissing, &link.ContentHash,
		&link.SyncedAt, &link.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if link.RemoteBlobSHA == "" {
		link.RemoteBlobSHA = link.LastCommitSHA
	}
	link.BaseMissing = sqlBool(baseMissing)
	return link, nil
}

func githubImportLinksEquivalent(left, right *GitHubScriptLink) bool {
	if left == nil || right == nil {
		return left == right
	}
	return strings.TrimSpace(left.ScriptID) == strings.TrimSpace(right.ScriptID) &&
		strings.TrimSpace(left.RefKey) == strings.TrimSpace(right.RefKey) &&
		strings.EqualFold(strings.TrimSpace(left.Owner), strings.TrimSpace(right.Owner)) &&
		strings.EqualFold(strings.TrimSpace(left.Repo), strings.TrimSpace(right.Repo)) &&
		strings.TrimSpace(left.Branch) == strings.TrimSpace(right.Branch) &&
		strings.TrimSpace(left.Path) == strings.TrimSpace(right.Path) &&
		strings.TrimSpace(left.LastCommitSHA) == strings.TrimSpace(right.LastCommitSHA) &&
		strings.TrimSpace(left.RemoteBlobSHA) == strings.TrimSpace(right.RemoteBlobSHA) &&
		strings.TrimSpace(left.RemoteCommitSHA) == strings.TrimSpace(right.RemoteCommitSHA) &&
		strings.TrimSpace(left.BaseBranch) == strings.TrimSpace(right.BaseBranch) &&
		strings.TrimSpace(left.BaseBlobSHA) == strings.TrimSpace(right.BaseBlobSHA) &&
		left.BaseMissing == right.BaseMissing &&
		strings.TrimSpace(left.ContentHash) == strings.TrimSpace(right.ContentHash)
}

// CommitGitHubScriptImport is the atomic local commit boundary for one remote
// file. The project row serializes the absent-script case, the existing script
// row fences updates, and the normalized target anchor serializes ownership.
// Script bytes, link metadata, and target ownership either all commit or none
// do; a normal ownership/revision mismatch is reported as Conflict.
func (s *SQLiteStore) CommitGitHubScriptImport(script *dsl.Script, link *GitHubScriptLink, expectedUpdatedAt time.Time) (GitHubScriptImportOutcome, error) {
	if script == nil || link == nil {
		return GitHubScriptImportConflict, fmt.Errorf("github script import requires a script and link")
	}
	script.ID = strings.TrimSpace(script.ID)
	link.ScriptID = strings.TrimSpace(link.ScriptID)
	if script.ID == "" || link.ScriptID == "" || script.ID != link.ScriptID {
		return GitHubScriptImportConflict, fmt.Errorf("github script import requires matching script and link ids")
	}
	script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
	link.Owner = strings.TrimSpace(link.Owner)
	link.Repo = strings.TrimSpace(link.Repo)
	link.Branch = strings.TrimSpace(link.Branch)
	link.Path = strings.TrimSpace(link.Path)
	link.BaseBranch = strings.TrimSpace(link.BaseBranch)
	targets, err := githubScriptSyncTargetsForLink(link)
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	if len(targets) != 1 {
		return GitHubScriptImportConflict, fmt.Errorf("github script import requires exactly one target")
	}
	target := targets[0]

	tx, err := s.db.Begin()
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, script.ProjectID); err != nil {
		return GitHubScriptImportConflict, err
	}
	current, err := loadImportSourceRetentionScriptTx(tx, script.ID, s.db.dialect == storeDialectPostgres)
	currentExists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return GitHubScriptImportConflict, err
	}
	existingLink, err := loadGitHubScriptLinkTx(tx, script.ID)
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	if currentExists {
		if normalizeProjectID(current.ProjectID) != script.ProjectID || expectedUpdatedAt.IsZero() ||
			!current.UpdatedAt.Equal(expectedUpdatedAt) || existingLink == nil {
			return GitHubScriptImportConflict, nil
		}
		existingTargets, err := githubScriptSyncTargetsForLink(existingLink)
		if err != nil || len(existingTargets) != 1 || existingTargets[0].targetKey != target.targetKey {
			return GitHubScriptImportConflict, nil
		}
		// Workspace placement and identity are local state, never remote import
		// input. Enforce preservation again at the storage boundary.
		script.RefKey = current.RefKey
		if current.FolderID == nil {
			script.FolderID = nil
		} else {
			folderID := *current.FolderID
			script.FolderID = &folderID
		}
		script.CreatedAt = current.CreatedAt
	} else {
		if !expectedUpdatedAt.IsZero() || existingLink != nil {
			return GitHubScriptImportConflict, nil
		}
	}
	if err := validateScriptFolderProjectTx(tx, script.ProjectID, script.FolderID); err != nil {
		return GitHubScriptImportConflict, err
	}
	if err := validateScriptObjectRepositoryProjectsTx(tx, script.ProjectID, script); err != nil {
		return GitHubScriptImportConflict, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return GitHubScriptImportConflict, fmt.Errorf("read github import database clock: %w", err)
	}
	ownedTargets, err := githubOwnedSyncTargetsTx(tx, script.ID)
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	if err := s.lockGitHubSyncTargetsTx(tx, mergeNormalizedGitHubSyncTargets(targets, ownedTargets), now); err != nil {
		return GitHubScriptImportConflict, err
	}
	if err := rejectActiveGitHubScriptSyncClaimTx(tx, script.ID); err != nil {
		return GitHubScriptImportConflict, err
	}
	var activeTargetClaims int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM github_script_sync_target_claims
		WHERE target_key = ? AND expires_at > ?`, target.targetKey, now).Scan(&activeTargetClaims); err != nil {
		return GitHubScriptImportConflict, err
	}
	if activeTargetClaims != 0 {
		return GitHubScriptImportConflict, fmt.Errorf("%w for github target %s/%s:%s:%s",
			ErrGitHubScriptSyncBusy, target.ownerNorm, target.repoNorm, target.branch, target.path)
	}
	conflict, err := githubSyncTargetConflictsWithOtherScriptTx(tx, script.ID, target)
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	if conflict {
		return GitHubScriptImportConflict, nil
	}

	link.RefKey = script.RefKey
	if err := prepareGitHubScriptLink(link, now); err != nil {
		return GitHubScriptImportConflict, err
	}
	if !currentExists {
		if script.CreatedAt.IsZero() {
			script.CreatedAt = now
		}
		if script.UpdatedAt.IsZero() {
			script.UpdatedAt = now
		}
		encoded, err := marshalScriptForStorage(script)
		if err != nil {
			return GitHubScriptImportConflict, err
		}
		result, err := tx.Exec(`INSERT INTO scripts
			(id, ref_key, name, protocol, version, project_id, folder_id, generated_by, generated_from,
			 metadata_json, goal_config_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`, script.ID, script.RefKey, script.Name, script.Protocol, script.Version,
			script.ProjectID, script.FolderID, script.GeneratedBy, script.GeneratedFrom,
			encoded.metadata, encoded.goals, encoded.config, encoded.dataSources, encoded.parameters, encoded.init, encoded.actions, encoded.teardown,
			script.CreatedAt, script.UpdatedAt)
		if err != nil {
			return GitHubScriptImportConflict, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return GitHubScriptImportConflict, err
		}
		if affected != 1 {
			return GitHubScriptImportConflict, nil
		}
		if err := s.upsertGitHubScriptLinkTx(tx, link); err != nil {
			return GitHubScriptImportConflict, err
		}
		if err := s.persistGitHubSyncTargetOwnersTx(tx, script.ID, script.ProjectID, targets, now); err != nil {
			return GitHubScriptImportConflict, err
		}
		if err := tx.Commit(); err != nil {
			return GitHubScriptImportConflict, err
		}
		return GitHubScriptImportCreated, nil
	}

	currentFingerprint, err := ImportSourceScriptFingerprint(current)
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	desiredFingerprint, err := ImportSourceScriptFingerprint(script)
	if err != nil {
		return GitHubScriptImportConflict, err
	}
	if err := ensureObjectActiveTx(tx, "script", script.ID); err != nil {
		return GitHubScriptImportConflict, err
	}
	scriptChanged := currentFingerprint != desiredFingerprint
	if !scriptChanged {
		script.UpdatedAt = current.UpdatedAt
	}
	ownershipCurrent := len(ownedTargets) == 1 && ownedTargets[0].targetKey == target.targetKey
	if !scriptChanged && ownershipCurrent && githubImportLinksEquivalent(existingLink, link) {
		*script = *current
		*link = *existingLink
		return GitHubScriptImportUnchanged, nil
	}
	if scriptChanged {
		if !script.UpdatedAt.After(current.UpdatedAt) {
			return GitHubScriptImportConflict, fmt.Errorf("github script import updated_at must advance the durable revision")
		}
		encoded, err := marshalScriptForStorage(script)
		if err != nil {
			return GitHubScriptImportConflict, err
		}
		var currentUpdatedRaw string
		if err := tx.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM scripts WHERE id = ?`, script.ID).Scan(&currentUpdatedRaw); err != nil {
			return GitHubScriptImportConflict, err
		}
		result, err := tx.Exec(`UPDATE scripts SET
			ref_key = ?, name = ?, protocol = ?, version = ?, folder_id = ?, generated_by = ?, generated_from = ?,
			metadata_json = ?, goal_config_json = ?, config = ?, data_sources = ?, parameters = ?, init = ?, actions = ?, teardown = ?, updated_at = ?
			WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`,
			script.RefKey, script.Name, script.Protocol, script.Version, script.FolderID, script.GeneratedBy, script.GeneratedFrom,
			encoded.metadata, encoded.goals, encoded.config, encoded.dataSources, encoded.parameters, encoded.init, encoded.actions, encoded.teardown,
			script.UpdatedAt, script.ID, script.ProjectID, currentUpdatedRaw)
		if err != nil {
			return GitHubScriptImportConflict, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return GitHubScriptImportConflict, err
		}
		if affected != 1 {
			return GitHubScriptImportConflict, nil
		}
	} else {
		*script = *current
	}
	link.RefKey = script.RefKey
	if err := s.upsertGitHubScriptLinkTx(tx, link); err != nil {
		return GitHubScriptImportConflict, err
	}
	if err := s.persistGitHubSyncTargetOwnersTx(tx, script.ID, script.ProjectID, targets, now); err != nil {
		return GitHubScriptImportConflict, err
	}
	if err := tx.Commit(); err != nil {
		return GitHubScriptImportConflict, err
	}
	return GitHubScriptImportUpdated, nil
}

// AcquireGitHubScriptSyncClaim grants an operation token exclusive, bounded
// ownership of one script's GitHub sync. The script row is the serialization
// anchor for the absent-claim case, where PostgreSQL has no claim row to lock.
func (s *SQLiteStore) AcquireGitHubScriptSyncClaim(scriptID, claimID string, targets []GitHubScriptSyncTarget, expiresAt time.Time) (bool, error) {
	scriptID = strings.TrimSpace(scriptID)
	claimID = strings.TrimSpace(claimID)
	if scriptID == "" || claimID == "" || expiresAt.IsZero() {
		return false, fmt.Errorf("github script sync claim requires script id, claim id, and expiry")
	}
	normalizedTargets, err := normalizeGitHubScriptSyncTargets(targets)
	if err != nil {
		return false, err
	}
	expiresAt = expiresAt.UTC()
	projectID, err := s.githubAnchorProject("scripts", scriptID)
	if err != nil {
		return false, fmt.Errorf("load github script sync claim anchor: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	if err := lockGitHubAnchor(tx, "scripts", scriptID, projectID); err != nil {
		return false, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read github script sync database clock: %w", err)
	}
	if !expiresAt.After(now) {
		return false, fmt.Errorf("github script sync claim expiry must be in the future")
	}
	if err := s.lockGitHubSyncTargetsTx(tx, normalizedTargets, now); err != nil {
		return false, err
	}

	query := `SELECT project_id, claim_id, expires_at FROM github_script_sync_claims WHERE script_id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var currentProjectID, currentClaimID string
	var currentExpiry time.Time
	err = tx.QueryRow(query, scriptID).Scan(&currentProjectID, &currentClaimID, &currentExpiry)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO github_script_sync_claims
			(script_id, project_id, claim_id, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, scriptID, projectID, claimID, expiresAt, now, now); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	default:
		if normalizeProjectID(currentProjectID) != projectID {
			return false, fmt.Errorf("github script sync claim %q belongs to another project", scriptID)
		}
		if currentExpiry.After(now) {
			return false, nil
		}
		result, err := tx.Exec(`UPDATE github_script_sync_claims
			SET project_id = ?, claim_id = ?, expires_at = ?, created_at = ?, updated_at = ?
			WHERE script_id = ? AND claim_id = ?`, projectID, claimID, expiresAt, now, now, scriptID, currentClaimID)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 1 {
			return false, nil
		}
	}
	if _, err := tx.Exec(`DELETE FROM github_script_sync_target_claims WHERE script_id = ? AND expires_at <= ?`, scriptID, now); err != nil {
		return false, err
	}
	for _, target := range normalizedTargets {
		conflict, err := githubSyncTargetConflictsWithOtherScriptTx(tx, scriptID, target)
		if err != nil {
			return false, err
		}
		if conflict {
			return false, nil
		}
		var currentProjectID, currentScriptID, currentClaimID string
		var currentExpiry time.Time
		err = tx.QueryRow(`SELECT project_id, script_id, claim_id, expires_at
			FROM github_script_sync_target_claims WHERE target_key = ?`, target.targetKey).
			Scan(&currentProjectID, &currentScriptID, &currentClaimID, &currentExpiry)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(`INSERT INTO github_script_sync_target_claims
				(target_key, project_id, script_id, claim_id, owner_norm, repo_norm, branch, path, expires_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, target.targetKey, projectID, scriptID, claimID,
				target.ownerNorm, target.repoNorm, target.branch, target.path, expiresAt, now, now); err != nil {
				return false, err
			}
		case err != nil:
			return false, err
		default:
			if currentExpiry.After(now) {
				return false, nil
			}
			result, err := tx.Exec(`UPDATE github_script_sync_target_claims SET
				project_id = ?, script_id = ?, claim_id = ?, owner_norm = ?, repo_norm = ?, branch = ?, path = ?,
				expires_at = ?, created_at = ?, updated_at = ?
				WHERE target_key = ? AND script_id = ? AND claim_id = ?`,
				projectID, scriptID, claimID, target.ownerNorm, target.repoNorm, target.branch, target.path,
				expiresAt, now, now, target.targetKey, currentScriptID, currentClaimID)
			if err != nil {
				return false, err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return false, err
			}
			if affected != 1 {
				return false, nil
			}
			_ = currentProjectID
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// RenewGitHubScriptSyncClaim extends (and never shortens) the same currently
// held, unexpired claim. Once a claim expires, its former owner must acquire a
// fresh operation token instead of reviving stale authority.
func (s *SQLiteStore) RenewGitHubScriptSyncClaim(scriptID, claimID string, expiresAt time.Time) (bool, error) {
	scriptID = strings.TrimSpace(scriptID)
	claimID = strings.TrimSpace(claimID)
	if scriptID == "" || claimID == "" || expiresAt.IsZero() {
		return false, fmt.Errorf("github script sync claim renewal requires script id, claim id, and expiry")
	}
	expiresAt = expiresAt.UTC()
	projectID, err := s.githubAnchorProject("scripts", scriptID)
	if err != nil {
		return false, fmt.Errorf("load github script sync claim anchor: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	if err := lockGitHubAnchor(tx, "scripts", scriptID, projectID); err != nil {
		return false, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read github script sync database clock: %w", err)
	}
	reservedTargets, _, err := githubSyncTargetRowsTx(tx, "github_script_sync_target_claims", scriptID, claimID)
	if err != nil {
		return false, err
	}
	if len(reservedTargets) == 0 {
		return false, nil
	}
	if err := s.lockGitHubSyncTargetsTx(tx, reservedTargets, now); err != nil {
		return false, err
	}
	now, err = preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("refresh github script sync database clock: %w", err)
	}
	if !expiresAt.After(now) {
		return false, fmt.Errorf("github script sync claim renewal expiry must be in the future")
	}
	query := `SELECT project_id, claim_id, expires_at FROM github_script_sync_claims WHERE script_id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var currentProjectID, currentClaimID string
	var currentExpiry time.Time
	if err := tx.QueryRow(query, scriptID).Scan(&currentProjectID, &currentClaimID, &currentExpiry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != projectID {
		return false, fmt.Errorf("github script sync claim %q belongs to another project", scriptID)
	}
	if currentClaimID != claimID || !currentExpiry.After(now) {
		return false, nil
	}
	lockedTargets, targetExpiries, err := githubSyncTargetRowsTx(tx, "github_script_sync_target_claims", scriptID, claimID)
	if err != nil {
		return false, err
	}
	if len(lockedTargets) != len(reservedTargets) {
		return false, nil
	}
	for index := range lockedTargets {
		if lockedTargets[index].targetKey != reservedTargets[index].targetKey || !targetExpiries[index].After(now) {
			return false, nil
		}
	}
	if expiresAt.After(currentExpiry) {
		result, err := tx.Exec(`UPDATE github_script_sync_claims SET expires_at = ?, updated_at = ?
			WHERE script_id = ? AND claim_id = ?`, expiresAt, now, scriptID, claimID)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 1 {
			return false, nil
		}
		result, err = tx.Exec(`UPDATE github_script_sync_target_claims SET expires_at = ?, updated_at = ?
			WHERE script_id = ? AND claim_id = ? AND expires_at > ?`, expiresAt, now, scriptID, claimID, now)
		if err != nil {
			return false, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != int64(len(lockedTargets)) {
			return false, nil
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// FinalizeGitHubScriptSyncClaim is the compare-and-set commit for a sync
// operation. A stale or expired worker cannot overwrite a newer link, and a
// successful finalization consumes the lease in the same transaction.
func (s *SQLiteStore) FinalizeGitHubScriptSyncClaim(link *GitHubScriptLink, claimID string) (bool, error) {
	claimID = strings.TrimSpace(claimID)
	if link == nil || strings.TrimSpace(link.ScriptID) == "" || claimID == "" {
		return false, fmt.Errorf("github script sync finalization requires link, script id, and claim id")
	}
	link.ScriptID = strings.TrimSpace(link.ScriptID)
	link.Owner = strings.TrimSpace(link.Owner)
	link.Repo = strings.TrimSpace(link.Repo)
	link.Branch = strings.TrimSpace(link.Branch)
	link.Path = strings.TrimSpace(link.Path)
	link.BaseBranch = strings.TrimSpace(link.BaseBranch)
	desiredTargets, err := githubScriptSyncTargetsForLink(link)
	if err != nil {
		return false, err
	}
	projectID, err := s.githubAnchorProject("scripts", link.ScriptID)
	if err != nil {
		return false, fmt.Errorf("load github script sync finalization anchor: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	if err := lockGitHubAnchor(tx, "scripts", link.ScriptID, projectID); err != nil {
		return false, err
	}
	reservedTargets, _, err := githubSyncTargetRowsTx(tx, "github_script_sync_target_claims", link.ScriptID, claimID)
	if err != nil {
		return false, err
	}
	if len(reservedTargets) == 0 {
		return false, nil
	}
	ownedTargets, err := githubOwnedSyncTargetsTx(tx, link.ScriptID)
	if err != nil {
		return false, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read github script sync database clock: %w", err)
	}
	if err := s.lockGitHubSyncTargetsTx(tx, mergeNormalizedGitHubSyncTargets(reservedTargets, ownedTargets), now); err != nil {
		return false, err
	}
	now, err = preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("refresh github script sync database clock: %w", err)
	}
	query := `SELECT project_id, claim_id, expires_at FROM github_script_sync_claims WHERE script_id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var currentProjectID, currentClaimID string
	var currentExpiry time.Time
	if err := tx.QueryRow(query, link.ScriptID).Scan(&currentProjectID, &currentClaimID, &currentExpiry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != projectID || currentClaimID != claimID || !currentExpiry.After(now) {
		return false, nil
	}
	lockedTargets, targetExpiries, err := githubSyncTargetRowsTx(tx, "github_script_sync_target_claims", link.ScriptID, claimID)
	if err != nil {
		return false, err
	}
	if len(lockedTargets) != len(reservedTargets) || len(lockedTargets) != len(desiredTargets) {
		return false, nil
	}
	for index := range lockedTargets {
		if lockedTargets[index].targetKey != reservedTargets[index].targetKey ||
			lockedTargets[index].targetKey != desiredTargets[index].targetKey ||
			!targetExpiries[index].After(now) {
			return false, nil
		}
	}
	if err := prepareGitHubScriptLink(link, now); err != nil {
		return false, err
	}
	if err := s.upsertGitHubScriptLinkTx(tx, link); err != nil {
		return false, err
	}
	if err := s.persistGitHubSyncTargetOwnersTx(tx, link.ScriptID, projectID, desiredTargets, now); err != nil {
		return false, err
	}
	targetResult, err := tx.Exec(`DELETE FROM github_script_sync_target_claims
		WHERE script_id = ? AND claim_id = ? AND expires_at > ?`, link.ScriptID, claimID, now)
	if err != nil {
		return false, err
	}
	targetsDeleted, err := targetResult.RowsAffected()
	if err != nil {
		return false, err
	}
	if targetsDeleted != int64(len(desiredTargets)) {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM github_script_sync_claims
		WHERE script_id = ? AND claim_id = ? AND expires_at > ?`, link.ScriptID, claimID, now)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseGitHubScriptSyncClaim abandons only the caller's own claim. A stale
// worker cannot release a replacement claim acquired after its lease expired.
func (s *SQLiteStore) ReleaseGitHubScriptSyncClaim(scriptID, claimID string) (bool, error) {
	scriptID = strings.TrimSpace(scriptID)
	claimID = strings.TrimSpace(claimID)
	if scriptID == "" || claimID == "" {
		return false, fmt.Errorf("github script sync claim release requires script id and claim id")
	}
	projectID, err := s.githubAnchorProject("scripts", scriptID)
	if err != nil {
		return false, fmt.Errorf("load github script sync claim anchor: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	if err := lockGitHubAnchor(tx, "scripts", scriptID, projectID); err != nil {
		return false, err
	}
	targets, _, err := githubSyncTargetRowsTx(tx, "github_script_sync_target_claims", scriptID, claimID)
	if err != nil {
		return false, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read github script sync database clock: %w", err)
	}
	if err := s.lockGitHubSyncTargetsTx(tx, targets, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM github_script_sync_target_claims WHERE script_id = ? AND claim_id = ?`, scriptID, claimID); err != nil {
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM github_script_sync_claims WHERE script_id = ? AND project_id = ? AND claim_id = ?`, scriptID, projectID, claimID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected == 1, nil
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

func rejectActiveGitHubScriptSyncClaimTx(tx *storeTx, scriptID string) error {
	now, err := preciseDatabaseTime(tx, tx.dialect)
	if err != nil {
		return fmt.Errorf("read github script sync database clock: %w", err)
	}
	var active int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM github_script_sync_claims WHERE script_id = ? AND expires_at > ?) +
		(SELECT COUNT(*) FROM github_script_sync_target_claims WHERE script_id = ? AND expires_at > ?)`,
		scriptID, now, scriptID, now).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("%w for script %q", ErrGitHubScriptSyncBusy, scriptID)
	}
	return nil
}

func rejectActiveGitHubProjectSyncClaimsTx(tx *storeTx, projectID string) error {
	now, err := preciseDatabaseTime(tx, tx.dialect)
	if err != nil {
		return fmt.Errorf("read github script sync database clock: %w", err)
	}
	var active int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM github_script_sync_claims WHERE project_id = ? AND expires_at > ?) +
		(SELECT COUNT(*) FROM github_script_sync_target_claims WHERE project_id = ? AND expires_at > ?)`,
		projectID, now, projectID, now).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("%w for project %q", ErrGitHubScriptSyncBusy, projectID)
	}
	return nil
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
	var baseMissing any
	err := s.db.QueryRow(`SELECT script_id, ref_key, owner, repo, branch, path, last_commit_sha,
		COALESCE(remote_blob_sha, ''), COALESCE(remote_commit_sha, ''), COALESCE(base_branch, ''), COALESCE(base_blob_sha, ''), base_missing,
		content_hash, synced_at, updated_at FROM github_script_links WHERE script_id = ?`, scriptID).
		Scan(&link.ScriptID, &link.RefKey, &link.Owner, &link.Repo, &link.Branch, &link.Path,
			&link.LastCommitSHA, &link.RemoteBlobSHA, &link.RemoteCommitSHA,
			&link.BaseBranch, &link.BaseBlobSHA, &baseMissing,
			&link.ContentHash, &link.SyncedAt, &link.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if link.RemoteBlobSHA == "" {
		link.RemoteBlobSHA = link.LastCommitSHA
	}
	link.BaseMissing = sqlBool(baseMissing)
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
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return fmt.Errorf("github script link deletion requires a script id")
	}
	var projectID string
	if err := s.db.QueryRow(`SELECT COALESCE(NULLIF(project_id, ''), ?) FROM scripts WHERE id = ?`, DefaultProjectID, scriptID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	projectID = normalizeProjectID(projectID)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return err
	}
	if err := lockGitHubAnchor(tx, "scripts", scriptID, projectID); err != nil {
		return err
	}
	ownedTargets, err := githubOwnedSyncTargetsTx(tx, scriptID)
	if err != nil {
		return err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return fmt.Errorf("read github script sync database clock: %w", err)
	}
	if err := s.lockGitHubSyncTargetsTx(tx, ownedTargets, now); err != nil {
		return err
	}
	if err := rejectActiveGitHubScriptSyncClaimTx(tx, scriptID); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM github_script_sync_target_claims WHERE script_id = ?`,
		`DELETE FROM github_script_sync_claims WHERE script_id = ?`,
		`DELETE FROM github_script_sync_target_owners WHERE script_id = ?`,
		`DELETE FROM github_script_links WHERE script_id = ?`,
	} {
		if _, err := tx.Exec(query, scriptID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	_, err := s.saveGitHubCheckLink(link, "")
	return err
}

// SaveGitHubCheckLinkIfCreationClaimHeld atomically verifies that claimant
// still owns an unexpired creation claim before persisting the controller-owned
// CI Check identity. This prevents a replica whose lease expired during a slow
// GitHub request from committing a stale or duplicate remote Check.
func (s *SQLiteStore) SaveGitHubCheckLinkIfCreationClaimHeld(link *GitHubCheckLink, claimant string) (bool, error) {
	claimant = strings.TrimSpace(claimant)
	if claimant == "" {
		return false, fmt.Errorf("github check creation claimant is required")
	}
	return s.saveGitHubCheckLink(link, claimant)
}

func (s *SQLiteStore) saveGitHubCheckLink(link *GitHubCheckLink, creationClaimant string) (bool, error) {
	if link == nil || strings.TrimSpace(link.ID) == "" {
		return false, fmt.Errorf("github check link requires an id")
	}
	link.ID = strings.TrimSpace(link.ID)
	link.Kind = strings.ToLower(strings.TrimSpace(link.Kind))
	if link.Kind == "" {
		link.Kind = "manual"
	}
	link.TestID = strings.TrimSpace(link.TestID)
	link.ScriptID = strings.TrimSpace(link.ScriptID)
	switch link.Kind {
	case "manual":
	case "ci":
		if link.TestID == "" || link.ScriptID != "" {
			return false, fmt.Errorf("controller CI github check links require a test-only anchor")
		}
		expectedID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+link.TestID)).String()
		if link.ID != expectedID {
			return false, fmt.Errorf("controller CI github check link id does not match its test")
		}
	default:
		return false, fmt.Errorf("github check link kind must be manual or ci")
	}
	if creationClaimant != "" && link.Kind != "ci" {
		return false, fmt.Errorf("github check creation claims apply only to controller CI links")
	}
	if link.TestID == "" && link.ScriptID == "" {
		return false, fmt.Errorf("github check link requires a test or script anchor")
	}
	projectID := ""
	if link.TestID != "" {
		var err error
		projectID, err = s.githubAnchorProject("tests", link.TestID)
		if err != nil {
			return false, fmt.Errorf("load github check test anchor: %w", err)
		}
	}
	if link.ScriptID != "" {
		scriptProjectID, err := s.githubAnchorProject("scripts", link.ScriptID)
		if err != nil {
			return false, fmt.Errorf("load github check script anchor: %w", err)
		}
		if projectID != "" && projectID != scriptProjectID {
			return false, fmt.Errorf("github check test and script anchors belong to different projects")
		}
		projectID = scriptProjectID
	}
	now := time.Now().UTC()
	if link.PublishedAt.IsZero() {
		link.PublishedAt = now
	}
	link.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	if link.TestID != "" {
		if err := lockGitHubAnchor(tx, "tests", link.TestID, projectID); err != nil {
			return false, err
		}
	}
	if link.ScriptID != "" {
		if err := lockGitHubAnchor(tx, "scripts", link.ScriptID, projectID); err != nil {
			return false, err
		}
	}
	if creationClaimant != "" {
		query := `SELECT project_id, COALESCE(creation_claim, ''), creation_claim_expires_at FROM github_check_intents WHERE test_id = ?`
		if s.db.dialect == storeDialectPostgres {
			query += ` FOR UPDATE`
		}
		var claimProjectID, currentClaim string
		var currentExpiry sql.NullTime
		if err := tx.QueryRow(query, link.TestID).Scan(&claimProjectID, &currentClaim, &currentExpiry); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		databaseNow, err := preciseDatabaseTime(tx, s.db.dialect)
		if err != nil {
			return false, fmt.Errorf("read github check creation database clock: %w", err)
		}
		if claimProjectID != projectID || currentClaim != creationClaimant || !currentExpiry.Valid || !currentExpiry.Time.After(databaseNow) {
			return false, nil
		}
	}
	var currentKind, currentTestID, currentScriptID, currentStatus, currentConclusion, currentPendingConclusion string
	err = tx.QueryRow(`SELECT COALESCE(kind, ''), COALESCE(test_id, ''), COALESCE(script_id, ''), COALESCE(status, ''), COALESCE(conclusion, ''), COALESCE(pending_conclusion, '') FROM github_check_links WHERE id = ?`, link.ID).
		Scan(&currentKind, &currentTestID, &currentScriptID, &currentStatus, &currentConclusion, &currentPendingConclusion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && (currentTestID != link.TestID || currentScriptID != link.ScriptID || (currentKind != "" && !strings.EqualFold(currentKind, link.Kind))) {
		return false, fmt.Errorf("github check link %q belongs to different anchors", link.ID)
	}
	if err == nil && strings.EqualFold(strings.TrimSpace(currentStatus), "completed") && strings.TrimSpace(currentConclusion) != "" &&
		(!strings.EqualFold(strings.TrimSpace(link.Status), "completed") ||
			!strings.EqualFold(strings.TrimSpace(link.Conclusion), strings.TrimSpace(currentConclusion)) ||
			strings.TrimSpace(link.PendingConclusion) != "") {
		return false, fmt.Errorf("github check link %q already has immutable terminal conclusion %q", link.ID, currentConclusion)
	}
	if err == nil && strings.TrimSpace(currentPendingConclusion) != "" {
		completesClaim := strings.EqualFold(strings.TrimSpace(link.Status), "completed") &&
			strings.EqualFold(strings.TrimSpace(link.Conclusion), strings.TrimSpace(currentPendingConclusion)) &&
			strings.TrimSpace(link.PendingConclusion) == ""
		preservesClaim := strings.EqualFold(strings.TrimSpace(link.PendingConclusion), strings.TrimSpace(currentPendingConclusion))
		if !completesClaim && !preservesClaim {
			return false, fmt.Errorf("github check link %q has pending terminal conclusion %q", link.ID, currentPendingConclusion)
		}
	}
	result, err := tx.Exec(`INSERT INTO github_check_links
		(id, kind, test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, pending_conclusion, published_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind=excluded.kind,
			owner=excluded.owner,
			repo=excluded.repo,
			commit_sha=excluded.commit_sha,
			check_run_id=excluded.check_run_id,
			check_run_url=excluded.check_run_url,
			status=excluded.status,
			conclusion=excluded.conclusion,
			pending_conclusion=excluded.pending_conclusion,
			updated_at=excluded.updated_at
		WHERE COALESCE(github_check_links.kind, excluded.kind) = excluded.kind
			AND COALESCE(github_check_links.test_id, '') = excluded.test_id
			AND COALESCE(github_check_links.script_id, '') = excluded.script_id`,
		link.ID, link.Kind, link.TestID, link.ScriptID, link.Owner, link.Repo, link.CommitSHA, link.CheckRunID, link.CheckRunURL,
		link.Status, link.Conclusion, link.PendingConclusion, link.PublishedAt, link.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, fmt.Errorf("github check link %q belongs to different anchors", link.ID)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) GetGitHubCheckLink(id string) (*GitHubCheckLink, error) {
	link := &GitHubCheckLink{}
	err := s.db.QueryRow(`SELECT id, COALESCE(kind, ''), test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, COALESCE(pending_conclusion, ''), published_at, updated_at
		FROM github_check_links WHERE id = ?`, id).
		Scan(&link.ID, &link.Kind, &link.TestID, &link.ScriptID, &link.Owner, &link.Repo, &link.CommitSHA, &link.CheckRunID,
			&link.CheckRunURL, &link.Status, &link.Conclusion, &link.PendingConclusion, &link.PublishedAt, &link.UpdatedAt)
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
	query := `SELECT id, COALESCE(kind, ''), test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, COALESCE(pending_conclusion, ''), published_at, updated_at FROM github_check_links`
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
		if err := rows.Scan(&link.ID, &link.Kind, &link.TestID, &link.ScriptID, &link.Owner, &link.Repo, &link.CommitSHA, &link.CheckRunID,
			&link.CheckRunURL, &link.Status, &link.Conclusion, &link.PendingConclusion, &link.PublishedAt, &link.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListIncompleteGitHubCheckLinks(limit int) ([]*GitHubCheckLink, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT l.id, COALESCE(l.kind, ''), l.test_id, l.script_id, l.owner, l.repo, l.commit_sha, l.check_run_id, l.check_run_url, l.status, l.conclusion, COALESCE(l.pending_conclusion, ''), l.published_at, l.updated_at
		FROM github_check_links l
		JOIN tests t ON t.id = l.test_id
		WHERE (LOWER(l.status) != 'completed' OR TRIM(l.conclusion) = '')
			AND LOWER(COALESCE(l.kind, '')) = 'ci'
			AND UPPER(TRIM(t.status)) IN ('COMPLETE', 'FAILED', 'ABORTED')
		ORDER BY l.updated_at ASC, l.published_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*GitHubCheckLink, 0)
	for rows.Next() {
		link := &GitHubCheckLink{}
		if err := rows.Scan(&link.ID, &link.Kind, &link.TestID, &link.ScriptID, &link.Owner, &link.Repo, &link.CommitSHA, &link.CheckRunID,
			&link.CheckRunURL, &link.Status, &link.Conclusion, &link.PendingConclusion, &link.PublishedAt, &link.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// TouchGitHubCheckLink rotates a failed reconciliation attempt to the back of
// the oldest-first queue without changing its durable status or claimed
// conclusion.
func (s *SQLiteStore) TouchGitHubCheckLink(id string, updatedAt time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("github check link id is required")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	} else {
		updatedAt = updatedAt.UTC()
	}
	result, err := s.db.Exec(`UPDATE github_check_links SET updated_at = ? WHERE id = ?`, updatedAt, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("github check link %q was not found", id)
	}
	return nil
}

// ClaimGitHubCheckConclusion atomically chooses the sole terminal conclusion
// for a Check before any controller replica calls GitHub. A retry or competing
// replica receives the already-claimed value and must converge on it.
func (s *SQLiteStore) ClaimGitHubCheckConclusion(id string, conclusion string) (*GitHubCheckLink, error) {
	id = strings.TrimSpace(id)
	conclusion = strings.TrimSpace(conclusion)
	if id == "" || conclusion == "" {
		return nil, fmt.Errorf("github check link id and conclusion are required")
	}
	initial, err := s.GetGitHubCheckLink(id)
	if err != nil || initial == nil {
		if err == nil {
			err = fmt.Errorf("github check link %q was not found", id)
		}
		return nil, err
	}
	if strings.TrimSpace(initial.TestID) == "" {
		return nil, fmt.Errorf("github check link %q is not test anchored", id)
	}
	projectID, err := s.githubAnchorProject("tests", initial.TestID)
	if err != nil {
		return nil, fmt.Errorf("load github check test anchor: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return nil, err
	}
	if err := lockGitHubAnchor(tx, "tests", initial.TestID, projectID); err != nil {
		return nil, err
	}
	query := `SELECT id, COALESCE(kind, ''), test_id, script_id, owner, repo, commit_sha, check_run_id, check_run_url, status, conclusion, COALESCE(pending_conclusion, ''), published_at, updated_at
		FROM github_check_links WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	link := &GitHubCheckLink{}
	if err := tx.QueryRow(query, id).Scan(&link.ID, &link.Kind, &link.TestID, &link.ScriptID, &link.Owner, &link.Repo, &link.CommitSHA,
		&link.CheckRunID, &link.CheckRunURL, &link.Status, &link.Conclusion, &link.PendingConclusion, &link.PublishedAt, &link.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("github check link %q was not found", id)
		}
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(link.Status), "completed") && strings.TrimSpace(link.Conclusion) != "" {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return link, nil
	}
	if strings.TrimSpace(link.PendingConclusion) == "" {
		link.PendingConclusion = conclusion
		link.UpdatedAt = time.Now().UTC()
		if _, err := tx.Exec(`UPDATE github_check_links SET pending_conclusion = ?, updated_at = ? WHERE id = ?`, link.PendingConclusion, link.UpdatedAt, link.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return link, nil
}

func (s *SQLiteStore) SaveGitHubCheckIntent(intent *GitHubCheckIntent) error {
	if intent == nil {
		return fmt.Errorf("github check intent is required")
	}
	intent.TestID = strings.TrimSpace(intent.TestID)
	intent.ProjectID = strings.TrimSpace(intent.ProjectID)
	intent.Owner = strings.TrimSpace(intent.Owner)
	intent.Repo = strings.TrimSpace(intent.Repo)
	intent.CommitSHA = strings.TrimSpace(intent.CommitSHA)
	if intent.TestID == "" || intent.ProjectID == "" || intent.InstallationID <= 0 || intent.Owner == "" || intent.Repo == "" || intent.CommitSHA == "" {
		return fmt.Errorf("github check intent requires test, project, installation, repository, and commit")
	}
	now := time.Now().UTC()
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = now
	}
	intent.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, intent.ProjectID); err != nil {
		return err
	}
	var currentProject, currentOwner, currentRepo, currentSHA string
	var currentInstallation int64
	err = tx.QueryRow(`SELECT project_id, installation_id, owner, repo, commit_sha FROM github_check_intents WHERE test_id = ?`, intent.TestID).
		Scan(&currentProject, &currentInstallation, &currentOwner, &currentRepo, &currentSHA)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (currentProject != intent.ProjectID || currentInstallation != intent.InstallationID ||
		!strings.EqualFold(currentOwner, intent.Owner) || !strings.EqualFold(currentRepo, intent.Repo) || !strings.EqualFold(currentSHA, intent.CommitSHA)) {
		return fmt.Errorf("github check intent %q is already bound to different CI metadata", intent.TestID)
	}
	if _, err := tx.Exec(`INSERT INTO github_check_intents
		(test_id, project_id, installation_id, owner, repo, commit_sha, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET updated_at=excluded.updated_at
		WHERE github_check_intents.project_id=excluded.project_id
			AND github_check_intents.installation_id=excluded.installation_id
			AND LOWER(github_check_intents.owner)=LOWER(excluded.owner)
			AND LOWER(github_check_intents.repo)=LOWER(excluded.repo)
			AND LOWER(github_check_intents.commit_sha)=LOWER(excluded.commit_sha)`,
		intent.TestID, intent.ProjectID, intent.InstallationID, intent.Owner, intent.Repo, intent.CommitSHA, intent.CreatedAt, intent.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetGitHubCheckIntent(testID string) (*GitHubCheckIntent, error) {
	intent := &GitHubCheckIntent{}
	var claim sql.NullString
	var claimExpires sql.NullTime
	err := s.db.QueryRow(`SELECT test_id, project_id, installation_id, owner, repo, commit_sha, creation_claim, creation_claim_expires_at, created_at, updated_at
		FROM github_check_intents WHERE test_id = ?`, strings.TrimSpace(testID)).
		Scan(&intent.TestID, &intent.ProjectID, &intent.InstallationID, &intent.Owner, &intent.Repo, &intent.CommitSHA, &claim, &claimExpires, &intent.CreatedAt, &intent.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	intent.CreationClaim = claim.String
	if claimExpires.Valid {
		expires := claimExpires.Time
		intent.CreationClaimExpiresAt = &expires
	}
	return intent, nil
}

func (s *SQLiteStore) ListPendingGitHubCheckIntents(limit int) ([]*GitHubCheckIntent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	// Only materialized tests are actionable. Pre-materialization intents remain
	// durable for request retry, but cannot occupy the reconciliation page and
	// starve newer runs if a controller crashed before creating their tests.
	rows, err := s.db.Query(`SELECT i.test_id, i.project_id, i.installation_id, i.owner, i.repo, i.commit_sha, i.creation_claim, i.creation_claim_expires_at, i.created_at, i.updated_at
		FROM github_check_intents i
		JOIN tests t ON t.id = i.test_id
		ORDER BY i.updated_at ASC, i.created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := make([]*GitHubCheckIntent, 0)
	for rows.Next() {
		intent := &GitHubCheckIntent{}
		var claim sql.NullString
		var claimExpires sql.NullTime
		if err := rows.Scan(&intent.TestID, &intent.ProjectID, &intent.InstallationID, &intent.Owner, &intent.Repo, &intent.CommitSHA, &claim, &claimExpires, &intent.CreatedAt, &intent.UpdatedAt); err != nil {
			return nil, err
		}
		intent.CreationClaim = claim.String
		if claimExpires.Valid {
			expires := claimExpires.Time
			intent.CreationClaimExpiresAt = &expires
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

// TouchGitHubCheckIntent rotates an existing failed intent without recreating
// an outbox row that a concurrent successful Check creation already deleted.
func (s *SQLiteStore) TouchGitHubCheckIntent(testID string, updatedAt time.Time) error {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return fmt.Errorf("github check intent test id is required")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	} else {
		updatedAt = updatedAt.UTC()
	}
	_, err := s.db.Exec(`UPDATE github_check_intents SET updated_at = ? WHERE test_id = ?`, updatedAt, testID)
	return err
}

// ClaimGitHubCheckCreation grants one controller replica a bounded lease around
// the remote Find/Create/save sequence. GitHub does not enforce external_id
// uniqueness, so the process-local mutex alone cannot prevent duplicate Check
// runs when HA replicas reconcile the same durable intent concurrently.
func (s *SQLiteStore) ClaimGitHubCheckCreation(testID string, claimant string, lease time.Duration) (bool, error) {
	testID = strings.TrimSpace(testID)
	claimant = strings.TrimSpace(claimant)
	if testID == "" || claimant == "" || lease <= 0 {
		return false, fmt.Errorf("github check creation claim requires test id, claimant, and positive lease")
	}
	intent, err := s.GetGitHubCheckIntent(testID)
	if err != nil {
		return false, err
	}
	if intent == nil {
		return false, fmt.Errorf("github check intent %q was not found", testID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, intent.ProjectID); err != nil {
		return false, err
	}
	query := `SELECT COALESCE(creation_claim, ''), creation_claim_expires_at FROM github_check_intents WHERE test_id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var currentClaim string
	var currentExpiry sql.NullTime
	if err := tx.QueryRow(query, testID).Scan(&currentClaim, &currentExpiry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("github check intent %q was not found", testID)
		}
		return false, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read github check creation database clock: %w", err)
	}
	if currentClaim != "" && currentClaim != claimant && currentExpiry.Valid && currentExpiry.Time.After(now) {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	expiresAt := now.Add(lease)
	if _, err := tx.Exec(`UPDATE github_check_intents SET creation_claim = ?, creation_claim_expires_at = ?, updated_at = ? WHERE test_id = ?`, claimant, expiresAt, now, testID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// RenewGitHubCheckCreation extends only a currently held, unexpired creation
// claim. An expired claimant must recover through the durable external-id lookup
// rather than silently reacquiring ownership after another replica may have won.
func (s *SQLiteStore) RenewGitHubCheckCreation(testID string, claimant string, lease time.Duration) (bool, error) {
	testID = strings.TrimSpace(testID)
	claimant = strings.TrimSpace(claimant)
	if testID == "" || claimant == "" || lease <= 0 {
		return false, fmt.Errorf("github check creation renewal requires test id, claimant, and positive lease")
	}
	intent, err := s.GetGitHubCheckIntent(testID)
	if err != nil {
		return false, err
	}
	if intent == nil {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, intent.ProjectID); err != nil {
		return false, err
	}
	query := `SELECT COALESCE(creation_claim, ''), creation_claim_expires_at FROM github_check_intents WHERE test_id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var currentClaim string
	var currentExpiry sql.NullTime
	if err := tx.QueryRow(query, testID).Scan(&currentClaim, &currentExpiry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read github check creation database clock: %w", err)
	}
	if currentClaim != claimant || !currentExpiry.Valid || !currentExpiry.Time.After(now) {
		return false, nil
	}
	expiresAt := now.Add(lease)
	result, err := tx.Exec(`UPDATE github_check_intents SET creation_claim_expires_at = ?, updated_at = ? WHERE test_id = ? AND creation_claim = ?`, expiresAt, now, testID, claimant)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) ReleaseGitHubCheckCreation(testID string, claimant string) error {
	testID = strings.TrimSpace(testID)
	claimant = strings.TrimSpace(claimant)
	if testID == "" || claimant == "" {
		return nil
	}
	intent, err := s.GetGitHubCheckIntent(testID)
	if err != nil || intent == nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, intent.ProjectID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE github_check_intents SET creation_claim = NULL, creation_claim_expires_at = NULL, updated_at = ? WHERE test_id = ? AND creation_claim = ?`, time.Now().UTC(), testID, claimant); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteGitHubCheckIntent(testID string) error {
	_, err := s.db.Exec(`DELETE FROM github_check_intents WHERE test_id = ?`, strings.TrimSpace(testID))
	return err
}

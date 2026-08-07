package storage

// SQLite store — fleet pools, generator metadata, and cloud provisioning.
// Same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---- helpers -------------------------------------------------------------

func marshalTags(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func unmarshalTags(raw string) map[string]string {
	if raw == "" || raw == "null" {
		return nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

// ---- fleet pools ---------------------------------------------------------

func (s *SQLiteStore) SaveFleetPool(pool *FleetPool) error {
	if pool == nil {
		return fmt.Errorf("fleet pool is required")
	}
	if pool.Status == "" {
		pool.Status = "active"
	}
	now := time.Now()
	if pool.CreatedAt.IsZero() {
		pool.CreatedAt = now
	}
	pool.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO fleet_pools (
			id, name, description, cloud_provider, region, min_size, max_size, desired_size, status, tags_json,
			cluster_id, allocation_json, domain_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description, cloud_provider=excluded.cloud_provider,
			region=excluded.region, min_size=excluded.min_size, max_size=excluded.max_size,
			desired_size=excluded.desired_size, status=excluded.status, tags_json=excluded.tags_json,
			cluster_id=excluded.cluster_id, allocation_json=excluded.allocation_json,
			domain_id=excluded.domain_id,
			updated_at=excluded.updated_at`,
		pool.ID, pool.Name, pool.Description, pool.CloudProvider, pool.Region, pool.MinSize, pool.MaxSize,
		pool.DesiredSize, pool.Status, marshalTags(pool.Tags), pool.ClusterID, marshalAllocation(pool.Allocation),
		pool.DomainID, pool.CreatedAt, pool.UpdatedAt)
	return err
}

// CreateFleetPoolWithMembers creates the pool and its initial membership in a
// single transaction so a bad member cannot leave an orphaned partial pool.
func (s *SQLiteStore) CreateFleetPoolWithMembers(pool *FleetPool, members []*FleetPoolMember) error {
	if pool == nil || strings.TrimSpace(pool.ID) == "" {
		return errors.New("fleet pool id is required")
	}
	if strings.TrimSpace(pool.Name) == "" || strings.TrimSpace(pool.CloudProvider) == "" {
		return errors.New("fleet pool name and cloud provider are required")
	}
	if pool.Status == "" {
		pool.Status = "active"
	}
	now := time.Now()
	if pool.CreatedAt.IsZero() {
		pool.CreatedAt = now
	}
	pool.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO fleet_pools (
		id, name, description, cloud_provider, region, min_size, max_size, desired_size, status, tags_json,
		cluster_id, allocation_json, domain_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, pool.ID, pool.Name, pool.Description, pool.CloudProvider, pool.Region,
		pool.MinSize, pool.MaxSize, pool.DesiredSize, pool.Status, marshalTags(pool.Tags), pool.ClusterID,
		marshalAllocation(pool.Allocation), pool.DomainID, pool.CreatedAt, pool.UpdatedAt); err != nil {
		return err
	}
	if err := replaceFleetPoolMembersTx(tx, pool.ID, members); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateFleetPoolIfUnchanged applies pool fields and an optional full member
// replacement under one optimistic transaction.
func (s *SQLiteStore) UpdateFleetPoolIfUnchanged(pool *FleetPool, members []*FleetPoolMember, replaceMembers bool, expectedUpdatedAt time.Time) (bool, error) {
	if pool == nil || strings.TrimSpace(pool.ID) == "" {
		return false, errors.New("fleet pool id is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected fleet pool updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM fleet_pools WHERE id = ?`, pool.ID).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	pool.UpdatedAt = time.Now()
	// domain_id MUST be in this SET list. This method writes exactly what it is
	// given, so omitting the column would silently reset a constrained pool to
	// unscoped on every unrelated edit — a silent widening of blast radius.
	result, err := tx.Exec(`UPDATE fleet_pools SET name = ?, description = ?, cloud_provider = ?, region = ?, min_size = ?, max_size = ?, desired_size = ?, status = ?, tags_json = ?, cluster_id = ?, allocation_json = ?, domain_id = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, pool.Name, pool.Description, pool.CloudProvider, pool.Region, pool.MinSize, pool.MaxSize,
		pool.DesiredSize, pool.Status, marshalTags(pool.Tags), pool.ClusterID, marshalAllocation(pool.Allocation),
		pool.DomainID, pool.UpdatedAt, pool.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	if replaceMembers {
		if err := replaceFleetPoolMembersTx(tx, pool.ID, members); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// UpdateFleetPoolDesiredSize updates only provisioner-owned state so a scaling
// completion cannot restore stale administrator fields.
func (s *SQLiteStore) UpdateFleetPoolDesiredSize(id string, desiredSize int, updatedAt time.Time) error {
	result, err := s.db.Exec(`UPDATE fleet_pools SET desired_size = ?, updated_at = ? WHERE id = ?`, desiredSize, updatedAt, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

const fleetPoolSelect = `SELECT id, name, COALESCE(description, ''), cloud_provider, COALESCE(region, ''),
	min_size, max_size, desired_size, COALESCE(status, 'active'), COALESCE(tags_json, ''),
	COALESCE(cluster_id, ''), COALESCE(allocation_json, ''), COALESCE(domain_id, ''), created_at, updated_at
	FROM fleet_pools`

func scanFleetPool(row capacityRowScanner) (*FleetPool, error) {
	var pool FleetPool
	var tagsJSON string
	var allocationJSON string
	if err := row.Scan(&pool.ID, &pool.Name, &pool.Description, &pool.CloudProvider, &pool.Region,
		&pool.MinSize, &pool.MaxSize, &pool.DesiredSize, &pool.Status, &tagsJSON,
		&pool.ClusterID, &allocationJSON, &pool.DomainID, &pool.CreatedAt, &pool.UpdatedAt); err != nil {
		return nil, err
	}
	pool.Tags = unmarshalTags(tagsJSON)
	pool.Allocation = unmarshalAllocation(allocationJSON)
	return &pool, nil
}

func (s *SQLiteStore) GetFleetPool(id string) (*FleetPool, error) {
	pool, err := scanFleetPool(s.db.QueryRow(fleetPoolSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	members, err := s.ListFleetPoolMembers(id)
	if err != nil {
		return nil, err
	}
	pool.Members = members
	return pool, nil
}

func (s *SQLiteStore) ListFleetPools(limit int, offset int) ([]*FleetPool, error) {
	query := fleetPoolSelect + ` ORDER BY created_at DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	pools := make([]*FleetPool, 0)
	for rows.Next() {
		pool, err := scanFleetPool(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		pools = append(pools, pool)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// Populate members so list and get responses carry the same shape (the pool
	// count is admin-scale, so the per-pool member lookup is acceptable).
	for _, pool := range pools {
		members, err := s.ListFleetPoolMembers(pool.ID)
		if err != nil {
			return nil, err
		}
		pool.Members = members
	}
	return pools, nil
}

func (s *SQLiteStore) DeleteFleetPool(id string) error {
	// Application-level integrity (SQLite FK enforcement is not relied upon):
	// detach members and generator metadata before removing the pool. All three
	// writes run in one transaction so a mid-cascade failure cannot leave orphaned
	// members or generator_metadata referencing a deleted pool (matches the
	// DeleteScenario cascade convention).
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the pool row against CreateProvisioningRequestIfAbsent. Whichever
	// operation wins first establishes the invariant: deletion either prevents
	// the request from being created, or observes it and refuses to orphan an
	// external scale attempt.
	result, err := tx.Exec(`UPDATE fleet_pools SET updated_at = updated_at WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	var activeRequests int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM provisioning_requests
		WHERE fleet_pool_id = ? AND LOWER(TRIM(COALESCE(status, ''))) NOT IN ('success', 'failed', 'not_configured', 'cancelled', 'timed_out')`, id).Scan(&activeRequests); err != nil {
		return err
	}
	if activeRequests > 0 {
		return ErrFleetPoolHasActiveProvisioning
	}
	if _, err := tx.Exec(`UPDATE generator_metadata SET fleet_pool_id = NULL, updated_at = ? WHERE fleet_pool_id = ?`, time.Now(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fleet_pool_members WHERE pool_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fleet_pools WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveFleetPoolMembers replaces a pool's entire membership: it clears existing
// rows then inserts the supplied set. The ON CONFLICT clause is not redundant —
// it tolerates duplicate generator_ids within the input slice. The clear+insert
// runs in one transaction so a failed insert cannot leave the pool with the old
// membership wiped and the new set only partially applied.
func (s *SQLiteStore) SaveFleetPoolMembers(poolID string, members []*FleetPoolMember) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceFleetPoolMembersTx(tx, poolID, members); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceFleetPoolMembersTx(tx *storeTx, poolID string, members []*FleetPoolMember) error {
	if _, err := tx.Exec(`DELETE FROM fleet_pool_members WHERE pool_id = ?`, poolID); err != nil {
		return err
	}
	// Release the legacy metadata pointer for generators this replace drops.
	// Without it the members table says the generator is free while the pointer
	// still names this pool, so it would be counted in the pool's runtime
	// targets and simultaneously claimable by another pool.
	retained := make(map[string]struct{}, len(members))
	for _, m := range members {
		if m != nil {
			retained[m.GeneratorID] = struct{}{}
		}
	}
	rows, err := tx.Query(`SELECT generator_id FROM generator_metadata WHERE fleet_pool_id = ?`, poolID)
	if err != nil {
		return err
	}
	orphaned := make([]string, 0)
	for rows.Next() {
		var generatorID string
		if err := rows.Scan(&generatorID); err != nil {
			rows.Close()
			return err
		}
		if _, keep := retained[generatorID]; !keep {
			orphaned = append(orphaned, generatorID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, generatorID := range orphaned {
		if _, err := tx.Exec(`UPDATE generator_metadata SET fleet_pool_id = NULL WHERE generator_id = ?`, generatorID); err != nil {
			return err
		}
	}
	for _, m := range members {
		if m == nil {
			continue
		}
		if m.JoinedAt.IsZero() {
			m.JoinedAt = time.Now()
		}
		if m.Status == "" {
			m.Status = "provisioned"
		}
		if _, err := tx.Exec(`INSERT INTO fleet_pool_members (id, pool_id, generator_id, target, status, joined_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(pool_id, generator_id) DO UPDATE SET
				target=excluded.target, status=excluded.status`,
			m.ID, poolID, m.GeneratorID, m.Target, m.Status, m.JoinedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ListFleetPoolMembers(poolID string) ([]*FleetPoolMember, error) {
	rows, err := s.db.Query(`SELECT id, pool_id, generator_id, target, COALESCE(status, ''), joined_at
		FROM fleet_pool_members WHERE pool_id = ? ORDER BY joined_at ASC`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]*FleetPoolMember, 0)
	for rows.Next() {
		var m FleetPoolMember
		if err := rows.Scan(&m.ID, &m.PoolID, &m.GeneratorID, &m.Target, &m.Status, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, &m)
	}
	return members, rows.Err()
}

// ---- generator metadata --------------------------------------------------

func (s *SQLiteStore) SaveGeneratorMetadata(metadata *GeneratorMetadata) error {
	if metadata == nil || strings.TrimSpace(metadata.GeneratorID) == "" {
		return fmt.Errorf("generator metadata requires a generator_id")
	}
	now := time.Now()
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = now
	}
	metadata.UpdatedAt = now
	cordoned := 0
	if metadata.Cordoned {
		cordoned = 1
	}
	_, err := s.db.Exec(`INSERT INTO generator_metadata (
			generator_id, target, fleet_pool_id, region, provider_id, provider_status, cordoned, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(generator_id) DO UPDATE SET
			target=excluded.target, fleet_pool_id=excluded.fleet_pool_id, region=excluded.region,
			provider_id=excluded.provider_id, provider_status=excluded.provider_status, cordoned=excluded.cordoned,
			tags_json=excluded.tags_json, updated_at=excluded.updated_at`,
		metadata.GeneratorID, metadata.Target, metadata.FleetPoolID, metadata.Region, metadata.ProviderID,
		metadata.ProviderStatus, cordoned, marshalTags(metadata.Tags), metadata.CreatedAt, metadata.UpdatedAt)
	return err
}

// PatchGeneratorMetadata applies only supplied fields in one SQL statement.
// Concurrent PATCH requests therefore merge instead of restoring one another's
// fields from stale read-modify-write snapshots.
func (s *SQLiteStore) PatchGeneratorMetadata(generatorID string, patch GeneratorMetadataPatch) (*GeneratorMetadata, error) {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" {
		return nil, errors.New("generator id is required")
	}
	now := time.Now()
	flag := func(value bool) int {
		if value {
			return 1
		}
		return 0
	}
	value := func(input *string) any {
		if input == nil {
			return ""
		}
		return *input
	}
	var fleetPoolID any
	if patch.FleetPoolID != nil {
		fleetPoolID = *patch.FleetPoolID
	}
	cordoned := 0
	if patch.Cordoned != nil && *patch.Cordoned {
		cordoned = 1
	}
	tagsJSON := marshalTags(patch.Tags)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if patch.FleetPoolIDSet && patch.FleetPoolID != nil {
		// Take the pool row's write lock in the same transaction as the metadata
		// upsert. A concurrent pool deletion will therefore either detach this row
		// after commit or win first and make this patch fail, never leave a dangling
		// membership reference.
		result, err := tx.Exec(`UPDATE fleet_pools SET updated_at = updated_at WHERE id = ?`, *patch.FleetPoolID)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, sql.ErrNoRows
		}
	}
	if _, err := tx.Exec(`INSERT INTO generator_metadata (
		generator_id, target, fleet_pool_id, region, provider_id, provider_status, cordoned, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(generator_id) DO UPDATE SET
			target=CASE WHEN ? = 1 THEN excluded.target ELSE generator_metadata.target END,
			fleet_pool_id=CASE WHEN ? = 1 THEN excluded.fleet_pool_id ELSE generator_metadata.fleet_pool_id END,
			region=CASE WHEN ? = 1 THEN excluded.region ELSE generator_metadata.region END,
			provider_id=CASE WHEN ? = 1 THEN excluded.provider_id ELSE generator_metadata.provider_id END,
			provider_status=CASE WHEN ? = 1 THEN excluded.provider_status ELSE generator_metadata.provider_status END,
			cordoned=CASE WHEN ? = 1 THEN excluded.cordoned ELSE generator_metadata.cordoned END,
			tags_json=CASE WHEN ? = 1 THEN excluded.tags_json ELSE generator_metadata.tags_json END,
			updated_at=excluded.updated_at`,
		generatorID, value(patch.Target), fleetPoolID, value(patch.Region), value(patch.ProviderID), value(patch.ProviderStatus), cordoned, tagsJSON, now, now,
		flag(patch.Target != nil), flag(patch.FleetPoolIDSet), flag(patch.Region != nil), flag(patch.ProviderID != nil), flag(patch.ProviderStatus != nil), flag(patch.Cordoned != nil), flag(patch.TagsSet)); err != nil {
		return nil, err
	}
	// fleet_pool_members is the authoritative membership record, so a pool
	// change made through generator metadata has to land there too. Without
	// this the two sources drift and the one-generator-one-pool index never
	// sees the binding.
	if patch.FleetPoolIDSet {
		if err := syncGeneratorPoolMembershipTx(tx, generatorID, patch.FleetPoolID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetGeneratorMetadata(generatorID)
}

// syncGeneratorPoolMembershipTx mirrors a generator's metadata pool pointer into
// fleet_pool_members. A nil poolID clears membership.
//
// A unique-index violation means another pool already owns the generator; it is
// translated to ErrGeneratorAlreadyInPool so callers can answer 409 rather than
// leaking a driver error. This is the backstop behind the API's pre-check.
func syncGeneratorPoolMembershipTx(tx *storeTx, generatorID string, poolID *string) error {
	if poolID == nil || strings.TrimSpace(*poolID) == "" {
		_, err := tx.Exec(`DELETE FROM fleet_pool_members WHERE generator_id = ?`, generatorID)
		return err
	}
	pool := strings.TrimSpace(*poolID)
	var target string
	if err := tx.QueryRow(`SELECT COALESCE(target, '') FROM generator_metadata WHERE generator_id = ?`,
		generatorID).Scan(&target); err != nil && err != sql.ErrNoRows {
		return err
	}
	// Upsert on (pool_id, generator_id) the same way replaceFleetPoolMembersTx
	// does, so re-patching a generator's target does not reset joined_at and
	// reorder ListFleetPoolMembers. A generator held by a DIFFERENT pool misses
	// this conflict target and trips the unique index on generator_id instead,
	// which is the cross-pool rejection.
	if _, err := tx.Exec(`INSERT INTO fleet_pool_members (id, pool_id, generator_id, target, status, joined_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(pool_id, generator_id) DO UPDATE SET target=excluded.target`,
		uuid.New().String(), pool, generatorID, target, "provisioned", time.Now()); err != nil {
		if isUniqueConstraintErr(err) {
			return ErrGeneratorAlreadyInPool
		}
		return err
	}
	return nil
}

// isUniqueConstraintErr matches both dialects' unique-violation text, since the
// storage layer talks to SQLite and PostgreSQL through the same driver surface.
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "unique index") ||
		strings.Contains(message, "duplicate key")
}

const generatorMetadataSelect = `SELECT generator_id, COALESCE(target, ''), fleet_pool_id, COALESCE(region, ''),
	COALESCE(provider_id, ''), COALESCE(provider_status, ''), COALESCE(cordoned, 0), COALESCE(tags_json, ''), created_at, updated_at
	FROM generator_metadata`

func scanGeneratorMetadata(row capacityRowScanner) (*GeneratorMetadata, error) {
	var m GeneratorMetadata
	var tagsJSON string
	var cordoned int
	if err := row.Scan(&m.GeneratorID, &m.Target, &m.FleetPoolID, &m.Region, &m.ProviderID,
		&m.ProviderStatus, &cordoned, &tagsJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.Cordoned = cordoned != 0
	m.Tags = unmarshalTags(tagsJSON)
	return &m, nil
}

func (s *SQLiteStore) GetGeneratorMetadata(generatorID string) (*GeneratorMetadata, error) {
	return scanGeneratorMetadata(s.db.QueryRow(generatorMetadataSelect+` WHERE generator_id = ?`, generatorID))
}

func (s *SQLiteStore) ListGeneratorMetadata(limit int, offset int) ([]*GeneratorMetadata, error) {
	query := generatorMetadataSelect + ` ORDER BY generator_id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*GeneratorMetadata, 0)
	for rows.Next() {
		m, err := scanGeneratorMetadata(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) DeleteGeneratorMetadata(generatorID string) error {
	_, err := s.db.Exec(`DELETE FROM generator_metadata WHERE generator_id = ?`, generatorID)
	return err
}

// ---- provisioning requests + events --------------------------------------

func (s *SQLiteStore) SaveProvisioningRequest(req *ProvisioningRequest) error {
	if req == nil {
		return fmt.Errorf("provisioning request is required")
	}
	if req.Status == "" {
		req.Status = "pending"
	}
	if req.Revision < 1 {
		req.Revision = 1
	}
	now := time.Now()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO provisioning_requests (
			id, revision, attempt_token, fleet_pool_id, project_id, status, desired_count, reason, requested_by_subject, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			revision=excluded.revision, attempt_token=excluded.attempt_token,
			fleet_pool_id=excluded.fleet_pool_id, project_id=excluded.project_id, status=excluded.status,
			desired_count=excluded.desired_count, reason=excluded.reason,
			requested_by_subject=excluded.requested_by_subject, updated_at=excluded.updated_at,
			completed_at=excluded.completed_at`,
		req.ID, req.Revision, req.AttemptToken, req.FleetPoolID, req.ProjectID, req.Status, req.DesiredCount, req.Reason,
		req.RequestedBySubject, req.CreatedAt, req.UpdatedAt, req.CompletedAt)
	return err
}

func (s *SQLiteStore) CreateProvisioningRequestIfAbsent(req *ProvisioningRequest) (bool, error) {
	if req == nil || strings.TrimSpace(req.ID) == "" {
		return false, fmt.Errorf("provisioning request id is required")
	}
	if strings.TrimSpace(req.FleetPoolID) == "" {
		return false, fmt.Errorf("provisioning request fleet pool id is required")
	}
	if req.Status == "" {
		req.Status = "pending"
	}
	if req.Revision < 1 {
		req.Revision = 1
	}
	now := time.Now()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// The no-op write is a portable row lock for both SQLite's serialized writer
	// and PostgreSQL. It closes the handler's preflight race with pool suspension
	// or deletion before the durable request becomes visible.
	lockResult, err := tx.Exec(`UPDATE fleet_pools SET updated_at = updated_at WHERE id = ?`, req.FleetPoolID)
	if err != nil {
		return false, err
	}
	locked, err := lockResult.RowsAffected()
	if err != nil {
		return false, err
	}
	if locked != 1 {
		return false, sql.ErrNoRows
	}
	var poolStatus string
	if err := tx.QueryRow(`SELECT COALESCE(status, 'active') FROM fleet_pools WHERE id = ?`, req.FleetPoolID).Scan(&poolStatus); err != nil {
		return false, err
	}
	if status := strings.ToLower(strings.TrimSpace(poolStatus)); status != "" && status != "active" {
		return false, ErrFleetPoolNotActive
	}
	result, err := tx.Exec(`INSERT INTO provisioning_requests (id, revision, attempt_token, fleet_pool_id, project_id, status, desired_count, reason, requested_by_subject, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		req.ID, req.Revision, req.AttemptToken, req.FleetPoolID, req.ProjectID, req.Status, req.DesiredCount, req.Reason,
		req.RequestedBySubject, req.CreatedAt, req.UpdatedAt, req.CompletedAt)
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

func (s *SQLiteStore) TransitionProvisioningRequest(req *ProvisioningRequest, expectedRevision int, expectedAttemptToken string) (bool, error) {
	if req == nil {
		return false, fmt.Errorf("provisioning request is required")
	}
	if req.Revision != expectedRevision+1 {
		return false, fmt.Errorf("provisioning request revision must advance by one")
	}
	req.UpdatedAt = time.Now()
	result, err := s.db.Exec(`UPDATE provisioning_requests SET revision=?, attempt_token=?, fleet_pool_id=?, project_id=?, status=?, desired_count=?, reason=?, requested_by_subject=?, updated_at=?, completed_at=? WHERE id=? AND COALESCE(revision, 1)=? AND COALESCE(attempt_token, '')=?`,
		req.Revision, req.AttemptToken, req.FleetPoolID, req.ProjectID, req.Status, req.DesiredCount, req.Reason, req.RequestedBySubject, req.UpdatedAt, req.CompletedAt,
		req.ID, expectedRevision, expectedAttemptToken)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

const provisioningRequestSelect = `SELECT id, COALESCE(revision, 1), COALESCE(attempt_token, ''), fleet_pool_id, COALESCE(project_id, ''), COALESCE(status, 'pending'),
	desired_count, COALESCE(reason, ''), COALESCE(requested_by_subject, ''), created_at, updated_at, completed_at
	FROM provisioning_requests`

func scanProvisioningRequest(row capacityRowScanner) (*ProvisioningRequest, error) {
	var req ProvisioningRequest
	if err := row.Scan(&req.ID, &req.Revision, &req.AttemptToken, &req.FleetPoolID, &req.ProjectID, &req.Status, &req.DesiredCount,
		&req.Reason, &req.RequestedBySubject, &req.CreatedAt, &req.UpdatedAt, &req.CompletedAt); err != nil {
		return nil, err
	}
	return &req, nil
}

func (s *SQLiteStore) GetProvisioningRequest(id string) (*ProvisioningRequest, error) {
	req, err := scanProvisioningRequest(s.db.QueryRow(provisioningRequestSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	events, err := s.ListProvisioningEvents(id)
	if err != nil {
		return nil, err
	}
	req.Events = events
	return req, nil
}

func (s *SQLiteStore) ListProvisioningRequests(fleetPoolID string, limit int, offset int) ([]*ProvisioningRequest, error) {
	query := provisioningRequestSelect
	args := []any{}
	if fleetPoolID != "" {
		query += ` WHERE fleet_pool_id = ?`
		args = append(args, fleetPoolID)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*ProvisioningRequest, 0)
	for rows.Next() {
		req, err := scanProvisioningRequest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, req)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) SaveProvisioningEvent(event *ProvisioningEvent) error {
	if event == nil {
		return fmt.Errorf("provisioning event is required")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO provisioning_events (id, request_id, event_type, message, timestamp)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET event_type=excluded.event_type, message=excluded.message`,
		event.ID, event.RequestID, event.EventType, event.Message, event.Timestamp)
	return err
}

func (s *SQLiteStore) ListProvisioningEvents(requestID string) ([]*ProvisioningEvent, error) {
	rows, err := s.db.Query(`SELECT id, request_id, event_type, COALESCE(message, ''), timestamp
		FROM provisioning_events WHERE request_id = ? ORDER BY timestamp ASC`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]*ProvisioningEvent, 0)
	for rows.Next() {
		var e ProvisioningEvent
		if err := rows.Scan(&e.ID, &e.RequestID, &e.EventType, &e.Message, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}

// ---- fleet alert acknowledgements ----------------------------------------

func (s *SQLiteStore) SaveFleetAlertAck(ack *FleetAlertAck) error {
	if ack == nil {
		return fmt.Errorf("fleet alert acknowledgement is required")
	}
	if ack.ID == "" {
		return fmt.Errorf("fleet alert acknowledgement id is required")
	}
	now := time.Now()
	if ack.AcknowledgedAt.IsZero() {
		ack.AcknowledgedAt = now
	}
	ack.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO fleet_alert_acks (
			id, alert_type, target, region, severity, summary, note,
			acknowledged_by_subject, acknowledged_by_display_name, acknowledged_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			alert_type=excluded.alert_type, target=excluded.target, region=excluded.region,
			severity=excluded.severity, summary=excluded.summary, note=excluded.note,
			acknowledged_by_subject=excluded.acknowledged_by_subject,
			acknowledged_by_display_name=excluded.acknowledged_by_display_name,
			acknowledged_at=excluded.acknowledged_at, updated_at=excluded.updated_at`,
		ack.ID, ack.AlertType, ack.Target, ack.Region, ack.Severity, ack.Summary, ack.Note,
		ack.AcknowledgedBySubject, ack.AcknowledgedByDisplayName, ack.AcknowledgedAt, ack.UpdatedAt)
	return err
}

func (s *SQLiteStore) DeleteFleetAlertAck(id string) error {
	_, err := s.db.Exec(`DELETE FROM fleet_alert_acks WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) ListFleetAlertAcks() ([]*FleetAlertAck, error) {
	rows, err := s.db.Query(`SELECT id, alert_type, target, COALESCE(region, ''), COALESCE(severity, ''),
			COALESCE(summary, ''), COALESCE(note, ''), COALESCE(acknowledged_by_subject, ''),
			COALESCE(acknowledged_by_display_name, ''), acknowledged_at, updated_at
		FROM fleet_alert_acks ORDER BY acknowledged_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acks := make([]*FleetAlertAck, 0)
	for rows.Next() {
		var ack FleetAlertAck
		if err := rows.Scan(&ack.ID, &ack.AlertType, &ack.Target, &ack.Region, &ack.Severity, &ack.Summary, &ack.Note,
			&ack.AcknowledgedBySubject, &ack.AcknowledgedByDisplayName, &ack.AcknowledgedAt, &ack.UpdatedAt); err != nil {
			return nil, err
		}
		acks = append(acks, &ack)
	}
	return acks, rows.Err()
}

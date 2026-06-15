package storage

// SQLite store — fleet pools, generator metadata, and cloud provisioning.
// Same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
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
			id, name, description, cloud_provider, region, min_size, max_size, desired_size, status, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description, cloud_provider=excluded.cloud_provider,
			region=excluded.region, min_size=excluded.min_size, max_size=excluded.max_size,
			desired_size=excluded.desired_size, status=excluded.status, tags_json=excluded.tags_json,
			updated_at=excluded.updated_at`,
		pool.ID, pool.Name, pool.Description, pool.CloudProvider, pool.Region, pool.MinSize, pool.MaxSize,
		pool.DesiredSize, pool.Status, marshalTags(pool.Tags), pool.CreatedAt, pool.UpdatedAt)
	return err
}

const fleetPoolSelect = `SELECT id, name, COALESCE(description, ''), cloud_provider, COALESCE(region, ''),
	min_size, max_size, desired_size, COALESCE(status, 'active'), COALESCE(tags_json, ''), created_at, updated_at
	FROM fleet_pools`

func scanFleetPool(row capacityRowScanner) (*FleetPool, error) {
	var pool FleetPool
	var tagsJSON string
	if err := row.Scan(&pool.ID, &pool.Name, &pool.Description, &pool.CloudProvider, &pool.Region,
		&pool.MinSize, &pool.MaxSize, &pool.DesiredSize, &pool.Status, &tagsJSON, &pool.CreatedAt, &pool.UpdatedAt); err != nil {
		return nil, err
	}
	pool.Tags = unmarshalTags(tagsJSON)
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
	// detach members and generator metadata before removing the pool.
	if _, err := s.db.Exec(`UPDATE generator_metadata SET fleet_pool_id = NULL, updated_at = ? WHERE fleet_pool_id = ?`, time.Now(), id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM fleet_pool_members WHERE pool_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM fleet_pools WHERE id = ?`, id)
	return err
}

// SaveFleetPoolMembers replaces a pool's entire membership: it clears existing
// rows then inserts the supplied set. The ON CONFLICT clause is not redundant —
// it tolerates duplicate generator_ids within the input slice. The clear+insert
// is not wrapped in a transaction; membership edits are infrequent admin
// operations where a brief intermediate state is acceptable.
func (s *SQLiteStore) SaveFleetPoolMembers(poolID string, members []*FleetPoolMember) error {
	if _, err := s.db.Exec(`DELETE FROM fleet_pool_members WHERE pool_id = ?`, poolID); err != nil {
		return err
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
		if _, err := s.db.Exec(`INSERT INTO fleet_pool_members (id, pool_id, generator_id, target, status, joined_at)
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
	now := time.Now()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO provisioning_requests (
			id, fleet_pool_id, project_id, status, desired_count, reason, requested_by_subject, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			fleet_pool_id=excluded.fleet_pool_id, project_id=excluded.project_id, status=excluded.status,
			desired_count=excluded.desired_count, reason=excluded.reason,
			requested_by_subject=excluded.requested_by_subject, updated_at=excluded.updated_at,
			completed_at=excluded.completed_at`,
		req.ID, req.FleetPoolID, req.ProjectID, req.Status, req.DesiredCount, req.Reason,
		req.RequestedBySubject, req.CreatedAt, req.UpdatedAt, req.CompletedAt)
	return err
}

const provisioningRequestSelect = `SELECT id, fleet_pool_id, COALESCE(project_id, ''), COALESCE(status, 'pending'),
	desired_count, COALESCE(reason, ''), COALESCE(requested_by_subject, ''), created_at, updated_at, completed_at
	FROM provisioning_requests`

func scanProvisioningRequest(row capacityRowScanner) (*ProvisioningRequest, error) {
	var req ProvisioningRequest
	if err := row.Scan(&req.ID, &req.FleetPoolID, &req.ProjectID, &req.Status, &req.DesiredCount,
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

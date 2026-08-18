package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Fleet pool membership had two independent sources of truth: fleet_pool_members
// rows, and a single-valued generator_metadata.fleet_pool_id pointer that the
// generator PATCH endpoint wrote. Readers unioned them, so the two could
// disagree and a generator could count toward two pools at once.
//
// A pool carries at most one domain, so that ambiguity makes domain scoping
// unenforceable: the same generator could be claimed by two domains. These
// helpers make fleet_pool_members authoritative and keep the legacy pointer
// consistent with it.

// consolidateFleetPoolMembership reconciles the two membership sources so the
// unique index on fleet_pool_members(generator_id) can be created. It runs on
// every boot and is idempotent; on a consistent database it is a no-op.
//
// Order matters. Promote first so a metadata-only binding is not lost when the
// duplicate pass picks a winner, then dedupe, then realign the pointer.
func (s *SQLiteStore) consolidateFleetPoolMembership() error {
	if err := s.promoteGeneratorMetadataMemberships(); err != nil {
		return err
	}
	if err := s.dedupeFleetPoolMembers(); err != nil {
		return err
	}
	return s.alignGeneratorMetadataPoolPointers()
}

// promoteGeneratorMetadataMemberships materializes a fleet_pool_members row for
// every generator bound to a pool only through generator_metadata, so making
// the members table authoritative does not silently drop those generators from
// their pool.
func (s *SQLiteStore) promoteGeneratorMetadataMemberships() error {
	rows, err := s.db.Query(`SELECT g.generator_id, g.fleet_pool_id, COALESCE(g.target, '')
		FROM generator_metadata g
		JOIN fleet_pools p ON p.id = g.fleet_pool_id
		WHERE g.fleet_pool_id IS NOT NULL AND TRIM(g.fleet_pool_id) != ''
			AND NOT EXISTS (SELECT 1 FROM fleet_pool_members m WHERE m.generator_id = g.generator_id)`)
	if err != nil {
		return fmt.Errorf("list metadata-only fleet pool memberships: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type promotion struct{ generatorID, poolID, target string }
	pending := make([]promotion, 0)
	for rows.Next() {
		var item promotion
		if err := rows.Scan(&item.generatorID, &item.poolID, &item.target); err != nil {
			rows.Close()
			return fmt.Errorf("scan metadata-only fleet pool membership: %w", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list metadata-only fleet pool memberships: %w", err)
	}
	rows.Close()

	now := time.Now().UTC()
	for _, item := range pending {
		if _, err := s.db.Exec(`INSERT INTO fleet_pool_members (id, pool_id, generator_id, target, status, joined_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(pool_id, generator_id) DO NOTHING`,
			uuid.New().String(), item.poolID, item.generatorID, item.target, "provisioned", now); err != nil {
			return fmt.Errorf("promote fleet pool membership for generator %s: %w", item.generatorID, err)
		}
	}
	return nil
}

// dedupeFleetPoolMembers keeps exactly one membership row per generator.
//
// Rows with an empty generator_id are left alone: those members are identified
// by target, they are not one generator, and the unique index skips them too.
//
// The winner is the pool the generator's own metadata pointer names — that is
// what the fleet UI has been showing as "the" pool — falling back to the
// earliest joined_at so the choice is deterministic rather than arbitrary.
func (s *SQLiteStore) dedupeFleetPoolMembers() error {
	rows, err := s.db.Query(`SELECT m.generator_id, m.id, m.pool_id, m.joined_at,
			COALESCE(g.fleet_pool_id, '')
		FROM fleet_pool_members m
		LEFT JOIN generator_metadata g ON g.generator_id = m.generator_id
		WHERE TRIM(m.generator_id) != '' AND m.generator_id IN (
			SELECT generator_id FROM fleet_pool_members
			WHERE TRIM(generator_id) != ''
			GROUP BY generator_id HAVING COUNT(*) > 1
		)
		ORDER BY m.generator_id ASC, m.joined_at ASC`)
	if err != nil {
		return fmt.Errorf("list duplicate fleet pool memberships: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type candidate struct {
		memberID string
		poolID   string
		joinedAt time.Time
	}
	byGenerator := make(map[string][]candidate)
	preferredPool := make(map[string]string)
	for rows.Next() {
		var generatorID, metadataPoolID string
		var item candidate
		if err := rows.Scan(&generatorID, &item.memberID, &item.poolID, &item.joinedAt, &metadataPoolID); err != nil {
			rows.Close()
			return fmt.Errorf("scan duplicate fleet pool membership: %w", err)
		}
		byGenerator[generatorID] = append(byGenerator[generatorID], item)
		if trimmed := strings.TrimSpace(metadataPoolID); trimmed != "" {
			preferredPool[generatorID] = trimmed
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list duplicate fleet pool memberships: %w", err)
	}
	rows.Close()

	for generatorID, candidates := range byGenerator {
		keep := candidates[0] // earliest joined_at; rows arrived sorted
		if preferred, ok := preferredPool[generatorID]; ok {
			for _, item := range candidates {
				if item.poolID == preferred {
					keep = item
					break
				}
			}
		}
		for _, item := range candidates {
			if item.memberID == keep.memberID {
				continue
			}
			if _, err := s.db.Exec(`DELETE FROM fleet_pool_members WHERE id = ?`, item.memberID); err != nil {
				return fmt.Errorf("drop duplicate fleet pool membership %s: %w", item.memberID, err)
			}
		}
	}
	return nil
}

// alignGeneratorMetadataPoolPointers points the legacy single-valued column at
// whichever pool the members table now says owns the generator, so the two
// sources cannot disagree after consolidation.
func (s *SQLiteStore) alignGeneratorMetadataPoolPointers() error {
	if _, err := s.db.Exec(`UPDATE generator_metadata
		SET fleet_pool_id = (SELECT m.pool_id FROM fleet_pool_members m WHERE m.generator_id = generator_metadata.generator_id)
		WHERE EXISTS (SELECT 1 FROM fleet_pool_members m WHERE m.generator_id = generator_metadata.generator_id)
			AND COALESCE(fleet_pool_id, '') != (SELECT m.pool_id FROM fleet_pool_members m WHERE m.generator_id = generator_metadata.generator_id)`); err != nil {
		return fmt.Errorf("align generator metadata pool pointers: %w", err)
	}
	return nil
}

// FleetPoolIDForGenerator returns the pool that owns a generator, or "" when it
// is unassigned. Callers use it to reject a cross-pool claim with a message
// naming the pool that already holds the generator.
func (s *SQLiteStore) FleetPoolIDForGenerator(generatorID string) (string, error) {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" {
		return "", nil
	}
	var poolID string
	err := s.db.QueryRow(`SELECT pool_id FROM fleet_pool_members WHERE generator_id = ?`, generatorID).Scan(&poolID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(poolID), nil
}

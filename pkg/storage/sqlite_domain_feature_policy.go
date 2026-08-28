package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DomainFeaturePolicyStore is deliberately separate from Store so lightweight
// test adapters and third-party stores remain source compatible. Production
// SQLite and PostgreSQL stores implement it through SQLiteStore.
type DomainFeaturePolicyStore interface {
	GetDomainFeaturePolicy(domainID string) (*DomainFeaturePolicy, error)
	UpdateDomainFeaturePolicyIfRevision(domainID string, disabledFeatureIDs []string, expectedRevision string) (*DomainFeaturePolicy, bool, error)
}

func normalizeFeatureIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	sort.Strings(normalized)
	return normalized
}

func (s *SQLiteStore) GetDomainFeaturePolicy(domainID string) (*DomainFeaturePolicy, error) {
	domainID = normalizeDomainID(domainID)
	var raw string
	policy := &DomainFeaturePolicy{DomainID: domainID, Revision: "0", DisabledFeatureIDs: []string{}}
	err := s.db.QueryRow(`SELECT disabled_feature_ids_json, revision, created_at, updated_at
		FROM domain_feature_policies WHERE domain_id = ?`, domainID).Scan(&raw, &policy.Revision, &policy.CreatedAt, &policy.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if _, domainErr := s.GetDomain(domainID); domainErr != nil {
			return nil, domainErr
		}
		return policy, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &policy.DisabledFeatureIDs); err != nil {
		return nil, err
	}
	policy.DisabledFeatureIDs = normalizeFeatureIDs(policy.DisabledFeatureIDs)
	return policy, nil
}

func (s *SQLiteStore) UpdateDomainFeaturePolicyIfRevision(domainID string, disabledFeatureIDs []string, expectedRevision string) (*DomainFeaturePolicy, bool, error) {
	domainID = normalizeDomainID(domainID)
	expectedRevision = strings.TrimSpace(expectedRevision)
	if expectedRevision == "" {
		return nil, false, errors.New("expected feature-policy revision is required")
	}
	if _, err := s.GetDomain(domainID); err != nil {
		return nil, false, err
	}
	disabledFeatureIDs = normalizeFeatureIDs(disabledFeatureIDs)
	raw, err := json.Marshal(disabledFeatureIDs)
	if err != nil {
		return nil, false, err
	}
	now := time.Now()
	nextRevision := uuid.NewString()
	if expectedRevision == "0" {
		policy := &DomainFeaturePolicy{DomainID: domainID, DisabledFeatureIDs: disabledFeatureIDs}
		err := s.db.QueryRow(`INSERT INTO domain_feature_policies
			(domain_id, disabled_feature_ids_json, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(domain_id) DO NOTHING
			RETURNING revision, created_at, updated_at`, domainID, string(raw), nextRevision, now, now).
			Scan(&policy.Revision, &policy.CreatedAt, &policy.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return policy, true, nil
	}
	policy := &DomainFeaturePolicy{DomainID: domainID, DisabledFeatureIDs: disabledFeatureIDs}
	err = s.db.QueryRow(`UPDATE domain_feature_policies
		SET disabled_feature_ids_json = ?, revision = ?, updated_at = ?
		WHERE domain_id = ? AND revision = ?
		RETURNING revision, created_at, updated_at`, string(raw), nextRevision, now, domainID, expectedRevision).
		Scan(&policy.Revision, &policy.CreatedAt, &policy.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return policy, true, nil
}

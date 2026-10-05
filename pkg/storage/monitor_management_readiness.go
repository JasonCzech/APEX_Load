package storage

import (
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

func validateManagementEncryptionKey(key string) error {
	b, e := base64.StdEncoding.DecodeString(key)
	if e != nil {
		return errors.New("invalid management collector encryption key")
	}
	_, e = ecdh.X25519().NewPublicKey(b)
	if e != nil {
		return errors.New("invalid management collector encryption key")
	}
	return nil
}
func (s *SQLiteStore) SaveCollectorManagementReadiness(project, collector string, r CollectorManagementReadiness) error {
	if !validManagementText(r.InstanceID, 128) || r.SeenAt.IsZero() || len(r.Packs) > 16 || len(r.Destinations) > 16 || len(r.Capabilities) > 16 || len(r.Scopes) > 16 || !slices.Contains(r.Capabilities, ManagementEvidenceCapability) {
		return errors.New("invalid management readiness")
	}
	seen := map[string]bool{}
	for _, c := range r.Capabilities {
		if !validManagementText(c, 128) || seen[c] {
			return errors.New("invalid readiness capability")
		}
		seen[c] = true
	}
	seen = map[string]bool{}
	for _, scope := range r.Scopes {
		key := scope.ScopeHash + "/" + scope.IdentityHash
		if !validManagementDigest(scope.ScopeHash) || !validManagementDigest(scope.IdentityHash) || seen[key] {
			return errors.New("invalid readiness scope")
		}
		seen[key] = true
	}
	legacy := []string{}
	hasManagement := false
	for _, pack := range r.Packs {
		if pack == "kafka-offsets" || pack == "servicebus-runtime" || pack == "solace-semp" {
			hasManagement = true
		} else {
			legacy = append(legacy, pack)
		}
	}
	if err := validateCollectorRunReadiness(project, collector, legacy, nil, r.SeenAt); err != nil {
		return err
	}
	for _, dest := range r.Destinations {
		if validateCollectorRunReadiness(project, collector, legacy, []string{dest}, r.SeenAt) == nil {
			continue
		}
		if !hasManagement {
			return errors.New("unapproved readiness destination")
		}
		raw, key, hasKey := strings.Cut(dest, "#")
		if hasKey && validateManagementEncryptionKey(key) != nil {
			return errors.New("invalid readiness encryption key")
		}
		if err := validateManagementDestination(raw); err != nil {
			return err
		}
	}
	encoded, e := json.Marshal(r)
	if e != nil || len(encoded) > 8<<10 {
		return errors.New("management readiness exceeds header limit")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return e
	}
	var owner string
	if e = tx.QueryRow(`SELECT project_id FROM monitoring_collectors WHERE id=?`, collector).Scan(&owner); e != nil {
		return e
	}
	if owner != project {
		return errors.New("collector belongs to another project")
	}
	p, _ := json.Marshal(nonNilStrings(r.Packs))
	d, _ := json.Marshal(nonNilStrings(r.Destinations))
	c, _ := json.Marshal(nonNilStrings(r.Capabilities))
	scopes := r.Scopes
	if scopes == nil {
		scopes = []ManagementReadinessScope{}
	}
	h, _ := json.Marshal(scopes)
	_, e = tx.Exec(`INSERT INTO run_monitor_collector_readiness(collector_id,project_id,seen_at,packs,destinations,instance_id,capabilities_json,management_scopes_json) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(collector_id) DO UPDATE SET seen_at=excluded.seen_at,packs=excluded.packs,destinations=excluded.destinations,instance_id=excluded.instance_id,capabilities_json=excluded.capabilities_json,management_scopes_json=excluded.management_scopes_json`, collector, project, r.SeenAt.UTC(), string(p), string(d), r.InstanceID, string(c), string(h))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func nonNilStrings(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}
func managementReadinessTx(tx *storeTx, m MonitorSource, instance string, now time.Time) error {
	var exists int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM monitor_sources WHERE project_id=? AND id=?`, m.ProjectID, m.ID).Scan(&exists); e != nil {
		return e
	}
	if exists != 1 {
		return ErrManagementPollUnavailable
	}

	sh, ih, e := managementSourceHashes(m)
	if e != nil {
		return e
	}
	var health, packsJSON string
	var seen sql.NullTime
	if e = tx.QueryRow(`SELECT health,last_seen_at,COALESCE(allowed_metric_packs_json,'[]') FROM monitoring_collectors WHERE project_id=? AND id=?`, m.ProjectID, m.CollectorID).Scan(&health, &seen, &packsJSON); e != nil {
		return ErrManagementPollUnavailable
	}
	var allowed []string
	if json.Unmarshal([]byte(packsJSON), &allowed) != nil || health != "healthy" || !seen.Valid || seen.Time.Before(now.Add(-90*time.Second)) || seen.Time.After(now.Add(30*time.Second)) || !slices.Contains(allowed, m.Pack) {
		return ErrManagementPollUnavailable
	}
	var r CollectorManagementReadiness
	var current sql.NullString
	var caps, scopes sql.NullString
	var packs, dest string
	if e = tx.QueryRow(`SELECT seen_at,packs,destinations,instance_id,capabilities_json,management_scopes_json FROM run_monitor_collector_readiness WHERE project_id=? AND collector_id=?`, m.ProjectID, m.CollectorID).Scan(&r.SeenAt, &packs, &dest, &current, &caps, &scopes); e != nil {
		return ErrManagementPollUnavailable
	}
	if !current.Valid || !validManagementText(current.String, 128) || instance != "" && current.String != instance || !caps.Valid || !scopes.Valid || json.Unmarshal([]byte(caps.String), &r.Capabilities) != nil || json.Unmarshal([]byte(scopes.String), &r.Scopes) != nil || json.Unmarshal([]byte(packs), &r.Packs) != nil || json.Unmarshal([]byte(dest), &r.Destinations) != nil {
		return ErrManagementPollUnavailable
	}
	if !slices.Contains(r.Capabilities, ManagementEvidenceCapability) || !slices.Contains(r.Packs, m.Pack) || !slices.Contains(r.Destinations, m.ReadinessDestination()) || r.SeenAt.Before(now.Add(-90*time.Second)) || r.SeenAt.After(now.Add(30*time.Second)) {
		return ErrManagementPollUnavailable
	}
	for _, scope := range r.Scopes {
		if scope.ScopeHash == sh && scope.IdentityHash == ih {
			return nil
		}
	}
	return ErrManagementPollUnavailable
}

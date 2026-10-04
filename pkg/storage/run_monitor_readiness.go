package storage

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// ValidateRunMonitorReadiness checks the exact Start readiness contract inside
// a rolled-back transaction. No collection job is visible and no source is polled.
func (s *SQLiteStore) ValidateRunMonitorReadiness(project, run, scenario string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = startRunMonitorsTx(tx, project, run, scenario, now, now.Add(time.Hour))
	return err
}

// This advertisement is authenticated by the collector token and accepted only
// after the API checks the collector's pinned controller signing key.
func (s *SQLiteStore) SaveCollectorRunReadiness(project, collector string, packs, destinations []string, seen time.Time) error {
	if len(packs) > 16 || len(destinations) > 16 || seen.IsZero() {
		return errors.New("invalid collector readiness")
	}
	for _, pack := range packs {
		if pack != "go-runtime" && pack != "linux-hosts" && pack != "prometheus" && pack != "ssh-linux" && pack != "otel-metrics" && pack != "jmx" && pack != "jdbc" && pack != "cloudwatch" && pack != "azure-monitor" && pack != "appdynamics" && pack != "appdynamics.v1" && pack != "dynatrace" && pack != "dynatrace.v1" {
			return errors.New("unsupported collector pack")
		}
	}
	if containsReadinessPack(packs, "appdynamics.v1") && !containsReadinessPack(packs, "appdynamics") {
		return errors.New("AppDynamics contract requires pack approval")
	}
	if containsReadinessPack(packs, "dynatrace.v1") && !containsReadinessPack(packs, "dynatrace") {
		return errors.New("Dynatrace contract requires its pack")
	}
	for _, destination := range destinations {
		valid := false
		raw, key, hasKey := strings.Cut(destination, "#")
		if hasKey {
			decoded, err := base64.StdEncoding.DecodeString(key)
			if err != nil {
				return errors.New("invalid readiness encryption key")
			}
			if _, err = ecdh.X25519().NewPublicKey(decoded); err != nil {
				return err
			}
		}
		for _, pack := range packs {
			if pack == "appdynamics" || pack == "dynatrace" {
				u, e := url.Parse(raw)
				valid = valid || (hasKey && e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "")
			}
			if pack == "prometheus" {
				source := MonitorSource{ID: "validation", ProjectID: project, Name: "validation", CollectorID: collector, Pack: pack, Destination: destination, IntervalSeconds: 1}
				valid = valid || source.Validate() == nil
			} else if pack == "cloudwatch" || pack == "azure-monitor" {
				valid = valid || !hasKey && validateRemoteMonitorDestination(pack, raw) == nil
			} else if pack == "ssh-linux" || pack == "jmx" || pack == "jdbc" || pack == "otel-metrics" {
				valid = valid || (pack == "otel-metrics" && !hasKey || pack != "otel-metrics" && hasKey) && validateRemoteMonitorDestination(pack, raw) == nil
			}
		}
		if !valid {
			return errors.New("unapproved readiness destination")
		}
	}

	p, _ := json.Marshal(packs)
	d, _ := json.Marshal(destinations)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	var owner string
	if err = tx.QueryRow(`SELECT project_id FROM monitoring_collectors WHERE id=?`, collector).Scan(&owner); err != nil {
		return err
	}
	if owner != project {
		return errors.New("collector belongs to another project")
	}
	_, err = tx.Exec(`INSERT INTO run_monitor_collector_readiness(collector_id,project_id,seen_at,packs,destinations) VALUES(?,?,?,?,?) ON CONFLICT(collector_id) DO UPDATE SET seen_at=excluded.seen_at,packs=excluded.packs,destinations=excluded.destinations`, collector, project, seen, string(p), string(d))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func containsReadinessPack(packs []string, pack string) bool {
	for _, candidate := range packs {
		if candidate == pack {
			return true
		}
	}
	return false
}

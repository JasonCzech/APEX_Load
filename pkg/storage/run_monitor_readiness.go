package storage

import (
	"encoding/json"
	"errors"
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
		if pack != "go-runtime" && pack != "linux-hosts" && pack != "prometheus" {
			return errors.New("unsupported collector pack")
		}
	}
	for _, destination := range destinations {
		source := MonitorSource{ID: "validation", ProjectID: project, Name: "validation", CollectorID: collector, Pack: "prometheus", Destination: destination, IntervalSeconds: 1}
		if err := source.Validate(); err != nil {
			return err
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

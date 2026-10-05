package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"strings"
	"time"
)

// ReserveDynatraceScan admits a new upstream execution attempt. Reservations
// survive executor crashes and token expiry: a replay with a new attempt ID is
// a new potentially billed execution and requires a separate reservation.
func (s *SQLiteStore) ReserveDynatraceScan(project, jobID, attempt string, bytes int64, now time.Time) (bool, error) {
	return s.ReserveDynatraceScanFenced(project, jobID, attempt, bytes, now, "", "", 0)
}
func (s *SQLiteStore) ReserveDynatraceScanFenced(project, jobID, attempt string, bytes int64, now time.Time, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if project == "" || attempt == "" || len(attempt) > 256 || strings.ContainsAny(attempt, "\r\n\x00") || bytes < 1 || bytes > 100_000_000_000 {
		return false, errors.New("invalid Dynatrace scan reservation")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, generation)
		if err == nil && tx == nil {
			return false, errors.New("monitor execution owner fence rejected")
		}
	} else {
		tx, err = s.db.Begin()
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return false, err
	}
	var raw, state string
	var expiry time.Time
	if err = tx.QueryRow(`SELECT payload,state,expires_at FROM run_monitor_jobs WHERE id=? AND project_id=?`, jobID, project).Scan(&raw, &state, &expiry); err != nil {
		return false, err
	}
	var job RunMonitorJob
	if err = json.Unmarshal([]byte(raw), &job); err != nil {
		return false, err
	}
	if job.Source.Pack != "dynatrace" || job.DynatraceSnapshot == nil || state != "collecting" && state != "reconciling" || !expiry.After(now) {
		return false, errors.New("Dynatrace job is unavailable")
	}
	var prior int64
	err = tx.QueryRow(`SELECT reserved_bytes FROM dynatrace_scan_reservations WHERE job_id=? AND attempt_id=?`, jobID, attempt).Scan(&prior)
	if err == nil {
		if prior != bytes {
			return false, errors.New("conflicting scan reservation retry")
		}
		return true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	cfg := job.DynatraceSnapshot.Config.Defaults()
	limit := cfg.MaxRunScanBytes
	if state == "collecting" {
		limit -= cfg.DrainReserveBytes
	}
	if bytes > cfg.MaxScanBytes || job.ScanConsumedBytes < 0 || job.ScanReservedBytes < 0 || bytes > limit-job.ScanConsumedBytes-job.ScanReservedBytes {
		return false, nil
	}
	if _, err = tx.Exec(`INSERT INTO dynatrace_scan_reservations(job_id,attempt_id,reserved_bytes,created_at)VALUES(?,?,?,?)`, jobID, attempt, bytes, now.UTC()); err != nil {
		return false, err
	}
	job.ScanReservedBytes += bytes
	body, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE run_monitor_jobs SET payload=? WHERE id=? AND project_id=?`, string(body), jobID, project); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// commitDynatraceScanProgressTx confirms this pass's new reservations and
// settles each attempt that reported its scanned bytes. Attempts are settled
// independently: one still running, or finished without cost metadata, keeps
// only its own reservation and never strands attempts that did finish.
func commitDynatraceScanProgressTx(tx *storeTx, job *RunMonitorJob, p *analysis.ProviderQueryProgress) error {
	if p == nil {
		return nil
	}
	if p.ScannedBytes < 0 || p.ScanReservationBytes < 0 || len(p.ScanReservationIDs) > 32 || len(p.ScanSettlements) > 32 {
		return errors.New("invalid Dynatrace scan accounting")
	}
	seen := map[string]bool{}
	var reserved int64
	for _, id := range p.ScanReservationIDs {
		if id == "" || len(id) > 256 || seen[id] {
			return errors.New("invalid scan execution identity")
		}
		seen[id] = true
		var amount int64
		var state string
		if err := tx.QueryRow(`SELECT reserved_bytes,state FROM dynatrace_scan_reservations WHERE job_id=? AND attempt_id=?`, job.ID, id).Scan(&amount, &state); err != nil {
			return err
		}
		if state != "pending" {
			return errors.New("scan execution already accounted")
		}
		reserved += amount
	}
	if p.ScanReservationBytes != 0 && p.ScanReservationBytes != reserved || reserved > job.ScanReservedBytes {
		return errors.New("scan reservation differs from admitted amount")
	}
	settled := map[string]bool{}
	var total int64
	for _, settlement := range p.ScanSettlements {
		if settlement.AttemptID == "" || len(settlement.AttemptID) > 256 || settled[settlement.AttemptID] || settlement.ScannedBytes < 0 {
			return errors.New("invalid scan settlement")
		}
		settled[settlement.AttemptID] = true
		total += settlement.ScannedBytes
		var amount int64
		var state string
		if err := tx.QueryRow(`SELECT reserved_bytes,state FROM dynatrace_scan_reservations WHERE job_id=? AND attempt_id=?`, job.ID, settlement.AttemptID).Scan(&amount, &state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("scan settlement names an unadmitted execution")
			}
			return err
		}
		if state != "pending" {
			return errors.New("scan execution already accounted")
		}
		if _, err := tx.Exec(`UPDATE dynatrace_scan_reservations SET state='settled',consumed_bytes=? WHERE job_id=? AND attempt_id=? AND state='pending'`, settlement.ScannedBytes, job.ID, settlement.AttemptID); err != nil {
			return err
		}
		// The actual cost is charged even above the reservation, so a later
		// admission sees the true remaining run allowance.
		job.ScanReservedBytes -= amount
		job.ScanConsumedBytes += settlement.ScannedBytes
	}
	if len(p.ScanSettlements) > 0 && total != p.ScannedBytes || job.ScanReservedBytes < 0 {
		return errors.New("scan cost differs from settled executions")
	}
	return nil
}

// The common quota table requires a 64-hex opaque resource identity. Hash the
// provider namespace as well so a matching AppDynamics account key cannot
// consume a Dynatrace executor lease.
func (s *SQLiteStore) AllocateDynatraceQuota(resourceKey, executorID string, now time.Time) (bool, error) {
	if !appDynamicsQuotaKey.MatchString(resourceKey) {
		return false, errors.New("invalid Dynatrace quota identity")
	}
	key := sha256.Sum256([]byte("dynatrace:" + resourceKey))
	return s.AllocateAppDynamicsQuota(hex.EncodeToString(key[:]), executorID, now)
}

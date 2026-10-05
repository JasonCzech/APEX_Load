package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

const maxPrometheusDiagnosticPollSlots = 10_000

// Current-state APIs provide sampled observations, never recoverable history.
// Slot identity uses the provider's successful query time, not receipt arrival.
func recordPrometheusDiagnosticPollsTx(tx *storeTx, job *RunMonitorJob, receipt MonitorBatchReceipt, coverage map[string]monitorprovider.Coverage) error {
	if job.Source.Prometheus == nil || job.State != "collecting" && job.State != "reconciling" {
		return nil
	}
	for _, kind := range []string{"targets", "alerts"} {
		cov, ok := coverage[kind]
		if !ok || cov.ObservedAt == nil || cov.Truncated || len(cov.Warnings) > 0 || cov.Status != "complete" && cov.Status != "no_data" {
			continue
		}
		observed := cov.ObservedAt.UTC()
		if observed.Before(job.StartedAt) || observed.After(receipt.ReceivedAt.Add(30*time.Second)) || job.LoadEndedAt != nil && observed.After(*job.LoadEndedAt) {
			return errors.New("invalid current-state diagnostic observation time")
		}
		if job.Coverage == nil {
			job.Coverage = map[string]monitorprovider.Coverage{}
		}
		previous := job.Coverage[kind+"_last_success"]
		if previous.ObservedAt == nil || observed.After(*previous.ObservedAt) {
			job.Coverage[kind+"_last_success"] = cov
		}
		slot := int64(observed.Sub(job.StartedAt) / (time.Duration(max(60, job.Source.IntervalSeconds)) * time.Second))
		key := fmt.Sprintf("poll:%s:%d", kind, slot)
		var exists, count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND record_key=?`, job.ID, key).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND record_kind='poll' AND diagnostic_kind=?`, job.ID, kind).Scan(&count); err != nil {
				return err
			}
			if count >= maxPrometheusDiagnosticPollSlots {
				if job.Coverage == nil {
					job.Coverage = map[string]monitorprovider.Coverage{}
				}
				job.Coverage[kind+"_polling"] = monitorprovider.Coverage{Status: "partial", Truncated: true, Message: "Current-state diagnostic poll-slot retention limit reached"}
				continue
			}
			digest := sha256.Sum256([]byte(key))
			if _, err := tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,diagnostic_kind,content_digest) VALUES(?,?,?,?,'poll',?,?)`, job.ID, key, receipt.Sequence, slot, kind, hex.EncodeToString(digest[:])); err != nil {
				return err
			}
		}
		coverageEnd := observed
		if latest := job.Coverage[kind+"_last_success"].ObservedAt; latest != nil && latest.After(coverageEnd) {
			coverageEnd = *latest
		}
		if job.LoadEndedAt != nil {
			coverageEnd = *job.LoadEndedAt
		}
		if err := setPrometheusDiagnosticPollingCoverageTx(tx, job, kind, coverageEnd); err != nil {
			return err
		}
	}
	return nil
}

func setPrometheusDiagnosticPollingCoverageTx(tx *storeTx, job *RunMonitorJob, kind string, end time.Time) error {
	expected := max(1, int(math.Ceil(end.Sub(job.StartedAt).Seconds()/float64(max(60, job.Source.IntervalSeconds)))))
	var captured int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND record_kind='poll' AND diagnostic_kind=? AND record_index>=0 AND record_index<?`, job.ID, kind, expected).Scan(&captured); err != nil {
		return err
	}
	if job.Coverage == nil {
		job.Coverage = map[string]monitorprovider.Coverage{}
	}
	prior := job.Coverage[kind+"_polling"]
	cov := monitorprovider.Coverage{Status: "partial", From: job.StartedAt, To: end.UTC(), Points: captured, ExpectedPoints: expected, MissingPoints: max(0, expected-captured), Truncated: prior.Truncated, Freshness: "sampled_current_state", Message: "Coverage describes successful current-state polling slots; missed historical state cannot be recovered"}
	if !cov.Truncated && 100*float64(captured)/float64(expected) >= 95 {
		cov.Status = "complete"
	}
	job.Coverage[kind+"_polling"] = cov
	return nil
}

func finalizePrometheusDiagnosticCoverageTx(tx *storeTx, job *RunMonitorJob, end time.Time) error {
	if job.Source.Prometheus == nil {
		return nil
	}
	for _, kind := range []struct {
		name    string
		enabled bool
	}{{"targets", job.Source.Prometheus.Targets}, {"alerts", job.Source.Prometheus.Alerts}} {
		if kind.enabled {
			if err := setPrometheusDiagnosticPollingCoverageTx(tx, job, kind.name, end); err != nil {
				return err
			}
		}
	}
	return nil
}

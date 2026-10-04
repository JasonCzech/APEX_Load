package storage

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// Legacy exporter evidence is capture-only. A successful poll covers one actual
// source-observation slot; duplicate exporter timestamps cannot fill later gaps.
func recordPrometheusScrapeSlotTx(tx *storeTx, job RunMonitorJob, receipt MonitorBatchReceipt, points []analysis.MetricPoint) error {
	var observed time.Time
	for _, point := range points {
		if point.ProjectID != job.ProjectID || point.RunID != job.RunID || point.Source != job.Source.ID || point.Provider != nil || point.Validate() != nil {
			return errors.New("invalid legacy Prometheus observation")
		}
		if point.Quality != analysis.QualityValid || point.Value == nil || point.TimestampUTC.Before(job.StartedAt) || point.TimestampUTC.After(receipt.ReceivedAt.Add(30*time.Second)) || job.LoadEndedAt != nil && point.TimestampUTC.After(*job.LoadEndedAt) {
			continue
		}
		if point.TimestampUTC.After(observed) {
			observed = point.TimestampUTC
		}
	}
	if observed.IsZero() {
		return nil
	}
	slot := int64(observed.Sub(job.StartedAt) / (time.Duration(max(1, job.Source.IntervalSeconds)) * time.Second))
	_, err := tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest) VALUES(?,?,?,?,'scrape',?) ON CONFLICT(job_id,record_key) DO NOTHING`, job.ID, "scrape:"+strconv.FormatInt(slot, 10), receipt.Sequence, slot, receipt.Digest)
	return err
}
func finalizePrometheusScrapeJobTx(tx *storeTx, job *RunMonitorJob, end time.Time, terminal bool) error {
	duration := end.Sub(job.StartedAt).Seconds()
	expected := int(math.Ceil(duration / float64(max(1, job.Source.IntervalSeconds))))
	expected = max(1, expected)
	var captured int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND record_kind='scrape' AND record_index>=0 AND record_index<?`, job.ID, expected).Scan(&captured); err != nil {
		return err
	}
	coverage := monitorprovider.Coverage{Status: "partial", From: job.StartedAt, To: end.UTC(), Points: captured, ExpectedPoints: expected, MissingPoints: max(0, expected-captured), Freshness: "source_observations", Message: "exporter capture covers actual observed polling slots; absent history cannot be recovered"}
	if 100*float64(captured)/float64(expected) >= 95 {
		coverage.Status = "complete"
		coverage.Message = "exporter polling coverage met"
	}
	if job.Coverage == nil {
		job.Coverage = map[string]monitorprovider.Coverage{}
	}
	job.Coverage["scrape"] = coverage
	if terminal {
		job.State = "stopped"
		job.CoverageReason = ""
		if coverage.Status != "complete" {
			job.State = "incomplete"
			job.CoverageReason = "exporter_capture_gaps"
		}
	}
	return nil
}
func (s *SQLiteStore) AcceptLegacyPrometheusBatch(project, collector string, receipt MonitorBatchReceipt, points []analysis.MetricPoint) error {
	return s.acceptMonitorBatch(project, collector, receipt, points)
}
func (s *SQLiteStore) CompleteLegacyPrometheusFlush(project, collector, id string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	var raw, state string
	if err = tx.QueryRow(`SELECT payload,state FROM run_monitor_jobs WHERE id=? AND project_id=? AND collector_id=?`, id, project, collector).Scan(&raw, &state); err != nil {
		return err
	}
	var job RunMonitorJob
	if json.Unmarshal([]byte(raw), &job) != nil || job.Source.Pack != "prometheus" || !job.FlushOnly || job.LoadEndedAt == nil {
		return errors.New("job is not an exporter flush assignment")
	}
	if state == "stopped" || state == "incomplete" {
		return tx.Commit()
	}
	if state != "flushing" {
		return errors.New("exporter job is not flushing")
	}
	if err = finalizePrometheusScrapeJobTx(tx, &job, *job.LoadEndedAt, true); err != nil {
		return err
	}
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE run_monitor_jobs SET state=?,payload=? WHERE id=? AND project_id=?`, job.State, string(body), id, project); err != nil {
		return err
	}
	return tx.Commit()
}

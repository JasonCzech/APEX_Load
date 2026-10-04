package storage

import (
	"encoding/json"
	"errors"
	"time"
)

// PollDynatraceCancellationJobs discovers bounded direct cleanup work after
// terminality. The private cursor survives archive closure and owner changes;
// cleanup cannot extend collection or mutate canonical observations.
func (s *SQLiteStore) PollDynatraceCancellationJobs(now time.Time, limit int) ([]RunMonitorJob, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, errors.New("invalid Dynatrace cancellation poll")
	}
	rows, err := s.db.Query(`SELECT payload,state,next_sequence FROM run_monitor_jobs WHERE collector_id='' AND (state IN ('stopped','incomplete') OR expires_at<=?) AND payload LIKE '%"pack":"dynatrace"%' AND payload LIKE '%"provider_cursor":%' ORDER BY expires_at,id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	jobs, err := readRunMonitorJobs(rows)
	if err != nil {
		return nil, err
	}
	out := make([]RunMonitorJob, 0, len(jobs))
	for _, job := range jobs {
		if job.Source.Pack == "dynatrace" && job.Source.ExecutorMode == "direct" && job.DynatraceSnapshot != nil && job.ProviderCursor != "" {
			out = append(out, job)
		}
	}
	return out, nil
}

func (s *SQLiteStore) ClearDynatraceCancellationCursorFenced(project, jobID, cursor, leaseName, owner string, generation uint64) (bool, error) {
	if project == "" || jobID == "" || cursor == "" || len(cursor) > 16<<10 || leaseName != "" && (owner == "" || generation == 0) {
		return false, errors.New("invalid Dynatrace cancellation completion")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, owner, generation)
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
	var body, state string
	var expires time.Time
	if err = tx.QueryRow(`SELECT payload,state,expires_at FROM run_monitor_jobs WHERE id=? AND project_id=? AND collector_id=''`, jobID, project).Scan(&body, &state, &expires); err != nil {
		return false, err
	}
	var job RunMonitorJob
	if err = json.Unmarshal([]byte(body), &job); err != nil {
		return false, err
	}
	if job.Source.Pack != "dynatrace" || job.Source.ExecutorMode != "direct" || job.ProviderCursor != cursor || state != "stopped" && state != "incomplete" && expires.After(time.Now().UTC()) {
		return false, nil
	}
	job.ProviderCursor = ""
	job.State = state
	raw, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE run_monitor_jobs SET payload=? WHERE id=? AND project_id=? AND payload=?`, string(raw), jobID, project, body)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, tx.Commit()
}

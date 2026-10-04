package storage

import (
	"errors"
	"strings"
	"time"
)

// RunMonitorJobPageStore supplements the original array contract. A collector
// can advance fairly across concurrent runs without increasing a response's
// grant or byte budget. MaxRunMonitorSources remains a per-run limit.
type RunMonitorJobPageStore interface {
	PollRunMonitorJobPage(project, collector, after string, limit int, now time.Time) ([]RunMonitorJob, bool, error)
}

const pollMonitorJobsScope = ` FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id WHERE j.project_id=? AND j.collector_id=? AND j.state IN ('collecting','reconciling') AND j.expires_at>? AND (t.status IN ('RUNNING','PAUSED') OR j.state='reconciling')`

// A durable job key joins two independently bounded 256-byte identities.
const MaxRunMonitorJobIDBytes = 513

func (s *SQLiteStore) PollRunMonitorJobPage(project, collector, after string, limit int, now time.Time) ([]RunMonitorJob, bool, error) {
	if limit < 1 || limit > MaxRunMonitorSources || len(after) > MaxRunMonitorJobIDBytes || strings.TrimSpace(after) != after || strings.ContainsAny(after, "\r\n\x00") {
		return nil, false, errors.New("invalid collector job page")
	}
	rows, err := s.db.Query(`SELECT j.payload,j.state,j.next_sequence`+pollMonitorJobsScope+` AND j.id>? ORDER BY j.id LIMIT ?`, project, collector, now.UTC(), after, limit+1)
	if err != nil {
		return nil, false, err
	}
	jobs, err := readRunMonitorJobs(rows)
	if err != nil {
		return nil, false, err
	}
	more := len(jobs) > limit
	if more {
		jobs = jobs[:limit]
	}
	return jobs, more, nil
}

// Older collectors receive a bounded rotating page in the original array
// shape. Current collectors use the cursor protocol to retain retries across
// pages. No number of concurrent runs causes the whole collector poll to fail.
func (s *SQLiteStore) pollLegacyRunMonitorJobs(project, collector string, now time.Time) ([]RunMonitorJob, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*)`+pollMonitorJobsScope, project, collector, now.UTC()).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return []RunMonitorJob{}, nil
	}
	pages := (count + MaxRunMonitorSources - 1) / MaxRunMonitorSources
	page := (now.Unix() / 15) % int64(pages)
	if page < 0 {
		page += int64(pages)
	}
	rows, err := s.db.Query(`SELECT j.payload,j.state,j.next_sequence`+pollMonitorJobsScope+` ORDER BY j.id LIMIT ? OFFSET ?`, project, collector, now.UTC(), MaxRunMonitorSources, page*MaxRunMonitorSources)
	if err != nil {
		return nil, err
	}
	return readRunMonitorJobs(rows)
}

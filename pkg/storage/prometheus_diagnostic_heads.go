package storage

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

type PrometheusDiagnosticObservationTimes struct {
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

func retainPrometheusDiagnosticTimes(job *RunMonitorJob, key string, diagnostic monitorprovider.Diagnostic) (first, latest bool, err error) {
	if job.PrometheusDiagnosticTimes == nil {
		job.PrometheusDiagnosticTimes = map[string]PrometheusDiagnosticObservationTimes{}
	}
	previous, exists := job.PrometheusDiagnosticTimes[key]
	if !exists && len(job.PrometheusDiagnosticTimes) >= 1000 {
		return false, false, errors.New("Prometheus diagnostic identity budget exceeded")
	}
	at := diagnostic.TimestampUTC.UTC()
	first = previous.First.IsZero() || at.Before(previous.First)
	latest = previous.Last.IsZero() || !at.Before(previous.Last)
	if first {
		previous.First = at
	}
	if latest {
		previous.Last = at
	}
	job.PrometheusDiagnosticTimes[key] = previous
	return first, latest, nil
}

// ListProviderFirstDiagnosticHeads resolves only the identities in the requested
// evidence page. Its SQL work and result count remain bounded by that page.
func (s *SQLiteStore) ListProviderFirstDiagnosticHeads(project, run string, keys []string) ([]ObservationHead, error) {
	if len(keys) > 999 {
		return nil, errors.New("first diagnostic head page exceeds limits")
	}
	if len(keys) == 0 {
		return []ObservationHead{}, nil
	}
	args := []any{project, run}
	for _, key := range keys {
		if len(key) != 64 {
			return nil, errors.New("invalid diagnostic identity")
		}
		if _, err := hex.DecodeString(key); err != nil {
			return nil, errors.New("invalid diagnostic identity")
		}
		args = append(args, "first:"+key)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := s.db.Query(`SELECT h.job_id,h.record_key,h.sequence,h.record_index,h.record_kind FROM run_monitor_observation_heads h JOIN run_monitor_jobs j ON j.id=h.job_id WHERE j.project_id=? AND j.test_id=? AND h.record_kind='diagnostic_first' AND h.record_key IN (`+placeholders+`) ORDER BY h.record_key,h.job_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	heads := []ObservationHead{}
	for rows.Next() {
		var head ObservationHead
		if err := rows.Scan(&head.JobID, &head.Key, &head.Sequence, &head.Index, &head.Kind); err != nil {
			return nil, err
		}
		heads = append(heads, head)
	}
	return heads, rows.Err()
}

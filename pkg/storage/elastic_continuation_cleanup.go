package storage

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const maxElasticCleanupBytes = 256 << 10

// Cleanup ownership is independent from query coverage and pagination. A
// complete read can retain a failed DELETE privately until cleanup succeeds.
func retainElasticCleanupContinuations(job *RunMonitorJob, incoming []*analysis.ProviderContinuation, from, until, now time.Time) error {
	for _, cursor := range incoming {
		if cursor == nil || cursor.Validate() != nil || cursor.PITID == "" && cursor.AsyncID == "" || !cursor.WindowFrom.Equal(from) || !cursor.WindowUntil.Equal(until) {
			return errors.New("invalid owned Elastic cleanup continuation")
		}
	}
	retained := make([]*analysis.ProviderContinuation, 0, len(job.ElasticRetiredContinuations)+len(incoming))
	for _, cursor := range append(append([]*analysis.ProviderContinuation(nil), job.ElasticRetiredContinuations...), incoming...) {
		if cursor == nil || !cursor.KeepAliveUntil.IsZero() && !cursor.KeepAliveUntil.After(now) {
			continue
		}
		duplicate := false
		for i, prior := range retained {
			if prior.PITID == cursor.PITID && prior.AsyncID == cursor.AsyncID {
				copy := *cursor
				copy.ResolvedIndices, copy.SearchAfter, copy.CompositeAfter = nil, nil, nil
				retained[i] = &copy
				duplicate = true
				break
			}
		}
		if !duplicate {
			copy := *cursor
			copy.ResolvedIndices, copy.SearchAfter, copy.CompositeAfter = nil, nil, nil
			retained = append(retained, &copy)
		}
	}
	if len(retained) > 32 {
		return errors.New("Elastic retired continuation cleanup backlog reached")
	}
	body, err := json.Marshal(retained)
	if err != nil || len(body) > maxElasticCleanupBytes {
		return errors.New("Elastic retired continuation cleanup exceeds private bound")
	}
	job.ElasticRetiredContinuations = retained
	return nil
}

// Apply backpressure before LIMIT/OFFSET so suspended Elastic jobs do not
// consume collector pages or hide ready jobs behind an empty cursor page.
func (s *SQLiteStore) elasticCleanupReadyPredicate(payloadColumn string) string {
	if s.db.dialect == storeDialectPostgres {
		return ` AND COALESCE(jsonb_array_length(NULLIF(` + payloadColumn + `::jsonb->'retired_continuations','null'::jsonb)),0)=0`
	}
	return ` AND COALESCE(json_array_length(json_extract(` + payloadColumn + `,'$.retired_continuations')),0)=0`
}

// Cleanup cursors remain private and recoverable after cancellation or a drain
// deadline. The next cursor includes every examined row, including other packs,
// so a bounded cleanup sweep cannot starve behind unrelated terminal jobs.
func (s *SQLiteStore) ListElasticContinuationCleanupJobs(project, run, after string, limit int) ([]RunMonitorJob, string, error) {
	if limit < 1 || limit > 256 || len(after) > 768 {
		return nil, "", errors.New("invalid cleanup page")
	}
	rows, err := s.db.Query(`SELECT payload,state,next_sequence FROM run_monitor_jobs WHERE state IN ('collecting','reconciling','stopped','incomplete') AND (?='' OR project_id=?) AND (?='' OR test_id=?) AND id>? ORDER BY id LIMIT ?`, project, project, run, run, after, limit)
	if err != nil {
		return nil, "", err
	}
	jobs, err := readRunMonitorJobs(rows)
	if err != nil {
		return nil, "", err
	}
	result := []RunMonitorJob{}
	next := after
	for _, job := range jobs {
		next = job.ID
		if job.Source.Pack == "elastic" {
			if len(job.ElasticRetiredContinuations) > 0 {
				job.ElasticContinuation = job.ElasticRetiredContinuations[0]
				result = append(result, job)
			} else if (job.State == "stopped" || job.State == "incomplete") && job.ElasticContinuation != nil {
				result = append(result, job)
			}
		}
	}
	if len(jobs) < limit {
		next = ""
	}
	return result, next, nil
}

// Clear only the terminal cursor whose owned upstream work was acknowledged.
// A former controller owner or stale collector grant cannot clear a replacement
// cursor, even when the PIT/async handle has already expired upstream.
func (s *SQLiteStore) ClearElasticContinuation(project, jobID string, expected *analysis.ProviderContinuation, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if expected == nil || expected.Validate() != nil {
		return false, errors.New("expected Elastic continuation required")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, generation)
		if err != nil {
			return false, err
		}
		if tx == nil {
			return false, nil
		}
	} else {
		tx, err = s.db.Begin()
		if err != nil {
			return false, err
		}
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return false, err
	}
	var raw, state string
	if err = tx.QueryRow(`SELECT payload,state FROM run_monitor_jobs WHERE id=? AND project_id=?`, jobID, project).Scan(&raw, &state); err != nil {
		return false, err
	}
	if state != "stopped" && state != "incomplete" && state != "collecting" && state != "reconciling" {
		return false, nil
	}
	var job RunMonitorJob
	if err = json.Unmarshal([]byte(raw), &job); err != nil {
		return false, err
	}
	if job.Source.Pack != "elastic" {
		return false, nil
	}
	found := false
	for i, cursor := range job.ElasticRetiredContinuations {
		if reflect.DeepEqual(cursor, expected) {
			job.ElasticRetiredContinuations = append(job.ElasticRetiredContinuations[:i], job.ElasticRetiredContinuations[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		if (state != "stopped" && state != "incomplete") || !reflect.DeepEqual(job.ElasticContinuation, expected) {
			return false, nil
		}
		job.ElasticContinuation = nil
	}
	job.State = state
	body, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE run_monitor_jobs SET payload=? WHERE id=? AND project_id=? AND state=? AND payload=?`, string(body), jobID, project, state, raw)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

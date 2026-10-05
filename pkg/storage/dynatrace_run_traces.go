package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

const MaxDynatraceRunTraceIDs = 64

func normalizeDynatraceRunTraceIDs(ids []string) ([]string, error) {
	if len(ids) > MaxDynatraceRunTraceIDs {
		return nil, errors.New("run trace identifiers exceed sampling bound")
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if len(id) != 32 {
			return nil, errors.New("invalid W3C trace identifier")
		}
		decoded, err := hex.DecodeString(id)
		if err != nil {
			return nil, errors.New("invalid W3C trace identifier")
		}
		nonzero := false
		for _, b := range decoded {
			nonzero = nonzero || b != 0
		}
		if !nonzero {
			return nil, errors.New("invalid zero W3C trace identifier")
		}
		id = strings.ToLower(id)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// RecordDynatraceRunTraceIDsFenced retains a bounded sample of actual run-owned
// W3C identifiers. This private attribution state never changes a provider
// artifact, receipt digest, query cursor, or monitoring coverage. The first 64
// observed identifiers remain stable across retries and executor restarts.
func (s *SQLiteStore) RecordDynatraceRunTraceIDsFenced(project, run string, startedAt time.Time, traceIDs []string, leaseName, leaseOwner string, generation uint64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.RecordDynatraceRunTraceIDsFencedContext(ctx, project, run, startedAt, traceIDs, leaseName, leaseOwner, generation)
}

func (s *SQLiteStore) RecordDynatraceRunTraceIDsFencedContext(ctx context.Context, project, run string, startedAt time.Time, traceIDs []string, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if ctx == nil {
		return false, errors.New("run trace attribution requires context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if project == "" || run == "" || startedAt.IsZero() {
		return false, errors.New("run trace attribution requires project, run, and incarnation")
	}
	ids, err := normalizeDynatraceRunTraceIDs(traceIDs)
	if err != nil || len(ids) == 0 {
		return false, err
	}
	var tx *storeTx
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransactionContext(ctx, leaseName, leaseOwner, generation)
		if err == nil && tx == nil {
			return false, errors.New("monitor execution owner fence rejected")
		}
	} else {
		tx, err = s.db.BeginTx(ctx, nil)
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutationContext(ctx, tx, project); err != nil {
		return false, err
	}
	identityQuery := `SELECT started_at FROM tests WHERE id=? AND project_id=?`
	if s.db.dialect == storeDialectPostgres {
		identityQuery += ` FOR UPDATE`
	}
	var currentStartedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, identityQuery, run, project).Scan(&currentStartedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !shakeoutTransitionTimeEqual(currentStartedAt, &startedAt, s.db.dialect) {
		return false, errors.New("run trace attribution incarnation fence rejected")
	}
	rows, err := tx.QueryContext(ctx, `SELECT j.payload,j.state,j.next_sequence FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id AND t.project_id=j.project_id WHERE j.project_id=? AND j.test_id=? AND j.state IN ('collecting','reconciling') ORDER BY j.id LIMIT ?`, project, run, MaxRunMonitorSources+1)
	if err != nil {
		return false, err
	}
	jobs, err := readRunMonitorJobs(rows)
	if err != nil {
		return false, err
	}
	if len(jobs) > MaxRunMonitorSources {
		return false, errors.New("run trace attribution exceeds source bound")
	}
	accepted := false
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if job.Source.Pack != monitorprovider.PackDynatrace {
			continue
		}
		if job.ProjectID != project || job.RunID != run || job.DynatraceSnapshot == nil || job.DynatraceSnapshot.ProjectID != project || job.Source.ProjectID != project || job.Source.Dynatrace == nil || job.Source.Dynatrace.ProjectID != project {
			return false, errors.New("invalid native run trace attribution scope")
		}
		if !shakeoutTransitionTimeEqual(sql.NullTime{Time: job.StartedAt, Valid: true}, &startedAt, s.db.dialect) {
			return false, errors.New("monitor job trace attribution incarnation fence rejected")
		}
		prior, err := normalizeDynatraceRunTraceIDs(job.RunTraceIDs)
		if err != nil {
			return false, err
		}
		seen := make(map[string]bool, MaxDynatraceRunTraceIDs)
		for _, id := range prior {
			seen[id] = true
		}
		for _, id := range ids {
			if !seen[id] && len(prior) < MaxDynatraceRunTraceIDs {
				seen[id] = true
				prior = append(prior, id)
			}
		}
		accepted = true
		if slices.Equal(job.RunTraceIDs, prior) {
			continue
		}
		job.RunTraceIDs = prior
		body, err := json.Marshal(job)
		if err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE run_monitor_jobs SET payload=? WHERE id=? AND project_id=? AND test_id=? AND state IN ('collecting','reconciling')`, string(body), job.ID, project, run); err != nil {
			return false, err
		}
	}
	return accepted, tx.Commit()
}

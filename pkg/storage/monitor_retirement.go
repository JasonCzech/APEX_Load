package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"sort"
	"strings"
)

var ErrMonitorProviderRetired = errors.New("AppDynamics monitoring is retired; remove its monitor bindings before preparing or starting this test")

// RetireAppDynamicsMonitoring terminalizes abandoned provider work without
// touching retained evidence, definitions or already terminal records. An empty
// fence is for single-controller installations. False means the supplied HA
// execution term no longer owns the lease; true also covers an idempotent retry.
func (s *SQLiteStore) RetireAppDynamicsMonitoring(leaseName, leaseOwner string, leaseGeneration uint64) (bool, error) {
	var tx *storeTx
	var err error
	if leaseName != "" || leaseOwner != "" || leaseGeneration != 0 {
		if strings.TrimSpace(leaseName) == "" || strings.TrimSpace(leaseOwner) == "" || leaseGeneration == 0 {
			return false, errors.New("complete execution lease fence required for monitoring retirement")
		}
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, leaseGeneration)
		if err != nil || tx == nil {
			return false, err
		}
	} else {
		tx, err = s.db.Begin()
		if err != nil {
			return false, err
		}
	}
	defer tx.Rollback()
	type retiredJob struct {
		id, project, state, before, after string
	}
	jobs := []retiredJob{}
	projects := map[string]bool{}
	// Preserve unknown and private JSON fields: a typed round trip would erase
	// fields belonging to a newer installation during a rollback or upgrade.
	rows, err := tx.Query(`SELECT id,project_id,state,payload FROM run_monitor_jobs WHERE state IN ('collecting','reconciling') ORDER BY id`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var job retiredJob
		if err = rows.Scan(&job.id, &job.project, &job.state, &job.before); err != nil {
			rows.Close()
			return false, err
		}
		var fields map[string]json.RawMessage
		var source struct {
			Pack string `json:"pack"`
		}
		if err = json.Unmarshal([]byte(job.before), &fields); err == nil {
			err = json.Unmarshal(fields["source"], &source)
		}
		if err != nil {
			rows.Close()
			return false, err
		}
		if source.Pack != monitorprovider.PackAppDynamics {
			continue
		}
		fields["state"] = json.RawMessage(`"incomplete"`)
		fields["coverage_reason"] = json.RawMessage(`"provider_retired"`)
		body, marshalErr := json.Marshal(fields)
		if marshalErr != nil {
			rows.Close()
			return false, marshalErr
		}
		job.after = string(body)
		jobs = append(jobs, job)
		projects[job.project] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	rows, err = tx.Query(`SELECT DISTINCT project_id FROM appdynamics_inspections WHERE provider='appdynamics' AND state IN ('pending','running')`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var project string
		if err = rows.Scan(&project); err != nil {
			rows.Close()
			return false, err
		}
		projects[project] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	ordered := make([]string, 0, len(projects))
	for project := range projects {
		ordered = append(ordered, project)
	}
	sort.Strings(ordered)
	for _, project := range ordered {
		if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
			return false, err
		}
	}
	for _, job := range jobs {
		result, updateErr := tx.Exec(`UPDATE run_monitor_jobs SET state='incomplete',payload=? WHERE id=? AND project_id=? AND state=? AND payload=?`, job.after, job.id, job.project, job.state, job.before)
		if updateErr != nil {
			return false, updateErr
		}
		changed, updateErr := result.RowsAffected()
		if updateErr != nil {
			return false, updateErr
		}
		if changed == 1 {
			continue
		}
		// A rolling upgrade may still have an old provider worker. Do not
		// report completion if its newer active payload escaped this sweep.
		var current string
		if err = tx.QueryRow(`SELECT state FROM run_monitor_jobs WHERE id=? AND project_id=?`, job.id, job.project).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if current == "collecting" || current == "reconciling" {
			return false, errors.New("monitor retirement raced an active update; retry")
		}
	}
	if _, err = tx.Exec(`UPDATE appdynamics_inspections SET state='error',error='provider_retired',owner_token='',lease_expires_at=NULL WHERE provider='appdynamics' AND state IN ('pending','running')`); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

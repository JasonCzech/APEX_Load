package storage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/elastic"
)

// ElasticSecretConsumer exposes resource identity and a reference count,
// never a secret locator or resolved value. It supplements script consumers.
type ElasticSecretConsumer = MonitorSecretConsumer

type ElasticSecretReferenceStore interface {
	ListElasticSecretConsumers(project, name string) ([]ElasticSecretConsumer, error)
	InvalidateElasticSecretPreparations(project, name string, now time.Time) ([]Preparation, error)
}

func elasticSecretReferenceCount(snapshot *ExpandedElasticConnectionSnapshot, name string) int {
	canonicalName, err := credential.NormalizeLogicalName(name)
	if snapshot == nil || err != nil {
		return 0
	}
	count := 0
	for _, reference := range []string{snapshot.APIKeySecretName, snapshot.UsernameSecretName, snapshot.PasswordSecretName, snapshot.BearerTokenSecretName, snapshot.ClientCertificateSecretName, snapshot.ClientKeySecretName, snapshot.KibanaCredentials.APIKeySecretName, snapshot.KibanaCredentials.UsernameSecretName, snapshot.KibanaCredentials.PasswordSecretName, snapshot.KibanaCredentials.BearerTokenSecretName, snapshot.KibanaCredentials.ClientCertificateSecretName, snapshot.KibanaCredentials.ClientKeySecretName, snapshot.LogstashCredentials.APIKeySecretName, snapshot.LogstashCredentials.UsernameSecretName, snapshot.LogstashCredentials.PasswordSecretName, snapshot.LogstashCredentials.BearerTokenSecretName, snapshot.LogstashCredentials.ClientCertificateSecretName, snapshot.LogstashCredentials.ClientKeySecretName} {
		// Frozen profiles preserve their original logical spelling. Secret APIs
		// address the canonical identity, including case and dot/hyphen aliases.
		canonicalReference, err := credential.NormalizeLogicalName(reference)
		if err == nil && canonicalReference == canonicalName {
			count++
		}
	}
	return count
}

func (s *SQLiteStore) ListElasticSecretConsumers(project, name string) ([]ElasticSecretConsumer, error) {
	consumers := []ElasticSecretConsumer{}
	connections, err := s.ListELKConnections(project)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if count := elasticSecretReferenceCount(connection.Snapshot(), name); count > 0 {
			consumers = append(consumers, ElasticSecretConsumer{"elastic_connection", connection.ID, connection.Name, count})
		}
	}
	inspections, err := s.db.Query(`SELECT `+elasticInspectionColumns+` FROM elastic_inspections WHERE project_id=?`, project)
	if err != nil {
		return nil, err
	}
	inspectionNow := time.Now().UTC()
	for inspections.Next() {
		op, err := scanElasticInspection(inspections)
		if err != nil {
			inspections.Close()
			return nil, err
		}
		if !elasticInspectionNeedsCredentials(op, inspectionNow) {
			continue
		}
		snapshot, err := elasticInspectionConnectionSnapshot(op.PayloadJSON)
		if err != nil {
			inspections.Close()
			return nil, err
		}
		if count := elasticSecretReferenceCount(snapshot, name); count > 0 {
			consumers = append(consumers, ElasticSecretConsumer{"elastic_inspection", op.ID, "Elastic " + op.Kind + " inspection", count})
		}
	}
	err = inspections.Err()
	inspections.Close()
	if err != nil {
		return nil, err
	}
	// Read both sides of the preparation-to-job handoff in one statement. Separate
	// queries could observe no jobs before the handoff, then exclude the consumed
	// preparation after jobs were inserted. The SQL statement's snapshot avoids
	// that gap on both SQLite and PostgreSQL.
	rows, err := s.db.Query(`SELECT 'run_monitor_job',payload,state FROM run_monitor_jobs WHERE project_id=? AND state IN ('collecting','reconciling','stopped','incomplete') UNION ALL SELECT 'preparation',p.payload,p.state FROM preparations p JOIN tests t ON t.id=p.test_id AND t.project_id=p.project_id WHERE p.project_id=? AND (p.state IN ('preparing','ready') OR (p.state='consumed' AND t.status IN ('INITIALIZING','RUNNING','PAUSED','STOPPING') AND NOT EXISTS (SELECT 1 FROM run_monitor_jobs j WHERE j.project_id=p.project_id AND j.test_id=p.test_id)))`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var kind, body, state string
		if err = rows.Scan(&kind, &body, &state); err != nil {
			return nil, err
		}
		if kind == "run_monitor_job" {
			var job RunMonitorJob
			if err = json.Unmarshal([]byte(body), &job); err != nil {
				return nil, err
			}
			if state != "collecting" && state != "reconciling" && !elasticJobNeedsCleanupCredentials(job, now) {
				continue
			}
			if count := elasticSecretReferenceCount(job.ElasticConnectionSnapshot, name); count > 0 {
				consumers = append(consumers, ElasticSecretConsumer{kind, job.ID, job.Source.Name, count})
			}
			continue
		}
		var preparation Preparation
		if err = json.Unmarshal([]byte(body), &preparation); err != nil {
			return nil, err
		}
		if count := preparationElasticSecretReferences(&preparation, name); count > 0 {
			consumers = append(consumers, ElasticSecretConsumer{kind, preparation.ID, "Preparation for test " + preparation.TestID, count})
		}
	}
	return consumers, rows.Err()
}

// Frozen terminal artifacts are historical references. Only outstanding owned
// upstream work continues to use credentials after the collection job ends.
func elasticJobNeedsCleanupCredentials(job RunMonitorJob, now time.Time) bool {
	needsCleanup := func(cursor *analysis.ProviderContinuation) bool {
		return cursor != nil && (cursor.PITID != "" || cursor.AsyncID != "") && (cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(now))
	}
	if needsCleanup(job.ElasticContinuation) {
		return true
	}
	for _, cursor := range job.ElasticRetiredContinuations {
		if needsCleanup(cursor) {
			return true
		}
	}
	return false
}

func preparationElasticSecretReferences(preparation *Preparation, name string) int {
	if preparation == nil || preparation.Monitors == nil {
		return 0
	}
	count := 0
	for _, connection := range preparation.Monitors.ExpandedElasticConnections {
		count += elasticSecretReferenceCount(connection, name)
	}
	return count
}

// Invalidation shares the project row lock with preparation consumption. A
// consumed run preserves its immutable snapshot; unconsumed authorizations must
// be verified again after credential creation or replacement.
func (s *SQLiteStore) InvalidateElasticSecretPreparations(project, name string, now time.Time) ([]Preparation, error) {
	if project == "" || name == "" {
		return nil, errors.New("project and logical credential name required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE project_id=?`, project)
	if err != nil {
		return nil, err
	}
	var connections []*ELKConnection
	for rows.Next() {
		c, e := scanELKConnection(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if elasticSecretReferenceCount(c.Snapshot(), name) > 0 {
			c.Revision++
			c.VerifiedRevision = 0
			c.UpdatedAt = now.UTC()
			c.LastCheckedAt = nil
			c.LastCheckStatus = "credential_changed"
			c.LastCheckMessage = "Connection credentials changed; verify before the next run"
			c.Capabilities = elastic.Capabilities{}
			connections = append(connections, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, c := range connections {
		body, e := marshalElasticConnection(c)
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec(`UPDATE elk_connections SET elastic_profile_json=?,updated_at=?,last_checked_at=NULL,last_check_status='credential_changed',last_check_message=? WHERE id=? AND project_id=?`, string(body), now.UTC(), c.LastCheckMessage, c.ID, project); err != nil {
			return nil, err
		}
	}

	rows, err = tx.Query(`SELECT payload,owner_fence FROM preparations WHERE project_id=? AND state IN ('preparing','ready')`, project)
	if err != nil {
		return nil, err
	}
	var cancelled []Preparation
	for rows.Next() {
		var body, owner string
		if err = rows.Scan(&body, &owner); err != nil {
			rows.Close()
			return nil, err
		}
		var preparation Preparation
		if err = json.Unmarshal([]byte(body), &preparation); err != nil {
			rows.Close()
			return nil, err
		}
		if preparationElasticSecretReferences(&preparation, name) == 0 {
			continue
		}
		preparation.OwnerFence = owner
		preparation.State = "cancelled"
		preparation.Failure = "Monitoring credentials changed; prepare the test again"
		preparation.Revision++
		cancelled = append(cancelled, preparation)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, preparation := range cancelled {
		body, marshalErr := json.Marshal(preparation)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if _, err = tx.Exec(`UPDATE preparations SET state='cancelled',revision=?,payload=? WHERE id=? AND project_id=? AND state IN ('preparing','ready')`, preparation.Revision, string(body), preparation.ID, project); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return cancelled, nil
}

package storage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

// AppDynamicsSecretConsumer exposes resource identity and a reference count,
// never a secret locator or resolved value. It supplements script consumers.
type AppDynamicsSecretConsumer struct {
	ResourceType   string
	ResourceID     string
	Name           string
	ReferenceCount int
}

type AppDynamicsSecretReferenceStore interface {
	ListAppDynamicsSecretConsumers(project, name string) ([]AppDynamicsSecretConsumer, error)
	InvalidateAppDynamicsSecretPreparations(project, name string, now time.Time) ([]Preparation, error)
}

func appDynamicsSecretReferenceCount(snapshot *AppDynamicsConnectionSnapshot, name string) int {
	canonicalName, err := credential.NormalizeLogicalName(name)
	if snapshot == nil || err != nil {
		return 0
	}
	count := 0
	for _, reference := range []string{snapshot.ClientIDSecretName, snapshot.ClientSecretSecretName, snapshot.UsernameSecretName, snapshot.PasswordSecretName, snapshot.EventsAPIKeySecretName} {
		// Frozen profiles preserve their original logical spelling. Secret APIs
		// address the canonical identity, including case and dot/hyphen aliases.
		canonicalReference, err := credential.NormalizeLogicalName(reference)
		if err == nil && canonicalReference == canonicalName {
			count++
		}
	}
	return count
}

func (s *SQLiteStore) ListAppDynamicsSecretConsumers(project, name string) ([]AppDynamicsSecretConsumer, error) {
	consumers := []AppDynamicsSecretConsumer{}
	connections, err := s.ListAppDynamicsConnections(project)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if count := appDynamicsSecretReferenceCount(connection.Snapshot(), name); count > 0 {
			consumers = append(consumers, AppDynamicsSecretConsumer{"appdynamics_connection", connection.ID, connection.Name, count})
		}
	}
	promConnections, err := s.ListPrometheusConnections(project)
	if err != nil {
		return nil, err
	}
	for _, connection := range promConnections {
		if count := prometheusSecretReferenceCount(connection.Snapshot(), name); count > 0 {
			consumers = append(consumers, AppDynamicsSecretConsumer{"prometheus_connection", connection.ID, connection.Name, count})
		}
	}
	// Read both sides of the preparation-to-job handoff in one statement. Separate
	// queries could observe no jobs before the handoff, then exclude the consumed
	// preparation after jobs were inserted. The SQL statement's snapshot avoids
	// that gap on both SQLite and PostgreSQL.
	rows, err := s.db.Query(`SELECT 'run_monitor_job',payload FROM run_monitor_jobs WHERE project_id=? AND state IN ('collecting','reconciling') UNION ALL SELECT 'preparation',p.payload FROM preparations p JOIN tests t ON t.id=p.test_id AND t.project_id=p.project_id WHERE p.project_id=? AND (p.state IN ('preparing','ready') OR (p.state='consumed' AND t.status IN ('INITIALIZING','RUNNING','PAUSED','STOPPING') AND NOT EXISTS (SELECT 1 FROM run_monitor_jobs j WHERE j.project_id=p.project_id AND j.test_id=p.test_id)))`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, body string
		if err = rows.Scan(&kind, &body); err != nil {
			return nil, err
		}
		if kind == "run_monitor_job" {
			var job RunMonitorJob
			if err = json.Unmarshal([]byte(body), &job); err != nil {
				return nil, err
			}
			if count := appDynamicsSecretReferenceCount(job.ConnectionSnapshot, name); count > 0 {
				consumers = append(consumers, AppDynamicsSecretConsumer{kind, job.ID, job.Source.Name, count})
			}
			if count := prometheusSecretReferenceCount(job.PrometheusConnectionSnapshot, name); count > 0 {
				consumers = append(consumers, AppDynamicsSecretConsumer{kind, job.ID, job.Source.Name, count})
			}
			continue
		}
		var preparation Preparation
		if err = json.Unmarshal([]byte(body), &preparation); err != nil {
			return nil, err
		}
		if count := preparationAppDynamicsSecretReferences(&preparation, name) + preparationPrometheusSecretReferences(&preparation, name); count > 0 {
			consumers = append(consumers, AppDynamicsSecretConsumer{kind, preparation.ID, "Preparation for test " + preparation.TestID, count})
		}
	}
	return consumers, rows.Err()
}

func preparationAppDynamicsSecretReferences(preparation *Preparation, name string) int {
	if preparation == nil || preparation.Monitors == nil {
		return 0
	}
	count := 0
	for _, connection := range preparation.Monitors.Connections {
		count += appDynamicsSecretReferenceCount(connection, name)
	}
	return count
}

// Invalidation shares the project row lock with preparation consumption. A
// consumed run preserves its immutable snapshot; unconsumed authorizations must
// be verified again after credential creation or replacement.
func (s *SQLiteStore) InvalidateAppDynamicsSecretPreparations(project, name string, now time.Time) ([]Preparation, error) {
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
	rows, err := tx.Query(`SELECT payload_json,revision FROM appdynamics_connections WHERE project_id=?`, project)
	if err != nil {
		return nil, err
	}
	var connections []*AppDynamicsConnection
	for rows.Next() {
		var body string
		var revision int64
		if err = rows.Scan(&body, &revision); err != nil {
			rows.Close()
			return nil, err
		}
		connection, decodeErr := UnmarshalAppDynamicsConnectionPrivate([]byte(body))
		if decodeErr != nil {
			rows.Close()
			return nil, decodeErr
		}
		if appDynamicsSecretReferenceCount(connection.Snapshot(), name) > 0 {
			connection.Revision = revision + 1
			connection.VerifiedRevision = 0
			connection.UpdatedAt = now.UTC()
			connection.LastCheckedAt = nil
			connection.LastCheckStatus = "credential_changed"
			connection.LastCheckMessage = "Connection credentials changed; verify before the next run"
			connection.Capabilities = appdynamics.Capabilities{}
			connections = append(connections, connection)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		body, marshalErr := MarshalAppDynamicsConnectionPrivate(connection)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if _, err = tx.Exec(`UPDATE appdynamics_connections SET revision=?,verified_revision=0,payload_json=?,updated_at=? WHERE id=? AND project_id=?`, connection.Revision, string(body), now.UTC(), connection.ID, project); err != nil {
			return nil, err
		}
	}
	rows, err = tx.Query(`SELECT payload_json,revision FROM prometheus_connections WHERE project_id=?`, project)
	if err != nil {
		return nil, err
	}
	var promConnections []*PrometheusConnection
	for rows.Next() {
		var body string
		var revision int64
		if err = rows.Scan(&body, &revision); err != nil {
			rows.Close()
			return nil, err
		}
		connection, e := UnmarshalPrometheusConnectionPrivate([]byte(body))
		if e != nil {
			rows.Close()
			return nil, e
		}
		if prometheusSecretReferenceCount(connection.Snapshot(), name) > 0 {
			connection.Revision = revision + 1
			connection.VerifiedRevision = 0
			connection.UpdatedAt = now.UTC()
			connection.LastCheckedAt = nil
			connection.LastCheckStatus = "credential_changed"
			connection.LastCheckMessage = "Connection credentials changed; verify before the next run"
			connection.Capabilities = prometheus.Capabilities{}
			promConnections = append(promConnections, connection)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, connection := range promConnections {
		body, e := MarshalPrometheusConnectionPrivate(connection)
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec(`UPDATE prometheus_connections SET revision=?,verified_revision=0,payload_json=?,updated_at=? WHERE id=? AND project_id=?`, connection.Revision, string(body), now.UTC(), connection.ID, project); err != nil {
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
		if preparationAppDynamicsSecretReferences(&preparation, name)+preparationPrometheusSecretReferences(&preparation, name) == 0 {
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

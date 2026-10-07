package storage

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"time"
)

type DynatraceSecretConsumer = MonitorSecretConsumer
type DynatraceSecretReferenceStore interface {
	ListDynatraceSecretConsumers(project, name string) ([]DynatraceSecretConsumer, error)
	InvalidateDynatraceSecretPreparations(project, name string, now time.Time) ([]Preparation, error)
}

func dynatraceSecretReferenceCount(c *DynatraceConnectionSnapshot, name string) int {
	logical, err := credential.NormalizeLogicalName(name)
	if c == nil || err != nil {
		return 0
	}
	count := 0
	for _, ref := range []string{c.EnvironmentTokenSecret, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret} {
		canonical, e := credential.NormalizeLogicalName(ref)
		if e == nil && canonical == logical {
			count++
		}
	}
	return count
}
func preparationDynatraceSecretReferences(p *Preparation, name string) int {
	if p == nil || p.Monitors == nil {
		return 0
	}
	count := 0
	for _, c := range p.Monitors.DynatraceConnections {
		count += dynatraceSecretReferenceCount(c, name)
	}
	return count
}
func (s *SQLiteStore) ListDynatraceSecretConsumers(project, name string) ([]DynatraceSecretConsumer, error) {
	out := []DynatraceSecretConsumer{}
	conns, err := s.ListDynatraceConnections(project)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if n := dynatraceSecretReferenceCount(c.Snapshot(), name); n > 0 {
			out = append(out, DynatraceSecretConsumer{"dynatrace_connection", c.ID, c.Name, n})
		}
	}
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
			if n := dynatraceSecretReferenceCount(job.DynatraceSnapshot, name); n > 0 {
				out = append(out, DynatraceSecretConsumer{kind, job.ID, job.Source.Name, n})
			}
		} else {
			var p Preparation
			if err = json.Unmarshal([]byte(body), &p); err != nil {
				return nil, err
			}
			if n := preparationDynatraceSecretReferences(&p, name); n > 0 {
				out = append(out, DynatraceSecretConsumer{kind, p.ID, "Preparation for test " + p.TestID, n})
			}
		}
	}
	return out, rows.Err()
}
func (s *SQLiteStore) InvalidateDynatraceSecretPreparations(project, name string, now time.Time) ([]Preparation, error) {
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
	rows, err := tx.Query(`SELECT `+dynatraceConnectionColumns+` FROM dynatrace_connections WHERE project_id=?`, project)
	if err != nil {
		return nil, err
	}
	conns := []*DynatraceConnection{}
	for rows.Next() {
		c, e := scanDynatraceConnection(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if dynatraceSecretReferenceCount(c.Snapshot(), name) > 0 {
			conns = append(conns, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		c.Revision++
		c.VerifiedRevision = 0
		c.Capabilities = dynatrace.Capabilities{}
		native, e := encodeDynatraceNative(c)
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec(`UPDATE dynatrace_connections SET revision=?,verified_revision=0,native_config_json=?,last_checked_at=NULL,last_check_status='credential_changed',last_check_message='Connection credentials changed; verify before the next run',last_capability_health_json='{}',updated_at=? WHERE id=? AND project_id=?`, c.Revision, native, now.UTC(), c.ID, project); err != nil {
			return nil, err
		}
	}
	cancelled, err := cancelMonitorPreparationsTx(tx, project, func(p *Preparation) bool { return preparationDynatraceSecretReferences(p, name) > 0 })
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return cancelled, nil
}
func cancelMonitorPreparationsTx(tx *storeTx, project string, affected func(*Preparation) bool) ([]Preparation, error) {
	rows, err := tx.Query(`SELECT payload,owner_fence FROM preparations WHERE project_id=? AND state IN ('preparing','ready')`, project)
	if err != nil {
		return nil, err
	}
	out := []Preparation{}
	for rows.Next() {
		var body, owner string
		if err = rows.Scan(&body, &owner); err != nil {
			rows.Close()
			return nil, err
		}
		var p Preparation
		if err = json.Unmarshal([]byte(body), &p); err != nil {
			rows.Close()
			return nil, err
		}
		if affected(&p) {
			p.OwnerFence = owner
			p.State = "cancelled"
			p.Failure = "Monitoring configuration or credentials changed; prepare the test again"
			p.Revision++
			out = append(out, p)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, p := range out {
		body, e := json.Marshal(p)
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec(`UPDATE preparations SET state='cancelled',revision=?,payload=? WHERE id=? AND project_id=? AND state IN ('preparing','ready')`, p.Revision, string(body), p.ID, project); err != nil {
			return nil, err
		}
	}
	return out, nil
}

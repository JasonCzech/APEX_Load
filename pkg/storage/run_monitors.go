package storage

import (
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

// ErrMonitorRevision means a configuration changed after the caller read it.
// Callers must reload it instead of silently overwriting a concurrent edit.
var ErrMonitorRevision = errors.New("monitor configuration revision conflict")

// SaveTestWithRunMonitors atomically crosses the durable execution boundary
// and snapshots monitoring, under the same controller ownership fence.
func (s *SQLiteStore) SaveTestWithRunMonitors(test *TestRecord, leaseName, leaseOwner string, terminal bool) (bool, error) {
	if test == nil {
		return false, errors.New("test required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwner)
		if err != nil || !held {
			return false, err
		}
	}
	if _, err = lockProjectForOwnedMutation(tx, test.ProjectID); err != nil {
		return false, err
	}
	if err = s.saveTestWithExecutor(tx, test); err != nil {
		return false, err
	}
	if (test.Status == "RUNNING" || test.Status == "PAUSED") && test.StartedAt != nil {
		if _, err = startRunMonitorsTx(tx, test.ProjectID, test.ID, test.ScenarioID, *test.StartedAt, test.StartedAt.Add(7*24*time.Hour)); err != nil {
			return false, err
		}
	}
	if terminal {
		at := time.Now().UTC()
		if test.EndTime != nil {
			at = test.EndTime.UTC()
		}
		if err = finishRunMonitorLoadTx(tx, test.ProjectID, test.ID, at); err != nil {
			return false, err
		}
	}

	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const MaxRunMonitorSources = 32

// Messaging-management capacity includes immutable jobs committed before
// execution starts. Other collector packs retain their concurrent-run paging
// contract and do not consume the management scheduler's 32 assignment slots.
const MaxCollectorAssignedMonitorJobs = 32

type RunMonitorStore interface {
	SaveCollectorRunReadiness(string, string, []string, []string, time.Time) error
	SaveMonitorSource(MonitorSource, int64) (*MonitorSource, error)
	ListMonitorSources(string) ([]MonitorSource, error)
	DeleteMonitorSource(string, string, int64) error
	GetMonitorBindings(string, string) (*MonitorBindings, error)
	SaveMonitorBindings(MonitorBindings, int64) (*MonitorBindings, error)
	StartRunMonitors(string, string, string, time.Time, time.Time) ([]RunMonitorJob, error)
	ListRunMonitorJobs(string, string) ([]RunMonitorJob, error)
	PollRunMonitorJobs(string, string, time.Time) ([]RunMonitorJob, error)
	GetRunMonitorJob(string, string) (*RunMonitorJob, error)
	AcceptMonitorBatch(string, string, MonitorBatchReceipt) error
	ListMonitorBatchReceipts(string, string, string, int64, int) ([]MonitorBatchReceipt, error)
	StopRunMonitors(string, string, time.Time) error
}

// MonitorSource contains only public collection configuration. Credentials are
// resolved on the assigned collector; bearer tokens must never enter this row.
type MonitorSource struct {
	ManagementScope    *analysis.ManagementScope    `json:"management_scope,omitempty"`
	ManagementIdentity *analysis.ManagementIdentity `json:"management_identity,omitempty"`
	Prometheus         *prometheus.Selection        `json:"prometheus,omitempty"`
	ExecutorMode       string                       `json:"executor_mode,omitempty"`
	ConnectionID       string                       `json:"connection_id,omitempty"`
	AppDynamics        *appdynamics.Selection       `json:"appdynamics,omitempty"`
	CredentialRef      string                       `json:"credential_ref,omitempty"`
	CollectorPublicKey string                       `json:"collector_public_key,omitempty"`
	Service            string                       `json:"service,omitempty"`
	ID                 string                       `json:"id"`
	ProjectID          string                       `json:"project_id"`
	Name               string                       `json:"name"`
	Revision           int64                        `json:"revision"`
	CollectorID        string                       `json:"collector_id"`
	Pack               string                       `json:"pack"`
	Destination        string                       `json:"destination,omitempty"`
	IntervalSeconds    int                          `json:"interval_seconds"`
	Enabled            bool                         `json:"enabled"`
}

type MonitorBinding struct {
	SourceID string `json:"source_id"`
	Required bool   `json:"required"`
}

type MonitorBindings struct {
	ScenarioID string           `json:"scenario_id"`
	ProjectID  string           `json:"project_id"`
	Revision   int64            `json:"revision"`
	Sources    []MonitorBinding `json:"sources"`
}

// RunMonitorJob is an immutable source snapshot. State and sequence are the
// only mutable fields; changes to a source cannot redirect an existing job.
type RunMonitorJob struct {
	FlushOnly                    bool                                            `json:"flush_only,omitempty"`
	FrozenMonitorRules           []analysis.MonitorRule                          `json:"frozen_monitor_rules,omitempty"`
	RulesFrozen                  bool                                            `json:"rules_frozen,omitempty"`
	QueryTime                    *time.Time                                      `json:"query_time,omitempty"`
	PrometheusConnectionSnapshot *PrometheusConnectionSnapshot                   `json:"-"`
	Topology                     []appdynamics.Entity                            `json:"topology,omitempty"`
	ConnectionSnapshot           *AppDynamicsConnectionSnapshot                  `json:"-"`
	LoadEndedAt                  *time.Time                                      `json:"load_ended_at,omitempty"`
	DrainDeadline                *time.Time                                      `json:"drain_deadline,omitempty"`
	LastSuccessAt                *time.Time                                      `json:"last_success_at,omitempty"`
	QueryCoverage                map[string]appdynamics.Coverage                 `json:"-"`
	PrometheusDiagnosticTimes    map[string]PrometheusDiagnosticObservationTimes `json:"-"`
	QueryOffset                  int                                             `json:"query_offset,omitempty"`
	QueryFromUTC                 time.Time                                       `json:"query_from_utc,omitempty"`
	QueryUntilUTC                time.Time                                       `json:"query_until_utc,omitempty"`
	QueryWatermark               time.Time                                       `json:"query_watermark,omitempty,omitzero"`
	Coverage                     map[string]appdynamics.Coverage                 `json:"coverage,omitempty"`
	StablePasses                 int                                             `json:"stable_passes,omitempty"`
	StableSinceAt                *time.Time                                      `json:"-"`
	FinalDigest                  string                                          `json:"final_digest,omitempty"`
	ID                           string                                          `json:"id"`
	ProjectID                    string                                          `json:"project_id"`
	RunID                        string                                          `json:"run_id"`
	ScenarioID                   string                                          `json:"scenario_id,omitempty"`
	BindingRevision              int64                                           `json:"binding_revision"`
	Source                       MonitorSource                                   `json:"source"`
	Required                     bool                                            `json:"required"`
	StartedAt                    time.Time                                       `json:"started_at"`
	ExpiresAt                    time.Time                                       `json:"expires_at"`
	State                        string                                          `json:"state"`
	NextSequence                 int64                                           `json:"next_sequence"`
	CoverageReason               string                                          `json:"coverage_reason,omitempty"`
}

type MonitorBatchReceipt struct {
	EvidenceVersion         *int64    `json:"evidence_version,omitempty"`
	ManagementSnapshotCount *int64    `json:"management_snapshot_count,omitempty"`
	ManagementRowCount      *int64    `json:"management_row_count,omitempty"`
	CollectionGeneration    *int64    `json:"collection_generation,omitempty,string"`
	ManagementPollID        *string   `json:"management_poll_id,omitempty"`
	JobID                   string    `json:"job_id"`
	Sequence                int64     `json:"sequence"`
	Digest                  string    `json:"digest"`
	ArtifactKey             string    `json:"artifact_key"`
	SampleCount             int       `json:"sample_count"`
	ReceivedAt              time.Time `json:"received_at"`
}

// StartRunMonitors snapshots every binding exactly once, including an empty
// binding set. Holding the project mutation lock serializes source edits and
// deletion with this execution boundary. Required readiness fails atomically.
func (s *SQLiteStore) StartRunMonitors(project, run, scenario string, started, expires time.Time) ([]RunMonitorJob, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	jobs, err := startRunMonitorsTx(tx, project, run, scenario, started, expires)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func startRunMonitorsTx(tx *storeTx, project, run, scenario string, started, expires time.Time) ([]RunMonitorJob, error) {
	if started.IsZero() || !expires.After(started) || expires.Sub(started) > 7*24*time.Hour {
		return nil, errors.New("invalid monitor execution window")
	}
	var err error
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return nil, err
	}
	var owner string
	if err = tx.QueryRow(`SELECT project_id FROM tests WHERE id=?`, run).Scan(&owner); err != nil {
		return nil, err
	}
	if owner != project {
		return nil, errors.New("monitor run belongs to another project")
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_sessions WHERE test_id=?`, run).Scan(&n); err != nil {
		return nil, err
	}
	if n != 0 {
		rows, err := tx.Query(`SELECT payload, state, next_sequence FROM run_monitor_jobs WHERE project_id=? AND test_id=? ORDER BY id`, project, run)
		if err != nil {
			return nil, err
		}
		return readRunMonitorJobs(rows)
	}
	b := MonitorBindings{}
	prepared, err := consumedMonitorSnapshotTx(tx, project, run)
	if err != nil {
		return nil, err
	}
	if prepared != nil && prepared.ScenarioID != scenario {
		return nil, ErrPreparationConflict
	}
	if prepared != nil {
		b = prepared.Bindings
	}
	if prepared == nil {
		resolved, resolveErr := resolveRunMonitorBindingsTx(tx, project, run, scenario)
		if resolveErr != nil {
			return nil, resolveErr
		}
		b = *resolved
	}
	jobs := make([]RunMonitorJob, 0, len(b.Sources))
	var frozenRules []analysis.MonitorRule
	if prepared != nil && prepared.RulesFrozen {
		frozenRules = prepared.MonitorRules
	} else {
		var goalsRaw string
		if err = tx.QueryRow(`SELECT COALESCE(goal_config_json,'') FROM tests WHERE id=? AND project_id=?`, run, project).Scan(&goalsRaw); err != nil {
			return nil, err
		}
		if goalsRaw != "" {
			var goals struct {
				MonitorRules []analysis.MonitorRule `json:"monitor_rules"`
			}
			if err = json.Unmarshal([]byte(goalsRaw), &goals); err != nil {
				return nil, err
			}
			frozenRules = goals.MonitorRules
		}
	}
	for _, binding := range b.Sources {
		job := RunMonitorJob{ID: run + ":" + binding.SourceID, ProjectID: project, RunID: run, ScenarioID: scenario, BindingRevision: b.Revision, Required: binding.Required, StartedAt: started.UTC(), ExpiresAt: expires.UTC(), State: "collecting", Source: MonitorSource{ID: binding.SourceID, ProjectID: project}}
		job.RulesFrozen = true
		for _, rule := range frozenRules {
			if rule.SourceID == binding.SourceID {
				job.FrozenMonitorRules = append(job.FrozenMonitorRules, rule)
			}
		}
		var body string
		if prepared != nil {
			source, ok := prepared.Sources[binding.SourceID]
			if !ok {
				err = sql.ErrNoRows
			} else {
				encoded, encodeErr := json.Marshal(source)
				body = string(encoded)
				err = encodeErr
			}
		} else {
			err = tx.QueryRow(`SELECT payload FROM monitor_sources WHERE id=? AND project_id=?`, binding.SourceID, project).Scan(&body)
		}
		if errors.Is(err, sql.ErrNoRows) {
			job.CoverageReason = "source_deleted"
		} else if err != nil {
			return nil, err
		} else if err = json.Unmarshal([]byte(body), &job.Source); err != nil {
			return nil, err
		}
		if job.CoverageReason == "" && !job.Source.Enabled {
			job.CoverageReason = "source_disabled"
		}
		if job.CoverageReason == "" && job.Source.Pack == "appdynamics" {
			if prepared != nil {
				job.ConnectionSnapshot = prepared.Connections[binding.SourceID]
				if job.ConnectionSnapshot == nil {
					job.CoverageReason = "connection_not_ready"
				}
			}
			if job.CoverageReason == "" && job.ConnectionSnapshot == nil {
				var connBody string
				err = tx.QueryRow(`SELECT payload_json FROM appdynamics_connections WHERE id=? AND project_id=?`, job.Source.ConnectionID, project).Scan(&connBody)
				if errors.Is(err, sql.ErrNoRows) {
					job.CoverageReason = "connection_deleted"
				} else if err != nil {
					return nil, err
				} else {
					conn, e := UnmarshalAppDynamicsConnectionPrivate([]byte(connBody))
					if e != nil {
						return nil, e
					}
					if conn.ConfigurationStatus() != "ready" {
						job.CoverageReason = "connection_not_ready"
					} else {
						job.ConnectionSnapshot = conn.Snapshot()
					}
				}
			}
			if job.CoverageReason == "" && ValidateAppDynamicsSourceConnection(job.Source, job.ConnectionSnapshot) != nil {
				job.CoverageReason = "connection_source_mismatch"
			}
		}
		if job.CoverageReason == "" && job.Source.Pack == "prometheus-query" {
			if prepared != nil {
				job.PrometheusConnectionSnapshot = prepared.PrometheusConnections[binding.SourceID]
				if job.PrometheusConnectionSnapshot == nil {
					job.CoverageReason = "connection_not_ready"
				}
			}
			if job.CoverageReason == "" && job.PrometheusConnectionSnapshot == nil {
				var connBody string
				err = tx.QueryRow(`SELECT payload_json FROM prometheus_connections WHERE id=? AND project_id=?`, job.Source.ConnectionID, project).Scan(&connBody)
				if errors.Is(err, sql.ErrNoRows) {
					job.CoverageReason = "connection_deleted"
				} else if err != nil {
					return nil, err
				} else {
					conn, e := UnmarshalPrometheusConnectionPrivate([]byte(connBody))
					if e != nil {
						return nil, e
					}
					if conn.ConfigurationStatus() != "ready" {
						job.CoverageReason = "connection_not_ready"
					} else {
						job.PrometheusConnectionSnapshot = conn.Snapshot()
					}
				}
			}
			if job.CoverageReason == "" && ValidatePrometheusSourceConnection(job.Source, job.PrometheusConnectionSnapshot) != nil {
				job.CoverageReason = "connection_source_mismatch"
			}
		}
		if job.CoverageReason == "" && job.Source.ExecutorMode != "direct" {
			var health, packsJSON string
			var seen sql.NullTime
			err = tx.QueryRow(`SELECT health, last_seen_at, COALESCE(allowed_metric_packs_json, '[]') FROM monitoring_collectors WHERE id=? AND project_id=?`, job.Source.CollectorID, project).Scan(&health, &seen, &packsJSON)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			var packs []string
			if err == nil {
				if err = json.Unmarshal([]byte(packsJSON), &packs); err != nil {
					return nil, err
				}
			}
			if !seen.Valid || seen.Time.Before(started.Add(-90*time.Second)) || seen.Time.After(started.Add(30*time.Second)) || health != "healthy" || !slices.Contains(packs, job.Source.Pack) {
				job.CoverageReason = "collector_not_ready"
			}
		}
		if job.CoverageReason == "" && job.Source.ExecutorMode != "direct" {
			var seen time.Time
			var packsJSON, destinationsJSON string
			err = tx.QueryRow(`SELECT seen_at,packs,destinations FROM run_monitor_collector_readiness WHERE collector_id=? AND project_id=?`, job.Source.CollectorID, project).Scan(&seen, &packsJSON, &destinationsJSON)
			if errors.Is(err, sql.ErrNoRows) {
				job.CoverageReason = "collector_run_jobs_unavailable"
			} else if err != nil {
				return nil, err
			} else {
				var packs, destinations []string
				if err = json.Unmarshal([]byte(packsJSON), &packs); err != nil {
					return nil, err
				}
				if err = json.Unmarshal([]byte(destinationsJSON), &destinations); err != nil {
					return nil, err
				}
				if started.Sub(seen) > 90*time.Second || seen.After(started.Add(30*time.Second)) || !slices.Contains(packs, job.Source.Pack) || job.Source.Destination != "" && !slices.Contains(destinations, job.Source.ReadinessDestination()) {
					job.CoverageReason = "collector_run_configuration_unavailable"
				}
				if job.Source.Pack == "appdynamics" && !slices.Contains(packs, "appdynamics.v1") {
					job.CoverageReason = "collector_appdynamics_contract_unavailable"
				}
				if job.Source.Pack == "prometheus-query" && !slices.Contains(packs, "prometheus-query.v1") {
					job.CoverageReason = "collector_prometheus_contract_unavailable"
				}
			}
		}
		if job.CoverageReason == "" && job.Source.ManagementScope != nil {
			if err = managementReadinessTx(tx, job.Source, "", started); err != nil {
				job.CoverageReason = "collector_management_configuration_unavailable"
			}
		}
		if job.CoverageReason != "" {
			if job.Required {
				return nil, fmt.Errorf("required monitor %s: %s", binding.SourceID, job.CoverageReason)
			}
			job.State = "incomplete"
		}
		jobs = append(jobs, job)
	}
	// Required management sources take the finite management assignment capacity
	// before optional sources, independent of authoring order. The project fence
	// serializes concurrent starts and includes jobs committed by prelaunch work.
	// Admission reserves slots immediately, including before a future
	// execution barrier. Historical recovery remains conservative as well.
	assignmentTime := started.UTC()
	if now := time.Now().UTC(); now.Before(assignmentTime) {
		assignmentTime = now
	}
	assigned := map[string]int{}
	for _, required := range []bool{true, false} {
		for i := range jobs {
			job := &jobs[i]
			if job.Required != required || job.State != "collecting" || job.Source.ManagementScope == nil {
				continue
			}
			count, known := assigned[job.Source.CollectorID]
			if !known {
				if err = tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id WHERE j.project_id=? AND j.collector_id=? AND j.management_config_key IS NOT NULL AND j.state='collecting' AND j.expires_at>? AND t.status NOT IN ('COMPLETE','FAILED','ABORTED')`, project, job.Source.CollectorID, assignmentTime).Scan(&count); err != nil {
					return nil, err
				}
			}
			if count >= MaxCollectorAssignedMonitorJobs {
				if required {
					return nil, fmt.Errorf("required monitor %s: collector_capacity", job.Source.ID)
				}
				job.State, job.CoverageReason = "incomplete", "collector_capacity"
			} else {
				count++
			}
			assigned[job.Source.CollectorID] = count
		}
	}
	if _, err = tx.Exec(`INSERT INTO run_monitor_sessions (test_id, project_id, started_at, binding_revision) VALUES (?, ?, ?, ?)`, run, project, started.UTC(), b.Revision); err != nil {
		return nil, err
	}
	for _, job := range jobs {
		body, err := json.Marshal(job)
		if err != nil {
			return nil, err
		}
		configuration := ""
		if job.Source.ManagementScope != nil {
			scope, identity, configErr := managementSourceHashes(job.Source)
			if configErr != nil {
				return nil, configErr
			}
			configuration = managementConfigurationKey(job.Source, scope, identity)
		}
		if _, err = tx.Exec(`INSERT INTO run_monitor_jobs (id, project_id, test_id, collector_id, state, next_sequence, expires_at, payload, source_id, management_config_key) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`, job.ID, project, run, job.Source.CollectorID, job.State, job.ExpiresAt, string(body), job.Source.ID, nullableString(configuration)); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

func readRunMonitorJobs(rows *sql.Rows) ([]RunMonitorJob, error) {
	defer rows.Close()
	jobs := []RunMonitorJob{}
	for rows.Next() {
		var body, state string
		var seq int64
		if err := rows.Scan(&body, &state, &seq); err != nil {
			return nil, err
		}
		var job RunMonitorJob
		if err := json.Unmarshal([]byte(body), &job); err != nil {
			return nil, err
		}
		job.State, job.NextSequence = state, seq
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) ListRunMonitorJobs(project, run string) ([]RunMonitorJob, error) {
	rows, err := s.db.Query(`SELECT payload, state, next_sequence FROM run_monitor_jobs WHERE project_id=? AND test_id=? ORDER BY id`, project, run)
	if err != nil {
		return nil, err
	}
	return readRunMonitorJobs(rows)
}

func (s *SQLiteStore) PollRunMonitorJobs(project, collector string, now time.Time) ([]RunMonitorJob, error) {
	return s.pollLegacyRunMonitorJobs(project, collector, now)
}

func (s *SQLiteStore) GetRunMonitorJob(project, id string) (*RunMonitorJob, error) {
	var body, state string
	var seq int64
	if err := s.db.QueryRow(`SELECT payload, state, next_sequence FROM run_monitor_jobs WHERE id=? AND project_id=?`, id, project).Scan(&body, &state, &seq); err != nil {
		return nil, err
	}
	var job RunMonitorJob
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		return nil, err
	}
	job.State, job.NextSequence = state, seq
	return &job, nil
}

// AcceptMonitorBatch must only be called after the immutable artifact is
// durable. The journal and sequence advance in one transaction. Replayed
// identical batches are acknowledged even after collection stops; conflicts
// or gaps are rejected without advancing the collector cursor.
func (s *SQLiteStore) AcceptMonitorBatch(project, collector string, b MonitorBatchReceipt) error {
	if b.EvidenceVersion != nil || b.ManagementSnapshotCount != nil || b.ManagementRowCount != nil || b.CollectionGeneration != nil || b.ManagementPollID != nil {
		return errors.New("management receipts require cohort publication")
	}
	return s.acceptMonitorBatch(project, collector, b, nil)
}
func (s *SQLiteStore) acceptMonitorBatch(project, collector string, b MonitorBatchReceipt, points []analysis.MetricPoint) error {
	digest, err := hex.DecodeString(b.Digest)
	if err != nil || len(digest) != 32 || b.Sequence < 0 || b.Sequence >= 1<<62 || b.SampleCount < 1 || b.SampleCount > 512 || b.ReceivedAt.IsZero() || len(b.ArtifactKey) == 0 || len(b.ArtifactKey) > 2048 {
		return errors.New("invalid monitor batch receipt")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	var state, runStatus, jobPayload string
	var next int64
	var expires time.Time
	if err = tx.QueryRow(`SELECT j.state, j.next_sequence, j.expires_at,t.status,j.payload FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id WHERE j.id=? AND j.project_id=? AND j.collector_id=?`, b.JobID, project, collector).Scan(&state, &next, &expires, &runStatus, &jobPayload); err != nil {
		return err
	}
	var job RunMonitorJob
	if err = json.Unmarshal([]byte(jobPayload), &job); err != nil {
		return err
	}
	if job.Source.ManagementScope != nil {
		return errors.New("management jobs require cohort publication")
	}
	var priorDigest, priorKey string
	var priorCount int
	err = tx.QueryRow(`SELECT digest, artifact_key, sample_count FROM run_monitor_batches WHERE job_id=? AND sequence=?`, b.JobID, b.Sequence).Scan(&priorDigest, &priorKey, &priorCount)
	if err == nil {
		if priorDigest != b.Digest || priorKey != b.ArtifactKey || priorCount != b.SampleCount {
			return errors.New("conflicting monitor batch retry")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	flushing := job.Source.Pack == "prometheus" && job.FlushOnly && state == "flushing"
	if (state != "collecting" && !flushing) || (!flushing && runStatus != "RUNNING" && runStatus != "PAUSED") || !expires.After(b.ReceivedAt) || next != b.Sequence {
		return errors.New("monitor job stopped, expired, or sequence out of order")
	}
	if _, err = tx.Exec(`INSERT INTO run_monitor_batches (job_id, sequence, digest, artifact_key, sample_count, received_at) VALUES (?, ?, ?, ?, ?, ?)`, b.JobID, b.Sequence, b.Digest, b.ArtifactKey, b.SampleCount, b.ReceivedAt.UTC()); err != nil {
		return err
	}
	if points != nil {
		if job.Source.Pack != "prometheus" || len(points) != b.SampleCount {
			return errors.New("legacy scrape payload does not match receipt")
		}
		if err = recordPrometheusScrapeSlotTx(tx, job, b, points); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE run_monitor_jobs SET next_sequence=next_sequence+1 WHERE id=?`, b.JobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListMonitorBatchReceipts(project, run string, afterJob string, afterSequence int64, limit int) ([]MonitorBatchReceipt, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("monitor receipt page must contain 1-1000 records")
	}
	rows, err := s.db.Query(`SELECT b.job_id, b.sequence, b.digest, b.artifact_key, b.sample_count, b.received_at, b.evidence_version,b.management_snapshot_count,b.management_row_count,b.collection_generation,b.management_poll_id FROM run_monitor_batches b JOIN run_monitor_jobs j ON j.id=b.job_id WHERE j.project_id=? AND j.test_id=? AND (b.job_id>? OR (b.job_id=? AND b.sequence>?)) ORDER BY b.job_id, b.sequence LIMIT ?`, project, run, afterJob, afterJob, afterSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MonitorBatchReceipt{}
	for rows.Next() {
		var b MonitorBatchReceipt
		if err := rows.Scan(&b.JobID, &b.Sequence, &b.Digest, &b.ArtifactKey, &b.SampleCount, &b.ReceivedAt, &b.EvidenceVersion, &b.ManagementSnapshotCount, &b.ManagementRowCount, &b.CollectionGeneration, &b.ManagementPollID); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) StopRunMonitors(project, run string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE run_monitor_sessions SET stopped_at=COALESCE(stopped_at, ?) WHERE test_id=? AND project_id=?`, at.UTC(), run, project); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE run_monitor_jobs SET state=CASE WHEN next_sequence=0 THEN 'incomplete' ELSE 'stopped' END WHERE test_id=? AND project_id=? AND state='collecting'`, run, project); err != nil {
		return err
	}
	return tx.Commit()
}

func (m MonitorSource) Validate() error {
	if m.ManagementScope != nil || m.ManagementIdentity != nil {
		if m.Prometheus != nil || m.AppDynamics != nil || m.ExecutorMode != "" || m.ConnectionID != "" {
			return errors.New("management source cannot include provider query configuration")
		}
		return validateManagementMonitorSource(m)
	}
	for _, v := range []string{m.ID, m.ProjectID, m.Name} {
		if strings.TrimSpace(v) == "" || len(v) > 256 {
			return errors.New("monitor source identity must contain 1-256 bytes")
		}
	}
	if m.ExecutorMode != "" && m.ExecutorMode != "collector" && m.ExecutorMode != "direct" {
		return errors.New("unsupported monitor executor mode")
	}
	if m.ExecutorMode != "direct" && strings.TrimSpace(m.CollectorID) == "" {
		return errors.New("collector ID is required")
	}
	if m.Pack == "appdynamics" {
		if m.Prometheus != nil {
			return errors.New("Prometheus selection requires its own query pack")
		}
		if m.ConnectionID == "" || m.AppDynamics == nil || m.AppDynamics.ProjectID != m.ProjectID || m.AppDynamics.Validate() != nil || m.IntervalSeconds < 60 || m.IntervalSeconds > 3600 || m.CredentialRef != "" {
			return errors.New("invalid AppDynamics monitor source")
		}
		if len(m.AppDynamics.Metrics) == 0 && len(m.AppDynamics.Analytics) == 0 && !m.AppDynamics.Events && !m.AppDynamics.Violations && !m.AppDynamics.Snapshots {
			return errors.New("AppDynamics monitor source must select a collection capability")
		}
		return nil
	}
	if m.Pack == "prometheus-query" {
		if m.ConnectionID == "" || m.Prometheus == nil || m.Prometheus.ProjectID != m.ProjectID || m.Prometheus.Validate() != nil || m.IntervalSeconds < 15 || m.IntervalSeconds > 3600 || m.CredentialRef != "" || m.AppDynamics != nil {
			return errors.New("invalid Prometheus query source")
		}
		return nil
	}
	if m.ExecutorMode == "direct" || m.ConnectionID != "" || m.AppDynamics != nil || m.Prometheus != nil {
		return errors.New("provider fields require a governed provider pack")
	}
	if m.IntervalSeconds < 1 || m.IntervalSeconds > 3600 {
		return errors.New("monitor interval must be 1-3600 seconds")
	}
	if len(m.Service) > 128 || len(m.CredentialRef) > 128 {
		return errors.New("monitor identity exceeds limit")
	}
	if m.Pack != "ssh-linux" && m.Pack != "jmx" && m.Pack != "jdbc" && (m.CredentialRef != "" || m.CollectorPublicKey != "") {
		return errors.New("this monitor pack does not accept sealed credentials")
	}
	if m.CredentialRef != "" {
		key, err := base64.StdEncoding.DecodeString(m.CollectorPublicKey)
		if err != nil {
			return errors.New("invalid collector encryption key")
		}
		if _, err = ecdh.X25519().NewPublicKey(key); err != nil {
			return errors.New("invalid collector encryption key")
		}
	}
	switch m.Pack {
	case "linux-hosts", "go-runtime":
		if m.Destination != "" {
			return errors.New("local host packs do not accept destinations")
		}
	case "ssh-linux", "jmx", "jdbc":
		if m.CredentialRef == "" {
			return errors.New("remote monitor requires project secret reference and enrolled collector encryption key")
		}
		return validateRemoteMonitorDestination(m.Pack, m.Destination)
	case "cloudwatch", "azure-monitor":
		if m.IntervalSeconds < 60 {
			return errors.New("cloud monitor interval must be at least 60 seconds")
		}
		return validateRemoteMonitorDestination(m.Pack, m.Destination)
	case "otel-metrics":
		return validateRemoteMonitorDestination(m.Pack, m.Destination)
	case "prometheus":
		u, err := url.Parse(m.Destination)
		if err != nil || len(m.Destination) > 2048 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return errors.New("Prometheus destination must be an absolute HTTP(S) URL without credentials, query, or fragment")
		}
	default:
		return fmt.Errorf("unsupported run monitor pack %q", m.Pack)
	}
	return nil
}

// SaveMonitorSource uses revision zero for creation and CAS for updates. The
// collector's project and approved packs are rechecked inside the transaction.
func (s *SQLiteStore) SaveMonitorSource(m MonitorSource, expected int64) (*MonitorSource, error) {
	if m.Pack == "prometheus-query" && m.IntervalSeconds == 0 {
		m.IntervalSeconds = 30
	}
	if m.Pack == "appdynamics" && m.IntervalSeconds == 0 {
		m.IntervalSeconds = 60
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if expected < 0 || expected >= 1<<62 {
		return nil, ErrMonitorRevision
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, m.ProjectID); err != nil {
		return nil, err
	}
	if m.Pack == "appdynamics" {
		if err = validateAppDynamicsSourceTx(tx, &m); err != nil {
			return nil, err
		}
	}
	if m.Pack == "prometheus-query" {
		if err = validatePrometheusSourceTx(tx, &m); err != nil {
			return nil, err
		}
	}
	var packsJSON string
	if m.ExecutorMode != "direct" {
		if err = tx.QueryRow(`SELECT COALESCE(allowed_metric_packs_json, '[]') FROM monitoring_collectors WHERE id=? AND project_id=?`, m.CollectorID, m.ProjectID).Scan(&packsJSON); err != nil {
			return nil, fmt.Errorf("collector unavailable in project: %w", err)
		}
		var packs []string
		if err = json.Unmarshal([]byte(packsJSON), &packs); err != nil {
			return nil, err
		}
		if !slices.Contains(packs, m.Pack) {
			return nil, errors.New("collector is not approved for the requested pack")
		}
	}
	m.Revision = expected + 1
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if m.ManagementScope != nil && len(payload) > 16<<10 {
		return nil, errors.New("management source exceeds 16 KiB")
	}

	var result sql.Result
	if expected == 0 {
		var count int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM monitor_sources WHERE project_id=?`, m.ProjectID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 256 {
			return nil, errors.New("project monitor source limit reached")
		}
		result, err = tx.Exec(`INSERT INTO monitor_sources (id, project_id, revision, payload) VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, m.ID, m.ProjectID, m.Revision, string(payload))
	} else {
		result, err = tx.Exec(`UPDATE monitor_sources SET revision=?, payload=? WHERE id=? AND project_id=? AND revision=?`, m.Revision, string(payload), m.ID, m.ProjectID, expected)
	}
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrMonitorRevision
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *SQLiteStore) ListMonitorSources(project string) ([]MonitorSource, error) {
	rows, err := s.db.Query(`SELECT payload FROM monitor_sources WHERE project_id=? ORDER BY id LIMIT 256`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MonitorSource{}
	for rows.Next() {
		var body string
		var m MonitorSource
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteMonitorSource(project, id string, expected int64) error {
	if expected <= 0 {
		return ErrMonitorRevision
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM monitor_sources WHERE id=? AND project_id=? AND revision=?`, id, project, expected)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrMonitorRevision
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetMonitorBindings(project, scenario string) (*MonitorBindings, error) {
	var body string
	err := s.db.QueryRow(`SELECT payload FROM monitor_bindings WHERE project_id=? AND scenario_id=?`, project, scenario).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return &MonitorBindings{ProjectID: project, ScenarioID: scenario, Sources: []MonitorBinding{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var b MonitorBindings
	err = json.Unmarshal([]byte(body), &b)
	return &b, err
}

func (s *SQLiteStore) SaveMonitorBindings(b MonitorBindings, expected int64) (*MonitorBindings, error) {
	if expected < 0 || expected >= 1<<62 {
		return nil, ErrMonitorRevision
	}
	if len(b.Sources) > MaxRunMonitorSources {
		return nil, errors.New("at most 32 monitor sources may be bound")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, b.ProjectID); err != nil {
		return nil, err
	}
	if err = validateScenarioProjectPlacement(tx, b.ScenarioID, b.ProjectID, "monitor binding"); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, binding := range b.Sources {
		if binding.SourceID == "" || seen[binding.SourceID] {
			return nil, errors.New("monitor bindings must have unique source IDs")
		}
		seen[binding.SourceID] = true
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM monitor_sources WHERE id=? AND project_id=?`, binding.SourceID, b.ProjectID).Scan(&n); err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, errors.New("monitor source unavailable in project")
		}
	}
	b.Revision = expected + 1
	if b.Sources == nil {
		b.Sources = []MonitorBinding{}
	}
	body, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var result sql.Result
	if expected == 0 {
		result, err = tx.Exec(`INSERT INTO monitor_bindings (scenario_id, project_id, revision, payload) VALUES (?, ?, ?, ?) ON CONFLICT(scenario_id) DO NOTHING`, b.ScenarioID, b.ProjectID, b.Revision, string(body))
	} else {
		result, err = tx.Exec(`UPDATE monitor_bindings SET revision=?, payload=? WHERE scenario_id=? AND project_id=? AND revision=?`, b.Revision, string(body), b.ScenarioID, b.ProjectID, expected)
	}
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrMonitorRevision
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &b, nil
}

func validateRemoteMonitorDestination(pack, destination string) error {
	u, err := url.Parse(destination)
	if err != nil || len(destination) > 2048 || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" {
		return errors.New("invalid monitor destination")
	}
	expected := map[string]string{"ssh-linux": "ssh", "jmx": "jmx", "jdbc": "postgresql", "otel-metrics": "otlp", "cloudwatch": "cloudwatch", "azure-monitor": "azure-monitor"}[pack]
	if u.Scheme != expected || pack != "jdbc" && u.Path != "" {
		return errors.New("invalid monitor destination scheme or path")
	}
	if (pack == "cloudwatch" || pack == "azure-monitor") && u.Port() != "" {
		return errors.New("cloud destination must be an alias without a port")
	}

	if pack == "jmx" && u.Port() == "" {
		return errors.New("JMX requires a fixed TLS registry/server port")
	}
	return nil
}

func (m MonitorSource) ReadinessDestination() string {
	if m.CredentialRef != "" || m.ManagementIdentity != nil && m.ManagementIdentity.CredentialRef != "" || m.CanonicalProvider() && m.ExecutorMode == "collector" && m.CollectorPublicKey != "" {
		return m.Destination + "#" + m.CollectorPublicKey
	}
	return m.Destination
}

// CanonicalProvider identifies jobs whose query progress and revisions are
// durably reconciled; legacy point-only packs retain their wire contract.
func (m MonitorSource) CanonicalProvider() bool {
	return m.Pack == "appdynamics" || m.Pack == "prometheus-query"
}

package storage

import (
	"crypto/ecdh"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
)

// ResolveRunMonitorBindings preserves the distinction between inheritance and
// an explicit empty override for ad-hoc and scenario-managed runs.
func (s *SQLiteStore) ResolveRunMonitorBindings(project, run, scenario string) (*MonitorBindings, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return resolveRunMonitorBindingsTx(tx, project, run, scenario)
}
func resolveRunMonitorBindingsTx(tx *storeTx, project, run, scenario string) (*MonitorBindings, error) {
	var raw string
	if err := tx.QueryRow(`SELECT COALESCE(monitor_bindings_json,'') FROM tests WHERE id=? AND project_id=?`, run, project).Scan(&raw); err != nil {
		return nil, err
	}
	b := &MonitorBindings{ProjectID: project, ScenarioID: scenario, Sources: []MonitorBinding{}}
	if raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), b); err != nil {
			return nil, err
		}
		b.ProjectID = project
		b.ScenarioID = scenario
		return b, nil
	}
	if scenario == "" {
		return b, nil
	}
	if err := validateScenarioProjectPlacement(tx, scenario, project, "run monitors"); err != nil {
		return nil, err
	}
	err := tx.QueryRow(`SELECT payload FROM monitor_bindings WHERE scenario_id=? AND project_id=?`, scenario, project).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), b); err != nil {
		return nil, err
	}
	return b, nil
}
func validateAppDynamicsSourceTx(tx *storeTx, m *MonitorSource) error {
	var raw string
	if err := tx.QueryRow(`SELECT payload_json FROM appdynamics_connections WHERE id=? AND project_id=?`, m.ConnectionID, m.ProjectID).Scan(&raw); err != nil {
		return fmt.Errorf("AppDynamics connection unavailable in project: %w", err)
	}
	c, err := UnmarshalAppDynamicsConnectionPrivate([]byte(raw))
	if err != nil {
		return err
	}
	if m.ExecutorMode == "" {
		m.ExecutorMode = c.CollectionMode
	}
	if m.ExecutorMode != c.CollectionMode || (m.ExecutorMode == "collector" && m.CollectorID != c.CollectorAgentID) || (m.ExecutorMode == "direct" && m.CollectorID != "") {
		return errors.New("monitor execution location must match its connection")
	}
	m.Destination = c.Config.Endpoint
	if m.ExecutorMode == "collector" {
		var destinations string
		if err = tx.QueryRow(`SELECT destinations FROM run_monitor_collector_readiness WHERE collector_id=? AND project_id=?`, m.CollectorID, m.ProjectID).Scan(&destinations); err != nil {
			return errors.New("collector must advertise AppDynamics destination and encryption key before source creation")
		}
		var list []string
		if err = json.Unmarshal([]byte(destinations), &list); err != nil {
			return err
		}
		enrolled := false
		for _, d := range list {
			origin, key, ok := strings.Cut(d, "#")
			if ok && origin == m.Destination {
				if m.CollectorPublicKey != "" && m.CollectorPublicKey != key {
					return errors.New("collector key differs from enrolled destination")
				}
				m.CollectorPublicKey = key
				enrolled = true
				break
			}
		}
		if !enrolled {
			return errors.New("collector encryption key unavailable")
		}
	}
	return ValidateAppDynamicsSourceConnection(*m, c.Snapshot())
}

// ValidateAppDynamicsSourceConnection binds reusable sources to the exact
// connection execution location. Profile edits affect future runs only after
// their source is updated; immutable prepared and active snapshots stay valid.
func ValidateAppDynamicsSourceConnection(source MonitorSource, connection *AppDynamicsConnectionSnapshot) error {
	mode := source.ExecutorMode
	if mode == "" {
		mode = "collector"
	}
	if source.Pack != "appdynamics" || connection == nil || source.ProjectID != connection.ProjectID || source.ConnectionID != connection.ConnectionID || mode != connection.CollectionMode || source.Destination != connection.Config.Endpoint {
		return errors.New("AppDynamics source must match its connection execution location and destination")
	}
	if mode == "direct" {
		if source.CollectorID != "" || source.CollectorPublicKey != "" {
			return errors.New("direct AppDynamics source cannot retain a collector assignment or encryption key")
		}
		return nil
	}
	if mode != "collector" || source.CollectorID == "" || source.CollectorID != connection.CollectorAgentID {
		return errors.New("AppDynamics source must use its connection assigned collector")
	}
	key, err := base64.StdEncoding.DecodeString(source.CollectorPublicKey)
	if err != nil {
		return errors.New("AppDynamics collector encryption identity unavailable")
	}
	if _, err = ecdh.X25519().NewPublicKey(key); err != nil {
		return errors.New("AppDynamics collector encryption identity unavailable")
	}
	return nil
}
func finishRunMonitorLoadTx(tx *storeTx, project, run string, at time.Time) error {
	rows, err := tx.Query(`SELECT payload,state,next_sequence FROM run_monitor_jobs WHERE test_id=? AND project_id=? AND state='collecting'`, run, project)
	if err != nil {
		return err
	}
	jobs, err := readRunMonitorJobs(rows)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Source.Pack == "appdynamics" || j.Source.Pack == "elastic" {
			end := at.UTC()
			deadline := end.Add(5 * time.Minute)
			if j.Source.Pack == "elastic" {
				if j.ElasticContinuation != nil {
					if len(j.ElasticRetiredContinuations) >= 32 {
						return errors.New("Elastic retired continuation cleanup backlog reached")
					}
					cursor := *j.ElasticContinuation
					j.ElasticRetiredContinuations = append(j.ElasticRetiredContinuations, &cursor)
				}
				j.ElasticContinuation = nil
				j.ElasticPendingIngestionWatermark = time.Time{}
				j.ElasticResolvedIndices = nil
			}
			j.LoadEndedAt = &end
			j.DrainDeadline = &deadline
			j.ExpiresAt = deadline
			j.State = "reconciling"
			j.QueryOffset = 0
			j.QueryFromUTC = time.Time{}
			j.QueryUntilUTC = time.Time{}
			j.QueryCoverage = nil
		} else {
			j.State = "stopped"
			if j.NextSequence == 0 {
				j.State = "incomplete"
			}
		}
		raw, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE run_monitor_jobs SET state=?,expires_at=?,payload=? WHERE id=?`, j.State, j.ExpiresAt, string(raw), j.ID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE run_monitor_sessions SET stopped_at=COALESCE(stopped_at,?) WHERE test_id=? AND project_id=?`, at.UTC(), run, project)
	return err
}

// ObservationHead points into a verified immutable receipt artifact. The head
// can change for delayed buckets; archive publication freezes the job first.
type ObservationHead struct {
	JobID    string
	Key      string
	Sequence int64
	Index    int
	Kind     string
}
type ProviderMonitorBatch struct {
	ResolvedIndices []string                        `json:"resolved_indices,omitempty"`
	Topology        []appdynamics.Entity            `json:"topology,omitempty"`
	Points          []analysis.MetricPoint          `json:"points"`
	Evidence        []appdynamics.Diagnostic        `json:"evidence,omitempty"`
	Coverage        map[string]appdynamics.Coverage `json:"coverage,omitempty"`
	QueryWindowEnd  *time.Time                      `json:"query_window_end,omitempty"`
	Progress        *appdynamics.QueryProgress      `json:"progress,omitempty"`
	QueryFromUTC    time.Time                       `json:"query_from_utc,omitempty"`
	QueryUntilUTC   time.Time                       `json:"query_until_utc,omitempty"`
	Final           bool                            `json:"final,omitempty"`
}

func ObservationKey(p analysis.MetricPoint) string {
	if p.Provider == nil {
		return ""
	}
	raw, _ := json.Marshal([]any{p.Source, p.MetricName, p.Unit, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Provider.BucketStart, p.Provider.ResolutionSeconds, p.Provider.Statistic})
	if p.Provider.Name != "appdynamics" {
		raw, _ = json.Marshal([]any{p.Source, p.MetricName, p.Unit, p.Provider.Name, p.Provider.QueryID, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Provider.BucketStart, p.Provider.BucketEnd, p.Provider.WindowKind, p.Provider.ResolutionSeconds, p.Provider.Statistic})
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func DiagnosticKeyForJob(job string, d appdynamics.Diagnostic) string {
	if d.Provider == "" || d.Provider == "appdynamics" {
		raw, _ := json.Marshal([]string{job, d.Kind, d.ApplicationID, d.ID})
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	raw, _ := json.Marshal([]string{job, d.Provider, d.QueryID, d.Kind, d.ApplicationID, d.Index, d.DocumentID, d.ID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *SQLiteStore) ListProviderObservationHeads(project, run, source, kind, after string, limit int) ([]ObservationHead, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid head page size")
	}
	rows, err := s.db.Query(`SELECT h.job_id,h.record_key,h.sequence,h.record_index,h.record_kind FROM run_monitor_observation_heads h JOIN run_monitor_jobs j ON j.id=h.job_id WHERE j.project_id=? AND j.test_id=? AND (?='' OR j.id=?) AND h.record_kind=? AND h.record_key>? ORDER BY h.record_key,h.job_id LIMIT ?`, project, run, source, run+":"+source, kind, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	heads := []ObservationHead{}
	for rows.Next() {
		var h ObservationHead
		if err := rows.Scan(&h.JobID, &h.Key, &h.Sequence, &h.Index, &h.Kind); err != nil {
			return nil, err
		}
		heads = append(heads, h)
	}
	return heads, rows.Err()
}
func (s *SQLiteStore) PollDirectRunMonitorJobs(now time.Time) ([]RunMonitorJob, error) {
	rows, err := s.db.Query(`SELECT payload,state,next_sequence FROM run_monitor_jobs WHERE collector_id='' AND state IN ('collecting','reconciling') ORDER BY next_sequence,id LIMIT 256`)
	if err != nil {
		return nil, err
	}
	return readRunMonitorJobs(rows)
}
func (s *SQLiteStore) AcceptProviderMonitorBatch(project, collector string, b MonitorBatchReceipt, payload ProviderMonitorBatch, leaseName, leaseOwner string, leaseGeneration uint64) error {
	if b.Sequence < 0 || len(b.Digest) != 64 || len(payload.Points) > 512 || len(payload.Evidence) > 100 || b.SampleCount != len(payload.Points) {
		return errors.New("invalid provider receipt")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, leaseGeneration)
		if err != nil {
			return err
		}
		if tx == nil {
			return errors.New("monitor execution owner fence rejected")
		}
	} else {
		tx, err = s.db.Begin()
		if err != nil {
			return err
		}
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	var raw, state, status string
	var next int64
	var expires time.Time
	if err = tx.QueryRow(`SELECT j.payload,j.state,j.next_sequence,j.expires_at,t.status FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id WHERE j.id=? AND j.project_id=? AND j.collector_id=?`, b.JobID, project, collector).Scan(&raw, &state, &next, &expires, &status); err != nil {
		return err
	}
	var job RunMonitorJob
	if err = json.Unmarshal([]byte(raw), &job); err != nil {
		return err
	}
	if job.Source.Pack != "appdynamics" && job.Source.Pack != "elastic" {
		return errors.New("provider payload requires a registered provider job")
	}
	var prior string
	err = tx.QueryRow(`SELECT digest FROM run_monitor_batches WHERE job_id=? AND sequence=?`, b.JobID, b.Sequence).Scan(&prior)
	if err == nil {
		if prior != b.Digest {
			return errors.New("conflicting provider retry")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if (state != "collecting" && state != "reconciling") || next != b.Sequence || !expires.After(b.ReceivedAt) || (state == "collecting" && status != "RUNNING" && status != "PAUSED") {
		return errors.New("provider job stopped, expired or sequence changed")
	}
	if job.Source.Pack == "elastic" {
		scope := payload.ResolvedIndices
		if payload.Progress != nil && payload.Progress.Continuation != nil {
			cursorScope := payload.Progress.Continuation.ResolvedIndices
			if len(scope) == 0 {
				scope = cursorScope
			} else if len(cursorScope) > 0 && !slices.Equal(scope, cursorScope) {
				return errors.New("Elastic continuation differs from resolved document scope")
			}
		}
		if err := analysis.ValidateResolvedIndices(scope); err != nil {
			return err
		}
		if !job.QueryUntilUTC.IsZero() && !slices.Equal(scope, job.ElasticResolvedIndices) {
			return errors.New("Elastic fixed-window concrete scope changed")
		}
		if job.QueryUntilUTC.IsZero() {
			job.ElasticResolvedIndices = append([]string(nil), scope...)
		}
	}
	if job.Source.Pack == "elastic" && job.QueryUntilUTC.IsZero() && payload.Progress != nil && !payload.QueryFromUTC.IsZero() {
		// Page counters describe one complete read pass. A later overlapping
		// poll or reconciliation of the same fixed window starts fresh.
		job.QueryCoverage = nil
	}
	if !payload.QueryUntilUTC.IsZero() {
		if payload.QueryFromUTC.Before(job.StartedAt) || !payload.QueryUntilUTC.After(payload.QueryFromUTC) || payload.QueryUntilUTC.After(b.ReceivedAt.Add(30*time.Second)) || job.LoadEndedAt != nil && payload.QueryUntilUTC.After(*job.LoadEndedAt) || (!job.QueryUntilUTC.IsZero() && (!job.QueryFromUTC.Equal(payload.QueryFromUTC) || !job.QueryUntilUTC.Equal(payload.QueryUntilUTC))) {
			return errors.New("invalid provider query window")
		}
		job.QueryFromUTC = payload.QueryFromUTC
		job.QueryUntilUTC = payload.QueryUntilUTC
	}
	if payload.QueryWindowEnd != nil && (payload.QueryWindowEnd.Before(job.StartedAt) || payload.QueryWindowEnd.After(b.ReceivedAt.Add(30*time.Second)) || job.LoadEndedAt != nil && payload.QueryWindowEnd.After(*job.LoadEndedAt)) {
		return errors.New("invalid provider watermark")
	}

	if job.Source.Pack == "elastic" {
		encoded, e := json.Marshal(payload)
		if e != nil || len(encoded) > 1<<20 {
			return errors.New("Elastic batch exceeds immutable artifact bound")
		}
		total, e := elasticAcceptedArtifactBytesTx(tx, job)
		if e != nil {
			return e
		}
		if total+int64(len(encoded)) > 64<<20 {
			job.State = "incomplete"
			job.CoverageReason = "immutable_artifact_budget"
			raw, e := json.Marshal(job)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE run_monitor_jobs SET state='incomplete',payload=? WHERE id=?`, string(raw), job.ID); e != nil {
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			return errors.New("Elastic durable artifact budget reached")
		}
		job.AcceptedArtifactBytes += int64(len(encoded))
	}
	if _, err = tx.Exec(`INSERT INTO run_monitor_batches(job_id,sequence,digest,artifact_key,sample_count,received_at) VALUES(?,?,?,?,?,?)`, b.JobID, b.Sequence, b.Digest, b.ArtifactKey, b.SampleCount, b.ReceivedAt.UTC()); err != nil {
		return err
	}
	var elasticQuota *elasticQuotaTracker
	if job.Source.Pack == "elastic" {
		elasticQuota, err = newElasticQuotaTracker(tx, job)
		if err != nil {
			return err
		}
	}
	headAllowed := func(p analysis.MetricPoint, d *appdynamics.Diagnostic) (bool, error) {
		if elasticQuota != nil {
			return elasticQuota.allow(p, d)
		}
		return providerHeadWithinLimits(tx, job, p, d)
	}
	limited := false
	truncationReasons := map[string]string{}
	for _, coverage := range job.Coverage {
		if coverage.Truncated && coverage.QueryID != "" && coverage.Reason != "" {
			truncationReasons[coverage.QueryID] = coverage.Reason
		}
	}
	markTruncated := func(query, reason string) {
		if job.Source.Pack == "elastic" && query != "" {
			if job.TruncatedQueries == nil {
				job.TruncatedQueries = map[string]bool{}
			}
			job.TruncatedQueries[query] = true
			if truncationReasons[query] == "" && reason != "" {
				truncationReasons[query] = reason
			}
		}
	}
	for i, p := range payload.Points {
		if p.Provider == nil || p.Validate() != nil || job.Source.Pack == "elastic" && (job.ElasticConnectionSnapshot == nil || p.Provider.Name != job.ElasticConnectionSnapshot.Config.Product) || p.ProjectID != project || p.RunID != job.RunID || p.Source != job.Source.ID {
			return errors.New("provider metadata required")
		}
		allowed, e := headAllowed(p, nil)
		if e != nil {
			return e
		}
		if !allowed {
			limited = true
			markTruncated(p.Provider.QueryID, "retention_quota")
			continue
		}
		if err = upsertProviderHeadTx(tx, b.JobID, ObservationKey(p), b.Sequence, i, "point", p); err != nil {
			return err
		}
	}
	for i, d := range payload.Evidence {
		if job.Source.Pack == "elastic" && (d.Validate() != nil || job.ElasticConnectionSnapshot == nil || d.Provider != job.ElasticConnectionSnapshot.Config.Product) {
			return errors.New("invalid Elastic evidence provider projection")
		}
		if job.Source.Pack == "elastic" && d.IngestedAt != nil && d.IngestedAt.After(b.ReceivedAt.Add(30*time.Second)) {
			return errors.New("invalid Elastic evidence ingestion time")
		}
		allowed, e := headAllowed(analysis.MetricPoint{}, &d)
		if e != nil {
			return e
		}
		if !allowed {
			limited = true
			markTruncated(d.QueryID, "retention_quota")
			continue
		}
		if err = upsertProviderHeadTx(tx, b.JobID, DiagnosticKeyForJob(b.JobID, d), b.Sequence, i, "diagnostic", d); err != nil {
			return err
		}
		if job.Source.Pack == "elastic" && d.IngestedAt != nil && d.IngestedAt.After(job.ElasticPendingIngestionWatermark) {
			job.ElasticPendingIngestionWatermark = d.IngestedAt.UTC()
		}
	}
	for _, entity := range payload.Topology {
		key := entity.Type + ":" + entity.ID + ":" + entity.ParentID
		found := false
		for i, prior := range job.Topology {
			if prior.Type+":"+prior.ID+":"+prior.ParentID == key {
				job.Topology[i] = entity
				found = true
				break
			}
		}
		if !found {
			if len(job.Topology) >= 256 {
				limited = true
				continue
			}
			job.Topology = append(job.Topology, entity)
		}
	}
	job.NextSequence = next + 1
	job.State = state
	if limited {
		if payload.Coverage == nil {
			payload.Coverage = map[string]appdynamics.Coverage{}
		}
		payload.Coverage["retention"] = appdynamics.Coverage{Status: "partial", Truncated: true, Message: "retention or expanded series budget reached"}
		if job.Source.Pack == "elastic" {
			payload.Coverage["retention"] = appdynamics.Coverage{Status: "truncated", Truncated: true, Reason: "retention_quota", From: payload.QueryFromUTC, To: payload.QueryUntilUTC, Message: "retention or expanded series budget reached"}
		}
	}
	if job.Coverage == nil {
		job.Coverage = map[string]appdynamics.Coverage{}
	}
	if job.QueryCoverage == nil {
		job.QueryCoverage = map[string]appdynamics.Coverage{}
	}
	for k, c := range payload.Coverage {
		if job.Source.Pack == "elastic" {
			if c.Points < 0 || c.Evidence < 0 || c.Points > 10000 || c.Evidence > 10000 {
				return errors.New("invalid Elastic query coverage counts")
			}
			if prior, ok := job.QueryCoverage[k]; ok && prior.QueryID == c.QueryID && prior.From.Equal(c.From) && prior.To.Equal(c.To) {
				c.Points += prior.Points
				c.Evidence += prior.Evidence
			}
			if c.Status == "empty" && c.Points+c.Evidence > 0 {
				c.Status = "complete"
			}
		}
		if job.Source.Pack == "elastic" && (c.Truncated || c.Status == "truncated") {
			markTruncated(c.QueryID, c.Reason)
		}
		if job.TruncatedQueries[c.QueryID] {
			c.Truncated = true
			c.Status = "truncated"
			c.Reason = truncationReasons[c.QueryID]
			if c.Reason == "" {
				c.Reason = "retention_quota"
			}
		}
		if prior, ok := job.Coverage[k]; ok && prior.Truncated {
			c.Truncated = true
			c.Status = "truncated"
		}
		job.Coverage[k] = c
		job.QueryCoverage[k] = c
	}
	if payload.Progress != nil && payload.Progress.TotalQueries > 0 {
		progress := payload.Progress
		if progress.TotalQueries > 128 || progress.NextQuery < job.QueryOffset || progress.NextQuery > progress.TotalQueries || progress.Complete != (progress.NextQuery == progress.TotalQueries) || payload.QueryFromUTC.IsZero() || !payload.QueryUntilUTC.After(payload.QueryFromUTC) || (!job.QueryUntilUTC.IsZero() && (!job.QueryFromUTC.Equal(payload.QueryFromUTC) || !job.QueryUntilUTC.Equal(payload.QueryUntilUTC))) {
			return errors.New("invalid provider query continuation")
		}
		job.QueryOffset = progress.NextQuery
		if job.Source.Pack == "elastic" {
			if progress.Continuation != nil && (progress.Continuation.Validate() != nil || !progress.Continuation.WindowFrom.Equal(payload.QueryFromUTC) || !progress.Continuation.WindowUntil.Equal(payload.QueryUntilUTC)) {
				return errors.New("invalid immutable Elastic continuation")
			}
			job.ElasticContinuation = progress.Continuation
		}
		job.QueryFromUTC = payload.QueryFromUTC
		job.QueryUntilUTC = payload.QueryUntilUTC
		if progress.Complete {
			if job.Source.Pack == "elastic" {
				if job.ElasticPendingIngestionWatermark.After(job.ElasticIngestionWatermark) {
					job.ElasticIngestionWatermark = job.ElasticPendingIngestionWatermark
				}
				job.ElasticPendingIngestionWatermark = time.Time{}
				job.ElasticResolvedIndices = nil
			}
			job.QueryOffset = 0
			job.ElasticContinuation = nil
			job.QueryFromUTC = time.Time{}
			job.QueryUntilUTC = time.Time{}
		}
	}
	complete, readComplete := len(job.QueryCoverage) > 0, len(job.QueryCoverage) > 0
	for _, c := range job.QueryCoverage {
		complete = complete && (c.Status == "complete" || c.Status == "empty") && !c.Truncated
		readComplete = readComplete && (c.Status == "complete" || c.Status == "empty" || job.Source.Pack == "elastic" && c.Status == "truncated")
	}
	if payload.QueryWindowEnd != nil {
		if payload.Progress != nil && !payload.Progress.Complete {
			return errors.New("incomplete query cannot advance watermark")
		}
		if complete {
			job.QueryWatermark = payload.QueryWindowEnd.UTC()
			at := b.ReceivedAt.UTC()
			job.LastSuccessAt = &at
		}
	}
	settle := 120
	if job.Source.AppDynamics != nil && job.Source.AppDynamics.SettleSeconds > 0 {
		settle = job.Source.AppDynamics.SettleSeconds
	}
	if job.Source.Elastic != nil {
		settle = job.Source.Elastic.Defaults().SettleSeconds
		if settle == 0 {
			settle = 60
		}
	}
	settledAfter := time.Time{}
	if job.LoadEndedAt != nil {
		settledAfter = job.LoadEndedAt.Truncate(time.Minute).Add(time.Duration(settle+60) * time.Second)
		if job.Source.Pack == "elastic" {
			settledAfter = job.LoadEndedAt.Add(time.Duration(settle) * time.Second)
		}
	}
	if state == "reconciling" && payload.Final && readComplete && job.LoadEndedAt != nil && !b.ReceivedAt.Before(settledAfter) && payload.QueryWindowEnd != nil && payload.QueryWindowEnd.Equal(*job.LoadEndedAt) {
		rows, e := tx.Query(`SELECT content_digest FROM run_monitor_observation_heads WHERE job_id=? ORDER BY record_key`, b.JobID)
		if e != nil {
			return e
		}
		hash := sha256.New()
		for rows.Next() {
			var digest string
			if e = rows.Scan(&digest); e != nil {
				rows.Close()
				return e
			}
			hash.Write([]byte(digest))
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()
		current := hex.EncodeToString(hash.Sum(nil))
		if job.FinalDigest == current && job.StableSinceAt != nil {
			if b.ReceivedAt.Sub(*job.StableSinceAt) >= providerStableDuration(job) {
				job.StablePasses++
			}
		} else {
			job.StablePasses = 1
			job.FinalDigest = current
			at := b.ReceivedAt.UTC()
			job.StableSinceAt = &at
		}
		if job.StablePasses >= 2 {
			job.State = "stopped"
			if !complete {
				job.State = "incomplete"
				job.CoverageReason = "retention_quota"
				if job.Source.Pack == "elastic" {
					job.CoverageReason = elasticFinalTruncationReason(job.QueryCoverage)
				}
			}
		}
	}
	if state == "reconciling" && payload.QueryWindowEnd != nil && !readComplete {
		job.StablePasses = 0
		job.FinalDigest = ""
		job.StableSinceAt = nil
	}
	if payload.QueryWindowEnd != nil {
		job.QueryCoverage = nil
	}
	if job.Source.Pack == "elastic" {
		if err = tx.QueryRow(`SELECT COALESCE(SUM(record_bytes),0) FROM run_monitor_observation_heads WHERE job_id=?`, job.ID).Scan(&job.RetainedEvidenceBytes); err != nil {
			return err
		}
	}
	rawBytes, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE run_monitor_jobs SET next_sequence=?,state=?,payload=? WHERE id=?`, job.NextSequence, job.State, string(rawBytes), job.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func upsertProviderHeadTx(tx *storeTx, job, key string, seq int64, index int, kind string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	series, diag, service, severity, trace, timestamp := "", "", "", "", "", ""
	switch v := value.(type) {
	case analysis.MetricPoint:
		series = providerSeriesKey(v)
	case appdynamics.Diagnostic:
		diag = v.Kind
		service, severity, trace, timestamp = v.Service, v.Severity, v.EntityIDs["trace_id"], v.TimestampUTC.UTC().Format(evidenceTimestampLayout)
		if v.Span != nil {
			trace = v.Span.TraceID
		}
	}
	_, err = tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest,series_key,diagnostic_kind,record_bytes,evidence_service,evidence_severity,evidence_trace_id,evidence_time) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,record_key) DO UPDATE SET sequence=excluded.sequence,record_index=excluded.record_index,content_digest=excluded.content_digest,record_bytes=excluded.record_bytes,evidence_service=excluded.evidence_service,evidence_severity=excluded.evidence_severity,evidence_trace_id=excluded.evidence_trace_id,evidence_time=excluded.evidence_time WHERE run_monitor_observation_heads.sequence<excluded.sequence`, job, key, seq, index, kind, digest, series, diag, len(body), service, severity, trace, timestamp)
	return err
}
func (s *SQLiteStore) CloseExpiredProviderMonitors(project, run string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE run_monitor_jobs SET state='incomplete' WHERE project_id=? AND test_id=? AND state='reconciling' AND expires_at<=?`, project, run, now.UTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func providerSeriesKey(p analysis.MetricPoint) string {
	body, _ := json.Marshal([]any{p.Source, p.MetricName, p.Unit, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Unit, p.Provider.Statistic})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func providerHeadWithinLimits(tx *storeTx, job RunMonitorJob, p analysis.MetricPoint, d *appdynamics.Diagnostic) (bool, error) {
	if job.Source.Pack == "elastic" {
		q, err := newElasticQuotaTracker(tx, job)
		if err != nil {
			return false, err
		}
		return q.allow(p, d)
	}
	var count int
	if d != nil {
		key := DiagnosticKeyForJob(job.ID, *d)
		if err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND record_key=?`, job.ID, key).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
		limits := map[string]int{"event": 1000, "violation": 200, "snapshot": 200, "log": 100, "transaction": 100}
		limit, ok := limits[d.Kind]
		if !ok {
			return false, errors.New("invalid evidence kind")
		}
		err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND diagnostic_kind=?`, job.ID, d.Kind).Scan(&count)
		return count < limit, err
	}
	key := providerSeriesKey(p)
	if err := tx.QueryRow(`SELECT CASE WHEN EXISTS (SELECT 1 FROM run_monitor_observation_heads WHERE job_id=? AND series_key=?) THEN 1 ELSE 0 END`, job.ID, key).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if err := tx.QueryRow(`SELECT COUNT(DISTINCT series_key) FROM run_monitor_observation_heads WHERE job_id=? AND series_key<>''`, job.ID).Scan(&count); err != nil {
		return false, err
	}
	if count >= 200 {
		return false, nil
	}
	err := tx.QueryRow(`SELECT COUNT(DISTINCT h.series_key) FROM run_monitor_observation_heads h JOIN run_monitor_jobs j ON j.id=h.job_id WHERE j.test_id=? AND h.series_key<>''`, job.RunID).Scan(&count)
	return count < 1000, err
}

package storage

import (
	"crypto/sha256"
	"database/sql"

	"encoding/hex"
	"encoding/json"
	"errors"

	"slices"

	"time"

	"github.com/jasonczech/apexload/pkg/analysis"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
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
		if j.Source.Pack == monitorprovider.PackAppDynamics {
			j.State = "incomplete"
			j.CoverageReason = "provider_retired"
		} else if j.Source.CanonicalProvider() {
			end := at.UTC()
			if err = finalizePrometheusDiagnosticCoverageTx(tx, &j, end); err != nil {
				return err
			}
			deadline := end.Add(5 * time.Minute)
			if j.Source.Pack == monitorprovider.PackElastic {
				if j.ElasticContinuation != nil {
					cursor := j.ElasticContinuation
					if err = retainElasticCleanupContinuations(&j, []*analysis.ProviderContinuation{cursor}, cursor.WindowFrom, cursor.WindowUntil, end); err != nil {
						return err
					}
				}
				j.ElasticContinuation = nil
				j.ElasticPendingIngestionWatermark = time.Time{}
				j.ElasticResolvedIndices = nil
			}
			j.LoadEndedAt = &end
			j.DrainDeadline = &deadline
			j.ExpiresAt = deadline
			j.State = "reconciling"
			// Dynatrace may have an async request or a partially committed chunk
			// sequence. Finish its frozen window before querying the terminal one.
			if j.Source.Pack != monitorprovider.PackDynatrace || j.QueryUntilUTC.IsZero() {
				j.QueryOffset = 0
				j.QueryTime = nil
				j.QueryFromUTC = time.Time{}
				j.QueryUntilUTC = time.Time{}
				j.QueryCoverage = nil
			}
		} else if j.Source.Pack == "prometheus" {
			end := at.UTC()
			j.LoadEndedAt = &end
			if err = finalizePrometheusScrapeJobTx(tx, &j, end, true); err != nil {
				return err
			}
			var readinessJSON string
			_ = tx.QueryRow(`SELECT packs FROM run_monitor_collector_readiness WHERE collector_id=? AND project_id=?`, j.Source.CollectorID, project).Scan(&readinessJSON)
			var packs []string
			_ = json.Unmarshal([]byte(readinessJSON), &packs)
			if slices.Contains(packs, "prometheus-scrape.v2") {
				deadline := end.Add(30 * time.Second)
				j.DrainDeadline = &deadline
				j.ExpiresAt = deadline
				j.FlushOnly = true
				j.State = "flushing"
			}
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
	JobID         string
	Key           string
	Sequence      int64
	Index         int
	Kind          string
	ContentDigest string `json:"-"`
}
type ProviderMonitorBatch struct {
	CleanupContinuations []*analysis.ProviderContinuation    `json:"cleanup_continuations,omitempty"`
	ResolvedIndices      []string                            `json:"resolved_indices,omitempty"`
	Provider             string                              `json:"provider,omitempty"`
	Topology             []monitorprovider.Entity            `json:"topology,omitempty"`
	Points               []analysis.MetricPoint              `json:"points"`
	Evidence             []monitorprovider.Diagnostic        `json:"evidence,omitempty"`
	Coverage             map[string]monitorprovider.Coverage `json:"coverage,omitempty"`
	QueryWindowEnd       *time.Time                          `json:"query_window_end,omitempty"`
	Progress             *monitorprovider.QueryProgress      `json:"progress,omitempty"`
	QueryFromUTC         time.Time                           `json:"query_from_utc,omitempty"`
	QueryUntilUTC        time.Time                           `json:"query_until_utc,omitempty"`
	Final                bool                                `json:"final,omitempty"`
}

func ObservationKey(p analysis.MetricPoint) string {
	if p.Provider == nil {
		return ""
	}
	if p.Provider.Name == monitorprovider.ProviderPrometheus {
		// Empty dimensions are omitted from durable JSON, so hash their decoded form.
		if len(p.Dimensions) == 0 {
			p.Dimensions = nil
		}
		raw, _ := json.Marshal([]any{p.Source, p.MetricName, p.Unit, p.Provider.QueryID, p.Provider.DefinitionHash, p.Provider.BackendProfile, p.Provider.EntityIDs, p.Dimensions, p.TimestampUTC, p.Provider.Mode, p.Provider.BucketStart, p.Provider.BucketEnd, p.Provider.Statistic})
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	identity := []any{p.Source, p.MetricName, p.Unit, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Provider.BucketStart, p.Provider.ResolutionSeconds, p.Provider.Statistic}
	if p.Provider.Name == monitorprovider.ProviderDynatrace {
		identity = append(identity, p.Provider.Name, p.Provider.Backend, p.Provider.Synthetic, p.Provider.BucketEnd)
	}
	if (p.Provider.Name == monitorprovider.ProviderElasticsearch || p.Provider.Name == monitorprovider.ProviderOpenSearch) && (p.Provider.QueryID != "" || p.Provider.WindowKind != "") {
		identity = []any{p.Source, p.MetricName, p.Unit, p.Provider.Name, p.Provider.QueryID, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Provider.BucketStart, p.Provider.BucketEnd, p.Provider.WindowKind, p.Provider.ResolutionSeconds, p.Provider.Statistic}
	}
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func DiagnosticKeyForJob(job string, d monitorprovider.Diagnostic) string {
	identity := []string{job, d.Kind, d.ApplicationID, d.ID}
	if d.Provider == monitorprovider.ProviderDynatrace {
		identity = append(identity, d.Provider, d.Backend)
	}
	if (d.Provider == monitorprovider.ProviderElasticsearch || d.Provider == monitorprovider.ProviderOpenSearch) && d.QueryID != "" {
		identity = []string{job, d.Provider, d.QueryID, d.Kind, d.ApplicationID, d.Index, d.DocumentID, d.ID}
	}
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *SQLiteStore) ListProviderObservationHeads(project, run, source, kind, after string, limit int) ([]ObservationHead, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid head page size")
	}
	rows, err := s.db.Query(`SELECT h.job_id,h.record_key,h.sequence,h.record_index,h.record_kind,h.content_digest FROM run_monitor_observation_heads h JOIN run_monitor_jobs j ON j.id=h.job_id WHERE j.project_id=? AND j.test_id=? AND (?='' OR j.id=?) AND h.record_kind=? AND h.record_key>? ORDER BY h.record_key,h.job_id LIMIT ?`, project, run, source, run+":"+source, kind, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	heads := []ObservationHead{}
	for rows.Next() {
		var h ObservationHead
		if err := rows.Scan(&h.JobID, &h.Key, &h.Sequence, &h.Index, &h.Kind, &h.ContentDigest); err != nil {
			return nil, err
		}
		heads = append(heads, h)
	}
	return heads, rows.Err()
}
func (s *SQLiteStore) PollDirectRunMonitorJobs(now time.Time) ([]RunMonitorJob, error) {
	rows, err := s.db.Query(`SELECT payload,state,next_sequence FROM run_monitor_jobs WHERE collector_id='' AND state IN ('collecting','reconciling')` + s.elasticCleanupReadyPredicate("payload") + ` ORDER BY next_sequence,id LIMIT 256`)
	if err != nil {
		return nil, err
	}
	return readRunMonitorJobs(rows)
}
func (s *SQLiteStore) AcceptProviderMonitorBatch(project, collector string, b MonitorBatchReceipt, payload ProviderMonitorBatch, leaseName, leaseOwner string, leaseGeneration uint64) error {
	if b.Sequence < 0 || len(b.Digest) != 64 || len(payload.Points) > 512 || len(payload.Evidence) > 100 || len(payload.CleanupContinuations) > 32 || b.SampleCount != len(payload.Points) {
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
	if !job.Source.CanonicalProvider() {
		return errors.New("provider payload requires a governed provider job")
	}
	if len(payload.CleanupContinuations) > 0 && job.Source.Pack != "elastic" {
		return errors.New("cleanup continuations require an Elastic job")
	}
	if job.Source.Prometheus != nil {
		if err = validatePrometheusCoverage(job, payload.Coverage, b.ReceivedAt); err != nil {
			return err
		}
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
	if job.Source.Pack == monitorprovider.PackDynatrace {
		if err = commitDynatraceScanProgressTx(tx, &job, payload.Progress); err != nil {
			return err
		}
	}
	if job.Source.Pack == "elastic" {
		if err = retainElasticCleanupContinuations(&job, payload.CleanupContinuations, payload.QueryFromUTC, payload.QueryUntilUTC, b.ReceivedAt); err != nil {
			return err
		}
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
		if job.Source.Prometheus != nil && job.QueryUntilUTC.IsZero() {
			job.QueryCoverage = nil
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
	headAllowed := func(p analysis.MetricPoint, d *monitorprovider.Diagnostic) (bool, error) {
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
	expectedProvider := job.Source.Pack
	if provider, governed := monitorprovider.Lookup(job.Source.Pack); governed {
		expectedProvider = provider.ProviderName
	}
	if job.Source.Pack == "elastic" {
		if job.ElasticConnectionSnapshot == nil {
			return errors.New("Elastic snapshot required")
		}
		expectedProvider = job.ElasticConnectionSnapshot.Config.Product
	}
	for i, p := range payload.Points {
		if p.Provider == nil || p.Validate() != nil || p.Provider.Name != expectedProvider || p.ProjectID != project || p.RunID != job.RunID || p.Source != job.Source.ID {
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
		if d.Provider != "" && d.Provider != expectedProvider || job.Source.Pack == monitorprovider.PackDynatrace && d.Provider != monitorprovider.ProviderDynatrace {
			return errors.New("diagnostic provider does not match the assigned job")
		}
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
		if job.Source.Prometheus != nil {
			first, latest, e := retainPrometheusDiagnosticTimes(&job, DiagnosticKeyForJob(b.JobID, d), d)
			if e != nil {
				return e
			}
			if first {
				body, e := json.Marshal(d)
				if e != nil {
					return e
				}
				sum := sha256.Sum256(body)
				// The reference follows earliest source observation chronology;
				// its referenced batch remains immutable and outside identity quotas.
				if _, e = tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest) VALUES(?,?,?,?,'diagnostic_first',?) ON CONFLICT(job_id,record_key) DO UPDATE SET sequence=excluded.sequence,record_index=excluded.record_index,content_digest=excluded.content_digest`, b.JobID, "first:"+DiagnosticKeyForJob(b.JobID, d), b.Sequence, i, hex.EncodeToString(sum[:])); e != nil {
					return e
				}
			}
			if !latest {
				continue
			}
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
	if err = recordPrometheusDiagnosticPollsTx(tx, &job, b, payload.Coverage); err != nil {
		return err
	}
	job.NextSequence = next + 1
	job.State = state
	if limited {
		if payload.Coverage == nil {
			payload.Coverage = map[string]monitorprovider.Coverage{}
		}
		payload.Coverage["retention"] = monitorprovider.Coverage{Status: "partial", Truncated: true, Message: "retention or expanded series budget reached"}
		if job.Source.Pack == "elastic" {
			payload.Coverage["retention"] = monitorprovider.Coverage{Status: "truncated", Truncated: true, Reason: "retention_quota", From: payload.QueryFromUTC, To: payload.QueryUntilUTC, Message: "retention or expanded series budget reached"}
		}
	}
	if job.Coverage == nil {
		job.Coverage = map[string]monitorprovider.Coverage{}
	}
	if job.QueryCoverage == nil {
		job.QueryCoverage = map[string]monitorprovider.Coverage{}
	}
	for k, c := range payload.Coverage {
		if job.Source.Prometheus != nil && job.QueryTime != nil && job.QueryOffset < len(job.Source.Prometheus.Queries) && k == job.Source.Prometheus.Queries[job.QueryOffset].ID {
			if prior, exists := job.QueryCoverage[k]; exists && prior.From.Equal(c.From) && prior.To.Equal(c.To) {
				c = mergePrometheusChunkCoverage(prior, c)
				if c.Points > maxPrometheusCoveragePoints || c.ExpectedPoints > maxPrometheusCoveragePoints || c.MissingPoints > maxPrometheusCoveragePoints {
					return errors.New("Prometheus accumulated coverage exceeds bound")
				}
			}
		}
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
		if prior, ok := job.Coverage[k]; job.Source.Pack == "elastic" && ok && prior.Truncated {
			c.Truncated = true
			c.Status = "truncated"
		}
		job.Coverage[k] = c
		job.QueryCoverage[k] = c
	}
	if payload.Progress != nil && payload.Progress.TotalQueries > 0 {
		progress := payload.Progress
		maxQueries := 256
		if job.Source.Dynatrace != nil {
			maxQueries = 128
		}
		if progress.TotalQueries > maxQueries || progress.NextQuery < job.QueryOffset || progress.NextQuery > progress.TotalQueries || progress.Complete != (progress.NextQuery == progress.TotalQueries) || progress.Complete && progress.Cursor != "" || payload.QueryFromUTC.IsZero() || !payload.QueryUntilUTC.After(payload.QueryFromUTC) || (!job.QueryUntilUTC.IsZero() && (!job.QueryFromUTC.Equal(payload.QueryFromUTC) || !job.QueryUntilUTC.Equal(payload.QueryUntilUTC))) {
			return errors.New("invalid provider query continuation")
		}
		if progress.NextTime != nil && (progress.Complete || progress.NextQuery >= progress.TotalQueries || progress.NextTime.Before(payload.QueryFromUTC) || progress.NextTime.After(payload.QueryUntilUTC) || job.QueryTime != nil && progress.NextQuery == job.QueryOffset && (progress.NextTime.Before(*job.QueryTime) || !progress.NextTime.After(*job.QueryTime) && len(payload.Points) > 0)) {
			return errors.New("invalid provider time continuation")
		}
		job.QueryOffset = progress.NextQuery
		if job.Source.Pack == "elastic" {
			if progress.Continuation != nil && (progress.Continuation.Validate() != nil || !progress.Continuation.WindowFrom.Equal(payload.QueryFromUTC) || !progress.Continuation.WindowUntil.Equal(payload.QueryUntilUTC)) {
				return errors.New("invalid immutable Elastic continuation")
			}
			job.ElasticContinuation = progress.Continuation
		}
		if len(progress.Cursor) > 16<<10 {
			return errors.New("provider cursor exceeds bound")
		}
		job.ProviderCursor = progress.Cursor
		job.QueryFromUTC = payload.QueryFromUTC
		job.QueryUntilUTC = payload.QueryUntilUTC
		job.QueryTime = progress.NextTime
		if progress.Complete && progress.Cursor == "" {
			job.QueryTime = nil
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
	complete := len(job.QueryCoverage) > 0
	queried := len(job.QueryCoverage) > 0
	for key, c := range job.QueryCoverage {
		if job.Source.Prometheus != nil && (key == "targets" || key == "alerts") && c.ObservedAt != nil && (c.Status == "complete" || c.Status == "no_data") && !c.Truncated && len(c.Warnings) == 0 {
			// Successfully observed empty current state is valid sampled evidence.
			// Poll-slot completeness remains a separate archive coverage contract.
			continue
		}
		complete = complete && c.Status == "complete" && !c.Truncated && (job.Source.Prometheus == nil || len(c.Warnings) == 0)
		queried = queried && (c.Status == "complete" || c.Status == "no_data" || c.Status == "partial")
	}
	if job.Source.Pack == monitorprovider.PackDynatrace {
		complete = complete && dynatraceSelectedCoverageComplete(job.Source.Dynatrace, job.QueryCoverage, payload.QueryFromUTC, payload.QueryUntilUTC)
	}
	readComplete := complete
	if job.Source.Pack == "elastic" {
		complete, readComplete = len(job.QueryCoverage) > 0, len(job.QueryCoverage) > 0
		for _, c := range job.QueryCoverage {
			complete = complete && (c.Status == "complete" || c.Status == "empty") && !c.Truncated
			readComplete = readComplete && (c.Status == "complete" || c.Status == "empty" || c.Status == "truncated")
		}
	}
	if payload.QueryWindowEnd != nil {
		if job.Source.Pack == monitorprovider.PackDynatrace && (payload.Progress == nil || payload.Progress.TotalQueries < 1 || !payload.Progress.Complete) {
			return errors.New("Dynatrace watermark requires committed terminal query progress")
		}
		if payload.Progress != nil && !payload.Progress.Complete {
			return errors.New("incomplete query cannot advance watermark")
		}
		if complete || job.Source.Prometheus != nil && queried && payload.Progress != nil && payload.Progress.Complete {
			job.QueryWatermark = payload.QueryWindowEnd.UTC()
		}
		if complete {
			at := b.ReceivedAt.UTC()
			job.LastSuccessAt = &at
		}
	}
	settle := 120
	if job.Source.AppDynamics != nil && job.Source.AppDynamics.SettleSeconds > 0 {
		settle = job.Source.AppDynamics.SettleSeconds
	}
	if job.Source.Dynatrace != nil && job.Source.Dynatrace.SettleSeconds > 0 {
		settle = job.Source.Dynatrace.SettleSeconds
	}
	if job.Source.ElasticQuery != nil {
		settle = job.Source.ElasticQuery.Defaults().OverlapSeconds
	}
	stableDelay := 50 * time.Second
	if job.Source.Pack == monitorprovider.PackDynatrace {
		stableDelay = time.Duration(max(60, job.Source.IntervalSeconds)) * time.Second
	}
	settledAt := time.Time{}
	if job.LoadEndedAt != nil {
		settledAt = job.LoadEndedAt.Truncate(time.Minute).Add(time.Duration(settle+60) * time.Second)
	}
	if job.Source.Prometheus != nil {
		if job.Source.Prometheus.SettleSeconds > 0 {
			settle = job.Source.Prometheus.SettleSeconds
		}
		if job.LoadEndedAt != nil {
			settledAt = job.LoadEndedAt.Add(time.Duration(settle) * time.Second)
		}
		stableDelay = 15 * time.Second
	}
	if job.Source.Elastic != nil {
		settle = job.Source.Elastic.Defaults().SettleSeconds
		if settle == 0 {
			settle = 60
		}
		if job.LoadEndedAt != nil {
			settledAt = job.LoadEndedAt.Add(time.Duration(settle) * time.Second)
		}
		stableDelay = 15 * time.Second
	}
	settled := !b.ReceivedAt.Before(settledAt)
	if job.Source.Pack == monitorprovider.PackDynatrace {
		settled = b.ReceivedAt.After(settledAt)
	}
	if state == "reconciling" && payload.Final && (complete || job.Source.Pack == "elastic" && readComplete) && job.LoadEndedAt != nil && settled && payload.QueryWindowEnd != nil && payload.QueryWindowEnd.Equal(*job.LoadEndedAt) {
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
			if b.ReceivedAt.Sub(*job.StableSinceAt) >= stableDelay {
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
	digest, err := providerRecordDigest(value)
	if err != nil {
		return err
	}
	series, diag, service, severity, trace, timestamp := "", "", "", "", "", ""
	switch v := value.(type) {
	case analysis.MetricPoint:
		series = providerSeriesKey(v)
	case monitorprovider.Diagnostic:
		diag = v.Kind
		service, severity, trace, timestamp = v.Service, v.Severity, v.EntityIDs["trace_id"], v.TimestampUTC.UTC().Format(evidenceTimestampLayout)
		if v.Span != nil {
			trace = v.Span.TraceID
		}
	}
	_, err = tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest,series_key,diagnostic_kind,record_bytes,evidence_service,evidence_severity,evidence_trace_id,evidence_time) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,record_key) DO UPDATE SET sequence=excluded.sequence,record_index=excluded.record_index,content_digest=excluded.content_digest,record_bytes=excluded.record_bytes,evidence_service=excluded.evidence_service,evidence_severity=excluded.evidence_severity,evidence_trace_id=excluded.evidence_trace_id,evidence_time=excluded.evidence_time WHERE run_monitor_observation_heads.sequence<excluded.sequence`, job, key, seq, index, kind, digest, series, diag, len(body), service, severity, trace, timestamp)
	return err
}
func providerRecordDigest(value any) (string, error) {
	hashValue := value
	if point, ok := value.(analysis.MetricPoint); ok && point.Provider != nil && point.Provider.Name == monitorprovider.ProviderPrometheus && point.Provider.BackendProfile == "azure_managed" {
		// Display casing does not revise an Azure numeric observation.
		provider := *point.Provider
		provider.DisplayLabels = nil
		point.Provider = &provider
		hashValue = point
	}
	body, err := json.Marshal(hashValue)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyProviderHeadContent checks a parsed immutable artifact record before
// receive-time enrichment. Identity checks alone cannot detect redirection to
// an older accepted receipt carrying a different value for the same bucket.
func VerifyProviderHeadContent(head ObservationHead, value any) error {
	digest, err := providerRecordDigest(value)
	if err != nil {
		return err
	}
	if head.ContentDigest == "" || digest != head.ContentDigest {
		return errors.New("canonical provider content digest mismatch")
	}
	return nil
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
	rows, err := tx.Query(`SELECT payload,state,next_sequence FROM run_monitor_jobs WHERE project_id=? AND test_id=? AND state='flushing' AND expires_at<=?`, project, run, now.UTC())
	if err != nil {
		return err
	}
	jobs, err := readRunMonitorJobs(rows)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Source.Pack != "prometheus" || job.LoadEndedAt == nil {
			return errors.New("invalid expired exporter flush")
		}
		if err = finalizePrometheusScrapeJobTx(tx, &job, *job.LoadEndedAt, true); err != nil {
			return err
		}
		body, e := json.Marshal(job)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(`UPDATE run_monitor_jobs SET state=?,payload=? WHERE id=? AND project_id=?`, job.State, string(body), job.ID, project); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE run_monitor_jobs SET state='incomplete' WHERE project_id=? AND test_id=? AND state='reconciling' AND expires_at<=?`, project, run, now.UTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func providerSeriesKey(p analysis.MetricPoint) string {
	if p.Provider.Name == monitorprovider.ProviderPrometheus {
		if len(p.Dimensions) == 0 {
			p.Dimensions = nil
		}
		body, _ := json.Marshal([]any{p.Source, p.MetricName, p.Unit, p.Provider.QueryID, p.Provider.DefinitionHash, p.Provider.BackendProfile, p.Provider.EntityIDs, p.Dimensions})
		sum := sha256.Sum256(body)
		return hex.EncodeToString(sum[:])
	}
	identity := []any{p.Source, p.MetricName, p.Unit, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Unit, p.Provider.Statistic}
	if p.Provider.Name == monitorprovider.ProviderDynatrace {
		identity = append(identity, p.Provider.Name, p.Provider.Backend, p.Provider.Synthetic)
	}
	body, _ := json.Marshal(identity)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func providerHeadWithinLimits(tx *storeTx, job RunMonitorJob, p analysis.MetricPoint, d *monitorprovider.Diagnostic) (bool, error) {
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
		limits := map[string]int{"event": 1000, "violation": 200, "snapshot": 200, "log": 100, "transaction": 100, "target": 500, "alert": 500, "problem": 200, "span": 200, "trace": 100}
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

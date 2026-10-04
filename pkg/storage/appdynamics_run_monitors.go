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
		if j.Source.IsProvider() {
			end := at.UTC()
			deadline := end.Add(5 * time.Minute)
			j.LoadEndedAt = &end
			j.DrainDeadline = &deadline
			j.ExpiresAt = deadline
			j.State = "reconciling"
			// Dynatrace may have an async request or a partially committed chunk
			// sequence. Finish its frozen window before querying the terminal one.
			if j.Source.Pack != "dynatrace" || j.QueryUntilUTC.IsZero() {
				j.QueryOffset = 0
				j.QueryFromUTC = time.Time{}
				j.QueryUntilUTC = time.Time{}
				j.QueryCoverage = nil
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
	Topology       []analysis.ProviderEntity            `json:"topology,omitempty"`
	Points         []analysis.MetricPoint               `json:"points"`
	Evidence       []analysis.ProviderDiagnostic        `json:"evidence,omitempty"`
	Coverage       map[string]analysis.ProviderCoverage `json:"coverage,omitempty"`
	QueryWindowEnd *time.Time                           `json:"query_window_end,omitempty"`
	Progress       *analysis.ProviderQueryProgress      `json:"progress,omitempty"`
	QueryFromUTC   time.Time                            `json:"query_from_utc,omitempty"`
	QueryUntilUTC  time.Time                            `json:"query_until_utc,omitempty"`
	Final          bool                                 `json:"final,omitempty"`
}

func ObservationKey(p analysis.MetricPoint) string {
	if p.Provider == nil {
		return ""
	}
	identity := []any{p.Source, p.MetricName, p.Unit, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Provider.BucketStart, p.Provider.ResolutionSeconds, p.Provider.Statistic}
	if p.Provider.Name == "dynatrace" {
		identity = append(identity, p.Provider.Name, p.Provider.Backend, p.Provider.Synthetic, p.Provider.BucketEnd)
	}
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func DiagnosticKeyForJob(job string, d appdynamics.Diagnostic) string {
	identity := []string{job, d.Kind, d.ApplicationID, d.ID}
	if d.Provider == "dynatrace" {
		identity = append(identity, d.Provider, d.Backend)
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
	if !job.Source.IsProvider() {
		return errors.New("provider payload requires a native provider job")
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
	if job.Source.Pack == "dynatrace" {
		if err = commitDynatraceScanProgressTx(tx, &job, payload.Progress); err != nil {
			return err
		}
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
	if _, err = tx.Exec(`INSERT INTO run_monitor_batches(job_id,sequence,digest,artifact_key,sample_count,received_at) VALUES(?,?,?,?,?,?)`, b.JobID, b.Sequence, b.Digest, b.ArtifactKey, b.SampleCount, b.ReceivedAt.UTC()); err != nil {
		return err
	}
	limited := false
	for i, p := range payload.Points {
		if p.Provider == nil || p.Validate() != nil || p.Provider.Name != job.Source.Pack || p.ProjectID != project || p.RunID != job.RunID || p.Source != job.Source.ID {
			return errors.New("provider metadata required")
		}
		allowed, e := providerHeadWithinLimits(tx, job, p, nil)
		if e != nil {
			return e
		}
		if !allowed {
			limited = true
			continue
		}
		if err = upsertProviderHeadTx(tx, b.JobID, ObservationKey(p), b.Sequence, i, "point", p); err != nil {
			return err
		}
	}
	for i, d := range payload.Evidence {
		if d.Provider != "" && d.Provider != job.Source.Pack || job.Source.Pack == "dynatrace" && d.Provider != "dynatrace" {
			return errors.New("diagnostic provider does not match the assigned job")
		}
		allowed, e := providerHeadWithinLimits(tx, job, analysis.MetricPoint{}, &d)
		if e != nil {
			return e
		}
		if !allowed {
			limited = true
			continue
		}
		if err = upsertProviderHeadTx(tx, b.JobID, DiagnosticKeyForJob(b.JobID, d), b.Sequence, i, "diagnostic", d); err != nil {
			return err
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
	}
	if job.Coverage == nil {
		job.Coverage = map[string]appdynamics.Coverage{}
	}
	if job.QueryCoverage == nil {
		job.QueryCoverage = map[string]appdynamics.Coverage{}
	}
	for k, c := range payload.Coverage {
		job.Coverage[k] = c
		job.QueryCoverage[k] = c
	}
	if payload.Progress != nil && payload.Progress.TotalQueries > 0 {
		progress := payload.Progress
		if progress.TotalQueries > 128 || progress.NextQuery < job.QueryOffset || progress.NextQuery > progress.TotalQueries || progress.Complete != (progress.NextQuery == progress.TotalQueries) || progress.Complete && progress.Cursor != "" || payload.QueryFromUTC.IsZero() || !payload.QueryUntilUTC.After(payload.QueryFromUTC) || (!job.QueryUntilUTC.IsZero() && (!job.QueryFromUTC.Equal(payload.QueryFromUTC) || !job.QueryUntilUTC.Equal(payload.QueryUntilUTC))) {
			return errors.New("invalid provider query continuation")
		}
		job.QueryOffset = progress.NextQuery
		if len(progress.Cursor) > 16<<10 {
			return errors.New("provider cursor exceeds bound")
		}
		job.ProviderCursor = progress.Cursor
		job.QueryFromUTC = payload.QueryFromUTC
		job.QueryUntilUTC = payload.QueryUntilUTC
		if progress.Complete && progress.Cursor == "" {
			job.QueryOffset = 0
			job.QueryFromUTC = time.Time{}
			job.QueryUntilUTC = time.Time{}
		}
	}
	complete := len(job.QueryCoverage) > 0
	for _, c := range job.QueryCoverage {
		complete = complete && c.Status == "complete" && !c.Truncated
	}
	if job.Source.Pack == "dynatrace" {
		complete = complete && dynatraceSelectedCoverageComplete(job.Source.Dynatrace, job.QueryCoverage, payload.QueryFromUTC, payload.QueryUntilUTC)
	}
	if payload.QueryWindowEnd != nil {
		if job.Source.Pack == "dynatrace" && (payload.Progress == nil || payload.Progress.TotalQueries < 1 || !payload.Progress.Complete) {
			return errors.New("Dynatrace watermark requires committed terminal query progress")
		}
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
	if job.Source.Dynatrace != nil && job.Source.Dynatrace.SettleSeconds > 0 {
		settle = job.Source.Dynatrace.SettleSeconds
	}
	if state == "reconciling" && payload.Final && complete && job.LoadEndedAt != nil && b.ReceivedAt.After(job.LoadEndedAt.Truncate(time.Minute).Add(time.Duration(settle+60)*time.Second)) && payload.QueryWindowEnd != nil && payload.QueryWindowEnd.Equal(*job.LoadEndedAt) {
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
		stableInterval := 50 * time.Second
		if job.Source.Pack == "dynatrace" {
			stableInterval = time.Duration(max(60, job.Source.IntervalSeconds)) * time.Second
		}
		if job.FinalDigest == current && job.StableSinceAt != nil {
			if b.ReceivedAt.Sub(*job.StableSinceAt) >= stableInterval {
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
		}
	}
	if state == "reconciling" && payload.QueryWindowEnd != nil && !complete {
		job.StablePasses = 0
		job.FinalDigest = ""
		job.StableSinceAt = nil
	}
	if payload.QueryWindowEnd != nil {
		job.QueryCoverage = nil
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
	digest, err := providerRecordDigest(value)
	if err != nil {
		return err
	}
	series, diag := "", ""
	switch v := value.(type) {
	case analysis.MetricPoint:
		series = providerSeriesKey(v)
	case appdynamics.Diagnostic:
		diag = v.Kind
	}
	_, err = tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest,series_key,diagnostic_kind) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(job_id,record_key) DO UPDATE SET sequence=excluded.sequence,record_index=excluded.record_index,content_digest=excluded.content_digest WHERE run_monitor_observation_heads.sequence<excluded.sequence`, job, key, seq, index, kind, digest, series, diag)
	return err
}

func providerRecordDigest(value any) (string, error) {
	body, err := json.Marshal(value)
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
	_, err = tx.Exec(`UPDATE run_monitor_jobs SET state='incomplete' WHERE project_id=? AND test_id=? AND state='reconciling' AND expires_at<=?`, project, run, now.UTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func providerSeriesKey(p analysis.MetricPoint) string {
	identity := []any{p.Source, p.MetricName, p.Unit, p.Provider.MetricID, p.Provider.EntityIDs, p.Dimensions, p.Unit, p.Provider.Statistic}
	if p.Provider.Name == "dynatrace" {
		identity = append(identity, p.Provider.Name, p.Provider.Backend, p.Provider.Synthetic)
	}
	body, _ := json.Marshal(identity)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func providerHeadWithinLimits(tx *storeTx, job RunMonitorJob, p analysis.MetricPoint, d *appdynamics.Diagnostic) (bool, error) {
	var count int
	if d != nil {
		key := DiagnosticKeyForJob(job.ID, *d)
		if err := tx.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND record_key=?`, job.ID, key).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
		limits := map[string]int{"event": 1000, "violation": 200, "snapshot": 200, "log": 100, "transaction": 100, "problem": 200, "span": 200, "trace": 100}
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

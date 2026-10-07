package storage

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"sort"
	"strings"
	"time"
)

func validateExpandedElasticSourceTx(tx *storeTx, m *MonitorSource) error {
	c, err := scanELKConnection(tx.QueryRow(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE id=? AND project_id=?`, m.ConnectionID, m.ProjectID))
	if err != nil {
		return err
	}
	if !c.Enabled || c.ConfigurationStatus() != "ready" {
		return errors.New("Elastic connection must be verified for its saved revision")
	}
	if m.ExecutorMode == "" {
		m.ExecutorMode = c.Snapshot().CollectionMode
	}
	m.Destination = c.RuntimeConfig().Endpoint
	if m.ExecutorMode == "collector" {
		var body string
		if err = tx.QueryRow(`SELECT destinations FROM run_monitor_collector_readiness WHERE collector_id=? AND project_id=?`, m.CollectorID, m.ProjectID).Scan(&body); err != nil {
			return errors.New("collector enrollment unavailable")
		}
		var list []string
		if json.Unmarshal([]byte(body), &list) != nil {
			return errors.New("invalid collector enrollment")
		}
		enrolled := false
		for _, d := range list {
			origin, key, ok := strings.Cut(d, "#")
			if ok && origin == m.Destination {
				if m.CollectorPublicKey != "" && m.CollectorPublicKey != key {
					return errors.New("collector key changed")
				}
				m.CollectorPublicKey = key
				enrolled = true
				break
			}
		}
		if !enrolled {
			return errors.New("Elastic collector destination unavailable")
		}
	}
	return ValidateExpandedElasticSourceConnection(*m, c.Snapshot())
}
func providerStableDuration(j RunMonitorJob) time.Duration {
	if j.Source.Pack == "elastic" {
		return 15 * time.Second
	}
	return 50 * time.Second
}

func elasticFinalTruncationReason(coverage map[string]analysis.ProviderCoverage) string {
	reasons := []string{}
	for _, query := range coverage {
		if !query.Truncated && query.Status != "truncated" {
			continue
		}
		if query.Reason == "retention_quota" {
			return query.Reason
		}
		if query.Reason != "" {
			reasons = append(reasons, query.Reason)
		}
	}
	if len(reasons) == 0 {
		return "retention_quota"
	}
	sort.Strings(reasons)
	return reasons[0]
}
func elasticAcceptedArtifactBytesTx(tx *storeTx, j RunMonitorJob) (int64, error) {
	rows, e := tx.Query(`SELECT payload FROM run_monitor_jobs WHERE project_id=? AND test_id=?`, j.ProjectID, j.RunID)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return 0, e
		}
		var job RunMonitorJob
		if e = json.Unmarshal([]byte(b), &job); e != nil {
			return 0, e
		}
		if job.Source.Pack == "elastic" {
			total += job.AcceptedArtifactBytes
		}
	}
	return total, rows.Err()
}

// Snapshot quota usage once under the project mutation lock. A page of 100
// records performs constant SQL work instead of rescanning the retained set
// for every record; the bounded key map also handles revisions within a page.
type elasticQuotaTracker struct {
	job                                  RunMonitorJob
	keys                                 map[string]int64
	series                               map[string]bool
	sourceBytes, runBytes                int64
	sourceRecords, runRecords, runSeries int
	byteLimit                            int64
	recordLimit                          int
}

func newElasticQuotaTracker(tx *storeTx, j RunMonitorJob) (*elasticQuotaTracker, error) {
	q := &elasticQuotaTracker{job: j, keys: map[string]int64{}, series: map[string]bool{}, byteLimit: 8 << 20, recordLimit: 10000}
	if j.Source.Elastic != nil {
		if n := j.Source.Elastic.MaxRetainedRecords; n > 0 {
			q.recordLimit = n
		}
		if n := j.Source.Elastic.MaxRetainedBytes; n > 0 {
			q.byteLimit = int64(n)
		}
	}
	rows, e := tx.Query(`SELECT record_key,record_bytes,record_kind,series_key FROM run_monitor_observation_heads WHERE job_id=?`, j.ID)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var key, kind, series string
		var size int64
		if e = rows.Scan(&key, &size, &kind, &series); e != nil {
			rows.Close()
			return nil, e
		}
		q.keys[key] = size
		q.sourceBytes += size
		if kind == "diagnostic" {
			q.sourceRecords++
		}
		if series != "" {
			q.series[series] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Canonical byte/evidence budgets belong to Elastic. The series ceiling is
	// shared by every source in the run; series identities include the source.
	rows, e = tx.Query(`SELECT j.payload,COALESCE(SUM(h.record_bytes),0),COALESCE(SUM(CASE WHEN h.record_kind='diagnostic' THEN 1 ELSE 0 END),0),COUNT(DISTINCT CASE WHEN h.series_key<>'' THEN h.series_key END) FROM run_monitor_jobs j LEFT JOIN run_monitor_observation_heads h ON h.job_id=j.id WHERE j.project_id=? AND j.test_id=? GROUP BY j.id,j.payload`, j.ProjectID, j.RunID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var bytes int64
		var records, series int
		if e = rows.Scan(&raw, &bytes, &records, &series); e != nil {
			return nil, e
		}
		var sourceJob RunMonitorJob
		if e = json.Unmarshal([]byte(raw), &sourceJob); e != nil {
			return nil, e
		}
		if sourceJob.Source.Pack == "elastic" {
			q.runBytes += bytes
			q.runRecords += records
		}
		q.runSeries += series
	}
	return q, rows.Err()
}
func (q *elasticQuotaTracker) allow(p analysis.MetricPoint, d *analysis.ProviderEvidence) (bool, error) {
	var value any = p
	key := ObservationKey(p)
	if d != nil {
		value = *d
		key = DiagnosticKeyForJob(q.job.ID, *d)
	}
	body, e := json.Marshal(value)
	if e != nil {
		return false, e
	}
	size := int64(len(body))
	old, exists := q.keys[key]
	if size > 8192 || q.sourceBytes-old+size > q.byteLimit || q.runBytes-old+size > 32<<20 {
		return false, nil
	}
	if d != nil && !exists && (q.sourceRecords >= q.recordLimit || q.runRecords >= 50000) {
		return false, nil
	}
	if d == nil {
		series := providerSeriesKey(p)
		if !q.series[series] {
			if len(q.series) >= 200 || q.runSeries >= 1000 {
				return false, nil
			}
			q.series[series] = true
			q.runSeries++
		}
	}
	if d != nil && !exists {
		q.sourceRecords++
		q.runRecords++
	}
	q.keys[key] = size
	q.sourceBytes += size - old
	q.runBytes += size - old
	return true, nil
}

func ValidateExpandedElasticSourceConnection(m MonitorSource, c *ExpandedElasticConnectionSnapshot) error {
	if c == nil || m.Pack != "elastic" || m.Elastic == nil || m.ProjectID != c.ProjectID || m.ConnectionID != c.ConnectionID || m.ExecutorMode != c.CollectionMode || m.Destination != c.Config.Endpoint {
		return errors.New("Elastic source differs from connection execution identity")
	}
	if c.Config.Validate() != nil || m.Elastic.Validate() != nil {
		return errors.New("invalid immutable Elastic source configuration")
	}
	for _, query := range m.Elastic.Queries {
		if query.Language == "esql" && (c.Config.Product != "elasticsearch" || !c.Capabilities.ESQL) {
			return errors.New("ES|QL is unavailable on the selected connection")
		}
	}
	if m.ExecutorMode == "direct" {
		if m.CollectorID != "" || m.CollectorPublicKey != "" {
			return errors.New("direct source cannot assign collector")
		}
		return nil
	}
	if m.ExecutorMode != "collector" || m.CollectorID == "" || m.CollectorID != c.CollectorAgentID {
		return errors.New("Elastic source collector differs from connection")
	}
	b, e := base64.StdEncoding.DecodeString(m.CollectorPublicKey)
	if e != nil {
		return errors.New("invalid collector encryption identity")
	}
	_, e = ecdh.X25519().NewPublicKey(b)
	return e
}

// Endpoint/account hash namespaces are supplied by the provider client. Reuse
// the durable two-executor admission policy without creating a second lease engine.
func (s *SQLiteStore) AllocateElasticQuota(resourceKey, executor string, now time.Time) (bool, error) {
	return s.AllocateMonitorQuota(resourceKey, executor, now)
}

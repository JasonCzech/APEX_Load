package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	elasticQueryMaxResponseBytes = 8 << 20
	elasticTraceLookupLimit      = 64
	// elasticMaxTermValueBytes bounds one terms value before the metric name
	// itself is checked against the point identity limit.
	elasticMaxTermValueBytes = 128

	elasticMetricCount  = "logs.count"
	elasticMetricErrors = "logs.errors"
	elasticMetricTerms  = "logs.terms."
	elasticCoverageKey  = "logs"
)

// elasticQueryWindow returns the next poll's window: from the run start, or
// the overlap before the watermark aligned to a bucket, to the last complete
// bucket (the load end once the run stops). One poll reads at most the
// selection's BucketsPerPoll, which validation keeps above the re-read, so
// every poll can reach at least one new bucket.
func elasticQueryWindow(job storage.RunMonitorJob, now time.Time) (time.Time, time.Time) {
	if !job.QueryUntilUTC.IsZero() {
		return job.QueryFromUTC, job.QueryUntilUTC
	}
	sel := job.Source.ElasticQuery.Defaults()
	bucket := time.Duration(sel.BucketSeconds) * time.Second
	from := job.StartedAt.UTC()
	if !job.QueryWatermark.IsZero() {
		if start := job.QueryWatermark.UTC().Add(-time.Duration(sel.OverlapSeconds) * time.Second).Truncate(bucket); start.After(from) {
			from = start
		}
	}
	to := now.UTC().Truncate(bucket)
	if job.LoadEndedAt != nil && !to.Before(job.LoadEndedAt.UTC()) {
		to = job.LoadEndedAt.UTC()
	}
	if limit := from.Truncate(bucket).Add(time.Duration(sel.BucketsPerPoll()) * bucket); to.After(limit) {
		to = limit
	}
	return from, to
}

// elasticQueryResult is one poll's bucketed evidence.
type elasticQueryResult struct {
	Points    []analysis.MetricPoint
	Coverage  appdynamics.Coverage
	FoundIDs  []string
	Truncated bool
}

// buildElasticQueryBody is a size-0 search: a UTC-aligned date histogram with
// error and terms sub-aggregations, plus an optional trace ID lookup.
func buildElasticQueryBody(conn *storage.ElasticConnectionSnapshot, sel storage.ElasticQuerySelection, from, to time.Time, traceIDs []string) (map[string]any, []string) {
	bucket := time.Duration(sel.BucketSeconds) * time.Second
	filters := []any{map[string]any{"range": map[string]any{conn.TimestampField: map[string]any{"gte": from.UTC().Format(time.RFC3339Nano), "lt": to.UTC().Format(time.RFC3339Nano)}}}}
	if strings.TrimSpace(conn.BaseFilter) != "" {
		filters = append(filters, map[string]any{"query_string": map[string]any{"query": conn.BaseFilter}})
	}
	sub := map[string]any{}
	if strings.TrimSpace(conn.ErrorFilter) != "" {
		sub["errors"] = map[string]any{"filter": map[string]any{"query_string": map[string]any{"query": conn.ErrorFilter}}}
	}
	fields := sel.TermsFields
	if len(fields) > storage.ElasticMaxQueriedTermsFields {
		fields = fields[:storage.ElasticMaxQueriedTermsFields]
	}
	for i, field := range fields {
		sub["terms_"+strconv.Itoa(i)] = map[string]any{"terms": map[string]any{"field": field, "size": sel.TermsSize}}
	}
	last := to.Add(-time.Nanosecond).Truncate(bucket)
	aggs := map[string]any{"buckets": map[string]any{
		"date_histogram": map[string]any{
			"field": conn.TimestampField, "fixed_interval": strconv.Itoa(sel.BucketSeconds) + "s", "time_zone": "UTC", "min_doc_count": 0,
			"extended_bounds": map[string]any{"min": from.Truncate(bucket).UnixMilli(), "max": last.UnixMilli()},
		},
		"aggs": sub,
	}}
	if len(traceIDs) > elasticTraceLookupLimit {
		traceIDs = traceIDs[:elasticTraceLookupLimit]
	}
	if conn.TraceIDField != "" && len(traceIDs) > 0 {
		aggs["traces"] = map[string]any{"filter": map[string]any{"terms": map[string]any{conn.TraceIDField: traceIDs}}, "aggs": map[string]any{"ids": map[string]any{"terms": map[string]any{"field": conn.TraceIDField, "size": elasticTraceLookupLimit}}}}
	}
	return map[string]any{"size": 0, "track_total_hits": false, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "aggs": aggs}, fields
}

type elasticAggResponse struct {
	TimedOut bool `json:"timed_out"`
	Shards   struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
	Aggregations struct {
		Buckets struct {
			Buckets []map[string]json.RawMessage `json:"buckets"`
		} `json:"buckets"`
		Traces struct {
			IDs struct {
				Buckets []struct {
					Key string `json:"key"`
				} `json:"buckets"`
			} `json:"ids"`
		} `json:"traces"`
	} `json:"aggregations"`
}

type elasticTermsAgg struct {
	SumOtherDocCount int64 `json:"sum_other_doc_count"`
	Buckets          []struct {
		Key         json.RawMessage `json:"key"`
		KeyAsString *string         `json:"key_as_string"`
		DocCount    int64           `json:"doc_count"`
	} `json:"buckets"`
}

// elasticTermValue names a terms bucket as the server displays it: its
// key_as_string (dates, booleans, IPs), a string key, or a number's exact JSON
// literal, never a float re-rendering that could round or collide.
func elasticTermValue(key json.RawMessage, keyAsString *string) string {
	if keyAsString != nil {
		return strings.TrimSpace(*keyAsString)
	}
	var text string
	if json.Unmarshal(key, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(key, &number) == nil {
		return number.String()
	}
	return ""
}

// decodeElasticBuckets turns a response into bucket points. Buckets outside
// the window or off the UTC grid are dropped; a bucket settles once it is
// behind the late-data overlap.
func decodeElasticBuckets(data []byte, job storage.RunMonitorJob, sel storage.ElasticQuerySelection, fields []string, from, to, now time.Time) (elasticQueryResult, error) {
	var response elasticAggResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return elasticQueryResult{}, errors.New("Elasticsearch aggregation response is invalid")
	}
	bucket := time.Duration(sel.BucketSeconds) * time.Second
	overlap := time.Duration(sel.OverlapSeconds) * time.Second
	result := elasticQueryResult{Coverage: appdynamics.Coverage{Status: "complete", From: from, To: to}}
	if response.TimedOut || response.Shards.Failed > 0 {
		result.Coverage.Status = "partial"
		result.Coverage.Warnings = append(result.Coverage.Warnings, "Elasticsearch reported a timeout or failed shards; the window is re-read")
	}
	if len(sel.TermsFields) > len(fields) {
		result.Truncated = true
		result.Coverage.Warnings = append(result.Coverage.Warnings, fmt.Sprintf("terms limited to the first %d of %d fields", len(fields), len(sel.TermsFields)))
	}
	if strings.TrimSpace(job.ElasticSnapshot.ErrorFilter) == "" {
		result.Coverage.Warnings = append(result.Coverage.Warnings, "no error filter is configured on the connection, so logs.errors is not collected")
	}
	expected := int(to.Sub(from.Truncate(bucket)) / bucket)
	if to.Sub(from.Truncate(bucket))%bucket != 0 {
		expected++
	}
	result.Coverage.ExpectedPoints = expected
	truncatedFields, unnamedFields := map[string]bool{}, map[string]bool{}
	point := func(name string, start time.Time, value float64, settled bool) analysis.MetricPoint {
		v := value
		return analysis.MetricPoint{
			SchemaVersion: analysis.SchemaVersion, ProjectID: job.ProjectID, RunID: job.RunID, ScenarioID: job.ScenarioID, Source: job.Source.ID,
			TimestampUTC: start, MetricName: name, Kind: analysis.KindGauge, Value: &v, Unit: "count", Quality: analysis.QualityValid,
			Provider: &analysis.ProviderMeasurement{
				Name: monitorprovider.ProviderElasticsearch, MetricID: name, Mode: "interval_bucket", BucketStart: start, BucketEnd: start.Add(bucket),
				ResolutionSeconds: sel.BucketSeconds, Statistic: "count", Count: &v, Settled: settled, AggregationEligible: true, Backend: sel.Profile,
			},
		}
	}
	seen := 0
	for _, raw := range response.Aggregations.Buckets.Buckets {
		var key int64
		var docCount float64
		if json.Unmarshal(raw["key"], &key) != nil || json.Unmarshal(raw["doc_count"], &docCount) != nil || math.IsNaN(docCount) || docCount < 0 {
			continue
		}
		start := time.UnixMilli(key).UTC()
		if start.Truncate(bucket) != start || start.Before(from.Truncate(bucket)) || !start.Before(to) {
			continue
		}
		seen++
		settled := !start.Add(bucket).Add(overlap).After(now)
		if job.LoadEndedAt != nil && !now.Before(job.LoadEndedAt.Add(overlap)) {
			settled = true
		}
		result.Points = append(result.Points, point(elasticMetricCount, start, docCount, settled))
		if body, ok := raw["errors"]; ok {
			var errorsAgg struct {
				DocCount float64 `json:"doc_count"`
			}
			if json.Unmarshal(body, &errorsAgg) == nil && errorsAgg.DocCount >= 0 {
				result.Points = append(result.Points, point(elasticMetricErrors, start, errorsAgg.DocCount, settled))
			}
		}
		for i, field := range fields {
			body, ok := raw["terms_"+strconv.Itoa(i)]
			if !ok {
				continue
			}
			var terms elasticTermsAgg
			if json.Unmarshal(body, &terms) != nil {
				continue
			}
			if terms.SumOtherDocCount > 0 {
				truncatedFields[field] = true
			}
			for _, term := range terms.Buckets {
				value := elasticTermValue(term.Key, term.KeyAsString)
				if value == "" || term.DocCount < 0 {
					continue
				}
				// A value too long or too odd to name a series is skipped with a
				// warning; one bad name must not fail the whole batch.
				name := elasticMetricTerms + field + "." + value
				if len(value) > elasticMaxTermValueBytes || len(name) > analysis.MaxMetricIdentityBytes || strings.ContainsAny(value, "\r\n\x00") {
					unnamedFields[field] = true
					continue
				}
				result.Points = append(result.Points, point(name, start, float64(term.DocCount), settled))
			}
		}
	}
	for _, warning := range []struct {
		fields  map[string]bool
		message string
	}{
		{truncatedFields, "terms beyond the top values were not counted for: "},
		{unnamedFields, "terms values too long to name a series were skipped for: "},
	} {
		if len(warning.fields) == 0 {
			continue
		}
		names := make([]string, 0, len(warning.fields))
		for name := range warning.fields {
			names = append(names, name)
		}
		sort.Strings(names)
		result.Truncated = true
		result.Coverage.Warnings = append(result.Coverage.Warnings, warning.message+strings.Join(names, ", "))
	}
	result.Coverage.Points = seen
	if seen < expected && result.Coverage.Status == "complete" {
		result.Coverage.Status = "partial"
		result.Coverage.MissingPoints = expected - seen
	}
	for _, b := range response.Aggregations.Traces.IDs.Buckets {
		result.FoundIDs = append(result.FoundIDs, strings.ToLower(b.Key))
	}
	return result, nil
}

// queryElastic runs one bucketed search with the connection's ELK
// authentication through the host-allowlisted outbound client.
func (s *Server) queryElastic(ctx context.Context, job storage.RunMonitorJob, from, to time.Time, traceIDs []string) (elasticQueryResult, error) {
	conn := job.ElasticSnapshot
	sel := job.Source.ElasticQuery.Defaults()
	parsed, err := url.Parse(conn.Endpoint)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return elasticQueryResult{}, errors.New("invalid Elasticsearch endpoint")
	}
	endpoint := strings.TrimRight(conn.Endpoint, "/") + "/" + url.PathEscape(conn.IndexPattern) + "/_search"
	if err := validateOutboundURLAllowingHosts(endpoint, map[string]struct{}{strings.ToLower(parsed.Hostname()): {}}); err != nil {
		return elasticQueryResult{}, errors.New("Elasticsearch endpoint is not permitted")
	}
	body, fields := buildElasticQueryBody(conn, sel, from, to, traceIDs)
	payload, err := json.Marshal(body)
	if err != nil {
		return elasticQueryResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return elasticQueryResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	auth := &ELKCaptureConfig{AuthType: conn.AuthType, APIKeySecretName: conn.APIKeySecretName, UsernameSecretName: conn.UsernameSecretName, PasswordSecretName: conn.PasswordSecretName}
	if err := applyELKAuthentication(ctx, req, s.runner.secretResolverForProject(job.ProjectID), auth); err != nil {
		return elasticQueryResult{}, err
	}
	client := s.elasticHTTPClient
	if client == nil {
		client = newHostAllowlistedHTTPClient([]string{parsed.Hostname()}, 10*time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return elasticQueryResult{}, errors.New("Elasticsearch request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return elasticQueryResult{}, fmt.Errorf("Elasticsearch returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, elasticQueryMaxResponseBytes+1))
	if err != nil || len(data) > elasticQueryMaxResponseBytes {
		return elasticQueryResult{}, errors.New("Elasticsearch response is unreadable or too large")
	}
	return decodeElasticBuckets(data, job, sel, fields, from, to, time.Now().UTC())
}

// collectDirectElastic polls one window and ingests it through the shared
// chunked provider protocol. Each poll is one complete query.
func (s *Server) collectDirectElastic(parent context.Context, job storage.RunMonitorJob) {
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil || job.ElasticSnapshot == nil || job.Source.ElasticQuery == nil {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	from, to := elasticQueryWindow(job, time.Now())
	if !to.After(from) {
		return
	}
	result, err := s.queryElastic(ctx, job, from, to, nil)
	if err != nil {
		result.Coverage = appdynamics.Coverage{Status: "unavailable", From: from, To: to, Message: scrubTextBounded(err.Error(), 256)}
	}
	if !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	ingestCtx, ingestCancel := s.runner.executionOperationContext(parent)
	defer ingestCancel()
	common := appdynamics.Result{Points: result.Points, Coverage: map[string]appdynamics.Coverage{elasticCoverageKey: result.Coverage}, Progress: monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}}
	sequence := job.NextSequence
	for len(common.Points) > 0 {
		payload, n, _, e := boundedDirectAppDynamicsBatch(common, job, from, to)
		if e != nil {
			return
		}
		if e = s.ingestElasticBatch(ingestCtx, job, sequence, payload, term); e != nil {
			return
		}
		common.Points = common.Points[n:]
		sequence++
	}
	if sequence == job.NextSequence {
		_ = s.ingestElasticBatch(ingestCtx, job, sequence, directCoverageBatch(common, job, from, to), term)
	}
}

// ingestElasticBatch accepts only bucket points this source's frozen
// selection could have produced, then persists them like every provider.
func (s *Server) ingestElasticBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, batch storage.ProviderMonitorBatch, term executionLeaseTerm) error {
	if job.ElasticSnapshot == nil || job.Source.ElasticQuery == nil || sequence < 0 || len(batch.Points) > 512 || len(batch.Evidence) != 0 || len(batch.Coverage) > 1 || len(batch.Topology) != 0 {
		return errors.New("invalid Elasticsearch provider batch")
	}
	sel := job.Source.ElasticQuery.Defaults()
	now := time.Now().UTC()
	from, to := batch.QueryFromUTC, batch.QueryUntilUTC
	if from.IsZero() || from.Location() != time.UTC || to.Location() != time.UTC || !to.After(from) || from.Before(job.StartedAt) || to.After(now.Add(time.Minute)) || job.LoadEndedAt != nil && to.After(*job.LoadEndedAt) {
		return errors.New("invalid Elasticsearch query window")
	}
	bucket := time.Duration(sel.BucketSeconds) * time.Second
	allowed := map[string]bool{elasticMetricCount: true, elasticMetricErrors: true}
	fields := sel.TermsFields
	if len(fields) > storage.ElasticMaxQueriedTermsFields {
		fields = fields[:storage.ElasticMaxQueriedTermsFields]
	}
	for i, p := range batch.Points {
		m := p.Provider
		if p.Validate() != nil || m == nil || m.Name != monitorprovider.ProviderElasticsearch || p.ProjectID != job.ProjectID || p.RunID != job.RunID || p.Source != job.Source.ID || p.ScenarioID != job.ScenarioID || p.DatasetID != "" || !p.ReceivedAt.IsZero() || p.ActiveRuntimeMS != nil || p.Kind != analysis.KindGauge || p.Unit != "count" {
			return errors.New("invalid or misattributed Elasticsearch point")
		}
		name := p.MetricName
		approved := allowed[name]
		for _, field := range fields {
			approved = approved || strings.HasPrefix(name, elasticMetricTerms+field+".") && len(name) > len(elasticMetricTerms+field+".")
		}
		if !approved || m.MetricID != name || m.Mode != "interval_bucket" || m.Statistic != "count" || m.ResolutionSeconds != sel.BucketSeconds || m.BucketStart.Truncate(bucket) != m.BucketStart || m.BucketStart.Before(from.Truncate(bucket)) || !m.BucketStart.Before(to) || len(m.EntityIDs) != 0 || len(p.Dimensions) != 0 {
			return errors.New("point differs from the approved Elasticsearch selection")
		}
		if providerSettledClaimPremature(m.Settled, m.BucketEnd, sel.OverlapSeconds, now) && (job.LoadEndedAt == nil || now.Before(job.LoadEndedAt.Add(time.Duration(sel.OverlapSeconds)*time.Second))) {
			return errors.New("bucket claims to be settled inside the late-data overlap")
		}
		normalized, err := analysis.NormalizeRunClock(p, job.StartedAt)
		if err != nil {
			return err
		}
		normalized.Service = job.Source.Service
		batch.Points[i] = normalized
	}
	for key, c := range batch.Coverage {
		if key != elasticCoverageKey || c.From.IsZero() || c.From.Location() != time.UTC || c.To.Location() != time.UTC || c.From.Before(job.StartedAt) || !c.To.After(c.From) || c.To.After(to) || c.Points < 0 || c.ExpectedPoints < 0 || c.MissingPoints < 0 || len(c.Warnings) > 32 {
			return errors.New("invalid Elasticsearch coverage")
		}
		c.Message = scrubTextBounded(c.Message, 256)
		for n, v := range c.Warnings {
			c.Warnings[n] = scrubTextBounded(v, 256)
		}
		batch.Coverage[key] = c
	}
	batch.Provider = monitorprovider.ProviderElasticsearch
	return s.persistProviderBatch(ctx, job, sequence, batch, term, now)
}

func populateElasticGrant(*Server, context.Context, *collectoragent.RunJob, storage.RunMonitorJob) error {
	return errors.New("Elasticsearch query sources run in the controller")
}

func validateElasticSubmission(collectoragent.RunJob, storage.RunMonitorJob, string, collectoragent.RunSampleBatch) (int, string) {
	return 403, "Elasticsearch query sources run in the controller"
}

// validatePreparedElastic confirms a required source's ELK connection is
// enabled, in the project, matches the source and has resolvable credentials.
func (tr *TestRunner) validatePreparedElastic(ctx context.Context, source storage.MonitorSource, project string) error {
	connections, ok := tr.testStore.(interface {
		GetELKConnection(string) (*storage.ELKConnection, error)
	})
	if !ok {
		return errors.New("Elasticsearch connection storage unavailable")
	}
	connection, err := connections.GetELKConnection(source.ConnectionID)
	if err != nil || connection == nil || connection.ProjectID != project || !connection.Enabled {
		return errors.New("required Elasticsearch connection is not enabled in this project")
	}
	snapshot := connection.ElasticSnapshot()
	if err := storage.ValidateElasticSourceConnection(source, snapshot); err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://elastic.invalid", nil)
	auth := &ELKCaptureConfig{AuthType: snapshot.AuthType, APIKeySecretName: snapshot.APIKeySecretName, UsernameSecretName: snapshot.UsernameSecretName, PasswordSecretName: snapshot.PasswordSecretName}
	if err := applyELKAuthentication(ctx, req, tr.secretResolverForProject(project), auth); err != nil {
		return fmt.Errorf("required Elasticsearch connection credentials: %w", err)
	}
	return nil
}

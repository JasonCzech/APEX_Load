package api

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

func prometheusGridAnchor(start time.Time) time.Time {
	anchor := start.UTC().Truncate(time.Millisecond)
	if anchor.Before(start) {
		anchor = anchor.Add(time.Millisecond)
	}
	return anchor
}

func (s *Server) ingestPrometheusBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, batch storage.ProviderMonitorBatch, term executionLeaseTerm) error {
	if job.PrometheusConnectionSnapshot == nil || job.Source.Prometheus == nil || sequence < 0 || len(batch.Points) > 512 || len(batch.Evidence) > 100 || len(batch.Coverage) > 128 || len(batch.Topology) != 0 {
		return errors.New("invalid Prometheus provider batch")
	}
	now := time.Now().UTC()
	from, to := batch.QueryFromUTC, batch.QueryUntilUTC
	if from.IsZero() || from.Location() != time.UTC || to.Location() != time.UTC || !to.After(from) || from.Before(job.StartedAt) || to.After(now.Add(time.Minute)) || job.LoadEndedAt != nil && to.After(*job.LoadEndedAt) {
		return errors.New("invalid Prometheus query window")
	}
	if sequence >= job.NextSequence && !job.QueryUntilUTC.IsZero() && (!from.Equal(job.QueryFromUTC) || !to.Equal(job.QueryUntilUTC)) {
		return errors.New("query continuation changed its frozen window")
	}
	selection := job.Source.Prometheus
	step := selection.StepSeconds
	if step == 0 {
		step = 15
	}
	settle := selection.SettleSeconds
	if settle == 0 {
		settle = 120
	}
	anchor := prometheusGridAnchor(job.StartedAt)
	if batch.Progress != nil {
		p := batch.Progress
		expected := len(selection.Queries)
		if job.State != "reconciling" {
			if selection.Targets {
				expected++
			}
			if selection.Alerts {
				expected++
			}
		} else {
			expected += len(prometheusTerminalWindows(job))
		}
		if p.TotalQueries != expected || p.NextQuery < 0 || p.NextQuery > expected || p.Complete != (p.NextQuery == expected) || sequence >= job.NextSequence && p.NextQuery < job.QueryOffset {
			return errors.New("invalid approved Prometheus continuation")
		}
		if p.NextTime != nil && (p.Complete || p.NextQuery >= len(selection.Queries) || p.NextTime.Location() != time.UTC || p.NextTime.Before(from) || p.NextTime.After(to) || p.NextTime.Sub(anchor)%(time.Duration(step)*time.Second) != 0) {
			return errors.New("invalid Prometheus time continuation")
		}
	}
	if batch.QueryWindowEnd != nil && (batch.Progress == nil || !batch.Progress.Complete || !batch.QueryWindowEnd.Equal(to)) {
		return errors.New("watermark requires all approved chunks")
	}
	for i, p := range batch.Points {
		m := p.Provider
		if p.Validate() != nil || m == nil || m.Name != "prometheus" || p.ProjectID != job.ProjectID || p.RunID != job.RunID || p.Source != job.Source.ID || p.ScenarioID != job.ScenarioID || p.Host != job.Source.CollectorID || p.DatasetID != "" || !p.ReceivedAt.IsZero() || p.ActiveRuntimeMS != nil || m.Mode != "evaluation_point" && m.Mode != "query_window" || m.BackendProfile != job.PrometheusConnectionSnapshot.Config.Defaults().BackendProfile || m.EvaluationStepSeconds != step {
			return errors.New("invalid or misattributed Prometheus point")
		}
		var query *prometheus.Query
		for n := range selection.Queries {
			q := &selection.Queries[n]
			if q.ID == m.QueryID {
				query = q
				break
			}
		}
		if query == nil || p.MetricName != query.Name || p.Unit != query.Unit || m.DefinitionHash != prometheus.DefinitionHash(*query) || m.MetricID != query.ID {
			return errors.New("point differs from approved query")
		}
		if m.Mode == "evaluation_point" {
			if p.TimestampUTC.Before(from) || p.TimestampUTC.After(to) || p.TimestampUTC.Sub(anchor)%(time.Duration(step)*time.Second) != 0 {
				return errors.New("point outside frozen evaluation grid")
			}
		} else {
			if query.TerminalExpression == "" || job.State != "reconciling" || job.LoadEndedAt == nil {
				return errors.New("unapproved terminal evaluation")
			}
			approved := m.BucketStart.Equal(job.StartedAt) && m.BucketEnd.Equal(*job.LoadEndedAt)
			for _, w := range prometheusTerminalWindows(job) {
				approved = approved || (w.QueryID == query.ID && m.BucketStart.Equal(w.From) && m.BucketEnd.Equal(w.To))
			}
			if !approved {
				return errors.New("terminal result differs from frozen rule window")
			}
		}
		expectedStatistic := prometheus.QueryStatistic(*query, m.Mode == "query_window")
		if m.Statistic != expectedStatistic || p.Kind != analysis.KindGauge {
			return errors.New("point semantics differ from approved query")
		}
		m.CatalogPath = ""
		m.Count = nil
		m.Sum = nil
		m.Min = nil
		m.Max = nil
		if len(m.EntityIDs) != 1 {
			return errors.New("provider series identity required")
		}
		for key, value := range m.EntityIDs {
			if key != "series" || len(value) != 64 {
				return errors.New("unexpected provider identity")
			}
			if _, err := hex.DecodeString(value); err != nil {
				return errors.New("invalid provider identity")
			}
		}
		if m.Settled && now.Before(p.TimestampUTC.Add(time.Duration(settle)*time.Second)) {
			return errors.New("unsettled evaluation claims terminal eligibility")
		}
		if len(p.Dimensions) > len(query.AllowedLabels) {
			return errors.New("unexpected result labels")
		}
		for k, v := range p.Dimensions {
			approved := slices.Contains(query.AllowedLabels, k)
			if m.BackendProfile == prometheus.BackendAzureManaged {
				for _, label := range query.AllowedLabels {
					approved = approved || strings.EqualFold(label, k)
				}
			}
			if !approved || credential.SensitiveName(k) || strings.ContainsAny(k+v, "\r\n\x00") {
				return errors.New("unsafe or unapproved result labels")
			}
			if m.BackendProfile == prometheus.BackendAzureManaged && (k != strings.ToLower(k) || v != strings.ToLower(v)) {
				return errors.New("Azure result identity must use canonical case")
			}
		}
		for key := range m.DisplayLabels {
			if credential.SensitiveName(key) {
				return errors.New("unsafe display label")
			}
		}
		if m.FreshnessKnown && query.FreshnessExpression == "" {
			return errors.New("freshness requires approved companion evidence")
		}
		if m.UnderlyingSampleTime != nil {
			age := query.MaxSampleAgeSeconds
			if age == 0 {
				age = 90
			}
			if p.TimestampUTC.Sub(*m.UnderlyingSampleTime) > time.Duration(age)*time.Second {
				p.Quality = analysis.QualityStale
			}
		}
		p, err := analysis.NormalizeRunClock(p, job.StartedAt)
		if err != nil {
			return err
		}
		p.Service = job.Source.Service
		batch.Points[i] = p
	}
	for i, d := range batch.Evidence {
		if job.State == "reconciling" && (job.LoadEndedAt == nil || d.TimestampUTC.After(*job.LoadEndedAt)) {
			return errors.New("current-state diagnostics cannot reconstruct the completed load window")
		}
		if d.ID == "" || len(d.ID) > 256 || d.TimestampUTC.IsZero() || d.TimestampUTC.Location() != time.UTC || d.TimestampUTC.Before(job.StartedAt.Add(-time.Minute)) || d.TimestampUTC.After(now.Add(time.Minute)) || len(d.EntityIDs) > 16 || len(d.Properties) > 16 || (d.Kind != "target" && d.Kind != "alert") || d.Kind == "target" && !selection.Targets || d.Kind == "alert" && !selection.Alerts {
			return errors.New("invalid Prometheus diagnostic")
		}
		identity := strings.TrimPrefix(d.ID, d.Kind+"-")
		if len(identity) != 64 || identity == d.ID || identity != strings.ToLower(identity) {
			return errors.New("invalid Prometheus diagnostic fingerprint")
		}
		if _, err := hex.DecodeString(identity); err != nil {
			return errors.New("invalid Prometheus diagnostic fingerprint")
		}
		d.ApplicationID = ""
		if !prometheus.MatchesEvidence(selection.EvidenceSelector, d.EntityIDs) {
			return errors.New("diagnostic outside approved label scope")
		}
		if snapshot, ok := batch.Coverage[d.Kind+"s"]; ok && snapshot.ObservedAt != nil && !d.TimestampUTC.Equal(*snapshot.ObservedAt) {
			return errors.New("diagnostic timestamp differs from its snapshot observation time")
		}
		d.Summary = scrubTextBounded(d.Summary, 1024)
		d.Status = scrubTextBounded(d.Status, 128)
		d.Severity = scrubTextBounded(d.Severity, 64)
		for k, v := range d.EntityIDs {
			if len(k) > 128 || len(v) > 128 || credential.SensitiveName(k) || strings.ContainsAny(k+v, "\r\n\x00") {
				return errors.New("unsafe diagnostic identity")
			}
		}
		allowed := map[string]bool{"job": true, "instance": true, "health": true, "last_scrape": true, "last_scrape_duration": true, "last_scrape_utc": true, "active_at": true, "alertname": true, "state": true, "observed_at": true, "correlation": true, "query_id": true}
		for k, v := range d.Properties {
			if !allowed[k] {
				delete(d.Properties, k)
			} else {
				d.Properties[k] = scrubTextBounded(v, 256)
			}
		}
		if d.DeepLink != "" {
			link, e := url.Parse(d.DeepLink)
			origin, _ := url.Parse(job.PrometheusConnectionSnapshot.Config.Endpoint)
			expectedPath := strings.TrimRight(origin.Path, "/") + job.PrometheusConnectionSnapshot.Config.ContextPath + "/" + d.Kind + "s"
			if e != nil || link.User != nil || link.Scheme != origin.Scheme || link.Host != origin.Host || link.Path != expectedPath || link.RawQuery != "" || link.Fragment != "" || len(d.DeepLink) > 2048 {
				d.DeepLink = ""
			}
		}
		batch.Evidence[i] = d
	}
	for key, c := range batch.Coverage {
		if key == "" || len(key) > 256 || strings.ContainsAny(key, "\r\n\x00") || c.From.IsZero() || c.From.Location() != time.UTC || c.To.Location() != time.UTC || c.From.Before(job.StartedAt) || !c.To.After(c.From) || c.To.After(to) || c.Points < 0 || c.ExpectedPoints < 0 || c.MissingPoints < 0 || c.Evidence < 0 || len(c.Warnings) > 32 || len(c.Infos) > 32 {
			return errors.New("invalid provider coverage")
		}
		diagnostic := key == "targets" || key == "alerts"
		approvedQuery := key == "query" && c.Status == "unavailable" && c.Points == 0 && c.Evidence == 0
		for _, query := range selection.Queries {
			approvedQuery = approvedQuery || key == query.ID
		}
		if strings.HasPrefix(key, "terminal:") && job.State == "reconciling" {
			parts := strings.Split(key, ":")
			if len(parts) == 2 || len(parts) == 3 {
				index, err := strconv.Atoi(parts[1])
				windows := prometheusTerminalWindows(job)
				if err == nil && index >= 0 && index < len(windows) {
					window := windows[index]
					approvedQuery = c.From.Equal(window.From) && c.To.Equal(window.To) && (len(parts) == 3 && parts[2] == window.QueryID || len(parts) == 2 && c.Status == "unavailable")
				}
			}
		}
		if !diagnostic && !approvedQuery {
			return errors.New("coverage outside approved query scope")
		}
		if diagnostic && (job.State == "reconciling" && (job.LoadEndedAt == nil || c.ObservedAt == nil || c.ObservedAt.After(*job.LoadEndedAt)) || key == "targets" && !selection.Targets || key == "alerts" && !selection.Alerts) {
			return errors.New("unapproved diagnostic coverage")
		}
		if c.CacheAgeSeconds < 0 || c.CacheAgeSeconds > 60 || c.Cached && (!diagnostic || c.ObservedAt == nil) || !c.Cached && c.CacheAgeSeconds != 0 || c.ObservedAt != nil && (!diagnostic || c.ObservedAt.Location() != time.UTC || c.ObservedAt.Before(job.StartedAt) || c.ObservedAt.After(now.Add(time.Minute))) {
			return errors.New("invalid diagnostic observation freshness")
		}
		if diagnostic && (c.Status == "complete" || c.Status == "no_data") && c.ObservedAt == nil {
			return errors.New("diagnostic coverage requires the actual observation time")
		}
		c.Message = scrubTextBounded(c.Message, 256)
		for n, v := range c.Warnings {
			c.Warnings[n] = scrubTextBounded(v, 256)
		}
		for n, v := range c.Infos {
			c.Infos[n] = scrubTextBounded(v, 256)
		}
		batch.Coverage[key] = c
	}
	batch.Provider = "prometheus"
	return s.persistProviderBatch(ctx, job, sequence, batch, term, now)
}

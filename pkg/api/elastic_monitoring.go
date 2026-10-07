package api

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func canonicalProviderPack(pack string) bool { return nativeProviderPack(pack) }

func expandedElasticQueryWindow(job storage.RunMonitorJob, now time.Time) (time.Time, time.Time) {
	if !job.QueryUntilUTC.IsZero() {
		return job.QueryFromUTC, job.QueryUntilUTC
	}
	end := now.UTC()
	if job.LoadEndedAt != nil {
		end = *job.LoadEndedAt
	}
	start := job.StartedAt
	if job.State != "reconciling" && !job.QueryWatermark.IsZero() && (job.Source.Elastic == nil || job.Source.Elastic.IngestedField == "") {
		overlap := 120
		if job.Source.Elastic != nil && job.Source.Elastic.OverlapSeconds > 0 {
			overlap = job.Source.Elastic.OverlapSeconds
		}
		start = job.QueryWatermark.Add(-time.Duration(overlap) * time.Second)
		if start.Before(job.StartedAt) {
			start = job.StartedAt
		}
	}
	return start, end
}

func (s *Server) collectDirectExpandedElastic(parent context.Context, job storage.RunMonitorJob) {
	if s.hasUnacceptedElasticCleanup(elasticCleanupRun, job.ID) {
		s.resumeUnacceptedElasticCleanup()
		return
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil || job.ElasticConnectionSnapshot == nil || job.Source.Elastic == nil {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, time.Duration(elasticPolicyConfig(job.ElasticConnectionSnapshot.Config).MaxPollSeconds)*time.Second)
	defer stop()
	from, to := expandedElasticQueryWindow(job, time.Now().UTC())
	if !to.After(from) {
		return
	}
	result := elastic.Result{}
	var collectionClient *elastic.Client
	checkpointAccepted := false
	defer func() {
		if collectionClient != nil {
			if !checkpointAccepted && (result.Continuation != nil || len(result.CleanupContinuations) > 0) {
				s.finishUnacceptedDirectElasticResult(collectionClient, job, result)
			}
			collectionClient.Close()
		}
	}()
	creds, err := resolveElasticCredentials(ctx, s.runner.secretResolverForProject(job.ProjectID), job.ElasticConnectionSnapshot)
	if err == nil {
		var client *elastic.Client
		config, quotaErr := s.elasticQuotaConfig(ctx, job.ElasticConnectionSnapshot.Config, "direct:"+s.controllerID)
		err = quotaErr
		if err == nil {
			client, err = elastic.NewClient(config, creds)
		}
		if err == nil {
			collectionClient = client
			_ = client.UseVerifiedCapabilities(job.ElasticConnectionSnapshot.Capabilities)
			selection := *job.Source.Elastic
			selection.ProjectID = job.ProjectID
			selection.QueryOffset = job.QueryOffset
			selection.ResolvedIndices = job.ElasticResolvedIndices
			selection.Continuation = job.ElasticContinuation
			selection.DisabledQueries = job.TruncatedQueries
			selection.LastHealthAt = elasticHealthWatermark(job)
			selection.HealthCoverage = elasticHealthCoverage(job)
			selection.FinalHealthAfter = elasticFinalHealthAfter(job)
			selection.TerminalWindows = s.elasticTerminalWindows(job)
			selection.ObservedTraceIDs, selection.CorrelationLimitReached = s.elasticObservedTraceIDs(job.RunID)
			selection.IngestionFrom = elasticIngestionFrom(job)
			result, err = client.Collect(ctx, selection, from, to, job.RunID)
		}
	}
	if err != nil && len(result.Coverage) == 0 {
		result.Coverage = map[string]analysis.ProviderCoverage{"collection": {Status: "unavailable", From: from, To: to, Reason: "upstream_unavailable", Message: "Elastic collection failed"}}
	}
	for i := range result.Evidence {
		result.Evidence[i].ReceivedAt = time.Time{}
	}
	for i := range result.Points {
		p := &result.Points[i]
		p.ProjectID = job.ProjectID
		p.RunID = job.RunID
		p.ScenarioID = job.ScenarioID
		p.Source = job.Source.ID
		p.Host = ""
		p.ReceivedAt = time.Time{}
	}
	if !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	ingestCtx, ingestCancel := s.runner.executionOperationContext(parent)
	defer ingestCancel()
	sequence := job.NextSequence
	for len(result.Points) > 0 || len(result.Evidence) > 0 {
		payload, n, m, err := boundedDirectElasticBatch(result, job, from, to)
		if err != nil {
			return
		}
		if err = s.ingestExpandedElasticBatch(ingestCtx, job, sequence, payload, term); err != nil {
			return
		}
		result.Points = result.Points[n:]
		result.Evidence = result.Evidence[m:]
		sequence++
	}
	if sequence == job.NextSequence {
		payload, _, _, err := boundedDirectElasticBatch(result, job, from, to)
		if err == nil {
			checkpointAccepted = s.ingestExpandedElasticBatch(ingestCtx, job, sequence, payload, term) == nil
		}
	} else {
		checkpointAccepted = true
	}
}

func boundedDirectElasticBatch(result elastic.Result, job storage.RunMonitorJob, from, to time.Time) (storage.ProviderMonitorBatch, int, int, error) {
	n, m := min(256, len(result.Points)), min(50, len(result.Evidence))
	for {
		payload := storage.ProviderMonitorBatch{ResolvedIndices: result.ResolvedIndices, Points: result.Points[:n], Evidence: result.Evidence[:m], QueryFromUTC: from, QueryUntilUTC: to}
		if n == len(result.Points) && m == len(result.Evidence) {
			payload.Coverage = result.Coverage
			payload.CleanupContinuations = result.CleanupContinuations
			progress := result.Progress
			if result.Continuation != nil {
				progress.Continuation = result.Continuation
			}
			payload.Progress = &progress
			if progress.Complete {
				payload.QueryWindowEnd = &to
				payload.Final = job.State == "reconciling"
			}
		}
		body, err := json.Marshal(payload)
		if err == nil && len(body) <= 768<<10 {
			return payload, n, m, nil
		}
		if err != nil {
			return payload, 0, 0, errors.New("invalid Elastic batch encoding")
		}
		if n > 1 {
			n = (n + 1) / 2
		} else if m > 1 {
			m = (m + 1) / 2
		} else {
			return payload, 0, 0, errors.New("Elastic record exceeds provider batch limit")
		}
	}
}

func validateElasticBatch(job storage.RunMonitorJob, payload *storage.ProviderMonitorBatch, now time.Time, checkCursor bool) error {
	if job.Source.Elastic == nil || job.ElasticConnectionSnapshot == nil || payload == nil || len(payload.CleanupContinuations) > 32 || len(payload.Points) > 512 || len(payload.Evidence) > 100 || len(payload.Coverage) > 128 || len(payload.Topology) > 0 {
		return errors.New("invalid Elastic batch")
	}
	selection := job.Source.Elastic.Defaults()
	if selection.Validate() != nil {
		return errors.New("invalid immutable Elastic source")
	}
	from, to := payload.QueryFromUTC, payload.QueryUntilUTC
	if from.IsZero() || from.Location() != time.UTC || to.Location() != time.UTC || !to.After(from) || from.Before(job.StartedAt.Truncate(time.Millisecond)) || to.After(now.Add(time.Minute)) || job.LoadEndedAt != nil && to.After(*job.LoadEndedAt) {
		return errors.New("Elastic query outside immutable run window")
	}
	if checkCursor && !job.QueryUntilUTC.IsZero() && (!from.Equal(job.QueryFromUTC) || !to.Equal(job.QueryUntilUTC)) {
		return errors.New("Elastic continuation changed its query window")
	}
	if analysis.ValidateResolvedIndices(payload.ResolvedIndices) != nil || checkCursor && len(job.ElasticResolvedIndices) > 0 && !slices.Equal(job.ElasticResolvedIndices, payload.ResolvedIndices) {
		return errors.New("Elastic concrete index scope changed")
	}
	if p := payload.Progress; p != nil {
		if p.NextTime != nil || p.Cursor != "" || p.CursorExpiresAt != nil || p.ScannedBytes != 0 || p.ScanReservationBytes != 0 || len(p.ScanReservationIDs) > 0 || len(p.ScanSettlements) > 0 || p.QueryCostKnown {
			return errors.New("Elastic progress contains unrelated provider state")
		}
		if p.NextQuery < 0 || p.TotalQueries < 0 || p.TotalQueries > 64 || p.NextQuery > p.TotalQueries || checkCursor && p.NextQuery < job.QueryOffset || p.Complete && p.Continuation != nil {
			return errors.New("invalid Elastic progress")
		}
		if p.Continuation != nil && (p.Continuation.Validate() != nil || !p.Continuation.WindowFrom.Equal(from) || !p.Continuation.WindowUntil.Equal(to)) {
			return errors.New("invalid Elastic private continuation")
		}
		if p.Continuation != nil && !slices.Equal(p.Continuation.ResolvedIndices, payload.ResolvedIndices) {
			return errors.New("Elastic cursor changed concrete index scope")
		}
	}
	for _, owned := range payload.CleanupContinuations {
		if owned == nil || owned.Validate() != nil || owned.PITID == "" && owned.AsyncID == "" || !owned.WindowFrom.Equal(from) || !owned.WindowUntil.Equal(to) {
			return errors.New("invalid Elastic cleanup ownership")
		}
		approved := false
		for _, definition := range elastic.QueryDefinitions(selection) {
			approved = approved || owned.QueryID == definition.QueryID
		}
		if !approved {
			return errors.New("Elastic cleanup outside approved query")
		}
	}
	if payload.QueryWindowEnd != nil && (payload.Progress == nil || !payload.Progress.Complete || !payload.QueryWindowEnd.Equal(to)) {
		return errors.New("Elastic watermark requires completed pagination")
	}
	if payload.Final && (job.State != "reconciling" || payload.QueryWindowEnd == nil) {
		return errors.New("invalid Elastic finalization")
	}
	product := job.ElasticConnectionSnapshot.Config.Defaults().Product
	for i, p := range payload.Points {
		if p.Validate() != nil || p.Provider == nil || p.Provider.Backend != "" || p.Provider.BackendProfile != "" || p.Provider.DefinitionHash != "" || len(p.Provider.DisplayLabels) > 0 || p.Provider.EvaluationStepSeconds != 0 || p.Provider.AnalysisStatistic != "" || p.Provider.UnderlyingSampleTime != nil || p.Provider.FreshnessKnown || p.Provider.Synthetic || p.Provider.Name != product || p.ProjectID != job.ProjectID || p.RunID != job.RunID || p.Source != job.Source.ID || p.Host != job.Source.CollectorID || p.ScenarioID != job.ScenarioID || p.DatasetID != "" || !p.ReceivedAt.IsZero() || p.ActiveRuntimeMS != nil {
			return errors.New("invalid or misattributed Elastic point")
		}
		if p.Provider.WindowKind == "" || (p.Provider.WindowKind == "query_window") != p.Provider.QueryWindow {
			return errors.New("Elastic metric has inconsistent window semantics")
		}
		approved := false
		for _, definition := range elastic.QueryMetricDefinitions(selection) {
			matched := definition.Name == p.MetricName && (definition.Unit == p.Unit || definition.Unit == "" && p.Unit == "count") && definition.QueryID == p.Provider.QueryID && definition.QueryID == p.Provider.MetricID && definition.Statistic == p.Provider.Statistic
			if definition.Kind == "counter" {
				matched = matched && p.InputKind == analysis.KindCounter && !p.Provider.AggregationEligible
			}
			if strings.HasPrefix(definition.Statistic, "p") || definition.Statistic == "cardinality" {
				matched = matched && p.Provider.Approximate
			}
			approved = approved || matched
		}

		for _, definition := range elastic.QueryDefinitions(selection) {
			if definition.Kind != "health" || definition.QueryID != p.Provider.QueryID || p.Provider.WindowKind != "instant" || p.Provider.AggregationEligible {
				continue
			}
			switch definition.Name {
			case "cluster":
				approved = approved || strings.HasPrefix(p.MetricName, product+".cluster.") || strings.HasPrefix(p.MetricName, product+".node.") || strings.HasPrefix(p.MetricName, product+".index.")
			case "kibana":
				approved = approved || strings.HasPrefix(p.MetricName, "kibana.")
			case "logstash":
				approved = approved || strings.HasPrefix(p.MetricName, "logstash.")
			}
		}
		if !approved {
			return errors.New("Elastic metric outside approved selection")
		}
		if p.Provider.QueryWindow {
			exact := p.Provider.BucketStart.Equal(from) && p.Provider.BucketEnd.Equal(to)
			for _, window := range selection.TerminalWindows {
				exact = exact || window.Metric == p.MetricName && p.Provider.BucketStart.Equal(window.FromUTC) && p.Provider.BucketEnd.Equal(window.UntilUTC)
			}
			if !exact {
				return errors.New("Elastic aggregate changed its approved query window")
			}
		}
		if p.Provider.WindowKind == "instant" && (p.TimestampUTC.Before(job.StartedAt) || p.TimestampUTC.After(now.Add(time.Minute))) {
			return errors.New("instant Elastic reading outside observation horizon")
		}
		if !p.Provider.QueryWindow && p.Provider.WindowKind != "instant" {
			margin := time.Duration(p.Provider.ResolutionSeconds) * time.Second
			if p.Provider.BucketStart.Before(from.Add(-margin)) || !p.Provider.BucketStart.Before(to) || p.Provider.BucketEnd.After(to.Add(margin)) {
				return errors.New("Elastic bucket outside approved query horizon")
			}
			if p.Provider.BucketStart.Before(from) || p.Provider.BucketEnd.After(to) {
				p.Provider.AggregationEligible = false
			}
		}
		canonical, err := analysis.NormalizeRunClock(p, job.StartedAt)
		if err != nil {
			return err
		}
		payload.Points[i] = canonical
	}
	for i, e := range payload.Evidence {
		if e.Validate() != nil || e.Backend != "" || !e.ReceivedAt.IsZero() || e.Provider != product || e.TimestampUTC.Before(from) || !e.TimestampUTC.Before(to) || e.IngestedAt != nil && e.IngestedAt.After(now.Add(30*time.Second)) {
			return errors.New("invalid Elastic evidence identity or scope")
		}
		approved := false
		for _, definition := range elastic.QuerySignalDefinitions(selection) {
			approved = approved || definition.QueryID == e.QueryID && definition.Kind == e.Kind
		}
		indexApproved := slices.Contains(payload.ResolvedIndices, e.Index)
		if len(payload.ResolvedIndices) == 0 {
			for _, pattern := range selection.Indices {
				match, _ := path.Match(pattern, e.Index)
				indexApproved = indexApproved || match
			}
		}
		approved = approved && indexApproved

		if !approved {
			return errors.New("Elastic evidence outside approved signal")
		}
		e.DeepLink = ""
		e.Summary = scrubElasticTextBounded(e.Summary, 4096)
		e.Status = scrubElasticTextBounded(e.Status, 128)
		e.Severity = scrubElasticTextBounded(e.Severity, 128)
		if e.Log != nil {
			e.Log.Message = scrubElasticTextBounded(e.Log.Message, 4096)
			scrubElasticAttributes(e.Log.Attributes)
		}
		if e.Span != nil {
			e.Span.Operation = scrubElasticTextBounded(e.Span.Operation, 256)
			scrubElasticAttributes(e.Span.Attributes)
		}
		if e.Transaction != nil {
			e.Transaction.Name = scrubElasticTextBounded(e.Transaction.Name, 256)
			scrubElasticAttributes(e.Transaction.Attributes)
		}
		for key, value := range e.Properties {
			e.Properties[key] = scrubElasticTextBounded(value, 4096)
		}
		body, err := json.Marshal(e)
		if err != nil || len(body) > 8<<10 {
			return errors.New("Elastic projected record exceeds limit")
		}
		payload.Evidence[i] = scrubRetainedProviderEvidence(e)
	}
	for key, c := range payload.Coverage {
		if c.ExpectedPoints != 0 || c.MissingPoints != 0 || len(c.Warnings) > 0 || len(c.Infos) > 0 || c.Cached || c.CacheAgeSeconds != 0 || c.ObservedAt != nil || c.Freshness != "" {
			return errors.New("Elastic coverage contains unrelated provider state")
		}
		if key == "" || len(key) > 256 || c.From.Before(from) || c.To.After(to) || !c.To.After(c.From) || c.Points < 0 || c.Evidence < 0 || c.AsOf != nil && (c.AsOf.Location() != time.UTC || c.AsOf.After(now.Add(time.Minute))) || c.LateArrivalSeconds < 0 || c.LateArrivalSeconds > 86400 || len(c.Reason) > 128 {
			return errors.New("invalid Elastic coverage")
		}
		approved := c.QueryID == "" && key == "collection" && c.Status == "unavailable"
		for _, definition := range elastic.QueryDefinitions(selection) {
			approved = approved || c.QueryID == definition.QueryID
		}
		if !approved {
			return errors.New("Elastic coverage outside approved query")
		}
		switch c.Status {
		case "complete", "empty", "partial", "unavailable", "forbidden", "unsupported", "stale", "truncated":
		default:
			return errors.New("invalid Elastic coverage status")
		}
		c.Message = scrubElasticTextBounded(c.Message, 256)
		payload.Coverage[key] = c
	}
	return nil
}

func scrubElasticAttributes(attrs map[string]analysis.ScalarValue) {
	for k, v := range attrs {
		if v.Kind == "string" {
			v.StringValue = scrubElasticTextBounded(v.StringValue, 4096)
			attrs[k] = v
		}
	}
}

func (s *Server) ingestExpandedElasticBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, payload storage.ProviderMonitorBatch, term executionLeaseTerm) error {
	store, ok := s.store.(providerMonitorStore)
	if !ok || sequence < 0 {
		return errors.New("provider storage unavailable")
	}
	now := time.Now().UTC()
	if job.Source.Elastic != nil {
		selection := *job.Source.Elastic
		selection.TerminalWindows = s.elasticTerminalWindows(job)
		job.Source.Elastic = &selection
	}
	if err := validateElasticBatch(job, &payload, now, sequence >= job.NextSequence); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > 1<<20 {
		return errors.New("Elastic batch exceeds byte limit")
	}
	artifact, digest := monitorArtifactKey(job.ProjectID, job.RunID, body)
	if err = writeMonitorArtifact(ctx, artifact, body); err != nil {
		return err
	}
	receipt := storage.MonitorBatchReceipt{JobID: job.ID, Sequence: sequence, Digest: digest, ArtifactKey: artifact, SampleCount: len(payload.Points), ReceivedAt: now}
	name, owner, generation := "", "", uint64(0)
	if job.Source.ExecutorMode == "direct" && s.runner.durableExecutionFence {
		name, owner, generation = executionLeaseName, term.ownerID, term.generation
		if owner == "" || generation == 0 {
			return errNotExecutionLeader
		}
	}
	return store.AcceptProviderMonitorBatch(job.ProjectID, job.Source.CollectorID, receipt, payload, name, owner, generation)
}

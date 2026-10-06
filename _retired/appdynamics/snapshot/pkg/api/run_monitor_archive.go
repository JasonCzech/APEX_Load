package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ElasticQueryMetadata struct {
	ID        string `json:"id"`
	Language  string `json:"language"`
	Signal    string `json:"signal"`
	Statistic string `json:"statistic,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Revision  string `json:"revision"`
}

// This projection records interpretation and immutable query identities without
// retaining query text, parameter values, component destinations or credentials.
type ElasticMonitoringMetadata struct {
	Product         string                    `json:"product"`
	Preset          string                    `json:"preset,omitempty"`
	MappingRevision string                    `json:"mapping_revision"`
	Metrics         []elastic.MetricSelection `json:"metrics,omitempty"`
	Queries         []ElasticQueryMetadata    `json:"queries,omitempty"`
	Signals         []string                  `json:"signals"`
}

func elasticMonitoringMetadata(selection elastic.Selection, product string) *ElasticMonitoringMetadata {
	encoded, _ := json.Marshal(selection)
	hash := sha256.Sum256(encoded)
	result := &ElasticMonitoringMetadata{Product: product, Preset: selection.Preset, MappingRevision: hex.EncodeToString(hash[:]), Metrics: selection.Metrics, Signals: []string{}}
	for _, q := range selection.Queries {
		body, _ := json.Marshal(q)
		digest := sha256.Sum256(body)
		result.Queries = append(result.Queries, ElasticQueryMetadata{ID: q.Name, Language: q.Language, Signal: q.Kind, Statistic: q.Statistic, Unit: q.Unit, Revision: hex.EncodeToString(digest[:])})
	}
	for name, enabled := range map[string]bool{"log": selection.Logs, "error": selection.Errors, "span": selection.Traces, "transaction": selection.Transactions, "cluster_health": selection.ClusterHealth, "kibana_health": selection.KibanaHealth, "logstash_health": selection.LogstashHealth} {
		if enabled {
			result.Signals = append(result.Signals, name)
		}
	}
	sort.Strings(result.Signals)
	return result
}

type RunMonitorCoverage struct {
	JobID                     string                          `json:"job_id"`
	SourceID                  string                          `json:"source_id"`
	SourceRevision            int64                           `json:"source_revision"`
	BindingRevision           int64                           `json:"binding_revision"`
	ConnectionID              string                          `json:"connection_id,omitempty"`
	ConnectionRevision        int64                           `json:"connection_revision,omitempty"`
	ExecutorMode              string                          `json:"executor_mode,omitempty"`
	Elastic                   *ElasticMonitoringMetadata      `json:"elastic,omitempty"`
	Selection                 *appdynamics.Selection          `json:"appdynamics,omitempty"`
	Required                  bool                            `json:"required"`
	State                     string                          `json:"state"`
	Reason                    string                          `json:"reason,omitempty"`
	Batches                   int64                           `json:"batches"`
	ProviderCoverage          map[string]appdynamics.Coverage `json:"provider_coverage,omitempty"`
	QueryWatermark            time.Time                       `json:"query_watermark,omitempty,omitzero"`
	ElasticIngestionWatermark time.Time                       `json:"elastic_ingestion_watermark,omitempty,omitzero"`
	Topology                  []appdynamics.Entity            `json:"topology,omitempty"`
}

func (tr *TestRunner) persistRunMonitorArchive(project, run string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budget *archiveOutputBudget) error {
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok || manifest.MonitorEvidenceFile != "" {
		return nil
	}
	jobs, err := store.ListRunMonitorJobs(project, run)
	if err != nil || len(jobs) == 0 {
		return err
	}
	if err := tr.ensureRunMonitorFinalization(project, run); err != nil {
		return err
	}
	if err := ensureAnalysisBackend(tr.testStore); err != nil {
		return err
	}
	// Terminal provider jobs reject further batches. Freeze remaining generic jobs
	// under the project mutation lock before resolving canonical observation heads.
	if err := tr.trackDurableWrite(func() error { return store.StopRunMonitors(project, run, time.Now().UTC()) }); err != nil {
		return err
	}
	jobs, err = store.ListRunMonitorJobs(project, run)
	if err != nil {
		return err
	}
	starts := map[string]time.Time{}
	packs := map[string]string{}
	manifest.MonitoringStatus = "complete"
	manifest.MonitorCoverage = nil
	for _, job := range jobs {
		starts[job.ID] = job.StartedAt
		packs[job.ID] = job.Source.Pack
		reason := job.CoverageReason
		if reason == "" && job.NextSequence == 0 {
			reason = "no_accepted_samples"
		}
		coverage := RunMonitorCoverage{JobID: job.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, BindingRevision: job.BindingRevision, Required: job.Required, State: job.State, Reason: reason, Batches: job.NextSequence, ExecutorMode: job.Source.ExecutorMode, ConnectionID: job.Source.ConnectionID, ProviderCoverage: job.Coverage, Topology: job.Topology}
		coverage.QueryWatermark = job.QueryWatermark
		coverage.ElasticIngestionWatermark = job.ElasticIngestionWatermark
		if job.ElasticConnectionSnapshot != nil && job.Source.Elastic != nil {
			coverage.ConnectionRevision = job.ElasticConnectionSnapshot.Revision
			coverage.Elastic = elasticMonitoringMetadata(*job.Source.Elastic, job.ElasticConnectionSnapshot.Config.Product)
		}
		if job.ConnectionSnapshot != nil {
			coverage.ConnectionRevision = job.ConnectionSnapshot.Revision
		}
		if job.Source.AppDynamics != nil {
			selection := *job.Source.AppDynamics
			selection.QueryOffset = 0
			selection.CollectorValue = ""
			selection.CollectorHeader = ""
			coverage.Selection = &selection
		}
		if job.State == "incomplete" {
			manifest.MonitoringStatus = "incomplete"
		}
		manifest.MonitorCoverage = append(manifest.MonitorCoverage, coverage)
	}
	ctx := budget.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	const pointsName = "monitor_evidence.v1.jsonl"
	var pointCount int64
	eligibleMetrics := make(map[string]map[string]bool)
	var rulePoints []analysis.MetricPoint
	rules := monitorRulesFromManifest(manifest)
	ruleBudgetExceeded := false
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	// Leave room for prepared/final manifest writes and terminal report metadata.
	// Monitoring can exhaust its export allowance without latching a failure in
	// the shared archive writer or discarding the performance archive.
	reserve := int64(1<<20) + 4*int64(len(encodedManifest))
	truncatedSources := map[string]bool{}
	truncatedMetricSources := map[string]bool{}
	recordPoint := func(encoder *json.Encoder, p analysis.MetricPoint) error {
		if p.Provider != nil {
			written, err := encodeProviderMonitorArchiveRecord(encoder, budget, reserve, p)
			if err != nil {
				return err
			}
			if !written {
				truncatedSources[p.Source] = true
				truncatedMetricSources[p.Source] = true
				return nil
			}
		} else {
			if err := encoder.Encode(p); err != nil {
				return err
			}
		}
		pointCount++
		if monitorPointSatisfiesCapture(p, manifestLoadStart(manifest), manifest.CompletedAt) {
			if eligibleMetrics[p.Source] == nil {
				eligibleMetrics[p.Source] = map[string]bool{}
			}
			eligibleMetrics[p.Source][p.Provider.EntityIDs["application"]+":"+p.MetricName] = true
			eligibleMetrics[p.Source][p.MetricName] = true
		}
		for _, rule := range rules {
			if p.Source == rule.SourceID && p.MetricName == rule.Metric {
				if len(rulePoints) < 200000 {
					rulePoints = append(rulePoints, p)
				} else {
					ruleBudgetExceeded = true
				}
				break
			}
		}
		return nil
	}
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, pointsName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		encoder := json.NewEncoder(&archiveOutputFile{file: file, budget: budget})
		afterJob, afterSequence := "", int64(-1)
		for {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			page, err := store.ListMonitorBatchReceipts(project, run, afterJob, afterSequence, 100)
			if err != nil {
				return 0, err
			}
			if len(page) == 0 {
				break
			}
			for _, receipt := range page {
				afterJob, afterSequence = receipt.JobID, receipt.Sequence
				if canonicalProviderPack(packs[receipt.JobID]) {
					continue
				}
				body, err := readMonitorArtifact(ctx, receipt.ArtifactKey, receipt.Digest)
				if err != nil {
					return 0, err
				}
				batch, err := decodeMonitorArtifact(body)
				if err != nil {
					return 0, err
				}
				if len(batch.Points) != receipt.SampleCount {
					return 0, fmt.Errorf("monitor receipt sample count mismatch")
				}
				for _, point := range batch.Points {
					point, err = analysis.NormalizeRunClock(point, starts[receipt.JobID])
					if err != nil {
						return 0, err
					}
					point.ReceivedAt = receipt.ReceivedAt
					point.Quality = monitorPointQuality(point, receipt.ReceivedAt)
					if err := recordPoint(encoder, point); err != nil {
						return 0, err
					}
				}
			}
		}
		// Each provider bucket appears once with its latest accepted revision. Delayed
		// values supersede older receipt contents rather than inflating counts.
		for _, job := range jobs {
			if !canonicalProviderPack(job.Source.Pack) {
				continue
			}
			after := ""
			for {
				page, err := loadCanonicalMonitorData(ctx, store, project, run, job.Source.ID, "point", after, 500)
				if err != nil {
					return 0, err
				}
				for _, point := range page.Points {
					if err := recordPoint(encoder, point); err != nil {
						return 0, err
					}
				}
				if !page.HasMore {
					break
				}
				after = page.AfterKey
			}
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.MonitorEvidenceFile, manifest.MonitorEvidenceCount = pointsName, pointCount
	for index := range manifest.MonitorCoverage {
		coverage := &manifest.MonitorCoverage[index]
		if coverage.Selection != nil && !selectedMonitorMetricsObserved(*coverage.Selection, eligibleMetrics[coverage.SourceID]) || coverage.Elastic != nil && !selectedElasticMetricsObserved(*coverage.Elastic, eligibleMetrics[coverage.SourceID]) {
			coverage.State = "incomplete"
			coverage.Reason = "no_eligible_metric_buckets"
			manifest.MonitoringStatus = "incomplete"
		}
	}
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: pointsName, Kind: "evidence", Format: "jsonl", RecordType: "monitor_points_v1", Count: pointCount}); err != nil {
		return err
	}
	const diagnosticsName = "monitor_diagnostics.v1.jsonl"
	var diagnosticCount int64
	hasProvider := false
	for _, job := range jobs {
		hasProvider = hasProvider || canonicalProviderPack(job.Source.Pack)
	}
	if hasProvider {
		err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, diagnosticsName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
			encoder := json.NewEncoder(&archiveOutputFile{file: file, budget: budget})
			for _, job := range jobs {
				if !canonicalProviderPack(job.Source.Pack) {
					continue
				}
				after := ""
				for {
					page, err := loadCanonicalMonitorData(ctx, store, project, run, job.Source.ID, "diagnostic", after, 500)
					if err != nil {
						return 0, err
					}
					for _, diagnostic := range page.Evidence {
						row := RunMonitorDiagnostic{SourceID: job.Source.ID, Diagnostic: scrubRetainedProviderEvidence(diagnostic), SchemaVersion: providerEvidenceVersion(diagnostic)}
						written, err := encodeProviderMonitorArchiveRecord(encoder, budget, reserve, row)
						if err != nil {
							return 0, err
						}
						if !written {
							truncatedSources[job.Source.ID] = true
							continue
						}
						diagnosticCount++
					}
					if !page.HasMore {
						break
					}
					after = page.AfterKey
				}
			}
			return 0, nil
		}, ctx)
		if err != nil {
			return err
		}
		manifest.MonitorDiagnosticsFile, manifest.MonitorDiagnosticsCount = diagnosticsName, diagnosticCount
		if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: diagnosticsName, Kind: "evidence", Format: "jsonl", RecordType: "monitor_diagnostics_v1", Count: diagnosticCount}); err != nil {
			return err
		}
	}
	start := manifestLoadStart(manifest)
	end := manifest.CompletedAt
	for index := range manifest.MonitorCoverage {
		coverage := &manifest.MonitorCoverage[index]
		if !truncatedSources[coverage.SourceID] {
			continue
		}
		coverage.State, coverage.Reason = "incomplete", "archive_evidence_budget"
		providerCoverage := make(map[string]appdynamics.Coverage, len(coverage.ProviderCoverage)+1)
		for key, value := range coverage.ProviderCoverage {
			providerCoverage[key] = value
		}
		providerCoverage["archive"] = appdynamics.Coverage{Status: "partial", From: start, To: end, Truncated: true, Message: "monitoring export reached the archive evidence budget"}
		coverage.ProviderCoverage = providerCoverage
		manifest.MonitoringStatus = "incomplete"
	}
	manifest.MonitorRuleResults = nil
	for _, rule := range rules {
		result := analysis.EvaluateMonitorRule(rule, rulePoints, start, end)
		if truncatedMetricSources[rule.SourceID] {
			result = analysis.MonitorRuleResult{Rule: rule, Status: "not_evaluated", Message: "monitoring export reached the archive evidence budget"}
		} else if ruleBudgetExceeded {
			result = analysis.MonitorRuleResult{Rule: rule, Status: "not_evaluated", Message: "monitor rule evaluation exceeds bounded evidence budget"}
		}
		manifest.MonitorRuleResults = append(manifest.MonitorRuleResults, result)
		if result.Status == "fail" {
			manifest.SLABreached = true
			manifest.SLADetails = compactNonEmptyStrings(append(manifest.SLADetails, "Monitoring metric "+rule.Metric+" breached its configured criterion")...)
		}
		if result.Status == "pass" || result.Status == "fail" || result.Status == "warning" {
			manifest.SLAEvaluated = true
		}
	}
	return nil
}

func encodeProviderMonitorArchiveRecord(encoder *json.Encoder, budget *archiveOutputBudget, reserve int64, record any) (bool, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	budget.mu.Lock()
	remaining, failure := budget.remaining, budget.err
	budget.mu.Unlock()
	if failure != nil {
		return false, failure
	}
	if int64(len(body)+1) > remaining-reserve {
		return false, nil
	}
	if err := encoder.Encode(record); err != nil {
		return false, err
	}
	return true, nil
}

func providerEvidenceVersion(e analysis.ProviderEvidence) string {
	if e.Provider == "elasticsearch" || e.Provider == "opensearch" {
		return "apexload.provider-evidence.v1"
	}
	return ""
}

type RunMonitorDiagnostic struct {
	SchemaVersion string                 `json:"schema_version,omitempty"`
	SourceID      string                 `json:"source_id"`
	Diagnostic    appdynamics.Diagnostic `json:"diagnostic"`
}

func monitorRulesFromManifest(manifest *RunArchiveManifest) []analysis.MonitorRule {
	if manifest == nil {
		return nil
	}
	if manifest.InitialProvenance != nil && manifest.InitialProvenance.GoalConfig != nil {
		return manifest.InitialProvenance.GoalConfig.MonitorRules
	}
	if manifest.Provenance.GoalConfig != nil {
		return manifest.Provenance.GoalConfig.MonitorRules
	}
	return nil
}

func manifestLoadStart(manifest *RunArchiveManifest) time.Time {
	if manifest.Provenance.StartedAt != nil {
		return *manifest.Provenance.StartedAt
	}
	if manifest.InitialProvenance != nil && manifest.InitialProvenance.StartedAt != nil {
		return *manifest.InitialProvenance.StartedAt
	}
	return manifest.CreatedAt
}

// A successful empty diagnostic query is valid sampled evidence. Numeric
// monitoring additionally needs a valid settled bucket for each selected metric.
// A captured percentile or unweighted average is still evidence even when it
// cannot support a whole-run acceptance statistic.
func monitorPointSatisfiesCapture(point analysis.MetricPoint, start, end time.Time) bool {
	return point.Provider != nil && point.Value != nil && point.Quality == analysis.QualityValid && point.Validate() == nil && point.Provider.Settled && !point.Provider.BucketStart.Before(start) && !point.Provider.BucketEnd.After(end)
}

func selectedMonitorMetricsObserved(selection appdynamics.Selection, observed map[string]bool) bool {
	for _, metric := range selection.Metrics {
		if !observed[metric.ApplicationID+":"+metric.Name] {
			return false
		}
	}
	for _, query := range selection.Analytics {
		if query.Kind == "metric" && !observed[query.ApplicationID+":"+query.Name] {
			return false
		}
	}
	return true
}
func selectedElasticMetricsObserved(selection ElasticMonitoringMetadata, observed map[string]bool) bool {
	for _, metric := range selection.Metrics {
		if !observed[metric.Name] {
			return false
		}
	}
	for _, query := range selection.Queries {
		if query.Signal == "metric" && !observed[query.ID] {
			return false
		}
	}
	return true
}
func monitoringAcceptanceIncomplete(manifest *RunArchiveManifest) bool {
	if manifest == nil {
		return false
	}
	for _, coverage := range manifest.MonitorCoverage {
		if coverage.Required && coverage.State == "incomplete" {
			return true
		}
	}
	rules := monitorRulesFromManifest(manifest)
	results := map[string]string{}
	for _, result := range manifest.MonitorRuleResults {
		body, err := json.Marshal(result.Rule)
		if err == nil {
			results[string(body)] = result.Status
		}
	}
	for _, rule := range rules {
		if rule.Severity == "warning" {
			continue
		}
		body, err := json.Marshal(rule)
		if err != nil || results[string(body)] == "" || results[string(body)] == "not_evaluated" {
			return true
		}
	}
	for _, result := range manifest.MonitorRuleResults {
		if result.Status == "not_evaluated" && result.Rule.Severity != "warning" {
			return true
		}
	}
	return false
}

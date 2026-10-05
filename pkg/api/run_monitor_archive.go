package api

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

type RunMonitorCoverage struct {
	Dynatrace                     *dynatrace.Selection            `json:"dynatrace,omitempty"`
	ManagementSnapshots           int64                           `json:"management_snapshots,omitempty"`
	ManagementIncompleteSnapshots int64                           `json:"management_incomplete_snapshots,omitempty"`
	JobID                         string                          `json:"job_id"`
	SourceID                      string                          `json:"source_id"`
	SourceRevision                int64                           `json:"source_revision"`
	BindingRevision               int64                           `json:"binding_revision"`
	ConnectionID                  string                          `json:"connection_id,omitempty"`
	ConnectionRevision            int64                           `json:"connection_revision,omitempty"`
	ExecutorMode                  string                          `json:"executor_mode,omitempty"`
	PrometheusSelection           *prometheus.Selection           `json:"prometheus,omitempty"`
	Selection                     *appdynamics.Selection          `json:"appdynamics,omitempty"`
	Required                      bool                            `json:"required"`
	State                         string                          `json:"state"`
	Reason                        string                          `json:"reason,omitempty"`
	Batches                       int64                           `json:"batches"`
	ProviderCoverage              map[string]appdynamics.Coverage `json:"provider_coverage,omitempty"`
	Topology                      []appdynamics.Entity            `json:"topology,omitempty"`
}

func (tr *TestRunner) persistRunMonitorArchive(project, run string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budget *archiveOutputBudget) error {
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
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
	coverageByJob := map[string]int{}
	starts := map[string]time.Time{}
	packs := map[string]string{}
	manifest.MonitoringStatus = "complete"
	manifest.MonitorCoverage = nil
	manifest.FrozenMonitorRules = nil
	manifest.MonitorRulesFrozen = false
	for _, job := range jobs {
		if job.RulesFrozen {
			manifest.MonitorRulesFrozen = true
			manifest.FrozenMonitorRules = append(manifest.FrozenMonitorRules, job.FrozenMonitorRules...)
		}
	}
	for _, job := range jobs {
		starts[job.ID] = job.StartedAt
		packs[job.ID] = job.Source.Pack
		reason := job.CoverageReason
		if reason == "" && job.NextSequence == 0 {
			reason = "no_accepted_samples"
		}
		coverage := RunMonitorCoverage{JobID: job.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, BindingRevision: job.BindingRevision, Required: job.Required, State: job.State, Reason: reason, Batches: job.NextSequence, ExecutorMode: job.Source.ExecutorMode, ConnectionID: job.Source.ConnectionID, ProviderCoverage: job.Coverage, Topology: job.Topology}
		if job.ConnectionSnapshot != nil {
			coverage.ConnectionRevision = job.ConnectionSnapshot.Revision
		}
		if job.PrometheusConnectionSnapshot != nil {
			coverage.ConnectionRevision = job.PrometheusConnectionSnapshot.Revision
		}
		if job.Source.Prometheus != nil {
			selection := *job.Source.Prometheus
			selection.QueryOffset = 0
			selection.QueryTime = nil
			selection.CollectionID = ""
			selection.GridAnchor = nil
			coverage.PrometheusSelection = &selection
		}
		if job.DynatraceSnapshot != nil {
			coverage.ConnectionRevision = job.DynatraceSnapshot.Revision
		}
		if job.Source.Dynatrace != nil {
			selection := *job.Source.Dynatrace
			selection.QueryOffset = 0
			selection.Cursor = ""
			coverage.Dynatrace = &selection
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
		coverageByJob[job.ID] = len(manifest.MonitorCoverage)
		manifest.MonitorCoverage = append(manifest.MonitorCoverage, coverage)
	}
	ctx := budget.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	const pointsName = "monitor_evidence.v1.jsonl"
	var pointCount, managementSnapshots int64
	eligibleMetrics := make(map[string]map[string]bool)
	prometheusEvaluations := map[string]map[int64]map[string]bool{}
	freshnessCounts := map[string][3]int{}
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
	recordPoint := func(encoder *json.Encoder, p analysis.MetricPoint) error {
		if p.Provider != nil {
			written, err := encodeProviderMonitorArchiveRecord(encoder, budget, reserve, p)
			if err != nil {
				return err
			}
			if !written {
				truncatedSources[p.Source] = true
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
		}
		if p.Provider != nil && p.Provider.Name == "prometheus" && p.Provider.Mode == "evaluation_point" {
			key := p.Source + ":" + p.Provider.QueryID
			counts := freshnessCounts[key]
			counts[0]++
			if p.Provider.FreshnessKnown {
				counts[1]++
			}
			if p.Quality == analysis.QualityStale {
				counts[2]++
			}
			freshnessCounts[key] = counts
		}
		if p.Provider != nil && p.Provider.Name == "prometheus" && p.Provider.Mode == "evaluation_point" && p.Value != nil && p.Quality == analysis.QualityValid && p.Provider.Settled {
			key := p.Source + ":" + p.Provider.QueryID
			if prometheusEvaluations[key] == nil {
				prometheusEvaluations[key] = map[int64]map[string]bool{}
			}
			at := p.TimestampUTC.UnixNano()
			if prometheusEvaluations[key][at] == nil {
				prometheusEvaluations[key][at] = map[string]bool{}
			}
			labels, _ := json.Marshal(p.Dimensions)
			prometheusEvaluations[key][at][string(labels)+p.Provider.EntityIDs["series"]] = true
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
				if packs[receipt.JobID] == "appdynamics" || packs[receipt.JobID] == "prometheus-query" || packs[receipt.JobID] == "dynatrace" {
					continue
				}
				evidence, err := readMonitorEvidence(ctx, project, run, receipt)
				if err != nil {
					return 0, err
				}
				if evidence.MessagingManagement != nil {
					managementSnapshots++
					if index, ok := coverageByJob[receipt.JobID]; ok {
						coverage := &manifest.MonitorCoverage[index]
						coverage.ManagementSnapshots++
						if evidence.MessagingManagement.Incomplete {
							coverage.ManagementIncompleteSnapshots++
							if coverage.Reason == "" {
								coverage.Reason = "management_evidence_incomplete"
							}
						}
					}
				}
				for _, point := range evidence.Points {
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
			if !job.Source.CanonicalProvider() {
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
		if coverage.PrometheusSelection != nil {
			if coverage.ProviderCoverage == nil {
				coverage.ProviderCoverage = map[string]appdynamics.Coverage{}
			}
			selection := coverage.PrometheusSelection.Defaults()
			anchor := prometheusGridAnchor(manifestLoadStart(manifest))
			until := manifest.CompletedAt
			expected := 0
			if !anchor.After(until) {
				expected = int(until.Sub(anchor)/(time.Duration(selection.StepSeconds)*time.Second)) + 1
			}
			for _, query := range selection.Queries {
				eligible := 0
				minimum := max(1, query.MinSeries)
				for at, series := range prometheusEvaluations[coverage.SourceID+":"+query.ID] {
					pointTime := time.Unix(0, at)
					if !pointTime.Before(anchor) && !pointTime.After(until) && pointTime.Sub(anchor)%(time.Duration(selection.StepSeconds)*time.Second) == 0 && len(series) >= minimum {
						eligible++
					}
				}
				status := "complete"
				if expected == 0 || eligible == 0 || float64(eligible) < .95*float64(expected) {
					status = "partial"
					coverage.State = "incomplete"
					coverage.Reason = "insufficient_numeric_evaluations"
					manifest.MonitoringStatus = "incomplete"
				}
				freshness := "unknown"
				counts := freshnessCounts[coverage.SourceID+":"+query.ID]
				if counts[2] > 0 {
					freshness = "stale"
				} else if counts[1] > 0 && counts[1] == counts[0] {
					freshness = "known"
				} else if counts[1] > 0 {
					freshness = "mixed"
				}
				coverage.ProviderCoverage["numeric:"+query.ID] = appdynamics.Coverage{Status: status, From: manifestLoadStart(manifest), To: until, Points: eligible, ExpectedPoints: expected, MissingPoints: max(0, expected-eligible), Freshness: freshness}
			}
		}
		for key, value := range coverage.ProviderCoverage {
			if (key == "targets_polling" || key == "alerts_polling") && value.Status != "complete" {
				coverage.State = "incomplete"
				coverage.Reason = "diagnostic_poll_gaps"
				manifest.MonitoringStatus = "incomplete"
			}
		}
		if coverage.Selection != nil && !selectedMonitorMetricsObserved(*coverage.Selection, eligibleMetrics[coverage.SourceID]) {
			coverage.State = "incomplete"
			coverage.Reason = "no_eligible_metric_buckets"
			manifest.MonitoringStatus = "incomplete"
		}
		if coverage.Dynatrace != nil && !selectedDynatraceMetricsObserved(*coverage.Dynatrace, eligibleMetrics[coverage.SourceID]) {
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
		hasProvider = hasProvider || job.Source.CanonicalProvider()
	}
	if hasProvider {
		err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, diagnosticsName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
			encoder := json.NewEncoder(&archiveOutputFile{file: file, budget: budget})
			for _, job := range jobs {
				if !job.Source.CanonicalProvider() {
					continue
				}
				after := ""
				for {
					page, err := loadCanonicalMonitorData(ctx, store, project, run, job.Source.ID, "diagnostic", after, 500)
					if err != nil {
						return 0, err
					}
					for _, diagnostic := range page.Evidence {
						row := RunMonitorDiagnostic{SourceID: job.Source.ID, Diagnostic: diagnostic}
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
	if managementSnapshots > 0 {
		if err := persistRunMonitorManagementArchive(ctx, project, run, store, record, manifest, budget, packs, reserve, truncatedSources); err != nil {
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
		result = guardPrometheusRuleCoverage(result, jobs, start, end)
		if truncatedSources[rule.SourceID] {
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

func persistRunMonitorManagementArchive(ctx context.Context, project, run string, store storage.RunMonitorStore, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budget *archiveOutputBudget, packs map[string]string, reserve int64, truncatedSources map[string]bool) error {
	const managementName = "monitor_management.v1.jsonl"
	var writtenSnapshots, writtenRows int64
	err := writeArchiveArtifactDurableWithWriter(record.ArchivePath, managementName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
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
				if packs[receipt.JobID] == "appdynamics" || packs[receipt.JobID] == "prometheus-query" || packs[receipt.JobID] == "dynatrace" {
					afterJob, afterSequence = receipt.JobID, receipt.Sequence
					continue
				}
				evidence, err := readMonitorEvidence(ctx, project, run, receipt)
				if err != nil {
					return 0, err
				}
				if evidence.MessagingManagement != nil {
					record := MonitorManagementRecord{receipt.JobID, strconv.FormatInt(receipt.Sequence, 10), receipt.ReceivedAt, evidence}
					written, encodeErr := encodeProviderMonitorArchiveRecord(encoder, budget, reserve, record)
					if encodeErr != nil {
						return 0, encodeErr
					}
					if written {
						writtenSnapshots++
						writtenRows += int64(len(evidence.MessagingManagement.Rows))
					} else {
						truncatedSources[evidence.Attribution.SourceID] = true
					}
				}
				afterJob, afterSequence = receipt.JobID, receipt.Sequence
			}
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.MonitorManagementFile = managementName
	manifest.MonitorManagementVersion = analysis.MessagingManagementVersion
	manifest.MonitorManagementSnapshotCount = writtenSnapshots
	manifest.MonitorManagementRowCount = writtenRows
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: managementName, Kind: "evidence", Format: "jsonl", RecordType: "monitor_management_v1", Count: writtenSnapshots})
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

type RunMonitorDiagnostic struct {
	SourceID   string                 `json:"source_id"`
	Diagnostic appdynamics.Diagnostic `json:"diagnostic"`
}

func monitorRulesFromManifest(manifest *RunArchiveManifest) []analysis.MonitorRule {
	if manifest == nil {
		return nil
	}
	if manifest.MonitorRulesFrozen {
		return manifest.FrozenMonitorRules
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
	if point.Provider != nil && point.Provider.Name == "prometheus" {
		return point.Value != nil && point.Quality == analysis.QualityValid && point.Validate() == nil && point.Provider.Settled && !point.TimestampUTC.Before(start) && !point.TimestampUTC.After(end)
	}
	return point.Provider != nil && !point.Provider.Synthetic && point.Value != nil && point.Quality == analysis.QualityValid && point.Validate() == nil && point.Provider.Settled && !point.Provider.BucketStart.Before(start) && !point.Provider.BucketEnd.After(end)
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

func selectedDynatraceMetricsObserved(selection dynatrace.Selection, observed map[string]bool) bool {
	for _, metric := range selection.Metrics {
		if !observed[":"+metric.Name] {
			return false
		}
	}
	for _, query := range selection.Grail {
		if (query.Kind == "metric" || query.Kind == "aggregate") && !observed[":"+query.Name] {
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

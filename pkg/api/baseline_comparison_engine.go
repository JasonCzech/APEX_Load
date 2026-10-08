package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	protocolmetrics "github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
)

type baselineTransactionKey struct{ group, protocol, name string }
type baselineTransactionAccumulator struct {
	messaging       *protocolmetrics.MessagingSummary
	grpc            *protocolmetrics.GRPCSummary
	websocket       *protocolmetrics.WebSocketSummary
	count, errors   int64
	total, min, max float64
	digest          *engine.TDigest
	points          []BaselineMetricPoint
}
type baselineComparisonRun struct {
	telemetry      BaselineTelemetryEvidence
	incomplete     bool
	population     string
	originalGroups map[string]string
	identity       BaselineRunIdentity
	manifest       *RunArchiveManifest
	windows        []BaselineLoadWindow
	transactions   map[baselineTransactionKey]map[int]*baselineTransactionAccumulator
	resources      map[string]baselineResourceSeries
	warnings       []string
	achievedTPS    float64
	// browserDocuments is the run's final document metric summary.
	browserDocuments *protocolmetrics.BrowserDocumentSummary
}

func baselineRunInfo(t *ManagedTest) BaselineRunIdentity {
	peak := int64(0)
	return BaselineRunIdentity{ID: t.ID, Name: t.Name, ReleaseID: t.ReleaseID, ReleaseName: t.ReleaseID, ScenarioID: t.ScenarioID, Status: t.Status, VUsers: t.TotalVUsers, PeakVUsers: peak, CompletedAt: t.EndedAt, Pinned: t.BaselinePinned}
}

func (s *Server) readBaselineComparisonRun(ctx context.Context, t *ManagedTest, request BaselineComparisonRequest, p BaselineComparisonPolicy) (*baselineComparisonRun, error) {
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, t.ID)
	if !ok {
		return nil, fmt.Errorf("saved archive unavailable for %s", t.ID)
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, err
	}
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		return nil, err
	}
	if len(summaries) > 100000 {
		return nil, fmt.Errorf("comparison exceeds 100000 intervals; narrow the archive")
	}
	sort.SliceStable(summaries, func(i, j int) bool { return summaries[i].Timestamp.Before(summaries[j].Timestamp) })
	r := &baselineComparisonRun{population: request.Population, originalGroups: map[string]string{}, identity: baselineRunInfo(t), manifest: manifest, transactions: map[baselineTransactionKey]map[int]*baselineTransactionAccumulator{}, resources: map[string]baselineResourceSeries{}}
	r.identity.ArchiveAvailable = true
	if warning := controllerFailoverComparisonWarning(manifest.Continuity); warning != "" {
		r.warnings = append(r.warnings, warning)
	}
	if s.store != nil && t.ReleaseID != "" {
		if release, e := s.store.GetRelease(t.ReleaseID); e == nil && release != nil {
			r.identity.ReleaseName = release.Name
		}
	}
	started := time.Time{}
	if manifest.Provenance.StartedAt != nil {
		started = *manifest.Provenance.StartedAt
	}
	r.achievedTPS = achievedComparisonTPS(summaries, started)
	groups := map[string]string{}
	canonicalGroups := map[string]bool{}
	for _, g := range manifest.Provenance.Groups {
		if _, exists := groups[g.Name]; exists {
			return nil, fmt.Errorf("ambiguous recorded group identity %q", g.Name)
		}
		canonical := g.Name
		if mapped := p.GroupMappings[t.ID+"/"+g.Name]; mapped != "" {
			canonical = mapped
		}
		if canonicalGroups[canonical] {
			return nil, fmt.Errorf("group mappings must be one-to-one; %q is ambiguous", canonical)
		}
		canonicalGroups[canonical] = true
		r.originalGroups[canonical] = g.Name
		groups[g.Name] = g.Protocol
	}
	groupNames := []string{""}
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, group := range groupNames {
		windows := baselineStableWindows(summaries, started, group, request.Windows[t.ID])
		for i := range windows {
			if mapped := p.GroupMappings[t.ID+"/"+group]; mapped != "" {
				windows[i].Group = mapped
			}
			mix := map[string]float64{}
			for k, v := range windows[i].Mix {
				if m := p.GroupMappings[t.ID+"/"+k]; m != "" {
					k = m
				}
				mix[k] += v
			}
			windows[i].Mix = mix
		}
		for _, window := range windows {
			if window.Load >= request.MinimumLoad && (request.MaximumLoad == 0 || window.Load <= request.MaximumLoad) {
				r.windows = append(r.windows, window)
			}
		}
	}
	if len(r.windows) > maxBaselineComparisonWindows {
		return nil, fmt.Errorf("comparison exceeds %d stable windows; narrow the measurement window", maxBaselineComparisonWindows)
	}
	r.telemetry = baselineTelemetryEvidence(t.ID, summaries)
	if documents, _, _, err := readArchiveBrowserDocumentSummaryContext(ctx, record, manifest); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.warnings = append(r.warnings, "Browser document evidence could not be read: "+err.Error())
	} else {
		// Documents pair by group as transactions do: through the policy's
		// group mappings.
		if documents != nil && len(p.GroupMappings) > 0 {
			mapped := *documents
			mapped.Metrics = append([]protocolmetrics.BrowserDocumentMetricSummary(nil), documents.Metrics...)
			for i := range mapped.Metrics {
				if group := p.GroupMappings[t.ID+"/"+mapped.Metrics[i].Group]; group != "" {
					mapped.Metrics[i].Group = group
				}
			}
			documents = &mapped
		}
		r.browserDocuments = documents
		if documents != nil && documents.Incomplete {
			r.warnings = append(r.warnings, "Browser document evidence is incomplete; its document comparisons cover only the retained measurements")
		}
	}
	for _, summary := range summaries {
		if summary.Telemetry != nil && (summary.Telemetry.DroppedResults > 0 || summary.Telemetry.DroppedSubscriberBatches > 0 || summary.Telemetry.MissingMetricWindows > 0) {
			r.incomplete = true
		}
		if summary.ActiveVUsers > r.identity.PeakVUsers {
			r.identity.PeakVUsers = summary.ActiveVUsers
		}
	}
	cells := 0
	err = forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if archiveEventPopulation(*tx) != request.Population {
			return nil
		}
		if !finiteComparison(tx.DurationMs) || tx.DurationMs < 0 {
			return fmt.Errorf("invalid archived transaction duration")
		}
		name := tx.SourceTransaction
		if request.Population != "transaction" {
			name = tx.ActionName
		}
		if name == "" {
			name = tx.ActionName
		}
		group := tx.Group
		protocol := groups[group]
		if request.Population != "transaction" && tx.ServerTelemetry != nil && tx.ServerTelemetry.GRPC.Valid() {
			protocol = "grpc"
			name += " [" + tx.ServerTelemetry.GRPC.Method + " / " + tx.ServerTelemetry.GRPC.Mode + "]"
		}
		if mapped := p.GroupMappings[t.ID+"/"+group]; mapped != "" {
			group = mapped
		}
		key := baselineTransactionKey{group, protocol, name}
		if r.transactions[key] == nil {
			if len(r.transactions) >= maxArchiveAnalysisDimensions {
				return fmt.Errorf("too many transaction identities")
			}
			r.transactions[key] = map[int]*baselineTransactionAccumulator{}
		}
		seconds := tx.Timestamp.Sub(started).Seconds()
		for i, w := range r.windows {
			if w.Group != group || seconds <= w.StartSeconds || seconds > w.EndSeconds {
				continue
			}
			a := r.transactions[key][i]
			if a == nil {
				cells++
				if cells > maxLoadComparisonDistributionCells {
					return fmt.Errorf("transaction distribution budget exceeded; narrow the comparison")
				}
				a = &baselineTransactionAccumulator{digest: engine.NewTDigest(100), min: math.Inf(1)}
				r.transactions[key][i] = a
			}
			a.count++
			if tx.ServerTelemetry != nil {
				if request.Population == "action" {
					protocolmetrics.MergeMessagingTelemetry(&a.messaging, tx.ServerTelemetry, tx.DependencyTarget, tx.DependencyType, tx.Group)
				}
				protocolmetrics.MergeGRPCDiagnostic(&a.grpc, tx.ServerTelemetry.GRPC)
				protocolmetrics.MergeWebSocketDiagnostic(&a.websocket, tx.ServerTelemetry.WebSocket)
			}
			if !tx.Success {
				a.errors++
				continue
			}
			a.total += tx.DurationMs
			a.min = math.Min(a.min, tx.DurationMs)
			a.max = math.Max(a.max, tx.DurationMs)
			a.digest.Add(tx.DurationMs)
			// Bounded per-window time buckets preserve a useful elapsed-time latency chart.
			bucket := int((seconds - w.StartSeconds) / math.Max(1, (w.EndSeconds-w.StartSeconds)/60))
			if bucket >= 60 {
				bucket = 59
			}
			for len(a.points) <= bucket {
				a.points = append(a.points, BaselineMetricPoint{})
			}
			point := &a.points[bucket]
			point.Seconds = w.StartSeconds + float64(bucket)*math.Max(1, (w.EndSeconds-w.StartSeconds)/60)
			point.Value = math.Max(point.Value, tx.DurationMs)
			point.Load = w.Load
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(r.windows) == 0 {
		r.warnings = append(r.warnings, "No stable observed load window contains 60 seconds after the 30-second settling period")
	}
	if err = s.readBaselineResources(ctx, record, r, started, p); err != nil {
		return nil, err
	}
	return r, nil
}

func baselineTransactionMeasurement(r *baselineComparisonRun, k baselineTransactionKey, index int) *BaselineMeasurement {
	a := r.transactions[k][index]
	if a == nil {
		return nil
	}
	w := r.windows[index]
	seconds := w.EndSeconds - w.StartSeconds
	m := &BaselineMeasurement{Samples: a.count, Successes: a.count - a.errors, Errors: a.errors, Throughput: float64(a.count) / seconds, SuccessfulThroughput: float64(a.count-a.errors) / seconds, ErrorPct: 100 * float64(a.errors) / float64(a.count), CoveragePct: 100, Instances: 1}
	m.Messaging = a.messaging.Clone()
	m.GRPC = a.grpc.Clone()
	m.WebSocket = a.websocket.Clone()
	if a.digest.Count() > 0 {
		m.Mean = a.total / float64(m.Successes)
		m.Min = a.min
		m.Max = a.max
		m.P50 = a.digest.Quantile(.5)
		m.P90 = a.digest.Quantile(.9)
		m.P95 = a.digest.Quantile(.95)
		m.P99 = a.digest.Quantile(.99)
	}
	for _, point := range a.points {
		if point.Load > 0 {
			m.Points = append(m.Points, point)
		}
	}
	for _, sla := range r.manifest.TransactionResponseTimeSLAResults {
		originalGroup := k.group
		if original := r.originalGroups[k.group]; original != "" {
			originalGroup = original
		}
		if r.population == "transaction" && sla.Group == originalGroup && sla.Transaction == k.name {
			m.SLA = append(m.SLA, sla)
		}
	}
	return m
}

func baselineWindowIndex(r *baselineComparisonRun, w *BaselineLoadWindow) int {
	if w == nil {
		return -1
	}
	for i, c := range r.windows {
		if c.Group == w.Group && c.StartSeconds == w.StartSeconds && c.EndSeconds == w.EndSeconds {
			return i
		}
	}
	return -1
}

func compareBaselineRuns(current *baselineComparisonRun, baselines []*baselineComparisonRun, request BaselineComparisonRequest, p BaselineComparisonPolicy) *BaselineComparisonSnapshot {
	out := &BaselineComparisonSnapshot{Current: current.identity, CreatedAt: time.Now().UTC(), RuleVersion: baselineComparisonRuleVersion, Request: request, Policy: p, Baselines: []BaselineRunIdentity{}, Transactions: []BaselineComparisonRow{}, Resources: []BaselineComparisonRow{}, Setup: []BaselineSetupDifference{}, Findings: []BaselineComparisonFinding{}, Warnings: append([]string{}, current.warnings...)}
	out.Telemetry = append(out.Telemetry, current.telemetry)
	currentWorkload := comparisonWorkloadKey(current, p.GroupMappings, request.Population)
	out.Workload = &BaselineWorkloadQualification{Current: currentWorkload, Baselines: map[string]rca.WorkloadQualification{}}
	qualifications := map[string][]string{}
	keys := map[baselineTransactionKey]bool{}
	for k := range current.transactions {
		keys[k] = true
	}
	for _, b := range baselines {
		out.Baselines = append(out.Baselines, b.identity)
		out.Telemetry = append(out.Telemetry, b.telemetry)
		for _, warning := range b.warnings {
			out.Warnings = append(out.Warnings, b.identity.Name+": "+warning)
		}
		workload := rca.QualifyBaseline(currentWorkload, comparisonWorkloadKey(b, p.GroupMappings, request.Population))
		workload.AchievedThroughput = &rca.AchievedThroughput{CurrentTPS: current.achievedTPS, BaselineTPS: b.achievedTPS}
		out.Workload.Baselines[b.identity.ID] = workload
		out.TelemetryComparisons = append(out.TelemetryComparisons, compareBaselineTelemetry(current, b, workload))
		differences := baselineSetupDifferences(current, b)
		out.Setup = append(out.Setup, differences...)
		for _, d := range differences {
			if d.Status != "match" {
				qualifications[b.identity.ID] = append(qualifications[b.identity.ID], d.Field+" "+d.Status)
			}
		}
		if current.incomplete || b.incomplete || archiveSLAIncomplete(current.manifest) || archiveSLAIncomplete(b.manifest) {
			qualifications[b.identity.ID] = append(qualifications[b.identity.ID], "Incomplete archive source coverage")
		}
		if b.identity.Status != "COMPLETE" {
			qualifications[b.identity.ID] = append(qualifications[b.identity.ID], "Baseline run did not complete")
		}
		for k := range b.transactions {
			keys[k] = true
		}
	}
	if len(keys)*max(1, len(current.windows)) > maxBaselineComparisonCells || len(keys)*max(1, len(current.windows))*len(baselines)*max(3, len(request.RegressionMetrics)) > 20000 {
		return nil
	}
	ordered := make([]baselineTransactionKey, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool { return fmt.Sprint(ordered[i]) < fmt.Sprint(ordered[j]) })
	for _, key := range ordered {
		emitted := false
		for i, w := range current.windows {
			if w.Group != key.group {
				continue
			}
			row := BaselineComparisonRow{ID: "tx/" + strconv.Itoa(len(out.Transactions)), Name: key.name, Group: key.group, Protocol: key.protocol, Source: request.Population, Unit: "ms", Window: &w, Current: baselineTransactionMeasurement(current, key, i), Baselines: map[string]BaselineComparisonCell{}}
			for _, b := range baselines {
				cell := BaselineComparisonCell{Qualification: append([]string{}, qualifications[b.identity.ID]...)}
				bw := matchingBaselineWindow(w, b.windows, p.LoadTolerancePct)
				cell.Window = bw
				if bw == nil {
					cell.Qualification = append(cell.Qualification, "No matching stable observed load")
				} else {
					cell.Measurement = baselineTransactionMeasurement(b, key, baselineWindowIndex(b, bw))
					cell.Approximate = math.Abs(w.Load-bw.Load) > 1e-9
					cell.Qualification = append(cell.Qualification, baselineWindowQualification(w, *bw, p.LoadTolerancePct)...)
				}
				if row.Current == nil || cell.Measurement == nil {
					cell.Qualification = append(cell.Qualification, "Transaction absent or has no samples in the selected window")
				} else {
					if row.Current.Successes > 0 && cell.Measurement.Successes > 0 {
						cell.Delta, cell.DeltaPct = baselineDelta(row.Current.P95, cell.Measurement.P95)
					}
					if row.Current.Successes < 100 || cell.Measurement.Successes < 100 {
						cell.Qualification = append(cell.Qualification, "Fewer than 100 successful transaction samples")
					}
				}
				row.Baselines[b.identity.ID] = cell
			}
			out.Transactions = append(out.Transactions, row)
			emitted = true
		}
		if !emitted {
			row := BaselineComparisonRow{ID: "tx/" + strconv.Itoa(len(out.Transactions)), Name: key.name, Group: key.group, Protocol: key.protocol, Source: request.Population, Unit: "ms", Baselines: map[string]BaselineComparisonCell{}}
			for _, b := range baselines {
				row.Baselines[b.identity.ID] = BaselineComparisonCell{Qualification: []string{"No comparable current-run transaction window"}}
			}
			out.Transactions = append(out.Transactions, row)
		}
	}
	out.Resources = compareBaselineResources(current, baselines, p, qualifications)
	if out.Resources == nil {
		return nil
	}
	for _, b := range baselines {
		for _, row := range protocolmetrics.CompareBrowserDocumentSummaries(current.browserDocuments, b.browserDocuments) {
			row.BaselineID = b.identity.ID
			out.BrowserDocuments = append(out.BrowserDocuments, row)
		}
	}
	if len(out.BrowserDocuments) > 0 {
		out.BrowserSoftNavigation = protocolmetrics.BrowserSoftNavigationUnsupported
		out.Warnings = append(out.Warnings, "Browser document metrics compare the p75 of qualified final document measurements per tuple over each whole run (not the selected measurement windows or load range); they are not part of the regression verdict. Soft (same-document) navigations are not measured.")
	}
	buildBaselineFindings(out)
	out.Verdict = evaluateBaselineRegression(out)
	return out
}

// Only disclosure-safe metadata is displayed. Runtime settings are compared as
// serialized values but never returned: they can contain credential references.
func baselineSetupDifferences(a, b *baselineComparisonRun) []BaselineSetupDifference {
	left, right := maskRunArchiveProvenance(a.manifest.Provenance), maskRunArchiveProvenance(b.manifest.Provenance)
	if a.manifest.InitialProvenance != nil {
		left = maskRunArchiveProvenance(*a.manifest.InitialProvenance)
	}
	if b.manifest.InitialProvenance != nil {
		right = maskRunArchiveProvenance(*b.manifest.InitialProvenance)
	}
	result := []BaselineSetupDifference{}
	add := func(field string, x, y any, known bool, show bool) {
		sx, _ := json.Marshal(x)
		sy, _ := json.Marshal(y)
		status := "match"
		if !known {
			status = "unverified"
		} else if string(sx) != string(sy) {
			status = "differs"
		}
		if status == "match" {
			return
		}
		cx, cy := string(sx), string(sy)
		if len(cx) > 1000 {
			cx = cx[:1000] + "… (open run evidence)"
		}
		if len(cy) > 1000 {
			cy = cy[:1000] + "… (open run evidence)"
		}
		if !show {
			cx, cy = "Recorded (open run evidence)", "Recorded (open run evidence)"
		}
		if !known {
			cx, cy = "Not fully recorded", "Not fully recorded"
		}
		result = append(result, BaselineSetupDifference{b.identity.ID, field, cx, cy, status})
	}
	add("Scenario identity", left.ScenarioID, right.ScenarioID, left.ScenarioID != "" && right.ScenarioID != "", true)
	add("Configured VUsers", left.TotalVUsers, right.TotalVUsers, left.TotalVUsers > 0 && right.TotalVUsers > 0, true)
	add("Duration", left.Duration, right.Duration, left.Duration != "" && right.Duration != "", true)
	groupFields := func(groups []TestGroup) map[string]any {
		fields := map[string]any{}
		for _, g := range groups {
			prefix := "Group " + g.Name + " / "
			fields[prefix+"protocol"] = g.Protocol
			fields[prefix+"script identity"] = g.ScriptID
			fields[prefix+"VUsers"] = g.VUsers
			fields[prefix+"role"] = g.Role
			fields[prefix+"start offset"] = g.StartOffset
			fields[prefix+"duration"] = g.TargetDuration
			fields[prefix+"ramp up"] = g.RampUp
			fields[prefix+"ramp down"] = g.RampDown
			fields[prefix+"load stages"] = g.Stages
			fields[prefix+"iterations"] = g.Iterations
			fields[prefix+"initialization"] = g.Initialization
			fields[prefix+"generator placement"] = g.Assignment
			if g.RuntimeSettings != nil {
				encoded, _ := json.Marshal(g.RuntimeSettings)
				var values map[string]any
				_ = json.Unmarshal(encoded, &values)
				for key, value := range values {
					fields[prefix+"runtime "+key] = value
				}
			}
		}
		return fields
	}
	af, bf := groupFields(left.Groups), groupFields(right.Groups)
	fields := map[string]bool{}
	for field := range af {
		fields[field] = true
	}
	for field := range bf {
		fields[field] = true
	}
	names := make([]string, 0, len(fields))
	for field := range fields {
		names = append(names, field)
	}
	sort.Strings(names)
	for _, field := range names {
		av, aok := af[field]
		bv, bok := bf[field]
		add(field, av, bv, aok && bok, true)
	}
	add("Scenario mode", left.ScenarioMode, right.ScenarioMode, true, true)
	add("Termination mode", left.TerminationMode, right.TerminationMode, true, true)
	add("Start delay", left.StartDelay, right.StartDelay, true, true)
	add("Resource baseline phase", left.ResourceBaseline, right.ResourceBaseline, true, true)
	add("Shakeout phase", left.ScriptShakeout, right.ScriptShakeout, true, true)
	add("Scenario runtime overrides", left.RuntimeSettings, right.RuntimeSettings, a.manifest.InitialProvenance != nil && b.manifest.InitialProvenance != nil, false)
	add("Traffic profile", left.TrafficProfile, right.TrafficProfile, true, false)
	add("Generator assignment", left.Assignment, right.Assignment, left.Assignment != nil && right.Assignment != nil, false)
	add("Generator capacity and versions", a.manifest.Provenance.GeneratorInventory, b.manifest.Provenance.GeneratorInventory, len(a.manifest.Provenance.GeneratorInventory) > 0 && len(b.manifest.Provenance.GeneratorInventory) > 0, false)
	add("Execution configuration fingerprint", left.ComparisonFingerprint, right.ComparisonFingerprint, left.ComparisonFingerprint != "" && right.ComparisonFingerprint != "", true)
	add("Environment", left.ControllerEnv["APEXLOAD_ENV"], right.ControllerEnv["APEXLOAD_ENV"], left.ControllerEnv["APEXLOAD_ENV"] != "" && right.ControllerEnv["APEXLOAD_ENV"] != "", true)
	add("Current run group configuration changes", groupFields(left.Groups), groupFields(maskRunArchiveProvenance(a.manifest.Provenance).Groups), a.manifest.InitialProvenance != nil, true)
	add("Baseline group configuration changes", groupFields(right.Groups), groupFields(maskRunArchiveProvenance(b.manifest.Provenance).Groups), b.manifest.InitialProvenance != nil, true)
	add("Current run runtime changes", left.RuntimeSettings, a.manifest.Provenance.RuntimeSettings, a.manifest.InitialProvenance != nil, false)
	add("Baseline runtime changes", right.RuntimeSettings, b.manifest.Provenance.RuntimeSettings, b.manifest.InitialProvenance != nil, false)
	return result
}

func buildBaselineFindings(out *BaselineComparisonSnapshot) {
	for _, d := range out.Setup {
		out.Findings = append(out.Findings, BaselineComparisonFinding{BaselineID: d.BaselineID, Category: "setup", Metric: d.Field, Message: d.Field + " " + d.Status, Status: "review"})
	}
	evaluate := func(rows []BaselineComparisonRow, resource bool) {
		for _, row := range rows {
			for _, b := range out.Baselines {
				cell, ok := row.Baselines[b.ID]
				if !ok {
					continue
				}
				category := "transaction"
				if resource {
					category = "resource"
				}
				if row.Current == nil || cell.Measurement == nil {
					out.Findings = append(out.Findings, BaselineComparisonFinding{b.ID, row.ID, category, "coverage", "Unmatched or unavailable evidence for " + row.Name, "unverified"})
					continue
				}
				a, v := row.Current, cell.Measurement
				add := func(metric string, x, y, percent, absolute float64, enough bool) {
					delta, pct := baselineDelta(x, y)
					changed := math.Abs(*delta) >= absolute && *delta != 0 && (pct != nil && math.Abs(*pct) >= percent || pct == nil && absolute > 0)
					if !changed {
						return
					}
					status := "changed"
					if !enough || len(cell.Qualification) > 0 {
						status = "unverified"
					}
					unit := row.Unit
					if metric == "throughput" || metric == "successful throughput" || metric == "WebSocket message throughput" {
						unit = "/s"
					}
					if metric == "consumer_offset_lag" {
						unit = "offsets"
					}
					if metric == "error percentage points" {
						unit = "percentage points"
					}
					change := fmt.Sprintf("%+.3g %s", *delta, unit)
					if pct != nil {
						change += fmt.Sprintf(" (%+.1f%%)", *pct)
					} else {
						change += " (percentage unavailable: baseline zero)"
					}
					message := fmt.Sprintf("%s: %s %s at %.0f vs %.0f VUsers", row.Group+" / "+row.Name, metric, change, row.Window.Load, cell.Window.Load)
					if status == "unverified" {
						message += "; limited comparability"
					}
					out.Findings = append(out.Findings, BaselineComparisonFinding{b.ID, row.ID, category, metric, message, status})
				}
				if resource {
					if cell.Delta == nil {
						continue
					}
					absolute := 0.0
					for _, m := range out.Policy.ResourceMappings {
						if m.Confirmed && (m.RunID == "*" || m.RunID == out.Current.ID || m.RunID == b.ID) && (m.Identity == row.Name || "mapped/"+m.Identity == row.Name) {
							_, factor := canonicalBaselineUnit(m.Unit)
							absolute = math.Max(absolute, m.AbsoluteThreshold*factor)
						}
					}
					add("time-weighted mean", a.Mean, v.Mean, out.Policy.ResourcePct, absolute, a.Samples >= 10 && v.Samples >= 10 && a.CoveragePct >= 80 && v.CoveragePct >= 80)
				} else {
					enough := a.Successes >= 100 && v.Successes >= 100
					compareDistribution := func(metric string, current, baseline protocolmetrics.DiagnosticDistribution) {
						x, okX := current.Quantile(.95)
						y, okY := baseline.Quantile(.95)
						if okX && okY {
							add(metric, x, y, out.Policy.LatencyPct, out.Policy.LatencyMs, current.Count >= 100 && baseline.Count >= 100)
						}
					}
					if a.GRPC != nil && v.GRPC != nil && !a.GRPC.MixedMethods && !v.GRPC.MixedMethods && a.GRPC.Method == v.GRPC.Method && a.GRPC.Mode == v.GRPC.Mode {
						compareDistribution("gRPC call P95", a.GRPC.Call, v.GRPC.Call)
						compareDistribution("gRPC first-message P95", a.GRPC.FirstMessage, v.GRPC.FirstMessage)
						compareDistribution("gRPC protobuf encode P95", a.GRPC.Encode, v.GRPC.Encode)
						compareDistribution("gRPC protobuf decode P95", a.GRPC.Decode, v.GRPC.Decode)
					}
					if a.WebSocket != nil && v.WebSocket != nil {
						compareDistribution("WebSocket handshake P95", a.WebSocket.Handshake, v.WebSocket.Handshake)
						compareDistribution("WebSocket write-wait P95", a.WebSocket.WriteWait, v.WebSocket.WriteWait)
						compareDistribution("WebSocket matched ping RTT P95", a.WebSocket.PingRTT, v.WebSocket.PingRTT)
						compareDistribution("WebSocket text receive-wait P95", a.WebSocket.TextReceive, v.WebSocket.TextReceive)
						compareDistribution("WebSocket binary receive-wait P95", a.WebSocket.BinaryReceive, v.WebSocket.BinaryReceive)
						if row.Window != nil && cell.Window != nil {
							currentSeconds := row.Window.EndSeconds - row.Window.StartSeconds
							baselineSeconds := cell.Window.EndSeconds - cell.Window.StartSeconds
							if currentSeconds > 0 && baselineSeconds > 0 {
								add("WebSocket message throughput", float64(a.WebSocket.MessagesReceived)/currentSeconds, float64(v.WebSocket.MessagesReceived)/baselineSeconds, out.Policy.ThroughputPct, 0, a.WebSocket.MessagesReceived >= 100 && v.WebSocket.MessagesReceived >= 100 && a.WebSocket.Samples == a.Samples && v.WebSocket.Samples == v.Samples)
							}
						}
					}
					if a.Successes > 0 && v.Successes > 0 {
						add("successful P95", a.P95, v.P95, out.Policy.LatencyPct, out.Policy.LatencyMs, enough)
						add("successful mean", a.Mean, v.Mean, out.Policy.LatencyPct, out.Policy.LatencyMs, enough)
						add("successful P99", a.P99, v.P99, out.Policy.LatencyPct, out.Policy.LatencyMs, a.Successes >= 1000 && v.Successes >= 1000)
					}
					for _, metric := range []string{"consumer_lag_ms", "consumer_offset_lag", "acknowledgement_ms"} {
						x, okX := baselineRegressionValue(a, metric)
						y, okY := baselineRegressionValue(v, metric)
						if okX && okY {
							absolute := out.Policy.LatencyMs
							if metric == "consumer_offset_lag" {
								absolute = 0
							}
							add(metric, x, y, out.Policy.LatencyPct, absolute, true)
						}
					}
					add("successful throughput", a.SuccessfulThroughput, v.SuccessfulThroughput, out.Policy.ThroughputPct, 0, a.Samples >= 100 && v.Samples >= 100)
					add("throughput", a.Throughput, v.Throughput, out.Policy.ThroughputPct, 0, a.Samples >= 100 && v.Samples >= 100)
					add("error percentage points", a.ErrorPct, v.ErrorPct, 0, out.Policy.ErrorPoints, a.Samples >= 100 && v.Samples >= 100)
				}
			}
		}
	}
	evaluate(out.Transactions, false)
	evaluate(out.Resources, true)
	sort.SliceStable(out.Findings, func(i, j int) bool {
		rank := func(s string) int {
			if s == "changed" {
				return 0
			}
			if s == "review" {
				return 1
			}
			return 2
		}
		return rank(out.Findings[i].Status) < rank(out.Findings[j].Status)
	})
	if len(out.Findings) == 0 {
		out.Warnings = append(out.Warnings, "No configured change threshold was exceeded; this is not an acceptance verdict")
	}
	out.Warnings = append(out.Warnings, "Transaction latency uses successful samples. Protocol timing includes every measured call, including failed calls; message throughput requires diagnostics for every retained action. Transaction chart points show interval maxima. SLA results describe the whole recorded run. Resource changes are contemporaneous evidence, not causal attribution.")
}

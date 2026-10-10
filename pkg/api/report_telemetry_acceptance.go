package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// The telemetry acceptance report section (C7): the run's telemetry verdict
// beside, never inside, the application verdict, with the evidence it read.
// HTML, PDF, CSV and (through HTML) DOCX all render this one view.

type telemetryAcceptanceReportView struct {
	Verdict *TelemetryAcceptanceVerdict
	// Summary is one sentence stating the verdict, or why there is none.
	Summary    string
	Pins       []RunPolicyPin
	Criteria   []telemetryAcceptanceReportCriterion
	Loss       []telemetryAcceptanceReportLoss
	Coverage   string
	Sources    []telemetryAcceptanceReportSource
	Breach     string
	Clock      string
	Stages     []telemetryAcceptanceReportStage
	Exclusions []string
}

type telemetryAcceptanceReportCriterion struct {
	Criterion, Outcome, Observed, Threshold, Bound, Basis, Reason string
	// BasisLabel is Basis with grouped counts, for readers; the CSV keeps Basis.
	BasisLabel string
}

// CriterionLabel, OutcomeLabel and BoundLabel are the row as a reader sees
// it; the CSV keeps the identifiers.
func (c telemetryAcceptanceReportCriterion) CriterionLabel() string {
	if rest, ok := strings.CutPrefix(c.Criterion, "stage_lag:"); ok {
		stage, percentile, _ := strings.Cut(rest, "/")
		return telemetryReportLabel(stage) + " lag, " + strings.ToUpper(percentile) + " (maximum)"
	}
	return telemetryReportLabel(c.Criterion)
}
func (c telemetryAcceptanceReportCriterion) OutcomeLabel() string { return reportTokenLabel(c.Outcome) }
func (c telemetryAcceptanceReportCriterion) BoundLabel() string   { return telemetryReportLabel(c.Bound) }

type telemetryAcceptanceReportLoss struct {
	Counter, State, Value, Reason string
}

// Label, StateLabel and ReasonLabel are the row as a reader sees it; the CSV
// keeps the identifiers.
func (l telemetryAcceptanceReportLoss) Label() string      { return telemetryReportLabel(l.Counter) }
func (l telemetryAcceptanceReportLoss) StateLabel() string { return telemetryReportLabel(l.State) }
func (l telemetryAcceptanceReportLoss) ReasonLabel() string {
	if l.Reason == "" {
		return ""
	}
	return telemetryReportLabel(l.Reason)
}

type telemetryAcceptanceReportSource struct {
	Source, Observed string
}

type telemetryAcceptanceReportStage struct {
	Stage, Availability, Samples, P50, P95, P99, Max, Excluded string
	// AvailabilityLabel and SamplesLabel are for readers; the CSV keeps the
	// raw values above.
	AvailabilityLabel, SamplesLabel, ExcludedLabel string
}

// Label is the stage as a reader sees it; the CSV keeps the identifier.
func (s telemetryAcceptanceReportStage) Label() string { return telemetryReportLabel(s.Stage) }

// telemetryReportLabels names telemetry counters, stages, states and reasons
// for readers of a report.
var telemetryReportLabels = map[string]string{
	TelemetryCounterDroppedResults:           "Dropped results",
	TelemetryCounterShedArrivals:             "Shed arrivals",
	TelemetryCounterDroppedSubscriberBatches: "Dropped subscriber batches",
	TelemetryCounterLostPublications:         "Lost publications",
	TelemetryCounterWebsocketDroppedMessages: "Dashboard WebSocket messages dropped",
	TelemetryCounterWebsocketDroppedEvents:   "Dashboard WebSocket events dropped",
	TelemetryCounterExact:                    "Exact",
	TelemetryCounterLowerBound:               "Lower bound",
	TelemetryCounterUnavailable:              "Unavailable",
	"controller_wide":                        "Controller-wide, not attributed to a run",
	"generator_processing":                   "Load generator processing",
	"transport":                              "Transport",
	"controller_fanout":                      "Controller fan-out",
	"sample_age":                             "Sample age",
	"browser_render":                         "Browser render",
	"clock_unqualified":                      "clock not qualified",
	"telemetry_gaps":                         "Telemetry gaps",
	"min_wall_time_coverage":                 "Wall-time coverage (minimum)",
	"max_lost_records":                       "Lost records (maximum)",
	"max_loss_ratio":                         "Loss ratio (maximum)",
	"max_breached_known_time_fraction":       "Breached share of known time (maximum)",
	"telemetry_acceptance_policy":            "Telemetry acceptance policy",
}

// telemetryReportLabel names a telemetry identifier for readers.
func telemetryReportLabel(id string) string {
	if label, ok := telemetryReportLabels[id]; ok {
		return label
	}
	return reportTokenLabel(id)
}

// telemetryExclusionText rewrites an exclusion's leading identifier
// ("shedded_arrivals: …") as its label.
func telemetryExclusionText(text string) string {
	if head, rest, ok := strings.Cut(text, ": "); ok {
		switch head {
		case TelemetryCounterShedArrivals:
			return telemetryReportLabel(head) + ": " + rest
		case "websocket drops":
			return "Dashboard WebSocket drops: " + rest
		}
	}
	return text
}

// TelemetryAcceptanceView is the section's view of the report.
func (d *runArchiveHTMLReportData) TelemetryAcceptanceView() telemetryAcceptanceReportView {
	if d == nil || d.Report == nil {
		return telemetryAcceptanceReportView{Summary: "No archive manifest is available."}
	}
	return buildTelemetryAcceptanceReportView(d.Report.Manifest, d.Report.TelemetryAcceptance)
}

func buildTelemetryAcceptanceReportView(manifest *RunArchiveManifest, verdict *TelemetryAcceptanceVerdict) telemetryAcceptanceReportView {
	view := telemetryAcceptanceReportView{Verdict: verdict, Exclusions: append([]string(nil), telemetryAcceptanceExclusions...)}
	if manifest == nil {
		view.Summary = "No archive manifest is available."
		return view
	}
	for _, pin := range manifest.Provenance.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindTelemetryPolicy || pin.Kind == storage.AnalysisDefinitionKindTelemetryAcceptancePolicy {
			view.Pins = append(view.Pins, pin)
		}
	}
	switch {
	case verdict == nil:
		view.Summary = "No telemetry acceptance policy was pinned when this run started, so it has no telemetry verdict. The evidence below is recorded either way."
	default:
		policy := "an unnamed policy"
		if verdict.Policy != nil {
			policy = fmt.Sprintf("%s revision %d", verdict.Policy.Name, verdict.Policy.Revision)
		}
		effect := "advisory: it never changes a CI outcome"
		switch verdict.Mode {
		case "fail_ci":
			effect = "fail_ci: it can fail an otherwise passing CI run"
		case telemetryAcceptanceModeUnknown:
			effect = "mode not retained"
		}
		view.Summary = fmt.Sprintf("Telemetry verdict %s under %s (%s). This is separate from the application verdict.", strings.ToUpper(verdict.Outcome), policy, effect)
		for _, criterion := range verdict.Criteria {
			view.Criteria = append(view.Criteria, telemetryAcceptanceReportCriterionOf(criterion))
		}
		if len(verdict.Exclusions) > 0 {
			view.Exclusions = append([]string(nil), verdict.Exclusions...)
		}
	}
	view.Loss = telemetryAcceptanceReportLossRows(manifest.TelemetryLoss)
	if record := manifest.TelemetryCoverage; record != nil {
		coverage := record.Coverage
		view.Coverage = fmt.Sprintf("Observed %s of %s ms (%s); unknown %s ms.", formatReportCount(coverage.ObservedMs), formatReportCount(coverage.WindowMs), formatAcceptanceRatio(safeAcceptanceRatio(coverage.ObservedMs, coverage.WindowMs)), formatReportCount(coverage.UnknownMs))
		if coverage.Truncated {
			view.Coverage += " Observed time is a lower bound: a source's spans exceeded their bound."
		}
		switch coverage.ObserverLost {
		case telemetryCoverageLedgerLost:
			view.Coverage += " Observed time is a lower bound: the run was finalized without the record of what was observed (after a restart, or by a controller that never executed it)."
		case telemetryCoverageAdopted:
			view.Coverage += " Observed time is a lower bound: a controller adopted the run and observed it only from then."
		}
		for _, source := range coverage.Sources {
			observed := fmt.Sprintf("%s ms observed; required for %s ms, unknown %s ms", formatReportCount(source.ObservedMs), formatReportCount(source.RequiredMs), formatReportCount(source.UnknownMs))
			if !coverage.SourceRequirements {
				// Coverage recorded before each source's requirement was.
				observed = fmt.Sprintf("%s of %s ms", formatReportCount(source.ObservedMs), formatReportCount(coverage.WindowMs))
			}
			view.Sources = append(view.Sources, telemetryAcceptanceReportSource{Source: source.Source, Observed: observed})
		}
		view.Breach = fmt.Sprintf("Classified %s ms: healthy %s, degraded %s, breached %s; unknown %s ms.", formatReportCount(record.ClassifiedMs), formatReportCount(record.HealthyMs), formatReportCount(record.DegradedMs), formatReportCount(record.BreachedMs), formatReportCount(record.UnknownMs))
		if record.BreachedKnownFraction != nil {
			view.Breach += fmt.Sprintf(" Breached known-time fraction %s (breached ÷ classified).", formatAcceptanceRatio(*record.BreachedKnownFraction))
		}
	} else {
		view.Coverage = "Wall-time coverage was not recorded for this run."
		view.Breach = "Classified telemetry time was not recorded for this run."
	}
	view.Clock = "No generator clock observations were retained; cross-host stages count only samples taken while a generator's clock was qualified."
	if manifest.ClockObservationCount > 0 {
		view.Clock = fmt.Sprintf("%d generator clock observations retained; transport and a distributed run's sample age count only samples taken while the generator's clock was qualified (others are excluded as clock_unqualified).", manifest.ClockObservationCount)
		if manifest.ClockObservationsTruncated {
			view.Clock += " The clock observations were truncated."
		}
	}
	for _, stage := range manifest.TelemetryStageDistributions {
		view.Stages = append(view.Stages, telemetryAcceptanceReportStageOf(stage))
	}
	return view
}

func telemetryAcceptanceReportCriterionOf(criterion TelemetryAcceptanceCriterion) telemetryAcceptanceReportCriterion {
	outcome := criterion.Outcome
	if criterion.Advisory {
		outcome += " (advisory, never decides)"
	}
	row := telemetryAcceptanceReportCriterion{Criterion: criterion.Criterion, Outcome: outcome, Observed: "unknown", Bound: criterion.Bound, Reason: criterion.Reason,
		Threshold: criterion.Comparator + " " + formatCITelemetryValue(criterion.Threshold, criterion.Unit)}
	if criterion.Observed != nil {
		row.Observed = formatCITelemetryValue(*criterion.Observed, criterion.Unit)
	}
	switch {
	case criterion.Numerator != nil && criterion.Denominator != nil:
		row.Basis = fmt.Sprintf("%.0f / %.0f", *criterion.Numerator, *criterion.Denominator)
		row.BasisLabel = groupThousands(fmt.Sprintf("%.0f", *criterion.Numerator)) + " of " + groupThousands(fmt.Sprintf("%.0f", *criterion.Denominator))
	case criterion.Denominator != nil:
		row.Basis = fmt.Sprintf("%.0f samples", *criterion.Denominator)
		row.BasisLabel = reportCountNoun(int64(*criterion.Denominator), "sample", "samples")
	}
	return row
}

func telemetryAcceptanceReportLossRows(loss *TelemetryLossAccounting) []telemetryAcceptanceReportLoss {
	if loss == nil {
		return nil
	}
	var rows []telemetryAcceptanceReportLoss
	for _, counter := range []struct {
		name  string
		total *TelemetryCounterTotal
	}{
		{TelemetryCounterDroppedResults, loss.DroppedResults}, {TelemetryCounterShedArrivals, loss.ShedArrivals},
		{TelemetryCounterDroppedSubscriberBatches, loss.DroppedSubscriberBatches}, {TelemetryCounterLostPublications, loss.LostPublications},
		{TelemetryCounterWebsocketDroppedMessages, loss.WebsocketDroppedMessages}, {TelemetryCounterWebsocketDroppedEvents, loss.WebsocketDroppedEvents},
	} {
		if counter.total == nil {
			continue
		}
		value := "n/a"
		if counter.total.State != TelemetryCounterUnavailable {
			value = fmt.Sprint(counter.total.Value)
			if counter.total.State == TelemetryCounterLowerBound {
				value = "≥ " + value
			}
		}
		rows = append(rows, telemetryAcceptanceReportLoss{Counter: counter.name, State: counter.total.State, Value: value, Reason: counter.total.Reason})
	}
	return rows
}

func telemetryAcceptanceReportStageOf(stage TelemetryStageDistribution) telemetryAcceptanceReportStage {
	format := func(value *float64) string {
		if value == nil {
			return "n/a"
		}
		return fmt.Sprintf("%.1f", *value)
	}
	row := telemetryAcceptanceReportStage{Stage: stage.Stage, Availability: stage.Availability, Samples: fmt.Sprint(stage.Samples),
		AvailabilityLabel: reportTokenLabel(stage.Availability), SamplesLabel: formatReportCount(int64(stage.Samples)),
		P50: format(stage.P50Ms), P95: format(stage.P95Ms), P99: format(stage.P99Ms), Max: format(stage.MaxMs)}
	if stage.Advisory {
		row.Availability += " (advisory)"
		row.AvailabilityLabel += " (advisory)"
	}
	if stage.Reason != "" {
		row.Availability += ": " + stage.Reason
		row.AvailabilityLabel += ": " + telemetryReportLabel(stage.Reason)
	}
	reasons := make([]string, 0, len(stage.Excluded))
	for reason := range stage.Excluded {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	excluded := make([]string, 0, len(reasons))
	labels := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		excluded = append(excluded, fmt.Sprintf("%s %d", reason, stage.Excluded[reason]))
		labels = append(labels, fmt.Sprintf("%s %d", telemetryReportLabel(reason), stage.Excluded[reason]))
	}
	row.Excluded = strings.Join(excluded, ", ")
	row.ExcludedLabel = strings.Join(labels, ", ")
	return row
}

func safeAcceptanceRatio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func telemetryAcceptanceReportPDF(d *runArchiveHTMLReportData, lines *[]string) {
	view := d.TelemetryAcceptanceView()
	pdfHeading(lines, reportSectionTitle("telemetry_acceptance"))
	*lines = append(*lines, view.Summary)
	for _, pin := range view.Pins {
		*lines = append(*lines, fmt.Sprintf("Policy pin: %s %s revision %d (sha256 %s)", telemetryReportLabel(pin.Kind), pin.Name, pin.Revision, pin.SHA256))
	}
	for _, criterion := range view.Criteria {
		line := fmt.Sprintf("- %s: %s (observed %s, %s; threshold %s)", criterion.CriterionLabel(), criterion.OutcomeLabel(), criterion.Observed, strings.ToLower(criterion.BoundLabel()), criterion.Threshold)
		if criterion.BasisLabel != "" {
			line += "; basis " + criterion.BasisLabel
		}
		if criterion.Reason != "" {
			line += "; " + criterion.Reason
		}
		*lines = append(*lines, line)
	}
	for _, loss := range view.Loss {
		*lines = append(*lines, fmt.Sprintf("Loss %s: %s (%s)%s", loss.Label(), loss.Value, loss.StateLabel(), reportReasonSuffix(loss.ReasonLabel())))
	}
	*lines = append(*lines, "Coverage: "+view.Coverage, "Classified time: "+view.Breach, "Clock quality: "+view.Clock)
	for _, stage := range view.Stages {
		line := fmt.Sprintf("Stage %s: %s; samples %s; P50 %s P95 %s P99 %s max %s ms", stage.Label(), stage.AvailabilityLabel, stage.SamplesLabel, stage.P50, stage.P95, stage.P99, stage.Max)
		if stage.ExcludedLabel != "" {
			line += "; excluded " + stage.ExcludedLabel
		}
		*lines = append(*lines, line)
	}
	for _, exclusion := range view.Exclusions {
		*lines = append(*lines, "Excluded: "+telemetryExclusionText(exclusion))
	}
	*lines = append(*lines, "")
}

func reportReasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func telemetryAcceptanceReportCSV(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	view := d.TelemetryAcceptanceView()
	outcome, mode := "not_evaluated", ""
	if view.Verdict != nil {
		outcome, mode = view.Verdict.Outcome, view.Verdict.Mode
	}
	rows := [][4]string{{"telemetry_acceptance", "", "outcome", outcome}, {"telemetry_acceptance", "", "mode", mode}, {"telemetry_acceptance", "", "summary", view.Summary}}
	for _, pin := range view.Pins {
		rows = append(rows, [4]string{"telemetry_policy_pin", pin.Kind, "revision", fmt.Sprintf("%s@%d sha256:%s", pin.ID, pin.Revision, pin.SHA256)})
	}
	for _, criterion := range view.Criteria {
		for _, item := range [][2]string{{"outcome", criterion.Outcome}, {"observed", criterion.Observed}, {"bound", criterion.Bound}, {"threshold", criterion.Threshold}, {"basis", criterion.Basis}, {"reason", criterion.Reason}} {
			rows = append(rows, [4]string{"telemetry_acceptance_criterion", criterion.Criterion, item[0], item[1]})
		}
	}
	for _, loss := range view.Loss {
		rows = append(rows, [4]string{"telemetry_loss", loss.Counter, "value", loss.Value}, [4]string{"telemetry_loss", loss.Counter, "state", loss.State})
		if loss.Reason != "" {
			rows = append(rows, [4]string{"telemetry_loss", loss.Counter, "reason", loss.Reason})
		}
	}
	rows = append(rows, [4]string{"telemetry_coverage", "", "summary", view.Coverage}, [4]string{"telemetry_coverage", "", "classified", view.Breach}, [4]string{"telemetry_clock", "", "quality", view.Clock})
	for _, stage := range view.Stages {
		for _, item := range [][2]string{{"availability", stage.Availability}, {"samples", stage.Samples}, {"p50_ms", stage.P50}, {"p95_ms", stage.P95}, {"p99_ms", stage.P99}, {"max_ms", stage.Max}, {"excluded", stage.Excluded}} {
			rows = append(rows, [4]string{"telemetry_stage", stage.Stage, item[0], item[1]})
		}
	}
	for index, exclusion := range view.Exclusions {
		rows = append(rows, [4]string{"telemetry_acceptance_exclusion", fmt.Sprint(index), "exclusion", exclusion})
	}
	for _, row := range rows {
		if err := write(row[0], row[1], row[2], row[3]); err != nil {
			return err
		}
	}
	return nil
}

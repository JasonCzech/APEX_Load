package api

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Phase attribution limitations: a deterministic build failure of a frozen
// archive (unavailable, reusable) and a build the read time or a possibly
// transient error stopped (interrupted, which rca.CutByBudget counts so an
// identical request is analyzed again).
const (
	limitationPhaseAttributionUnavailable = rca.LimitationPhaseAttributionUnavailable
	limitationPhaseAttributionInterrupted = rca.LimitationPhaseAttributionInterrupted
)

// rcaReportDeterministicError reports whether a report build failed for a
// reason the frozen archive itself decides: one of the report's own bounds.
func rcaReportDeterministicError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "archive report exceeds ")
}

// rcaBuildArchiveReport builds (or reads the cached) archive report the
// phase attributions come from; tests replace it.
var rcaBuildArchiveReport = func(s *Server, record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	return s.archiveReadCaches().cachedRunArchiveReport(record)
}

// rcaReportWithin builds a report within readCtx. The cached build takes no
// context, so it runs aside and the wait stops when readCtx ends: a build
// still running then finishes into the cache for the next reader.
func (s *Server) rcaReportWithin(readCtx context.Context, record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	if err := readCtx.Err(); err != nil {
		return nil, err
	}
	type built struct {
		report *RunArchiveReport
		err    error
	}
	done := make(chan built, 1)
	// The hook is read here, not in the goroutine, which may outlive the
	// wait.
	build := rcaBuildArchiveReport
	go func() {
		report, err := build(s, record)
		done <- built{report, err}
	}()
	select {
	case result := <-done:
		if result.err == nil && result.report == nil {
			result.err = errors.New("no report")
		}
		return result.report, result.err
	case <-readCtx.Done():
		return nil, readCtx.Err()
	}
}

// rcaPhaseAttributionInput reads the run's time anatomy (and, when the
// baseline is comparable, the baseline's) from their archive reports and
// returns the assessment's phase attributions. The reports are pure
// functions of the frozen archives, so the attributions are too. A report
// that cannot be built leaves its anatomy out: phase_attribution_unavailable
// when the archive decides it, phase_attribution_interrupted when the read
// time ran out or the error may be transient. Only the caller's own
// cancellation (ctx) is an error.
func (s *Server) rcaPhaseAttributionInput(ctx, readCtx context.Context, record *storage.RunArchiveRecord, baseline *rcaBaselineArchive) ([]rca.PhaseAttribution, []rca.Limitation, error) {
	unavailable := func(what string, err error) rca.Limitation {
		switch {
		case rcaReportDeterministicError(err):
			return rca.Limitation{Code: limitationPhaseAttributionUnavailable, Detail: what + " could not be built: " + err.Error()}
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
			return rca.Limitation{Code: limitationPhaseAttributionInterrupted, Detail: what + " was not built within the evidence read time"}
		}
		return rca.Limitation{Code: limitationPhaseAttributionInterrupted, Detail: what + " could not be built: " + err.Error()}
	}
	report, err := s.rcaReportWithin(readCtx, record)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	if err != nil {
		return nil, []rca.Limitation{unavailable("the run's time anatomy", err)}, nil
	}
	var limitations []rca.Limitation
	var baselineAnatomy *RunArchiveTimeAnatomy
	if baseline.comparable() && baseline.record != nil {
		baselineReport, err := s.rcaReportWithin(readCtx, baseline.record)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		if err != nil {
			limitations = append(limitations, unavailable("the baseline's time anatomy", err))
		} else {
			baselineAnatomy = baselineReport.TimeAnatomy
		}
	}
	return rcaPhaseAttributions(report.TimeAnatomy, baselineAnatomy), limitations, nil
}

// rcaPhaseAttributions turns time anatomies into phase attributions: each
// transaction's tail attribution the report could state (status ok), and
// its mean decomposition against the baseline's where both runs have one.
// The engine applies the sample and effect bounds.
func rcaPhaseAttributions(current, baseline *RunArchiveTimeAnatomy) []rca.PhaseAttribution {
	if current == nil {
		return nil
	}
	var out []rca.PhaseAttribution
	baselineByName := map[string]*RunArchiveTransactionAnatomy{}
	if baseline != nil {
		for i := range baseline.Transactions {
			baselineByName[baseline.Transactions[i].Name] = &baseline.Transactions[i]
		}
	}
	for i := range current.Transactions {
		tx := &current.Transactions[i]
		if tail := tx.Tail; tail != nil && tail.Status == "ok" {
			phases := map[string]float64{}
			for _, component := range tail.Delta.Components {
				if slices.Contains(timeAnatomyAttributedPhases, component.Component) {
					phases[component.Component] = component.DeltaMs
				}
			}
			out = append(out, rca.PhaseAttribution{
				Transaction: tx.Name, Source: signatures.PhaseSourceTail, Population: tail.Instances,
				Instances: tail.Tail.Joined, ReferenceInstances: tail.Median.Joined,
				JoinCoverage: bandCoverage(tail.Tail), ReferenceJoinCoverage: bandCoverage(tail.Median),
				ReferenceMs: tail.Median.AvgDurationMs, DeltaMs: tail.Delta.TotalMs, Phases: phases,
				Evidence: timeAnatomyEvidence(tx.Name, signatures.PhaseSourceTail),
			})
		}
		reference := baselineByName[tx.Name]
		if reference == nil || tx.Decomposition.Instances <= 0 || reference.Decomposition.Instances <= 0 {
			continue
		}
		cur, base := tx.Decomposition, reference.Decomposition
		attribution := rca.PhaseAttribution{
			Transaction: tx.Name, Source: signatures.PhaseSourceBaseline, Population: tx.Instances,
			Instances: cur.Instances, ReferenceInstances: base.Instances,
			JoinCoverage: decompositionCoverage(tx), ReferenceJoinCoverage: decompositionCoverage(reference),
			ReferenceMs: base.AvgDurationMs, DeltaMs: cur.AvgDurationMs - base.AvgDurationMs,
			Phases: map[string]float64{
				signatures.PhaseDNS:      cur.Phases.DNSMs - base.Phases.DNSMs,
				signatures.PhaseConnect:  cur.Phases.ConnectMs - base.Phases.ConnectMs,
				signatures.PhaseTLS:      cur.Phases.TLSMs - base.Phases.TLSMs,
				signatures.PhaseServer:   cur.Phases.ServerMs - base.Phases.ServerMs,
				signatures.PhaseDownload: cur.Phases.DownloadMs - base.Phases.DownloadMs,
			},
			Evidence: timeAnatomyEvidence(tx.Name, signatures.PhaseSourceBaseline),
		}
		attribution.ReusePct, attribution.TLSObservedPct = transactionConnectionShares(tx)
		attribution.ReferenceReusePct, attribution.ReferenceTLSObservedPct = transactionConnectionShares(reference)
		out = append(out, attribution)
	}
	return out
}

// timeAnatomyAttributedPhases are the tail components that are request
// phases; unsplit, non-request and overlap time name no cause.
var timeAnatomyAttributedPhases = []string{signatures.PhaseDNS, signatures.PhaseConnect, signatures.PhaseTLS, signatures.PhaseServer, signatures.PhaseDownload}

func timeAnatomyEvidence(transaction, source string) rca.EvidenceRef {
	return rca.EvidenceRef{Kind: "time_anatomy", Locator: "time_anatomy#" + url.Values{"transaction": {transaction}, "source": {source}}.Encode()}
}

// bandCoverage is the share of a tail band's members its means cover.
func bandCoverage(band RunArchiveTailBand) float64 {
	if band.Count <= 0 {
		return 0
	}
	return min(1, float64(band.Joined)/float64(band.Count))
}

// decompositionCoverage is the share of a transaction's not-interrupted
// instances its decomposition covers, as the baseline anatomy diff reads it.
func decompositionCoverage(tx *RunArchiveTransactionAnatomy) float64 {
	matched := tx.Decomposition.Instances
	eligible := max(tx.Instances-tx.InterruptedInstances, matched)
	if eligible <= 0 {
		return 0
	}
	return min(1, float64(matched)/float64(eligible))
}

// transactionConnectionShares sums a transaction's request rows: the share
// of classified connections that were reused and the share of requests
// that ran a TLS handshake, each in percent, or nil when unknown.
func transactionConnectionShares(tx *RunArchiveTransactionAnatomy) (*float64, *float64) {
	var reused, created, samples int64
	var tlsObserved float64
	rows := tx.Requests
	if tx.OtherRequests != nil {
		rows = append(slices.Clone(rows), *tx.OtherRequests)
	}
	for _, row := range rows {
		reused += row.Connections.Reused
		created += row.Connections.New
		if row.Phases.TLS.ObservedPct != nil && row.Samples > 0 {
			samples += row.Samples
			tlsObserved += *row.Phases.TLS.ObservedPct * float64(row.Samples)
		}
	}
	var reuse, tls *float64
	if reused+created > 0 {
		value := float64(reused) / float64(reused+created) * 100
		reuse = &value
	}
	if samples > 0 {
		value := tlsObserved / float64(samples)
		tls = &value
	}
	return reuse, tls
}

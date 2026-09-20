package api

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

// The additional distribution state is bounded separately from scalar cells.
// Three compression-100 digests per cell stay well below this reservation,
// including their merge buffers. Enforce it both per archive and per request.
const loadComparisonDistributionBytes int64 = 64 << 10
const maxLoadComparisonDistributionCells = 1024

type loadComparisonQuery struct {
	Population   string   `json:"population,omitempty"`
	StartSeconds float64  `json:"start_seconds"`
	EndSeconds   *float64 `json:"end_seconds,omitempty"`
}

func parseLoadComparisonQuery(values url.Values) (loadComparisonQuery, error) {
	q := loadComparisonQuery{Population: values.Get("population")}
	switch q.Population {
	case "", "action", "transaction", "unknown":
	default:
		return q, fmt.Errorf("population must be action, transaction, or unknown")
	}
	for _, field := range []string{"start_seconds", "end_seconds"} {
		if raw := values.Get(field); raw != "" {
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 365*24*60*60 {
				return q, fmt.Errorf("%s must be a finite nonnegative number of seconds, at most one year", field)
			}
			if field == "start_seconds" {
				q.StartSeconds = value
			} else {
				q.EndSeconds = &value
			}
		}
	}
	if q.EndSeconds != nil && *q.EndSeconds <= q.StartSeconds {
		return q, fmt.Errorf("end_seconds must be greater than start_seconds")
	}
	return q, nil
}

func (q loadComparisonQuery) customWindow() bool { return q.StartSeconds > 0 || q.EndSeconds != nil }

// Windows include complete recorded intervals only. Cutting an interval would
// invent a load duration and change the rate denominator independently of events.
func (q loadComparisonQuery) includesSummary(summaries []IntervalSummaryRecord, index int, started time.Time) bool {
	if !q.customWindow() {
		return true
	}
	if started.IsZero() || index < 0 || index >= len(summaries) {
		return false
	}
	end := summaries[index].Timestamp.Sub(started).Seconds()
	seconds, _ := loadComparisonIntervalSeconds(summaries, index, started)
	start := end - seconds
	return start >= q.StartSeconds && (q.EndSeconds == nil || end <= *q.EndSeconds)
}

type loadComparisonDistribution struct {
	All             *engine.TDigest
	Successful      *engine.TDigest
	Failed          *engine.TDigest
	SuccessfulTotal float64
	FailedTotal     float64
}

func (d *loadComparisonDistribution) add(tx TransactionEvent) {
	if tx.DurationMs < 0 || math.IsNaN(tx.DurationMs) || math.IsInf(tx.DurationMs, 0) {
		return
	}
	if d.All == nil {
		d.All = engine.NewTDigest(100)
	}
	d.All.Add(tx.DurationMs)
	if tx.Success {
		if d.Successful == nil {
			d.Successful = engine.NewTDigest(100)
		}
		d.Successful.Add(tx.DurationMs)
		d.SuccessfulTotal += tx.DurationMs
	} else {
		if d.Failed == nil {
			d.Failed = engine.NewTDigest(100)
		}
		d.Failed.Add(tx.DurationMs)
		d.FailedTotal += tx.DurationMs
	}
}

func (d *loadComparisonDistribution) merge(other *loadComparisonDistribution) {
	if other == nil {
		return
	}
	merge := func(target **engine.TDigest, source *engine.TDigest) {
		if source == nil {
			return
		}
		if *target == nil {
			*target = engine.NewTDigest(100)
		}
		(*target).Merge(source)
	}
	merge(&d.All, other.All)
	merge(&d.Successful, other.Successful)
	merge(&d.Failed, other.Failed)
	d.SuccessfulTotal += other.SuccessfulTotal
	d.FailedTotal += other.FailedTotal
}

type LoadComparisonLatency struct {
	Samples   int64   `json:"samples"`
	AverageMs float64 `json:"average_ms"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
}

func loadComparisonLatency(digest *engine.TDigest, total float64) *LoadComparisonLatency {
	if digest == nil || digest.Count() == 0 {
		return nil
	}
	return &LoadComparisonLatency{Samples: digest.Count(), AverageMs: total / float64(digest.Count()), P50Ms: digest.Quantile(.5), P95Ms: digest.Quantile(.95), P99Ms: digest.Quantile(.99)}
}

func (q loadComparisonQuery) includesEvent(timestamp time.Time, started time.Time) bool {
	if !q.customWindow() {
		return true
	}
	if started.IsZero() {
		return false
	}
	seconds := timestamp.Sub(started).Seconds()
	return seconds >= q.StartSeconds && (q.EndSeconds == nil || seconds <= *q.EndSeconds)
}

// Include observed idle intervals at the same workload, so the rate denominator
// does not shrink to only the seconds in which an action produced an event.
func fillLoadComparisonIdleIntervals(intervals map[transactionIntervalLoadKey]*transactionIntervalAccumulator, groups map[string]struct{}, summaries []IntervalSummaryRecord, query loadComparisonQuery, started time.Time, maxCells int) bool {
	for index, summary := range summaries {
		if !query.includesSummary(summaries, index, started) {
			continue
		}
		for group := range groups {
			load := transactionLoadForSummary(TransactionEvent{Group: group}, summary)
			if load <= 0 {
				continue
			}
			key := transactionIntervalLoadKey{Index: index, Load: load}
			if intervals[key] == nil {
				if len(intervals) >= maxCells {
					return false
				}
				intervals[key] = &transactionIntervalAccumulator{}
			}
		}
	}
	return true
}

// Use the interval's own window when retained, then timestamp boundaries. A
// one-second legacy fallback remains compatible but is explicitly unverified.
func loadComparisonIntervalSeconds(summaries []IntervalSummaryRecord, index int, started time.Time) (float64, bool) {
	if index < 0 || index >= len(summaries) {
		return 0, true
	}
	if value := summaries[index].SampleWindowSeconds; value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
		return value, false
	}
	if index > 0 {
		if value := summaries[index].Timestamp.Sub(summaries[index-1].Timestamp).Seconds(); value > 0 {
			return value, false
		}
	}
	if index == 0 && !started.IsZero() {
		if value := summaries[index].Timestamp.Sub(started).Seconds(); value > 0 {
			return value, false
		}
	}
	return archiveSummaryBucketSeconds(summaries, index), true
}

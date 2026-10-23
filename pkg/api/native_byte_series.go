package api

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// buildNativeByteSeries reads HTTP requests' body bytes on the wire from the
// archive's raw observations (NA-C). Each request's count is a measurement
// only when its adapter set http_body.v1 byte evidence; a request archived
// without it (by an earlier binary) is unavailable, and its bucket is
// unscored with the exclusion counted. A step that dispatched no request
// (evidence "none") is not in the population. Requests are bucketed by their
// start. A sum or a rate needs a complete bucket: every selected
// request evidenced, the archive's collection complete and required, and
// the bucket ended by the run's completion; otherwise it is unscored, never
// a partial total. The per-request distribution reads every request of a
// fully evidenced bucket. A bucket without requests has no value.
func buildNativeByteSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, q storage.AnalyticsQuery, result NativeArchiveSeries, from, to time.Time) (NativeArchiveSeries, error) {
	definition, _ := analysis.LookupMetric(q.Metric)
	result.Unit = definition.Unit
	result.Excluded = map[string]int64{}
	received := q.Metric == "bytes_received" || q.Metric == "bytes_received_rate"
	values := make([][]int64, len(result.Buckets))
	missing := make([]string, len(result.Buckets))
	identities := map[string]bool{}
	selected := 0
	err := forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		// The population is HTTP actions; another protocol's same-named
		// action neither qualifies nor refuses a bucket.
		if tx.Timestamp.Before(from) || !tx.Timestamp.Before(to) || tx.BusinessTransaction || tx.DependencyType != "http" || archiveEventPopulation(*tx) != "action" {
			return nil
		}
		if action := q.Filters["action"]; action != "" && tx.ActionName != action {
			return nil
		}
		if tx.ByteEvidence == protocol.ByteEvidenceNone {
			result.Excluded["no_request"]++
			return nil
		}
		if !identities[tx.ActionName] && len(identities) >= 1000 {
			return errors.New("identity limit exceeded; select an action")
		}
		identities[tx.ActionName] = true
		if selected >= 100000 {
			return errors.New("raw observation limit exceeded; narrow the window or filters")
		}
		selected++
		i := int(tx.Timestamp.Sub(from)/time.Second) / q.BucketSeconds
		value := tx.BytesSent
		if received {
			value = tx.BytesReceived
		}
		refuse := func(reason string) {
			if missing[i] == "" {
				missing[i] = reason
			}
			result.Excluded[reason]++
		}
		switch {
		case tx.ByteEvidence != protocol.ByteEvidenceHTTPBody:
			refuse("byte_evidence_unavailable")
		case value < 0:
			refuse("invalid_byte_count")
		default:
			values[i] = append(values[i], value)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	// Totals need every source's requests: a missing or best-effort source
	// leaves them unproven.
	collected := !archiveCollectionIncomplete(manifest) && (manifest.SourceCoverage == nil || manifest.SourceCoverage.Mode == "required")
	completedAt := archiveExecutionCompletedAt(manifest)
	unscored := false
	for i, v := range values {
		b := &result.Buckets[i]
		b.Count = len(v)
		b.complete = missing[i] == "" && collected && !b.To.After(completedAt)
		if len(v) == 0 && missing[i] == "" {
			continue
		}
		if missing[i] != "" {
			b.Reason, unscored = missing[i], true
			continue
		}
		var value float64
		switch q.Aggregation {
		case "sum", "rate":
			if !b.complete {
				b.Reason, unscored = "incomplete_bucket", true
				continue
			}
			var sum int64
			for _, x := range v {
				if x > 1<<53-1-sum {
					sum = -1
					break
				}
				sum += x
			}
			if sum < 0 {
				b.Reason, unscored = "exact_sum_limit", true
				continue
			}
			value = float64(sum)
			if q.Aggregation == "rate" {
				value /= b.To.Sub(b.From).Seconds()
			}
		default:
			sorted := append([]int64(nil), v...)
			sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
			switch q.Aggregation {
			case "avg":
				// Whole byte counts add exactly well past any bucket's
				// requests; the one division rounds once.
				for _, x := range sorted {
					value += float64(x)
				}
				value /= float64(len(sorted))
			case "min":
				value = float64(sorted[0])
			case "max":
				value = float64(sorted[len(sorted)-1])
			default:
				p := map[string]float64{"p50": .50, "p90": .90, "p95": .95, "p99": .99}[q.Aggregation]
				value = float64(sorted[int(math.Ceil(p*float64(len(sorted))))-1])
			}
		}
		b.Value = &value
	}
	for id := range identities {
		result.Identities = append(result.Identities, id)
	}
	sort.Strings(result.Identities)
	if selected == 0 {
		result.Warnings = append(result.Warnings, "No matching HTTP requests; missing evidence is not zero.")
	}
	if result.Excluded["byte_evidence_unavailable"] > 0 {
		result.Warnings = append(result.Warnings, "Some HTTP requests were archived without byte evidence (by an earlier version); their buckets are unscored.")
	}
	if result.Excluded["invalid_byte_count"] > 0 {
		result.Warnings = append(result.Warnings, "Some HTTP requests carry an invalid byte count; their buckets are unscored.")
	}
	if !collected {
		result.Warnings = append(result.Warnings, "The archive does not hold every source's requests; sums and rates are unscored and distributions describe the collected requests only.")
	} else if unscored {
		result.Warnings = append(result.Warnings, "Some buckets are unscored; their reason says why.")
	}
	if to.After(completedAt) {
		result.Warnings = append(result.Warnings, "The requested window extends past run completion.")
	}
	return result, nil
}

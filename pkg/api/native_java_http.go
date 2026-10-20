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

// Representation pairs are added before division. Scalar latency/byte
// percentiles use only observed values; missing observations unscore the bucket.
// None of these values is an authoritative request rate or Java object size.
func nativeJavaHTTPValue(d *protocol.JavaHTTPDiagnostics, metric string) (n, den float64, available bool) {
	if d == nil || !d.Valid() {
		return
	}
	scalar := func(v *int64) (float64, float64, bool) {
		if v == nil {
			return 0, 0, false
		}
		return float64(*v), 0, true
	}
	timing := func(v *float64) (float64, float64, bool) {
		if v == nil {
			return 0, 0, false
		}
		return *v, 0, true
	}
	pair := func(a, b *int64) (float64, float64, bool) {
		if a == nil || b == nil {
			return 0, 0, false
		}
		return float64(*a), float64(*b), true
	}
	switch metric {
	case "java_http_input_bytes":
		return float64(d.RequestEncodedBytes), 0, true
	case "java_http_decoded_request_bytes":
		return scalar(d.RequestDecodedBytes)
	case "java_http_prepared_body_bytes":
		return scalar(d.PreparedRequestBodyBytes)
	case "java_http_retained_response_bytes":
		return scalar(d.ResponseRetainedBytes)
	case "java_http_response_base64_bytes":
		return scalar(d.ResponseBase64Bytes)
	case "java_http_base64_decode_ms":
		return timing(d.Base64DecodeMs)
	case "java_http_base64_encode_ms":
		return timing(d.Base64EncodeMs)
	case "java_http_enclosing_ms":
		return d.EnclosingMs, 0, true
	case "java_http_decoded_per_encoded":
		return pair(d.RequestDecodedBytes, &d.RequestEncodedBytes)
	case "java_http_prepared_per_decoded":
		return pair(d.PreparedRequestBodyBytes, d.RequestDecodedBytes)
	case "java_http_base64_per_retained":
		return pair(d.ResponseBase64Bytes, d.ResponseRetainedBytes)
	case "java_http_base64_work_share":
		if d.Base64DecodeMs == nil || d.Base64EncodeMs == nil || d.EnclosingMs <= 0 {
			return
		}
		return *d.Base64DecodeMs + *d.Base64EncodeMs, d.EnclosingMs, true
	}
	return
}
func buildNativeJavaHTTPSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, q storage.AnalyticsQuery, result NativeArchiveSeries, from, to time.Time) (NativeArchiveSeries, error) {
	definition, _ := analysis.LookupMetric(q.Metric)
	result.Unit = definition.Unit
	result.Excluded = map[string]int64{}
	values := make([][]float64, len(result.Buckets))
	numerators := make([]float64, len(values))
	denominators := make([]float64, len(values))
	missing := make([]bool, len(values))
	selected := 0
	compressionSet := map[string]bool{}
	err := forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if tx.Timestamp.Before(from) || !tx.Timestamp.Before(to) || tx.ActionName != q.Filters["action"] || tx.BusinessTransaction {
			return nil
		}
		// A same-named action of another protocol is outside the Java
		// population; it neither qualifies nor refuses a Java bucket.
		if tx.DependencyType != "java_http" && (tx.ServerTelemetry == nil || tx.ServerTelemetry.JavaHTTP == nil) {
			return nil
		}
		population := archiveEventPopulation(*tx)
		if population == "transaction" {
			return nil
		}
		selected++
		if selected > 100000 {
			return errors.New("raw observation limit exceeded; narrow the window")
		}
		i := int(tx.Timestamp.Sub(from)/time.Second) / q.BucketSeconds
		refuse := func(reason string) { missing[i] = true; result.Excluded[reason]++ }
		if population != "action" {
			refuse("unknown_population")
			return nil
		}
		var d *protocol.JavaHTTPDiagnostics
		if tx.ServerTelemetry != nil {
			d = tx.ServerTelemetry.JavaHTTP
		}
		if d == nil || !d.Valid() {
			refuse("unavailable_or_unsupported_diagnostics")
			return nil
		}
		// A collection-window response is truthful retained evidence in reports,
		// but cannot qualify a complete native response comparison. A discarded
		// body's zero retained size is the logging policy, not a response size.
		if (d.ResponseState == "collection_window" || d.ResponseState == "discarded") && (q.Metric == "java_http_retained_response_bytes" || q.Metric == "java_http_response_base64_bytes" || q.Metric == "java_http_base64_per_retained") {
			refuse("response_" + d.ResponseState)
			return nil
		}
		n, den, ok := nativeJavaHTTPValue(d, q.Metric)
		if !ok {
			refuse("measurement_unavailable")
			return nil
		}
		if q.Metric == "java_http_prepared_body_bytes" || q.Metric == "java_http_prepared_per_decoded" {
			compressionSet[d.AppliedRequestCompression] = true
		}
		values[i] = append(values[i], n)
		if q.Aggregation == "ratio_of_sums" {
			if q.Metric != "java_http_base64_work_share" && (n > float64(1<<53-1)-numerators[i] || den > float64(1<<53-1)-denominators[i]) {
				refuse("exact_sum_limit")
				return nil
			}
			numerators[i] += n
			denominators[i] += den
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if len(compressionSet) == 1 {
		for compression := range compressionSet {
			result.JavaHTTPCompression = compression
		}
	}
	if len(compressionSet) > 1 {
		result.Warnings = append(result.Warnings, "Mixed request compression; prepared representation values are unscored.")
	}
	archiveIncomplete := archiveCollectionIncomplete(manifest)
	if manifest.SourceCoverage != nil {
		archiveIncomplete = true
	}
	anyIncomplete := archiveIncomplete || to.After(archiveExecutionCompletedAt(manifest))
	for i, v := range values {
		bucket := &result.Buckets[i]
		bucket.Count = len(v)
		bucket.complete = !missing[i] && !archiveIncomplete && !bucket.To.After(archiveExecutionCompletedAt(manifest)) && len(compressionSet) <= 1
		if !bucket.complete {
			bucket.Reason = "incomplete_representation_evidence"
			anyIncomplete = true
			continue
		}
		if len(v) == 0 {
			bucket.Reason = "measurement_unavailable"
			continue
		}
		sort.Float64s(v)
		value := 0.0
		switch q.Aggregation {
		case "ratio_of_sums":
			paired, err := analysis.EvaluateJavaHTTPPairedSums(q.Metric, analysis.JavaHTTPPairedSums{Version: 1, Eligible: int64(len(v)), Samples: int64(len(v)), Numerator: numerators[i], Denominator: denominators[i], Complete: bucket.complete})
			if err != nil {
				return result, err
			}
			if paired.Value == nil {
				bucket.Reason = paired.Reason
				continue
			}
			value = *paired.Value
		case "avg":
			for _, x := range v {
				value += x / float64(len(v))
			}
		case "min":
			value = v[0]
		case "max":
			value = v[len(v)-1]
		default:
			p := map[string]float64{"p50": .5, "p90": .9, "p95": .95, "p99": .99}[q.Aggregation]
			value = v[int(math.Ceil(p*float64(len(v))))-1]
		}
		if math.IsInf(value, 0) || math.IsNaN(value) {
			bucket.Reason = "invalid_aggregate"
			anyIncomplete = true
			continue
		}
		bucket.Value = &value
	}
	qualified := 0
	for _, bucket := range result.Buckets {
		if bucket.Value != nil {
			qualified++
		}
	}
	if qualified == 0 && selected > 0 {
		result.Warnings = append(result.Warnings, "No qualified Java representation values; unavailable measurements and zero denominators are not zero.")
	}
	if selected > 0 {
		result.Identities = []string{q.Filters["action"]}
	} else {
		result.Warnings = append(result.Warnings, "No matching Java action observations; missing evidence is not zero.")
	}
	if anyIncomplete {
		result.Warnings = append(result.Warnings, "Selected Java representation evidence is incomplete; affected buckets are unscored.")
	}
	return result, nil
}

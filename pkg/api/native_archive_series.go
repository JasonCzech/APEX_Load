package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type NativeArchiveBucket struct {
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	OffsetSeconds int       `json:"offset_seconds"`
	Count         int       `json:"count"`
	Value         *float64  `json:"value"`
}
type NativeArchiveSeries struct {
	RunID       string                `json:"run_id"`
	Buckets     []NativeArchiveBucket `json:"buckets"`
	Identities  []string              `json:"identities"`
	Warnings    []string              `json:"warnings"`
	Fingerprint string                `json:"-"`
}
type NativeArchiveResponse struct {
	Current    NativeArchiveSeries  `json:"current"`
	Baseline   *NativeArchiveSeries `json:"baseline,omitempty"`
	Comparable bool                 `json:"comparable"`
	Warnings   []string             `json:"warnings"`
	Added      []string             `json:"added"`
	Removed    []string             `json:"removed"`
}

func (s *Server) handleNativeArchiveSeries(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var q storage.AnalyticsQuery
	if !decodeJSONBody(w, r, &q) {
		return
	}
	if err := storage.ValidateNativeArchiveQuery(q); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Check both identities before materializing either archive.
	archives := s.archiveDomainService()
	current, ok := archives.GetTest(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "test not found")
		return
	}
	if q.ComparisonRunID != "" {
		baseline, exists := archives.GetTest(q.ComparisonRunID)
		if !exists || !testsShareProject(current, baseline) {
			writeError(w, 404, "comparison test not found")
			return
		}
	}
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	load := func(id string) (NativeArchiveSeries, error) {
		record, exists := archives.GetRunArchiveContext(ctx, id)
		if !exists {
			return NativeArchiveSeries{}, errors.New("completed archive unavailable")
		}
		return buildNativeArchiveSeries(ctx, record, q)
	}
	series, err := load(current.ID)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	response := NativeArchiveResponse{Current: series, Warnings: []string{}, Added: []string{}, Removed: []string{}}
	if q.ComparisonRunID != "" {
		baseline, err := load(q.ComparisonRunID)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		response.Baseline = &baseline
		response.Comparable = series.Fingerprint != "" && series.Fingerprint == baseline.Fingerprint && len(series.Warnings) == 0 && len(baseline.Warnings) == 0
		if !response.Comparable {
			response.Warnings = append(response.Warnings, "Workload/version compatibility or evidence completeness is not established; review both runs before interpreting differences.")
		}
		response.Added = nativeIdentityDifference(series.Identities, baseline.Identities)
		response.Removed = nativeIdentityDifference(baseline.Identities, series.Identities)
	}
	writeJSON(w, 200, response)
}
func nativeIdentityDifference(left, right []string) []string {
	seen := map[string]bool{}
	for _, id := range right {
		seen[id] = true
	}
	result := []string{}
	for _, id := range left {
		if !seen[id] {
			result = append(result, id)
		}
	}
	return result
}
func buildNativeArchiveSeries(ctx context.Context, record *storage.RunArchiveRecord, q storage.AnalyticsQuery) (NativeArchiveSeries, error) {
	result := NativeArchiveSeries{RunID: record.TestID, Buckets: []NativeArchiveBucket{}, Identities: []string{}, Warnings: []string{}}
	if err := storage.ValidateNativeArchiveQuery(q); err != nil {
		return result, err
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return result, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return result, err
	}
	if manifest.CompletedAt.IsZero() || manifest.Provenance.StartedAt == nil || manifest.Provenance.StartedAt.IsZero() {
		return result, errors.New("completed archive with execution start time required")
	}
	result.Fingerprint = manifest.Provenance.ComparisonFingerprint
	start := *manifest.Provenance.StartedAt
	from := start.Add(time.Duration(q.StartSeconds) * time.Second)
	to := start.Add(time.Duration(q.EndSeconds) * time.Second)
	n := (q.EndSeconds - q.StartSeconds + q.BucketSeconds - 1) / q.BucketSeconds
	values := make([][]float64, n)
	failures := make([]int, n)
	for i := 0; i < n; i++ {
		at := from.Add(time.Duration(i*q.BucketSeconds) * time.Second)
		until := at.Add(time.Duration(q.BucketSeconds) * time.Second)
		if until.After(to) {
			until = to
		}
		result.Buckets = append(result.Buckets, NativeArchiveBucket{From: at, To: until, OffsetSeconds: q.StartSeconds + i*q.BucketSeconds})
	}
	identities := map[string]bool{}
	missingProperty, unknownPopulation, selected := 0, 0, 0
	err = forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if tx.Timestamp.Before(from) || !tx.Timestamp.Before(to) {
			return nil
		}
		population := archiveEventPopulation(*tx)
		if population == "unknown" {
			unknownPopulation++
			return nil
		}
		if population != q.Population {
			return nil
		}
		// Exact identity, also used by the drill-down endpoint's action_exact filter.
		if action := q.Filters["action"]; action != "" && tx.ActionName != action {
			return nil
		}
		if !matchesNativeDimensionFilters(tx, q.Filters) {
			return nil
		}
		if q.PropertyName != "" {
			value, exists := tx.PropertyDimensions[q.PropertyName]
			if !exists {
				missingProperty++
				return nil
			}
			if value != q.PropertyValue {
				return nil
			}
		}
		if !identities[tx.ActionName] && len(identities) >= 1000 {
			return errors.New("identity limit exceeded; select an action")
		}
		identities[tx.ActionName] = true
		if selected >= 100000 {
			return errors.New("raw observation limit exceeded; narrow the window or filters")
		}
		selected++
		if math.IsNaN(tx.DurationMs) || math.IsInf(tx.DurationMs, 0) || tx.DurationMs < 0 {
			return errors.New("invalid raw duration evidence")
		}
		i := int(tx.Timestamp.Sub(from)/time.Second) / q.BucketSeconds
		values[i] = append(values[i], tx.DurationMs)
		if !tx.Success {
			failures[i]++
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	for i, v := range values {
		result.Buckets[i].Count = len(v)
		if len(v) == 0 {
			continue
		}
		sort.Float64s(v)
		var value float64
		switch q.Metric {
		case "observation_rate", "error_observation_rate", "success_observation_rate":
			value = *storage.NativeObservationRate(q.Metric, int64(len(v)), int64(failures[i]), result.Buckets[i].To.Sub(result.Buckets[i].From).Seconds())
		case "observation_count":
			value = float64(len(v))
		case "error_rate":
			value = 100 * float64(failures[i]) / float64(len(v))
		default:
			switch q.Aggregation {
			case "avg":
				for _, x := range v {
					value += x / float64(len(v))
				}
			case "min":
				value = v[0]
			case "max":
				value = v[len(v)-1]
			default:
				p := map[string]float64{"p50": .50, "p90": .90, "p95": .95, "p99": .99}[q.Aggregation]
				value = v[int(math.Ceil(p*float64(len(v))))-1]
			}
		}
		result.Buckets[i].Value = &value
	}
	for id := range identities {
		result.Identities = append(result.Identities, id)
	}
	sort.Strings(result.Identities)
	if selected == 0 {
		result.Warnings = append(result.Warnings, "No matching raw observations; missing evidence is not zero.")
	}
	if missingProperty > 0 {
		result.Warnings = append(result.Warnings, "Some observations have no retained property; this selection is incomplete.")
	}
	if unknownPopulation > 0 {
		result.Warnings = append(result.Warnings, "Legacy observations with unknown population were excluded.")
	}
	if to.After(manifest.CompletedAt) {
		result.Warnings = append(result.Warnings, "The requested window extends past run completion.")
	}
	if manifest.SourceCoverage != nil {
		result.Warnings = append(result.Warnings, "Review archive source coverage before interpreting this selection.")
	}
	return result, nil
}

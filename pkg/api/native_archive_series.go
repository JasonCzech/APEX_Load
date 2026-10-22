package api

import (
	"context"
	"errors"
	"maps"
	"math"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type NativeArchiveBucket struct {
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	OffsetSeconds int       `json:"offset_seconds"`
	Count         int       `json:"count"`
	Value         *float64  `json:"value"`
	// Reason says why a derived value is unscored.
	Reason string `json:"reason,omitempty"`
	// subset (failures, or the messages a message ratio counts) and
	// complete are the bucket's derivation inputs.
	subset   int64
	complete bool
}
type NativeArchiveSeries struct {
	archiveManifestSHA256 string
	// BrowserTuples is a versioned separate-tuple result; legacy Buckets remains empty.
	BrowserTuples *NativeBrowserTupleSeries `json:"browser_tuples,omitempty"`
	RunID         string                    `json:"run_id"`
	// Unit is set when a derivation changed the series' unit.
	Unit       string                `json:"unit,omitempty"`
	Buckets    []NativeArchiveBucket `json:"buckets"`
	Identities []string              `json:"identities"`
	Warnings   []string              `json:"warnings"`
	// Excluded counts, by reason, the browser document measurements of the
	// selection that are not final eligible values (unqualified_final,
	// unavailable_final, provisional, censored); they never enter a value.
	Excluded            map[string]int64 `json:"excluded,omitempty"`
	JavaHTTPCompression string           `json:"java_http_compression,omitempty"`
	Fingerprint         string           `json:"-"`
	// documentTuple is the browser document measurement tuple selected.
	documentTuple *protocol.BrowserDocumentMetricSummary
}
type NativeArchiveResponse struct {
	BrowserInputs           *storage.BrowserPanelInputs    `json:"browser_inputs,omitempty"`
	BrowserTupleComparisons []NativeBrowserTupleComparison `json:"browser_tuple_comparisons,omitempty"`
	// Derivation names the catalog operation the current series values are.
	Derivation *analysis.QueryDerivation `json:"derivation,omitempty"`
	Current    NativeArchiveSeries       `json:"current"`
	Baseline   *NativeArchiveSeries      `json:"baseline,omitempty"`
	Comparable bool                      `json:"comparable"`
	Warnings   []string                  `json:"warnings"`
	Added      []string                  `json:"added"`
	Removed    []string                  `json:"removed"`
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
		if series.BrowserTuples != nil && baseline.BrowserTuples != nil {
			response.BrowserTupleComparisons = compareNativeBrowserTuples(series, baseline)
		}
		response.Comparable = series.Fingerprint != "" && series.Fingerprint == baseline.Fingerprint && len(series.Warnings) == 0 && len(baseline.Warnings) == 0
		if series.BrowserTuples != nil || baseline.BrowserTuples != nil {
			response.Comparable = false
		}
		if series.documentTuple != nil && baseline.documentTuple != nil {
			if reason := protocol.BrowserDocumentTupleMismatch(*series.documentTuple, *baseline.documentTuple); reason != "" {
				response.Warnings = append(response.Warnings, "The runs measured different browser document tuples ("+reason+"); their values are not compared.")
			}
		}
		if q.Population == analysis.PopulationJavaHTTPAction && series.JavaHTTPCompression != baseline.JavaHTTPCompression {
			response.Comparable = false
			response.Warnings = append(response.Warnings, "Java HTTP request compression differs between runs; representation measurements are not compared.")
		}
		if series.BrowserTuples != nil || baseline.BrowserTuples != nil {
			response.Warnings = append(response.Warnings, "Browser measurement tuples are compared separately; no pooled comparison is produced.")
		} else if !response.Comparable {
			response.Warnings = append(response.Warnings, "Workload/version compatibility or evidence completeness is not established; review both runs before interpreting differences.")
		}
		response.Added = nativeIdentityDifference(series.Identities, baseline.Identities)
		response.Removed = nativeIdentityDifference(baseline.Identities, series.Identities)
	}
	if err := applyNativeArchiveDerivation(current.ProjectID, q, &response); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	if err := bindBrowserPanelInputs(q, &response); err != nil {
		writeError(w, 422, err.Error())
		return
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
	return buildNativeArchiveSeriesForTest(ctx, record, q, nil, nil)
}

// The optional durable identity is checked under the same materialization lease
// and manifest read as the series, preventing stale same-ID publications.
func buildNativeArchiveSeriesForTest(ctx context.Context, record *storage.RunArchiveRecord, q storage.AnalyticsQuery, test *storage.TestRecord, metadataStore any) (NativeArchiveSeries, error) {
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
	if test != nil && !runArchiveManifestMatchesStoredTest(manifest, test, metadataStore) {
		return result, errors.New("input archive identity changed")
	}
	if archiveExecutionCompletedAt(manifest).IsZero() || manifest.Provenance.StartedAt == nil || manifest.Provenance.StartedAt.IsZero() {
		return result, errors.New("completed archive with execution start time required")
	}
	if q.QueryVersion == 4 && record.ManifestSHA256 == "" {
		return result, errors.New("browser input publication digest unavailable")
	}
	result.archiveManifestSHA256 = record.ManifestSHA256
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
	if q.Population == analysis.PopulationJavaHTTPAction {
		return buildNativeJavaHTTPSeries(ctx, record, manifest, q, result, from, to)
	}
	if q.Population == analysis.PopulationMessage {
		return buildNativeMessageSeries(ctx, record, manifest, q, result, from, to, n)
	}
	if q.Population == analysis.PopulationBrowserDocument {
		return buildNativeBrowserDocumentSeries(ctx, record, manifest, q, result, from, to)
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
		result.Buckets[i].subset = int64(failures[i])
		result.Buckets[i].complete = missingProperty == 0 && unknownPopulation == 0 && !archiveCollectionIncomplete(manifest) && !result.Buckets[i].To.After(archiveExecutionCompletedAt(manifest))
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
	if to.After(archiveExecutionCompletedAt(manifest)) {
		result.Warnings = append(result.Warnings, "The requested window extends past run completion.")
	}
	if manifest.SourceCoverage != nil {
		result.Warnings = append(result.Warnings, "Review archive source coverage before interpreting this selection.")
	}
	return result, nil
}

// buildNativeMessageSeries reads received messages from the archive's
// messaging v2 evidence, per bucket: the received messages a client observed
// and, for a message ratio derivation, the messages its counter counted among
// the observed ones. A bucket is complete only when every selected dimension
// carried complete v2 evidence and the counter observed every eligible
// message; nothing missing becomes zero.
func buildNativeMessageSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, q storage.AnalyticsQuery, result NativeArchiveSeries, from, to time.Time, n int) (NativeArchiveSeries, error) {
	counter := ""
	if q.Derivation != nil {
		counter, _ = analysis.MessageRatioCounter(q.Derivation.Operation)
	}
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		return result, err
	}
	evidenced, incomplete, counted := make([]bool, n), make([]bool, n), make([]bool, n)
	received, observed, subset := make([]int64, n), make([]int64, n), make([]int64, n)
	sources := map[string]bool{}
	for _, summary := range summaries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if summary.Timestamp.Before(from) || !summary.Timestamp.Before(to) {
			continue
		}
		i := int(summary.Timestamp.Sub(from)/time.Second) / q.BucketSeconds
		for _, name := range slices.Sorted(maps.Keys(summary.Messaging)) {
			messaging := summary.Messaging[name]
			if messaging == nil {
				continue
			}
			if messaging.Incomplete {
				incomplete[i] = true
			}
			for _, d := range messaging.Dimensions {
				if source := q.Filters["source"]; source != "" && d.Source != source {
					continue
				}
				sources[d.Source] = true
				if d.Evidence == nil {
					// Messaging v1 summaries count operations only.
					incomplete[i] = true
					continue
				}
				evidenced[i] = true
				incomplete[i] = incomplete[i] || d.Evidence.Incomplete
				received[i] += d.Received
				for _, c := range d.Evidence.Counters {
					if counter == "" || c.Metric != counter || c.Population != "received_message" {
						continue
					}
					counted[i] = true
					incomplete[i] = incomplete[i] || c.Observed != c.Eligible
					observed[i] += c.Observed
					subset[i] += c.Count
				}
			}
		}
	}
	any := false
	for i := range result.Buckets {
		b := &result.Buckets[i]
		b.complete = evidenced[i] && !incomplete[i] && (counter == "" || counted[i]) && !archiveCollectionIncomplete(manifest) && !b.To.After(archiveExecutionCompletedAt(manifest))
		if !evidenced[i] {
			continue
		}
		any = true
		count := received[i]
		if counter != "" {
			count = observed[i]
		}
		b.Count, b.subset = int(count), subset[i]
		value := float64(received[i])
		b.Value = &value
	}
	for source := range sources {
		result.Identities = append(result.Identities, source)
	}
	sort.Strings(result.Identities)
	if !any {
		result.Warnings = append(result.Warnings, "No messaging v2 evidence in the window; missing evidence is not zero.")
	}
	if counter == "duplicate" {
		result.Warnings = append(result.Warnings, "Duplicates are counted within each consumer's local sequence window, so the ratio is a lower bound.")
	}
	if to.After(archiveExecutionCompletedAt(manifest)) {
		result.Warnings = append(result.Warnings, "The requested window extends past run completion.")
	}
	return result, nil
}

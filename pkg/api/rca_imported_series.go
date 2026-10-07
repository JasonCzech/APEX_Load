package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Imported analysis datasets in root-cause analysis (B1). A ready dataset of
// the run's project whose run association names the analysed run contributes
// series of kind imported, one per metric and identity its partition manifest
// records, read through bounded native series queries. The evidence revision
// freezes each dataset's revision and partition digest; a dataset that has
// changed since is reported, never read.

const (
	// rcaMaxImportedDatasets bounds the associated datasets one evidence
	// revision records.
	rcaMaxImportedDatasets = 8
	// rcaImportListPages bounds the project dataset listing scanned for
	// associations, rcaImportListPage rows at a time.
	rcaImportListPages = 20
	rcaImportListPage  = 100
	// rcaImportedReadShare: imported reads may use at most this fraction
	// (1/n) of the analysis's wall-time budget, so the run's own evidence is
	// always judged; rcaMaxImportedQueries bounds their native queries.
	rcaImportedReadShare  = 4
	rcaMaxImportedQueries = 128
	// rcaImportedAdmissionRetry is the first wait before retrying a read the
	// single native-analysis slot refused (another kind of analytics job holds
	// it); it doubles up to rcaImportedAdmissionMaxWait.
	rcaImportedAdmissionRetry   = 250 * time.Millisecond
	rcaImportedAdmissionMaxWait = 2 * time.Second
)

// rcaNativeAnalysisAvailable says whether this build reads datasets at all
// (tests replace it).
var rcaNativeAnalysisAvailable = analytics.Available

// rcaImportedReadGate queues the imported reads of concurrent analyses for
// the native-analysis slot one query at a time, in arrival order, so one
// analysis reading back to back never starves another polling for the slot.
var rcaImportedReadGate = make(chan struct{}, 1)

// rcaQueryImportedDataset runs one native series query (tests replace it).
var rcaQueryImportedDataset = queryAnalysisDataset

// errRCAImportedEvidenceBusy fails an analysis whose associated datasets the
// native-analysis slot stayed too busy to read: the same inputs must not
// complete with less evidence because of timing, and a failed analysis is
// retried by an identical request.
var errRCAImportedEvidenceBusy = errors.New("native analysis stayed busy while reading the run's associated datasets; retry the analysis")

// Limitations of imported evidence.
const (
	limitationImportedDatasetsCut        = "imported_datasets_truncated"
	limitationImportedDatasetChanged     = "imported_dataset_changed"
	limitationImportedDatasetUnavailable = "imported_dataset_unavailable"
	limitationImportedSeriesUnenumerated = "imported_series_unenumerated"
)

// rcaEvidenceDataset is one associated dataset as an evidence revision froze it.
type rcaEvidenceDataset struct {
	DatasetID       string `json:"dataset_id"`
	Revision        int64  `json:"revision"`
	MappingSHA256   string `json:"mapping_sha256"`
	PartitionDigest string `json:"partition_digest"`
}

// loadRCADatasets lists the ready datasets of project associated with testID,
// in ID order, at most rcaMaxImportedDatasets of them; truncated says more
// existed. A store without dataset storage has none.
func loadRCADatasets(store any, project, testID string) ([]rcaEvidenceDataset, bool, error) {
	imports, ok := store.(storage.AnalysisImportStore)
	if !ok {
		return nil, false, nil
	}
	var found []rcaEvidenceDataset
	seen := map[string]bool{}
	listed := false
	for page := 0; page < rcaImportListPages; page++ {
		rows, err := imports.ListAnalysisImports(project, rcaImportListPage, page*rcaImportListPage)
		if err != nil {
			return nil, false, err
		}
		for _, in := range rows {
			// Offset paging can repeat a row a concurrent change shifted.
			if seen[in.ID] || in.ProjectID != project || in.RunID != testID || in.State != "ready" || in.PartitionManifest == nil {
				continue
			}
			seen[in.ID] = true
			found = append(found, rcaEvidenceDataset{DatasetID: in.ID, Revision: in.Revision, MappingSHA256: in.PartitionManifest.MappingSHA256, PartitionDigest: in.PartitionManifest.Digest})
		}
		if len(rows) < rcaImportListPage {
			listed = true
			break
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].DatasetID < found[j].DatasetID })
	// A listing the page bound stopped may have missed an association.
	truncated := len(found) > rcaMaxImportedDatasets || !listed
	if len(found) > rcaMaxImportedDatasets {
		found = found[:rcaMaxImportedDatasets]
	}
	return found, truncated, nil
}

// rcaImportedSeriesKey is one metric and identity a dataset partition records.
type rcaImportedSeriesKey struct {
	metric   string
	identity analysis.NormalizedPartitionIdentity
	kind     string
}

func (k rcaImportedSeriesKey) sortKey() string {
	body, _ := json.Marshal(struct {
		Metric   string                               `json:"m"`
		Identity analysis.NormalizedPartitionIdentity `json:"i"`
	}{k.metric, k.identity})
	return string(body)
}

// importedSeriesKeys enumerates the metric/identity pairs of a dataset whose
// partitions overlap [from, to). A partition that mixes metrics or
// identities records neither and is counted as unenumerated.
func importedSeriesKeys(manifest *analysis.NormalizedPartitionManifest, from, to time.Time) ([]rcaImportedSeriesKey, int) {
	seen := map[string]rcaImportedSeriesKey{}
	mixed := 0
	for _, p := range manifest.Partitions {
		// An empty partition (every row rejected) records no metric or
		// identity and holds nothing to judge.
		if p.Stats.Rows == 0 || p.Stats.MinTime != nil && !p.Stats.MinTime.Before(to) || p.Stats.MaxTime != nil && p.Stats.MaxTime.Before(from) {
			continue
		}
		if p.Stats.Metric == nil || p.Stats.Identity == nil {
			mixed++
			continue
		}
		key := rcaImportedSeriesKey{metric: *p.Stats.Metric, identity: *p.Stats.Identity, kind: p.Stats.Kind}
		key.identity.Dimensions = maps.Clone(key.identity.Dimensions)
		seen[key.sortKey()] = key
	}
	keys := make([]rcaImportedSeriesKey, 0, len(seen))
	for _, sortKey := range slices.Sorted(maps.Keys(seen)) {
		keys = append(keys, seen[sortKey])
	}
	return keys, mixed
}

// rcaImportedWindow is the run span imported series are read over, on the
// run's interval grid, widened so it never exceeds the native point bound.
func rcaImportedWindow(summaries []IntervalSummaryRecord) (time.Time, time.Time, time.Duration, bool) {
	if len(summaries) == 0 {
		return time.Time{}, time.Time{}, 0, false
	}
	first, last := summaries[0].Timestamp, summaries[0].Timestamp
	step := time.Duration(0)
	for i, s := range summaries {
		if s.Timestamp.Before(first) {
			first = s.Timestamp
		}
		if s.Timestamp.After(last) {
			last = s.Timestamp
		}
		if i > 0 {
			if gap := s.Timestamp.Sub(summaries[i-1].Timestamp).Abs(); gap > 0 && (step == 0 || gap < step) {
				step = gap
			}
		}
	}
	bucket := max(step.Round(time.Second), time.Second)
	from, to := first.Add(-bucket).Truncate(bucket), last.Add(bucket)
	// The native query counts a partial last bucket, so this does too.
	for (to.Sub(from)+bucket-1)/bucket > analysis.MaxSeriesPoints {
		bucket *= 2
	}
	return from.UTC(), to.UTC(), bucket, true
}

// rcaImportedSeries reads the frozen associated datasets' series over the
// run's window: at most limit series of at most maxPoints buckets (limit <
// 0 means unbounded), each one bounded native query, within readBudget of
// wall time (0 means the analysis budget alone) and rcaMaxImportedQueries
// queries. A dataset that changed while it was read is dropped. What cannot
// be read is a limitation; only cancellation, or a native-analysis slot that
// stays busy past the read budget, fails the read.
func (s *Server) rcaImportedSeries(ctx context.Context, project, testID string, frozen []rcaEvidenceDataset, summaries []IntervalSummaryRecord, limit int, budgeter *rca.Budgeter, readBudget time.Duration) ([]rca.Series, []rca.Limitation, error) {
	if len(frozen) == 0 {
		return nil, nil, nil
	}
	var limitations []rca.Limitation
	note := func(code, detail string) {
		limitations = append(limitations, rca.Limitation{Code: code, Detail: detail})
	}
	from, to, bucket, ok := rcaImportedWindow(summaries)
	store, stored := s.store.(storage.AnalysisImportStore)
	switch {
	case !ok:
		note(limitationImportedDatasetUnavailable, "the run recorded no intervals to align associated datasets with")
		return nil, limitations, nil
	case !stored || !rcaNativeAnalysisAvailable() || ensureAnalysisBackend(s.store) != nil || !analysisProjectEnabled(project):
		note(limitationImportedDatasetUnavailable, fmt.Sprintf("native analysis is unavailable, so %d associated datasets were not judged", len(frozen)))
		return nil, limitations, nil
	}
	var deadline time.Time
	if readBudget > 0 {
		deadline = time.Now().Add(readBudget)
	}
	outOfBudget := func() bool {
		return budgeter.Exhausted() || !deadline.IsZero() && !time.Now().Before(deadline)
	}
	unchanged := func(want rcaEvidenceDataset) (*storage.AnalysisImport, bool) {
		in, err := store.GetAnalysisImport(want.DatasetID)
		if err != nil || in == nil || in.ProjectID != project || in.RunID != testID || in.State != "ready" || in.PartitionManifest == nil ||
			in.Revision != want.Revision || in.PartitionManifest.Digest != want.PartitionDigest || in.PartitionManifest.MappingSHA256 != want.MappingSHA256 {
			return nil, false
		}
		return in, true
	}
	var series []rca.Series
	dropped, longest, queries, unread := 0, 0, 0, 0
	for _, want := range frozen {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		in, current := unchanged(want)
		if !current {
			note(limitationImportedDatasetChanged, "associated dataset "+want.DatasetID+" changed or was removed after the evidence was captured and was not judged")
			continue
		}
		snapshot, err := snapshotAnalysisQueryDataset(in)
		if err != nil {
			note(limitationImportedDatasetUnavailable, "associated dataset "+want.DatasetID+" could not be read: "+err.Error())
			continue
		}
		keys, mixed := importedSeriesKeys(snapshot.PartitionManifest, from, to)
		if mixed > 0 {
			note(limitationImportedSeriesUnenumerated, fmt.Sprintf("%d partitions of dataset %s mix metrics or identities and were not judged", mixed, want.DatasetID))
		}
		var datasetSeries []rca.Series
		for _, key := range keys {
			if key.kind == analysis.KindHistogram || key.kind == analysis.KindSummary {
				unread++
				continue
			}
			if limit >= 0 && len(series)+len(datasetSeries) >= limit || queries >= rcaMaxImportedQueries || outOfBudget() {
				dropped++
				continue
			}
			readCtx, cancel := ctx, context.CancelFunc(func() {})
			if !deadline.IsZero() {
				readCtx, cancel = context.WithDeadline(ctx, deadline)
			}
			built, err := rcaReadWithAdmission(readCtx, deadline, rcaImportedAdmissionRetry, func() (*rca.Series, error) {
				select {
				case rcaImportedReadGate <- struct{}{}:
				case <-readCtx.Done():
					return nil, errRCAImportedEvidenceBusy
				}
				defer func() { <-rcaImportedReadGate }()
				return s.readImportedSeries(readCtx, snapshot, key, from, to, bucket)
			})
			expired := readCtx.Err() != nil
			cancel()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, nil, ctxErr
			}
			// Only an admitted read is a query; refused attempts cost nothing.
			if !errors.Is(err, errRCAImportedEvidenceBusy) {
				queries++
			}
			if errors.Is(err, errRCAImportedEvidenceBusy) || expired {
				// The read budget ran out waiting for the slot or inside a
				// read. With nothing imported read yet the analysis fails
				// and can be retried; otherwise the rest is a budget cut.
				if len(series) == 0 && len(datasetSeries) == 0 && errors.Is(err, errRCAImportedEvidenceBusy) {
					return nil, nil, errRCAImportedEvidenceBusy
				}
				dropped++
				continue
			}
			if err != nil {
				note(limitationImportedDatasetUnavailable, fmt.Sprintf("a series of dataset %s could not be read: %v", want.DatasetID, err))
				continue
			}
			if built != nil {
				datasetSeries = append(datasetSeries, *built)
			}
		}
		// The dataset must still be the one the evidence froze after its
		// reads, or what was read describes something else.
		if _, current := unchanged(want); !current {
			note(limitationImportedDatasetChanged, "associated dataset "+want.DatasetID+" changed or lost its association while it was read and was not judged")
			continue
		}
		for _, built := range datasetSeries {
			longest = max(longest, len(built.Points))
		}
		series = append(series, datasetSeries...)
	}
	budgeter.SeriesPoints(longest)
	if unread > 0 {
		note(limitationImportedSeriesUnenumerated, fmt.Sprintf("%d histogram or summary measures of associated datasets are not read as series", unread))
	}
	if dropped > 0 {
		note(limitationDetectionEvidenceCut, fmt.Sprintf("the series budget, the analysis budget or the imported read budget left out %d imported series", dropped))
	}
	return series, limitations, nil
}

// rcaReadWithAdmission runs read, retrying while the single native-analysis
// slot is held by another job, waiting from wait and doubling (at most
// rcaImportedAdmissionMaxWait between attempts), until deadline (zero: until
// ctx ends). A slot still busy at the deadline is errRCAImportedEvidenceBusy.
func rcaReadWithAdmission(ctx context.Context, deadline time.Time, wait time.Duration, read func() (*rca.Series, error)) (*rca.Series, error) {
	for attempt := 1; ; attempt++ {
		built, err := read()
		if !errors.Is(err, errAnalyticalAdmissionUnavailable) {
			return built, err
		}
		pause := min(wait<<min(attempt-1, 5), rcaImportedAdmissionMaxWait)
		if !deadline.IsZero() && time.Now().Add(pause).After(deadline) {
			return nil, errRCAImportedEvidenceBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pause):
		}
	}
}

// readImportedSeries runs one bounded native query for a dataset series. A
// counter is read as its per-second rate; other kinds as the bucket mean.
func (s *Server) readImportedSeries(ctx context.Context, in *storage.AnalysisImport, key rcaImportedSeriesKey, from, to time.Time, bucket time.Duration) (*rca.Series, error) {
	aggregation := "avg"
	switch key.kind {
	case analysis.KindCounter:
		aggregation = "rate"
	case analysis.KindHistogram, analysis.KindSummary:
		return nil, nil
	}
	query := analytics.SeriesQuery{ProjectID: in.ProjectID, DatasetID: in.ID, Metric: key.metric, SourceID: key.identity.Source, Host: key.identity.Host, Service: key.identity.Service,
		Dimensions: maps.Clone(key.identity.Dimensions), From: from, To: to, BucketSeconds: int(bucket / time.Second), Aggregation: aggregation}
	points, err := rcaQueryImportedDataset(ctx, in, query)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return nil, nil
	}
	sample := points[0]
	var seriesPoints []rca.SeriesPoint
	for _, p := range points {
		value := 0.0
		if p.Value != nil {
			value = *p.Value
		}
		valid := p.Value != nil && p.Quality == analysis.QualityValid && !math.IsNaN(value) && !math.IsInf(value, 0)
		seriesPoints = append(seriesPoints, rca.SeriesPoint{At: p.TimestampUTC.UTC(), Value: value, Valid: valid})
	}
	// The identity names the series on the grid it is finally judged on.
	seriesPoints, bucket = coarsenImportedSeries(seriesPoints, bucket)
	identity, err := analysis.MeasurementSeriesIdentity(sample, analysis.SeriesObservation{Mode: rca.ObservationInterval, BucketNanos: int64(bucket)})
	if err != nil {
		return nil, err
	}
	id := "imported/" + in.ID + "/" + key.metric + "/" + identity.SHA256[:12]
	out := &rca.Series{ID: id, Kind: rca.SeriesImported, Unit: sample.Unit, Observation: rca.ObservationInterval, Bucket: bucket, Identity: identity.SHA256, Points: seriesPoints,
		Evidence: rca.EvidenceRef{Kind: "imported_series", Locator: fmt.Sprintf("dataset:%s@%d#%s", in.ID, in.Revision, id)},
		Measures: signatures.Classify(key.metric), Nodes: importedSeriesNodes(sample)}
	return out, nil
}

// coarsenImportedSeries regrids a dataset sampled more coarsely than the
// run's grid onto its own sampling interval (the median gap between valid
// buckets, a whole multiple of the run grid), averaging the valid buckets in
// each; otherwise most buckets would be gaps the detector cannot judge.
func coarsenImportedSeries(points []rca.SeriesPoint, bucket time.Duration) ([]rca.SeriesPoint, time.Duration) {
	var gaps []time.Duration
	var previous *time.Time
	for i := range points {
		if !points[i].Valid {
			continue
		}
		if previous != nil {
			gaps = append(gaps, points[i].At.Sub(*previous))
		}
		previous = &points[i].At
	}
	if len(gaps) == 0 || bucket <= 0 {
		return points, bucket
	}
	slices.Sort(gaps)
	factor := int64((gaps[len(gaps)/2] + bucket/2) / bucket)
	if factor < 2 {
		return points, bucket
	}
	wide := time.Duration(factor) * bucket
	origin := points[0].At
	var out []rca.SeriesPoint
	sums, counts := map[int64]float64{}, map[int64]int{}
	for _, p := range points {
		slot := int64(p.At.Sub(origin) / wide)
		if len(out) == 0 || out[len(out)-1].At != origin.Add(time.Duration(slot)*wide) {
			out = append(out, rca.SeriesPoint{At: origin.Add(time.Duration(slot) * wide)})
		}
		if p.Valid {
			sums[slot] += p.Value
			counts[slot]++
		}
	}
	for i := range out {
		slot := int64(out[i].At.Sub(origin) / wide)
		if counts[slot] > 0 {
			out[i].Value, out[i].Valid = sums[slot]/float64(counts[slot]), true
		}
	}
	return out, wide
}

// importedSeriesNodes are the infrastructure nodes an imported series names:
// its host and its infrastructure dimensions.
func importedSeriesNodes(point analysis.MetricPoint) []string {
	nodes := pointNodes(point)
	if host := rca.DependencyHost(strings.TrimSpace(point.Host)); host != "" {
		nodes = append(nodes, rca.InfrastructureNodeID("host", host))
	}
	slices.Sort(nodes)
	nodes = slices.Compact(nodes)
	if len(nodes) > maxProviderSeriesNodes {
		nodes = nodes[:maxProviderSeriesNodes]
	}
	return nodes
}

// rcaDatasetsDigest identifies a frozen dataset set for evidence reuse.
func rcaDatasetsDigest(datasets []rcaEvidenceDataset) string {
	body, _ := json.Marshal(datasets)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

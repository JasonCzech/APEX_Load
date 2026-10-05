package api

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"os"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	envArchiveExemplarsMaxBytes = "APEXLOAD_ARCHIVE_EXEMPLARS_MAX_BYTES"

	exemplarsArchiveFileName = "exemplars.jsonl"
	exemplarSchema           = "apexload.exemplar.v1"
	exemplarRecordType       = "exemplars_v1"

	// Post-run exemplar reasons, in output order within an interval.
	exemplarReasonError      = "error"
	exemplarReasonSlowest    = "slowest_success"
	exemplarReasonNearMedian = "near_median"

	exemplarsPerReason = 2
)

// Selection bounds. Variables so tests can exercise truncation.
var (
	maxRunExemplars         = 20000
	maxTransactionExemplars = 2000
	maxExemplarTransactions = 1000
	// maxExemplarSlots bounds the transaction-interval pairs held while the
	// rows are scanned; each holds at most six candidates.
	maxExemplarSlots = 100000
)

// ArchiveExemplar is one request picked after the run as evidence for one
// transaction in one interval.
type ArchiveExemplar struct {
	Schema        string    `json:"schema"`
	Population    string    `json:"population"`
	Group         string    `json:"group,omitempty"`
	Name          string    `json:"name"`
	IntervalStart time.Time `json:"interval_start"`
	IntervalEnd   time.Time `json:"interval_end"`
	Reason        string    `json:"reason"`
	TraceID       string    `json:"trace_id"`
	SpanID        string    `json:"span_id,omitempty"`
	DurationMs    float64   `json:"duration_ms"`
	Success       bool      `json:"success"`
	StatusCode    int       `json:"status_code,omitempty"`
	ErrorClass    string    `json:"error_class,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	// IntervalMedianMs is the interval median a near_median pick was close to.
	IntervalMedianMs *float64 `json:"interval_median_ms,omitempty"`
}

// exemplarRow decodes only the row fields the selector reads.
type exemplarRow struct {
	Timestamp           time.Time `json:"timestamp"`
	Population          string    `json:"population"`
	BusinessTransaction bool      `json:"business_transaction"`
	DependencyType      string    `json:"dependency_type"`
	Group               string    `json:"group"`
	ActionName          string    `json:"action_name"`
	DurationMs          float64   `json:"duration_ms"`
	Success             bool      `json:"success"`
	StatusCode          int       `json:"status_code"`
	ErrorMessage        string    `json:"error_message"`
	TraceSent           *bool     `json:"trace_sent"`
	SentTraceID         string    `json:"sent_trace_id"`
	SentSpanID          string    `json:"sent_span_id"`
}

type exemplarKey struct{ population, group, name string }

type exemplarSlot struct {
	slowest []ArchiveExemplar
	// errors holds the earliest error of up to two distinct classes.
	errors []ArchiveExemplar
	median []ArchiveExemplar
}

// exemplarBefore orders candidates: longer duration, then earlier, then trace ID.
func exemplarBefore(a, b ArchiveExemplar) bool {
	if a.DurationMs != b.DurationMs {
		return a.DurationMs > b.DurationMs
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.TraceID < b.TraceID
}

// earlierExemplar orders errors: earlier, then longer, then trace ID.
func earlierExemplar(a, b ArchiveExemplar) bool {
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	if a.DurationMs != b.DurationMs {
		return a.DurationMs > b.DurationMs
	}
	return a.TraceID < b.TraceID
}

func keepTop(list []ArchiveExemplar, candidate ArchiveExemplar, before func(a, b ArchiveExemplar) bool) []ArchiveExemplar {
	list = append(list, candidate)
	sort.SliceStable(list, func(i, j int) bool { return before(list[i], list[j]) })
	if len(list) > exemplarsPerReason {
		list = list[:exemplarsPerReason]
	}
	return list
}

// keepEarliestClasses keeps the earliest error of each of the two classes
// whose earliest errors come first. A class's earliest error only moves
// earlier, so a class dropped here can return only by beating a kept one.
func keepEarliestClasses(list []ArchiveExemplar, candidate ArchiveExemplar) []ArchiveExemplar {
	for i, existing := range list {
		if existing.ErrorClass == candidate.ErrorClass {
			if earlierExemplar(candidate, existing) {
				list[i] = candidate
				sort.SliceStable(list, func(a, b int) bool { return earlierExemplar(list[a], list[b]) })
			}
			return list
		}
	}
	return keepTop(list, candidate, earlierExemplar)
}

func medianDistance(e ArchiveExemplar) float64 {
	if e.IntervalMedianMs == nil {
		return math.Inf(1)
	}
	return math.Abs(e.DurationMs - *e.IntervalMedianMs)
}

func nearerMedian(a, b ArchiveExemplar) bool {
	if da, db := medianDistance(a), medianDistance(b); da != db {
		return da < db
	}
	return earlierExemplar(a, b)
}

// persistExemplarArchive writes exemplars.jsonl once after finalize. It has
// its own byte allowance so it cannot exhaust the adjunct budget.
func (tr *TestRunner) persistExemplarArchive(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if manifest.ExemplarsFile != "" {
		return nil
	}
	exemplars, truncated, err := selectArchiveExemplars(ctx, record, manifest)
	if err != nil {
		return err
	}
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchiveExemplarsMaxBytes, 64<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	var count int64
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, exemplarsArchiveFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		for _, exemplar := range exemplars {
			line, err := json.Marshal(exemplar)
			if err != nil {
				return 0, err
			}
			line = append(line, '\n')
			if !budget.admits(len(line)) {
				truncated = true
				return 0, nil
			}
			if _, err := out.Write(line); err != nil {
				return 0, err
			}
			count++
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.ExemplarsFile = exemplarsArchiveFileName
	manifest.ExemplarCount = count
	manifest.ExemplarsTruncated = truncated
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: exemplarsArchiveFileName, Kind: "evidence", Format: "jsonl", RecordType: exemplarRecordType, Count: count})
}

// selectArchiveExemplars picks, per transaction and interval, up to two
// slowest successes, two errors of distinct classes and two requests nearest
// the interval median, from rows whose trace context was sent. The result is
// a pure function of the archive, so repeated runs are byte-identical.
func selectArchiveExemplars(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) ([]ArchiveExemplar, bool, error) {
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(summaries, func(i, j int) bool { return summaries[i].Timestamp.Before(summaries[j].Timestamp) })
	windows := archiveIntervalWindows(summaries)
	truncated := false
	if len(windows) > intervalDistributionMaxBuckets {
		windows, truncated = windows[:intervalDistributionMaxBuckets], true
	}
	if len(windows) == 0 {
		return nil, truncated, nil
	}
	medians, err := readIntervalMedians(record, manifest)
	if err != nil {
		return nil, false, err
	}
	slots := map[exemplarKey]map[int]*exemplarSlot{}
	slotCount := 0
	err = forEachArchiveRecordContext(ctx, record, func(m *RunArchiveManifest) string { return m.TransactionsFile }, func(row *exemplarRow) error {
		if row.TraceSent == nil || !*row.TraceSent || !protocol.ValidTraceID(row.SentTraceID) || math.IsNaN(row.DurationMs) || math.IsInf(row.DurationMs, 0) || row.DurationMs < 0 || row.ActionName == "" || len(row.ActionName) > 256 || len(row.Group) > 256 {
			return nil
		}
		at := row.Timestamp.UTC()
		index := sort.Search(len(windows), func(i int) bool { return !windows[i].end.Before(at) })
		if index == len(windows) || !at.After(windows[index].start) {
			return nil
		}
		population := archiveEventPopulation(TransactionEvent{Population: row.Population, BusinessTransaction: row.BusinessTransaction, DependencyType: row.DependencyType})
		key := exemplarKey{population: population, group: row.Group, name: row.ActionName}
		byInterval := slots[key]
		if byInterval == nil {
			if len(slots) >= maxExemplarTransactions {
				truncated = true
				return nil
			}
			byInterval = map[int]*exemplarSlot{}
			slots[key] = byInterval
		}
		slot := byInterval[index]
		if slot == nil {
			if slotCount >= maxExemplarSlots {
				truncated = true
				return nil
			}
			slotCount++
			slot = &exemplarSlot{}
			byInterval[index] = slot
		}
		spanID := row.SentSpanID
		if len(spanID) > 16 {
			spanID = ""
		}
		candidate := ArchiveExemplar{
			Schema: exemplarSchema, Population: population, Group: row.Group, Name: row.ActionName,
			IntervalStart: windows[index].start, IntervalEnd: windows[index].end,
			TraceID: row.SentTraceID, SpanID: spanID, DurationMs: row.DurationMs, Success: row.Success,
			StatusCode: row.StatusCode, Timestamp: at,
		}
		if !row.Success {
			candidate.Reason = exemplarReasonError
			candidate.ErrorClass = engine.ClassifyError(row.StatusCode, row.ErrorMessage, false)
			slot.errors = keepEarliestClasses(slot.errors, candidate)
			return nil
		}
		slowest := candidate
		slowest.Reason = exemplarReasonSlowest
		slot.slowest = keepTop(slot.slowest, slowest, exemplarBefore)
		if median, ok := medians.lookup(population, row.ActionName, windows[index].end); ok {
			near := candidate
			near.Reason = exemplarReasonNearMedian
			near.IntervalMedianMs = &median
			slot.median = keepTop(slot.median, near, nearerMedian)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	keys := make([]exemplarKey, 0, len(slots))
	for key := range slots {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].population != keys[j].population {
			return keys[i].population < keys[j].population
		}
		if keys[i].group != keys[j].group {
			return keys[i].group < keys[j].group
		}
		return keys[i].name < keys[j].name
	})
	perKey := make([][]ArchiveExemplar, 0, len(keys))
	for _, key := range keys {
		picked, cut := capRoundRobin(intervalExemplarLists(slots[key]), maxTransactionExemplars)
		truncated = truncated || cut
		perKey = append(perKey, picked)
	}
	selected, cut := capRoundRobin(perKey, maxRunExemplars)
	truncated = truncated || cut
	sort.SliceStable(selected, func(i, j int) bool { return exemplarOutputBefore(selected[i], selected[j]) })
	return selected, truncated, nil
}

// intervalExemplarLists orders each interval's picks: errors (two distinct
// classes, earliest first), slowest successes, then near-median requests.
// A request picked for an earlier reason is not repeated.
func intervalExemplarLists(byInterval map[int]*exemplarSlot) [][]ArchiveExemplar {
	indexes := make([]int, 0, len(byInterval))
	for index := range byInterval {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	lists := make([][]ArchiveExemplar, 0, len(indexes))
	for _, index := range indexes {
		slot := byInterval[index]
		seen := map[string]bool{}
		var list []ArchiveExemplar
		for _, group := range [][]ArchiveExemplar{slot.errors, slot.slowest, slot.median} {
			for _, e := range group {
				if !seen[e.TraceID] {
					seen[e.TraceID] = true
					list = append(list, e)
				}
			}
		}
		lists = append(lists, list)
	}
	return lists
}

// capRoundRobin takes one item from each list in turn until limit, so a cap
// spreads across intervals or transactions instead of keeping only the first.
func capRoundRobin(lists [][]ArchiveExemplar, limit int) ([]ArchiveExemplar, bool) {
	total := 0
	for _, list := range lists {
		total += len(list)
	}
	if total <= limit {
		out := make([]ArchiveExemplar, 0, total)
		for _, list := range lists {
			out = append(out, list...)
		}
		return out, false
	}
	out := make([]ArchiveExemplar, 0, limit)
	for depth := 0; len(out) < limit; depth++ {
		added := false
		for _, list := range lists {
			if depth < len(list) && len(out) < limit {
				out = append(out, list[depth])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out, true
}

var exemplarReasonRank = map[string]int{exemplarReasonError: 0, exemplarReasonSlowest: 1, exemplarReasonNearMedian: 2}

func exemplarOutputBefore(a, b ArchiveExemplar) bool {
	switch {
	case a.Population != b.Population:
		return a.Population < b.Population
	case a.Group != b.Group:
		return a.Group < b.Group
	case a.Name != b.Name:
		return a.Name < b.Name
	case !a.IntervalEnd.Equal(b.IntervalEnd):
		return a.IntervalEnd.Before(b.IntervalEnd)
	case exemplarReasonRank[a.Reason] != exemplarReasonRank[b.Reason]:
		return exemplarReasonRank[a.Reason] < exemplarReasonRank[b.Reason]
	}
	return exemplarBefore(a, b)
}

type intervalMedianKey struct {
	dimension, name string
	endUnixNano     int64
}

type intervalMedians map[intervalMedianKey]float64

// lookup prefers the row's own population and falls back to the transaction
// dimension, which live evidence keys by every request name.
func (m intervalMedians) lookup(population, name string, end time.Time) (float64, bool) {
	if median, ok := m[intervalMedianKey{population, name, end.UnixNano()}]; ok {
		return median, true
	}
	median, ok := m[intervalMedianKey{"transaction", name, end.UnixNano()}]
	return median, ok
}

// readIntervalMedians loads each interval's P50 from the archived interval
// distributions. Intervals without one get no near-median picks.
func readIntervalMedians(record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (intervalMedians, error) {
	medians := intervalMedians{}
	if manifest == nil || manifest.IntervalDistributionsFile == "" {
		return medians, nil
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.IntervalDistributionsFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		var r IntervalDistributionRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return nil, err
		}
		end := r.Timestamp.UTC().UnixNano()
		for dimension, values := range r.Dimensions {
			if dimension != "transaction" && dimension != "action" {
				continue
			}
			for name, metric := range values {
				if metric.Count > 0 && !math.IsNaN(metric.P50) && !math.IsInf(metric.P50, 0) {
					medians[intervalMedianKey{dimension, name, end}] = metric.P50
				}
			}
		}
	}
	return medians, scanner.Err()
}

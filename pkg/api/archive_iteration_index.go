package api

import (
	"cmp"
	"hash/fnv"
	"math"
	"slices"
	"strings"
	"time"
)

// Iteration index limits; package vars so tests can lower them.
var (
	// maxIterationIndexIdentities bounds the open-iteration table. When it is
	// full, the stalest half (by last row) is closed and released.
	maxIterationIndexIdentities = 65536
	// iterationIndexSampleK is the bottom-k sample the p50/p95 picks come from.
	iterationIndexSampleK = 4096
	// iterationIndexTopN is how many slowest and failed iterations are kept.
	iterationIndexTopN = 5
)

// archiveIterationIndexAccumulator builds RunArchiveIterationIndex in the
// report's single pass. Iterations of one VUser lifetime are sequential, so
// each identity keeps only its open iteration and the one before it. The
// previous iteration closes when the identity moves on once more (or at
// eviction and finalize), so a row of it written after the identity moved
// on (a background request archived late) still counts in its span and
// rows, as the replay finds it.
type archiveIterationIndexAccumulator struct {
	states  map[timeAnatomyIdentity]*iterationIndexState
	spans   archiveQuantileAccumulator
	maxSpan float64
	timed   int64
	total   int64
	quality RunArchiveIterationIdentityQuality
	outside int64
	late    int64
	untimed int64
	evicted int64
	seen    int64
	// partial counts timed iterations left out of the span distribution:
	// cut short by the test's stop, or split by identity eviction.
	partial int64
	// resplit counts evicted iterations their identity resumed: those are
	// really split (identity_evicted).
	resplit int64
	// evictedIDs holds identities evicted with an open iteration, with that
	// iteration's number and last row, so a later row of the same iteration
	// marks both the closed record and the resumed one split. It is bounded
	// like the state table: when full, its stalest half (by last row) moves
	// into pruned.
	evictedIDs map[timeAnatomyIdentity]iterationIndexEvicted
	// pruned remembers, in fixed memory, the (identity, iteration) pairs
	// dropped from evictedIDs, so one that still resumes is flagged split
	// (a rare false positive flags a new identity split too). New identities
	// that never were evicted stay whole.
	pruned  *iterationIndexPrunedFilter
	slowest []iterationIndexRecord
	failed  []iterationIndexRecord
	sample  []iterationIndexRecord // max-heap on priority: the bottom-k
	sampled bool
}

type iterationIndexEvicted struct {
	iteration   int64
	lastOrdinal int64
}

// iterationIndexPrunedFilterWords sizes the pruned-pair filter: 1<<16 words
// is 4 Mi bits (512 KiB), about a 0.25% false-positive rate at 200k pairs.
const iterationIndexPrunedFilterWords = 1 << 16

// iterationIndexPrunedFilter is a Bloom filter (three probes, deterministic
// FNV-1a hashing) over (identity, iteration) pairs.
type iterationIndexPrunedFilter struct {
	bits []uint64
}

func iterationIndexPairHash(identity timeAnatomyIdentity, iteration int64) (uint64, uint64) {
	hasher := fnv.New64a()
	var buf [32]byte
	_, _ = hasher.Write([]byte(identity.group))
	for index, value := range []uint64{0, uint64(identity.vuserID), identity.lifetime, uint64(iteration)} {
		for b := range 8 {
			buf[index*8+b] = byte(value >> (8 * b))
		}
	}
	_, _ = hasher.Write(buf[:])
	sum := hasher.Sum64()
	// Double hashing: the second probe step is odd so it cycles the table.
	return sum, (sum>>33 | sum<<31) | 1
}

func (f *iterationIndexPrunedFilter) add(identity timeAnatomyIdentity, iteration int64) {
	h1, h2 := iterationIndexPairHash(identity, iteration)
	size := uint64(len(f.bits) * 64)
	for probe := range uint64(3) {
		bit := (h1 + probe*h2) % size
		f.bits[bit/64] |= 1 << (bit % 64)
	}
}

func (f *iterationIndexPrunedFilter) mayContain(identity timeAnatomyIdentity, iteration int64) bool {
	if f == nil {
		return false
	}
	h1, h2 := iterationIndexPairHash(identity, iteration)
	size := uint64(len(f.bits) * 64)
	for probe := range uint64(3) {
		bit := (h1 + probe*h2) % size
		if f.bits[bit/64]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

type iterationIndexState struct {
	legacy bool
	open   bool
	cur    iterationIndexRecord
	// prev is the identity's previous iteration, held open for late rows.
	prev    iterationIndexRecord
	hasPrev bool
	// pending is a legacy row of a lower iteration than the open one, held
	// until the identity's next row says what it was (legacyIterationLate).
	pending *iterationIndexPending
}

type iterationIndexPending struct {
	tx         *TransactionEvent
	population string
	ordinal    int64
}

// legacyIterationLate is the rule for a legacy row (no lifetime) whose
// iteration is lower than the identity's open one: it is a late row of an
// earlier iteration when the identity's next row continues at or above the
// open iteration, and a restart of a reused VUser ID otherwise (the next row
// stays lower, or there is none). The index, the full replay scan and the
// hinted replay scan all decide with it, so they split rows alike.
func legacyIterationLate(openIteration, nextIteration int64) bool {
	return nextIteration >= openIteration
}

type iterationIndexRecord struct {
	identity     timeAnatomyIdentity
	legacy       bool
	iteration    int64
	firstOrdinal int64
	lastOrdinal  int64
	startNs      int64
	endNs        int64
	timed        bool
	txRows       int64
	reqRows      int64
	failedRows   int64
	priority     uint64
	// split is set when identity eviction closed this iteration early, or
	// when this record resumed an evicted identity: the iteration's rows may
	// lie outside first..last.
	split bool
	// interrupted is set when a row was interrupted by the test's stop or
	// ramp-down: the iteration was cut short.
	interrupted bool
}

// whole reports whether the record is a complete iteration: not cut short by
// the test's stop and not split by identity eviction. Only whole iterations
// feed the span distribution and the p50/p95 picks.
func (r *iterationIndexRecord) whole() bool {
	return !r.split && !r.interrupted
}

func (r *iterationIndexRecord) spanMs() float64 {
	if !r.timed {
		return 0
	}
	return float64(r.endNs-r.startNs) / 1e6
}

func newArchiveIterationIndexAccumulator() *archiveIterationIndexAccumulator {
	return &archiveIterationIndexAccumulator{
		states:     map[timeAnatomyIdentity]*iterationIndexState{},
		evictedIDs: map[timeAnatomyIdentity]iterationIndexEvicted{},
	}
}

// merge adds one archived row. ordinal is its line index in
// transactions.jsonl.
func (a *archiveIterationIndexAccumulator) merge(tx *TransactionEvent, population string, ordinal int64) {
	if a == nil || tx == nil || (population != "action" && population != "transaction") {
		return
	}
	if tx.Iteration <= 0 || (tx.Phase != "" && tx.Phase != "action") {
		a.outside++
		return
	}
	identity, quality := timeAnatomyIdentityOf(tx.Group, tx.VUserID, tx.VUserLifetime)
	if quality == timeAnatomyIdentityNone {
		a.quality.NoneRows++
		return
	}
	state := a.states[identity]
	resumed := false
	if state == nil {
		// A lifetime's iterations only grow: a row below the evicted
		// iteration is a late row of an iteration already closed. Keep the
		// eviction record so the evicted iteration's next row still resumes
		// it (a legacy VUser ID can restart, so it opens a record instead).
		if evicted, ok := a.evictedIDs[identity]; ok && quality != timeAnatomyIdentityLegacy && tx.Iteration < evicted.iteration {
			a.late++
			return
		}
		if len(a.states) >= maxIterationIndexIdentities {
			a.evictStalest()
		}
		state = &iterationIndexState{legacy: quality == timeAnatomyIdentityLegacy}
		a.states[identity] = state
		a.seen++
		if evicted, ok := a.evictedIDs[identity]; ok {
			delete(a.evictedIDs, identity)
			// Only a row of the evicted iteration continues it; a later
			// iteration starts cleanly (an earlier one is a late row).
			if tx.Iteration == evicted.iteration {
				resumed = true
			}
		} else if a.pruned.mayContain(identity, tx.Iteration) {
			resumed = true
		}
		if resumed {
			a.resplit++
			a.markEvictedSplit(identity, tx.Iteration)
		}
	}
	if pending := state.pending; pending != nil {
		state.pending = nil
		if legacyIterationLate(state.cur.iteration, tx.Iteration) {
			a.addLate(state, pending.tx, pending.population, pending.ordinal)
		} else {
			// A reused VUser ID restarted: the held row opens its iteration,
			// and no later row belongs to the old instance's iterations.
			a.closeAll(state)
			a.add(state, identity, pending.tx, pending.population, pending.ordinal, false)
		}
	}
	if state.open && tx.Iteration != state.cur.iteration {
		if tx.Iteration < state.cur.iteration {
			// A lifetime's iterations only grow: a lower one is a late row
			// of an iteration already closed. A legacy VUser ID can also
			// restart (a reused ID): the next row decides.
			if !state.legacy {
				a.addLate(state, tx, population, ordinal)
				return
			}
			// The caller may reuse tx for its next row: hold a copy.
			held := *tx
			state.pending = &iterationIndexPending{tx: &held, population: population, ordinal: ordinal}
			return
		}
		a.moveOn(state)
	}
	a.add(state, identity, tx, population, ordinal, resumed)
}

// moveOn closes the identity's previous iteration and holds the open one as
// previous, for its late rows.
func (a *archiveIterationIndexAccumulator) moveOn(state *iterationIndexState) {
	if state.hasPrev {
		a.close(&state.prev)
	}
	state.prev, state.hasPrev = state.cur, true
	state.open = false
}

// closeAll closes the identity's previous and open iterations.
func (a *archiveIterationIndexAccumulator) closeAll(state *iterationIndexState) {
	if state.hasPrev {
		a.close(&state.prev)
		state.hasPrev = false
	}
	if state.open {
		a.close(&state.cur)
		state.open = false
	}
}

// addLate takes a row of an iteration lower than the identity's open one.
// A row of the previous iteration joins its record; an older one is only
// counted (late_rows), outside every span.
func (a *archiveIterationIndexAccumulator) addLate(state *iterationIndexState, tx *TransactionEvent, population string, ordinal int64) {
	if state.hasPrev && state.prev.iteration == tx.Iteration {
		addIterationIndexRow(&state.prev, tx, population, ordinal)
		return
	}
	a.late++
}

// add puts one row into the identity's open iteration, opening it first.
func (a *archiveIterationIndexAccumulator) add(state *iterationIndexState, identity timeAnatomyIdentity, tx *TransactionEvent, population string, ordinal int64, resumed bool) {
	if !state.open {
		state.open = true
		state.cur = iterationIndexRecord{
			identity: identity, legacy: state.legacy, iteration: tx.Iteration,
			firstOrdinal: ordinal, lastOrdinal: ordinal,
			// Only the first record after a resume can continue the
			// iteration eviction split.
			split: resumed,
		}
	}
	addIterationIndexRow(&state.cur, tx, population, ordinal)
}

// addIterationIndexRow adds one row to an iteration's record.
func addIterationIndexRow(cur *iterationIndexRecord, tx *TransactionEvent, population string, ordinal int64) {
	cur.lastOrdinal = max(cur.lastOrdinal, ordinal)
	if population == "transaction" {
		cur.txRows++
	} else {
		cur.reqRows++
	}
	// A row interrupted by the test's stop or ramp-down is not a failure:
	// the replay shows it as interrupted, and the time anatomy leaves it out.
	if tx.Interrupted {
		cur.interrupted = true
	} else if !tx.Success {
		cur.failedRows++
	}
	if startNs, endNs, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, tx.ElapsedMs); ok {
		if !cur.timed {
			cur.startNs, cur.endNs, cur.timed = startNs, endNs, true
		} else {
			cur.startNs, cur.endNs = min(cur.startNs, startNs), max(cur.endNs, endNs)
		}
	}
}

// markEvictedSplit flags the already-closed record of an evicted iteration
// its identity resumed, wherever a copy was kept, so its replay does not
// trust first..last. Its span already counted (the distribution cannot
// forget one); the p50/p95 picks skip it.
func (a *archiveIterationIndexAccumulator) markEvictedSplit(identity timeAnatomyIdentity, iteration int64) {
	for _, records := range [][]iterationIndexRecord{a.slowest, a.failed, a.sample} {
		for index := range records {
			if records[index].identity == identity && records[index].iteration == iteration {
				records[index].split = true
			}
		}
	}
}

// evictStalest closes the open iterations of the half of the identities whose
// last row is oldest. A lifetime that has not written for that long has
// usually ended, so its iteration closes whole; only when the identity
// writes again to that iteration are both records flagged split
// (identity_evicted).
func (a *archiveIterationIndexAccumulator) evictStalest() {
	last := make([]int64, 0, len(a.states))
	for _, state := range a.states {
		last = append(last, state.cur.lastOrdinal)
	}
	slices.Sort(last)
	cutoff := last[len(last)/2]
	// Close the victims in a fixed order (oldest last row first, then
	// identity), not map order: the evicted table can fill partway through
	// this pass, and which identities its prune moves into the filter (late
	// rows versus new iterations later) must not differ between rebuilds of
	// the same archive.
	victims := make([]timeAnatomyIdentity, 0, len(a.states)/2+1)
	for identity, state := range a.states {
		if state.cur.lastOrdinal <= cutoff {
			victims = append(victims, identity)
		}
	}
	slices.SortFunc(victims, func(left, right timeAnatomyIdentity) int {
		return cmp.Or(
			cmp.Compare(a.states[left].cur.lastOrdinal, a.states[right].cur.lastOrdinal),
			compareTimeAnatomyIdentity(left, right),
		)
	})
	for _, identity := range victims {
		state := a.states[identity]
		a.resolvePending(state)
		if state.hasPrev {
			a.close(&state.prev)
			state.hasPrev = false
		}
		if state.open {
			a.close(&state.cur)
			if len(a.evictedIDs) >= maxIterationIndexIdentities {
				a.pruneEvicted()
			}
			a.evictedIDs[identity] = iterationIndexEvicted{iteration: state.cur.iteration, lastOrdinal: state.cur.lastOrdinal}
		}
		delete(a.states, identity)
		a.evicted++
	}
}

// compareTimeAnatomyIdentity orders identities by group, VUser ID, then
// lifetime.
func compareTimeAnatomyIdentity(left, right timeAnatomyIdentity) int {
	return cmp.Or(
		strings.Compare(left.group, right.group),
		cmp.Compare(left.vuserID, right.vuserID),
		cmp.Compare(left.lifetime, right.lifetime),
	)
}

// pruneEvicted moves the stalest half of evictedIDs (by last row) into the
// fixed-size pruned filter. Those identities have not written for longest;
// one that still resumes is caught by the filter.
func (a *archiveIterationIndexAccumulator) pruneEvicted() {
	if len(a.evictedIDs) == 0 {
		return
	}
	last := make([]int64, 0, len(a.evictedIDs))
	for _, evicted := range a.evictedIDs {
		last = append(last, evicted.lastOrdinal)
	}
	slices.Sort(last)
	cutoff := last[len(last)/2]
	if a.pruned == nil {
		a.pruned = &iterationIndexPrunedFilter{bits: make([]uint64, iterationIndexPrunedFilterWords)}
	}
	for identity, evicted := range a.evictedIDs {
		if evicted.lastOrdinal > cutoff {
			continue
		}
		a.pruned.add(identity, evicted.iteration)
		delete(a.evictedIDs, identity)
	}
}

// resolvePending settles a held legacy row when no next row will come (the
// identity is evicted, or the archive ended): with nothing continuing the
// open iteration, it is a restart.
func (a *archiveIterationIndexAccumulator) resolvePending(state *iterationIndexState) {
	pending := state.pending
	if pending == nil {
		return
	}
	state.pending = nil
	identity := state.cur.identity
	a.closeAll(state)
	a.add(state, identity, pending.tx, pending.population, pending.ordinal, false)
}

func (a *archiveIterationIndexAccumulator) close(record *iterationIndexRecord) {
	a.total++
	if record.legacy {
		a.quality.Legacy++
	} else {
		a.quality.Lifetime++
	}
	closed := *record
	closed.priority = timeAnatomyPriority(closed.firstOrdinal)
	// A failed iteration is replayable whether or not its rows are timed;
	// only the span, slowest and sample bookkeeping need timestamps.
	if closed.failedRows > 0 {
		a.failed = iterationIndexInsertTop(a.failed, closed, func(left, right *iterationIndexRecord) int {
			return cmp.Compare(left.firstOrdinal, right.firstOrdinal)
		})
	}
	if !closed.timed {
		a.untimed++
		return
	}
	span := closed.spanMs()
	// A partial iteration can still be the slowest (a request hung until
	// the stop is worth replaying); its ref carries split / interrupted so
	// the chip says why its span can exceed the whole iterations' max.
	a.slowest = iterationIndexInsertTop(a.slowest, closed, func(left, right *iterationIndexRecord) int {
		return cmp.Or(cmp.Compare(right.spanMs(), left.spanMs()), cmp.Compare(left.firstOrdinal, right.firstOrdinal))
	})
	// An iteration cut short by the test's stop, or split, is not a typical
	// iteration: it stays out of the span distribution and the p50/p95 picks.
	if !closed.whole() {
		a.partial++
		return
	}
	a.timed++
	a.spans.Add(span)
	a.maxSpan = max(a.maxSpan, span)
	a.insertSample(closed)
}

// iterationIndexInsertTop keeps the iterationIndexTopN smallest records by
// order, sorted.
func iterationIndexInsertTop(top []iterationIndexRecord, record iterationIndexRecord, order func(left, right *iterationIndexRecord) int) []iterationIndexRecord {
	if len(top) >= iterationIndexTopN && order(&record, &top[len(top)-1]) >= 0 {
		return top
	}
	at, _ := slices.BinarySearchFunc(top, record, func(item, target iterationIndexRecord) int { return order(&item, &target) })
	top = slices.Insert(top, at, record)
	if len(top) > iterationIndexTopN {
		top = top[:iterationIndexTopN]
	}
	return top
}

// insertSample keeps the bottom-k records by priority (a deterministic hash
// of first_ordinal), like the time anatomy's instance sample.
func (a *archiveIterationIndexAccumulator) insertSample(record iterationIndexRecord) {
	k := max(iterationIndexSampleK, 1)
	switch {
	case len(a.sample) < k:
		a.sample = append(a.sample, record)
		a.sampleUp(len(a.sample) - 1)
		return
	case record.priority < a.sample[0].priority:
		a.sample[0] = record
		a.sampleDown(0)
	}
	a.sampled = true
}

func (a *archiveIterationIndexAccumulator) sampleUp(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if a.sample[index].priority <= a.sample[parent].priority {
			return
		}
		a.sample[index], a.sample[parent] = a.sample[parent], a.sample[index]
		index = parent
	}
}

func (a *archiveIterationIndexAccumulator) sampleDown(index int) {
	for {
		largest := index
		for _, child := range []int{2*index + 1, 2*index + 2} {
			if child < len(a.sample) && a.sample[child].priority > a.sample[largest].priority {
				largest = child
			}
		}
		if largest == index {
			return
		}
		a.sample[index], a.sample[largest] = a.sample[largest], a.sample[index]
		index = largest
	}
}

// nearest returns the sampled iteration whose span is closest to target:
// iterations without failed rows first, then any; ties go to the lower
// first_ordinal.
func (a *archiveIterationIndexAccumulator) nearest(target float64) *iterationIndexRecord {
	for _, successOnly := range []bool{true, false} {
		var best *iterationIndexRecord
		bestDistance := math.Inf(1)
		for index := range a.sample {
			record := &a.sample[index]
			if (successOnly && record.failedRows > 0) || !record.whole() {
				continue
			}
			distance := math.Abs(record.spanMs() - target)
			if distance < bestDistance || (distance == bestDistance && best != nil && record.firstOrdinal < best.firstOrdinal) {
				best, bestDistance = record, distance
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// finalize closes every open iteration and returns the index, or nil when the
// archive has no iteration rows at all.
func (a *archiveIterationIndexAccumulator) finalize() *RunArchiveIterationIndex {
	if a == nil {
		return nil
	}
	identities := make([]timeAnatomyIdentity, 0, len(a.states))
	for identity := range a.states {
		identities = append(identities, identity)
	}
	slices.SortFunc(identities, compareTimeAnatomyIdentity)
	for _, identity := range identities {
		state := a.states[identity]
		a.resolvePending(state)
		a.closeAll(state)
	}
	a.states = map[timeAnatomyIdentity]*iterationIndexState{}
	a.evictedIDs = map[timeAnatomyIdentity]iterationIndexEvicted{}
	a.pruned = nil
	if a.total == 0 && a.quality.NoneRows == 0 {
		return nil
	}
	index := &RunArchiveIterationIndex{
		Schema:                iterationIndexSchema,
		IterationsTotal:       a.total,
		Identities:            a.seen,
		IdentityQuality:       a.quality,
		RowsOutsideIterations: a.outside,
		LateRows:              a.late,
		UntimedIterations:     a.untimed,
		IdentitiesEvicted:     a.evicted,
		PartialIterations:     a.partial,
		Representatives:       []RunArchiveIterationRef{},
	}
	if a.timed > 0 {
		index.Span = RunArchiveIterationSpan{
			Samples:           a.timed,
			P50Ms:             a.spans.Quantile(.50),
			P95Ms:             a.spans.Quantile(.95),
			MaxMs:             a.maxSpan,
			PercentileQuality: a.spans.PercentileQuality(),
		}
	}
	seen := map[int64]bool{}
	appendRef := func(record *iterationIndexRecord, reason string, target *float64, sampled bool) {
		if record == nil || seen[record.firstOrdinal] {
			return
		}
		seen[record.firstOrdinal] = true
		ref := record.ref(reason)
		ref.TargetMs, ref.Sampled = target, sampled
		index.Representatives = append(index.Representatives, ref)
	}
	if a.timed > 0 {
		for _, pick := range []struct {
			reason string
			target float64
		}{{"p50", index.Span.P50Ms}, {"p95", index.Span.P95Ms}} {
			target := pick.target
			appendRef(a.nearest(target), pick.reason, &target, a.sampled)
		}
	}
	for i := range a.slowest {
		appendRef(&a.slowest[i], "slowest", nil, false)
	}
	for i := range a.failed {
		appendRef(&a.failed[i], "failed", nil, false)
	}
	// Eviction alone splits nothing: an evicted identity usually ended. Only
	// a resumed iteration is a real split.
	if a.resplit > 0 {
		index.Reasons = append(index.Reasons, iterationIndexReasonIdentityEvicted)
	}
	if a.sampled {
		index.Reasons = append(index.Reasons, iterationIndexReasonSample)
	}
	slices.Sort(index.Reasons)
	index.Truncated = len(index.Reasons) > 0
	return index
}

func (r *iterationIndexRecord) ref(reason string) RunArchiveIterationRef {
	ref := RunArchiveIterationRef{
		Reason:          reason,
		IdentityQuality: timeAnatomyIdentityLifetime,
		Group:           r.identity.group,
		VUserID:         r.identity.vuserID,
		VUserLifetime:   r.identity.lifetime,
		Iteration:       r.iteration,
		FirstOrdinal:    r.firstOrdinal,
		LastOrdinal:     r.lastOrdinal,
		SpanMs:          r.spanMs(),
		TransactionRows: r.txRows,
		RequestRows:     r.reqRows,
		FailedRows:      r.failedRows,
		Split:           r.split,
		Interrupted:     r.interrupted,
	}
	if r.legacy {
		ref.IdentityQuality = timeAnatomyIdentityLegacy
	}
	if r.timed {
		ref.StartedAt = time.Unix(0, r.startNs).UTC()
	}
	return ref
}

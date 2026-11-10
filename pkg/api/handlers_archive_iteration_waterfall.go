package api

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Iteration replay limits.
const (
	archiveIterationWaterfallMaxRows    = 2000
	archiveIterationWaterfallMaxGroup   = 512
	archiveIterationWaterfallCacheEntry = 32
	archiveIterationWaterfallCacheBytes = int64(8 << 20)
)

var errArchiveIterationNotFound = errors.New("iteration not found in archive")

// archiveIterationQuery names one VUser iteration: its identity (group plus
// lifetime, else the legacy VUser ID), its iteration number, and optional
// hints from the report's iteration index.
type archiveIterationQuery struct {
	group      string
	vuserID    int
	hasVUserID bool
	lifetime   uint64
	iteration  int64
	// first and last are the index's first and last row ordinals. They are
	// verified (both rows must belong to the iteration) before they bound
	// the scan.
	first, last int64
	hasHints    bool
	// anchor is the line of one row known to belong to the iteration (a
	// business-key match, a representative transaction); it picks between
	// iterations of a reused legacy VUser ID.
	anchor    int64
	hasAnchor bool
}

func (q archiveIterationQuery) quality() string {
	if q.lifetime > 0 {
		return timeAnatomyIdentityLifetime
	}
	return timeAnatomyIdentityLegacy
}

// matchesIdentity reports whether a row belongs to the queried VUser. A
// lifetime nonce is unique per VUser lifetime, so with one the VUser ID is
// compared only when the caller gave it. Without one, every lifetime of the
// VUser ID matches; the full scan splits them into occurrences.
func (q archiveIterationQuery) matchesIdentity(tx *TransactionEvent) bool {
	if tx.Group != q.group {
		return false
	}
	if q.lifetime > 0 {
		return tx.VUserLifetime == q.lifetime && (!q.hasVUserID || tx.VUserID == q.vuserID)
	}
	return tx.VUserID == q.vuserID
}

func (q archiveIterationQuery) matches(tx *TransactionEvent) bool {
	population := archiveEventPopulation(*tx)
	if population != "action" && population != "transaction" {
		return false
	}
	return tx.Iteration == q.iteration && (tx.Phase == "" || tx.Phase == "action") && q.matchesIdentity(tx)
}

// newArchiveIterationWaterfallCache holds rebuilt iterations. Its keys start
// with the report cache key, so forgetRunArchiveCache drops a run's entries
// and a re-archived run never reads an older one.
func newArchiveIterationWaterfallCache() *boundedArchiveCache[ArchiveIterationWaterfall] {
	return newWeightedBoundedArchiveCache(archiveIterationWaterfallCacheEntry, archiveIterationWaterfallCacheBytes, archiveJSONWeight[ArchiveIterationWaterfall])
}

// archiveIterationWaterfallCacheKey is one key per query: the hints and the
// anchor can pick a different legacy iteration, so they are part of it. A
// queried lifetime is unique, so its anchor cannot change the result and is
// left out (a caller varying it cannot defeat the result and miss caches).
func archiveIterationWaterfallCacheKey(record *storage.RunArchiveRecord, q archiveIterationQuery) string {
	base := runArchiveReportCacheKey(record)
	if base == "" {
		return ""
	}
	parts := []string{base, "iteration", strconv.Quote(q.group), strconv.Itoa(q.vuserID), strconv.FormatBool(q.hasVUserID),
		strconv.FormatUint(q.lifetime, 10), strconv.FormatInt(q.iteration, 10)}
	if q.hasHints {
		parts = append(parts, "hints", strconv.FormatInt(q.first, 10), strconv.FormatInt(q.last, 10))
	}
	if q.hasAnchor && q.lifetime == 0 {
		parts = append(parts, "anchor", strconv.FormatInt(q.anchor, 10))
	}
	return strings.Join(parts, "|")
}

// parseArchiveIterationQuery validates the query string; the error is the
// 400 message.
// It takes the request and reads each parameter with query.Get and a literal
// name, so the OpenAPI generator follows it and documents every parameter.
func parseArchiveIterationQuery(r *http.Request) (archiveIterationQuery, int, string) {
	query := r.URL.Query()
	var q archiveIterationQuery
	q.group = query.Get("group")
	if len(q.group) > archiveIterationWaterfallMaxGroup || !utf8.ValidString(q.group) {
		return q, 0, "invalid group"
	}
	if raw := query.Get("vuser_id"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return q, 0, "invalid vuser_id"
		}
		q.vuserID, q.hasVUserID = value, true
	}
	if raw := query.Get("vuser_lifetime"); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value == 0 {
			return q, 0, "invalid vuser_lifetime"
		}
		q.lifetime = value
	}
	if q.lifetime == 0 && q.vuserID == 0 {
		return q, 0, "vuser_lifetime or a non-zero vuser_id is required"
	}
	raw := query.Get("iteration")
	if raw == "" {
		return q, 0, "iteration is required"
	}
	iteration, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || iteration < 1 {
		return q, 0, "invalid iteration"
	}
	q.iteration = iteration
	parseOrdinal := func(key, raw string) (int64, bool, string) {
		if raw == "" {
			return 0, false, ""
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return 0, false, "invalid " + key
		}
		return value, true, ""
	}
	first, hasFirst, message := parseOrdinal("first_ordinal", query.Get("first_ordinal"))
	if message != "" {
		return q, 0, message
	}
	last, hasLast, message := parseOrdinal("last_ordinal", query.Get("last_ordinal"))
	if message != "" {
		return q, 0, message
	}
	switch {
	case hasFirst != hasLast:
		return q, 0, "first_ordinal and last_ordinal go together"
	case hasFirst && first > last:
		return q, 0, "first_ordinal must be at most last_ordinal"
	case hasFirst:
		q.first, q.last, q.hasHints = first, last, true
	}
	anchor, hasAnchor, message := parseOrdinal("ordinal", query.Get("ordinal"))
	if message != "" {
		return q, 0, message
	}
	q.anchor, q.hasAnchor = anchor, hasAnchor
	maxRows := archiveIterationWaterfallMaxRows
	if raw := query.Get("max_rows"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > archiveIterationWaterfallMaxRows {
			return q, 0, "max_rows must be between 1 and " + strconv.Itoa(archiveIterationWaterfallMaxRows)
		}
		maxRows = parsed
	}
	return q, maxRows, ""
}

// handleGetRunArchiveIterationWaterfall rebuilds one VUser iteration from the
// archive: every transaction and request row it wrote, in start order, with
// the time no row covers.
func (s *Server) handleGetRunArchiveIterationWaterfall(w http.ResponseWriter, r *http.Request) {
	q, maxRows, message := parseArchiveIterationQuery(r)
	if message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	caller, limit := archiveWaterfallAnonymousCaller, archiveWaterfallAnonymousRequests
	if key, ok := archiveWaterfallCaller(r); ok {
		caller, limit = key, archiveWaterfallCallerRequests
	}
	releaseCaller, admitted := archiveWaterfallCallerShares.tryAcquire(caller, limit)
	if !admitted {
		w.Header().Set("Retry-After", archiveWaterfallRetryAfterSeconds)
		writeError(w, http.StatusTooManyRequests, "too many archive drill-downs in flight; wait for one to finish")
		return
	}
	defer releaseCaller()

	waterfall, err := cachedArchiveIterationWaterfall(r.Context(), s.archiveReadCaches().iterationWaterfalls, record, q, maxRows)
	if err != nil {
		var budget *archiveScanBudgetError
		switch {
		case errors.Is(err, errArchiveIterationNotFound):
			writeError(w, http.StatusNotFound, "iteration not found in archive")
		case errors.As(err, &budget):
			writeError(w, http.StatusRequestEntityTooLarge, "archive too large to rebuild this iteration: "+budget.Error())
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeArchiveWaterfallUnavailable(w, r, err)
		default:
			writeError(w, http.StatusInternalServerError, "failed to rebuild archive iteration: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, waterfall)
}

// cachedArchiveIterationWaterfall serves a rebuilt iteration from the bounded
// cache (built once with the full row cap, sliced per caller), behind the
// same miss cache, load slots and lease as the transaction drill-down.
func cachedArchiveIterationWaterfall(ctx context.Context, cache *boundedArchiveCache[ArchiveIterationWaterfall], record *storage.RunArchiveRecord, q archiveIterationQuery, maxRows int) (*ArchiveIterationWaterfall, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	load := func(loadCtx context.Context) (*ArchiveIterationWaterfall, error) {
		return runArchiveWaterfallLoad(loadCtx, record, func(ctx context.Context) (*ArchiveIterationWaterfall, error) {
			return buildArchiveIterationWaterfall(ctx, record, q, archiveIterationWaterfallMaxRows)
		})
	}
	key := archiveIterationWaterfallCacheKey(record, q)
	if key != "" && archiveWaterfallMisses.has(key) {
		return nil, errArchiveIterationNotFound
	}
	var (
		waterfall *ArchiveIterationWaterfall
		err       error
	)
	if cache == nil || key == "" {
		waterfall, err = load(ctx)
	} else {
		waterfall, err = cache.getOrLoadShared(ctx, key, load)
	}
	if err != nil {
		if key != "" && errors.Is(err, errArchiveIterationNotFound) {
			archiveWaterfallMisses.add(key)
		}
		return nil, err
	}
	if maxRows < 1 || len(waterfall.Rows) <= maxRows {
		return waterfall, nil
	}
	limited := *waterfall
	limited.Rows = waterfall.Rows[:maxRows:maxRows]
	limited.Truncated = true
	limited.GapsMs = nil
	return &limited, nil
}

type archiveIterationEntry struct {
	ordinal int64
	startNs int64 // math.MaxInt64 for a row without a timestamp
	endNs   int64
	timed   bool
	row     ArchiveWaterfallRow
}

// archiveIterationCollector gathers one iteration's rows. It keeps the
// earliest maxRows by (start, ordinal) and streams the rest into the totals.
// Think time is streamed too: txs holds transaction windows only until it
// reaches thinkCap, then compactThink folds it (see there), so the working
// set stays bounded by the row cap and not by the iteration's size.
type archiveIterationCollector struct {
	maxRows     int
	entries     []archiveIterationEntry
	total       int
	first, last int64
	startNs     int64
	endNs       int64
	timed       bool
	// txs holds transaction rows' windows and think time not yet folded, so
	// think time is summed over outermost transactions.
	txs      []archiveIterationThink
	thinkCap int
	// thinkUntimed is think time of rows that cannot be placed (they count
	// on their own). thinkFolded is think time of outermost rows folded out
	// of txs; they lie in [foldedStart, foldedEnd].
	thinkUntimed           float64
	thinkFolded            float64
	foldedStart, foldedEnd int64
	hasFolded              bool
	// thinkApprox is set while folded rows stand on their own: a row
	// arriving later that ends inside the folded range is taken as nested
	// in it. A row enclosing the whole range absorbs the fold exactly.
	thinkApprox bool
	cut         bool
	vuserID     int
	lifetime    uint64
}

// archiveIterationThinkCapFloor is the least number of transaction windows
// the think-time accumulator holds before it compacts.
const archiveIterationThinkCapFloor = 4096

// archiveIterationThink is one transaction row's window and the think time
// the engine subtracted from it (elapsed minus duration).
type archiveIterationThink struct {
	startNs, endNs int64
	ordinal        int64
	thinkMs        float64
}

func newArchiveIterationCollector(maxRows int) *archiveIterationCollector {
	return &archiveIterationCollector{maxRows: maxRows, first: -1, last: -1, thinkCap: max(4*maxRows, archiveIterationThinkCapFloor)}
}

func archiveIterationEntryLess(left, right archiveIterationEntry) int {
	return cmp.Or(cmp.Compare(left.startNs, right.startNs), cmp.Compare(left.ordinal, right.ordinal))
}

func (c *archiveIterationCollector) add(ordinal int64, tx *TransactionEvent) {
	population := archiveEventPopulation(*tx)
	entry := archiveIterationEntry{ordinal: ordinal, startNs: math.MaxInt64, row: archiveWaterfallRowOf(tx, ordinal, population, 0)}
	if startNs, endNs, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, tx.ElapsedMs); ok {
		entry.startNs, entry.endNs, entry.timed = startNs, endNs, true
		if !c.timed {
			c.startNs, c.endNs, c.timed = startNs, endNs, true
		} else {
			c.startNs, c.endNs = min(c.startNs, startNs), max(c.endNs, endNs)
		}
	}
	if population == "transaction" {
		thinkMs := max(timeAnatomyPos(tx.ElapsedMs)-timeAnatomyPos(tx.DurationMs), 0)
		if !entry.timed {
			c.thinkUntimed += thinkMs // cannot be placed: counts on its own
		} else {
			c.txs = append(c.txs, archiveIterationThink{startNs: entry.startNs, endNs: entry.endNs, ordinal: ordinal, thinkMs: thinkMs})
			if len(c.txs) >= c.thinkCap {
				c.compactThink()
			}
		}
	}
	if c.first < 0 || ordinal < c.first {
		c.first = ordinal
	}
	c.last = max(c.last, ordinal)
	c.vuserID, c.lifetime = tx.VUserID, tx.VUserLifetime
	c.total++
	c.entries = append(c.entries, entry)
	if len(c.entries) >= 2*c.maxRows {
		slices.SortFunc(c.entries, archiveIterationEntryLess)
		clear(c.entries[c.maxRows:])
		c.entries = c.entries[:c.maxRows]
		c.cut = true
	}
}

// outermostThink sorts txs in place and keeps only the outermost windows. A
// transaction nested in another (by window containment, with the join's
// 2 ms slack, as the replay tree nests them) has its think time inside the
// outer row's elapsed already, so adding both would count it twice. A
// window ending at or before the folded range is nested in it.
func (c *archiveIterationCollector) outermostThink() {
	if c.hasFolded && slices.ContainsFunc(c.txs, func(tx archiveIterationThink) bool {
		return tx.startNs <= c.foldedStart+timeAnatomyEpsilonNs && tx.endNs+timeAnatomyEpsilonNs >= c.foldedEnd
	}) {
		// A row enclosing every folded row arrived (an outer transaction
		// completes after the loop of children inside it): the folded think
		// time is inside its elapsed, so the fold is exact again.
		c.thinkFolded, c.hasFolded, c.thinkApprox = 0, false, false
	}
	slices.SortFunc(c.txs, func(left, right archiveIterationThink) int {
		// Longest first at one start, so an enclosing row precedes its children.
		return cmp.Or(cmp.Compare(left.startNs, right.startNs), cmp.Compare(right.endNs, left.endNs), cmp.Compare(left.ordinal, right.ordinal))
	})
	outerEnd, open := c.foldedEnd, c.hasFolded
	kept := c.txs[:0]
	for _, tx := range c.txs {
		if open && tx.endNs <= outerEnd+timeAnatomyEpsilonNs {
			continue // inside the current outermost transaction
		}
		kept = append(kept, tx)
		outerEnd, open = tx.endNs, true
	}
	clear(c.txs[len(kept):])
	c.txs = kept
}

// compactThink bounds txs. Dropping nested windows loses nothing: their
// think time is inside an outer row kept in txs. When the outermost windows
// alone still fill half the cap (sequential transactions in one long
// iteration), the earlier half is folded into thinkFolded and the total is
// flagged approximate, since a row arriving later could still enclose or
// fall inside it.
func (c *archiveIterationCollector) compactThink() {
	c.outermostThink()
	if len(c.txs) < c.thinkCap/2 {
		return
	}
	fold := len(c.txs) / 2
	for _, tx := range c.txs[:fold] {
		c.thinkFolded += tx.thinkMs
		if !c.hasFolded {
			c.foldedStart, c.foldedEnd, c.hasFolded = tx.startNs, tx.endNs, true
		}
		c.foldedStart, c.foldedEnd = min(c.foldedStart, tx.startNs), max(c.foldedEnd, tx.endNs)
	}
	rest := copy(c.txs, c.txs[fold:])
	clear(c.txs[rest:])
	c.txs = c.txs[:rest]
	c.thinkApprox = true
}

// thinkInsideTransactions sums think time over the outermost transaction
// rows only. Untimed rows cannot be placed and count on their own.
func (c *archiveIterationCollector) thinkInsideTransactions() float64 {
	c.outermostThink()
	think := c.thinkUntimed + c.thinkFolded
	for _, tx := range c.txs {
		think += tx.thinkMs
	}
	return think
}

// result builds the response from the collected rows.
func (c *archiveIterationCollector) result(q archiveIterationQuery) *ArchiveIterationWaterfall {
	slices.SortFunc(c.entries, archiveIterationEntryLess)
	if len(c.entries) > c.maxRows {
		clear(c.entries[c.maxRows:])
		c.entries = c.entries[:c.maxRows]
		c.cut = true
	}
	result := &ArchiveIterationWaterfall{
		Schema:                    iterationWaterfallSchema,
		IdentityQuality:           timeAnatomyIdentityLegacy,
		Group:                     q.group,
		VUserID:                   c.vuserID,
		VUserLifetime:             c.lifetime,
		Iteration:                 q.iteration,
		FirstOrdinal:              c.first,
		LastOrdinal:               c.last,
		Rows:                      make([]ArchiveWaterfallRow, 0, len(c.entries)),
		RowsTotal:                 c.total,
		Truncated:                 c.cut,
		ThinkMsInsideTransactions: c.thinkInsideTransactions(),
		ThinkMsApproximate:        c.thinkApprox,
	}
	if c.lifetime > 0 {
		result.IdentityQuality = timeAnatomyIdentityLifetime
	}
	if c.timed {
		result.StartedAt = time.Unix(0, c.startNs).UTC()
		result.SpanMs = float64(c.endNs-c.startNs) / 1e6
	}
	var covered, coverStart, coverEnd int64
	open := false
	for _, entry := range c.entries {
		row := entry.row
		if entry.timed {
			row.OffsetMs = max(float64(entry.startNs-c.startNs)/1e6, 0)
			switch {
			case !open:
				coverStart, coverEnd, open = entry.startNs, entry.endNs, true
			case entry.startNs > coverEnd:
				covered += coverEnd - coverStart
				coverStart, coverEnd = entry.startNs, entry.endNs
			default:
				coverEnd = max(coverEnd, entry.endNs)
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if open {
		covered += coverEnd - coverStart
	}
	if c.timed && !c.cut {
		gaps := max(float64(c.endNs-c.startNs-covered)/1e6, 0)
		result.GapsMs = &gaps
	}
	return result
}

// legacyIterationSplitter follows one legacy identity's rows (no lifetime)
// the way the iteration index does, so a replay splits them into iterations
// alike: a row of a higher iteration opens it; a row of a lower one is held
// until the identity's next row says whether it was a late row of an earlier
// iteration or a restart of a reused VUser ID (legacyIterationLate).
//
// Rows of the queried iteration go to the occurrence open() returns when its
// iteration opens (nil stops the scan), or, when late, to the latest one.
type legacyIterationSplitter struct {
	iteration  int64
	cur        int64
	held       *TransactionEvent
	heldAt     int64
	occurrence *archiveIterationCollector
	open       func() *archiveIterationCollector
	add        func(c *archiveIterationCollector, ordinal int64, tx *TransactionEvent)
}

// legacyIterationRow reports whether a row takes part in iteration splitting:
// an action-phase iteration row of the action or transaction population.
func legacyIterationRow(tx *TransactionEvent) bool {
	population := archiveEventPopulation(*tx)
	return (population == "action" || population == "transaction") && tx.Iteration > 0 && (tx.Phase == "" || tx.Phase == "action")
}

// step takes one row of the identity; false stops the scan.
func (l *legacyIterationSplitter) step(ordinal int64, tx *TransactionEvent) bool {
	if held := l.held; held != nil {
		l.held = nil
		late := legacyIterationLate(l.cur, tx.Iteration)
		if !late && !l.openIteration(held.Iteration) {
			return false
		}
		if held.Iteration == l.iteration && l.occurrence != nil {
			l.add(l.occurrence, l.heldAt, held)
		}
	}
	if l.cur != 0 && tx.Iteration < l.cur {
		held := *tx
		l.held, l.heldAt = &held, ordinal
		return true
	}
	if tx.Iteration != l.cur && !l.openIteration(tx.Iteration) {
		return false
	}
	if tx.Iteration == l.iteration && l.occurrence != nil {
		l.add(l.occurrence, ordinal, tx)
	}
	return true
}

// finish settles a held row at the end of the scan: nothing continued the
// open iteration, so it was a restart (as the index decides at finalize).
// A hinted scan ends at its window instead, where the next row is unknown,
// and leaves it out (the full scan settles it).
func (l *legacyIterationSplitter) finish() {
	held := l.held
	l.held = nil
	if held != nil && l.openIteration(held.Iteration) && held.Iteration == l.iteration && l.occurrence != nil {
		l.add(l.occurrence, l.heldAt, held)
	}
}

func (l *legacyIterationSplitter) openIteration(iteration int64) bool {
	l.cur = iteration
	if iteration != l.iteration {
		return true
	}
	l.occurrence = l.open()
	return l.occurrence != nil
}

// buildArchiveIterationWaterfall rebuilds one iteration. With hints it reads
// only lines first_ordinal..last_ordinal (plus the late window for a
// lifetime, whose rows of an earlier iteration may still be written after
// it moved on), once both hinted rows verify as the iteration's; otherwise
// it scans the archive with the identity prefilter. Both passes share one
// scan budget, so a wrong hint cannot make a request read the archive twice.
func buildArchiveIterationWaterfall(ctx context.Context, record *storage.RunArchiveRecord, q archiveIterationQuery, maxRows int) (*ArchiveIterationWaterfall, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxRows < 1 {
		maxRows = archiveIterationWaterfallMaxRows
	}
	identity := timeAnatomyIdentity{group: q.group, vuserID: q.vuserID, lifetime: q.lifetime}
	prefilter := archiveWaterfallIdentityPrefilter(identity, q.quality())
	budget := newArchiveScanBudget(ctx, archiveWaterfallScanRecords, archiveWaterfallScanBytes)
	if q.hasHints {
		collector, hint, err := scanArchiveIterationHinted(ctx, record, budget, q, maxRows, prefilter)
		budgetCut := err != nil && isArchiveScanBudget(err)
		if err != nil && !budgetCut {
			return nil, err
		}
		// A budget cut after the first hinted row verified is the hinted
		// iteration, partial: a full scan reads the same lines and more.
		if hint == archiveIterationHintVerified || (budgetCut && hint == archiveIterationHintFirstVerified) {
			result := collector.result(q)
			if budgetCut {
				markArchiveIterationPartial(result, iterationWaterfallReasonScanBudget)
			}
			return result, nil
		}
		if budgetCut {
			return nil, err
		}
	}
	return scanArchiveIterationFull(ctx, record, budget, q, maxRows, prefilter)
}

func isArchiveScanBudget(err error) bool {
	var budget *archiveScanBudgetError
	return errors.As(err, &budget)
}

func markArchiveIterationPartial(result *ArchiveIterationWaterfall, reason string) {
	result.Partial = true
	if !slices.Contains(result.PartialReasons, reason) {
		result.PartialReasons = append(result.PartialReasons, reason)
		slices.Sort(result.PartialReasons)
	}
	result.GapsMs = nil
}

// archiveIterationHint is how far a hinted read verified its hints.
type archiveIterationHint int

const (
	archiveIterationHintUnverified archiveIterationHint = iota
	archiveIterationHintFirstVerified
	archiveIterationHintVerified
)

// scanArchiveIterationHinted reads the hinted range. The rows at both hinted
// ordinals are always decoded and must belong to the iteration; the read
// stops at the first that does not, so a wrong first_ordinal costs only the
// lines before it. A stale or wrong hint is not an error, the caller falls
// back to a full scan.
func scanArchiveIterationHinted(ctx context.Context, record *storage.RunArchiveRecord, budget *archiveScanBudget, q archiveIterationQuery, maxRows int, prefilter func([]byte) bool) (*archiveIterationCollector, archiveIterationHint, error) {
	// Rows of an earlier iteration can still be written after the VUser
	// moved on, for a lifetime and for a legacy VUser ID alike: read a late
	// window past last_ordinal.
	to := q.last + archiveWaterfallLateLines
	collector := newArchiveIterationCollector(maxRows)
	firstOK, lastOK, wrong := false, false, false
	hinted := func(ordinal int64, line []byte) bool {
		return ordinal == q.first || ordinal == q.last || prefilter(line)
	}
	// A legacy VUser ID can be reused, so its rows are split as the index
	// split them: the scan stops where another occurrence of the iteration
	// would open.
	var legacy *legacyIterationSplitter
	if q.lifetime == 0 {
		opened := false
		legacy = &legacyIterationSplitter{
			iteration: q.iteration,
			open: func() *archiveIterationCollector {
				if opened {
					return nil
				}
				opened = true
				return collector
			},
			add: func(c *archiveIterationCollector, ordinal int64, tx *TransactionEvent) {
				lastOK = lastOK || ordinal == q.last
				c.add(ordinal, tx)
			},
		}
	}
	err := scanArchiveWaterfallLinesBudget(ctx, record, budget, q.first, to, hinted, func(ordinal int64, tx *TransactionEvent) bool {
		if !q.matches(tx) {
			if ordinal == q.first || ordinal == q.last {
				wrong = true
				return true
			}
			if legacy != nil && firstOK && tx.VUserLifetime == 0 && q.matchesIdentity(tx) && legacyIterationRow(tx) {
				return !legacy.step(ordinal, tx)
			}
			return false
		}
		if legacy != nil && tx.VUserLifetime == 0 {
			if ordinal == q.first {
				firstOK = true
			} else if !firstOK {
				return false
			}
			return !legacy.step(ordinal, tx)
		}
		firstOK = firstOK || ordinal == q.first
		lastOK = lastOK || ordinal == q.last
		collector.add(ordinal, tx)
		return false
	})
	switch {
	case wrong || !firstOK:
		return collector, archiveIterationHintUnverified, err
	case !lastOK:
		return collector, archiveIterationHintFirstVerified, err
	}
	return collector, archiveIterationHintVerified, err
}

// scanArchiveIterationFull scans the whole archive for the iteration's rows.
// A queried lifetime is unique, so every matching row belongs to it. A query
// by VUser ID alone can match several VUsers (a legacy ID reused by another
// generator or lifetime), so its rows are split into occurrences: a new one
// starts when the lifetime changes, or, for legacy rows (no lifetime), when
// the iteration opens again by the index's rule (legacyIterationSplitter: a
// late row of an earlier iteration does not split it). The occurrence
// holding the anchor row wins, else the first, and the result is partial
// when there was more than one.
func scanArchiveIterationFull(ctx context.Context, record *storage.RunArchiveRecord, budget *archiveScanBudget, q archiveIterationQuery, maxRows int, prefilter func([]byte) bool) (*ArchiveIterationWaterfall, error) {
	var (
		first, current, chosen *archiveIterationCollector
		occurrences            int
	)
	add := func(c *archiveIterationCollector, ordinal int64, tx *TransactionEvent) {
		if q.hasAnchor && ordinal == q.anchor {
			chosen = c
		}
		c.add(ordinal, tx)
	}
	newOccurrence := func() *archiveIterationCollector {
		occurrences++
		c := newArchiveIterationCollector(maxRows)
		if first == nil {
			first = c
		}
		return c
	}
	// Legacy rows (no lifetime) split into occurrences as the index splits
	// them; rows with a lifetime start one when the lifetime changes.
	var legacy *legacyIterationSplitter
	if q.lifetime == 0 {
		legacy = &legacyIterationSplitter{iteration: q.iteration, open: newOccurrence, add: add}
	}
	err := scanArchiveWaterfallLinesBudget(ctx, record, budget, 0, -1, func(_ int64, line []byte) bool { return prefilter(line) }, func(ordinal int64, tx *TransactionEvent) bool {
		if !q.matchesIdentity(tx) {
			return false
		}
		if legacy != nil && tx.VUserLifetime == 0 {
			if legacyIterationRow(tx) {
				legacy.step(ordinal, tx)
			}
			return false
		}
		if !q.matches(tx) {
			return false
		}
		if current == nil || (q.lifetime == 0 && tx.VUserLifetime != current.lifetime) {
			current = newOccurrence()
		}
		add(current, ordinal, tx)
		return false
	})
	if legacy != nil && err == nil {
		legacy.finish()
	}
	budgetCut := err != nil && isArchiveScanBudget(err)
	if err != nil && !budgetCut {
		return nil, err
	}
	if chosen == nil {
		chosen = first
	}
	if chosen == nil {
		if budgetCut {
			return nil, err
		}
		return nil, errArchiveIterationNotFound
	}
	result := chosen.result(q)
	if occurrences > 1 {
		markArchiveIterationPartial(result, iterationWaterfallReasonAmbiguousVUser)
	}
	if budgetCut {
		markArchiveIterationPartial(result, iterationWaterfallReasonScanBudget)
	}
	return result, nil
}

package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Drill-down limits.
const (
	archiveWaterfallDefaultMaxRequests = 500
	archiveWaterfallLateLines          = int64(10_000)
	archiveWaterfallMaxEnclosing       = 8
	archiveWaterfallErrorMessageBytes  = 512
	archiveWaterfallCacheEntries       = 64
	archiveWaterfallCacheBytes         = int64(4 << 20)
)

// The drill-down's scan budget per pass; package vars so tests can lower it.
var (
	archiveWaterfallScanRecords = maxArchiveQueryRecords
	archiveWaterfallScanBytes   = int64(maxArchiveQueryBytes)
)

// archiveWaterfallLoadSlots bounds the drill-down loads that read archives at
// once, process-wide. A load waits for a slot under its own context, so a
// load whose callers all left gives up its place in the queue.
var archiveWaterfallLoadSlots = make(chan struct{}, 4)

// archiveWaterfallAnonymousCaller is the shared fairness key of callers with
// no authenticated principal. It cannot collide with a principal key.
const archiveWaterfallAnonymousCaller = "\x00anonymous"

// Drill-down fairness. One test's loads hold at most archiveWaterfallTestSlots
// of the process-wide load slots, so drill-downs of other runs keep a slot;
// one authenticated caller has at most archiveWaterfallCallerRequests
// drill-downs in flight, and unauthenticated callers together at most
// archiveWaterfallAnonymousRequests (a request past that is answered 429 at
// once), so one viewer cannot queue loads for every slot. An event the archive does not hold is
// remembered for archiveWaterfallNotFoundTTL, so asking again for it does not
// rescan the archive. Package vars so tests can lower them.
var (
	archiveWaterfallTestSlots      = 2
	archiveWaterfallCallerRequests = 2
	// archiveWaterfallAnonymousRequests bounds the drill-downs in flight for
	// all unauthenticated callers together.
	archiveWaterfallAnonymousRequests = 2 * cap(archiveWaterfallLoadSlots)
	archiveWaterfallNotFoundTTL       = 30 * time.Second
	archiveWaterfallNotFoundLimit     = 1024
	archiveWaterfallNow               = time.Now
)

var (
	archiveWaterfallTestShares   = newArchiveWaterfallShares()
	archiveWaterfallCallerShares = newArchiveWaterfallShares()
	archiveWaterfallMisses       = newArchiveWaterfallMissCache()
)

// archiveWaterfallShares counts what each key (a test, a caller) holds.
type archiveWaterfallShares struct {
	mu   sync.Mutex
	held map[string]int
	// wake is closed (and replaced) on every release, so a waiter rechecks.
	wake chan struct{}
}

func newArchiveWaterfallShares() *archiveWaterfallShares {
	return &archiveWaterfallShares{held: map[string]int{}, wake: make(chan struct{})}
}

// tryAcquire takes one share of key when key holds fewer than limit.
func (s *archiveWaterfallShares) tryAcquire(key string, limit int) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[key] >= max(limit, 1) {
		return nil, false
	}
	s.held[key]++
	var once sync.Once
	return func() { once.Do(func() { s.release(key) }) }, true
}

// acquire waits under ctx for one share of key.
func (s *archiveWaterfallShares) acquire(ctx context.Context, key string, limit int) (func(), error) {
	for {
		if release, ok := s.tryAcquire(key, limit); ok {
			return release, nil
		}
		s.mu.Lock()
		wake := s.wake
		s.mu.Unlock()
		// A release between tryAcquire and reading wake closed the
		// previous channel, which this one replaced: recheck first.
		if release, ok := s.tryAcquire(key, limit); ok {
			return release, nil
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *archiveWaterfallShares) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[key]--; s.held[key] <= 0 {
		delete(s.held, key)
	}
	close(s.wake)
	s.wake = make(chan struct{})
}

// count reports what key holds (for tests).
func (s *archiveWaterfallShares) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[key]
}

// archiveWaterfallMissCache remembers, for a short TTL, the drill-down cache
// keys whose event the archive does not hold. The key starts with the
// report cache key (test id, manifest digest, completion time), so a
// re-archived run never reads an older miss. It is bounded: when full,
// expired entries go first, then arbitrary ones.
type archiveWaterfallMissCache struct {
	mu      sync.Mutex
	expires map[string]time.Time
}

func newArchiveWaterfallMissCache() *archiveWaterfallMissCache {
	return &archiveWaterfallMissCache{expires: map[string]time.Time{}}
}

func (m *archiveWaterfallMissCache) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	expires, ok := m.expires[key]
	if !ok {
		return false
	}
	if !archiveWaterfallNow().Before(expires) {
		delete(m.expires, key)
		return false
	}
	return true
}

func (m *archiveWaterfallMissCache) add(key string) {
	if archiveWaterfallNotFoundTTL <= 0 || archiveWaterfallNotFoundLimit < 1 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := archiveWaterfallNow()
	if _, ok := m.expires[key]; !ok && len(m.expires) >= archiveWaterfallNotFoundLimit {
		for other, expires := range m.expires {
			if !now.Before(expires) {
				delete(m.expires, other)
			}
		}
		for other := range m.expires {
			if len(m.expires) < archiveWaterfallNotFoundLimit {
				break
			}
			delete(m.expires, other)
		}
	}
	m.expires[key] = now.Add(archiveWaterfallNotFoundTTL)
}

func (m *archiveWaterfallMissCache) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.expires)
}

// archiveWaterfallCaller is the principal a drill-down's in-flight share is
// counted against: the service account, else the user. A request with no
// authenticated principal (auth disabled) has no caller key (ok false) and
// takes no per-caller share: its only address is r.RemoteAddr
// (requestSourceIP does not trust X-Forwarded-For), which behind an ingress
// is the proxy's for every viewer, so keying on it would make all viewers
// share one caller's two drill-downs. The per-test share and the
// process-wide load slots still bound its work.
func archiveWaterfallCaller(r *http.Request) (string, bool) {
	if account, ok := AuthenticatedServiceAccountFromContext(r.Context()); ok && account != nil && account.ID != "" {
		return "service-account:" + account.ID, true
	}
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		if user.ID != "" {
			return "user:" + user.ID, true
		}
		if user.Subject != "" {
			return "subject:" + user.Subject, true
		}
	}
	return "", false
}

// archiveWaterfallAcquireLease takes the archive materialization lease a
// cache-owned load holds while it reads (a package var so tests can observe
// it).
var archiveWaterfallAcquireLease = func(ctx context.Context, record *storage.RunArchiveRecord) (func(), error) {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, err
	}
	return lease.Release, nil
}

// archiveWaterfallLoadHook, when set by a test, observes every load that
// starts reading, with the load's context.
var archiveWaterfallLoadHook func(ctx context.Context)

// archiveWaterfallRunWideReasons are the report's truncation reasons that
// come from run-wide state (every identity's buffer, the identity table, the
// report's CPU budget). A one-identity replay cannot reproduce them, so when
// the report carries one the drill-down says its join may differ.
var archiveWaterfallRunWideReasons = []string{
	timeAnatomyReasonPendingGlobalCap,
	timeAnatomyReasonIdentityCap,
	timeAnatomyReasonWorkBudget,
}

var archiveWaterfallEventIDPattern = regexp.MustCompile(`^tx_[0-9a-f]{32}$`)

// newArchiveWaterfallCache holds rebuilt drill-downs. It is one of the
// controller's archiveReadCaches, so forgetRunArchiveCache drops a pruned or
// deleted run's entries. The key starts with the report cache key (test id,
// manifest digest, completion time and counts), so a re-archived run never
// reads an older entry.
func newArchiveWaterfallCache() *boundedArchiveCache[archiveWaterfallResult] {
	return newWeightedBoundedArchiveCache(archiveWaterfallCacheEntries, archiveWaterfallCacheBytes, archiveJSONWeight[archiveWaterfallResult])
}

// archiveWaterfallDecodeHook, when set by a test, observes every line the
// drill-down decodes.
var archiveWaterfallDecodeHook func(ordinal int64)

var errArchiveWaterfallNotFound = errors.New("event not found in archive")

// archiveWaterfallHints is the report's ordinal hint for an event: the row's
// line index. It only saves the search for the row (it is verified against
// the event_id); the result never depends on it.
//
// The report's first_ordinal (the smallest line index of a request it
// claimed) is accepted and validated but not used to start the join late.
// The report's buffer for an identity depends on every request row the
// identity wrote before the root: the per-identity cap's drops and the drop
// hull reach back past the first claimed child, and a report that lost
// children to the global cap has a first_ordinal past them. So the join
// always replays the root's identity from line 0. Lines of other identities
// are only matched by a byte prefilter, never decoded, and every line is
// read for the scan budget either way.
type archiveWaterfallHints struct {
	ordinal int64
	ok      bool
}

// archiveWaterfallResult is the drill-down response.
type archiveWaterfallResult = ArchiveTransactionWaterfall

// handleGetRunArchiveTransactionWaterfall rebuilds one archived transaction
// (or request) as a waterfall: its claimed requests in start order with their
// phase split, and the same one-instance decomposition the report's time
// anatomy computed for it.
func (s *Server) handleGetRunArchiveTransactionWaterfall(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	if !archiveWaterfallEventIDPattern.MatchString(eventID) {
		writeError(w, http.StatusBadRequest, "invalid event_id")
		return
	}
	query := r.URL.Query()
	var hints archiveWaterfallHints
	parseOrdinal := func(raw string) (int64, bool, bool) {
		if raw == "" {
			return 0, false, true
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return 0, false, false
		}
		return value, true, true
	}
	ordinal, hasOrdinal, valid := parseOrdinal(query.Get("ordinal"))
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid ordinal")
		return
	}
	firstOrdinal, hasFirst, valid := parseOrdinal(query.Get("first_ordinal"))
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid first_ordinal")
		return
	}
	switch {
	case hasFirst && !hasOrdinal:
		writeError(w, http.StatusBadRequest, "first_ordinal requires ordinal")
		return
	case hasOrdinal:
		// first_ordinal is validated but bounds nothing (see
		// archiveWaterfallHints), so a transaction open across any number of
		// rows is fine.
		if hasFirst && firstOrdinal > ordinal {
			writeError(w, http.StatusBadRequest, "first_ordinal must be at most ordinal")
			return
		}
		hints = archiveWaterfallHints{ordinal: ordinal, ok: true}
	}
	maxRequests := archiveWaterfallDefaultMaxRequests
	if raw := query.Get("max_requests"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > archiveWaterfallDefaultMaxRequests {
			writeError(w, http.StatusBadRequest, "max_requests must be between 1 and 500")
			return
		}
		maxRequests = parsed
	}

	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	// Unauthenticated (auth disabled) callers cannot be told apart behind an
	// ingress, so they share one coarse bound instead of none: past it they
	// are answered 429 at once rather than queueing loads that hold leases.
	caller, limit := archiveWaterfallAnonymousCaller, archiveWaterfallAnonymousRequests
	if key, ok := archiveWaterfallCaller(r); ok {
		caller, limit = key, archiveWaterfallCallerRequests
	}
	{
		releaseCaller, admitted := archiveWaterfallCallerShares.tryAcquire(caller, limit)
		if !admitted {
			w.Header().Set("Retry-After", archiveWaterfallRetryAfterSeconds)
			writeError(w, http.StatusTooManyRequests, "too many transaction drill-downs in flight; wait for one to finish")
			return
		}
		defer releaseCaller()
	}

	waterfall, err := cachedArchiveTransactionWaterfall(r.Context(), s.archiveReadCaches().waterfalls, record, eventID, hints, maxRequests)
	if err == nil {
		waterfall, err = s.markArchiveWaterfallJoinMayDiffer(r.Context(), record, waterfall)
	}
	if err != nil {
		var budget *archiveScanBudgetError
		switch {
		case errors.Is(err, errArchiveWaterfallNotFound):
			writeError(w, http.StatusNotFound, "event not found in archive")
		case errors.As(err, &budget):
			writeError(w, http.StatusRequestEntityTooLarge, "archive too large to rebuild this transaction: "+budget.Error())
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeArchiveWaterfallUnavailable(w, r, err)
		default:
			writeError(w, http.StatusInternalServerError, "failed to rebuild archive transaction: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, waterfall)
}

// archiveWaterfallRetryAfterSeconds is the Retry-After a busy drill-down
// answers with.
const archiveWaterfallRetryAfterSeconds = "2"

// writeArchiveWaterfallUnavailable answers a load that ended on a context
// error. The load runs under its own deadline (configuredArchiveMaterializationTimeout)
// and a context detached from any one caller, so a DeadlineExceeded or
// Canceled from it while the viewer's own request is live means the server
// was saturated (every archiveWaterfallLoadSlots slot busy, the lease wait
// ran out) or the build ran past its deadline: a retryable busy 503 with
// Retry-After. Only a request whose own context ended reads as canceled.
// The message is static and public; the cause is logged under the ref.
func writeArchiveWaterfallUnavailable(w http.ResponseWriter, r *http.Request, cause error) {
	message := "archive busy, try again"
	if r.Context().Err() != nil {
		message = "archive read canceled"
	} else {
		w.Header().Set("Retry-After", archiveWaterfallRetryAfterSeconds)
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": message,
		"ref":   logServerError(w, http.StatusServiceUnavailable, cause),
	})
}

// markArchiveWaterfallJoinMayDiffer returns waterfall (a copy when it
// changes; the cached value is shared) with join_may_differ set when the
// run's report carries a run-wide truncation reason, or when the report
// cannot be read. The report is the cached one the drawer was opened from;
// a cache miss rebuilds it once per archive (coalesced, like the report
// endpoint).
func (s *Server) markArchiveWaterfallJoinMayDiffer(ctx context.Context, record *storage.RunArchiveRecord, waterfall *archiveWaterfallResult) (*archiveWaterfallResult, error) {
	if waterfall == nil || waterfall.Root.Population != "transaction" {
		return waterfall, nil
	}
	report, err := s.archiveReadCaches().reports.getOrLoadContext(ctx, runArchiveReportCacheKey(record), func() (*RunArchiveReport, error) {
		return buildRunArchiveReport(record)
	})
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
		return nil, err
	}
	var reasons []string
	if err == nil && report != nil && report.TimeAnatomy != nil {
		for _, reason := range archiveWaterfallRunWideReasons {
			if slices.Contains(report.TimeAnatomy.Truncation.Reasons, reason) {
				reasons = append(reasons, reason)
			}
		}
		if len(reasons) == 0 {
			return waterfall, nil
		}
	}
	// No report (an error, or no time anatomy): the join may differ.
	marked := *waterfall
	marked.JoinMayDiffer, marked.JoinMayDifferReasons = true, reasons
	return &marked, nil
}

// cachedArchiveTransactionWaterfall serves a rebuilt drill-down from the
// bounded cache, building it once per event with the full request cap and
// slicing it to maxRequests per caller. Coalesced callers share one load that
// no single caller can cancel; it is canceled when the last caller stops
// waiting (getOrLoadShared) and is bounded by the materialization timeout.
// The load holds its own archive lease while it reads and waits for one of
// the process-wide archiveWaterfallLoadSlots first.
func cachedArchiveTransactionWaterfall(ctx context.Context, cache *boundedArchiveCache[archiveWaterfallResult], record *storage.RunArchiveRecord, eventID string, hints archiveWaterfallHints, maxRequests int) (*archiveWaterfallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	load := func(loadCtx context.Context) (*archiveWaterfallResult, error) {
		return loadArchiveTransactionWaterfall(loadCtx, record, eventID, hints)
	}
	var (
		waterfall *archiveWaterfallResult
		err       error
	)
	key := archiveWaterfallCacheKey(record, eventID)
	if key != "" && archiveWaterfallMisses.has(key) {
		return nil, errArchiveWaterfallNotFound
	}
	if cache == nil || key == "" {
		waterfall, err = load(ctx)
	} else {
		waterfall, err = cache.getOrLoadShared(ctx, key, load)
	}
	if err != nil {
		if key != "" && errors.Is(err, errArchiveWaterfallNotFound) {
			archiveWaterfallMisses.add(key)
		}
		return nil, err
	}
	return archiveWaterfallLimited(waterfall, maxRequests), nil
}

// loadArchiveTransactionWaterfall is one bounded drill-down load: the test's
// share of the load slots, a load slot, then the archive lease, then the
// build with the full request cap.
func loadArchiveTransactionWaterfall(ctx context.Context, record *storage.RunArchiveRecord, eventID string, hints archiveWaterfallHints) (*archiveWaterfallResult, error) {
	return runArchiveWaterfallLoad(ctx, record, func(ctx context.Context) (*archiveWaterfallResult, error) {
		return buildArchiveTransactionWaterfall(ctx, record, eventID, hints, archiveWaterfallDefaultMaxRequests)
	})
}

// runArchiveWaterfallLoad runs one bounded drill-down build (a transaction or
// an iteration): under the materialization timeout, it takes the test's
// share of the load slots, a load slot, then the archive lease.
func runArchiveWaterfallLoad[T any](ctx context.Context, record *storage.RunArchiveRecord, build func(context.Context) (*T, error)) (*T, error) {
	ctx, cancel := context.WithTimeout(ctx, configuredArchiveMaterializationTimeout())
	defer cancel()
	releaseTest, err := archiveWaterfallTestShares.acquire(ctx, archiveWaterfallTestShareKey(record), archiveWaterfallTestSlots)
	if err != nil {
		return nil, err
	}
	defer releaseTest()
	select {
	case archiveWaterfallLoadSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-archiveWaterfallLoadSlots }()
	release, err := archiveWaterfallAcquireLease(ctx, record)
	if err != nil {
		return nil, err
	}
	defer release()
	if archiveWaterfallLoadHook != nil {
		archiveWaterfallLoadHook(ctx)
	}
	return build(ctx)
}

// archiveWaterfallTestShareKey is the key one run's loads share.
func archiveWaterfallTestShareKey(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	if record.TestID != "" {
		return "test:" + record.TestID
	}
	return "archive:" + record.ArchivePath
}

// archiveWaterfallLimited returns waterfall with at most maxRequests request
// rows (a copy when it cuts; the cached value is shared). The decomposition
// and requests_total always cover every claimed request.
func archiveWaterfallLimited(waterfall *archiveWaterfallResult, maxRequests int) *archiveWaterfallResult {
	if waterfall == nil || maxRequests < 1 || len(waterfall.Requests) <= maxRequests {
		return waterfall
	}
	limited := *waterfall
	limited.Requests = waterfall.Requests[:maxRequests:maxRequests]
	limited.Truncated = true
	return &limited
}

// archiveWaterfallCacheKey is one key per event: the result depends neither
// on the hint nor on max_requests (it is built with the full cap and sliced).
func archiveWaterfallCacheKey(record *storage.RunArchiveRecord, eventID string) string {
	base := runArchiveReportCacheKey(record)
	if base == "" {
		return ""
	}
	return base + "|" + eventID
}

// archiveWaterfallLight is the lean projection the drill-down decodes: every
// field the time anatomy, the row and the legacy event_id derivation read,
// and none of the snapshot, network entries, extractions, console or DOM
// (protocol evidence only for the keys the exclusion reads).
type archiveWaterfallLight struct {
	Population          string    `json:"population,omitempty"`
	EventID             string    `json:"event_id"`
	Timestamp           time.Time `json:"timestamp"`
	TestID              string    `json:"test_id"`
	Group               string    `json:"group,omitempty"`
	Region              string    `json:"region,omitempty"`
	SourceTransaction   string    `json:"source_transaction,omitempty"`
	ActionName          string    `json:"action_name,omitempty"`
	VUserID             int       `json:"vuser_id,omitempty"`
	Iteration           int64     `json:"iteration,omitempty"`
	Interrupted         bool      `json:"interrupted,omitempty"`
	VUserLifetime       uint64    `json:"vuser_lifetime,omitempty"`
	ElapsedMs           float64   `json:"elapsed_ms,omitempty"`
	BusinessTransaction bool      `json:"business_transaction,omitempty"`
	CorrelationID       string    `json:"correlation_id,omitempty"`
	TraceID             string    `json:"trace_id,omitempty"`
	TraceSent           *bool     `json:"trace_sent,omitempty"`
	DependencyType      string    `json:"dependency_type,omitempty"`
	DependencyTarget    string    `json:"dependency_target,omitempty"`
	DurationMs          float64   `json:"duration_ms"`
	DNSMs               float64   `json:"dns_ms,omitempty"`
	ConnectMs           float64   `json:"connect_ms,omitempty"`
	TLSMs               float64   `json:"tls_ms,omitempty"`
	TTFBMs              float64   `json:"ttfb_ms,omitempty"`
	StatusCode          int       `json:"status_code,omitempty"`
	Success             bool      `json:"success"`
	ErrorMessage        string    `json:"error_message,omitempty"`
	ServerTelemetry     *struct {
		ConnectionReused bool `json:"connection_reused"`
	} `json:"server_telemetry,omitempty"`
	SourceStepID    string  `json:"source_step_id,omitempty"`
	SourceStepIndex int     `json:"source_step_index,omitempty"`
	HTTPVersion     string  `json:"http_version,omitempty"`
	AttemptCount    int     `json:"attempt_count,omitempty"`
	RetryWaitMs     float64 `json:"retry_wait_ms,omitempty"`
	EndsIteration   bool    `json:"ends_iteration,omitempty"`
	// Phase is read by timeAnatomyRequestExcluded (an "async " callback
	// step in archives written before async_dispatch was stamped).
	Phase string `json:"phase,omitempty"`
	// ProtocolEvidence is kept only for the keys timeAnatomyRequestExcluded
	// reads.
	ProtocolEvidence map[string]json.RawMessage `json:"protocol_evidence,omitempty"`
}

// archiveWaterfallEvidenceKeys are the protocol_evidence keys the drill-down
// decodes: the ones timeAnatomyRequestExcluded reads (it also reads Phase,
// which the lean projection carries) and the dispatch epoch the claim rule
// reads (timeAnatomyDispatchEpoch).
var archiveWaterfallEvidenceKeys = []string{"async_conversation", "background_request", "async_dispatch", timeAnatomyDispatchEpochKey}

// decodeArchiveWaterfallLine decodes one archive line into a TransactionEvent
// carrying only the lean projection, sanitized and with its derived fields
// (event_id included) computed at its line ordinal exactly as the report's
// streaming pass and the paged loader do.
func decodeArchiveWaterfallLine(line []byte, ordinal int64) (TransactionEvent, error) {
	if archiveWaterfallDecodeHook != nil {
		archiveWaterfallDecodeHook(ordinal)
	}
	var light archiveWaterfallLight
	if err := json.Unmarshal(line, &light); err != nil {
		return TransactionEvent{}, fmt.Errorf("decode archived transaction %d: %w", ordinal+1, err)
	}
	tx := TransactionEvent{
		Population: light.Population, EventID: light.EventID, Timestamp: light.Timestamp, TestID: light.TestID,
		Group: light.Group, Region: light.Region, SourceTransaction: light.SourceTransaction, ActionName: light.ActionName,
		VUserID: light.VUserID, Iteration: light.Iteration, Interrupted: light.Interrupted, VUserLifetime: light.VUserLifetime,
		ElapsedMs: light.ElapsedMs, BusinessTransaction: light.BusinessTransaction, CorrelationID: light.CorrelationID,
		TraceID: light.TraceID, TraceSent: light.TraceSent, DependencyType: light.DependencyType,
		DependencyTarget: light.DependencyTarget, DurationMs: light.DurationMs, DNSMs: light.DNSMs, ConnectMs: light.ConnectMs,
		TLSMs: light.TLSMs, TTFBMs: light.TTFBMs, StatusCode: light.StatusCode, Success: light.Success,
		ErrorMessage: light.ErrorMessage, SourceStepID: light.SourceStepID, SourceStepIndex: light.SourceStepIndex,
		HTTPVersion: light.HTTPVersion, AttemptCount: light.AttemptCount, RetryWaitMs: light.RetryWaitMs,
		EndsIteration: light.EndsIteration, Phase: light.Phase,
	}
	for _, key := range archiveWaterfallEvidenceKeys {
		raw, ok := light.ProtocolEvidence[key]
		if !ok {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return TransactionEvent{}, fmt.Errorf("decode archived transaction %d: %w", ordinal+1, err)
		}
		if tx.ProtocolEvidence == nil {
			tx.ProtocolEvidence = map[string]any{}
		}
		// Sanitized below exactly as the report's full decode is.
		tx.ProtocolEvidence[key] = value
	}
	if light.ServerTelemetry != nil {
		tx.ServerTelemetry = &protocol.ServerTelemetry{ConnectionReused: light.ServerTelemetry.ConnectionReused}
	}
	sanitizeArchiveDecodedRecordAt(&tx, ordinal)
	return tx, nil
}

// scanArchiveWaterfallLines streams the archive's transaction lines from
// line index from through to (inclusive; to < 0 reads to the end). Lines
// before from are read but never decoded; a line the prefilter rejects is
// not decoded either. Every line read counts against the query budget. fn
// returns true to stop the scan.
func scanArchiveWaterfallLines(ctx context.Context, record *storage.RunArchiveRecord, from, to int64, prefilter func([]byte) bool, fn func(ordinal int64, tx *TransactionEvent) bool) error {
	var linePrefilter func(int64, []byte) bool
	if prefilter != nil {
		linePrefilter = func(_ int64, line []byte) bool { return prefilter(line) }
	}
	return scanArchiveWaterfallLinesBudget(ctx, record, newArchiveScanBudget(ctx, archiveWaterfallScanRecords, archiveWaterfallScanBytes), from, to, linePrefilter, fn)
}

// scanArchiveWaterfallLinesBudget is scanArchiveWaterfallLines against a
// caller's budget, so several passes of one request share it, and with a
// prefilter that also sees the line's ordinal.
func scanArchiveWaterfallLinesBudget(ctx context.Context, record *storage.RunArchiveRecord, budget *archiveScanBudget, from, to int64, prefilter func(ordinal int64, line []byte) bool, fn func(ordinal int64, tx *TransactionEvent) bool) error {
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := newArchiveJSONLScanner(file)
	for ordinal := int64(0); scanner.Scan(); ordinal++ {
		if to >= 0 && ordinal > to {
			return nil
		}
		line := scanner.Bytes()
		if err := budget.Observe(line); err != nil {
			return err
		}
		if ordinal < from || (prefilter != nil && !prefilter(ordinal, line)) {
			continue
		}
		tx, err := decodeArchiveWaterfallLine(line, ordinal)
		if err != nil {
			return err
		}
		if fn(ordinal, &tx) {
			return nil
		}
	}
	return scanner.Err()
}

// locateArchiveWaterfallRoot finds the row with eventID: at the hinted
// ordinal when the hint verifies, otherwise by a substring prefilter over
// persisted ids, then by a decode of every row whose id is derived (legacy
// rows without a persisted event_id).
func locateArchiveWaterfallRoot(ctx context.Context, record *storage.RunArchiveRecord, eventID string, hints *archiveWaterfallHints) (TransactionEvent, int64, error) {
	var (
		root    TransactionEvent
		ordinal = int64(-1)
	)
	match := func(at int64, tx *TransactionEvent) bool {
		if tx.EventID != eventID {
			return false
		}
		root, ordinal = *tx, at
		return true
	}
	if hints.ok {
		if err := scanArchiveWaterfallLines(ctx, record, hints.ordinal, hints.ordinal, nil, match); err != nil {
			return TransactionEvent{}, 0, err
		}
		if ordinal >= 0 {
			return root, ordinal, nil
		}
		// A stale or wrong hint: fall back to a full search.
		*hints = archiveWaterfallHints{}
	}
	needle := []byte(eventID)
	if err := scanArchiveWaterfallLines(ctx, record, 0, -1, func(line []byte) bool { return bytes.Contains(line, needle) }, match); err != nil {
		return TransactionEvent{}, 0, err
	}
	if ordinal >= 0 {
		return root, ordinal, nil
	}
	// Every archived row carries the event_id key; a row whose persisted id
	// is non-empty cannot derive another one.
	persisted, empty := []byte(`"event_id":"`), []byte(`"event_id":""`)
	derived := func(line []byte) bool { return bytes.Contains(line, empty) || !bytes.Contains(line, persisted) }
	if err := scanArchiveWaterfallLines(ctx, record, 0, -1, derived, match); err != nil {
		return TransactionEvent{}, 0, err
	}
	if ordinal >= 0 {
		return root, ordinal, nil
	}
	return TransactionEvent{}, 0, errArchiveWaterfallNotFound
}

// archiveWaterfallIdentityPrefilter keeps the lines that can belong to the
// root's identity; the decoded identity is compared exactly afterwards.
func archiveWaterfallIdentityPrefilter(identity timeAnatomyIdentity, quality string) func([]byte) bool {
	var needle []byte
	if quality == timeAnatomyIdentityLifetime {
		needle = []byte(`"vuser_lifetime":` + strconv.FormatUint(identity.lifetime, 10))
	} else {
		needle = []byte(`"vuser_id":` + strconv.Itoa(identity.vuserID))
	}
	return func(line []byte) bool { return bytes.Contains(line, needle) }
}

type archiveWaterfallChild struct {
	ordinal int64
	startNs int64
	tx      TransactionEvent
}

// archiveWaterfallReplay replays the report accumulator's state for the
// root's identity: the same exclusion (timeAnatomyRequestExcluded), the same
// iteration retirement and legacy-lifetime restart, the same per-identity cap
// (timeAnatomyPendingBuffer.insertCapped), the same claim rule (dispatch
// epochs included), the same drop hull and the same work charges against
// the same budget (timeAnatomyWorkBudget): each request's insert, each
// iteration flush and legacy restart walk the report makes, each
// transaction's candidate scan and each joined transaction's fold
// (timeAnatomyJoinWork). Only the claimable
// requests still buffered keep their decoded row, so the replay holds at
// most maxTimeAnatomyPendingPerVUser of them however long the root's window
// is.
//
// The report's run-wide state (the global entry and byte caps, identity
// eviction and tombstones, and work other identities spent) cannot be
// replayed from one identity; the handler reports it as join_may_differ.
type archiveWaterfallReplay struct {
	window       timeAnatomyClaimWindow
	legacy       bool
	pending      timeAnatomyPendingBuffer
	payloads     map[int64]TransactionEvent // by ordinal
	hull         timeAnatomyDropHull
	current      int64
	hasIteration bool
	// endedIteration is the latest iteration an ends_iteration row of the
	// identity closed (the report's state.endedIteration).
	endedIteration int64
	hasEnded       bool
	// work is what the report charged for this identity's rows; once it
	// passes the budget at the row it charges (timeAnatomyWorkBudget of the
	// rows up to and including it, never below the report's at that row)
	// the report's budget is spent too, so every later transaction row (the
	// root included) is partial.
	work          int64
	workExhausted bool
	// rootRefused is set when the root's own candidate scan could not be
	// charged: the report's root then claims nothing. A root whose scan was
	// charged but whose fold was not keeps its claims (and is partial).
	rootRefused bool
}

func newArchiveWaterfallReplay(window timeAnatomyClaimWindow) *archiveWaterfallReplay {
	return &archiveWaterfallReplay{window: window, payloads: map[int64]TransactionEvent{}}
}

// spend is the report's spendWork for this identity's share, charged at
// line ordinal (the report has merged at most ordinal+1 rows by then).
func (r *archiveWaterfallReplay) spend(ordinal, units int64) bool {
	if r.workExhausted {
		return false
	}
	if r.work+units > timeAnatomyWorkBudget(ordinal+1) {
		r.workExhausted = true
		return false
	}
	r.work += units
	return true
}

// offer is mergeRequest for one request row of the root's identity written
// before the root.
func (r *archiveWaterfallReplay) offer(ordinal int64, tx *TransactionEvent, startNs int64) {
	if r.workExhausted || timeAnatomyRequestExcluded(tx) {
		return
	}
	dropPayload := func(entry *timeAnatomyPendingRequest) {
		delete(r.payloads, entry.ordinal)
	}
	if !r.hasIteration || tx.Iteration != r.current {
		current := tx.Iteration
		// restartLegacyLifetime: a reused legacy VUser ID. The walk is
		// charged exactly when the report charges it; a refused charge
		// spends the budget (nothing after it claims).
		if r.legacy && r.hasIteration && r.hasEnded && current >= 1 && current <= r.endedIteration {
			if r.pending.len() > 0 && r.pending.maxIteration >= 1 && r.spend(ordinal, int64(r.pending.len())) {
				r.pending.filter(func(entry *timeAnatomyPendingRequest) bool {
					return entry.iteration < 1
				}, dropPayload)
			}
			r.hasEnded, r.endedIteration = false, 0
		}
		// flushIteration: skipped when nothing can retire, charged
		// otherwise.
		if r.pending.len() > 0 && !timeAnatomyIterationRetained(r.pending.minIteration, current) && r.spend(ordinal, int64(r.pending.len())) {
			r.pending.filter(func(entry *timeAnatomyPendingRequest) bool {
				return timeAnatomyIterationRetained(entry.iteration, current)
			}, dropPayload)
		}
		r.current, r.hasIteration = current, true
	}
	if !r.spend(ordinal, int64(r.pending.insertCost(startNs))+1) {
		return
	}
	entry := timeAnatomyPendingRequest{
		ordinal:       ordinal,
		startNs:       startNs,
		iteration:     tx.Iteration,
		dispatchEpoch: timeAnatomyDispatchEpoch(tx),
		durationMs:    archiveRequestPhaseSplitOf(tx).Duration,
		sourceTx:      tx.SourceTransaction,
	}
	// insertCapped drops the incoming request when it starts before every
	// buffered one, otherwise the earliest buffered one.
	limit := maxTimeAnatomyPendingPerVUser
	rejected, evicted := false, int64(-1)
	if r.pending.len() >= limit && r.pending.len() > 0 {
		if head := r.pending.items[r.pending.head]; startNs < head.startNs {
			rejected = true
		} else {
			evicted = head.ordinal
		}
	}
	if droppedStartNs, dropped := r.pending.insertCapped(entry, limit); dropped {
		r.hull.add(droppedStartNs, timeAnatomyDropKeyCap)
		if evicted >= 0 {
			delete(r.payloads, evicted)
		}
	}
	if !rejected && timeAnatomyClaimsRequest(r.window, entry.claimChild()) {
		r.payloads[ordinal] = *tx
	}
}

// transaction is mergeTransaction's effect on the identity's own state for a
// transaction row of the identity at line ordinal: the candidates it scans
// are charged, a joined row's fold is charged (timeAnatomyJoinWork of the
// buffered requests it claims, unless it is partial or interrupted), and an
// ends_iteration row records the iteration it closed. The root itself
// passes root=true: its claims are the payloads the replay kept, and a
// refused scan means it claims nothing.
func (r *archiveWaterfallReplay) transaction(ordinal int64, tx *TransactionEvent, root bool) {
	startNs, endNs, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, tx.ElapsedMs)
	if !ok {
		return
	}
	lo, hi := r.pending.candidates(startNs, endNs)
	if !r.spend(ordinal, int64(hi-lo)+1) {
		if root {
			r.rootRefused = true
		}
		hi = lo
	}
	if !root && tx.EndsIteration && !tx.Interrupted && (!r.hasEnded || tx.Iteration > r.endedIteration) {
		r.endedIteration, r.hasEnded = tx.Iteration, true
	}
	if hi == lo || tx.Interrupted || r.hull.overlaps(startNs, endNs) {
		return
	}
	claims := 0
	if root {
		claims = len(r.payloads)
	} else {
		window := timeAnatomyClaimWindowOf(tx, startNs, endNs)
		for index := lo; index < hi; index++ {
			if timeAnatomyClaimsRequest(window, r.pending.items[index].claimChild()) {
				claims++
			}
		}
	}
	if claims > 0 {
		r.spend(ordinal, timeAnatomyJoinWork(claims))
	}
}

// partialReasons is the report's partial verdict for the root, with its
// reasons: the drop hull over the root's window, or a spent work budget.
func (r *archiveWaterfallReplay) partialReasons(startNs, endNs int64) []string {
	var reasons []string
	if r.hull.overlaps(startNs, endNs) {
		for _, drop := range timeAnatomyDropReasons {
			if r.hull.reasons&drop.bit != 0 {
				reasons = append(reasons, drop.reason)
			}
		}
	}
	if r.workExhausted {
		reasons = append(reasons, timeAnatomyReasonWorkBudget)
	}
	return reasons
}

// claimed returns the requests the root claims, in (start, ordinal) order.
// When the budget was spent before the root's own scan the report's root
// claims nothing; one spent by the root's fold keeps its claims.
func (r *archiveWaterfallReplay) claimed() []archiveWaterfallChild {
	if r.rootRefused {
		return nil
	}
	claimed := make([]archiveWaterfallChild, 0, len(r.payloads))
	for index := r.pending.head; index < len(r.pending.items); index++ {
		entry := &r.pending.items[index]
		if tx, ok := r.payloads[entry.ordinal]; ok {
			claimed = append(claimed, archiveWaterfallChild{ordinal: entry.ordinal, startNs: entry.startNs, tx: tx})
		}
	}
	return claimed
}

// buildArchiveTransactionWaterfall rebuilds one event's waterfall. It joins
// requests with the time anatomy's identity, exclusion, window, claim rule,
// two-iteration grace, per-identity buffer cap and work budget, so a
// transaction root's claimed requests and decomposition equal the report's
// instance, and it is marked partial exactly when that identity's own state
// made the report's instance partial (run-wide state: join_may_differ).
func buildArchiveTransactionWaterfall(ctx context.Context, record *storage.RunArchiveRecord, eventID string, hints archiveWaterfallHints, maxRequests int) (*archiveWaterfallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxRequests < 1 {
		maxRequests = archiveWaterfallDefaultMaxRequests
	}
	root, rootOrdinal, err := locateArchiveWaterfallRoot(ctx, record, eventID, &hints)
	if err != nil {
		return nil, err
	}
	population := archiveEventPopulation(root)
	identity, quality := timeAnatomyIdentityOf(root.Group, root.VUserID, root.VUserLifetime)
	startNs, endNs, timed := timeAnatomyWindow(root.Timestamp, root.DurationMs, root.ElapsedMs)
	if population == "action" {
		_, endNs, _ = timeAnatomyWindow(root.Timestamp, root.DurationMs, 0)
	}
	result := &ArchiveTransactionWaterfall{
		Schema:          transactionWaterfallSchema,
		JoinRule:        timeAnatomyJoinRule,
		PhaseModel:      timeAnatomyPhaseModel,
		IdentityQuality: quality,
		Root:            archiveWaterfallRowOf(&root, rootOrdinal, population, startNs),
		Requests:        []ArchiveWaterfallRow{},
	}
	if quality == timeAnatomyIdentityNone || !timed {
		return result, nil
	}
	// A request the report counts outside every transaction (async,
	// background, async dispatch) has no enclosing transaction, whatever
	// window covers it.
	if population == "action" && timeAnatomyRequestExcluded(&root) {
		return result, nil
	}
	prefilter := archiveWaterfallIdentityPrefilter(identity, quality)
	sameIdentity := func(tx *TransactionEvent) bool {
		other, _ := timeAnatomyIdentityOf(tx.Group, tx.VUserID, tx.VUserLifetime)
		return other == identity
	}
	switch population {
	case "transaction":
		if err := collectArchiveWaterfallChildren(ctx, record, result, &root, rootOrdinal, startNs, endNs, maxRequests, prefilter, sameIdentity); err != nil {
			return nil, err
		}
	case "action":
		if err := collectArchiveWaterfallEnclosing(ctx, record, result, &root, rootOrdinal, startNs, prefilter, sameIdentity); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// collectArchiveWaterfallChildren replays the root identity's rows from line
// 0 up to the root, and counts the late ones (request rows written after the
// root, starting inside it, that the report could claim). A partial root
// (the report keeps it out of its decomposition) gets its claimed requests
// but no decomposition: the requests the report lost would show as time
// outside requests.
func collectArchiveWaterfallChildren(ctx context.Context, record *storage.RunArchiveRecord, result *archiveWaterfallResult, root *TransactionEvent, rootOrdinal, startNs, endNs int64, maxRequests int, prefilter func([]byte) bool, sameIdentity func(*TransactionEvent) bool) error {
	replay := newArchiveWaterfallReplay(timeAnatomyClaimWindowOf(root, startNs, endNs))
	replay.legacy = result.IdentityQuality == timeAnatomyIdentityLegacy
	late := 0
	err := scanArchiveWaterfallLines(ctx, record, 0, rootOrdinal+archiveWaterfallLateLines, prefilter, func(ordinal int64, tx *TransactionEvent) bool {
		if ordinal == rootOrdinal || !sameIdentity(tx) {
			return false
		}
		switch archiveEventPopulation(*tx) {
		case "transaction":
			if ordinal < rootOrdinal {
				replay.transaction(ordinal, tx, false)
			}
			return false
		case "action":
		default:
			return false
		}
		if timeAnatomyRequestExcluded(tx) {
			return false
		}
		childStartNs, _, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, 0)
		if !ok {
			return false
		}
		if ordinal > rootOrdinal {
			// The report's late test (timeAnatomyIdentityState.late): only a
			// row the root could claim, so the dispatch half of the claim
			// rule applies too.
			if timeAnatomyDispatchClaimable(replay.window.DispatchEpoch, timeAnatomyDispatchEpoch(tx)) && timeAnatomyLateFor(startNs, endNs, childStartNs) {
				late++
			}
			return false
		}
		replay.offer(ordinal, tx, childStartNs)
		return false
	})
	if err != nil {
		return err
	}
	// The root's own claim scan and fold.
	replay.transaction(rootOrdinal, root, true)
	claimed := replay.claimed()
	waterfall := result
	if reasons := replay.partialReasons(startNs, endNs); len(reasons) > 0 {
		waterfall.Partial, waterfall.PartialReasons = true, reasons
	}
	waterfall.LateRequests = late
	waterfall.RequestsTotal = len(claimed)
	if len(claimed) == 0 {
		return nil
	}
	spans := make([]timeAnatomySpan, 0, len(claimed))
	for index := range claimed {
		child := &claimed[index]
		split := archiveRequestPhaseSplitOf(&child.tx)
		spans = append(spans, timeAnatomySpan{StartNs: child.startNs, DurationMs: split.Duration, Phases: split.Parts})
		if index < maxRequests {
			waterfall.Requests = append(waterfall.Requests, archiveWaterfallRowOf(&child.tx, child.ordinal, "action", startNs))
		}
	}
	waterfall.Truncated = len(claimed) > maxRequests
	if waterfall.Partial {
		return nil
	}
	duration := timeAnatomyPos(root.DurationMs)
	vector, _ := timeAnatomyDecompose(duration, startNs, endNs, spans, nil)
	decomposition := vector.decomposition(duration)
	waterfall.Decomposition = &decomposition
	return nil
}

func collectArchiveWaterfallEnclosing(ctx context.Context, record *storage.RunArchiveRecord, waterfall *ArchiveTransactionWaterfall, root *TransactionEvent, rootOrdinal, rootStartNs int64, prefilter func([]byte) bool, sameIdentity func(*TransactionEvent) bool) error {
	var (
		enclosing        []ArchiveWaterfallRef
		currentIteration = root.Iteration
	)
	err := scanArchiveWaterfallLines(ctx, record, rootOrdinal+1, rootOrdinal+archiveWaterfallLateLines, prefilter, func(ordinal int64, tx *TransactionEvent) bool {
		if !sameIdentity(tx) {
			return false
		}
		switch archiveEventPopulation(*tx) {
		case "action":
			if tx.Iteration != currentIteration {
				currentIteration = tx.Iteration
				if !timeAnatomyIterationRetained(root.Iteration, currentIteration) {
					// The accumulator retired the request: nothing after
					// this can claim it.
					return true
				}
			}
		case "transaction":
			startNs, endNs, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, tx.ElapsedMs)
			if ok && timeAnatomyClaimsRequest(timeAnatomyClaimWindowOf(tx, startNs, endNs), timeAnatomyClaimChildOf(root, rootStartNs)) {
				enclosing = append(enclosing, ArchiveWaterfallRef{
					EventID: tx.EventID, Ordinal: ordinal, ActionName: tx.ActionName,
					Timestamp: tx.Timestamp, SpanMs: float64(endNs-startNs) / 1e6,
				})
			}
		}
		return false
	})
	if err != nil {
		return err
	}
	slices.SortFunc(enclosing, func(left, right ArchiveWaterfallRef) int {
		return cmp.Or(cmp.Compare(left.SpanMs, right.SpanMs), cmp.Compare(left.Ordinal, right.Ordinal))
	})
	if len(enclosing) > archiveWaterfallMaxEnclosing {
		enclosing = enclosing[:archiveWaterfallMaxEnclosing]
	}
	if len(enclosing) > 0 {
		waterfall.EnclosingTransactions = enclosing
	}
	return nil
}

// archiveWaterfallRowOf projects one archived row. A request row carries its
// sequential_clipped_v1 phase split; a transaction row's own network fields
// are rollups of its requests, so its whole duration is unsplit and its
// requests carry the phases.
func archiveWaterfallRowOf(tx *TransactionEvent, ordinal int64, population string, windowStartNs int64) ArchiveWaterfallRow {
	row := ArchiveWaterfallRow{
		EventID:           tx.EventID,
		Ordinal:           ordinal,
		Population:        population,
		Timestamp:         tx.Timestamp,
		DurationMs:        tx.DurationMs,
		ElapsedMs:         tx.ElapsedMs,
		ActionName:        tx.ActionName,
		SourceTransaction: tx.SourceTransaction,
		SourceStepIndex:   tx.SourceStepIndex,
		DependencyType:    tx.DependencyType,
		DependencyTarget:  tx.DependencyTarget,
		HTTPVersion:       tx.HTTPVersion,
		StatusCode:        tx.StatusCode,
		Success:           tx.Success,
		Interrupted:       tx.Interrupted,
		ErrorMessage:      archiveWaterfallTruncate(tx.ErrorMessage, archiveWaterfallErrorMessageBytes),
		VUserID:           tx.VUserID,
		VUserLifetime:     tx.VUserLifetime,
		Iteration:         tx.Iteration,
	}
	if !tx.Timestamp.IsZero() && windowStartNs != 0 {
		row.OffsetMs = max(float64(tx.Timestamp.UnixNano()-windowStartNs)/1e6, 0)
	}
	if !tx.Success {
		row.ErrorClass = engine.ClassifyError(tx.StatusCode, tx.ErrorMessage, tx.Success)
	}
	if tx.TraceSent != nil && *tx.TraceSent {
		row.TraceID = tx.TraceID
	}
	if population == "action" {
		split := archiveRequestPhaseSplitOf(tx)
		row.Phases = split.means()
		row.Clamped = split.Clamped
		row.Retried = tx.AttemptCount > 1 || tx.RetryWaitMs > 0
		class, _ := timeAnatomyConnectionClass(tx)
		row.Connection = timeAnatomyConnectionName(class)
	} else {
		row.Phases.UnsplitMs = timeAnatomyPos(tx.DurationMs)
	}
	return row
}

// archiveWaterfallTruncate cuts value to at most limit bytes on a rune
// boundary.
func archiveWaterfallTruncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

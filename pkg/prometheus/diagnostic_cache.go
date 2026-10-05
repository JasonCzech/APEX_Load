package prometheus

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

const diagnosticInterval = time.Minute
const maxDiagnosticCacheEntries = 2048

var errDiagnosticCache = errors.New("Prometheus diagnostic polling cache capacity exceeded")

type diagnosticState struct {
	permit   chan struct{}
	coverage monitorprovider.Coverage
	used     time.Time
}

var diagnosticCache = struct {
	sync.Mutex
	entries map[[32]byte]*diagnosticState
}{entries: map[[32]byte]*diagnosticState{}}

func diagnosticStateFor(key [32]byte) (*diagnosticState, error) {
	diagnosticCache.Lock()
	defer diagnosticCache.Unlock()
	if state := diagnosticCache.entries[key]; state != nil {
		state.used = time.Now()
		return state, nil
	}
	if len(diagnosticCache.entries) >= maxDiagnosticCacheEntries {
		for k, state := range diagnosticCache.entries {
			if time.Since(state.used) > 10*time.Minute && len(state.permit) == 0 {
				delete(diagnosticCache.entries, k)
			}
		}
	}
	if len(diagnosticCache.entries) >= maxDiagnosticCacheEntries {
		return nil, errDiagnosticCache
	}
	state := &diagnosticState{permit: make(chan struct{}, 1), used: time.Now()}
	diagnosticCache.entries[key] = state
	return state, nil
}

// Successful current-state observations are sampled no more often than once a
// minute. Cached coverage retains its original observation timestamp and never
// replays evidence records. CollectionID separates distinct jobs in one run;
// the cache survives SDK recreation but deliberately does not claim durability.
func (c *Client) collectDiagnostics(ctx context.Context, s Selection, from, to time.Time, runID, kind, path string) ([]monitorprovider.Diagnostic, monitorprovider.Coverage, error) {
	coverage := monitorprovider.Coverage{Status: "complete", From: from, To: to, Freshness: "sampled_current_state"}
	key := sha256.Sum256([]byte(QuotaKey(c.cfg, c.credentials) + "\x00" + runID + "\x00" + s.CollectionID + "\x00" + s.EvidenceSelector + "\x00" + kind))
	state, err := diagnosticStateFor(key)
	if err != nil {
		return nil, coverage, err
	}
	select {
	case state.permit <- struct{}{}:
	case <-ctx.Done():
		return nil, coverage, ctx.Err()
	}
	defer func() { <-state.permit }()
	if state.coverage.ObservedAt != nil && time.Since(*state.coverage.ObservedAt) < diagnosticInterval {
		coverage = state.coverage
		coverage.From = from
		coverage.To = to
		coverage.Evidence = 0
		coverage.Cached = true
		coverage.CacheAgeSeconds = max(0, int(time.Since(*coverage.ObservedAt).Seconds()))
		coverage.Message = "Cached current-state diagnostic observation; no evidence records replayed"
		return nil, coverage, nil
	}
	env, err := c.query(ctx, path, nil)
	if err != nil {
		return nil, coverage, err
	}
	observed := time.Now().UTC()
	var records []monitorprovider.Diagnostic
	var truncated bool
	if kind == "targets" {
		records, truncated, err = c.targetEvidence(env, observed, s.EvidenceSelector)
	} else {
		records, truncated, err = c.alertEvidence(env, observed, s.EvidenceSelector)
	}
	if err != nil {
		return nil, coverage, err
	}
	coverage.ObservedAt = &observed
	coverage.Evidence = len(records)
	coverage.Truncated = truncated
	coverage.Warnings = env.Warnings
	coverage.Infos = env.Infos
	if len(records) == 0 {
		coverage.Status = "no_data"
		coverage.Message = "No current-state diagnostics matched the approved selector"
	}
	if truncated {
		coverage.Status = "partial"
		coverage.Message = "Diagnostic evidence retention limits truncated this sampled observation"
	} else if len(env.Warnings) > 0 {
		// An upstream warning may describe omitted current-state records. Neither
		// populated nor empty partial snapshots establish successful poll coverage.
		coverage.Status = "partial"
		coverage.Message = "Upstream warnings indicate incomplete sampled diagnostic evidence"
	}
	// Only a complete successful snapshot suppresses another poll. A partial
	// response must leave the last success untouched and permit recovery.
	if coverage.Status == "complete" || coverage.Status == "no_data" {
		state.coverage = coverage
	}
	return records, coverage, nil
}

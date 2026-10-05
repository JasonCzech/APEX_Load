package api

import (
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	// maxProviderLookupTraceIDs is how many trace IDs one provider poll asks about.
	maxProviderLookupTraceIDs = 64
	// maxLookupCandidates bounds what accumulates between polls.
	maxLookupCandidates = 512
	// maxProviderLookupRecords bounds the lookup outcomes one run keeps.
	maxProviderLookupRecords = 20000

	providerLookupSchema     = "apexload.provider_lookup.v1"
	providerLookupRecordType = "provider_lookups_v1"
	providerLookupsFileName  = "provider_lookups.jsonl"
)

// Lookup outcomes. not_sampled, not_retained and expired need provider
// sampling or retention evidence that the live queries do not return yet.
const (
	LookupFound       = "found"
	LookupNotFound    = "not_found"
	LookupNotSampled  = "not_sampled"
	LookupNotRetained = "not_retained"
	LookupExpired     = "expired"
	LookupError       = "error"
)

// Lookup ranks: errors first, then slow requests, then representative
// samples, then correlation IDs from peers that send no exemplars.
const (
	lookupRankError = iota
	lookupRankSlow
	lookupRankSample
	lookupRankCorrelation
)

type lookupCandidate struct {
	traceID    string
	rank       int
	durationMs float64
}

// exemplarLookupSet gathers the exemplars seen since a provider's last poll.
// Each poll takes the best 64 and starts a new window.
type exemplarLookupSet struct {
	candidates map[string]lookupCandidate
}

func (s *exemplarLookupSet) add(c lookupCandidate) {
	if s.candidates == nil {
		s.candidates = map[string]lookupCandidate{}
	}
	if existing, ok := s.candidates[c.traceID]; ok {
		if lookupBetter(c, existing) {
			s.candidates[c.traceID] = c
		}
		return
	}
	if len(s.candidates) < maxLookupCandidates {
		s.candidates[c.traceID] = c
		return
	}
	worst := ""
	for id, existing := range s.candidates {
		if worst == "" || lookupBetter(s.candidates[worst], existing) {
			worst = id
		}
	}
	if lookupBetter(c, s.candidates[worst]) {
		delete(s.candidates, worst)
		s.candidates[c.traceID] = c
	}
}

// observe adds a live window's exemplars. Transactions without exemplars
// contribute their correlation IDs at the lowest rank.
func (s *exemplarLookupSet) observe(live *LiveMetrics) {
	if live == nil {
		return
	}
	for _, tx := range live.Transactions {
		if tx == nil {
			continue
		}
		for _, exemplar := range tx.Exemplars {
			if !protocol.ValidTraceID(exemplar.TraceID) {
				continue
			}
			rank := lookupRankSample
			switch {
			case !exemplar.Success:
				rank = lookupRankError
			case exemplar.Reason == engine.ExemplarSlowestSuccess:
				rank = lookupRankSlow
			}
			s.add(lookupCandidate{traceID: exemplar.TraceID, rank: rank, durationMs: exemplar.DurationMs})
		}
		if len(tx.Exemplars) == 0 {
			for _, correlation := range tx.CorrelationIDs {
				if id := strings.ToLower(strings.TrimSpace(traceIDFromCorrelationID(correlation))); protocol.ValidTraceID(id) {
					s.add(lookupCandidate{traceID: id, rank: lookupRankCorrelation})
				}
			}
		}
	}
}

// take returns up to 64 trace IDs, errors first, then slow, then
// representative, and clears the window.
func (s *exemplarLookupSet) take() []string {
	candidates := make([]lookupCandidate, 0, len(s.candidates))
	for _, c := range s.candidates {
		candidates = append(candidates, c)
	}
	s.candidates = nil
	sort.Slice(candidates, func(i, j int) bool { return lookupBetter(candidates[i], candidates[j]) })
	if len(candidates) > maxProviderLookupTraceIDs {
		candidates = candidates[:maxProviderLookupTraceIDs]
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.traceID)
	}
	return out
}

// restore puts unqueried IDs back when a poll could not run.
func (s *exemplarLookupSet) restore(ids []string) {
	for i, id := range ids {
		s.add(lookupCandidate{traceID: id, rank: lookupRankCorrelation, durationMs: float64(len(ids) - i)})
	}
}

func lookupBetter(a, b lookupCandidate) bool {
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	if a.durationMs != b.durationMs {
		return a.durationMs > b.durationMs
	}
	return a.traceID < b.traceID
}

// ProviderLookupRecord is one trace ID asked of one provider in one poll.
type ProviderLookupRecord struct {
	Schema   string    `json:"schema"`
	Provider string    `json:"provider"`
	TraceID  string    `json:"trace_id"`
	Outcome  string    `json:"outcome"`
	At       time.Time `json:"at"`
	// ResultLimited means the provider returned its row limit, so a miss is
	// not conclusive.
	ResultLimited bool   `json:"result_limited,omitempty"`
	Message       string `json:"message,omitempty"`
}

// providerLookupOutcomes classifies one poll's trace IDs against what the
// provider returned. A failed query marks every ID error.
func providerLookupOutcomes(provider string, traceIDs []string, returned []string, limited bool, err error, at time.Time) []ProviderLookupRecord {
	found := make(map[string]bool, len(returned))
	for _, id := range returned {
		found[strings.ToLower(strings.TrimSpace(id))] = true
	}
	message := ""
	if err != nil {
		message = err.Error()
		if len(message) > 256 {
			message = message[:256]
		}
	}
	out := make([]ProviderLookupRecord, 0, len(traceIDs))
	for _, id := range traceIDs {
		record := ProviderLookupRecord{Schema: providerLookupSchema, Provider: provider, TraceID: id, At: at.UTC()}
		switch {
		case err != nil:
			record.Outcome, record.Message = LookupError, message
		case found[id]:
			record.Outcome = LookupFound
		default:
			record.Outcome, record.ResultLimited = LookupNotFound, limited
		}
		out = append(out, record)
	}
	return out
}

// recordProviderLookups keeps a bounded copy for distributed archives and
// streams local runs' records through the archive writer.
func (a *runArtifacts) recordProviderLookups(records []ProviderLookupRecord) {
	if a == nil || len(records) == 0 {
		return
	}
	if recorder, ok := a.recorder.(interface {
		RecordProviderLookups([]ProviderLookupRecord) error
	}); ok {
		_ = recorder.RecordProviderLookups(records)
	}
	a.lookupMu.Lock()
	defer a.lookupMu.Unlock()
	room := max(0, maxProviderLookupRecords-len(a.providerLookups))
	if room < len(records) {
		a.providerLookupsTruncated = true
		records = records[:room]
	}
	a.providerLookups = append(a.providerLookups, records...)
}

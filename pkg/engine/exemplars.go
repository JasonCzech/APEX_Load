package engine

import (
	"math/rand/v2"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Exemplar reasons. Each transaction keeps at most one of each per window.
const (
	ExemplarSlowestSuccess = "slowest_success"
	ExemplarFirstError     = "first_error"
	ExemplarSample         = "sample"
)

// MaxTraceExemplars bounds the exemplars one transaction carries per window.
const MaxTraceExemplars = 3

// TraceExemplar is one request whose trace context reached the server,
// kept so APM and log tools can be asked about that exact request.
type TraceExemplar struct {
	TraceID    string    `json:"trace_id"`
	SpanID     string    `json:"span_id,omitempty"`
	DurationMs float64   `json:"duration_ms"`
	Success    bool      `json:"success"`
	StatusCode int       `json:"status_code,omitempty"`
	Reason     string    `json:"reason"`
	Timestamp  time.Time `json:"timestamp"`
}

// recordTraceExemplar keeps the window's slowest success, its first error and
// one uniformly sampled request. Only results that sent a valid trace qualify.
func recordTraceExemplar(metrics *ActionMetrics, result *protocol.Result) {
	if metrics == nil || result == nil {
		return
	}
	traceID := result.TraceContext.LookupTraceID()
	if traceID == "" {
		return
	}
	candidate := TraceExemplar{
		TraceID: traceID, SpanID: result.TraceContext.SpanID, DurationMs: durationToMilliseconds(result.Duration),
		Success: result.Success, StatusCode: result.StatusCode, Timestamp: result.StartTime,
	}
	metrics.exemplarCandidates++
	replace := func(reason string, keep func(existing TraceExemplar) bool) {
		candidate := candidate
		candidate.Reason = reason
		for i, existing := range metrics.Exemplars {
			if existing.Reason == reason {
				if !keep(existing) {
					metrics.Exemplars[i] = candidate
				}
				return
			}
		}
		metrics.Exemplars = append(metrics.Exemplars, candidate)
	}
	if result.Success {
		replace(ExemplarSlowestSuccess, func(existing TraceExemplar) bool { return existing.DurationMs >= candidate.DurationMs })
	} else {
		replace(ExemplarFirstError, func(TraceExemplar) bool { return true })
	}
	// Reservoir of one: every qualifying request is equally likely to remain.
	n := metrics.exemplarCandidates
	replace(ExemplarSample, func(TraceExemplar) bool { return rand.Int64N(n) != 0 })
}

// MergeTraceExemplars combines two sources' exemplars for one transaction:
// the slower success, the earlier error, and either source's sample.
func MergeTraceExemplars(current, next []TraceExemplar) []TraceExemplar {
	byReason := map[string]TraceExemplar{}
	order := []string{ExemplarSlowestSuccess, ExemplarFirstError, ExemplarSample}
	for _, set := range [][]TraceExemplar{current, next} {
		for _, exemplar := range set {
			existing, ok := byReason[exemplar.Reason]
			switch {
			case !ok:
				byReason[exemplar.Reason] = exemplar
			case exemplar.Reason == ExemplarSlowestSuccess && exemplar.DurationMs > existing.DurationMs:
				byReason[exemplar.Reason] = exemplar
			case exemplar.Reason == ExemplarFirstError && exemplar.Timestamp.Before(existing.Timestamp):
				byReason[exemplar.Reason] = exemplar
			}
		}
	}
	var out []TraceExemplar
	for _, reason := range order {
		if exemplar, ok := byReason[reason]; ok {
			out = append(out, exemplar)
		}
	}
	return out
}

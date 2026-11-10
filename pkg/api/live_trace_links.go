package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/deeplink"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	// maxLiveTraceLinkIDs bounds the trace ids one live link request expands.
	// It holds a whole live window's exemplars (the dashboard keeps up to 200
	// transactions with 3 exemplars each), so the drawer resolves the run's
	// link targets once per window instead of once per small batch.
	maxLiveTraceLinkIDs = 600
	// liveTraceLinksMaxBodyBytes fits 600 quoted 32-character trace ids
	// (~22 KiB), two timestamps and slack.
	liveTraceLinksMaxBodyBytes = 32 << 10
)

// LiveTraceLinksRequest names live exemplar trace ids and the window to open
// them in (RFC 3339, from before to).
type LiveTraceLinksRequest struct {
	TraceIDs []string `json:"trace_ids"`
	From     string   `json:"from"`
	To       string   `json:"to"`
}

// LiveTraceLinks are one trace id's provider links.
type LiveTraceLinks struct {
	TraceID string         `json:"trace_id"`
	Links   []ExemplarLink `json:"links"`
}

// LiveTraceLinksResponse lists each requested trace id, in request order
// without duplicates, with its links. Configured is false when the run has no
// trace provider with a trace link template, so no trace has links.
// LinksUnavailable means the link settings could not be read.
type LiveTraceLinksResponse struct {
	Traces           []LiveTraceLinks `json:"traces"`
	Configured       bool             `json:"configured"`
	LinksUnavailable bool             `json:"links_unavailable,omitempty"`
}

// handleLiveTraceLinks expands live exemplar trace ids into the run's trace
// provider links. It reads only the run's connection bindings, so it works
// while the run is live and needs no archive.
func (s *Server) handleLiveTraceLinks(w http.ResponseWriter, r *http.Request) {
	test, ok := s.runner.GetTestSummary(r.PathValue("id"))
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	var request LiveTraceLinksRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	from, fromErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.From))
	to, toErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.To))
	if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > maxRCAWindowLinkSpan {
		writeError(w, http.StatusBadRequest, "from and to must be RFC 3339 times, from before to, at most 7 days apart")
		return
	}
	if len(request.TraceIDs) == 0 || len(request.TraceIDs) > maxLiveTraceLinkIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("trace_ids must name 1 to %d trace ids", maxLiveTraceLinkIDs))
		return
	}
	seen := make(map[string]bool, len(request.TraceIDs))
	ids := make([]string, 0, len(request.TraceIDs))
	for _, raw := range request.TraceIDs {
		id := strings.TrimSpace(raw)
		if !protocol.ValidTraceID(id) {
			writeError(w, http.StatusBadRequest, "each trace id must be 32 lowercase hex characters and not all zero")
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	out := LiveTraceLinksResponse{Traces: make([]LiveTraceLinks, 0, len(ids))}
	targets, err := s.runTraceLinkTargets(test)
	if err != nil {
		// Links are secondary: report them unavailable rather than fail.
		slog.Warn("live trace links unavailable", "test_id", test.ID, "error", err)
		out.LinksUnavailable, targets = true, nil
	}
	out.Configured = len(targets) > 0
	for _, id := range ids {
		entry := LiveTraceLinks{TraceID: id, Links: []ExemplarLink{}}
		values := deeplink.Values{TraceID: id, From: from, To: to}
		for _, t := range targets {
			if link, err := t.template.Expand(values); err == nil {
				entry.Links = append(entry.Links, ExemplarLink{Provider: t.provider, ConnectionID: t.connectionID, URL: link})
			}
		}
		out.Traces = append(out.Traces, entry)
	}
	writeJSON(w, http.StatusOK, out)
}

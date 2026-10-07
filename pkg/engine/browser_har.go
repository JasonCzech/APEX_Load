package engine

import (
	"log/slog"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// BrowserHARArtifactSink retains a session's published native HAR artifact
// in the run archive. The sink takes the file over: once it returns nil the
// artifact has left its HAR directory.
type BrowserHARArtifactSink interface {
	RecordBrowserHARArtifact(protocol.BrowserHARArtifact) error
}

// handOffBrowserHARArtifacts gives a sink the HAR artifacts a protocol
// published since its last teardown. Without such a sink the artifacts stay
// in their HAR directory under its retention policy. A failed handoff is
// optional evidence lost, never a failed run; the sink records it.
func handOffBrowserHARArtifacts(p protocol.Protocol, sink ResultSink) {
	provider, ok := p.(protocol.BrowserHARArtifactProvider)
	if !ok {
		return
	}
	artifacts := takeBrowserHARArtifacts(provider)
	harSink, ok := sink.(BrowserHARArtifactSink)
	if !ok {
		return
	}
	for _, artifact := range artifacts {
		if err := harSink.RecordBrowserHARArtifact(artifact); err != nil {
			slog.Warn("browser HAR artifact was not retained in the run archive", "generation", artifact.Generation, "err", err)
		}
	}
}

func takeBrowserHARArtifacts(provider protocol.BrowserHARArtifactProvider) (out []protocol.BrowserHARArtifact) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return provider.TakeBrowserHARArtifacts()
}

// BrowserInteractionEvidenceSink retains a session's interaction evidence.
type BrowserInteractionEvidenceSink interface {
	RecordBrowserInteractionEvidence(*protocol.BrowserInteractionEvidence) error
}

// maxHandedOffInteractionRoute is the interaction route bound
// (protocol.BrowserInteraction.Valid).
const maxHandedOffInteractionRoute = 512

// handOffBrowserInteractions gives a sink the interactions a protocol
// recorded since the last handoff, their routes scrubbed like any other run
// evidence. A record the scrubbing lengthened past the record bound is split
// so the sink can keep all of it. Without such a sink they are dropped.
func handOffBrowserInteractions(p protocol.Protocol, sink ResultSink, config *RedactionConfig) {
	provider, ok := p.(protocol.BrowserInteractionEvidenceProvider)
	if !ok {
		return
	}
	evidence := takeBrowserInteractions(provider)
	interactionSink, ok := sink.(BrowserInteractionEvidenceSink)
	if !ok {
		if len(evidence) > 0 {
			slog.Warn("browser interaction evidence has no run archive to go to and was dropped", "records", len(evidence))
		}
		return
	}
	for _, record := range evidence {
		if record == nil {
			continue
		}
		for i := range record.Interactions {
			route := scrubSensitiveTextWithConfig(record.Interactions[i].Route, config)
			if route != "" && (!strings.HasPrefix(route, "/") || strings.ContainsAny(route, "?#") || len(route) > maxHandedOffInteractionRoute) {
				// Scrubbing left no path (detail withheld), or lengthened it
				// past the route bound: record none, so the record stays valid.
				route = ""
			}
			record.Interactions[i].Route = route
		}
		for _, part := range protocol.SplitBrowserInteractionEvidence(record) {
			if err := interactionSink.RecordBrowserInteractionEvidence(part); err != nil {
				slog.Warn("browser interaction evidence was not retained in the run archive", "generation", part.SourceGeneration, "err", err)
			}
		}
	}
}

func takeBrowserInteractions(provider protocol.BrowserInteractionEvidenceProvider) (out []*protocol.BrowserInteractionEvidence) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return provider.TakeBrowserInteractionEvidence()
}

// handOffBrowserSessionEvidence hands over everything a closed browser
// session produced: its HARs and its interactions.
func handOffBrowserSessionEvidence(p protocol.Protocol, sink ResultSink, config *RedactionConfig) {
	handOffBrowserHARArtifacts(p, sink)
	handOffBrowserInteractions(p, sink, config)
}

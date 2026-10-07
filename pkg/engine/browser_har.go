package engine

import (
	"log/slog"

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

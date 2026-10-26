package recordbridge

import (
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayHARRequestKey struct {
	generation, request string
	hop                 int
}
type replayHARPosition struct{ event, entry int }

func replayHARNetwork(event engine.ReplayActionEvent) ([]protocol.NetworkEntry, string) {
	if len(event.Network) > 0 {
		return event.Network, event.PageURL
	}
	if event.Snapshot != nil {
		pageURL := event.PageURL
		if pageURL == "" {
			pageURL = event.Snapshot.PageURL
		}
		return event.Snapshot.NetworkEntries, pageURL
	}
	return nil, event.PageURL
}

func replayHARIdentity(entry protocol.NetworkEntry) (replayHARRequestKey, bool) {
	e := entry.BrowserEvidence
	if e == nil || e.SourceGeneration == "" || e.RequestID == "" {
		return replayHARRequestKey{}, false
	}
	return replayHARRequestKey{e.SourceGeneration, e.RequestID, e.RedirectHop}, true
}

func WriteReplaySyntheticHAR(events []engine.ReplayActionEvent, out io.Writer) (harsynth.Summary, error) {
	// Snapshots overlap. Keep the latest observation for measured identities so
	// a later completion replaces an earlier in-flight observation. Equal URLs
	// alone never prove duplication. Bound identity bookkeeping to the HAR limit.
	latest := make(map[replayHARRequestKey]replayHARPosition)
	for ei, event := range events {
		entries, _ := replayHARNetwork(event)
		for ni, entry := range entries {
			if strings.HasSuffix(entry.ResourceType, "(cache)") {
				continue
			}
			if key, ok := replayHARIdentity(entry); ok {
				if _, exists := latest[key]; exists || len(latest) < harsynth.MaxEntries {
					latest[key] = replayHARPosition{ei, ni}
				}
			}
		}
	}
	writer := harsynth.NewWriter(out, harsynth.Metadata{CaptureMode: harsynth.CaptureReplayNetworkEntries})
	for ei, event := range events {
		entries, pageURL := replayHARNetwork(event)
		for ni, entry := range entries {
			if strings.HasSuffix(entry.ResourceType, "(cache)") {
				writer.Omit("cache_hit")
				continue
			}
			if key, ok := replayHARIdentity(entry); ok {
				if position, tracked := latest[key]; tracked && position != (replayHARPosition{ei, ni}) {
					continue
				}
			}
			if err := writer.Add(harsynth.Entry{RecordTime: event.StartedAt, ActionName: event.ActionName, PageURL: pageURL, Network: entry}); err != nil {
				return harsynth.Summary{}, err
			}
		}
	}
	return writer.Close()
}

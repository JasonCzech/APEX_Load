package recordbridge

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/harsynth"
	"io"
)

func WriteReplaySyntheticHAR(events []engine.ReplayActionEvent, out io.Writer) (harsynth.Summary, error) {
	writer := harsynth.NewWriter(out, harsynth.Metadata{CaptureMode: harsynth.CaptureReplayNetworkEntries})
	for _, event := range events {
		entries, pageURL := event.Network, event.PageURL
		if len(entries) == 0 && event.Snapshot != nil {
			entries = event.Snapshot.NetworkEntries
			if pageURL == "" {
				pageURL = event.Snapshot.PageURL
			}
		}
		for _, entry := range entries {
			if err := writer.Add(harsynth.Entry{RecordTime: event.StartedAt, ActionName: event.ActionName, PageURL: pageURL, Network: entry}); err != nil {
				return harsynth.Summary{}, err
			}
		}
	}
	return writer.Close()
}

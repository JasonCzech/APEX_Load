package generatorrpc

import (
	"encoding/json"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func snapshotJSON(snapshot *protocol.ErrorSnapshot) string {
	const maxMetricsSnapshotJSONBytes = 32 << 10
	if snapshot == nil {
		return ""
	}
	data, err := json.Marshal(engine.BoundErrorSnapshot(snapshot, maxMetricsSnapshotJSONBytes))
	if err != nil {
		return ""
	}
	if len(data) > maxMetricsSnapshotJSONBytes {
		truncated := &protocol.ErrorSnapshot{
			OmittedResources: max(0, snapshot.OmittedResources) + len(snapshot.Resources),
			RequestMethod:    truncateSnapshotField(snapshot.RequestMethod, 256),
			RequestURL:       truncateSnapshotField(snapshot.RequestURL, 2048),
			ResponseStatus:   snapshot.ResponseStatus,
			ResponseBody:     "[snapshot truncated: exceeded metrics transport limit]",
			PageURL:          truncateSnapshotField(snapshot.PageURL, 2048),
			PageTitle:        truncateSnapshotField(snapshot.PageTitle, 1024),
		}
		data, err = json.Marshal(truncated)
		if err != nil {
			return ""
		}
	}
	return string(data)
}

func truncateSnapshotField(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	return boundedtext.TruncateUTF8(value, maxBytes)
}

func resultStartTimeUnixMs(startTime time.Time) int64 {
	if startTime.IsZero() {
		return 0
	}
	return startTime.UnixMilli()
}

func cloneErrorSnapshot(snapshot *protocol.ErrorSnapshot) *protocol.ErrorSnapshot {
	return engine.BoundErrorSnapshot(snapshot, 32<<10)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func mergeMetricsTelemetry(values map[string]MetricsTelemetry) *MetricsTelemetry {
	if len(values) == 0 {
		return nil
	}
	combined := &MetricsTelemetry{}
	hasCollectedAt := false
	hasPublishedAt := false
	for _, value := range values {
		combined.DroppedResults += value.DroppedResults
		combined.SheddedArrivals += value.SheddedArrivals
		if value.CollectedAtUnixMs > 0 && (!hasCollectedAt || value.CollectedAtUnixMs < combined.CollectedAtUnixMs) {
			combined.CollectedAtUnixMs = value.CollectedAtUnixMs
			hasCollectedAt = true
		}
		if value.PublishedAtUnixMs > 0 && (!hasPublishedAt || value.PublishedAtUnixMs > combined.PublishedAtUnixMs) {
			combined.PublishedAtUnixMs = value.PublishedAtUnixMs
			hasPublishedAt = true
		}
		if value.SampleAgeMs > combined.SampleAgeMs {
			combined.SampleAgeMs = value.SampleAgeMs
		}
		if value.GeneratorProcessingLagMs > combined.GeneratorProcessingLagMs {
			combined.GeneratorProcessingLagMs = value.GeneratorProcessingLagMs
		}
		if value.TransportLagMs > combined.TransportLagMs {
			combined.TransportLagMs = value.TransportLagMs
		}
		if value.ControllerReceivedAtUnixMs > combined.ControllerReceivedAtUnixMs {
			combined.ControllerReceivedAtUnixMs = value.ControllerReceivedAtUnixMs
		}
		if value.DroppedSubscriberBatches > combined.DroppedSubscriberBatches {
			combined.DroppedSubscriberBatches = value.DroppedSubscriberBatches
		}
		// Every group of a test reads the same per-test count.
		if value.TestDroppedSubscriberBatchesReported {
			combined.TestDroppedSubscriberBatchesReported = true
			combined.TestDroppedSubscriberBatches = max(combined.TestDroppedSubscriberBatches, value.TestDroppedSubscriberBatches)
		}
		// Groups on one pod publish on the same cadence, so the max per-group
		// window is the representative batch window.
		if value.WindowMs > combined.WindowMs {
			combined.WindowMs = value.WindowMs
		}
	}
	return combined
}

func cloneServerTelemetry(input *protocol.ServerTelemetry) *ServerTelemetry {
	if input == nil {
		return nil
	}
	return &ServerTelemetry{
		SubscriptionID:            input.SubscriptionID,
		AcknowledgementMs:         cloneMessagingAck(input.AcknowledgementMs),
		Messaging:                 input.Messaging.Clone(),
		GRPC:                      input.GRPC.Clone(),
		WebSocket:                 input.WebSocket.Clone(),
		ServerProcessingMs:        input.ServerProcessingMs,
		TransferMs:                input.TransferMs,
		RequestPayloadKB:          input.RequestPayloadKB,
		ResponsePayloadKB:         input.ResponsePayloadKB,
		ConnectionReused:          input.ConnectionReused,
		RequestSizeClass:          input.RequestSizeClass,
		ResponseSizeClass:         input.ResponseSizeClass,
		FirstEventMs:              input.FirstEventMs,
		EventCount:                int32(input.EventCount),
		MessageLatencyMs:          input.MessageLatencyMs,
		MessagesSent:              int32(input.MessagesSent),
		MessagesReceived:          int32(input.MessagesReceived),
		MessagesSentPerSec:        input.MessagesSentPerSec,
		MessagesReceivedPerSec:    input.MessagesReceivedPerSec,
		ConsumerLagMs:             input.ConsumerLagMs,
		ConsumerLagObserved:       input.ConsumerLagObserved,
		ConsumerOffsetLagObserved: input.ConsumerOffsetLagObserved,
		SendFailureCount:          int32(input.SendFailureCount),
		RedeliveryCount:           int32(input.RedeliveryCount),
		DeadLetteredCount:         int32(input.DeadLetteredCount),
		DuplicateCount:            int32(input.DuplicateCount),
		OutOfOrderCount:           int32(input.OutOfOrderCount),
		CommitCount:               int32(input.CommitCount),
		AbortCount:                int32(input.AbortCount),
		ConsumerOffsetLag:         input.ConsumerOffsetLag,
		ThrottledRequests:         int32(input.ThrottledRequests),
		ServerBusyCount:           int32(input.ServerBusyCount),
		QuotaExceededCount:        int32(input.QuotaExceededCount),
		EventTooLargeCount:        int32(input.EventTooLargeCount),
		EndToEndLatencyMs:         input.EndToEndLatencyMs,
		PartitionID:               input.PartitionID,
	}
}

package engine

import (
	"strings"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	maxMetricDimensionEntries = 512
	maxMetricLabelBytes       = 512
	maxMetricErrorBytes       = 4 << 10
	maxMetricsSnapshotBytes   = 64 << 20
	maxMetricsBatchSize       = 10_000
	metricOverflowLabel       = "__other__"
	// maxGroupTransactionEntries bounds a window's group-transaction pairs
	// across all groups; past it a group's new transactions fold into its
	// overflow entry.
	maxGroupTransactionEntries = 2048
)

func compactMetricsResult(input *protocol.Result) *protocol.Result {
	if input == nil {
		return nil
	}
	out := *input
	out.ActionName = boundedMetricString(input.ActionName, maxMetricLabelBytes)
	out.SampleKind = boundedMetricString(input.SampleKind, maxMetricLabelBytes)
	out.ParentActionName = boundedMetricString(input.ParentActionName, maxMetricLabelBytes)
	out.Group = boundedMetricString(input.Group, maxMetricLabelBytes)
	out.Region = boundedMetricString(input.Region, maxMetricLabelBytes)
	out.CorrelationID = boundedMetricString(input.CorrelationID, maxMetricLabelBytes)
	out.NetworkProfile = boundedMetricString(input.NetworkProfile, maxMetricLabelBytes)
	out.DependencyType = boundedMetricString(input.DependencyType, maxMetricLabelBytes)
	out.DependencyTarget = boundedMetricString(input.DependencyTarget, maxMetricLabelBytes)
	out.ErrorMessage = boundedMetricString(input.ErrorMessage, maxMetricErrorBytes)
	out.ResponseBody = ""
	out.PageURL = ""
	out.PageTitle = ""
	out.HTML = ""
	out.ScreenshotBase64 = ""
	out.ConsoleEntries = nil
	out.NetworkEntries = nil
	out.DOMSummary = nil
	out.ProtocolEvidence = nil
	out.Snapshot = nil
	out.SourceStepID = ""
	out.SourceAction = ""
	out.SourceActionName = ""
	out.SourceTransaction = ""
	out.SourceProfile = ""
	out.SourcePath = ""
	out.Phase = boundedMetricString(input.Phase, maxMetricLabelBytes)
	out.Hierarchy = boundedMetricString(input.Hierarchy, maxMetricLabelBytes)
	out.SourceUID = ""
	out.RequestType = ""
	out.OperationKind = ""
	out.Extractions = nil
	if operation := strings.TrimSpace(input.Extractions["soap_operation"]); operation != "" {
		out.Extractions = map[string]string{"soap_operation": boundedMetricString(operation, maxMetricLabelBytes)}
	}
	if input.ClientMetrics != nil {
		client := *input.ClientMetrics
		client.DocumentEvidence = nil
		client.RuntimeAvailability = input.ClientMetrics.RuntimeAvailability.Clone()
		client.DeviceProfile = boundedMetricString(client.DeviceProfile, maxMetricLabelBytes)
		client.DevicePlatform = boundedMetricString(client.DevicePlatform, maxMetricLabelBytes)
		client.DeviceTier = boundedMetricString(client.DeviceTier, maxMetricLabelBytes)
		client.BrowserEngine = boundedMetricString(client.BrowserEngine, maxMetricLabelBytes)
		out.ClientMetrics = &client
	}
	if input.ServerTelemetry != nil {
		server := *input.ServerTelemetry
		if !server.WebSocket.Valid() {
			server.WebSocket = nil
		}
		server.GRPC = server.GRPC.Clone()
		server.WebSocket = server.WebSocket.Clone()
		if server.Messaging != nil && !server.Messaging.Valid() {
			server.Messaging = &protocol.MessagingDiagnostics{}
		} else {
			server.Messaging = server.Messaging.Clone()
		}
		if !server.GRPC.Valid() {
			server.GRPC = nil
		}
		server.SubscriptionID = boundedMetricString(server.SubscriptionID, maxMetricLabelBytes)
		if server.AcknowledgementMs != nil {
			v := *server.AcknowledgementMs
			server.AcknowledgementMs = &v
		}
		server.PartitionID = boundedMetricString(server.PartitionID, maxMetricLabelBytes)
		server.RequestSizeClass = boundedMetricString(server.RequestSizeClass, maxMetricLabelBytes)
		server.ResponseSizeClass = boundedMetricString(server.ResponseSizeClass, maxMetricLabelBytes)
		out.ServerTelemetry = &server
	}
	return &out
}

func metricActionDimension(result *protocol.Result) string {
	if result == nil {
		return ""
	}
	name := strings.TrimSpace(result.ActionName)
	switch strings.ToLower(strings.TrimSpace(result.SampleKind)) {
	case "child":
		return "child:" + name
	case "aggregate":
		return "aggregate:" + name
	default:
		return name
	}
}

func boundedMetricString(value string, maxBytes int) string {
	if maxBytes <= 0 || value == "" {
		return ""
	}
	return boundedtext.TruncateUTF8(value, maxBytes)
}

func boundedDimensionKey[V any](raw string, values map[string]V) string {
	key := boundedMetricString(strings.TrimSpace(raw), maxMetricLabelBytes)
	if key == "" {
		key = "unnamed"
	}
	if _, ok := values[key]; ok || len(values) < maxMetricDimensionEntries-1 {
		return key
	}
	return metricOverflowLabel
}

func boundExternalMetrics(values map[string]float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]float64, min(len(values), maxMetricDimensionEntries))
	for _, key := range boundedtext.SmallestStringKeys(values, maxMetricDimensionEntries) {
		value := values[key]
		key = boundedDimensionKey(key, out)
		out[key] += value
	}
	return out
}

// durationDistributionBytes estimates the memory a window's duration
// distributions retain.
func durationDistributionBytes(digest *TDigestSnapshot, sketch *DurationSketchSnapshot) int {
	bytes := 0
	if digest != nil {
		bytes += 80 + len(digest.Centroids)*16
	}
	if sketch != nil {
		bytes += 64 + len(sketch.Bins)*16
	}
	return bytes
}

func estimateMetricsSnapshotBytes(snapshot MetricsSnapshot) int {
	bytes := 1024
	if documents := snapshot.BrowserDocuments; documents != nil {
		bytes += 256 + len(documents.Metrics)*4096
	}
	estimateActions := func(values map[string]*ActionMetrics) {
		for key, value := range values {
			bytes += len(key) + 384
			if value == nil {
				continue
			}
			bytes += durationDistributionBytes(value.DurationDigest, value.DurationSketch)
			bytes += len(value.Name) + len(value.DependencyType) + len(value.DependencyTarget) + len(value.LastErrorMessage)
			for _, id := range value.CorrelationIDs {
				bytes += len(id) + 16
			}
			if telemetry := value.ServerTelemetry; telemetry != nil {
				bytes += 512
				if telemetry.WebSocket != nil {
					bytes += int(unsafe.Sizeof(*telemetry.WebSocket))
				}
				if telemetry.GRPC != nil {
					// The distributions and status buckets are inline arrays, so
					// the pointed-to struct size accounts for the full allocation.
					bytes += int(unsafe.Sizeof(*telemetry.GRPC))
				}
				if telemetry.Messaging != nil {
					bytes += int(unsafe.Sizeof(*telemetry.Messaging)) + cap(telemetry.Messaging.Dimensions)*int(unsafe.Sizeof(protocol.MessagingDimension{}))
					for _, d := range telemetry.Messaging.Dimensions {
						bytes += len(d.Protocol) + len(d.Group) + len(d.Source) + len(d.Partition) + len(d.Subscription)
						if e := d.Evidence; e != nil {
							bytes += int(unsafe.Sizeof(*e)) + cap(e.Durations)*int(unsafe.Sizeof(protocol.MessagingDurationSummary{})) + cap(e.Counters)*int(unsafe.Sizeof(protocol.MessagingCounterSummary{}))
							for _, duration := range e.Durations {
								bytes += len(duration.Metric) + len(duration.Population)
								if duration.Unavailable != nil {
									bytes += 128
								}
								for reason := range duration.Unavailable {
									bytes += len(reason) + 80
								}
							}
							for _, counter := range e.Counters {
								bytes += len(counter.Metric) + len(counter.Population) + len(counter.Scope)
							}
						}
					}
				}
				for partition := range telemetry.PartitionEvents {
					bytes += len(partition) + 24
				}
			}
		}
	}
	estimateActions(snapshot.ByAction)
	estimateActions(snapshot.ByDimensions)
	if snapshot.Joint != nil {
		estimateActions(snapshot.Joint.Tuples)
		if snapshot.Joint.Other != nil {
			estimateActions(map[string]*ActionMetrics{"other": snapshot.Joint.Other})
		}
	}
	estimateActions(snapshot.ByActionProperty)
	estimateActions(snapshot.ByProperty)
	estimateActions(snapshot.ByTransaction)
	estimateActions(snapshot.ByDependencyTarget)
	estimateActions(snapshot.BySOAPOperation)
	estimateActions(snapshot.ByNetworkProfile)
	estimateActions(snapshot.ByRegion)
	for key, value := range snapshot.ByGroup {
		bytes += len(key) + 256
		if value != nil {
			bytes += durationDistributionBytes(value.DurationDigest, value.DurationSketch)
		}
	}
	for key, transactions := range snapshot.ByGroupTransaction {
		bytes += len(key) + 48
		estimateActions(transactions)
	}
	for key := range snapshot.BrowserByDevice {
		bytes += len(key) + 256
	}
	for _, row := range snapshot.TransactionResponseTimeSLA {
		bytes += len(row.Group) + len(row.Transaction) + 96
	}
	for key := range snapshot.ErrorsByClass {
		bytes += len(key) + 24
	}
	if progress := snapshot.MessagingProgress; progress != nil {
		bytes += int(unsafe.Sizeof(*progress)) + cap(progress.Rows)*int(unsafe.Sizeof(protocol.MessagingProgressRow{}))
		bytes += len(progress.Lifecycle) + len(progress.UnavailableReason)
		for _, row := range progress.Rows {
			bytes += len(row.Origin) + len(row.Group) + len(row.Protocol) + len(row.Mode) + len(row.SourceID) + len(row.Source) + len(row.Subscription) + len(row.SessionID) + len(row.Partition) + len(row.Generation) + len(row.Revision) + len(row.CaptureRevision) + len(row.Ownership)
			if row.OwnershipVerifiedAgeMs != nil {
				bytes += 8
			}
			for _, position := range []protocol.MessagingProgressPosition{row.DeliveredPosition, row.CheckpointPosition, row.TailPosition} {
				bytes += len(position.Sequence) + len(position.ProviderOffset) + len(position.State)
				if position.ObservationAgeMs != nil {
					bytes += 8
				}
			}
			bytes += len(row.Checkpoint.LastOutcome) + len(row.Checkpoint.Duration.Metric) + len(row.Checkpoint.Duration.Population)
			if row.Checkpoint.Duration.Unavailable != nil {
				bytes += 128
			}
			for reason := range row.Checkpoint.Duration.Unavailable {
				bytes += len(reason) + 80
			}
		}
	}
	if snapshot.Resources != nil {
		bytes += 1024
		for key := range snapshot.Resources.External {
			bytes += len(key) + 24
		}
	}
	return bytes
}

package engine

import (
	"strings"

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
	out.SourceUID = ""
	out.RequestType = ""
	out.OperationKind = ""
	out.Extractions = nil
	if operation := strings.TrimSpace(input.Extractions["soap_operation"]); operation != "" {
		out.Extractions = map[string]string{"soap_operation": boundedMetricString(operation, maxMetricLabelBytes)}
	}
	if input.ClientMetrics != nil {
		client := *input.ClientMetrics
		client.DeviceProfile = boundedMetricString(client.DeviceProfile, maxMetricLabelBytes)
		client.DevicePlatform = boundedMetricString(client.DevicePlatform, maxMetricLabelBytes)
		client.DeviceTier = boundedMetricString(client.DeviceTier, maxMetricLabelBytes)
		client.BrowserEngine = boundedMetricString(client.BrowserEngine, maxMetricLabelBytes)
		out.ClientMetrics = &client
	}
	if input.ServerTelemetry != nil {
		server := *input.ServerTelemetry
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

func estimateMetricsSnapshotBytes(snapshot MetricsSnapshot) int {
	bytes := 1024
	estimateActions := func(values map[string]*ActionMetrics) {
		for key, value := range values {
			bytes += len(key) + 384
			if value == nil {
				continue
			}
			bytes += len(value.Name) + len(value.DependencyType) + len(value.DependencyTarget) + len(value.LastErrorMessage)
			for _, id := range value.CorrelationIDs {
				bytes += len(id) + 16
			}
			if telemetry := value.ServerTelemetry; telemetry != nil {
				bytes += 512
				for partition := range telemetry.PartitionEvents {
					bytes += len(partition) + 24
				}
			}
		}
	}
	estimateActions(snapshot.ByAction)
	estimateActions(snapshot.ByTransaction)
	estimateActions(snapshot.ByDependencyTarget)
	estimateActions(snapshot.BySOAPOperation)
	estimateActions(snapshot.ByNetworkProfile)
	for key := range snapshot.ByGroup {
		bytes += len(key) + 256
	}
	for key := range snapshot.BrowserByDevice {
		bytes += len(key) + 256
	}
	for key := range snapshot.ErrorsByClass {
		bytes += len(key) + 24
	}
	if snapshot.Resources != nil {
		bytes += 1024
		for key := range snapshot.Resources.External {
			bytes += len(key) + 24
		}
	}
	return bytes
}

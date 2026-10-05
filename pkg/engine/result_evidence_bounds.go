package engine

import (
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	maxRetainedResultEvidenceBytes = 4 << 20
	maxResultExtractions           = protocol.MaxExtractionEntries
	maxResultExtractionBytes       = protocol.MaxExtractionBytes
	maxResultErrorBytes            = 16 << 10
	maxResultMetadataBytes         = 512
)

// boundResultEvidence detaches and bounds every rich evidence field before a
// result enters metrics, replay, or archive sinks. Protocol execution may still
// inspect its full local response, but asynchronous consumers never retain the
// original payload graph.
func boundResultEvidence(result *protocol.Result) {
	if result == nil {
		return
	}
	boundResultMetadata(result)
	projection, _ := boundedErrorSnapshot(&protocol.ErrorSnapshot{
		ResponseBody:     result.ResponseBody,
		PageURL:          result.PageURL,
		PageTitle:        result.PageTitle,
		HTML:             result.HTML,
		ScreenshotBase64: result.ScreenshotBase64,
		ConsoleEntries:   result.ConsoleEntries,
		NetworkEntries:   result.NetworkEntries,
		DOMSummary:       result.DOMSummary,
	}, maxRetainedResultEvidenceBytes)
	if projection != nil {
		result.ResponseBody = projection.ResponseBody
		result.PageURL = projection.PageURL
		result.PageTitle = projection.PageTitle
		result.HTML = projection.HTML
		result.ScreenshotBase64 = projection.ScreenshotBase64
		result.ConsoleEntries = projection.ConsoleEntries
		result.NetworkEntries = projection.NetworkEntries
		result.DOMSummary = projection.DOMSummary
	} else {
		result.ResponseBody = ""
		result.PageURL = ""
		result.PageTitle = ""
		result.HTML = ""
		result.ScreenshotBase64 = ""
		result.ConsoleEntries = nil
		result.NetworkEntries = nil
		result.DOMSummary = nil
	}
	result.ErrorMessage = cloneBoundedResultString(result.ErrorMessage, maxResultErrorBytes)
	boundedExtractions := boundedResultStringMap(result.Extractions, maxResultExtractions, maxResultExtractionBytes)
	if len(boundedExtractions) != len(result.Extractions) {
		result.ExtractionsIncomplete = true
	}
	for k, v := range result.Extractions {
		if boundedExtractions[k] != v {
			result.ExtractionsIncomplete = true
		}
	}
	result.Extractions = boundedExtractions
	result.ProtocolEvidence = protocol.BoundProtocolEvidence(result.ProtocolEvidence)
	result.Snapshot, _ = boundedErrorSnapshot(result.Snapshot, defaultErrorSnapshotMaxBytes)
}

func boundResultMetadata(result *protocol.Result) {
	result.ActionName = cloneBoundedResultString(result.ActionName, maxResultMetadataBytes)
	result.SampleKind = cloneBoundedResultString(result.SampleKind, maxResultMetadataBytes)
	result.ParentActionName = cloneBoundedResultString(result.ParentActionName, maxResultMetadataBytes)
	result.Group = cloneBoundedResultString(result.Group, maxResultMetadataBytes)
	result.Region = cloneBoundedResultString(result.Region, maxResultMetadataBytes)
	result.CorrelationID = cloneBoundedResultString(result.CorrelationID, maxResultMetadataBytes)
	result.NetworkProfile = cloneBoundedResultString(result.NetworkProfile, maxResultMetadataBytes)
	result.DependencyType = cloneBoundedResultString(result.DependencyType, maxResultMetadataBytes)
	result.DependencyTarget = cloneBoundedResultString(result.DependencyTarget, maxResultMetadataBytes)
	result.SourceStepID = cloneBoundedResultString(result.SourceStepID, maxResultMetadataBytes)
	result.SourceAction = cloneBoundedResultString(result.SourceAction, maxResultMetadataBytes)
	result.SourceActionName = cloneBoundedResultString(result.SourceActionName, maxResultMetadataBytes)
	result.SourceTransaction = cloneBoundedResultString(result.SourceTransaction, maxResultMetadataBytes)
	result.SourceProfile = cloneBoundedResultString(result.SourceProfile, maxResultMetadataBytes)
	result.SourcePath = cloneBoundedResultString(result.SourcePath, maxResultMetadataBytes)
	result.Phase = cloneBoundedResultString(result.Phase, maxResultMetadataBytes)
	result.Hierarchy = cloneBoundedResultString(result.Hierarchy, maxResultMetadataBytes)
	result.SourceUID = cloneBoundedResultString(result.SourceUID, maxResultMetadataBytes)
	result.RequestType = cloneBoundedResultString(result.RequestType, maxResultMetadataBytes)
	result.OperationKind = cloneBoundedResultString(result.OperationKind, maxResultMetadataBytes)
	if result.ClientMetrics != nil {
		result.ClientMetrics.DeviceProfile = cloneBoundedResultString(result.ClientMetrics.DeviceProfile, maxResultMetadataBytes)
		result.ClientMetrics.DevicePlatform = cloneBoundedResultString(result.ClientMetrics.DevicePlatform, maxResultMetadataBytes)
		result.ClientMetrics.DeviceTier = cloneBoundedResultString(result.ClientMetrics.DeviceTier, maxResultMetadataBytes)
		result.ClientMetrics.BrowserEngine = cloneBoundedResultString(result.ClientMetrics.BrowserEngine, maxResultMetadataBytes)
	}
	if result.ServerTelemetry != nil {
		result.ServerTelemetry.Messaging = result.ServerTelemetry.Messaging.Clone()
		if !result.ServerTelemetry.WebSocket.Valid() {
			result.ServerTelemetry.WebSocket = nil
		}
		if !result.ServerTelemetry.GRPC.Valid() {
			result.ServerTelemetry.GRPC = nil
		}
		result.ServerTelemetry.RequestSizeClass = cloneBoundedResultString(result.ServerTelemetry.RequestSizeClass, maxResultMetadataBytes)
		result.ServerTelemetry.ResponseSizeClass = cloneBoundedResultString(result.ServerTelemetry.ResponseSizeClass, maxResultMetadataBytes)
		result.ServerTelemetry.SOAPFaultCode = cloneBoundedResultString(result.ServerTelemetry.SOAPFaultCode, maxResultMetadataBytes)
		result.ServerTelemetry.SOAPFaultCategory = cloneBoundedResultString(result.ServerTelemetry.SOAPFaultCategory, maxResultMetadataBytes)
		result.ServerTelemetry.SOAPNamespaceConflict = cloneBoundedResultString(result.ServerTelemetry.SOAPNamespaceConflict, maxResultMetadataBytes)
		result.ServerTelemetry.SubscriptionID = cloneBoundedResultString(result.ServerTelemetry.SubscriptionID, maxResultMetadataBytes)
		result.ServerTelemetry.PartitionID = cloneBoundedResultString(result.ServerTelemetry.PartitionID, maxResultMetadataBytes)
	}
}

func boundedResultStringMap(values map[string]string, maxEntries, maxBytes int) map[string]string {
	if len(values) == 0 || maxEntries <= 0 || maxBytes <= 0 {
		return nil
	}
	out := make(map[string]string, min(len(values), maxEntries))
	remaining := maxBytes
	for _, originalKey := range boundedtext.SmallestStringKeys(values, maxEntries) {
		if len(out) >= maxEntries || remaining <= 0 {
			break
		}
		key := originalKey
		value := values[originalKey]
		key = cloneBoundedResultString(key, min(4<<10, remaining))
		remaining -= len(key)
		if key == "" || remaining <= 0 {
			continue
		}
		value = cloneBoundedResultString(value, min(16<<10, remaining))
		remaining -= len(value)
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneBoundedResultString(value string, limit int) string {
	if value == "" || limit <= 0 {
		return ""
	}
	return boundedtext.TruncateUTF8(value, limit)
}

// BoundErrorSnapshot is the generator/archive projection seam. It returns a
// detached snapshot that cannot exceed maxBytes (using the engine default when
// maxBytes is non-positive).
func BoundErrorSnapshot(snapshot *protocol.ErrorSnapshot, maxBytes int) *protocol.ErrorSnapshot {
	if maxBytes <= 0 {
		maxBytes = defaultErrorSnapshotMaxBytes
	}
	bounded, _ := boundedErrorSnapshot(snapshot, maxBytes)
	return bounded
}

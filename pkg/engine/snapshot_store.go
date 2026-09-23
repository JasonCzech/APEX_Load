package engine

import (
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	"maps"
)

const (
	defaultErrorSnapshotMaxBytes = 1 << 20
	defaultSnapshotStoreMaxBytes = 16 << 20
	snapshotBaseOverhead         = 256
	snapshotMapEntryOverhead     = 32
	snapshotConsoleEntryOverhead = 64
	snapshotNetworkEntryOverhead = 128
	snapshotDOMOverhead          = 64
	maxSnapshotMapEntries        = 256
)

const snapshotTruncatedMarker = "...[truncated]"

func newSnapshotStoreWithLimits(maxSize, maxSnapshotBytes, maxBytes int) *SnapshotStore {
	if maxSize < 0 {
		maxSize = 0
	}
	if maxSnapshotBytes < snapshotBaseOverhead {
		maxSnapshotBytes = snapshotBaseOverhead
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &SnapshotStore{
		snapshots:        make([]*protocol.ErrorSnapshot, 0),
		maxSize:          maxSize,
		maxSnapshotBytes: maxSnapshotBytes,
		maxBytes:         maxBytes,
	}
}

func boundedErrorSnapshot(snapshot *protocol.ErrorSnapshot, maxBytes int) (*protocol.ErrorSnapshot, int) {
	if snapshot == nil || maxBytes < snapshotBaseOverhead {
		return nil, 0
	}
	remaining := maxBytes - snapshotBaseOverhead
	largeFieldLimit := max(1, maxBytes/2)
	out := &protocol.ErrorSnapshot{ResponseStatus: snapshot.ResponseStatus}
	switch snapshot.ResponseBodyState {
	case "captured", "empty", "redacted", "not_captured", "expired":
		out.ResponseBodyState = snapshot.ResponseBodyState
	}
	out.RequestMethod = takeSnapshotString(snapshot.RequestMethod, &remaining, 64)
	out.RequestURL = takeSnapshotString(snapshot.RequestURL, &remaining, 8<<10)
	out.PageURL = takeSnapshotString(snapshot.PageURL, &remaining, 8<<10)
	out.PageTitle = takeSnapshotString(snapshot.PageTitle, &remaining, 2<<10)
	out.RequestHeaders = takeSnapshotStringMap(snapshot.RequestHeaders, &remaining)
	out.ResponseHeaders = takeSnapshotStringMap(snapshot.ResponseHeaders, &remaining)
	// Preserve response evidence before request payloads: for failed assertions
	// it is usually the most useful part of the snapshot. Each large field is
	// independently capped so one body cannot crowd out all other evidence.
	out.ResponseBody = takeSnapshotString(snapshot.ResponseBody, &remaining, largeFieldLimit)
	out.RequestBody = takeSnapshotString(snapshot.RequestBody, &remaining, max(1, maxBytes/4))
	out.HTML = takeSnapshotString(snapshot.HTML, &remaining, largeFieldLimit)
	out.ScreenshotBase64 = takeSnapshotString(snapshot.ScreenshotBase64, &remaining, largeFieldLimit)
	out.ConsoleEntries = takeSnapshotConsoleEntries(snapshot.ConsoleEntries, &remaining)
	out.NetworkEntries = takeSnapshotNetworkEntries(snapshot.NetworkEntries, &remaining)
	out.DOMSummary = takeSnapshotDOMSummary(snapshot.DOMSummary, &remaining)
	out.OmittedResources = max(0, snapshot.OmittedResources)
	for i, resource := range snapshot.Resources {
		// Reserve body-state metadata even when variable fields exhaust the envelope.
		const resourceOverhead = snapshotNetworkEntryOverhead + 32
		if i >= 80 || remaining < resourceOverhead {
			out.OmittedResources += len(snapshot.Resources) - i
			break
		}
		remaining -= resourceOverhead
		r := protocol.ResourceSnapshot{ResponseStatus: resource.ResponseStatus, CacheHit: resource.CacheHit, Truncated: resource.Truncated}
		r.RequestURL = takeSnapshotString(resource.RequestURL, &remaining, 8<<10)
		r.FailureKind = takeSnapshotString(resource.FailureKind, &remaining, 64)
		switch resource.ResponseBodyState {
		case "captured", "empty", "redacted", "not_captured", "expired":
			r.ResponseBodyState = resource.ResponseBodyState
		default:
			r.ResponseBodyState = "not_captured"
		}
		r.RequestHeaders = takeSnapshotStringMap(resource.RequestHeaders, &remaining)
		r.ResponseHeaders = takeSnapshotStringMap(resource.ResponseHeaders, &remaining)
		r.ResponseBody = takeSnapshotString(resource.ResponseBody, &remaining, 32<<10)
		r.Truncated = r.Truncated || r.ResponseBody != resource.ResponseBody || r.RequestURL != resource.RequestURL || r.FailureKind != resource.FailureKind || !maps.Equal(r.RequestHeaders, resource.RequestHeaders) || !maps.Equal(r.ResponseHeaders, resource.ResponseHeaders)
		if resource.ResponseBody != "" && r.ResponseBody == "" {
			r.ResponseBodyState = "not_captured"
		}
		out.Resources = append(out.Resources, r)
	}
	return out, maxBytes - remaining
}

func takeSnapshotString(value string, remaining *int, fieldLimit int) string {
	if value == "" || remaining == nil || *remaining <= 0 || fieldLimit <= 0 {
		return ""
	}
	limit := min(*remaining, fieldLimit)
	if len(value) <= limit {
		bounded := boundedtext.TruncateUTF8(value, limit)
		*remaining -= len(bounded)
		return bounded
	}
	if limit <= len(snapshotTruncatedMarker) {
		truncated := boundedtext.TruncateUTF8(value, limit)
		*remaining -= len(truncated)
		return truncated
	}
	truncated := boundedtext.TruncateUTF8(value, limit-len(snapshotTruncatedMarker)) + snapshotTruncatedMarker
	*remaining -= len(truncated)
	return truncated
}

func takeSnapshotStringMap(values map[string]string, remaining *int) map[string]string {
	if len(values) == 0 || remaining == nil {
		return nil
	}
	out := make(map[string]string)
	for _, originalKey := range boundedtext.SmallestStringKeys(values, maxSnapshotMapEntries) {
		if *remaining <= snapshotMapEntryOverhead {
			break
		}
		value := values[originalKey]
		*remaining -= snapshotMapEntryOverhead
		boundedKey := takeSnapshotString(originalKey, remaining, 4<<10)
		if boundedKey == "" {
			break
		}
		out[boundedKey] = takeSnapshotString(value, remaining, 16<<10)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func takeSnapshotConsoleEntries(entries []protocol.ConsoleEntry, remaining *int) []protocol.ConsoleEntry {
	if len(entries) == 0 || remaining == nil {
		return nil
	}
	out := make([]protocol.ConsoleEntry, 0, min(len(entries), 64))
	for _, entry := range entries {
		if *remaining <= snapshotConsoleEntryOverhead {
			break
		}
		*remaining -= snapshotConsoleEntryOverhead
		out = append(out, protocol.ConsoleEntry{
			Level:     takeSnapshotString(entry.Level, remaining, 64),
			Text:      takeSnapshotString(entry.Text, remaining, 16<<10),
			Source:    takeSnapshotString(entry.Source, remaining, 4<<10),
			Timestamp: entry.Timestamp,
		})
	}
	return out
}

func takeSnapshotNetworkEntries(entries []protocol.NetworkEntry, remaining *int) []protocol.NetworkEntry {
	if len(entries) == 0 || remaining == nil {
		return nil
	}
	out := make([]protocol.NetworkEntry, 0, min(len(entries), 64))
	for _, entry := range entries {
		if *remaining <= snapshotNetworkEntryOverhead {
			break
		}
		*remaining -= snapshotNetworkEntryOverhead
		out = append(out, protocol.NetworkEntry{
			URL:                  takeSnapshotString(entry.URL, remaining, 8<<10),
			Method:               takeSnapshotString(entry.Method, remaining, 64),
			StatusCode:           entry.StatusCode,
			ResourceType:         takeSnapshotString(entry.ResourceType, remaining, 128),
			DurationMs:           entry.DurationMs,
			BytesReceived:        entry.BytesReceived,
			RequestHeaders:       takeSnapshotStringMap(entry.RequestHeaders, remaining),
			RequestBody:          takeSnapshotString(entry.RequestBody, remaining, 16<<10),
			RequestBodyEncoding:  takeSnapshotString(entry.RequestBodyEncoding, remaining, 64),
			RequestBodyTruncated: entry.RequestBodyTruncated,
			ResponseHeaders:      takeSnapshotStringMap(entry.ResponseHeaders, remaining),
		})
	}
	return out
}

func takeSnapshotDOMSummary(summary *protocol.DOMSummary, remaining *int) *protocol.DOMSummary {
	if summary == nil || remaining == nil || *remaining <= snapshotDOMOverhead {
		return nil
	}
	*remaining -= snapshotDOMOverhead
	out := &protocol.DOMSummary{
		Title:      takeSnapshotString(summary.Title, remaining, 2<<10),
		ReadyState: takeSnapshotString(summary.ReadyState, remaining, 64),
	}
	for _, text := range summary.VisibleText {
		if *remaining <= snapshotMapEntryOverhead {
			break
		}
		*remaining -= snapshotMapEntryOverhead
		out.VisibleText = append(out.VisibleText, takeSnapshotString(text, remaining, 8<<10))
	}
	return out
}

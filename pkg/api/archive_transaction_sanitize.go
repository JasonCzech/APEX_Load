package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

type sanitizedTransactionStats struct {
	Count     int64
	SizeBytes int64
	SHA256    string
}

type archiveCountingWriter struct {
	written int64
}

func (w *archiveCountingWriter) Write(data []byte) (int, error) {
	w.written += int64(len(data))
	return len(data), nil
}

// writeSanitizedArchiveTransactions stream-decodes and rewrites one JSONL
// record at a time. The query budget bounds total work and Scanner bounds an
// individual record; no map of the archive payload is retained in memory.
func writeSanitizedArchiveTransactions(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, destination io.Writer) (sanitizedTransactionStats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if record == nil || manifest == nil {
		return sanitizedTransactionStats{}, fmt.Errorf("archive record and manifest are required")
	}
	if destination == nil {
		destination = io.Discard
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return sanitizedTransactionStats{}, err
	}
	defer file.Close()

	hash := sha256.New()
	counter := &archiveCountingWriter{}
	encoder := json.NewEncoder(io.MultiWriter(destination, hash, counter))
	scanner := newArchiveJSONLScanner(file)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	var count int64
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return sanitizedTransactionStats{}, err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var item TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return sanitizedTransactionStats{}, fmt.Errorf("decode archived transaction %d: %w", count+1, err)
		}
		sanitizeArchivedTransaction(&item)
		if err := encoder.Encode(&item); err != nil {
			return sanitizedTransactionStats{}, fmt.Errorf("encode sanitized transaction %d: %w", count+1, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return sanitizedTransactionStats{}, fmt.Errorf("scan archived transactions: %w", err)
	}
	return sanitizedTransactionStats{
		Count:     count,
		SizeBytes: counter.written,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func sanitizeArchiveDecodedRecord(value any) {
	sanitizeArchiveDecodedRecordAt(value, 0)
}

func sanitizeArchiveDecodedRecordAt(value any, ordinal int64) {
	if item, ok := value.(*TransactionEvent); ok {
		sanitizeArchivedTransaction(item)
		finalizeTransactionEventDerivedFields(item, ordinal)
	}
}

func sanitizeArchivedTransaction(item *TransactionEvent) {
	if item == nil {
		return
	}
	policy := scrub.Policy{}
	item.TestName = scrub.ValueLabel(item.TestName, policy)
	// Group and proven business-transaction names are authored identities used as
	// report aggregation keys. They must remain visible exactly as configured.
	// Ordinary action names retain the full defense-in-depth scrub for direct and
	// legacy archive input.
	item.Region = scrub.ValueLabel(item.Region, policy)
	item.Hierarchy = scrub.ValueLabel(item.Hierarchy, policy)
	item.PageName = scrub.ValueLabel(item.PageName, policy)
	item.Phase = scrub.ValueLabel(item.Phase, policy)
	if item.BusinessTransaction {
		// Preserve the authored transaction name.
	} else {
		item.ActionName = scrub.ValueLabel(item.ActionName, policy)
	}
	item.CorrelationID = scrub.ValueLabel(item.CorrelationID, policy)
	item.TraceID = scrub.ValueLabel(item.TraceID, policy)
	item.SpanID = scrub.ValueLabel(item.SpanID, policy)
	item.NetworkProfile = scrub.ValueLabel(item.NetworkProfile, policy)
	item.DependencyType = scrub.ValueLabel(item.DependencyType, policy)
	item.DependencyTarget = scrubArchiveTarget(item.DependencyTarget, policy)
	item.ErrorMessage = scrub.ValueLabel(item.ErrorMessage, policy)
	item.Extractions = sanitizeArchivedStringMap(item.Extractions, policy)
	item.PageURL = scrub.ValueURL(item.PageURL, policy)
	item.PageTitle = scrub.ValueLabel(item.PageTitle, policy)
	item.ConsoleEntries = sanitizeArchivedConsoleEntries(item.ConsoleEntries, policy)
	item.NetworkEntries = sanitizeArchivedNetworkEntries(item.NetworkEntries, policy)
	item.DOMSummary = sanitizeArchivedDOMSummary(item.DOMSummary, policy)
	if structured, ok := sanitizeArchivedEvidenceValue(item.ProtocolEvidence, policy).(map[string]any); ok {
		item.ProtocolEvidence = structured
	} else {
		item.ProtocolEvidence = nil
	}
	item.Snapshot = sanitizeArchivedSnapshot(item.Snapshot, policy)
	item.SourceStepID = scrub.ValueLabel(item.SourceStepID, policy)
	item.SourceAction = scrub.ValueLabel(item.SourceAction, policy)
	item.SourceActionName = scrub.ValueLabel(item.SourceActionName, policy)
	// Preserve the authored source transaction name.
	item.SourceProfile = scrub.ValueLabel(item.SourceProfile, policy)
	if item.ServerTelemetry != nil {
		cloned := *item.ServerTelemetry
		cloned.GRPC = cloned.GRPC.Clone()
		if !cloned.GRPC.Valid() {
			cloned.GRPC = nil
		} else {
			cloned.GRPC.Method = scrub.ValueLabel(cloned.GRPC.Method, policy)
		}
		cloned.PartitionID = scrub.ValueLabel(cloned.PartitionID, policy)
		cloned.SOAPFaultCode = scrub.ValueLabel(cloned.SOAPFaultCode, policy)
		cloned.SOAPFaultCategory = scrub.ValueLabel(cloned.SOAPFaultCategory, policy)
		cloned.SOAPNamespaceConflict = scrub.ValueLabel(cloned.SOAPNamespaceConflict, policy)
		item.ServerTelemetry = &cloned
	}
}

func sanitizeArchivedStringMap(values map[string]string, policy scrub.Policy) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if scrub.SensitiveName(key) {
			out[key] = scrub.Redacted
		} else {
			out[key] = scrub.ValueLabel(value, policy)
		}
	}
	return out
}

func sanitizeArchivedEvidenceValue(value any, policy scrub.Policy) any {
	switch typed := value.(type) {
	case nil, bool, json.Number,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return value
	case string:
		return scrub.ValueLabel(typed, policy)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = sanitizeArchivedEvidenceValue(item, policy)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if scrub.SensitiveName(key) {
				out[key] = scrub.Redacted
			} else {
				out[key] = sanitizeArchivedEvidenceValue(item, policy)
			}
		}
		return out
	default:
		return sanitizeArchivedTypedEvidence(value, policy)
	}
}

// sanitizeArchivedTypedEvidence scrubs evidence a protocol adapter recorded as a
// concrete Go value — a struct, a pointer, a typed map or slice — rather than as
// the JSON-native shapes above. Such a value used to be returned untouched, so a
// sensitive field name inside it reached the archive verbatim even though the
// encoder was about to serialize it as an ordinary JSON object.
//
// It is normalized to exactly the JSON the encoder would have produced and then
// scrubbed through the same recursion, so field-name redaction and label
// scrubbing apply uniformly regardless of how the adapter typed its evidence.
// A value that cannot be represented as JSON is dropped: it would otherwise have
// failed the whole transaction encode, and losing one evidence blob is better
// than losing the archived transaction or emitting it unscrubbed.
func sanitizeArchivedTypedEvidence(value any, policy scrub.Policy) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil
	}
	return sanitizeArchivedEvidenceValue(normalized, policy)
}

func sanitizeArchivedHeaders(headers map[string]string, policy scrub.Policy) map[string]string {
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if scrub.SensitiveName(key) {
			out[key] = scrub.Redacted
		} else {
			out[key] = scrub.ValueLabel(value, policy)
		}
	}
	return out
}

func scrubArchiveTarget(value string, policy scrub.Policy) string {
	trimmed := strings.TrimSpace(value)
	if strings.Contains(trimmed, "://") {
		return scrub.ValueURL(value, policy)
	}
	return scrub.ValueLabel(value, policy)
}

func sanitizeArchivedConsoleEntries(entries []protocol.ConsoleEntry, policy scrub.Policy) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]protocol.ConsoleEntry, len(entries))
	copy(out, entries)
	for index := range out {
		out[index].Level = scrub.ValueLabel(out[index].Level, policy)
		out[index].Text = scrub.ValueLabel(out[index].Text, policy)
		out[index].Source = scrubArchiveTarget(out[index].Source, policy)
	}
	return out
}

func sanitizeArchivedNetworkEntries(entries []protocol.NetworkEntry, policy scrub.Policy) []protocol.NetworkEntry {
	out := protocol.CloneNetworkEntries(entries)
	for index := range out {
		out[index].URL = scrub.ValueURL(out[index].URL, policy)
		out[index].Method = scrub.ValueLabel(out[index].Method, policy)
		out[index].ResourceType = scrub.ValueLabel(out[index].ResourceType, policy)
		out[index].RequestHeaders = sanitizeArchivedHeaders(out[index].RequestHeaders, policy)
		out[index].RequestBody = scrub.ValueLabel(out[index].RequestBody, policy)
		out[index].ResponseHeaders = sanitizeArchivedHeaders(out[index].ResponseHeaders, policy)
	}
	return out
}

func sanitizeArchivedDOMSummary(summary *protocol.DOMSummary, policy scrub.Policy) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	out := *summary
	out.Title = scrub.ValueLabel(out.Title, policy)
	out.ReadyState = scrub.ValueLabel(out.ReadyState, policy)
	out.VisibleText = append([]string(nil), summary.VisibleText...)
	for index := range out.VisibleText {
		out.VisibleText[index] = scrub.ValueLabel(out.VisibleText[index], policy)
	}
	return &out
}

func sanitizeArchivedSnapshot(snapshot *protocol.ErrorSnapshot, policy scrub.Policy) *protocol.ErrorSnapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.RequestMethod = scrub.ValueLabel(out.RequestMethod, policy)
	out.RequestURL = scrub.ValueURL(out.RequestURL, policy)
	out.RequestHeaders = sanitizeArchivedHeaders(out.RequestHeaders, policy)
	out.RequestBody = scrub.ValueLabel(out.RequestBody, policy)
	out.ResponseHeaders = sanitizeArchivedHeaders(out.ResponseHeaders, policy)
	previousResponseBody := out.ResponseBody
	out.ResponseBody = scrub.ValueLabel(out.ResponseBody, policy)
	if previousResponseBody != out.ResponseBody {
		out.ResponseBodyState = "redacted"
	}
	out.Resources = protocol.CloneResourceSnapshots(snapshot.Resources)
	for i := range out.Resources {
		r := &out.Resources[i]
		r.RequestURL = scrub.ValueURL(r.RequestURL, policy)
		r.RequestHeaders = sanitizeArchivedHeaders(r.RequestHeaders, policy)
		r.ResponseHeaders = sanitizeArchivedHeaders(r.ResponseHeaders, policy)
		previous := r.ResponseBody
		r.ResponseBody = scrub.ValueLabel(r.ResponseBody, policy)
		if previous != r.ResponseBody {
			r.ResponseBodyState = "redacted"
		}
	}
	out.PageURL = scrub.ValueURL(out.PageURL, policy)
	out.PageTitle = scrub.ValueLabel(out.PageTitle, policy)
	out.HTML = scrub.ValueLabel(out.HTML, policy)
	out.ConsoleEntries = sanitizeArchivedConsoleEntries(out.ConsoleEntries, policy)
	out.NetworkEntries = sanitizeArchivedNetworkEntries(out.NetworkEntries, policy)
	out.DOMSummary = sanitizeArchivedDOMSummary(out.DOMSummary, policy)
	return &out
}

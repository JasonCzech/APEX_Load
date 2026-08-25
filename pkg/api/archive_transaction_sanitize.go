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
	item.TestName = scrub.Value(item.TestName, policy)
	// Group and proven business-transaction names are stable identities used as
	// report aggregation keys. ValueLabel skips only generic entropy/JWT guesses;
	// structured credentials and policy rules still apply. Ordinary action names
	// retain the full defense-in-depth scrub for direct and legacy archive input.
	item.Group = scrub.ValueLabel(item.Group, policy)
	item.Region = scrub.Value(item.Region, policy)
	item.Hierarchy = scrub.ValueLabel(item.Hierarchy, policy)
	item.PageName = scrub.ValueLabel(item.PageName, policy)
	item.Phase = scrub.ValueLabel(item.Phase, policy)
	if item.BusinessTransaction {
		item.ActionName = scrub.ValueLabel(item.ActionName, policy)
	} else {
		item.ActionName = scrub.Value(item.ActionName, policy)
	}
	item.CorrelationID = scrub.Value(item.CorrelationID, policy)
	item.TraceID = scrub.Value(item.TraceID, policy)
	item.SpanID = scrub.Value(item.SpanID, policy)
	item.NetworkProfile = scrub.Value(item.NetworkProfile, policy)
	item.DependencyType = scrub.Value(item.DependencyType, policy)
	item.DependencyTarget = scrubArchiveTarget(item.DependencyTarget, policy)
	item.ErrorMessage = scrub.Value(item.ErrorMessage, policy)
	item.Extractions = scrub.StringMap(item.Extractions, policy)
	item.PageURL = scrub.ValueURL(item.PageURL, policy)
	item.PageTitle = scrub.Value(item.PageTitle, policy)
	item.ConsoleEntries = sanitizeArchivedConsoleEntries(item.ConsoleEntries, policy)
	item.NetworkEntries = sanitizeArchivedNetworkEntries(item.NetworkEntries, policy)
	item.DOMSummary = sanitizeArchivedDOMSummary(item.DOMSummary, policy)
	if structured, ok := scrub.Structured(item.ProtocolEvidence, policy).(map[string]any); ok {
		item.ProtocolEvidence = structured
	} else {
		item.ProtocolEvidence = nil
	}
	item.Snapshot = sanitizeArchivedSnapshot(item.Snapshot, policy)
	item.SourceStepID = scrub.Value(item.SourceStepID, policy)
	item.SourceAction = scrub.Value(item.SourceAction, policy)
	item.SourceActionName = scrub.Value(item.SourceActionName, policy)
	item.SourceTransaction = scrub.ValueLabel(item.SourceTransaction, policy)
	item.SourceProfile = scrub.Value(item.SourceProfile, policy)
	if item.ServerTelemetry != nil {
		cloned := *item.ServerTelemetry
		cloned.PartitionID = scrub.Value(cloned.PartitionID, policy)
		cloned.SOAPFaultCode = scrub.Value(cloned.SOAPFaultCode, policy)
		cloned.SOAPFaultCategory = scrub.Value(cloned.SOAPFaultCategory, policy)
		cloned.SOAPNamespaceConflict = scrub.Value(cloned.SOAPNamespaceConflict, policy)
		item.ServerTelemetry = &cloned
	}
}

func scrubArchiveTarget(value string, policy scrub.Policy) string {
	trimmed := strings.TrimSpace(value)
	if strings.Contains(trimmed, "://") {
		return scrub.ValueURL(value, policy)
	}
	return scrub.Value(value, policy)
}

func sanitizeArchivedConsoleEntries(entries []protocol.ConsoleEntry, policy scrub.Policy) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]protocol.ConsoleEntry, len(entries))
	copy(out, entries)
	for index := range out {
		out[index].Level = scrub.Value(out[index].Level, policy)
		out[index].Text = scrub.Value(out[index].Text, policy)
		out[index].Source = scrubArchiveTarget(out[index].Source, policy)
	}
	return out
}

func sanitizeArchivedNetworkEntries(entries []protocol.NetworkEntry, policy scrub.Policy) []protocol.NetworkEntry {
	out := protocol.CloneNetworkEntries(entries)
	for index := range out {
		out[index].URL = scrub.ValueURL(out[index].URL, policy)
		out[index].Method = scrub.Value(out[index].Method, policy)
		out[index].ResourceType = scrub.Value(out[index].ResourceType, policy)
		out[index].RequestHeaders = scrub.Headers(out[index].RequestHeaders, policy)
		out[index].RequestBody = scrub.Value(out[index].RequestBody, policy)
		out[index].ResponseHeaders = scrub.Headers(out[index].ResponseHeaders, policy)
	}
	return out
}

func sanitizeArchivedDOMSummary(summary *protocol.DOMSummary, policy scrub.Policy) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	out := *summary
	out.Title = scrub.Value(out.Title, policy)
	out.ReadyState = scrub.Value(out.ReadyState, policy)
	out.VisibleText = append([]string(nil), summary.VisibleText...)
	for index := range out.VisibleText {
		out.VisibleText[index] = scrub.Value(out.VisibleText[index], policy)
	}
	return &out
}

func sanitizeArchivedSnapshot(snapshot *protocol.ErrorSnapshot, policy scrub.Policy) *protocol.ErrorSnapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.RequestMethod = scrub.Value(out.RequestMethod, policy)
	out.RequestURL = scrub.ValueURL(out.RequestURL, policy)
	out.RequestHeaders = scrub.Headers(out.RequestHeaders, policy)
	out.RequestBody = scrub.Value(out.RequestBody, policy)
	out.ResponseHeaders = scrub.Headers(out.ResponseHeaders, policy)
	out.ResponseBody = scrub.Value(out.ResponseBody, policy)
	out.PageURL = scrub.ValueURL(out.PageURL, policy)
	out.PageTitle = scrub.Value(out.PageTitle, policy)
	out.HTML = scrub.Value(out.HTML, policy)
	out.ConsoleEntries = sanitizeArchivedConsoleEntries(out.ConsoleEntries, policy)
	out.NetworkEntries = sanitizeArchivedNetworkEntries(out.NetworkEntries, policy)
	out.DOMSummary = sanitizeArchivedDOMSummary(out.DOMSummary, policy)
	return &out
}

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// finalizeTransactionEventDerivedFields supplies fields that older archive
// schemas did not persist. The content-based fallback keeps legacy event IDs
// stable across pagination and controller restarts; new writers add the archive
// ordinal so otherwise-identical events remain distinct.
func finalizeTransactionEventDerivedFields(item *TransactionEvent, ordinal int64) {
	if item == nil {
		return
	}
	// Rows that recorded their sent context already carry authoritative IDs;
	// deriving from CorrelationID would resurrect a gateway request ID or a
	// trace that a cache hit never sent.
	if item.TraceSent == nil {
		if item.TraceID == "" {
			item.TraceID = traceIDFromCorrelationID(item.CorrelationID)
		}
		if item.SpanID == "" {
			item.SpanID = spanIDFromCorrelationID(item.CorrelationID)
		}
	}
	item.HasDetail = transactionEventHasDetail(item)
	if item.EventID != "" {
		return
	}
	fingerprint := struct {
		Timestamp         int64  `json:"timestamp"`
		TestID            string `json:"test_id"`
		Group             string `json:"group"`
		Region            string `json:"region"`
		Action            string `json:"action"`
		Correlation       string `json:"correlation"`
		Trace             string `json:"trace"`
		SourceStep        string `json:"source_step"`
		SourceTransaction string `json:"source_transaction"`
		DurationBits      uint64 `json:"duration_bits"`
		Status            int    `json:"status"`
		Ordinal           int64  `json:"ordinal,omitempty"`
	}{
		Timestamp: item.Timestamp.UnixNano(), TestID: item.TestID, Group: item.Group,
		Region: item.Region, Action: item.ActionName, Correlation: item.CorrelationID,
		Trace: item.TraceID, SourceStep: item.SourceStepID,
		SourceTransaction: item.SourceTransaction, DurationBits: math.Float64bits(item.DurationMs),
		Status: item.StatusCode, Ordinal: ordinal,
	}
	encoded, _ := json.Marshal(fingerprint)
	sum := sha256.Sum256(encoded)
	item.EventID = "tx_" + hex.EncodeToString(sum[:16])
}

func transactionEventHasDetail(item *TransactionEvent) bool {
	return item != nil && (item.Snapshot != nil || item.ClientMetrics != nil ||
		item.ServerTelemetry != nil || len(item.ProtocolEvidence) > 0 ||
		len(item.NetworkEntries) > 0 || len(item.ConsoleEntries) > 0 ||
		item.DOMSummary != nil)
}

// applySentTraceContext records the W3C context a request carried. Archived
// trace and span IDs come from it whenever it exists; when it never reached a
// server they stay empty, so the row cannot be looked up as a trace. Without a
// recorded context the legacy CorrelationID derivation applies.
func applySentTraceContext(item *TransactionEvent, sent *protocol.SentTraceContext) {
	if item == nil {
		return
	}
	if sent == nil {
		item.TraceID = traceIDFromCorrelationID(item.CorrelationID)
		item.SpanID = spanIDFromCorrelationID(item.CorrelationID)
		return
	}
	wasSent := sent.Sent
	item.TraceSent = &wasSent
	item.TraceSource = sent.Source
	item.SentTraceID = sent.TraceID
	item.SentSpanID = sent.SpanID
	item.TraceID, item.SpanID = "", ""
	if wasSent {
		item.TraceID, item.SpanID = sent.TraceID, sent.SpanID
	}
}

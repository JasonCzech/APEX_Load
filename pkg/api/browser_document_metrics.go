package api

import "github.com/jasonczech/apexload/pkg/protocol"

// Older action-only browser evidence cannot qualify an independent document
// population. Keep it as unknown coverage, without deriving counts or zeroes.
func browserDocumentsForLive(summary *protocol.BrowserDocumentSummary, browserParticipant bool) *protocol.BrowserDocumentSummary {
	if summary == nil {
		if !browserParticipant {
			return nil
		}
		return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true}
	}
	copy := summary.Clone()
	if copy == nil {
		return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true}
	}
	return copy
}

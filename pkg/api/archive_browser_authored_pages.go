package api

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxArchiveAuthoredPageBindings = 1024

// BrowserAuthoredPageReport keeps command/response bindings separate from
// document metrics. Counts describe retained receipts, not all issued pages.
// Missing historical receipts and an incomplete archive never imply zero pages.
type BrowserAuthoredPageReport struct {
	Version        int                                    `json:"version"`
	ReceiptRecords int64                                  `json:"receipt_records"`
	Bindings       []*protocol.BrowserAuthoredPageBinding `json:"bindings"`
	Unavailable    map[string]int64                       `json:"unavailable,omitempty"`
	Dropped        int64                                  `json:"dropped"`
	Incomplete     bool                                   `json:"incomplete"`
}
type archiveAuthoredPageAccumulator struct {
	receipts    int64
	rows        map[string]*protocol.BrowserAuthoredPageBinding
	conflicts   map[string]bool
	unavailable map[string]int64
	dropped     int64
}

func newArchiveAuthoredPageAccumulator() *archiveAuthoredPageAccumulator {
	return &archiveAuthoredPageAccumulator{rows: map[string]*protocol.BrowserAuthoredPageBinding{}, conflicts: map[string]bool{}, unavailable: map[string]int64{}}
}
func (a *archiveAuthoredPageAccumulator) merge(tx TransactionEvent, sources map[string]protocol.BrowserAuthoredDefinition) {
	raw, exists := tx.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey]
	if !exists {
		return
	}
	a.receipts++
	data, err := json.Marshal(raw)
	if err != nil {
		a.unavailable["malformed_receipt"]++
		return
	}
	receipt, err := protocol.DecodeBrowserAuthoredPageBinding(data)
	if err != nil {
		a.unavailable["malformed_receipt"]++
		return
	}
	if receipt.State != "bound" {
		a.unavailable[receipt.Reason]++
		return
	}
	definition := sources[tx.Group]
	step := receipt.AuthoredStep
	if !definition.Valid() || definition.ScriptSHA256 != step.ScriptSHA256 || definition.StructureSHA256 != step.SourceStructureSHA256 {
		a.unavailable["original_source_mismatch"]++
		return
	}
	key := receipt.SourceGeneration + "/" + receipt.DocumentID + "/" + receipt.LifecycleID
	if a.conflicts[key] {
		a.unavailable["conflicting_binding"]++
		return
	}
	if prior, ok := a.rows[key]; ok {
		if prior.PageID != receipt.PageID || prior.RequestID != receipt.RequestID || prior.Version != receipt.Version || prior.Association != receipt.Association || prior.NavigationSequence != receipt.NavigationSequence {
			delete(a.rows, key)
			a.conflicts[key] = true
			a.unavailable["conflicting_binding"]++
		}
		return
	}
	if len(a.rows)+len(a.conflicts) >= maxArchiveAuthoredPageBindings {
		a.dropped++
		return
	}
	a.rows[key] = receipt.Clone()
}
func (a *archiveAuthoredPageAccumulator) result() *BrowserAuthoredPageReport {
	if a.receipts == 0 {
		return nil
	}
	report := &BrowserAuthoredPageReport{Version: 1, ReceiptRecords: a.receipts, Bindings: []*protocol.BrowserAuthoredPageBinding{}, Unavailable: maps.Clone(a.unavailable), Dropped: a.dropped, Incomplete: a.dropped > 0 || len(a.conflicts) > 0 || a.unavailable["malformed_receipt"] > 0 || a.unavailable["original_source_mismatch"] > 0}
	for _, key := range slices.Sorted(maps.Keys(a.rows)) {
		report.Bindings = append(report.Bindings, a.rows[key].Clone())
	}
	return report
}

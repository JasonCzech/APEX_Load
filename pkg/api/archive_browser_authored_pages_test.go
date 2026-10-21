package api

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
	"time"
)

func archiveAuthoredPageFixture(index int) (*protocol.BrowserAuthoredPageBinding, map[string]protocol.BrowserAuthoredDefinition) {
	definition := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	step := protocol.NewBrowserOriginalAuthoredStep(definition, "actions[0]")
	g := strings.Repeat("c", 32)
	return &protocol.BrowserAuthoredPageBinding{Version: 1, State: "bound", ObservedAt: time.Now().UTC(), AuthoredStep: step, PageID: protocol.BrowserAuthoredPageID(step), SourceGeneration: g, RequestID: fmt.Sprintf("%s-r%d", g, index), DocumentID: fmt.Sprintf("%s-d%d", g, index), LifecycleID: fmt.Sprintf("%s-l%d", g, index), Association: "cdp_loader_join"}, map[string]protocol.BrowserAuthoredDefinition{"browser": definition}
}
func pageBindingTransaction(b *protocol.BrowserAuthoredPageBinding) TransactionEvent {
	return TransactionEvent{Group: "browser", ProtocolEvidence: map[string]any{protocol.BrowserAuthoredPageEvidenceKey: b}}
}
func TestArchiveAuthoredPageReceiptsPreserveSourceAndConflicts(t *testing.T) {
	a := newArchiveAuthoredPageAccumulator()
	if a.result() != nil {
		t.Fatal("historical absence invented evidence")
	}
	b, sources := archiveAuthoredPageFixture(1)
	a.merge(pageBindingTransaction(b), sources)
	a.merge(pageBindingTransaction(b), sources)
	result := a.result()
	if result.ReceiptRecords != 2 || len(result.Bindings) != 1 || result.Incomplete {
		t.Fatal(result)
	}
	result.Bindings[0].AuthoredStep.StepPath = "changed"
	if !a.result().Bindings[0].Valid() {
		t.Fatal("snapshot alias")
	}
	other := b.Clone()
	other.AuthoredStep = protocol.NewBrowserOriginalAuthoredStep(sources["browser"], "actions[1]")
	other.PageID = protocol.BrowserAuthoredPageID(other.AuthoredStep)
	a.merge(pageBindingTransaction(other), sources)
	a.merge(pageBindingTransaction(b), sources)
	result = a.result()
	if !result.Incomplete || len(result.Bindings) != 0 || result.Unavailable["conflicting_binding"] != 2 {
		t.Fatal("conflicted identity readmitted", result)
	}
	a = newArchiveAuthoredPageAccumulator()
	a.merge(pageBindingTransaction(b), nil)
	if a.result().Unavailable["original_source_mismatch"] != 1 || len(a.result().Bindings) != 0 {
		t.Fatal("untrusted source accepted")
	}
	b.Version = 99
	a.merge(pageBindingTransaction(b), sources)
	if a.result().Unavailable["malformed_receipt"] != 1 {
		t.Fatal("malformed admitted")
	}
}
func TestArchiveAuthoredPageReceiptsBoundOutputAndMissingness(t *testing.T) {
	a := newArchiveAuthoredPageAccumulator()
	for i := 0; i <= maxArchiveAuthoredPageBindings; i++ {
		b, s := archiveAuthoredPageFixture(i)
		a.merge(pageBindingTransaction(b), s)
	}
	result := a.result()
	if len(result.Bindings) != maxArchiveAuthoredPageBindings || result.Dropped != 1 || !result.Incomplete {
		t.Fatal(result)
	}
	for i := 1; i < len(result.Bindings); i++ {
		if result.Bindings[i-1].DocumentID > result.Bindings[i].DocumentID {
			t.Fatal("nondeterministic order")
		}
	}
	b, s := archiveAuthoredPageFixture(1)
	unavailable := &protocol.BrowserAuthoredPageBinding{Version: 1, State: "unavailable", Reason: "navigation_join_unavailable", ObservedAt: b.ObservedAt, AuthoredStep: b.AuthoredStep}
	a = newArchiveAuthoredPageAccumulator()
	a.merge(pageBindingTransaction(unavailable), s)
	if a.result().Unavailable["navigation_join_unavailable"] != 1 || len(a.result().Bindings) != 0 {
		t.Fatal("missingness invented a binding")
	}
}

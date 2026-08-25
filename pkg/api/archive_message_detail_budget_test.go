package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRunArchiveWriterEnforcesSuccessfulMessageDetailBudgets(t *testing.T) {
	writer := &runArchiveWriter{}
	result := &protocol.Result{
		Success: true, MessageDetailRequested: true, MessageDetailMaxItems: 1,
		MessageDetailMaxItemBytes: 256, MessageDetailMaxTotalBytes: 256,
	}
	first := &TransactionEvent{Snapshot: &protocol.ErrorSnapshot{ResponseBody: strings.Repeat("x", 1024)}}
	writer.applyMessageDetailBudget(first, result)
	if first.Snapshot == nil || writer.manifest.MessageDetailCapture == nil {
		t.Fatalf("first detail was not retained: event=%#v stats=%#v", first, writer.manifest.MessageDetailCapture)
	}
	stats := writer.manifest.MessageDetailCapture
	if stats.RetainedItems != 1 || stats.TruncatedItems != 1 || stats.RetainedBytes > 256 {
		t.Fatalf("first capture stats = %#v", stats)
	}
	second := &TransactionEvent{Snapshot: &protocol.ErrorSnapshot{ResponseBody: "second"}}
	writer.applyMessageDetailBudget(second, result)
	if second.Snapshot != nil || stats.Dropped["item_limit"] != 1 {
		t.Fatalf("second capture = %#v stats=%#v", second, stats)
	}
}

func TestMessageDetailBudgetDoesNotRepurposeLegacyFailureSnapshot(t *testing.T) {
	writer := &runArchiveWriter{}
	event := &TransactionEvent{Snapshot: &protocol.ErrorSnapshot{ResponseBody: "failure"}}
	writer.applyMessageDetailBudget(event, &protocol.Result{Success: false})
	if event.Snapshot == nil || writer.manifest.MessageDetailCapture != nil {
		t.Fatalf("legacy failure evidence changed: event=%#v manifest=%#v", event, writer.manifest)
	}
}

func TestAdaptiveMessageDetailReservesBudgetByPriority(t *testing.T) {
	writer := &runArchiveWriter{}
	base := protocol.Result{Success: true, MessageDetailRequested: true, MessageDetailMode: "adaptive", MessageDetailMaxItems: 8, MessageDetailMaxItemBytes: 256, MessageDetailMaxTotalBytes: 2048}
	for index := 0; index < 3; index++ {
		result := base
		result.MessageDetailPriority = 1
		event := &TransactionEvent{Snapshot: &protocol.ErrorSnapshot{ResponseBody: "representative"}}
		writer.applyMessageDetailBudget(event, &result)
		if index < 2 && event.Snapshot == nil {
			t.Fatalf("representative item %d unexpectedly dropped", index)
		}
		if index == 2 && event.Snapshot != nil {
			t.Fatal("representative class exceeded its reserved item share")
		}
	}
	for index := 0; index < 4; index++ {
		result := base
		result.MessageDetailPriority = 3
		event := &TransactionEvent{Snapshot: &protocol.ErrorSnapshot{ResponseBody: "protocol-outlier"}}
		writer.applyMessageDetailBudget(event, &result)
		if event.Snapshot == nil {
			t.Fatalf("priority item %d was not retained", index)
		}
	}
	if got := writer.manifest.MessageDetailCapture.Dropped["priority_1_item_limit"]; got != 1 {
		t.Fatalf("priority drop accounting = %d, want 1", got)
	}
}

func TestAdaptiveMessageDetailOneItemBudgetRetainsHighPriority(t *testing.T) {
	writer := &runArchiveWriter{}
	result := &protocol.Result{
		Success: true, MessageDetailRequested: true, MessageDetailMode: "adaptive", MessageDetailPriority: 3,
		MessageDetailMaxItems: 1, MessageDetailMaxItemBytes: 256, MessageDetailMaxTotalBytes: 256,
	}
	event := &TransactionEvent{Snapshot: &protocol.ErrorSnapshot{ResponseBody: "outlier"}}
	writer.applyMessageDetailBudget(event, result)
	if event.Snapshot == nil || writer.manifest.MessageDetailCapture.RetainedItems != 1 {
		t.Fatalf("one-item adaptive capture = event %#v stats %#v", event, writer.manifest.MessageDetailCapture)
	}
}

package engine

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestMessageDetailBudgetSplitsAdaptiveSharesByPriority(t *testing.T) {
	// Four items split 1 low / 1 middle / 2 high.
	limits := MessageDetailLimits{MaxItems: 4, MaxItemBytes: 1024, MaxTotalBytes: 4096, Adaptive: true}
	var usage MessageDetailUsage
	kept := map[int]int{}
	var drops []string
	for _, priority := range []int{1, 1, 2, 2, 3, 3, 3, 0} {
		admission := usage.Admit(limits, priority, &protocol.ErrorSnapshot{ResponseBody: "detail"})
		if admission.Drop == MessageDetailKept {
			kept[admission.Priority]++
			continue
		}
		drops = append(drops, admission.Drop.Reason(admission.Priority))
	}
	if kept[1] != 1 || kept[2] != 1 || kept[3] != 2 || usage.Items != 4 {
		t.Fatalf("kept by priority = %v, items = %d; want 1/1/2 and 4", kept, usage.Items)
	}
	// Priority 0 is clamped to low, whose share is already used.
	want := "priority_1_item_limit,priority_2_item_limit,priority_3_item_limit,priority_1_item_limit"
	if got := strings.Join(drops, ","); got != want {
		t.Fatalf("drops = %s, want %s", got, want)
	}
}

func TestMessageDetailBudgetTruncatesToTheBytesThatRemain(t *testing.T) {
	limits := MessageDetailLimits{MaxItems: 10, MaxItemBytes: 4096, MaxTotalBytes: 300}
	var usage MessageDetailUsage
	admission := usage.Admit(limits, 0, &protocol.ErrorSnapshot{ResponseBody: strings.Repeat("x", 1000)})
	if admission.Drop != MessageDetailKept || !admission.Truncated || admission.Bytes > 300 || admission.Bytes != usage.Bytes {
		t.Fatalf("admission = %+v, usage = %+v; want a truncated detail within 300 bytes", admission, usage)
	}
	if admission.Priority != 0 || usage.PriorityItems != [4]int64{} {
		t.Fatalf("a non-adaptive budget counted priorities: %+v", usage)
	}
	usage.Bytes = limits.MaxTotalBytes
	if admission := usage.Admit(limits, 0, &protocol.ErrorSnapshot{ResponseBody: "more"}); admission.Drop != MessageDetailTotalByteLimit || admission.Snapshot != nil {
		t.Fatalf("admission past the byte budget = %+v, want total_byte_limit", admission)
	}
}

func TestMessageDetailBudgetStopsAtItemLimit(t *testing.T) {
	limits := MessageDetailLimits{MaxItems: 1, MaxItemBytes: 1024, MaxTotalBytes: 4096}
	var usage MessageDetailUsage
	if admission := usage.Admit(limits, 0, &protocol.ErrorSnapshot{ResponseBody: "first"}); admission.Drop != MessageDetailKept {
		t.Fatalf("first admission = %+v, want kept", admission)
	}
	if admission := usage.Admit(limits, 0, &protocol.ErrorSnapshot{ResponseBody: "second"}); admission.Drop.Reason(admission.Priority) != "item_limit" {
		t.Fatalf("second admission = %+v, want item_limit", admission)
	}
}

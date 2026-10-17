package engine

import (
	"encoding/json"
	"fmt"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// MessageDetailLimits bounds the message details a run keeps for successful
// transactions. An adaptive budget splits the item and byte limits 25/25/50
// across priorities 1 (low), 2 and 3 (high).
type MessageDetailLimits struct {
	MaxItems      int64
	MaxItemBytes  int64
	MaxTotalBytes int64
	Adaptive      bool
}

// MessageDetailUsage counts the details a budget has kept. Only adaptive
// budgets count per priority; index 0 of those arrays is unused.
type MessageDetailUsage struct {
	Items         int64
	Bytes         int64
	PriorityItems [4]int64
	PriorityBytes [4]int64
}

// MessageDetailDrop says why a budget refused a message detail.
type MessageDetailDrop int

const (
	MessageDetailKept MessageDetailDrop = iota
	// The detail's priority share of items is used up.
	MessageDetailPriorityItemLimit
	// The detail's priority share of bytes is used up.
	MessageDetailPriorityByteLimit
	// The budget holds its maximum number of details.
	MessageDetailItemLimit
	// No bytes remain in the budget or in the detail's priority share.
	MessageDetailTotalByteLimit
	// The detail does not fit what remains, even truncated.
	MessageDetailItemByteLimit
)

// Reason names a drop as generator and local archive capture stats record it.
// Those stats have always reported a detail that does not fit, even
// truncated, as total_byte_limit.
func (d MessageDetailDrop) Reason(priority int) string {
	switch d {
	case MessageDetailPriorityItemLimit:
		return fmt.Sprintf("priority_%d_item_limit", priority)
	case MessageDetailPriorityByteLimit:
		return fmt.Sprintf("priority_%d_byte_limit", priority)
	case MessageDetailItemLimit:
		return "item_limit"
	case MessageDetailTotalByteLimit, MessageDetailItemByteLimit:
		return "total_byte_limit"
	default:
		return ""
	}
}

// MessageDetailAdmission is the outcome of offering one detail to a budget.
type MessageDetailAdmission struct {
	// Snapshot is the detail to keep, truncated to fit; nil when dropped.
	Snapshot *protocol.ErrorSnapshot
	// Bytes is what the kept detail charged to the budget.
	Bytes int64
	// Priority is the detail's priority (1-3) in an adaptive budget, else 0.
	Priority int
	// Truncated reports that the detail was shortened, whether or not it was
	// then kept.
	Truncated bool
	Drop      MessageDetailDrop
}

// Admit offers one message detail to the budget. A kept detail is truncated
// to the smaller of the per-item limit and the bytes that remain, and charged
// at its encoded JSON size.
func (u *MessageDetailUsage) Admit(limits MessageDetailLimits, priority int, snapshot *protocol.ErrorSnapshot) MessageDetailAdmission {
	var admission MessageDetailAdmission
	priorityByteLimit := limits.MaxTotalBytes
	if limits.Adaptive {
		admission.Priority = min(max(priority, 1), 3)
		priorityItemLimit := messageDetailPriorityShare(limits.MaxItems, admission.Priority)
		priorityByteLimit = messageDetailPriorityShare(limits.MaxTotalBytes, admission.Priority)
		if priorityItemLimit == 0 || u.PriorityItems[admission.Priority] >= priorityItemLimit {
			admission.Drop = MessageDetailPriorityItemLimit
			return admission
		}
		if priorityByteLimit == 0 || u.PriorityBytes[admission.Priority] >= priorityByteLimit {
			admission.Drop = MessageDetailPriorityByteLimit
			return admission
		}
	}
	if u.Items >= limits.MaxItems {
		admission.Drop = MessageDetailItemLimit
		return admission
	}
	remaining := limits.MaxTotalBytes - u.Bytes
	if admission.Priority > 0 {
		remaining = min(remaining, priorityByteLimit-u.PriorityBytes[admission.Priority])
	}
	if remaining <= 0 {
		admission.Drop = MessageDetailTotalByteLimit
		return admission
	}
	limit := min(limits.MaxItemBytes, remaining)
	if messageDetailSnapshotBytes(snapshot) > limit {
		snapshot = BoundErrorSnapshot(snapshot, int(limit))
		admission.Truncated = true
	}
	retained := messageDetailSnapshotBytes(snapshot)
	if snapshot == nil || retained <= 0 || retained > limit {
		admission.Drop = MessageDetailItemByteLimit
		return admission
	}
	u.Items++
	u.Bytes += retained
	if admission.Priority > 0 {
		u.PriorityItems[admission.Priority]++
		u.PriorityBytes[admission.Priority] += retained
	}
	admission.Snapshot = snapshot
	admission.Bytes = retained
	return admission
}

// messageDetailPriorityShare divides a finite budget into the authored
// 25/25/50 low/mid/high shares without losing units to integer rounding. The
// highest priority receives the odd unit first, followed by the middle class;
// this keeps a one-item adaptive budget useful while the three limits always
// sum to the original total.
func messageDetailPriorityShare(total int64, priority int) int64 {
	if total <= 0 || priority < 1 || priority > 3 {
		return 0
	}
	high := (total + 1) / 2
	remaining := total - high
	low := remaining / 2
	middle := remaining - low
	switch priority {
	case 1:
		return low
	case 2:
		return middle
	default:
		return high
	}
}

func messageDetailSnapshotBytes(snapshot *protocol.ErrorSnapshot) int64 {
	if snapshot == nil {
		return 0
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return 0
	}
	return int64(len(encoded))
}

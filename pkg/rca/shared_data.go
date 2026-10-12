package rca

import "slices"

// SharedDataEvidence is value-free runtime evidence. A wait or an empty queue
// can be intentional; neither by itself establishes a target-system failure.
type SharedDataEvidence struct {
	TableID         string `json:"table_id"`
	WaitCount       uint64 `json:"wait_count"`
	WaitTotalNS     uint64 `json:"wait_total_ns"`
	EmptyOutcomes   uint64 `json:"empty_outcomes"`
	Throttled       uint64 `json:"throttled"`
	UnknownOutcomes uint64 `json:"unknown_outcomes"`
	Redeliveries    uint64 `json:"redeliveries"`
	RecoveryGaps    uint64 `json:"recovery_gaps"`
	Incomplete      bool   `json:"incomplete"`
}

const RuleSharedDataThrottled = "shared_data_throttled"
const RuleSharedDataUnknown = "shared_data_outcome_unknown"
const LimitationSharedDataGap = "shared_data_observation_gap"

// applySharedDataValidity keeps load-harness uncertainty separate from target
// latency. It runs after the ordinary verdict, including for an empty archive.
func applySharedDataValidity(out *Validity, evidence []SharedDataEvidence) {
	if len(evidence) == 0 {
		return
	}
	out.SharedData = append([]SharedDataEvidence(nil), evidence[:min(len(evidence), 16)]...)
	if len(evidence) > 16 {
		out.Limitations = append(out.Limitations, "shared_data_evidence_truncated")
		out.Verdict = worseVerdict(out.Verdict, ValidityUnknown)
	}
	throttled, unknown, gap := false, false, false
	for _, item := range out.SharedData {
		throttled = throttled || item.Throttled > 0
		unknown = unknown || item.UnknownOutcomes > 0
		gap = gap || item.Incomplete || item.RecoveryGaps > 0
	}
	if gap {
		out.Verdict = worseVerdict(out.Verdict, ValidityUnknown)
		out.Limitations = append(out.Limitations, LimitationSharedDataGap)
		if out.Verdict == ValidityUnknown {
			out.Reasons = append(out.Reasons, RuleEvidenceIncomplete)
		}
	}
	if throttled || unknown {
		if ValidityRank(out.Verdict) < ValidityRank(ValiditySuspect) {
			out.Reasons = nil
		}
		out.Verdict = worseVerdict(out.Verdict, ValiditySuspect)
		if out.Verdict == ValiditySuspect {
			if throttled {
				out.Reasons = append(out.Reasons, RuleSharedDataThrottled)
			}
			if unknown {
				out.Reasons = append(out.Reasons, RuleSharedDataUnknown)
			}
		}
	}
	slices.Sort(out.Reasons)
	out.Reasons = slices.Compact(out.Reasons)
	slices.Sort(out.Limitations)
	out.Limitations = slices.Compact(out.Limitations)
}

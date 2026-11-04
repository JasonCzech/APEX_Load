package api

import (
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	llmRollingWindowSlots    = 30
	llmRollingWindowDuration = 30 * time.Second
)

// LiveLLMMetrics exposes both the current interval's LLM inference metrics and
// the 30-second rolling-window summary used for stable live percentiles,
// token-throughput gauges, goodput, and goal-oriented SLA enforcement.
type LiveLLMMetrics struct {
	RollingWindowSeconds       float64                       `json:"rolling_window_seconds,omitempty"`
	IntervalRequests           int64                         `json:"interval_requests,omitempty"`
	IntervalOutputTokensPerSec float64                       `json:"interval_output_tokens_per_sec,omitempty"`
	IntervalInputTokensPerSec  float64                       `json:"interval_input_tokens_per_sec,omitempty"`
	OutputTokensPerSec         float64                       `json:"output_tokens_per_sec,omitempty"`
	InputTokensPerSec          float64                       `json:"input_tokens_per_sec,omitempty"`
	TotalTokensPerSec          float64                       `json:"total_tokens_per_sec,omitempty"`
	GoodputPct                 float64                       `json:"goodput_pct,omitempty"`
	ThrottleRatePct            float64                       `json:"throttle_rate_pct,omitempty"`
	TruncationRatePct          float64                       `json:"truncation_rate_pct,omitempty"`
	Incomplete                 bool                          `json:"incomplete,omitempty"`
	Summary                    *engine.LLMSummary            `json:"summary,omitempty"`
	IntervalSummary            *engine.LLMSummary            `json:"interval_summary,omitempty"`
	ByAction                   map[string]*engine.LLMSummary `json:"by_action,omitempty"`
	rawIntervalSummary         *engine.LLMSummary
}

type llmRollingSlot struct {
	at        time.Time
	windowSec float64
	summary   *engine.LLMSummary
}

type llmRollingWindow struct {
	slots [llmRollingWindowSlots]llmRollingSlot
	head  int
	count int
}

func (w *llmRollingWindow) record(at time.Time, windowSec float64, intervalSummary *engine.LLMSummary, byAction map[string]*engine.LLMSummary, goalConfig *GoalConfig) *LiveLLMMetrics {
	if windowSec <= 0 {
		windowSec = 1.0
	}
	if w != nil && !at.IsZero() && intervalSummary != nil && intervalSummary.Requests > 0 {
		// Idempotent update if called for the same timestamp (e.g. goal controller then publishLiveMetrics)
		if w.count > 0 {
			lastIdx := (w.head + w.count - 1) % llmRollingWindowSlots
			if w.slots[lastIdx].at.Equal(at) {
				w.slots[lastIdx] = llmRollingSlot{
					at:        at,
					windowSec: windowSec,
					summary:   intervalSummary.Clone(),
				}
			} else if at.After(w.slots[lastIdx].at) {
				if w.count == llmRollingWindowSlots {
					w.slots[w.head] = llmRollingSlot{}
					w.head = (w.head + 1) % llmRollingWindowSlots
					w.count--
				}
				idx := (w.head + w.count) % llmRollingWindowSlots
				w.slots[idx] = llmRollingSlot{
					at:        at,
					windowSec: windowSec,
					summary:   intervalSummary.Clone(),
				}
				w.count++
			}
		} else {
			w.slots[0] = llmRollingSlot{
				at:        at,
				windowSec: windowSec,
				summary:   intervalSummary.Clone(),
			}
			w.head = 0
			w.count = 1
		}
	}

	var merged *engine.LLMSummary
	var totalWindowSec float64
	if w != nil && w.count > 0 {
		cutoff := time.Time{}
		if !at.IsZero() {
			cutoff = at.Add(-llmRollingWindowDuration)
		}
		for i := 0; i < w.count; i++ {
			slot := w.slots[(w.head+i)%llmRollingWindowSlots]
			if slot.summary == nil || slot.summary.Requests <= 0 {
				continue
			}
			if !cutoff.IsZero() && !slot.at.IsZero() && slot.at.Before(cutoff) {
				continue
			}
			engine.MergeLLMSummary(&merged, slot.summary)
			totalWindowSec += max(slot.windowSec, 1.0)
		}
	}
	if merged == nil && intervalSummary != nil && intervalSummary.Requests > 0 {
		merged = intervalSummary.Clone()
		totalWindowSec = windowSec
	}
	if merged == nil || merged.Requests <= 0 {
		return nil
	}
	if totalWindowSec <= 0 {
		totalWindowSec = windowSec
	}

	sloTTFT, sloTPOT, sloE2E := resolveGoalLLMSLOThresholds(goalConfig)
	goodputPct := merged.GoodputPct(sloTTFT, sloTPOT, sloE2E)
	incomplete := merged.Incomplete || (intervalSummary != nil && intervalSummary.Incomplete)
	for _, a := range byAction {
		if a != nil && a.Incomplete {
			incomplete = true
		}
	}
	summaryOut := merged.NumbersOnly()
	if summaryOut != nil && incomplete {
		summaryOut.Incomplete = true
	}
	out := &LiveLLMMetrics{
		RollingWindowSeconds: totalWindowSec,
		OutputTokensPerSec:   float64(merged.CompletionTokens) / totalWindowSec,
		InputTokensPerSec:    float64(merged.PromptTokens) / totalWindowSec,
		TotalTokensPerSec:    float64(merged.TotalTokens) / totalWindowSec,
		GoodputPct:           goodputPct,
		ThrottleRatePct:      merged.ThrottleRatePct(),
		TruncationRatePct:    merged.TruncationRatePct(),
		Incomplete:           incomplete,
		Summary:              summaryOut,
	}
	if intervalSummary != nil && intervalSummary.Requests > 0 {
		out.IntervalRequests = intervalSummary.Requests
		out.IntervalOutputTokensPerSec = float64(intervalSummary.CompletionTokens) / windowSec
		out.IntervalInputTokensPerSec = float64(intervalSummary.PromptTokens) / windowSec
		out.rawIntervalSummary = intervalSummary.Clone()
		out.IntervalSummary = intervalSummary.NumbersOnly()
		if out.IntervalSummary != nil && incomplete {
			out.IntervalSummary.Incomplete = true
		}
	}
	if len(byAction) > 0 {
		out.ByAction = numbersOnlyLLMSummaryMap(byAction)
	}
	return out
}

func resolveGoalLLMSLOThresholds(config *GoalConfig) (sloTTFT, sloTPOT, sloE2E float64) {
	if config == nil {
		return 0, 0, 0
	}
	if config.LLMSLOTTFTTargetMs != nil && *config.LLMSLOTTFTTargetMs > 0 {
		sloTTFT = *config.LLMSLOTTFTTargetMs
	} else if config.MaxLLMP95TTFTMs != nil && *config.MaxLLMP95TTFTMs > 0 {
		sloTTFT = *config.MaxLLMP95TTFTMs
	}
	if config.LLMSLOTPOTTargetMs != nil && *config.LLMSLOTPOTTargetMs > 0 {
		sloTPOT = *config.LLMSLOTPOTTargetMs
	} else if config.MaxLLMP95TPOTMs != nil && *config.MaxLLMP95TPOTMs > 0 {
		sloTPOT = *config.MaxLLMP95TPOTMs
	}
	if config.LLMSLOE2ETargetMs != nil && *config.LLMSLOE2ETargetMs > 0 {
		sloE2E = *config.LLMSLOE2ETargetMs
	} else if config.MaxEndToEndLatencyMs != nil && *config.MaxEndToEndLatencyMs > 0 {
		sloE2E = *config.MaxEndToEndLatencyMs
	}
	return sloTTFT, sloTPOT, sloE2E
}

func buildLiveLLMMetricsFromEngineActions(actions map[string]*engine.ActionMetrics, window time.Duration) *LiveLLMMetrics {
	if len(actions) == 0 {
		return nil
	}
	names := make([]string, 0, len(actions))
	for name := range actions {
		names = append(names, name)
	}
	sort.Strings(names)
	var intervalSummary *engine.LLMSummary
	byAction := make(map[string]*engine.LLMSummary)
	for _, name := range names {
		am := actions[name]
		if am == nil || am.LLM == nil || am.LLM.Requests <= 0 {
			continue
		}
		cloned := am.LLM.Clone()
		byAction[name] = cloned
		engine.MergeLLMSummary(&intervalSummary, cloned)
	}
	if intervalSummary == nil || intervalSummary.Requests <= 0 {
		return nil
	}
	windowSec := window.Seconds()
	if windowSec <= 0 {
		windowSec = 1.0
	}
	var singleWindow llmRollingWindow
	return singleWindow.record(time.Time{}, windowSec, intervalSummary, byAction, nil)
}

func buildLiveLLMMetricsFromAggregates(aggregates []generatorrpc.MetricAggregate, windowSec float64) *LiveLLMMetrics {
	if len(aggregates) == 0 {
		return nil
	}
	var intervalSummary *engine.LLMSummary
	byAction := make(map[string]*engine.LLMSummary)
	for _, agg := range aggregates {
		if agg.LLM == nil || agg.LLM.Requests <= 0 {
			continue
		}
		name := strings.TrimSpace(agg.Name)
		cloned := agg.LLM.Clone()
		if name != "" {
			byAction[name] = cloned
		}
		engine.MergeLLMSummary(&intervalSummary, cloned)
	}
	if intervalSummary == nil || intervalSummary.Requests <= 0 {
		return nil
	}
	if windowSec <= 0 {
		windowSec = 1.0
	}
	var singleWindow llmRollingWindow
	return singleWindow.record(time.Time{}, windowSec, intervalSummary, byAction, nil)
}

func updateTestLLMRollingWindowLocked(test *ManagedTest, live *LiveMetrics) {
	if test == nil || live == nil {
		return
	}
	var intervalSummary *engine.LLMSummary
	var byAction map[string]*engine.LLMSummary
	var incomplete bool
	if live.LLM != nil {
		incomplete = live.LLM.Incomplete
		intervalSummary = live.LLM.rawIntervalSummary
		if intervalSummary == nil {
			intervalSummary = live.LLM.IntervalSummary
		}
		if intervalSummary == nil {
			intervalSummary = live.LLM.Summary
		}
		byAction = live.LLM.ByAction
	}
	updated := test.llmWindow.record(live.Timestamp, live.SampleWindowSeconds, intervalSummary, byAction, test.GoalConfig)
	if updated != nil {
		if incomplete {
			updated.Incomplete = true
			if updated.Summary != nil {
				updated.Summary.Incomplete = true
			}
			if updated.IntervalSummary != nil {
				updated.IntervalSummary.Incomplete = true
			}
		}
		live.LLM = updated
	}
}

func (in *LiveLLMMetrics) Clone() *LiveLLMMetrics {
	return cloneLiveLLMMetrics(in)
}

func cloneLiveLLMMetrics(in *LiveLLMMetrics) *LiveLLMMetrics {
	if in == nil {
		return nil
	}
	out := *in
	out.Summary = in.Summary.Clone()
	out.IntervalSummary = in.IntervalSummary.Clone()
	out.ByAction = cloneLLMSummaryMap(in.ByAction)
	out.rawIntervalSummary = in.rawIntervalSummary.Clone()
	return &out
}

func cloneLLMSummaryMap(in map[string]*engine.LLMSummary) map[string]*engine.LLMSummary {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*engine.LLMSummary, len(in))
	for k, v := range in {
		if v != nil {
			out[k] = v.Clone()
		}
	}
	return out
}

func numbersOnlyLLMSummaryMap(in map[string]*engine.LLMSummary) map[string]*engine.LLMSummary {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*engine.LLMSummary, len(in))
	for k, v := range in {
		if v != nil {
			out[k] = v.NumbersOnly()
		}
	}
	return out
}

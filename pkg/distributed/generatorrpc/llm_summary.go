package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
)

func llmSummaryToProto(s *engine.LLMSummary) *pb.LLMSummary {
	if s == nil || s.Requests <= 0 {
		return nil
	}
	out := &pb.LLMSummary{
		Requests:                s.Requests,
		SuccessfulRequests:      s.SuccessfulRequests,
		StreamedRequests:        s.StreamedRequests,
		FailedRequests:          s.FailedRequests,
		ThrottledRequests:       s.ThrottledRequests,
		ThrottledAttempts:       s.ThrottledAttempts,
		TotalRetryWaitMs:        s.TotalRetryWaitMs,
		TruncatedRequests:       s.TruncatedRequests,
		EmptyOutputRequests:     s.EmptyOutputRequests,
		ContentFilterRequests:   s.ContentFilterRequests,
		Http_200ErrorRequests:   s.HTTP200ErrorRequests,
		SchemaFailedRequests:    s.SchemaFailedRequests,
		TokensEstimatedRequests: s.TokensEstimatedRequests,
		PromptTokens:            s.PromptTokens,
		CompletionTokens:        s.CompletionTokens,
		ReasoningTokens:         s.ReasoningTokens,
		CachedTokens:            s.CachedTokens,
		TotalTokens:             s.TotalTokens,
		ToolCallsCount:          s.ToolCallsCount,
		FinishReasons:           cloneInt64Map(s.FinishReasons),
		Models:                  cloneInt64Map(s.Models),
		Dialects:                cloneInt64Map(s.Dialects),
		TtftSketch:              durationSketchSnapshotToProto(s.TTFTSketch),
		FirstVisibleTokenSketch: durationSketchSnapshotToProto(s.FirstVisibleTokenSketch),
		TpotSketch:              durationSketchSnapshotToProto(s.TPOTSketch),
		E2ESketch:               durationSketchSnapshotToProto(s.E2ESketch),
		AvgTtftMs:               s.AvgTTFTMs,
		P50TtftMs:               s.P50TTFTMs,
		P90TtftMs:               s.P90TTFTMs,
		P95TtftMs:               s.P95TTFTMs,
		P99TtftMs:               s.P99TTFTMs,
		AvgFirstVisibleTokenMs:  s.AvgFirstVisibleTokenMs,
		P50FirstVisibleTokenMs:  s.P50FirstVisibleTokenMs,
		P95FirstVisibleTokenMs:  s.P95FirstVisibleTokenMs,
		P99FirstVisibleTokenMs:  s.P99FirstVisibleTokenMs,
		AvgTpotMs:               s.AvgTPOTMs,
		P50TpotMs:               s.P50TPOTMs,
		P90TpotMs:               s.P90TPOTMs,
		P95TpotMs:               s.P95TPOTMs,
		P99TpotMs:               s.P99TPOTMs,
		AvgItlMs:                s.AvgITLMs,
		P50ItlMs:                s.P50ITLMs,
		P90ItlMs:                s.P90ITLMs,
		P95ItlMs:                s.P95ITLMs,
		P99ItlMs:                s.P99ITLMs,
		AvgE2EMs:                s.AvgE2EMs,
		P50E2EMs:                s.P50E2EMs,
		P90E2EMs:                s.P90E2EMs,
		P95E2EMs:                s.P95E2EMs,
		P99E2EMs:                s.P99E2EMs,
		ServerQueueSamples:      s.ServerQueueSamples,
		ServerQueueSumMs:        s.ServerQueueSumMs,
		AvgServerQueueMs:        s.AvgServerQueueMs,
		ServerPrefillSamples:    s.ServerPrefillSamples,
		ServerPrefillSumMs:      s.ServerPrefillSumMs,
		AvgServerPrefillMs:      s.AvgServerPrefillMs,
		ServerDecodeSamples:     s.ServerDecodeSamples,
		ServerDecodeSumMs:       s.ServerDecodeSumMs,
		AvgServerDecodeMs:       s.AvgServerDecodeMs,
		Incomplete:              s.Incomplete,
	}
	if s.ITLDistribution != nil && s.ITLDistribution.Count > 0 {
		out.ItlMs = javaHTTPDurationDistributionToProto(*s.ITLDistribution)
	}
	if s.MinRateLimitRemainingTokens != nil {
		v := *s.MinRateLimitRemainingTokens
		out.MinRatelimitRemainingTokens = &v
	}
	if s.MinRateLimitRemainingRequests != nil {
		v := *s.MinRateLimitRemainingRequests
		out.MinRatelimitRemainingRequests = &v
	}
	if len(s.ByInputBand) > 0 {
		out.ByInputBand = make(map[string]*pb.LLMBandSummary, len(s.ByInputBand))
		for k, b := range s.ByInputBand {
			if pbBand := llmBandSummaryToProto(b); pbBand != nil {
				out.ByInputBand[k] = pbBand
			}
		}
	}
	return out
}

func llmSummaryFromProto(p *pb.LLMSummary) *engine.LLMSummary {
	if p == nil || p.GetRequests() <= 0 {
		return nil
	}
	out := &engine.LLMSummary{
		Requests:                p.GetRequests(),
		SuccessfulRequests:      p.GetSuccessfulRequests(),
		StreamedRequests:        p.GetStreamedRequests(),
		FailedRequests:          p.GetFailedRequests(),
		ThrottledRequests:       p.GetThrottledRequests(),
		ThrottledAttempts:       p.GetThrottledAttempts(),
		TotalRetryWaitMs:        p.GetTotalRetryWaitMs(),
		TruncatedRequests:       p.GetTruncatedRequests(),
		EmptyOutputRequests:     p.GetEmptyOutputRequests(),
		ContentFilterRequests:   p.GetContentFilterRequests(),
		HTTP200ErrorRequests:    p.GetHttp_200ErrorRequests(),
		SchemaFailedRequests:    p.GetSchemaFailedRequests(),
		TokensEstimatedRequests: p.GetTokensEstimatedRequests(),
		PromptTokens:            p.GetPromptTokens(),
		CompletionTokens:        p.GetCompletionTokens(),
		ReasoningTokens:         p.GetReasoningTokens(),
		CachedTokens:            p.GetCachedTokens(),
		TotalTokens:             p.GetTotalTokens(),
		ToolCallsCount:          p.GetToolCallsCount(),
		FinishReasons:           cloneBoundedInt64Map(p.GetFinishReasons(), 16),
		Models:                  cloneBoundedInt64Map(p.GetModels(), 16),
		Dialects:                cloneBoundedInt64Map(p.GetDialects(), 16),
		TTFTSketch:              durationSketchSnapshotFromProto(p.GetTtftSketch()),
		FirstVisibleTokenSketch: durationSketchSnapshotFromProto(p.GetFirstVisibleTokenSketch()),
		TPOTSketch:              durationSketchSnapshotFromProto(p.GetTpotSketch()),
		E2ESketch:               durationSketchSnapshotFromProto(p.GetE2ESketch()),
		AvgTTFTMs:               p.GetAvgTtftMs(),
		P50TTFTMs:               p.GetP50TtftMs(),
		P90TTFTMs:               p.GetP90TtftMs(),
		P95TTFTMs:               p.GetP95TtftMs(),
		P99TTFTMs:               p.GetP99TtftMs(),
		AvgFirstVisibleTokenMs:  p.GetAvgFirstVisibleTokenMs(),
		P50FirstVisibleTokenMs:  p.GetP50FirstVisibleTokenMs(),
		P95FirstVisibleTokenMs:  p.GetP95FirstVisibleTokenMs(),
		P99FirstVisibleTokenMs:  p.GetP99FirstVisibleTokenMs(),
		AvgTPOTMs:               p.GetAvgTpotMs(),
		P50TPOTMs:               p.GetP50TpotMs(),
		P90TPOTMs:               p.GetP90TpotMs(),
		P95TPOTMs:               p.GetP95TpotMs(),
		P99TPOTMs:               p.GetP99TpotMs(),
		AvgITLMs:                p.GetAvgItlMs(),
		P50ITLMs:                p.GetP50ItlMs(),
		P90ITLMs:                p.GetP90ItlMs(),
		P95ITLMs:                p.GetP95ItlMs(),
		P99ITLMs:                p.GetP99ItlMs(),
		AvgE2EMs:                p.GetAvgE2EMs(),
		P50E2EMs:                p.GetP50E2EMs(),
		P90E2EMs:                p.GetP90E2EMs(),
		P95E2EMs:                p.GetP95E2EMs(),
		P99E2EMs:                p.GetP99E2EMs(),
		ServerQueueSamples:      p.GetServerQueueSamples(),
		ServerQueueSumMs:        p.GetServerQueueSumMs(),
		AvgServerQueueMs:        p.GetAvgServerQueueMs(),
		ServerPrefillSamples:    p.GetServerPrefillSamples(),
		ServerPrefillSumMs:      p.GetServerPrefillSumMs(),
		AvgServerPrefillMs:      p.GetAvgServerPrefillMs(),
		ServerDecodeSamples:     p.GetServerDecodeSamples(),
		ServerDecodeSumMs:       p.GetServerDecodeSumMs(),
		AvgServerDecodeMs:       p.GetAvgServerDecodeMs(),
		Incomplete:              p.GetIncomplete(),
	}
	if p.GetItlMs() != nil && len(p.GetItlMs().GetBuckets()) == 144 {
		dist := javaHTTPDurationDistributionFromProto(p.GetItlMs())
		out.ITLDistribution = &dist
	}
	if p.MinRatelimitRemainingTokens != nil {
		v := p.GetMinRatelimitRemainingTokens()
		out.MinRateLimitRemainingTokens = &v
	}
	if p.MinRatelimitRemainingRequests != nil {
		v := p.GetMinRatelimitRemainingRequests()
		out.MinRateLimitRemainingRequests = &v
	}
	if len(p.GetByInputBand()) > 0 {
		out.ByInputBand = make(map[string]*engine.LLMBandSummary, min(len(p.GetByInputBand()), 16))
		for k, pbBand := range p.GetByInputBand() {
			if len(out.ByInputBand) >= 16 {
				break
			}
			if band := llmBandSummaryFromProto(pbBand); band != nil {
				out.ByInputBand[k] = band
			}
		}
	}
	engine.FinalizeLLMSummary(out)
	return out
}

// LLMPeerUncounted reports whether a peer that sent no (or partial) LLM summary
// for count executions of an action may still have run LLM actions: for example,
// an older generator without LLM summary support.
func LLMPeerUncounted(s *engine.LLMSummary, count int64, dependencyType string) bool {
	if s != nil {
		return s.Incomplete || s.Requests < count
	}
	return count > 0 && (dependencyType == "" || dependencyType == "http" || dependencyType == "llm" || dependencyType == "multiple")
}

func llmBandSummaryToProto(b *engine.LLMBandSummary) *pb.LLMBandSummary {
	if b == nil || b.Requests <= 0 {
		return nil
	}
	return &pb.LLMBandSummary{
		Band:             b.Band,
		Requests:         b.Requests,
		StreamedRequests: b.StreamedRequests,
		PromptTokens:     b.PromptTokens,
		CompletionTokens: b.CompletionTokens,
		TtftSketch:       durationSketchSnapshotToProto(b.TTFTSketch),
		TpotSketch:       durationSketchSnapshotToProto(b.TPOTSketch),
		E2ESketch:        durationSketchSnapshotToProto(b.E2ESketch),
		AvgTtftMs:        b.AvgTTFTMs,
		P50TtftMs:        b.P50TTFTMs,
		P95TtftMs:        b.P95TTFTMs,
		P99TtftMs:        b.P99TTFTMs,
		AvgTpotMs:        b.AvgTPOTMs,
		P50TpotMs:        b.P50TPOTMs,
		P95TpotMs:        b.P95TPOTMs,
		P99TpotMs:        b.P99TPOTMs,
		AvgE2EMs:         b.AvgE2EMs,
		P95E2EMs:         b.P95E2EMs,
	}
}

func llmBandSummaryFromProto(p *pb.LLMBandSummary) *engine.LLMBandSummary {
	if p == nil || p.GetRequests() <= 0 {
		return nil
	}
	return &engine.LLMBandSummary{
		Band:             p.GetBand(),
		Requests:         p.GetRequests(),
		StreamedRequests: p.GetStreamedRequests(),
		PromptTokens:     p.GetPromptTokens(),
		CompletionTokens: p.GetCompletionTokens(),
		TTFTSketch:       durationSketchSnapshotFromProto(p.GetTtftSketch()),
		TPOTSketch:       durationSketchSnapshotFromProto(p.GetTpotSketch()),
		E2ESketch:        durationSketchSnapshotFromProto(p.GetE2ESketch()),
		AvgTTFTMs:        p.GetAvgTtftMs(),
		P50TTFTMs:        p.GetP50TtftMs(),
		P95TTFTMs:        p.GetP95TtftMs(),
		P99TTFTMs:        p.GetP99TtftMs(),
		AvgTPOTMs:        p.GetAvgTpotMs(),
		P50TPOTMs:        p.GetP50TpotMs(),
		P95TPOTMs:        p.GetP95TpotMs(),
		P99TPOTMs:        p.GetP99TpotMs(),
		AvgE2EMs:         p.GetAvgE2EMs(),
		P95E2EMs:         p.GetP95E2EMs(),
	}
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneBoundedInt64Map(in map[string]int64, maxEntries int) map[string]int64 {
	if len(in) == 0 || maxEntries <= 0 {
		return nil
	}
	out := make(map[string]int64, min(len(in), maxEntries))
	for k, v := range in {
		if len(out) >= maxEntries {
			break
		}
		out[k] = v
	}
	return out
}

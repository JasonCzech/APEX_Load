package dsl

import "strings"

const (
	FeatureParallelLoopControlV1 = "parallel_loop_control_v1"
	FeatureOpenArrivalV2         = "open_arrival_v2"
	FeatureDataJSONLV1           = "data_jsonl_v1"
	FeatureLLMInferenceV1        = "llm_inference_v1"
)

// RequiredCompositionRuntimeFeatures admits newer branch-local repeat semantics,
// open-model v2 arrival processes/rate stages, JSONL data sources, and LLM inference actions.
func RequiredCompositionRuntimeFeatures(s *Script) []string {
	if s == nil {
		return nil
	}
	var features []string
	requested := false
	llmRequested := s.Config.ErrorHandling.HonorRetryAfter || strings.EqualFold(strings.TrimSpace(s.Config.ErrorHandling.RetryBackoff), "retry_after")
	visitScriptActions(s, func(_ string, a Action) {
		canonical := CanonicalActionType(strings.ToLower(strings.TrimSpace(a.Type)))
		if canonical == "llm_request" || strings.EqualFold(strings.TrimSpace(a.BodyTemplateEncoding), "json") {
			llmRequested = true
		}
		if a.Params != nil {
			if b, ok := a.Params["llm"].(bool); ok && b {
				llmRequested = true
			}
			if d, ok := a.Params["llm_dialect"].(string); ok && strings.TrimSpace(d) != "" {
				llmRequested = true
			}
		}
		if canonical != "control_parallel" || a.Control == nil {
			return
		}
		var visit func([]Action)
		visit = func(actions []Action) {
			for _, child := range actions {
				kind := CanonicalActionType(strings.ToLower(strings.TrimSpace(child.Type)))
				if kind == "control_break" || kind == "control_continue" {
					requested = true
				}
				if child.Control != nil {
					for _, b := range child.Control.ActionBranches() {
						visit(b.Actions)
					}
				}
			}
		}
		for _, b := range a.Control.Branches {
			visit(b.Steps)
		}
	})
	if requested {
		features = append(features, FeatureParallelLoopControlV1)
	}
	if llmRequested {
		features = append(features, FeatureLLMInferenceV1)
	}
	proc := strings.ToLower(strings.TrimSpace(s.Config.Pacing.ArrivalProcess))
	if proc == "poisson" || proc == "gamma" || len(s.Config.Pacing.RateStages) > 0 || s.Config.Pacing.Quota != nil {
		features = append(features, FeatureOpenArrivalV2)
	}
	for _, ds := range s.DataSources {
		if strings.EqualFold(strings.TrimSpace(ds.Type), "jsonl") {
			features = append(features, FeatureDataJSONLV1)
			break
		}
	}
	return features
}

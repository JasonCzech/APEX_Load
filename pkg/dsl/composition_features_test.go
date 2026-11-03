package dsl

import "testing"

func TestParallelLoopControlCapability(t *testing.T) {
	a := Action{Type: "control_parallel", Control: &ControlFlow{Branches: []ParallelBranch{{Name: "one", Steps: []Action{{Type: "control_loop", Control: &ControlFlow{Count: 3, Steps: []Action{{Type: "control_break"}}}}}}}}}
	for _, s := range []*Script{{Init: []Action{a}}, {Actions: []Action{a}}, {Teardown: []Action{a}}} {
		f := RequiredCompositionRuntimeFeatures(s)
		if len(f) != 1 || f[0] != FeatureParallelLoopControlV1 {
			t.Fatal(f)
		}
	}
	if len(RequiredCompositionRuntimeFeatures(nil)) != 0 || len(RequiredCompositionRuntimeFeatures(&Script{Actions: []Action{{Type: "control_break"}}})) != 0 {
		t.Fatal("unrelated script requires parallel feature")
	}
	llmFeatures := RequiredCompositionRuntimeFeatures(&Script{
		Config:      ScriptConfig{Pacing: PacingConfig{ArrivalProcess: "poisson"}},
		DataSources: []DataSource{{Name: "prompts", Type: "jsonl", Path: "prompts.jsonl"}},
		Actions:     []Action{{Type: "llm_request", Target: "/v1/chat/completions"}},
	})
	if len(llmFeatures) != 3 || llmFeatures[0] != FeatureLLMInferenceV1 || llmFeatures[1] != FeatureOpenArrivalV2 || llmFeatures[2] != FeatureDataJSONLV1 {
		t.Fatalf("unexpected llm/arrival/jsonl features: %v", llmFeatures)
	}
}

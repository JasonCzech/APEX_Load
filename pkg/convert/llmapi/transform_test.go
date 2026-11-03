package llmapi

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAnalyzeAndTransformLLMCalls(t *testing.T) {
	script := &dsl.Script{
		Name: "Captured Chat Session",
		Config: dsl.ScriptConfig{
			BaseURL: "https://my-resource.openai.azure.com",
		},
		Actions: []dsl.Action{
			{
				Name:   "Get User Profile",
				Type:   "http_request",
				Method: "GET",
				Target: "https://api.example.com/v1/profile",
			},
			{
				Name:   "Azure Chat Completion",
				Type:   "http_request",
				Method: "POST",
				Target: "/openai/deployments/gpt-4o-mini/chat/completions?api-version=2024-10-21",
				Headers: map[string]string{
					"Content-Type": "application/json",
					"api-key":      "sk-live-secret-12345",
				},
				Body: `{"messages":[{"role":"user","content":"{{data.prompts.question}}"}],"stream":true}`,
			},
			{
				Name: "Nested RAG Turn",
				Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 1,
					Steps: []dsl.Action{
						{
							Name:   "vLLM Chat",
							Type:   "http_request",
							Method: "POST",
							Target: "http://vllm:8000/v1/chat/completions",
							Headers: map[string]string{
								"Authorization": "Bearer sk-raw-token-abc",
							},
							Body: `{"model":"meta-llama/Llama-3.1-8B-Instruct","messages":[{"role":"user","content":"Hello"}],"stream":false}`,
						},
					},
				},
			},
		},
	}

	rev := Analyze(script)
	if !rev.Detected || !rev.Streaming || len(rev.Calls) != 2 {
		t.Fatalf("unexpected review: %+v", rev)
	}
	if rev.Calls[0].Dialect != "azure_openai_chat" || rev.Calls[0].Model != "gpt-4o-mini" || !rev.Calls[0].Stream {
		t.Fatalf("unexpected first call: %+v", rev.Calls[0])
	}
	if rev.Calls[1].Dialect != "openai_chat" || rev.Calls[1].Model != "meta-llama/Llama-3.1-8B-Instruct" || rev.Calls[1].Stream {
		t.Fatalf("unexpected second call: %+v", rev.Calls[1])
	}

	changes := Transform(script)
	if len(changes) != 4 {
		t.Fatalf("expected 4 changes (2 upgrades + 2 auth scrubs), got %d: %+v", len(changes), changes)
	}
	if script.Actions[0].Type != "http_request" {
		t.Fatalf("non-LLM action should remain http_request, got %s", script.Actions[0].Type)
	}
	az := script.Actions[1]
	if az.Type != "llm_request" || az.Params["dialect"] != "azure_openai_chat" || az.Params["stream"] != true || az.BodyTemplateEncoding != "json" {
		t.Fatalf("unexpected transformed Azure action: %+v", az)
	}
	if az.Headers["api-key"] != "{{secret.llm_api_key}}" {
		t.Fatalf("expected scrubbed api-key header, got %q", az.Headers["api-key"])
	}
	vllm := script.Actions[2].Control.Steps[0]
	if vllm.Type != "llm_request" || vllm.Params["dialect"] != "openai_chat" || vllm.Headers["Authorization"] != "Bearer {{secret.llm_api_key}}" {
		t.Fatalf("unexpected transformed vLLM action: %+v", vllm)
	}

	// Idempotence: second Transform call should produce 0 changes.
	if second := Transform(script); len(second) != 0 {
		t.Fatalf("expected idempotent second Transform, got %+v", second)
	}
}

func TestAnalyzeAndTransformStandardRequestActionAndHeaderList(t *testing.T) {
	script := &dsl.Script{
		Name:     "cURL Import",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.openai.com"},
		Actions: []dsl.Action{
			{
				Name:   "POST /v1/chat/completions",
				Type:   "request",
				Method: "POST",
				Target: "/v1/chat/completions",
				HeaderList: dsl.HeaderList{
					{Name: "Accept", Value: "text/event-stream"},
					{Name: "Authorization", Value: "Bearer sk-secret-headerlist"},
				},
				Body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
			},
			{
				Name:   "POST /v1/responses",
				Type:   "POST",
				Target: "/v1/responses",
				Body:   `{"model":"gpt-4o","input":"hi"}`,
			},
		},
	}

	rev := Analyze(script)
	if !rev.Detected || !rev.Streaming || len(rev.Calls) != 2 {
		t.Fatalf("unexpected review: %+v", rev)
	}
	changes := Transform(script)
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}
	if script.Actions[0].Type != "llm_request" || script.Actions[0].HeaderList[1].Value != "Bearer {{secret.llm_api_key}}" {
		t.Fatalf("expected upgraded action and scrubbed HeaderList, got %+v", script.Actions[0])
	}
	if script.Actions[1].Type != "llm_request" || script.Actions[1].Method != "POST" {
		t.Fatalf("expected upgraded POST action with Method=POST, got %+v", script.Actions[1])
	}
}

package dataset

import (
	"strings"
	"testing"
)

func TestNormalizeShareGPTAndOpenAIChat(t *testing.T) {
	sharegptJSON := `[
		{
			"id": "sg-1",
			"conversations": [
				{"from": "system", "value": "You are a helpful SRE."},
				{"from": "human", "value": "Contact alice@example.com about KV cache."},
				{"from": "gpt", "value": "KV cache stores attention keys and values across decode steps."},
				{"from": "human", "value": "How does PagedAttention help?"},
				{"from": "gpt", "value": "It eliminates external fragmentation via non-contiguous blocks."}
			]
		}
	]`
	res, err := Normalize([]byte(sharegptJSON), ImportOptions{
		Format: "auto",
		Redact: func(s string) string {
			return strings.ReplaceAll(s, "alice@example.com", "[REDACTED_EMAIL]")
		},
	})
	if err != nil {
		t.Fatalf("Normalize ShareGPT: %v", err)
	}
	if res.Format != "sharegpt" || res.TotalRecords != 2 || res.Conversations != 1 {
		t.Fatalf("unexpected ShareGPT result: %+v", res)
	}
	if strings.Contains(res.NormalizedJSONL, "alice@example.com") || !strings.Contains(res.NormalizedJSONL, "[REDACTED_EMAIL]") {
		t.Fatalf("expected PII redaction in normalized JSONL: %s", res.NormalizedJSONL)
	}
	if len(res.Records[1].Messages) != 4 {
		t.Fatalf("expected 4 cumulative messages in turn 1, got %d", len(res.Records[1].Messages))
	}

	openaiJSONL := strings.Join([]string{
		`{"messages":[{"role":"system","content":"Be concise."},{"role":"user","content":"What is TPOT?"},{"role":"assistant","content":"Time per output token."}]}`,
	}, "\n")
	res2, err := Normalize([]byte(openaiJSONL), ImportOptions{Format: "auto"})
	if err != nil {
		t.Fatalf("Normalize OpenAI chat: %v", err)
	}
	if res2.Format != "openai_chat" || res2.TotalRecords != 1 || res2.Records[0].ExpectedOutputTokens <= 0 {
		t.Fatalf("unexpected OpenAI chat result: %+v", res2)
	}
}

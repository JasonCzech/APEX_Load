package dataset

import (
	"fmt"
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

// A conversation repeats its history in every turn's record, so the output can
// grow quadratically: the record limit applies inside a conversation, and the
// output stops at its size limit with the truncation reported.
func TestNormalizeBoundsShareGPTExpansion(t *testing.T) {
	turn := strings.Repeat("word ", 1000)
	var convs []string
	for i := 0; i < 400; i++ {
		convs = append(convs, fmt.Sprintf(`{"from":"human","value":%q},{"from":"gpt","value":"ok"}`, turn+fmt.Sprint(i)))
	}
	payload := `{"id":"c1","conversations":[` + strings.Join(convs, ",") + `]}`
	res, err := Normalize([]byte(payload), ImportOptions{MaxRecords: 5})
	if err != nil || res.TotalRecords != 5 || !res.Truncated || res.TruncationReason != TruncatedMaxRecords {
		t.Fatalf("max_records must cap the turns of one conversation: %v %+v", err, res)
	}
	res, err = Normalize([]byte(payload), ImportOptions{})
	if err != nil || !res.Truncated || res.TruncationReason != TruncatedMaxBytes || len(res.NormalizedJSONL) > MaxNormalizedBytes || res.TotalRecords == 0 || res.TotalRecords != len(res.Records) {
		t.Fatalf("a quadratic expansion must stop at the size limit and say so: %v truncated=%v reason=%q bytes=%d", err, res.Truncated, res.TruncationReason, len(res.NormalizedJSONL))
	}
	if strings.Count(res.NormalizedJSONL, "\n") != res.TotalRecords {
		t.Fatalf("records and JSONL lines disagree: %d vs %d", res.TotalRecords, strings.Count(res.NormalizedJSONL, "\n"))
	}
	if res, err := Normalize([]byte(payload), ImportOptions{MaxTurns: 3}); err != nil || res.TotalRecords != 3 || res.Truncated {
		t.Fatalf("max_turns keeps it small and whole: %v %+v", err, res)
	}
}

// A realistic multi-turn upload near the import route's 16 MiB limit is
// imported (truncated if it must be), not refused.
func TestNormalizeRealisticShareGPTUpload(t *testing.T) {
	human, gpt := strings.Repeat("q", 300), strings.Repeat("a", 1200)
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; b.Len() < 15<<20; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"c%d","conversations":[`, i)
		for turn := 0; turn < 8; turn++ {
			if turn > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"from":"human","value":"%s%d"},{"from":"gpt","value":"%s"}`, human, turn, gpt)
		}
		b.WriteString(`]}`)
	}
	b.WriteByte(']')
	res, err := Normalize([]byte(b.String()), ImportOptions{})
	if err != nil || res.TotalRecords == 0 || len(res.NormalizedJSONL) > MaxNormalizedBytes {
		t.Fatalf("a 15 MiB ShareGPT upload must import: %v", err)
	}
	if !res.Truncated || res.TruncationReason != TruncatedMaxBytes {
		t.Fatalf("this upload expands past the output limit and must report it, got truncated=%v reason=%q", res.Truncated, res.TruncationReason)
	}
}

// Escaping can push the JSONL past the size limit after the budget's
// estimate admitted the records: the kept records and their stats agree.
func TestNormalizeEscapeTruncationKeepsStatsConsistent(t *testing.T) {
	text := strings.Repeat("<", 1<<20) // each < escapes to six bytes
	var lines []string
	for i := 0; i < 12; i++ {
		// Items share an id: conversations are counted per item, on the
		// truncated path as on every other.
		lines = append(lines, fmt.Sprintf(`{"id":"shared","prompt":%q}`, text))
	}
	res, err := Normalize([]byte(strings.Join(lines, "\n")), ImportOptions{})
	if err != nil || !res.Truncated || res.TruncationReason != TruncatedMaxBytes {
		t.Fatalf("want an escape-driven truncation: %v %+v", err, res != nil && res.Truncated)
	}
	if res.Conversations != len(res.Records) || res.TotalRecords != len(res.Records) || len(res.NormalizedJSONL) > MaxNormalizedBytes {
		t.Fatalf("conversations=%d total=%d records=%d bytes=%d, want one conversation per kept item", res.Conversations, res.TotalRecords, len(res.Records), len(res.NormalizedJSONL))
	}
}

func TestNormalizeReportsJSONLRecordLimit(t *testing.T) {
	payload := "{\"prompt\":\"a\"}\n{\"prompt\":\"b\"}\n{\"prompt\":\"c\"}\n"
	res, err := Normalize([]byte(payload), ImportOptions{MaxRecords: 2})
	if err != nil || res.TotalRecords != 2 || !res.Truncated || res.TruncationReason != TruncatedMaxRecords {
		t.Fatalf("JSONL beyond max_records must be reported truncated: %v %+v", err, res)
	}
	res, err = Normalize([]byte(payload), ImportOptions{MaxRecords: 3})
	if err != nil || res.TotalRecords != 3 || res.Truncated {
		t.Fatalf("a dataset that fits is not truncated: %v %+v", err, res)
	}
}

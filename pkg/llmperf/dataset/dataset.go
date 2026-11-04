package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Message represents a canonical chat message in a normalized prompt dataset.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Record is one canonical JSONL prompt-dataset entry.
type Record struct {
	PromptID             string    `json:"prompt_id"`
	ConversationID       string    `json:"conversation_id,omitempty"`
	TurnIndex            int       `json:"turn_index"`
	Prompt               string    `json:"prompt"`
	Messages             []Message `json:"messages"`
	EstimatedInputTokens int       `json:"estimated_input_tokens"`
	ExpectedOutputTokens int       `json:"expected_output_tokens,omitempty"`
	InputBand            string    `json:"input_band"`
}

// Normalization bounds. A multi-turn conversation repeats its history in every
// turn's record, so the normalized output can grow quadratically in the
// input. The record count is enforced inside a conversation, and the output
// stops at MaxNormalizedBytes; either limit truncates the dataset, which the
// result reports, instead of exhausting memory.
const (
	MaxRecords         = 50000
	MaxNormalizedBytes = 32 << 20
)

// Truncation reasons reported in ImportResult.TruncationReason.
const (
	TruncatedMaxRecords = "max_records"
	TruncatedMaxBytes   = "max_bytes"
)

// outputBudget tracks the records and the (approximate, pre-escaping) output
// bytes a normalization has produced, and why it stopped, if it did.
type outputBudget struct {
	records    int
	maxRecords int
	bytes      int64
	truncated  string
}

// admit charges one record. It reports false, recording why, once the record
// limit is reached or the output would exceed MaxNormalizedBytes.
func (b *outputBudget) admit(rec *Record) bool {
	if b.truncated != "" {
		return false
	}
	if b.records >= b.maxRecords {
		b.truncated = TruncatedMaxRecords
		return false
	}
	size := int64(len(rec.PromptID) + len(rec.ConversationID) + len(rec.Prompt) + 128)
	for _, m := range rec.Messages {
		size += int64(len(m.Role) + len(m.Content) + 32)
	}
	if b.bytes+size > MaxNormalizedBytes {
		b.truncated = TruncatedMaxBytes
		return false
	}
	b.bytes += size
	b.records++
	return true
}

// ImportOptions configures dataset normalization.
type ImportOptions struct {
	Format        string // "auto", "sharegpt", "openai_chat", "jsonl"
	MaxTurns      int    // if > 0, cap multi-turn conversations
	BytesPerToken float64
	Redact        func(string) string
	MaxRecords    int
}

// ImportResult holds the normalized records, JSONL payload, and summary statistics.
type ImportResult struct {
	Format                   string         `json:"format"`
	TotalRecords             int            `json:"total_records"`
	Conversations            int            `json:"conversations"`
	MeanInputTokens          float64        `json:"mean_input_tokens"`
	MaxInputTokens           int            `json:"max_input_tokens"`
	MeanExpectedOutputTokens float64        `json:"mean_expected_output_tokens"`
	InputBandCounts          map[string]int `json:"input_band_counts"`
	// Truncated reports a dataset cut at a limit: TruncationReason is
	// max_records (the record limit) or max_bytes (the output size limit;
	// set max_turns or max_records to keep whole conversations instead).
	Truncated        bool     `json:"truncated,omitempty"`
	TruncationReason string   `json:"truncation_reason,omitempty"`
	Records          []Record `json:"records"`
	NormalizedJSONL  string   `json:"normalized_jsonl"`
}

// ClassifyInputBand maps a prompt token count into the 4 canonical input bands.
func ClassifyInputBand(tokens int) string {
	switch {
	case tokens < 512:
		return "short"
	case tokens < 2048:
		return "medium"
	case tokens < 8192:
		return "long"
	default:
		return "very_long"
	}
}

func estimateTokens(text string, bytesPerToken float64) int {
	if bytesPerToken <= 0 || math.IsNaN(bytesPerToken) || math.IsInf(bytesPerToken, 0) {
		bytesPerToken = 4.0
	}
	n := len(strings.TrimSpace(text))
	if n == 0 {
		return 0
	}
	est := int(math.Ceil(float64(n) / bytesPerToken))
	if est < 1 {
		est = 1
	}
	return est
}

// Normalize parses raw dataset bytes (JSON array or JSONL) and normalizes them into canonical Records.
func Normalize(raw []byte, opts ImportOptions) (*ImportResult, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("dataset payload is empty")
	}
	maxRecords := opts.MaxRecords
	if maxRecords <= 0 || maxRecords > MaxRecords {
		maxRecords = MaxRecords
	}
	bpt := opts.BytesPerToken
	if bpt <= 0 {
		bpt = 4.0
	}
	redact := opts.Redact
	if redact == nil {
		redact = func(s string) string { return s }
	}

	var rawItems []map[string]any
	inputCapped := false // lines remained after the record limit
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &rawItems); err != nil {
			return nil, fmt.Errorf("parse JSON array dataset: %w", err)
		}
	} else {
		lines := bytes.Split(trimmed, []byte{'\n'})
		for idx, line := range lines {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var item map[string]any
			if err := json.Unmarshal(line, &item); err != nil {
				return nil, fmt.Errorf("parse JSONL line %d: %w", idx+1, err)
			}
			rawItems = append(rawItems, item)
			if len(rawItems) >= maxRecords {
				for _, rest := range lines[idx+1:] {
					if len(bytes.TrimSpace(rest)) > 0 {
						inputCapped = true
						break
					}
				}
				break
			}
		}
	}
	if len(rawItems) == 0 {
		return nil, fmt.Errorf("no records found in dataset")
	}

	detectedFormat := strings.ToLower(strings.TrimSpace(opts.Format))
	if detectedFormat == "" || detectedFormat == "auto" {
		detectedFormat = sniffFormat(rawItems[0])
	}

	var records []Record
	// recordItem is each record's source item, so a truncation can count the
	// conversations it kept the same way the import counts them: per item.
	var recordItem []int
	conversations := 0
	budget := &outputBudget{maxRecords: maxRecords}
	for itemIdx, item := range rawItems {
		if budget.records >= maxRecords || budget.truncated == TruncatedMaxBytes {
			if budget.truncated == "" {
				budget.truncated = TruncatedMaxRecords
			}
			break
		}
		convID := stringField(item, "id", "conversation_id", "prompt_id")
		if convID == "" {
			convID = fmt.Sprintf("conv-%d", itemIdx+1)
		}
		switch detectedFormat {
		case "sharegpt":
			turns := extractShareGPTTurns(item, convID, opts.MaxTurns, bpt, redact, budget)
			if len(turns) > 0 {
				conversations++
				records = append(records, turns...)
				for range turns {
					recordItem = append(recordItem, itemIdx)
				}
			}
		case "openai_chat":
			rec, ok := extractOpenAIChatRecord(item, convID, itemIdx, bpt, redact)
			if ok && budget.admit(&rec) {
				conversations++
				records = append(records, rec)
				recordItem = append(recordItem, itemIdx)
			}
		default:
			rec, ok := extractGenericRecord(item, convID, itemIdx, bpt, redact)
			if ok && budget.admit(&rec) {
				conversations++
				records = append(records, rec)
				recordItem = append(recordItem, itemIdx)
			}
		}
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no valid prompt records could be normalized from %s dataset", detectedFormat)
	}
	if len(records) > maxRecords {
		records = records[:maxRecords]
	}

	bandCounts := map[string]int{
		"short":     0,
		"medium":    0,
		"long":      0,
		"very_long": 0,
	}
	var totalIn, totalOut, outCount, maxIn int
	var jsonlBuf strings.Builder
	for i := range records {
		r := &records[i]
		r.InputBand = ClassifyInputBand(r.EstimatedInputTokens)
		line, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("marshal normalized record %d: %w", i, err)
		}
		if i > 0 && jsonlBuf.Len()+len(line)+1 > MaxNormalizedBytes {
			// JSON escaping can outgrow the budget's estimate: keep the
			// records written so far, and count only their source items.
			records = records[:i]
			conversations = 0
			for j := range records {
				if j == 0 || recordItem[j] != recordItem[j-1] {
					conversations++
				}
			}
			budget.truncated = TruncatedMaxBytes
			break
		}
		jsonlBuf.Write(line)
		jsonlBuf.WriteByte('\n')
		bandCounts[r.InputBand]++
		totalIn += r.EstimatedInputTokens
		if r.EstimatedInputTokens > maxIn {
			maxIn = r.EstimatedInputTokens
		}
		if r.ExpectedOutputTokens > 0 {
			totalOut += r.ExpectedOutputTokens
			outCount++
		}
	}

	truncation := budget.truncated
	if truncation == "" && inputCapped {
		truncation = TruncatedMaxRecords
	}

	meanIn := float64(totalIn) / float64(len(records))
	var meanOut float64
	if outCount > 0 {
		meanOut = float64(totalOut) / float64(outCount)
	}

	return &ImportResult{
		Format:                   detectedFormat,
		TotalRecords:             len(records),
		Conversations:            conversations,
		MeanInputTokens:          meanIn,
		MaxInputTokens:           maxIn,
		MeanExpectedOutputTokens: meanOut,
		InputBandCounts:          bandCounts,
		Truncated:                truncation != "",
		TruncationReason:         truncation,
		Records:                  records,
		NormalizedJSONL:          jsonlBuf.String(),
	}, nil
}

func sniffFormat(first map[string]any) string {
	if _, ok := first["conversations"]; ok {
		return "sharegpt"
	}
	if _, ok := first["messages"]; ok {
		return "openai_chat"
	}
	return "jsonl"
}

func extractShareGPTTurns(item map[string]any, convID string, maxTurns int, bpt float64, redact func(string) string, budget *outputBudget) []Record {
	rawConvs, ok := item["conversations"].([]any)
	if !ok || len(rawConvs) == 0 {
		return nil
	}
	var history []Message
	var out []Record
	turnIdx := 0
	for i := 0; i < len(rawConvs); i++ {
		m, ok := rawConvs[i].(map[string]any)
		if !ok {
			continue
		}
		from := strings.ToLower(strings.TrimSpace(stringField(m, "from", "role")))
		val := redact(strings.TrimSpace(stringField(m, "value", "content")))
		if val == "" {
			continue
		}
		switch from {
		case "system":
			history = append(history, Message{Role: "system", Content: val})
		case "human", "user":
			history = append(history, Message{Role: "user", Content: val})
			expectedOut := 0
			if i+1 < len(rawConvs) {
				if next, ok := rawConvs[i+1].(map[string]any); ok {
					nextFrom := strings.ToLower(strings.TrimSpace(stringField(next, "from", "role")))
					if nextFrom == "gpt" || nextFrom == "assistant" || nextFrom == "model" {
						reply := redact(strings.TrimSpace(stringField(next, "value", "content")))
						expectedOut = estimateTokens(reply, bpt)
					}
				}
			}
			msgsCopy := make([]Message, len(history))
			copy(msgsCopy, history)
			inTokens := estimateMessagesTokens(msgsCopy, bpt)
			rec := Record{
				PromptID:             fmt.Sprintf("%s-t%d", convID, turnIdx),
				ConversationID:       convID,
				TurnIndex:            turnIdx,
				Prompt:               val,
				Messages:             msgsCopy,
				EstimatedInputTokens: inTokens,
				ExpectedOutputTokens: expectedOut,
				InputBand:            ClassifyInputBand(inTokens),
			}
			if !budget.admit(&rec) {
				return out
			}
			out = append(out, rec)
			turnIdx++
			if maxTurns > 0 && turnIdx >= maxTurns {
				return out
			}
		case "gpt", "assistant", "model":
			history = append(history, Message{Role: "assistant", Content: val})
		}
	}
	return out
}

func extractOpenAIChatRecord(item map[string]any, convID string, idx int, bpt float64, redact func(string) string) (Record, bool) {
	rawMsgs, ok := item["messages"].([]any)
	if !ok || len(rawMsgs) == 0 {
		return Record{}, false
	}
	var promptMsgs []Message
	var lastUserPrompt string
	var expectedOut int
	for i, rm := range rawMsgs {
		m, ok := rm.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringField(m, "role", "from")))
		content := redact(strings.TrimSpace(stringField(m, "content", "value")))
		if content == "" {
			continue
		}
		if role == "assistant" && i == len(rawMsgs)-1 {
			expectedOut = estimateTokens(content, bpt)
			continue
		}
		if role == "user" {
			lastUserPrompt = content
		}
		promptMsgs = append(promptMsgs, Message{Role: role, Content: content})
	}
	if len(promptMsgs) == 0 {
		return Record{}, false
	}
	if lastUserPrompt == "" {
		lastUserPrompt = promptMsgs[len(promptMsgs)-1].Content
	}
	inTokens := estimateMessagesTokens(promptMsgs, bpt)
	return Record{
		PromptID:             fmt.Sprintf("p-%d", idx+1),
		ConversationID:       convID,
		TurnIndex:            0,
		Prompt:               lastUserPrompt,
		Messages:             promptMsgs,
		EstimatedInputTokens: inTokens,
		ExpectedOutputTokens: expectedOut,
		InputBand:            ClassifyInputBand(inTokens),
	}, true
}

func extractGenericRecord(item map[string]any, convID string, idx int, bpt float64, redact func(string) string) (Record, bool) {
	if _, hasMsgs := item["messages"]; hasMsgs {
		return extractOpenAIChatRecord(item, convID, idx, bpt, redact)
	}
	prompt := redact(strings.TrimSpace(stringField(item, "prompt", "input", "question", "text")))
	if prompt == "" {
		return Record{}, false
	}
	msgs := []Message{{Role: "user", Content: prompt}}
	if sys := redact(strings.TrimSpace(stringField(item, "system", "system_prompt"))); sys != "" {
		msgs = []Message{{Role: "system", Content: sys}, {Role: "user", Content: prompt}}
	}
	expectedOut := intField(item, "expected_output_tokens", "max_tokens", "output_tokens")
	if expectedOut <= 0 {
		if completion := redact(strings.TrimSpace(stringField(item, "completion", "output", "response", "answer"))); completion != "" {
			expectedOut = estimateTokens(completion, bpt)
		}
	}
	promptID := stringField(item, "prompt_id", "id")
	if promptID == "" {
		promptID = fmt.Sprintf("p-%d", idx+1)
	}
	turnIdx := intField(item, "turn_index")
	inTokens := estimateMessagesTokens(msgs, bpt)
	return Record{
		PromptID:             promptID,
		ConversationID:       convID,
		TurnIndex:            turnIdx,
		Prompt:               prompt,
		Messages:             msgs,
		EstimatedInputTokens: inTokens,
		ExpectedOutputTokens: expectedOut,
		InputBand:            ClassifyInputBand(inTokens),
	}, true
}

func estimateMessagesTokens(msgs []Message, bpt float64) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Content, bpt) + 4
	}
	return total
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func intField(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			case json.Number:
				if i, err := n.Int64(); err == nil {
					return int(i)
				}
			}
		}
	}
	return 0
}

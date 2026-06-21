package eventhubs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// ---- config / param value helpers ----

func stringValue(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

func boolValue(cfg map[string]any, key string) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return false
}

func durationValue(cfg map[string]any, key string, def time.Duration) time.Duration {
	switch v := cfg[key].(type) {
	case float64:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	case int:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	}
	return def
}

func paramString(params map[string]any, key, fallback string) string {
	if params == nil {
		return fallback
	}
	if v, ok := params[key].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// ---- telemetry ----

func finalizeTelemetry(result *protocol.Result, sent event, hasSent bool, received event, hasReceived bool) *protocol.ServerTelemetry {
	if result == nil || (!hasSent && !hasReceived) {
		return nil
	}
	t := &protocol.ServerTelemetry{TransferMs: durationToMs(result.Duration)}
	if hasSent {
		t.MessagesSent = 1
		t.RequestPayloadKB = bytesToKB(len(sent.Body))
		t.RequestSizeClass = protocol.ClassifyPayloadSize(int64(len(sent.Body)))
	}
	if hasReceived {
		t.MessagesReceived = 1
		t.ResponsePayloadKB = bytesToKB(len(received.Body))
		t.ResponseSizeClass = protocol.ClassifyPayloadSize(int64(len(received.Body)))
		observedAt := result.StartTime.Add(result.Duration)
		if !received.EnqueuedAt.IsZero() && !observedAt.Before(received.EnqueuedAt) {
			t.ConsumerLagMs = durationToMs(observedAt.Sub(received.EnqueuedAt))
		}
		if lag, err := strconv.ParseInt(result.Extractions["eventhubs_consumer_lag"], 10, 64); err == nil && lag > 0 {
			t.ConsumerOffsetLag = lag
		}
	}
	t.MessagesSentPerSec = perSec(t.MessagesSent, result.Duration)
	t.MessagesReceivedPerSec = perSec(t.MessagesReceived, result.Duration)
	return t
}

func durationToMs(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(d) / float64(time.Millisecond)
}

func bytesToKB(n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n) / 1024
}

func perSec(count int, d time.Duration) float64 {
	if count <= 0 || d <= 0 {
		return 0
	}
	return float64(count) / d.Seconds()
}

// ---- extractions / assertions ----

func addEventExtractions(result *protocol.Result, ev event) {
	result.Extractions["eventhubs_offset"] = strconv.FormatInt(ev.Offset, 10)
	result.Extractions["eventhubs_sequence_number"] = strconv.FormatInt(ev.SequenceNum, 10)
	if ev.PartitionID != "" {
		result.Extractions["eventhubs_partition"] = ev.PartitionID
	}
}

// runExtractors supports body (regex) and property extractors for the observer
// probe; richer JSONPath extraction can layer on later.
func runExtractors(extractors []protocol.Extractor, result *protocol.Result, ev event) error {
	for _, ex := range extractors {
		switch ex.Source {
		case "body":
			if ex.Type == "regex" {
				re, err := regexp.Compile(ex.Expression)
				if err != nil {
					return fmt.Errorf("eventhubs extractor %q: %w", ex.Name, err)
				}
				if m := re.FindStringSubmatch(string(ev.Body)); m != nil {
					if len(m) > 1 {
						result.Extractions[ex.Name] = m[1]
					} else {
						result.Extractions[ex.Name] = m[0]
					}
				}
			}
		case "property":
			if v, ok := ev.Properties[ex.Expression]; ok {
				result.Extractions[ex.Name] = v
			}
		}
	}
	return nil
}

func evaluateAssertions(assertions []protocol.Assertion, result *protocol.Result, ev event) error {
	for _, a := range assertions {
		switch a.Type {
		case "message_received":
			if len(ev.Body) == 0 {
				return fmt.Errorf("assertion message_received failed: empty event")
			}
		case "body_contains":
			if !strings.Contains(string(ev.Body), a.Value) {
				return fmt.Errorf("assertion body_contains %q failed", a.Value)
			}
		case "property_equals":
			want := fmt.Sprint(a.Expected)
			if ev.Properties[a.Value] != want {
				return fmt.Errorf("assertion property_equals %q != %q", a.Value, want)
			}
		case "response_time_ms":
			if limit, err := strconv.ParseFloat(fmt.Sprint(a.Expected), 64); err == nil && durationToMs(result.Duration) > limit {
				return fmt.Errorf("assertion response_time_ms exceeded: %.1f > %.1f", durationToMs(result.Duration), limit)
			}
		}
	}
	return nil
}

package eventhubs

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func boundedInteger(value any, fallback, minimum, maximum int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	var parsed int64
	switch typed := value.(type) {
	case int:
		parsed = int64(typed)
	case int64:
		parsed = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed >= math.MaxInt64 {
			return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
		}
		parsed = int64(typed)
	case string:
		converted, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
		}
		parsed = converted
	default:
		return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
	}
	if parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
	}
	return int(parsed), nil
}

var extractorRegexCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileExtractorRegex(expr string) (*regexp.Regexp, error) {
	if cached, ok := extractorRegexCache.Load(expr); ok {
		return cached, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := extractorRegexCache.LoadOrStore(expr, re)
	return actual, nil
}

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

func finalizeTelemetry(result *protocol.Result, sent event, hasSent bool, received event, hasReceived bool, opErr error) *protocol.ServerTelemetry {
	throttle, serverBusy, quota, tooLarge := classifyProduceError(opErr)
	hasErrSignal := throttle != 0 || serverBusy != 0 || quota != 0 || tooLarge != 0
	if result == nil || (!hasSent && !hasReceived && !hasErrSignal) {
		return nil
	}
	t := &protocol.ServerTelemetry{TransferMs: durationToMs(result.Duration)}
	if hasSent || hasReceived {
		t.Messaging = &protocol.MessagingDiagnostics{Version: protocol.MessagingEvidenceVersion}
	}
	if hasSent {
		t.AcknowledgementMs = sent.AcknowledgementMs
		observation := protocol.MessagingDurationObservation{Metric: "producer_ack", ValueMs: sent.AcknowledgementMs}
		if observation.ValueMs == nil {
			observation.UnavailableReason = "not_observed"
		}
		t.Messaging.Durations = append(t.Messaging.Durations, observation)
		t.MessagesSent = max(1, sent.MessageCount)
		t.RequestPayloadKB = bytesToKB(len(sent.Body))
		t.RequestSizeClass = protocol.ClassifyPayloadSize(int64(len(sent.Body)))
		// PartitionID is populated only when the script pins an explicit partition;
		// partition-key routing is resolved broker-side and left empty (no skew).
		if sent.PartitionID != "" {
			t.PartitionID = sent.PartitionID
		}
	}
	if hasReceived {
		t.PartitionID = received.PartitionID
		t.MessagesReceived = 1
		t.ResponsePayloadKB = bytesToKB(len(received.Body))
		t.ResponseSizeClass = protocol.ClassifyPayloadSize(int64(len(received.Body)))
		observedAt := result.StartTime.Add(result.Duration)
		if !received.EnqueuedAt.IsZero() && !observedAt.Before(received.EnqueuedAt) {
			lagMs := durationToMs(observedAt.Sub(received.EnqueuedAt))
			t.ConsumerLagMs = lagMs
			t.ConsumerLagObserved = true
			// For the observer probe, enqueue->observe IS the end-to-end latency.
			t.EndToEndLatencyMs = lagMs
		}
		ageReason := "not_observed"
		if t.ConsumerLagObserved {
			ageReason = "clock_unqualified"
		}
		t.Messaging.Durations = append(t.Messaging.Durations, protocol.MessagingDurationObservation{Metric: "enqueue_age", UnavailableReason: ageReason})
		if lag, err := strconv.ParseInt(result.Extractions["eventhubs_consumer_lag"], 10, 64); err == nil && lag >= 0 {
			t.ConsumerOffsetLag = lag
			t.ConsumerOffsetLagObserved = true
		}
		if result.Extractions["eventhubs_duplicate"] == "1" {
			t.DuplicateCount = 1
		}
		if result.Extractions["eventhubs_out_of_order"] == "1" {
			t.OutOfOrderCount = 1
		}
	}
	t.ThrottledRequests = throttle
	t.ServerBusyCount = serverBusy
	t.QuotaExceededCount = quota
	t.EventTooLargeCount = tooLarge
	t.MessagesSentPerSec = perSec(t.MessagesSent, result.Duration)
	t.MessagesReceivedPerSec = perSec(t.MessagesReceived, result.Duration)
	return t
}

// classifyProduceError maps a produce/consume transport error to Event Hubs
// ingestion-pressure counters. It is intentionally string-based and
// build-agnostic: the real azeventhubs adapter surfaces these as AMQP error
// conditions (amqp:resource-limit-exceeded / ServerBusy / quota) and the
// size-cap rejection as "too large"; the mock/stub builds produce none of these
// strings, so their counters stay honestly zero.
func classifyProduceError(err error) (throttle, serverBusy, quota, tooLarge int) {
	if err == nil {
		return 0, 0, 0, 0
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "too large"):
		tooLarge = 1
	case strings.Contains(msg, "server-busy") || strings.Contains(msg, "server busy") || strings.Contains(msg, "serverbusy"):
		// Event Hubs throttling surfaces as the AMQP condition
		// `com.microsoft:server-busy` (rendered with a hyphen by go-amqp).
		serverBusy, throttle = 1, 1
	case strings.Contains(msg, "resource-limit-exceeded") || strings.Contains(msg, "quota"):
		quota, throttle = 1, 1
	case strings.Contains(msg, "throttl") || strings.Contains(msg, "429"):
		throttle = 1
	}
	return throttle, serverBusy, quota, tooLarge
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

func addEventExtractions(result *protocol.Result, ev event) error {
	for key, value := range map[string]string{
		"eventhubs_offset":          strconv.FormatInt(ev.Offset, 10),
		"eventhubs_sequence_number": strconv.FormatInt(ev.SequenceNum, 10),
	} {
		if err := protocol.StoreExtraction(result, key, value); err != nil {
			result.Extractions = nil
			return fmt.Errorf("eventhubs metadata extraction %q rejected: %w", key, err)
		}
	}
	if ev.PartitionID != "" {
		if err := protocol.StoreExtraction(result, "eventhubs_partition", ev.PartitionID); err != nil {
			result.Extractions = nil
			return fmt.Errorf("eventhubs metadata extraction %q rejected: %w", "eventhubs_partition", err)
		}
	}
	return nil
}

// runExtractors supports body (regex) and property extractors for the observer
// probe; richer JSONPath extraction can layer on later.
func runExtractors(extractors []protocol.Extractor, result *protocol.Result, ev event) error {
	for _, ex := range extractors {
		switch ex.Source {
		case "body":
			if ex.Type == "regex" {
				re, err := compileExtractorRegex(ex.Expression)
				if err != nil {
					return fmt.Errorf("eventhubs extractor %q: %w", ex.Name, err)
				}
				if m := re.FindStringSubmatch(string(ev.Body)); m != nil {
					value := m[0]
					if len(m) > 1 {
						value = m[1]
					}
					if err := protocol.StoreExtraction(result, ex.Name, value); err != nil {
						result.Extractions = nil
						return fmt.Errorf("eventhubs extractor %q output rejected: %w", ex.Name, err)
					}
				}
			}
		case "property":
			if v, ok := ev.Properties[ex.Expression]; ok {
				if err := protocol.StoreExtraction(result, ex.Name, v); err != nil {
					result.Extractions = nil
					return fmt.Errorf("eventhubs extractor %q output rejected: %w", ex.Name, err)
				}
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

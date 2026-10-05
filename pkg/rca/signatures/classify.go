package signatures

import (
	"strings"
	"unicode"
)

// Queue kinds Little's law reads at a broker hop.
const (
	KindQueueDepth    = "queue_depth"
	KindArrivalRate   = "arrival_rate"
	KindResidenceTime = "residence_time"
)

// classifiers are tried in order, the most specific first, so a JVM GC
// metric is gc rather than memory and a consumer-lag metric is not a queue
// depth. Each matches whole name tokens or a phrase of joined tokens.
var classifiers = []struct {
	kind    string
	tokens  []string
	phrases []string
}{
	{KindDeadLetters, []string{"dlq", "deadletter", "deadletters"}, []string{"dead_letter"}},
	{KindRebalance, []string{"rebalance", "rebalances", "rebalancing"}, nil},
	{KindConsumerLag, []string{"lag"}, []string{"consumer_lag", "records_lag", "offset_lag"}},
	{KindRetries, []string{"retry", "retries", "retried", "redelivered", "redeliveries"}, nil},
	{KindGC, []string{"gc", "garbage"}, []string{"suspension_time", "collection_time"}},
	{KindScaling, []string{"replica", "replicas", "restart", "restarts", "oom", "coldstart", "scaling"}, []string{"cold_start", "oom_kill"}},
	{KindResidenceTime, []string{"residence", "sojourn"}, []string{"time_in_queue", "message_age", "queue_time"}},
	{KindArrivalRate, []string{"enqueue", "enqueued", "arrival", "arrivals", "incoming"}, []string{"publish_rate", "produce_rate"}},
	{KindQueueDepth, []string{"depth", "backlog", "queued"}, []string{"queue_size", "messages_ready", "queue_length"}},
	{KindPool, []string{"pool"}, []string{"active_connections", "connections_active", "pending_acquire", "threads_busy"}},
	{KindDBWait, []string{"deadlock", "deadlocks"}, []string{"lock_wait", "db_wait", "row_lock", "buffer_wait", "query_wait"}},
	{KindCPU, []string{"cpu", "processor"}, nil},
	{KindMemory, []string{"memory", "mem", "heap", "rss"}, nil},
}

// Classify names the evidence kind a provider series measures from its
// metric name, or "" when no kind applies. It is a deterministic keyword
// match; the evaluation corpus checks it with the signatures.
func Classify(name string) string {
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	joined := "_" + strings.Join(tokens, "_") + "_"
	set := map[string]bool{}
	for _, t := range tokens {
		set[t] = true
	}
	for _, c := range classifiers {
		for _, t := range c.tokens {
			if set[t] {
				return c.kind
			}
		}
		for _, p := range c.phrases {
			if strings.Contains(joined, "_"+p+"_") {
				return c.kind
			}
		}
	}
	return ""
}

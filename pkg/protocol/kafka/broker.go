package kafka

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// kafkaClient abstracts the Kafka transport so the protocol can run against an
// in-memory mock broker (for local shakeout and CI) or, in a network-enabled
// build, a real Kafka cluster. The real adapter is intentionally a stub in this
// build (see unavailableClient); swapping in a franz-go-backed implementation is
// a single constructor change in Init.
type kafkaClient interface {
	Produce(ctx context.Context, topic string, rec kafkaRecord) (recordMeta, error)
	Consume(ctx context.Context, topic string, group string, timeout time.Duration) (kafkaRecord, int64, error)
	RequestReply(ctx context.Context, reqTopic, replyTopic string, req kafkaRecord, timeout time.Duration) (recordMeta, kafkaRecord, error)
	Commit(ctx context.Context) error
	Seek(ctx context.Context, topic, group, target string) error
	HealthCheck(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// kafkaRecord is a provider-neutral representation of a Kafka record.
type kafkaRecord struct {
	Key           string
	Value         []byte
	Headers       map[string]string
	Topic         string
	Partition     int32
	Offset        int64
	Timestamp     time.Time
	CorrelationID string
}

// recordMeta is the broker acknowledgement returned by a produce.
type recordMeta struct {
	Topic     string
	Partition int32
	Offset    int64
	Timestamp time.Time
}

// memoryBroker is a process-local broker keyed by topic. Each topic is an
// append-only commit log (offset = index, records are never removed), and each
// (topic, consumer-group) pair has an independent read position. This mirrors a
// real cluster closely enough to model offset lag, non-destructive reads, seek,
// and replay. Reads default to the earliest offset so produce-then-consume
// flows work without an explicit seek.
//
// The log is append-only and never trimmed (unlike a destructive queue), so it
// grows for the process lifetime. This is acceptable because mock mode is
// strictly for local shakeout/CI (real load uses the kafka_real adapter); do not
// use the mock for sustained high-volume runs.
type memoryBroker struct {
	mu      sync.Mutex
	logs    map[string][]kafkaRecord // append-only per topic; offset == index
	cursors map[string]int64         // per cursorKey(topic, group): next read offset
}

func newMemoryBroker() *memoryBroker {
	return &memoryBroker{
		logs:    make(map[string][]kafkaRecord),
		cursors: make(map[string]int64),
	}
}

func cursorKey(topic, group string) string { return topic + "\x00" + group }

// sharedBroker lets multiple VUser goroutines in a single process exercise the
// same mock topics (e.g. one VUser produces, another consumes).
var sharedBroker = newMemoryBroker()

func (b *memoryBroker) produce(topic string, rec kafkaRecord) recordMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	offset := int64(len(b.logs[topic]))
	rec.Topic = topic
	rec.Offset = offset
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	b.logs[topic] = append(b.logs[topic], rec)
	return recordMeta{Topic: topic, Partition: rec.Partition, Offset: offset, Timestamp: rec.Timestamp}
}

// consume returns the next unread record for (topic, group) without removing it
// from the log (offset-based, like a real consumer), advances the group's read
// position, and returns the remaining offset lag (records still behind the tail).
func (b *memoryBroker) consume(topic, group string) (kafkaRecord, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	log := b.logs[topic]
	key := cursorKey(topic, group)
	pos := b.cursors[key]
	if pos >= int64(len(log)) {
		return kafkaRecord{}, 0, false
	}
	rec := log[pos]
	b.cursors[key] = pos + 1
	lag := int64(len(log)) - (pos + 1)
	return rec, lag, true
}

func (b *memoryBroker) hasMessages(topic, group string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cursors[cursorKey(topic, group)] < int64(len(b.logs[topic]))
}

// seek repositions the (topic, group) consumer to earliest, latest, or an
// explicit offset, returning the resolved offset.
func (b *memoryBroker) seek(topic, group, target string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	end := int64(len(b.logs[topic]))
	key := cursorKey(topic, group)
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", "earliest", "beginning", "start":
		b.cursors[key] = 0
		return 0, nil
	case "latest", "end":
		b.cursors[key] = end
		return end, nil
	default:
		n, err := strconv.ParseInt(strings.TrimSpace(target), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid kafka seek offset %q (use earliest, latest, or a number)", target)
		}
		if n < 0 {
			n = 0
		}
		if n > end {
			n = end
		}
		b.cursors[key] = n
		return n, nil
	}
}

// mockClient adapts the in-memory broker to the kafkaClient interface.
type mockClient struct {
	broker *memoryBroker
}

func (c mockClient) Produce(_ context.Context, topic string, rec kafkaRecord) (recordMeta, error) {
	if topic == "" {
		return recordMeta{}, fmt.Errorf("kafka topic is required")
	}
	return c.broker.produce(topic, rec), nil
}

func (c mockClient) Consume(ctx context.Context, topic string, group string, timeout time.Duration) (kafkaRecord, int64, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for {
		if rec, lag, ok := c.broker.consume(topic, group); ok {
			return rec, lag, nil
		}
		select {
		case <-ctx.Done():
			return kafkaRecord{}, 0, ctx.Err()
		case <-deadline.C:
			return kafkaRecord{}, 0, fmt.Errorf("no kafka record received from %q within %s", topic, timeout)
		case <-tick.C:
		}
	}
}

func (c mockClient) RequestReply(ctx context.Context, reqTopic, replyTopic string, req kafkaRecord, timeout time.Duration) (recordMeta, kafkaRecord, error) {
	meta, err := c.Produce(ctx, reqTopic, req)
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	// Synthesize a correlated reply if nothing is waiting, so request/reply
	// flows can be exercised end-to-end against the mock broker.
	if !c.broker.hasMessages(replyTopic, "") {
		c.broker.produce(replyTopic, kafkaRecord{
			Key:           req.Key,
			Value:         req.Value,
			Headers:       cloneStringMap(req.Headers),
			CorrelationID: req.CorrelationID,
			Timestamp:     time.Now(),
		})
	}
	reply, _, err := c.Consume(ctx, replyTopic, "", timeout)
	return meta, reply, err
}

func (c mockClient) Commit(context.Context) error { return nil }

func (c mockClient) Seek(_ context.Context, topic, group, target string) error {
	if topic == "" {
		return fmt.Errorf("kafka topic is required")
	}
	_, err := c.broker.seek(topic, group, target)
	return err
}

func (c mockClient) HealthCheck(context.Context) error {
	if c.broker == nil {
		return fmt.Errorf("in-memory kafka broker is not initialized")
	}
	return nil
}

func (c mockClient) Teardown(context.Context) error { return nil }

// unavailableClient is returned when mock mode is disabled. A real,
// franz-go-backed adapter is not compiled into this build; this stub fails
// loudly with actionable guidance, mirroring the JMS Solace/IBM MQ adapters.
type unavailableClient struct {
	brokers []string
}

func (c unavailableClient) Produce(context.Context, string, kafkaRecord) (recordMeta, error) {
	return recordMeta{}, c.err()
}

func (c unavailableClient) Consume(context.Context, string, string, time.Duration) (kafkaRecord, int64, error) {
	return kafkaRecord{}, 0, c.err()
}

func (c unavailableClient) RequestReply(context.Context, string, string, kafkaRecord, time.Duration) (recordMeta, kafkaRecord, error) {
	return recordMeta{}, kafkaRecord{}, c.err()
}

func (c unavailableClient) Commit(context.Context) error { return c.err() }

func (c unavailableClient) Seek(context.Context, string, string, string) error { return c.err() }

func (c unavailableClient) HealthCheck(context.Context) error {
	if len(c.brokers) == 0 {
		return fmt.Errorf("kafka brokers are not configured")
	}
	return c.err()
}

func (c unavailableClient) Teardown(context.Context) error { return nil }

func (c unavailableClient) err() error {
	return fmt.Errorf("real Kafka network adapter is not available in this build; set config.kafka.mock for local replay")
}

func newCorrelationID() string { return uuid.NewString() }

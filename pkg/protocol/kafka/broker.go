package kafka

import (
	"context"
	"fmt"
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

// memoryBroker is a process-local FIFO broker keyed by topic. Offsets are
// monotonic per topic and a high-watermark is tracked so consumer lag can be
// reported the way a real cluster would.
type memoryBroker struct {
	mu            sync.Mutex
	topics        map[string][]kafkaRecord
	highWatermark map[string]int64
}

func newMemoryBroker() *memoryBroker {
	return &memoryBroker{
		topics:        make(map[string][]kafkaRecord),
		highWatermark: make(map[string]int64),
	}
}

// sharedBroker lets multiple VUser goroutines in a single process exercise the
// same mock topics (e.g. one VUser produces, another consumes).
var sharedBroker = newMemoryBroker()

func (b *memoryBroker) produce(topic string, rec kafkaRecord) recordMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	offset := b.highWatermark[topic]
	rec.Topic = topic
	rec.Offset = offset
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	b.topics[topic] = append(b.topics[topic], rec)
	b.highWatermark[topic] = offset + 1
	return recordMeta{Topic: topic, Partition: rec.Partition, Offset: offset, Timestamp: rec.Timestamp}
}

// consume pops the oldest record for the topic and returns it along with the
// number of records still pending (consumer lag).
func (b *memoryBroker) consume(topic string) (kafkaRecord, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.topics[topic]
	if len(queue) == 0 {
		return kafkaRecord{}, 0, false
	}
	rec := queue[0]
	copy(queue, queue[1:])
	queue = queue[:len(queue)-1]
	b.topics[topic] = queue
	lag := int64(len(queue))
	return rec, lag, true
}

func (b *memoryBroker) hasMessages(topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.topics[topic]) > 0
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

func (c mockClient) Consume(ctx context.Context, topic string, _ string, timeout time.Duration) (kafkaRecord, int64, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for {
		if rec, lag, ok := c.broker.consume(topic); ok {
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
	if !c.broker.hasMessages(replyTopic) {
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

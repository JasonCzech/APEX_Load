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
	BeginTransaction(ctx context.Context) error
	CommitTransaction(ctx context.Context) error
	AbortTransaction(ctx context.Context) error
	HealthCheck(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// kafkaRecord is a provider-neutral representation of a Kafka record.
type kafkaRecord struct {
	Key       string
	Value     []byte
	Headers   map[string]string
	Topic     string
	Partition int32
	// ManualPartition distinguishes an authored partition (including zero) from
	// the mock broker's resolved partition hint. Real clients must let their
	// partitioner use broker metadata for key-hash and round-robin routing.
	ManualPartition bool
	Offset          int64
	LeaderEpoch     int32
	Timestamp       time.Time
	CorrelationID   string
}

// recordMeta is the broker acknowledgement returned by a produce.
type recordMeta struct {
	Topic     string
	Partition int32
	Offset    int64
	Timestamp time.Time
}

type brokerProduce struct {
	topic string
	rec   kafkaRecord
}

// memoryBroker is a process-local broker keyed by topic. Each topic is an
// append-only commit log (offset = index, records are never removed), and each
// (topic, consumer-group) pair has an independent read position. This mirrors a
// real cluster closely enough to model offset lag, non-destructive reads, seek,
// and replay. Reads default to the earliest offset so produce-then-consume
// flows work without an explicit seek.
//
// To keep a long-lived process (e.g. a controller running many mock shakeouts)
// from growing without bound, each topic's log is capped at maxBrokerLogPerTopic
// records: when exceeded, the oldest records are evicted and the per-topic base
// offset advances. Offsets remain absolute (offset == base + index), so cursors,
// lag, and seek stay correct across eviction. Mock mode is for shakeout/CI only
// (real load uses the kafka_real adapter); do not use it for sustained runs.
type memoryBroker struct {
	mu         sync.Mutex
	logs       map[string][]kafkaRecord // per topic; logs[topic][i] has offset base[topic]+i
	base       map[string]int64         // per topic: absolute offset of logs[topic][0] (evicted count)
	cursors    map[string]int64         // per cursorKey(topic, group): next read offset (absolute)
	refs       map[string]int
	bytes      map[string]int64
	totalBytes int64
}

// maxBrokerLogPerTopic bounds retained records per topic in the in-memory mock so
// a long-lived process does not accumulate every produced event forever. It is
// generous enough that ordinary shakeout/CI runs never evict.
const (
	maxBrokerLogPerTopic   = 50000
	maxBrokerBytesPerTopic = 64 << 20
	maxBrokerRetainedBytes = 256 << 20
	maxBrokerTopicKeys     = 1024
	maxBrokerCursorKeys    = 4096
	maxBrokerKeyBytes      = 16 << 10
)

func newMemoryBroker() *memoryBroker {
	return &memoryBroker{
		logs:    make(map[string][]kafkaRecord),
		base:    make(map[string]int64),
		cursors: make(map[string]int64),
		refs:    make(map[string]int),
		bytes:   make(map[string]int64),
	}
}

func cursorKey(topic, group string) string { return topic + "\x00" + group }

// sharedBroker lets multiple VUser goroutines in a single process exercise the
// same mock topics (e.g. one VUser produces, another consumes).
var sharedBroker = newMemoryBroker()

func mockTopicKey(namespace, topic string) string {
	if namespace == "" {
		return topic
	}
	return namespace + "\x00" + topic
}

func (b *memoryBroker) acquire(namespace string) {
	if namespace == "" {
		return
	}
	b.mu.Lock()
	b.refs[namespace]++
	b.mu.Unlock()
}

func (b *memoryBroker) release(namespace string) {
	if namespace == "" {
		return
	}
	prefix := namespace + "\x00"
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refs[namespace]--
	if b.refs[namespace] > 0 {
		return
	}
	delete(b.refs, namespace)
	for topic := range b.logs {
		if strings.HasPrefix(topic, prefix) {
			b.totalBytes -= b.bytes[topic]
			delete(b.logs, topic)
			delete(b.base, topic)
			delete(b.bytes, topic)
		}
	}
	if b.totalBytes < 0 {
		b.totalBytes = 0
	}
	for key := range b.cursors {
		if strings.HasPrefix(key, prefix) {
			delete(b.cursors, key)
		}
	}
}

func (b *memoryBroker) produce(topic string, rec kafkaRecord) (recordMeta, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(topic) > maxBrokerKeyBytes {
		return recordMeta{}, fmt.Errorf("mock kafka topic key exceeds %d bytes", maxBrokerKeyBytes)
	}
	if _, exists := b.logs[topic]; !exists && len(b.logs) >= maxBrokerTopicKeys {
		return recordMeta{}, fmt.Errorf("mock kafka topic capacity exceeded (%d keys)", maxBrokerTopicKeys)
	}
	rec.Topic = topic
	size := int64(kafkaRecordSize(rec))
	if size > maxBrokerBytesPerTopic {
		return recordMeta{}, fmt.Errorf("mock kafka record exceeds per-topic capacity (%d bytes)", maxBrokerBytesPerTopic)
	}
	if size > maxBrokerRetainedBytes-b.totalBytes {
		return recordMeta{}, fmt.Errorf("mock kafka aggregate retention capacity exceeded (%d bytes)", maxBrokerRetainedBytes)
	}
	return b.appendLocked(topic, rec), nil
}

// produceBatch atomically admits and appends a mock transaction. Every capacity
// and record-size check runs before the first log mutation, and the broker lock
// remains held through all appends, so consumers observe either the entire
// transaction or none of it.
func (b *memoryBroker) produceBatch(batch []brokerProduce) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	newTopics := make(map[string]struct{})
	batchBytes := int64(0)
	for i := range batch {
		if len(batch[i].topic) > maxBrokerKeyBytes {
			return fmt.Errorf("mock kafka topic key exceeds %d bytes", maxBrokerKeyBytes)
		}
		batch[i].rec.Topic = batch[i].topic
		size := int64(kafkaRecordSize(batch[i].rec))
		if size > maxBrokerBytesPerTopic {
			return fmt.Errorf("mock kafka record exceeds per-topic capacity (%d bytes)", maxBrokerBytesPerTopic)
		}
		if size > maxBrokerRetainedBytes-batchBytes {
			return fmt.Errorf("mock kafka transaction exceeds aggregate retention capacity (%d bytes)", maxBrokerRetainedBytes)
		}
		batchBytes += size
		if _, exists := b.logs[batch[i].topic]; !exists {
			newTopics[batch[i].topic] = struct{}{}
		}
	}
	if len(b.logs)+len(newTopics) > maxBrokerTopicKeys {
		return fmt.Errorf("mock kafka topic capacity exceeded (%d keys)", maxBrokerTopicKeys)
	}
	if batchBytes > maxBrokerRetainedBytes-b.totalBytes {
		return fmt.Errorf("mock kafka aggregate retention capacity exceeded (%d bytes)", maxBrokerRetainedBytes)
	}
	for _, pending := range batch {
		b.appendLocked(pending.topic, pending.rec)
	}
	return nil
}

// appendLocked appends one already-validated record and enforces bounded mock
// retention. The caller must hold b.mu.
func (b *memoryBroker) appendLocked(topic string, rec kafkaRecord) recordMeta {
	offset := b.base[topic] + int64(len(b.logs[topic]))
	rec.Offset = offset
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	b.logs[topic] = append(b.logs[topic], rec)
	retained := int64(kafkaRecordSize(rec))
	b.bytes[topic] += retained
	b.totalBytes += retained
	// Bound retained records: evict the oldest and advance the base offset so a
	// long-lived process does not grow unbounded. Offsets are absolute, so this is
	// transparent to cursors/lag/seek.
	drop := 0
	for drop < len(b.logs[topic]) && (len(b.logs[topic])-drop > maxBrokerLogPerTopic || b.bytes[topic] > maxBrokerBytesPerTopic) {
		b.bytes[topic] -= int64(kafkaRecordSize(b.logs[topic][drop]))
		b.totalBytes -= int64(kafkaRecordSize(b.logs[topic][drop]))
		drop++
	}
	if drop > 0 {
		b.logs[topic] = append(b.logs[topic][:0], b.logs[topic][drop:]...)
		b.base[topic] += int64(drop)
	}
	return recordMeta{Topic: topic, Partition: rec.Partition, Offset: offset, Timestamp: rec.Timestamp}
}

func (b *memoryBroker) ensureCursorCapacityLocked(key string) error {
	if len(key) > maxBrokerKeyBytes {
		return fmt.Errorf("mock kafka consumer cursor key exceeds %d bytes", maxBrokerKeyBytes)
	}
	if _, exists := b.cursors[key]; exists || len(b.cursors) < maxBrokerCursorKeys {
		return nil
	}
	return fmt.Errorf("mock kafka consumer cursor capacity exceeded (%d keys)", maxBrokerCursorKeys)
}

func kafkaRecordSize(rec kafkaRecord) int {
	size := len(rec.Key) + len(rec.Value) + len(rec.CorrelationID) + len(rec.Topic)
	for key, value := range rec.Headers {
		size += len(key) + len(value)
	}
	return size
}

// consume returns the next unread record for (topic, group) without removing it
// from the log (offset-based, like a real consumer), advances the group's read
// position, and returns the remaining offset lag (records still behind the tail).
func (b *memoryBroker) consumeChecked(topic, group string) (kafkaRecord, int64, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	log := b.logs[topic]
	base := b.base[topic]
	end := base + int64(len(log))
	key := cursorKey(topic, group)
	if err := b.ensureCursorCapacityLocked(key); err != nil {
		return kafkaRecord{}, 0, false, err
	}
	pos := b.cursors[key]
	if pos < base {
		// The cursor lagged behind eviction; skip to the earliest retained record.
		pos = base
	}
	if pos >= end {
		return kafkaRecord{}, 0, false, nil
	}
	rec := log[pos-base]
	b.cursors[key] = pos + 1
	lag := end - (pos + 1)
	return rec, lag, true, nil
}

func (b *memoryBroker) consume(topic, group string) (kafkaRecord, int64, bool) {
	record, lag, ok, _ := b.consumeChecked(topic, group)
	return record, lag, ok
}

func (b *memoryBroker) hasMessages(topic, group string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	base := b.base[topic]
	pos := b.cursors[cursorKey(topic, group)]
	if pos < base {
		pos = base
	}
	return pos < base+int64(len(b.logs[topic]))
}

// seek repositions the (topic, group) consumer to earliest, latest, or an
// explicit offset, returning the resolved offset.
func (b *memoryBroker) seek(topic, group, target string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	base := b.base[topic]
	end := base + int64(len(b.logs[topic]))
	key := cursorKey(topic, group)
	if err := b.ensureCursorCapacityLocked(key); err != nil {
		return 0, err
	}
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", "earliest", "beginning", "start":
		b.cursors[key] = base
		return base, nil
	case "latest", "end":
		b.cursors[key] = end
		return end, nil
	default:
		n, err := strconv.ParseInt(strings.TrimSpace(target), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid kafka seek offset %q (use earliest, latest, or a number)", target)
		}
		if n < base {
			n = base
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
	broker    *memoryBroker
	namespace string
	release   *sync.Once
}

func (c mockClient) topic(topic string) string { return mockTopicKey(c.namespace, topic) }

func (c mockClient) Produce(_ context.Context, topic string, rec kafkaRecord) (recordMeta, error) {
	if topic == "" {
		return recordMeta{}, fmt.Errorf("kafka topic is required")
	}
	meta, err := c.broker.produce(c.topic(topic), rec)
	if err != nil {
		return recordMeta{}, err
	}
	meta.Topic = topic
	return meta, nil
}

// CommitBufferedTransaction atomically moves a Kafka protocol transaction's
// buffered records into the in-memory broker. Real clients use their native
// Begin/Produce/Commit transaction lifecycle instead.
func (c mockClient) CommitBufferedTransaction(_ context.Context, pending []pendingProduce) error {
	batch := make([]brokerProduce, len(pending))
	for i, produce := range pending {
		if produce.topic == "" {
			return fmt.Errorf("kafka topic is required")
		}
		batch[i] = brokerProduce{topic: c.topic(produce.topic), rec: produce.rec}
	}
	return c.broker.produceBatch(batch)
}

func (c mockClient) Consume(ctx context.Context, topic string, group string, timeout time.Duration) (kafkaRecord, int64, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for {
		rec, lag, ok, err := c.broker.consumeChecked(c.topic(topic), group)
		if err != nil {
			return kafkaRecord{}, 0, err
		}
		if ok {
			rec.Topic = topic
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
	if !c.broker.hasMessages(c.topic(replyTopic), "") {
		if _, err := c.broker.produce(c.topic(replyTopic), kafkaRecord{
			Key:           req.Key,
			Value:         req.Value,
			Headers:       cloneStringMap(req.Headers),
			CorrelationID: req.CorrelationID,
			Timestamp:     time.Now(),
		}); err != nil {
			return recordMeta{}, kafkaRecord{}, err
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		reply, _, err := c.Consume(ctx, replyTopic, "", remaining)
		if err != nil {
			return recordMeta{}, kafkaRecord{}, err
		}
		if req.CorrelationID == "" || reply.CorrelationID == req.CorrelationID || reply.Headers["kafka_correlation_id"] == req.CorrelationID {
			return meta, reply, nil
		}
	}
	return recordMeta{}, kafkaRecord{}, fmt.Errorf("no correlated kafka reply received from %q within %s", replyTopic, timeout)
}

func (c mockClient) Commit(context.Context) error { return nil }

func (c mockClient) Seek(_ context.Context, topic, group, target string) error {
	if topic == "" {
		return fmt.Errorf("kafka topic is required")
	}
	_, err := c.broker.seek(c.topic(topic), group, target)
	return err
}

// Transaction lifecycle is a no-op on the mock: the Kafka protocol buffers
// transactional produces per-VUser and flushes them on commit (so consumers
// never see uncommitted records and abort discards them), which provides the
// observable read_committed semantics without broker-side transaction state.
func (c mockClient) BeginTransaction(context.Context) error  { return nil }
func (c mockClient) CommitTransaction(context.Context) error { return nil }
func (c mockClient) AbortTransaction(context.Context) error  { return nil }

func (c mockClient) HealthCheck(context.Context) error {
	if c.broker == nil {
		return fmt.Errorf("in-memory kafka broker is not initialized")
	}
	return nil
}

func (c mockClient) Teardown(context.Context) error {
	if c.release != nil {
		c.release.Do(func() { c.broker.release(c.namespace) })
	}
	return nil
}

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

func (c unavailableClient) BeginTransaction(context.Context) error  { return c.err() }
func (c unavailableClient) CommitTransaction(context.Context) error { return c.err() }
func (c unavailableClient) AbortTransaction(context.Context) error  { return c.err() }

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

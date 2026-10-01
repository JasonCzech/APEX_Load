//go:build kafka_real

// Package-local franz-go-backed Kafka adapter, compiled only with the
// `kafka_real` build tag. The default build uses the unavailable stub
// (realclient_default.go) so the standard binary stays self-contained and the
// franz-go dependency is opt-in. This path is exercised by the
// `kafka_integration` test against a live broker; it is not part of default CI.
package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"hash/fnv"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/oauth"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

func newRealClient(cfg config) kafkaClient {
	if cfg.TransactionalID != "" {
		// A transactional.id identifies one logical producer. The engine creates
		// one franz client per VUser, so sharing the configured base ID would make
		// Kafka fence all but the last VUser. Keep the configured prefix for
		// operator visibility while giving each client a unique bounded ID.
		suffix := "-" + uuid.NewString()
		const maxTransactionalIDBytes = 249
		baseLimit := maxTransactionalIDBytes - len(suffix)
		base := cfg.TransactionalID
		if len(base) > baseLimit {
			base = base[:baseLimit]
		}
		cfg.TransactionalID = base + suffix
	}
	return &franzClient{cfg: cfg, admission: newProducerAdmission(cfg.Producer)}
}

type franzClient struct {
	admission            *producerAdmission
	cfg                  config
	once                 sync.Once
	cl                   *kgo.Client
	err                  error
	replyOnce            sync.Once
	replyCl              *kgo.Client
	replyErr             error
	replyAssignmentMu    sync.RWMutex
	replyPollMu          sync.Mutex
	replyAssigned        map[string]bool
	replyTopicRefs       map[string]int
	consumeTopicMu       sync.Mutex
	consumePollMu        sync.Mutex
	consumeTopicRefs     map[string]int
	assignmentMu         sync.RWMutex
	assignmentGeneration uint64
	ownedPartitions      map[string]map[int32]uint64
	consumedMu           sync.Mutex
	consumedOffsets      map[string]map[int32]trackedConsumedRecord
	pendingMu            sync.Mutex
	pending              []pendingReply
	pendingBytes         int64
	replyPending         []pendingReply
	replyPendingBytes    int64
}

const (
	maxPendingReplyRecords      = 256
	maxPendingReplyBytes        = 4 << 20
	pendingReplyTTL             = 5 * time.Minute
	maxTrackedConsumeTopics     = 256
	maxTrackedConsumeTopicBytes = 64 << 10
	maxTrackedConsumePartitions = 1024
	maxGlobalPendingRecords     = 16_384
	maxGlobalPendingBytes       = int64(256 << 20)
)

type pendingReply struct {
	record        kafkaRecord
	highWatermark int64
	receivedAt    time.Time
	bytes         int64
	generation    uint64
}

type trackedConsumedRecord struct {
	record     kafkaRecord
	generation uint64
}

type kafkaPendingBudget struct {
	mu      sync.Mutex
	records int
	bytes   int64
}

var globalKafkaPending kafkaPendingBudget

func (b *kafkaPendingBudget) reserve(bytes int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.records >= maxGlobalPendingRecords || bytes > maxGlobalPendingBytes-b.bytes {
		return false
	}
	b.records++
	b.bytes += bytes
	return true
}

func (b *kafkaPendingBudget) release(bytes int64) {
	b.mu.Lock()
	b.records--
	b.bytes -= bytes
	if b.records < 0 {
		b.records = 0
	}
	if b.bytes < 0 {
		b.bytes = 0
	}
	b.mu.Unlock()
}

// handle lazily constructs the underlying *kgo.Client so Init never blocks or
// fails; connection problems surface on first use / HealthCheck instead.
func (c *franzClient) handle() (*kgo.Client, error) {
	c.once.Do(func() {
		c.consumeTopicRefs = make(map[string]int)
		c.ownedPartitions = make(map[string]map[int32]uint64)
		c.cl, c.err = c.newClient(c.cfg.ConsumerGroup)
	})
	return c.cl, c.err
}

// replyHandle uses a dedicated, unique consumer group. Sharing the configured
// group could assign a reply partition to a different VUser, while a direct
// consumer cannot use the assignment callback needed to prime from the end
// before producing the request.
func (c *franzClient) replyHandle() (*kgo.Client, error) {
	c.replyOnce.Do(func() {
		c.replyAssigned = make(map[string]bool)
		c.replyTopicRefs = make(map[string]int)
		replyGroup := "apexload-reply-" + uuid.NewString()
		c.replyCl, c.replyErr = c.newClient(replyGroup,
			kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
			kgo.OnPartitionsAssigned(func(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
				c.replyAssignmentMu.Lock()
				for topic := range partitions {
					c.replyAssigned[topic] = true
				}
				c.replyAssignmentMu.Unlock()
			}))
	})
	return c.replyCl, c.replyErr
}

func (c *franzClient) newClient(consumerGroup string, extra ...kgo.Opt) (*kgo.Client, error) {
	// Explicit partitions (including zero) must be honored, while key-hash and
	// round-robin routing must use the real topic's broker-reported partition
	// count. config.kafka.partitions is only a mock-broker hint.
	opts := []kgo.Opt{
		kgo.SeedBrokers(c.cfg.Brokers...),
		kgo.RecordPartitioner(resolvedRecordPartitioner(c.cfg.PartitionStrategy)),
	}
	p := c.cfg.Producer
	if p.Retries != nil {
		opts = append(opts, kgo.RecordRetries(*p.Retries))
	}
	if p.LingerMs != nil {
		opts = append(opts, kgo.ProducerLinger(time.Duration(*p.LingerMs)*time.Millisecond))
	}
	if p.BufferBytes != nil {
		opts = append(opts, kgo.MaxBufferedBytes(*p.BufferBytes), kgo.MaxBufferedRecords(2147483647))
	}
	if p.RequestTimeoutMs != nil {
		opts = append(opts, kgo.ProduceRequestTimeout(time.Duration(*p.RequestTimeoutMs)*time.Millisecond))
	}
	if p.DeliveryTimeoutMs != nil {
		opts = append(opts, kgo.RecordDeliveryTimeout(time.Duration(*p.DeliveryTimeoutMs)*time.Millisecond))
	}
	if p.BatchBytes != nil {
		opts = append(opts, kgo.ProducerBatchMaxBytes(int32(*p.BatchBytes)))
	}
	if c.cfg.ClientID != "" {
		opts = append(opts, kgo.ClientID(c.cfg.ClientID))
	}
	if consumerGroup != "" {
		// The DSL exposes kafka_commit as the explicit delivery boundary. Leaving
		// franz-go auto-commit enabled would commit on its timer or on the rebalance
		// caused by removing a dynamically consumed topic, before that action runs.
		opts = append(opts, kgo.ConsumerGroup(consumerGroup), kgo.DisableAutoCommit())
		if consumerGroup == c.cfg.ConsumerGroup {
			opts = append(opts,
				kgo.OnPartitionsAssigned(func(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
					c.onPartitionsAssigned(partitions)
				}),
				kgo.OnPartitionsRevoked(func(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
					c.onPartitionsRevoked(partitions)
				}),
				kgo.OnPartitionsLost(func(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
					c.onPartitionsRevoked(partitions)
				}),
			)
		}
	}
	opts = append(opts, kgo.FetchMaxBytes(int32(maxPendingReplyBytes)), kgo.FetchMaxPartitionBytes(int32(maxPendingReplyBytes)))
	if strings.EqualFold(c.cfg.AutoOffsetReset, "earliest") {
		opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	} else {
		opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()))
	}
	switch strings.ToLower(c.cfg.Acks) {
	case "none":
		// Idempotence requires acks=all; non-all acks must disable it.
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()), kgo.DisableIdempotentWrite())
	case "leader":
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
	default:
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
		// franz enables idempotent writes by default at acks=all; honor an
		// explicit opt-out (config.kafka.idempotence: disabled).
		if c.cfg.Idempotence == "disabled" {
			opts = append(opts, kgo.DisableIdempotentWrite())
		}
	}
	if strings.EqualFold(c.cfg.IsolationLevel, "read_committed") {
		opts = append(opts, kgo.FetchIsolationLevel(kgo.ReadCommitted()))
	} else if strings.EqualFold(c.cfg.IsolationLevel, "read_uncommitted") {
		opts = append(opts, kgo.FetchIsolationLevel(kgo.ReadUncommitted()))
	}
	if c.cfg.TransactionalID != "" {
		// A transactional producer requires acks=all + idempotence, which
		// franz enforces; the validator also requires acks=all.
		opts = append(opts, kgo.TransactionalID(c.cfg.TransactionalID))
	}
	if codec, ok := compressionCodec(c.cfg.Compression); ok {
		opts = append(opts, kgo.ProducerBatchCompression(codec))
	}
	if c.cfg.MaxMessageBytes > 0 && (p.BatchBytes == nil || c.cfg.MaxMessageBytes < *p.BatchBytes) {
		// Authoritative client-side cap (e.g. Event Hubs' 1 MiB limit): franz
		// rejects any record exceeding the batch-max, complementing the
		// conservative pre-flight guard in kafka.go checkRecordSize.
		opts = append(opts, kgo.ProducerBatchMaxBytes(int32(c.cfg.MaxMessageBytes)))
	}
	if hasExplicitOAuth(c.cfg) {
		if err := validateExplicitOAuth(c.cfg); err != nil {
			return nil, err
		}
		opts = append(opts, kgo.SASL(oauth.Oauth(func(ctx context.Context) (oauth.Auth, error) {
			token, err := explicitOAuthToken(ctx, c.cfg)
			return oauth.Auth{Token: token}, err
		})))
	} else if strings.EqualFold(c.cfg.SASLMechanism, "oauthbearer") {
		src, serr := c.oauthTokenSource()
		if serr != nil {
			return nil, serr
		}
		opts = append(opts, kgo.SASL(oauth.Oauth(func(ctx context.Context) (oauth.Auth, error) {
			tok, terr := src.Token(ctx)
			if terr != nil {
				return oauth.Auth{}, terr
			}
			return oauth.Auth{Token: tok}, nil
		})))
	} else if mech := c.saslMechanism(); mech != nil {
		opts = append(opts, kgo.SASL(mech))
	}
	var brokerTLS *tls.Config
	if c.usesTLS() {
		tlsCfg, terr := c.buildTLSConfig()
		if terr != nil {
			return nil, terr
		}
		brokerTLS = tlsCfg
	}
	if len(c.cfg.AllowedTargetHosts) > 0 {
		opts = append(opts, kgo.Dialer(c.targetPolicyDialer(brokerTLS)))
	} else if brokerTLS != nil {
		opts = append(opts, kgo.DialTLSConfig(brokerTLS))
	}
	// Call-specific policy must win over configured defaults. Reply consumers,
	// for example, always start at the end even when normal consumption starts
	// at the beginning.
	opts = append(opts, extra...)
	return kgo.NewClient(opts...)
}

func (c *franzClient) targetPolicyDialer(tlsConfig *tls.Config) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := protocol.ValidateTargetAddress(address, c.cfg.AllowedTargetHosts); err != nil {
			return nil, err
		}
		if tlsConfig == nil {
			return dialer.DialContext(ctx, network, address)
		}
		config := tlsConfig.Clone()
		if config.ServerName == "" {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("split Kafka broker address: %w", err)
			}
			config.ServerName = host
		}
		return (&tls.Dialer{NetDialer: dialer, Config: config}).DialContext(ctx, network, address)
	}
}

func (c *franzClient) onPartitionsAssigned(partitions map[string][]int32) {
	c.assignmentMu.Lock()
	c.assignmentGeneration++
	generation := c.assignmentGeneration
	if c.ownedPartitions == nil {
		c.ownedPartitions = make(map[string]map[int32]uint64)
	}
	for topic, assigned := range partitions {
		owned := c.ownedPartitions[topic]
		if owned == nil {
			owned = make(map[int32]uint64)
			c.ownedPartitions[topic] = owned
		}
		for _, partition := range assigned {
			owned[partition] = generation
		}
	}
	c.assignmentMu.Unlock()
	// Any buffered state for a newly assigned partition predates this assignment
	// and must not be returned or committed under the new owner generation.
	c.discardPartitionState(partitions)
}

func (c *franzClient) onPartitionsRevoked(partitions map[string][]int32) {
	c.assignmentMu.Lock()
	c.assignmentGeneration++
	for topic, revoked := range partitions {
		owned := c.ownedPartitions[topic]
		for _, partition := range revoked {
			delete(owned, partition)
		}
		if len(owned) == 0 {
			delete(c.ownedPartitions, topic)
		}
	}
	c.assignmentMu.Unlock()
	c.discardPartitionState(partitions)
}

func (c *franzClient) discardPartitionState(partitions map[string][]int32) {
	contains := func(topic string, partition int32) bool {
		for _, candidate := range partitions[topic] {
			if candidate == partition {
				return true
			}
		}
		return false
	}
	c.pendingMu.Lock()
	for i := 0; i < len(c.pending); {
		record := c.pending[i].record
		if contains(record.Topic, record.Partition) {
			c.removePendingLocked(i)
			continue
		}
		i++
	}
	c.pendingMu.Unlock()
	c.consumedMu.Lock()
	for topic, tracked := range c.consumedOffsets {
		for partition := range tracked {
			if contains(topic, partition) {
				delete(tracked, partition)
			}
		}
		if len(tracked) == 0 {
			delete(c.consumedOffsets, topic)
		}
	}
	c.consumedMu.Unlock()
}

func (c *franzClient) ownershipGeneration(topic string, partition int32) (uint64, bool) {
	if c.cfg.ConsumerGroup == "" {
		return 0, true
	}
	c.assignmentMu.RLock()
	defer c.assignmentMu.RUnlock()
	generation, owned := c.ownedPartitions[topic][partition]
	return generation, owned
}

func (c *franzClient) pendingStillOwned(pending pendingReply) bool {
	generation, owned := c.ownershipGeneration(pending.record.Topic, pending.record.Partition)
	return owned && generation == pending.generation
}

func rewindKafkaRecord(cl *kgo.Client, record kafkaRecord) {
	if cl == nil {
		return
	}
	cl.SetOffsets(map[string]map[int32]kgo.EpochOffset{
		record.Topic: {record.Partition: {Epoch: record.LeaderEpoch, Offset: record.Offset}},
	})
}

func (c *franzClient) Produce(ctx context.Context, topic string, rec kafkaRecord) (recordMeta, error) {
	cl, err := c.handle()
	if err != nil {
		return recordMeta{}, err
	}
	size := int64(len(rec.Value) + len(rec.Key))
	for key, value := range rec.Headers {
		size += int64(len(key) + len(value))
	}
	release, err := c.admission.admit(ctx, size, func(admission context.Context) error {
		req := kmsg.NewPtrMetadataRequest()
		req.AllowAutoTopicCreation = true
		req.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
		maxAge := 5 * time.Minute
		for {
			response, err := cl.RequestCachedMetadata(admission, req, maxAge)
			if err != nil {
				return err
			}
			ready := false
			for _, t := range response.Topics {
				if t.Topic == nil || *t.Topic != topic {
					continue
				}
				if t.ErrorCode != 0 {
					err = kerr.ErrorForCode(t.ErrorCode)
					if !kerr.IsRetriable(err) {
						return err
					}
					continue
				}
				for _, partition := range t.Partitions {
					if !rec.ManualPartition || partition.Partition == rec.Partition {
						ready = true
						break
					}
				}
			}
			if ready {
				return nil
			}
			maxAge = 0 // A missing partition must be checked against fresh broker metadata.
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-admission.Done():
				timer.Stop()
				return admission.Err()
			case <-timer.C:
				cl.ForceMetadataRefresh()
			}
		}
	})
	if err != nil {
		return recordMeta{}, err
	}
	defer release()
	r := resolvedProducerRecord(topic, rec)
	ackStarted := time.Now()
	results := cl.ProduceSync(ctx, r)
	if err := results.FirstErr(); err != nil {
		return recordMeta{}, err
	}
	var ack *float64
	if strings.TrimSpace(c.cfg.Acks) != "0" && !strings.EqualFold(strings.TrimSpace(c.cfg.Acks), "none") {
		elapsed := float64(time.Since(ackStarted)) / float64(time.Millisecond)
		ack = &elapsed
	}
	produced := results[0].Record
	return recordMeta{
		AcknowledgementMs: ack,
		Topic:             produced.Topic,
		Partition:         produced.Partition,
		Offset:            produced.Offset,
		Timestamp:         produced.Timestamp,
	}, nil
}

type routedRecordPartitioner struct {
	fallback kgo.Partitioner
}

func resolvedRecordPartitioner(strategy string) kgo.Partitioner {
	var fallback kgo.Partitioner
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "round-robin":
		fallback = kgo.RoundRobinPartitioner()
	default:
		fallback = kgo.StickyKeyPartitioner(func(key []byte, partitions int) int {
			if partitions <= 0 {
				return 0
			}
			hash := fnv.New32a()
			_, _ = hash.Write(key)
			return int(hash.Sum32() % uint32(partitions))
		})
	}
	return &routedRecordPartitioner{fallback: fallback}
}

func (p *routedRecordPartitioner) ForTopic(topic string) kgo.TopicPartitioner {
	return &routedTopicPartitioner{fallback: p.fallback.ForTopic(topic)}
}

type routedTopicPartitioner struct {
	fallback kgo.TopicPartitioner
}

func (p *routedTopicPartitioner) RequiresConsistency(record *kgo.Record) bool {
	if record != nil && record.Partition >= 0 {
		return true
	}
	return p.fallback.RequiresConsistency(record)
}

func (p *routedTopicPartitioner) Partition(record *kgo.Record, partitions int) int {
	if record != nil && record.Partition >= 0 {
		return int(record.Partition)
	}
	return p.fallback.Partition(record, partitions)
}

func (p *routedTopicPartitioner) OnNewBatch() {
	if fallback, ok := p.fallback.(kgo.TopicPartitionerOnNewBatch); ok {
		fallback.OnNewBatch()
	}
}

func resolvedProducerRecord(topic string, rec kafkaRecord) *kgo.Record {
	partition := int32(-1)
	if rec.ManualPartition {
		partition = rec.Partition
	}
	r := &kgo.Record{Topic: topic, Value: rec.Value, Partition: partition}
	if rec.KeyPresent || rec.Key != "" {
		r.Key = []byte(rec.Key)
	}
	for k, v := range rec.Headers {
		r.Headers = append(r.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	return r
}

func (c *franzClient) Consume(ctx context.Context, topic string, group string, timeout time.Duration) (kafkaRecord, int64, error) {
	// franz binds a single consumer group per client (set at handle() time from
	// config.kafka.consumer_group). A per-action group that differs cannot be
	// honored without a distinct client, so fail loudly rather than silently
	// consuming under the configured group (the mock honors per-action groups, so
	// this keeps mock and real from diverging unannounced).
	if group != "" && group != c.cfg.ConsumerGroup {
		return kafkaRecord{}, 0, fmt.Errorf("per-action consumer_group %q differs from the configured config.kafka.consumer_group %q; the real Kafka adapter binds one group per client", group, c.cfg.ConsumerGroup)
	}
	cl, err := c.handle()
	if err != nil {
		return kafkaRecord{}, 0, err
	}
	c.consumePollMu.Lock()
	defer c.consumePollMu.Unlock()
	if err := c.addConsumeTopic(cl, topic); err != nil {
		return kafkaRecord{}, 0, err
	}
	defer c.releaseConsumeTopic(cl, topic)
	c.pendingMu.Lock()
	for i := 0; i < len(c.pending); {
		pending := c.pending[i]
		if pending.record.Topic == topic {
			if !c.pendingStillOwned(pending) {
				c.removePendingLocked(i)
				continue
			}
			record := pending.record
			c.removePendingLocked(i)
			c.pendingMu.Unlock()
			if err := c.trackConsumedRecord(record); err != nil {
				rewindKafkaRecord(cl, record)
				return kafkaRecord{}, 0, err
			}
			return record, pendingConsumerLag(pending), nil
		}
		i++
	}
	c.pendingMu.Unlock()
	restorePaused := c.pauseOtherConsumeTopics(cl, topic)
	defer restorePaused()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		fetches := cl.PollRecords(cctx, 1)
		highWatermarks := make(map[string]map[int32]int64)
		fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
			if highWatermarks[partition.Topic] == nil {
				highWatermarks[partition.Topic] = make(map[int32]int64)
			}
			highWatermarks[partition.Topic][partition.Partition] = partition.HighWatermark
		})
		var rec *kgo.Record
		for _, candidate := range fetches.Records() {
			if rec == nil && candidate.Topic == topic {
				rec = candidate
				continue
			}
			pendingRecord := fromKgoRecord(candidate)
			c.pendingMu.Lock()
			pendingErr := c.addPendingLocked(pendingRecord, highWatermarks[candidate.Topic][candidate.Partition])
			c.pendingMu.Unlock()
			if pendingErr != nil {
				rewindKafkaRecord(cl, pendingRecord)
				return kafkaRecord{}, 0, pendingErr
			}
		}
		if rec != nil {
			highWatermark := int64(-1)
			fetches.EachPartition(func(p kgo.FetchTopicPartition) {
				if p.Topic == topic && p.Partition == rec.Partition && p.HighWatermark > highWatermark {
					highWatermark = p.HighWatermark
				}
			})
			lag := int64(0)
			if highWatermark > rec.Offset+1 {
				lag = highWatermark - rec.Offset - 1
			}
			record := fromKgoRecord(rec)
			if err := c.trackConsumedRecord(record); err != nil {
				rewindKafkaRecord(cl, record)
				return kafkaRecord{}, 0, err
			}
			return record, lag, nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return kafkaRecord{}, 0, errs[0].Err
		}
		if cctx.Err() != nil {
			return kafkaRecord{}, 0, fmt.Errorf("no kafka record received from %q within %s: %w", topic, timeout, cctx.Err())
		}
	}
}

func (c *franzClient) pauseOtherConsumeTopics(cl *kgo.Client, active string) func() {
	c.consumeTopicMu.Lock()
	others := make([]string, 0, len(c.consumeTopicRefs))
	for topic := range c.consumeTopicRefs {
		if topic != active {
			others = append(others, topic)
		}
	}
	c.consumeTopicMu.Unlock()
	if len(others) == 0 {
		return func() {}
	}
	alreadyPaused := cl.PauseFetchTopics()
	wasPaused := make(map[string]struct{}, len(alreadyPaused))
	for _, topic := range alreadyPaused {
		wasPaused[topic] = struct{}{}
	}
	cl.PauseFetchTopics(others...)
	return func() {
		resume := others[:0]
		for _, topic := range others {
			if _, existed := wasPaused[topic]; !existed {
				resume = append(resume, topic)
			}
		}
		if len(resume) > 0 {
			cl.ResumeFetchTopics(resume...)
		}
	}
}

func (c *franzClient) addConsumeTopic(cl *kgo.Client, topic string) error {
	c.consumeTopicMu.Lock()
	defer c.consumeTopicMu.Unlock()
	refs, tracked := c.consumeTopicRefs[topic]
	if !tracked {
		keyBytes := 0
		for trackedTopic := range c.consumeTopicRefs {
			keyBytes += len(trackedTopic)
		}
		if len(c.consumeTopicRefs) >= maxTrackedConsumeTopics || len(topic) > maxTrackedConsumeTopicBytes-keyBytes {
			return fmt.Errorf("kafka consumer topic capacity exceeded (%d topics, %d key bytes)", maxTrackedConsumeTopics, maxTrackedConsumeTopicBytes)
		}
		cl.AddConsumeTopics(topic)
	}
	c.consumeTopicRefs[topic] = refs + 1
	return nil
}

func (c *franzClient) releaseConsumeTopic(cl *kgo.Client, topic string) {
	c.consumeTopicMu.Lock()
	defer c.consumeTopicMu.Unlock()
	refs := c.consumeTopicRefs[topic]
	if refs <= 1 {
		if c.cfg.ConsumerGroup != "" {
			// Keep the topic assignment and its uncommitted offsets until the
			// script reaches kafka_commit. A zero ref count means no Consume call
			// is currently polling it, not that its offset is safe to discard.
			c.consumeTopicRefs[topic] = 0
			return
		}
		delete(c.consumeTopicRefs, topic)
		cl.PurgeTopicsFromConsuming(topic)
		return
	}
	c.consumeTopicRefs[topic] = refs - 1
}

func (c *franzClient) purgeCommittedConsumeTopics(cl *kgo.Client) {
	// Keep the delivered-offset check and topic removal atomic with respect to
	// trackConsumedRecord. Otherwise a Consume returning between the snapshot
	// and the map deletion could lose the topic before its offset is committed.
	c.consumedMu.Lock()
	c.consumeTopicMu.Lock()
	topics := make([]string, 0, len(c.consumeTopicRefs))
	for topic, refs := range c.consumeTopicRefs {
		if refs != 0 {
			continue
		}
		if len(c.consumedOffsets[topic]) > 0 {
			continue
		}
		topics = append(topics, topic)
		delete(c.consumeTopicRefs, topic)
	}
	c.consumeTopicMu.Unlock()
	c.consumedMu.Unlock()
	if len(topics) > 0 {
		cl.PurgeTopicsFromConsuming(topics...)
	}
}

func (c *franzClient) trackConsumedRecord(record kafkaRecord) error {
	generation, owned := c.ownershipGeneration(record.Topic, record.Partition)
	if !owned {
		return fmt.Errorf("kafka partition %s/%d was revoked before delivery", record.Topic, record.Partition)
	}
	c.consumedMu.Lock()
	defer c.consumedMu.Unlock()
	if c.consumedOffsets == nil {
		c.consumedOffsets = make(map[string]map[int32]trackedConsumedRecord)
	}
	partitions := c.consumedOffsets[record.Topic]
	if partitions == nil {
		trackedPartitions := 0
		for _, existing := range c.consumedOffsets {
			trackedPartitions += len(existing)
		}
		if trackedPartitions >= maxTrackedConsumePartitions {
			return fmt.Errorf("kafka delivered partition capacity exceeded (%d)", maxTrackedConsumePartitions)
		}
		partitions = make(map[int32]trackedConsumedRecord)
		c.consumedOffsets[record.Topic] = partitions
	} else if _, exists := partitions[record.Partition]; !exists {
		trackedPartitions := 0
		for _, existing := range c.consumedOffsets {
			trackedPartitions += len(existing)
		}
		if trackedPartitions >= maxTrackedConsumePartitions {
			return fmt.Errorf("kafka delivered partition capacity exceeded (%d)", maxTrackedConsumePartitions)
		}
	}
	if current, ok := partitions[record.Partition]; !ok || record.Offset > current.record.Offset || (record.Offset == current.record.Offset && record.LeaderEpoch > current.record.LeaderEpoch) {
		partitions[record.Partition] = trackedConsumedRecord{record: record, generation: generation}
	}
	return nil
}

func (c *franzClient) consumedRecordsSnapshot() []*kgo.Record {
	c.assignmentMu.RLock()
	defer c.assignmentMu.RUnlock()
	return c.consumedRecordsSnapshotAssignmentLocked()
}

func (c *franzClient) consumedRecordsSnapshotAssignmentLocked() []*kgo.Record {
	c.consumedMu.Lock()
	defer c.consumedMu.Unlock()
	records := make([]*kgo.Record, 0)
	for topic, partitions := range c.consumedOffsets {
		for partition, tracked := range partitions {
			if c.cfg.ConsumerGroup != "" {
				generation, owned := c.ownedPartitions[topic][partition]
				if !owned || generation != tracked.generation {
					continue
				}
			}
			record := tracked.record
			records = append(records, &kgo.Record{
				Topic:       topic,
				Partition:   partition,
				Offset:      record.Offset,
				LeaderEpoch: record.LeaderEpoch,
			})
		}
	}
	return records
}

func (c *franzClient) clearCommittedRecords(committed []*kgo.Record) {
	c.consumedMu.Lock()
	defer c.consumedMu.Unlock()
	for _, record := range committed {
		if record == nil {
			continue
		}
		partitions := c.consumedOffsets[record.Topic]
		current, ok := partitions[record.Partition]
		if !ok || current.record.Offset > record.Offset || (current.record.Offset == record.Offset && current.record.LeaderEpoch > record.LeaderEpoch) {
			continue
		}
		delete(partitions, record.Partition)
		if len(partitions) == 0 {
			delete(c.consumedOffsets, record.Topic)
		}
	}
}

func (c *franzClient) RequestReply(ctx context.Context, reqTopic, replyTopic string, req kafkaRecord, timeout time.Duration) (recordMeta, kafkaRecord, error) {
	c.replyPollMu.Lock()
	defer c.replyPollMu.Unlock()
	cl, err := c.replyHandle()
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	if err := c.addReplyTopic(cl, replyTopic); err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	defer c.releaseReplyTopic(cl, replyTopic)
	deadline := time.Now().Add(timeout)
	if err := c.primeReplyConsumer(ctx, cl, replyTopic, time.Until(deadline)); err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	meta, err := c.Produce(ctx, reqTopic, req)
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}

	for time.Now().Before(deadline) {
		c.pendingMu.Lock()
		now := time.Now()
		for i := 0; i < len(c.replyPending); {
			pending := c.replyPending[i]
			if now.Sub(pending.receivedAt) > pendingReplyTTL {
				rewindKafkaRecord(cl, pending.record)
				c.removeReplyPendingLocked(i)
				c.pendingMu.Unlock()
				return recordMeta{}, kafkaRecord{}, fmt.Errorf("expired buffered kafka reply was rewound before reuse")
			}
			if pending.record.Topic == replyTopic && (req.CorrelationID == "" || pending.record.CorrelationID == req.CorrelationID || pending.record.Headers["kafka_correlation_id"] == req.CorrelationID) {
				c.removeReplyPendingLocked(i)
				c.pendingMu.Unlock()
				return meta, pending.record, nil
			}
			i++
		}
		c.pendingMu.Unlock()
		cctx, cancel := context.WithDeadline(ctx, deadline)
		fetches := cl.PollRecords(cctx, 1)
		cancel()
		var matched *kafkaRecord
		for _, rec := range fetches.Records() {
			converted := fromKgoRecord(rec)
			if matched == nil && converted.Topic == replyTopic && (req.CorrelationID == "" || converted.CorrelationID == req.CorrelationID || converted.Headers["kafka_correlation_id"] == req.CorrelationID) {
				candidate := converted
				matched = &candidate
				continue
			}
			c.pendingMu.Lock()
			pendingErr := c.addReplyPendingLocked(converted)
			c.pendingMu.Unlock()
			if pendingErr != nil {
				rewindKafkaRecord(cl, converted)
				return recordMeta{}, kafkaRecord{}, pendingErr
			}
		}
		if matched != nil {
			return meta, *matched, nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return recordMeta{}, kafkaRecord{}, errs[0].Err
		}
	}
	return recordMeta{}, kafkaRecord{}, fmt.Errorf("no correlated kafka reply received from %q within %s", replyTopic, timeout)
}

func (c *franzClient) addReplyTopic(cl *kgo.Client, topic string) error {
	c.replyAssignmentMu.Lock()
	if _, tracked := c.replyTopicRefs[topic]; !tracked {
		keyBytes := 0
		for trackedTopic := range c.replyTopicRefs {
			keyBytes += len(trackedTopic)
		}
		if len(c.replyTopicRefs) >= maxTrackedConsumeTopics || len(topic) > maxTrackedConsumeTopicBytes-keyBytes {
			c.replyAssignmentMu.Unlock()
			return fmt.Errorf("kafka reply topic capacity exceeded (%d topics, %d key bytes)", maxTrackedConsumeTopics, maxTrackedConsumeTopicBytes)
		}
	}
	c.replyTopicRefs[topic]++
	first := c.replyTopicRefs[topic] == 1
	c.replyAssignmentMu.Unlock()
	if first {
		cl.AddConsumeTopics(topic)
	}
	return nil
}

func (c *franzClient) releaseReplyTopic(cl *kgo.Client, topic string) {
	c.replyAssignmentMu.Lock()
	if c.replyTopicRefs[topic] > 1 {
		c.replyTopicRefs[topic]--
		c.replyAssignmentMu.Unlock()
		return
	}
	delete(c.replyTopicRefs, topic)
	delete(c.replyAssigned, topic)
	c.replyAssignmentMu.Unlock()
	cl.PurgeTopicsFromConsuming(topic)
	c.pendingMu.Lock()
	for i := 0; i < len(c.replyPending); {
		if c.replyPending[i].record.Topic == topic {
			c.removeReplyPendingLocked(i)
			continue
		}
		i++
	}
	c.pendingMu.Unlock()
}

func (c *franzClient) primeReplyConsumer(ctx context.Context, cl *kgo.Client, replyTopic string, timeout time.Duration) error {
	primeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		c.replyAssignmentMu.RLock()
		assigned := c.replyAssigned[replyTopic]
		c.replyAssignmentMu.RUnlock()
		if assigned {
			return nil
		}
		pollCtx, cancelPoll := context.WithTimeout(primeCtx, 100*time.Millisecond)
		fetches := cl.PollRecords(pollCtx, 1)
		cancelPoll()
		c.pendingMu.Lock()
		for _, record := range fetches.Records() {
			converted := fromKgoRecord(record)
			if err := c.addReplyPendingLocked(converted); err != nil {
				c.pendingMu.Unlock()
				rewindKafkaRecord(cl, converted)
				return err
			}
		}
		c.pendingMu.Unlock()
		if errs := fetches.Errors(); len(errs) > 0 {
			return fmt.Errorf("prime kafka reply consumer for %q: %w", replyTopic, errs[0].Err)
		}
		if primeCtx.Err() != nil {
			return fmt.Errorf("prime kafka reply consumer for %q: %w", replyTopic, primeCtx.Err())
		}
	}
}

func (c *franzClient) addPendingLocked(record kafkaRecord, highWatermarks ...int64) error {
	bytes := pendingReplyBytes(record)
	generation, owned := c.ownershipGeneration(record.Topic, record.Partition)
	if !owned {
		return fmt.Errorf("kafka partition %s/%d was revoked before buffering", record.Topic, record.Partition)
	}
	if len(c.pending)+len(c.replyPending) >= maxPendingReplyRecords || bytes > int64(maxPendingReplyBytes)-c.pendingBytes-c.replyPendingBytes {
		return fmt.Errorf("kafka per-VUser pending capacity exceeded (%d records, %d bytes)", maxPendingReplyRecords, maxPendingReplyBytes)
	}
	if !globalKafkaPending.reserve(bytes) {
		return fmt.Errorf("kafka generator-wide pending capacity exceeded (%d records, %d bytes)", maxGlobalPendingRecords, maxGlobalPendingBytes)
	}
	highWatermark := int64(-1)
	if len(highWatermarks) > 0 {
		highWatermark = highWatermarks[0]
	}
	c.pending = append(c.pending, pendingReply{record: record, highWatermark: highWatermark, receivedAt: time.Now(), bytes: bytes, generation: generation})
	c.pendingBytes += bytes
	return nil
}

func pendingConsumerLag(pending pendingReply) int64 {
	if pending.highWatermark > pending.record.Offset+1 {
		return pending.highWatermark - pending.record.Offset - 1
	}
	return 0
}

func (c *franzClient) removePendingLocked(index int) {
	if index < 0 || index >= len(c.pending) {
		return
	}
	c.pendingBytes -= c.pending[index].bytes
	globalKafkaPending.release(c.pending[index].bytes)
	copy(c.pending[index:], c.pending[index+1:])
	clear(c.pending[len(c.pending)-1:])
	c.pending = c.pending[:len(c.pending)-1]
	if c.pendingBytes < 0 {
		c.pendingBytes = 0
	}
}

func (c *franzClient) addReplyPendingLocked(record kafkaRecord) error {
	bytes := pendingReplyBytes(record)
	if len(c.pending)+len(c.replyPending) >= maxPendingReplyRecords || bytes > int64(maxPendingReplyBytes)-c.pendingBytes-c.replyPendingBytes {
		return fmt.Errorf("kafka per-VUser pending capacity exceeded (%d records, %d bytes)", maxPendingReplyRecords, maxPendingReplyBytes)
	}
	if !globalKafkaPending.reserve(bytes) {
		return fmt.Errorf("kafka generator-wide pending capacity exceeded (%d records, %d bytes)", maxGlobalPendingRecords, maxGlobalPendingBytes)
	}
	c.replyPending = append(c.replyPending, pendingReply{record: record, receivedAt: time.Now(), bytes: bytes})
	c.replyPendingBytes += bytes
	return nil
}

func (c *franzClient) removeReplyPendingLocked(index int) {
	if index < 0 || index >= len(c.replyPending) {
		return
	}
	c.replyPendingBytes -= c.replyPending[index].bytes
	globalKafkaPending.release(c.replyPending[index].bytes)
	copy(c.replyPending[index:], c.replyPending[index+1:])
	clear(c.replyPending[len(c.replyPending)-1:])
	c.replyPending = c.replyPending[:len(c.replyPending)-1]
	if c.replyPendingBytes < 0 {
		c.replyPendingBytes = 0
	}
}

func pendingReplyBytes(record kafkaRecord) int64 {
	bytes := int64(len(record.Topic) + len(record.Key) + len(record.Value) + len(record.CorrelationID))
	for key, value := range record.Headers {
		bytes += int64(len(key) + len(value))
	}
	return bytes
}

func (c *franzClient) Commit(ctx context.Context) error {
	cl, err := c.handle()
	if err != nil {
		return err
	}
	if err := cl.Flush(ctx); err != nil {
		return err
	}
	if c.cfg.ConsumerGroup != "" {
		// Keep ownership callbacks fenced from snapshot through broker commit and
		// local clear. If the broker has already advanced the generation, Kafka
		// rejects the commit; otherwise every included partition is still ours.
		c.assignmentMu.RLock()
		records := c.consumedRecordsSnapshotAssignmentLocked()
		if len(records) > 0 {
			if err := cl.CommitRecords(ctx, records...); err != nil {
				c.assignmentMu.RUnlock()
				return err
			}
			c.clearCommittedRecords(records)
		}
		c.assignmentMu.RUnlock()
		c.purgeCommittedConsumeTopics(cl)
	}
	return nil
}

func (c *franzClient) BeginTransaction(_ context.Context) error {
	cl, err := c.handle()
	if err != nil {
		return err
	}
	return cl.BeginTransaction()
}

func (c *franzClient) CommitTransaction(ctx context.Context) error {
	cl, err := c.handle()
	if err != nil {
		return err
	}
	if err := cl.Flush(ctx); err != nil {
		return err
	}
	return cl.EndTransaction(ctx, kgo.TryCommit)
}

func (c *franzClient) AbortTransaction(ctx context.Context) error {
	cl, err := c.handle()
	if err != nil {
		return err
	}
	return cl.EndTransaction(ctx, kgo.TryAbort)
}

func (c *franzClient) Seek(_ context.Context, _, _, _ string) error {
	// Real-broker seek (kgo SetOffsets across the topic's partitions, honoring
	// earliest/latest/explicit offsets) lands with the P2.1 part-2 real-broker
	// work; the in-memory mock supports seek/replay today for shakeout and CI.
	return fmt.Errorf("kafka_seek is not yet supported on the real (kafka_real) adapter; use config.kafka.mock for seek/replay shakeout")
}

func (c *franzClient) HealthCheck(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return fmt.Errorf("kafka brokers are not configured")
	}
	cl, err := c.handle()
	if err != nil {
		return err
	}
	return cl.Ping(ctx)
}

func (c *franzClient) Teardown(context.Context) error {
	c.pendingMu.Lock()
	for len(c.pending) > 0 {
		c.removePendingLocked(len(c.pending) - 1)
	}
	for len(c.replyPending) > 0 {
		c.removeReplyPendingLocked(len(c.replyPending) - 1)
	}
	c.pendingMu.Unlock()
	if c.cl != nil {
		c.cl.Close()
	}
	if c.replyCl != nil {
		c.replyCl.Close()
	}
	return nil
}

func (c *franzClient) saslMechanism() sasl.Mechanism {
	if c.cfg.SASLUsername == "" && c.cfg.SASLPassword == "" {
		return nil
	}
	switch strings.ToLower(c.cfg.SASLMechanism) {
	case "scram-sha-256":
		return scram.Auth{User: c.cfg.SASLUsername, Pass: c.cfg.SASLPassword}.AsSha256Mechanism()
	case "scram-sha-512":
		return scram.Auth{User: c.cfg.SASLUsername, Pass: c.cfg.SASLPassword}.AsSha512Mechanism()
	default:
		return plain.Auth{User: c.cfg.SASLUsername, Pass: c.cfg.SASLPassword}.AsMechanism()
	}
}

func (c *franzClient) usesTLS() bool {
	return c.cfg.tlsConfigured() || strings.Contains(strings.ToLower(c.cfg.SecurityProtocol), "ssl")
}

func (c *franzClient) buildTLSConfig() (*tls.Config, error) {
	minimum, maximum, err := producerTLSVersions(c.cfg)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{MinVersion: minimum, MaxVersion: maximum, InsecureSkipVerify: c.cfg.TLSInsecureSkipVerify} //nolint:gosec // operator-controlled, mirrors HTTP/database TLS knobs
	certFile := strings.TrimSpace(c.cfg.TLSClientCertFile)
	keyFile := strings.TrimSpace(c.cfg.TLSClientKeyFile)
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("kafka TLS client certificate and key files must be configured together")
	}
	if certFile != "" {
		certPEM, err := fileasset.ReadCredential(certFile)
		if err != nil {
			return nil, fmt.Errorf("kafka tls client cert: %w", err)
		}
		keyPEM, err := fileasset.ReadCredential(keyFile)
		if err != nil {
			return nil, fmt.Errorf("kafka tls client key: %w", err)
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("kafka tls client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	if caFile := strings.TrimSpace(c.cfg.TLSCACertFile); caFile != "" {
		caPEM, err := fileasset.ReadCredential(caFile)
		if err != nil {
			return nil, fmt.Errorf("kafka tls ca cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("kafka tls ca cert %q contained no valid certificates", caFile)
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

func compressionCodec(name string) (kgo.CompressionCodec, bool) {
	switch strings.ToLower(name) {
	case "gzip":
		return kgo.GzipCompression(), true
	case "snappy":
		return kgo.SnappyCompression(), true
	case "lz4":
		return kgo.Lz4Compression(), true
	case "zstd":
		return kgo.ZstdCompression(), true
	default:
		return kgo.CompressionCodec{}, false
	}
}

func fromKgoRecord(rec *kgo.Record) kafkaRecord {
	headers := make(map[string]string, len(rec.Headers))
	for _, h := range rec.Headers {
		headers[h.Key] = string(h.Value)
	}
	return kafkaRecord{
		Key:           string(rec.Key),
		KeyPresent:    rec.Key != nil,
		Value:         rec.Value,
		Headers:       headers,
		Topic:         rec.Topic,
		Partition:     rec.Partition,
		Offset:        rec.Offset,
		LeaderEpoch:   rec.LeaderEpoch,
		Timestamp:     rec.Timestamp,
		CorrelationID: headers["kafka_correlation_id"],
	}
}

func (c *franzClient) initializeProducer(context.Context) error { _, err := c.handle(); return err }
func (c *franzClient) Flush(ctx context.Context) error {
	cl, err := c.handle()
	if err != nil {
		return err
	}
	return cl.Flush(ctx)
}

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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
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
	return &franzClient{cfg: cfg}
}

type franzClient struct {
	cfg          config
	once         sync.Once
	cl           *kgo.Client
	err          error
	replyOnce    sync.Once
	replyCl      *kgo.Client
	replyErr     error
	pendingMu    sync.Mutex
	pending      []pendingReply
	pendingBytes int64
}

const (
	maxPendingReplyRecords = 10_000
	maxPendingReplyBytes   = 64 << 20
	pendingReplyTTL        = 5 * time.Minute
)

type pendingReply struct {
	record     kafkaRecord
	receivedAt time.Time
	bytes      int64
}

// handle lazily constructs the underlying *kgo.Client so Init never blocks or
// fails; connection problems surface on first use / HealthCheck instead.
func (c *franzClient) handle() (*kgo.Client, error) {
	c.once.Do(func() {
		c.cl, c.err = c.newClient(c.cfg.ConsumerGroup)
	})
	return c.cl, c.err
}

// replyHandle uses a dedicated non-group consumer. A shared consumer group can
// assign a reply partition to a different VUser, whose private pending queue
// cannot satisfy the original requester's correlation.
func (c *franzClient) replyHandle() (*kgo.Client, error) {
	c.replyOnce.Do(func() {
		c.replyCl, c.replyErr = c.newClient("")
	})
	return c.replyCl, c.replyErr
}

func (c *franzClient) newClient(consumerGroup string) (*kgo.Client, error) {
	opts := []kgo.Opt{kgo.SeedBrokers(c.cfg.Brokers...)}
	if c.cfg.ClientID != "" {
		opts = append(opts, kgo.ClientID(c.cfg.ClientID))
	}
	if consumerGroup != "" {
		opts = append(opts, kgo.ConsumerGroup(consumerGroup))
	}
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
	if c.cfg.MaxMessageBytes > 0 {
		// Authoritative client-side cap (e.g. Event Hubs' 1 MiB limit): franz
		// rejects any record exceeding the batch-max, complementing the
		// conservative pre-flight guard in kafka.go checkRecordSize.
		opts = append(opts, kgo.ProducerBatchMaxBytes(int32(c.cfg.MaxMessageBytes)))
	}
	if strings.EqualFold(c.cfg.SASLMechanism, "oauthbearer") {
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
	if c.usesTLS() {
		tlsCfg, terr := c.buildTLSConfig()
		if terr != nil {
			return nil, terr
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}
	return kgo.NewClient(opts...)
}

func (c *franzClient) Produce(ctx context.Context, topic string, rec kafkaRecord) (recordMeta, error) {
	cl, err := c.handle()
	if err != nil {
		return recordMeta{}, err
	}
	r := &kgo.Record{Topic: topic, Value: rec.Value}
	if rec.Key != "" {
		r.Key = []byte(rec.Key)
	}
	if rec.Partition > 0 {
		r.Partition = rec.Partition
	}
	for k, v := range rec.Headers {
		r.Headers = append(r.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	results := cl.ProduceSync(ctx, r)
	if err := results.FirstErr(); err != nil {
		return recordMeta{}, err
	}
	produced := results[0].Record
	return recordMeta{
		Topic:     produced.Topic,
		Partition: produced.Partition,
		Offset:    produced.Offset,
		Timestamp: produced.Timestamp,
	}, nil
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
	cl.AddConsumeTopics(topic)
	c.pendingMu.Lock()
	for i, pending := range c.pending {
		if pending.record.Topic == topic {
			record := pending.record
			c.removePendingLocked(i)
			c.pendingMu.Unlock()
			return record, 0, nil
		}
	}
	c.pendingMu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fetches := cl.PollRecords(cctx, 10)
	if errs := fetches.Errors(); len(errs) > 0 {
		return kafkaRecord{}, 0, errs[0].Err
	}
	var rec *kgo.Record
	for _, candidate := range fetches.Records() {
		if candidate.Topic == topic {
			rec = candidate
			break
		}
	}
	for _, candidate := range fetches.Records() {
		if rec == nil || candidate != rec {
			c.pendingMu.Lock()
			c.addPendingLocked(fromKgoRecord(candidate))
			c.pendingMu.Unlock()
		}
	}
	if rec == nil {
		return kafkaRecord{}, 0, fmt.Errorf("no kafka record received from %q within %s", topic, timeout)
	}

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
	return fromKgoRecord(rec), lag, nil
}

func (c *franzClient) RequestReply(ctx context.Context, reqTopic, replyTopic string, req kafkaRecord, timeout time.Duration) (recordMeta, kafkaRecord, error) {
	cl, err := c.replyHandle()
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	cl.AddConsumeTopics(replyTopic)
	meta, err := c.Produce(ctx, reqTopic, req)
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.pendingMu.Lock()
		now := time.Now()
		for i := 0; i < len(c.pending); {
			pending := c.pending[i]
			if now.Sub(pending.receivedAt) > pendingReplyTTL {
				c.removePendingLocked(i)
				continue
			}
			if pending.record.Topic == replyTopic && (req.CorrelationID == "" || pending.record.CorrelationID == req.CorrelationID || pending.record.Headers["kafka_correlation_id"] == req.CorrelationID) {
				c.removePendingLocked(i)
				c.pendingMu.Unlock()
				return meta, pending.record, nil
			}
			i++
		}
		c.pendingMu.Unlock()
		cctx, cancel := context.WithDeadline(ctx, deadline)
		fetches := cl.PollRecords(cctx, 10)
		cancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			return recordMeta{}, kafkaRecord{}, errs[0].Err
		}
		var matched *kafkaRecord
		for _, rec := range fetches.Records() {
			converted := fromKgoRecord(rec)
			if matched == nil && converted.Topic == replyTopic && (req.CorrelationID == "" || converted.CorrelationID == req.CorrelationID || converted.Headers["kafka_correlation_id"] == req.CorrelationID) {
				candidate := converted
				matched = &candidate
				continue
			}
			c.pendingMu.Lock()
			c.addPendingLocked(converted)
			c.pendingMu.Unlock()
		}
		if matched != nil {
			return meta, *matched, nil
		}
	}
	return recordMeta{}, kafkaRecord{}, fmt.Errorf("no correlated kafka reply received from %q within %s", replyTopic, timeout)
}

func (c *franzClient) addPendingLocked(record kafkaRecord) {
	bytes := pendingReplyBytes(record)
	for len(c.pending) >= maxPendingReplyRecords || c.pendingBytes+bytes > maxPendingReplyBytes {
		if len(c.pending) == 0 {
			return
		}
		c.removePendingLocked(0)
	}
	c.pending = append(c.pending, pendingReply{record: record, receivedAt: time.Now(), bytes: bytes})
	c.pendingBytes += bytes
}

func (c *franzClient) removePendingLocked(index int) {
	if index < 0 || index >= len(c.pending) {
		return
	}
	c.pendingBytes -= c.pending[index].bytes
	copy(c.pending[index:], c.pending[index+1:])
	clear(c.pending[len(c.pending)-1:])
	c.pending = c.pending[:len(c.pending)-1]
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
		return cl.CommitUncommittedOffsets(ctx)
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
	tlsCfg := &tls.Config{InsecureSkipVerify: c.cfg.TLSInsecureSkipVerify} //nolint:gosec // operator-controlled, mirrors HTTP/database TLS knobs
	if c.cfg.TLSClientCertFile != "" && c.cfg.TLSClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.cfg.TLSClientCertFile, c.cfg.TLSClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("kafka tls client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	if c.cfg.TLSCACertFile != "" {
		caPEM, err := os.ReadFile(c.cfg.TLSCACertFile)
		if err != nil {
			return nil, fmt.Errorf("kafka tls ca cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("kafka tls ca cert %q contained no valid certificates", c.cfg.TLSCACertFile)
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
		Value:         rec.Value,
		Headers:       headers,
		Topic:         rec.Topic,
		Partition:     rec.Partition,
		Offset:        rec.Offset,
		Timestamp:     rec.Timestamp,
		CorrelationID: headers["kafka_correlation_id"],
	}
}

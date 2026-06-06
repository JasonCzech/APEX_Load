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

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

func newRealClient(cfg config) kafkaClient {
	return &franzClient{cfg: cfg}
}

type franzClient struct {
	cfg  config
	once sync.Once
	cl   *kgo.Client
	err  error
}

// handle lazily constructs the underlying *kgo.Client so Init never blocks or
// fails; connection problems surface on first use / HealthCheck instead.
func (c *franzClient) handle() (*kgo.Client, error) {
	c.once.Do(func() {
		opts := []kgo.Opt{kgo.SeedBrokers(c.cfg.Brokers...)}
		if c.cfg.ClientID != "" {
			opts = append(opts, kgo.ClientID(c.cfg.ClientID))
		}
		if c.cfg.ConsumerGroup != "" {
			opts = append(opts, kgo.ConsumerGroup(c.cfg.ConsumerGroup))
		}
		if strings.EqualFold(c.cfg.AutoOffsetReset, "earliest") {
			opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		} else {
			opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()))
		}
		switch strings.ToLower(c.cfg.Acks) {
		case "none":
			opts = append(opts, kgo.RequiredAcks(kgo.NoAck()), kgo.DisableIdempotentWrite())
		case "leader":
			opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
		default:
			opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
		}
		if codec, ok := compressionCodec(c.cfg.Compression); ok {
			opts = append(opts, kgo.ProducerBatchCompression(codec))
		}
		if mech := c.saslMechanism(); mech != nil {
			opts = append(opts, kgo.SASL(mech))
		}
		if c.usesTLS() {
			tlsCfg, terr := c.buildTLSConfig()
			if terr != nil {
				c.err = terr
				return
			}
			opts = append(opts, kgo.DialTLSConfig(tlsCfg))
		}
		c.cl, c.err = kgo.NewClient(opts...)
	})
	return c.cl, c.err
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

func (c *franzClient) Consume(ctx context.Context, topic string, _ string, timeout time.Duration) (kafkaRecord, int64, error) {
	cl, err := c.handle()
	if err != nil {
		return kafkaRecord{}, 0, err
	}
	cl.AddConsumeTopics(topic)

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fetches := cl.PollRecords(cctx, 1)
	if errs := fetches.Errors(); len(errs) > 0 {
		return kafkaRecord{}, 0, errs[0].Err
	}
	records := fetches.Records()
	if len(records) == 0 {
		return kafkaRecord{}, 0, fmt.Errorf("no kafka record received from %q within %s", topic, timeout)
	}
	rec := records[0]

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
	meta, err := c.Produce(ctx, reqTopic, req)
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	cl, err := c.handle()
	if err != nil {
		return recordMeta{}, kafkaRecord{}, err
	}
	cl.AddConsumeTopics(replyTopic)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithDeadline(ctx, deadline)
		fetches := cl.PollRecords(cctx, 10)
		cancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			return recordMeta{}, kafkaRecord{}, errs[0].Err
		}
		for _, rec := range fetches.Records() {
			converted := fromKgoRecord(rec)
			if req.CorrelationID == "" || converted.CorrelationID == req.CorrelationID || converted.Headers["kafka_correlation_id"] == req.CorrelationID {
				return meta, converted, nil
			}
		}
	}
	return recordMeta{}, kafkaRecord{}, fmt.Errorf("no correlated kafka reply received from %q within %s", replyTopic, timeout)
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

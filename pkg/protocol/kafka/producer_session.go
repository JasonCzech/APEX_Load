package kafka

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"time"
)

// ProducerSession is created by an engine run, never a process-global cache.
// VUser teardown releases no producer: the owner flushes/closes exactly once
// after all source-engine workers have drained.
type ProducerSession struct {
	mu       sync.Mutex
	entries  map[string]producerSessionEntry
	closed   bool
	closeErr error
}
type producerSessionEntry struct {
	cfg       config
	namespace string
	client    kafkaClient
}

func NewProducerSession() *ProducerSession {
	return &ProducerSession{entries: map[string]producerSessionEntry{}}
}
func (s *ProducerSession) acquire(ctx context.Context, key string, cfg config, namespace string, create func() kafkaClient) (kafkaClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("Kafka source producer session is closed")
	}
	if key == "" {
		return nil, fmt.Errorf("Kafka source producer identity is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg.Brokers = append([]string(nil), cfg.Brokers...)
	cfg.AllowedTargetHosts = append([]string(nil), cfg.AllowedTargetHosts...)
	cfg.Properties = maps.Clone(cfg.Properties)
	cfg.Topic = "" // Topic is sampler-local, not part of producer ownership.
	if entry, ok := s.entries[key]; ok {
		if !reflect.DeepEqual(entry.cfg, cfg) || entry.namespace != namespace {
			return nil, fmt.Errorf("conflicting Kafka source producer configuration")
		}
		return borrowedProducer{entry.client}, nil
	}
	client := create()
	if client == nil {
		return nil, fmt.Errorf("Kafka producer factory returned no client")
	}
	if initialize, ok := client.(interface{ initializeProducer(context.Context) error }); ok {
		if err := initialize.initializeProducer(ctx); err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = producerCleanupCall(func() error { return client.Teardown(cleanup) })
			cancel()
			return nil, err
		}
	}
	s.entries[key] = producerSessionEntry{cfg: cfg, namespace: namespace, client: client}
	return borrowedProducer{client}, nil
}
func (s *ProducerSession) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	for _, entry := range s.entries {
		if flush, ok := entry.client.(interface{ Flush(context.Context) error }); ok {
			s.closeErr = errors.Join(s.closeErr, producerCleanupCall(func() error { return flush.Flush(ctx) }))
		}
		s.closeErr = errors.Join(s.closeErr, producerCleanupCall(func() error { return entry.client.Teardown(ctx) }))
	}
	s.entries = nil
	return s.closeErr
}

type borrowedProducer struct{ kafkaClient }

func (borrowedProducer) Teardown(context.Context) error { return nil }

// Cleanup of one producer must not prevent the remaining session resources
// closing. Panic text is untrusted and may contain credentials.
func producerCleanupCall(call func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("Kafka producer cleanup panicked")
		}
	}()
	err = call()
	if err == nil {
		return nil
	}
	// This owner closes outside the VUser redaction boundary. Never publish
	// adapter error text, which can include broker credentials or token data.
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("Kafka producer cleanup failed")
}

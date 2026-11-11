package kafka

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"time"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const maxProducerSessionCachedTokens = 64

type producerTokenListener struct {
	id uint64
	fn func(httpprotocol.OAuthToken)
}

type producerTokenBroadcaster struct {
	mu        sync.Mutex
	nextID    uint64
	listeners []producerTokenListener
	seen      []httpprotocol.OAuthToken
}

func (b *producerTokenBroadcaster) add(fn func(httpprotocol.OAuthToken)) ([]httpprotocol.OAuthToken, func()) {
	if b == nil || fn == nil {
		return nil, func() {}
	}
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.listeners = append(b.listeners, producerTokenListener{id: id, fn: fn})
	replay := append([]httpprotocol.OAuthToken(nil), b.seen...)
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			for i := range b.listeners {
				if b.listeners[i].id == id {
					copy(b.listeners[i:], b.listeners[i+1:])
					b.listeners[len(b.listeners)-1] = producerTokenListener{}
					b.listeners = b.listeners[:len(b.listeners)-1]
					break
				}
			}
			b.mu.Unlock()
		})
	}
	return replay, unsubscribe
}

func (b *producerTokenBroadcaster) notify(tok httpprotocol.OAuthToken) {
	if b == nil {
		return
	}
	b.mu.Lock()
	alreadySeen := false
	for _, existing := range b.seen {
		if existing.AccessToken == tok.AccessToken && existing.RefreshToken == tok.RefreshToken {
			alreadySeen = true
			break
		}
	}
	if !alreadySeen {
		if len(b.seen) < maxProducerSessionCachedTokens {
			b.seen = append(b.seen, tok)
		} else if maxProducerSessionCachedTokens > 0 {
			copy(b.seen, b.seen[1:])
			b.seen[len(b.seen)-1] = tok
		}
	}
	listeners := make([]func(httpprotocol.OAuthToken), len(b.listeners))
	for i, l := range b.listeners {
		listeners[i] = l.fn
	}
	b.mu.Unlock()
	for _, fn := range listeners {
		fn(tok)
	}
}

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
	tokens    *producerTokenBroadcaster
}

func NewProducerSession() *ProducerSession {
	return &ProducerSession{entries: map[string]producerSessionEntry{}}
}

func (s *ProducerSession) acquire(ctx context.Context, key string, cfg config, namespace string, create func() kafkaClient) (kafkaClient, error) {
	return s.acquireWithConfig(ctx, key, cfg, namespace, func(config) kafkaClient { return create() })
}

func (s *ProducerSession) acquireWithConfig(ctx context.Context, key string, cfg config, namespace string, create func(config) kafkaClient) (kafkaClient, error) {
	onToken := cfg.OnOAuthToken
	client, replay, err := s.acquireLocked(ctx, key, cfg, namespace, create)
	if err != nil {
		return nil, err
	}
	if onToken != nil {
		for _, tok := range replay {
			onToken(tok)
		}
	}
	return client, nil
}

func (s *ProducerSession) acquireLocked(ctx context.Context, key string, cfg config, namespace string, create func(config) kafkaClient) (kafkaClient, []httpprotocol.OAuthToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, fmt.Errorf("Kafka source producer session is closed")
	}
	if key == "" {
		return nil, nil, fmt.Errorf("Kafka source producer identity is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	onToken := cfg.OnOAuthToken
	cfg.OnOAuthToken = nil // Callback is VUser-local, not part of producer identity.
	cfg.Brokers = append([]string(nil), cfg.Brokers...)
	cfg.AllowedTargetHosts = append([]string(nil), cfg.AllowedTargetHosts...)
	cfg.Properties = maps.Clone(cfg.Properties)
	cfg.Topic = "" // Topic is sampler-local, not part of producer ownership.
	if entry, ok := s.entries[key]; ok {
		if !reflect.DeepEqual(entry.cfg, cfg) || entry.namespace != namespace {
			return nil, nil, fmt.Errorf("conflicting Kafka source producer configuration")
		}
		replay, unsubscribe := entry.tokens.add(onToken)
		return borrowedProducer{kafkaClient: entry.client, unsubscribe: unsubscribe}, replay, nil
	}
	tokens := &producerTokenBroadcaster{}
	replay, unsubscribe := tokens.add(onToken)
	clientCfg := cfg
	clientCfg.OnOAuthToken = tokens.notify
	client := create(clientCfg)
	if client == nil {
		unsubscribe()
		return nil, nil, fmt.Errorf("Kafka producer factory returned no client")
	}
	if initialize, ok := client.(interface{ initializeProducer(context.Context) error }); ok {
		if err := initialize.initializeProducer(ctx); err != nil {
			unsubscribe()
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = producerCleanupCall(func() error { return client.Teardown(cleanup) })
			cancel()
			return nil, nil, err
		}
	}
	s.entries[key] = producerSessionEntry{cfg: cfg, namespace: namespace, client: client, tokens: tokens}
	return borrowedProducer{kafkaClient: client, unsubscribe: unsubscribe}, replay, nil
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

type borrowedProducer struct {
	kafkaClient
	unsubscribe func()
}

func (b borrowedProducer) Teardown(context.Context) error {
	if b.unsubscribe != nil {
		b.unsubscribe()
	}
	return nil
}

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

package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type sessionTraceClient struct {
	kafkaClient
	created, flushed, closed atomic.Int64
	fail                     bool
}

func (c *sessionTraceClient) initializeProducer(context.Context) error { c.created.Add(1); return nil }
func (c *sessionTraceClient) Flush(context.Context) error {
	c.flushed.Add(1)
	if c.fail {
		return errors.New("flush failed")
	}
	return nil
}
func (c *sessionTraceClient) Teardown(context.Context) error { c.closed.Add(1); return nil }
func TestProducerSessionSharedLifetime(t *testing.T) {
	s := NewProducerSession()
	client := &sessionTraceClient{}
	var constructs atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			borrowed, err := s.acquire(context.Background(), "plan/config", config{}, "run", func() kafkaClient { constructs.Add(1); return client })
			if err != nil {
				t.Error(err)
				return
			}
			if err = borrowed.Teardown(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if constructs.Load() != 1 || client.created.Load() != 1 || client.closed.Load() != 0 {
		t.Fatal("producer lifetime not shared")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.flushed.Load() != 1 || client.closed.Load() != 1 {
		t.Fatal("cleanup not once")
	}
	if _, err := s.acquire(context.Background(), "plan/config", config{}, "run", func() kafkaClient { return client }); err == nil {
		t.Fatal("reopened closed run")
	}
}
func TestProducerSessionConflictsAndFailureCleanup(t *testing.T) {
	s := NewProducerSession()
	client := &sessionTraceClient{fail: true}
	cfg := config{Brokers: []string{"broker"}, Properties: map[string]string{"key": "value"}, Topic: "one"}
	create := func() kafkaClient { return client }
	if _, err := s.acquire(context.Background(), "plan/config", cfg, "run", create); err != nil {
		t.Fatal(err)
	}
	cfg.Topic = "two"
	if _, err := s.acquire(context.Background(), "plan/config", cfg, "run", create); err != nil {
		t.Fatal("topic is sampler-local", err)
	}
	cfg.Properties["key"] = "mutated"
	if _, err := s.acquire(context.Background(), "plan/config", cfg, "run", create); err == nil {
		t.Fatal("mutated shared config accepted")
	}
	if err := s.Close(context.Background()); err == nil {
		t.Fatal("flush failure lost")
	}
	if client.closed.Load() != 1 {
		t.Fatal("flush failure skipped close")
	}
	fresh := NewProducerSession()
	other := &sessionTraceClient{}
	if _, err := fresh.acquire(context.Background(), "plan/config", cfg, "run", func() kafkaClient { return other }); err != nil {
		t.Fatal(err)
	}
	defer fresh.Close(context.Background())
	if other.created.Load() != 1 {
		t.Fatal("cross-run producer reuse")
	}
}

type panicCleanupClient struct {
	sessionTraceClient
	panicFlush, panicClose bool
}

func (c *panicCleanupClient) Flush(ctx context.Context) error {
	if c.panicFlush {
		panic("credential-bearing panic")
	}
	return c.sessionTraceClient.Flush(ctx)
}
func (c *panicCleanupClient) Teardown(ctx context.Context) error {
	c.closed.Add(1)
	if c.panicClose {
		panic("credential-bearing panic")
	}
	return nil
}
func TestProducerSessionCleanupPanicDoesNotLeakOtherClients(t *testing.T) {
	session := NewProducerSession()
	bad := &panicCleanupClient{panicFlush: true, panicClose: true}
	good := &sessionTraceClient{}
	for key, client := range map[string]kafkaClient{"bad": bad, "good": good} {
		if _, err := session.acquire(context.Background(), key, config{}, "run", func() kafkaClient { return client }); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Close(context.Background()); err == nil || strings.Contains(err.Error(), "credential-bearing") {
		t.Fatal("missing safe cleanup error")
	}
	if bad.closed.Load() != 1 || good.closed.Load() != 1 || good.flushed.Load() != 1 {
		t.Fatal("cleanup skipped another producer")
	}
}

func TestKafkaReinitializationDoesNotRetainSharedMode(t *testing.T) {
	session := NewProducerSession()
	defer session.Close(context.Background())
	k := &Kafka{name: "kafka"}
	if err := k.Init(context.Background(), map[string]any{"kafka_mock": true, "kafka_producer_lifecycle": "source_engine", "kafka_producer_session": session, "kafka_source_configuration": "plan/config"}); err != nil {
		t.Fatal(err)
	}
	if !k.sharedProducer {
		t.Fatal("shared mode missing")
	}
	if err := k.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := k.Init(context.Background(), map[string]any{"kafka_mock": true}); err != nil {
		t.Fatal(err)
	}
	defer k.Teardown(context.Background())
	if k.sharedProducer {
		t.Fatal("shared mode leaked to a new initialization")
	}
}

func TestProducerCleanupErrorsDoNotDiscloseAdapterText(t *testing.T) {
	for _, cause := range []error{errors.New("credential-canary"), fmt.Errorf("credential-canary: %w", context.Canceled), fmt.Errorf("credential-canary: %w", context.DeadlineExceeded)} {
		err := producerCleanupCall(func() error { return cause })
		if err == nil || strings.Contains(err.Error(), "credential-canary") {
			t.Fatal("unsafe cleanup error", err)
		}
		for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
			if errors.Is(err, sentinel) != errors.Is(cause, sentinel) {
				t.Fatal("lost context classification", err)
			}
		}
	}
}

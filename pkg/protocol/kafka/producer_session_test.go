package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
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

func TestProducerSessionMultipleKafkaInstancesShareProducerAndBroadcastOAuthTokens(t *testing.T) {
	session := NewProducerSession()
	defer session.Close(context.Background())

	k1 := &Kafka{name: "kafka"}
	k2 := &Kafka{name: "kafka"}
	var got1, got2 []string
	k1.SetRuntimeSecretRegistrar(func(_ string, vals []string) { got1 = append(got1, vals...) })
	k2.SetRuntimeSecretRegistrar(func(_ string, vals []string) { got2 = append(got2, vals...) })

	cfg := map[string]any{
		"kafka_mock":                 true,
		"kafka_topic":                "topic-a",
		"kafka_producer_lifecycle":   "source_engine",
		"kafka_producer_session":     session,
		"kafka_source_configuration": "plan/shared",
	}
	if err := k1.Init(context.Background(), cfg); err != nil {
		t.Fatalf("k1.Init: %v", err)
	}
	cfg2 := map[string]any{
		"kafka_mock":                 true,
		"kafka_topic":                "topic-b",
		"kafka_producer_lifecycle":   "source_engine",
		"kafka_producer_session":     session,
		"kafka_source_configuration": "plan/shared",
	}
	if err := k2.Init(context.Background(), cfg2); err != nil {
		t.Fatalf("k2.Init failed on shared producer session: %v", err)
	}

	// Verify acquireWithConfig relays pre-existing and subsequent OAuth tokens to all borrowers.
	tokenSession := NewProducerSession()
	defer tokenSession.Close(context.Background())
	var relayCfg config
	_, err := tokenSession.acquireWithConfig(context.Background(), "oauth-key", config{
		OnOAuthToken: func(tok httpprotocol.OAuthToken) { got1 = append(got1, "r1:"+tok.AccessToken) },
	}, "run", func(effective config) kafkaClient {
		relayCfg = effective
		return &sessionTraceClient{}
	})
	if err != nil {
		t.Fatalf("first acquireWithConfig: %v", err)
	}
	relayCfg.OnOAuthToken(httpprotocol.OAuthToken{AccessToken: "tok-initial"})
	_, err = tokenSession.acquireWithConfig(context.Background(), "oauth-key", config{
		OnOAuthToken: func(tok httpprotocol.OAuthToken) { got2 = append(got2, "r2:"+tok.AccessToken) },
	}, "run", func(config) kafkaClient {
		t.Fatal("unexpected second client construction")
		return nil
	})
	if err != nil {
		t.Fatalf("second acquireWithConfig: %v", err)
	}
	relayCfg.OnOAuthToken(httpprotocol.OAuthToken{AccessToken: "tok-rotated"})
	if strings.Join(got1, ",") != "r1:tok-initial,r1:tok-rotated" || strings.Join(got2, ",") != "r2:tok-initial,r2:tok-rotated" {
		t.Fatalf("unexpected token broadcast: got1=%v got2=%v", got1, got2)
	}

	// Finding 7 regression: rotating > maxProducerSessionCachedTokens (64) evicts
	// oldest tokens so late subscribers receive the most recent active tokens.
	ringSession := NewProducerSession()
	defer ringSession.Close(context.Background())
	var ringCfg config
	_, err = ringSession.acquireWithConfig(context.Background(), "ring-key", config{}, "run", func(effective config) kafkaClient {
		ringCfg = effective
		return &sessionTraceClient{}
	})
	if err != nil {
		t.Fatalf("ringSession initial acquire: %v", err)
	}
	const totalRotations = 70
	for i := 1; i <= totalRotations; i++ {
		ringCfg.OnOAuthToken(httpprotocol.OAuthToken{AccessToken: fmt.Sprintf("rot-tok-%d", i)})
	}
	var lateSeen []string
	_, err = ringSession.acquireWithConfig(context.Background(), "ring-key", config{
		OnOAuthToken: func(tok httpprotocol.OAuthToken) { lateSeen = append(lateSeen, tok.AccessToken) },
	}, "run", func(config) kafkaClient {
		t.Fatal("unexpected client construction on ringSession")
		return nil
	})
	if err != nil {
		t.Fatalf("ringSession late acquire: %v", err)
	}
	if len(lateSeen) != maxProducerSessionCachedTokens {
		t.Fatalf("expected %d cached tokens replayed, got %d", maxProducerSessionCachedTokens, len(lateSeen))
	}
	if lateSeen[0] != "rot-tok-7" || lateSeen[len(lateSeen)-1] != "rot-tok-70" {
		t.Fatalf("expected sliding window [rot-tok-7..rot-tok-70], got [%s..%s]", lateSeen[0], lateSeen[len(lateSeen)-1])
	}

	// Round-8 Finding 4 regression: borrowedProducer.Teardown unsubscribes its
	// per-VUser OAuth listener so finished open-arrival VUsers are not retained
	// across the run, while active and new borrowers keep receiving tokens.
	unsubSession := NewProducerSession()
	defer unsubSession.Close(context.Background())
	var unsubCfg config
	var b1Seen, b2Seen, b3Seen []string
	b1, err := unsubSession.acquireWithConfig(context.Background(), "unsub-key", config{
		OnOAuthToken: func(tok httpprotocol.OAuthToken) { b1Seen = append(b1Seen, tok.AccessToken) },
	}, "run", func(effective config) kafkaClient {
		unsubCfg = effective
		return &sessionTraceClient{}
	})
	if err != nil {
		t.Fatalf("b1 acquire: %v", err)
	}
	b2, err := unsubSession.acquireWithConfig(context.Background(), "unsub-key", config{
		OnOAuthToken: func(tok httpprotocol.OAuthToken) { b2Seen = append(b2Seen, tok.AccessToken) },
	}, "run", func(config) kafkaClient { return nil })
	if err != nil {
		t.Fatalf("b2 acquire: %v", err)
	}
	defer b2.Teardown(context.Background())
	unsubCfg.OnOAuthToken(httpprotocol.OAuthToken{AccessToken: "u-tok-1"})
	if err := b1.Teardown(context.Background()); err != nil {
		t.Fatalf("b1 Teardown: %v", err)
	}
	// Idempotent double Teardown must also be safe.
	if err := b1.Teardown(context.Background()); err != nil {
		t.Fatalf("b1 second Teardown: %v", err)
	}
	unsubCfg.OnOAuthToken(httpprotocol.OAuthToken{AccessToken: "u-tok-2"})
	if strings.Join(b1Seen, ",") != "u-tok-1" {
		t.Fatalf("torn-down borrower b1 still received token refresh after Teardown: %v", b1Seen)
	}
	if strings.Join(b2Seen, ",") != "u-tok-1,u-tok-2" {
		t.Fatalf("active borrower b2 missed token refresh: %v", b2Seen)
	}
	b3, err := unsubSession.acquireWithConfig(context.Background(), "unsub-key", config{
		OnOAuthToken: func(tok httpprotocol.OAuthToken) { b3Seen = append(b3Seen, tok.AccessToken) },
	}, "run", func(config) kafkaClient { return nil })
	if err != nil {
		t.Fatalf("b3 acquire: %v", err)
	}
	defer b3.Teardown(context.Background())
	if strings.Join(b3Seen, ",") != "u-tok-1,u-tok-2" {
		t.Fatalf("new borrower b3 missed seen token replay: %v", b3Seen)
	}
}

package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type groupCommitClient struct {
	kafkaClient
	commits int
}

func (c *groupCommitClient) Commit(ctx context.Context) error {
	c.commits++
	return c.kafkaClient.Commit(ctx)
}

func TestKafkaCommitConsumerGroupFence(t *testing.T) {
	for _, test := range []struct {
		name       string
		mock       bool
		configured string
		params     map[string]any
		allowed    bool
	}{
		{name: "real matching", configured: "workers", params: map[string]any{"consumer_group": "workers"}, allowed: true},
		{name: "real fallback", configured: "workers", allowed: true},
		{name: "real blank fallback", configured: "workers", params: map[string]any{"consumer_group": " \t"}, allowed: true},
		{name: "real mismatch", configured: "workers", params: map[string]any{"consumer_group": "others"}},
		{name: "resolved dynamic mismatch", configured: "workers", params: map[string]any{"consumer_group": "runtime-others"}},
		{name: "resolved dynamic match", configured: "runtime-workers", params: map[string]any{"consumer_group": "runtime-workers"}, allowed: true},
		{name: "real padded mismatch", configured: "workers", params: map[string]any{"consumer_group": " workers "}},
		{name: "real exact padded identity", configured: " workers ", params: map[string]any{"consumer_group": " workers "}, allowed: true},
		{name: "real missing configured group", params: map[string]any{"consumer_group": "workers"}},
		{name: "real group-free flush", allowed: true},
		{name: "mock override preserved", mock: true, configured: "workers", params: map[string]any{"consumer_group": "others"}, allowed: true},
		{name: "mock unconfigured override preserved", mock: true, params: map[string]any{"consumer_group": "workers"}, allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			k := newMockKafka(t, map[string]any{"kafka_consumer_group": test.configured})
			// The local client isolates the admission check in both build profiles.
			// The real-tag test separately proves the native handle stays unopened.
			k.cfg.Mock = test.mock
			client := &groupCommitClient{kafkaClient: k.client}
			k.client = client
			result, err := k.Execute(context.Background(), &protocol.Action{Name: "commit", Type: "kafka_commit", Params: test.params})
			if err != nil || result.Success != test.allowed {
				t.Fatalf("allowed=%t result=%+v err=%v", test.allowed, result, err)
			}
			want := 0
			if test.allowed {
				want = 1
				if result.ServerTelemetry == nil || result.ServerTelemetry.CommitCount != 1 {
					t.Fatalf("missing successful commit telemetry: %+v", result.ServerTelemetry)
				}
			} else {
				if result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, "one group per client") {
					t.Fatalf("missing admission refusal: %+v", result)
				}
				if result.Extractions["kafka_operation"] != "" || result.ServerTelemetry != nil && result.ServerTelemetry.CommitCount != 0 {
					t.Fatalf("refused commit became successful evidence: %+v", result)
				}
			}
			if client.commits != want || k.session.Commits() != want {
				t.Fatalf("broker calls=%d session commits=%d want=%d", client.commits, k.session.Commits(), want)
			}
		})
	}
}

func TestConsumerGroupOverrideRefusalDoesNotDiscloseIdentities(t *testing.T) {
	err := ValidateConsumerGroupOverride("private-authored-group", "private-configured-group")
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatalf("group identity disclosed by refusal: %v", err)
	}
}

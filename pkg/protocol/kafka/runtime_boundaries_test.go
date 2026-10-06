package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestKafkaInitRejectsUnsupportedIsolationBeforeClientCreation(t *testing.T) {
	values := []struct {
		name  string
		value any
	}{
		{"misspelled", "read_commited"},
		{"unsupported", "snapshot"},
		{"unsupported_normalized", "  READ_SERIALIZABLE  "},
		{"literal_default", "default"},
		{"non_string", 1},
		{"null", nil},
	}
	for _, mock := range []bool{false, true} {
		for _, test := range values {
			t.Run(fmt.Sprintf("mock=%t/%s", mock, test.name), func(t *testing.T) {
				instance := &Kafka{name: "kafka"}
				err := instance.Init(context.Background(), map[string]any{
					"kafka_mock": mock, "kafka_brokers": []string{"127.0.0.1:1"},
					"kafka_isolation_level": test.value,
				})
				if err == nil || !strings.Contains(err.Error(), "kafka_isolation_level") {
					t.Fatalf("unsupported isolation error = %v", err)
				}
				if instance.client != nil || instance.initialized || instance.session != nil {
					t.Fatal("invalid isolation created or initialized a client")
				}
			})
		}
	}
}

func TestKafkaInitNormalizesSupportedIsolation(t *testing.T) {
	tests := []struct {
		name       string
		configured bool
		value      string
		want       string
	}{
		{"omitted", false, "", ""},
		{"empty", true, "", ""},
		{"whitespace_default", true, " \t", ""},
		{"committed", true, "read_committed", "read_committed"},
		{"committed_normalized", true, "  READ_COMMITTED  ", "read_committed"},
		{"uncommitted", true, "read_uncommitted", "read_uncommitted"},
		{"uncommitted_normalized", true, " Read_Uncommitted\t", "read_uncommitted"},
	}
	for _, mock := range []bool{false, true} {
		for _, test := range tests {
			t.Run(fmt.Sprintf("mock=%t/%s", mock, test.name), func(t *testing.T) {
				instance := &Kafka{name: "kafka"}
				cfg := map[string]any{
					"kafka_mock": mock, "kafka_brokers": []string{"127.0.0.1:1"},
					"runtime_mock_namespace": t.Name(),
				}
				if test.configured {
					cfg["kafka_isolation_level"] = test.value
				}
				// Init must stay traffic-free even if its context is already cancelled.
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := instance.Init(ctx, cfg); err != nil {
					t.Fatalf("Init: %v", err)
				}
				t.Cleanup(func() {
					if err := instance.Teardown(context.Background()); err != nil {
						t.Errorf("Teardown: %v", err)
					}
				})
				if instance.cfg.IsolationLevel != test.want {
					t.Fatalf("isolation = %q, want %q", instance.cfg.IsolationLevel, test.want)
				}
			})
		}
	}
}

// The wrapper injects provider cleanup failures while retaining the actual mock
// broker lifecycle, so the test can verify cleanup still releases its namespace.
type teardownBoundaryClient struct {
	kafkaClient
	abortErr    error
	teardownErr error
	calls       []string
}

func (c *teardownBoundaryClient) AbortTransaction(ctx context.Context) error {
	c.calls = append(c.calls, "abort")
	if c.abortErr != nil {
		return c.abortErr
	}
	return c.kafkaClient.AbortTransaction(ctx)
}

func (c *teardownBoundaryClient) Teardown(ctx context.Context) error {
	c.calls = append(c.calls, "teardown")
	return errors.Join(c.kafkaClient.Teardown(ctx), c.teardownErr)
}

func TestKafkaTeardownPreservesFailuresAndConfirmedAbortAccounting(t *testing.T) {
	abortErr := errors.New("provider abort unconfirmed")
	cleanupErr := errors.New("provider physical cleanup failed")
	tests := []struct {
		name       string
		open       bool
		abortErr   error
		cleanupErr error
		wantAborts int
		wantPoison bool
		wantCalls  string
	}{
		{"no_transaction", false, nil, nil, 0, false, "teardown"},
		{"cleanup_failure_without_transaction", false, nil, cleanupErr, 0, false, "teardown"},
		{"confirmed_abort", true, nil, nil, 1, false, "abort,teardown"},
		{"confirmed_abort_cleanup_failure", true, nil, cleanupErr, 1, false, "abort,teardown"},
		{"unconfirmed_abort", true, abortErr, nil, 0, true, "abort,teardown"},
		{"both_failures", true, abortErr, cleanupErr, 0, true, "abort,teardown"},
		{"cancelled_abort", true, context.Canceled, nil, 0, true, "abort,teardown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := &Kafka{name: "kafka"}
			namespace := t.Name()
			if err := instance.Init(context.Background(), map[string]any{
				"kafka_mock": true, "kafka_transactional_id": "teardown-boundary",
				"runtime_mock_namespace": namespace,
			}); err != nil {
				t.Fatal(err)
			}
			client := &teardownBoundaryClient{kafkaClient: instance.client, abortErr: test.abortErr, teardownErr: test.cleanupErr}
			instance.client = client
			// Release the acquired namespace even if an earlier assertion fails.
			t.Cleanup(func() { _ = client.kafkaClient.Teardown(context.Background()) })
			if test.open {
				for _, action := range []*protocol.Action{
					{Name: "begin", Type: "kafka_begin_transaction"},
					{Name: "buffer", Type: "kafka_produce", Target: "orders", Body: []byte("pending")},
				} {
					result, err := instance.Execute(context.Background(), action)
					if err != nil || !result.Success {
						t.Fatalf("%s: result=%#v error=%v", action.Type, result, err)
					}
				}
			}

			err := instance.Teardown(context.Background())
			for _, cause := range []error{test.abortErr, test.cleanupErr} {
				if cause != nil && !errors.Is(err, cause) {
					t.Fatalf("Teardown error %v does not retain %v", err, cause)
				}
			}
			if test.abortErr == nil && test.cleanupErr == nil && err != nil {
				t.Fatalf("unexpected Teardown error: %v", err)
			}
			if got := strings.Join(client.calls, ","); got != test.wantCalls {
				t.Fatalf("cleanup calls = %q, want %q", got, test.wantCalls)
			}
			if instance.txOpen || len(instance.txBuffer) != 0 || instance.txBytes != 0 {
				t.Fatal("teardown retained an open transaction or buffered records")
			}
			if instance.txPoisoned != test.wantPoison || instance.session.Aborts() != test.wantAborts {
				t.Fatalf("poisoned=%t aborts=%d, want poisoned=%t aborts=%d", instance.txPoisoned, instance.session.Aborts(), test.wantPoison, test.wantAborts)
			}
			sharedBroker.mu.Lock()
			_, reserved := sharedBroker.refs[namespace]
			sharedBroker.mu.Unlock()
			if reserved {
				t.Fatal("physical teardown did not release the mock namespace")
			}
			if test.wantPoison {
				result, err := instance.Execute(context.Background(), &protocol.Action{Name: "retry", Type: "kafka_begin_transaction"})
				if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "unresolved") {
					t.Fatalf("uncertain transaction reused after teardown: result=%#v error=%v", result, err)
				}
			}
			// A second teardown must not retry an uncertain transaction or count
			// another rollback after the local state was made terminal.
			client.calls = nil
			_ = instance.Teardown(context.Background())
			if strings.Join(client.calls, ",") != "teardown" || instance.session.Aborts() != test.wantAborts || instance.txPoisoned != test.wantPoison {
				t.Fatal("repeated teardown retried the abort or changed its accounting")
			}
		})
	}
}

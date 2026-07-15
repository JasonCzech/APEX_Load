package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type retryBoundsProtocol struct{ calls int }

func (p *retryBoundsProtocol) Name() string                               { return "retry_bounds" }
func (p *retryBoundsProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *retryBoundsProtocol) Teardown(context.Context) error             { return nil }
func (p *retryBoundsProtocol) HealthCheck(context.Context) error          { return nil }
func (p *retryBoundsProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	p.calls++
	return &protocol.Result{Success: false}, nil
}

func TestExecuteWithRetryRejectsUnboundedConfigBeforeExecution(t *testing.T) {
	adapter := &retryBoundsProtocol{}
	vu := &VUser{Proto: adapter}
	_, err := vu.executeWithRetry(context.Background(), &protocol.Action{Name: "request"}, dsl.ErrorHandlingConfig{
		MaxRetries: dsl.MaxErrorRetries + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "max_retries") {
		t.Fatalf("retry validation error = %v, want max_retries rejection", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter calls = %d, want zero before invalid retry config is rejected", adapter.calls)
	}
}

func TestBoundedRetryBackoffSaturates(t *testing.T) {
	if got := boundedRetryBackoff(time.Hour, 10); got != dsl.MaxConfiguredProtocolTimeout {
		t.Fatalf("boundedRetryBackoff = %s, want %s", got, dsl.MaxConfiguredProtocolTimeout)
	}
	if got := boundedRetryBackoff(time.Second, 3); got != 4*time.Second {
		t.Fatalf("boundedRetryBackoff = %s, want 4s", got)
	}
}

func TestExecuteWithRetryMatchesCaseInsensitiveValidation(t *testing.T) {
	adapter := &retryBoundsProtocol{}
	vu := &VUser{Proto: adapter}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := vu.executeWithRetry(ctx, &protocol.Action{Name: "request"}, dsl.ErrorHandlingConfig{
		MaxRetries:   1,
		RetryDelayMs: 1,
		RetryBackoff: " Exponential ",
	})
	if err != nil {
		t.Fatalf("mixed-case validated retry backoff returned error: %v", err)
	}
	if adapter.calls != 2 {
		t.Fatalf("adapter calls = %d, want initial attempt plus retry", adapter.calls)
	}
}

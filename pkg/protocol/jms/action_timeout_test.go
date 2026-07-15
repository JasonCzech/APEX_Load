package jms

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type blockingJMSPublishAdapter struct{}

func (blockingJMSPublishAdapter) Publish(ctx context.Context, _ string, _ message) (message, error) {
	<-ctx.Done()
	return message{}, ctx.Err()
}

func (blockingJMSPublishAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, nil
}

func (blockingJMSPublishAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, nil
}

func (blockingJMSPublishAdapter) HealthCheck(context.Context) error { return nil }
func (blockingJMSPublishAdapter) Teardown(context.Context) error    { return nil }

func TestJMSActionTimeoutBoundsPublish(t *testing.T) {
	const timeout = 20 * time.Millisecond
	j := &JMS{
		name:    "jms",
		cfg:     config{Destination: "orders", Timeout: timeout},
		adapter: blockingJMSPublishAdapter{},
	}
	start := time.Now()
	result, err := j.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, context.DeadlineExceeded.Error()) {
		t.Fatalf("result = %#v, want deadline failure", result)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("publish took %s, want configured %s action timeout", elapsed, timeout)
	}
}

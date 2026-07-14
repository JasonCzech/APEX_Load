//go:build kafka_real

package kafka

import (
	"context"
	"testing"
	"time"
)

func TestPrimeReplyConsumerReturnsForExistingAssignment(t *testing.T) {
	client := &franzClient{replyAssigned: map[string]bool{"replies": true}}
	if err := client.primeReplyConsumer(context.Background(), nil, "replies", time.Second); err != nil {
		t.Fatalf("prime existing assignment: %v", err)
	}
}

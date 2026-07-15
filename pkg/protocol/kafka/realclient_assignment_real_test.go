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

func TestReplyHandleBuildsValidDedicatedGroupConsumer(t *testing.T) {
	client := &franzClient{cfg: config{Brokers: []string{"127.0.0.1:1"}}}
	handle, err := client.replyHandle()
	if err != nil {
		t.Fatalf("replyHandle rejected its consumer configuration: %v", err)
	}
	if handle == nil {
		t.Fatal("replyHandle returned a nil client")
	}
	handle.Close()
}

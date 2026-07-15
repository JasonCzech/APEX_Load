//go:build jms_solace_real

package jms

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	amqp "github.com/Azure/go-amqp"
)

func TestSolaceAwaitCorrelatedReplyReleasesEveryForeignReplyDelivery(t *testing.T) {
	foreign := solaceTestReply("foreign", "other")
	match := solaceTestReply("match", "mine")
	receiver := &fakeSolaceReplyReceiver{messages: []*amqp.Message{foreign, foreign, match}}
	reply, err := solaceAwaitCorrelatedReply(context.Background(), receiver, "reply.q", message{CorrelationID: "mine"}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if reply.ID != "match" || receiver.accepted != 1 {
		t.Fatalf("reply/accept = %#v/%d", reply, receiver.accepted)
	}
	if receiver.released != 2 {
		t.Fatalf("foreign release count = %d, want every one of the 2 deliveries released", receiver.released)
	}
}

func TestSolaceResourceAdmissionBoundsCachedAndTransientLinks(t *testing.T) {
	adapter := &solaceAMQPAdapter{
		senders:   make(map[string]*amqp.Sender),
		receivers: make(map[string]*amqp.Receiver),
	}
	for i := 0; i < maxSolaceCachedLinks; i++ {
		adapter.senders[fmt.Sprintf("sender-%d", i)] = nil
	}
	if err := adapter.cachedLinkAdmissionErrorLocked("overflow"); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("cached link overflow error = %v", err)
	}
	adapter.transientLinks = maxSolaceTransientLinks
	if _, _, err := adapter.openReceiver(context.Background(), "overflow", nil); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("transient link overflow error = %v", err)
	}
}

func TestSolaceCloseUsesDeadline(t *testing.T) {
	start := time.Now()
	err := closeSolaceResourceWithin(context.Background(), stuckSolaceCloser{}, 20*time.Millisecond)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("bounded close = %v after %s", err, time.Since(start))
	}
}

type fakeSolaceReplyReceiver struct {
	messages []*amqp.Message
	accepted int
	released int
}

func (r *fakeSolaceReplyReceiver) Receive(ctx context.Context, _ *amqp.ReceiveOptions) (*amqp.Message, error) {
	if len(r.messages) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := r.messages[0]
	r.messages = r.messages[1:]
	return msg, nil
}

func (r *fakeSolaceReplyReceiver) AcceptMessage(context.Context, *amqp.Message) error {
	r.accepted++
	return nil
}

func (r *fakeSolaceReplyReceiver) ReleaseMessage(context.Context, *amqp.Message) error {
	r.released++
	return nil
}

func solaceTestReply(id, correlation string) *amqp.Message {
	return &amqp.Message{Properties: &amqp.MessageProperties{MessageID: id, CorrelationID: correlation}}
}

type stuckSolaceCloser struct{}

func (stuckSolaceCloser) Close(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

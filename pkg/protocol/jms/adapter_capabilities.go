package jms

import (
	"context"
	"time"
)

// brokerAdapter is the provider-neutral data-plane contract used by the JMS
// dispatcher. Provider-specific features stay in the optional capability
// interfaces below so an adapter cannot accidentally claim semantics it does
// not implement.
type brokerAdapter interface {
	Publish(ctx context.Context, destination string, msg message) (message, error)
	Consume(ctx context.Context, destination string, timeout time.Duration) (message, error)
	RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error)
	HealthCheck(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// settlementAdapter is an optional capability for stateful, client-controlled
// settlement and advanced Azure Service Bus consume modes.
type settlementAdapter interface {
	ConsumeAdvanced(ctx context.Context, destination string, opts consumeOptions, timeout time.Duration) (message, error)
	Settle(ctx context.Context, token string, disposition string, opts settleOptions) error
	RenewLock(ctx context.Context, token string) error
	AcceptSession(ctx context.Context, destination, sessionID string) (sessionToken string, resolvedSessionID string, err error)
	SessionState(ctx context.Context, sessionToken string, state []byte, set bool) ([]byte, error)
	CloseSession(ctx context.Context, sessionToken string) error
	ReceiveDeferred(ctx context.Context, destination string, sequenceNumbers []int64, timeout time.Duration) ([]message, error)
	CancelScheduled(ctx context.Context, destination string, sequenceNumbers []int64) error
}

type consumeOptions struct {
	SubQueue     string
	PeekLock     bool
	SessionToken string
}

type settleOptions struct {
	DeadLetterReason      string
	DeadLetterDescription string
}

// selectiveConsumer supports JMS message selectors on receive. Service Bus
// intentionally does not implement this data-plane capability.
type selectiveConsumer interface {
	ConsumeSelective(ctx context.Context, destination, selector string, timeout time.Duration) (message, error)
}

// messageBrowser supports non-destructive queue inspection.
type messageBrowser interface {
	Browse(ctx context.Context, destination, selector string, max int, timeout time.Duration) ([]message, error)
}

// topicAdapter exposes independent subscription cursors for JMS topic fan-out.
type topicAdapter interface {
	PublishTopic(ctx context.Context, topic string, msg message) error
	ConsumeTopic(ctx context.Context, topic, subscription, selector string, durable bool, timeout time.Duration) (message, error)
	BrowseTopic(ctx context.Context, topic, subscription, selector string, max int, timeout time.Duration) ([]message, error)
}

// topicRequestReplyAdapter is the mock-only companion for topic request/reply.
type topicRequestReplyAdapter interface {
	RequestReplyTopic(ctx context.Context, requestTopic, replyTopic, subscription string, request message, timeout time.Duration) (message, message, error)
}

// serviceBusMockTopicAdapter models Service Bus subscriptions as durable broker
// entities without changing ordinary JMS topic semantics.
type serviceBusMockTopicAdapter interface {
	PublishServiceBusTopic(ctx context.Context, topic string, msg message) error
	ConsumeServiceBusTopic(ctx context.Context, topic, subscription, selector string, timeout time.Duration) (message, error)
	BrowseServiceBusTopic(ctx context.Context, topic, subscription, selector string, max int, timeout time.Duration) ([]message, error)
}

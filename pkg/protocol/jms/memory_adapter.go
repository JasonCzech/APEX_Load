// Package jms provides a provider-neutral JMS protocol implementation.
package jms

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type memoryAdapter struct {
	broker    *memoryBroker
	namespace string
}

func (a memoryAdapter) destination(value string) string { return mockJMSKey(a.namespace, value) }

func (a memoryAdapter) Publish(_ context.Context, destination string, msg message) (message, error) {
	if err := a.broker.publish(a.destination(destination), msg); err != nil {
		return message{}, err
	}
	return msg, nil
}

func (a memoryAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for {
		if msg, ok := a.broker.consume(a.destination(destination)); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message received from %q within %s", destination, timeout)
		case <-tick.C:
		}
	}
}

func (a memoryAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	if err := a.broker.publish(a.destination(requestDestination), request); err != nil {
		return message{}, message{}, err
	}

	matchesRequest := func(reply message) bool {
		return reply.ID != request.ID && (request.CorrelationID == "" ||
			reply.CorrelationID == request.CorrelationID ||
			reply.Headers["jms_correlation_id"] == request.CorrelationID ||
			reply.Properties["jms_correlation_id"] == request.CorrelationID ||
			reply.Properties["JMSCorrelationID"] == request.CorrelationID)
	}
	if (request.CorrelationID != "" && !a.broker.hasMatching(a.destination(replyDestination), matchesRequest)) ||
		(request.CorrelationID == "" && !a.broker.hasMessages(a.destination(replyDestination))) {
		if err := a.broker.publish(a.destination(replyDestination), message{
			ID:            uuid.NewString(),
			CorrelationID: request.CorrelationID,
			Destination:   replyDestination,
			Headers:       cloneStringMap(request.Headers),
			Properties:    cloneStringMap(request.Properties),
			Body:          request.Body,
			CreatedAt:     time.Now(),
		}); err != nil {
			return request, message{}, err
		}
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for {
		if response, ok := a.broker.consumeWhere(a.destination(replyDestination), matchesRequest); ok {
			return request, response, nil
		}
		select {
		case <-ctx.Done():
			return request, message{}, ctx.Err()
		case <-deadline.C:
			return request, message{}, fmt.Errorf("no correlated JMS reply received from %q within %s", replyDestination, timeout)
		case <-tick.C:
		}
	}
}

func (a memoryAdapter) RequestReplyTopic(ctx context.Context, requestTopic, replyTopic, subscription string, request message, timeout time.Duration) (message, message, error) {
	requestKey := a.destination(requestTopic)
	replyKey := a.destination(replyTopic)
	if err := a.broker.subscribe(requestKey, subscription); err != nil {
		return message{}, message{}, err
	}
	if err := a.broker.subscribe(replyKey, subscription); err != nil {
		return message{}, message{}, err
	}
	if err := a.broker.publishTopic(requestKey, request); err != nil {
		return message{}, message{}, err
	}

	matchesRequest := func(reply message) bool {
		return reply.ID != request.ID && (request.CorrelationID == "" ||
			reply.CorrelationID == request.CorrelationID ||
			reply.Headers["jms_correlation_id"] == request.CorrelationID ||
			reply.Properties["jms_correlation_id"] == request.CorrelationID ||
			reply.Properties["JMSCorrelationID"] == request.CorrelationID)
	}
	if !a.broker.hasTopicMatching(replyKey, subscription, matchesRequest) {
		logicalReplyDestination := serviceBusConsumeDestination(replyTopic, "topic", subscription)
		if err := a.broker.publishTopic(replyKey, message{
			ID:            uuid.NewString(),
			CorrelationID: request.CorrelationID,
			Destination:   logicalReplyDestination,
			Headers:       cloneStringMap(request.Headers),
			Properties:    cloneStringMap(request.Properties),
			Body:          request.Body,
			CreatedAt:     time.Now(),
		}); err != nil {
			return request, message{}, err
		}
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if response, ok := a.broker.consumeTopicWhere(replyKey, subscription, matchesRequest); ok {
			return request, response, nil
		}
		select {
		case <-ctx.Done():
			return request, message{}, ctx.Err()
		case <-deadline.C:
			return request, message{}, fmt.Errorf("no correlated JMS reply received on topic %q subscription %q within %s", replyTopic, subscription, timeout)
		case <-tick.C:
		}
	}
}

func (a memoryAdapter) HealthCheck(context.Context) error {
	if a.broker == nil {
		return fmt.Errorf("memory JMS broker is not initialized")
	}
	return nil
}

func (a memoryAdapter) Teardown(context.Context) error {
	return nil
}

// ConsumeSelective polls for the first message matching the selector (P2.3).
func (a memoryAdapter) ConsumeSelective(ctx context.Context, destination, selectorExpr string, timeout time.Duration) (message, error) {
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return message{}, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if msg, ok := a.broker.consumeMatching(a.destination(destination), sel); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message matching the selector received from %q within %s", destination, timeout)
		case <-tick.C:
		}
	}
}

// Browse returns matching messages without consuming them (P2.3).
func (a memoryAdapter) Browse(_ context.Context, destination, selectorExpr string, max int, _ time.Duration) ([]message, error) {
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return nil, err
	}
	return a.broker.browse(a.destination(destination), sel, max), nil
}

// PublishTopic fans a message out to every registered subscription (P2.3).
func (a memoryAdapter) PublishTopic(_ context.Context, topic string, msg message) error {
	return a.broker.publishTopic(a.destination(topic), msg)
}

func (a memoryAdapter) PublishServiceBusTopic(_ context.Context, topic string, msg message) error {
	return a.broker.publishServiceBusTopic(a.destination(topic), msg)
}

// ConsumeTopic registers the subscription (so future publishes fan out to it,
// even across timed-out polls) then consumes its next matching message (P2.3).
// The mock retains messages for durable and non-durable subscriptions alike (it
// has no long-lived consumer connection to model a non-durable drop).
func (a memoryAdapter) ConsumeTopic(ctx context.Context, topic, subscription, selectorExpr string, _ bool, timeout time.Duration) (message, error) {
	if err := a.broker.subscribe(a.destination(topic), subscription); err != nil {
		return message{}, err
	}
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return message{}, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if msg, ok := a.broker.consumeTopic(a.destination(topic), subscription, sel); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message received on topic %q subscription %q within %s", topic, subscription, timeout)
		case <-tick.C:
		}
	}
}

func (a memoryAdapter) ConsumeServiceBusTopic(ctx context.Context, topic, subscription, selectorExpr string, timeout time.Duration) (message, error) {
	if err := a.broker.subscribeServiceBus(a.destination(topic), subscription); err != nil {
		return message{}, err
	}
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return message{}, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if msg, ok := a.broker.consumeTopic(a.destination(topic), subscription, sel); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message received on Service Bus topic %q subscription %q within %s", topic, subscription, timeout)
		case <-tick.C:
		}
	}
}

// BrowseTopic registers the subscription then non-destructively reads its
// matching messages (P2.3).
func (a memoryAdapter) BrowseTopic(_ context.Context, topic, subscription, selectorExpr string, max int, _ time.Duration) ([]message, error) {
	if err := a.broker.subscribe(a.destination(topic), subscription); err != nil {
		return nil, err
	}
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return nil, err
	}
	return a.broker.browseTopic(a.destination(topic), subscription, sel, max), nil
}

func (a memoryAdapter) BrowseServiceBusTopic(_ context.Context, topic, subscription, selectorExpr string, max int, _ time.Duration) ([]message, error) {
	if err := a.broker.subscribeServiceBus(a.destination(topic), subscription); err != nil {
		return nil, err
	}
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return nil, err
	}
	return a.broker.browseTopic(a.destination(topic), subscription, sel, max), nil
}

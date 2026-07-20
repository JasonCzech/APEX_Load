package dsl

// jmsActionSpecs declares the JMS action types shared by the jms-* protocols
// (Solace, IBM MQ, Azure Service Bus). Destination/reply-to fallbacks depend
// on config.jms, so each spec carries the shared configuration-aware residual
// callback in addition to membership and field metadata.
func jmsActionSpecs() []ActionSpec {
	specs := []ActionSpec{
		{
			Type:      "jms_publish",
			Protocols: []string{"jms"},
			Summary:   "Publishes a message to a queue or topic.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Destination queue/topic; falls back to config.jms.destination."},
				{Name: "body", Kind: FieldKindString, Doc: "Message payload; template variables are expanded."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the message payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "headers", Kind: FieldKindObject, Doc: "Message properties/headers."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Destination alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Destination kind; falls back to config.jms.destination_type."},
				{Name: "params.correlation_id", Kind: FieldKindString, Doc: "Correlation ID set on the outgoing message."},
				{Name: "params.dead_letter", Kind: FieldKindBool, Doc: "Marks this action as targeting a dead-letter destination (drives dead-letter classification)."},
				{Name: "params.message_id", Kind: FieldKindString, Doc: "Explicit message ID (Service Bus duplicate detection uses it); defaults to a generated UUID."},
				{Name: "params.session_id", Kind: FieldKindString, Doc: "Azure Service Bus session ID for session-enabled entities; requires the real Service Bus adapter."},
				{Name: "params.partition_key", Kind: FieldKindString, Doc: "Azure Service Bus partition key (co-locates messages on one partition); requires the real Service Bus adapter."},
				{Name: "params.scheduled_enqueue_ms", Kind: FieldKindInt, Doc: "Azure Service Bus scheduled enqueue delay in milliseconds; requires the real Service Bus adapter, which holds the message until then."},
				{Name: "params.time_to_live_ms", Kind: FieldKindInt, Doc: "Message time-to-live in milliseconds before the broker expires it."},
				{Name: "params.message_type", Kind: FieldKindEnum, Enum: []string{"text", "bytes", "map", "stream", "object"}, Doc: "JMS message type. map/stream read params.map_body/params.stream_body; text/bytes/object use body."},
				{Name: "params.map_body", Kind: FieldKindObject, Doc: "Named values for a MapMessage (message_type=map); JSON-encoded into the body for replay."},
				{Name: "params.stream_body", Kind: FieldKindArray, Doc: "Ordered values for a StreamMessage (message_type=stream); JSON-encoded into the body for replay."},
				{Name: "params.delivery_mode", Kind: FieldKindEnum, Enum: []string{"persistent", "non_persistent"}, Doc: "Per-message delivery mode; falls back to config.jms.delivery_mode. Applied by the AMQP/Solace adapter."},
				{Name: "params.priority", Kind: FieldKindInt, Doc: "JMS message priority 0-9; applied by the AMQP/Solace adapter (Azure Service Bus carries it as a JMSPriority property but does not honor priority ordering)."},
			},
		},
		{
			Type:      "jms_consume",
			Protocols: []string{"jms"},
			Summary:   "Consumes the next message from a queue or topic.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Source queue/topic; falls back to config.jms.destination."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Source alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Destination kind; falls back to config.jms.destination_type."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Topic subscription name (also the Azure Service Bus topic subscription); falls back to config.jms.subscription. Required to consume from a topic."},
				{Name: "params.subscription_mode", Kind: FieldKindEnum, Enum: []string{"non_durable", "durable", "shared"}, Doc: "Topic subscription mode. A subscription is identified by name: multiple consumers using the same subscription name compete for its messages (shared), while distinct names each get their own copy (fan-out). durable persists the named subscription on Solace/AMQP; non_durable/shared do not. Service Bus subscriptions are natively durable."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Consume timeout in milliseconds; falls back to config.jms.timeout_ms."},
				{Name: "params.dead_letter", Kind: FieldKindBool, Doc: "Consume from / classify the dead-letter destination."},
				{Name: "params.sub_queue", Kind: FieldKindEnum, Enum: []string{"none", "dead_letter", "transfer_dead_letter"}, Doc: "Azure Service Bus sub-queue to receive from (requires jms_servicebus_real)."},
				{Name: "params.peek_lock", Kind: FieldKindBool, Doc: "Azure Service Bus: defer settlement instead of auto-completing, returning a settlement_token for a later jms_settle (requires jms_servicebus_real)."},
				{Name: "params.session_token", Kind: FieldKindString, Doc: "Token from jms_session_accept used to consume through an accepted Azure Service Bus session (requires jms_servicebus_real)."},
				{Name: "params.selector", Kind: FieldKindString, Doc: "JMS message selector (SQL-92 subset over headers/properties) — only matching messages are consumed. Supported by Solace/AMQP and the mock; Azure Service Bus uses subscription rules instead."},
			},
		},
		{
			Type:      "jms_browse",
			Protocols: []string{"jms"},
			Summary:   "Browses a queue non-destructively (JMS QueueBrowser); messages are not consumed.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Queue to browse; falls back to config.jms.destination."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Queue alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Destination kind; falls back to config.jms.destination_type."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Topic subscription name to browse (required when browsing a topic)."},
				{Name: "params.selector", Kind: FieldKindString, Doc: "JMS message selector (SQL-92 subset) restricting which messages are browsed."},
				{Name: "params.max", Kind: FieldKindInt, Doc: "Maximum messages to browse (default 10)."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Browse timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
		{
			Type:      "jms_request_reply",
			Protocols: []string{"jms"},
			Summary:   "Sends a request message and waits for the correlated reply.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Request destination; falls back to config.jms.destination."},
				{Name: "body", Kind: FieldKindString, Doc: "Request payload; template variables are expanded."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the request payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "headers", Kind: FieldKindObject, Doc: "Request message properties/headers."},
				{Name: "params.reply_to", Kind: FieldKindString, Doc: "Reply destination; required unless config.jms.reply_to is set."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Azure Service Bus topic subscription for consuming replies from a topic; falls back to config.jms.subscription."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Reply timeout in milliseconds."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Request destination alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Destination kind; falls back to config.jms.destination_type."},
				{Name: "params.correlation_id", Kind: FieldKindString, Doc: "Correlation ID for matching the reply; defaults to the request message ID."},
				{Name: "params.dead_letter", Kind: FieldKindBool, Doc: "Marks the request destination as a dead-letter destination for dependency classification."},
				{Name: "params.message_id", Kind: FieldKindString, Doc: "Explicit request message ID; defaults to a generated UUID."},
				{Name: "params.session_id", Kind: FieldKindString, Doc: "Azure Service Bus session ID for a session-enabled request entity; requires the real Service Bus adapter."},
				{Name: "params.partition_key", Kind: FieldKindString, Doc: "Azure Service Bus partition key for the request message; requires the real Service Bus adapter."},
				{Name: "params.scheduled_enqueue_ms", Kind: FieldKindInt, Doc: "Azure Service Bus scheduled enqueue delay for the request message; requires the real Service Bus adapter."},
				{Name: "params.time_to_live_ms", Kind: FieldKindInt, Doc: "Request message time-to-live in milliseconds."},
				{Name: "params.message_type", Kind: FieldKindEnum, Enum: []string{"text", "bytes", "map", "stream", "object"}, Doc: "JMS message type of the request. map/stream read params.map_body/params.stream_body; text/bytes/object use body."},
				{Name: "params.map_body", Kind: FieldKindObject, Doc: "Named values for a MapMessage request (message_type=map); JSON-encoded into the body for replay."},
				{Name: "params.stream_body", Kind: FieldKindArray, Doc: "Ordered values for a StreamMessage request (message_type=stream); JSON-encoded into the body for replay."},
				{Name: "params.delivery_mode", Kind: FieldKindEnum, Enum: []string{"persistent", "non_persistent"}, Doc: "Per-message delivery mode of the request; falls back to config.jms.delivery_mode."},
				{Name: "params.priority", Kind: FieldKindInt, Doc: "JMS message priority 0-9 of the request; applied by the AMQP/Solace adapter."},
			},
		},
		{
			Type:      "jms_ack",
			Protocols: []string{"jms"},
			Summary:   "Acknowledges consumed messages in mock mode. Real adapters reject generic JMS settlement; use provider-specific settlement where available.",
		},
		{
			Type:      "jms_commit",
			Protocols: []string{"jms"},
			Summary:   "Commits the transacted mock session. Real adapters reject generic JMS transaction actions.",
		},
		{
			Type:      "jms_rollback",
			Protocols: []string{"jms"},
			Summary:   "Rolls back and requeues the transacted mock session. Real adapters reject generic JMS transaction actions.",
		},
		{
			Type:      "jms_settle",
			Protocols: []string{"jms"},
			Summary:   "Settles a peek-locked Azure Service Bus message (complete/abandon/dead-letter/defer). Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.settlement_token", Required: true, Kind: FieldKindString, Doc: "Token from a peek_lock jms_consume identifying the in-flight message."},
				{Name: "params.disposition", Kind: FieldKindEnum, Enum: []string{"complete", "abandon", "dead_letter", "defer"}, Doc: "How to settle the message (default complete)."},
				{Name: "params.dead_letter_reason", Kind: FieldKindString, Doc: "Reason recorded when disposition=dead_letter."},
				{Name: "params.dead_letter_description", Kind: FieldKindString, Doc: "Description recorded when disposition=dead_letter."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Settlement operation timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
		{
			Type:      "jms_renew_lock",
			Protocols: []string{"jms"},
			Summary:   "Renews the broker lock on a peek-locked Azure Service Bus message. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.settlement_token", Required: true, Kind: FieldKindString, Doc: "Token from a peek_lock jms_consume identifying the in-flight message."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Lock-renewal timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
		{
			Type:      "jms_session_accept",
			Protocols: []string{"jms"},
			Summary:   "Accepts an Azure Service Bus session (specific or next available), returning a session token. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Session-enabled queue/topic; falls back to config.jms.destination."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Session-enabled queue/topic alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Destination kind; falls back to config.jms.destination_type."},
				{Name: "params.session_id", Kind: FieldKindString, Doc: "Specific session id to accept; empty accepts the next available session."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Topic subscription when the destination is a topic."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Session-accept timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
		{
			Type:      "jms_session_state",
			Protocols: []string{"jms"},
			Summary:   "Gets or sets the session state for an accepted Azure Service Bus session. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.session_token", Required: true, Kind: FieldKindString, Doc: "Token from jms_session_accept."},
				{Name: "params.session_state", Kind: FieldKindString, Doc: "When set, stores this as the session state; when absent, the current state is read into the jms_session_state extraction."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Session-state operation timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
		{
			Type:      "jms_session_close",
			Protocols: []string{"jms"},
			Summary:   "Closes an accepted Azure Service Bus session and releases its receiver. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.session_token", Required: true, Kind: FieldKindString, Doc: "Token from jms_session_accept."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Session-close timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
		{
			Type:      "jms_receive_deferred",
			Protocols: []string{"jms"},
			Summary:   "Receives previously deferred Azure Service Bus messages by sequence number (peek-locked). Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Source queue/topic; falls back to config.jms.destination."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Source queue/topic alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Source destination kind; falls back to config.jms.destination_type."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Topic subscription when receiving deferred messages from a topic."},
				{Name: "params.sequence_numbers", Required: true, Kind: FieldKindAny, Doc: "Positive sequence numbers to fetch (array of numbers, one number, or a comma-separated string)."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Receive timeout in milliseconds."},
			},
		},
		{
			Type:      "jms_cancel_scheduled",
			Protocols: []string{"jms"},
			Summary:   "Cancels scheduled (future-enqueue) Azure Service Bus messages by sequence number. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Destination the messages were scheduled to; falls back to config.jms.destination."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Scheduled-message destination alternative to target; falls back to config.jms.destination."},
				{Name: "params.sequence_numbers", Required: true, Kind: FieldKindAny, Doc: "Positive scheduled sequence numbers to cancel (array, one number, or comma-separated string; values come from jms_scheduled_sequence)."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Cancellation timeout in milliseconds; falls back to config.jms.timeout_ms."},
			},
		},
	}
	for i := range specs {
		specs[i].validateInScript = validateJMSCatalogActionIssues
	}
	return specs
}

package dsl

// jmsActionSpecs declares the JMS action types shared by the jms-* protocols
// (Solace, IBM MQ, Azure Service Bus). Destination/reply-to fallbacks depend
// on config.jms, so those rules stay in validateJMSScriptIssues; specs carry
// membership and field metadata.
func jmsActionSpecs() []ActionSpec {
	return []ActionSpec{
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
				{Name: "params.session_id", Kind: FieldKindString, Doc: "Azure Service Bus session ID for session-enabled entities (ignored by other providers)."},
				{Name: "params.partition_key", Kind: FieldKindString, Doc: "Azure Service Bus partition key (co-locates messages on one partition)."},
				{Name: "params.scheduled_enqueue_ms", Kind: FieldKindInt, Doc: "Azure Service Bus scheduled enqueue delay in milliseconds (the broker holds the message until then)."},
				{Name: "params.time_to_live_ms", Kind: FieldKindInt, Doc: "Message time-to-live in milliseconds before the broker expires it."},
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
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Azure Service Bus topic subscription; falls back to config.jms.subscription."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Consume timeout in milliseconds; falls back to config.jms.timeout_ms."},
				{Name: "params.dead_letter", Kind: FieldKindBool, Doc: "Consume from / classify the dead-letter destination."},
				{Name: "params.sub_queue", Kind: FieldKindEnum, Enum: []string{"none", "dead_letter", "transfer_dead_letter"}, Doc: "Azure Service Bus sub-queue to receive from (requires jms_servicebus_real)."},
				{Name: "params.peek_lock", Kind: FieldKindBool, Doc: "Azure Service Bus: defer settlement instead of auto-completing, returning a settlement_token for a later jms_settle (requires jms_servicebus_real)."},
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
				{Name: "params.reply_to", Kind: FieldKindString, Doc: "Reply destination; required unless config.jms.reply_to is set."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Azure Service Bus topic subscription for consuming replies from a topic; falls back to config.jms.subscription."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Reply timeout in milliseconds."},
				{Name: "params.destination", Kind: FieldKindString, Doc: "Request destination alternative to target; falls back to config.jms.destination."},
				{Name: "params.destination_type", Kind: FieldKindEnum, Enum: []string{"queue", "topic"}, Doc: "Destination kind; falls back to config.jms.destination_type."},
				{Name: "params.correlation_id", Kind: FieldKindString, Doc: "Correlation ID for matching the reply; defaults to the request message ID."},
			},
		},
		{
			Type:      "jms_ack",
			Protocols: []string{"jms"},
			Summary:   "Acknowledges the last consumed message (client acknowledgement mode).",
		},
		{
			Type:      "jms_commit",
			Protocols: []string{"jms"},
			Summary:   "Commits the transacted session.",
		},
		{
			Type:      "jms_rollback",
			Protocols: []string{"jms"},
			Summary:   "Rolls back the transacted session.",
		},
		{
			Type:      "jms_settle",
			Protocols: []string{"jms"},
			Summary:   "Settles a peek-locked Azure Service Bus message (complete/abandon/dead-letter/defer). Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.settlement_token", Kind: FieldKindString, Doc: "Token from a peek_lock jms_consume identifying the in-flight message."},
				{Name: "params.disposition", Kind: FieldKindEnum, Enum: []string{"complete", "abandon", "dead_letter", "defer"}, Doc: "How to settle the message (default complete)."},
				{Name: "params.dead_letter_reason", Kind: FieldKindString, Doc: "Reason recorded when disposition=dead_letter."},
				{Name: "params.dead_letter_description", Kind: FieldKindString, Doc: "Description recorded when disposition=dead_letter."},
			},
		},
		{
			Type:      "jms_renew_lock",
			Protocols: []string{"jms"},
			Summary:   "Renews the broker lock on a peek-locked Azure Service Bus message. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.settlement_token", Kind: FieldKindString, Doc: "Token from a peek_lock jms_consume identifying the in-flight message."},
			},
		},
		{
			Type:      "jms_session_accept",
			Protocols: []string{"jms"},
			Summary:   "Accepts an Azure Service Bus session (specific or next available), returning a session token. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Session-enabled queue/topic; falls back to config.jms.destination."},
				{Name: "params.session_id", Kind: FieldKindString, Doc: "Specific session id to accept; empty accepts the next available session."},
				{Name: "params.subscription", Kind: FieldKindString, Doc: "Topic subscription when the destination is a topic."},
			},
		},
		{
			Type:      "jms_session_state",
			Protocols: []string{"jms"},
			Summary:   "Gets or sets the session state for an accepted Azure Service Bus session. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "params.session_token", Kind: FieldKindString, Doc: "Token from jms_session_accept."},
				{Name: "params.session_state", Kind: FieldKindString, Doc: "When set, stores this as the session state; when absent, the current state is read into the jms_session_state extraction."},
			},
		},
		{
			Type:      "jms_receive_deferred",
			Protocols: []string{"jms"},
			Summary:   "Receives previously deferred Azure Service Bus messages by sequence number (peek-locked). Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Source queue/topic; falls back to config.jms.destination."},
				{Name: "params.sequence_numbers", Kind: FieldKindArray, Doc: "Sequence numbers (array of numbers, or comma-separated string) to fetch."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Receive timeout in milliseconds."},
			},
		},
		{
			Type:      "jms_cancel_scheduled",
			Protocols: []string{"jms"},
			Summary:   "Cancels scheduled (future-enqueue) Azure Service Bus messages by sequence number. Requires jms_servicebus_real.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Destination the messages were scheduled to; falls back to config.jms.destination."},
				{Name: "params.sequence_numbers", Kind: FieldKindArray, Doc: "Scheduled sequence numbers (from jms_scheduled_sequence) to cancel."},
			},
		},
	}
}

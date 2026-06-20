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
	}
}

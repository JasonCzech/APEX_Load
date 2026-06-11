package dsl

// kafkaActionSpecs declares the kafka protocol action types. Topic/reply-to
// fallbacks depend on config.kafka, so those rules stay in
// validateKafkaScriptIssues; specs carry membership and field metadata.
func kafkaActionSpecs() []ActionSpec {
	return []ActionSpec{
		{
			Type:      "kafka_produce",
			Protocols: []string{"kafka"},
			Summary:   "Produces a record to a topic.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Topic; falls back to config.kafka.topic or params.topic."},
				{Name: "body", Kind: FieldKindString, Doc: "Record value; template variables are expanded."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the record value. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "headers", Kind: FieldKindObject, Doc: "Record headers."},
				{Name: "params.topic", Kind: FieldKindString, Doc: "Topic alternative to target."},
				{Name: "params.key", Kind: FieldKindString, Doc: "Record key (drives key-hash partitioning)."},
				{Name: "params.partition", Kind: FieldKindInt, Doc: "Explicit partition (with partition_strategy: explicit)."},
			},
		},
		{
			Type:      "kafka_consume",
			Protocols: []string{"kafka"},
			Summary:   "Consumes the next record from a topic.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Topic; falls back to config.kafka.topic or params.topic."},
				{Name: "params.topic", Kind: FieldKindString, Doc: "Topic alternative to target."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Consume timeout in milliseconds; falls back to config.kafka.timeout_ms."},
			},
		},
		{
			Type:      "kafka_request_reply",
			Protocols: []string{"kafka"},
			Summary:   "Produces a request record and waits for the correlated reply record.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Request topic; falls back to config.kafka.topic or params.topic."},
				{Name: "body", Kind: FieldKindString, Doc: "Request record value."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the request record value. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "params.reply_to", Required: true, Kind: FieldKindString, Doc: "Reply topic."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Reply timeout in milliseconds."},
			},
		},
		{
			Type:      "kafka_commit",
			Protocols: []string{"kafka"},
			Summary:   "Commits consumer group offsets.",
		},
		{
			Type:      "kafka_seek",
			Protocols: []string{"kafka"},
			Summary:   "Seeks the consumer to an offset.",
			Fields: []FieldSpec{
				{Name: "params.offset", Kind: FieldKindAny, Doc: "Target offset: a number, \"earliest\", or \"latest\"."},
				{Name: "params.partition", Kind: FieldKindInt, Doc: "Partition to seek on."},
			},
		},
	}
}

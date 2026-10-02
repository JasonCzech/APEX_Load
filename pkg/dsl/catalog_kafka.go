package dsl

// kafkaActionSpecs declares the kafka protocol action types. Topic/reply-to
// fallbacks depend on config.kafka, so each spec carries the shared
// configuration-aware residual callback with its membership and field metadata.
func kafkaActionSpecs() []ActionSpec {
	specs := []ActionSpec{
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
				{Name: "params.correlation_id", Kind: FieldKindString, Doc: "Explicit record correlation ID; when omitted kafka_request_reply generates one."},
			},
		},
		{
			Type:      "kafka_consume",
			Protocols: []string{"kafka"},
			Summary:   "Consumes the next record from a topic.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Topic; falls back to config.kafka.topic or params.topic."},
				{Name: "params.topic", Kind: FieldKindString, Doc: "Topic alternative to target."},
				{Name: "params.consumer_group", Kind: FieldKindString, Doc: "Consumer group override; falls back to config.kafka.consumer_group."},
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
				{Name: "params.correlation_id", Kind: FieldKindString, Doc: "Explicit request correlation ID; generated automatically when omitted."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Reply timeout in milliseconds."},
			},
		},
		{
			Type:      "kafka_commit",
			Protocols: []string{"kafka"},
			Summary:   "Commits consumer group offsets.",
			Fields: []FieldSpec{
				{Name: "params.consumer_group", Kind: FieldKindString, Doc: "Consumer group identity; falls back to config.kafka.consumer_group."},
			},
		},
		{
			Type:      "kafka_seek",
			Protocols: []string{"kafka"},
			Summary:   "Repositions the consumer cursor. The real adapter requires a configured group and previously polled owned partitions; earliest/latest respect isolation.",
			Fields: []FieldSpec{
				{Name: "params.consumer_group", Kind: FieldKindString, Doc: "Consumer group override; falls back to config.kafka.consumer_group."},
				{Name: "params.offset", Kind: FieldKindAny, Doc: "Target offset: a number, \"earliest\", or \"latest\"."},
				{Name: "params.partition", Kind: FieldKindInt, Doc: "Reserved: not yet honored at runtime — seek repositions the whole (topic, group) cursor, not a single partition."},
			},
		},
		{
			Type:      "kafka_begin_transaction",
			Protocols: []string{"kafka"},
			Summary:   "Begins a transaction (requires config.kafka.transactional_id). consumer_transactions also commits consumed offsets atomically and aborts on ownership loss.",
		},
		{
			Type:      "kafka_commit_transaction",
			Protocols: []string{"kafka"},
			Summary:   "Commits the open producer transaction, making buffered records atomically visible (EOS).",
		},
		{
			Type:      "kafka_abort_transaction",
			Protocols: []string{"kafka"},
			Summary:   "Aborts the open producer transaction, discarding buffered records.",
		},
	}
	for i := range specs {
		specs[i].validateInScript = validateKafkaCatalogActionIssues
	}
	return specs
}

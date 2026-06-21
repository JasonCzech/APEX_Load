package dsl

// eventHubsActionSpecs declares the native Azure Event Hubs action types. The
// protocol is producer-first: eventhubs_produce is the primary action and
// eventhubs_consume is a lightweight read-only observer probe. Event hub /
// partition fallbacks depend on config.eventhubs, so those rules stay in
// validateEventHubsScriptIssues.
func eventHubsActionSpecs() []ActionSpec {
	return []ActionSpec{
		{
			Type:      "eventhubs_produce",
			Protocols: []string{"eventhubs"},
			Summary:   "Publishes an event to an event hub (the primary ingestion action).",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Event hub; falls back to config.eventhubs.event_hub or params.event_hub."},
				{Name: "body", Kind: FieldKindString, Doc: "Event body; template variables are expanded."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the event body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "headers", Kind: FieldKindObject, Doc: "Application properties set on the event."},
				{Name: "params.event_hub", Kind: FieldKindString, Doc: "Event hub alternative to target."},
				{Name: "params.partition_key", Kind: FieldKindString, Doc: "Partition key (hash-routes co-located events); falls back to config.eventhubs.partition_key."},
				{Name: "params.partition_id", Kind: FieldKindString, Doc: "Explicit partition id (mutually exclusive with partition_key); falls back to config.eventhubs.partition_id."},
			},
		},
		{
			Type:      "eventhubs_consume",
			Protocols: []string{"eventhubs"},
			Summary:   "Reads the next event (read-only observer probe for end-to-end latency / backlog).",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Event hub; falls back to config.eventhubs.event_hub or params.event_hub."},
				{Name: "params.event_hub", Kind: FieldKindString, Doc: "Event hub alternative to target."},
				{Name: "params.consumer_group", Kind: FieldKindString, Doc: "Consumer group; falls back to config.eventhubs.consumer_group ($Default)."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Receive timeout in milliseconds; falls back to config.eventhubs.timeout_ms."},
			},
		},
	}
}

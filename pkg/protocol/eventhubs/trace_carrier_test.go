package eventhubs

import (
	"context"
	"fmt"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestEventHubsTraceCarrierSingleAndBatch(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "telemetry", "eventhubs_timeout_ms": float64(250), protocol.ConfigMessagingTraceCarrier: "w3c"})
	produced, err := e.Execute(ctx, &protocol.Action{Name: "produce", Type: "eventhubs_produce", Body: []byte("one")})
	if err != nil || !produced.Success || produced.TraceContext.LookupTraceID() == "" {
		t.Fatalf("produce = %+v %v", produced, err)
	}
	consumed, err := e.Execute(ctx, &protocol.Action{Name: "consume", Type: "eventhubs_consume", Extractors: []protocol.Extractor{{Name: "diag", Source: "property", Expression: protocol.DiagnosticIDKey}}})
	if err != nil || !consumed.Success || len(consumed.TraceLinks) != 1 || consumed.TraceLinks[0].SpanID != produced.TraceContext.SpanID {
		t.Fatalf("consume = %+v %v", consumed, err)
	}
	if consumed.Extractions["diag"] == "" {
		t.Fatal("Azure SDK consumers read Diagnostic-Id, which was not sent")
	}

	// A batch links every member, each with its own span, bounded at MaxTraceLinks.
	bodies := make([]any, protocol.MaxTraceLinks+2)
	for i := range bodies {
		bodies[i] = fmt.Sprintf("event-%d", i)
	}
	batch, err := e.Execute(ctx, &protocol.Action{Name: "batch", Type: "eventhubs_produce", Params: map[string]any{"messages": bodies}})
	if err != nil || !batch.Success || len(batch.TraceLinks) != protocol.MaxTraceLinks || !batch.TraceLinksTruncated {
		t.Fatalf("batch = links %d truncated %v err %v", len(batch.TraceLinks), batch.TraceLinksTruncated, err)
	}
	spans := map[string]bool{}
	for _, link := range batch.TraceLinks {
		if link.Kind != protocol.TraceLinkBatchMember || spans[link.SpanID] {
			t.Fatalf("batch member link %+v repeated or mislabeled", link)
		}
		spans[link.SpanID] = true
	}
	if batch.TraceContext == nil || batch.TraceContext.SpanID != batch.TraceLinks[0].SpanID {
		t.Fatalf("batch row trace context %+v", batch.TraceContext)
	}
}

package jms

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func newCarrierJMS(t *testing.T, name, provider, destination, ack string) *JMS {
	t.Helper()
	p := &JMS{name: name, provider: provider}
	cfg := map[string]any{"jms_destination": destination, "jms_mock": true, "jms_timeout_ms": float64(250), protocol.ConfigMessagingTraceCarrier: "w3c"}
	if ack != "" {
		cfg["jms_acknowledgement"] = ack
	}
	if err := p.Init(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Teardown(context.Background()) })
	return p
}

func TestJMSTraceCarrierAcrossProviders(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ name, provider string }{{"jms-solace", "solace"}, {"jms-ibmmq", "ibmmq"}, {"jms-servicebus", "servicebus"}} {
		t.Run(tc.provider, func(t *testing.T) {
			p := newCarrierJMS(t, tc.name, tc.provider, "orders.carrier."+tc.provider, "")
			published, err := p.Execute(ctx, &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte(`{"id":1}`)})
			if err != nil || !published.Success || published.TraceContext.LookupTraceID() == "" {
				t.Fatalf("publish = %+v %v", published, err)
			}
			consumed, err := p.Execute(ctx, &protocol.Action{Name: "consume", Type: "jms_consume", Extractors: []protocol.Extractor{{Name: "diag", Source: "property", Expression: protocol.DiagnosticIDKey}}})
			if err != nil || !consumed.Success || len(consumed.TraceLinks) != 1 || consumed.TraceLinks[0].SpanID != published.TraceContext.SpanID || consumed.TraceLinks[0].Kind != protocol.TraceLinkConsumed {
				t.Fatalf("consume = %+v %v", consumed, err)
			}
			// Only the Azure SDK profile also writes Diagnostic-Id.
			if hasDiagnosticID := consumed.Extractions["diag"] != ""; hasDiagnosticID != (tc.provider == "servicebus") {
				t.Fatalf("%s Diagnostic-Id present = %v", tc.provider, hasDiagnosticID)
			}
		})
	}
}

func TestJMSTraceCarrierAuthoredPropertyWins(t *testing.T) {
	p := newCarrierJMS(t, "jms-solace", "solace", "orders.carrier.authored", "")
	authored := "00-11111111111111111111111111111111-2222222222222222-01"
	published, err := p.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("x"), Metadata: map[string]any{"traceparent": authored}})
	if err != nil || published.TraceContext == nil || published.TraceContext.Source != protocol.TraceSourceAuthored || published.TraceContext.SpanID != "2222222222222222" {
		t.Fatalf("authored publish = %+v %v", published.TraceContext, err)
	}
}

func TestJMSTraceCarrierLinksRedelivery(t *testing.T) {
	ctx := context.Background()
	p := newCarrierJMS(t, "jms-solace", "solace", "orders.carrier.redelivery", "transacted")
	published, _ := p.Execute(ctx, &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("x")})
	first, _ := p.Execute(ctx, &protocol.Action{Name: "c1", Type: "jms_consume"})
	if len(first.TraceLinks) != 1 || first.TraceLinks[0].Kind != protocol.TraceLinkConsumed {
		t.Fatalf("first delivery links = %+v", first.TraceLinks)
	}
	if _, err := p.Execute(ctx, &protocol.Action{Name: "rollback", Type: "jms_rollback"}); err != nil {
		t.Fatal(err)
	}
	second, _ := p.Execute(ctx, &protocol.Action{Name: "c2", Type: "jms_consume"})
	if len(second.TraceLinks) != 1 || second.TraceLinks[0].Kind != protocol.TraceLinkRedelivery || second.TraceLinks[0].SpanID != published.TraceContext.SpanID {
		t.Fatalf("redelivery links = %+v", second.TraceLinks)
	}
}

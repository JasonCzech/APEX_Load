package brokerconfig

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/jms"
)

// Topology exports generate mock drafts, not proof of a live IBM MQ/Solace
// connection. Verify every destination round-trips its own message exactly.
func TestFixtureBrokerTopologyReplayExactPayload(t *testing.T) {
	for _, fixture := range []struct {
		name, source, protocol string
		destinations           int
	}{
		{"mqsc", "DEFINE QLOCAL('ORDER.REQUEST')\nDEFINE QLOCAL('ORDER.REPLY')", "jms-ibmmq", 2},
		{"jndi", "queue.OrderQueue=ORDER.QUEUE\nqueue.ReplyQueue=REPLY.QUEUE", "jms-solace", 2},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result, err := Convert(ConvertRequest{Name: fixture.name, Data: []byte(fixture.source)})
			if err != nil {
				t.Fatal(err)
			}
			if result.Script.Protocol != fixture.protocol || !result.Script.Config.JMS.Mock {
				t.Fatal("incorrect provider or draft lost mock mode")
			}
			if len(result.Script.Actions) != fixture.destinations*2 {
				t.Fatal("topology destinations dropped")
			}
			for i := 0; i < len(result.Script.Actions); i += 2 {
				result.Script.Actions[i].Body = fmt.Sprintf(`{"destination":%q,"row":%d}`, result.Script.Actions[i].Target, i/2)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			report, err := engine.ReplayScript(ctx, result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
			if err != nil {
				t.Fatal(err)
			}
			if !report.Success || len(report.Events) != len(result.Script.Actions) {
				t.Fatalf("mock replay failed: %s", report.Error)
			}
			for i := 0; i < len(report.Events); i += 2 {
				if report.Events[i+1].Extractions["jms_body"] != result.Script.Actions[i].Body {
					t.Fatalf("destination %s did not round-trip its own message", result.Script.Actions[i].Target)
				}
			}
		})
	}
}

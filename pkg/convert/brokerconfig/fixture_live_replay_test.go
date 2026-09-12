//go:build jms_solace_real && jms_solace_integration

package brokerconfig

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

// The named queue must be dedicated to this test. Unlike the provider's
// publish-only smoke, this checks the imported draft's consumed body exactly.
func TestFixtureSolaceImportedTopologyLiveRoundTrip(t *testing.T) {
	broker, queue := os.Getenv("SOLACE_AMQP_URL"), os.Getenv("SOLACE_QUEUE")
	if broker == "" || queue == "" {
		t.Skip("SOLACE_AMQP_URL and dedicated SOLACE_QUEUE are required")
	}
	result, err := Convert(ConvertRequest{Data: []byte("queue.Fixture=" + queue)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 || result.Script.Protocol != "jms-solace" {
		t.Fatal("unexpected imported topology")
	}
	cfg := &result.Script.Config.JMS
	cfg.Mock = false
	cfg.BrokerURL = broker
	cfg.Username = os.Getenv("SOLACE_USERNAME")
	cfg.Password = os.Getenv("SOLACE_PASSWORD")
	cfg.MessageVPN = os.Getenv("SOLACE_VPN")
	marker := fmt.Sprintf(`{"fixture":"solace-import","marker":"%d","amount":19.95}`, time.Now().UnixNano())
	result.Script.Actions[0].Body = marker
	result.Script.Actions[1].Params["timeout_ms"] = 10000
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := engine.ReplayScript(ctx, result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || len(report.Events) != 2 {
		t.Fatalf("Solace replay failed: %s", report.Error)
	}
	if report.Events[1].Extractions["jms_body"] != marker {
		t.Fatal("Solace consume did not return the exact imported draft payload")
	}
}

package builtin

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRegistersBundledProtocols(t *testing.T) {
	want := []string{
		"browser",
		"database",
		"eventhubs",
		"grpc",
		"http",
		"java_http",
		"java_vuser",
		"jms-ibmmq",
		"jms-servicebus",
		"jms-solace",
		"kafka",
		"network",
		"soap",
		"websocket",
	}

	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			p, err := protocol.Get(name)
			if err != nil {
				t.Fatalf("protocol.Get(%q) returned error: %v", name, err)
			}
			if p.Name() != name {
				t.Fatalf("protocol name = %q, want %q", p.Name(), name)
			}
		})
	}
}

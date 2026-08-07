package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// protocolDocAliases maps every registered builtin protocol to the spellings a
// document may legitimately use for it. The enumerations in the docs below are
// hand-maintained, so this test is the cross-check that keeps them from drifting
// away from pkg/protocol/builtin when a protocol is added.
var protocolDocAliases = map[string][]string{
	"browser":        {"browser"},
	"bruno":          {"bruno"},
	"database":       {"database"},
	"eventhubs":      {"eventhubs", "Event Hubs"},
	"grpc":           {"grpc", "gRPC"},
	"http":           {"http", "HTTP"},
	"java_http":      {"java_http", "Java-over-HTTP", "Java over HTTP"},
	"java_vuser":     {"java_vuser", "Java Vuser"},
	"jms-ibmmq":      {"jms-ibmmq", "IBM MQ"},
	"jms-servicebus": {"jms-servicebus", "Service Bus"},
	"jms-solace":     {"jms-solace", "Solace"},
	"kafka":          {"kafka", "Kafka"},
	"network":        {"network"},
	"soap":           {"soap", "SOAP"},
	"websocket":      {"websocket", "WebSocket"},
}

// protocolSurfaceDocs are the documents that enumerate the runtime protocol
// surface; a contributor scoping a cross-protocol change reads one of them.
var protocolSurfaceDocs = []string{"AGENTS.md", "ARCHITECTURE.md", "DEVELOPMENT.md", "QUICK_REFERENCE.md"}

func TestProtocolDocAliasesCoverRegistry(t *testing.T) {
	for _, name := range protocol.List() {
		if _, ok := protocolDocAliases[name]; !ok {
			t.Errorf("protocol %q is registered in builtin but has no documentation alias; add it to protocolDocAliases and to %v", name, protocolSurfaceDocs)
		}
	}
}

func TestProtocolSurfaceDocsMentionEveryBundledProtocol(t *testing.T) {
	registered := protocol.List()
	for _, doc := range protocolSurfaceDocs {
		body := readRepoDoc(t, doc)
		t.Run(doc, func(t *testing.T) {
			for _, name := range registered {
				aliases, ok := protocolDocAliases[name]
				if !ok {
					continue // reported by TestProtocolDocAliasesCoverRegistry
				}
				if !containsAny(body, aliases) {
					t.Errorf("%s never mentions bundled protocol %q (tried %v)", doc, name, aliases)
				}
			}
		})
	}
}

func containsAny(body string, aliases []string) bool {
	for _, alias := range aliases {
		if strings.Contains(body, alias) {
			return true
		}
	}
	return false
}

func readRepoDoc(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

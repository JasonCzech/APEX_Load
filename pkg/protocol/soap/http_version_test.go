package soap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSOAPLeafHTTPVersionKeepsWirePolicyAndEvidence(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		fmt.Fprintf(w, `<Envelope><Body><actual>%d</actual></Body></Envelope>`, request.ProtoMajor)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	instance := &SOAP{}
	if err := instance.Init(context.Background(), map[string]any{"http_version": "2", "tls_insecure_skip_verify": true, "max_conns_per_host": 1}); err != nil {
		t.Fatal(err)
	}
	defer instance.Teardown(context.Background())
	for _, policy := range []string{"1.1", "2", "auto"} {
		version, err := protocol.NewHTTPVersionOverride(policy)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		result, err := instance.Execute(ctx, &protocol.Action{Name: "SOAP policy", Type: "POST", Target: server.URL, Body: []byte(`<Envelope><Body><Read/></Body></Envelope>`), SOAP: &protocol.SOAPActionConfig{Operation: "Read"}, HTTPVersion: version})
		cancel()
		actual := "2"
		if policy == "1.1" {
			actual = "1"
		}
		negotiated := "2"
		if policy == "1.1" {
			negotiated = "1.1"
		}
		if err != nil || !result.Success || !strings.Contains(result.ResponseBody, "<actual>"+actual+"</actual>") || result.ProtocolEvidence["protocol"] != "soap" || result.ProtocolEvidence["http_version_policy"] != policy || result.ProtocolEvidence["http_version_negotiated"] != negotiated || result.ProtocolEvidence["http_version_source"] != "wire" {
			t.Fatalf("SOAP leaf policy=%s result=%v err=%v", policy, result.ProtocolEvidence, err)
		}
	}
}

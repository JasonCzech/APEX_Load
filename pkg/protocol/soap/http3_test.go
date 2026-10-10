package soap

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpproto "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestSOAPRunsOverExactHTTP3(t *testing.T) {
	t.Setenv(httpproto.EnvHTTP3, "true")
	fixture := h3fixture.Start(t, h3fixture.Options{})
	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{"base_url": fixture.URL(""), "soap_version": "1.1", "http_version": "3", "tls_insecure_skip_verify": true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Teardown(context.Background()) })
	result, err := s.Execute(context.Background(), &protocol.Action{Name: "login", Target: "/echo", Body: []byte(`<Envelope><Body><Login/></Body></Envelope>`)})
	if err != nil || !result.Success || result.HTTPVersionNegotiated != "3" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if n := fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Proto == "HTTP/3.0" && a.Method == "POST" }); n != 1 {
		t.Fatalf("fixture saw %d HTTP/3 SOAP posts", n)
	}
}

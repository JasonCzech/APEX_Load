package java_http

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpproto "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestJavaHTTPRunsOverExactHTTP3(t *testing.T) {
	t.Setenv(httpproto.EnvHTTP3, "true")
	fixture := h3fixture.Start(t, h3fixture.Options{})
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{"base_url": fixture.URL(""), "http_version": "3", "tls_insecure_skip_verify": true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Teardown(context.Background()) })
	result, err := p.Execute(context.Background(), &protocol.Action{Name: "java-call", Method: "POST", Target: "/echo", Body: []byte(base64.StdEncoding.EncodeToString([]byte(`{"amount":1}`)))})
	if err != nil || !result.Success || result.HTTPVersionNegotiated != "3" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

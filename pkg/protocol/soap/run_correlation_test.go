package soap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSOAPManagedRunHeaderUsesHTTPOriginPolicy(t *testing.T) {
	var marker string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marker = r.Header.Get("X-Apex-Test-Run")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer server.Close()
	runtime := &SOAP{}
	if err := runtime.Init(context.Background(), map[string]any{"tls_insecure_skip_verify": true, "apexload_run_correlation_id": "run-123", "apexload_run_correlation_origins": []string{server.URL}}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Teardown(context.Background())
	result, err := runtime.Execute(context.Background(), &protocol.Action{Type: "soap_request", Method: http.MethodPost, Target: server.URL, Body: []byte(`<Envelope><Body><ping/></Body></Envelope>`)})
	if err != nil || !result.Success || marker != "run-123" {
		t.Fatalf("result=%+v err=%v marker=%q", result, err, marker)
	}
}

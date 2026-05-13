package soap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httpproto "github.com/jasonczech/stormrunner/pkg/protocol/http"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func BenchmarkSOAPExecuteComparison(b *testing.B) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><ok/></soapenv:Body></soapenv:Envelope>`))
	}))
	defer ts.Close()

	httpProto := &httpproto.HTTP{}
	if err := httpProto.Init(context.Background(), map[string]any{
		"base_url": ts.URL,
	}); err != nil {
		b.Fatalf("http Init returned error: %v", err)
	}
	defer httpProto.Teardown(context.Background())

	soapProto := &SOAP{}
	if err := soapProto.Init(context.Background(), map[string]any{
		"base_url":         ts.URL,
		"soap_version":     "1.1",
		"soap_namespaces":  map[string]string{"auth": "urn:auth"},
	}); err != nil {
		b.Fatalf("soap Init returned error: %v", err)
	}
	defer soapProto.Teardown(context.Background())

	httpAction := &protocol.Action{
		Name:   "http-login",
		Method: http.MethodPost,
		Target: "/AuthService",
		Body:   []byte(`<Login><username>alice</username></Login>`),
		Headers: map[string]string{
			"Content-Type": "text/xml; charset=utf-8",
		},
	}
	soapAction := &protocol.Action{
		Name:   "soap-login",
		Target: "/AuthService",
		Body:   []byte(`<username>alice</username>`),
		SOAP: &protocol.SOAPActionConfig{
			Action:    "urn:auth#Login",
			Operation: "Login",
			BodyMode:  "generated",
		},
	}

	b.ReportAllocs()
	b.Run("http-baseline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			result, err := httpProto.Execute(context.Background(), httpAction)
			if err != nil {
				b.Fatalf("http Execute returned error: %v", err)
			}
			if result == nil || !result.Success {
				b.Fatalf("http result = %#v, want success", result)
			}
		}
	})

	b.Run("soap-generated", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			result, err := soapProto.Execute(context.Background(), soapAction)
			if err != nil {
				b.Fatalf("soap Execute returned error: %v", err)
			}
			if result == nil || !result.Success {
				b.Fatalf("soap result = %#v, want success", result)
			}
		}
	})
}
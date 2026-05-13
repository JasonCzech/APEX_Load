package soap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestSOAPProtocolRegistered(t *testing.T) {
	p, err := protocol.Get("soap")
	if err != nil {
		t.Fatalf("Get(soap) returned error: %v", err)
	}
	if p.Name() != "soap" {
		t.Fatalf("Name() = %q, want soap", p.Name())
	}
}

func TestSOAPExecuteAddsSOAP11Defaults(t *testing.T) {
	var seenContentType string
	var seenSOAPAction string
	var seenMethod string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenContentType = r.Header.Get("Content-Type")
		seenSOAPAction = r.Header.Get("SOAPAction")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                    ts.URL,
		"soap_version":                "1.1",
		"soap_fault_fail_on_fault":    true,
		"request_timeout_ms":          float64(1000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "login",
		Target: "/AuthService",
		Body:   []byte(`<Envelope><Body><Login/></Body></Envelope>`),
		SOAP:   &protocol.SOAPActionConfig{Action: "urn:auth#Login"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("result = %#v, want successful response", result)
	}
	if seenMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", seenMethod)
	}
	if seenContentType != "text/xml; charset=utf-8" {
		t.Fatalf("content-type = %q, want text/xml; charset=utf-8", seenContentType)
	}
	if seenSOAPAction != "urn:auth#Login" {
		t.Fatalf("SOAPAction = %q, want urn:auth#Login", seenSOAPAction)
	}
}

func TestSOAPExecuteAddsSOAP12Defaults(t *testing.T) {
	var seenContentType string
	var seenSOAPAction string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenContentType = r.Header.Get("Content-Type")
		seenSOAPAction = r.Header.Get("SOAPAction")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"></env:Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":     ts.URL,
		"soap_version": "1.2",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	_, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "lookup",
		Target: "/AuthService",
		Body:   []byte(`<Envelope><Body><Lookup/></Body></Envelope>`),
		SOAP:   &protocol.SOAPActionConfig{Action: "urn:auth#Lookup"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(seenContentType, "application/soap+xml") {
		t.Fatalf("content-type = %q, want application/soap+xml", seenContentType)
	}
	if !strings.Contains(seenContentType, `action="urn:auth#Lookup"`) {
		t.Fatalf("content-type = %q, want action parameter", seenContentType)
	}
	if seenSOAPAction != "" {
		t.Fatalf("SOAPAction = %q, want empty for SOAP 1.2", seenSOAPAction)
	}
}
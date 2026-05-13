package soap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestSOAPExecuteWrapsGeneratedBodyInEnvelope(t *testing.T) {
	var seenBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		seenBody = string(payload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":         ts.URL,
		"soap_version":     "1.1",
		"soap_namespaces":  map[string]string{"auth": "urn:auth"},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	_, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "login",
		Target: "/AuthService",
		Body:   []byte(`<username>alice</username>`),
		SOAP: &protocol.SOAPActionConfig{
			Operation: "Login",
			BodyMode:  "generated",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(seenBody, `<soapenv:Envelope`) {
		t.Fatalf("body = %q, want soap envelope", seenBody)
	}
	if !strings.Contains(seenBody, `xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"`) {
		t.Fatalf("body = %q, want soapenv namespace", seenBody)
	}
	if !strings.Contains(seenBody, `xmlns:auth="urn:auth"`) {
		t.Fatalf("body = %q, want auth namespace", seenBody)
	}
	if !strings.Contains(seenBody, `<soapenv:Body><Login><username>alice</username></Login></soapenv:Body>`) {
		t.Fatalf("body = %q, want wrapped login body", seenBody)
	}
}

func TestSOAPExecuteAddsWSSecurityHeader(t *testing.T) {
	var seenBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		seenBody = string(payload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer ts.Close()

	fixedNow := time.Date(2026, time.May, 12, 15, 4, 5, 0, time.UTC)
	s := &SOAP{
		now: func() time.Time { return fixedNow },
		nonceGenerator: func() ([]byte, error) { return []byte("abc123"), nil },
	}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                              ts.URL,
		"soap_version":                          "1.1",
		"soap_ws_security_username":             "svc-user",
		"soap_ws_security_password":             "svc-pass",
		"soap_ws_security_password_type":        "text",
		"soap_ws_security_nonce":                true,
		"soap_ws_security_created":              true,
		"soap_ws_security_timestamp_ttl_seconds": float64(60),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	_, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "login",
		Target: "/AuthService",
		Body:   []byte(`<username>alice</username>`),
		SOAP: &protocol.SOAPActionConfig{
			Operation: "Login",
			BodyMode:  "generated",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(seenBody, `<soapenv:Header><wsse:Security`) {
		t.Fatalf("body = %q, want ws-security header", seenBody)
	}
	if !strings.Contains(seenBody, `<wsse:Username>svc-user</wsse:Username>`) {
		t.Fatalf("body = %q, want username token username", seenBody)
	}
	if !strings.Contains(seenBody, `PasswordText`) || !strings.Contains(seenBody, `>svc-pass</wsse:Password>`) {
		t.Fatalf("body = %q, want password text entry", seenBody)
	}
	if !strings.Contains(seenBody, `<wsse:Nonce>YWJjMTIz</wsse:Nonce>`) {
		t.Fatalf("body = %q, want base64 nonce", seenBody)
	}
	if !strings.Contains(seenBody, `<wsu:Created>2026-05-12T15:04:05Z</wsu:Created>`) {
		t.Fatalf("body = %q, want created timestamp", seenBody)
	}
	if !strings.Contains(seenBody, `<wsu:Expires>2026-05-12T15:05:05Z</wsu:Expires>`) {
		t.Fatalf("body = %q, want expires timestamp", seenBody)
	}
}

func TestSOAPExecuteFlagsSOAP11FaultEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Client</faultcode><faultstring>Invalid login</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                 ts.URL,
		"soap_version":             "1.1",
		"soap_fault_fail_on_fault": true,
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
	if result.Success {
		t.Fatalf("result.Success = true, want false for SOAP Fault")
	}
	if result.ErrorMessage != "soap fault: soapenv:Client: Invalid login" {
		t.Fatalf("ErrorMessage = %q, want SOAP fault details", result.ErrorMessage)
	}
	if !strings.Contains(result.ResponseBody, `<faultstring>Invalid login</faultstring>`) {
		t.Fatalf("ResponseBody = %q, want original fault payload", result.ResponseBody)
	}
}

func TestSOAPExecuteFlagsSOAP12FaultEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                 ts.URL,
		"soap_version":             "1.2",
		"soap_fault_fail_on_fault": true,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "lookup",
		Target: "/AuthService",
		Body:   []byte(`<Envelope><Body><Lookup/></Body></Envelope>`),
		SOAP:   &protocol.SOAPActionConfig{Action: "urn:auth#Lookup"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatalf("result.Success = true, want false for SOAP Fault")
	}
	if result.ErrorMessage != "soap fault: env:Sender: Request validation failed" {
		t.Fatalf("ErrorMessage = %q, want SOAP 1.2 fault details", result.ErrorMessage)
	}
}
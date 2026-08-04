package soap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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
		"base_url":                 ts.URL,
		"soap_version":             "1.1",
		"soap_fault_fail_on_fault": true,
		"request_timeout_ms":       float64(1000),
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
	if seenSOAPAction != `"urn:auth#Login"` {
		t.Fatalf("SOAPAction = %q, want quoted urn:auth#Login", seenSOAPAction)
	}
	if result.DependencyType != "soap" {
		t.Fatalf("DependencyType = %q, want soap", result.DependencyType)
	}
	if result.DependencyTarget != "127.0.0.1#Login" && !strings.HasSuffix(result.DependencyTarget, "#Login") {
		t.Fatalf("DependencyTarget = %q, want operation-tagged target", result.DependencyTarget)
	}
	if result.Extractions["soap_operation"] != "Login" {
		t.Fatalf("soap_operation = %q, want Login", result.Extractions["soap_operation"])
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.EnvelopeOverheadKB <= 0 {
		t.Fatalf("EnvelopeOverheadKB = %#v, want > 0", result.ServerTelemetry)
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
		"base_url":        ts.URL,
		"soap_version":    "1.1",
		"soap_namespaces": map[string]string{"auth": "urn:auth"},
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

func TestSOAPExecuteCapturesNamespaceConflictTelemetry(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":     ts.URL,
		"soap_version": "1.1",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "login",
		Target: "/AuthService",
		Body:   []byte(`<auth:username>alice</auth:username>`),
		SOAP: &protocol.SOAPActionConfig{
			Operation: "Login",
			BodyMode:  "generated",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want namespace conflict telemetry")
	}
	if result.ServerTelemetry.SOAPNamespaceConflict != "undeclared prefixes: auth" {
		t.Fatalf("SOAPNamespaceConflict = %q, want undeclared prefixes: auth", result.ServerTelemetry.SOAPNamespaceConflict)
	}
	if result.Extractions["soap_namespace_conflict"] != "undeclared prefixes: auth" {
		t.Fatalf("soap_namespace_conflict = %q, want undeclared prefixes: auth", result.Extractions["soap_namespace_conflict"])
	}
	if !result.Success {
		t.Fatalf("result.Success = false, want namespace conflict to be non-fatal")
	}
}

func TestShapeSOAPBodyDetectsReservedEnvelopePrefixConflict(t *testing.T) {
	body, conflict := shapeSOAPBody(&protocol.Action{
		Body: []byte(`<username>alice</username>`),
		SOAP: &protocol.SOAPActionConfig{
			Operation: "Login",
			BodyMode:  "generated",
		},
	}, "1.1", map[string]string{"soapenv": "urn:auth"})

	if conflict != "prefix soapenv already mapped to urn:auth" {
		t.Fatalf("conflict = %q, want reserved soapenv prefix conflict", conflict)
	}
	if !strings.Contains(string(body), `xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"`) {
		t.Fatalf("body = %q, want SOAP envelope namespace to win", string(body))
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
		now:            func() time.Time { return fixedNow },
		nonceGenerator: func() ([]byte, error) { return []byte("abc123"), nil },
	}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                               ts.URL,
		"soap_version":                           "1.1",
		"soap_ws_security_username":              "svc-user",
		"soap_ws_security_password":              "svc-pass",
		"soap_ws_security_password_type":         "text",
		"soap_ws_security_nonce":                 true,
		"soap_ws_security_created":               true,
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
	if result.Extractions["soap_fault_category"] != "client" {
		t.Fatalf("soap_fault_category = %q, want client", result.Extractions["soap_fault_category"])
	}
	if result.Extractions["soap_fault_code"] != "soapenv:Client" {
		t.Fatalf("soap_fault_code = %q, want soapenv:Client", result.Extractions["soap_fault_code"])
	}
	if result.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want SOAP fault telemetry")
	}
	if result.ServerTelemetry.SOAPFaultCode != "soapenv:Client" {
		t.Fatalf("SOAPFaultCode = %q, want soapenv:Client", result.ServerTelemetry.SOAPFaultCode)
	}
	if result.ServerTelemetry.SOAPFaultCategory != "client" {
		t.Fatalf("SOAPFaultCategory = %q, want client", result.ServerTelemetry.SOAPFaultCategory)
	}
	if !strings.Contains(result.ResponseBody, `<faultstring>Invalid login</faultstring>`) {
		t.Fatalf("ResponseBody = %q, want original fault payload", result.ResponseBody)
	}
	if result.Snapshot == nil {
		t.Fatal("Snapshot = nil, want SOAP fault snapshot")
	}
	if result.Snapshot.RequestMethod != http.MethodPost {
		t.Fatalf("Snapshot.RequestMethod = %q, want POST", result.Snapshot.RequestMethod)
	}
	if result.Snapshot.RequestURL != ts.URL+"/AuthService" {
		t.Fatalf("Snapshot.RequestURL = %q, want %q", result.Snapshot.RequestURL, ts.URL+"/AuthService")
	}
	if result.Snapshot.RequestHeaders["SOAPAction"] != `"urn:auth#Login"` {
		t.Fatalf("Snapshot.RequestHeaders SOAPAction = %q, want quoted urn:auth#Login", result.Snapshot.RequestHeaders["SOAPAction"])
	}
	if !strings.Contains(result.Snapshot.RequestHeaders["Content-Type"], "text/xml") {
		t.Fatalf("Snapshot.RequestHeaders Content-Type = %q, want text/xml", result.Snapshot.RequestHeaders["Content-Type"])
	}
	if result.Snapshot.RequestBody != `<Envelope><Body><Login/></Body></Envelope>` {
		t.Fatalf("Snapshot.RequestBody = %q, want original request body", result.Snapshot.RequestBody)
	}
	if !strings.Contains(result.Snapshot.ResponseBody, `<faultstring>Invalid login</faultstring>`) {
		t.Fatalf("Snapshot.ResponseBody = %q, want fault payload", result.Snapshot.ResponseBody)
	}
}

func TestSOAPExecuteInspectsFaultBeforeDiscardingSuccessfulBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Client</faultcode><faultstring>bad request</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`)
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url": ts.URL, "soap_fault_fail_on_fault": true,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Execute(context.Background(), &protocol.Action{
		Target: "/", Body: []byte(`<Envelope><Body/></Envelope>`),
		DiscardResponseBodyOnSuccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ServerTelemetry == nil || result.ServerTelemetry.SOAPFaultCode != "soapenv:Client" {
		t.Fatalf("fault was not retained for inspection: %#v", result)
	}
	if result.ResponseBody == "" {
		t.Fatal("failed SOAP response body was discarded")
	}
}

func TestSOAPExecutePreservesAuthoredContentType(t *testing.T) {
	var contentType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		_, _ = io.WriteString(w, `<Envelope><Body><ok/></Body></Envelope>`)
	}))
	defer ts.Close()
	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{"base_url": ts.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), &protocol.Action{
		Target: "/", Body: []byte(`<Envelope><Body/></Envelope>`),
		Headers: map[string]string{"content-type": "application/vnd.example+xml"},
	}); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/vnd.example+xml" {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestBuildSOAPSnapshot_RedactsSecretRequestValues(t *testing.T) {
	action := &protocol.Action{
		Method:          http.MethodPost,
		Headers:         map[string]string{"SOAPAction": "urn:auth#super-secret"},
		RedactedHeaders: map[string]string{"SOAPAction": "urn:auth#[REDACTED]"},
		Body:            []byte(`<Envelope><Body><Token>super-secret</Token></Body></Envelope>`),
		RedactedBody:    []byte(`<Envelope><Body><Token>[REDACTED]</Token></Body></Envelope>`),
		RedactedTarget:  "https://soap.example.test/AuthService?token=[REDACTED]",
	}
	result := &protocol.Result{StatusCode: 500, ResponseBody: "fault"}

	snapshot := buildSOAPSnapshot(action, "https://soap.example.test/AuthService?token=super-secret", result)

	if snapshot.RequestURL != "https://soap.example.test/AuthService?token=[REDACTED]" {
		t.Fatalf("RequestURL = %q", snapshot.RequestURL)
	}
	if snapshot.RequestHeaders["SOAPAction"] != "urn:auth#[REDACTED]" {
		t.Fatalf("RequestHeaders[SOAPAction] = %q", snapshot.RequestHeaders["SOAPAction"])
	}
	if snapshot.RequestBody != `<Envelope><Body><Token>[REDACTED]</Token></Body></Envelope>` {
		t.Fatalf("RequestBody = %q", snapshot.RequestBody)
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
	if result.ServerTelemetry == nil || result.ServerTelemetry.SOAPFaultCode != "env:Sender" {
		t.Fatalf("ServerTelemetry = %#v, want SOAP 1.2 fault code", result.ServerTelemetry)
	}
}

func TestSOAPExecuteDoesNotFlagValueOutsideFault(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><LookupResponse><Value>env:Sender</Value><Text>not a fault</Text></LookupResponse></env:Body></env:Envelope>`))
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
	if !result.Success {
		t.Fatalf("result.Success = false, want non-Fault payload to pass: %#v", result)
	}
	if result.Extractions["soap_fault_code"] != "" {
		t.Fatalf("soap_fault_code = %q, want empty", result.Extractions["soap_fault_code"])
	}
}

func TestSOAPExecuteUsesPositiveTimestampTTLWhenConfiguredZero(t *testing.T) {
	var seenBody string
	fixedNow := time.Date(2026, time.May, 12, 15, 4, 5, 0, time.UTC)
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

	s := &SOAP{now: func() time.Time { return fixedNow }}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                               ts.URL,
		"soap_ws_security_timestamp_ttl_seconds": float64(0),
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
	if !strings.Contains(seenBody, `<wsu:Created>2026-05-12T15:04:05Z</wsu:Created>`) {
		t.Fatalf("body = %q, want created timestamp", seenBody)
	}
	if !strings.Contains(seenBody, `<wsu:Expires>2026-05-12T15:09:05Z</wsu:Expires>`) {
		t.Fatalf("body = %q, want corrected positive expires timestamp", seenBody)
	}
}

func TestSOAPApplySecurityExpandsSelfClosingHeader(t *testing.T) {
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
		"base_url":                       ts.URL,
		"soap_ws_security_username":      "svc-user",
		"soap_ws_security_password":      "svc-pass",
		"soap_ws_security_password_type": "text",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	_, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "login",
		Target: "/AuthService",
		Body:   []byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header/><soapenv:Body><Login/></soapenv:Body></soapenv:Envelope>`),
		SOAP:   &protocol.SOAPActionConfig{Action: "urn:auth#Login", Operation: "Login", BodyMode: "generated"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(seenBody, `<soapenv:Header><wsse:Security`) {
		t.Fatalf("body = %q, want expanded security header", seenBody)
	}
}

func TestSOAPExecuteBuildsProtocolEvidence(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":     ts.URL,
		"soap_version": "1.1",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := s.Execute(context.Background(), &protocol.Action{
		Name:   "login",
		Target: "/AuthService",
		Body:   []byte(`<Envelope><Body><Login/></Body></Envelope>`),
		SOAP:   &protocol.SOAPActionConfig{Action: "urn:auth#Login", Operation: "Login", BodyMode: "generated"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ProtocolEvidence["protocol"] != "soap" {
		t.Fatalf("ProtocolEvidence = %#v, want SOAP protocol", result.ProtocolEvidence)
	}
	if result.ProtocolEvidence["operation"] != "Login" {
		t.Fatalf("ProtocolEvidence operation = %#v, want Login", result.ProtocolEvidence["operation"])
	}
	metadata, ok := result.ProtocolEvidence["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("ProtocolEvidence metadata = %#v", result.ProtocolEvidence["metadata"])
	}
	if metadata["status_code"] != 200 {
		t.Fatalf("status_code = %#v, want 200", metadata["status_code"])
	}
}

// TestSOAPClearCookiesDelegatesToWrappedHTTP covers the CookieController
// delegation browser emulation's "simulate a new user on each iteration" relies
// on. The compile-time interface assertion only proves the method exists; this
// proves it reaches the wrapped HTTP instance that owns the jar.
//
// ClearCache has no equivalent behavioral assertion here because the simulated
// response cache only stores GETs and SOAP always POSTs, so a SOAP session can
// never accumulate a cache entry to observe. Its delegation is covered
// end-to-end by the java_http suite, which shares the same wrapped HTTP.
func TestSOAPClearCookiesDelegatesToWrappedHTTP(t *testing.T) {
	var (
		mu         sync.Mutex
		sawCookies []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawCookies = append(sawCookies, r.Header.Get("Cookie"))
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "soap", Path: "/"})
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	}))
	defer ts.Close()

	s := &SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":           ts.URL,
		"soap_version":       "1.1",
		"request_timeout_ms": float64(1000),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() { _ = s.Teardown(context.Background()) }()

	call := func() {
		t.Helper()
		if _, err := s.Execute(context.Background(), &protocol.Action{
			Name:   "op",
			Target: "/Service",
			Body:   []byte(`<Envelope><Body><Op/></Body></Envelope>`),
			SOAP:   &protocol.SOAPActionConfig{Action: "urn:svc#Op"},
		}); err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
	}
	lastCookie := func() string {
		mu.Lock()
		defer mu.Unlock()
		return sawCookies[len(sawCookies)-1]
	}

	call()
	call()
	if !strings.Contains(lastCookie(), "sid=soap") {
		t.Fatalf("expected the jar to replay the cookie, got %q", lastCookie())
	}

	if err := s.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies: %v", err)
	}
	call()
	if got := lastCookie(); got != "" {
		t.Fatalf("cookie header after ClearCookies = %q, want empty", got)
	}
}

// Nil inner protocol must not panic: the engine may clear before Init on an
// aborted VUser start.
func TestSOAPClearCacheAndCookiesNilInnerAreSafe(t *testing.T) {
	s := &SOAP{}
	if err := s.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache on uninitialized protocol: %v", err)
	}
	if err := s.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies on uninitialized protocol: %v", err)
	}
}

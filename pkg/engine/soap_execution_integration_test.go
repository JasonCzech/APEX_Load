package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	soapprotocol "github.com/jasonczech/apexload/pkg/protocol/soap"
)

// This composes the engine's production-default action resolution with the
// SOAP adapter. The engine normally discards successful response bodies; SOAP
// must temporarily retain that body long enough to detect an HTTP-200 fault.
func TestDefaultEngineResponsePolicyStillDetectsSOAPFault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Client</faultcode><faultstring>invalid order</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`)
	}))
	defer server.Close()

	adapter := &soapprotocol.SOAP{}
	if err := adapter.Init(context.Background(), map[string]any{
		"base_url":                 server.URL,
		"soap_version":             "1.1",
		"soap_fault_fail_on_fault": true,
	}); err != nil {
		t.Fatalf("initialize SOAP adapter: %v", err)
	}
	defer adapter.Teardown(context.Background())

	vu := &VUser{
		Script: &dsl.Script{
			Protocol: "soap",
			Config:   dsl.ScriptConfig{BaseURL: server.URL},
		},
		Variables: map[string]string{},
	}
	action := vu.resolveAction(dsl.Action{
		Name:   "Submit order",
		Type:   "POST",
		Target: "/orders",
		Body:   `<Envelope><Body><SubmitOrder/></Body></Envelope>`,
		SOAP:   &dsl.SOAPActionConfig{Action: "urn:orders#SubmitOrder"},
	})
	if !action.DiscardResponseBodyOnSuccess {
		t.Fatal("engine default did not request successful response-body discard")
	}

	result, err := adapter.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("execute SOAP action: %v", err)
	}
	if result.Success {
		t.Fatalf("SOAP fault reported success: %#v", result)
	}
	if result.Extractions["soap_fault_code"] != "soapenv:Client" {
		t.Fatalf("soap fault code = %q, want soapenv:Client", result.Extractions["soap_fault_code"])
	}
	if !strings.Contains(result.ResponseBody, "invalid order") {
		t.Fatalf("failed SOAP response body was discarded: %q", result.ResponseBody)
	}
}

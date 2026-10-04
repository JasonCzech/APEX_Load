package soap

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSOAPCompressedContentRetainsFixedReceipt(t *testing.T) {
	const payload = `<Envelope><Body><value>synthetic</value></Body></Envelope>`
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	_, _ = writer.Write([]byte(payload))
	_ = writer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(encoded.Bytes())
	}))
	defer server.Close()
	h := &SOAP{}
	if err := h.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "POST", Target: server.URL, Body: []byte(`<Envelope/>`), SOAP: &protocol.SOAPActionConfig{Operation: "Read"}})
	if err != nil || !result.Success || result.ResponseBody != payload || result.ProtocolEvidence["protocol"] != "soap" || result.ProtocolEvidence["response_content_state"] != "complete" || result.ProtocolEvidence["response_content_basis"] != "decoded" {
		t.Fatal("SOAP wrapper lost decoded response or fixed completion receipt")
	}
}

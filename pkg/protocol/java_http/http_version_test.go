package java_http

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJavaHTTPLeafHTTPVersionKeepsCodecWirePolicyAndEvidence(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if string(body) != "synthetic object" || request.Header.Get("Content-Type") != "application/x-java-serialized-object" {
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, request.ProtoMajor)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	instance := &JavaHTTP{}
	if err := instance.Init(context.Background(), map[string]any{"http_version": "2", "tls_insecure_skip_verify": true, "max_conns_per_host": 1, "java_http_preserve_binary_result": true}); err != nil {
		t.Fatal(err)
	}
	defer instance.Teardown(context.Background())
	for _, policy := range []string{"1.1", "2", "auto"} {
		version, err := protocol.NewHTTPVersionOverride(policy)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		result, err := instance.Execute(ctx, &protocol.Action{Name: "Java HTTP policy", Type: "POST", Target: server.URL, Body: []byte(base64.StdEncoding.EncodeToString([]byte("synthetic object"))), HTTPVersion: version})
		cancel()
		actual, negotiated := "2", "2"
		if policy == "1.1" {
			actual, negotiated = "1", "1.1"
		}
		body, decodeErr := base64.StdEncoding.DecodeString(result.ResponseBody)
		if err != nil || !result.Success || decodeErr != nil || string(body) != actual || result.ProtocolEvidence["http_version_policy"] != policy || result.ProtocolEvidence["http_version_negotiated"] != negotiated || result.ProtocolEvidence["http_version_source"] != "wire" {
			t.Fatalf("Java HTTP policy=%s result=%v err=%v", policy, result.ProtocolEvidence, err)
		}
	}
}

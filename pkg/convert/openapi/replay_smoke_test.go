package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestOpenAPIImportReplaySmokeChainsCreatedID(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/orders":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", "/orders/ord-123")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ord-123"})
		case r.Method == http.MethodGet && r.URL.Path == "/orders/ord-123":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ord-123", "status": "created"})
		case r.Method == http.MethodPut && r.URL.Path == "/orders/ord-123":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ord-123", "status": "updated"})
		case r.Method == http.MethodDelete && r.URL.Path == "/orders/ord-123":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := Convert(ImportRequest{
		Name: "Orders API",
		Content: `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "` + server.URL + `"}],
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "requestBody": {"content": {"application/json": {"schema": {
          "type": "object",
          "properties": {"customerId": {"type": "string"}}
        }}}},
        "responses": {"201": {"description": "created", "content": {"application/json": {"schema": {
          "type": "object",
          "properties": {"id": {"type": "string"}}
        }}}}}
      }
    },
    "/orders/{orderId}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      },
      "put": {
        "operationId": "updateOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "requestBody": {"content": {"application/json": {"schema": {
          "type": "object",
          "properties": {"status": {"type": "string"}}
        }}}},
        "responses": {"200": {"description": "ok"}}
      },
      "delete": {
        "operationId": "deleteOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"204": {"description": "deleted"}}
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if got := report.FinalVariables["order_id"]; got != "ord-123" {
		t.Fatalf("final order_id = %q, want ord-123", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /orders", "GET /orders/ord-123", "PUT /orders/ord-123", "DELETE /orders/ord-123"}
	if len(seen) != len(want) {
		t.Fatalf("requests = %#v, want %#v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("requests = %#v, want %#v", seen, want)
		}
	}
}

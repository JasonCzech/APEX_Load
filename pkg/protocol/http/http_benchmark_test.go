package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func BenchmarkHTTPExecuteSmallJSON(b *testing.B) {
	body := `{"status":"ok","id":"cart-123","amount":42}`
	benchmarkHTTPExecute(b, body)
}

func BenchmarkHTTPExecute50KBJSON(b *testing.B) {
	body := `{"items":[` + strings.Repeat(`{"sku":"SKU-123","quantity":1,"price":42},`, 1280) + `{"sku":"SKU-999","quantity":1,"price":42}]}`
	benchmarkHTTPExecute(b, body)
}

func benchmarkHTTPExecute(b *testing.B, body string) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{
		"base_url":           server.URL,
		"request_timeout_ms": float64(5000),
	}); err != nil {
		b.Fatalf("Init: %v", err)
	}

	action := &protocol.Action{
		Name:   "bench",
		Method: "GET",
		Target: "/payload",
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := h.Execute(context.Background(), action)
		if err != nil {
			b.Fatalf("Execute: %v", err)
		}
		if result == nil || !result.Success || result.ResponseBody == "" {
			b.Fatalf("unexpected result: %#v", result)
		}
	}
}

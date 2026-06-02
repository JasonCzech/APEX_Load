package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPStampsNetworkProfileLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{
		"base_url":                  server.URL,
		"web_network_profile_label": "3g",
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if h.networkProfile != "3g" {
		t.Fatalf("networkProfile = %q, want 3g", h.networkProfile)
	}

	result, err := h.Execute(context.Background(), &protocol.Action{Name: "home", Type: "GET", Target: "/"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.NetworkProfile != "3g" {
		t.Fatalf("result network profile = %q, want 3g", result.NetworkProfile)
	}
}

func TestHTTPNoNetworkProfileLabelLeavesEmpty(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://example.com"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if h.networkProfile != "" {
		t.Fatalf("expected empty network profile, got %q", h.networkProfile)
	}
}

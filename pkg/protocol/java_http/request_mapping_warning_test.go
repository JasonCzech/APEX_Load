package java_http

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestJavaHTTPWarnsOnUnusedRequestMapping(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	j := &JavaHTTP{}
	err := j.Init(context.Background(), map[string]any{
		"base_url":                  "http://example.test",
		"java_http_request_mapping": map[string]any{"field": "value"},
		"java_http_object_schema":   "com.example.Order",
	})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "NOT applied at runtime") {
		t.Fatalf("expected a warning about unused request mapping, log was: %q", buf.String())
	}
}

func TestJavaHTTPNoWarnWithoutRequestMapping(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	j := &JavaHTTP{}
	if err := j.Init(context.Background(), map[string]any{"base_url": "http://example.test"}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if strings.Contains(buf.String(), "NOT applied at runtime") {
		t.Fatalf("did not expect a request-mapping warning, log was: %q", buf.String())
	}
}

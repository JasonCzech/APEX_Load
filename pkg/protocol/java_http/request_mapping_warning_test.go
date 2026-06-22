package java_http

import (
	"context"
	"strings"
	"testing"
)

func TestJavaHTTPRejectsUnusedRequestMapping(t *testing.T) {
	j := &JavaHTTP{}
	err := j.Init(context.Background(), map[string]any{
		"base_url":                  "http://example.test",
		"java_http_request_mapping": map[string]any{"field": "value"},
		"java_http_object_schema":   "com.example.Order",
	})
	if err == nil || !strings.Contains(err.Error(), "request_mapping/object_schema") {
		t.Fatalf("Init error = %v, want request_mapping/object_schema rejection", err)
	}
}

func TestJavaHTTPAllowsNoRequestMapping(t *testing.T) {
	j := &JavaHTTP{}
	if err := j.Init(context.Background(), map[string]any{"base_url": "http://example.test"}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}

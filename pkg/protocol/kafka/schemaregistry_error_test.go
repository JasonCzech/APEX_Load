package kafka

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSchemaRegistryClientErrorPaths covers xcut-tests-1: the three distinct
// schemaID failure branches (non-2xx status, undecodable body, transport error)
// must each surface an error rather than returning a bogus schema id.
func TestSchemaRegistryClientErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("non-2xx status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := newSchemaRegistryClient(srv.URL, `"string"`, "AVRO")
		if _, err := c.schemaID(ctx, "subj"); err == nil {
			t.Fatal("expected an error on a 500 registry response")
		}
	})

	t.Run("undecodable 2xx body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
		}))
		defer srv.Close()
		c := newSchemaRegistryClient(srv.URL, `"string"`, "AVRO")
		if _, err := c.schemaID(ctx, "subj"); err == nil {
			t.Fatal("expected a decode error on a non-JSON registry body")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		c := newSchemaRegistryClient("http://127.0.0.1:1", `"string"`, "AVRO")
		c.httpClient.Timeout = 500 * time.Millisecond
		if _, err := c.schemaID(ctx, "subj"); err == nil {
			t.Fatal("expected a transport error against an unreachable registry")
		}
	})
}

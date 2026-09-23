package soap

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHTTPWrapperDelegatesCacheIteration(t *testing.T) {
	var wrapper SOAP
	if err := wrapper.BeginIteration(context.Background()); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	if err := wrapper.Init(context.Background(), map[string]any{"http_cache_enabled": true, "cache_max_unused_iterations": 1}); err != nil {
		t.Fatal(err)
	}
	defer wrapper.Teardown(context.Background())
	action := &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL}
	execute := func() {
		t.Helper()
		result, err := wrapper.http.Execute(context.Background(), action)
		if err != nil || !result.Success {
			t.Fatalf("%+v %v", result, err)
		}
	}
	wrapper.BeginIteration(context.Background())
	execute()
	execute()
	if hits.Load() != 1 {
		t.Fatal("cache not populated")
	}
	wrapper.BeginIteration(context.Background())
	wrapper.BeginIteration(context.Background())
	execute()
	if hits.Load() != 2 {
		t.Fatal("wrapper did not evict unreferenced cache")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wrapper.BeginIteration(ctx) != context.Canceled {
		t.Fatal("cancellation not delegated")
	}
}

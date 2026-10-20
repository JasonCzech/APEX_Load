package java_http

import (
	"bytes"
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The endpoint count, independent of Result.BytesSent, proves that an empty
// request cannot escape codec validation and send target traffic.
func TestJavaHTTPUnsupportedEncodingNeverReachesTarget(t *testing.T) {
	var calls atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{"base_url": endpoint.URL}); err != nil {
		t.Fatal(err)
	}
	defer p.Teardown(context.Background())
	for _, encoding := range []string{"raw", "gzip", "Base64", " raw "} {
		for _, tc := range []struct {
			name string
			body []byte
		}{
			{"nil", nil}, {"empty", []byte{}}, {"whitespace", []byte(" ")}, {"nonempty", []byte("YQ==")},
		} {
			t.Run(encoding+"/"+tc.name, func(t *testing.T) {
				action := &protocol.Action{Name: "rejected", Method: "POST", Target: "/invoke", BodyEncoding: encoding, Body: tc.body, Headers: map[string]string{"X-Request-ID": "preflight"}}
				before := *action
				before.Headers = maps.Clone(action.Headers)
				before.Body = bytes.Clone(action.Body)
				beforeCalls := calls.Load()
				result, err := p.Execute(context.Background(), action)
				if err != nil || result == nil || result.Success || !strings.Contains(result.ErrorMessage, "unsupported java_http action body encoding") {
					t.Fatalf("unsupported encoding result=%+v err=%v", result, err)
				}
				if calls.Load() != beforeCalls || result.BytesSent != 0 || result.BytesReceived != 0 || result.StatusCode != 0 {
					t.Fatalf("unsupported encoding reached HTTP: calls=%d result=%+v", calls.Load(), result)
				}
				if result.Snapshot == nil || result.Snapshot.RequestURL != endpoint.URL+"/invoke" || result.Snapshot.RequestHeaders["X-Request-ID"] != "preflight" {
					t.Fatalf("preflight diagnostic lost: %+v", result.Snapshot)
				}
				if !reflect.DeepEqual(action, &before) {
					t.Fatal("caller action mutated")
				}
			})
		}
	}
}

func TestJavaHTTPEmptySupportedEncodingRemainsExecutable(t *testing.T) {
	var calls atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ContentLength != 0 {
			t.Errorf("empty request content length=%d", r.ContentLength)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{"base_url": endpoint.URL}); err != nil {
		t.Fatal(err)
	}
	defer p.Teardown(context.Background())
	for _, encoding := range []string{"", "base64", " base64 ", "   "} {
		t.Run(encoding, func(t *testing.T) {
			before := calls.Load()
			result, err := p.Execute(context.Background(), &protocol.Action{Name: "empty", Method: "POST", Target: "/invoke", BodyEncoding: encoding})
			if err != nil || result == nil || !result.Success || calls.Load() != before+1 || result.StatusCode != http.StatusNoContent || result.BytesSent != 0 {
				t.Fatalf("supported empty request result=%+v calls=%d err=%v", result, calls.Load(), err)
			}
			if result.ServerTelemetry == nil || result.ServerTelemetry.SerializationMs != 0 {
				t.Fatal("empty body acquired conversion work")
			}
		})
	}
}

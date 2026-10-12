package javarunner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeHTTPAuthenticatedHopsAndLiteralPayloads(t *testing.T) {
	var callbacks atomic.Int32
	var handler http.HandlerFunc
	handler = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if ServeNativeHTTP(w, r, 4096, handler) {
			return
		}
		var q struct {
			Hop int `json:"hop"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		cb := NativeDataCallbackFrom(r.Context())
		if cb == nil {
			http.Error(w, "callback_missing", 400)
			return
		}
		for i := uint64(1); i <= 2; i++ {
			reply, err := cb(r.Context(), i, []byte(`{"alias":"work","operation":"push","values":{"id":"{{secret.literal}}雪"}}`))
			if err != nil || string(reply) != `{"result":{"status":"ok"}}` {
				http.Error(w, "callback_failed", 500)
				return
			}
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}
	backend := httptest.NewServer(handler)
	defer backend.Close()
	var proxy http.HandlerFunc
	proxy = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if ServeNativeHTTP(w, r, 4096, proxy) {
			return
		}
		req, _ := http.NewRequestWithContext(r.Context(), "POST", backend.URL, r.Body)
		req.Header.Set("Authorization", "Bearer fixture")
		out, err := DoNativeHTTP(backend.Client(), req, 4096)
		if err != nil {
			http.Error(w, "proxy_failed", 500)
			return
		}
		defer out.Body.Close()
		w.WriteHeader(out.StatusCode)
		_, _ = io.Copy(w, out.Body)
	}
	front := httptest.NewServer(proxy)
	defer front.Close()
	ctx := WithNativeDataCallback(t.Context(), func(_ context.Context, seq uint64, payload []byte) ([]byte, error) {
		if seq != uint64(callbacks.Add(1)) || !bytes.Contains(payload, []byte("{{secret.literal}}雪")) {
			t.Error("literal or sequence changed")
		}
		return []byte(`{"result":{"status":"ok"}}`), nil
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", front.URL, strings.NewReader(`{"hop":2}`))
	req.Header.Set("Authorization", "Bearer fixture")
	response, err := DoNativeHTTP(front.Client(), req, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 201 || string(body) != `{"ok":true}` || callbacks.Load() != 2 {
		t.Fatalf("status=%d body=%s callbacks=%d", response.StatusCode, body, callbacks.Load())
	}
}

func TestNativeHTTPBoundsAndCancellation(t *testing.T) {
	for _, kind := range []string{"oversized_request", "oversized_reply", "cancel_wait", "old_server", "wrong_sequence"} {
		t.Run(kind, func(t *testing.T) {
			var handler http.HandlerFunc
			handler = func(w http.ResponseWriter, r *http.Request) {
				if kind == "old_server" {
					_, _ = io.WriteString(w, `{}`)
					return
				}
				if ServeNativeHTTP(w, r, 1024, handler) {
					return
				}
				seq := uint64(1)
				if kind == "wrong_sequence" {
					seq = 2
				}
				_, err := NativeDataCallbackFrom(r.Context())(r.Context(), seq, []byte(`{}`))
				if err != nil {
					http.Error(w, "lost", 500)
					return
				}
				_, _ = io.WriteString(w, `{}`)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			ctx = WithNativeDataCallback(ctx, func(ctx context.Context, _ uint64, _ []byte) ([]byte, error) {
				if kind == "cancel_wait" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if kind == "oversized_reply" {
					return bytes.Repeat([]byte("x"), NativeDataPayloadBytes+1), nil
				}
				return []byte(`{}`), nil
			})
			body := `{}`
			if kind == "oversized_request" {
				body = strings.Repeat("x", (1<<20)+1)
			}
			req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(body))
			out, err := DoNativeHTTP(server.Client(), req, 1024)
			if out != nil {
				defer out.Body.Close()
			}
			if kind == "wrong_sequence" {
				if err == nil && out.StatusCode != 500 {
					t.Fatal("wrong sequence accepted")
				}
			} else if err == nil {
				t.Fatal("expected bounded failure", strconv.Itoa(out.StatusCode))
			}
		})
	}
}

package http

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPBodyObservationMeasuresPreparedInitialBody(t *testing.T) {
	for _, tc := range []struct {
		name, compression, authoredEncoding, wantCompression string
		body                                                 []byte
	}{
		{"empty", "", "", "none", nil},
		{"empty compression is not applied", "gzip", "", "none", nil},
		{"binary", "", "", "none", []byte{0, 255, 1, 2}},
		{"gzip", "gzip", "", "gzip", bytes.Repeat([]byte("payload"), 100)},
		{"deflate", "deflate", "", "deflate", bytes.Repeat([]byte("payload"), 100)},
		{"authored encoding is not adapter compression", "", "br", "none", []byte{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type observation struct {
				body []byte
				size int64
			}
			seen := make(chan observation, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				body := wire
				switch tc.wantCompression {
				case "gzip":
					reader, err := gzip.NewReader(bytes.NewReader(wire))
					if err != nil {
						t.Error(err)
						return
					}
					body, err = io.ReadAll(reader)
					_ = reader.Close()
					if err != nil {
						t.Error(err)
						return
					}
				case "deflate":
					reader, err := zlib.NewReader(bytes.NewReader(wire))
					if err != nil {
						t.Error(err)
						return
					}
					body, err = io.ReadAll(reader)
					_ = reader.Close()
					if err != nil {
						t.Error(err)
						return
					}
				}
				seen <- observation{body, int64(len(wire))}
				w.WriteHeader(204)
			}))
			defer server.Close()
			h := &HTTP{}
			if err := h.Init(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			defer h.Teardown(context.Background())
			var samples []protocol.HTTPBodyObservation
			ctx := protocol.WithHTTPBodyObserver(context.Background(), func(s protocol.HTTPBodyObservation) { samples = append(samples, s) })
			action := &protocol.Action{Name: "body", Method: "POST", Target: server.URL, Body: tc.body, Params: map[string]any{"content_encoding": tc.compression}, Headers: map[string]string{}}
			if tc.authoredEncoding != "" {
				action.Headers["Content-Encoding"] = tc.authoredEncoding
			}
			result, err := h.Execute(ctx, action)
			if err != nil || result == nil || !result.Success {
				t.Fatal(result, err)
			}
			select {
			case got := <-seen:
				if !bytes.Equal(got.body, tc.body) || !reflect.DeepEqual(samples, []protocol.HTTPBodyObservation{{Bytes: got.size, AppliedCompression: tc.wantCompression}}) {
					t.Fatal("independent endpoint/preparation mismatch", samples, got)
				}
			default:
				t.Fatal("no endpoint observation")
			}
		})
	}
}

func TestHTTPBodyObservationSeparatesPreparationFromDelivery(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("closed fixture received traffic") }))
	closed.Close()
	h := &HTTP{}
	if err := h.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	var samples []protocol.HTTPBodyObservation
	ctx := protocol.WithHTTPBodyObserver(context.Background(), func(s protocol.HTTPBodyObservation) { samples = append(samples, s) })
	result, err := h.Execute(ctx, &protocol.Action{Name: "not-delivered", Method: "POST", Target: closed.URL, Body: []byte("abc")})
	if err != nil || result == nil || result.Success || !reflect.DeepEqual(samples, []protocol.HTTPBodyObservation{{Bytes: 3, AppliedCompression: "none"}}) {
		t.Fatal(result, err, samples)
	}
	samples = nil
	ctx = protocol.WithHTTPBodyObserver(context.Background(), func(s protocol.HTTPBodyObservation) { samples = append(samples, s) })
	result, err = h.Execute(ctx, &protocol.Action{Name: "invalid-compression", Method: "POST", Target: closed.URL, Body: []byte("abc"), Params: map[string]any{"content_encoding": "unsupported"}})
	if err != nil || result == nil || result.Success || len(samples) != 0 {
		t.Fatal("refused preparation fabricated sample", result, err, samples)
	}
}

func TestHTTPBodyObservationMetaRefreshCannotReplaceInitialBody(t *testing.T) {
	seen := make(chan int, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		seen <- len(body)
		if r.URL.Path == "/start" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<meta http-equiv="refresh" content="0; url=/final">`)
		}
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"max_meta_refresh": 2})
	var samples []protocol.HTTPBodyObservation
	ctx := protocol.WithHTTPBodyObserver(context.Background(), func(s protocol.HTTPBodyObservation) { samples = append(samples, s) })
	result, err := h.Execute(ctx, &protocol.Action{Name: "initial", Type: "request", Method: "GET", Target: server.URL + "/start", Body: []byte("seed")})
	if err != nil || result == nil || !result.Success {
		t.Fatal(result, err)
	}
	if len(seen) != 2 {
		t.Fatal("refresh fixture did not execute both requests", len(seen))
	}
	if first, second := <-seen, <-seen; first != 4 || second != 0 {
		t.Fatal("unexpected endpoint body populations", first, second)
	}
	if !reflect.DeepEqual(samples, []protocol.HTTPBodyObservation{{Bytes: 4, AppliedCompression: "none"}}) {
		t.Fatal("nested request replaced initial preparation", samples)
	}
}

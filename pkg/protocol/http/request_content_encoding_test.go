package http

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// params.content_encoding compresses the body on the wire and labels it, as
// LoadRunner's ContentEncoding does; the step's snapshot keeps the readable
// body.
func TestHTTPCompressesTheRequestBody(t *testing.T) {
	type received struct {
		encoding, body string
		length         int64
	}
	got := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			reader = gz
		case "deflate":
			zr, err := zlib.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			reader = zr
		}
		body, _ := io.ReadAll(reader)
		got <- received{encoding: r.Header.Get("Content-Encoding"), body: string(body), length: r.ContentLength}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	payload := `{"events":[` + strings.Repeat(`{"name":"screen_view"},`, 50) + `{"name":"end"}]}`
	for _, encoding := range []string{"gzip", "deflate", " GZIP "} {
		result, err := h.Execute(context.Background(), &protocol.Action{
			Name: "analytics", Type: "POST", Method: "POST", Target: server.URL + "/connect/proxy/app-analytics-logging",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(payload),
			Params:  map[string]any{RequestContentEncodingParam: encoding},
		})
		if err != nil || !result.Success {
			t.Fatalf("%s: err %v, result %+v", encoding, err, result)
		}
		sent := <-got
		want := strings.ToLower(strings.TrimSpace(encoding))
		if sent.encoding != want || sent.body != payload || sent.length >= int64(len(payload)) || result.BytesSent != sent.length {
			t.Fatalf("%s: server saw encoding %q, %d bytes (sent %d), body %q", encoding, sent.encoding, sent.length, result.BytesSent, sent.body)
		}
	}

	// The header names the compression applied, replacing an authored one.
	if _, err := h.Execute(context.Background(), &protocol.Action{
		Name: "authored", Type: "POST", Method: "POST", Target: server.URL + "/x",
		HeaderList: protocol.HeaderList{{Name: "content-encoding", Value: "deflate"}}, Body: []byte("hello"),
		Params: map[string]any{RequestContentEncodingParam: "gzip"},
	}); err != nil {
		t.Fatal(err)
	}
	if sent := <-got; sent.encoding != "gzip" || sent.body != "hello" {
		t.Fatalf("authored header: %+v", sent)
	}

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name: "bad", Type: "POST", Method: "POST", Target: server.URL + "/x", Body: []byte("x"),
		Params: map[string]any{RequestContentEncodingParam: "br"},
	})
	if err != nil || result.Success || result.StatusCode != 0 || !strings.Contains(result.ErrorMessage, `"br" is not supported`) {
		t.Fatalf("unsupported encoding: err %v, result %+v", err, result)
	}
}

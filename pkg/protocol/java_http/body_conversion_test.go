package java_http

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Independent endpoint bytes detect both a second decode that fails and a
// second decode that silently succeeds (when the opaque bytes look like base64).
func TestJavaHTTPBodyIsDecodedExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name, encoding, compression string
		payload                     []byte
	}{
		{"implicit binary", "", "", []byte{0xac, 0xed, 0, 5, 0xff}},
		{"explicit binary", "base64", "", []byte{0xac, 0xed, 0, 5, 0xff}},
		{"base64 looking bytes", "base64", "", []byte("YWJj")},
		{"trimmed encoding", " base64 ", "", []byte("YWJj")},
		{"empty", "base64", "", nil},
		{"compressed", "base64", "gzip", bytes.Repeat([]byte{0xac, 0xed, 0, 5}, 256)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type observation struct {
				body      []byte
				wireBytes int
				encoding  string
			}
			received := make(chan observation, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				body := wire
				if r.Header.Get("Content-Encoding") == "gzip" {
					reader, err := gzip.NewReader(bytes.NewReader(wire))
					if err != nil {
						t.Error(err)
						return
					}
					body, err = io.ReadAll(reader)
					if err != nil {
						t.Error(err)
					}
					_ = reader.Close()
				}
				received <- observation{body, len(wire), r.Header.Get("Content-Encoding")}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			p := &JavaHTTP{}
			if err := p.Init(context.Background(), map[string]any{protocol.CorrelationEvidenceCaptureConfigKey: true}); err != nil {
				t.Fatal(err)
			}
			defer p.Teardown(context.Background())
			encoded := []byte(base64.StdEncoding.EncodeToString(tc.payload))
			action := &protocol.Action{Name: "opaque", Method: "POST", Target: server.URL, Body: encoded, BodyEncoding: tc.encoding}
			if tc.compression != "" {
				action.Params = map[string]any{"content_encoding": tc.compression}
			}
			original := *action
			result, err := p.Execute(context.Background(), action)
			if err != nil || result == nil || !result.Success {
				t.Fatalf("Execute: result=%+v err=%v", result, err)
			}
			select {
			case got := <-received:
				if !bytes.Equal(got.body, tc.payload) || got.encoding != tc.compression {
					t.Fatalf("endpoint received %x (%s), want %x (%s)", got.body, got.encoding, tc.payload, tc.compression)
				}
				if result.BytesSent != int64(got.wireBytes) {
					t.Fatalf("body byte count %d != endpoint %d", result.BytesSent, got.wireBytes)
				}
			default:
				t.Fatal("no endpoint observation")
			}
			if !reflect.DeepEqual(action, &original) || !bytes.Equal(action.Body, encoded) {
				t.Fatal("caller action mutated")
			}
			evidence := result.CorrelationEvidence
			if evidence == nil || evidence.RequestBody != string(tc.payload) || evidence.RequestBodyTransformed != (len(encoded) > 0) {
				t.Fatalf("incorrect transformed-body provenance: %+v", evidence)
			}
		})
	}
}

func TestJavaHTTPBodyConversionFailureAndRedaction(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	defer p.Teardown(context.Background())
	for _, body := range []string{"Y", "YWJ", "YWJj%"} {
		result, err := p.Execute(context.Background(), &protocol.Action{Name: "bad", Method: "POST", Target: server.URL, Body: []byte(body), BodyEncoding: "base64"})
		if err != nil || result == nil || result.Success || result.BytesSent != 0 {
			t.Fatalf("invalid input: %+v %v", result, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("malformed bodies sent %d requests", calls.Load())
	}
	const safe = "[redacted opaque request]"
	body := base64.StdEncoding.EncodeToString([]byte("credential-sentinel"))
	result, err := p.Execute(context.Background(), &protocol.Action{Name: "redacted", Method: "POST", Target: server.URL, Body: []byte(body), BodyEncoding: "base64", RedactedBody: []byte(safe)})
	if err != nil || result == nil || result.Success || result.StatusCode != http.StatusInternalServerError || result.Snapshot == nil {
		t.Fatalf("HTTP failure not preserved: %+v %v", result, err)
	}
	if result.Snapshot.RequestBody != safe {
		t.Fatalf("snapshot did not preserve redacted body: %q", result.Snapshot.RequestBody)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one request, got %d", calls.Load())
	}
}

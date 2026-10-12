package javarunner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// NativeDataCallback exists only in a trusted invocation context. It carries no
// endpoint or credentials and is never serialized into Java parameters.
type NativeDataCallback func(context.Context, uint64, []byte) ([]byte, error)
type nativeDataContextKey struct{}

func WithNativeDataCallback(ctx context.Context, callback NativeDataCallback) context.Context {
	return context.WithValue(ctx, nativeDataContextKey{}, callback)
}
func NativeDataCallbackFrom(ctx context.Context) NativeDataCallback {
	cb, _ := ctx.Value(nativeDataContextKey{}).(NativeDataCallback)
	return cb
}

const nativeHTTPHeader = "X-Apexload-Native-Exchange"
const nativeHTTPFrameLimit = 12 << 20

var ErrNativeExchange = errors.New("native_service_exchange_lost")

type nativeHTTPFrame struct {
	Kind     string `json:"kind"`
	Sequence uint64 `json:"sequence,omitempty"`
	Payload  []byte `json:"payload,omitempty"`
	Status   int    `json:"status,omitempty"`
}

func nativeReadFrame(scanner *bufio.Scanner) (nativeHTTPFrame, error) {
	var frame nativeHTTPFrame
	if !scanner.Scan() {
		return frame, ErrNativeExchange
	}
	dec := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
	dec.DisallowUnknownFields()
	if dec.Decode(&frame) != nil || dec.Decode(new(any)) != io.EOF {
		return frame, ErrNativeExchange
	}
	return frame, nil
}
func nativeHTTPScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), nativeHTTPFrameLimit)
	return s
}

// DoNativeHTTP preserves the existing authenticated request and final response
// contract. Only the explicit callback-bearing invocation uses duplex frames.
// There is one outstanding callback per invocation, at most 1024 callbacks, and
// no retry of the Java method or transport after an uncertain exchange.
func DoNativeHTTP(client *http.Client, request *http.Request, responseLimit int64) (*http.Response, error) {
	cb := NativeDataCallbackFrom(request.Context())
	if cb == nil {
		return client.Do(request)
	}
	if responseLimit <= 0 || responseLimit > 8<<20 || request.Body == nil {
		return nil, ErrNativeExchange
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
	_ = request.Body.Close()
	if err != nil || len(body) > 1<<20 {
		return nil, ErrNativeExchange
	}
	timeout := time.Hour
	if client.Timeout > 0 && client.Timeout < timeout {
		timeout = client.Timeout
	}
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	read, write := io.Pipe()
	stopPipe := context.AfterFunc(ctx, func() { _ = read.CloseWithError(ErrNativeExchange); _ = write.CloseWithError(ErrNativeExchange) })
	defer stopPipe()
	defer read.Close()
	defer write.Close()
	req := request.Clone(ctx)
	req.Header = request.Header.Clone()
	req.Header.Set(nativeHTTPHeader, "v1")
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Body = read
	req.GetBody = nil
	req.ContentLength = -1
	sent := make(chan error, 1)
	go func() { sent <- json.NewEncoder(write).Encode(nativeHTTPFrame{Kind: "start", Payload: body}) }()
	response, err := client.Do(req)
	if err != nil {
		return nil, ErrNativeExchange
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get(nativeHTTPHeader) != "v1" {
		return nil, ErrNativeExchange
	}
	scanner := nativeHTTPScanner(response.Body)
	ready, err := nativeReadFrame(scanner)
	if err != nil || ready.Kind != "ready" || ready.Sequence != 0 || ready.Status != 0 || len(ready.Payload) != 0 {
		return nil, ErrNativeExchange
	}
	select {
	case err = <-sent:
		if err != nil {
			return nil, ErrNativeExchange
		}
	case <-ctx.Done():
		return nil, ErrNativeExchange
	}
	var last uint64
	for {
		frame, e := nativeReadFrame(scanner)
		if e != nil {
			return nil, e
		}
		switch frame.Kind {
		case "call":
			if frame.Sequence != last+1 || frame.Sequence > 1024 || frame.Status != 0 || len(frame.Payload) > NativeDataPayloadBytes {
				return nil, ErrNativeExchange
			}
			result, e := cb(ctx, frame.Sequence, frame.Payload)
			if e != nil || len(result) > NativeDataPayloadBytes {
				return nil, ErrNativeExchange
			}
			last = frame.Sequence
			if json.NewEncoder(write).Encode(nativeHTTPFrame{Kind: "reply", Sequence: last, Payload: result}) != nil {
				return nil, ErrNativeExchange
			}
		case "finish":
			if frame.Sequence != 0 || frame.Status < 100 || frame.Status > 599 || int64(len(frame.Payload)) > responseLimit {
				return nil, ErrNativeExchange
			}
			return &http.Response{StatusCode: frame.Status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(frame.Payload)), Request: request}, nil
		default:
			return nil, ErrNativeExchange
		}
	}
}

type nativeCapture struct {
	header http.Header
	status int
	data   bytes.Buffer
	limit  int
	failed bool
}

func (w *nativeCapture) Header() http.Header { return w.header }
func (w *nativeCapture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *nativeCapture) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.failed || len(b) > w.limit-w.data.Len() {
		w.failed = true
		return 0, ErrNativeExchange
	}
	return w.data.Write(b)
}

// ServeNativeHTTP must be called only after the endpoint's outer authentication.
// The wrapped handler repeats all ordinary request/owner/lease/target validation
// on the original JSON body before it can obtain a scoped runtime callback.
func ServeNativeHTTP(w http.ResponseWriter, r *http.Request, limit int64, next http.HandlerFunc) bool {
	if r.Header.Get(nativeHTTPHeader) == "" {
		return false
	}
	if r.Header.Get(nativeHTTPHeader) != "v1" || limit <= 0 || limit > 8<<20 {
		http.Error(w, "native_exchange_invalid", 400)
		return true
	}
	control := http.NewResponseController(w)
	if control.EnableFullDuplex() != nil {
		http.Error(w, "native_exchange_unavailable", 503)
		return true
	}
	scanner := nativeHTTPScanner(r.Body)
	first, err := nativeReadFrame(scanner)
	if err != nil || first.Kind != "start" || first.Sequence != 0 || first.Status != 0 || len(first.Payload) > 1<<20 {
		http.Error(w, "native_exchange_invalid", 400)
		return true
	}
	w.Header().Set(nativeHTTPHeader, "v1")
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	if encoder.Encode(nativeHTTPFrame{Kind: "ready"}) != nil || control.Flush() != nil {
		return true
	}
	var lock sync.Mutex
	var last uint64
	callback := func(ctx context.Context, sequence uint64, payload []byte) ([]byte, error) {
		if !lock.TryLock() {
			return nil, ErrNativeExchange
		}
		defer lock.Unlock()
		if ctx.Err() != nil || sequence != last+1 || sequence > 1024 || len(payload) > NativeDataPayloadBytes {
			return nil, ErrNativeExchange
		}
		stop := context.AfterFunc(ctx, func() { _ = control.SetReadDeadline(time.Now()) })
		defer stop()
		if encoder.Encode(nativeHTTPFrame{Kind: "call", Sequence: sequence, Payload: payload}) != nil || control.Flush() != nil {
			return nil, ErrNativeExchange
		}
		reply, e := nativeReadFrame(scanner)
		if e != nil || reply.Kind != "reply" || reply.Sequence != sequence || reply.Status != 0 || len(reply.Payload) > NativeDataPayloadBytes {
			return nil, ErrNativeExchange
		}
		last = sequence
		return reply.Payload, nil
	}
	clone := r.Clone(WithNativeDataCallback(r.Context(), callback))
	clone.Header = r.Header.Clone()
	clone.Header.Del(nativeHTTPHeader)
	clone.Header.Set("Content-Type", "application/json")
	clone.Body = io.NopCloser(bytes.NewReader(first.Payload))
	clone.ContentLength = int64(len(first.Payload))
	capture := &nativeCapture{header: make(http.Header), limit: int(limit)}
	next(capture, clone)
	if capture.failed {
		return true
	}
	if capture.status == 0 {
		capture.status = 200
	}
	_ = encoder.Encode(nativeHTTPFrame{Kind: "finish", Status: capture.status, Payload: capture.data.Bytes()})
	_ = control.Flush()
	return true
}

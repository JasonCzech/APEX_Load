package http

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The observer sits above the existing policy transport. It never fetches a
// target, changes redirect decisions, or consumes a body ahead of execution.
type replayHARTransport struct {
	base    http.RoundTripper
	capture *protocol.ReplayHARCapture
	mu      sync.Mutex
	records []*replayHARRecord
}
type replayHARRecord struct {
	entry        protocol.NetworkEntry
	response     *http.Response
	intermediate bool
	observed     bool
	mu           sync.Mutex
}

func newReplayHARTransport(base http.RoundTripper, c *protocol.ReplayHARCapture) *replayHARTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &replayHARTransport{base: base, capture: c}
}
func harHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}
func (r *replayHARTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	if len(r.records) >= 2048 {
		r.mu.Unlock()
		return r.base.RoundTrip(req)
	}
	record := &replayHARRecord{entry: protocol.NetworkEntry{URL: req.URL.String(), Method: req.Method, HAR: protocol.NewReplayHARExchange()}}
	r.records = append(r.records, record)
	r.mu.Unlock()
	c := r.capture
	observed := req.Clone(req.Context())
	if c != nil {
		record.entry.RequestHeaders = harHeaders(req.Header)
		if c.Options.KeepBody(true) {
			if req.Body == nil {
				record.entry.HAR.RequestBody = c.Body(nil, req.Header.Get("Content-Type"), true)
			} else {
				observed.Body = &replayHARBodyReader{ReadCloser: req.Body, limit: c.Options.MaxBodyBytes, done: func(data []byte, complete bool) {
					record.mu.Lock()
					defer record.mu.Unlock()
					record.entry.HAR.RequestBody = c.Body(data, req.Header.Get("Content-Type"), complete)
				}}
			}
		}
	}
	var phaseMu sync.Mutex
	start := record.entry.HAR.StartedAt
	marks := map[string]time.Time{}
	phases := []protocol.BrowserResourcePhase{}
	mark := func(name string) { phaseMu.Lock(); marks[name] = time.Now(); phaseMu.Unlock() }
	end := func(name string) {
		phaseMu.Lock()
		defer phaseMu.Unlock()
		if t := marks[name]; !t.IsZero() {
			a, b := float64(t.Sub(start))/float64(time.Millisecond), float64(time.Since(start))/float64(time.Millisecond)
			phase := protocol.BrowserResourcePhase{Name: name, StartMS: &a, EndMS: &b}
			replaced := false
			for i := range phases {
				if phases[i].Name == name {
					phases[i] = phase
					replaced = true
					break
				}
			}
			if !replaced {
				phases = append(phases, phase)
			}
			delete(marks, name)
		}
	}
	// A transport-level policy or header rejection is not retained traffic.
	// Keep connection attempts (including failed dials) even with timings off.
	var attempted atomic.Bool
	trace := &httptrace.ClientTrace{GetConn: func(string) { attempted.Store(true) }}
	if c != nil && c.Options.Timings {
		t := trace
		*t = httptrace.ClientTrace{GetConn: t.GetConn,
			DNSStart: func(httptrace.DNSStartInfo) { mark("dns") }, DNSDone: func(httptrace.DNSDoneInfo) { end("dns") },
			ConnectStart: func(string, string) { mark("connect") }, ConnectDone: func(string, string, error) { end("connect") },
			TLSHandshakeStart: func() { mark("tls") }, TLSHandshakeDone: func(tls.ConnectionState, error) { end("tls") },
			GotConn: func(httptrace.GotConnInfo) { mark("send") }, WroteRequest: func(httptrace.WroteRequestInfo) { end("send"); mark("request_wait") },
			GotFirstResponseByte: func() { end("request_wait"); mark("response") },
		}
	}
	observed = observed.WithContext(httptrace.WithClientTrace(observed.Context(), trace))
	resp, err := r.base.RoundTrip(observed)
	record.mu.Lock()
	if resp != nil {
		record.entry.StatusCode = resp.StatusCode
		if c != nil {
			record.entry.ResponseHeaders = harHeaders(resp.Header)
		}
	}
	if err != nil {
		record.entry.FailureKind = "transport"
	}
	record.mu.Unlock()
	r.mu.Lock()
	record.response = resp
	record.observed = attempted.Load() || resp != nil
	if record.observed && req.Response != nil {
		for _, prior := range r.records {
			if prior.response == req.Response {
				prior.intermediate = true
			}
		}
	}
	r.mu.Unlock()
	finish := func(data []byte, complete bool) {
		end("response")
		record.mu.Lock()
		defer record.mu.Unlock()
		if c != nil && c.Options.Timings {
			phaseMu.Lock()
			record.entry.HAR.Phases = harConnectIncludesTLS(phases)
			phaseMu.Unlock()
		}
		if c != nil && c.Options.KeepBody(false) && resp != nil {
			if encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
				record.entry.HAR.ResponseBody = &protocol.ReplayHARBody{State: "unavailable"}
			} else {
				record.entry.HAR.ResponseBody = c.Body(data, resp.Header.Get("Content-Type"), complete)
			}
		}
	}
	if resp != nil && resp.Body != nil {
		limit := 0
		if c != nil && c.Options.KeepBody(false) {
			limit = c.Options.MaxBodyBytes
		}
		resp.Body = &replayHARBodyReader{ReadCloser: resp.Body, limit: limit, done: finish}
	} else {
		finish(nil, err == nil)
	}
	return resp, err
}
func (r *replayHARTransport) entries() []protocol.NetworkEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]protocol.NetworkEntry, 0, len(r.records))
	for _, record := range r.records {
		if !record.observed {
			continue
		}
		if record.intermediate && (r.capture == nil || !r.capture.Options.Redirects) {
			continue
		}
		record.mu.Lock()
		entry := protocol.CloneNetworkEntries([]protocol.NetworkEntry{record.entry})[0]
		record.mu.Unlock()
		out = append(out, entry)
	}
	return out
}

type replayHARBodyReader struct {
	io.ReadCloser
	limit    int
	mu       sync.Mutex
	closed   bool
	data     []byte
	complete bool
	once     sync.Once
	done     func([]byte, bool)
}

func (b *replayHARBodyReader) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if !b.closed {
		if b.limit > 0 && len(b.data) <= b.limit {
			keep := min(n, b.limit+1-len(b.data))
			b.data = append(b.data, p[:keep]...)
		}
		if err == io.EOF {
			b.complete = true
		}
	}
	b.mu.Unlock()
	if err == io.EOF {
		b.finish()
	}
	return n, err
}
func (b *replayHARBodyReader) finish() {
	b.once.Do(func() {
		b.mu.Lock()
		data, complete := b.data, b.complete
		b.data = nil
		b.closed = true
		b.mu.Unlock()
		b.done(data, complete)
	})
}
func (b *replayHARBodyReader) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }

// Execution already decodes the final response under its own bounded reader.
// Reuse that result instead of decoding or fetching content a second time.
func (r *replayHARTransport) decodedBody(resp *http.Response, data []byte, complete bool) {
	if r.capture == nil || !r.capture.Options.KeepBody(false) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		if record.response == resp {
			record.mu.Lock()
			record.entry.HAR.ResponseBody = r.capture.Body(data, resp.Header.Get("Content-Type"), complete)
			record.mu.Unlock()
			return
		}
	}
}

// Resource fetches publish extra observed hops without altering logical fetch counters.
type replayResourceHARKey struct{}

// HAR connect includes TLS. httptrace's ConnectDone measures only the TCP leg.
func harConnectIncludesTLS(phases []protocol.BrowserResourcePhase) []protocol.BrowserResourcePhase {
	out := append([]protocol.BrowserResourcePhase(nil), phases...)
	var tlsEnd *float64
	for _, p := range out {
		if p.Name == "tls" {
			tlsEnd = p.EndMS
		}
	}
	for i, p := range out {
		if p.Name == "connect" && tlsEnd != nil && p.EndMS != nil && *tlsEnd > *p.EndMS {
			v := *tlsEnd
			out[i].EndMS = &v
		}
	}
	return out
}

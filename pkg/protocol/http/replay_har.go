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
	base         http.RoundTripper
	capture      *protocol.ReplayHARCapture
	captureTrace bool
	// scriptAction marks the recorder of the action's own top-level request:
	// only its first hop is protocol.NetworkEntry.ScriptRequest. Page resource
	// fetches and meta-refresh follow-ups leave it false.
	scriptAction bool
	mu           sync.Mutex
	records      []*replayHARRecord
	// authoredCookie is the Cookie header the script's request carried before
	// net/http added jar cookies (noteAuthoredRequest); authoredNoted reports
	// whether it was recorded at all.
	authoredCookie string
	authoredNoted  bool
}

// noteAuthoredRequest records the request headers the script authored, before
// the client adds cookies from its jar.
func (r *replayHARTransport) noteAuthoredRequest(header http.Header) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authoredCookie = strings.Join(header.Values("Cookie"), "; ")
	r.authoredNoted = true
}

type replayHARRecord struct {
	entry        protocol.NetworkEntry
	response     *http.Response
	trace        *httpTraceCollector
	intermediate bool
	observed     bool
	mu           sync.Mutex
}

func newReplayHARTransport(base http.RoundTripper, c *protocol.ReplayHARCapture, captureTrace ...bool) *replayHARTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	withTrace := len(captureTrace) > 0 && captureTrace[0]
	return &replayHARTransport{base: base, capture: c, captureTrace: withTrace}
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
	var hopTrace *httpTraceCollector
	if r.captureTrace {
		hopTrace = newHTTPTraceCollector()
	}
	record := &replayHARRecord{
		// The action's own recorder: its first hop is the request the script's
		// action authored; later hops are redirects or auth retries.
		entry: protocol.NetworkEntry{URL: req.URL.String(), Method: req.Method, HAR: protocol.NewReplayHARExchange(), ScriptRequest: r.scriptAction && len(r.records) == 0},
		trace: hopTrace,
	}
	if record.entry.ScriptRequest {
		// A Cookie header that differs from the authored one carries cookies
		// the jar added from earlier server responses.
		if cookie := strings.Join(req.Header.Values("Cookie"), "; "); cookie != "" && (!r.authoredNoted || cookie != r.authoredCookie) {
			record.entry.RequestCookieNotAuthored = true
		}
		// An Authorization header built from script-resolved credential halves
		// (the NTLM/Negotiate pre-challenge Basic, or the Basic net/http builds
		// from URL userinfo) is script-authored credential material: it follows
		// config.log.redact_run_evidence like the halves it was built from. When
		// a half carries a platform value, the engine registers the composite as
		// platform-managed (platformDerivedActionComposites and the authored
		// {{secret.*}} surface registration), so the exact matcher withholds it
		// in every mode.
	}
	r.records = append(r.records, record)
	r.mu.Unlock()
	c := r.capture
	observed := req.Clone(req.Context())
	if c != nil || r.captureTrace {
		record.entry.RequestHeaders = harHeaders(req.Header)
		// A typed Authorization the request snapshot withholds (a Basic pair
		// over a platform-managed value, a Digest response that is not
		// provably over a script literal) is withheld on every hop here too.
		if masked, ok := withheldAuthorization(req, req.Header.Get("Authorization")); ok {
			record.entry.RequestHeaders["Authorization"] = masked
		}
	}
	if c != nil && c.Options.KeepBody(true) {
		if req.Body == nil {
			record.entry.HAR.RequestBody = c.Body(nil, req.Header.Get("Content-Type"), true)
		} else {
			observed.Body = &replayHARBodyReader{ReadCloser: req.Body, limit: c.Options.MaxBodyBytes, done: func(data []byte, complete bool) {
				record.mu.Lock()
				defer record.mu.Unlock()
				// A transformed body that carries a platform value is recorded
				// as the redacted authored body (transformedBodyWithholding).
				if complete {
					data = withheldRequestBody(req, data)
				}
				record.entry.HAR.RequestBody = c.Body(data, req.Header.Get("Content-Type"), complete)
			}}
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
	wantTimings := (c != nil && c.Options.Timings) || (c == nil && r.captureTrace)
	trace := &httptrace.ClientTrace{GetConn: func(string) { attempted.Store(true) }}
	if wantTimings || hopTrace != nil {
		t := trace
		*t = httptrace.ClientTrace{
			GetConn: t.GetConn,
			DNSStart: func(httptrace.DNSStartInfo) {
				if wantTimings {
					mark("dns")
				}
			},
			DNSDone: func(info httptrace.DNSDoneInfo) {
				if wantTimings {
					end("dns")
				}
				hopTrace.onDNSDone(info)
			},
			ConnectStart: func(string, string) {
				if wantTimings {
					mark("connect")
				}
			},
			ConnectDone: func(netw, addr string, err error) {
				if wantTimings {
					end("connect")
				}
				hopTrace.onConnectDone(netw, addr, err)
			},
			TLSHandshakeStart: func() {
				if wantTimings {
					mark("tls")
				}
			},
			TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
				if wantTimings {
					end("tls")
				}
				hopTrace.onTLSHandshakeDone(cs, err)
			},
			GotConn: func(info httptrace.GotConnInfo) {
				if wantTimings {
					mark("send")
				}
				hopTrace.onGotConn(info)
			},
			Wait100Continue: func() {
				hopTrace.onWait100Continue()
			},
			Got100Continue: func() {
				hopTrace.onGot100Continue()
			},
			WroteHeaders: func() {
				hopTrace.onWroteHeaders()
			},
			WroteRequest: func(httptrace.WroteRequestInfo) {
				hopTrace.onWait100ContinueDone()
				if wantTimings {
					end("send")
					mark("request_wait")
				}
			},
			GotFirstResponseByte: func() {
				hopTrace.onWait100ContinueDone()
				if wantTimings {
					end("request_wait")
					mark("response")
				}
			},
		}
	}
	observed = observed.WithContext(httptrace.WithClientTrace(observed.Context(), trace))
	resp, err := r.base.RoundTrip(observed)
	if hopTrace != nil {
		hopTrace.observeExchange(req, resp, "", 0, 0)
	}
	record.mu.Lock()
	if resp != nil {
		record.entry.StatusCode = resp.StatusCode
		if c != nil || r.captureTrace {
			record.entry.ResponseHeaders = harHeaders(resp.Header)
		}
	}
	if err != nil {
		record.entry.FailureKind = "transport"
	}
	if hopTrace != nil {
		record.entry.Trace = hopTrace.snapshot()
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
	var respReader *replayHARBodyReader
	finish := func(data []byte, complete bool) {
		if wantTimings {
			end("response")
		}
		totalBytes := int64(0)
		if respReader != nil {
			totalBytes = respReader.bytesRead()
		}
		if hopTrace != nil && totalBytes > 0 {
			hopTrace.setBodyBytes(totalBytes, 0)
		}
		record.mu.Lock()
		defer record.mu.Unlock()
		if record.entry.DurationMs == 0 && !start.IsZero() {
			record.entry.DurationMs = float64(time.Since(start)) / float64(time.Millisecond)
		}
		if record.entry.BytesReceived == 0 && totalBytes > 0 {
			record.entry.BytesReceived = totalBytes
		}
		if wantTimings {
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
		if hopTrace != nil {
			record.entry.Trace = hopTrace.snapshot()
		}
	}
	if resp != nil && resp.Body != nil {
		limit := 0
		if c != nil && c.Options.KeepBody(false) {
			limit = c.Options.MaxBodyBytes
		}
		respReader = &replayHARBodyReader{ReadCloser: resp.Body, limit: limit, done: finish}
		resp.Body = respReader
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
		keepRedirects := r.captureTrace
		if r.capture != nil {
			keepRedirects = r.capture.Options.Redirects
		}
		if record.intermediate && !keepRedirects {
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
	limit      int
	mu         sync.Mutex
	closed     bool
	data       []byte
	totalBytes int64
	complete   bool
	once       sync.Once
	done       func([]byte, bool)
}

func (b *replayHARBodyReader) bytesRead() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBytes
}

func (b *replayHARBodyReader) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if !b.closed {
		if n > 0 {
			b.totalBytes += int64(n)
		}
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
		if b.done != nil {
			b.done(data, complete)
		}
	})
}
func (b *replayHARBodyReader) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }

// Execution already decodes the final response under its own bounded reader.
// Reuse that result instead of decoding or fetching content a second time.
func (r *replayHARTransport) decodedBody(resp *http.Response, data []byte, complete bool) {
	if (r.capture == nil || !r.capture.Options.KeepBody(false)) && !r.captureTrace {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		if record.response == resp {
			if record.trace != nil {
				record.trace.setBodyBytes(0, int64(len(data)))
			}
			record.mu.Lock()
			if r.capture != nil && r.capture.Options.KeepBody(false) {
				record.entry.HAR.ResponseBody = r.capture.Body(data, resp.Header.Get("Content-Type"), complete)
			}
			if record.trace != nil {
				record.entry.Trace = record.trace.snapshot()
			}
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

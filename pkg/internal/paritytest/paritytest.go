// Package paritytest is the shared harness behind the LoadRunner web parity
// corpus. A corpus case is a directory holding a source (an enterprise script
// archive, a recorder session or a HAR) plus expected.json, which states what
// VuGen would put on the wire for that source. Tests convert the source with
// the production converter, replay it once through the real engine against a
// recording fixture server, and compare the requests the server received with
// the expectation.
//
// A case may also carry replay.har: a LoadRunner replay capture of the same
// script against the same fixture responses. When present it is the ground
// truth and its non-resource entries replace the hand-written requests.
//
// The package is imported only by tests.
package paritytest

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	// WebSocket steps inside HTTP scripts run on the registered websocket
	// protocol.
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
)

// Origin is the placeholder origin corpus sources and expectations use. Tests
// replace it with the fixture server's origin before converting.
const Origin = "http://parity.test"

// WebSocketOrigin is the placeholder origin of WebSocket URIs; it reaches
// the fixture server too.
const WebSocketOrigin = "ws://parity.test"

// AliasOrigin is a second placeholder origin that reaches the same fixture
// server under a different host name ("localhost" instead of 127.0.0.1), for
// cases that must prove something is sent to one host and not another.
const AliasOrigin = "http://alias.parity.test"

// Outcome values for Expectation.Outcome.
const (
	// OutcomeReplay means the converted script must be executable and must put
	// exactly the expected requests on the wire.
	OutcomeReplay = "replay"
	// OutcomeGated means the construct is not reproduced yet, so the import
	// must carry an execution gate with the listed reason codes and replay must
	// refuse to run. A silent conversion is the failure this outcome catches.
	OutcomeGated = "gated"
	// OutcomeReview means the import is an approximation that may still work:
	// it must carry the listed promotion-gate reason codes, and replay must be
	// allowed so testers can debug it. Requests are compared only when listed.
	OutcomeReview = "review"
)

// Expectation is the parsed expected.json of one corpus case.
type Expectation struct {
	Description string `json:"description"`
	// Source names the VuGen documentation or capture the expectation is
	// derived from, so a reviewer can check it.
	Source          string            `json:"source"`
	Outcome         string            `json:"outcome"`
	GateReasonCodes []string          `json:"gate_reason_codes,omitempty"`
	WarningsContain []string          `json:"warnings_contain,omitempty"`
	Requests        []ExpectedRequest `json:"requests,omitempty"`
	Responses       []Response        `json:"responses,omitempty"`
	// Variables seeds replay variables, for values a real run would get from
	// earlier steps or data files the case does not include.
	Variables map[string]string `json:"variables,omitempty"`
	// ExpectFailures names the actions of a replay case that must fail (a
	// check against a broken page), each with a fragment of its error. Every
	// other action must succeed; replay continues past each failure.
	ExpectFailures map[string]string `json:"expect_failures,omitempty"`
	// Secrets are the project secrets the replay resolves.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Dependencies states request ordering and concurrency overlap invariants
	// that must hold across the recorded requests.
	Dependencies []DependencyRule `json:"dependencies,omitempty"`
}

// DependencyRule asserts a request ordering or concurrency overlap invariant
// over recorded fixture traffic. Request specs are "METHOD /path" (or "/path"
// to match any method, optionally with "?query").
type DependencyRule struct {
	// Prerequisite requires the matching request(s) to have strictly completed
	// at the server before each Dependent request arrived
	// (prereq.CompletionSeq < dep.ArrivalSeq). When Prerequisite and Dependent
	// request counts differ across waves, each Dependent is checked against the
	// latest Prerequisite that arrived before it.
	Prerequisite string   `json:"prerequisite,omitempty"`
	Dependents   []string `json:"dependents,omitempty"`
	// OverlapGroup requires all listed requests (within each wave) to have
	// overlapped in flight at the server (max ArrivalSeq < min CompletionSeq).
	OverlapGroup []string `json:"overlap_group,omitempty"`
	// MinMaxInFlight requires the server's observed concurrency high-water mark
	// (and, when OverlapGroup is set, the peak in-flight count observed across
	// each OverlapGroup wave's arrivals) to be at least this value.
	MinMaxInFlight int `json:"min_max_in_flight,omitempty"`
}

// ExpectedRequest describes one request the fixture server must receive.
// Only the fields that are set are compared.
type ExpectedRequest struct {
	Method string  `json:"method"`
	Path   string  `json:"path"`
	Query  *string `json:"query,omitempty"`
	// QueryPattern is an RE2 pattern the whole raw query must match, for
	// values that change on every run (timestamps).
	QueryPattern string            `json:"query_pattern,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	// HeaderList asserts per-name ordered header occurrences using the DSL's
	// multi-header representation, distinguishing multiple separate header lines
	// from a single comma-joined header value.
	HeaderList dsl.HeaderList `json:"header_list,omitempty"`
	// HeaderOccurrences asserts the exact slice of wire header occurrences per
	// header name in order, distinguishing separate occurrences from a single
	// comma-joined value.
	HeaderOccurrences map[string][]string `json:"header_occurrences,omitempty"`
	AbsentHeaders     []string            `json:"absent_headers,omitempty"`
	// HeaderContains asserts that a header's joined values contain a substring.
	HeaderContains map[string]string `json:"header_contains,omitempty"`
	// HeaderCounts asserts how many comma- or semicolon-separated values a
	// header carries for a given name, for example {"Cookie:SESSIONID": 1}.
	HeaderCounts map[string]int `json:"header_counts,omitempty"`
	Body         *string        `json:"body,omitempty"`
	BodyBase64   *string        `json:"body_base64,omitempty"`
	BodyContains []string       `json:"body_contains,omitempty"`
	// DecodedBody is the body once the request's Content-Encoding (gzip or
	// deflate) is undone, for a body sent compressed.
	DecodedBody *string `json:"decoded_body,omitempty"`
	// Multipart compares a multipart body part by part. The boundary is not
	// compared: it is generated per sender and carries no meaning.
	Multipart []ExpectedPart `json:"multipart,omitempty"`
	// ExtraRepeats allows up to this many further identical requests right
	// after this one, for a background conversation whose last poll may
	// already be on the wire when the script stops it.
	ExtraRepeats int `json:"extra_repeats,omitempty"`
	// Messages are the WebSocket messages the client must send over this
	// handshake's connection, in order.
	Messages []string `json:"messages,omitempty"`
}

// ExpectedPart is one multipart part, in order.
type ExpectedPart struct {
	Name        string `json:"name"`
	FileName    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
}

// Response is a scripted fixture response. Responses for the same method and
// path are served in order; the last one repeats once the list is used up.
type Response struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// HeaderList emits multiple response headers in order via Header().Add,
	// allowing multiple distinct Set-Cookie or duplicate header occurrences.
	HeaderList dsl.HeaderList `json:"header_list,omitempty"`
	// HeaderOccurrences emits multiple response headers per name in order via
	// Header().Add.
	HeaderOccurrences map[string][]string `json:"header_occurrences,omitempty"`
	Body              string              `json:"body,omitempty"`
	// Barrier names an optional deterministic rendezvous barrier on the server.
	// Concurrent requests naming the same barrier wait until BarrierCount
	// requests have arrived before any completes, proving overlap without
	// wall-clock sleeps.
	Barrier          string `json:"barrier,omitempty"`
	BarrierCount     int    `json:"barrier_count,omitempty"`
	BarrierTimeoutMs int    `json:"barrier_timeout_ms,omitempty"`
	// WebSocket upgrades the request and answers each message.
	WebSocket *WebSocketBehavior `json:"websocket,omitempty"`
	// NTLM requires an NTLM handshake naming User before the response is
	// served.
	NTLM *NTLMBehavior `json:"ntlm,omitempty"`
}

// NTLMBehavior scripts an NTLM-protected fixture endpoint.
type NTLMBehavior struct {
	User string `json:"user"`
}

// WebSocketBehavior scripts a fixture WebSocket endpoint: each received
// message is answered with Reply, or with ReplyPrefix followed by the
// message.
type WebSocketBehavior struct {
	ReplyPrefix string `json:"reply_prefix,omitempty"`
	Reply       string `json:"reply,omitempty"`
}

// Recorded is one request the fixture server received.
type Recorded struct {
	// Seq is the 1-based arrival order of this request at the server.
	Seq int
	// ArrivalSeq is the server's monotonic event counter when the request
	// arrived (before any barrier or response write).
	ArrivalSeq int
	// CompletionSeq is the server's monotonic event counter when the handler
	// finished serving the request. Request A strictly completed before
	// Request B arrived iff A.CompletionSeq < B.ArrivalSeq; A and B overlapped
	// in flight iff max(A.ArrivalSeq, B.ArrivalSeq) < min(A.CompletionSeq, B.CompletionSeq).
	CompletionSeq int
	// InFlightAtArrival is the number of active in-flight requests at the
	// server when this request arrived (including this request).
	InFlightAtArrival int
	Method            string
	Path              string
	Query             string
	Header            http.Header
	Body              []byte
	// Messages are the WebSocket messages received over this request's
	// connection.
	Messages []string
}

// LoadExpectation reads dir/expected.json and, when dir/replay.har exists,
// replaces the hand-written requests with the capture's entries.
func LoadExpectation(dir string) (*Expectation, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		return nil, err
	}
	var exp Expectation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&exp); err != nil {
		return nil, fmt.Errorf("%s/expected.json: %w", filepath.Base(dir), err)
	}
	switch exp.Outcome {
	case OutcomeReplay, OutcomeGated, OutcomeReview:
	default:
		return nil, fmt.Errorf("%s/expected.json: outcome must be %q, %q or %q", filepath.Base(dir), OutcomeReplay, OutcomeGated, OutcomeReview)
	}
	if (exp.Outcome == OutcomeGated || exp.Outcome == OutcomeReview) && len(exp.GateReasonCodes) == 0 {
		return nil, fmt.Errorf("%s/expected.json: a %s case must name its gate reason codes", filepath.Base(dir), exp.Outcome)
	}
	if strings.TrimSpace(exp.Source) == "" {
		return nil, fmt.Errorf("%s/expected.json: source is required", filepath.Base(dir))
	}
	harPath := filepath.Join(dir, "replay.har")
	if _, err := os.Stat(harPath); err == nil {
		requests, err := ExpectedFromHAR(harPath)
		if err != nil {
			return nil, err
		}
		exp.Requests = requests
	}
	return &exp, nil
}

// ExpectedFromHAR turns a LoadRunner replay capture into expected requests.
// Static resources are skipped because their download policy is a runtime
// setting the corpus does not pin.
func ExpectedFromHAR(path string) ([]ExpectedRequest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var har struct {
		Log struct {
			Entries []struct {
				ResourceType string `json:"_resourceType"`
				Request      struct {
					Method  string `json:"method"`
					URL     string `json:"url"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
					PostData *struct {
						Text     string `json:"text"`
						Encoding string `json:"encoding"`
					} `json:"postData"`
				} `json:"request"`
				Response struct {
					Content struct {
						MimeType string `json:"mimeType"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(raw, &har); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make([]ExpectedRequest, 0, len(har.Log.Entries))
	for _, entry := range har.Log.Entries {
		if isStaticResource(entry.ResourceType, entry.Response.Content.MimeType) {
			continue
		}
		parsed, err := url.Parse(entry.Request.URL)
		if err != nil {
			return nil, fmt.Errorf("%s: entry URL %q: %w", path, entry.Request.URL, err)
		}
		query := parsed.RawQuery
		req := ExpectedRequest{Method: strings.ToUpper(entry.Request.Method), Path: parsed.EscapedPath(), Query: &query}
		if entry.Request.PostData != nil {
			if strings.EqualFold(entry.Request.PostData.Encoding, "base64") {
				body := entry.Request.PostData.Text
				req.BodyBase64 = &body
			} else {
				body := entry.Request.PostData.Text
				req.Body = &body
			}
		}
		for _, header := range entry.Request.Headers {
			if strings.EqualFold(header.Name, "Referer") {
				if req.Headers == nil {
					req.Headers = map[string]string{}
				}
				req.Headers["Referer"] = header.Value
			}
		}
		out = append(out, req)
	}
	return out, nil
}

func isStaticResource(resourceType, mimeType string) bool {
	switch strings.ToLower(resourceType) {
	case "image", "stylesheet", "script", "font", "media":
		return true
	}
	mime := strings.ToLower(mimeType)
	return strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "font/") || mime == "text/css" ||
		strings.Contains(mime, "javascript")
}

// Server is a fixture HTTP server that records requests and serves scripted
// responses.
type Server struct {
	*httptest.Server
	mu          sync.Mutex
	recorded    []Recorded
	responses   map[string][]Response
	served      map[string]int
	webSockets  []<-chan struct{}
	eventSeq    int
	inFlight    int
	maxInFlight int
	barriers    map[string]*concurrencyBarrier
}

type concurrencyBarrier struct {
	count   int
	timeout time.Duration
	waiting int
	ch      chan struct{}
}

// NewServer starts a fixture server for the given scripted responses.
func NewServer(responses []Response) *Server {
	s := &Server{
		responses: map[string][]Response{},
		served:    map[string]int{},
		barriers:  map[string]*concurrencyBarrier{},
	}
	for _, response := range responses {
		key := strings.ToUpper(response.Method) + " " + response.Path
		s.responses[key] = append(s.responses[key], response)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// SetBarrier configures a named rendezvous barrier so count concurrent requests
// wait until all count have arrived before any completes, and resets cleanly
// for subsequent waves.
func (s *Server) SetBarrier(name string, count int, timeout time.Duration) {
	if count <= 0 {
		count = 2
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.barriers[name] = &concurrencyBarrier{
		count:   count,
		timeout: timeout,
		ch:      make(chan struct{}),
	}
}

// MaxInFlight returns the maximum number of requests simultaneously in flight
// at the server so far.
func (s *Server) MaxInFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxInFlight
}

func (s *Server) waitBarrier(ctx context.Context, name string, count, timeoutMs int) {
	if name == "" {
		return
	}
	s.mu.Lock()
	b := s.barriers[name]
	if b == nil {
		c := count
		if c <= 0 {
			c = 2
		}
		timeout := 2 * time.Second
		if timeoutMs > 0 {
			timeout = time.Duration(timeoutMs) * time.Millisecond
		}
		b = &concurrencyBarrier{
			count:   c,
			timeout: timeout,
			ch:      make(chan struct{}),
		}
		s.barriers[name] = b
	}
	ch := b.ch
	b.waiting++
	if b.waiting >= b.count {
		close(ch)
		b.waiting = 0
		b.ch = make(chan struct{})
		s.mu.Unlock()
		return
	}
	timeout := b.timeout
	s.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-ctx.Done():
		s.mu.Lock()
		if b.ch == ch && b.waiting > 0 {
			b.waiting--
		}
		s.mu.Unlock()
	case <-timer.C:
		s.mu.Lock()
		if b.ch == ch && b.waiting > 0 {
			b.waiting--
		}
		s.mu.Unlock()
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	s.mu.Lock()
	s.eventSeq++
	arrivalSeq := s.eventSeq
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	inFlightAtArrival := s.inFlight
	seq := len(s.recorded) + 1
	s.recorded = append(s.recorded, Recorded{
		Seq:               seq,
		ArrivalSeq:        arrivalSeq,
		InFlightAtArrival: inFlightAtArrival,
		Method:            r.Method,
		Path:              r.URL.EscapedPath(),
		Query:             r.URL.RawQuery,
		Header:            r.Header.Clone(),
		Body:              body,
	})
	recordedIndex := len(s.recorded) - 1
	key := r.Method + " " + r.URL.Path
	var response *Response
	if list := s.responses[key]; len(list) > 0 {
		index := s.served[key]
		if index >= len(list) {
			index = len(list) - 1
		}
		chosen := list[index]
		// An NTLM handshake leg answers with a challenge and does not use
		// up the scripted response.
		if chosen.NTLM == nil || ntlmAuthenticated(r, chosen.NTLM.User) {
			s.served[key]++
		}
		response = &chosen
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.eventSeq++
		s.recorded[recordedIndex].CompletionSeq = s.eventSeq
		s.inFlight--
		s.mu.Unlock()
	}()
	if response != nil && response.NTLM != nil && !ntlmAuthenticated(r, response.NTLM.User) {
		if kind, _ := ntlmMessage(r.Header.Get("Authorization")); kind == 1 {
			w.Header().Set("WWW-Authenticate", "NTLM "+ntlmChallenge())
		} else {
			w.Header().Set("WWW-Authenticate", "NTLM")
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if response == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if response.Barrier != "" {
		s.waitBarrier(r.Context(), response.Barrier, response.BarrierCount, response.BarrierTimeoutMs)
	}
	if response.WebSocket != nil {
		s.serveWebSocket(w, r, recordedIndex, *response.WebSocket)
		return
	}
	headerNames := make([]string, 0, len(response.Headers))
	for name := range response.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		w.Header().Set(name, s.expand(response.Headers[name]))
	}
	for _, h := range response.HeaderList {
		w.Header().Add(h.Name, s.expand(h.Value))
	}
	occNames := make([]string, 0, len(response.HeaderOccurrences))
	for name := range response.HeaderOccurrences {
		occNames = append(occNames, name)
	}
	sort.Strings(occNames)
	for _, name := range occNames {
		for _, value := range response.HeaderOccurrences[name] {
			w.Header().Add(name, s.expand(value))
		}
	}
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, s.expand(response.Body))
}

// serveWebSocket upgrades the request, records each message the client
// sends, and answers it.
func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request, recordedIndex int, behavior WebSocketBehavior) {
	done := make(chan struct{})
	s.mu.Lock()
	s.webSockets = append(s.webSockets, done)
	s.mu.Unlock()
	defer close(done)
	conn, err := (&gorillawebsocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		kind, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.recorded[recordedIndex].Messages = append(s.recorded[recordedIndex].Messages, string(message))
		s.mu.Unlock()
		reply := append([]byte(behavior.ReplyPrefix), message...)
		if behavior.Reply != "" {
			reply = []byte(behavior.Reply)
		}
		if err := conn.WriteMessage(kind, reply); err != nil {
			return
		}
	}
}

// ntlmAuthenticated reports whether a request carries an NTLM Type 3
// message naming user.
func ntlmAuthenticated(r *http.Request, user string) bool {
	kind, raw := ntlmMessage(r.Header.Get("Authorization"))
	if kind != 3 {
		return false
	}
	var name []byte
	for _, ch := range user {
		name = append(name, byte(ch), byte(ch>>8))
	}
	return bytes.Contains(raw, name)
}

func ntlmMessage(header string) (uint32, []byte) {
	token, ok := strings.CutPrefix(header, "NTLM ")
	if !ok {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(raw) < 12 || string(raw[:8]) != "NTLMSSP\x00" {
		return 0, nil
	}
	return binary.LittleEndian.Uint32(raw[8:12]), raw
}

// ntlmChallenge is a minimal NTLM Type 2 message: a server challenge and an
// empty target-info list.
func ntlmChallenge() string {
	var message bytes.Buffer
	message.WriteString("NTLMSSP\x00")
	_ = binary.Write(&message, binary.LittleEndian, uint32(2))
	_ = binary.Write(&message, binary.LittleEndian, [2]uint16{0, 0})
	_ = binary.Write(&message, binary.LittleEndian, uint32(48))
	flags := uint32(1<<0 | 1<<2 | 1<<9 | 1<<19 | 1<<23 | 1<<29 | 1<<31)
	_ = binary.Write(&message, binary.LittleEndian, flags)
	message.Write([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	message.Write(make([]byte, 8))
	_ = binary.Write(&message, binary.LittleEndian, [2]uint16{4, 4})
	_ = binary.Write(&message, binary.LittleEndian, uint32(48))
	message.Write([]byte{0, 0, 0, 0})
	return base64.StdEncoding.EncodeToString(message.Bytes())
}

// Recorded returns a copy of the requests received so far.
func (s *Server) Recorded() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	recorded := append([]Recorded(nil), s.recorded...)
	for i := range recorded {
		recorded[i].Messages = append([]string(nil), recorded[i].Messages...)
	}
	return recorded
}

// expand replaces the placeholder origins with the server's origins.
func (s *Server) expand(value string) string {
	value = strings.ReplaceAll(value, WebSocketOrigin, "ws"+strings.TrimPrefix(s.URL, "http"))
	value = strings.ReplaceAll(value, AliasOrigin, s.AliasURL())
	return strings.ReplaceAll(value, Origin, s.URL)
}

// AliasURL is the server's origin under the "localhost" host name.
func (s *Server) AliasURL() string {
	return strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
}

// Expand replaces the placeholder origins with the server's origins.
func (s *Server) Expand(value string) string { return s.expand(value) }

// ReplayOnce runs one iteration of script through the engine's replay path,
// the same path the script debugger uses.
func ReplayOnce(ctx context.Context, script *dsl.Script, variables, secrets map[string]string) (*engine.ReplayReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	seed := int64(1)
	options := engine.ReplayOptions{
		GroupName:       "parity",
		IgnoreThinkTime: true,
		Seed:            &seed,
		Variables:       variables,
	}
	if len(secrets) > 0 {
		options.SecretResolver = engine.SecretResolverFunc(func(name string) (string, bool) {
			value, ok := secrets[name]
			return value, ok
		})
	}
	return engine.ReplayScript(ctx, script, options)
}

// waitForWebSockets joins the fixture readers after replay closes its sockets.
// A successful client write need not yet be visible in the server's ledger.
func (s *Server) waitForWebSockets(ctx context.Context) error {
	s.mu.Lock()
	pending := append([]<-chan struct{}(nil), s.webSockets...)
	s.mu.Unlock()
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Compare checks recorded requests against the expectation and returns one
// line per mismatch. Requests must arrive in the expected order and no extra
// requests are allowed.
func (s *Server) Compare(expected []ExpectedRequest) []string {
	var problems []string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.waitForWebSockets(ctx); err != nil {
		problems = append(problems, "WebSocket fixture did not finish recording after replay: "+err.Error())
	}
	recorded := s.Recorded()
	next := 0
	for i, want := range expected {
		if next >= len(recorded) {
			problems = append(problems, fmt.Sprintf("request %d: expected %s %s, but only %d request(s) arrived", i+1, want.Method, want.Path, len(recorded)))
			continue
		}
		problems = append(problems, s.compareOne(next+1, want, recorded[next])...)
		next++
		for repeat := 0; repeat < want.ExtraRepeats && next < len(recorded) && len(s.compareOne(next+1, want, recorded[next])) == 0; repeat++ {
			next++
		}
	}
	for i := next; i < len(recorded); i++ {
		problems = append(problems, fmt.Sprintf("request %d: unexpected extra request %s %s?%s", i+1, recorded[i].Method, recorded[i].Path, recorded[i].Query))
	}
	return problems
}

func (s *Server) compareOne(n int, want ExpectedRequest, got Recorded) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("request %d (%s %s): ", n, want.Method, want.Path)+fmt.Sprintf(format, args...))
	}
	if !strings.EqualFold(want.Method, got.Method) {
		add("method = %s", got.Method)
	}
	if want.Path != got.Path {
		add("path = %s", got.Path)
	}
	if want.Messages != nil && !reflect.DeepEqual(want.Messages, got.Messages) {
		add("WebSocket messages = %q, want %q", got.Messages, want.Messages)
	}
	if want.Query != nil && s.expand(*want.Query) != got.Query {
		add("query = %q, want %q", got.Query, s.expand(*want.Query))
	}
	if want.QueryPattern != "" {
		if pattern, err := regexp.Compile("^(?:" + want.QueryPattern + ")$"); err != nil {
			add("query_pattern %q is not valid RE2: %v", want.QueryPattern, err)
		} else if !pattern.MatchString(got.Query) {
			add("query = %q, want it to match %q", got.Query, want.QueryPattern)
		}
	}
	names := make([]string, 0, len(want.Headers))
	for name := range want.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		wantValue := s.expand(want.Headers[name])
		gotValues := got.Header.Values(name)
		if len(gotValues) == 0 {
			add("header %s missing, want %q", name, wantValue)
			continue
		}
		if strings.Join(gotValues, ", ") != wantValue {
			add("header %s = %q, want %q", name, strings.Join(gotValues, ", "), wantValue)
		}
	}
	checkOccurrences := func(name string, wantRaw []string) {
		wantValues := make([]string, len(wantRaw))
		for i, v := range wantRaw {
			wantValues[i] = s.expand(v)
		}
		gotValues := got.Header.Values(name)
		if !reflect.DeepEqual(gotValues, wantValues) {
			add("header %s occurrences = %q (%d), want %q (%d)", name, gotValues, len(gotValues), wantValues, len(wantValues))
		}
	}
	if len(want.HeaderList) > 0 {
		var order []string
		grouped := map[string][]string{}
		for _, h := range want.HeaderList {
			canon := http.CanonicalHeaderKey(h.Name)
			if _, seen := grouped[canon]; !seen {
				order = append(order, canon)
			}
			grouped[canon] = append(grouped[canon], h.Value)
		}
		for _, canon := range order {
			checkOccurrences(canon, grouped[canon])
		}
	}
	occNames := make([]string, 0, len(want.HeaderOccurrences))
	for name := range want.HeaderOccurrences {
		occNames = append(occNames, name)
	}
	sort.Strings(occNames)
	for _, name := range occNames {
		checkOccurrences(name, want.HeaderOccurrences[name])
	}
	containNames := make([]string, 0, len(want.HeaderContains))
	for name := range want.HeaderContains {
		containNames = append(containNames, name)
	}
	sort.Strings(containNames)
	for _, name := range containNames {
		fragment := s.expand(want.HeaderContains[name])
		if joined := strings.Join(got.Header.Values(name), ", "); !strings.Contains(joined, fragment) {
			add("header %s = %q, want it to contain %q", name, joined, fragment)
		}
	}
	for _, name := range want.AbsentHeaders {
		if values := got.Header.Values(name); len(values) > 0 {
			add("header %s must be absent, got %q", name, strings.Join(values, ", "))
		}
	}
	countSpecs := make([]string, 0, len(want.HeaderCounts))
	for spec := range want.HeaderCounts {
		countSpecs = append(countSpecs, spec)
	}
	sort.Strings(countSpecs)
	for _, spec := range countSpecs {
		wantCount := want.HeaderCounts[spec]
		header, item, ok := strings.Cut(spec, ":")
		if !ok {
			add("header_counts key %q must be Header:name", spec)
			continue
		}
		count := 0
		for _, value := range got.Header.Values(header) {
			for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' }) {
				name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
				if strings.EqualFold(name, item) {
					count++
				}
			}
		}
		if count != wantCount {
			add("header %s carries %s %d time(s), want %d", header, item, count, wantCount)
		}
	}
	if want.Body != nil && s.expand(*want.Body) != string(got.Body) {
		add("body = %q, want %q", truncate(string(got.Body)), truncate(s.expand(*want.Body)))
	}
	if want.BodyBase64 != nil {
		wantBytes, err := base64.StdEncoding.DecodeString(*want.BodyBase64)
		if err != nil {
			add("body_base64 is not valid base64: %v", err)
		} else if !bytes.Equal(wantBytes, got.Body) {
			add("body bytes = %s, want %s", base64.StdEncoding.EncodeToString(got.Body), *want.BodyBase64)
		}
	}
	for _, fragment := range want.BodyContains {
		if !strings.Contains(string(got.Body), s.expand(fragment)) {
			add("body does not contain %q", s.expand(fragment))
		}
	}
	if want.DecodedBody != nil {
		if decoded, err := decodeRequestBody(got); err != nil {
			add("%v", err)
		} else if s.expand(*want.DecodedBody) != string(decoded) {
			add("decoded body = %q, want %q", truncate(string(decoded)), truncate(s.expand(*want.DecodedBody)))
		}
	}
	if len(want.Multipart) > 0 {
		for _, problem := range compareMultipart(got, want.Multipart, s.expand) {
			add("%s", problem)
		}
	}
	return problems
}

// decodeRequestBody undoes a request's gzip or deflate Content-Encoding.
func decodeRequestBody(got Recorded) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch encoding := got.Header.Get("Content-Encoding"); encoding {
	case "gzip":
		reader, err = gzip.NewReader(bytes.NewReader(got.Body))
	case "deflate":
		reader, err = zlib.NewReader(bytes.NewReader(got.Body))
	default:
		return nil, fmt.Errorf("Content-Encoding = %q, want gzip or deflate for decoded_body", encoding)
	}
	if err != nil {
		return nil, fmt.Errorf("body does not decode as %s: %v", got.Header.Get("Content-Encoding"), err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("body does not decode as %s: %v", got.Header.Get("Content-Encoding"), err)
	}
	return decoded, nil
}

func compareMultipart(got Recorded, want []ExpectedPart, expand func(string) string) []string {
	mediaType, params, err := mime.ParseMediaType(got.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || params["boundary"] == "" {
		return []string{fmt.Sprintf("Content-Type %q is not multipart with a boundary", got.Header.Get("Content-Type"))}
	}
	reader := multipart.NewReader(bytes.NewReader(got.Body), params["boundary"])
	var problems []string
	for index := 0; ; index++ {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			if index != len(want) {
				problems = append(problems, fmt.Sprintf("multipart has %d part(s), want %d", index, len(want)))
			}
			return problems
		}
		if err != nil {
			return append(problems, fmt.Sprintf("multipart part %d is malformed: %v", index+1, err))
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return append(problems, fmt.Sprintf("multipart part %d is malformed: %v", index+1, err))
		}
		if index >= len(want) {
			continue
		}
		expected := want[index]
		if part.FormName() != expected.Name {
			problems = append(problems, fmt.Sprintf("part %d name = %q, want %q", index+1, part.FormName(), expected.Name))
		}
		if part.FileName() != expected.FileName {
			problems = append(problems, fmt.Sprintf("part %d filename = %q, want %q", index+1, part.FileName(), expected.FileName))
		}
		if contentType := part.Header.Get("Content-Type"); contentType != expected.ContentType {
			problems = append(problems, fmt.Sprintf("part %d Content-Type = %q, want %q", index+1, contentType, expected.ContentType))
		}
		if string(data) != expand(expected.Body) {
			problems = append(problems, fmt.Sprintf("part %d body = %q, want %q", index+1, truncate(string(data)), truncate(expand(expected.Body))))
		}
	}
}

func truncate(value string) string {
	if len(value) > 160 {
		return value[:160] + "..."
	}
	return value
}

// GateReasonCodes collects every import-review code in the script: execution
// blockers and promotion review reasons alike.
func GateReasonCodes(script *dsl.Script) []string {
	return collectMetadataCodes(script, "reason_codes", "execution_blockers", "execution_blocker", "review_reasons", "review_gate")
}

// ExecutionBlockerCodes collects the codes that block execution.
func ExecutionBlockerCodes(script *dsl.Script) []string {
	return collectMetadataCodes(script, "reason_codes", "execution_blockers", "execution_blocker")
}

// ReviewReasonCodes collects the promotion review codes (replay allowed).
func ReviewReasonCodes(script *dsl.Script) []string {
	return collectMetadataCodes(script, "review_reasons", "review_gate")
}

// collectMetadataCodes walks the script's JSON form and gathers the string or
// string-list values stored under any of the given metadata keys.
func collectMetadataCodes(script *dsl.Script, keys ...string) []string {
	raw, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	seen := map[string]struct{}{}
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if wanted[key] {
					switch codes := child.(type) {
					case string:
						if codes != "" {
							seen[codes] = struct{}{}
						}
					case []any:
						for _, item := range codes {
							if code, ok := item.(string); ok && code != "" {
								seen[code] = struct{}{}
							}
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(generic)
	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// checkExpectedFailures verifies that exactly the named actions failed, each
// with its expected error fragment.
func checkExpectedFailures(report *engine.ReplayReport, expected map[string]string) []string {
	if report == nil {
		return []string{"replay produced no report"}
	}
	var problems []string
	seen := map[string]bool{}
	for _, event := range report.Events {
		fragment, wantFailure := expected[event.ActionName]
		if !wantFailure {
			if !event.Success {
				problems = append(problems, fmt.Sprintf("action %q failed: %s", event.ActionName, event.ErrorMessage))
			}
			continue
		}
		seen[event.ActionName] = true
		switch {
		case event.Success:
			problems = append(problems, fmt.Sprintf("action %q passed, but its check must fail on this page", event.ActionName))
		case !strings.Contains(event.ErrorMessage, fragment):
			problems = append(problems, fmt.Sprintf("action %q failed with %q, want it to mention %q", event.ActionName, event.ErrorMessage, fragment))
		}
	}
	for name := range expected {
		if !seen[name] {
			problems = append(problems, fmt.Sprintf("action %q did not run", name))
		}
	}
	sort.Strings(problems)
	return problems
}

// CheckOutcome verifies a converted script against the expectation: gated
// cases must carry their reason codes and be refused by replay; replay cases
// must run and put exactly the expected requests on the wire. It returns one
// line per problem.
func CheckOutcome(ctx context.Context, server *Server, exp *Expectation, script *dsl.Script, warnings []string) []string {
	var problems []string
	joined := strings.Join(warnings, "\n")
	for _, fragment := range exp.WarningsContain {
		if !strings.Contains(joined, fragment) {
			problems = append(problems, fmt.Sprintf("warnings do not mention %q; got:\n%s", fragment, joined))
		}
	}
	codes := GateReasonCodes(script)
	contains := func(list []string, code string) bool {
		for _, item := range list {
			if item == code {
				return true
			}
		}
		return false
	}
	switch exp.Outcome {
	case OutcomeGated:
		blockers := ExecutionBlockerCodes(script)
		for _, code := range exp.GateReasonCodes {
			if !contains(blockers, code) {
				problems = append(problems, fmt.Sprintf("execution gate %q missing; script carries %v", code, codes))
			}
		}
		if _, err := ReplayOnce(ctx, script, exp.Variables, exp.Secrets); err == nil {
			problems = append(problems, "replay ran, but a gated import must be refused")
		}
		if recorded := server.Recorded(); len(recorded) > 0 {
			problems = append(problems, fmt.Sprintf("a gated import sent %d request(s)", len(recorded)))
		}
	case OutcomeReview:
		reviews := ReviewReasonCodes(script)
		for _, code := range exp.GateReasonCodes {
			if !contains(reviews, code) {
				problems = append(problems, fmt.Sprintf("review gate %q missing; script carries %v", code, codes))
			}
		}
		if _, err := ReplayOnce(ctx, script, exp.Variables, exp.Secrets); err != nil {
			problems = append(problems, fmt.Sprintf("a review-gated import must still replay for debugging, but replay refused: %v", err))
		}
		if len(exp.Requests) > 0 {
			problems = append(problems, server.Compare(exp.Requests)...)
		}
		if len(exp.Dependencies) > 0 {
			problems = append(problems, server.CheckDependencies(exp.Dependencies...)...)
		}
	case OutcomeReplay:
		if len(exp.ExpectFailures) > 0 {
			continueOnError := true
			script.Config.ErrorHandling.ContinueOnError = &continueOnError
		}
		report, err := ReplayOnce(ctx, script, exp.Variables, exp.Secrets)
		if err != nil {
			problems = append(problems, fmt.Sprintf("replay refused or failed: %v (gate reason codes %v)", err, codes))
			return problems
		}
		if len(exp.ExpectFailures) > 0 {
			problems = append(problems, checkExpectedFailures(report, exp.ExpectFailures)...)
		} else if report != nil && !report.Success {
			detail := report.Error
			if summary := report.DiagnosisSummary; summary != nil {
				detail = fmt.Sprintf("%d failure(s); first: %s: %s", summary.FailureCount, summary.FirstFailureAction, summary.FirstFailureCause)
			}
			problems = append(problems, "replay did not succeed: "+detail)
		}
		problems = append(problems, server.Compare(exp.Requests)...)
		if len(exp.Dependencies) > 0 {
			problems = append(problems, server.CheckDependencies(exp.Dependencies...)...)
		}
	}
	return problems
}

// CheckDependencies verifies request ordering and concurrency overlap rules
// against the server's recorded request ledger.
func (s *Server) CheckDependencies(rules ...DependencyRule) []string {
	var problems []string
	recorded := s.Recorded()
	maxInFlight := s.MaxInFlight()
	matchAll := func(spec string) []Recorded {
		var out []Recorded
		for _, rec := range recorded {
			if matchRequestSpec(rec, spec) {
				out = append(out, rec)
			}
		}
		return out
	}
	for _, rule := range rules {
		if rule.MinMaxInFlight > 0 && maxInFlight < rule.MinMaxInFlight {
			problems = append(problems, fmt.Sprintf("max in-flight requests = %d, want >= %d", maxInFlight, rule.MinMaxInFlight))
		}
		if rule.Prerequisite != "" && len(rule.Dependents) > 0 {
			prereqs := matchAll(rule.Prerequisite)
			if len(prereqs) == 0 {
				problems = append(problems, fmt.Sprintf("dependency prerequisite %q was never recorded", rule.Prerequisite))
			} else {
				for _, depSpec := range rule.Dependents {
					deps := matchAll(depSpec)
					if len(deps) == 0 {
						problems = append(problems, fmt.Sprintf("dependency dependent %q was never recorded", depSpec))
						continue
					}
					if len(prereqs) == len(deps) {
						for i := range deps {
							if prereqs[i].CompletionSeq == 0 || prereqs[i].CompletionSeq >= deps[i].ArrivalSeq {
								problems = append(problems, fmt.Sprintf(
									"dependency violation (wave %d): prerequisite %s (seq %d, completed @%d) did not complete before dependent %s (seq %d, arrived @%d)",
									i+1, rule.Prerequisite, prereqs[i].Seq, prereqs[i].CompletionSeq, depSpec, deps[i].Seq, deps[i].ArrivalSeq,
								))
							}
						}
					} else {
						for _, dep := range deps {
							// Require the latest prerequisite that arrived before this dependent
							// to have completed before the dependent arrived, catching later-wave
							// dependents that overtake their wave's prerequisite.
							var latestPrereq *Recorded
							for i := range prereqs {
								if prereqs[i].ArrivalSeq < dep.ArrivalSeq {
									if latestPrereq == nil || prereqs[i].ArrivalSeq > latestPrereq.ArrivalSeq {
										latestPrereq = &prereqs[i]
									}
								}
							}
							if latestPrereq == nil {
								problems = append(problems, fmt.Sprintf(
									"dependency violation: no prerequisite %s arrived before dependent %s (seq %d, arrived @%d)",
									rule.Prerequisite, depSpec, dep.Seq, dep.ArrivalSeq,
								))
								continue
							}
							if latestPrereq.CompletionSeq == 0 || latestPrereq.CompletionSeq >= dep.ArrivalSeq {
								problems = append(problems, fmt.Sprintf(
									"dependency violation: prerequisite %s (seq %d, completed @%d) did not complete before dependent %s (seq %d, arrived @%d)",
									rule.Prerequisite, latestPrereq.Seq, latestPrereq.CompletionSeq, depSpec, dep.Seq, dep.ArrivalSeq,
								))
							}
						}
					}
				}
			}
		}
		if len(rule.OverlapGroup) > 0 {
			if len(rule.OverlapGroup) < 2 {
				problems = append(problems, fmt.Sprintf("overlap_group %v must list at least 2 request specs", rule.OverlapGroup))
				continue
			}
			var specsInOrder []string
			perWaveNeed := map[string]int{}
			for _, spec := range rule.OverlapGroup {
				if perWaveNeed[spec] == 0 {
					specsInOrder = append(specsInOrder, spec)
				}
				perWaveNeed[spec]++
			}
			matchesBySpec := map[string][]Recorded{}
			missing := false
			waves := -1
			for _, spec := range specsInOrder {
				matches := matchAll(spec)
				need := perWaveNeed[spec]
				if len(matches) < need {
					problems = append(problems, fmt.Sprintf("concurrency overlap spec %q recorded %d time(s), want at least %d", spec, len(matches), need))
					missing = true
					continue
				}
				matchesBySpec[spec] = matches
				w := len(matches) / need
				if waves == -1 || w < waves {
					waves = w
				}
			}
			if missing || waves <= 0 {
				continue
			}
			for w := 0; w < waves; w++ {
				var group []Recorded
				for _, spec := range specsInOrder {
					need := perWaveNeed[spec]
					start := w * need
					group = append(group, matchesBySpec[spec][start:start+need]...)
				}
				maxArrival := group[0].ArrivalSeq
				minCompletion := group[0].CompletionSeq
				waveMaxInFlight := group[0].InFlightAtArrival
				for _, rec := range group[1:] {
					if rec.ArrivalSeq > maxArrival {
						maxArrival = rec.ArrivalSeq
					}
					if rec.CompletionSeq == 0 || (minCompletion > 0 && rec.CompletionSeq < minCompletion) {
						minCompletion = rec.CompletionSeq
					}
					if rec.InFlightAtArrival > waveMaxInFlight {
						waveMaxInFlight = rec.InFlightAtArrival
					}
				}
				if minCompletion == 0 || maxArrival >= minCompletion {
					problems = append(problems, fmt.Sprintf(
						"concurrency violation: overlap_group %v (wave %d) did not overlap in flight (max arrival @%d >= min completion @%d)",
						rule.OverlapGroup, w+1, maxArrival, minCompletion,
					))
				}
				if rule.MinMaxInFlight > 0 && waveMaxInFlight < rule.MinMaxInFlight {
					problems = append(problems, fmt.Sprintf(
						"concurrency violation: overlap_group %v (wave %d) max in-flight at arrival = %d, want >= %d",
						rule.OverlapGroup, w+1, waveMaxInFlight, rule.MinMaxInFlight,
					))
				}
			}
		}
	}
	return problems
}

func matchRequestSpec(rec Recorded, spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	wantMethod := ""
	wantTarget := spec
	if method, rest, ok := strings.Cut(spec, " "); ok {
		wantMethod = strings.TrimSpace(method)
		wantTarget = strings.TrimSpace(rest)
	}
	if wantMethod != "" && !strings.EqualFold(rec.Method, wantMethod) {
		return false
	}
	if wantPath, wantQuery, hasQuery := strings.Cut(wantTarget, "?"); hasQuery {
		return rec.Path == wantPath && rec.Query == wantQuery
	}
	return rec.Path == wantTarget
}

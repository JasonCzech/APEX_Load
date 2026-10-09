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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// Origin is the placeholder origin corpus sources and expectations use. Tests
// replace it with the fixture server's origin before converting.
const Origin = "http://parity.test"

// Outcome values for Expectation.Outcome.
const (
	// OutcomeReplay means the converted script must be executable and must put
	// exactly the expected requests on the wire.
	OutcomeReplay = "replay"
	// OutcomeGated means the construct is not reproduced yet, so the import
	// must carry an execution gate with the listed reason codes and replay must
	// refuse to run. A silent conversion is the failure this outcome catches.
	OutcomeGated = "gated"
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
}

// ExpectedRequest describes one request the fixture server must receive.
// Only the fields that are set are compared.
type ExpectedRequest struct {
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Query         *string           `json:"query,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	AbsentHeaders []string          `json:"absent_headers,omitempty"`
	// HeaderCounts asserts how many comma- or semicolon-separated values a
	// header carries for a given name, for example {"Cookie:SESSIONID": 1}.
	HeaderCounts map[string]int `json:"header_counts,omitempty"`
	Body         *string        `json:"body,omitempty"`
	BodyBase64   *string        `json:"body_base64,omitempty"`
	BodyContains []string       `json:"body_contains,omitempty"`
}

// Response is a scripted fixture response. Responses for the same method and
// path are served in order; the last one repeats once the list is used up.
type Response struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// Recorded is one request the fixture server received.
type Recorded struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
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
	case OutcomeReplay, OutcomeGated:
	default:
		return nil, fmt.Errorf("%s/expected.json: outcome must be %q or %q", filepath.Base(dir), OutcomeReplay, OutcomeGated)
	}
	if exp.Outcome == OutcomeGated && len(exp.GateReasonCodes) == 0 {
		return nil, fmt.Errorf("%s/expected.json: a gated case must name its gate reason codes", filepath.Base(dir))
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
	mu        sync.Mutex
	recorded  []Recorded
	responses map[string][]Response
	served    map[string]int
}

// NewServer starts a fixture server for the given scripted responses.
func NewServer(responses []Response) *Server {
	s := &Server{responses: map[string][]Response{}, served: map[string]int{}}
	for _, response := range responses {
		key := strings.ToUpper(response.Method) + " " + response.Path
		s.responses[key] = append(s.responses[key], response)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	s.mu.Lock()
	s.recorded = append(s.recorded, Recorded{
		Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body,
	})
	key := r.Method + " " + r.URL.Path
	var response *Response
	if list := s.responses[key]; len(list) > 0 {
		index := s.served[key]
		if index >= len(list) {
			index = len(list) - 1
		}
		s.served[key]++
		chosen := list[index]
		response = &chosen
	}
	s.mu.Unlock()
	if response == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	for name, value := range response.Headers {
		w.Header().Set(name, s.expand(value))
	}
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, s.expand(response.Body))
}

// Recorded returns a copy of the requests received so far.
func (s *Server) Recorded() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.recorded...)
}

// Expand replaces the placeholder origin with the server's origin.
func (s *Server) expand(value string) string { return strings.ReplaceAll(value, Origin, s.URL) }

// Expand replaces the placeholder origin with the server's origin.
func (s *Server) Expand(value string) string { return s.expand(value) }

// ReplayOnce runs one iteration of script through the engine's replay path,
// the same path the script debugger uses.
func ReplayOnce(ctx context.Context, script *dsl.Script, variables map[string]string) (*engine.ReplayReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	seed := int64(1)
	return engine.ReplayScript(ctx, script, engine.ReplayOptions{
		GroupName:       "parity",
		IgnoreThinkTime: true,
		Seed:            &seed,
		Variables:       variables,
	})
}

// Compare checks recorded requests against the expectation and returns one
// line per mismatch. Requests must arrive in the expected order and no extra
// requests are allowed.
func (s *Server) Compare(expected []ExpectedRequest) []string {
	recorded := s.Recorded()
	var problems []string
	for i, want := range expected {
		if i >= len(recorded) {
			problems = append(problems, fmt.Sprintf("request %d: expected %s %s, but only %d request(s) arrived", i+1, want.Method, want.Path, len(recorded)))
			continue
		}
		problems = append(problems, s.compareOne(i+1, want, recorded[i])...)
	}
	for i := len(expected); i < len(recorded); i++ {
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
	if want.Query != nil && s.expand(*want.Query) != got.Query {
		add("query = %q, want %q", got.Query, s.expand(*want.Query))
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
	for _, name := range want.AbsentHeaders {
		if values := got.Header.Values(name); len(values) > 0 {
			add("header %s must be absent, got %q", name, strings.Join(values, ", "))
		}
	}
	for spec, wantCount := range want.HeaderCounts {
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
	return problems
}

func truncate(value string) string {
	if len(value) > 160 {
		return value[:160] + "..."
	}
	return value
}

// GateReasonCodes collects every import-review reason code in the script.
func GateReasonCodes(script *dsl.Script) []string {
	raw, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "reason_codes" {
					if list, ok := child.([]any); ok {
						for _, item := range list {
							if code, ok := item.(string); ok {
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
	has := map[string]bool{}
	for _, code := range codes {
		has[code] = true
	}
	switch exp.Outcome {
	case OutcomeGated:
		for _, code := range exp.GateReasonCodes {
			if !has[code] {
				problems = append(problems, fmt.Sprintf("gate reason code %q missing; script carries %v", code, codes))
			}
		}
		if _, err := ReplayOnce(ctx, script, exp.Variables); err == nil {
			problems = append(problems, "replay ran, but a gated import must be refused")
		}
		if recorded := server.Recorded(); len(recorded) > 0 {
			problems = append(problems, fmt.Sprintf("a gated import sent %d request(s)", len(recorded)))
		}
	case OutcomeReplay:
		report, err := ReplayOnce(ctx, script, exp.Variables)
		if err != nil {
			problems = append(problems, fmt.Sprintf("replay refused or failed: %v (gate reason codes %v)", err, codes))
			return problems
		}
		if report != nil && !report.Success {
			detail := report.Error
			if summary := report.DiagnosisSummary; summary != nil {
				detail = fmt.Sprintf("%d failure(s); first: %s: %s", summary.FailureCount, summary.FirstFailureAction, summary.FirstFailureCause)
			}
			problems = append(problems, "replay did not succeed: "+detail)
		}
		problems = append(problems, server.Compare(exp.Requests)...)
	}
	return problems
}

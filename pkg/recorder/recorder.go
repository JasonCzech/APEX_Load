package recorder

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const defaultBodyCaptureLimitBytes = 512 * 1024

const (
	defaultRecorderCADir  = "artifacts/recorder"
	defaultRecorderCACert = "ca-cert.pem"
	defaultRecorderCAKey  = "ca-key.pem"
)

type RecordedRequest struct {
	Seq                   int                 `json:"seq"`
	Timestamp             time.Time           `json:"timestamp"`
	Method                string              `json:"method"`
	URL                   string              `json:"url"`
	Scheme                string              `json:"scheme,omitempty"`
	Path                  string              `json:"path"`
	QueryString           string              `json:"query_string,omitempty"`
	Host                  string              `json:"host"`
	Headers               map[string]string   `json:"headers"`
	HeadersMulti          map[string][]string `json:"headers_multi,omitempty"`
	Body                  string              `json:"body,omitempty"`
	BodySizeBytes         int                 `json:"body_size_bytes,omitempty"`
	BodyTruncated         bool                `json:"body_truncated,omitempty"`
	ContentType           string              `json:"content_type,omitempty"`
	StatusCode            int                 `json:"status_code"`
	ResponseTimeMs        float64             `json:"response_time_ms"`
	ResponseBody          string              `json:"response_body,omitempty"`
	ResponseBodySizeBytes int                 `json:"response_body_size_bytes,omitempty"`
	ResponseBodyTruncated bool                `json:"response_body_truncated,omitempty"`
	ResponseHeaders       map[string]string   `json:"response_headers,omitempty"`
	ResponseHeadersMulti  map[string][]string `json:"response_headers_multi,omitempty"`
	ThinkTimeMs           int64               `json:"think_time_ms"`
	IsStatic              bool                `json:"is_static"`
	ResourceType          string              `json:"resource_type,omitempty"`
	StepName              string              `json:"step_name,omitempty"`
	TransactionName       string              `json:"transaction_name,omitempty"`
	Note                  string              `json:"note,omitempty"`
	Excluded              bool                `json:"excluded,omitempty"`
}

type Session struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	ProjectID string            `json:"project_id,omitempty"`
	Status    string            `json:"status"`
	StartedAt time.Time         `json:"started_at"`
	StoppedAt *time.Time        `json:"stopped_at,omitempty"`
	BaseURL   string            `json:"base_url,omitempty"`
	Requests  []RecordedRequest `json:"requests"`
	Filters   SessionFilters    `json:"filters"`

	mu          sync.Mutex
	seq         int
	lastReqTime time.Time
	notify      func(SessionEvent)
}

type SessionFilters struct {
	ExcludeStatic         bool     `json:"exclude_static"`
	ExcludeHosts          []string `json:"exclude_hosts,omitempty"`
	IncludeHosts          []string `json:"include_hosts,omitempty"`
	BodyCaptureLimitBytes int      `json:"body_capture_limit_bytes,omitempty"`
	RedactHeaders         []string `json:"redact_headers,omitempty"`
}

type SessionEvent struct {
	Type      string           `json:"type"`
	SessionID string           `json:"session_id"`
	Request   *RecordedRequest `json:"request,omitempty"`
	Session   *Session         `json:"session,omitempty"`
}

type RequestUpdate struct {
	StepName        *string `json:"step_name,omitempty"`
	TransactionName *string `json:"transaction_name,omitempty"`
	Note            *string `json:"note,omitempty"`
	Excluded        *bool   `json:"excluded,omitempty"`
	Included        *bool   `json:"included,omitempty"`
	ThinkTimeMs     *int64  `json:"think_time_ms,omitempty"`
}

type Recorder struct {
	mu         sync.RWMutex
	sessions   map[string]*Session
	active     *Session
	proxy      *http.Server
	addr       string
	caCertPath string
	caKeyPath  string

	// TLS interception fields
	caOnce    sync.Once
	caErr     error
	caCert    *x509.Certificate
	caKey     *rsa.PrivateKey
	caCertPEM []byte
	certCache sync.Map // map[string]*tls.Certificate
}

func New(proxyAddr string) *Recorder {
	certPath, keyPath := defaultCAFiles()
	return NewWithCAFiles(proxyAddr, certPath, keyPath)
}

func NewWithCAFiles(proxyAddr, caCertPath, caKeyPath string) *Recorder {
	if proxyAddr == "" {
		proxyAddr = ":8081"
	}
	return &Recorder{
		sessions:   make(map[string]*Session),
		addr:       proxyAddr,
		caCertPath: caCertPath,
		caKeyPath:  caKeyPath,
	}
}

func defaultCAFiles() (string, string) {
	baseDir := os.Getenv("STORMRUNNER_RECORDER_CA_DIR")
	if strings.TrimSpace(baseDir) == "" {
		baseDir = defaultRecorderCADir
	}
	return filepath.Join(baseDir, defaultRecorderCACert), filepath.Join(baseDir, defaultRecorderCAKey)
}

func (r *Recorder) CACertPath() string {
	return r.caCertPath
}

func (r *Recorder) StartSession(id, name string, filters SessionFilters, notify func(SessionEvent)) (*Session, error) {
	return r.StartSessionForProject(id, name, "", filters, notify)
}

func (r *Recorder) StartSessionForProject(id, name, projectID string, filters SessionFilters, notify func(SessionEvent)) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.active != nil {
		return nil, fmt.Errorf("session %q is already active, stop it first", r.active.ID)
	}

	s := &Session{
		ID:        id,
		Name:      name,
		ProjectID: projectID,
		Status:    "recording",
		StartedAt: time.Now(),
		Filters:   filters,
		notify:    notify,
	}
	if filters.ExcludeStatic == false {
		s.Filters.ExcludeStatic = true
	}
	if s.Filters.BodyCaptureLimitBytes <= 0 {
		s.Filters.BodyCaptureLimitBytes = defaultBodyCaptureLimitBytes
	}

	r.sessions[id] = s
	r.active = s

	slog.Info("recording session started", "id", id, "name", name)
	return s, nil
}

func (r *Recorder) StopSession(id string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	if s.Status != "recording" && s.Status != "paused" {
		return nil, fmt.Errorf("session %q is not recording or paused (status: %s)", id, s.Status)
	}

	now := time.Now()
	s.Status = "stopped"
	s.StoppedAt = &now

	if r.active != nil && r.active.ID == id {
		r.active = nil
	}

	slog.Info("recording session stopped", "id", id, "requests", len(s.Requests))
	return s, nil
}

func (r *Recorder) PauseSession(id string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	if s.Status != "recording" {
		return nil, fmt.Errorf("session %q is not recording (status: %s)", id, s.Status)
	}
	s.Status = "paused"
	if s.notify != nil {
		s.notify(SessionEvent{Type: "recording_paused", SessionID: s.ID, Session: s})
	}
	return s, nil
}

func (r *Recorder) ResumeSession(id string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	if s.Status != "paused" {
		return nil, fmt.Errorf("session %q is not paused (status: %s)", id, s.Status)
	}
	s.Status = "recording"
	if s.notify != nil {
		s.notify(SessionEvent{Type: "recording_resumed", SessionID: s.ID, Session: s})
	}
	return s, nil
}

func (r *Recorder) UpdateRequest(sessionID string, seq int, update RequestUpdate) (*RecordedRequest, error) {
	r.mu.RLock()
	s, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Requests {
		if s.Requests[i].Seq != seq {
			continue
		}
		if update.StepName != nil {
			s.Requests[i].StepName = strings.TrimSpace(*update.StepName)
		}
		if update.TransactionName != nil {
			s.Requests[i].TransactionName = strings.TrimSpace(*update.TransactionName)
		}
		if update.Note != nil {
			s.Requests[i].Note = strings.TrimSpace(*update.Note)
		}
		if update.Excluded != nil {
			s.Requests[i].Excluded = *update.Excluded
		}
		if update.Included != nil {
			s.Requests[i].Excluded = !*update.Included
		}
		if update.ThinkTimeMs != nil {
			if *update.ThinkTimeMs < 0 {
				return nil, fmt.Errorf("think_time_ms must be non-negative")
			}
			s.Requests[i].ThinkTimeMs = *update.ThinkTimeMs
		}
		updated := s.Requests[i]
		if s.notify != nil {
			s.notify(SessionEvent{Type: "request_updated", SessionID: s.ID, Request: &updated})
		}
		return &updated, nil
	}

	return nil, fmt.Errorf("request seq %d not found in session %q", seq, sessionID)
}

func (r *Recorder) GetSession(id string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return s, ok
}

func (r *Recorder) ListSessions() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		result = append(result, s)
	}
	return result
}

func (r *Recorder) DeleteSession(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return false
	}
	if r.active != nil && r.active.ID == id {
		r.active = nil
	}
	_ = s
	delete(r.sessions, id)
	return true
}

func (r *Recorder) ActiveSession() *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

func (r *Recorder) StartProxy() error {
	r.proxy = &http.Server{
		Addr:         r.addr,
		Handler:      http.HandlerFunc(r.handleProxy),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	slog.Info("recording proxy starting", "addr", r.addr)
	go func() {
		if err := r.proxy.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("recording proxy error", "err", err)
		}
	}()
	return nil
}

func (r *Recorder) StopProxy() error {
	if r.proxy == nil {
		return nil
	}
	return r.proxy.Close()
}

func (r *Recorder) ProxyAddr() string {
	return r.addr
}

func (r *Recorder) handleProxy(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		r.handleConnect(w, req)
		return
	}

	r.mu.RLock()
	session := r.active
	r.mu.RUnlock()

	start := time.Now()

	targetURL := req.URL.String()
	if !req.URL.IsAbs() {
		targetURL = "http://" + req.Host + req.URL.String()
	}

	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	proxyReq, err := http.NewRequest(req.Method, targetURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		http.Error(w, "proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}

	for k, vv := range req.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}
	proxyReq.Header.Del("Proxy-Connection")

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)

	if session != nil {
		r.recordRequest(session, req, bodyBytes, resp, respBody, elapsed)
	}
}

func (r *Recorder) handleConnect(w http.ResponseWriter, req *http.Request) {
	r.handleConnectMITM(w, req)
}

func (r *Recorder) recordRequest(s *Session, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != "recording" {
		return
	}

	parsed, _ := url.Parse(req.URL.String())
	host := req.Host
	if parsed != nil && parsed.Host != "" {
		host = parsed.Host
	}

	if s.Filters.ExcludeStatic && isStaticResource(parsed) {
		return
	}

	if len(s.Filters.IncludeHosts) > 0 {
		matched := false
		for _, h := range s.Filters.IncludeHosts {
			if strings.Contains(host, h) {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
	}

	for _, h := range s.Filters.ExcludeHosts {
		if strings.Contains(host, h) {
			return
		}
	}

	s.seq++

	var thinkMs int64
	if !s.lastReqTime.IsZero() {
		thinkMs = time.Since(s.lastReqTime).Milliseconds() - int64(elapsed.Milliseconds())
		if thinkMs < 0 {
			thinkMs = 0
		}
	}
	s.lastReqTime = time.Now()

	headersMulti := sanitizeHeaders(req.Header, s.Filters.RedactHeaders, true)
	headers := firstHeaderValues(headersMulti)
	respHeadersMulti := sanitizeHeaders(resp.Header, s.Filters.RedactHeaders, false)
	respHeaders := firstHeaderValues(respHeadersMulti)

	ct := resp.Header.Get("Content-Type")
	respBodyStr, respBodyTruncated := capturedBody(respBody, ct, s.Filters.BodyCaptureLimitBytes)
	reqBodyStr, reqBodyTruncated := capturedBody(reqBody, req.Header.Get("Content-Type"), s.Filters.BodyCaptureLimitBytes)

	if s.BaseURL == "" && parsed != nil {
		s.BaseURL = parsed.Scheme + "://" + parsed.Host
	}

	resourceType := classifyResource(parsed, ct)
	rec := RecordedRequest{
		Seq:                   s.seq,
		Timestamp:             time.Now(),
		Method:                req.Method,
		URL:                   req.URL.String(),
		Scheme:                parsed.Scheme,
		Path:                  parsed.Path,
		QueryString:           parsed.RawQuery,
		Host:                  host,
		Headers:               headers,
		HeadersMulti:          headersMulti,
		Body:                  reqBodyStr,
		BodySizeBytes:         len(reqBody),
		BodyTruncated:         reqBodyTruncated,
		ContentType:           req.Header.Get("Content-Type"),
		StatusCode:            resp.StatusCode,
		ResponseTimeMs:        float64(elapsed.Microseconds()) / 1000.0,
		ResponseBody:          respBodyStr,
		ResponseBodySizeBytes: len(respBody),
		ResponseBodyTruncated: respBodyTruncated,
		ResponseHeaders:       respHeaders,
		ResponseHeadersMulti:  respHeadersMulti,
		ThinkTimeMs:           thinkMs,
		IsStatic:              isStaticResource(parsed),
		ResourceType:          resourceType,
	}

	s.Requests = append(s.Requests, rec)

	if s.notify != nil {
		s.notify(SessionEvent{
			Type:      "request_recorded",
			SessionID: s.ID,
			Request:   &rec,
		})
	}
}

func sanitizeHeaders(headers http.Header, extraRedact []string, skipProxy bool) map[string][]string {
	redact := map[string]bool{
		"authorization": true,
		"cookie":        true,
		"set-cookie":    true,
		"x-api-key":     true,
		"x-auth-token":  true,
		"x-csrf-token":  true,
		"x-xsrf-token":  true,
	}
	for _, h := range extraRedact {
		redact[strings.ToLower(strings.TrimSpace(h))] = true
	}

	result := make(map[string][]string)
	for k, vv := range headers {
		lk := strings.ToLower(k)
		if skipProxy && (lk == "proxy-connection" || lk == "proxy-authorization") {
			continue
		}
		values := make([]string, len(vv))
		for i, v := range vv {
			if redact[lk] {
				values[i] = "[redacted]"
			} else {
				values[i] = v
			}
		}
		result[k] = values
	}
	return result
}

func firstHeaderValues(headers map[string][]string) map[string]string {
	result := make(map[string]string)
	for k, vv := range headers {
		if len(vv) > 0 {
			result[k] = vv[0]
		}
	}
	return result
}

func capturedBody(body []byte, contentType string, limit int) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	if !isTextContent(contentType) && !utf8.Valid(body) {
		return "", false
	}
	if len(body) > limit {
		return string(body[:limit]), true
	}
	return string(body), false
}

func classifyResource(u *url.URL, contentType string) string {
	if u == nil {
		return "unknown"
	}
	path := strings.ToLower(u.Path)
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "json") || strings.HasPrefix(path, "/api/") || strings.Contains(path, "/api/"):
		return "api"
	case strings.Contains(ct, "html"):
		return "document"
	case strings.Contains(ct, "javascript") || strings.HasSuffix(path, ".js"):
		return "script"
	case strings.Contains(ct, "css") || strings.HasSuffix(path, ".css"):
		return "style"
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case strings.Contains(ct, "font") || strings.Contains(path, ".woff") || strings.Contains(path, ".ttf"):
		return "font"
	case isStaticResource(u):
		return "static"
	default:
		return "other"
	}
}

func isStaticResource(u *url.URL) bool {
	if u == nil {
		return false
	}
	path := strings.ToLower(u.Path)
	staticExts := []string{
		".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".eot", ".map", ".webp", ".avif",
	}
	for _, ext := range staticExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func isTextContent(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "html") ||
		strings.Contains(ct, "text") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "form-urlencoded")
}

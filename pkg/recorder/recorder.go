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
	"strings"
	"sync"
	"time"
)

type RecordedRequest struct {
	Seq            int               `json:"seq"`
	Timestamp      time.Time         `json:"timestamp"`
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Path           string            `json:"path"`
	Host           string            `json:"host"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body,omitempty"`
	ContentType    string            `json:"content_type,omitempty"`
	StatusCode     int               `json:"status_code"`
	ResponseTimeMs float64           `json:"response_time_ms"`
	ResponseBody   string            `json:"response_body,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ThinkTimeMs    int64             `json:"think_time_ms"`
	IsStatic       bool              `json:"is_static"`
}

type Session struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
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
	ExcludeStatic  bool     `json:"exclude_static"`
	ExcludeHosts   []string `json:"exclude_hosts,omitempty"`
	IncludeHosts   []string `json:"include_hosts,omitempty"`
}

type SessionEvent struct {
	Type      string           `json:"type"`
	SessionID string           `json:"session_id"`
	Request   *RecordedRequest `json:"request,omitempty"`
}

type Recorder struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	active   *Session
	proxy    *http.Server
	addr     string

	// TLS interception fields
	caOnce    sync.Once
	caErr     error
	caCert    *x509.Certificate
	caKey     *rsa.PrivateKey
	caCertPEM []byte
	certCache sync.Map // map[string]*tls.Certificate
}

func New(proxyAddr string) *Recorder {
	if proxyAddr == "" {
		proxyAddr = ":8081"
	}
	return &Recorder{
		sessions: make(map[string]*Session),
		addr:     proxyAddr,
	}
}

func (r *Recorder) StartSession(id, name string, filters SessionFilters, notify func(SessionEvent)) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.active != nil {
		return nil, fmt.Errorf("session %q is already active, stop it first", r.active.ID)
	}

	s := &Session{
		ID:        id,
		Name:      name,
		Status:    "recording",
		StartedAt: time.Now(),
		Filters:   filters,
		notify:    notify,
	}
	if filters.ExcludeStatic == false {
		s.Filters.ExcludeStatic = true
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
	if s.Status != "recording" {
		return nil, fmt.Errorf("session %q is not recording (status: %s)", id, s.Status)
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

	headers := make(map[string]string)
	for k, vv := range req.Header {
		lk := strings.ToLower(k)
		if lk == "proxy-connection" || lk == "proxy-authorization" {
			continue
		}
		headers[k] = vv[0]
	}

	respHeaders := make(map[string]string)
	for k, vv := range resp.Header {
		respHeaders[k] = vv[0]
	}

	respBodyStr := ""
	ct := resp.Header.Get("Content-Type")
	if isTextContent(ct) && len(respBody) < 512*1024 {
		respBodyStr = string(respBody)
	}

	if s.BaseURL == "" && parsed != nil {
		s.BaseURL = parsed.Scheme + "://" + parsed.Host
	}

	rec := RecordedRequest{
		Seq:             s.seq,
		Timestamp:       time.Now(),
		Method:          req.Method,
		URL:             req.URL.String(),
		Path:            parsed.Path,
		Host:            host,
		Headers:         headers,
		Body:            string(reqBody),
		ContentType:     req.Header.Get("Content-Type"),
		StatusCode:      resp.StatusCode,
		ResponseTimeMs:  float64(elapsed.Microseconds()) / 1000.0,
		ResponseBody:    respBodyStr,
		ResponseHeaders: respHeaders,
		ThinkTimeMs:     thinkMs,
		IsStatic:        isStaticResource(parsed),
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

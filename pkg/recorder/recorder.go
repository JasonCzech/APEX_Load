package recorder

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/internal/contentdecode"
	"github.com/jasonczech/apexload/pkg/internal/hostpattern"
	"github.com/jasonczech/apexload/pkg/recorder/marker"
)

const defaultBodyCaptureLimitBytes = 512 * 1024
const maxBodyCaptureLimitBytes = requestimport.MaxResponseContentStorageBytes
const maxRecordedRequests = 10_000
const maxRecordedBodyBytes = 256 << 20
const maxRecordedWebSocketMessages = 10_000
const maxRecordedWebSocketPayloadBytes = 64 << 20
const maxRecordedSessionBytes = 512 << 20
const maxRetainedRecorderBytes = 1 << 30
const maxRetainedRecorderSessions = 32
const maxRecorderSessionIDBytes = 256
const maxRecorderSessionNameBytes = 4 << 10
const maxRecorderProjectIDBytes = 512
const maxRecorderFilterEntries = 256
const maxRecorderFilterValueBytes = 4 << 10
const maxRecorderFilterBytes = 256 << 10
const maxRecorderStepNameBytes = 4 << 10
const maxRecorderTransactionNameBytes = 4 << 10
const maxRecorderNoteBytes = 64 << 10
const streamingRecordDelay = 250 * time.Millisecond
const maxMultipartRequestCaptureBytes = 50 << 20
const maxRecorderLeafCertificates = 256
const maxRecorderCertificateHostBytes = 253
const maxRecorderCertificateCacheKeyBytes = 64 << 10
const maxRecorderHeaderBytes = 64 << 10
const maxConcurrentRecorderProxyRequests = 256

const (
	defaultRecorderCADir  = "artifacts/recorder"
	defaultRecorderCACert = "ca-cert.pem"
	defaultRecorderCAKey  = "ca-key.pem"
)

type RecordedRequest struct {
	Seq                     int                                  `json:"seq"`
	Timestamp               time.Time                            `json:"timestamp"`
	Method                  string                               `json:"method"`
	RequestHTTPVersion      string                               `json:"request_http_version,omitempty"`
	ResponseHTTPVersion     string                               `json:"response_http_version,omitempty"`
	URL                     string                               `json:"url"`
	Scheme                  string                               `json:"scheme,omitempty"`
	Path                    string                               `json:"path"`
	QueryString             string                               `json:"query_string,omitempty"`
	Host                    string                               `json:"host"`
	Headers                 map[string]string                    `json:"headers"`
	HeadersMulti            map[string][]string                  `json:"headers_multi,omitempty"`
	RawHeaders              map[string]string                    `json:"-"`
	RawHeadersMulti         map[string][]string                  `json:"-"`
	Body                    string                               `json:"body,omitempty"`
	BodyEncoding            string                               `json:"body_encoding,omitempty"`
	BodySizeBytes           int                                  `json:"body_size_bytes,omitempty"`
	BodyTruncated           bool                                 `json:"body_truncated,omitempty"`
	ContentType             string                               `json:"content_type,omitempty"`
	StatusCode              int                                  `json:"status_code"`
	ResponseTimeMs          float64                              `json:"response_time_ms"`
	ResponseBody            string                               `json:"response_body,omitempty"`
	ResponseBodyEncoding    string                               `json:"response_body_encoding,omitempty"`
	ResponseBodySizeBytes   int                                  `json:"response_body_size_bytes,omitempty"`
	ResponseContentCapture  requestimport.ResponseContentCapture `json:"response_content_capture,omitzero"`
	ResponseBodyTruncated   bool                                 `json:"response_body_truncated,omitempty"`
	ResponseHeaders         map[string]string                    `json:"response_headers,omitempty"`
	ResponseHeadersMulti    map[string][]string                  `json:"response_headers_multi,omitempty"`
	RawResponseHeaders      map[string]string                    `json:"-"`
	RawResponseHeadersMulti map[string][]string                  `json:"-"`
	ThinkTimeMs             int64                                `json:"think_time_ms"`
	IsStatic                bool                                 `json:"is_static"`
	ResourceType            string                               `json:"resource_type,omitempty"`
	StepName                string                               `json:"step_name,omitempty"`
	TransactionName         string                               `json:"transaction_name,omitempty"`
	Note                    string                               `json:"note,omitempty"`
	Excluded                bool                                 `json:"excluded,omitempty"`
	WebSocketMessages       []RecordedWebSocketMessage           `json:"websocket_messages,omitempty"`
}

type RecordedWebSocketMessage struct {
	Direction        string    `json:"direction"`
	Opcode           int       `json:"opcode"`
	Payload          string    `json:"payload,omitempty"`
	PayloadEncoding  string    `json:"payload_encoding,omitempty"`
	PayloadSizeBytes int       `json:"payload_size_bytes,omitempty"`
	PayloadTruncated bool      `json:"payload_truncated,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

type Session struct {
	ID                       string            `json:"id"`
	Name                     string            `json:"name"`
	ProjectID                string            `json:"project_id,omitempty"`
	Status                   string            `json:"status"`
	StartedAt                time.Time         `json:"started_at"`
	StoppedAt                *time.Time        `json:"stopped_at,omitempty"`
	BaseURL                  string            `json:"base_url,omitempty"`
	Requests                 []RecordedRequest `json:"requests,omitempty"`
	RequestCount             int               `json:"request_count,omitempty"`
	LastSeq                  int               `json:"last_seq,omitempty"`
	DroppedRequests          int64             `json:"dropped_requests,omitempty"`
	DroppedWebSocketMessages int64             `json:"dropped_websocket_messages,omitempty"`
	RejectedRequests         int64             `json:"rejected_requests,omitempty"`  // targets the proxy refused to forward to
	LastRejectedHost         string            `json:"last_rejected_host,omitempty"` // the most recent of them
	Truncated                bool              `json:"truncated,omitempty"`
	Warnings                 []string          `json:"warnings,omitempty"`
	Filters                  SessionFilters    `json:"filters"`
	// Markers are the transactions and notes the operator marked while
	// recording; OpenTransactions are those not yet ended.
	Markers          []marker.Marker `json:"markers,omitempty"`
	OpenTransactions []string        `json:"open_transactions,omitempty"`

	mu                    sync.Mutex
	markerBytes           int
	seq                   int
	lastReqTime           time.Time
	lastReqEnd            time.Time
	notify                func(SessionEvent)
	storedBodyBytes       int
	webSocketMessages     int
	webSocketPayloadBytes int
	retainedBytes         int64
	requestDropWarned     bool
	webSocketDropWarned   bool
}

type SessionFilters struct {
	ExcludeStatic         bool     `json:"exclude_static"`
	ExcludeStaticSet      bool     `json:"-"`
	ExcludeHosts          []string `json:"exclude_hosts,omitempty"`
	IncludeHosts          []string `json:"include_hosts,omitempty"`
	BodyCaptureLimitBytes int      `json:"body_capture_limit_bytes,omitempty"`
	RedactHeaders         []string `json:"redact_headers,omitempty"`
	CaptureCookies        bool     `json:"capture_cookies,omitempty"`
	// RecordingProfileID names the project recording profile the session was
	// started with; conversions apply it unless they name another.
	RecordingProfileID string `json:"recording_profile_id,omitempty"`
}

type SessionEvent struct {
	Type      string           `json:"type"`
	SessionID string           `json:"session_id"`
	Request   *RecordedRequest `json:"request,omitempty"`
	Session   *Session         `json:"session,omitempty"`
	Warning   string           `json:"warning,omitempty"`
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
	mu               sync.RWMutex
	sessions         map[string]*Session
	active           *Session
	proxy            *http.Server
	proxyListener    net.Listener
	proxyDone        chan struct{}
	proxyStopped     bool
	proxyStartCode   string // why the last StartProxy failed; "" once running
	proxyStartReason string
	proxyClient      *http.Client
	addr             string
	proxyAuthToken   string
	requireProxyAuth bool
	requireLoopback  bool
	caCertPath       string
	caKeyPath        string
	// upstreamBodyInactivityTimeout is internal so tests can exercise stalled
	// response handling without waiting for the production timeout.
	upstreamBodyInactivityTimeout time.Duration
	// streamingSnapshotDelay is internal so tests can choose whether a
	// streamed response ends before or after its snapshot.
	streamingSnapshotDelay time.Duration

	// TLS interception fields
	caOnce     sync.Once
	caErr      error
	caCert     *x509.Certificate
	caKey      *rsa.PrivateKey
	caCertPEM  []byte
	certMu     sync.Mutex
	certCache  *boundedcache.StringCache[*tls.Certificate]
	tunnels    sync.Map // map[*recorderTunnel]struct{}
	proxySlots chan struct{}
}

// Options configures the recording proxy and its persistent interception CA.
// RequireProxyAuth disables the legacy environment-token fallback and prevents
// the proxy from binding unless ProxyAuthToken is explicitly supplied. Desktop
// shells should enable it and use a high-entropy token distinct from the API
// capability. RequireLoopback prevents strict embedded proxies from being
// redirected to a network-visible listener by configuration drift.
type Options struct {
	ProxyAddr        string
	ProxyAuthToken   string
	CACertPath       string
	CAKeyPath        string
	RequireProxyAuth bool
	RequireLoopback  bool
}

func New(proxyAddr string) *Recorder {
	return NewWithOptions(Options{ProxyAddr: proxyAddr})
}

func NewWithCAFiles(proxyAddr, caCertPath, caKeyPath string) *Recorder {
	return NewWithOptions(Options{
		ProxyAddr:  proxyAddr,
		CACertPath: caCertPath,
		CAKeyPath:  caKeyPath,
	})
}

// NewWithOptions constructs a recorder without starting its proxy listener.
// Hosted callers retain the historical environment-backed proxy token when
// RequireProxyAuth is false; strict embedders must pass the token explicitly.
func NewWithOptions(options Options) *Recorder {
	proxyAddr := strings.TrimSpace(options.ProxyAddr)
	if proxyAddr == "" {
		proxyAddr = defaultProxyAddr
	}
	proxyAuthToken := strings.TrimSpace(options.ProxyAuthToken)
	if proxyAuthToken == "" && !options.RequireProxyAuth {
		proxyAuthToken = strings.TrimSpace(os.Getenv(envProxyAuthToken))
	}
	caCertPath, caKeyPath := resolveCAFiles(options.CACertPath, options.CAKeyPath)
	transport := &http.Transport{
		DisableCompression:     true, // Capture and forward original response wire bytes.
		DialContext:            proxySafeDialer(30 * time.Second).DialContext,
		TLSClientConfig:        recorderUpstreamTLSConfig(""),
		MaxIdleConns:           256,
		MaxIdleConnsPerHost:    64,
		IdleConnTimeout:        90 * time.Second,
		MaxResponseHeaderBytes: maxRecorderHeaderBytes,
	}
	return &Recorder{
		sessions:                      make(map[string]*Session),
		certCache:                     boundedcache.NewStringCache[*tls.Certificate](maxRecorderLeafCertificates, maxRecorderCertificateHostBytes, maxRecorderCertificateCacheKeyBytes),
		proxySlots:                    make(chan struct{}, maxConcurrentRecorderProxyRequests),
		proxyClient:                   &http.Client{Timeout: 30 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }},
		addr:                          proxyAddr,
		proxyAuthToken:                proxyAuthToken,
		requireProxyAuth:              options.RequireProxyAuth,
		requireLoopback:               options.RequireLoopback,
		caCertPath:                    caCertPath,
		caKeyPath:                     caKeyPath,
		upstreamBodyInactivityTimeout: recorderUpstreamBodyInactivityTimeout,
		streamingSnapshotDelay:        streamingRecordDelay,
	}
}

func resolveCAFiles(caCertPath, caKeyPath string) (string, string) {
	caCertPath = strings.TrimSpace(caCertPath)
	caKeyPath = strings.TrimSpace(caKeyPath)
	switch {
	case caCertPath == "" && caKeyPath == "":
		return defaultCAFiles()
	case caCertPath == "":
		caCertPath = filepath.Join(filepath.Dir(caKeyPath), defaultRecorderCACert)
	case caKeyPath == "":
		caKeyPath = filepath.Join(filepath.Dir(caCertPath), defaultRecorderCAKey)
	}
	return filepath.Clean(caCertPath), filepath.Clean(caKeyPath)
}

func defaultCAFiles() (string, string) {
	baseDir := os.Getenv("APEXLOAD_RECORDER_CA_DIR")
	if strings.TrimSpace(baseDir) == "" {
		baseDir = defaultRecorderCADir
	}
	return filepath.Join(baseDir, defaultRecorderCACert), filepath.Join(baseDir, defaultRecorderCAKey)
}

func (r *Recorder) CACertPath() string {
	return r.caCertPath
}

func (r *Recorder) CAKeyPath() string {
	return r.caKeyPath
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
	if err := validateRecorderSessionConfig(id, name, projectID, filters); err != nil {
		return nil, err
	}
	if err := r.pruneRetainedSessionsLocked(maxRecordedSessionBytes, id); err != nil {
		return nil, err
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
	if !filters.ExcludeStaticSet {
		s.Filters.ExcludeStatic = true
	}
	if s.Filters.BodyCaptureLimitBytes <= 0 {
		s.Filters.BodyCaptureLimitBytes = defaultBodyCaptureLimitBytes
	}
	if s.Filters.BodyCaptureLimitBytes > maxBodyCaptureLimitBytes {
		return nil, fmt.Errorf("body_capture_limit_bytes must not exceed %d", maxBodyCaptureLimitBytes)
	}
	s.retainedBytes = estimateRecorderSessionBaseBytes(s)

	r.sessions[id] = s
	r.active = s

	slog.Info("recording session started", "id", id, "name", name)
	return s, nil
}

func validateRecorderSessionConfig(id, name, projectID string, filters SessionFilters) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("recording session id is required")
	}
	if len(id) > maxRecorderSessionIDBytes {
		return fmt.Errorf("recording session id exceeds %d bytes", maxRecorderSessionIDBytes)
	}
	if len(name) > maxRecorderSessionNameBytes {
		return fmt.Errorf("recording session name exceeds %d bytes", maxRecorderSessionNameBytes)
	}
	if len(projectID) > maxRecorderProjectIDBytes {
		return fmt.Errorf("recording project id exceeds %d bytes", maxRecorderProjectIDBytes)
	}
	if filters.BodyCaptureLimitBytes > maxBodyCaptureLimitBytes {
		return fmt.Errorf("body_capture_limit_bytes must not exceed %d", maxBodyCaptureLimitBytes)
	}
	entries := 0
	bytes := 0
	for _, values := range [][]string{filters.ExcludeHosts, filters.IncludeHosts, filters.RedactHeaders} {
		entries += len(values)
		for _, value := range values {
			if len(value) > maxRecorderFilterValueBytes {
				return fmt.Errorf("recording filter value exceeds %d bytes", maxRecorderFilterValueBytes)
			}
			bytes += len(value)
		}
	}
	if entries > maxRecorderFilterEntries {
		return fmt.Errorf("recording filters exceed %d entries", maxRecorderFilterEntries)
	}
	if bytes > maxRecorderFilterBytes {
		return fmt.Errorf("recording filters exceed %d bytes", maxRecorderFilterBytes)
	}
	return nil
}

func estimateRecorderSessionBaseBytes(session *Session) int64 {
	if session == nil {
		return 0
	}
	bytes := int64(1024 + len(session.ID) + len(session.Name) + len(session.ProjectID) + len(session.BaseURL))
	for _, values := range [][]string{session.Filters.ExcludeHosts, session.Filters.IncludeHosts, session.Filters.RedactHeaders} {
		bytes += int64(len(values) * 32)
		for _, value := range values {
			bytes += int64(len(value))
		}
	}
	return bytes
}

// pruneRetainedSessionsLocked evicts oldest stopped sessions before admitting
// a new active session. The reservedBytes argument leaves enough space for the
// incoming recording to reach its own hard cap without pushing the controller
// above the aggregate in-memory recorder budget. A session with replacingID is
// excluded because successful admission overwrites it. Caller holds r.mu.
func (r *Recorder) pruneRetainedSessionsLocked(reservedBytes int64, replacingID string) error {
	if r == nil {
		return fmt.Errorf("recorder is nil")
	}
	type retainedSession struct {
		id      string
		stopped time.Time
		bytes   int64
	}
	candidates := make([]retainedSession, 0, len(r.sessions))
	totalBytes := int64(0)
	retainedCount := 0
	for id, session := range r.sessions {
		if id == replacingID {
			continue
		}
		retainedCount++
		if session == nil {
			candidates = append(candidates, retainedSession{id: id})
			continue
		}
		session.mu.Lock()
		status := session.Status
		stopped := session.StartedAt
		if session.StoppedAt != nil {
			stopped = *session.StoppedAt
		}
		bytes := session.retainedBytes
		session.mu.Unlock()
		totalBytes += bytes
		if status != "recording" && status != "paused" {
			candidates = append(candidates, retainedSession{id: id, stopped: stopped, bytes: bytes})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].stopped.Equal(candidates[j].stopped) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].stopped.Before(candidates[j].stopped)
	})
	if reservedBytes < 0 || reservedBytes > maxRetainedRecorderBytes {
		return fmt.Errorf("recording session reservation exceeds %d byte retention limit", maxRetainedRecorderBytes)
	}
	for retainedCount+1 > maxRetainedRecorderSessions || totalBytes+reservedBytes > maxRetainedRecorderBytes {
		if len(candidates) == 0 {
			return fmt.Errorf("recording retention limit reached (%d sessions, %d bytes)", maxRetainedRecorderSessions, maxRetainedRecorderBytes)
		}
		oldest := candidates[0]
		candidates = candidates[1:]
		delete(r.sessions, oldest.id)
		retainedCount--
		totalBytes -= oldest.bytes
	}
	return nil
}

func (r *Recorder) StopSession(id string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}

	// Status/StoppedAt/Requests are read on the record/snapshot hot paths under
	// s.mu; guard the check+write here with s.mu too (r.mu only protects the
	// sessions map + r.active). Lock order is r.mu -> s.mu; readers only hold s.mu.
	s.mu.Lock()
	if s.Status != "recording" && s.Status != "paused" {
		status := s.Status
		s.mu.Unlock()
		return nil, fmt.Errorf("session %q is not recording or paused (status: %s)", id, status)
	}
	now := time.Now()
	s.Status = "stopped"
	s.StoppedAt = &now
	book := s.markerBookLocked()
	s.Warnings = append(s.Warnings, boundedStopWarnings(book.CloseOpen(now, marker.DefaultLimits))...)
	s.setMarkerBookLocked(book)
	reqCount := len(s.Requests)
	s.mu.Unlock()

	if r.active != nil && r.active.ID == id {
		r.active = nil
	}
	r.closeTunnelsForSession(id)

	slog.Info("recording session stopped", "id", id, "requests", reqCount)
	return s, nil
}

func (r *Recorder) PauseSession(id string) (*Session, error) {
	r.mu.Lock()
	s, ok := r.sessions[id]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("session %q not found", id)
	}
	s.mu.Lock()
	if s.Status != "recording" {
		status := s.Status
		s.mu.Unlock()
		r.mu.Unlock()
		return nil, fmt.Errorf("session %q is not recording (status: %s)", id, status)
	}
	s.Status = "paused"
	s.mu.Unlock()
	r.mu.Unlock()
	if s.notify != nil {
		s.notify(SessionEvent{Type: "recording_paused", SessionID: s.ID, Session: s.snapshot()})
	}
	return s, nil
}

func (r *Recorder) ResumeSession(id string) (*Session, error) {
	r.mu.Lock()
	s, ok := r.sessions[id]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("session %q not found", id)
	}
	s.mu.Lock()
	if s.Status != "paused" {
		status := s.Status
		s.mu.Unlock()
		r.mu.Unlock()
		return nil, fmt.Errorf("session %q is not paused (status: %s)", id, status)
	}
	s.Status = "recording"
	s.mu.Unlock()
	r.mu.Unlock()
	if s.notify != nil {
		s.notify(SessionEvent{Type: "recording_resumed", SessionID: s.ID, Session: s.snapshot()})
	}
	return s, nil
}

// maxStopMarkerWarnings is how many transactions still open at a stop the
// session's warnings name one by one; the rest are counted in one more.
const maxStopMarkerWarnings = 20

func boundedStopWarnings(warnings []string) []string {
	if len(warnings) <= maxStopMarkerWarnings {
		return warnings
	}
	more := len(warnings) - maxStopMarkerWarnings
	return append(warnings[:maxStopMarkerWarnings:maxStopMarkerWarnings],
		fmt.Sprintf("%d more transaction(s) were still open and were closed automatically when the recording stopped", more))
}

// Errors AddMarker returns for a session it cannot mark.
var (
	ErrSessionNotFound = errors.New("recording session not found")
	ErrSessionNotLive  = errors.New("recording session is not recording or paused")
)

// AddMarker records a transaction start or end, or a note, on a recording or
// paused session. Requests are later placed in the transaction that was open
// when they started.
func (r *Recorder) AddMarker(sessionID string, m marker.Marker) (*Session, marker.Marker, error) {
	r.mu.RLock()
	s, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return nil, marker.Marker{}, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	s.mu.Lock()
	if s.Status != "recording" && s.Status != "paused" {
		status := s.Status
		s.mu.Unlock()
		return nil, marker.Marker{}, fmt.Errorf("%w (status: %s)", ErrSessionNotLive, status)
	}
	m.Auto = false
	book := s.markerBookLocked()
	added, err := book.Add(m, marker.DefaultLimits)
	s.setMarkerBookLocked(book)
	s.mu.Unlock()
	if err != nil {
		return nil, marker.Marker{}, err
	}
	snapshot := s.summarySnapshot()
	if s.notify != nil {
		s.notify(SessionEvent{Type: "recording_marker", SessionID: s.ID, Session: snapshot})
	}
	return snapshot, added, nil
}

func (s *Session) markerBookLocked() marker.Book {
	return marker.Book{Markers: s.Markers, OpenTransactions: s.OpenTransactions, RetainedBytes: s.markerBytes}
}

func (s *Session) setMarkerBookLocked(book marker.Book) {
	s.Markers, s.OpenTransactions, s.markerBytes = book.Markers, book.OpenTransactions, book.RetainedBytes
}

// TransactionWindows pairs the session's transaction markers. Call it on a
// snapshot, or with the session's lock held.
func (s *Session) TransactionWindows() []marker.Window {
	return marker.Windows(s.Markers)
}

func (r *Recorder) UpdateRequest(sessionID string, seq int, update RequestUpdate) (*RecordedRequest, error) {
	r.mu.RLock()
	s, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}

	s.mu.Lock()

	for i := range s.Requests {
		if s.Requests[i].Seq != seq {
			continue
		}
		updated := s.Requests[i]
		if update.StepName != nil {
			value := strings.TrimSpace(*update.StepName)
			if len(value) > maxRecorderStepNameBytes {
				s.mu.Unlock()
				return nil, fmt.Errorf("step_name exceeds %d bytes", maxRecorderStepNameBytes)
			}
			updated.StepName = value
		}
		if update.TransactionName != nil {
			value := strings.TrimSpace(*update.TransactionName)
			if len(value) > maxRecorderTransactionNameBytes {
				s.mu.Unlock()
				return nil, fmt.Errorf("transaction_name exceeds %d bytes", maxRecorderTransactionNameBytes)
			}
			updated.TransactionName = value
		}
		if update.Note != nil {
			value := strings.TrimSpace(*update.Note)
			if len(value) > maxRecorderNoteBytes {
				s.mu.Unlock()
				return nil, fmt.Errorf("note exceeds %d bytes", maxRecorderNoteBytes)
			}
			updated.Note = value
		}
		if update.Excluded != nil {
			updated.Excluded = *update.Excluded
		}
		if update.Included != nil {
			updated.Excluded = !*update.Included
		}
		if update.ThinkTimeMs != nil {
			if *update.ThinkTimeMs < 0 {
				s.mu.Unlock()
				return nil, fmt.Errorf("think_time_ms must be non-negative")
			}
			updated.ThinkTimeMs = *update.ThinkTimeMs
		}
		oldBytes := estimateRecordedRequestBytes(s.Requests[i])
		newBytes := estimateRecordedRequestBytes(updated)
		baseBytes := s.retainedBytes - min(s.retainedBytes, oldBytes)
		if newBytes > maxRecordedSessionBytes-baseBytes {
			s.mu.Unlock()
			return nil, fmt.Errorf("recording session exceeds %d byte retention limit", maxRecordedSessionBytes)
		}
		s.retainedBytes = baseBytes + newBytes
		s.Requests[i] = updated
		eventRequest := recordingEventRequest(updated)
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify(SessionEvent{Type: "request_updated", SessionID: s.ID, Request: &eventRequest})
		}
		return &eventRequest, nil
	}

	s.mu.Unlock()
	return nil, fmt.Errorf("request seq %d not found in session %q", seq, sessionID)
}

// snapshot returns a deep-enough copy of the session for safe concurrent reads.
// It is taken under s.mu so it never races the recorder's write paths
// (recordRequestWithOptions / appendWebSocketMessage / UpdateRequest, which all
// hold s.mu). The returned *Session has a fresh zero-value mutex and must be
// treated as read-only — mutators look sessions up by id, never via this copy.
// Header maps are created once under lock and never mutated afterward, so they
// are aliased; WebSocketMessages is appended after creation, so it is cloned.
func (s *Session) snapshot() *Session {
	return s.snapshotWithOptions(true, 0)
}

func (s *Session) summarySnapshot() *Session {
	return s.snapshotWithOptions(false, 0)
}

func (s *Session) snapshotSince(seq int) *Session {
	return s.snapshotWithOptions(true, seq)
}

func (s *Session) snapshotWithOptions(includeRequests bool, sinceSeq int) *Session {
	return s.snapshotWindow(includeRequests, sinceSeq, 0)
}

func (s *Session) snapshotWindow(includeRequests bool, sinceSeq int, tail int) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := &Session{
		ID:                       s.ID,
		Name:                     s.Name,
		ProjectID:                s.ProjectID,
		Status:                   s.Status,
		StartedAt:                s.StartedAt,
		StoppedAt:                s.StoppedAt,
		BaseURL:                  s.BaseURL,
		RequestCount:             len(s.Requests),
		LastSeq:                  s.seq,
		DroppedRequests:          s.DroppedRequests,
		DroppedWebSocketMessages: s.DroppedWebSocketMessages,
		RejectedRequests:         s.RejectedRequests,
		LastRejectedHost:         s.LastRejectedHost,
		Truncated:                s.Truncated,
		Warnings:                 append([]string(nil), s.Warnings...),
		Filters:                  s.Filters,
		Markers:                  append([]marker.Marker(nil), s.Markers...),
		OpenTransactions:         append([]string(nil), s.OpenTransactions...),
	}
	if includeRequests && len(s.Requests) > 0 {
		start := 0
		if tail > 0 && len(s.Requests) > tail {
			start = len(s.Requests) - tail
		}
		cp.Requests = make([]RecordedRequest, 0, len(s.Requests)-start)
		for _, request := range s.Requests[start:] {
			if sinceSeq > 0 && request.Seq <= sinceSeq {
				continue
			}
			clonedRequest := request
			if msgs := clonedRequest.WebSocketMessages; len(msgs) > 0 {
				cloned := make([]RecordedWebSocketMessage, len(msgs))
				copy(cloned, msgs)
				clonedRequest.WebSocketMessages = cloned
			}
			cp.Requests = append(cp.Requests, clonedRequest)
		}
	}
	return cp
}

func (r *Recorder) GetSession(id string) (*Session, bool) {
	return r.GetSessionWithOptions(id, true, 0)
}

func (r *Recorder) GetSessionWithOptions(id string, includeRequests bool, sinceSeq int) (*Session, bool) {
	return r.GetSessionWindow(id, includeRequests, sinceSeq, 0)
}

// GetSessionWindow returns a bounded tail when tail is positive. The total
// RequestCount and LastSeq still describe the complete session.
func (r *Recorder) GetSessionWindow(id string, includeRequests bool, sinceSeq int, tail int) (*Session, bool) {
	r.mu.RLock()
	s, ok := r.sessions[id]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if sinceSeq > 0 {
		return s.snapshotWindow(true, sinceSeq, tail), true
	}
	if includeRequests {
		return s.snapshotWindow(true, 0, tail), true
	}
	return s.summarySnapshot(), true
}

func (r *Recorder) ListSessions() []*Session {
	return r.listSessions(true)
}

func (r *Recorder) ListSessionSummaries() []*Session {
	return r.listSessions(false)
}

func (r *Recorder) listSessions(includeRequests bool) []*Session {
	r.mu.RLock()
	live := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		live = append(live, s)
	}
	r.mu.RUnlock()
	result := make([]*Session, len(live))
	for i, s := range live {
		if includeRequests {
			result[i] = s.snapshot()
		} else {
			result[i] = s.summarySnapshot()
		}
	}
	return result
}

func (r *Recorder) DeleteSession(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[id]
	if !ok {
		return false
	}
	if r.active != nil && r.active.ID == id {
		r.active = nil
	}
	delete(r.sessions, id)
	r.closeTunnelsForSession(id)
	return true
}

func (r *Recorder) ActiveSession() *Session {
	return r.activeSessionWithRequests(true)
}

func (r *Recorder) ActiveSessionSummary() *Session {
	return r.activeSessionWithRequests(false)
}

func (r *Recorder) activeSessionWithRequests(includeRequests bool) *Session {
	r.mu.RLock()
	s := r.active
	r.mu.RUnlock()
	if s == nil {
		return nil
	}
	if includeRequests {
		return s.snapshot()
	}
	return s.summarySnapshot()
}

func (r *Recorder) StartProxy() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proxyStopped {
		return http.ErrServerClosed
	}
	if r.proxy != nil {
		return fmt.Errorf("recording proxy is already running")
	}
	if err := r.validateProxyStartup(); err != nil {
		r.noteProxyStartFailureLocked(err)
		return err
	}
	listener, err := net.Listen("tcp", r.addr)
	if err != nil {
		err = proxyStartupFailure(ProxyBindFailed, fmt.Errorf("listen recording proxy on %s: %w", r.addr, err))
		r.noteProxyStartFailureLocked(err)
		return err
	}
	r.proxyStartCode, r.proxyStartReason = "", ""
	server := &http.Server{
		Addr:           r.addr,
		Handler:        http.HandlerFunc(r.handleProxy),
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   60 * time.Second,
		MaxHeaderBytes: maxRecorderHeaderBytes,
	}
	done := make(chan struct{})
	r.proxy = server
	r.proxyListener = listener
	r.proxyDone = done
	r.addr = listener.Addr().String()

	slog.Info("recording proxy starting", "addr", r.addr)
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			slog.Error("recording proxy error", "err", err)
		}
	}()
	return nil
}

// StopProxy permanently closes the listener for this Recorder. Recorders are
// process-lifecycle components; making stop terminal prevents an in-flight
// startup or late hijack from resurrecting a listener during server shutdown.
func (r *Recorder) StopProxy() error {
	r.mu.Lock()
	server := r.proxy
	listener := r.proxyListener
	done := r.proxyDone
	r.proxyStopped = true
	r.proxy = nil
	r.proxyListener = nil
	r.proxyDone = nil
	r.mu.Unlock()
	r.closeAllTunnels()
	if server == nil && listener == nil {
		return nil
	}
	var stopErr error
	if server != nil {
		if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			stopErr = errors.Join(stopErr, err)
		}
	}
	// Close the concrete listener as well: StopProxy may race the Serve goroutine
	// before net/http has registered the listener with Server.Close. When a server
	// exists, its Close result is authoritative and a redundant listener-close
	// error only means the same socket was already closed.
	if listener != nil {
		if err := listener.Close(); server == nil && err != nil && !errors.Is(err, net.ErrClosed) {
			stopErr = errors.Join(stopErr, err)
		}
	}
	if done != nil {
		<-done
	}
	if transport, ok := r.proxyClient.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	return stopErr
}

func (r *Recorder) ProxyAddr() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.addr
}

func (r *Recorder) ProxyRunning() bool {
	return r.ProxyState().Running
}

// ProxyState is whether the recording proxy is listening, where, and — when it
// is not — why, as a reason code (ProxyNotStarted, ProxyProductionDisabled,
// ProxyConfiguration, ProxyBindFailed, ProxyStopped) and the operator-facing
// cause.
type ProxyState struct {
	Running               bool
	Addr                  string
	Loopback              bool
	ReasonCode            string
	Reason                string
	PrivateTargetsAllowed bool
}

func (r *Recorder) ProxyState() ProxyState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := ProxyState{
		Running:               r.proxy != nil,
		Addr:                  r.addr,
		Loopback:              proxyBindIsLoopback(r.addr),
		PrivateTargetsAllowed: proxyEnvBool(envProxyAllowPrivateTargets),
	}
	switch {
	case state.Running:
	case r.proxyStopped:
		state.ReasonCode, state.Reason = ProxyStopped, "the recording proxy has stopped"
	case r.proxyStartCode != "":
		state.ReasonCode, state.Reason = r.proxyStartCode, r.proxyStartReason
	default:
		state.ReasonCode, state.Reason = ProxyNotStarted, "the recording proxy has not started"
	}
	return state
}

func (r *Recorder) noteProxyStartFailureLocked(err error) {
	var startup *proxyStartupError
	if errors.As(err, &startup) {
		r.proxyStartCode = startup.code
	} else {
		r.proxyStartCode = ProxyConfiguration
	}
	r.proxyStartReason = err.Error()
}

// noteRejectedTarget counts a request the proxy refused to forward to a
// disallowed target, so the recording can say why the browser's traffic is
// missing from it. Other refusals (malformed targets) are not counted.
func (r *Recorder) noteRejectedTarget(session *Session, host string, err error) {
	if session == nil || !isRejectedProxyTarget(err) {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.Status != "recording" {
		return
	}
	session.RejectedRequests++
	session.LastRejectedHost = strings.Trim(strings.TrimSpace(host), "[]")
}

func (r *Recorder) ProxyAuthToken() string {
	return strings.TrimSpace(r.proxyAuthToken)
}

func (r *Recorder) handleProxy(w http.ResponseWriter, req *http.Request) {
	if !r.authorizeProxyRequest(req) {
		// Browsers implement Basic proxy challenges and will prompt for these
		// per-launch credentials. Keep Bearer as a second challenge for scripted
		// clients that already use the recorder token directly.
		w.Header().Add("Proxy-Authenticate", `Basic realm="apexload-recorder", charset="UTF-8"`)
		w.Header().Add("Proxy-Authenticate", `Bearer realm="apexload-recorder"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if !r.acquireProxySlot() {
		http.Error(w, "recording proxy concurrency limit reached", http.StatusServiceUnavailable)
		return
	}
	defer r.releaseProxySlot()
	if req.Method == http.MethodConnect {
		r.handleConnect(w, req)
		return
	}

	r.mu.RLock()
	session := r.active
	r.mu.RUnlock()

	start := time.Now()

	if isWebSocketUpgrade(req) {
		r.handleWebSocketProxy(w, req, session, start)
		return
	}

	targetURL := req.URL.String()
	if !req.URL.IsAbs() {
		targetURL = "http://" + req.Host + req.URL.String()
	}
	if err := validateProxyTargetURL(targetURL); err != nil {
		r.noteRejectedTarget(session, proxyTargetHostname(targetURL), err)
		http.Error(w, "proxy target rejected: "+err.Error(), http.StatusForbidden)
		return
	}

	reqBodyCapture := newBodyCapture(requestCaptureLimit(session))
	upstreamBody := requestBodyReader(req.Body, reqBodyCapture)

	upstreamCtx, cancelUpstream := context.WithCancel(req.Context())
	defer cancelUpstream()
	proxyReq, err := http.NewRequestWithContext(upstreamCtx, req.Method, targetURL, upstreamBody)
	if err != nil {
		http.Error(w, "proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}
	proxyReq.ContentLength = req.ContentLength

	for k, vv := range req.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}
	stripProxyRequestHeaders(proxyReq.Header, false)

	resp, err := r.proxyClient.Do(proxyReq)
	if err != nil {
		r.noteRejectedTarget(session, proxyReq.URL.Hostname(), err)
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if isStreamingResponse(resp) {
		stopClientCloseWatch := make(chan struct{})
		go func() {
			select {
			case <-req.Context().Done():
				resp.Body.Close()
			case <-stopClientCloseWatch:
			}
		}()
		r.streamAndRecordResponse(w, func() {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}, session, req, reqBodyCapture.bytes(), resp, start, recordOptions{
			requestBodySizeBytes:     boundedInt(reqBodyCapture.size()),
			requestBodySizeKnown:     true,
			forceRequestBodyTruncate: reqBodyCapture.truncated(),
		})
		close(stopClientCloseWatch)
		return
	}

	boundedBody := &inactivityTimeoutBody{
		body:    resp.Body,
		timeout: r.responseBodyInactivityTimeout(),
		cancel:  cancelUpstream,
	}
	respBody, respBodySize, respBodyTruncated, _ := copyAndCaptureBody(w, boundedBody, responseCaptureLimit(session))
	elapsed := time.Since(start)

	if session != nil {
		r.recordRequestWithOptions(session, req, reqBodyCapture.bytes(), resp, respBody, elapsed, recordOptions{
			requestStartedAt:          start,
			requestBodySizeBytes:      boundedInt(reqBodyCapture.size()),
			requestBodySizeKnown:      true,
			forceRequestBodyTruncate:  reqBodyCapture.truncated(),
			forceResponseBodyTruncate: respBodyTruncated,
			responseBodySizeBytes:     boundedInt(respBodySize),
			responseBodySizeKnown:     true,
		})
	}
}

func (r *Recorder) acquireProxySlot() bool {
	if r == nil || r.proxySlots == nil {
		return true
	}
	select {
	case r.proxySlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *Recorder) releaseProxySlot() {
	if r == nil || r.proxySlots == nil {
		return
	}
	select {
	case <-r.proxySlots:
	default:
	}
}

func (r *Recorder) handleConnect(w http.ResponseWriter, req *http.Request) {
	r.handleConnectMITM(w, req)
}

func isStreamingResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return true
	}
	if resp.ContentLength >= 0 || resp.Header.Get("Content-Length") != "" {
		return false
	}
	for _, enc := range resp.TransferEncoding {
		if strings.EqualFold(enc, "chunked") {
			return true
		}
	}
	for _, enc := range strings.Split(resp.Header.Get("Transfer-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(enc), "chunked") {
			return true
		}
	}
	return false
}

func (r *Recorder) streamAndRecordResponse(w io.Writer, flush func(), session *Session, req *http.Request, reqBody []byte, resp *http.Response, startedAt time.Time, opts recordOptions) {
	limit := responseCaptureLimit(session)
	opts.requestStartedAt = startedAt

	var (
		// mu guards the capture, which the snapshot reads from the timer.
		mu       sync.Mutex
		captured []byte
		wireSize int64
		// recordMu orders the snapshot and the final record, so the final
		// record knows which request, if any, the snapshot recorded.
		recordMu    sync.Mutex
		snapshotted bool
		finished    bool
		snapshotSeq int
	)
	capturedSoFar := func() ([]byte, int64) {
		mu.Lock()
		defer mu.Unlock()
		return append([]byte(nil), captured...), wireSize
	}
	withResponse := func(truncated bool, size int64) recordOptions {
		responseOpts := opts
		responseOpts.forceResponseBodyTruncate = truncated
		responseOpts.responseBodySizeBytes = boundedInt(size)
		responseOpts.responseBodySizeKnown = true
		return responseOpts
	}
	// snapshot records the response while it still streams, so a stream that
	// never ends (an event stream, a long poll) still appears in the
	// recording. Its body is incomplete, so it is marked truncated.
	snapshot := func() {
		if session == nil {
			return
		}
		recordMu.Lock()
		defer recordMu.Unlock()
		if snapshotted || finished {
			return
		}
		snapshotted = true
		body, size := capturedSoFar()
		snapshotSeq = r.recordRequestWithOptions(session, req, reqBody, resp, body, time.Since(startedAt), withResponse(true, size))
	}
	// finish records how the response ended. A body that reached EOF with
	// every byte captured is complete; the request a snapshot recorded while
	// it streamed is updated to match.
	finish := func(reachedEOF bool) {
		if session == nil {
			return
		}
		recordMu.Lock()
		defer recordMu.Unlock()
		finished = true
		body, size := capturedSoFar()
		if snapshotted {
			if snapshotSeq != 0 {
				r.finishStreamedResponse(session, snapshotSeq, req, resp, body, size, reachedEOF, time.Since(startedAt))
			}
			return
		}
		r.recordRequestWithOptions(session, req, reqBody, resp, body, time.Since(startedAt), withResponse(streamTruncated(body, size, reachedEOF), size))
	}
	timer := time.AfterFunc(r.streamingSnapshotDelayOrDefault(), snapshot)
	defer timer.Stop()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := w.Write(chunk); err != nil {
				// The client went away; the rest of the body was never read.
				finish(false)
				return
			}
			if flush != nil {
				flush()
			}

			captureFull := false
			mu.Lock()
			wireSize += int64(n)
			if len(captured) < limit {
				remaining := limit - len(captured)
				if remaining > len(chunk) {
					remaining = len(chunk)
				}
				captured = append(captured, chunk[:remaining]...)
				captureFull = len(captured) >= limit
			}
			mu.Unlock()
			if captureFull {
				snapshot()
			}
		}
		if readErr != nil {
			finish(readErr == io.EOF)
			return
		}
	}
}

// streamTruncated reports whether a streamed body is missing bytes: it
// ended before EOF, or more arrived than were captured.
func streamTruncated(captured []byte, size int64, reachedEOF bool) bool {
	return !reachedEOF || size > int64(len(captured))
}

func (r *Recorder) streamingSnapshotDelayOrDefault() time.Duration {
	if r != nil && r.streamingSnapshotDelay > 0 {
		return r.streamingSnapshotDelay
	}
	return streamingRecordDelay
}

func responseCaptureLimit(session *Session) int {
	if session != nil && session.Filters.BodyCaptureLimitBytes > 0 {
		return session.Filters.BodyCaptureLimitBytes
	}
	return defaultBodyCaptureLimitBytes
}

func requestCaptureLimit(session *Session) int {
	return responseCaptureLimit(session)
}

type bodyCapture struct {
	limit int
	mu    sync.Mutex
	buf   []byte
	total int64
}

func newBodyCapture(limit int) *bodyCapture {
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	return &bodyCapture{limit: limit}
}

func (c *bodyCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(len(p))
	if len(c.buf) < c.limit {
		remaining := c.limit - len(c.buf)
		if remaining > len(p) {
			remaining = len(p)
		}
		c.buf = append(c.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (c *bodyCapture) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

func (c *bodyCapture) size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func (c *bodyCapture) truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total > int64(len(c.buf))
}

func requestBodyReader(body io.ReadCloser, capture *bodyCapture) io.ReadCloser {
	if body == nil {
		return http.NoBody
	}
	return &teeReadCloser{Reader: io.TeeReader(body, capture), closer: body}
}

type teeReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *teeReadCloser) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

func copyAndCaptureBody(dst io.Writer, src io.Reader, limit int) ([]byte, int64, bool, error) {
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	var (
		captured  []byte
		wireSize  int64
		truncated bool
		firstErr  error
	)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			written, writeErr := dst.Write(chunk)
			wireSize += int64(written)
			if len(captured) < limit {
				remaining := limit - len(captured)
				if remaining > written {
					remaining = written
				}
				captured = append(captured, chunk[:remaining]...)
			}
			if written < n {
				truncated = true
				firstErr = io.ErrShortWrite
				break
			}
			if len(captured) >= limit && wireSize > int64(limit) {
				truncated = true
			}
			if writeErr != nil {
				truncated = true
				firstErr = writeErr
				break
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				truncated = true
				firstErr = readErr
			}
			break
		}
	}
	return captured, wireSize, truncated, firstErr
}

func boundedInt(n int64) int {
	if n <= 0 {
		return 0
	}
	maxInt := int64(int(^uint(0) >> 1))
	if n > maxInt {
		return int(maxInt)
	}
	return int(n)
}

func (r *Recorder) recordRequest(s *Session, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, elapsed time.Duration) int {
	return r.recordRequestWithOptions(s, req, reqBody, resp, respBody, elapsed, recordOptions{requestStartedAt: time.Now().Add(-elapsed)})
}

func (r *Recorder) recordRequestStarted(s *Session, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, elapsed time.Duration, requestStartedAt time.Time) int {
	return r.recordRequestWithOptions(s, req, reqBody, resp, respBody, elapsed, recordOptions{requestStartedAt: requestStartedAt})
}

type recordOptions struct {
	requestStartedAt          time.Time
	forceRequestBodyTruncate  bool
	requestBodySizeBytes      int
	requestBodySizeKnown      bool
	forceResponseBodyTruncate bool
	responseBodySizeBytes     int
	responseBodySizeKnown     bool
}

func (r *Recorder) recordRequestWithOptions(s *Session, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, elapsed time.Duration, opts recordOptions) int {
	s.mu.Lock()
	var event *SessionEvent
	notify := s.notify
	defer func() {
		s.mu.Unlock()
		if event != nil && notify != nil {
			notify(*event)
		}
	}()

	if s.Status != "recording" {
		return 0
	}
	if opts.requestStartedAt.IsZero() {
		opts.requestStartedAt = time.Now().Add(-elapsed)
	}
	if elapsed < 0 {
		elapsed = 0
	}

	parsed, parseErr := url.Parse(req.URL.String())
	if parseErr != nil || parsed == nil {
		parsed = req.URL
	}
	if parsed == nil {
		parsed = &url.URL{}
	}
	host := req.Host
	if parsed != nil && parsed.Host != "" {
		host = parsed.Host
	}

	if s.Filters.ExcludeStatic && isStaticResource(parsed) {
		return 0
	}

	// A host entry matches that host and its subdomains, as the browser
	// recorder's HAR filters do.
	if len(s.Filters.IncludeHosts) > 0 && hostpattern.Index(host, s.Filters.IncludeHosts) < 0 {
		return 0
	}
	if hostpattern.Index(host, s.Filters.ExcludeHosts) >= 0 {
		return 0
	}
	if len(s.Requests) >= maxRecordedRequests {
		event = s.noteRequestDropLocked("request-count retention limit")
		return 0
	}

	var thinkMs int64
	if !s.lastReqEnd.IsZero() {
		thinkMs = opts.requestStartedAt.Sub(s.lastReqEnd).Milliseconds()
		if thinkMs < 0 {
			thinkMs = 0
		}
	}
	responseEndedAt := opts.requestStartedAt.Add(elapsed)

	rawHeadersMulti := correlationHeaders(req.Header, s.Filters.RedactHeaders, true, s.Filters.CaptureCookies)
	rawHeaders := firstHeaderValues(rawHeadersMulti)
	headersMulti := sanitizeHeaders(req.Header, s.Filters.RedactHeaders, true, s.Filters.CaptureCookies)
	headers := firstHeaderValues(headersMulti)
	rawRespHeadersMulti := correlationHeaders(resp.Header, s.Filters.RedactHeaders, false, s.Filters.CaptureCookies)
	rawRespHeaders := firstHeaderValues(rawRespHeadersMulti)
	respHeadersMulti := sanitizeHeaders(resp.Header, s.Filters.RedactHeaders, false, s.Filters.CaptureCookies)

	ct := resp.Header.Get("Content-Type")
	respHeaders := firstHeaderValues(respHeadersMulti)
	respBodyStr, respBodyEncoding, respBodyTruncated, capture := recordedResponseContent(req.Context(), req.Method, resp, respBody, s.Filters.BodyCaptureLimitBytes, opts.forceResponseBodyTruncate)
	reqBodyStr, reqBodyTruncated, reqBodyEncoding := capturedRequestBody(reqBody, req.Header.Get("Content-Type"), s.Filters.BodyCaptureLimitBytes)
	if opts.forceRequestBodyTruncate {
		reqBodyTruncated = true
	}
	requestBodySizeBytes := len(reqBody)
	if opts.requestBodySizeKnown {
		requestBodySizeBytes = opts.requestBodySizeBytes
	}
	responseBodySizeBytes := len(respBody)
	if opts.responseBodySizeKnown {
		responseBodySizeBytes = opts.responseBodySizeBytes
	}

	resourceType := classifyResource(parsed, ct)
	rec := RecordedRequest{
		Seq:                     s.seq + 1,
		Timestamp:               opts.requestStartedAt,
		Method:                  req.Method,
		RequestHTTPVersion:      observedHTTPVersion(req.Proto, req.ProtoMajor, req.ProtoMinor),
		ResponseHTTPVersion:     observedHTTPVersion(resp.Proto, resp.ProtoMajor, resp.ProtoMinor),
		URL:                     req.URL.String(),
		Scheme:                  parsed.Scheme,
		Path:                    parsed.Path,
		QueryString:             parsed.RawQuery,
		Host:                    host,
		Headers:                 headers,
		HeadersMulti:            headersMulti,
		RawHeaders:              rawHeaders,
		RawHeadersMulti:         rawHeadersMulti,
		Body:                    reqBodyStr,
		BodyEncoding:            reqBodyEncoding,
		BodySizeBytes:           requestBodySizeBytes,
		BodyTruncated:           reqBodyTruncated,
		ContentType:             req.Header.Get("Content-Type"),
		StatusCode:              resp.StatusCode,
		ResponseTimeMs:          float64(elapsed.Microseconds()) / 1000.0,
		ResponseBody:            respBodyStr,
		ResponseBodyEncoding:    respBodyEncoding,
		ResponseBodySizeBytes:   responseBodySizeBytes,
		ResponseBodyTruncated:   respBodyTruncated,
		ResponseContentCapture:  capture,
		ResponseHeaders:         respHeaders,
		ResponseHeadersMulti:    respHeadersMulti,
		RawResponseHeaders:      rawRespHeaders,
		RawResponseHeadersMulti: rawRespHeadersMulti,
		ThinkTimeMs:             thinkMs,
		IsStatic:                isStaticResource(parsed),
		ResourceType:            resourceType,
	}
	remainingBodyBytes := maxRecordedBodyBytes - s.storedBodyBytes
	storedBodies := []struct {
		body      *string
		truncated *bool
	}{
		{body: &rec.Body, truncated: &rec.BodyTruncated},
		{body: &rec.ResponseBody, truncated: &rec.ResponseBodyTruncated},
	}
	bodyBytesAdded := 0
	for _, stored := range storedBodies {
		if len(*stored.body) > remainingBodyBytes {
			*stored.body = ""
			*stored.truncated = true
			continue
		}
		remainingBodyBytes -= len(*stored.body)
		bodyBytesAdded += len(*stored.body)
	}
	dropIncompleteResponseContent(&rec)
	requestBytes := estimateRecordedRequestBytes(rec)
	if requestBytes > maxRecordedSessionBytes-s.retainedBytes {
		event = s.noteRequestDropLocked("session byte retention limit")
		return 0
	}

	s.seq++
	rec.Seq = s.seq
	if s.lastReqTime.IsZero() || opts.requestStartedAt.After(s.lastReqTime) {
		s.lastReqTime = opts.requestStartedAt
	}
	if s.lastReqEnd.IsZero() || responseEndedAt.After(s.lastReqEnd) {
		s.lastReqEnd = responseEndedAt
	}
	if s.BaseURL == "" && parsed != nil {
		s.BaseURL = parsed.Scheme + "://" + parsed.Host
	}
	s.storedBodyBytes += bodyBytesAdded
	s.retainedBytes += requestBytes
	s.Requests = append(s.Requests, rec)

	if s.notify != nil {
		eventRequest := recordingEventRequest(rec)
		event = &SessionEvent{
			Type:      "request_recorded",
			SessionID: s.ID,
			Request:   &eventRequest,
		}
	}
	return rec.Seq
}

// recordedResponseContent derives a recorded response's stored body, its
// encoding and truncation, and its content capture from the captured bytes.
func recordedResponseContent(ctx context.Context, method string, resp *http.Response, raw []byte, limit int, truncated bool) (string, string, bool, requestimport.ResponseContentCapture) {
	// The browser receives the original wire bytes. Only a complete explicitly
	// wire-captured snapshot can become decoded logical response content.
	decoded, capture := decodeRecordedResponse(ctx, method, resp, raw, limit, truncated)
	body, bodyTruncated, encoding := capturedBody(decoded, resp.Header.Get("Content-Type"), limit)
	if capture.Complete() && encoding == "" && !requestimport.IsResponseContentText(decoded) {
		body, encoding = base64.StdEncoding.EncodeToString(decoded), "base64"
	}
	if capture.Complete() && encoding == "base64" {
		capture.BodyKind = "binary"
	}
	if !capture.Complete() {
		body, encoding = "", ""
		bodyTruncated = capture.ReasonCode == "response_content_truncated" || capture.ReasonCode == "response_content_decode_limit"
	}
	return body, encoding, bodyTruncated, capture
}

// dropIncompleteResponseContent keeps a response whose stored body was cut
// from claiming complete content.
func dropIncompleteResponseContent(rec *RecordedRequest) {
	if rec.ResponseBodyTruncated && rec.ResponseContentCapture.Complete() {
		rec.ResponseBody, rec.ResponseBodyEncoding = "", ""
		rec.ResponseContentCapture = requestimport.UnavailableResponseContent("wire", "response_content_truncated")
	}
}

// finishStreamedResponse completes the request a streamed response's
// snapshot recorded, now that the response has ended: its body, size,
// truncation and time become what was captured. A stopped session keeps
// the snapshot it had when it stopped.
func (r *Recorder) finishStreamedResponse(s *Session, seq int, req *http.Request, resp *http.Response, respBody []byte, size int64, reachedEOF bool, elapsed time.Duration) {
	s.mu.Lock()
	if s.Status != "recording" && s.Status != "paused" {
		s.mu.Unlock()
		return
	}
	for i := range s.Requests {
		if s.Requests[i].Seq != seq {
			continue
		}
		previous := s.Requests[i]
		updated := previous
		updated.ResponseBody, updated.ResponseBodyEncoding, updated.ResponseBodyTruncated, updated.ResponseContentCapture = recordedResponseContent(req.Context(), req.Method, resp, respBody, s.Filters.BodyCaptureLimitBytes, streamTruncated(respBody, size, reachedEOF))
		updated.ResponseBodySizeBytes = boundedInt(size)
		updated.ResponseTimeMs = float64(elapsed.Microseconds()) / 1000.0
		storedBodyBytes := s.storedBodyBytes - min(s.storedBodyBytes, len(previous.ResponseBody))
		baseBytes := s.retainedBytes - min(s.retainedBytes, estimateRecordedRequestBytes(previous))
		if len(updated.ResponseBody) > maxRecordedBodyBytes-storedBodyBytes || estimateRecordedRequestBytes(updated) > maxRecordedSessionBytes-baseBytes {
			updated.ResponseBody, updated.ResponseBodyEncoding, updated.ResponseBodyTruncated = "", "", true
		}
		dropIncompleteResponseContent(&updated)
		newBytes := estimateRecordedRequestBytes(updated)
		if newBytes > maxRecordedSessionBytes-baseBytes {
			s.mu.Unlock()
			return
		}
		s.storedBodyBytes = storedBodyBytes + len(updated.ResponseBody)
		s.retainedBytes = baseBytes + newBytes
		// A stream the client abandoned (an event stream or long poll closed
		// on navigation) did not end a response the user waited for, so the
		// next request's think time still counts from before it.
		if responseEndedAt := updated.Timestamp.Add(elapsed); reachedEOF && responseEndedAt.After(s.lastReqEnd) {
			s.lastReqEnd = responseEndedAt
		}
		s.Requests[i] = updated
		eventRequest := recordingEventRequest(updated)
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify(SessionEvent{Type: "request_updated", SessionID: s.ID, Request: &eventRequest})
		}
		return
	}
	s.mu.Unlock()
}

func (s *Session) noteRequestDropLocked(reason string) *SessionEvent {
	if s == nil {
		return nil
	}
	s.DroppedRequests++
	s.Truncated = true
	if s.requestDropWarned {
		return nil
	}
	s.requestDropWarned = true
	warning := fmt.Sprintf("recording truncated: requests are being dropped after reaching the %s", reason)
	s.Warnings = append(s.Warnings, warning)
	return &SessionEvent{Type: "recording_warning", SessionID: s.ID, Warning: warning}
}

func (s *Session) noteWebSocketDropLocked() *SessionEvent {
	if s == nil {
		return nil
	}
	s.DroppedWebSocketMessages++
	s.Truncated = true
	if s.webSocketDropWarned {
		return nil
	}
	s.webSocketDropWarned = true
	warning := "recording truncated: WebSocket messages are being dropped after reaching a capture limit"
	s.Warnings = append(s.Warnings, warning)
	return &SessionEvent{Type: "recording_warning", SessionID: s.ID, Warning: warning}
}

func (r *Recorder) appendWebSocketMessage(s *Session, seq int, msg RecordedWebSocketMessage) {
	if s == nil || seq == 0 {
		return
	}

	s.mu.Lock()
	if s.Status != "recording" {
		s.mu.Unlock()
		return
	}
	for i := range s.Requests {
		if s.Requests[i].Seq != seq {
			continue
		}
		messageBytes := estimateRecordedWebSocketMessageBytes(msg) + 32
		if s.webSocketMessages >= maxRecordedWebSocketMessages || s.webSocketPayloadBytes+len(msg.Payload) > maxRecordedWebSocketPayloadBytes || messageBytes > maxRecordedSessionBytes-s.retainedBytes {
			event := s.noteWebSocketDropLocked()
			notify := s.notify
			s.mu.Unlock()
			if event != nil && notify != nil {
				notify(*event)
			}
			return
		}
		s.Requests[i].WebSocketMessages = append(s.Requests[i].WebSocketMessages, msg)
		s.webSocketMessages++
		s.webSocketPayloadBytes += len(msg.Payload)
		s.retainedBytes += messageBytes
		updated := recordingEventRequest(s.Requests[i])
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify(SessionEvent{Type: "request_updated", SessionID: s.ID, Request: &updated})
		}
		return
	}
	s.mu.Unlock()
}

func estimateRecordedRequestBytes(request RecordedRequest) int64 {
	bytes := int64(1024)
	for _, value := range []string{
		request.Method, request.URL, request.Scheme, request.Path, request.QueryString,
		request.RequestHTTPVersion, request.ResponseHTTPVersion,
		request.Host, request.Body, request.BodyEncoding, request.ContentType,
		request.ResponseBody, request.ResponseBodyEncoding, request.StepName,
		request.ResponseContentCapture.Captured, request.ResponseContentCapture.Stored, request.ResponseContentCapture.State, request.ResponseContentCapture.ReasonCode,
		request.TransactionName, request.Note, request.ResourceType,
	} {
		bytes += int64(len(value))
	}
	bytes += estimateRecorderStringMapBytes(request.Headers)
	bytes += estimateRecorderStringMapBytes(request.RawHeaders)
	bytes += estimateRecorderStringMapBytes(request.ResponseHeaders)
	bytes += estimateRecorderStringMapBytes(request.RawResponseHeaders)
	bytes += estimateRecorderStringSliceMapBytes(request.HeadersMulti)
	bytes += estimateRecorderStringSliceMapBytes(request.RawHeadersMulti)
	bytes += estimateRecorderStringSliceMapBytes(request.ResponseHeadersMulti)
	bytes += estimateRecorderStringSliceMapBytes(request.RawResponseHeadersMulti)
	bytes += int64(len(request.WebSocketMessages)) * 32
	for _, message := range request.WebSocketMessages {
		bytes += estimateRecordedWebSocketMessageBytes(message)
	}
	return bytes
}

func estimateRecordedWebSocketMessageBytes(message RecordedWebSocketMessage) int64 {
	return int64(256 + len(message.Direction) + len(message.Payload) + len(message.PayloadEncoding))
}

func estimateRecorderStringMapBytes(values map[string]string) int64 {
	bytes := int64(len(values) * 64)
	for key, value := range values {
		bytes += int64(len(key) + len(value))
	}
	return bytes
}

func estimateRecorderStringSliceMapBytes(values map[string][]string) int64 {
	bytes := int64(len(values) * 64)
	for key, entries := range values {
		bytes += int64(len(key) + len(entries)*32)
		for _, value := range entries {
			bytes += int64(len(value))
		}
	}
	return bytes
}

func recordingEventRequest(request RecordedRequest) RecordedRequest {
	request.Body = ""
	request.ResponseBody = ""
	if len(request.WebSocketMessages) > 0 {
		messages := make([]RecordedWebSocketMessage, len(request.WebSocketMessages))
		copy(messages, request.WebSocketMessages)
		for i := range messages {
			messages[i].Payload = ""
		}
		request.WebSocketMessages = messages
	}
	return request
}

func correlationHeaders(headers http.Header, extraRedact []string, skipProxy bool, captureCookies bool) map[string][]string {
	redact := map[string]bool{}
	if !captureCookies {
		redact["cookie"] = true
		redact["set-cookie"] = true
	}
	for _, h := range extraRedact {
		redact[strings.ToLower(strings.TrimSpace(h))] = true
	}

	result := make(map[string][]string)
	for k, vv := range headers {
		lk := strings.ToLower(k)
		if isRecorderControlHeader(k) {
			continue
		}
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

func sanitizeHeaders(headers http.Header, extraRedact []string, skipProxy bool, captureCookies bool) map[string][]string {
	redact := map[string]bool{}
	for key := range headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if captureCookies && (lower == "cookie" || lower == "set-cookie") {
			continue
		}
		if paramize.SensitiveHeaderKey(key) {
			redact[lower] = true
		}
	}
	if !captureCookies {
		redact["cookie"] = true
		redact["set-cookie"] = true
	}
	for _, h := range extraRedact {
		redact[strings.ToLower(strings.TrimSpace(h))] = true
	}

	result := make(map[string][]string)
	for k, vv := range headers {
		lk := strings.ToLower(k)
		if isRecorderControlHeader(k) {
			continue
		}
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

// decodeBody is retained as a narrow compatibility helper. Failed decoding
// never returns a partially decoded successful result.
func decodeBody(raw []byte, encoding string, limit int) ([]byte, bool) {
	if strings.TrimSpace(encoding) == "" || strings.EqualFold(strings.TrimSpace(encoding), "identity") {
		return raw, false
	}
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	decoded, err := contentdecode.Decode(context.Background(), raw, []string{encoding}, contentdecode.DefaultLimits(int64(limit)))
	if err != nil {
		return raw, false
	}
	return decoded, true
}

func responseContentEncodingValues(headers http.Header) []string {
	keys := make([]string, 0)
	for name := range headers {
		if strings.EqualFold(name, "Content-Encoding") {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	var values []string
	for _, name := range keys {
		values = append(values, headers[name]...)
	}
	return values
}

func decodeRecordedResponse(ctx context.Context, method string, resp *http.Response, raw []byte, limit int, truncated bool) ([]byte, requestimport.ResponseContentCapture) {
	if strings.EqualFold(method, http.MethodHead) || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		if len(raw) != 0 {
			return nil, requestimport.UnavailableResponseContent("wire", "malformed_response_content_provenance")
		}
		return nil, requestimport.CompleteResponseContent("wire", 0)
	}
	if truncated {
		return nil, requestimport.UnavailableResponseContent("wire", "response_content_truncated")
	}
	if resp.Uncompressed {
		return nil, requestimport.UnavailableResponseContent("unknown", "response_content_provenance_unavailable")
	}
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	decoded, err := contentdecode.Decode(ctx, raw, responseContentEncodingValues(resp.Header), contentdecode.DefaultLimits(int64(limit)))
	if err != nil {
		reason := "invalid_response_content_encoding"
		switch contentdecode.Kind(err) {
		case contentdecode.UnsupportedEncoding:
			reason = "unsupported_response_content_encoding"
		case contentdecode.Truncated:
			reason = "response_content_truncated"
		case contentdecode.EncodedLimit, contentdecode.DecodedLimit, contentdecode.WorkLimit, contentdecode.LayerLimit, contentdecode.WindowLimit, contentdecode.MemoryLimit:
			reason = "response_content_decode_limit"
		case contentdecode.Canceled:
			reason = "response_content_decode_canceled"
		}
		return nil, requestimport.UnavailableResponseContent("wire", reason)
	}
	return decoded, requestimport.CompleteResponseContent("wire", len(decoded))
}

func capturedBody(body []byte, contentType string, limit int) (string, bool, string) {
	if len(body) == 0 {
		return "", false, ""
	}
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	captured := body
	truncated := false
	if len(body) > limit {
		captured = body[:limit]
		truncated = true
	}
	if !utf8.Valid(captured) && truncated && utf8.Valid(body) {
		// A byte capture limit can bisect the final UTF-8 rune. Retain the
		// largest complete prefix so JSON serialization and generated scripts do
		// not replace the split bytes with U+FFFD.
		end := len(captured)
		for end > 0 && len(captured)-end < utf8.UTFMax && !utf8.Valid(captured[:end]) {
			end--
		}
		if utf8.Valid(captured[:end]) {
			captured = captured[:end]
		}
	}
	if !utf8.Valid(captured) {
		return base64.StdEncoding.EncodeToString(captured), truncated, "base64"
	}
	return string(captured), truncated, ""
}

func capturedRequestBody(body []byte, contentType string, limit int) (string, bool, string) {
	if len(body) == 0 {
		return "", false, ""
	}
	if isMultipartContentType(contentType) {
		if len(body) <= maxMultipartRequestCaptureBytes {
			return base64.StdEncoding.EncodeToString(body), false, "base64"
		}
		if limit <= 0 {
			limit = defaultBodyCaptureLimitBytes
		}
		if limit > maxMultipartRequestCaptureBytes {
			limit = maxMultipartRequestCaptureBytes
		}
		return base64.StdEncoding.EncodeToString(body[:limit]), true, "base64"
	}
	return capturedBody(body, contentType, limit)
}

func isMultipartContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return strings.HasPrefix(mediaType, "multipart/")
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

package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func init() {
	protocol.Register("http", func() protocol.Protocol {
		return &HTTP{}
	})
}

type HTTP struct {
	client    *http.Client
	transport *http.Transport
	baseURL   string
	bufPool   sync.Pool
}

func (h *HTTP) Name() string { return "http" }

func (h *HTTP) Init(ctx context.Context, config map[string]any) error {
	h.baseURL, _ = config["base_url"].(string)

	maxConnsPerHost := 100
	if v, ok := config["max_conns_per_host"].(float64); ok {
		maxConnsPerHost = int(v)
	}

	h.transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        maxConnsPerHost,
		MaxIdleConnsPerHost: maxConnsPerHost,
		MaxConnsPerHost:     maxConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
	}

	jar, _ := cookiejar.New(nil)
	h.client = &http.Client{
		Transport: h.transport,
		Jar:       jar,
		Timeout:   60 * time.Second,
	}

	h.bufPool = sync.Pool{
		New: func() any { return new(bytes.Buffer) },
	}

	return nil
}

func (h *HTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:  action.Name,
		StartTime:   time.Now(),
		Extractions: make(map[string]string),
	}

	url := h.resolveURL(action.Target)

	var body io.Reader
	if len(action.Body) > 0 {
		body = bytes.NewReader(action.Body)
	}

	method := strings.ToUpper(action.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		return result, nil
	}

	for k, v := range action.Headers {
		req.Header.Set(k, v)
	}

	var dnsStart, connStart, tlsStart time.Time
	trace := &httptrace.ClientTrace{
		DNSStart:             func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(_ httptrace.DNSDoneInfo) { result.DNSTime = time.Since(dnsStart) },
		ConnectStart:         func(_, _ string) { connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { result.ConnectTime = time.Since(connStart) },
		TLSHandshakeStart:   func() { tlsStart = time.Now() },
		TLSHandshakeDone:    func(_ tls.ConnectionState, _ error) { result.TLSTime = time.Since(tlsStart) },
		GotFirstResponseByte: func() { result.TTFB = time.Since(result.StartTime) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	if len(action.Body) > 0 {
		result.BytesSent = int64(len(action.Body))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		return result, nil
	}

	buf := h.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer h.bufPool.Put(buf)

	n, _ := io.Copy(buf, resp.Body)
	resp.Body.Close()

	result.Duration = time.Since(result.StartTime)
	result.StatusCode = resp.StatusCode
	result.BytesReceived = n
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400

	respBody := buf.Bytes()
	for _, ext := range action.Extractors {
		val, err := extract(ext, resp.Header, respBody)
		if err == nil {
			result.Extractions[ext.Name] = val
		}
	}

	for _, assertion := range action.Assertions {
		if err := evaluate(assertion, result, respBody); err != nil {
			result.Success = false
			result.ErrorMessage = err.Error()
			break
		}
	}

	return result, nil
}

func (h *HTTP) Teardown(_ context.Context) error {
	h.transport.CloseIdleConnections()
	return nil
}

func (h *HTTP) HealthCheck(ctx context.Context) error {
	if h.baseURL == "" {
		return fmt.Errorf("no base_url configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, h.baseURL, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (h *HTTP) resolveURL(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	return strings.TrimRight(h.baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

package h3fixture

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

const (
	maxRequestBody  = 1 << 20
	maxResponseBody = 8 << 20
	maxSlow         = 5 * time.Second
	maxEvents       = 100
	partialBodySize = 64 << 10
)

// handler serves the fixture routes. Every request is recorded in the ledger
// when the handler returns, including handlers that abort.
func (s *Server) handler(listener string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", s.handleEcho)
	mux.HandleFunc("/effect", s.handleEffect)
	mux.HandleFunc("/partial", s.handlePartial)
	mux.HandleFunc("/redirect", handleRedirect)
	mux.HandleFunc("/status", handleStatus)
	mux.HandleFunc("/slow", handleSlow)
	mux.HandleFunc("/sse", handleSSE)
	mux.HandleFunc("/bytes", handleBytes)
	mux.HandleFunc("/cacheable", handleCacheable)
	mux.HandleFunc("/encoded", handleEncoded)
	mux.HandleFunc("/", s.handleIndex)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		body := &hashingReader{r: r.Body, h: sha256.New()}
		r.Body = body
		if v := s.altSvc.Load(); v != nil {
			if value := v.(string); value != "" {
				w.Header().Set("Alt-Svc", value)
			}
		}
		w.Header().Set("X-Fixture-Listener", listener)
		w.Header().Set("X-Fixture-Proto", r.Proto)
		misdirected := listener != "tcp" && take(&s.misdirect)
		defer func() {
			key := r.Header.Get("X-Ledger-Key")
			if key == "" {
				key = r.URL.Query().Get("key")
			}
			s.Ledger.record(Attempt{
				At:         started,
				Listener:   listener,
				Proto:      r.Proto,
				Method:     r.Method,
				Path:       r.URL.Path,
				Key:        key,
				BodyBytes:  body.n,
				BodySHA256: hex.EncodeToString(body.h.Sum(nil)),
				AltUsed:    r.Header.Get("Alt-Used"),
			})
		}()
		if misdirected {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type hashingReader struct {
	r io.ReadCloser
	h hash.Hash
	n int64
}

func (b *hashingReader) Read(p []byte) (int, error) {
	if b.r == nil {
		return 0, io.EOF
	}
	n, err := b.r.Read(p)
	b.h.Write(p[:n])
	b.n += int64(n)
	return n, err
}

func (b *hashingReader) Close() error {
	if b.r == nil {
		return nil
	}
	return b.r.Close()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"proto":    r.Proto,
		"listener": w.Header().Get("X-Fixture-Listener"),
		"method":   r.Method,
		"path":     r.URL.Path,
	})
}

func (s *Server) handleEcho(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(payload) > maxRequestBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	sum := sha256.Sum256(payload)
	headers := map[string][]string{}
	for name, values := range r.Header {
		headers[strings.ToLower(name)] = append([]string(nil), values...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proto":       r.Proto,
		"method":      r.Method,
		"body_bytes":  len(payload),
		"body_sha256": hex.EncodeToString(sum[:]),
		"headers":     headers,
		"host":        r.Host,
	})
}

func ledgerKey(r *http.Request) string {
	if key := r.Header.Get("X-Ledger-Key"); key != "" {
		return key
	}
	return r.URL.Query().Get("key")
}

// handleEffect applies one countable side effect, as a non-idempotent write
// would. The ledger proves whether a client duplicated it.
func (s *Server) handleEffect(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxRequestBody))
	key := ledgerKey(r)
	if key == "" {
		http.Error(w, "missing ledger key", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"proto": r.Proto, "effect_count": s.Ledger.applyEffect(key)})
}

// handlePartial applies the side effect, sends part of a response and then
// aborts, leaving the client unsure whether the write took effect.
func (s *Server) handlePartial(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxRequestBody))
	if key := ledgerKey(r); key != "" {
		s.Ledger.applyEffect(key)
	}
	w.Header().Set("Content-Length", strconv.Itoa(partialBodySize))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(make([]byte, partialBodySize/2))
	if streamer, ok := w.(http3.HTTPStreamer); ok {
		str := streamer.HTTPStream()
		str.CancelRead(quic.StreamErrorCode(http3.ErrCodeInternalError))
		str.CancelWrite(quic.StreamErrorCode(http3.ErrCodeInternalError))
		return
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	panic(http.ErrAbortHandler)
}

func handleRedirect(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	if to == "" {
		http.Error(w, "missing to", http.StatusBadRequest)
		return
	}
	code := http.StatusFound
	if raw := r.URL.Query().Get("code"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 300 || parsed > 399 {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		code = parsed
	}
	http.Redirect(w, r, to, code)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(r.URL.Query().Get("code"))
	if err != nil || code < 200 || code > 599 {
		http.Error(w, "bad code", http.StatusBadRequest)
		return
	}
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, "status %d\n", code)
}

func handleSlow(w http.ResponseWriter, r *http.Request) {
	ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
	delay := min(time.Duration(max(ms, 0))*time.Millisecond, maxSlow)
	select {
	case <-time.After(delay):
	case <-r.Context().Done():
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proto": r.Proto, "delayed_ms": delay.Milliseconds()})
}

func handleSSE(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	n = min(max(n, 1), maxEvents)
	interval, _ := strconv.Atoi(r.URL.Query().Get("interval_ms"))
	pause := min(time.Duration(max(interval, 0))*time.Millisecond, time.Second)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for i := 1; i <= n; i++ {
		if _, err := fmt.Fprintf(w, "id: %d\ndata: event-%d\n\n", i, i); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		if i < n && pause > 0 {
			select {
			case <-time.After(pause):
			case <-r.Context().Done():
				return
			}
		}
	}
}

func handleBytes(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	n = min(max(n, 0), maxResponseBody)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(n))
	w.WriteHeader(http.StatusOK)
	chunk := []byte(strings.Repeat("a", 32<<10))
	for remaining := n; remaining > 0; {
		size := min(remaining, len(chunk))
		if _, err := w.Write(chunk[:size]); err != nil {
			return
		}
		remaining -= size
	}
}

// handleCacheable returns a response any HTTP cache may keep for an hour.
func handleCacheable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"proto": r.Proto, "cacheable": true})
}

// EncodedBody is the plain text /encoded compresses.
const EncodedBody = `{"encoded":true,"text":"HTTP/3 carries the same content codings as TCP"}`

// handleEncoded returns EncodedBody compressed with the coding named by the
// "coding" query parameter: gzip, br or zstd.
func handleEncoded(w http.ResponseWriter, r *http.Request) {
	coding := r.URL.Query().Get("coding")
	var buf bytes.Buffer
	var encoder io.WriteCloser
	switch coding {
	case "gzip":
		encoder = gzip.NewWriter(&buf)
	case "br":
		encoder = brotli.NewWriter(&buf)
	case "zstd":
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		encoder = zw
	default:
		http.Error(w, "coding must be gzip, br or zstd", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(encoder, EncodedBody)
	_ = encoder.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", coding)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}

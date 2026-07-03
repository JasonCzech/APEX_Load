package recorder

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	webSocketOpcodeContinuation = 0
	webSocketOpcodeText         = 1
	webSocketOpcodeBinary       = 2
	webSocketOpcodeClose        = 8

	webSocketDirectionClientToServer = "client_to_server"
	webSocketDirectionServerToClient = "server_to_client"
)

type webSocketFrame struct {
	raw     []byte
	opcode  int
	payload []byte
}

func isWebSocketUpgrade(req *http.Request) bool {
	if req == nil {
		return false
	}
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket") &&
		headerHasToken(req.Header.Get("Connection"), "upgrade")
}

func headerHasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func (r *Recorder) handleWebSocketProxy(w http.ResponseWriter, req *http.Request, session *Session, start time.Time) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket proxy requires connection hijacking", http.StatusInternalServerError)
		return
	}

	targetURL, err := webSocketRequestURL(req, false, req.Host)
	if err != nil {
		http.Error(w, "websocket target error: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := validateProxyTargetURL(targetURL.String()); err != nil {
		http.Error(w, "websocket target rejected: "+err.Error(), http.StatusForbidden)
		return
	}
	upstreamAddr := hostWithDefaultPort(targetURL.Host, "80")
	upstreamConn, err := proxySafeDialer(30*time.Second).Dial("tcp", upstreamAddr)
	if err != nil {
		http.Error(w, "websocket upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}

	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		_ = upstreamConn.Close()
		return
	}

	upstreamReq := cloneWebSocketUpstreamRequest(req, targetURL)
	if err := upstreamReq.Write(upstreamConn); err != nil {
		slog.Debug("websocket upstream request write failed", "url", targetURL.String(), "err", err)
		writeHTTPError(clientConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		_ = upstreamConn.Close()
		_ = clientConn.Close()
		return
	}

	upstreamReader := bufio.NewReader(upstreamConn)
	resp, err := http.ReadResponse(upstreamReader, upstreamReq)
	if err != nil {
		slog.Debug("websocket upstream response read failed", "url", targetURL.String(), "err", err)
		writeHTTPError(clientConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		_ = upstreamConn.Close()
		_ = clientConn.Close()
		return
	}

	if err := resp.Write(clientConn); err != nil {
		_ = upstreamConn.Close()
		_ = clientConn.Close()
		return
	}

	recordReq := cloneWebSocketRecordRequest(req, targetURL)
	seq := 0
	if session != nil {
		seq = r.recordRequestStarted(session, recordReq, nil, resp, nil, time.Since(start), start)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = upstreamConn.Close()
		_ = clientConn.Close()
		return
	}

	r.tunnelWebSocket(clientConn, clientRW.Reader, upstreamConn, upstreamReader, session, seq)
}

func (r *Recorder) forwardWebSocketHTTPS(tlsConn net.Conn, clientReader *bufio.Reader, clientReq *http.Request, hostname, connectHost string, session *Session, start time.Time) {
	targetURL, err := webSocketRequestURL(clientReq, true, hostname)
	if err != nil {
		writeHTTPError(tlsConn, http.StatusBadGateway, "websocket target error: "+err.Error())
		return
	}
	if err := validateProxyTargetURL(targetURL.String()); err != nil {
		writeHTTPError(tlsConn, http.StatusForbidden, "websocket target rejected: "+err.Error())
		return
	}

	dialAddr := hostWithDefaultPort(connectHost, "443")
	upstreamConn, err := tls.DialWithDialer(proxySafeDialer(30*time.Second), "tcp", dialAddr, &tls.Config{
		ServerName:         hostname,
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Debug("websocket TLS upstream dial failed", "host", dialAddr, "err", err)
		writeHTTPError(tlsConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		return
	}

	upstreamReq := cloneWebSocketUpstreamRequest(clientReq, targetURL)
	if err := upstreamReq.Write(upstreamConn); err != nil {
		slog.Debug("websocket TLS upstream request write failed", "url", targetURL.String(), "err", err)
		writeHTTPError(tlsConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		_ = upstreamConn.Close()
		return
	}

	upstreamReader := bufio.NewReader(upstreamConn)
	resp, err := http.ReadResponse(upstreamReader, upstreamReq)
	if err != nil {
		slog.Debug("websocket TLS upstream response read failed", "url", targetURL.String(), "err", err)
		writeHTTPError(tlsConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		_ = upstreamConn.Close()
		return
	}

	if err := resp.Write(tlsConn); err != nil {
		_ = upstreamConn.Close()
		return
	}

	recordReq := cloneWebSocketRecordRequest(clientReq, targetURL)
	seq := 0
	if session != nil {
		seq = r.recordRequestStarted(session, recordReq, nil, resp, nil, time.Since(start), start)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = upstreamConn.Close()
		return
	}

	r.tunnelWebSocket(tlsConn, clientReader, upstreamConn, upstreamReader, session, seq)
}

func (r *Recorder) forwardWebSocketPlainConnect(clientConn net.Conn, clientReader *bufio.Reader, clientReq *http.Request, _ string, connectHost string, session *Session, start time.Time) {
	targetURL, err := webSocketRequestURL(clientReq, false, connectHost)
	if err != nil {
		writeHTTPError(clientConn, http.StatusBadGateway, "websocket target error: "+err.Error())
		return
	}
	if err := validateProxyTargetURL(targetURL.String()); err != nil {
		writeHTTPError(clientConn, http.StatusForbidden, "websocket target rejected: "+err.Error())
		return
	}

	upstreamAddr := hostWithDefaultPort(connectHost, "80")
	upstreamConn, err := proxySafeDialer(30*time.Second).Dial("tcp", upstreamAddr)
	if err != nil {
		slog.Debug("websocket CONNECT upstream dial failed", "host", upstreamAddr, "err", err)
		writeHTTPError(clientConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		return
	}

	upstreamReq := cloneWebSocketUpstreamRequest(clientReq, targetURL)
	if err := upstreamReq.Write(upstreamConn); err != nil {
		slog.Debug("websocket CONNECT upstream request write failed", "url", targetURL.String(), "err", err)
		writeHTTPError(clientConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		_ = upstreamConn.Close()
		return
	}

	upstreamReader := bufio.NewReader(upstreamConn)
	resp, err := http.ReadResponse(upstreamReader, upstreamReq)
	if err != nil {
		slog.Debug("websocket CONNECT upstream response read failed", "url", targetURL.String(), "err", err)
		writeHTTPError(clientConn, http.StatusBadGateway, "websocket upstream error: "+err.Error())
		_ = upstreamConn.Close()
		return
	}

	if err := resp.Write(clientConn); err != nil {
		_ = upstreamConn.Close()
		return
	}

	recordReq := cloneWebSocketRecordRequest(clientReq, targetURL)
	seq := 0
	if session != nil {
		seq = r.recordRequestStarted(session, recordReq, nil, resp, nil, time.Since(start), start)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = upstreamConn.Close()
		return
	}

	r.tunnelWebSocket(clientConn, clientReader, upstreamConn, upstreamReader, session, seq)
}

func webSocketRequestURL(req *http.Request, secure bool, fallbackHost string) (*url.URL, error) {
	if req == nil {
		return nil, fmt.Errorf("missing request")
	}
	target := *req.URL
	if secure {
		target.Scheme = "wss"
	} else {
		target.Scheme = "ws"
	}
	if req.URL.Scheme == "wss" || req.URL.Scheme == "ws" {
		target.Scheme = req.URL.Scheme
	}
	if target.Host == "" {
		target.Host = req.Host
	}
	if target.Host == "" {
		target.Host = fallbackHost
	}
	if target.Host == "" {
		return nil, fmt.Errorf("missing websocket host")
	}
	if target.Path == "" {
		target.Path = "/"
	}
	return &target, nil
}

func cloneWebSocketUpstreamRequest(req *http.Request, target *url.URL) *http.Request {
	out := new(http.Request)
	*out = *req
	path := target.Path
	if path == "" {
		path = "/"
	}
	out.URL = &url.URL{
		Path:       path,
		RawPath:    target.RawPath,
		ForceQuery: target.ForceQuery,
		RawQuery:   target.RawQuery,
	}
	out.RequestURI = ""
	out.Host = target.Host
	out.Header = req.Header.Clone()
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Proxy-Authorization")
	out.Body = nil
	out.GetBody = nil
	out.ContentLength = 0
	return out
}

func cloneWebSocketRecordRequest(req *http.Request, target *url.URL) *http.Request {
	out := new(http.Request)
	*out = *req
	out.URL = cloneURL(target)
	out.RequestURI = target.RequestURI()
	out.Host = target.Host
	out.Header = req.Header.Clone()
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Proxy-Authorization")
	out.Body = nil
	out.GetBody = nil
	out.ContentLength = 0
	return out
}

func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

func hostWithDefaultPort(host, defaultPort string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	unbracketed := host
	if strings.HasPrefix(unbracketed, "[") {
		if end := strings.Index(unbracketed, "]"); end >= 0 {
			unbracketed = unbracketed[1:end]
		}
	}
	return net.JoinHostPort(unbracketed, defaultPort)
}

func (r *Recorder) tunnelWebSocket(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn, upstreamReader *bufio.Reader, session *Session, seq int) {
	done := make(chan struct{}, 2)
	closeBoth := sync.OnceFunc(func() {
		_ = clientConn.Close()
		_ = upstreamConn.Close()
	})

	go func() {
		defer func() {
			closeBoth()
			done <- struct{}{}
		}()
		r.copyWebSocketFrames(upstreamConn, clientReader, session, seq, webSocketDirectionClientToServer)
	}()
	go func() {
		defer func() {
			closeBoth()
			done <- struct{}{}
		}()
		r.copyWebSocketFrames(clientConn, upstreamReader, session, seq, webSocketDirectionServerToClient)
	}()

	<-done
	<-done
}

func (r *Recorder) copyWebSocketFrames(dst io.Writer, src io.Reader, session *Session, seq int, direction string) {
	for {
		frame, err := readWebSocketFrame(src)
		if err != nil {
			return
		}
		if _, err := dst.Write(frame.raw); err != nil {
			return
		}
		r.recordWebSocketFrame(session, seq, direction, frame)
		if frame.opcode == webSocketOpcodeClose {
			return
		}
	}
}

func readWebSocketFrame(r io.Reader) (webSocketFrame, error) {
	var first [2]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return webSocketFrame{}, err
	}
	raw := append([]byte(nil), first[:]...)
	opcode := int(first[0] & 0x0f)
	masked := first[1]&0x80 != 0
	payloadLen := uint64(first[1] & 0x7f)

	switch payloadLen {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return webSocketFrame{}, err
		}
		raw = append(raw, ext[:]...)
		payloadLen = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return webSocketFrame{}, err
		}
		raw = append(raw, ext[:]...)
		payloadLen = binary.BigEndian.Uint64(ext[:])
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return webSocketFrame{}, err
		}
		raw = append(raw, maskKey[:]...)
	}

	if payloadLen > uint64(int(^uint(0)>>1)) {
		return webSocketFrame{}, fmt.Errorf("websocket frame too large: %d", payloadLen)
	}
	payload := make([]byte, int(payloadLen))
	if _, err := io.ReadFull(r, payload); err != nil {
		return webSocketFrame{}, err
	}
	raw = append(raw, payload...)

	unmasked := payload
	if masked && len(payload) > 0 {
		unmasked = make([]byte, len(payload))
		for i, b := range payload {
			unmasked[i] = b ^ maskKey[i%4]
		}
	}

	return webSocketFrame{raw: raw, opcode: opcode, payload: unmasked}, nil
}

func (r *Recorder) recordWebSocketFrame(session *Session, seq int, direction string, frame webSocketFrame) {
	if session == nil || seq == 0 {
		return
	}
	switch frame.opcode {
	case webSocketOpcodeText, webSocketOpcodeBinary, webSocketOpcodeContinuation:
	default:
		return
	}

	limit := session.Filters.BodyCaptureLimitBytes
	if limit <= 0 {
		limit = defaultBodyCaptureLimitBytes
	}
	payload := frame.payload
	truncated := false
	if len(payload) > limit {
		payload = payload[:limit]
		truncated = true
	}

	payloadText := string(payload)
	payloadEncoding := ""
	if frame.opcode == webSocketOpcodeBinary || !utf8.Valid(payload) {
		payloadText = base64.StdEncoding.EncodeToString(payload)
		payloadEncoding = "base64"
	}

	r.appendWebSocketMessage(session, seq, RecordedWebSocketMessage{
		Direction:        direction,
		Opcode:           frame.opcode,
		Payload:          payloadText,
		PayloadEncoding:  payloadEncoding,
		PayloadSizeBytes: len(frame.payload),
		PayloadTruncated: truncated,
		Timestamp:        time.Now(),
	})
}

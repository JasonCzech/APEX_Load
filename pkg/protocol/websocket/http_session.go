package websocket

import (
	"crypto/tls"
	"net/http"
)

// ShareHTTPSession makes the WebSocket handshake send, and store, the cookies
// of an HTTP session and use its TLS settings. HTTP scripts with WebSocket
// steps use it so a socket opened after an HTTP login carries the session.
// Call it after Init.
func (w *WebSocket) ShareHTTPSession(jar http.CookieJar, tlsConfig *tls.Config) {
	if w.dialer == nil {
		return
	}
	w.dialer.Jar = jar
	if tlsConfig != nil {
		w.dialer.TLSClientConfig = tlsConfig.Clone()
	}
}

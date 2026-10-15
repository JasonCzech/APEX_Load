package recorder

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

// DefaultCACertFile is the CA certificate's file name in its directory; the
// key sits beside it.
const DefaultCACertFile = "ca-cert.pem"

// DeviceSetupCertificatePath is where the proxy serves its CA certificate to a
// phone or tablet being set up for a device capture.
const DeviceSetupCertificatePath = "/apexload-ca.crt"

// SetDeviceSetup makes the proxy answer requests sent to it rather than
// through it with a setup page and its CA certificate, so a phone or tablet
// can be set up for a device capture by opening the proxy's address. The
// certificate is public; the page never shows the proxy password.
func (r *Recorder) SetDeviceSetup(enabled bool) {
	r.deviceSetup.Store(enabled)
}

// serveDeviceSetup answers a GET for the proxy's own address: the setup page
// at / and the certificate. It reports whether it answered.
func (r *Recorder) serveDeviceSetup(w http.ResponseWriter, req *http.Request) bool {
	// Checked before the proxy password on every request, so the common
	// case (off, or a proxied request) takes no lock.
	if !r.deviceSetup.Load() || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
		return false
	}
	r.mu.RLock()
	addr := r.addr
	r.mu.RUnlock()
	// A device without the proxy configured asks for the page directly (an
	// origin-form request); one with it asks the proxy for its own address.
	if req.URL.IsAbs() && !strings.EqualFold(req.URL.Host, addr) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	switch req.URL.Path {
	case "/", "/setup":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, deviceSetupPage(addr))
	case DeviceSetupCertificatePath:
		if err := r.initCA(); err != nil {
			http.Error(w, "the certificate is not available", http.StatusInternalServerError)
			return true
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="apexload-ca.crt"`)
		w.Write(r.caCert.Raw)
	default:
		http.NotFound(w, req)
	}
	return true
}

func deviceSetupPage(addr string) string {
	host, port := addr, ""
	if cut := strings.LastIndex(addr, ":"); cut >= 0 {
		host, port = addr[:cut], addr[cut+1:]
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>APEX Load device capture</title>` +
		`<style>body{font:17px -apple-system,system-ui,sans-serif;margin:0 auto;max-width:34em;padding:20px;line-height:1.45}` +
		`a.button{display:inline-block;background:#0057b8;color:#fff;padding:12px 18px;border-radius:8px;text-decoration:none;font-weight:600}` +
		`li{margin:.6em 0}code{font-size:1em;font-weight:600}</style></head><body>` +
		`<h1>Set up this device for capture</h1>` +
		`<p><a class="button" href="` + DeviceSetupCertificatePath + `">Download the certificate</a></p>` +
		`<ol>` +
		`<li>Tap <b>Download the certificate</b>, then <b>Allow</b>.</li>` +
		`<li>Open <b>Settings › General › VPN &amp; Device Management</b>, choose <b>APEX Load Recording Proxy CA</b> and tap <b>Install</b>.</li>` +
		`<li>Open <b>Settings › General › About › Certificate Trust Settings</b> and turn on full trust for <b>APEX Load Recording Proxy CA</b>.</li>` +
		`<li>Open <b>Settings › Wi-Fi</b>, tap <b>ⓘ</b> beside this network, then <b>Configure Proxy › Manual</b>: server <code>` + html.EscapeString(host) +
		`</code>, port <code>` + html.EscapeString(port) + `</code>, <b>Authentication</b> on, and the username and password Recording Studio shows.</li>` +
		`<li>Open the app and use it; Recording Studio counts each request.</li>` +
		`</ol>` +
		`<p>When you are done, set <b>Configure Proxy</b> back to <b>Off</b>.</p>` +
		`</body></html>`
}

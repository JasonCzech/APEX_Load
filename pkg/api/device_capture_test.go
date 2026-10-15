package api

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func freeDeviceCapturePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// A phone or tablet is captured through the Recording Agent: the agent's
// proxy listens on the chosen address with a fresh password, serves the
// device its setup page and certificate, records what the device sends, and
// stops listening when the capture stops.
func TestDeviceCaptureThroughTheAgent(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_ALLOW_PRIVATE_TARGETS", "true")
	t.Setenv("APEXLOAD_RECORDER_CA_DIR", t.TempDir())
	original := deviceCaptureInterfaces
	deviceCaptureInterfaces = func() []deviceCaptureInterface {
		return []deviceCaptureInterface{{Name: "lo0", Address: "127.0.0.1"}}
	}
	t.Cleanup(func() { deviceCaptureInterfaces = original })
	server := newBrowserRecorderBridgeTestServer(t, nil)
	t.Cleanup(func() { _ = server.recorder.StopProxy() })
	bridgeSession := pairBridgeForTest(t, server, bridgeTestOrigin)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Origin", bridgeTestOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(bridgeTokenHeader, bridgeTestToken)
		req.Header.Set(bridgeSessionHeader, bridgeSession)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	type captureEnvelope struct {
		Interfaces []deviceCaptureInterface `json:"interfaces"`
		Capture    *deviceCaptureView       `json:"capture"`
	}
	decode := func(resp *httptest.ResponseRecorder, want int) captureEnvelope {
		t.Helper()
		if resp.Code != want {
			t.Fatalf("status %d, want %d: %s", resp.Code, want, resp.Body.String())
		}
		var envelope captureEnvelope
		if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope
	}

	state := decode(call(http.MethodGet, "/api/v1/device-capture", ""), http.StatusOK)
	if len(state.Interfaces) != 1 || state.Capture != nil {
		t.Fatalf("initial state = %+v", state)
	}
	if resp := call(http.MethodPost, "/api/v1/device-capture", `{"address":"10.9.9.9"}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("an address the workstation lacks: %d %s", resp.Code, resp.Body.String())
	}
	port := freeDeviceCapturePort(t)
	capture := decode(call(http.MethodPost, "/api/v1/device-capture", fmt.Sprintf(`{"name":"iPad","address":"127.0.0.1","port":%d}`, port)), http.StatusCreated).Capture
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	if capture == nil || capture.Status != "recording" || capture.Port != port || capture.Username != "apexload" ||
		len(capture.Password) != 19 || capture.SetupURL != "http://"+proxyAddr+"/" || capture.CAURL != "http://"+proxyAddr+"/apexload-ca.crt" {
		t.Fatalf("capture = %+v", capture)
	}
	if resp := call(http.MethodPost, "/api/v1/device-capture", fmt.Sprintf(`{"address":"127.0.0.1","port":%d}`, port)); resp.Code != http.StatusConflict {
		t.Fatalf("a second capture: %d %s", resp.Code, resp.Body.String())
	}

	// The device opens the setup page and downloads the certificate before its
	// proxy is set: no password is asked for, and none is shown.
	page, err := http.Get(capture.SetupURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "Download the certificate") || strings.Contains(string(body), capture.Password) {
		t.Fatalf("setup page %d: %s", page.StatusCode, body)
	}
	certificate, err := http.Get(capture.CAURL)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := io.ReadAll(certificate.Body)
	certificate.Body.Close()
	if parsed, err := x509.ParseCertificate(der); err != nil || !parsed.IsCA || certificate.Header.Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Fatalf("certificate: %v %v %q", parsed, err, certificate.Header.Get("Content-Type"))
	}

	// The device's traffic needs the password and is counted.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	through := func(password string) int {
		t.Helper()
		proxyURL := &url.URL{Scheme: "http", Host: proxyAddr, User: url.UserPassword("apexload", password)}
		client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		resp, err := client.Get(upstream.URL + "/services/data/v66.0/limits")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := through("wrong"); code != http.StatusProxyAuthRequired {
		t.Fatalf("a wrong password: %d", code)
	}
	if code := through(capture.Password); code != http.StatusOK {
		t.Fatalf("the device's request: %d", code)
	}
	if resp := call(http.MethodPost, "/api/v1/recorder/"+capture.SessionID+"/transactions/start", `{"name":"Login"}`); resp.Code >= 300 {
		t.Fatalf("start transaction: %d %s", resp.Code, resp.Body.String())
	}
	live := decode(call(http.MethodGet, "/api/v1/device-capture", ""), http.StatusOK).Capture
	if live.RequestCount != 1 || live.LastHost == "" || len(live.OpenTransactions) != 1 || live.OpenTransactions[0] != "Login" {
		t.Fatalf("live capture = %+v", live)
	}
	qr := call(http.MethodGet, "/api/v1/device-capture/qr.svg", "")
	if qr.Code != http.StatusOK || qr.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(qr.Body.String(), "<svg") {
		t.Fatalf("qr code %d %q", qr.Code, qr.Body.String())
	}

	// Stopping closes the open transaction, spends the password and stops
	// the proxy listening; the recording converts.
	stopped := decode(call(http.MethodPost, "/api/v1/device-capture/stop", ""), http.StatusOK).Capture
	if stopped.Status != "stopped" || stopped.Password != "" || !strings.Contains(strings.Join(stopped.Warnings, "\n"), `"Login"`) {
		t.Fatalf("stopped capture = %+v", stopped)
	}
	if conn, err := net.DialTimeout("tcp", proxyAddr, time.Second); err == nil {
		conn.Close()
		t.Fatal("the proxy still listens after the capture stopped")
	}
	if resp := call(http.MethodGet, "/api/v1/device-capture/qr.svg", ""); resp.Code != http.StatusNotFound {
		t.Fatalf("qr code of a stopped capture: %d", resp.Code)
	}
	preview := call(http.MethodPost, "/api/v1/recorder/"+capture.SessionID+"/convert/preview", `{"name":"iPad"}`)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "/services/data/v66.0/limits") {
		t.Fatalf("preview %d: %.400s", preview.Code, preview.Body.String())
	}

	// The next capture listens again, with a new password.
	again := decode(call(http.MethodPost, "/api/v1/device-capture", fmt.Sprintf(`{"address":"127.0.0.1","port":%d}`, port)), http.StatusCreated).Capture
	if again.SessionID == capture.SessionID || again.Password == capture.Password || again.Status != "recording" {
		t.Fatalf("second capture = %+v", again)
	}
	if code := through(again.Password); code != http.StatusOK {
		t.Fatalf("the second capture's request: %d", code)
	}
}

// When the default port is taken, a capture started without a port listens
// on a free one and says which; a port asked for explicitly is not swapped.
func TestDeviceCaptureFallsBackFromABusyDefaultPort(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_CA_DIR", t.TempDir())
	original := deviceCaptureInterfaces
	deviceCaptureInterfaces = func() []deviceCaptureInterface {
		return []deviceCaptureInterface{{Name: "lo0", Address: "127.0.0.1"}}
	}
	t.Cleanup(func() { deviceCaptureInterfaces = original })
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	originalPort := defaultDeviceCapturePort
	defaultDeviceCapturePort = busyPort
	t.Cleanup(func() { defaultDeviceCapturePort = originalPort })

	server := newBrowserRecorderBridgeTestServer(t, nil)
	t.Cleanup(func() { _ = server.recorder.StopProxy() })
	bridgeSession := pairBridgeForTest(t, server, bridgeTestOrigin)
	start := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/device-capture", bytes.NewBufferString(body))
		req.Header.Set("Origin", bridgeTestOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(bridgeTokenHeader, bridgeTestToken)
		req.Header.Set(bridgeSessionHeader, bridgeSession)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	if resp := start(fmt.Sprintf(`{"address":"127.0.0.1","port":%d}`, busyPort)); resp.Code != http.StatusConflict {
		t.Fatalf("an explicit busy port: %d %s", resp.Code, resp.Body.String())
	}
	resp := start(`{"address":"127.0.0.1"}`)
	var envelope struct{ Capture deviceCaptureView }
	if resp.Code != http.StatusCreated || json.Unmarshal(resp.Body.Bytes(), &envelope) != nil {
		t.Fatalf("default port: %d %s", resp.Code, resp.Body.String())
	}
	if envelope.Capture.Port == busyPort || envelope.Capture.Port == 0 || server.recorder.ProxyAddr() != fmt.Sprintf("127.0.0.1:%d", envelope.Capture.Port) {
		t.Fatalf("capture port %d (busy %d), proxy %s", envelope.Capture.Port, busyPort, server.recorder.ProxyAddr())
	}
}

// A device capture records and converts with the recording profile selected
// in Recording Studio, which the dashboard hands to the agent: the agent has
// no project store of its own.
func TestDeviceCaptureAppliesTheStudioRecordingProfile(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_ALLOW_PRIVATE_TARGETS", "true")
	t.Setenv("APEXLOAD_RECORDER_CA_DIR", t.TempDir())
	original := deviceCaptureInterfaces
	deviceCaptureInterfaces = func() []deviceCaptureInterface {
		return []deviceCaptureInterface{{Name: "lo0", Address: "127.0.0.1"}}
	}
	t.Cleanup(func() { deviceCaptureInterfaces = original })
	server := newBrowserRecorderBridgeTestServer(t, nil)
	t.Cleanup(func() { _ = server.recorder.StopProxy() })
	bridgeSession := pairBridgeForTest(t, server, bridgeTestOrigin)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Origin", bridgeTestOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(bridgeTokenHeader, bridgeTestToken)
		req.Header.Set(bridgeSessionHeader, bridgeSession)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	port := freeDeviceCapturePort(t)
	if resp := call(http.MethodPost, "/api/v1/device-capture", fmt.Sprintf(`{"address":"127.0.0.1","port":%d,"recording_profile":{"id":"bad id","name":"x"}}`, port)); resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "recording_profile.id") {
		t.Fatalf("an invalid profile: %d %s", resp.Code, resp.Body.String())
	}
	start := call(http.MethodPost, "/api/v1/device-capture", fmt.Sprintf(`{"name":"iPad","address":"127.0.0.1","port":%d,"recording_profile":{"id":"mobile","name":"Mobile app","exclude_hosts":["blocked.example"],"background_requests":["telemetry/upload"]}}`, port))
	var envelope struct{ Capture deviceCaptureView }
	if start.Code != http.StatusCreated || json.Unmarshal(start.Body.Bytes(), &envelope) != nil {
		t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	capture := envelope.Capture
	session, ok := server.recorder.GetSessionWindow(capture.SessionID, false, 0, 0)
	if !ok || session.Filters.RecordingProfileID != "mobile" || len(session.Filters.ExcludeHosts) != 1 || session.Filters.ExcludeHosts[0] != "blocked.example" {
		t.Fatalf("session filters = %+v", session.Filters)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port), User: url.UserPassword("apexload", capture.Password)}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	for _, send := range []func() (*http.Response, error){
		func() (*http.Response, error) { return client.Get(upstream.URL + "/services/data/v66.0/limits") },
		func() (*http.Response, error) {
			return client.Post(upstream.URL+"/telemetry/upload", "application/json", strings.NewReader(`{"events":[]}`))
		},
	} {
		resp, err := send()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if resp := call(http.MethodPost, "/api/v1/device-capture/stop", ""); resp.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", resp.Code, resp.Body.String())
	}
	preview := call(http.MethodPost, "/api/v1/recorder/"+capture.SessionID+"/convert/preview", `{"name":"iPad"}`)
	var converted struct {
		Script struct {
			Actions []struct {
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"actions"`
		} `json:"script"`
	}
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &converted) != nil {
		t.Fatalf("preview %d: %.400s", preview.Code, preview.Body.String())
	}
	background := 0
	for _, action := range converted.Script.Actions {
		if action.Type == "http_async_start" && action.Params["pattern"] == "once" {
			background++
		}
	}
	if len(converted.Script.Actions) != 2 || background != 1 {
		t.Fatalf("actions = %+v, want the profile's telemetry path sent in the background", converted.Script.Actions)
	}
}

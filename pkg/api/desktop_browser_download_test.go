package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/recorder"
)

func TestDesktopBrowserDownloadMintRequiresBrowserCapability(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", Addr: "127.0.0.1:0"})
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities["browser-tab-secret"] = desktopBrowserCapability{issuedAt: time.Now()}
	server.desktopBootstrapMu.Unlock()

	request := func(capability, resourcePath string) *httptest.ResponseRecorder {
		body, err := json.Marshal(createDesktopBrowserDownloadRequest{ResourcePath: resourcePath})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/desktop/browser-downloads", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if capability != "" {
			req.Header.Set(DesktopCapabilityHeader, capability)
		}
		resp := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(resp, req)
		return resp
	}

	if got := request("", "/api/v1/tests/run-1/archive"); got.Code != http.StatusUnauthorized {
		t.Fatalf("missing capability status = %d, want %d", got.Code, http.StatusUnauthorized)
	}
	if got := request("native-renderer-secret", "/api/v1/tests/run-1/archive"); got.Code != http.StatusUnauthorized {
		t.Fatalf("native renderer capability status = %d, want %d; body=%s", got.Code, http.StatusUnauthorized, got.Body.String())
	}
	if got := request("browser-tab-secret", "/api/v1/system/settings"); got.Code != http.StatusBadRequest {
		t.Fatalf("non-download path status = %d, want %d; body=%s", got.Code, http.StatusBadRequest, got.Body.String())
	}

	created := request("browser-tab-secret", "/api/v1/tests/run-1/archive?format=zip")
	if created.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want %d; body=%s", created.Code, http.StatusCreated, created.Body.String())
	}
	if got := created.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload.URL, "/desktop/download?token=") || strings.Contains(payload.URL, "browser-tab-secret") {
		t.Fatalf("minted URL = %q", payload.URL)
	}
}

func TestDesktopBrowserDownloadStreamsOnceThroughNormalHandler(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", Addr: "127.0.0.1:0"})
	const (
		browserCapability = "browser-tab-secret"
		token             = "single-use-download-token"
	)
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[browserCapability] = desktopBrowserCapability{issuedAt: time.Now()}
	server.desktopBrowserDownloads[token] = desktopBrowserDownloadGrant{
		capability:   browserCapability,
		resourcePath: "/api/v1/tests/run-1/archive?format=zip",
		mode:         "download",
		issuedAt:     time.Now(),
		expiresAt:    time.Now().Add(time.Minute),
	}
	server.desktopBootstrapMu.Unlock()

	content := strings.Repeat("archive-chunk-", 128*1024)
	called := 0
	server.httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/v1/tests/run-1/archive?format=zip" {
			t.Errorf("inner request = %s %s", r.Method, r.URL.RequestURI())
		}
		if got := r.Header.Get(DesktopCapabilityHeader); got != browserCapability {
			t.Errorf("inner capability = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("ambient cookie was forwarded: %q", got)
		}
		if got := r.Header.Get("Range"); got != "" {
			t.Errorf("Range was forwarded: %q", got)
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="run-1.zip"`)
		_, _ = io.WriteString(w, content)
	})

	request := httptest.NewRequest(http.MethodGet, "/desktop/download?token="+token, nil)
	request.Header.Set("Cookie", "unrelated-loopback-cookie=secret")
	request.Header.Set("Range", "bytes=100-200")
	response := httptest.NewRecorder()
	server.handleDesktopBrowserDownload(response, request)
	if response.Code != http.StatusOK || response.Body.String() != content {
		t.Fatalf("download response status=%d bytes=%d", response.Code, response.Body.Len())
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}

	replay := httptest.NewRecorder()
	server.handleDesktopBrowserDownload(replay, httptest.NewRequest(http.MethodGet, "/desktop/download?token="+token, nil))
	if replay.Code != http.StatusForbidden {
		t.Fatalf("replay status = %d, want %d", replay.Code, http.StatusForbidden)
	}
	if called != 1 {
		t.Fatalf("inner handler called %d times, want 1", called)
	}
}

func TestDesktopBrowserDownloadDownstreamErrorsAreNotArtifacts(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		status          int
		disposition     string
		wantStatus      int
		wantDisposition bool
		wantBody        string
		location        string
	}{
		{name: "404 drops artifact headers", mode: "download", status: http.StatusNotFound, disposition: `attachment; filename="error.zip"`, wantStatus: http.StatusNotFound, wantBody: "archive not found"},
		{name: "redirect cannot escape response gate", mode: "download", status: http.StatusFound, disposition: `attachment; filename="redirect.zip"`, location: "https://downloads.example.test/redirect.zip", wantStatus: http.StatusBadGateway, wantBody: "returned a redirect"},
		{name: "download requires attachment", mode: "download", status: http.StatusOK, wantStatus: http.StatusBadGateway, wantBody: "did not return an attachment"},
		{name: "open accepts inline response", mode: "open", status: http.StatusOK, wantStatus: http.StatusOK, wantBody: "report body"},
		{name: "valid attachment streams", mode: "download", status: http.StatusOK, disposition: `attachment; filename="run.zip"`, wantStatus: http.StatusOK, wantDisposition: true, wantBody: "report body"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := httptest.NewRecorder()
			writer := newDesktopBrowserDownloadResponseWriter(destination, test.mode == "download")
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Content-Disposition", test.disposition)
			writer.Header().Set("Digest", "sha-256=not-an-error-artifact")
			writer.Header().Set("Location", test.location)
			writer.WriteHeader(test.status)
			body := []byte("report body")
			if test.status >= http.StatusBadRequest {
				body = []byte("archive not found")
			}
			if _, err := writer.Write(body); err != nil {
				t.Fatal(err)
			}
			writer.finish()

			if destination.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", destination.Code, test.wantStatus, destination.Body.String())
			}
			if got := destination.Header().Get("Content-Disposition"); (got != "") != test.wantDisposition {
				t.Fatalf("Content-Disposition = %q, want present=%t", got, test.wantDisposition)
			}
			if destination.Code >= http.StatusMultipleChoices && destination.Header().Get("Digest") != "" {
				t.Fatalf("error retained artifact digest %q", destination.Header().Get("Digest"))
			}
			if destination.Code >= http.StatusMultipleChoices && destination.Header().Get("Location") != "" {
				t.Fatalf("error retained redirect location %q", destination.Header().Get("Location"))
			}
			if !strings.Contains(destination.Body.String(), test.wantBody) {
				t.Fatalf("body = %q, want substring %q", destination.Body.String(), test.wantBody)
			}
		})
	}
}

func TestDesktopBrowserDownloadResponseWriterPropagatesArchiveDeadline(t *testing.T) {
	destination := &archiveDeadlineRecorder{}
	writer := newDesktopBrowserDownloadResponseWriter(destination, true)
	started := time.Now()

	clearArchiveWriteDeadline(writer)

	minimum := started.Add(configuredArchiveMaterializationTimeout() + 4*time.Second)
	if destination.deadline.Before(minimum) {
		t.Fatalf("archive deadline through desktop response gate = %v, want at least %v", destination.deadline, minimum)
	}
}

func TestDesktopBrowserDownloadRedispatchPreservesDevUserProjectIsolation(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", Addr: "127.0.0.1:0"})
	const browserCapability = "browser-tab-secret"
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[browserCapability] = desktopBrowserCapability{issuedAt: time.Now()}
	server.desktopBootstrapMu.Unlock()
	session, err := server.recorder.StartSessionForProject("project-b-recording", "Project B", "project-b", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://example.test/", Path: "/", StatusCode: http.StatusOK,
	})

	mint := func(projectID string) string {
		body := bytes.NewBufferString(`{"resourcePath":"/api/v1/recorder/project-b-recording/export?format=har&raw=true","mode":"download"}`)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/desktop/browser-downloads", body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(DesktopCapabilityHeader, browserCapability)
		request.Header.Set(devViewHeader, devViewModeUser)
		request.Header.Set(devProjectIDHeader, projectID)
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("mint for %s status=%d body=%s", projectID, response.Code, response.Body.String())
		}
		var payload struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.URL
	}
	download := func(target string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		return response
	}

	denied := download(mint("project-a"))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("cross-project download status=%d, want %d; body=%s", denied.Code, http.StatusForbidden, denied.Body.String())
	}
	if denied.Header().Get("Content-Disposition") != "" {
		t.Fatalf("cross-project denial retained attachment header %q", denied.Header().Get("Content-Disposition"))
	}

	missingBody := bytes.NewBufferString(`{"resourcePath":"/api/v1/recorder/missing-recording/export?format=har","mode":"download"}`)
	missingMintRequest := httptest.NewRequest(http.MethodPost, "/api/v1/desktop/browser-downloads", missingBody)
	missingMintRequest.Header.Set("Content-Type", "application/json")
	missingMintRequest.Header.Set(DesktopCapabilityHeader, browserCapability)
	missingMintRequest.Header.Set(devViewHeader, devViewModeUser)
	missingMintRequest.Header.Set(devProjectIDHeader, "project-b")
	missingMintResponse := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(missingMintResponse, missingMintRequest)
	if missingMintResponse.Code != http.StatusCreated {
		t.Fatalf("missing-session mint status=%d body=%s", missingMintResponse.Code, missingMintResponse.Body.String())
	}
	var missingPayload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(missingMintResponse.Body.Bytes(), &missingPayload); err != nil {
		t.Fatal(err)
	}
	missing := download(missingPayload.URL)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing-session download status=%d, want %d; body=%s", missing.Code, http.StatusNotFound, missing.Body.String())
	}
	if missing.Header().Get("Content-Disposition") != "" {
		t.Fatalf("missing-session denial retained attachment header %q", missing.Header().Get("Content-Disposition"))
	}

	allowed := download(mint("project-b"))
	if allowed.Code != http.StatusOK {
		t.Fatalf("same-project download status=%d, want %d; body=%s", allowed.Code, http.StatusOK, allowed.Body.String())
	}
	if !isAttachmentDisposition(allowed.Header().Get("Content-Disposition")) {
		t.Fatalf("same-project response is not an attachment: %q", allowed.Header().Get("Content-Disposition"))
	}
}

func TestDesktopBrowserDownloadHeadDoesNotConsumeGrant(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret"})
	now := time.Now().UTC()
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities["browser-tab"] = desktopBrowserCapability{issuedAt: now}
	server.desktopBrowserDownloads["download-token"] = desktopBrowserDownloadGrant{
		capability: "browser-tab", resourcePath: "/api/v1/tests/run-1/archive", issuedAt: now, expiresAt: now.Add(time.Minute),
	}
	server.desktopBootstrapMu.Unlock()

	response := httptest.NewRecorder()
	server.handleDesktopBrowserDownload(response, httptest.NewRequest(http.MethodHead, "/desktop/download?token=download-token", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("HEAD response status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
	if _, ok := server.takeDesktopBrowserDownload("download-token", now); !ok {
		t.Fatal("HEAD request consumed the one-time GET grant")
	}
}

func TestDesktopBrowserDownloadRejectsExpiredOrRevokedGrant(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret"})
	now := time.Now().UTC()
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities["active-tab"] = desktopBrowserCapability{issuedAt: now}
	server.desktopBrowserDownloads["expired"] = desktopBrowserDownloadGrant{
		capability: "active-tab", resourcePath: "/api/v1/tests/run-1/archive", issuedAt: now.Add(-2 * time.Minute), expiresAt: now.Add(-time.Minute),
	}
	server.desktopBrowserDownloads["revoked"] = desktopBrowserDownloadGrant{
		capability: "revoked-tab", resourcePath: "/api/v1/tests/run-1/archive", issuedAt: now, expiresAt: now.Add(time.Minute),
	}
	server.desktopBootstrapMu.Unlock()

	for _, token := range []string{"expired", "revoked"} {
		if _, ok := server.takeDesktopBrowserDownload(token, now); ok {
			t.Fatalf("grant %q was accepted", token)
		}
		if _, ok := server.takeDesktopBrowserDownload(token, now); ok {
			t.Fatalf("invalid grant %q was not consumed", token)
		}
	}
}

func TestValidateDesktopBrowserDownloadPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "run archive", raw: "/api/v1/tests/run-1/archive", want: "/api/v1/tests/run-1/archive"},
		{name: "diagnostics", raw: "/api/v1/tests/run-1/archive/browser-diagnostics", want: "/api/v1/tests/run-1/archive/browser-diagnostics"},
		{name: "artifact", raw: "/api/v1/tests/run-1/archive/files/transactions.jsonl", want: "/api/v1/tests/run-1/archive/files/transactions.jsonl"},
		{name: "archive report query", raw: "/api/v1/tests/run-1/archive/report?format=pdf&profile_id=full", want: "/api/v1/tests/run-1/archive/report?format=pdf&profile_id=full"},
		{name: "html report", raw: "/api/v1/tests/run-1/report", want: "/api/v1/tests/run-1/report"},
		{name: "bruno raw source", raw: "/api/v1/scripts/script-1/bruno-imports/source?raw=true", want: "/api/v1/scripts/script-1/bruno-imports/source?raw=true"},
		{name: "recording agent", raw: "/api/v1/downloads/recording-agent/latest?platform=linux-amd64", want: "/api/v1/downloads/recording-agent/latest?platform=linux-amd64"},
		{name: "desktop app", raw: "/api/v1/downloads/desktop/latest?platform=windows-amd64", want: "/api/v1/downloads/desktop/latest?platform=windows-amd64"},
		{name: "recorder export", raw: "/api/v1/recorder/session-1/export?format=har", want: "/api/v1/recorder/session-1/export?format=har"},
		{name: "browser recorder export", raw: "/api/v1/browser-recorder/sessions/session-1/export?format=har", want: "/api/v1/browser-recorder/sessions/session-1/export?format=har"},
		{name: "absolute URL", raw: "https://attacker.test/api/v1/tests/run-1/archive"},
		{name: "scheme relative", raw: "//attacker.test/api/v1/tests/run-1/archive"},
		{name: "non-download API", raw: "/api/v1/system/settings"},
		{name: "dot segment", raw: "/api/v1/tests/../system/settings"},
		{name: "encoded dot segment", raw: "/api/v1/tests/%2e%2e/system/settings"},
		{name: "encoded slash", raw: "/api/v1/tests/run-1%2f..%2fsystem/settings"},
		{name: "backslash", raw: "/api/v1/tests\\run-1/archive"},
		{name: "empty segment", raw: "/api/v1/tests//archive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateDesktopBrowserDownloadPath(test.raw)
			if test.want == "" {
				if err == nil {
					t.Fatalf("validateDesktopBrowserDownloadPath(%q) = %q, want error", test.raw, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("validateDesktopBrowserDownloadPath(%q) = %q, %v; want %q", test.raw, got, err, test.want)
			}
		})
	}
}

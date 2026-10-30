package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/har"
)

type harPairImportResponse struct {
	Warnings []string `json:"warnings"`
	Summary  struct {
		ArtifactMode string `json:"artifact_mode"`
		HARDiff      *struct {
			Compared          bool   `json:"compared"`
			Reason            string `json:"reason"`
			DynamicCandidates int    `json:"dynamic_candidates"`
			UnhandledInputs   []struct {
				Field    string `json:"field"`
				Kind     string `json:"kind"`
				Requests int    `json:"requests"`
			} `json:"unhandled_inputs"`
		} `json:"har_diff"`
	} `json:"summary"`
	ReviewSections []struct {
		Key      string `json:"key"`
		Severity string `json:"severity"`
	} `json:"review_sections"`
}

func postHARPairJSON(t *testing.T, route string, fields map[string]any) (*httptest.ResponseRecorder, harPairImportResponse) {
	t.Helper()
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewServer(ServerConfig{}).mux.ServeHTTP(response, req)
	var decoded harPairImportResponse
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
	}
	return response, decoded
}

func hasHARPairWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}

func TestHARPairComparisonFailureKeepsTheBaselineImport(t *testing.T) {
	for name, comparison := range map[string]string{
		"malformed": `{"log": not json`,
		"truncated": truncatedSalvageHAR(),
	} {
		t.Run(name, func(t *testing.T) {
			response, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
				"name":              "Pair",
				"content":           apiCredentialCorrelationHAR("baseline-token-1234567890"),
				"secondary_content": comparison,
			})
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want the baseline import: %s", response.Code, response.Body.String())
			}
			diff := decoded.Summary.HARDiff
			if diff == nil || diff.Compared || !strings.Contains(diff.Reason, "HAR pair diff failed") {
				t.Fatalf("har_diff = %#v, want compared=false with a reason", diff)
			}
			if decoded.Summary.ArtifactMode != har.ArtifactModeDesignHAR {
				t.Fatalf("artifact_mode = %q, want a single design HAR when nothing was compared", decoded.Summary.ArtifactMode)
			}
			if !hasHARPairWarning(decoded.Warnings, "The comparison HAR was not compared") {
				t.Fatalf("warnings = %q, want the skipped comparison explained", decoded.Warnings)
			}
			if name == "truncated" && !strings.Contains(diff.Reason, "salvage_truncated=true") {
				t.Fatalf("reason = %q, want the salvage hint", diff.Reason)
			}
			for _, section := range decoded.ReviewSections {
				if section.Key == "har_pair_diff" && section.Severity != "warning" {
					t.Fatalf("pair diff section severity = %q, want warning", section.Severity)
				}
			}
		})
	}
}

func TestHARPairSalvagesATruncatedComparisonUnderTheSameOptIn(t *testing.T) {
	response, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
		"name":              "Pair",
		"content":           apiCredentialCorrelationHAR("baseline-token-1234567890"),
		"secondary_content": truncatedSalvageHAR(),
		"salvage_truncated": true,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if decoded.Summary.HARDiff == nil || !decoded.Summary.HARDiff.Compared {
		t.Fatalf("har_diff = %#v, want the salvaged comparison compared", decoded.Summary.HARDiff)
	}
	if decoded.Summary.ArtifactMode != har.ArtifactModeDesignHARPair {
		t.Fatalf("artifact_mode = %q, want %q", decoded.Summary.ArtifactMode, har.ArtifactModeDesignHARPair)
	}
	if !hasHARPairWarning(decoded.Warnings, "Comparison HAR:") || !strings.Contains(response.Body.String(), "har_comparison_truncated_salvaged") {
		t.Fatalf("warnings = %q, want the comparison salvage recorded", decoded.Warnings)
	}
}

func TestHARPairModeIsReportedOnlyWhenAComparisonRan(t *testing.T) {
	response, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
		"name":    "Single",
		"content": apiCredentialCorrelationHAR("baseline-token-1234567890"),
		"options": map[string]any{"artifact_mode": har.ArtifactModeDesignHARPair},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if decoded.Summary.ArtifactMode != har.ArtifactModeDesignHAR || decoded.Summary.HARDiff != nil {
		t.Fatalf("summary = %#v, want a single design HAR without har_diff", decoded.Summary)
	}
}

func TestSalesforceImportSaysItDidNotUseTheComparisonHAR(t *testing.T) {
	response, decoded := postHARPairJSON(t, "/api/v1/scripts/import/salesforce", map[string]any{
		"name":              "Salesforce pair",
		"content":           apiCredentialCorrelationHAR("baseline-token-1234567890"),
		"secondary_content": apiCredentialCorrelationHAR("comparison-token-0987654321"),
	})
	if response.Code != http.StatusOK {
		// The synthetic capture is not Salesforce traffic; when the route
		// refuses it the governance claim below cannot be observed.
		t.Skipf("synthetic capture refused by the Salesforce route: %d", response.Code)
	}
	if decoded.Summary.ArtifactMode == har.ArtifactModeDesignHARPair || decoded.Summary.HARDiff != nil {
		t.Fatalf("summary = %#v, want no pair claim from a route that does not compare", decoded.Summary)
	}
	if !hasHARPairWarning(decoded.Warnings, "The comparison HAR was not used") {
		t.Fatalf("warnings = %q, want the unused comparison stated", decoded.Warnings)
	}
}

// pairMultipartBody streams a multipart body whose file parts are produced on
// the fly, so the test holds only what the reader keeps.
func pairMultipartBody(t *testing.T, fields map[string]string, files []struct {
	field string
	size  int64
}) (io.Reader, string) {
	t.Helper()
	envelope := []byte(`{"log":{"entries":[]}}`)
	var parts []io.Reader
	header := &bytes.Buffer{}
	writer := multipart.NewWriter(header)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		if _, err := writer.CreateFormFile(file.field, file.field+".har"); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, bytes.NewReader(append([]byte(nil), header.Bytes()...)), bytes.NewReader(envelope), io.LimitReader(spaceReader{}, file.size-int64(len(envelope))))
		header.Reset()
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	parts = append(parts, bytes.NewReader(header.Bytes()))
	return io.MultiReader(parts...), writer.FormDataContentType()
}

type spaceReader struct{}

func (spaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestHARPairEnvelopeFitsTwoCapturesAtTheFileLimit(t *testing.T) {
	if harImportMaxBodyBytes < 2*harImportMaxFileBytes+(1<<20) {
		t.Fatalf("harImportMaxBodyBytes = %d, want room for two %d-byte captures plus framing", harImportMaxBodyBytes, harImportMaxFileBytes)
	}
	for route, want := range map[string]int64{
		"/api/v1/scripts/import/har": harImportMaxBodyBytes,
		// The Salesforce importer converts one capture, so it keeps a
		// one-capture envelope.
		"/api/v1/scripts/import/salesforce": salesforceImportMaxBodyBytes,
	} {
		// The pair travels as multipart, the transport whose reader streams.
		req := httptest.NewRequest(http.MethodPost, route, nil)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		if got := requestMaxBodyBytes(req); got != want {
			t.Fatalf("%s body limit = %d, want %d", route, got, want)
		}
	}
	// Two captures over half the file limit each: together they exceed what one
	// capture may be, which the former shared 301 MiB envelope refused.
	const each = harImportMaxFileBytes/2 + (1 << 20)
	body, contentType := pairMultipartBody(t, map[string]string{"project_id": "synthetic-project"}, []struct {
		field string
		size  int64
	}{{"file", each}, {"secondary_file", each}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id=synthetic-project", body)
	req.Header.Set("Content-Type", contentType)
	resp := httptest.NewRecorder()
	var got *harImportRequest
	var ok bool
	bodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got, ok = readHARImportRequest(w, r) })).ServeHTTP(resp, req)
	if !ok {
		t.Fatalf("pair rejected: %d %s", resp.Code, resp.Body.String())
	}
	if int64(len(got.Data)) != each || int64(len(got.SecondaryData)) != each {
		t.Fatalf("pair sizes = %d/%d, want %d each", len(got.Data), len(got.SecondaryData), each)
	}
}

func TestImportUploadLimitsRefuseOversizedArtifactsWith413(t *testing.T) {
	limits := importUploadLimits{Body: 64 << 10, File: 4 << 10, Files: harComparisonFileFields}
	type file = struct {
		field string
		size  int64
	}
	for name, test := range map[string]struct {
		files []file
		want  string
	}{
		"primary over the file limit":    {[]file{{"file", 5 << 10}}, `file\" file exceeds the 4096-byte import limit`},
		"comparison over the file limit": {[]file{{"file", 1 << 10}, {"secondary_file", 5 << 10}}, `secondary_file\" file exceeds`},
		"pair within both limits":        {[]file{{"file", 3 << 10}, {"comparison_file", 3 << 10}}, ""},
		"envelope over the body limit":   {[]file{{"file", 3 << 10}, {"other", 70 << 10}}, "the request exceeds the 65536-byte import limit"},
	} {
		t.Run(name, func(t *testing.T) {
			body, contentType := pairMultipartBody(t, nil, test.files)
			req := httptest.NewRequest(http.MethodPost, "/upload", body)
			req.Header.Set("Content-Type", contentType)
			resp := httptest.NewRecorder()
			upload, files, ok := decodeImportUploadWithLimits(resp, req, limits)
			if test.want == "" {
				if !ok || len(upload.Data) != 3<<10 || len(files["comparison_file"]) != 3<<10 {
					t.Fatalf("pair within limits rejected: %d %s", resp.Code, resp.Body.String())
				}
				return
			}
			if ok || resp.Code != http.StatusRequestEntityTooLarge || !strings.Contains(resp.Body.String(), test.want) {
				t.Fatalf("got ok=%v %d %s, want 413 mentioning %q", ok, resp.Code, resp.Body.String(), test.want)
			}
		})
	}
	t.Run("raw body over the file limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/upload", io.LimitReader(spaceReader{}, limits.File+1))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		if _, _, ok := decodeImportUploadWithLimits(resp, req, limits); ok || resp.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got ok=%v %d, want 413", ok, resp.Code)
		}
	})
	t.Run("JSON envelope over the body limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/upload", io.LimitReader(spaceReader{}, limits.Body+1))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		if _, _, ok := decodeImportUploadWithLimits(resp, req, limits); ok || resp.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got ok=%v %d, want 413", ok, resp.Code)
		}
	})
}

func TestHARImportProcessingWindowKeepsAShorterCallerDeadline(t *testing.T) {
	callerDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil).WithContext(ctx)
	processing, stop := harImportProcessingRequest(httptest.NewRecorder(), req)
	defer stop()
	if deadline, ok := processing.Context().Deadline(); !ok || !deadline.Equal(callerDeadline) {
		t.Fatalf("deadline = %v, want the caller's %v", deadline, callerDeadline)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil)
	processing, stop = harImportProcessingRequest(httptest.NewRecorder(), req)
	defer stop()
	if deadline, ok := processing.Context().Deadline(); !ok || time.Until(deadline) > harImportProcessingTimeout || time.Until(deadline) < harImportProcessingTimeout-time.Minute {
		t.Fatalf("deadline = %v, want about %s from now", deadline, harImportProcessingTimeout)
	}
}

func TestHARImportStopsAtTheNextStageOnceTheRequestEnds(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	resp := httptest.NewRecorder()
	if harImportStageOK(resp, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(canceled)) || resp.Body.Len() != 0 {
		t.Fatalf("canceled request continued or got a response: %q", resp.Body.String())
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	resp = httptest.NewRecorder()
	if harImportStageOK(resp, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(expired)) || resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expired request continued or got %d", resp.Code)
	}
	if !harImportStageOK(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil)) {
		t.Fatal("live request stopped")
	}
}

// trickleBody delivers body in chunks spread over duration, like a slow link.
func trickleBody(body []byte, duration time.Duration) io.Reader {
	reader, writer := io.Pipe()
	go func() {
		const chunks = 10
		step := (len(body) + chunks - 1) / chunks
		for start := 0; start < len(body); start += step {
			end := min(start+step, len(body))
			if _, err := writer.Write(body[start:end]); err != nil {
				return
			}
			time.Sleep(duration / chunks)
		}
		_ = writer.Close()
	}()
	return reader
}

func TestLargeUploadRouteOutlivesTheServerReadAndWriteTimeouts(t *testing.T) {
	// The production server's deadlines, scaled down so the slow upload takes
	// a second instead of the 15 s+ it would take against the real values.
	previousRead, previousWrite, previousWindow := apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow
	apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow = 300*time.Millisecond, 300*time.Millisecond, 30*time.Second
	t.Cleanup(func() {
		apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow = previousRead, previousWrite, previousWindow
	})
	server := NewServer(ServerConfig{})
	if server.httpServer.ReadTimeout != apiServerReadTimeout || server.httpServer.WriteTimeout != apiServerWriteTimeout {
		t.Fatalf("server timeouts = %s/%s, want the configured %s/%s", server.httpServer.ReadTimeout, server.httpServer.WriteTimeout, apiServerReadTimeout, apiServerWriteTimeout)
	}
	live := httptest.NewUnstartedServer(server.httpServer.Handler)
	live.Config.ReadTimeout = server.httpServer.ReadTimeout
	live.Config.WriteTimeout = server.httpServer.WriteTimeout
	live.Start()
	defer live.Close()

	post := func(route, contentType string, body []byte) (int, string, error) {
		req, err := http.NewRequest(http.MethodPost, live.URL+route, trickleBody(body, 1200*time.Millisecond))
		if err != nil {
			return 0, "", err
		}
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", contentType)
		resp, err := live.Client().Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		text, err := io.ReadAll(resp.Body)
		return resp.StatusCode, string(text), err
	}

	payload, err := json.Marshal(map[string]any{"name": "Slow upload", "content": apiCredentialCorrelationHAR("slow-upload-token-1234567890")})
	if err != nil {
		t.Fatal(err)
	}
	status, text, err := post("/api/v1/scripts/import/har", "application/json", payload)
	if err != nil || status != http.StatusOK {
		t.Fatalf("slow HAR upload: status %d err %v body %.300s", status, err, text)
	}

	// An ordinary route keeps the server's read timeout.
	status, text, err = post("/api/v1/scripts/import/sample", "application/json", payload)
	if err == nil && !strings.Contains(strings.ToLower(text), "timeout") {
		t.Fatalf("slow upload to an ordinary route did not time out: %d %.300s", status, text)
	}
}

func TestLargeUploadWindowIsGrantedOnlyToAuthenticatedCallers(t *testing.T) {
	previousRead, previousWrite, previousWindow := apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow
	apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow = 300*time.Millisecond, 300*time.Millisecond, 30*time.Second
	t.Cleanup(func() {
		apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow = previousRead, previousWrite, previousWindow
	})
	server := NewServer(ServerConfig{})
	enableTrustedHeaderAuth(t, server)
	live := httptest.NewUnstartedServer(server.httpServer.Handler)
	live.Config.ReadTimeout = server.httpServer.ReadTimeout
	live.Config.WriteTimeout = server.httpServer.WriteTimeout
	live.Start()
	defer live.Close()

	payload, err := json.Marshal(map[string]any{"name": "Slow upload", "content": apiCredentialCorrelationHAR("slow-upload-token-1234567890")})
	if err != nil {
		t.Fatal(err)
	}
	post := func(headers map[string]string, trickle time.Duration) (int, time.Duration, error) {
		req, err := http.NewRequest(http.MethodPost, live.URL+"/api/v1/scripts/import/har", trickleBody(payload, trickle))
		if err != nil {
			return 0, 0, err
		}
		req.ContentLength = int64(len(payload))
		req.Header.Set("Content-Type", "application/json")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		start := time.Now()
		resp, err := live.Client().Do(req)
		if err != nil {
			return 0, time.Since(start), err
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode, time.Since(start), nil
	}

	// Anonymous: the request is cut off at the server's read timeout, long
	// before the trickled body could finish.
	status, elapsed, err := post(nil, 3*time.Second)
	if err == nil && status == http.StatusOK {
		t.Fatalf("anonymous slow upload was imported")
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("anonymous slow upload held the connection for %s; want it ended at the %s read timeout", elapsed, apiServerReadTimeout)
	}

	// Authenticated: the same slow upload gets the large-upload window.
	status, _, err = post(map[string]string{"X-Auth-Request-User": "importer", "X-APEXLoad-Roles": RoleSystemAdmin}, 1200*time.Millisecond)
	if err != nil || status != http.StatusOK {
		t.Fatalf("authenticated slow upload: status %d err %v", status, err)
	}
}

func TestHARImportWindowAnswers503WhileAStageIsStillRunning(t *testing.T) {
	previous := harImportProcessingTimeout
	harImportProcessingTimeout = 200 * time.Millisecond
	t.Cleanup(func() { harImportProcessingTimeout = previous })

	release := make(chan struct{})
	lateWrite := make(chan error, 1)
	stageCanceled := make(chan bool, 1)
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runHARImportWithinWindow(w, r, func(w http.ResponseWriter, r *http.Request) {
			// A stage that does not check its context, like a conversion.
			<-release
			stageCanceled <- r.Context().Err() != nil
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"late":true}`))
			lateWrite <- err
		})
	}))
	defer live.Close()

	start := time.Now()
	resp, err := live.Client().Post(live.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "processing window") {
		t.Fatalf("got %d %s, want 503 for the expired window", resp.StatusCode, body)
	}
	if elapsed > harImportProcessingTimeout+harImportErrorWriteGrace {
		t.Fatalf("503 arrived after %s, want it within the window plus grace", elapsed)
	}

	close(release)
	if !<-stageCanceled {
		t.Fatal("the stage's request context was still live after the window ended")
	}
	if err := <-lateWrite; err == nil {
		t.Fatal("the late result was written after the handler answered")
	}
}

func TestHARImportWindowPassesOnResponsesAndPanics(t *testing.T) {
	resp := httptest.NewRecorder()
	runHARImportWithinWindow(resp, httptest.NewRequest(http.MethodPost, "/", nil), func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: nope")
	})
	if resp.Code != http.StatusUnprocessableEntity || resp.Header().Get("Content-Type") != "application/json" || !strings.Contains(resp.Body.String(), "nope") {
		t.Fatalf("got %d %q %s, want the import's own response", resp.Code, resp.Header().Get("Content-Type"), resp.Body.String())
	}

	resp = httptest.NewRecorder()
	runHARImportWithinWindow(resp, httptest.NewRequest(http.MethodPost, "/", nil), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status-only response = %d, want 204", resp.Code)
	}

	defer func() {
		if recovered := recover(); recovered != "converter bug" {
			t.Fatalf("recovered %v, want the import's panic on the handler goroutine", recovered)
		}
	}()
	runHARImportWithinWindow(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), func(http.ResponseWriter, *http.Request) {
		panic("converter bug")
	})
}

// harPairLoadHAR is a synthetic capture large enough that converting it takes
// a measurable time.
func harPairLoadHAR(entries int) string {
	parts := make([]string, 0, 2*entries)
	for index := 0; index < entries; index++ {
		token := fmt.Sprintf("Tok%06dXq7Zr%06dLm", index, index*7)
		parts = append(parts,
			fmt.Sprintf(`{"startedDateTime":"2026-01-01T00:00:00Z","time":1,"request":{"method":"POST","url":"https://load.example.test/p%d","headers":[]},"response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":%q}}}`, index, fmt.Sprintf(`{"access_token":%q,"pad":%q}`, token, strings.Repeat("a", 2000))),
			fmt.Sprintf(`{"startedDateTime":"2026-01-01T00:00:01Z","time":1,"request":{"method":"GET","url":"https://load.example.test/u%d?access_token=%s","headers":[]},"response":{"status":200,"headers":[],"content":{"text":"ok"}}}`, index, token))
	}
	return `{"log":{"entries":[` + strings.Join(parts, ",") + `]}}`
}

func harComparisonConversionRunning() bool {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	return bytes.Contains(buf[:n], []byte("prepareHARComparison"))
}

func TestHARComparisonDoesNotOutliveAFailedImport(t *testing.T) {
	response, _ := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
		"name":              "Broken baseline",
		"content":           `{"foo":1}`,
		"secondary_content": harPairLoadHAR(1500),
	})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want the baseline failure: %s", response.Code, response.Body.String())
	}
	// The error response does not wait for the comparison, which ends on its
	// own soon after.
	deadline := time.Now().Add(30 * time.Second)
	for harComparisonConversionRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the comparison conversion was still running 30s after the import returned")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHARBaselineFailureDoesNotWaitForTheComparison(t *testing.T) {
	// A baseline that fails at once can cancel its comparison before the
	// comparison goroutine reaches the converter, which is correct but leaves
	// nothing mid-conversion to observe. Retry until the comparison is caught
	// converting; every attempt still checks that a failed baseline answers
	// without waiting for it.
	for attempt := 0; attempt < 100; attempt++ {
		if holdBaselineFailureWhileComparisonConverts(t) {
			return
		}
	}
	t.Fatal("the comparison never started converting before its import failed")
}

// holdBaselineFailureWhileComparisonConverts runs one failed-baseline import
// and reports whether its comparison was caught mid-conversion.
func holdBaselineFailureWhileComparisonConverts(t *testing.T) bool {
	t.Helper()
	// The comparison is held mid-conversion until the test lets it go.
	started := make(chan context.Context, 1)
	hold := make(chan struct{})
	ended := make(chan struct{})
	var once sync.Once
	previous, previousSlots := harComparisonPreparer, harComparisonSlots
	harComparisonPreparer = func(ctx context.Context, data []byte, opts har.ConvertOptions) (*har.ComparisonRecording, *har.TruncationSalvage, error) {
		defer once.Do(func() { close(ended) })
		started <- ctx
		<-hold
		return nil, nil, ctx.Err()
	}
	// Free slots of its own: comparisons earlier tests left converting would
	// otherwise hold the shared ones, so every attempt's comparison would wait
	// for a slot until its import had already failed.
	harComparisonSlots = make(chan struct{}, 2)
	defer func() { harComparisonPreparer, harComparisonSlots = previous, previousSlots }()

	payload, err := json.Marshal(map[string]any{
		"name":              "Broken baseline",
		"content":           `{"foo":1}`,
		"secondary_content": harPairLoadHAR(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		NewServer(ServerConfig{}).mux.ServeHTTP(response, req)
		responses <- response
	}()
	var comparisonCtx context.Context
	var response *httptest.ResponseRecorder
	select {
	case comparisonCtx = <-started:
		select {
		case response = <-responses:
		case <-time.After(30 * time.Second):
			close(hold)
			t.Fatal("the baseline failure waited for the comparison conversion")
		}
	case response = <-responses:
		// The import failed before its comparison reached the converter. The
		// comparison must then stay out of it for good.
		select {
		case <-started:
			t.Fatal("a comparison canceled before it started still converted")
		case <-time.After(200 * time.Millisecond):
		}
	}
	if response.Code != http.StatusUnprocessableEntity {
		if comparisonCtx != nil {
			close(hold)
		}
		t.Fatalf("status = %d, want the baseline failure: %s", response.Code, response.Body.String())
	}
	if comparisonCtx == nil {
		return false
	}
	// The import canceled the comparison it no longer needs.
	select {
	case <-comparisonCtx.Done():
	case <-time.After(5 * time.Second):
		close(hold)
		t.Fatal("the comparison was not canceled when the import failed")
	}
	close(hold)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the comparison did not end once released")
	}
	return true
}

func TestStreamedMultipartIsBoundedLikeReadForm(t *testing.T) {
	limits := importUploadLimits{Body: 64 << 20, File: 1 << 20, Files: harComparisonFileFields}
	build := func(fields int, nameBytes int) (io.Reader, string) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		for index := 0; index < fields; index++ {
			name := fmt.Sprintf("n%d_%s", index, strings.Repeat("x", nameBytes))
			if err := writer.WriteField(name, ""); err != nil {
				t.Fatal(err)
			}
		}
		part, err := writer.CreateFormFile("file", "capture.har")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(`{"log":{"entries":[]}}`))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return body, writer.FormDataContentType()
	}
	for name, test := range map[string]struct {
		fields, nameBytes int
		want              string
	}{
		"within the part limit":   {fields: 900, nameBytes: 8},
		"too many parts":          {fields: importMultipartMaxParts, nameBytes: 8, want: "more than 1000 parts"},
		"field names over budget": {fields: 990, nameBytes: 12 << 10, want: "multipart form fields exceed their limit"},
	} {
		t.Run(name, func(t *testing.T) {
			body, contentType := build(test.fields, test.nameBytes)
			req := httptest.NewRequest(http.MethodPost, "/upload", body)
			req.Header.Set("Content-Type", contentType)
			resp := httptest.NewRecorder()
			_, _, ok := decodeImportUploadWithLimits(resp, req, limits)
			if test.want == "" {
				if !ok {
					t.Fatalf("rejected: %d %s", resp.Code, resp.Body.String())
				}
				return
			}
			if ok || resp.Code != http.StatusRequestEntityTooLarge || !strings.Contains(resp.Body.String(), test.want) {
				t.Fatalf("got ok=%v %d %s, want 413 mentioning %q", ok, resp.Code, resp.Body.String(), test.want)
			}
		})
	}
}

func TestSalesforceUploadDrainsTheComparisonWithoutKeepingIt(t *testing.T) {
	body, contentType := pairMultipartBody(t, nil, []struct {
		field string
		size  int64
	}{{"file", 4 << 10}, {"secondary_file", 64 << 10}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/salesforce", body)
	req.Header.Set("Content-Type", contentType)
	resp := httptest.NewRecorder()
	got, comparisonSent, ok := readSalesforceImportRequest(resp, req)
	if !ok {
		t.Fatalf("rejected: %d %s", resp.Code, resp.Body.String())
	}
	if !comparisonSent || len(got.SecondaryData) != 0 || len(got.Data) != 4<<10 || got.Opts.ArtifactMode == har.ArtifactModeDesignHARPair {
		t.Fatalf("comparisonSent=%v secondary=%d data=%d mode=%q, want the comparison noted but not kept", comparisonSent, len(got.SecondaryData), len(got.Data), got.Opts.ArtifactMode)
	}

	body, contentType = pairMultipartBody(t, nil, []struct {
		field string
		size  int64
	}{{"file", 4 << 10}})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/salesforce", body)
	req.Header.Set("Content-Type", contentType)
	if _, comparisonSent, ok = readSalesforceImportRequest(httptest.NewRecorder(), req); !ok || comparisonSent {
		t.Fatalf("single capture: ok=%v comparisonSent=%v", ok, comparisonSent)
	}
}

func TestHARImportWindowKeepsTheRequestIDOnServerErrors(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil)
	response := httptest.NewRecorder()
	response.Header().Set(requestIDHeader, "req-abc-123")
	runHARImportWithinWindow(response, req, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "boom")
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", response.Code, response.Body.String())
	}
	var body struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", response.Body.String(), err)
	}
	if body.Ref != "req-abc-123" {
		t.Fatalf("ref = %q, want the request ID req-abc-123", body.Ref)
	}
	if got := response.Header().Get(requestIDHeader); got != "req-abc-123" {
		t.Fatalf("%s = %q, want req-abc-123", requestIDHeader, got)
	}
}

func TestHARComparisonWaitingForASlotEndsWithItsImport(t *testing.T) {
	previousSlots, previousPreparer := harComparisonSlots, harComparisonPreparer
	defer func() { harComparisonSlots, harComparisonPreparer = previousSlots, previousPreparer }()
	// The only slot is taken, as by another import's comparison.
	harComparisonSlots = make(chan struct{}, 1)
	harComparisonSlots <- struct{}{}
	prepared := make(chan struct{}, 1)
	harComparisonPreparer = func(ctx context.Context, data []byte, opts har.ConvertOptions) (*har.ComparisonRecording, *har.TruncationSalvage, error) {
		prepared <- struct{}{}
		return nil, nil, nil
	}

	job := startHARComparison(context.Background(), []byte(harPairLoadHAR(1)), har.ConvertOptions{})
	select {
	case <-job.done:
		t.Fatal("the comparison ran without a slot")
	case <-time.After(50 * time.Millisecond):
	}
	job.release()
	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a released comparison kept waiting for a slot")
	}
	if !errors.Is(job.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", job.err)
	}
	select {
	case <-prepared:
		t.Fatal("a comparison released while queued was still converted")
	default:
	}

	// Once the slot frees, the next comparison runs and gives it back.
	<-harComparisonSlots
	next := startHARComparison(context.Background(), []byte(harPairLoadHAR(1)), har.ConvertOptions{})
	defer next.release()
	select {
	case <-next.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a comparison with a free slot never ran")
	}
	if len(prepared) != 1 || len(harComparisonSlots) != 0 {
		t.Fatalf("prepared = %d, slots in use = %d; want 1 and 0", len(prepared), len(harComparisonSlots))
	}
}

func TestHARPairReturnsTheBaselineWhenItsComparisonCannotFinishInTime(t *testing.T) {
	previousSlots, previousTimeout := harComparisonSlots, harImportProcessingTimeout
	defer func() { harComparisonSlots, harImportProcessingTimeout = previousSlots, previousTimeout }()
	// Both slots are held, as by two other imports' long comparisons, so this
	// import's comparison stays queued.
	harComparisonSlots = make(chan struct{}, 2)
	harComparisonSlots <- struct{}{}
	harComparisonSlots <- struct{}{}
	// The import gives up on the comparison with a quarter of the window left,
	// which leaves the later stages about 1 s here. A 200 (rather than the
	// window's 503) shows it stopped waiting in time.
	harImportProcessingTimeout = 4 * time.Second

	response, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
		"name":              "Pair",
		"content":           apiCredentialCorrelationHAR("baseline-token-1234567890"),
		"secondary_content": apiCredentialCorrelationHAR("comparison-token-0987654321"),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want the baseline import: %s", response.Code, response.Body.String())
	}
	diff := decoded.Summary.HARDiff
	if diff == nil || diff.Compared || !strings.Contains(diff.Reason, "did not finish converting") {
		t.Fatalf("har_diff = %#v, want compared=false with the out-of-time reason", diff)
	}
	if decoded.Summary.ArtifactMode != har.ArtifactModeDesignHAR {
		t.Fatalf("artifact_mode = %q, want a single design HAR when nothing was compared", decoded.Summary.ArtifactMode)
	}
	if !hasHARPairWarning(decoded.Warnings, "The comparison HAR was not compared") {
		t.Fatalf("warnings = %q, want the skipped comparison explained", decoded.Warnings)
	}
	if len(harComparisonSlots) != 2 {
		t.Fatalf("slots in use = %d, want the 2 held by the other imports", len(harComparisonSlots))
	}
}

func TestHARComparisonWaitKeepsTimeForTheStagesAfterIt(t *testing.T) {
	job := &harComparisonJob{done: make(chan struct{}), cancel: func() {}} // never finishes
	for name, tc := range map[string]struct {
		conversion time.Duration
		wantWait   time.Duration
	}{
		// A quick baseline keeps a quarter of the time left.
		"quick baseline": {conversion: time.Millisecond, wantWait: 900 * time.Millisecond},
		// A slow one keeps ten times its conversion, at most half.
		"slow baseline": {conversion: 200 * time.Millisecond, wantWait: 600 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
			defer cancel()
			start := time.Now()
			if job.wait(ctx, tc.conversion) {
				t.Fatal("wait reported an unfinished comparison as finished")
			}
			elapsed := time.Since(start)
			if elapsed < tc.wantWait-150*time.Millisecond || elapsed > tc.wantWait+400*time.Millisecond {
				t.Fatalf("waited %s, want about %s", elapsed, tc.wantWait)
			}
			if ctx.Err() != nil {
				t.Fatal("wait used up the import's own window")
			}
		})
	}
}

func TestHARComparisonGivenUpOnDoesNotTakeAFreedSlot(t *testing.T) {
	previousSlots, previousPreparer := harComparisonSlots, harComparisonPreparer
	defer func() { harComparisonSlots, harComparisonPreparer = previousSlots, previousPreparer }()
	harComparisonSlots = make(chan struct{}, 1)
	harComparisonSlots <- struct{}{} // held, as by another import's comparison
	converted := make(chan struct{}, 1)
	harComparisonPreparer = func(ctx context.Context, data []byte, opts har.ConvertOptions) (*har.ComparisonRecording, *har.TruncationSalvage, error) {
		converted <- struct{}{}
		return nil, nil, nil
	}

	// The import's own context stays live: it gave up on the comparison only
	// to keep time for its later stages.
	importCtx, cancelImport := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancelImport()
	job := startHARComparison(context.Background(), []byte(harPairLoadHAR(1)), har.ConvertOptions{})
	if job.wait(importCtx, time.Millisecond) {
		t.Fatal("a comparison queued for a slot was reported finished")
	}
	// The other import's comparison then gives its slot back.
	<-harComparisonSlots
	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a comparison given up on kept waiting")
	}
	if !errors.Is(job.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", job.err)
	}
	select {
	case <-converted:
		t.Fatal("a comparison given up on took the freed slot and converted")
	default:
	}
	if len(harComparisonSlots) != 0 {
		t.Fatalf("slots in use = %d, want the freed slot left free", len(harComparisonSlots))
	}
}

func TestHARImportProcessingOutlivesTheUploadGrantBehindTheActivityBuffer(t *testing.T) {
	// Scaled-down server deadlines: the grant made at authentication covers
	// about 400 ms, and the comparison holds the import for longer, so the
	// response is only written in time if the processing window's own write
	// deadline reaches the connection through objectActivityMiddleware's
	// response buffer.
	previousRead, previousWrite, previousWindow, previousGrace := apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow, largeUploadWriteGrace
	previousPreparer := harComparisonPreparer
	apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow, largeUploadWriteGrace = 300*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond
	harComparisonPreparer = func(ctx context.Context, data []byte, opts har.ConvertOptions) (*har.ComparisonRecording, *har.TruncationSalvage, error) {
		time.Sleep(1500 * time.Millisecond)
		return nil, nil, errors.New("comparison HAR: held for the test")
	}
	t.Cleanup(func() {
		apiServerReadTimeout, apiServerWriteTimeout, largeUploadReadWindow, largeUploadWriteGrace = previousRead, previousWrite, previousWindow, previousGrace
		harComparisonPreparer = previousPreparer
	})
	server := NewServer(ServerConfig{})
	live := httptest.NewUnstartedServer(server.httpServer.Handler)
	live.Config.ReadTimeout = server.httpServer.ReadTimeout
	live.Config.WriteTimeout = server.httpServer.WriteTimeout
	live.Start()
	defer live.Close()

	payload, err := json.Marshal(map[string]any{
		"name":              "Pair",
		"content":           apiCredentialCorrelationHAR("baseline-token-1234567890"),
		"secondary_content": apiCredentialCorrelationHAR("comparison-token-0987654321"),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := live.Client().Post(live.URL+"/api/v1/scripts/import/har", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("import response was lost: %v", err)
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d err %v body %.300s", resp.StatusCode, err, text)
	}
	if !strings.Contains(string(text), "held for the test") {
		t.Fatalf("body %.300s, want the baseline with the comparison's failure", text)
	}
}

func unhandledInputPairHAR(token string) string {
	return `{"log":{"entries":[{"startedDateTime":"2026-01-01T00:00:00Z","time":10,` +
		`"request":{"method":"POST","url":"https://app.test/api/save","headers":[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],` +
		`"postData":{"mimeType":"application/x-www-form-urlencoded","text":"token=` + token + `"}},` +
		`"response":{"status":200,"headers":[],"content":{"mimeType":"text/plain","text":"ok"}}}]}}`
}

// The import compares the pair request by request on the final script and
// reports the request values that changed between the recordings but are
// still sent as recorded, in har_diff, a warning and the pair diff section.
func TestHARPairImportReportsUnhandledRequestInputs(t *testing.T) {
	response, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
		"name":              "Pair",
		"content":           unhandledInputPairHAR("Zq9vT0kenAAA"),
		"secondary_content": unhandledInputPairHAR("Pp8uU1lfoBBB"),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	diff := decoded.Summary.HARDiff
	if diff == nil || !diff.Compared || len(diff.UnhandledInputs) != 1 || diff.UnhandledInputs[0].Field != "form:token" || diff.UnhandledInputs[0].Kind != har.DiffInputUnknown || diff.UnhandledInputs[0].Requests != 1 {
		t.Fatalf("har_diff = %#v, want form:token as an unhandled input", diff)
	}
	if strings.Contains(response.Body.String(), "Pp8uU1lfoBBB") {
		t.Fatal("the response discloses a comparison value")
	}
	if !hasHARPairWarning(decoded.Warnings, "changed between the two recordings and are still sent as recorded") {
		t.Fatalf("warnings = %q, want the unhandled inputs summarized", decoded.Warnings)
	}
	for _, section := range decoded.ReviewSections {
		if section.Key == "har_pair_diff" && section.Severity != "warning" {
			t.Fatalf("pair diff section severity = %q, want warning", section.Severity)
		}
	}
}

package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"google.golang.org/grpc/test/bufconn"
)

type chunkedPlaywrightResponse struct {
	playwright.Response
	status  int
	headers map[string]string
	body    []byte
}

func (r *chunkedPlaywrightResponse) Status() int                { return r.status }
func (r *chunkedPlaywrightResponse) Headers() map[string]string { return r.headers }
func (r *chunkedPlaywrightResponse) Body() ([]byte, error)      { return r.body, nil }
func (r *chunkedPlaywrightResponse) URL() string                { return "http://127.0.0.1/chunked" }
func (r *chunkedPlaywrightResponse) SecurityDetails() (*playwright.ResponseSecurityDetailsResult, error) {
	return nil, nil
}
func (r *chunkedPlaywrightResponse) ServerAddr() (*playwright.ResponseServerAddrResult, error) {
	return nil, nil
}

type chunkedPlaywrightRequest struct {
	playwright.Request
	url      string
	method   string
	headers  map[string]string
	postBody []byte
	postText string
	response playwright.Response
	sizes    *playwright.RequestSizesResult
}

func (r *chunkedPlaywrightRequest) URL() string                { return r.url }
func (r *chunkedPlaywrightRequest) Method() string             { return r.method }
func (r *chunkedPlaywrightRequest) ResourceType() string       { return "fetch" }
func (r *chunkedPlaywrightRequest) Headers() map[string]string { return r.headers }
func (r *chunkedPlaywrightRequest) HeaderValue(k string) (string, error) {
	return r.headers[strings.ToLower(k)], nil
}
func (r *chunkedPlaywrightRequest) PostDataBuffer() ([]byte, error) { return r.postBody, nil }
func (r *chunkedPlaywrightRequest) PostData() (string, error)       { return r.postText, nil }
func (r *chunkedPlaywrightRequest) Response() (playwright.Response, error) {
	return r.response, nil
}
func (r *chunkedPlaywrightRequest) Sizes() (*playwright.RequestSizesResult, error) {
	return r.sizes, nil
}
func (r *chunkedPlaywrightRequest) Timing() *playwright.RequestTiming {
	return &playwright.RequestTiming{ResponseEnd: 5}
}
func (r *chunkedPlaywrightRequest) Frame() playwright.Frame            { return nil }
func (r *chunkedPlaywrightRequest) IsNavigationRequest() bool          { return false }
func (r *chunkedPlaywrightRequest) RedirectedFrom() playwright.Request { return nil }

func newChunkedHTTPTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			},
		},
	}
	return server, client
}

// TestHARCaptureChunkedFiniteBodyCapsSequential streams finite chunked bodies
// just above the 512 KiB per-resource cap, 32 MiB session retained-body budget,
// and 100 MiB native HAR working-file cap sequentially (without t.Parallel) so
// memory stays bounded under -race.
func TestHARCaptureChunkedFiniteBodyCapsSequential(t *testing.T) {
	const (
		overPerResourceBytes = browserNetworkBodyCaptureLimitBytes + 16*1024
		chunkSize            = 64 * 1024
	)

	_, client := newChunkedHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not support chunked flushing")
			return
		}
		var totalBytes int
		stepBytes := chunkSize
		switch r.URL.Path {
		case "/chunked-512k-exact":
			totalBytes = browserNetworkBodyCaptureLimitBytes
		case "/chunked-100m":
			totalBytes = int(maxRuntimeHARBytes) + 64*1024
			stepBytes = 1 << 20
		default:
			totalBytes = overPerResourceBytes
		}
		chunk := bytes.Repeat([]byte("a"), stepBytes)
		remaining := totalBytes
		for remaining > 0 {
			writeLen := min(remaining, len(chunk))
			if _, err := w.Write(chunk[:writeLen]); err != nil {
				return
			}
			flusher.Flush()
			remaining -= writeLen
		}
	}))

	t.Run("512KiB_per_resource_cap_and_utf8_boundary", func(t *testing.T) {
		// Construct a UTF-8 request body where a 3-byte rune ("€") straddles the
		// exact 512 KiB boundary, and stream it as a chunked HTTP request.
		prefix := bytes.Repeat([]byte("x"), browserNetworkBodyCaptureLimitBytes-1)
		utf8Body := append(prefix, []byte("€")...)
		utf8Body = append(utf8Body, bytes.Repeat([]byte("y"), 16*1024)...)

		req, err := http.NewRequest(http.MethodPost, "http://chunked.test/chunked-512k", io.NopCloser(bytes.NewReader(utf8Body)))
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = -1 // force Transfer-Encoding: chunked
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(resp.TransferEncoding, "chunked") {
			t.Fatalf("TransferEncoding = %v, want chunked", resp.TransferEncoding)
		}
		respBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(respBytes) != overPerResourceBytes {
			t.Fatalf("chunked response bytes = %d, want %d", len(respBytes), overPerResourceBytes)
		}

		replayCapture, err := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{
			Bodies:            "both",
			MaxBodyBytes:      browserNetworkBodyCaptureLimitBytes,
			MaxTotalBodyBytes: 16 << 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		b := inflightBrowser(t, false)
		b.replayHAR = replayCapture
		gen := b.evidence.generation

		pwResp := &chunkedPlaywrightResponse{
			status:  http.StatusOK,
			headers: map[string]string{"content-type": "text/plain; charset=utf-8"},
			body:    respBytes,
		}
		pwReq := &chunkedPlaywrightRequest{
			url:      "http://chunked.test/chunked-512k",
			method:   http.MethodPost,
			headers:  map[string]string{"content-type": "text/plain; charset=utf-8"},
			postBody: utf8Body,
			response: pwResp,
			sizes: &playwright.RequestSizesResult{
				RequestHeadersSize:  -1, // verify negative unknown Playwright sizes clamp to 0
				RequestBodySize:     len(utf8Body),
				ResponseHeadersSize: 64,
				ResponseBodySize:    len(respBytes),
			},
		}
		b.recordNetworkTask(browserNetworkTask{
			generation: gen,
			request:    pwReq,
			finished:   true,
		})
		if len(b.networkEntries) != 1 {
			t.Fatalf("networkEntries = %d, want 1", len(b.networkEntries))
		}
		entry := b.networkEntries[0]
		if !entry.RequestBodyTruncated {
			t.Fatal("expected RequestBodyTruncated = true for >512 KiB request body")
		}
		if len(entry.RequestBody) != browserNetworkBodyCaptureLimitBytes-1 {
			t.Fatalf("len(RequestBody) = %d, want %d (straddling UTF-8 rune trimmed cleanly)", len(entry.RequestBody), browserNetworkBodyCaptureLimitBytes-1)
		}
		if !utf8.ValidString(entry.RequestBody) || entry.RequestBodyEncoding != "" {
			t.Fatalf("straddling UTF-8 rune produced invalid UTF-8 or base64 encoding=%q", entry.RequestBodyEncoding)
		}
		if entry.BytesReceived != int64(64+len(respBytes)) {
			t.Fatalf("BytesReceived = %d, want %d", entry.BytesReceived, 64+len(respBytes))
		}
		if entry.HAR == nil || entry.HAR.ResponseBody == nil || entry.HAR.ResponseBody.State != "size_limit" || entry.HAR.ResponseBody.Text != "" {
			t.Fatalf("ResponseBody = %+v, want State=size_limit and empty Text", entry.HAR.ResponseBody)
		}

		// Also verify a binary body above 384 KiB raw is capped so its base64
		// representation never exceeds 512 KiB.
		binaryBody := bytes.Repeat([]byte{0xff, 0xfe, 0xfd, 0x80}, (browserNetworkBodyCaptureLimitBytes/4)+1024)
		binBody, binEncoding, binTruncated := browserRequestBody(&chunkedPlaywrightRequest{postBody: binaryBody})
		if !binTruncated || binEncoding != "base64" || len(binBody) > browserNetworkBodyCaptureLimitBytes {
			t.Fatalf("binary request body cap: truncated=%v encoding=%q len=%d (max %d)", binTruncated, binEncoding, len(binBody), browserNetworkBodyCaptureLimitBytes)
		}
	})

	t.Run("32MiB_session_body_budget", func(t *testing.T) {
		resp, err := client.Get("http://chunked.test/chunked-512k-exact")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(resp.TransferEncoding, "chunked") {
			t.Fatalf("TransferEncoding = %v, want chunked", resp.TransferEncoding)
		}
		bodyChunk, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(bodyChunk) != browserNetworkBodyCaptureLimitBytes {
			t.Fatalf("chunked body len = %d, want %d", len(bodyChunk), browserNetworkBodyCaptureLimitBytes)
		}

		replayCapture, err := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{
			Bodies:            "response",
			MaxBodyBytes:      browserNetworkBodyCaptureLimitBytes,
			MaxTotalBodyBytes: 16 << 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		b := inflightBrowser(t, false)
		b.replayHAR = replayCapture
		gen := b.evidence.generation
		const totalRequests = 66 // 66 * 512 KiB = 33 MiB > 32 MiB budget
		for i := range totalRequests {
			pwResp := &chunkedPlaywrightResponse{
				status:  http.StatusOK,
				headers: map[string]string{"content-type": "text/plain; charset=utf-8"},
				body:    bodyChunk,
			}
			pwReq := &chunkedPlaywrightRequest{
				url:      fmt.Sprintf("http://chunked.test/item-%d", i),
				method:   http.MethodPost,
				headers:  map[string]string{"content-type": "text/plain"},
				postBody: bodyChunk,
				response: pwResp,
				sizes: &playwright.RequestSizesResult{
					RequestBodySize:  len(bodyChunk),
					ResponseBodySize: len(bodyChunk),
				},
			}
			b.recordNetworkTask(browserNetworkTask{
				generation: gen,
				request:    pwReq,
				finished:   true,
			})
		}
		if b.networkBodyBytes > browserNetworkBodyBudgetBytes {
			t.Fatalf("networkBodyBytes = %d, exceeds 32 MiB budget (%d)", b.networkBodyBytes, browserNetworkBodyBudgetBytes)
		}
		if b.networkEntriesDropped < 2 {
			t.Fatalf("networkEntriesDropped = %d, want >= 2 after 33 MiB of bodies", b.networkEntriesDropped)
		}
		lastEntry := b.networkEntries[len(b.networkEntries)-1]
		if lastEntry.HAR == nil || lastEntry.HAR.ResponseBody == nil || lastEntry.HAR.ResponseBody.State != "total_limit" {
			t.Fatalf("last entry ReplayHAR ResponseBody = %+v, want State=total_limit after 32 MiB total budget", lastEntry.HAR.ResponseBody)
		}
	})

	t.Run("100MiB_native_har_cap_with_owned_writer", func(t *testing.T) {
		budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
		dir := t.TempDir()
		owner := fixtureHAROwner(t, dir)
		t.Cleanup(owner.finishFinalizationBudget)
		if err := owner.beginWriter(); err != nil {
			t.Fatal(err)
		}
		var closeCalls atomic.Int32
		owner.bindWriter(func() error {
			closeCalls.Add(1)
			return nil
		})

		b := inflightBrowser(t, false)
		b.harCapturePolicy = HARCaptureAlways
		b.harOwner = owner
		b.harWorkingPath = owner.path
		b.evidence.documents["doc-100m"] = &browserDocumentState{
			observations: map[string]protocol.BrowserDocumentObservation{
				"lcp": {State: "provisional", Available: true, Qualified: true},
			},
		}
		b.startHARMonitor()
		t.Cleanup(func() {
			b.mu.Lock()
			stop := b.harMonitorStop
			b.harMonitorStop = nil
			b.mu.Unlock()
			if stop != nil {
				close(stop)
			}
			b.harMonitorWG.Wait()
		})

		// Stream a >100 MiB finite chunked HTTP response from newChunkedHTTPTestServer
		// into the owned working.har file using a single reusable 1 MiB buffer so RSS
		// stays small under -race.
		resp, err := client.Get("http://chunked.test/chunked-100m")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(resp.TransferEncoding, "chunked") {
			_ = resp.Body.Close()
			t.Fatalf("TransferEncoding = %v, want chunked", resp.TransferEncoding)
		}
		f, err := os.OpenFile(owner.path, os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = resp.Body.Close()
			t.Fatal(err)
		}
		buf := make([]byte, 1<<20)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := f.Write(buf[:n]); writeErr != nil {
					_ = f.Close()
					_ = resp.Body.Close()
					t.Fatal(writeErr)
				}
				b.noteHARBytes(int64(n), b.evidence.generation)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = f.Close()
				_ = resp.Body.Close()
				t.Fatal(readErr)
			}
		}
		_ = resp.Body.Close()
		if closeErr := f.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}

		// Wait for the monitor goroutine to process the 100 MiB overflow.
		b.harMonitorWG.Wait()
		<-owner.closeDone

		if budget.counters.harOverflow.Load() != 1 {
			t.Fatalf("harOverflow = %d, want 1", budget.counters.harOverflow.Load())
		}
		if b.resourceLimit != resourceLimitHARCapture {
			t.Fatalf("resourceLimit = %q, want %q", b.resourceLimit, resourceLimitHARCapture)
		}
		obs := b.evidence.documents["doc-100m"].observations["lcp"]
		if obs.State != "censored" || obs.Available || obs.Qualified || obs.Boundary != "resource_limit" {
			t.Fatalf("document observation after 100 MiB overflow = %+v", obs)
		}
		// Mid-session 100 MiB overflow must call owner.startClose() without
		// starting the 30 s teardown finalization deadline.
		owner.mu.Lock()
		deadlineStartedMidSession := !owner.finalizationDeadline.IsZero()
		owner.mu.Unlock()
		if deadlineStartedMidSession {
			t.Fatal("mid-session 100 MiB HAR overflow started the 30s teardown finalization budget")
		}

		// Teardown finalization refuses to publish the >100 MiB working file,
		// records the capture omission, and retires the joined raw generation.
		artifactPath, finErr := b.finalizeHARCaptureContext(context.Background())
		if artifactPath != "" || finErr == nil || !strings.Contains(finErr.Error(), "browser HAR exceeds size limit") {
			t.Fatalf("finalizeHARCaptureContext(>100 MiB) = (%q, %v), want size limit error", artifactPath, finErr)
		}
		if !owner.retiredSuccessfully() || closeCalls.Load() != 1 {
			t.Fatalf("owner retired=%v closeCalls=%d, want true and 1", owner.retiredSuccessfully(), closeCalls.Load())
		}
		if _, statErr := os.Stat(owner.path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("joined oversized working.har still exists: %v", statErr)
		}
		omissions := b.TakeBrowserHARCaptureOmissions()
		if omissions[protocol.BrowserHAROmittedCaptureFailed] != 1 {
			t.Fatalf("omissions = %v, want capture_failed=1", omissions)
		}
	})
}

// TestHARCaptureAllSixOrdersOverflowCancelAndNativeClose exercises all six
// permutations of (cap_overflow, caller_cancel, native_close) across both the
// 2-second mid-session close wait (startClose) and the 30-second teardown
// finalization budget (startFinish), verifying censored document evidence,
// exact writer join/retention without orphan leaks, and deadline enforcement.
func TestHARCaptureAllSixOrdersOverflowCancelAndNativeClose(t *testing.T) {
	orders := [][]string{
		{"cap_overflow", "caller_cancel", "native_close"},
		{"cap_overflow", "native_close", "caller_cancel"},
		{"caller_cancel", "cap_overflow", "native_close"},
		{"caller_cancel", "native_close", "cap_overflow"},
		{"native_close", "cap_overflow", "caller_cancel"},
		{"native_close", "caller_cancel", "cap_overflow"},
	}

	for _, mode := range []string{"mid_session_2s_close_wait", "teardown_30s_within_budget", "teardown_30s_expired_budget"} {
		t.Run(mode, func(t *testing.T) {
			for _, order := range orders {
				orderName := strings.Join(order, "_then_")
				t.Run(orderName, func(t *testing.T) {
					budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
					dir := t.TempDir()
					owner := fixtureHAROwner(t, dir)
					t.Cleanup(owner.finishFinalizationBudget)
					writeOwnedHARFixture(t, owner)
					if err := owner.beginWriter(); err != nil {
						t.Fatal(err)
					}

					writerEntered := make(chan struct{})
					var writerEnteredOnce sync.Once
					releaseWriter := make(chan struct{})
					var releaseWriterOnce sync.Once
					unblockWriter := func() {
						releaseWriterOnce.Do(func() { close(releaseWriter) })
					}
					t.Cleanup(unblockWriter)

					var closes atomic.Int32
					owner.bindWriter(func() error {
						closes.Add(1)
						writerEnteredOnce.Do(func() { close(writerEntered) })
						<-releaseWriter
						return nil
					})

					b := inflightBrowser(t, false)
					b.harCapturePolicy = HARCaptureOnError
					b.harOwner = owner
					b.harWorkingPath = owner.path
					b.evidence.documents["doc"] = &browserDocumentState{
						observations: map[string]protocol.BrowserDocumentObservation{
							"inp": {State: "provisional", Available: true, Qualified: true},
						},
					}
					b.startHARMonitor()
					stopMonitor := func() {
						b.mu.Lock()
						stop := b.harMonitorStop
						b.harMonitorStop = nil
						b.mu.Unlock()
						if stop != nil {
							close(stop)
						}
						b.harMonitorWG.Wait()
					}
					t.Cleanup(stopMonitor)

					allStepsComplete := make(chan struct{})
					var allStepsOnce sync.Once
					finishSteps := func() {
						allStepsOnce.Do(func() { close(allStepsComplete) })
					}
					t.Cleanup(finishSteps)

					if mode != "mid_session_2s_close_wait" {
						// Mirror closeWithContext (lifecycle.go:100-121): teardown starts
						// runtimeJoin (which waits for closeDone and harMonitorWG.Wait) and
						// arms the shared 30 s finalization budget up front.
						owner.startRuntimeJoin(func() {
							<-owner.closeDone
							<-allStepsComplete
							stopMonitor()
						})
						deadline := owner.startFinalizationBudget()
						if deadline.IsZero() {
							t.Fatal("expected 30s teardown finalization deadline to be armed")
						}
					}

					nativeReleased := false
					overflowFired := false

					for _, step := range order {
						switch step {
						case "cap_overflow":
							for i := 0; i <= maxRuntimeHARInFlightRequests; i++ {
								b.beginRequestEvidence(&harInFlightRequest{id: i})
							}
							// Wait for the monitor goroutine to execute closeOwnedOversizedHARContext.
							b.harMonitorWG.Wait()
							overflowFired = true
							if mode == "mid_session_2s_close_wait" && !nativeReleased {
								select {
								case <-writerEntered:
								case <-time.After(runtimeHARCloseWait):
									t.Fatal("mid-session overflow did not enter bound writer close within 2s wait")
								}
							}

						case "caller_cancel":
							b.censorDocumentEvidence(nil, "cancelled")
							if mode == "mid_session_2s_close_wait" {
								// In mid-session mode, verify that startClose alone never starts
								// the 30s teardown finalization budget.
								owner.startClose()
								owner.mu.Lock()
								started := !owner.finalizationDeadline.IsZero()
								owner.mu.Unlock()
								if started {
									t.Fatal("mid-session close or cancel started the 30s finalization budget before teardown")
								}
								if !nativeReleased {
									select {
									case <-owner.closeDone:
										t.Fatal("closeDone completed before native writer was released")
									case <-time.After(15 * time.Millisecond):
									}
									if owner.retiredSuccessfully() {
										t.Fatal("unjoined mid-session owner retired early")
									}
									if _, err := os.Stat(owner.path); err != nil {
										t.Fatalf("unjoined working.har removed during mid-session cancel: %v", err)
									}
								}
							} else {
								if mode == "teardown_30s_expired_budget" {
									expireHARFinalization(t, owner)
								}
								callerCtx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
								_, err := b.finalizeHARCaptureContext(callerCtx)
								cancel()
								if !errors.Is(err, errHAROwnershipUnproven) {
									t.Fatalf("expected errHAROwnershipUnproven while runtime join is open, got %v", err)
								}
								if owner.retiredSuccessfully() {
									t.Fatal("unjoined teardown owner retired on caller cancel")
								}
								if _, statErr := os.Stat(owner.path); statErr != nil {
									t.Fatalf("unjoined working.har removed on caller cancel: %v", statErr)
								}
								runtimeHAROwners.Lock()
								stillReserved := runtimeHAROwners.owners[owner]
								runtimeHAROwners.Unlock()
								if !stillReserved {
									t.Fatal("caller cancel released owner reservation before physical writer join")
								}
							}

						case "native_close":
							owner.startClose()
							select {
							case <-writerEntered:
							case <-time.After(runtimeHARCloseWait):
								t.Fatal("native close did not enter writer within 2s close wait")
							}
							unblockWriter()
							select {
							case <-owner.closeDone:
							case <-time.After(runtimeHARCloseWait):
								t.Fatal("native close did not finish after unblock")
							}
							nativeReleased = true
						}
					}

					if !overflowFired {
						t.Fatal("cap_overflow step did not run")
					}
					finishSteps()
					stopMonitor()

					if mode == "teardown_30s_expired_budget" {
						expireHARFinalization(t, owner)
					}

					artifact, finErr := b.finalizeHARCaptureContext(context.Background())
					obs := b.evidence.documents["doc"].observations["inp"]
					if obs.State != "censored" || obs.Available || obs.Qualified {
						t.Fatalf("document observation not censored: %+v", obs)
					}
					if closes.Load() != 1 {
						t.Fatalf("native writer close calls = %d, want 1", closes.Load())
					}
					if !owner.retiredSuccessfully() {
						t.Fatalf("owner did not retire after native writer joined (err=%v)", finErr)
					}
					if _, statErr := os.Stat(owner.path); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("joined working.har survived retirement: %v", statErr)
					}
					runtimeHAROwners.Lock()
					leakedReservation := runtimeHAROwners.owners[owner]
					runtimeHAROwners.Unlock()
					if leakedReservation {
						t.Fatal("owner reservation leaked in runtimeHAROwners after retirement")
					}
					if budget.counters.harOverflow.Load() != 1 {
						t.Fatalf("harOverflow = %d, want 1", budget.counters.harOverflow.Load())
					}

					if mode == "teardown_30s_expired_budget" {
						if artifact != "" || !errors.Is(finErr, errHARFinalizationBudget) {
							t.Fatalf("expired 30s budget published %q or missed errHARFinalizationBudget: %v", artifact, finErr)
						}
						if matches, _ := filepath.Glob(filepath.Join(dir, "*_scrubbed.har")); len(matches) != 0 {
							t.Fatalf("expired 30s budget left scrubbed HAR on disk: %v", matches)
						}
						if budget.counters.harFinalizationExhausted.Load() != 1 {
							t.Fatalf("harFinalizationExhausted = %d, want 1", budget.counters.harFinalizationExhausted.Load())
						}
						omissions := b.TakeBrowserHARCaptureOmissions()
						if omissions[protocol.BrowserHAROmittedFinalizationBudget] != 1 {
							t.Fatalf("omissions = %v, want finalization_budget=1", omissions)
						}
					} else {
						if finErr != nil || artifact == "" {
							t.Fatalf("expected scrubbed HAR publication on HARCaptureOnError overflow, got path=%q err=%v", artifact, finErr)
						}
						if _, statErr := os.Stat(artifact); statErr != nil {
							t.Fatalf("published scrubbed HAR missing: %v", statErr)
						}
						if budget.counters.harFinalizationExhausted.Load() != 0 {
							t.Fatalf("harFinalizationExhausted = %d, want 0", budget.counters.harFinalizationExhausted.Load())
						}
						omissions := b.TakeBrowserHARCaptureOmissions()
						if mode == "mid_session_2s_close_wait" && len(omissions) != 0 {
							t.Fatalf("unexpected mid-session capture omissions = %v, want empty", omissions)
						}
						if omissions[protocol.BrowserHAROmittedFinalizationBudget] != 0 || omissions[protocol.BrowserHAROmittedCaptureFailed] != 0 {
							t.Fatalf("unexpected finalization_budget or capture_failed omission = %v", omissions)
						}
					}
				})
			}
		})
	}
}

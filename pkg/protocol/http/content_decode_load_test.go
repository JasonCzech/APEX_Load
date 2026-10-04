package http

import (
	"bytes"
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Many VUsers reading compressed responses at once must not turn decoder
// workspace accounting into request failures. This mirrors a load run: each
// VUser owns its HTTP instance and every response body arrives slowly.
func TestConcurrentCompressedResponsesDoNotFailOnDecoderCapacity(t *testing.T) {
	payload := bytes.Repeat([]byte(`{"records":[{"Id":"001xx000003DGb2AAG","Name":"synthetic"}]}`), 2000)
	gz := contentGzip(t, payload)
	var compressed bytes.Buffer
	writer := brotli.NewWriterOptions(&compressed, brotli.WriterOptions{Quality: 5, LGWin: 22})
	_, _ = writer.Write(payload)
	_ = writer.Close()
	for _, tc := range []struct {
		encoding    string
		body        []byte
		concurrency int
	}{{"gzip", gz, 300}, {"br", compressed.Bytes(), 64}} {
		t.Run(tc.encoding, func(t *testing.T) {
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
				w.Header().Set("Content-Encoding", tc.encoding)
				w.Header().Set("Content-Type", "application/json")
				half := len(tc.body) / 2
				_, _ = w.Write(tc.body[:half])
				w.(stdhttp.Flusher).Flush()
				time.Sleep(600 * time.Millisecond)
				_, _ = w.Write(tc.body[half:])
			}))
			defer server.Close()
			var failed atomic.Int64
			var group sync.WaitGroup
			var mu sync.Mutex
			reasons := map[string]int{}
			for vuser := 0; vuser < tc.concurrency; vuser++ {
				group.Add(1)
				// Stagger connects so the loopback listen backlog (128 on macOS)
				// is not the failure under test; bodies stall long enough that
				// every decoder is still in flight together.
				time.Sleep(time.Millisecond)
				go func() {
					defer group.Done()
					h := &HTTP{}
					if err := h.Init(context.Background(), map[string]any{}); err != nil {
						t.Error(err)
						return
					}
					result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL + "/records"})
					if err != nil || result == nil || !result.Success || result.ResponseBody != string(payload) {
						failed.Add(1)
						reason := "body mismatch"
						if err != nil {
							reason = err.Error()
						} else if result != nil && result.ErrorMessage != "" {
							reason = result.ErrorMessage
						}
						mu.Lock()
						reasons[reason]++
						mu.Unlock()
					}
				}()
			}
			group.Wait()
			if failed.Load() != 0 {
				t.Fatalf("%d of %d concurrent %s responses failed: %v", failed.Load(), tc.concurrency, tc.encoding, reasons)
			}
		})
	}
}

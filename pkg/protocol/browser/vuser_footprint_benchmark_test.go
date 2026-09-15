package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestBrowserVUserMarginalRSS measures the marginal Playwright/Chromium
// process-tree RSS of each additional concurrent browser VUser. Browser VUsers
// share one pooled Chromium per process (pool.go), so a single-VUser snapshot
// reports the whole shared browser; the slope across concurrent VUser counts
// isolates the per-VUser cost (context + page + renderer). The page is a tiny
// constant document so payload size does not influence the result.
//
// Note: the snapshot sums RSS across processes, so pages shared between
// Chromium processes are counted more than once; treat results as an upper
// bound.
//
//	APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^TestBrowserVUserMarginalRSS$' -v
func TestBrowserVUserMarginalRSS(t *testing.T) {
	if !mobileBrowserBenchmarksEnabled() {
		t.Skipf("set %s=1 to run browser footprint measurement", mobileBrowserBenchmarkEnv)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>ok</body></html>`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var browsers []*Browser
	defer func() {
		for _, br := range browsers {
			_ = br.Teardown(context.Background())
		}
	}()

	type point struct {
		vusers int
		rssMB  float64
	}
	var points []point
	for _, target := range []int{1, 2, 4, 8} {
		for len(browsers) < target {
			br := &Browser{}
			if err := br.Init(ctx, benchmarkBrowserConfig("desktop_1080p", false)); err != nil {
				t.Fatalf("Init VUser %d: %v", len(browsers)+1, err)
			}
			browsers = append(browsers, br)
			result, err := br.Execute(ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: server.URL})
			if err != nil {
				t.Fatalf("navigate VUser %d: %v", len(browsers), err)
			}
			if result == nil || !result.Success {
				t.Fatalf("navigate VUser %d failed: %#v", len(browsers), result)
			}
		}
		time.Sleep(2 * time.Second)
		snapshot, err := browsers[0].captureBrowserProcessSnapshot(ctx)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		t.Logf("%d concurrent browser VUsers: process-tree RSS %.1f MB", target, snapshot.MemoryMB)
		points = append(points, point{vusers: target, rssMB: snapshot.MemoryMB})
	}

	n := float64(len(points))
	var sumX, sumY, sumXY, sumXX float64
	for _, p := range points {
		x := float64(p.vusers)
		sumX += x
		sumY += p.rssMB
		sumXY += x * p.rssMB
		sumXX += x * x
	}
	slope := (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
	intercept := (sumY - slope*sumX) / n
	t.Logf("marginal RSS per browser VUser: %.1f MB; shared Playwright+Chromium base: %.1f MB", slope, intercept)
}

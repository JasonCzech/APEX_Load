package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type marginalRSSPoint struct {
	VUsers                int
	TotalRendererRSSBytes uint64
	TotalRendererPSSBytes *uint64
	FixedOverheadBytes    uint64
	DiffedVUserRSSBytes   uint64
}

func computeMarginalRSSLinearRegression(points []marginalRSSPoint) (slopeRSSBytes float64, interceptRSSBytes float64, slopePSSBytes *float64) {
	if len(points) < 2 {
		return 0, 0, nil
	}
	n := float64(len(points))
	var sumX, sumY, sumXY, sumXX float64
	var sumPSS, sumXPSS float64
	allHavePSS := true
	for _, p := range points {
		x := float64(p.VUsers)
		y := float64(p.TotalRendererRSSBytes)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
		if p.TotalRendererPSSBytes != nil {
			pss := float64(*p.TotalRendererPSSBytes)
			sumPSS += pss
			sumXPSS += x * pss
		} else {
			allHavePSS = false
		}
	}
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		return 0, 0, nil
	}
	slopeRSSBytes = (n*sumXY - sumX*sumY) / denom
	interceptRSSBytes = (sumY - slopeRSSBytes*sumX) / n
	if allHavePSS {
		sPSS := (n*sumXPSS - sumX*sumPSS) / denom
		slopePSSBytes = &sPSS
	}
	return slopeRSSBytes, interceptRSSBytes, slopePSSBytes
}

// TestBrowserVUserMarginalRSS measures the per-VUser Chromium renderer RSS (and
// Linux PSS) plus shared fixed overhead across N = 1..5 concurrent browser
// contexts using browser-level CDP SystemInfo.getProcessInfo.
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

	_ = benchmarkColdPool(t, "chromium")
	defer benchmarkClosePooledBrowser("chromium")

	var browsers []*Browser
	defer func() {
		for _, br := range browsers {
			_ = br.Teardown(context.Background())
		}
	}()

	var points []marginalRSSPoint
	for _, target := range []int{1, 2, 3, 4, 5} {
		var latestDiffedRSS uint64
		for len(browsers) < target {
			baselineRenderers := snapshotBaselineRendererPIDs("chromium")
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
			diffSample := br.sampleBenchmarkVUserMemory(baselineRenderers, len(browsers) == 1)
			if diffSample.PerVUserRSSBytes == nil {
				t.Fatalf("VUser %d renderer RSS diff unavailable", len(browsers))
			}
			latestDiffedRSS = *diffSample.PerVUserRSSBytes
		}
		time.Sleep(500 * time.Millisecond)
		// Passing nil baselineRendererPIDs sums all active renderer PIDs across
		// the N concurrent contexts while partitioning driver + browser/zygote/
		// gpu/utility into FixedOverheadBytes.
		totalSample := browsers[0].sampleBenchmarkVUserMemory(nil, true)
		if totalSample.PerVUserRSSBytes == nil || totalSample.FixedOverheadBytes == nil {
			t.Fatalf("%d concurrent VUsers: CDP process attribution unavailable", target)
		}
		pt := marginalRSSPoint{
			VUsers:                target,
			TotalRendererRSSBytes: *totalSample.PerVUserRSSBytes,
			TotalRendererPSSBytes: totalSample.PerVUserPSSBytes,
			FixedOverheadBytes:    *totalSample.FixedOverheadBytes,
			DiffedVUserRSSBytes:   latestDiffedRSS,
		}
		points = append(points, pt)
		if pt.TotalRendererPSSBytes != nil {
			t.Logf("%d concurrent browser VUsers: renderer VmRSS %d bytes (PSS %d bytes), latest diffed VUser %d bytes, fixed overhead %d bytes",
				target, pt.TotalRendererRSSBytes, *pt.TotalRendererPSSBytes, pt.DiffedVUserRSSBytes, pt.FixedOverheadBytes)
		} else {
			t.Logf("%d concurrent browser VUsers: renderer VmRSS %d bytes, latest diffed VUser %d bytes, fixed overhead %d bytes (os=%s)",
				target, pt.TotalRendererRSSBytes, pt.DiffedVUserRSSBytes, pt.FixedOverheadBytes, runtime.GOOS)
		}
	}

	slopeRSS, interceptRSS, slopePSS := computeMarginalRSSLinearRegression(points)
	if slopeRSS <= 0 {
		t.Fatalf("marginal renderer RSS slope = %.0f bytes, want > 0", slopeRSS)
	}
	if slopePSS != nil {
		t.Logf("marginal renderer RSS per browser VUser (N=1..5): %.0f bytes (PSS %.0f bytes); renderer intercept: %.0f bytes",
			slopeRSS, *slopePSS, interceptRSS)
	} else {
		t.Logf("marginal renderer RSS per browser VUser (N=1..5): %.0f bytes; renderer intercept: %.0f bytes",
			slopeRSS, interceptRSS)
	}
}

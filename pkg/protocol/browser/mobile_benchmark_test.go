package browser

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const mobileBrowserBenchmarkEnv = "APEXLOAD_BROWSER_BENCHMARKS"

type mobileBrowserBenchmarkProfile struct {
	name        string
	actionType  string
	memoryProbe bool
}

func mobileBrowserBenchmarkProfiles() []mobileBrowserBenchmarkProfile {
	return []mobileBrowserBenchmarkProfile{
		{name: "desktop_1080p", actionType: "browser_click", memoryProbe: true},
		{name: "pixel_8", actionType: "browser_tap", memoryProbe: true},
		{name: "galaxy_s24", actionType: "browser_tap", memoryProbe: true},
		{name: "iphone_15_pro", actionType: "browser_tap"},
		{name: "iphone_se", actionType: "browser_tap"},
		{name: "ipad_air", actionType: "browser_tap"},
	}
}

func mobileBrowserBenchmarksEnabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(mobileBrowserBenchmarkEnv)))
	return raw == "1" || raw == "true" || raw == "yes"
}

func requireMobileBrowserBenchmarks(b *testing.B) {
	b.Helper()
	if !mobileBrowserBenchmarksEnabled() {
		b.Skipf("set %s=1 to run browser launch, action latency, and RSS benchmarks", mobileBrowserBenchmarkEnv)
	}
}

func TestMobileBrowserBenchmarkProfilesResolve(t *testing.T) {
	seen := map[string]struct{}{}
	for _, target := range mobileBrowserBenchmarkProfiles() {
		if _, exists := seen[target.name]; exists {
			t.Fatalf("duplicate benchmark profile %q", target.name)
		}
		seen[target.name] = struct{}{}

		profile, ok := LookupDevice(target.name)
		if !ok {
			t.Fatalf("benchmark profile %q is not registered", target.name)
		}
		if profile.BrowserFamily == "" {
			t.Fatalf("benchmark profile %q has empty BrowserFamily", target.name)
		}
		if target.actionType == "browser_tap" && !profile.HasTouch {
			t.Fatalf("benchmark profile %q uses tap action without touch support", target.name)
		}
		if target.memoryProbe && profile.BrowserFamily != "chromium" {
			t.Fatalf("benchmark profile %q enables memoryProbe for non-Chromium engine %q", target.name, profile.BrowserFamily)
		}
	}
}

func BenchmarkBrowserInitWithDeviceProfile(b *testing.B) {
	requireMobileBrowserBenchmarks(b)
	for _, target := range mobileBrowserBenchmarkProfiles() {
		target := target
		b.Run(target.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				br := &Browser{}
				if err := br.Init(ctx, benchmarkBrowserConfig(target.name, false)); err != nil {
					cancel()
					b.Fatalf("Init(%s): %v", target.name, err)
				}
				if err := br.Teardown(ctx); err != nil {
					cancel()
					b.Fatalf("Teardown(%s): %v", target.name, err)
				}
				cancel()
			}
		})
	}
}

func BenchmarkBrowserTouchOrClickActionLatency(b *testing.B) {
	requireMobileBrowserBenchmarks(b)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body><button id="target" onclick="document.body.dataset.clicked=(Number(document.body.dataset.clicked||0)+1).toString()">Tap</button></body></html>`)
	}))
	defer server.Close()

	for _, target := range mobileBrowserBenchmarkProfiles() {
		target := target
		b.Run(target.name, func(b *testing.B) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			br := &Browser{}
			if err := br.Init(ctx, benchmarkBrowserConfig(target.name, false)); err != nil {
				b.Fatalf("Init(%s): %v", target.name, err)
			}
			defer func() {
				if err := br.Teardown(context.Background()); err != nil {
					b.Fatalf("Teardown(%s): %v", target.name, err)
				}
			}()

			if _, err := br.Execute(ctx, &protocol.Action{Name: "open fixture", Type: "browser_navigate", Target: server.URL}); err != nil {
				b.Fatalf("navigate(%s): %v", target.name, err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := br.Execute(ctx, &protocol.Action{Name: "activate target", Type: target.actionType, Target: "#target"})
				if err != nil {
					b.Fatalf("%s(%s): %v", target.actionType, target.name, err)
				}
				if result.ErrorMessage != "" {
					b.Fatalf("%s(%s): %s", target.actionType, target.name, result.ErrorMessage)
				}
			}
		})
	}
}

func BenchmarkBrowserRSSPerChromiumDeviceProfile(b *testing.B) {
	requireMobileBrowserBenchmarks(b)
	for _, target := range mobileBrowserBenchmarkProfiles() {
		if !target.memoryProbe {
			continue
		}
		target := target
		b.Run(target.name, func(b *testing.B) {
			var totalRSSBytes float64
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				br := &Browser{}
				if err := br.Init(ctx, benchmarkBrowserConfig(target.name, true)); err != nil {
					cancel()
					b.Fatalf("Init(%s): %v", target.name, err)
				}
				snapshot, err := br.captureBrowserProcessSnapshot(ctx)
				if err != nil {
					_ = br.Teardown(ctx)
					cancel()
					b.Fatalf("captureBrowserProcessSnapshot(%s): %v", target.name, err)
				}
				rssBytes, available := benchmarkRuntimeRSSBytes(snapshot)
				if !available {
					_ = br.Teardown(ctx)
					cancel()
					b.Fatalf("RSS attribution unavailable for %s", target.name)
				}
				totalRSSBytes += rssBytes
				if err := br.Teardown(ctx); err != nil {
					cancel()
					b.Fatalf("Teardown(%s): %v", target.name, err)
				}
				cancel()
			}
			if b.N > 0 {
				// MemoryMB is the existing runtime snapshot's binary-unit field.
				// Preserve its semantics and label new benchmark output explicitly.
				b.ReportMetric(totalRSSBytes/float64(1<<20)/float64(b.N), "rss_mib/op")
				b.ReportMetric(totalRSSBytes/float64(b.N), "rss_bytes/op")
			}
		})
	}
}

// Convert the existing binary-unit snapshot without inventing more precision
// than the platform RSS sampler supplies (for example, ps reports KiB).
func benchmarkRuntimeRSSBytes(snapshot browserProcessSnapshot) (float64, bool) {
	bytes := snapshot.MemoryMB * float64(1<<20)
	if snapshot.CapturedAt.IsZero() || bytes <= 0 || math.IsNaN(bytes) || math.IsInf(bytes, 0) {
		return 0, false
	}
	return bytes, true
}

func benchmarkBrowserConfig(profile string, clientMetrics bool) map[string]any {
	return map[string]any{
		"browser_device_profile": profile,
		"browser_client_metrics": clientMetrics,
		"browser_headless":       true,
		"browser_cache_enabled":  false,
	}
}

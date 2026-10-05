package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in backend qualification against owned disposable Prometheus containers.
// The ordinary fixture suite never requires Docker or a running server.
func TestPrometheusRealCompatibilityFixture(t *testing.T) {
	endpoints := os.Getenv("APEX_PROMETHEUS_FIXTURE_ENDPOINTS")
	if endpoints == "" {
		t.Skip("run pkg/prometheus/testdata/run_compatibility.py for disposable Docker qualification")
	}
	var fixtures map[string]string
	if err := json.Unmarshal([]byte(endpoints), &fixtures); err != nil {
		t.Fatal(err)
	}
	for version, endpoint := range fixtures {
		t.Run(version, func(t *testing.T) {
			if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
				t.Fatal("qualification endpoint must be owned loopback fixture")
			}
			c, err := NewClientWithHTTPClient(Config{Endpoint: endpoint, AllowPrivateHTTP: true, ApprovedCIDRs: []string{"127.0.0.1/32"}}, Credentials{}, &http.Client{})
			if err != nil {
				t.Fatal(err)
			}
			s := Selection{ProjectID: "fixture", StepSeconds: 1, SettleSeconds: 1, Targets: true, Alerts: true, EvidenceSelector: `{job="sut"}`, Queries: []Query{{ID: "availability", Name: "sut_up", Expression: `up{job="sut"}`, TerminalExpression: `avg_over_time(up{job="sut"}[{{window.duration}}])`, Unit: "ratio", AllowedLabels: []string{"job", "instance", "application"}}}}
			caps, err := c.TestSelection(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			if !caps.Metrics || !caps.Discovery || !caps.Targets || !caps.Alerts || caps.Version != version {
				t.Fatalf("capabilities: %+v", caps)
			}
			to := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Second)
			from := to.Add(-4 * time.Second)
			s.GridAnchor = &from
			result, err := c.Collect(context.Background(), s, from, to, "fixture-run")
			if err != nil {
				t.Fatal(err)
			}
			if !result.Progress.Complete || len(result.Points) != 5 || len(result.Evidence) < 2 {
				t.Fatalf("collection: %+v", result)
			}
			for _, p := range result.Points {
				if p.Value == nil || (*p.Value != 0 && *p.Value != 1) || !p.Provider.BucketStart.IsZero() {
					t.Fatalf("point: %+v", p)
				}
			}
			terminal, err := c.CollectTerminal(context.Background(), s, from, to, "fixture-run")
			if err != nil {
				t.Fatal(err)
			}
			if len(terminal.Points) != 1 || terminal.Points[0].Value == nil || (*terminal.Points[0].Value < 0 || *terminal.Points[0].Value > 1) || !terminal.Points[0].Provider.QueryWindow {
				t.Fatalf("terminal: %+v", terminal)
			}
			counts := map[string]int{}
			for _, kind := range []string{"metrics", "labels", "label_values", "series", "metadata", "targets"} {
				selector := ""
				label := ""
				if kind == "series" {
					selector = `up{job="sut"}`
				}
				if kind == "label_values" {
					label = "job"
				}
				if kind == "metadata" {
					selector = "go_goroutines"
				}
				entities, err := c.Discover(context.Background(), selector, kind, label)
				if err != nil || len(entities) == 0 {
					t.Fatalf("discovery %s: %v %+v", kind, err, entities)
				}
				counts[kind] = len(entities)
			}
			report, _ := json.Marshal(map[string]any{"version": version, "capabilities": caps, "range_points": len(result.Points), "terminal_points": len(terminal.Points), "diagnostic_records": len(result.Evidence), "discovery": counts})
			t.Log(string(report))
		})
	}
}

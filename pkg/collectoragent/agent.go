package collectoragent

// A minimal, dependency-free monitoring-collector agent. It authenticates to the
// controller with its collector token and pushes a health heartbeat plus scoped
// host-metric packs on an interval. Metric collection is an MVP (Go runtime
// everywhere; /proc on Linux) and is intentionally extensible — richer per-pack
// collectors can be added without changing the token transport.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config configures a collector agent.
type Config struct {
	ControllerURL string
	CollectorID   string
	Token         string
	Interval      time.Duration
	Packs         []string
	HTTPClient    *http.Client
}

// Agent pushes heartbeats to the controller on an interval.
type Agent struct {
	cfg    Config
	client *http.Client
}

type metricPack struct {
	Pack      string             `json:"pack"`
	Timestamp time.Time          `json:"timestamp"`
	Samples   map[string]float64 `json:"samples"`
}

type heartbeat struct {
	Health string       `json:"health"`
	Packs  []metricPack `json:"packs"`
}

// New validates the config and constructs an Agent.
func New(cfg Config) (*Agent, error) {
	cfg.ControllerURL = strings.TrimRight(strings.TrimSpace(cfg.ControllerURL), "/")
	if cfg.ControllerURL == "" {
		return nil, fmt.Errorf("controller URL is required")
	}
	if strings.TrimSpace(cfg.CollectorID) == "" {
		return nil, fmt.Errorf("collector id is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("collector token is required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	if len(cfg.Packs) == 0 {
		cfg.Packs = []string{"linux-hosts"}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Agent{cfg: cfg, client: client}, nil
}

// Run pushes a heartbeat immediately, then on every tick until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.cfg.Interval)
	defer ticker.Stop()
	a.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Agent) tick(ctx context.Context) {
	if err := a.pushHeartbeat(ctx, a.collect()); err != nil {
		slog.Warn("collector heartbeat failed", "collector_id", a.cfg.CollectorID, "err", err)
		return
	}
	slog.Debug("collector heartbeat sent", "collector_id", a.cfg.CollectorID)
}

// collect builds one metric pack per configured pack name from dependency-free
// host signals.
func (a *Agent) collect() heartbeat {
	now := time.Now().UTC()
	samples := hostSamples()
	packs := make([]metricPack, 0, len(a.cfg.Packs))
	for _, name := range a.cfg.Packs {
		packs = append(packs, metricPack{Pack: name, Timestamp: now, Samples: samples})
	}
	return heartbeat{Health: "healthy", Packs: packs}
}

func (a *Agent) pushHeartbeat(ctx context.Context, hb heartbeat) error {
	body, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/monitoring/collectors/%s/heartbeat", a.cfg.ControllerURL, a.cfg.CollectorID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("unauthorized — the collector token is invalid or was rotated")
	}
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("controller returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

// hostSamples returns dependency-free host/process signals. Go runtime metrics are
// available everywhere; load average + memory come from /proc on Linux.
func hostSamples() map[string]float64 {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	samples := map[string]float64{
		"num_cpu":       float64(runtime.NumCPU()),
		"goroutines":    float64(runtime.NumGoroutine()),
		"heap_alloc_mb": float64(mem.HeapAlloc) / (1024 * 1024),
		"go_sys_mb":     float64(mem.Sys) / (1024 * 1024),
	}
	if load, ok := readLoadAvg(); ok {
		samples["load_avg_1m"] = load
	}
	if total, available, ok := readMemInfo(); ok {
		samples["mem_total_mb"] = total
		samples["mem_available_mb"] = available
	}
	return samples
}

func readLoadAvg() (float64, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readMemInfo() (totalMB, availableMB float64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	var total, available float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kb / 1024
		case "MemAvailable:":
			available = kb / 1024
		}
	}
	if total == 0 {
		return 0, 0, false
	}
	return total, available, true
}

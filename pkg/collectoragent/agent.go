package collectoragent

// A minimal, dependency-free monitoring-collector agent. It authenticates to the
// controller with its collector token and pushes a health heartbeat plus scoped
// host-metric packs on an interval. Metric collection is an MVP (Go runtime
// everywhere; /proc on Linux) and is intentionally extensible — richer per-pack
// collectors can be added without changing the token transport.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/analysis"
	"io"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config configures a collector agent.
type Config struct {
	// ControllerPublicKey enables signed run-bound collection. In this mode
	// heartbeats report availability only and never poll monitor sources.
	ControllerPublicKey ed25519.PublicKey
	// SpoolDirectory durably retains at most one unacknowledged batch per job.
	SpoolDirectory string
	ControllerURL  string
	CollectorID    string
	Token          string
	Interval       time.Duration
	Packs          []string
	HTTPClient     *http.Client
	// PrometheusURL is an explicitly configured scrape endpoint. It is required
	// when the prometheus pack is enabled; credentials must not be put in the URL.
	PrometheusURL string
	// AllowInsecure permits a non-loopback plain-http controller URL. Off by
	// default: the agent's bearer token would otherwise be sent in cleartext
	// across the network. Set it only for a trusted-network deployment, where it
	// downgrades the hard error to a loud warning.
	AllowInsecure bool
}

// Agent pushes heartbeats to the controller on an interval.
type Agent struct {
	cfg            Config
	client         *http.Client
	pendingBatches map[string]*RunSampleBatch
	nextCollection map[string]time.Time
}

type metricPack struct {
	Pack           string                    `json:"pack"`
	Timestamp      time.Time                 `json:"timestamp"`
	Samples        map[string]float64        `json:"samples"`
	SampleMetadata map[string]sampleMetadata `json:"sample_metadata,omitempty"`
	SkippedSamples int                       `json:"skipped_samples,omitempty"`
}

type sampleMetadata struct {
	Name       string            `json:"name,omitempty"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	Quality    string            `json:"quality,omitempty"`
	Kind       string            `json:"kind"`
	Timestamp  *time.Time        `json:"timestamp,omitempty"`
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
	if err := validateControllerURL(cfg.ControllerURL, cfg.AllowInsecure); err != nil {
		return nil, err
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
		if runtime.GOOS == "linux" {
			cfg.Packs = []string{"linux-hosts"}
		} else {
			cfg.Packs = []string{"go-runtime"}
		}
	}
	cfg.Packs = append([]string(nil), cfg.Packs...)
	seenPacks := make(map[string]bool)
	for _, pack := range cfg.Packs {
		if seenPacks[pack] {
			return nil, fmt.Errorf("duplicate metric pack %q", pack)
		}
		seenPacks[pack] = true
		switch pack {
		case "linux-hosts", "go-runtime":
		case "prometheus":
			if strings.TrimSpace(cfg.PrometheusURL) == "" {
				return nil, fmt.Errorf("prometheus pack requires prometheus URL")
			}
		default:
			return nil, fmt.Errorf("unsupported metric pack %q", pack)
		}
	}
	if cfg.PrometheusURL != "" {
		parsed, err := url.Parse(cfg.PrometheusURL)
		// Validate secret-bearing URL components before any transport warning.
		if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
			return nil, fmt.Errorf("prometheus URL must not contain credentials, query parameters, or a fragment")
		}
		if err := validateControllerURL(cfg.PrometheusURL, cfg.AllowInsecure); err != nil {
			return nil, fmt.Errorf("prometheus URL: %w", err)
		}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if len(cfg.ControllerPublicKey) != 0 && len(cfg.ControllerPublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid controller monitor public key")
	}
	cfg.ControllerPublicKey = append(ed25519.PublicKey(nil), cfg.ControllerPublicKey...)
	return &Agent{cfg: cfg, client: client, pendingBatches: map[string]*RunSampleBatch{}, nextCollection: map[string]time.Time{}}, nil
}

// validateControllerURL rejects a controller URL that would leak the collector
// token: a non-http(s) scheme, or a non-loopback plain-http target (unless the
// operator explicitly opted into AllowInsecure, which only warns).
func validateControllerURL(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("controller URL must be an absolute http(s) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(parsed.Hostname()) {
			return nil
		}
		if allowInsecure {
			slog.Warn("collector token will be sent in CLEARTEXT to a non-loopback http controller; use https in production",
				"controller", raw)
			return nil
		}
		return fmt.Errorf("refusing to send the collector token in cleartext to non-loopback http controller %q; use https (or set -insecure to override)", raw)
	default:
		return fmt.Errorf("controller URL scheme %q is not allowed; use http or https", parsed.Scheme)
	}
}

// isLoopbackHost reports whether host is localhost or a loopback IP literal.
func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// Run pushes a heartbeat immediately, then on every tick until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	unlock, err := a.lockSpool(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err = a.loadSpool(); err != nil {
		return err
	}
	interval := a.cfg.Interval
	if len(a.cfg.ControllerPublicKey) > 0 {
		interval = min(interval, time.Second)
	}
	ticker := time.NewTicker(interval)
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
	if len(a.cfg.ControllerPublicKey) > 0 {
		if err := a.pushHeartbeat(ctx, heartbeat{Health: "healthy"}); err != nil {
			slog.Warn("collector heartbeat failed", "err", err)
			return
		}
		if err := a.runJobTick(ctx); err != nil {
			slog.Warn("collector run monitoring degraded", "err", err)
			_ = a.pushHeartbeat(ctx, heartbeat{Health: "degraded"})
		}
		return
	}
	hb, collectErr := a.collect(ctx)
	if collectErr != nil {
		slog.Warn("collector metric collection degraded", "collector_id", a.cfg.CollectorID, "err", collectErr)
	}
	if err := a.pushHeartbeat(ctx, hb); err != nil {
		slog.Warn("collector heartbeat failed", "collector_id", a.cfg.CollectorID, "err", err)
		return
	}
	slog.Debug("collector heartbeat sent", "collector_id", a.cfg.CollectorID)
}

// collect only advertises packs backed by their actual source. Failed packs
// make the heartbeat degraded and are omitted instead of relabeling host data.
func (a *Agent) collect(ctx context.Context) (heartbeat, error) {
	packs := make([]metricPack, 0, len(a.cfg.Packs))
	var firstErr error
	for _, name := range a.cfg.Packs {
		pack := metricPack{Pack: name}
		var samples map[string]float64
		var err error
		switch name {
		case "linux-hosts":
			samples = linuxHostSamples()
			if len(samples) == 0 {
				err = fmt.Errorf("linux host metrics unavailable")
			}
		case "go-runtime":
			samples = goRuntimeSamples()
		case "prometheus":
			pack, err = a.prometheusSamples(ctx)
			samples = pack.Samples
		}
		if err != nil || len(samples) == 0 {
			if err == nil {
				err = fmt.Errorf("no supported samples available")
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", name, err)
			}
			continue
		}
		pack.Pack, pack.Timestamp, pack.Samples = name, time.Now().UTC(), samples
		packs = append(packs, pack)
		if pack.SkippedSamples > 0 && firstErr == nil {
			firstErr = fmt.Errorf("%s: skipped %d unsupported samples", name, pack.SkippedSamples)
		}
	}
	health := "healthy"
	if firstErr != nil {
		health = "degraded"
	}
	return heartbeat{Health: health, Packs: packs}, firstErr
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
	client := *a.client
	// A redirect must not carry the collector token to another destination.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
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

// goRuntimeSamples returns metrics of the collector process itself.
func goRuntimeSamples() map[string]float64 {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return map[string]float64{
		"num_cpu":       float64(runtime.NumCPU()),
		"goroutines":    float64(runtime.NumGoroutine()),
		"heap_alloc_mb": float64(mem.HeapAlloc) / (1024 * 1024),
		"go_sys_mb":     float64(mem.Sys) / (1024 * 1024),
	}
}

func linuxHostSamples() map[string]float64 {
	samples := map[string]float64{}
	if load, ok := readLoadAvg(); ok {
		samples["load_avg_1m"] = load
	}
	if total, available, ok := readMemInfo(); ok {
		samples["mem_total_mb"] = total
		samples["mem_available_mb"] = available
	}
	return samples
}

// hostSamples remains the process and host diagnostic helper for callers that
// explicitly need both groups; it is never sent under an unrelated pack name.
func hostSamples() map[string]float64 {
	samples := goRuntimeSamples()
	for key, value := range linuxHostSamples() {
		samples[key] = value
	}
	return samples
}

const maxPrometheusBodyBytes = 1 << 20
const maxPrometheusSamples = 512

func (a *Agent) prometheusSamples(ctx context.Context) (metricPack, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.PrometheusURL, nil)
	if err != nil {
		return metricPack{}, err
	}
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	client := *a.client
	// Scrape only the explicitly configured endpoint.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return metricPack{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return metricPack{}, fmt.Errorf("scrape returned %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		mediaType, parameters, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "text/plain" || (parameters["version"] != "" && parameters["version"] != "0.0.4") {
			return metricPack{}, fmt.Errorf("scrape requires Prometheus text exposition 0.0.4")
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPrometheusBodyBytes+1))
	if err != nil {
		return metricPack{}, err
	}
	if len(body) > maxPrometheusBodyBytes {
		return metricPack{}, fmt.Errorf("scrape exceeds %d bytes", maxPrometheusBodyBytes)
	}
	return parsePrometheusScalarSet(string(body), len(a.cfg.ControllerPublicKey) > 0)
}

// parsePrometheusScalars accepts only unlabeled finite samples. Labeled
// measurements need bounded dimension handling before they can be admitted.
func parsePrometheusScalars(body string) (metricPack, error) {
	return parsePrometheusScalarSet(body, false)
}

func parsePrometheusScalarSet(body string, labeled bool) (metricPack, error) {
	pack := metricPack{Samples: make(map[string]float64), SampleMetadata: make(map[string]sampleMetadata)}
	if len(body) > maxPrometheusBodyBytes {
		return metricPack{}, fmt.Errorf("scrape exceeds %d bytes", maxPrometheusBodyBytes)
	}
	lines := strings.Split(body, "\n")
	types := make(map[string]string)
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "#" || fields[1] != "TYPE" {
			continue
		}
		if len(fields) != 4 || len(fields[2]) > 128 || !validPrometheusMetricName(fields[2]) {
			return metricPack{}, fmt.Errorf("invalid Prometheus TYPE declaration")
		}
		if _, exists := types[fields[2]]; exists {
			return metricPack{}, fmt.Errorf("duplicate Prometheus TYPE declaration")
		}
		if len(types) >= maxPrometheusSamples {
			return metricPack{}, fmt.Errorf("too many Prometheus TYPE declarations")
		}
		switch fields[3] {
		case "gauge", "counter", "histogram", "summary", "untyped":
		default:
			return metricPack{}, fmt.Errorf("unsupported Prometheus TYPE")
		}
		types[fields[2]] = fields[3]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var dimensions map[string]string
		if labeled && strings.Contains(line, "{") {
			var err error
			line, dimensions, err = parsePrometheusLabels(line)
			if err != nil {
				return metricPack{}, err
			}
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.ContainsAny(fields[0], "{}") {
			pack.SkippedSamples++
			continue
		}
		name := fields[0]
		key := name
		if len(dimensions) > 0 {
			data, _ := json.Marshal(dimensions)
			hash := sha256.Sum256(data)
			key = name + "_" + hex.EncodeToString(hash[:])
		}
		if len(fields) > 3 || len(name) > 128 || !validPrometheusMetricName(name) {
			return metricPack{}, fmt.Errorf("invalid Prometheus sample syntax")
		}
		if _, exists := pack.Samples[key]; exists {
			return metricPack{}, fmt.Errorf("duplicate Prometheus sample")
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return metricPack{}, fmt.Errorf("invalid Prometheus sample value")
		}
		missing := math.IsInf(value, 0) || math.IsNaN(value)
		if missing && !labeled {
			pack.SkippedSamples++
			continue
		}
		if missing {
			value = 0
		} // The metric-point adapter omits this placeholder.

		if len(pack.Samples) >= maxPrometheusSamples {
			return metricPack{}, fmt.Errorf("scrape exceeds %d accepted samples", maxPrometheusSamples)
		}
		kind := types[name]
		if kind == "" {
			for _, suffix := range []string{"_sum", "_count", "_bucket"} {
				if strings.HasSuffix(name, suffix) {
					parent := types[strings.TrimSuffix(name, suffix)]
					if parent == "histogram" || parent == "summary" {
						kind = parent
						break
					}
				}
			}
		}
		if kind == "" || kind == "untyped" {
			kind = "unknown"
		}
		metadata := sampleMetadata{Kind: kind}
		if labeled {
			metadata.Name = name
			metadata.Dimensions = dimensions
			metadata.Unit = prometheusUnit(name)
			if missing {
				metadata.Quality = analysis.QualityMissing
			}
		}
		if len(fields) == 3 {
			millis, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return metricPack{}, fmt.Errorf("invalid Prometheus sample timestamp")
			}
			at := time.UnixMilli(millis).UTC()
			if at.Year() < 1 || at.Year() > 9999 {
				return metricPack{}, fmt.Errorf("Prometheus sample timestamp out of range")
			}
			metadata.Timestamp = &at
		}
		pack.Samples[key], pack.SampleMetadata[key] = value, metadata
	}
	if len(pack.Samples) == 0 {
		return metricPack{}, fmt.Errorf("scrape contained no supported unlabeled finite samples")
	}
	return pack, nil
}

func validPrometheusMetricName(name string) bool {
	for i, r := range name {
		valid := r == '_' || r == ':' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9'
		if !valid {
			return false
		}
	}
	return name != ""
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

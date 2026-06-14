package api

// Network-latency probing for live test metrics: endpoint discovery from a
// script's actions/data sources, TCP+HTTP latency sampling with jitter, and
// traceroute-based route metadata. Extracted from runner.go to keep the
// TestRunner orchestration file focused.

import (
	"context"
	"crypto/tls"
	"encoding/csv"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (tr *TestRunner) measureNetworkLatency(test *ManagedTest, live *LiveMetrics) {
	// Identify unique base URLs across all groups
	endpoints := tr.latencyEndpointsForTest(test)

	if len(endpoints) == 0 {
		return
	}

	latency := &NetworkLatency{
		Targets: make([]LatencyTarget, 0, len(endpoints)),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for host, raw := range endpoints {
		wg.Add(1)
		go func(h, r string) {
			defer wg.Done()
			target := tr.measureEndpointLatency(h, r)
			mu.Lock()
			latency.Targets = append(latency.Targets, target)
			mu.Unlock()
		}(host, raw)
	}
	wg.Wait()

	tr.mu.Lock()
	tr.applyLatencyStabilityLocked(test, latency)
	live.Latency = cloneNetworkLatency(latency)
	test.lastNetworkLatency = cloneNetworkLatency(latency)
	if test.LiveMetrics != nil {
		test.LiveMetrics.Latency = cloneNetworkLatency(latency)
	}
	tr.mu.Unlock()

	// Broadcast the latency update specifically if needed,
	// though it will be part of the next metrics broadcast too.
	if tr.hub != nil {
		tr.hub.BroadcastJSON(map[string]any{
			"type":       "latency_update",
			"test_id":    test.ID,
			"project_id": normalizeProjectID(test.ProjectID),
			"latency":    latency,
		})
	}
}

func (tr *TestRunner) latencyEndpointsForTest(test *ManagedTest) map[string]string {
	endpoints := make(map[string]string)
	for _, g := range test.Groups {
		script := g.script
		if script == nil && g.ScriptID != "" {
			tr.mu.RLock()
			if stored, ok := tr.scripts[g.ScriptID]; ok {
				script = stored.Script
			}
			tr.mu.RUnlock()
		}
		addLatencyEndpoint(endpoints, script)
	}
	return endpoints
}

func addLatencyEndpoint(endpoints map[string]string, script *dsl.Script) {
	if script == nil {
		return
	}
	if addParameterizedLatencyEndpoints(endpoints, script) {
		return
	}
	raw := strings.TrimSpace(script.Config.BaseURL)
	if raw == "" {
		return
	}
	addLatencyEndpointValue(endpoints, raw)
}

func addParameterizedLatencyEndpoints(endpoints map[string]string, script *dsl.Script) bool {
	added := false
	for _, action := range script.Actions {
		raw := strings.TrimSpace(action.Target)
		if raw == "" {
			continue
		}
		if endpoint, ok := resolveLatencyEndpointTemplatePrefix(raw, script); ok {
			addLatencyEndpointValue(endpoints, endpoint)
			added = true
		}
	}
	return added
}

var latencyTemplatePrefixPattern = regexp.MustCompile(`^\{\{([^{}]+)\}\}`)

func resolveLatencyEndpointTemplatePrefix(raw string, script *dsl.Script) (string, bool) {
	raw = strings.TrimSpace(raw)
	match := latencyTemplatePrefixPattern.FindStringSubmatchIndex(raw)
	if len(match) != 4 {
		return "", false
	}
	expr := strings.TrimSpace(raw[match[2]:match[3]])
	if !strings.HasPrefix(expr, "var.") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(expr, "var."))
	value, ok := firstScriptParameterValue(script, name)
	if !ok {
		return "", false
	}
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if !strings.HasPrefix(strings.ToLower(value), "http://") && !strings.HasPrefix(strings.ToLower(value), "https://") {
		return "", false
	}
	suffix := strings.TrimLeft(raw[match[1]:], " \t\r\n")
	return normalizeLatencyEndpointValue(value + suffix), true
}

func addLatencyEndpointValue(endpoints map[string]string, raw string) {
	raw = normalizeLatencyEndpointValue(raw)
	host, ok := latencyHostFromEndpoint(raw)
	if !ok {
		return
	}
	endpoints[host] = raw
}

var latencyEndpointBoundaryWhitespacePattern = regexp.MustCompile(`^(https?://[^/?#\s]+)\s+([/?#].*)$`)

func normalizeLatencyEndpointValue(raw string) string {
	raw = strings.TrimSpace(raw)
	return latencyEndpointBoundaryWhitespacePattern.ReplaceAllString(raw, "$1$2")
}

func firstScriptParameterValue(script *dsl.Script, name string) (string, bool) {
	for _, param := range script.Parameters {
		if param.Name != name {
			continue
		}
		for _, value := range param.Values {
			if strings.TrimSpace(value) != "" {
				return value, true
			}
		}
		if param.DataSource != "" && param.Column != "" {
			return firstScriptDataSourceValue(script.DataSources, param.DataSource, param.Column)
		}
	}
	return "", false
}

func firstScriptDataSourceValue(sources []dsl.DataSource, sourceName, column string) (string, bool) {
	for _, source := range sources {
		if source.Name != sourceName || strings.TrimSpace(source.InlineData) == "" {
			continue
		}
		for _, delimiter := range latencyDataSourceDelimiters(source.Delimiter) {
			if value, ok := firstScriptDataSourceValueWithDelimiter(source, column, delimiter); ok {
				return value, true
			}
		}
	}
	return "", false
}

func latencyDataSourceDelimiters(delimiter string) []rune {
	if delimiter == "\t" {
		return []rune{'\t'}
	}
	if strings.TrimSpace(delimiter) == "" {
		return []rune{',', '\t', ';', '|'}
	}
	if strings.EqualFold(strings.TrimSpace(delimiter), "auto") {
		return []rune{',', '\t', ';', '|'}
	}
	if strings.EqualFold(strings.TrimSpace(delimiter), `\t`) || strings.EqualFold(strings.TrimSpace(delimiter), "tab") {
		return []rune{'\t'}
	}
	if strings.EqualFold(strings.TrimSpace(delimiter), "comma") {
		return []rune{','}
	}
	return []rune{[]rune(delimiter)[0]}
}

func firstScriptDataSourceValueWithDelimiter(source dsl.DataSource, column string, delimiter rune) (string, bool) {
	rows := parseInlineDataRows(source.InlineData, delimiter)
	if len(rows) == 0 {
		return "", false
	}
	rowIndex := 0
	columnIndex := 0
	if source.FirstRowHeader {
		if len(rows) < 2 {
			return "", false
		}
		index, ok := indexScriptDataSourceColumn(rows[0], column)
		if !ok {
			return "", false
		}
		columnIndex = index
		rowIndex = 1
	} else if len(source.Columns) > 0 {
		index, ok := indexScriptDataSourceColumn(source.Columns, column)
		if !ok {
			return "", false
		}
		columnIndex = index
	}
	if columnIndex >= len(rows[rowIndex]) {
		return "", false
	}
	value := strings.TrimSpace(rows[rowIndex][columnIndex])
	return value, value != ""
}

func parseInlineDataRows(content string, delimiter rune) [][]string {
	reader := csv.NewReader(strings.NewReader(content))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil
	}
	filtered := rows[:0]
	for _, row := range rows {
		nonEmpty := false
		for index := range row {
			row[index] = strings.TrimSpace(strings.TrimPrefix(row[index], "\ufeff"))
			if row[index] != "" {
				nonEmpty = true
			}
		}
		if nonEmpty {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func indexScriptDataSourceColumn(columns []string, column string) (int, bool) {
	for index, candidate := range columns {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(column)) {
			return index, true
		}
	}
	return 0, false
}

func latencyHostFromEndpoint(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		return parsed.Host, true
	}
	if !strings.Contains(raw, "://") {
		parsed, err = url.Parse("//" + raw)
		if err == nil && parsed.Host != "" {
			return parsed.Host, true
		}
	}
	return "", false
}

func cloneNetworkLatency(in *NetworkLatency) *NetworkLatency {
	if in == nil {
		return nil
	}
	out := &NetworkLatency{}
	if len(in.Targets) > 0 {
		out.Targets = append([]LatencyTarget(nil), in.Targets...)
	}
	return out
}

func (tr *TestRunner) applyLatencyStabilityLocked(test *ManagedTest, latency *NetworkLatency) {
	if test == nil || latency == nil {
		return
	}
	if test.latencyProbeSamples == nil {
		test.latencyProbeSamples = make(map[string][]latencyProbeSample)
	}
	for index := range latency.Targets {
		target := &latency.Targets[index]
		key := latencyTargetKey(*target)
		samples := append(test.latencyProbeSamples[key], latencyProbeSample{
			totalMs:   latencySampleValue(*target),
			success:   target.LastSuccess,
			timestamp: target.Timestamp,
		})
		if len(samples) > latencyStabilityWindowSize {
			samples = samples[len(samples)-latencyStabilityWindowSize:]
		}
		test.latencyProbeSamples[key] = samples
		target.SampleCount = len(samples)
		if len(samples) == 0 {
			continue
		}
		successes := 0
		successValues := make([]float64, 0, len(samples))
		for _, sample := range samples {
			if sample.success {
				successes++
				if sample.totalMs > 0 {
					successValues = append(successValues, sample.totalMs)
				}
			}
		}
		target.SuccessRatePct = float64(successes) / float64(len(samples)) * 100
		target.JitterMs = latencyJitterMs(successValues)
	}
}

func latencyTargetKey(target LatencyTarget) string {
	return strings.TrimSpace(target.Endpoint) + "|" + strings.TrimSpace(target.Name)
}

func latencySampleValue(target LatencyTarget) float64 {
	if target.ProbeTotalMs > 0 {
		return target.ProbeTotalMs
	}
	return target.LatencyMs
}

func latencyJitterMs(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	mean := sum / float64(len(values))
	var variance float64
	for _, value := range values {
		diff := value - mean
		variance += diff * diff
	}
	return math.Sqrt(variance / float64(len(values)))
}

func (tr *TestRunner) measureEndpointLatency(host, endpoint string) LatencyTarget {
	if isHTTPProbeEndpoint(endpoint) {
		target, err := tr.measureEndpointHTTPLatency(host, endpoint)
		if err == nil {
			tr.applyLatencyRouteMetadata(&target)
			return target
		}

		target = tr.measureEndpointTCPLatency(host, endpoint)
		if target.LastSuccess {
			target.Error = fmt.Sprintf("HTTP probe failed; TCP fallback succeeded: %s", err.Error())
		} else if target.Error == "" {
			target.Error = err.Error()
		}
		tr.applyLatencyRouteMetadata(&target)
		return target
	}

	target := tr.measureEndpointTCPLatency(host, endpoint)
	tr.applyLatencyRouteMetadata(&target)
	return target
}

func (tr *TestRunner) measureEndpointTCPLatency(host, endpoint string) LatencyTarget {
	target := LatencyTarget{
		Name:        host,
		Endpoint:    endpoint,
		ProbeMethod: "tcp",
		Timestamp:   time.Now(),
	}
	start := time.Now()

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	dialHost := latencyDialHost(host, endpoint)
	conn, err := dialer.Dial("tcp", dialHost)
	if err != nil {
		target.LastSuccess = false
		target.Error = err.Error()
		return target
	}
	defer conn.Close()

	target.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	target.ConnectMs = target.LatencyMs
	target.ProbeTotalMs = target.LatencyMs
	target.LastSuccess = true

	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		target.IP = addr.IP.String()
	}

	return target
}

func (tr *TestRunner) measureEndpointHTTPLatency(host, endpoint string) (LatencyTarget, error) {
	target, err := tr.performLatencyHTTPProbe(host, endpoint, http.MethodHead)
	if err == nil && target.HTTPStatus != http.StatusMethodNotAllowed && target.HTTPStatus != http.StatusNotImplemented {
		return target, nil
	}
	if err == nil {
		err = fmt.Errorf("HEAD probe returned %d", target.HTTPStatus)
	}
	fallback, fallbackErr := tr.performLatencyHTTPProbe(host, endpoint, http.MethodGet)
	if fallbackErr == nil {
		return fallback, nil
	}
	return fallback, fmt.Errorf("%s; GET fallback failed: %w", err.Error(), fallbackErr)
}

func (tr *TestRunner) performLatencyHTTPProbe(host, endpoint string, method string) (LatencyTarget, error) {
	target := LatencyTarget{
		Name:        host,
		Endpoint:    endpoint,
		ProbeMethod: strings.ToUpper(method),
		Timestamp:   time.Now(),
	}
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		target.Error = err.Error()
		return target, err
	}

	var dnsStart time.Time
	var connectStart time.Time
	var tlsStart time.Time
	start := time.Now()
	trace := &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) {
			dnsStart = time.Now()
		},
		DNSDone: func(_ httptrace.DNSDoneInfo) {
			if !dnsStart.IsZero() {
				target.DNSMs = durationMilliseconds(time.Since(dnsStart))
			}
		},
		ConnectStart: func(_, _ string) {
			connectStart = time.Now()
		},
		ConnectDone: func(_, _ string, _ error) {
			if !connectStart.IsZero() {
				target.ConnectMs = durationMilliseconds(time.Since(connectStart))
				target.LatencyMs = target.ConnectMs
			}
		},
		TLSHandshakeStart: func() {
			tlsStart = time.Now()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			if !tlsStart.IsZero() {
				target.TLSMs = durationMilliseconds(time.Since(tlsStart))
			}
		},
		GotFirstResponseByte: func() {
			target.TTFBMs = durationMilliseconds(time.Since(start))
		},
		GotConn: func(info httptrace.GotConnInfo) {
			if addr, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok {
				target.IP = addr.IP.String()
			}
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: newLatencyProbeTransport(),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	target.ProbeTotalMs = durationMilliseconds(time.Since(start))
	if target.LatencyMs == 0 && target.ConnectMs > 0 {
		target.LatencyMs = target.ConnectMs
	}
	if err != nil {
		target.LastSuccess = false
		target.Error = err.Error()
		return target, err
	}
	defer resp.Body.Close()
	target.HTTPStatus = resp.StatusCode
	target.LastSuccess = true
	if target.TTFBMs == 0 {
		target.TTFBMs = target.ProbeTotalMs
	}
	return target, nil
}

func newLatencyProbeTransport() *http.Transport {
	return &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
	}
}

func isHTTPProbeEndpoint(endpoint string) bool {
	lower := strings.ToLower(strings.TrimSpace(endpoint))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func latencyDialHost(host, endpoint string) string {
	dialHost := strings.TrimSpace(host)
	if _, _, err := net.SplitHostPort(dialHost); err == nil {
		return dialHost
	}
	if strings.Contains(dialHost, ":") && strings.Count(dialHost, ":") > 1 {
		return net.JoinHostPort(strings.Trim(dialHost, "[]"), defaultLatencyPort(endpoint))
	}
	if strings.Contains(dialHost, ":") {
		return dialHost
	}
	return net.JoinHostPort(dialHost, defaultLatencyPort(endpoint))
}

func defaultLatencyPort(endpoint string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(endpoint)), "https://") {
		return "443"
	}
	return "80"
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000.0
}

func (tr *TestRunner) applyLatencyRouteMetadata(target *LatencyTarget) {
	if target == nil {
		return
	}
	routeTarget := latencyRouteTarget(*target)
	if routeTarget == "" {
		target.RouteStatus = "unavailable"
		return
	}
	hops, status := tr.routeMetadataForLatencyTarget(routeTarget)
	target.Hops = hops
	target.RouteStatus = status
}

func latencyRouteTarget(target LatencyTarget) string {
	if strings.TrimSpace(target.IP) != "" {
		return strings.TrimSpace(target.IP)
	}
	return trimLatencyHostPort(target.Name)
}

func trimLatencyHostPort(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func (tr *TestRunner) routeMetadataForLatencyTarget(target string) (int, string) {
	target = trimLatencyHostPort(target)
	if target == "" {
		return 0, "unavailable"
	}
	if isLatencyLoopbackTarget(target) {
		return 0, "local"
	}

	now := time.Now()
	tr.mu.RLock()
	entry, ok := tr.routeCache[target]
	tr.mu.RUnlock()
	if ok && now.Sub(entry.checkedAt) < latencyRouteCacheTTL {
		return entry.hops, entry.status
	}

	hops, status := measureLatencyRoute(target)
	tr.mu.Lock()
	if tr.routeCache == nil {
		tr.routeCache = make(map[string]latencyRouteCacheEntry)
	}
	tr.routeCache[target] = latencyRouteCacheEntry{
		hops:      hops,
		status:    status,
		checkedAt: now,
	}
	tr.mu.Unlock()
	return hops, status
}

func isLatencyLoopbackTarget(target string) bool {
	if strings.EqualFold(target, "localhost") {
		return true
	}
	if ip := net.ParseIP(target); ip != nil {
		return ip.IsLoopback()
	}
	ips, err := net.LookupIP(target)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true
		}
	}
	return false
}

func measureLatencyRoute(target string) (int, string) {
	traceroutePath, err := exec.LookPath("traceroute")
	if err != nil {
		return 0, "unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, traceroutePath, "-m", "8", "-q", "1", "-w", "1", target).CombinedOutput()
	if ctx.Err() != nil {
		return 0, "timeout"
	}
	hops := parseTracerouteHopCount(string(output))
	if hops > 0 {
		return hops, "ok"
	}
	if err != nil {
		return 0, "unavailable"
	}
	return 0, "unavailable"
}

func parseTracerouteHopCount(output string) int {
	maxHop := 0
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		hop, err := strconv.Atoi(strings.TrimSuffix(fields[0], "."))
		if err != nil {
			continue
		}
		if hop > maxHop {
			maxHop = hop
		}
	}
	return maxHop
}

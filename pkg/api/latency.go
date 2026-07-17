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
	"io"
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
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxLatencyProbeTargets      = 64
	maxConcurrentLatencyTargets = 8
	maxConcurrentRouteProbes    = 16
	maxLatencyRouteCacheEntries = 1024
	latencyDNSLookupTimeout     = 2 * time.Second
)

var latencyRouteProbeSlots = make(chan struct{}, maxConcurrentRouteProbes)

func (tr *TestRunner) measureNetworkLatency(test *ManagedTest, _ *LiveMetrics) {
	if !tr.beginLatencyProbe(test) {
		return
	}
	defer tr.endLatencyProbe(test)

	// Identify unique base URLs across all groups
	endpoints := tr.latencyEndpointsForTest(test)

	if len(endpoints) == 0 {
		return
	}

	latency := &NetworkLatency{Targets: collectLatencyTargets(endpoints, tr.measureEndpointLatency)}

	tr.mu.Lock()
	if test == nil || tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return
	}
	tr.applyLatencyStabilityLocked(test, latency)
	test.lastNetworkLatency = cloneNetworkLatency(latency)
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		updated.Latency = cloneNetworkLatency(latency)
		replaceLiveMetricsLocked(test, updated)
	}
	testID := test.ID
	projectID := normalizeProjectID(test.ProjectID)
	tr.mu.Unlock()

	// Broadcast the latency update specifically if needed,
	// though it will be part of the next metrics broadcast too.
	if tr.hub != nil {
		tr.hub.BroadcastScopedJSON(testID, projectID, map[string]any{
			"type":       "latency_update",
			"test_id":    testID,
			"project_id": projectID,
			"latency":    latency,
		})
	}
}

type latencyProbeJob struct {
	host     string
	endpoint string
}

// collectLatencyTargets uses a fixed worker pool. A script may legitimately
// reference dozens of hosts, but one metrics tick must not create a goroutine,
// resolver call, HTTP connection, and traceroute subprocess for every host at
// once.
func collectLatencyTargets(endpoints map[string]string, probe func(host, endpoint string) LatencyTarget) []LatencyTarget {
	if len(endpoints) == 0 || probe == nil {
		return nil
	}
	jobs := make(chan latencyProbeJob)
	results := make(chan LatencyTarget, len(endpoints))
	workerCount := min(len(endpoints), maxConcurrentLatencyTargets)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobs {
				results <- probe(job.host, job.endpoint)
			}
		}()
	}
	for host, endpoint := range endpoints {
		jobs <- latencyProbeJob{host: host, endpoint: endpoint}
	}
	close(jobs)
	workers.Wait()
	close(results)
	targets := make([]LatencyTarget, 0, len(endpoints))
	for target := range results {
		targets = append(targets, target)
	}
	return targets
}

func (tr *TestRunner) beginLatencyProbe(test *ManagedTest) bool {
	if tr == nil || test == nil {
		return false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || test.latencyProbeInFlight {
		return false
	}
	test.latencyProbeInFlight = true
	return true
}

func (tr *TestRunner) endLatencyProbe(test *ManagedTest) {
	if tr == nil || test == nil {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] == test {
		test.latencyProbeInFlight = false
	}
	tr.mu.Unlock()
}

func (tr *TestRunner) latencyEndpointsForTest(test *ManagedTest) map[string]string {
	if tr == nil || test == nil {
		return map[string]string{}
	}
	signature, inputs := tr.latencyEndpointInputs(test)
	tr.mu.RLock()
	if test.latencyEndpointsCache.valid && test.latencyEndpointsCache.signature == signature {
		endpoints := cloneLatencyEndpoints(test.latencyEndpointsCache.endpoints)
		tr.mu.RUnlock()
		return endpoints
	}
	tr.mu.RUnlock()

	endpoints := make(map[string]string)
	for _, input := range inputs {
		addLatencyEndpoint(endpoints, input.script)
	}
	tr.mu.Lock()
	test.latencyEndpointsCache = latencyEndpointCache{
		signature: signature,
		endpoints: cloneLatencyEndpoints(endpoints),
		valid:     true,
	}
	tr.mu.Unlock()
	return endpoints
}

type latencyEndpointCache struct {
	signature string
	endpoints map[string]string
	valid     bool
}

type latencyEndpointInput struct {
	groupName string
	scriptID  string
	script    *dsl.Script
}

func (tr *TestRunner) latencyEndpointInputs(test *ManagedTest) (string, []latencyEndpointInput) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	inputs := make([]latencyEndpointInput, 0, len(test.Groups))
	var signature strings.Builder
	for _, group := range test.Groups {
		script := group.script
		if script == nil && group.ScriptID != "" {
			if stored, ok := tr.scripts[group.ScriptID]; ok {
				script = stored.Script
			}
		}
		inputs = append(inputs, latencyEndpointInput{
			groupName: group.Name,
			scriptID:  group.ScriptID,
			script:    script,
		})
		writeLatencyEndpointSignature(&signature, group, script)
	}
	return signature.String(), inputs
}

func writeLatencyEndpointSignature(signature *strings.Builder, group TestGroup, script *dsl.Script) {
	fmt.Fprintf(signature, "group=%s;script_id=%s;", group.Name, group.ScriptID)
	if script == nil {
		signature.WriteString("nil|")
		return
	}
	fmt.Fprintf(signature, "ptr=%p;id=%s;version=%s;updated=%d;base=%s;actions=%d;params=%d;sources=%d;",
		script,
		script.ID,
		script.Version,
		script.UpdatedAt.UnixNano(),
		script.Config.BaseURL,
		len(script.Actions),
		len(script.Parameters),
		len(script.DataSources),
	)
	for _, action := range script.Actions {
		fmt.Fprintf(signature, "target=%s;", action.Target)
	}
	for _, param := range script.Parameters {
		fmt.Fprintf(signature, "param=%s/%s/%s/%s/%d;", param.Name, param.Type, param.DataSource, param.Column, len(param.Values))
	}
	for _, source := range script.DataSources {
		fmt.Fprintf(signature, "source=%s/%s/%t/%s/%d;", source.Name, source.Type, source.FirstRowHeader, source.Delimiter, len(source.InlineData))
	}
	signature.WriteByte('|')
}

func cloneLatencyEndpoints(endpoints map[string]string) map[string]string {
	if len(endpoints) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(endpoints))
	for host, endpoint := range endpoints {
		cloned[host] = endpoint
	}
	return cloned
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
	paramCache := make(map[string]latencyParameterLookup)
	for _, action := range script.Actions {
		raw := strings.TrimSpace(action.Target)
		if raw == "" {
			continue
		}
		if endpoint, ok := resolveLatencyEndpointTemplatePrefix(raw, script, paramCache); ok {
			addLatencyEndpointValue(endpoints, endpoint)
			added = true
		}
	}
	return added
}

var latencyTemplatePrefixPattern = regexp.MustCompile(`^\{\{([^{}]+)\}\}`)

type latencyParameterLookup struct {
	value string
	ok    bool
}

func resolveLatencyEndpointTemplatePrefix(raw string, script *dsl.Script, paramCache map[string]latencyParameterLookup) (string, bool) {
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
	value, ok := firstScriptParameterValueCached(script, name, paramCache)
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

func firstScriptParameterValueCached(script *dsl.Script, name string, cache map[string]latencyParameterLookup) (string, bool) {
	if cache == nil {
		return firstScriptParameterValue(script, name)
	}
	key := strings.TrimSpace(name)
	if got, ok := cache[key]; ok {
		return got.value, got.ok
	}
	value, ok := firstScriptParameterValue(script, key)
	cache[key] = latencyParameterLookup{value: value, ok: ok}
	return value, ok
}

func addLatencyEndpointValue(endpoints map[string]string, raw string) {
	raw = normalizeLatencyEndpointValue(raw)
	host, ok := latencyHostFromEndpoint(raw)
	if !ok {
		return
	}
	if _, exists := endpoints[host]; !exists && len(endpoints) >= maxLatencyProbeTargets {
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
	rows := parseInlineDataRows(source.InlineData, delimiter, 2)
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

func parseInlineDataRows(content string, delimiter rune, maxRows int) [][]string {
	reader := csv.NewReader(strings.NewReader(content))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	filtered := make([][]string, 0, maxRows)
	for maxRows <= 0 || len(filtered) < maxRows {
		row, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil
		}
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
			return sanitizeLatencyTarget(target)
		}

		target = tr.measureEndpointTCPLatency(host, endpoint)
		if target.LastSuccess {
			target.Error = fmt.Sprintf("HTTP probe failed; TCP fallback succeeded: %s", err.Error())
		} else if target.Error == "" {
			target.Error = err.Error()
		}
		tr.applyLatencyRouteMetadata(&target)
		return sanitizeLatencyTarget(target)
	}

	target := tr.measureEndpointTCPLatency(host, endpoint)
	tr.applyLatencyRouteMetadata(&target)
	return sanitizeLatencyTarget(target)
}

// sanitizeLatencyTarget is the publication boundary for controller-generated
// probe data. Script endpoints can legitimately contain inline credentials or
// secret query values; those must not be copied into metrics, persistence, or
// project-wide WebSocket events.
func sanitizeLatencyTarget(target LatencyTarget) LatencyTarget {
	target.Endpoint = scrub.ValueURL(target.Endpoint, scrub.Policy{})
	target.Error = scrub.Value(target.Error, scrub.Policy{})
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

	var traceState struct {
		sync.Mutex
		dnsStart     time.Time
		tlsStart     time.Time
		connectStart map[string]time.Time
		dnsMs        float64
		connectMs    float64
		tlsMs        float64
		ttfbMs       float64
		ip           string
	}
	traceState.connectStart = make(map[string]time.Time)
	start := time.Now()
	trace := &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) {
			traceState.Lock()
			traceState.dnsStart = time.Now()
			traceState.Unlock()
		},
		DNSDone: func(_ httptrace.DNSDoneInfo) {
			traceState.Lock()
			if !traceState.dnsStart.IsZero() {
				traceState.dnsMs = durationMilliseconds(time.Since(traceState.dnsStart))
			}
			traceState.Unlock()
		},
		ConnectStart: func(network, addr string) {
			traceState.Lock()
			traceState.connectStart[network+"\x00"+addr] = time.Now()
			traceState.Unlock()
		},
		ConnectDone: func(network, addr string, _ error) {
			traceState.Lock()
			key := network + "\x00" + addr
			if connectStart := traceState.connectStart[key]; !connectStart.IsZero() {
				traceState.connectMs = durationMilliseconds(time.Since(connectStart))
				delete(traceState.connectStart, key)
			}
			traceState.Unlock()
		},
		TLSHandshakeStart: func() {
			traceState.Lock()
			traceState.tlsStart = time.Now()
			traceState.Unlock()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			traceState.Lock()
			if !traceState.tlsStart.IsZero() {
				traceState.tlsMs = durationMilliseconds(time.Since(traceState.tlsStart))
			}
			traceState.Unlock()
		},
		GotFirstResponseByte: func() {
			traceState.Lock()
			traceState.ttfbMs = durationMilliseconds(time.Since(start))
			traceState.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			if addr, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok {
				traceState.Lock()
				traceState.ip = addr.IP.String()
				traceState.Unlock()
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
	traceState.Lock()
	target.DNSMs = traceState.dnsMs
	target.ConnectMs = traceState.connectMs
	target.LatencyMs = traceState.connectMs
	target.TLSMs = traceState.tlsMs
	target.TTFBMs = traceState.ttfbMs
	target.IP = traceState.ip
	traceState.Unlock()
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
	if status != "busy" {
		tr.storeLatencyRouteCacheEntry(target, latencyRouteCacheEntry{
			hops:      hops,
			status:    status,
			checkedAt: now,
		})
	}
	return hops, status
}

func (tr *TestRunner) storeLatencyRouteCacheEntry(target string, entry latencyRouteCacheEntry) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.routeCache == nil {
		tr.routeCache = make(map[string]latencyRouteCacheEntry)
	}
	if _, exists := tr.routeCache[target]; !exists && len(tr.routeCache) >= maxLatencyRouteCacheEntries {
		cutoff := entry.checkedAt.Add(-latencyRouteCacheTTL)
		for key, cached := range tr.routeCache {
			if cached.checkedAt.Before(cutoff) {
				delete(tr.routeCache, key)
			}
		}
	}
	if _, exists := tr.routeCache[target]; !exists {
		for len(tr.routeCache) >= maxLatencyRouteCacheEntries {
			var oldestKey string
			var oldestTime time.Time
			for key, cached := range tr.routeCache {
				if oldestKey == "" || cached.checkedAt.Before(oldestTime) {
					oldestKey, oldestTime = key, cached.checkedAt
				}
			}
			if oldestKey == "" {
				break
			}
			delete(tr.routeCache, oldestKey)
		}
	}
	tr.routeCache[target] = entry
}

func isLatencyLoopbackTarget(target string) bool {
	if strings.EqualFold(target, "localhost") {
		return true
	}
	if ip := net.ParseIP(target); ip != nil {
		return ip.IsLoopback()
	}
	ctx, cancel := context.WithTimeout(context.Background(), latencyDNSLookupTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, target)
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if address.IP.IsLoopback() {
			return true
		}
	}
	return false
}

func measureLatencyRoute(target string) (int, string) {
	if !validLatencyRouteTarget(target) {
		return 0, "unavailable"
	}
	select {
	case latencyRouteProbeSlots <- struct{}{}:
		defer func() { <-latencyRouteProbeSlots }()
	default:
		return 0, "busy"
	}
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

func validLatencyRouteTarget(target string) bool {
	target = strings.TrimSuffix(strings.TrimSpace(target), ".")
	if target == "" || len(target) > 253 || strings.HasPrefix(target, "-") {
		return false
	}
	if net.ParseIP(target) != nil {
		return true
	}
	for _, label := range strings.Split(target, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
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

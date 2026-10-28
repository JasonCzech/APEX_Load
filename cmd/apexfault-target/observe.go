package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

// targetSnapshot is a projection, never a copy of an arbitrary remote response.
// It deliberately has no controller, VUser or execution-ownership claims.
type targetSnapshot struct {
	SchemaVersion              int               `json:"schema_version"`
	RunID                      string            `json:"run_id"`
	TestID                     string            `json:"test_id"`
	TargetInstanceID           string            `json:"target_instance_id"`
	StartedAt                  float64           `json:"started_at_unix"`
	ObservedAt                 float64           `json:"observed_at_unix"`
	TargetRequests             uint64            `json:"target_requests"`
	PerGenerationRequests      map[string]uint64 `json:"per_generation_requests,omitempty"`
	HighestGeneration          uint64            `json:"highest_generation,omitempty"`
	DuplicateGenerationOverlap uint64            `json:"duplicate_generation_overlap,omitempty"`
}

var instancePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func snapshotURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/" && u.Path != "/snapshot") || u.RawPath != "" {
		return nil, errors.New("target URL must be an origin or /snapshot URL without credentials, query or fragment")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, errors.New("target URL requires HTTPS except for numeric loopback HTTP")
		}
	}
	u.Path = "/snapshot"
	return u, nil
}

func decodeSnapshot(body []byte, runID, testID string, now float64) (targetSnapshot, error) {
	var result targetSnapshot
	invalid := errors.New("invalid target snapshot")
	// Reject missing, extra and duplicate keys, including duplicate identity or
	// timestamp fields that different JSON consumers could interpret differently.
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return result, invalid
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return result, invalid
		}
		key, ok := token.(string)
		if !ok {
			return result, invalid
		}
		if _, exists := fields[key]; exists {
			return result, invalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return result, invalid
		}
		fields[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return result, invalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return result, invalid
	}
	expected := []string{"schema_version", "run_id", "test_id", "target_instance_id", "started_at_unix", "observed_at_unix", "target_requests"}
	allowed := map[string]bool{
		"per_generation_requests":      true,
		"highest_generation":           true,
		"duplicate_generation_overlap": true,
	}
	for _, key := range expected {
		allowed[key] = true
		if _, ok := fields[key]; !ok {
			return result, invalid
		}
	}
	for key := range fields {
		if !allowed[key] {
			return result, invalid
		}
	}
	if json.Unmarshal(body, &result) != nil || result.SchemaVersion != 2 || result.RunID != runID || result.TestID != testID || !testIDPattern.MatchString(result.TestID) || !instancePattern.MatchString(result.TargetInstanceID) ||
		math.IsNaN(result.StartedAt) || math.IsInf(result.StartedAt, 0) || result.StartedAt <= 0 || result.StartedAt > result.ObservedAt ||
		math.IsNaN(result.ObservedAt) || math.IsInf(result.ObservedAt, 0) || result.ObservedAt < now-5 || result.ObservedAt > now {
		return targetSnapshot{}, invalid
	}
	if !validateSnapshotGenerations(fields, result) {
		return targetSnapshot{}, invalid
	}
	return result, nil
}

func validateSnapshotGenerations(fields map[string]json.RawMessage, result targetSnapshot) bool {
	rawPerGen, hasPerGen := fields["per_generation_requests"]
	_, hasHighest := fields["highest_generation"]
	_, hasOverlap := fields["duplicate_generation_overlap"]
	if !hasPerGen {
		return !hasHighest && !hasOverlap && len(result.PerGenerationRequests) == 0 && result.HighestGeneration == 0 && result.DuplicateGenerationOverlap == 0
	}
	if len(result.PerGenerationRequests) == 0 || len(result.PerGenerationRequests) > maxGenerationsPerTest {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(rawPerGen))
	if tok, err := decoder.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{}, len(result.PerGenerationRequests))
	var sum, maxGen uint64
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := tok.(string)
		if !ok {
			return false
		}
		if _, dup := seen[key]; dup {
			return false
		}
		seen[key] = struct{}{}
		gen, err := strconv.ParseUint(key, 10, 64)
		if err != nil || gen == 0 || strconv.FormatUint(gen, 10) != key {
			return false
		}
		var count uint64
		if decoder.Decode(&count) != nil || count == 0 || sum > ^uint64(0)-count {
			return false
		}
		sum += count
		if gen > maxGen {
			maxGen = gen
		}
	}
	if tok, err := decoder.Token(); err != nil || tok != json.Delim('}') || len(seen) != len(result.PerGenerationRequests) {
		return false
	}
	return sum <= result.TargetRequests && result.HighestGeneration == maxGen && result.DuplicateGenerationOverlap <= result.TargetRequests
}

func observeTarget(ctx context.Context, c targetConfig, testID, rawURL, caFile string, timeout time.Duration) (targetSnapshot, error) {
	var empty targetSnapshot
	if !testIDPattern.MatchString(testID) {
		return empty, errors.New("invalid test identity")
	}
	if timeout <= 0 || timeout > 10*time.Second {
		return empty, errors.New("timeout must be positive and at most 10s")
	}
	u, err := snapshotURL(rawURL)
	if err != nil {
		return empty, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		// Bound local trust input too. Never print its content or filesystem errors.
		f, err := os.Open(caFile)
		if err != nil {
			return empty, errors.New("cannot read target CA")
		}
		pem, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
		f.Close()
		if err != nil || len(pem) > 1024*1024 {
			return empty, errors.New("invalid target CA")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return empty, errors.New("invalid target CA")
		}
		tlsConfig.RootCAs = pool
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 8192}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty, errors.New("invalid target URL")
	}
	req.Header.Set("Authorization", "Bearer "+c.ObserverToken)
	req.Header.Set("X-Apexload-Fault-Test", testID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return empty, errors.New("target snapshot request failed")
	}
	defer response.Body.Close()
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || contentType != "application/json" || response.Header.Get("Content-Encoding") != "" {
		return empty, errors.New("target snapshot response rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(body) > 4096 {
		return empty, errors.New("target snapshot read failed or exceeded 4 KiB")
	}
	if ctx.Err() != nil {
		return empty, errors.New("target snapshot deadline exceeded")
	}
	return decodeSnapshot(body, c.RunID, testID, unixNow())
}

func observeCommand(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("apexfault-target observe", flag.ContinueOnError)
	config := flags.String("config", "", "absolute private target.json path")
	testID := flags.String("test-id", "", "managed test ID whose requests are observed")
	targetURL := flags.String("url", "", "target HTTPS URL; numeric loopback HTTP is also allowed")
	ca := flags.String("ca", "", "optional PEM CA file; otherwise use system trust roots")
	timeout := flags.Duration("timeout", 3*time.Second, "snapshot request deadline (maximum 10s)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected observe arguments")
	}
	c, err := readConfig(*config)
	if err != nil {
		return err
	}
	snapshot, err := observeTarget(ctx, c, *testID, *targetURL, *ca, *timeout)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(snapshot); err != nil {
		return errors.New("cannot write target snapshot")
	}
	return nil
}

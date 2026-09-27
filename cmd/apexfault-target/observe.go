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
	"time"
)

// targetSnapshot is a projection, never a copy of an arbitrary remote response.
// It deliberately has no controller, VUser or execution-ownership claims.
type targetSnapshot struct {
	SchemaVersion    int     `json:"schema_version"`
	RunID            string  `json:"run_id"`
	TargetInstanceID string  `json:"target_instance_id"`
	StartedAt        float64 `json:"started_at_unix"`
	ObservedAt       float64 `json:"observed_at_unix"`
	TargetRequests   uint64  `json:"target_requests"`
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

func decodeSnapshot(body []byte, runID string, now float64) (targetSnapshot, error) {
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
	expected := []string{"schema_version", "run_id", "target_instance_id", "started_at_unix", "observed_at_unix", "target_requests"}
	if len(fields) != len(expected) {
		return result, invalid
	}
	for _, key := range expected {
		if _, ok := fields[key]; !ok {
			return result, invalid
		}
	}
	if json.Unmarshal(body, &result) != nil || result.SchemaVersion != 1 || result.RunID != runID || !instancePattern.MatchString(result.TargetInstanceID) ||
		math.IsNaN(result.StartedAt) || math.IsInf(result.StartedAt, 0) || result.StartedAt <= 0 || result.StartedAt > result.ObservedAt ||
		math.IsNaN(result.ObservedAt) || math.IsInf(result.ObservedAt, 0) || result.ObservedAt < now-5 || result.ObservedAt > now {
		return targetSnapshot{}, invalid
	}
	return result, nil
}

func observeTarget(ctx context.Context, c targetConfig, rawURL, caFile string, timeout time.Duration) (targetSnapshot, error) {
	var empty targetSnapshot
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
	return decodeSnapshot(body, c.RunID, unixNow())
}

func observeCommand(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("apexfault-target observe", flag.ContinueOnError)
	config := flags.String("config", "", "absolute private target.json path")
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
	snapshot, err := observeTarget(ctx, c, *targetURL, *ca, *timeout)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(snapshot); err != nil {
		return errors.New("cannot write target snapshot")
	}
	return nil
}

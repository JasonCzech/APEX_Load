package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func validSnapshot(now float64) targetSnapshot {
	return targetSnapshot{2, testConfig().RunID, faultTestID, strings.Repeat("b", 32), now - 10, now, 42}
}

func TestSnapshotURL(t *testing.T) {
	for _, raw := range []string{"https://target.test", "https://target.test/", "https://target.test/snapshot", "http://127.0.0.1:8090", "http://[::1]:8090"} {
		u, err := snapshotURL(raw)
		if err != nil || u.Path != "/snapshot" {
			t.Fatalf("URL %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "target.test", "http://localhost:8090", "http://10.0.0.1", "https://u:secret@target.test", "https://target.test/?secret=x", "https://target.test/?", "https://target.test/#x", "https://target.test/other", "https://target.test/%73napshot", "file:///tmp/file", "https://:8443"} {
		if _, err := snapshotURL(raw); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("URL %q: %v", raw, err)
		}
	}
}

func TestDecodeSnapshot(t *testing.T) {
	now := unixNow()
	valid := validSnapshot(now)
	body, _ := json.Marshal(valid)
	got, err := decodeSnapshot(body, testConfig().RunID, faultTestID, now)
	if err != nil || got != valid {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"schema_version", 1}, {"schema_version", true}, {"run_id", strings.Repeat("c", 16)},
		{"test_id", "other-test"}, {"test_id", true},
		{"target_instance_id", "not-an-instance"}, {"started_at_unix", now + 1}, {"started_at_unix", 0},
		{"observed_at_unix", now - 6}, {"observed_at_unix", now + 1},
		{"target_requests", -1}, {"target_requests", 1.5}, {"target_requests", true}, {"target_requests", nil},
	} {
		t.Run(tc.field+"-"+stringMustJSON(tc.value), func(t *testing.T) {
			var fields map[string]any
			json.Unmarshal(body, &fields)
			fields[tc.field] = tc.value
			altered, _ := json.Marshal(fields)
			if _, err := decodeSnapshot(altered, testConfig().RunID, faultTestID, now); err == nil {
				t.Fatalf("accepted %s", altered)
			}
		})
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(body, &fields)
	for key := range fields {
		var missing map[string]json.RawMessage
		json.Unmarshal(body, &missing)
		delete(missing, key)
		altered, _ := json.Marshal(missing)
		if _, err := decodeSnapshot(altered, testConfig().RunID, faultTestID, now); err == nil {
			t.Fatalf("accepted missing %s", key)
		}
	}
	for _, raw := range []string{"[]", "null", "{", string(body) + "{}", strings.TrimSuffix(string(body), "}") + `,"run_id":"` + testConfig().RunID + `"}`, strings.TrimSuffix(string(body), "}") + `,"secret":"do-not-echo"}`} {
		if _, err := decodeSnapshot([]byte(raw), testConfig().RunID, faultTestID, now); err == nil || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatalf("unsafe parse result: %v", err)
		}
	}
}

func stringMustJSON(value any) string { data, _ := json.Marshal(value); return string(data) }

func TestObserveVerifiedTarget(t *testing.T) {
	c := testConfig()
	target, err := newTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(target)
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := observeTarget(context.Background(), c, faultTestID, server.URL, "", time.Second); err == nil {
		t.Fatal("untrusted TLS was accepted")
	}
	for _, invalid := range []string{"", "bad value", "../test"} {
		if _, err := observeTarget(context.Background(), c, invalid, server.URL, ca, time.Second); err == nil {
			t.Fatalf("invalid test ID %q was accepted", invalid)
		}
	}
	got, err := observeTarget(context.Background(), c, faultTestID, server.URL, ca, time.Second)
	if err != nil || got.RunID != c.RunID || got.TestID != faultTestID || got.TargetRequests != 0 || got.TargetInstanceID != target.instance {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
	data, _ := json.Marshal(c)
	config := configFile(t, string(data))
	var output bytes.Buffer
	if err := observeCommand(context.Background(), []string{"--config", config, "--test-id", faultTestID, "--url", server.URL, "--ca", ca}, &output); err != nil {
		t.Fatal(err)
	}
	var decoded targetSnapshot
	if json.Unmarshal(output.Bytes(), &decoded) != nil || decoded.TargetInstanceID != target.instance {
		t.Fatalf("output=%s", &output)
	}
	if strings.Contains(output.String(), c.LoadToken) || strings.Contains(output.String(), c.ObserverToken) {
		t.Fatal("output includes credentials")
	}
	for _, badCA := range []string{filepath.Join(t.TempDir(), "missing"), configFile(t, "invalid PEM")} {
		if _, err := observeTarget(context.Background(), c, faultTestID, server.URL, badCA, time.Second); err == nil {
			t.Fatal("invalid CA accepted")
		}
	}
}

func TestObserveRejectsRedirectWithoutSendingCredentials(t *testing.T) {
	var calls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	if _, err := observeTarget(context.Background(), testConfig(), faultTestID, origin.URL, "", time.Second); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("redirect target received request")
	}
}

func TestObserveBoundedResponses(t *testing.T) {
	for _, mode := range []string{"auth", "status", "type", "compressed", "large", "invalid", "slow", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			c := testConfig()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/snapshot" || r.Header.Get("Authorization") != "Bearer "+c.ObserverToken || r.Header.Get("X-Apexload-Fault-Test") != faultTestID {
					t.Error("wrong observer request")
				}
				if mode == "auth" {
					w.WriteHeader(401)
					return
				}
				if mode == "status" {
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "type" {
					w.Header().Set("Content-Type", "text/html")
				}
				if mode == "compressed" {
					w.Header().Set("Content-Encoding", "gzip")
				}
				if mode == "large" {
					io.WriteString(w, strings.Repeat("x", 4097))
					return
				}
				if mode == "invalid" {
					io.WriteString(w, "server-secret")
					return
				}
				if mode == "slow" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				json.NewEncoder(w).Encode(validSnapshot(unixNow()))
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			started := time.Now()
			_, err := observeTarget(ctx, c, faultTestID, server.URL, "", 50*time.Millisecond)
			if err == nil || strings.Contains(err.Error(), "server-secret") || strings.Contains(err.Error(), c.ObserverToken) {
				t.Fatalf("unsafe error=%v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("deadline did not bound response read")
			}
		})
	}
	for _, timeout := range []time.Duration{0, -time.Second, 11 * time.Second} {
		if _, err := observeTarget(context.Background(), testConfig(), faultTestID, "https://target.test", "", timeout); err == nil {
			t.Fatal("invalid timeout accepted")
		}
	}
}

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, errors.New("private-writer-detail") }

func TestObserveCommandErrors(t *testing.T) {
	c := testConfig()
	data, _ := json.Marshal(c)
	config := configFile(t, string(data))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(validSnapshot(unixNow()))
	}))
	defer server.Close()
	for _, args := range [][]string{{}, {"--unknown"}, {"--config", config, "extra"}, {"--config", config, "--url", server.URL, "--timeout", "0s"}} {
		var output bytes.Buffer
		if err := observeCommand(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("args=%v err=%v output=%s", args, err, &output)
		}
	}
	err := observeCommand(context.Background(), []string{"--config", config, "--test-id", faultTestID, "--url", server.URL}, failedOutput{})
	if err == nil || strings.Contains(err.Error(), "private-writer-detail") {
		t.Fatalf("unsafe output error: %v", err)
	}
}

func TestObserveResponseSizeBoundary(t *testing.T) {
	for _, size := range []int{4096, 4097} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			data, _ := json.Marshal(validSnapshot(unixNow()))
			w.Write(append(data, bytes.Repeat([]byte(" "), size-len(data))...))
		}))
		_, err := observeTarget(context.Background(), testConfig(), faultTestID, server.URL, "", time.Second)
		server.Close()
		if (err != nil) != (size > 4096) {
			t.Fatalf("size=%d error=%v", size, err)
		}
	}
}

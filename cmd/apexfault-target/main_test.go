package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const faultTestID = "test-1"

func testConfig() targetConfig {
	return targetConfig{strings.Repeat("a", 16), strings.Repeat("l", 43), strings.Repeat("o", 43)}
}
func configFile(t *testing.T, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(p, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadConfig(t *testing.T) {
	good, _ := json.Marshal(testConfig())
	got, err := readConfig(configFile(t, string(good)))
	if err != nil || got != testConfig() {
		t.Fatalf("config=%+v err=%v", got, err)
	}
	for _, tc := range []struct{ name, value string }{
		{"empty", ""}, {"extra-json", string(good) + "{}"}, {"unknown", strings.TrimSuffix(string(good), "}") + `,"secret":"do-not-echo"}`},
		{"run", strings.Replace(string(good), strings.Repeat("a", 16), "wrong", 1)},
		{"weak-token", strings.Replace(string(good), strings.Repeat("l", 43), "short", 1)},
		{"same-token", strings.Replace(string(good), strings.Repeat("l", 43), strings.Repeat("o", 43), 1)},
		{"oversized", strings.Repeat("x", 16385)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readConfig(configFile(t, tc.value))
			if err == nil || strings.Contains(err.Error(), "do-not-echo") {
				t.Fatalf("expected safe error, got %v", err)
			}
		})
	}
	for _, path := range []string{"relative.json", filepath.Join(t.TempDir(), "absent"), t.TempDir()} {
		if _, err := readConfig(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestTargetRequestIsolation(t *testing.T) {
	c := testConfig()
	target, err := newTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, token, run string
		status                         int
	}{
		{"health", "GET", "/healthz", "", "", 204},
		{"no-auth", "GET", "/load", "", c.RunID, 401},
		{"wrong-auth", "GET", "/load", c.ObserverToken, c.RunID, 401},
		{"no-run", "GET", "/load", c.LoadToken, "", 409},
		{"wrong-run", "GET", "/load", c.LoadToken, strings.Repeat("b", 16), 409},
		{"snapshot-load-token", "GET", "/snapshot", c.LoadToken, "", 401},
		{"snapshot", "GET", "/snapshot", c.ObserverToken, "", 200},
		{"method", "POST", "/load", c.LoadToken, c.RunID, 405},
		{"head", "HEAD", "/load", c.LoadToken, c.RunID, 405},
		{"query", "GET", "/load?token=do-not-echo", c.LoadToken, c.RunID, 400},
		{"missing", "GET", "/missing", c.LoadToken, c.RunID, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.run != "" {
				req.Header.Set("X-Apexload-Fault-Run", tc.run)
			}
			if tc.token != "" {
				req.Header.Set("X-Apexload-Fault-Test", faultTestID)
			}
			response := httptest.NewRecorder()
			target.ServeHTTP(response, req)
			if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d headers=%v", response.Code, response.Header())
			}
			if strings.Contains(response.Body.String(), c.LoadToken) || strings.Contains(response.Body.String(), c.ObserverToken) || strings.Contains(response.Body.String(), "do-not-echo") {
				t.Fatal("response leaked input")
			}
		})
	}
	for _, header := range []string{"Authorization", "X-Apexload-Fault-Run", "X-Apexload-Fault-Test"} {
		req := httptest.NewRequest("GET", "/load", nil)
		req.Header.Set("Authorization", "Bearer "+c.LoadToken)
		req.Header.Set("X-Apexload-Fault-Run", c.RunID)
		req.Header.Set("X-Apexload-Fault-Test", faultTestID)
		req.Header.Add(header, req.Header.Get(header))
		response := httptest.NewRecorder()
		target.ServeHTTP(response, req)
		if response.Code < 400 {
			t.Fatalf("accepted duplicate %s", header)
		}
	}
	if len(target.counts) != 0 {
		t.Fatalf("invalid/read requests counted: %v", target.counts)
	}
	target.counts[faultTestID] = ^uint64(0)
	req := httptest.NewRequest("GET", "/load", nil)
	req.Header.Set("Authorization", "Bearer "+c.LoadToken)
	req.Header.Set("X-Apexload-Fault-Run", c.RunID)
	req.Header.Set("X-Apexload-Fault-Test", faultTestID)
	response := httptest.NewRecorder()
	target.ServeHTTP(response, req)
	if response.Code != 503 || target.counts[faultTestID] != ^uint64(0) {
		t.Fatal("counter overflow not rejected")
	}
}

func TestTargetConcurrentTrafficAndSnapshots(t *testing.T) {
	c := testConfig()
	target, err := newTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(target)
	defer server.Close()
	client := server.Client()
	snapshot := func() map[string]any {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL+"/snapshot", nil)
		req.Header.Set("Authorization", "Bearer "+c.ObserverToken)
		req.Header.Set("X-Apexload-Fault-Test", faultTestID)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				req, _ := http.NewRequest("GET", server.URL+"/load", nil)
				req.Header.Set("Authorization", "Bearer "+c.LoadToken)
				req.Header.Set("X-Apexload-Fault-Run", c.RunID)
				req.Header.Set("X-Apexload-Fault-Test", faultTestID)
				response, err := client.Do(req)
				if err != nil {
					failures <- err
					return
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != 204 {
					failures <- io.ErrUnexpectedEOF
					return
				}
			}
		}()
	}
	previous := float64(0)
	for i := 0; i < 10; i++ {
		got := snapshot()["target_requests"].(float64)
		if got < previous {
			t.Fatal("counter regressed")
		}
		previous = got
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	after := snapshot()
	if after["target_requests"] != float64(500) || after["run_id"] != c.RunID || after["test_id"] != faultTestID || after["schema_version"] != float64(2) || after["target_instance_id"] != before["target_instance_id"] || after["observed_at_unix"].(float64) <= before["observed_at_unix"].(float64) || after["started_at_unix"] != before["started_at_unix"] || len(after) != 7 {
		t.Fatalf("snapshot=%v before=%v", after, before)
	}
	replacement, err := newTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.instance == target.instance || len(replacement.instance) != 32 || len(replacement.counts) != 0 {
		t.Fatal("replacement did not get a fresh identity/counter")
	}
}

func TestTargetCountersAreIsolatedByTestID(t *testing.T) {
	c := testConfig()
	target, err := newTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, token, testID string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Apexload-Fault-Test", testID)
		if path == "/load" {
			req.Header.Set("X-Apexload-Fault-Run", c.RunID)
		}
		response := httptest.NewRecorder()
		target.ServeHTTP(response, req)
		return response
	}
	for _, testID := range []string{"first", "first", "other"} {
		if got := request("/load", c.LoadToken, testID).Code; got != http.StatusNoContent {
			t.Fatalf("load %s status = %d", testID, got)
		}
	}
	for testID, want := range map[string]uint64{"first": 2, "other": 1, "new": 0} {
		response := request("/snapshot", c.ObserverToken, testID)
		if response.Code != http.StatusOK {
			t.Fatalf("snapshot %s status = %d", testID, response.Code)
		}
		var snapshot targetSnapshot
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || snapshot.TestID != testID || snapshot.TargetRequests != want {
			t.Fatalf("snapshot %s = %+v, err=%v", testID, snapshot, err)
		}
	}
	for _, testID := range []string{"", "bad value", "../other"} {
		for _, path := range []string{"/load", "/snapshot"} {
			if got := request(path, map[string]string{"/load": c.LoadToken, "/snapshot": c.ObserverToken}[path], testID).Code; got != http.StatusBadRequest {
				t.Fatalf("accepted test ID %q at %s: %d", testID, path, got)
			}
		}
	}
	for _, path := range []string{"/load", "/snapshot"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if path == "/load" {
			req.Header.Set("Authorization", "Bearer "+c.LoadToken)
			req.Header.Set("X-Apexload-Fault-Run", c.RunID)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.ObserverToken)
		}
		req.Header.Add("X-Apexload-Fault-Test", "first")
		req.Header.Add("X-Apexload-Fault-Test", "other")
		response := httptest.NewRecorder()
		target.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("duplicate test identity at %s accepted: %d", path, response.Code)
		}
	}
	if target.counts["first"] != 2 || target.counts["other"] != 1 || len(target.counts) != 2 {
		t.Fatalf("invalid requests affected counts: %v", target.counts)
	}
	for i := len(target.counts); i < maxTestCounters; i++ {
		target.counts["reserved-"+strconv.Itoa(i)] = 1
	}
	if got := request("/load", c.LoadToken, "overflow").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("counter capacity status = %d", got)
	}
	if got := request("/load", c.LoadToken, "first").Code; got != http.StatusNoContent {
		t.Fatalf("existing counter at capacity status = %d", got)
	}
}

func TestTargetGenerationCountersAndDuplicateOverlap(t *testing.T) {
	c := testConfig()
	target, err := newTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	loadGen := func(gen string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/load", nil)
		req.Header.Set("Authorization", "Bearer "+c.LoadToken)
		req.Header.Set("X-Apexload-Fault-Run", c.RunID)
		req.Header.Set("X-Apexload-Fault-Test", faultTestID)
		if gen != "" {
			req.Header.Set("X-Apexload-Fault-Generation", gen)
		}
		rec := httptest.NewRecorder()
		target.ServeHTTP(rec, req)
		return rec
	}
	for _, invalid := range []string{"0", "-1", "abc", "18446744073709551616"} {
		if got := loadGen(invalid).Code; got != http.StatusBadRequest {
			t.Fatalf("invalid generation %q accepted with status %d", invalid, got)
		}
	}
	dupReq := httptest.NewRequest(http.MethodGet, "/load", nil)
	dupReq.Header.Set("Authorization", "Bearer "+c.LoadToken)
	dupReq.Header.Set("X-Apexload-Fault-Run", c.RunID)
	dupReq.Header.Set("X-Apexload-Fault-Test", faultTestID)
	dupReq.Header.Add("X-Apexload-Fault-Generation", "1")
	dupReq.Header.Add("X-Apexload-Fault-Generation", "2")
	dupRec := httptest.NewRecorder()
	target.ServeHTTP(dupRec, dupReq)
	if dupRec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate generation header accepted: %d", dupRec.Code)
	}

	// Send 3 requests on generation 1, 2 on generation 2, then 1 late request on generation 1 (overlap).
	for _, gen := range []string{"1", "1", "1", "2", "2", "1"} {
		if got := loadGen(gen).Code; got != http.StatusNoContent {
			t.Fatalf("load gen %s status = %d", gen, got)
		}
	}

	snapReq := httptest.NewRequest(http.MethodGet, "/snapshot", nil)
	snapReq.Header.Set("Authorization", "Bearer "+c.ObserverToken)
	snapReq.Header.Set("X-Apexload-Fault-Test", faultTestID)
	snapRec := httptest.NewRecorder()
	target.ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d", snapRec.Code)
	}
	snap, err := decodeSnapshot(snapRec.Body.Bytes(), c.RunID, faultTestID, unixNow())
	if err != nil {
		t.Fatalf("decodeSnapshot: %v", err)
	}
	if snap.TargetRequests != 6 || snap.HighestGeneration != 2 || snap.DuplicateGenerationOverlap != 1 {
		t.Fatalf("unexpected snapshot counters: %+v", snap)
	}
	if snap.PerGenerationRequests["1"] != 4 || snap.PerGenerationRequests["2"] != 2 || len(snap.PerGenerationRequests) != 2 {
		t.Fatalf("unexpected per_generation_requests: %+v", snap.PerGenerationRequests)
	}

	// Verify generation cap per test (maxGenerationsPerTest) returns 503 for new generations.
	for g := 3; g <= maxGenerationsPerTest; g++ {
		if got := loadGen(strconv.Itoa(g)).Code; got != http.StatusNoContent {
			t.Fatalf("generation %d status = %d", g, got)
		}
	}
	if got := loadGen(strconv.Itoa(maxGenerationsPerTest + 1)).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("generation overflow status = %d, want 503", got)
	}
	if got := loadGen("2").Code; got != http.StatusNoContent {
		t.Fatalf("existing generation at cap status = %d, want 204", got)
	}
}

func TestCommandValidation(t *testing.T) {
	for _, args := range [][]string{{}, {"--tls-cert", "cert"}, {"unexpected"}, {"--unknown"}} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestServeTargetLifecycle(t *testing.T) {
	for _, initiallyCanceled := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		target, err := newTarget(testConfig())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if initiallyCanceled {
			cancel()
		}
		result := make(chan error, 1)
		go func() { result <- serveTarget(ctx, &http.Server{Handler: target}, listener, "", "") }()
		if !initiallyCanceled {
			client := &http.Client{Timeout: time.Second}
			response, err := client.Get("http://" + listener.Addr().String() + "/healthz")
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 204 {
				cancel()
				t.Fatal("health failed")
			}
		}
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("shutdown did not finish")
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	err = serveTarget(context.Background(), &http.Server{}, listener, "missing-private-cert", "missing-private-key")
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe TLS error: %v", err)
	}
}

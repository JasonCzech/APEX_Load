package collectoragent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{CollectorID: "c", Token: "t"}); err == nil {
		t.Fatal("expected error for missing controller URL")
	}
	if _, err := New(Config{ControllerURL: "http://localhost", Token: "t"}); err == nil {
		t.Fatal("expected error for missing collector id")
	}
	if _, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c"}); err == nil {
		t.Fatal("expected error for missing token")
	}
	a, err := New(Config{ControllerURL: "http://localhost/", CollectorID: "c", Token: "t"})
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if a.cfg.ControllerURL != "http://localhost" {
		t.Fatalf("trailing slash not trimmed: %q", a.cfg.ControllerURL)
	}
	if a.cfg.Interval <= 0 || len(a.cfg.Packs) == 0 {
		t.Fatalf("defaults not applied: %+v", a.cfg)
	}
}

func TestNewRejectsCleartextTokenTransport(t *testing.T) {
	cases := []struct {
		name          string
		url           string
		allowInsecure bool
		wantErr       bool
	}{
		{"loopback http ok", "http://127.0.0.1:8080", false, false},
		{"localhost http ok", "http://localhost:8080", false, false},
		{"https ok", "https://controller.example:8080", false, false},
		{"non-loopback http rejected", "http://controller.example:8080", false, true},
		{"non-loopback http allowed with opt-in", "http://controller.example:8080", true, false},
		{"unknown scheme rejected", "ftp://controller.example", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{ControllerURL: tc.url, CollectorID: "c", Token: "t", AllowInsecure: tc.allowInsecure})
			if tc.wantErr && err == nil {
				t.Fatalf("New(%q) = nil error, want rejection", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("New(%q) = %v, want accepted", tc.url, err)
			}
		})
	}
}

func TestHostSamplesNonEmpty(t *testing.T) {
	s := hostSamples()
	if len(s) == 0 {
		t.Fatal("expected non-empty host samples")
	}
	if s["num_cpu"] < 1 {
		t.Fatalf("num_cpu = %v, want >= 1", s["num_cpu"])
	}
}

func TestCollectBuildsPackPerName(t *testing.T) {
	a, _ := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", Packs: []string{"linux-hosts", "jvm"}})
	hb := a.collect()
	if hb.Health != "healthy" || len(hb.Packs) != 2 {
		t.Fatalf("collect = %+v", hb)
	}
	if hb.Packs[0].Pack != "linux-hosts" || len(hb.Packs[0].Samples) == 0 {
		t.Fatalf("pack 0 = %+v", hb.Packs[0])
	}
}

func TestPushHeartbeatSetsAuthAndPath(t *testing.T) {
	var gotAuth, gotPath string
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var hb heartbeat
		_ = json.NewDecoder(r.Body).Decode(&hb)
		if hb.Health == "" || len(hb.Packs) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	agent, err := New(Config{
		ControllerURL: srv.URL, CollectorID: "collector-7", Token: "srk_abc",
		Interval: time.Hour, Packs: []string{"linux-hosts"}, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := agent.pushHeartbeat(context.Background(), agent.collect()); err != nil {
		t.Fatalf("push: %v", err)
	}
	if gotAuth != "Bearer srk_abc" {
		t.Fatalf("Authorization = %q, want Bearer srk_abc", gotAuth)
	}
	if gotPath != "/api/v1/monitoring/collectors/collector-7/heartbeat" {
		t.Fatalf("path = %q", gotPath)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}

func TestPushHeartbeatHandles401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	agent, _ := New(Config{ControllerURL: srv.URL, CollectorID: "c", Token: "bad", HTTPClient: srv.Client()})
	if err := agent.pushHeartbeat(context.Background(), agent.collect()); err == nil {
		t.Fatal("expected error on 401")
	}
}

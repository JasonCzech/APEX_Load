package engine

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A goal run's group must hold its TPS target from the first iteration even
// when its VUsers could deliver far more, and follow a changed target at once.
// Before the cap, ten fast VUsers sent hundreds of requests a second against a
// 2 TPS traffic-profile point.
func TestMultiGroupThroughputCapHoldsGroupTarget(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		requests.Add(1)
		w.WriteHeader(stdhttp.StatusOK)
	}))
	defer server.Close()
	script := &dsl.Script{
		Name:     "browse",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  server.URL,
			RunLogic: dsl.RunLogicConfig{AutoTransactions: "per_action"},
		},
		Actions: []dsl.Action{
			{Name: "home", Type: "GET", Method: "GET", Target: "/home"},
			{Name: "catalog", Type: "GET", Method: "GET", Target: "/catalog"},
		},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:                 "browse",
			Script:               script,
			VUsers:               10,
			ThroughputCapTPS:     10,
			ThroughputCapEnabled: true,
		}},
		Duration: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	<-mge.Ready()
	time.Sleep(1500 * time.Millisecond)
	atFirst := requests.Load()
	if tps, enabled := mge.GroupThroughputCap("browse"); !enabled || tps != 10 {
		t.Fatalf("cap = %v/%v, want 10 TPS enabled", tps, enabled)
	}
	if err := mge.SetGroupThroughputCap("browse", 40, true); err != nil {
		t.Fatalf("SetGroupThroughputCap: %v", err)
	}
	if err := mge.SetGroupThroughputCap("missing", 1, true); err == nil {
		t.Fatal("an unknown group must be rejected")
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	total := requests.Load()
	// Each request is a per-action transaction: 10 TPS for 1.5 s is about 15.
	if atFirst < 10 || atFirst > 22 {
		t.Fatalf("requests in the first 1.5s at 10 TPS = %d, want about 15", atFirst)
	}
	// Then 40 TPS for about 1.5 s: about 60 more.
	if later := total - atFirst; later < 40 || later > 75 {
		t.Fatalf("requests after raising the cap to 40 TPS = %d, want about 60", later)
	}
}

package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type temperatureProtocol struct {
	iterationResetProtocol
	temperatures []string
	asked        int
}

func (p *temperatureProtocol) IterationTemperature() string {
	temperature := p.temperatures[min(p.asked, len(p.temperatures)-1)]
	p.asked++
	return temperature
}

type allResultsSink struct {
	mu      sync.Mutex
	results []*protocol.Result
}

func (s *allResultsSink) RecordResult(result *protocol.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, result)
	return nil
}

// A browser iteration's results carry whether it started cold or warm; init
// and teardown are not iterations, and a protocol that reports "" leaves its
// iterations untagged.
func TestVUserTagsBrowserIterationsColdOrWarm(t *testing.T) {
	for _, tc := range []struct {
		name         string
		temperatures []string
		want         []string
	}{
		{"persistent session", []string{protocol.IterationCold, protocol.IterationWarm, protocol.IterationWarm}, []string{"", protocol.IterationCold, protocol.IterationWarm, protocol.IterationWarm, ""}},
		{"new user each iteration", []string{""}, []string{"", "", "", "", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proto := &temperatureProtocol{temperatures: tc.temperatures}
			sink := &allResultsSink{}
			vu := &VUser{
				Proto:      proto,
				Metrics:    NewMetricsCollector(64),
				ResultSink: sink,
				Script: &dsl.Script{
					Protocol: "browser",
					Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 3}},
					Init:     []dsl.Action{{Name: "Sign in", Type: "browser_navigate", Target: "/login"}},
					Actions:  []dsl.Action{{Name: "Open account", Type: "browser_navigate", Target: "/account"}},
					Teardown: []dsl.Action{{Name: "Sign out", Type: "browser_navigate", Target: "/logout"}},
				},
			}
			vu.Run(context.Background())
			var got []string
			for _, result := range sink.results {
				if result.BusinessTransaction {
					continue
				}
				got = append(got, result.PropertyDimensions[protocol.BrowserIterationDimension])
			}
			if len(got) != len(tc.want) {
				t.Fatalf("results = %d (%v), want %d", len(got), got, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("iteration tags = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// The live response-time SLA judges warm iterations: a cold one is reported
// but not counted.
func TestTransactionResponseTimeSLASkipsColdBrowserIterations(t *testing.T) {
	mc := NewMetricsCollector(10)
	mc.SetTransactionResponseTimeSLAThresholds(map[string]float64{"A": 3000})
	cold := map[string]string{protocol.BrowserIterationDimension: protocol.IterationCold}
	warm := map[string]string{protocol.BrowserIterationDimension: protocol.IterationWarm}
	snapshot := mc.computeSnapshot([]*protocol.Result{
		{Group: "A", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: 9000 * time.Millisecond, PropertyDimensions: cold},
		{Group: "A", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: 1000 * time.Millisecond, PropertyDimensions: warm},
		{Group: "A", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: 4000 * time.Millisecond},
	}, time.Second)
	if len(snapshot.TransactionResponseTimeSLA) != 1 {
		t.Fatalf("rows = %#v", snapshot.TransactionResponseTimeSLA)
	}
	if got := snapshot.TransactionResponseTimeSLA[0]; got.TotalCount != 2 || got.EligibleSuccessCount != 2 || got.MissedCount != 1 {
		t.Fatalf("compliance = %#v, want the warm and the untagged sample", got)
	}
	if snapshot.Transactions != 3 {
		t.Fatalf("transactions = %d: a cold iteration still counts as a transaction", snapshot.Transactions)
	}
}

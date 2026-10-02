package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

func regressionFixture() *BaselineComparisonSnapshot {
	w := &BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 30, EndSeconds: 120}, Load: 100}
	current := &BaselineMeasurement{Samples: 1000, Successes: 1000, P95: 100, P99: 120, Mean: 80, SuccessfulThroughput: 10, ErrorPct: 0}
	base := *current
	return &BaselineComparisonSnapshot{Current: BaselineRunIdentity{Status: "COMPLETE"}, Baselines: []BaselineRunIdentity{{ID: "b", Status: "COMPLETE"}}, Policy: defaultBaselineComparisonPolicy(), Transactions: []BaselineComparisonRow{{ID: "r", Protocol: "kafka", Group: "consumer", Current: current, Window: w, Baselines: map[string]BaselineComparisonCell{"b": {Measurement: &base, Window: w}}}}}
}
func TestBaselineRegressionQualificationAndDirections(t *testing.T) {
	cases := []struct {
		name, status string
		change       func(*BaselineComparisonSnapshot)
	}{
		{"identical", "passed", func(*BaselineComparisonSnapshot) {}},
		{"latency", "regressed", func(s *BaselineComparisonSnapshot) { s.Transactions[0].Current.P95 = 150 }},
		{"successful throughput", "regressed", func(s *BaselineComparisonSnapshot) { s.Transactions[0].Current.SuccessfulThroughput = 5 }},
		{"error points zero baseline", "regressed", func(s *BaselineComparisonSnapshot) { s.Transactions[0].Current.ErrorPct = 2 }},
		{"improvement", "passed", func(s *BaselineComparisonSnapshot) {
			s.Transactions[0].Current.P95 = 50
			s.Transactions[0].Current.SuccessfulThroughput = 15
		}},
		{"version differs", "not_evaluated", func(s *BaselineComparisonSnapshot) {
			s.Setup = []BaselineSetupDifference{{BaselineID: "b", Field: "Version", Status: "differs"}}
		}},
		{"missing identity", "not_evaluated", func(s *BaselineComparisonSnapshot) { s.Transactions[0].Current = nil }},
		{"incomplete window", "not_evaluated", func(s *BaselineComparisonSnapshot) {
			c := s.Transactions[0].Baselines["b"]
			c.Qualification = []string{"Incomplete archive source coverage"}
			s.Transactions[0].Baselines["b"] = c
		}},
		{"insufficient", "not_evaluated", func(s *BaselineComparisonSnapshot) {
			s.Transactions[0].Current.Samples = 1
			s.Transactions[0].Current.Successes = 1
		}},
		{"no selected protocol", "not_evaluated", func(s *BaselineComparisonSnapshot) { s.Request.RegressionProtocols = []string{"grpc"} }},
		{"no selected group", "not_evaluated", func(s *BaselineComparisonSnapshot) { s.Request.RegressionGroups = []string{"other"} }},
		{"incomplete current run", "not_evaluated", func(s *BaselineComparisonSnapshot) { s.Current.Status = "STOPPED" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := regressionFixture()
			tc.change(s)
			v := evaluateBaselineRegression(s)
			if v.Status != tc.status {
				t.Fatalf("%+v", v)
			}
		})
	}
}
func TestBaselineMessagingRegressionRequiresEveryObservation(t *testing.T) {
	s := regressionFixture()
	s.Request.RegressionMetrics = []string{"consumer_lag_ms"}
	summary := func(n int64, sum float64) *protocol.MessagingSummary {
		return &protocol.MessagingSummary{Dimensions: []protocol.MessagingDimension{{Protocol: "kafka", Group: "consumer", Source: "orders", Operations: 1000, Age: protocol.MessagingMoments{Count: n, Sum: sum, Max: sum / float64(n)}}}}
	}
	s.Transactions[0].Current.Messaging = summary(1000, 150000)
	s.Transactions[0].Baselines["b"].Measurement.Messaging = summary(1000, 100000)
	if v := evaluateBaselineRegression(s); v.Status != "regressed" {
		t.Fatalf("%+v", v)
	}
	s.Transactions[0].Current.Messaging = summary(999, 150000)
	if v := evaluateBaselineRegression(s); v.Status != "not_evaluated" {
		t.Fatalf("partial scored: %+v", v)
	}
	s.Transactions[0].Current.Messaging = summary(1000, 0)
	s.Transactions[0].Baselines["b"].Measurement.Messaging = summary(1000, 0)
	if v := evaluateBaselineRegression(s); v.Status != "passed" {
		t.Fatalf("observed zero lost: %+v", v)
	}
	s.Request.RegressionMetrics = []string{"consumer_offset_lag"}
	if v := evaluateBaselineRegression(s); v.Status != "not_evaluated" {
		t.Fatalf("absent offset scored: %+v", v)
	}
}
func TestBaselineRegressionRequestBounds(t *testing.T) {
	for _, r := range []BaselineComparisonRequest{{RegressionMetrics: []string{"invented"}}, {RegressionMetrics: []string{"successful_p95", "successful_p95"}}, {RegressionGroups: []string{" bad"}}, {RegressionProtocols: []string{"kafka\n"}}} {
		if validateBaselineRegressionRequest(r) == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if err := validateBaselineRegressionRequest(BaselineComparisonRequest{RegressionMetrics: baselineRegressionMetrics}); err != nil {
		t.Fatal(err)
	}
}

func TestTypedProtocolRegressionRequiresCompleteMeasurements(t *testing.T) {
	s := regressionFixture()
	s.Request.RegressionMetrics = []string{"grpc_call_p95"}
	diagnostic := func(ms float64) *protocol.GRPCSummary {
		g := &protocol.GRPCSummary{Samples: 1000, Method: "/example/Get", Mode: "unary"}
		for i := 0; i < 1000; i++ {
			g.Call.Add(ms)
		}
		return g
	}
	s.Transactions[0].Current.GRPC = diagnostic(200)
	s.Transactions[0].Baselines["b"].Measurement.GRPC = diagnostic(100)
	if v := evaluateBaselineRegression(s); v.Status != "regressed" {
		t.Fatalf("%+v", v)
	}
	s.Transactions[0].Current.GRPC.MixedMethods = true
	if v := evaluateBaselineRegression(s); v.Status != "not_evaluated" {
		t.Fatalf("mixed methods scored: %+v", v)
	}
	s.Transactions[0].Current.GRPC.MixedMethods = false
	s.Transactions[0].Current.GRPC.Samples = 999
	if v := evaluateBaselineRegression(s); v.Status != "not_evaluated" {
		t.Fatalf("partial scored: %+v", v)
	}
}

func TestBaselineAcceptanceIdentityExcludesUnmeasuredPublisher(t *testing.T) {
	s := regressionFixture()
	s.Request.RegressionIdentities = []string{"Receive"}
	s.Request.RegressionMetrics = []string{"consumer_lag_ms"}
	s.Transactions[0].Name = "Receive"
	measured := &protocol.MessagingSummary{Dimensions: []protocol.MessagingDimension{{Operations: 1000, Age: protocol.MessagingMoments{Count: 1000, Sum: 0, Max: 0}}}}
	s.Transactions[0].Current.Messaging = measured
	s.Transactions[0].Baselines["b"].Measurement.Messaging = measured.Clone()
	s.Transactions = append(s.Transactions, BaselineComparisonRow{ID: "publisher", Name: "Publish", Protocol: "kafka", Group: "consumer"})
	if v := evaluateBaselineRegression(s); v.Status != "passed" || v.Evaluated != 1 {
		t.Fatalf("publisher pooled: %+v", v)
	}
	s.Request.RegressionIdentities = nil
	if v := evaluateBaselineRegression(s); v.Status != "not_evaluated" {
		t.Fatalf("missing publisher evidence hidden: %+v", v)
	}
}
func TestBaselineMessageThroughputPreservesMeasuredZero(t *testing.T) {
	s := regressionFixture()
	s.Request.RegressionMetrics = []string{"websocket_message_throughput"}
	s.Transactions[0].Current.Throughput = 10
	s.Transactions[0].Baselines["b"].Measurement.Throughput = 10
	s.Transactions[0].Current.WebSocket = &protocol.WebSocketSummary{Samples: 1000, MessagesReceived: 0}
	s.Transactions[0].Baselines["b"].Measurement.WebSocket = &protocol.WebSocketSummary{Samples: 1000, MessagesReceived: 1000}
	if v := evaluateBaselineRegression(s); v.Status != "regressed" {
		t.Fatalf("zero throughput lost: %+v", v)
	}
	s.Transactions[0].Current.WebSocket.Samples = 999
	if v := evaluateBaselineRegression(s); v.Status != "not_evaluated" {
		t.Fatalf("partial scored: %+v", v)
	}
}

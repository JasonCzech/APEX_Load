package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJointProjectionValidation(t *testing.T) {
	for _, tc := range []struct {
		projection JointProjection
		valid      bool
	}{
		{JointProjection{Population: "action", Dimensions: []string{"name", "region", "network_profile"}}, true},
		{JointProjection{Population: "transaction", Dimensions: []string{"group", "property:tier"}}, true},
		{JointProjection{Population: "message", Dimensions: []string{"name"}}, false},
		{JointProjection{Population: "action"}, false},
		{JointProjection{Population: "action", Dimensions: []string{"name", "group", "region", "phase"}}, false},
		{JointProjection{Population: "action", Dimensions: []string{"name", "name"}}, false},
		{JointProjection{Population: "action", Dimensions: []string{"host"}}, false},
		{JointProjection{Population: "action", Dimensions: []string{"property:a", "property:b"}}, false},
		{JointProjection{Population: "action", Dimensions: []string{"property:"}}, false},
	} {
		if err := tc.projection.Validate(); (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc.projection, err)
		}
	}
}

func jointResult(name, region, profile string, transaction bool, ms int) *protocol.Result {
	return &protocol.Result{ActionName: name, Group: "checkout", Region: region, NetworkProfile: profile, BusinessTransaction: transaction, Success: true, Duration: time.Duration(ms) * time.Millisecond, ClientMetrics: &protocol.ClientMetrics{BrowserEngine: "chromium"}, PropertyDimensions: map[string]string{"tier": "gold"}}
}

// Samples are counted under their exact tuples (a measured intersection),
// only for the projected population; a missing label is the empty value.
func TestJointAccumulatorCountsExactTuples(t *testing.T) {
	projection := &JointProjection{Population: "action", Dimensions: []string{"name", "region", "protocol"}}
	acc := newJointAccumulator(projection)
	protocols := map[string]string{"checkout": "http"}
	for _, r := range []*protocol.Result{
		jointResult("login", "east", "", false, 100),
		jointResult("login", "east", "", false, 300),
		jointResult("login", "west", "", false, 50),
		jointResult("search", "", "", false, 70),
		jointResult("Purchase", "east", "", true, 900), // a business transaction: other population
	} {
		acc.add(projection, r, protocols)
	}
	snapshot := acc.finish()
	if snapshot.Version != JointEvidenceVersion || len(snapshot.Tuples) != 3 || snapshot.Incomplete || snapshot.Other != nil {
		t.Fatalf("snapshot %+v", snapshot)
	}
	east, _ := JointTupleKey([]string{"login", "east", "http"})
	if got := snapshot.Tuples[east]; got == nil || got.Count != 2 || got.DurationDigest == nil || got.DurationDigest.Count != 2 || got.AvgDuration != 200*time.Millisecond {
		t.Fatalf("east tuple %+v", got)
	}
	empty, _ := JointTupleKey([]string{"search", "", "http"})
	if snapshot.Tuples[empty] == nil {
		t.Fatalf("a missing label was not kept as the empty value: %v", snapshot.Tuples)
	}
	if values, ok := JointTupleValues(east, 3); !ok || values[1] != "east" {
		t.Fatalf("decoded %v", values)
	}
	other := &JointProjection{Population: "transaction", Dimensions: []string{"browser_engine", "property:tier"}}
	values, in := other.tuple(jointResult("Purchase", "", "", true, 1), nil)
	if !in || values[0] != "chromium" || values[1] != "gold" {
		t.Fatalf("browser/property tuple %v %v", values, in)
	}
}

// Past JointMaxKeys tuples, samples go to one explicit other bucket, the
// distinct omitted tuples are counted and the window is incomplete.
func TestJointAccumulatorDisclosesOverflow(t *testing.T) {
	projection := &JointProjection{Population: "action", Dimensions: []string{"name"}}
	acc := newJointAccumulator(projection)
	for i := 0; i < JointMaxKeys+10; i++ {
		acc.add(projection, jointResult(fmt.Sprintf("action-%04d", i), "", "", false, 10), nil)
		acc.add(projection, jointResult(fmt.Sprintf("action-%04d", i), "", "", false, 10), nil)
	}
	acc.add(projection, jointResult(strings.Repeat("x", 300), "", "", false, 10), nil)
	snapshot := acc.finish()
	if len(snapshot.Tuples) != JointMaxKeys || !snapshot.Incomplete || snapshot.Other == nil || snapshot.Other.Count != 21 || snapshot.OmittedTuples != 11 {
		t.Fatalf("overflow tuples=%d incomplete=%v other=%+v omitted=%d", len(snapshot.Tuples), snapshot.Incomplete, snapshot.Other, snapshot.OmittedTuples)
	}
	if snapshot.Other.DurationDigest == nil || snapshot.Other.DurationDigest.Count != 21 {
		t.Fatalf("other digest %+v", snapshot.Other.DurationDigest)
	}
}

func TestMetricsCollectorPublishesJointEvidence(t *testing.T) {
	mc := NewMetricsCollector(16)
	mc.SetJointProjection(&JointProjection{Population: "action", Dimensions: []string{"name", "network_profile"}}, nil)
	snapshot := mc.computeSnapshot([]*protocol.Result{jointResult("login", "", "3g", false, 40), jointResult("login", "", "wifi", false, 10)}, time.Second)
	if snapshot.Joint == nil || len(snapshot.Joint.Tuples) != 2 || snapshot.Joint.Population != "action" {
		t.Fatalf("joint %+v", snapshot.Joint)
	}
	mc.SetJointProjection(nil, nil)
	if snapshot := mc.computeSnapshot([]*protocol.Result{jointResult("login", "", "3g", false, 40)}, time.Second); snapshot.Joint != nil {
		t.Fatal("joint evidence without a projection")
	}
}

// Labels past 256 bytes, invalid UTF-8 and keys past 1 KiB (after JSON
// escaping) are not representable tuples: they go to the other bucket.
func TestJointTupleKeyBounds(t *testing.T) {
	if _, ok := JointTupleKey([]string{strings.Repeat("a", 256)}); !ok {
		t.Fatal("a 256-byte label was refused")
	}
	if _, ok := JointTupleKey([]string{strings.Repeat("a", 257)}); ok {
		t.Fatal("a 257-byte label was admitted")
	}
	if _, ok := JointTupleKey([]string{"\xff"}); ok {
		t.Fatal("invalid UTF-8 was admitted")
	}
	quoted := strings.Repeat(`"`, 256) // each escapes to two bytes
	if _, ok := JointTupleKey([]string{quoted, quoted, quoted}); ok {
		t.Fatal("a key past 1 KiB was admitted")
	}
	if _, ok := JointTupleValues(`["`+strings.Repeat("a", 257)+`"]`, 1); ok {
		t.Fatal("a decoded label past 256 bytes was accepted")
	}
	projection := &JointProjection{Population: "action", Dimensions: []string{"name"}}
	acc := newJointAccumulator(projection)
	acc.add(projection, jointResult(strings.Repeat("x", 300), "", "", false, 10), nil)
	if snapshot := acc.finish(); len(snapshot.Tuples) != 0 || snapshot.Other == nil || !snapshot.Incomplete || snapshot.OmittedTuples != 1 {
		t.Fatalf("an unrepresentable label became a tuple: %+v", snapshot)
	}
}

// A joint tuple holds only what joint evidence publishes (counts, errors and
// the distribution beside them): a sample's diagnostics, transport timings,
// bytes, duration bounds and correlation identifiers stay with the marginal
// action metrics and never become per-tuple state.
func TestJointAccumulatorKeepsOnlyPublishedState(t *testing.T) {
	projection := &JointProjection{Population: "action", Dimensions: []string{"name"}}
	acc := newJointAccumulator(projection)
	for i, success := range []bool{true, false, true} {
		r := jointResult("login", "", "", false, 10*(i+1))
		r.Success = success
		r.DNSTime, r.ConnectTime, r.TLSTime, r.TTFB = time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond
		r.BytesSent, r.BytesReceived = 100, 200
		r.CorrelationID, r.DependencyType, r.DependencyTarget = fmt.Sprintf("corr-%d", i), "java_http", "orders"
		r.ServerTelemetry = &protocol.ServerTelemetry{ServerProcessingMs: 4, JavaHTTP: &protocol.JavaHTTPDiagnostics{}}
		acc.add(projection, r, nil)
	}
	key, _ := JointTupleKey([]string{"login"})
	tuple := acc.finish().Tuples[key]
	if tuple == nil || tuple.Count != 3 || tuple.Errors != 1 || tuple.AvgDuration != 20*time.Millisecond || tuple.P99 <= 0 ||
		tuple.DurationDigest == nil || tuple.DurationDigest.Count != 3 || tuple.DurationSketch == nil || tuple.DurationSketch.Count != 3 {
		t.Fatalf("tuple %+v", tuple)
	}
	if tuple.ServerTelemetry != nil || tuple.BytesSent != 0 || tuple.BytesRecv != 0 || tuple.AvgDNSMs != 0 || tuple.AvgTTFBMs != 0 ||
		len(tuple.CorrelationIDs) != 0 || tuple.CorrelationCount != 0 || tuple.DependencyType != "" || tuple.LastErrorMessage != "" || tuple.MinDuration != 0 || tuple.MaxDuration != 0 {
		t.Fatalf("tuple kept state joint evidence does not publish: %+v", tuple)
	}
}

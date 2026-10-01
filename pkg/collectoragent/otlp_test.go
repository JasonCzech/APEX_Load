package collectoragent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/analysis"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOTLPAuthenticationBoundsAndSemantics(t *testing.T) {
	token := strings.Repeat("t", 32)
	receiver, err := NewOTLPReceiver(map[string]string{"otlp://app": token})
	if err != nil {
		t.Fatal(err)
	}
	send := func(body, credential string) int {
		r := httptest.NewRequest("POST", "/v1/metrics", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-APEX-Monitor-Stream", "otlp://app")
		r.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		receiver.ServeHTTP(w, r)
		return w.Code
	}
	body := fmt.Sprintf(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"cpu","unit":"1","gauge":{"dataPoints":[{"timeUnixNano":"%d","asDouble":0.3}]}}]}]}]}`, time.Now().UnixNano())
	if send(body, "wrong") != http.StatusUnauthorized {
		t.Fatal("unauthorized ingest")
	}
	if send(body, token) != 200 {
		t.Fatal("gauge failed")
	}
	points := receiver.Snapshot(RunJob{Destination: "otlp://app", StartedAt: time.Now().Add(-time.Minute), IntervalSeconds: 15})
	if len(points) != 1 || *points[0].Value != .3 {
		t.Fatalf("snapshot: %+v", points)
	}
	if len(receiver.Snapshot(RunJob{Destination: "otlp://other", StartedAt: time.Now().Add(-time.Minute), IntervalSeconds: 15})) != 0 {
		t.Fatal("stream isolation")
	}
	if send(strings.Repeat("x", (1<<20)+1), token) != 400 {
		t.Fatal("body limit")
	}
}

func TestOTLPLateArrivalAcknowledgementSurvivesRestart(t *testing.T) {
	receiver, _ := NewOTLPReceiver(map[string]string{"otlp://app": strings.Repeat("t", 32)})
	value := 2.0
	at := time.Now().UTC().Add(-time.Minute)
	receiver.points["otlp://app"] = []analysis.MetricPoint{{MetricName: "late", TimestampUTC: at, Value: &value}}
	job := RunJob{ID: "job", Pack: "otel-metrics", Destination: "otlp://app", StartedAt: at.Add(-time.Minute), IntervalSeconds: 1}
	a := &Agent{cfg: Config{OTLP: receiver, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: job.Pack, Destination: job.Destination}}}, pendingBatches: map[string]*RunSampleBatch{}}
	points, err := a.collectAdapter(context.Background(), job)
	if err != nil || len(points) != 1 {
		t.Fatalf("late arrival omitted: %v %v", points, err)
	}
	a.acknowledgeOTLP(job, points)
	if err := a.saveSpool(); err != nil {
		t.Fatal(err)
	}
	restarted := &Agent{cfg: a.cfg}
	if err := restarted.loadSpool(); err != nil {
		t.Fatal(err)
	}
	points, err = restarted.collectAdapter(context.Background(), job)
	if err != nil || len(points) != 0 {
		t.Fatalf("acknowledged sample replayed: %v %v", points, err)
	}
	job.ID = "overlapping-run"
	points, err = restarted.collectAdapter(context.Background(), job)
	if err != nil || len(points) != 1 {
		t.Fatal("another run lost its observation")
	}
}

func TestAdapterUnitNormalization(t *testing.T) {
	for _, test := range []struct {
		input, want string
		scale       float64
	}{{"By", "bytes", 1}, {"s", "ms", 1000}, {"1", "ratio", 1}} {
		value := 2.0
		point := analysis.MetricPoint{Unit: test.input, Value: &value}
		if err := normalizeAdapterPoint(&point); err != nil || point.Unit != test.want || *point.Value != 2*test.scale {
			t.Fatalf("%s: %+v %v", test.input, point, err)
		}
		if value != 2 {
			t.Fatal("normalization mutated shared receiver data")
		}
	}
}

func TestOTLPJournalRestartAndFailure(t *testing.T) {
	tokens := map[string]string{"otlp://app": strings.Repeat("t", 32)}
	receiver, _ := NewOTLPReceiver(tokens)
	receiver.requireJournal = true
	send := func() int {
		body := fmt.Sprintf(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"cpu","unit":"1","gauge":{"dataPoints":[{"timeUnixNano":"%d","asDouble":0.3}]}}]}]}]}`, time.Now().UnixNano())
		request := httptest.NewRequest("POST", "/v1/metrics", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+tokens["otlp://app"])
		request.Header.Set("X-APEX-Monitor-Stream", "otlp://app")
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		return response.Code
	}
	if send() != 503 {
		t.Fatal("acknowledged before journal readiness")
	}
	path := filepath.Join(t.TempDir(), "receiver.json")
	if err := receiver.openJournal(path); err != nil {
		t.Fatal(err)
	}
	if send() != 200 {
		t.Fatal("durable acceptance failed")
	}
	recovered, _ := NewOTLPReceiver(tokens)
	if err := recovered.openJournal(path); err != nil {
		t.Fatal(err)
	}
	if len(recovered.points["otlp://app"]) != 1 {
		t.Fatal("acknowledged sample lost on restart")
	}
	receiver.closeJournal()
	if send() != 503 {
		t.Fatal("acknowledged after ownership release")
	}
}

func TestOTLPUnsupportedPointCounts(t *testing.T) {
	if got := unsupportedOTLPPointCount(json.RawMessage(`{"dataPoints":[{},{}]}`), json.RawMessage(`{"dataPoints":[{}]}`)); got != 3 {
		t.Fatalf("rejected points: %d", got)
	}
}

func TestOTLPBatchMetadataExpansionSplitsWithoutDropping(t *testing.T) {
	receiver, _ := NewOTLPReceiver(map[string]string{"otlp://app": strings.Repeat("t", 32)})
	at := time.Now().UTC().Add(-time.Second)
	points := make([]analysis.MetricPoint, 512)
	for i := range points {
		value := float64(i)
		dimensions := map[string]string{}
		for j := 0; j < 16; j++ {
			dimensions[fmt.Sprintf("label%d", j)] = strings.Repeat("x", 128)
		}
		points[i] = analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: strings.Repeat("p", 256), Source: strings.Repeat("s", 256), RunID: strings.Repeat("r", 256), TimestampUTC: at.Add(time.Duration(i) * time.Nanosecond), MetricName: "value", Value: &value, Kind: analysis.KindGauge, Unit: "count", Quality: analysis.QualityValid, Dimensions: dimensions}
	}
	receiver.points["otlp://app"] = points
	prefix, err := boundedMonitorPoints(points)
	if err != nil || len(prefix) == 0 || len(prefix) >= len(points) {
		t.Fatalf("split: %d %v", len(prefix), err)
	}
	encoded, _ := json.Marshal(prefix)
	if len(encoded) > (1<<20)-(96<<10) {
		t.Fatal("oversized prefix")
	}
	job := RunJob{ID: "j", Pack: "otel-metrics", Destination: "otlp://app", StartedAt: at.Add(-time.Second)}
	agent := &Agent{cfg: Config{OTLP: receiver, AdapterTargets: []AdapterTarget{{Pack: job.Pack, Destination: job.Destination}}}}
	agent.acknowledgeOTLP(job, prefix)
	remaining, err := agent.collectAdapter(context.Background(), job)
	if err != nil || len(remaining)+len(prefix) != len(points) {
		t.Fatalf("lost remainder: %d %v", len(remaining), err)
	}
}

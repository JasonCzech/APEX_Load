package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/storage"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestCollectorSnapshotMetricPointsPreservesSourceAndQuality(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	snap := collectorMetricSnapshot{
		CollectorID: "collector", ProjectID: "project", ReceivedAt: now,
		Packs: []collectorMetricPack{
			{Pack: "prometheus", Timestamp: now, Samples: map[string]float64{"requests_total": 0, "latency_ms": 9}, SampleMetadata: map[string]collectorSampleMetadata{"requests_total": {Kind: analysis.KindCounter}, "latency_ms": {Kind: analysis.KindGauge}}},
			{Pack: "linux-hosts", Timestamp: now.Add(-2 * collectorStaleAfter), Samples: map[string]float64{"load_avg_1m": 1.5}},
		},
	}
	points, coverage := collectorSnapshotMetricPoints(snap, now)
	if coverage.Returned != 3 || coverage.Invalid != 0 || coverage.Omitted != 0 {
		t.Fatalf("coverage: %+v", coverage)
	}
	if len(points) != 3 || points[0].MetricName != "latency_ms" || points[1].MetricName != "requests_total" {
		t.Fatalf("unexpected ordered points: %+v", points)
	}
	if points[1].Kind != analysis.KindCounter || points[1].Value == nil || *points[1].Value != 0 {
		t.Fatalf("zero-valued counter was lost: %+v", points[1])
	}
	if points[2].Quality != analysis.QualityStale || points[2].Source != "linux-hosts" {
		t.Fatalf("stale source was misclassified: %+v", points[2])
	}
	for _, p := range points {
		if p.Dimensions["collector_id"] != "collector" {
			t.Fatal("collector identity lost")
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("invalid point %+v: %v", p, err)
		}
	}
}

func TestCollectorSnapshotMetricPointsBounded(t *testing.T) {
	now := time.Now().UTC()
	samples := map[string]float64{}
	for i := 0; i < 600; i++ {
		samples["metric_"+string(rune(0x1000+i))] = float64(i)
	}
	snap := collectorMetricSnapshot{ProjectID: "project", ReceivedAt: now, Packs: []collectorMetricPack{{Pack: "prometheus", Timestamp: now, Samples: samples}}}
	points, coverage := collectorSnapshotMetricPoints(snap, now)
	if coverage.Omitted != 88 || coverage.Returned != 512 {
		t.Fatalf("coverage: %+v", coverage)
	}
	if got := len(points); got != 512 {
		t.Fatalf("point preview count = %d, want 512", got)
	}
}

func TestCollectorPointsAgeAndPreserveDeclaredSemantics(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-2 * collectorStaleAfter)
	future := now.Add(time.Minute)
	for _, tc := range []struct {
		name      string
		received  time.Time
		timestamp *time.Time
		now       time.Time
		quality   string
	}{
		{"fresh", now, nil, now, analysis.QualityValid},
		{"no more heartbeats", now, nil, now.Add(2 * collectorStaleAfter), analysis.QualityStale},
		{"old source sample", now, &old, now, analysis.QualityStale},
		{"future source", now, &future, now, analysis.QualityClockSkewed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := collectorMetricSnapshot{CollectorID: "collector", ProjectID: "p", ReceivedAt: tc.received, Packs: []collectorMetricPack{{Pack: "prometheus", Timestamp: now, Samples: map[string]float64{"gauge_total": 1, "count": 2, "untyped_total": 3}, SampleMetadata: map[string]collectorSampleMetadata{"gauge_total": {Kind: analysis.KindGauge, Timestamp: tc.timestamp}, "count": {Kind: analysis.KindCounter, Timestamp: tc.timestamp}, "untyped_total": {Kind: analysis.KindUnknown, Timestamp: tc.timestamp}}}}}
			points, _ := collectorSnapshotMetricPoints(snap, tc.now)
			if len(points) != 3 || points[0].Kind != analysis.KindCounter || points[1].Kind != analysis.KindGauge || points[2].Kind != analysis.KindUnknown {
				t.Fatalf("semantics: %+v", points)
			}
			for _, p := range points {
				if p.Quality != tc.quality {
					t.Fatalf("quality: %+v", p)
				}
				if tc.timestamp != nil && !p.TimestampUTC.Equal(*tc.timestamp) {
					t.Fatal("source timestamp lost")
				}
			}
		})
	}
}

func TestCollectorPointCoverageReportsInvalidAndSkipped(t *testing.T) {
	now := time.Now().UTC()
	snap := collectorMetricSnapshot{CollectorID: "c", ProjectID: "p", ReceivedAt: now, Packs: []collectorMetricPack{{Pack: "prometheus", Timestamp: now, Samples: map[string]float64{strings.Repeat("a", 300): 1, "nonfinite": math.Inf(1), "legacy_total": 1}, SkippedSamples: 4}}}
	points, coverage := collectorSnapshotMetricPoints(snap, now)
	if len(points) != 1 || points[0].Kind != analysis.KindUnknown || coverage.Invalid != 2 || coverage.SourceSkipped != 4 {
		t.Fatalf("points=%+v coverage=%+v", points, coverage)
	}
}

func TestCollectorSampleMetadataValidation(t *testing.T) {
	for _, pack := range []collectorMetricPack{
		{SkippedSamples: -1}, {SkippedSamples: 1 << 21},
		{Samples: map[string]float64{"a": 1}, SampleMetadata: map[string]collectorSampleMetadata{"b": {Kind: "gauge"}}},
		{Samples: map[string]float64{"a": 1}, SampleMetadata: map[string]collectorSampleMetadata{"a": {Kind: "invented"}}},
		{Samples: map[string]float64{"a": 1}, SampleMetadata: map[string]collectorSampleMetadata{"a": {Kind: "gauge", Timestamp: &time.Time{}}}},
	} {
		if err := validateCollectorSampleMetadata(pack); err == nil {
			t.Fatalf("invalid metadata accepted: %+v", pack)
		}
	}
}

func TestCollectorHeartbeatMetadataRoundTripAndAtomicValidation(t *testing.T) {
	server, _ := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors", map[string]any{"project_id": "proj-a", "name": "typed", "allowed_metric_packs": []string{"prometheus"}})
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var created struct {
		Collector storage.MonitoringCollector `json:"collector"`
		Token     string                      `json:"token"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/monitoring/collectors/" + created.Collector.ID
	at := time.Now().UTC().Add(-5 * time.Minute)
	pack := collectorMetricPack{Pack: "prometheus", Timestamp: time.Now().UTC(), Samples: map[string]float64{"count": 0}, SampleMetadata: map[string]collectorSampleMetadata{"count": {Kind: analysis.KindCounter, Timestamp: &at}}, SkippedSamples: 2}
	rec := collectorRequest(t, server, http.MethodPost, path+"/heartbeat", created.Token, collectorHeartbeatRequest{Health: "healthy", Packs: []collectorMetricPack{pack}})
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []collectorHeartbeatRequest{
		{Packs: []collectorMetricPack{pack, pack}},
		{Packs: []collectorMetricPack{{Pack: "prometheus", Samples: map[string]float64{"count": 1}, SampleMetadata: map[string]collectorSampleMetadata{"count": {Kind: "invalid"}}}}},
	} {
		if rec := collectorRequest(t, server, http.MethodPost, path+"/heartbeat", created.Token, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid heartbeat: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec = timeslotRequest(t, server, http.MethodGet, path+"/metrics", nil)
	var result struct {
		Health   string                 `json:"health"`
		Points   []analysis.MetricPoint `json:"metric_points"`
		Coverage collectorPointCoverage `json:"metric_points_coverage"`
		Packs    []collectorMetricPack  `json:"packs"`
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Health != "degraded" || result.Coverage.SourceSkipped != 2 || len(result.Points) != 1 || len(result.Packs) != 1 {
		t.Fatalf("round trip: %+v", result)
	}
	p := result.Points[0]
	if p.Kind != analysis.KindCounter || p.Quality != analysis.QualityStale || !p.TimestampUTC.Equal(at) || p.Value == nil || *p.Value != 0 {
		t.Fatalf("point: %+v", p)
	}
	if metadata := result.Packs[0].SampleMetadata["count"]; metadata.Kind != analysis.KindCounter || metadata.Timestamp == nil || !metadata.Timestamp.Equal(at) {
		t.Fatalf("metadata: %+v", metadata)
	}
}

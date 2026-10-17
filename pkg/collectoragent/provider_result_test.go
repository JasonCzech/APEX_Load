package collectoragent

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

func TestProviderResultFailureRetainsEvidenceWithoutMeasurements(t *testing.T) {
	from := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	for _, tc := range []struct {
		name, key string
		job       RunJob
	}{
		{"generic", "collection", RunJob{}},
		{"dynatrace", "controller", RunJob{Pack: monitorprovider.PackDynatrace}},
		{"prometheus", "query", RunJob{Prometheus: &prometheus.JobConfig{Selection: prometheus.Selection{Queries: []prometheus.Query{{ID: "cpu"}}, Targets: true, Alerts: true, QueryOffset: 1, QueryTime: &from}}, Reconciling: true, TerminalWindows: []monitorprovider.EvaluationWindow{{QueryID: "cpu"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := completeProviderResult(tc.job, providerResult{}, errors.New("unavailable"), from, to)
			coverage := got.Coverage[tc.key]
			if len(got.Points) != 0 || len(got.Coverage) != 1 || coverage.Status != "unavailable" || coverage.From != from || coverage.To != to {
				t.Fatalf("failed read=%+v", got)
			}
			if tc.job.Prometheus != nil && (got.Progress.TotalQueries != 4 || got.Progress.NextQuery != 1 || got.Progress.NextTime != &from || got.Progress.Complete) {
				t.Fatalf("lost failure retry cursor: %+v", got.Progress)
			}
			if got := completeProviderResult(tc.job, providerResult{}, nil, from, to); len(got.Coverage) != 0 || len(got.Points) != 0 {
				t.Fatalf("empty read invented evidence: %+v", got)
			}
		})
	}
	result := providerResult{Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "permission_denied"}}, Progress: monitorprovider.QueryProgress{TotalQueries: 2, NextQuery: 1, Cursor: "retained-provider-cursor", ScanReservationIDs: []string{"reservation"}}, Evidence: []monitorprovider.Diagnostic{{ID: "capability", Kind: "capability"}}}
	got := completeProviderResult(RunJob{Prometheus: &prometheus.JobConfig{}}, result, errors.New("partial failure"), from, to)
	if !reflect.DeepEqual(got, result) {
		t.Fatalf("provider-owned evidence changed: %+v", got)
	}
}

func TestProviderAcknowledgementControlsContinuationCadence(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                  string
		reconcile, prometheus bool
		progress              *monitorprovider.QueryProgress
		delay                 time.Duration
	}{
		{"normal empty", false, false, nil, time.Minute},
		{"normal completed", false, false, &monitorprovider.QueryProgress{Complete: true}, time.Minute},
		{"continuation", false, false, &monitorprovider.QueryProgress{Cursor: "next"}, 0},
		{"terminal completed", true, true, &monitorprovider.QueryProgress{Complete: true}, 15 * time.Second},
		{"terminal historical empty", true, false, nil, 15 * time.Second},
		{"terminal captured-only prefix", true, true, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := RunJob{IntervalSeconds: 60, Reconciling: tc.reconcile}
			if tc.prometheus {
				job.Prometheus = &prometheus.JobConfig{}
			}
			got := providerNextCollectionTime(now, job, &RunSampleBatch{Progress: tc.progress})
			if !got.Equal(now.Add(tc.delay)) {
				t.Fatalf("next=%s, want delay %s", got, tc.delay)
			}
		})
	}
}

func TestProviderTerminalEmptyResultPublishesOnlyCompletedWatermark(t *testing.T) {
	from := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	for _, complete := range []bool{false, true} {
		result := providerResult{Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "no_data"}}, Progress: monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: complete}}
		batches, err := providerBatches(RunJob{NextSequence: 7, Reconciling: true, QueryFromUTC: from, QueryUntilUTC: to}, SignedRunJob{}, result, to)
		if err != nil {
			t.Fatal(err)
		}
		if len(batches) != 1 {
			t.Fatalf("empty result batches=%d", len(batches))
		}
		batch := batches[0]
		if len(batch.Points) != 0 || batch.Sequence != 7 || batch.Progress == nil || batch.Progress.Complete != complete || batch.Final != complete || (batch.QueryWindowEnd != nil) != complete {
			t.Fatalf("terminal empty batch=%+v", batch)
		}
		if complete && !batch.QueryWindowEnd.Equal(to) {
			t.Fatal("watermark changed")
		}
	}
}

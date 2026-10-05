package analysis

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func TestPublicationSharedProgressPreservesBothContinuationContracts(t *testing.T) {
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	progress := ProviderQueryProgress{NextQuery: 1, TotalQueries: 3, NextTime: &at,
		Cursor: "async-request", CursorExpiresAt: &at, ScannedBytes: 10,
		ScanReservationBytes: 20, ScanReservationIDs: []string{"attempt"},
		ScanSettlements: []ProviderScanSettlement{{AttemptID: "attempt", ScannedBytes: 10}}, QueryCostKnown: true,
	}
	body, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	var restored monitorprovider.QueryProgress
	if err = json.Unmarshal(body, &restored); err != nil || !reflect.DeepEqual(progress, restored) {
		t.Fatalf("provider continuation lost: %+v %v", restored, err)
	}
}

func TestPublicationRuleProviderDoesNotCrossEvaluate(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	point := prometheusGridPoint(start, 1)
	rule := MonitorRule{Provider: "dynatrace", SourceID: point.Source, Metric: point.MetricName,
		Unit: point.Unit, Statistic: "avg", Comparator: "gte", Threshold: 0, MinCoveragePct: 1}
	if got := EvaluateMonitorRule(rule, []MetricPoint{point}, start, start); got.Status != "not_evaluated" {
		t.Fatal(got)
	}
	if got := EvaluateMonitorRule(rule, []MetricPoint{point}, start, start.Add(15*time.Second)); got.Status != "not_evaluated" {
		t.Fatal("Dynatrace selector evaluated Prometheus evidence", got)
	}
}

package api

import (
	"fmt"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func TestDistributedTransactionResponseTimeSLAMergesCumulativeCountersDuplicateSafely(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.GoalConfig = &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(3000)}
	testRun.Groups = []TestGroup{{Name: "buyers", VUsers: 1}}
	row := func(total, missed int64, threshold float64) generatorrpc.TransactionResponseTimeSLACompliance {
		return generatorrpc.TransactionResponseTimeSLACompliance{Group: "buyers", Transaction: "Checkout", ThresholdMs: threshold, TotalCount: total, EligibleSuccessCount: total, MissedCount: missed}
	}

	first := distributedP99Batch(testRun.ID, 1000, 100)
	first.BatchSequence = 1
	first.TransactionResponseTimeSLA = []generatorrpc.TransactionResponseTimeSLACompliance{row(10, 1, 3000)}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", first) {
		t.Fatal("first batch rejected")
	}
	duplicate := first
	duplicate.TimestampUnixMs = 2000
	if backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", duplicate) {
		t.Fatal("duplicate cumulative batch accepted")
	}

	third := distributedP99Batch(testRun.ID, 3000, 100)
	third.BatchSequence = 3
	third.TransactionResponseTimeSLA = []generatorrpc.TransactionResponseTimeSLACompliance{row(30, 2, 3000)}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", third) {
		t.Fatal("sequence-gap batch rejected")
	}
	secondTarget := distributedP99Batch(testRun.ID, 3000, 100)
	secondTarget.BatchSequence = 1
	secondTarget.TransactionResponseTimeSLA = []generatorrpc.TransactionResponseTimeSLACompliance{row(5, 1, 3000)}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-b", secondTarget) {
		t.Fatal("second target batch rejected")
	}
	results := remoteTransactionResponseTimeSLAResults(testRun)
	if len(results) != 1 || results[0].TotalCount != 35 || results[0].MissedCount != 3 {
		t.Fatalf("merged results = %#v, want cumulative 35/3 without duplicate or gap loss", results)
	}

	conflict := distributedP99Batch(testRun.ID, 4000, 100)
	conflict.BatchSequence = 2
	conflict.TransactionResponseTimeSLA = []generatorrpc.TransactionResponseTimeSLACompliance{row(999, 999, 2999)}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-b", conflict) {
		t.Fatal("conflict batch rejected at transport layer")
	}
	results = remoteTransactionResponseTimeSLAResults(testRun)
	if len(results) != 1 || results[0].TotalCount != 30 {
		t.Fatalf("threshold conflict was merged: %#v", results)
	}
}

func TestDistributedTransactionResponseTimeSLAMergesLegacyAndCumulativeTargets(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.GoalConfig = &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(3000)}
	testRun.Groups = []TestGroup{{Name: "buyers", VUsers: 1}}

	legacy := distributedP99Batch(testRun.ID, 1000, 100)
	legacy.BatchSequence = 1
	legacy.Transactions = []generatorrpc.TransactionMetric{
		{Group: "buyers", Name: "Checkout", DurationMs: 3500, Success: true},
		{Group: "buyers", Name: "Checkout", DurationMs: 2000, Success: true},
	}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "legacy-generator", legacy) {
		t.Fatal("legacy batch rejected")
	}

	upgraded := distributedP99Batch(testRun.ID, 1100, 100)
	upgraded.BatchSequence = 1
	upgraded.TransactionResponseTimeSLA = []generatorrpc.TransactionResponseTimeSLACompliance{{
		Group: "buyers", Transaction: "Checkout", ThresholdMs: 3000,
		TotalCount: 10, EligibleSuccessCount: 9, ExcludedErrorCount: 1, MissedCount: 2,
	}}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "upgraded-generator", upgraded) {
		t.Fatal("upgraded batch rejected")
	}

	results := remoteTransactionResponseTimeSLAResults(testRun)
	if len(results) != 1 || results[0].TotalCount != 12 || results[0].EligibleSuccessCount != 11 || results[0].ExcludedErrorCount != 1 || results[0].MissedCount != 3 {
		t.Fatalf("mixed-fleet results = %#v, want cumulative 12/11/1/3", results)
	}

	legacyNext := distributedP99Batch(testRun.ID, 2000, 100)
	legacyNext.BatchSequence = 2
	legacyNext.Transactions = []generatorrpc.TransactionMetric{{Group: "buyers", Name: "Checkout", DurationMs: 1000, Success: true}}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "legacy-generator", legacyNext) {
		t.Fatal("second legacy batch rejected")
	}
	results = remoteTransactionResponseTimeSLAResults(testRun)
	if len(results) != 1 || results[0].TotalCount != 13 || results[0].MissedCount != 3 {
		t.Fatalf("mixed-fleet update = %#v, want cumulative 13/3", results)
	}
}

func TestDistributedTransactionResponseTimeSLACardinalityIsBounded(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.GoalConfig = &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(100)}
	rows := make([]generatorrpc.TransactionResponseTimeSLACompliance, 0, maxDistributedMetricSeries+20)
	for index := 0; index < maxDistributedMetricSeries+20; index++ {
		rows = append(rows, generatorrpc.TransactionResponseTimeSLACompliance{Group: "buyers", Transaction: fmt.Sprintf("tx-%04d", index), ThresholdMs: 100, TotalCount: 1, EligibleSuccessCount: 1})
	}
	batch := distributedP99Batch(testRun.ID, 1000, 10)
	batch.BatchSequence = 1
	batch.TransactionResponseTimeSLA = rows
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", batch) {
		t.Fatal("batch rejected")
	}
	if got := len(remoteTransactionResponseTimeSLAResults(testRun)); got != maxDistributedMetricSeries {
		t.Fatalf("SLA dimensions = %d, want bound %d", got, maxDistributedMetricSeries)
	}
}

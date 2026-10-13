package generatorrpc

import (
	"fmt"
	"testing"
)

// A generator reports each group's transactions in the window, merges a
// group's shares from its executions, and sends them on the wire.
func TestGroupTransactionSharesAreCountedMergedAndSent(t *testing.T) {
	window := newTransactionWindowAggregate()
	for _, tx := range []TransactionMetric{
		{Name: "Login", Group: "buyers", Success: true, DurationMs: 10},
		{Name: "Login", Group: "buyers", Success: false, DurationMs: 30},
		{Name: "Search", Group: "buyers", Success: true, DurationMs: 20},
		{Name: "Login", Group: "sellers", Success: true, DurationMs: 15},
		{Name: "Login", Success: true, DurationMs: 5},
	} {
		window.add(tx)
	}
	set := window.finalize(1000)
	buyers, sellers := findGroupTransactionMetrics(set.Groups, "buyers"), findGroupTransactionMetrics(set.Groups, "sellers")
	if len(buyers) != 2 || buyers[0].Name != "Login" || buyers[0].Count != 2 || buyers[0].Errors != 1 || buyers[1].Name != "Search" {
		t.Fatalf("buyers' transactions = %+v", buyers)
	}
	if len(sellers) != 1 || sellers[0].Count != 1 {
		t.Fatalf("sellers' transactions = %+v", sellers)
	}

	// Two executions of the group merge their shares by name.
	merged := mergeGroupMetricSeries(append(append([]GroupMetric(nil), set.Groups...), set.Groups...))
	if shares := findGroupTransactionMetrics(merged, "buyers"); len(shares) != 2 || shares[0].Count != 4 || shares[0].Errors != 2 {
		t.Fatalf("merged buyers' transactions = %+v", shares)
	}

	// On the wire the shares go without digests (the run-wide transactions
	// carry those), and the batch says its groups carry their transactions.
	if findGroupTransactionMetrics(set.Groups, "buyers")[0].DurationDigest == nil {
		t.Fatal("the generator's own shares lost their digests")
	}
	batch := (&Service{}).metricsBatchForMetrics("run", 1, GeneratorStats{}, nil, set, false, map[string]int32{"buyers": 1, "sellers": 1}, "RUNNING", "", nil)
	if batch.GetGroupTransactionsVersion() != CurrentGroupTransactionsVersion {
		t.Fatalf("group transactions version = %d", batch.GetGroupTransactionsVersion())
	}
	for _, group := range groupMetricsFromProto(batch.GetGroups()) {
		want := map[string]int{"buyers": 2, "sellers": 1}[group.Name]
		if len(group.TransactionMetrics) != want {
			t.Fatalf("%s sent %d transactions, want %d", group.Name, len(group.TransactionMetrics), want)
		}
		for _, share := range group.TransactionMetrics {
			if share.DurationDigest != nil || share.P50Ms <= 0 {
				t.Fatalf("%s sent %+v, want percentiles without a digest", group.Name, share)
			}
		}
	}
}

// A window's group-transaction series are bounded across groups, and a
// group's share is named as the transaction is counted (bounded) run-wide.
func TestGroupTransactionSeriesAreBounded(t *testing.T) {
	window := newTransactionWindowAggregate()
	for group := 0; group < 8; group++ {
		for name := 0; name < maxTransactionWindowSeries; name++ {
			window.add(TransactionMetric{Name: fmt.Sprintf("tx-%d", name), Group: fmt.Sprintf("group-%d", group), Success: true, DurationMs: 1})
		}
	}
	series := 0
	for _, transactions := range window.groupTransactions {
		series += len(transactions)
		for name := range transactions {
			if name != metricOverflowSeries && window.transactions[name] == nil {
				t.Fatalf("group share %q is not a run-wide transaction name", name)
			}
		}
	}
	if series > maxGroupTransactionWindowSeries+len(window.groupTransactions) || series != window.groupTransactionSeries {
		t.Fatalf("group-transaction series = %d (counted %d), want at most %d", series, window.groupTransactionSeries, maxGroupTransactionWindowSeries+len(window.groupTransactions))
	}
	if window.groupTransactions["group-7"][metricOverflowSeries] == nil {
		t.Fatal("the last group's series past the bound did not fold into its overflow series")
	}
}

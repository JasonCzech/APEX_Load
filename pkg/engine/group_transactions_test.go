package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Each group's share of a transaction is counted beside the transaction's
// run-wide figures, named as the transaction is.
func TestComputeSnapshotCountsEachGroupsShareOfATransaction(t *testing.T) {
	mc := NewMetricsCollector(100)
	results := []*protocol.Result{
		{BusinessTransaction: true, Success: true, Group: "buyers", ActionName: "Login", Duration: 10 * time.Millisecond},
		{BusinessTransaction: true, Success: false, Group: "sellers", ActionName: "Login", Duration: 30 * time.Millisecond},
		{BusinessTransaction: true, Success: true, Group: "sellers", ActionName: "Search", Duration: 20 * time.Millisecond},
		// Requests and ungrouped transactions have no group share.
		{Success: true, Group: "buyers", ActionName: "GET /", Duration: time.Millisecond},
		{BusinessTransaction: true, Success: true, ActionName: "Login", Duration: time.Millisecond},
	}
	snap := mc.computeSnapshot(results, time.Second)
	if login := snap.ByTransaction["Login"]; login == nil || login.Count != 3 {
		t.Fatalf("Login = %+v, want 3 attempts", login)
	}
	buyers, sellers := snap.ByGroupTransaction["buyers"], snap.ByGroupTransaction["sellers"]
	if len(buyers) != 1 || buyers["Login"] == nil || buyers["Login"].Count != 1 || buyers["Login"].P50 != 10*time.Millisecond {
		t.Fatalf("buyers' transactions = %+v", buyers)
	}
	if len(sellers) != 2 || sellers["Login"].Errors != 1 || sellers["Search"].Count != 1 || sellers["Search"].AvgDuration != 20*time.Millisecond {
		t.Fatalf("sellers' transactions = %+v", sellers)
	}
	if empty := mc.computeSnapshot(nil, time.Second); empty.ByGroupTransaction != nil {
		t.Fatalf("an empty window's group shares = %+v, want none", empty.ByGroupTransaction)
	}
}

// A window's group-transaction pairs are bounded across groups: past the
// bound a group's new transactions fold into its overflow entry.
func TestComputeSnapshotBoundsGroupTransactionPairs(t *testing.T) {
	mc := NewMetricsCollector(100)
	results := make([]*protocol.Result, 0, 6*400)
	for group := 0; group < 6; group++ {
		for name := 0; name < 400; name++ {
			results = append(results, &protocol.Result{BusinessTransaction: true, Success: true, Group: fmt.Sprintf("group-%d", group), ActionName: fmt.Sprintf("tx-%d", name), Duration: time.Millisecond})
		}
	}
	snap := mc.computeSnapshot(results, time.Second)
	pairs, attempts := 0, int64(0)
	for _, transactions := range snap.ByGroupTransaction {
		pairs += len(transactions)
		for _, share := range transactions {
			attempts += share.Count
		}
	}
	if pairs > maxGroupTransactionEntries+len(snap.ByGroupTransaction) || attempts != int64(len(results)) {
		t.Fatalf("pairs = %d, attempts = %d; want at most %d pairs and every attempt counted", pairs, attempts, maxGroupTransactionEntries+len(snap.ByGroupTransaction))
	}
	if snap.ByGroupTransaction["group-5"][metricOverflowLabel] == nil {
		t.Fatal("the last group's transactions past the bound did not fold into its overflow entry")
	}
}

package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// Generators' per-group shares merge by group and name across the fleet,
// and each transaction says which groups ran it; a transaction more than one
// group ran carries each group's share.
func TestDistributedTransactionsAreAttributedToTheirGroups(t *testing.T) {
	run := &ManagedTest{ID: "group-transactions", Groups: []TestGroup{{Name: "buyers"}, {Name: "sellers"}}}
	login := func(count, errors int64) generatorrpc.MetricAggregate {
		return generatorrpc.MetricAggregate{Name: "Login", Count: count, Errors: errors, AvgMs: 20, P99Ms: 40}
	}
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a": {
			WindowMs:                 1000,
			GroupTransactionsVersion: generatorrpc.CurrentGroupTransactionsVersion,
			FleetMetrics:             &generatorrpc.MetricAggregate{Name: "fleet", Count: 5},
			TransactionMetrics:       []generatorrpc.MetricAggregate{login(3, 1), {Name: "Search", Count: 2}},
			Groups: []generatorrpc.GroupMetric{
				{Name: "buyers", Metrics: &generatorrpc.MetricAggregate{Count: 5}, TransactionMetrics: []generatorrpc.MetricAggregate{login(3, 1), {Name: "Search", Count: 2}}},
			},
		},
		"gen-b": {
			WindowMs:                 1000,
			GroupTransactionsVersion: generatorrpc.CurrentGroupTransactionsVersion,
			FleetMetrics:             &generatorrpc.MetricAggregate{Name: "fleet", Count: 6},
			TransactionMetrics:       []generatorrpc.MetricAggregate{login(6, 0)},
			Groups: []generatorrpc.GroupMetric{
				{Name: "buyers", Metrics: &generatorrpc.MetricAggregate{Count: 2}, TransactionMetrics: []generatorrpc.MetricAggregate{login(2, 0)}},
				{Name: "sellers", Metrics: &generatorrpc.MetricAggregate{Count: 4}, TransactionMetrics: []generatorrpc.MetricAggregate{login(4, 0)}},
			},
		},
	}
	aggregated, fleetTPS, groupTPS := aggregateDistributedMetricsBatches(run.ID, batches)
	live := buildLiveMetricsFromDistributedBatch(run, aggregated, fleetTPS, groupTPS)

	loginTx := live.Transactions["Login"]
	if loginTx == nil || loginTx.Count != 9 || !reflect.DeepEqual(loginTx.Groups, []string{"buyers", "sellers"}) {
		t.Fatalf("Login = %+v, want 9 attempts run by buyers and sellers", loginTx)
	}
	buyers, sellers := loginTx.ByGroup["buyers"], loginTx.ByGroup["sellers"]
	if buyers == nil || buyers.Count != 5 || buyers.Errors != 1 || buyers.TPS != 4 || sellers == nil || sellers.Count != 4 || sellers.TPS != 4 {
		t.Fatalf("Login shares = buyers %+v, sellers %+v; want 5 (1 error, 4/s) and 4 (4/s)", buyers, sellers)
	}
	search := live.Transactions["Search"]
	if search == nil || !reflect.DeepEqual(search.Groups, []string{"buyers"}) || search.ByGroup != nil {
		t.Fatalf("Search = %+v, want run by buyers alone, with no split", search)
	}

	// One generator in the window that predates group shares leaves every
	// transaction unattributed: its share of Login belongs to no group, so
	// the others' shares would not add up to Login.
	mixed := map[string]generatorrpc.MetricsBatch{"gen-a": batches["gen-a"], "gen-b": {
		WindowMs:           1000,
		FleetMetrics:       &generatorrpc.MetricAggregate{Name: "fleet", Count: 4},
		TransactionMetrics: []generatorrpc.MetricAggregate{login(4, 0)},
		Groups:             []generatorrpc.GroupMetric{{Name: "sellers", Metrics: &generatorrpc.MetricAggregate{Count: 4}}},
	}}
	aggregated, fleetTPS, groupTPS = aggregateDistributedMetricsBatches(run.ID, mixed)
	live = buildLiveMetricsFromDistributedBatch(run, aggregated, fleetTPS, groupTPS)
	if tx := live.Transactions["Login"]; tx == nil || tx.Count != 7 || tx.Groups != nil || tx.ByGroup != nil {
		t.Fatalf("mixed fleet's Login = %+v, want 7 attempts, unattributed", tx)
	}
}

// A local run's engine counts each group's share of a transaction.
func TestLocalTransactionsAreAttributedToTheirGroups(t *testing.T) {
	share := func(count, errors int64) *engine.ActionMetrics {
		return &engine.ActionMetrics{Name: "Login", Count: count, Errors: errors, AvgDuration: 10 * time.Millisecond}
	}
	snap := engine.MetricsSnapshot{
		Window:        2 * time.Second,
		ByTransaction: map[string]*engine.ActionMetrics{"Login": share(5, 1), "Search": {Name: "Search", Count: 2}},
		ByGroup:       map[string]*engine.GroupMetrics{"buyers": {Name: "buyers"}, "sellers": {Name: "sellers"}},
		ByGroupTransaction: map[string]map[string]*engine.ActionMetrics{
			"buyers":  {"Login": share(3, 1), "Search": {Name: "Search", Count: 2}},
			"sellers": {"Login": share(2, 0)},
		},
	}
	live := buildLiveMetricsFromSnapshot(snap, nil)
	login := live.Transactions["Login"]
	if login == nil || !reflect.DeepEqual(login.Groups, []string{"buyers", "sellers"}) || login.ByGroup["buyers"].Count != 3 || login.ByGroup["buyers"].TPS != 1 || login.ByGroup["sellers"].TPS != 1 {
		t.Fatalf("Login = %+v (shares %+v)", login, login.ByGroup)
	}
	if search := live.Transactions["Search"]; search == nil || !reflect.DeepEqual(search.Groups, []string{"buyers"}) || search.ByGroup != nil {
		t.Fatalf("Search = %+v, want buyers alone", search)
	}
	encoded, err := json.Marshal(login)
	if err != nil || !strings.Contains(string(encoded), `"groups":["buyers","sellers"]`) || !strings.Contains(string(encoded), `"by_group":{`) {
		t.Fatalf("Login JSON = %s, %v", encoded, err)
	}
	// Cloned live metrics do not share a transaction's attribution.
	cloned := cloneTxMetricsMap(live.Transactions)
	cloned["Login"].Groups[0] = "changed"
	cloned["Login"].ByGroup["buyers"].Count = 99
	if login.Groups[0] != "buyers" || login.ByGroup["buyers"].Count != 3 {
		t.Fatal("a clone shares the original's attribution")
	}
}

// A selected-group response keeps the transactions whose figures are
// exactly the selected groups'.
func TestGroupFilterKeepsTheSelectedGroupsTransactions(t *testing.T) {
	live := liveMetricsForGroupFilterTest()
	live.Transactions = map[string]*TxMetrics{
		"CheckoutAction": {Name: "CheckoutAction", Count: 5, Errors: 1, Groups: []string{"Checkout"}},
		"BrowseAction":   {Name: "BrowseAction", Count: 3, Groups: []string{"Browse"}},
		"Login": {Name: "Login", Count: 7, Groups: []string{"Browse", "Checkout"}, ByGroup: map[string]*TransactionGroupShare{
			"Browse":   {Count: 4},
			"Checkout": {Count: 3, Errors: 1},
		}},
		"Unattributed": {Name: "Unattributed", Count: 2},
	}
	filtered := filterLiveMetricsForGroups(live, groupFilterQuery{Names: []string{"Checkout"}, Explicit: true})
	if len(filtered.Transactions) != 2 || filtered.Transactions["CheckoutAction"].Count != 5 {
		t.Fatalf("Checkout's transactions = %#v, want CheckoutAction and its share of Login", filtered.Transactions)
	}
	if login := filtered.Transactions["Login"]; login.Count != 3 || login.Errors != 1 || !reflect.DeepEqual(login.Groups, []string{"Checkout"}) {
		t.Fatalf("Checkout's Login = %+v, want its own share", login)
	}
	if live.Transactions["Login"].Count != 7 || !reflect.DeepEqual(live.Transactions["Login"].Groups, []string{"Browse", "Checkout"}) {
		t.Fatal("filtering changed the live transactions")
	}
	// Both groups selected: every attributed transaction, whole.
	both := filterLiveMetricsForGroups(live, groupFilterQuery{Names: []string{"Checkout", "Browse"}, Explicit: true})
	if len(both.Transactions) != 3 || both.Transactions["Login"].Count != 7 {
		t.Fatalf("both groups' transactions = %#v", both.Transactions)
	}
	// Two selected groups sharing one with a hidden group cannot be split exactly.
	live.Transactions["Login"].Groups = []string{"Browse", "Checkout", "Search"}
	live.Transactions["Login"].ByGroup["Search"] = &TransactionGroupShare{Count: 1}
	if partial := filterLiveMetricsForGroups(live, groupFilterQuery{Names: []string{"Checkout", "Browse"}, Explicit: true}); partial.Transactions["Login"] != nil {
		t.Fatalf("partially selected Login = %+v, want left out", partial.Transactions["Login"])
	}
}

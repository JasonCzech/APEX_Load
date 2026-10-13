package api

import (
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// attributeTransactionsToGroups records on each of a window's transactions
// the groups whose VUsers ran it and, when more than one did, each group's
// share. byGroup holds each group's shares in the window by transaction,
// named as the window's transactions are. A transaction no group reports
// stays unattributed, as one from a generator that predates group shares
// does.
func attributeTransactionsToGroups(transactions map[string]*TxMetrics, byGroup map[string]map[string]*TransactionGroupShare) {
	if len(transactions) == 0 || len(byGroup) == 0 {
		return
	}
	shares := make(map[string]map[string]*TransactionGroupShare)
	for group, groupShares := range byGroup {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		for name, share := range groupShares {
			if share == nil || share.Count <= 0 || transactions[name] == nil {
				continue
			}
			if shares[name] == nil {
				shares[name] = make(map[string]*TransactionGroupShare)
			}
			shares[name][group] = share
		}
	}
	for name, groups := range shares {
		tx := transactions[name]
		tx.Groups = make([]string, 0, len(groups))
		for group := range groups {
			tx.Groups = append(tx.Groups, group)
		}
		sort.Strings(tx.Groups)
		if len(groups) > 1 {
			tx.ByGroup = groups
		}
	}
}

// groupTransactionShareFromAction is one group's share of a transaction in a
// local window.
func groupTransactionShareFromAction(am *engine.ActionMetrics, window time.Duration) *TransactionGroupShare {
	if am == nil || am.Count <= 0 {
		return nil
	}
	tps := 0.0
	if window > 0 {
		tps = float64(successfulMetricCount(am.Count, am.Errors)) / window.Seconds()
	}
	return &TransactionGroupShare{
		TPS:    tps,
		Count:  am.Count,
		Errors: boundedMetricErrors(am.Count, am.Errors),
		AvgMs:  float64(am.AvgDuration.Microseconds()) / 1000.0,
		P50Ms:  float64(am.P50.Microseconds()) / 1000.0,
		P90Ms:  float64(am.P90.Microseconds()) / 1000.0,
		P95Ms:  float64(am.P95.Microseconds()) / 1000.0,
		P99Ms:  float64(am.P99.Microseconds()) / 1000.0,
	}
}

// groupTransactionShareFromAggregate is one group's share of a transaction
// across the fleet's generators.
func groupTransactionShareFromAggregate(aggregate generatorrpc.MetricAggregate) *TransactionGroupShare {
	if strings.TrimSpace(aggregate.Name) == "" || aggregate.Count <= 0 {
		return nil
	}
	return &TransactionGroupShare{
		TPS:    aggregate.TPS,
		Count:  aggregate.Count,
		Errors: boundedMetricErrors(aggregate.Count, aggregate.Errors),
		AvgMs:  aggregate.AvgMs,
		P50Ms:  aggregate.P50Ms,
		P90Ms:  aggregate.P90Ms,
		P95Ms:  aggregate.P95Ms,
		P99Ms:  aggregate.P99Ms,
	}
}

package api

import (
	"fmt"
	"sort"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A browser session that keeps its context runs its first iteration cold and
// the rest warm; the engine tags each transaction with which
// (protocol.BrowserIterationDimension). The run report shows each
// transaction's cold and warm times side by side, since SLAs judge the warm
// ones by default.

// maxBrowserIterationTransactions bounds the transactions the report splits.
const maxBrowserIterationTransactions = 100

// BrowserIterationTimes is one transaction's passed times in browser
// iterations of one kind, and how many failed.
type BrowserIterationTimes struct {
	Count  int64   `json:"count"`
	Errors int64   `json:"errors,omitempty"`
	AvgMs  float64 `json:"avg_ms"`
	P90Ms  float64 `json:"p90_ms"`
}

// BrowserIterationTransaction is one transaction's times in cold and in warm
// browser iterations.
type BrowserIterationTransaction struct {
	Transaction string                 `json:"transaction"`
	Cold        *BrowserIterationTimes `json:"cold,omitempty"`
	Warm        *BrowserIterationTimes `json:"warm,omitempty"`
}

type browserIterationTimesAccumulator struct {
	passed int64
	errors int64
	sumMs  float64
	digest *engine.DurationSketch
}

func (a *browserIterationTimesAccumulator) result() *BrowserIterationTimes {
	if a == nil {
		return nil
	}
	times := &BrowserIterationTimes{Count: a.passed, Errors: a.errors}
	if a.passed > 0 {
		times.AvgMs = a.sumMs / float64(a.passed)
		times.P90Ms = a.digest.Quantile(0.90)
	}
	return times
}

type archiveBrowserIterationAccumulator struct {
	transactions map[string]*[2]*browserIterationTimesAccumulator
}

func newArchiveBrowserIterationAccumulator() *archiveBrowserIterationAccumulator {
	return &archiveBrowserIterationAccumulator{transactions: map[string]*[2]*browserIterationTimesAccumulator{}}
}

func (a *archiveBrowserIterationAccumulator) merge(tx TransactionEvent) {
	slot := 0
	switch tx.PropertyDimensions[protocol.BrowserIterationDimension] {
	case protocol.IterationCold:
	case protocol.IterationWarm:
		slot = 1
	default:
		return
	}
	if archiveEventPopulation(tx) != "transaction" || tx.ActionName == "" {
		return
	}
	pair := a.transactions[tx.ActionName]
	if pair == nil {
		if len(a.transactions) >= maxBrowserIterationTransactions {
			return
		}
		pair = &[2]*browserIterationTimesAccumulator{}
		a.transactions[tx.ActionName] = pair
	}
	times := pair[slot]
	if times == nil {
		times = &browserIterationTimesAccumulator{digest: engine.NewDurationSketch()}
		pair[slot] = times
	}
	if !tx.Success {
		times.errors++
		return
	}
	times.passed++
	times.sumMs += tx.DurationMs
	times.digest.Add(tx.DurationMs)
}

// browserIterationTimesText is one kind's times for the PDF report.
func browserIterationTimesText(times *BrowserIterationTimes) string {
	if times == nil {
		return "none"
	}
	if times.Count == 0 {
		return fmt.Sprintf("%d failed", times.Errors)
	}
	return fmt.Sprintf("%d passed, avg %.1f ms, p90 %.1f ms, %d failed", times.Count, times.AvgMs, times.P90Ms, times.Errors)
}

// result is each split transaction's cold and warm times, by name.
func (a *archiveBrowserIterationAccumulator) result() []BrowserIterationTransaction {
	if len(a.transactions) == 0 {
		return nil
	}
	out := make([]BrowserIterationTransaction, 0, len(a.transactions))
	for name, pair := range a.transactions {
		out = append(out, BrowserIterationTransaction{Transaction: name, Cold: pair[0].result(), Warm: pair[1].result()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Transaction < out[j].Transaction })
	return out
}

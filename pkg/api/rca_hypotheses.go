package api

import (
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

// Bounds on the network-phase series one archive contributes.
const (
	maxPhaseDependencies    = 64
	maxProviderSeriesNodes  = 16
	limitationPhaseEvidence = "network_phase_evidence_cut"
)

// networkPhases are the archived request timings that can slow a call
// before the dependency answers.
var networkPhases = []struct {
	suffix, kind string
	value        func(*TransactionEvent) float64
}{
	{"dns_ms", signatures.KindDNS, func(tx *TransactionEvent) float64 { return tx.DNSMs }},
	{"connect_ms", signatures.KindConnect, func(tx *TransactionEvent) float64 { return tx.ConnectMs }},
	{"tls_ms", signatures.KindTLS, func(tx *TransactionEvent) float64 { return tx.TLSMs }},
}

// networkPhaseCollector averages each dependency's DNS, connect and TLS
// times per archived interval, over the requests that spent any; an
// interval where every connection was reused is a gap.
type networkPhaseCollector struct {
	windows      []intervalWindow
	dependencies map[string]*phaseSums
	file         string
	truncated    bool
}

type phaseSums struct {
	sums   [3][]float64
	counts [3][]int
}

func newNetworkPhaseCollector(summaries []IntervalSummaryRecord, file string) *networkPhaseCollector {
	ordered := slices.Clone(summaries)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Timestamp.Before(ordered[j].Timestamp) })
	return &networkPhaseCollector{windows: archiveIntervalWindows(ordered), dependencies: map[string]*phaseSums{}, file: file}
}

func (c *networkPhaseCollector) add(tx *TransactionEvent) {
	if tx.DependencyTarget == "" || tx.DNSMs <= 0 && tx.ConnectMs <= 0 && tx.TLSMs <= 0 {
		return
	}
	at := tx.Timestamp.UTC()
	index := sort.Search(len(c.windows), func(i int) bool { return !c.windows[i].end.Before(at) })
	if index == len(c.windows) || !at.After(c.windows[index].start) {
		return
	}
	label := rca.DependencyLabel(tx.DependencyType, tx.DependencyTarget)
	sums := c.dependencies[label]
	if sums == nil {
		if len(c.dependencies) >= maxPhaseDependencies {
			c.truncated = true
			return
		}
		sums = &phaseSums{}
		for i := range networkPhases {
			sums.sums[i], sums.counts[i] = make([]float64, len(c.windows)), make([]int, len(c.windows))
		}
		c.dependencies[label] = sums
	}
	for i, phase := range networkPhases {
		if value := phase.value(tx); value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			sums.sums[i][index] += value
			sums.counts[i][index]++
		}
	}
}

// series returns one series per dependency and phase that saw any timing.
func (c *networkPhaseCollector) series() []rca.Series {
	var out []rca.Series
	for _, label := range slices.Sorted(maps.Keys(c.dependencies)) {
		sums := c.dependencies[label]
		for i, phase := range networkPhases {
			s := rca.Series{
				ID: rca.NodeDependency + "/" + label + "/" + phase.suffix, Kind: rca.SeriesLatency, Unit: "ms", Observation: rca.ObservationInterval,
				Measures: phase.kind, Nodes: []string{rca.DependencyNodeID(label)},
				Evidence: rca.EvidenceRef{Kind: "archive_row", Locator: c.file + "#dependency=" + label},
			}
			seen := false
			for w, window := range c.windows {
				point := rca.SeriesPoint{At: window.start}
				if n := sums.counts[i][w]; n > 0 {
					point.Value, point.Valid, seen = sums.sums[i][w]/float64(n), true, true
				}
				s.Points = append(s.Points, point)
			}
			if seen {
				if len(c.windows) > 0 {
					s.Bucket = c.windows[0].end.Sub(c.windows[0].start)
				}
				out = append(out, s)
			}
		}
	}
	return out
}

// pointNodes are the graph nodes a provider point measures: the entities it
// names and the infrastructure in its dimensions.
func pointNodes(point analysis.MetricPoint) []string {
	var nodes []string
	if point.Provider != nil {
		for _, key := range slices.Sorted(maps.Keys(point.Provider.EntityIDs)) {
			if id := monitorEntityKey(point.Provider.Name, key, point.Provider.EntityIDs[key]); id != "" {
				nodes = append(nodes, rca.EntityNodeID(point.Provider.Name, id))
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(point.Dimensions)) {
		kind, ok := infrastructureDimensions[strings.ToLower(key)]
		if !ok {
			continue
		}
		value := strings.TrimSpace(point.Dimensions[key])
		if kind == "host" {
			value = rca.DependencyHost(value)
		}
		if value != "" {
			nodes = append(nodes, rca.InfrastructureNodeID(kind, value))
		}
	}
	if len(nodes) > maxProviderSeriesNodes {
		nodes = nodes[:maxProviderSeriesNodes]
	}
	return nodes
}

// addSeriesNodes merges a later point's nodes into a series, within bounds.
func addSeriesNodes(nodes []string, more []string) []string {
	for _, n := range more {
		if len(nodes) >= maxProviderSeriesNodes {
			break
		}
		if !slices.Contains(nodes, n) {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

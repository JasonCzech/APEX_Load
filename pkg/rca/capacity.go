package rca

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

// Capacity parameters, recalibrated by the evaluation corpus.
const (
	// KneeSlopeRatio: the knee is where p95's slope against offered load at
	// least triples.
	KneeSlopeRatio = 3.0
	// MinKneeLevels qualified stable levels are needed for a fit.
	MinKneeLevels = 3
	// LittlesFlatFraction: a window's queue depth is flat when its trend
	// moves it by at most this share of its mean (or one message).
	LittlesFlatFraction = 0.10
	// LittlesMinPoints of each series a window needs.
	LittlesMinPoints = 3
	// MaxCapacityResults bounds each list.
	MaxCapacityResults = 200
)

// Load units of a knee.
const (
	LoadUnitRate = "rate"
)

// CapacityLevel is one qualified stable level's offered load and the
// series' median p95 there.
type CapacityLevel struct {
	Load  float64 `json:"load"`
	P95Ms float64 `json:"p95_ms"`
}

// Capacity is one transaction's latency against offered load. Knee is the
// load where the slope at least tripled, absent when it never did.
type Capacity struct {
	Series      string          `json:"series"`
	LoadUnit    string          `json:"load_unit"`
	Levels      []CapacityLevel `json:"levels"`
	Knee        *float64        `json:"knee,omitempty"`
	SlopeBefore float64         `json:"slope_before"`
	SlopeAfter  float64         `json:"slope_after"`
	Reason      string          `json:"reason,omitempty"`
}

// KneeFit fits two lines to p95 against load, joined at an interior level,
// choosing the join with the least squared error; the join is the knee when
// the second slope is positive, at least triples the first and raises p95
// by more than floorMs.
func KneeFit(levels []CapacityLevel, floorMs float64) (*float64, float64, float64, string) {
	points := append([]CapacityLevel(nil), levels...)
	sort.Slice(points, func(i, j int) bool { return points[i].Load < points[j].Load })
	if len(points) < MinKneeLevels {
		return nil, 0, 0, fmt.Sprintf("fewer than %d qualified load levels", MinKneeLevels)
	}
	best, bestError := -1, math.Inf(1)
	var before, after float64
	for b := 1; b < len(points)-1; b++ {
		s1, e1 := fitLine(points[:b+1])
		s2, e2 := fitLine(points[b:])
		if e1+e2 < bestError-1e-12 {
			best, bestError, before, after = b, e1+e2, s1, s2
		}
	}
	rise := points[len(points)-1].P95Ms - points[best].P95Ms
	if after <= 0 || after < KneeSlopeRatio*math.Max(before, 0) || rise <= floorMs {
		return nil, before, after, "p95 does not steepen threefold across the levels"
	}
	knee := points[best].Load
	return &knee, before, after, ""
}

// fitLine is the least-squares slope through the points and its squared
// error.
func fitLine(points []CapacityLevel) (float64, float64) {
	n := float64(len(points))
	var sx, sy, sxx, sxy float64
	for _, p := range points {
		sx, sy, sxx, sxy = sx+p.Load, sy+p.P95Ms, sxx+p.Load*p.Load, sxy+p.Load*p.P95Ms
	}
	denominator := n*sxx - sx*sx
	if denominator == 0 {
		return 0, 0
	}
	slope := (n*sxy - sx*sy) / denominator
	intercept := (sy - slope*sx) / n
	var squared float64
	for _, p := range points {
		residual := p.P95Ms - (intercept + slope*p.Load)
		squared += residual * residual
	}
	return slope, squared
}

// Capacities fits a knee for each transaction or action latency series
// measured at enough distinct loads.
func Capacities(series []Series, levels []LoadLevel, params DetectParams) []Capacity {
	sorted := sortedLevels(levels)
	groups := levelGroups(sorted, params.LevelTolerance)
	if len(groups) < MinKneeLevels {
		return []Capacity{}
	}
	out := []Capacity{}
	for _, s := range series {
		if measures(s) != signatures.KindLatency || !strings.HasPrefix(s.ID, NodeTransaction+"/") && !strings.HasPrefix(s.ID, NodeAction+"/") {
			continue
		}
		s.Points = sortedPoints(s.Points)
		spans := segments(s, sorted)
		c := Capacity{Series: s.ID, LoadUnit: LoadUnitVUsers, Levels: []CapacityLevel{}}
		for _, group := range groups {
			var values []float64
			for _, index := range group {
				for _, p := range spans[index] {
					if usable(p) {
						values = append(values, p.Value)
					}
				}
			}
			if len(values) == 0 {
				continue
			}
			sort.Float64s(values)
			level := sorted[group[0]]
			load := level.VUsers
			if load <= 0 {
				load, c.LoadUnit = level.Rate, LoadUnitRate
			}
			c.Levels = append(c.Levels, CapacityLevel{Load: load, P95Ms: medianSorted(values)})
		}
		if len(c.Levels) < MinKneeLevels {
			continue
		}
		sort.Slice(c.Levels, func(i, j int) bool { return c.Levels[i].Load < c.Levels[j].Load })
		c.Knee, c.SlopeBefore, c.SlopeAfter, c.Reason = KneeFit(c.Levels, params.LatencyFloorMs)
		out = append(out, c)
		if len(out) >= MaxCapacityResults {
			break
		}
	}
	return out
}

// LittlesLaw is one broker queue's check of depth = arrival rate x
// residence time in one steady level, or why it could not apply.
type LittlesLaw struct {
	Queue            string    `json:"queue"`
	Start            time.Time `json:"start,omitzero"`
	End              time.Time `json:"end,omitzero"`
	Eligible         bool      `json:"eligible"`
	Reason           string    `json:"reason,omitempty"`
	Depth            float64   `json:"depth,omitempty"`
	ArrivalRate      float64   `json:"arrival_rate_per_s,omitempty"`
	ResidenceSeconds float64   `json:"residence_s,omitempty"`
	Predicted        float64   `json:"predicted_depth,omitempty"`
	Ratio            float64   `json:"ratio,omitempty"`
}

// LittlesLawChecks applies Little's law only at broker hops whose depth,
// arrival rate and residence time were all measured for the same queue, in
// steady levels whose depth trend is flat. Anything else is ineligible,
// with the reason.
func LittlesLawChecks(series []Series, levels []LoadLevel) []LittlesLaw {
	type queueSeries map[string]Series
	queues := map[string]queueSeries{}
	for _, s := range series {
		kind := measures(s)
		if kind != signatures.KindQueueDepth && kind != signatures.KindArrivalRate && kind != signatures.KindResidenceTime {
			continue
		}
		for _, node := range queueNodes(seriesNodes(s)) {
			if queues[node] == nil {
				queues[node] = queueSeries{}
			}
			if _, taken := queues[node][kind]; !taken {
				queues[node][kind] = s
			}
		}
	}
	out := []LittlesLaw{}
	names := make([]string, 0, len(queues))
	for name := range queues {
		names = append(names, name)
	}
	sort.Strings(names)
	sorted := sortedLevels(levels)
	for _, queue := range names {
		q := queues[queue]
		var missing []string
		for _, kind := range []string{signatures.KindQueueDepth, signatures.KindArrivalRate, signatures.KindResidenceTime} {
			if _, ok := q[kind]; !ok {
				missing = append(missing, kind)
			}
		}
		if len(missing) > 0 {
			out = append(out, LittlesLaw{Queue: queue, Reason: "missing " + strings.Join(missing, ", ")})
			continue
		}
		scale, known := secondsPer(q[signatures.KindResidenceTime].Unit)
		if !known {
			out = append(out, LittlesLaw{Queue: queue, Reason: "the residence time's unit is not a time unit"})
			continue
		}
		if !perSecond(q[signatures.KindArrivalRate].Unit) {
			out = append(out, LittlesLaw{Queue: queue, Reason: "the arrival rate's unit is not per second"})
			continue
		}
		depthSpans := segments(withSortedPoints(q[signatures.KindQueueDepth]), sorted)
		rateSpans := segments(withSortedPoints(q[signatures.KindArrivalRate]), sorted)
		residenceSpans := segments(withSortedPoints(q[signatures.KindResidenceTime]), sorted)
		for i, level := range sorted {
			check := LittlesLaw{Queue: queue, Start: level.Start, End: level.End}
			depth, rate, residence := usablePoints(depthSpans[i]), usablePoints(rateSpans[i]), usablePoints(residenceSpans[i])
			switch {
			case len(depth) < LittlesMinPoints || len(rate) < LittlesMinPoints || len(residence) < LittlesMinPoints:
				check.Reason = fmt.Sprintf("fewer than %d points of each series in the level", LittlesMinPoints)
			default:
				mean := meanValue(depth)
				if change := math.Abs(trendSlope(depth) * level.End.Sub(level.Start).Seconds()); change > math.Max(LittlesFlatFraction*mean, 1) {
					check.Reason = fmt.Sprintf("the depth trend moves it by %.1f across the level, so the queue is not in steady state", change)
					break
				}
				check.Eligible, check.Depth = true, mean
				check.ArrivalRate, check.ResidenceSeconds = meanValue(rate), meanValue(residence)*scale
				check.Predicted = check.ArrivalRate * check.ResidenceSeconds
				if check.Predicted > 0 {
					check.Ratio = check.Depth / check.Predicted
				}
			}
			out = append(out, check)
			if len(out) >= MaxCapacityResults {
				return out
			}
		}
	}
	return out
}

// queueNodes are the nodes of a queue series that name a queue: queue
// infrastructure, or else provider entities. Hosts and pods in its
// dimensions are where the broker runs, not the queue.
func queueNodes(nodes []string) []string {
	var queues, entities []string
	for _, n := range nodes {
		switch {
		case strings.HasPrefix(n, InfrastructureNodeID("queue", "")):
			queues = append(queues, n)
		case strings.HasPrefix(n, entityNodePrefix):
			entities = append(entities, n)
		}
	}
	if len(queues) > 0 {
		return queues
	}
	return entities
}

func withSortedPoints(s Series) Series {
	s.Points = sortedPoints(s.Points)
	return s
}

func usablePoints(points []SeriesPoint) []SeriesPoint {
	out := make([]SeriesPoint, 0, len(points))
	for _, p := range points {
		if usable(p) {
			out = append(out, p)
		}
	}
	return out
}

func meanValue(points []SeriesPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	var sum float64
	for _, p := range points {
		sum += p.Value
	}
	return sum / float64(len(points))
}

// trendSlope is the least-squares slope of the values per second.
func trendSlope(points []SeriesPoint) float64 {
	if len(points) < 2 {
		return 0
	}
	origin := points[0].At
	levels := make([]CapacityLevel, len(points))
	for i, p := range points {
		levels[i] = CapacityLevel{Load: p.At.Sub(origin).Seconds(), P95Ms: p.Value}
	}
	slope, _ := fitLine(levels)
	return slope
}

// secondsPer converts a time unit to seconds.
func secondsPer(unit string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "s", "second", "seconds":
		return 1, true
	case "ms", "millisecond", "milliseconds":
		return 1e-3, true
	case "us", "µs", "microsecond", "microseconds":
		return 1e-6, true
	case "min", "minute", "minutes":
		return 60, true
	}
	return 0, false
}

// perSecond reports whether a rate's unit is per second.
func perSecond(unit string) bool {
	u := strings.ToLower(strings.TrimSpace(unit))
	return strings.HasSuffix(u, "/s") || strings.HasSuffix(u, "per_second") || strings.HasSuffix(u, "/sec")
}

package api

import (
	"math"
	"sort"
	"time"
)

// Stable windows are made only from fully recorded consecutive intervals. The
// settling period is trimmed, never extrapolated; interval-boundary cuts are excluded.
func baselineStableWindows(summaries []IntervalSummaryRecord, started time.Time, group string, selection BaselineComparisonWindow) []BaselineLoadWindow {
	result := []BaselineLoadWindow{}
	if started.IsZero() {
		return result
	}
	type interval struct {
		start, end, load, total float64
		mix                     map[string]float64
	}
	var pending []interval
	previousLoad := 0.0
	sum, weight, minimum, maximum := 0.0, 0.0, math.Inf(1), math.Inf(-1)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		settle := pending[0].start + 30
		var kept []interval
		for _, v := range pending {
			if v.start >= settle && v.start >= selection.StartSeconds && (selection.EndSeconds == 0 || v.end <= selection.EndSeconds) {
				kept = append(kept, v)
			}
		}
		direction := "steady"
		rawLoad := pending[0].load
		if previousLoad > 0 {
			if rawLoad > previousLoad*1.05 {
				direction = "increasing"
			} else if rawLoad < previousLoad*.95 {
				direction = "decreasing"
			}
		}
		previousLoad = rawLoad
		pending = nil
		sum, weight, minimum, maximum = 0, 0, math.Inf(1), math.Inf(-1)
		if len(kept) == 0 {
			return
		}
		w := BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{kept[0].start, kept[len(kept)-1].end}, Group: group, Direction: direction, Mix: map[string]float64{}}
		seconds := w.EndSeconds - w.StartSeconds
		if seconds < 60 {
			return
		}
		for _, v := range kept {
			weight := (v.end - v.start) / seconds
			w.Load += v.load * weight
			w.TotalLoad += v.total * weight
			for k, m := range v.mix {
				w.Mix[k] += m * weight
			}
		}
		result = append(result, w)
	}
	for i, s := range summaries {
		duration, estimated := loadComparisonIntervalSeconds(summaries, i, started)
		end := s.Timestamp.Sub(started).Seconds()
		start := end - duration
		load := float64(s.ActiveVUsers)
		if group != "" {
			g := loadComparisonGroupForSummary(s, group)
			if g == nil {
				flush()
				continue
			}
			load = float64(g.VUsers)
		}
		// An active-runtime clock that did not advance means this interval includes a pause.
		paused := i > 0 && s.ActiveRuntimeSeconds != nil && summaries[i-1].ActiveRuntimeSeconds != nil && *s.ActiveRuntimeSeconds-*summaries[i-1].ActiveRuntimeSeconds < duration-.5
		if estimated || paused || duration <= 0 || load <= 0 || s.ActiveVUsers <= 0 {
			flush()
			continue
		}
		mix := map[string]float64{}
		for name, g := range s.Groups {
			if g != nil {
				mix[name] = float64(g.VUsers) / float64(s.ActiveVUsers)
			}
		}
		v := interval{start, end, load, float64(s.ActiveVUsers), mix}
		if len(pending) > 0 {
			mean := (sum + load*duration) / (weight + duration)
			low, high := math.Min(minimum, load), math.Max(maximum, load)
			valid := math.Abs(low-mean) <= mean*.05+1e-9 && math.Abs(high-mean) <= mean*.05+1e-9

			if !valid || math.Abs(start-pending[len(pending)-1].end) > .001 {
				flush()
			}
		}
		pending = append(pending, v)
		sum += load * duration
		weight += duration
		minimum = math.Min(minimum, load)
		maximum = math.Max(maximum, load)
	}
	flush()
	return result
}

func matchingBaselineWindow(current BaselineLoadWindow, candidates []BaselineLoadWindow, tolerance float64) *BaselineLoadWindow {
	var best *BaselineLoadWindow
	for i := range candidates {
		c := candidates[i]
		if c.Group != current.Group || c.Direction != current.Direction {
			continue
		}
		if math.Abs(c.Load-current.Load) > current.Load*tolerance/100+1e-9 {
			continue
		}
		if best == nil || math.Abs(c.Load-current.Load) < math.Abs(best.Load-current.Load) {
			copy := c
			best = &copy
		}
	}
	return best
}

func baselineWindowQualification(a, b BaselineLoadWindow, tolerance float64) []string {
	q := []string{}
	if math.Abs(a.TotalLoad-b.TotalLoad) > a.TotalLoad*tolerance/100+1e-9 {
		q = append(q, "Total observed VUser load differs")
	}
	if len(a.Mix) == 0 || len(b.Mix) == 0 {
		q = append(q, "Workload mix is unverified")
	}
	keys := map[string]bool{}
	for k := range a.Mix {
		keys[k] = true
	}
	for k := range b.Mix {
		keys[k] = true
	}
	for k := range keys {
		if math.Abs(a.Mix[k]-b.Mix[k]) > .05 {
			q = append(q, "Workload mix differs for "+k)
		}
	}
	sort.Strings(q)
	return q
}

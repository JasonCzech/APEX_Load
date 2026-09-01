package trafficprofile

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// SelectOverallPeakWindow returns the relative start offset of the window
// with the greatest aggregate area across all supplied normalized series.
// Series are aligned at offset zero so independently dated per-group source
// files can still describe the same production interval.
func SelectOverallPeakWindow(series []NormalizedSeries, duration time.Duration) (time.Duration, error) {
	if len(series) == 0 {
		return 0, errors.New("overall peak selection requires at least one mapped series")
	}
	windowMS := duration.Milliseconds()
	if windowMS <= 0 {
		return 0, errors.New("overall peak window duration must be greater than zero")
	}
	commonEndMS := int64(0)
	for index, item := range series {
		if len(item.Points) < 2 {
			return 0, fmt.Errorf("mapped series %d contains fewer than two normalized points", index+1)
		}
		endMS := item.TargetDuration.Milliseconds()
		if endMS <= 0 {
			return 0, fmt.Errorf("mapped series %d has no positive source duration", index+1)
		}
		if commonEndMS == 0 || endMS < commonEndMS {
			commonEndMS = endMS
		}
	}
	if windowMS > commonEndMS {
		return 0, fmt.Errorf("overall peak window duration %s exceeds the shortest mapped source span %s", duration, time.Duration(commonEndMS)*time.Millisecond)
	}

	// Merge each step series into one aggregate step function. Storing deltas
	// lets duplicate mappings contribute independently without expanding a
	// potentially 50,000-point request into a dense shared grid.
	deltas := map[int64]float64{0: 0, commonEndMS: 0}
	sharedStarts := make(map[int64]int)
	for seriesIndex, item := range series {
		previousOffset := int64(-1)
		previousTarget := 0.0
		for pointIndex, point := range item.Points {
			if point.OffsetMS < 0 || point.OffsetMS <= previousOffset {
				return 0, fmt.Errorf("mapped series %d point %d has a non-increasing offset", seriesIndex+1, pointIndex+1)
			}
			previousOffset = point.OffsetMS
			if point.OffsetMS > commonEndMS {
				break
			}
			deltas[point.OffsetMS] += point.Target - previousTarget
			sharedStarts[point.OffsetMS]++
			previousTarget = point.Target
		}
	}

	offsets := make([]int64, 0, len(deltas))
	for offset := range deltas {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	values := make([]float64, len(offsets))
	prefixArea := make([]float64, len(offsets))
	current := 0.0
	for index, offset := range offsets {
		if index > 0 {
			prefixArea[index] = prefixArea[index-1] + values[index-1]*float64(offset-offsets[index-1])
		}
		current += deltas[offset]
		values[index] = current
	}
	integralAt := func(offset int64) float64 {
		index := sort.Search(len(offsets), func(i int) bool { return offsets[i] >= offset })
		if index < len(offsets) && offsets[index] == offset {
			return prefixArea[index]
		}
		index--
		return prefixArea[index] + values[index]*float64(offset-offsets[index])
	}

	maxStart := commonEndMS - windowMS
	candidates := make(map[int64]struct{})
	for offset, count := range sharedStarts {
		// Requiring a source bucket boundary in every series keeps the chosen
		// relative window directly representable for count and counter inputs;
		// synthesizing a partial raw bucket would change its rate semantics.
		if count == len(series) && offset >= 0 && offset <= maxStart {
			candidates[offset] = struct{}{}
		}
	}
	if len(candidates) == 0 {
		return 0, errors.New("mapped series have no shared bucket boundary that can start the overall peak window")
	}
	bestStart, bestArea := int64(0), -1.0
	for start := range candidates {
		area := integralAt(start+windowMS) - integralAt(start)
		if area > bestArea || (area == bestArea && start < bestStart) {
			bestStart, bestArea = start, area
		}
	}
	return time.Duration(bestStart) * time.Millisecond, nil
}

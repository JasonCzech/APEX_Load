package trafficprofile

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

func SeriesStats(series Series) (Stats, error) {
	points, err := validateAndSort(series.Points)
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Count: len(points), Start: points[0].Timestamp, End: points[len(points)-1].Timestamp, Min: points[0].Value, Max: points[0].Value}
	var total float64
	intervals := make([]time.Duration, 0, len(points)-1)
	for index, point := range points {
		total += point.Value
		stats.Min = math.Min(stats.Min, point.Value)
		stats.Max = math.Max(stats.Max, point.Value)
		if index > 0 {
			intervals = append(intervals, point.Timestamp.Sub(points[index-1].Timestamp))
		}
	}
	stats.Mean = total / float64(len(points))
	stats.DetectedBucket = medianDuration(intervals)
	for _, interval := range intervals {
		if stats.DetectedBucket > 0 && interval > stats.DetectedBucket*3/2 {
			stats.Gaps++
		}
	}
	return stats, nil
}

func Normalize(series Series, opts NormalizeOptions) (NormalizedSeries, error) {
	return normalizeSeries(series, opts, false)
}

// NormalizeForPeakSelection converts source values to their runtime target
// semantics without enforcing executable bucket cadence. It is intended only
// for choosing a source-time window; the selected result must still pass
// Normalize with its final target duration before it can be stored or run.
func NormalizeForPeakSelection(series Series, opts NormalizeOptions) (NormalizedSeries, error) {
	opts.Window = WindowSpec{Mode: "full"}
	opts.TargetDuration = 0
	opts.AmplitudeMode = ""
	opts.Amplitude = 1
	return normalizeSeries(series, opts, true)
}

func normalizeSeries(series Series, opts NormalizeOptions, skipRuntimeCadenceValidation bool) (NormalizedSeries, error) {
	points, err := validateAndSort(series.Points)
	if err != nil {
		return NormalizedSeries{}, err
	}
	if len(points) > MaxPointsPerSeries {
		return NormalizedSeries{}, fmt.Errorf("series contains %d points; limit is %d", len(points), MaxPointsPerSeries)
	}
	initialStats, err := SeriesStats(Series{Points: points})
	if err != nil {
		return NormalizedSeries{}, err
	}
	points, gapWarnings, err := applyGapPolicy(points, initialStats.DetectedBucket, opts.GapPolicy, opts.Semantics)
	if err != nil {
		return NormalizedSeries{}, err
	}
	if len(points) > MaxPointsPerSeries {
		return NormalizedSeries{}, fmt.Errorf("gap expansion produced %d points; limit is %d", len(points), MaxPointsPerSeries)
	}
	series.Points = points
	var explicitWindowEnd time.Time
	if opts.Window.Mode == "explicit" && opts.Window.RelativeOffsets {
		explicitWindowEnd = series.Points[0].Timestamp.Add(opts.Window.EndOffset)
	}
	series, err = selectWindow(series, opts.Window, opts.Semantics)
	if err != nil {
		return NormalizedSeries{}, err
	}
	stats, err := SeriesStats(series)
	if err != nil {
		return NormalizedSeries{}, err
	}
	sourceEnd := opts.SourceEnd
	if opts.Window.Mode == "explicit" {
		if opts.Window.RelativeOffsets {
			sourceEnd = explicitWindowEnd
		} else {
			sourceEnd = opts.Window.End
		}
	} else if opts.Window.Mode == "peak" && len(series.Points) > 0 {
		sourceEnd = series.Points[0].Timestamp.Add(opts.Window.Duration)
	}
	if !sourceEnd.After(stats.End) {
		sourceEnd = stats.End.Add(stats.DetectedBucket)
	}
	if !sourceEnd.After(stats.End) {
		return NormalizedSeries{}, errors.New("final bucket duration could not be inferred")
	}
	sourceDuration := sourceEnd.Sub(stats.Start)
	targetDuration := opts.TargetDuration
	if targetDuration <= 0 {
		targetDuration = sourceDuration
	}
	amplitude := opts.Amplitude
	if amplitude < 0 || math.IsNaN(amplitude) || math.IsInf(amplitude, 0) {
		return NormalizedSeries{}, errors.New("amplitude must be finite and zero or greater")
	}
	switch opts.AmplitudeMode {
	case "":
		// No transform requested: replay the series as recorded.
		if amplitude == 0 {
			amplitude = 1
		}
	case "factor", "peak":
		// Zero is exactly what an omitted amplitude value decodes to, and it
		// scales every target to zero in both modes. Reject it rather than
		// emitting a profile that validates, saves, and launches a run that
		// generates no load at all.
		if amplitude == 0 {
			return NormalizedSeries{}, fmt.Errorf("amplitude must be greater than zero when amplitude_mode is %q", opts.AmplitudeMode)
		}
	default:
		return NormalizedSeries{}, fmt.Errorf("unsupported amplitude_mode %q", opts.AmplitudeMode)
	}
	values, warnings, err := normalizedValues(series.Points, opts.Semantics, sourceEnd)
	if err != nil {
		return NormalizedSeries{}, err
	}
	warnings = append(gapWarnings, warnings...)
	if opts.AmplitudeMode == "peak" {
		peak := 0.0
		for _, value := range values {
			peak = math.Max(peak, value)
		}
		if peak <= 0 {
			return NormalizedSeries{}, errors.New("cannot scale an all-zero series to a positive peak")
		}
		amplitude /= peak
	}
	minimum := time.Second
	runtimeTarget := "vusers"
	if opts.Semantics != SemanticsVUsers {
		minimum = 5 * time.Second
		runtimeTarget = "tps"
	}
	scale := float64(targetDuration) / float64(sourceDuration)
	runtimePoints := make([]RuntimePoint, len(series.Points))
	for index, point := range series.Points {
		offset := time.Duration(float64(point.Timestamp.Sub(stats.Start)) * scale)
		if !skipRuntimeCadenceValidation && index > 0 && offset-time.Duration(runtimePoints[index-1].OffsetMS)*time.Millisecond < minimum {
			return NormalizedSeries{}, fmt.Errorf("compressed interval before point %d is below the %s minimum for %s profiles", index+1, minimum, runtimeTarget)
		}
		target := values[index] * amplitude
		if runtimeTarget == "vusers" {
			target = math.Round(target)
		}
		runtimePoints[index] = RuntimePoint{OffsetMS: offset.Milliseconds(), Target: target}
	}
	lastBucket := targetDuration - time.Duration(runtimePoints[len(runtimePoints)-1].OffsetMS)*time.Millisecond
	if !skipRuntimeCadenceValidation && lastBucket < minimum {
		return NormalizedSeries{}, fmt.Errorf("compressed final bucket is below the %s minimum for %s profiles", minimum, runtimeTarget)
	}
	if runtimeTarget == "tps" {
		shortIntervals := 0
		shortestInterval := time.Duration(0)
		longestShortInterval := time.Duration(0)
		largeChanges := 0
		for index := 1; index < len(runtimePoints); index++ {
			interval := time.Duration(runtimePoints[index].OffsetMS-runtimePoints[index-1].OffsetMS) * time.Millisecond
			if interval < time.Minute {
				shortIntervals++
				if shortestInterval == 0 || interval < shortestInterval {
					shortestInterval = interval
				}
				if interval > longestShortInterval {
					longestShortInterval = interval
				}
			}
			previous, current := runtimePoints[index-1].Target, runtimePoints[index].Target
			if (previous == 0 && current > 0) || (previous > 0 && (current/previous > 2 || current/previous < 0.5)) {
				largeChanges++
			}
		}
		if lastBucket < time.Minute {
			shortIntervals++
			if shortestInterval == 0 || lastBucket < shortestInterval {
				shortestInterval = lastBucket
			}
			if lastBucket > longestShortInterval {
				longestShortInterval = lastBucket
			}
		}
		if shortIntervals > 0 {
			minEvaluations := max(1, int(shortestInterval/(5*time.Second)))
			maxEvaluations := max(1, int(longestShortInterval/(5*time.Second)))
			intervalSummary := shortestInterval.String()
			if shortestInterval != longestShortInterval {
				intervalSummary = fmt.Sprintf("%s-%s", shortestInterval, longestShortInterval)
			}
			evaluationSummary := fmt.Sprintf("approximately %d five-second goal-control evaluations each", minEvaluations)
			if minEvaluations != maxEvaluations {
				evaluationSummary = fmt.Sprintf("approximately %d-%d five-second goal-control evaluations each", minEvaluations, maxEvaluations)
			}
			warnings = append(warnings, fmt.Sprintf("%d TPS intervals are under 1m (%s) and allow %s", shortIntervals, intervalSummary, evaluationSummary))
		}
		if largeChanges > 0 {
			warnings = append(warnings, fmt.Sprintf("%d adjacent TPS target changes exceed 2x or fall below 0.5x; achieved TPS may lag the requested steps", largeChanges))
		}
	}
	return NormalizedSeries{Source: series, Points: runtimePoints, SourceStart: stats.Start, SourceEnd: sourceEnd, SourceDuration: sourceDuration, TargetDuration: targetDuration, DetectedBucket: stats.DetectedBucket, RuntimeTarget: runtimeTarget, Warnings: dedupeStrings(warnings)}, nil
}

func applyGapPolicy(points []Point, cadence time.Duration, policy GapPolicy, semantics ValueSemantics) ([]Point, []string, error) {
	if cadence <= 0 || len(points) < 2 {
		return points, nil, nil
	}
	hasGap := false
	for index := 1; index < len(points); index++ {
		if points[index].Timestamp.Sub(points[index-1].Timestamp) > cadence*3/2 {
			hasGap = true
			break
		}
	}
	if !hasGap {
		return points, nil, nil
	}
	if policy == "" {
		return nil, nil, errors.New("gap_policy is required when the series contains missing buckets")
	}
	if policy != GapHold && policy != GapZero && policy != GapDrop {
		return nil, nil, fmt.Errorf("unsupported gap_policy %q", policy)
	}
	if policy == GapDrop {
		return points, []string{"missing buckets were dropped; surrounding source timestamps remain irregular"}, nil
	}
	result := make([]Point, 0, len(points))
	inserted := 0
	for index, point := range points {
		if index > 0 {
			previous := points[index-1]
			for timestamp := previous.Timestamp.Add(cadence); timestamp.Before(point.Timestamp); timestamp = timestamp.Add(cadence) {
				value := 0.0
				if policy == GapHold || semantics == SemanticsCumulativeCounter {
					value = previous.Value
				}
				result = append(result, Point{Timestamp: timestamp, Value: value})
				inserted++
				if len(result)+len(points)-index > MaxPointsPerSeries {
					return nil, nil, fmt.Errorf("gap expansion exceeds %d points", MaxPointsPerSeries)
				}
			}
		}
		result = append(result, point)
	}
	return result, []string{fmt.Sprintf("%d missing buckets were filled using the %s gap policy", inserted, policy)}, nil
}

func validateAndSort(input []Point) ([]Point, error) {
	if len(input) < 2 {
		return nil, errors.New("series requires at least two points")
	}
	points := append([]Point(nil), input...)
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	for index, point := range points {
		if point.Timestamp.IsZero() {
			return nil, fmt.Errorf("point %d timestamp is required", index+1)
		}
		if point.Value < 0 || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			return nil, fmt.Errorf("point %d value must be finite and zero or greater", index+1)
		}
		if index > 0 && point.Timestamp.Equal(points[index-1].Timestamp) {
			return nil, fmt.Errorf("duplicate timestamp %s", point.Timestamp.Format(time.RFC3339Nano))
		}
	}
	return points, nil
}

func normalizedValues(points []Point, semantics ValueSemantics, sourceEnd time.Time) ([]float64, []string, error) {
	values := make([]float64, len(points))
	warnings := make([]string, 0)
	switch semantics {
	case SemanticsTPS, SemanticsVUsers:
		for i := range points {
			values[i] = points[i].Value
		}
	case SemanticsTransactionsPerBucket:
		for i := range points {
			end := sourceEnd
			if i+1 < len(points) {
				end = points[i+1].Timestamp
			}
			seconds := end.Sub(points[i].Timestamp).Seconds()
			if seconds <= 0 {
				return nil, nil, fmt.Errorf("point %d has no positive bucket duration", i+1)
			}
			values[i] = points[i].Value / seconds
		}
	case SemanticsCumulativeCounter:
		for i := range points {
			end := sourceEnd
			if i+1 < len(points) {
				end = points[i+1].Timestamp
			}
			seconds := end.Sub(points[i].Timestamp).Seconds()
			if seconds <= 0 {
				return nil, nil, fmt.Errorf("point %d has no positive bucket duration", i+1)
			}
			delta := 0.0
			if i+1 < len(points) {
				delta = points[i+1].Value - points[i].Value
				if delta < 0 {
					delta = points[i+1].Value
					warnings = append(warnings, fmt.Sprintf("counter reset detected at %s", points[i+1].Timestamp.Format(time.RFC3339)))
				}
			}
			values[i] = delta / seconds
		}
	default:
		return nil, nil, fmt.Errorf("unsupported value_semantics %q", semantics)
	}
	return values, warnings, nil
}

func selectWindow(series Series, spec WindowSpec, semantics ValueSemantics) (Series, error) {
	mode := spec.Mode
	if mode == "" || mode == "full" {
		return series, nil
	}
	if mode == "explicit" {
		start, end := spec.Start, spec.End
		if spec.RelativeOffsets {
			if spec.StartOffset < 0 || spec.EndOffset <= spec.StartOffset {
				return Series{}, errors.New("explicit offset window end must be after start")
			}
			start = series.Points[0].Timestamp.Add(spec.StartOffset)
			end = series.Points[0].Timestamp.Add(spec.EndOffset)
		} else if !end.After(start) {
			return Series{}, errors.New("explicit window end must be after start")
		}
		return filterWindow(series, start, end)
	}
	if mode != "peak" {
		return Series{}, fmt.Errorf("unsupported window mode %q", mode)
	}
	if spec.Duration <= 0 {
		return Series{}, errors.New("peak window duration must be greater than zero")
	}
	// Build an area prefix once so peak selection stays O(n log n) at the
	// 10,000-point limit. A quadratic scan here made a valid multi-series
	// derive request an inexpensive CPU exhaustion vector.
	cadence := medianDuration(func() []time.Duration {
		values := make([]time.Duration, 0, len(series.Points)-1)
		for index := 1; index < len(series.Points); index++ {
			values = append(values, series.Points[index].Timestamp.Sub(series.Points[index-1].Timestamp))
		}
		return values
	}())
	if cadence <= 0 {
		return Series{}, errors.New("peak window cadence could not be inferred")
	}
	sourceEnd := series.Points[len(series.Points)-1].Timestamp.Add(cadence)
	rates := make([]float64, len(series.Points))
	prefix := make([]float64, len(series.Points)+1)
	for index, point := range series.Points {
		interval := cadence
		if index+1 < len(series.Points) {
			interval = series.Points[index+1].Timestamp.Sub(point.Timestamp)
		}
		rate := point.Value
		switch semantics {
		case SemanticsTPS, SemanticsVUsers:
		case SemanticsCumulativeCounter:
			if index+1 < len(series.Points) {
				rate = series.Points[index+1].Value - point.Value
				if rate < 0 {
					rate = series.Points[index+1].Value
				}
				rate /= interval.Seconds()
			} else {
				rate = 0
			}
		case SemanticsTransactionsPerBucket:
			rate = point.Value / interval.Seconds()
		}
		rates[index] = rate
		area := 0.0
		if index+1 < len(series.Points) {
			area = rate * interval.Seconds()
		}
		prefix[index+1] = prefix[index] + area
	}
	bestStart, bestEnd, bestArea := 0, 0, -1.0
	for start := range series.Points {
		endTime := series.Points[start].Timestamp.Add(spec.Duration)
		if endTime.After(sourceEnd) {
			continue
		}
		end := sort.Search(len(series.Points), func(index int) bool {
			return !series.Points[index].Timestamp.Before(endTime)
		})
		area := prefix[end] - prefix[start]
		if end > start {
			last := end - 1
			// The prefix includes the entire interval after the last selected
			// point. Replace it with only the portion inside this window.
			if last+1 < len(series.Points) {
				area -= rates[last] * series.Points[last+1].Timestamp.Sub(series.Points[last].Timestamp).Seconds()
			}
			area += rates[last] * endTime.Sub(series.Points[last].Timestamp).Seconds()
		}
		if end-start >= 2 && area > bestArea {
			bestStart, bestEnd, bestArea = start, end, area
		}
	}
	if bestArea < 0 {
		return Series{}, errors.New("no peak window contains at least two points")
	}
	series.Points = append([]Point(nil), series.Points[bestStart:bestEnd]...)
	return series, nil
}

func filterWindow(series Series, start, end time.Time) (Series, error) {
	points := make([]Point, 0)
	for _, point := range series.Points {
		if !point.Timestamp.Before(start) && point.Timestamp.Before(end) {
			points = append(points, point)
		}
	}
	if len(points) < 2 {
		return Series{}, errors.New("selected window contains fewer than two points")
	}
	series.Points = points
	return series, nil
}

func medianDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	return copyValues[len(copyValues)/2]
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

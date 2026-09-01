package trafficprofile

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// NormalizeART projects file-backed ART evidence onto the workload's resolved
// source window and transformed runtime clock. ART must cover every workload
// bucket exactly; otherwise the peak observed ART is used conservatively for
// the complete window.
func NormalizeART(series Series, workload NormalizedSeries, unit string) (NormalizedART, error) {
	if len(workload.Source.Points) == 0 || !workload.SourceEnd.After(workload.SourceStart) || workload.TargetDuration <= 0 {
		return NormalizedART{}, errors.New("normalized workload window is required for ART sizing")
	}
	factor, _, err := artUnitFactor(unit)
	if err != nil {
		return NormalizedART{}, err
	}
	points := append([]Point(nil), series.Points...)
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	seen := make(map[time.Time]struct{}, len(points))
	window := make([]Point, 0, len(points))
	peak := 0.0
	for _, point := range points {
		if point.Timestamp.Before(workload.SourceStart) || !point.Timestamp.Before(workload.SourceEnd) {
			continue
		}
		if _, exists := seen[point.Timestamp]; exists {
			return NormalizedART{}, fmt.Errorf("ART series contains duplicate timestamp %s", point.Timestamp.Format(time.RFC3339Nano))
		}
		seen[point.Timestamp] = struct{}{}
		value := point.Value * factor
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return NormalizedART{}, errors.New("ART values must be finite and greater than zero")
		}
		point.Value = value
		peak = math.Max(peak, value)
		window = append(window, point)
	}
	if len(window) == 0 || peak <= 0 {
		return NormalizedART{}, errors.New("ART series has no valid values in the selected workload window")
	}

	complete := true
	for _, workloadPoint := range workload.Source.Points {
		if workloadPoint.Timestamp.Before(workload.SourceStart) || !workloadPoint.Timestamp.Before(workload.SourceEnd) {
			continue
		}
		if _, ok := seen[workloadPoint.Timestamp]; !ok {
			complete = false
			break
		}
	}
	if !complete {
		return NormalizedART{
			Points:         []ARTPoint{{OffsetMS: 0, ARTMS: peak}},
			PeakARTMS:      peak,
			FallbackToPeak: true,
			Warnings:       []string{fmt.Sprintf("ART series %q was sparse in the selected workload window; peak ART %.2f ms is used for conservative sizing", series.Name, peak)},
		}, nil
	}

	scale := float64(workload.TargetDuration) / float64(workload.SourceEnd.Sub(workload.SourceStart))
	normalized := make([]ARTPoint, 0, len(window))
	for _, point := range window {
		offset := time.Duration(float64(point.Timestamp.Sub(workload.SourceStart)) * scale).Milliseconds()
		normalized = append(normalized, ARTPoint{OffsetMS: offset, ARTMS: point.Value})
	}
	return NormalizedART{Points: normalized, PeakARTMS: peak, Warnings: nil}, nil
}

func NormalizeARTPeak(value float64, unit string) (float64, error) {
	factor, _, err := artUnitFactor(unit)
	if err != nil {
		return 0, err
	}
	value *= factor
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("ART value must be finite and greater than zero")
	}
	return value, nil
}

func artUnitFactor(unit string) (factor float64, normalized string, err error) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "", "ms", "millisecond", "milliseconds":
		return 1, "milliseconds", nil
	case "s", "second", "seconds":
		return 1000, "seconds", nil
	default:
		return 0, "", fmt.Errorf("ART unit must be milliseconds or seconds, got %q", unit)
	}
}

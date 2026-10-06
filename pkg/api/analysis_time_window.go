package api

import (
	"context"
	"time"
)

// Canonical Parquet TIMESTAMPTZ values have microsecond precision. Whenever a
// dataset participates, apply one explicitly reported window to both sides.
// This changes derived bucket bounds, never retained source timestamps.
type AnalysisTimeWindow struct {
	RequestedFrom time.Time `json:"requested_from"`
	RequestedTo   time.Time `json:"requested_to"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	Resolution    string    `json:"resolution"`
}
type analysisTimeWindowKey struct{}

func nativeAnalysisTimeWindow(from, to time.Time) AnalysisTimeWindow {
	return AnalysisTimeWindow{RequestedFrom: from.UTC(), RequestedTo: to.UTC(), From: from.UTC().Truncate(time.Microsecond), To: to.UTC().Truncate(time.Microsecond), Resolution: "microsecond"}
}
func withAnalysisTimeWindow(ctx context.Context, window AnalysisTimeWindow) context.Context {
	return context.WithValue(ctx, analysisTimeWindowKey{}, window)
}
func completedAnalysisTimeWindow(ctx context.Context) *AnalysisTimeWindow {
	window, ok := ctx.Value(analysisTimeWindowKey{}).(AnalysisTimeWindow)
	if !ok {
		return nil
	}
	return &window
}

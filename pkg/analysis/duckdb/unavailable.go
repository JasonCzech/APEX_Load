//go:build !analytics_duckdb || !cgo

package duckdb

import (
	"context"
	"github.com/jasonczech/apexload/pkg/analysis"
)

func Available() bool                                            { return false }
func (*Engine) Preview(context.Context, string) (Preview, error) { return Preview{}, ErrUnavailable }
func (*Engine) Normalize(context.Context, NormalizeRequest) (NormalizeResult, error) {
	return NormalizeResult{}, ErrUnavailable
}
func (*Engine) Series(context.Context, []string, SeriesQuery) ([]analysis.MetricPoint, error) {
	return nil, ErrUnavailable
}

package duckdb

import (
	"errors"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func validateNativeSeriesIdentity(q SeriesQuery) error {
	if q.IdentitySchemaVersion < 0 || q.IdentitySchemaVersion > 2 || len(q.Dimensions) > 16 {
		return errors.New("invalid native identity schema or dimension bound")
	}
	for _, value := range []string{q.ProjectID, q.DatasetID, q.Metric, q.SourceID, q.Host, q.Service} {
		if len(value) > 256 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || value != "" && strings.TrimSpace(value) == "" {
			return errors.New("invalid native identity filter")
		}
	}
	for key, value := range q.Dimensions {
		if !analysis.ValidMetricDimension(key, value) {
			return errors.New("invalid native dimension filter")
		}
	}
	if q.IdentitySchemaVersion < 2 && (q.Host != "" || q.Service != "" || len(q.Dimensions) != 0 || q.SourceID != "" && q.SourceID != q.DatasetID) {
		return errors.New("legacy normalized artifacts do not retain requested identity filters")
	}
	return nil
}
func nativeSeriesIdentityMatches(q SeriesQuery, p analysis.MetricPoint) bool {
	if q.IdentitySchemaVersion < 2 && (p.Source != q.DatasetID || p.Host != "" || p.Service != "" || len(p.Dimensions) != 0) {
		return false
	}
	if strings.TrimSpace(p.Source) == "" {
		return false
	}
	for _, value := range []string{p.Source, p.Host, p.Service} {
		if len(value) > 256 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return false
		}
	}
	if q.SourceID != "" && p.Source != q.SourceID || q.Host != "" && p.Host != q.Host || q.Service != "" && p.Service != q.Service {
		return false
	}
	for key, value := range p.Dimensions {
		if !analysis.ValidMetricDimension(key, value) {
			return false
		}
	}
	for key, value := range q.Dimensions {
		if actual, exists := p.Dimensions[key]; !exists || actual != value {
			return false
		}
	}
	return true
}
func sameNativeSeriesIdentity(a, b analysis.MetricPoint) bool {
	return a.Source == b.Source && a.Host == b.Host && a.Service == b.Service && maps.Equal(a.Dimensions, b.Dimensions)
}

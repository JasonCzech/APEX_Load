package api

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func validateImportedIdentitySelection(s AnalysisSeriesSelection) error {
	if len(s.Dimensions) > 16 {
		return errors.New("imported dimension filters exceed population bound")
	}
	count := len(s.Dimensions)
	for _, pair := range [][2]string{{"transaction", s.Transaction}, {"protocol", s.Protocol}, {"group", s.Group}} {
		if pair[1] == "" {
			continue
		}
		if !analysis.ValidMetricDimension(pair[0], pair[1]) {
			return errors.New("imported named identity filters exceed scalar bound")
		}
		if existing, exists := s.Dimensions[pair[0]]; exists {
			if existing != pair[1] {
				return errors.New("conflicting imported identity filters")
			}
		} else {
			count++
		}
	}
	if count > 16 {
		return errors.New("combined imported dimension filters exceed population bound")
	}
	for key, value := range s.Dimensions {
		if !analysis.ValidMetricDimension(key, value) {
			return errors.New("invalid imported dimension filter")
		}
	}
	for _, value := range []string{s.SourceID, s.Host, s.Service} {
		if len(value) > 256 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || value != "" && strings.TrimSpace(value) == "" {
			return errors.New("invalid imported identity filter")
		}
	}
	return nil
}
func importedNativeSeriesQuery(project string, in *storage.AnalysisImport, s AnalysisSeriesSelection, q AnalysisComparisonRequest) (analytics.SeriesQuery, error) {
	if in == nil || in.ProjectID != project || in.ID != s.DatasetID || validateImportedIdentitySelection(s) != nil {
		return analytics.SeriesQuery{}, errors.New("imported selection ownership or identity mismatch")
	}
	version := 1
	if in.Mapping != nil {
		version = in.Mapping.Version
	}
	if version != 1 && version != 2 {
		return analytics.SeriesQuery{}, errors.New("unsupported imported identity mapping version")
	}
	dimensions := maps.Clone(s.Dimensions)
	if dimensions == nil {
		dimensions = map[string]string{}
	}
	for _, pair := range [][2]string{{"transaction", s.Transaction}, {"protocol", s.Protocol}, {"group", s.Group}} {
		if pair[1] != "" {
			dimensions[pair[0]] = pair[1]
		}
	}
	query := analytics.SeriesQuery{ProjectID: project, DatasetID: in.ID, Metric: s.Metric, IdentitySchemaVersion: version, SourceID: s.SourceID, Host: s.Host, Service: s.Service, Dimensions: dimensions, From: q.From, To: q.To, BucketSeconds: q.BucketSeconds, Aggregation: s.Aggregation}
	return bindImportedNativeSeriesQuery(in, query)
}

// Omitted identity version uses the immutable dataset mapping. An explicitly
// requested incompatible version cannot erase identities or upgrade old files.
func bindImportedNativeSeriesQuery(in *storage.AnalysisImport, query analytics.SeriesQuery) (analytics.SeriesQuery, error) {
	if in == nil || query.ProjectID != in.ProjectID || query.DatasetID != in.ID {
		return analytics.SeriesQuery{}, errors.New("dataset query scope mismatch")
	}
	if in.Mapping != nil {
		if err := in.Mapping.ValidateDisclosure(); err != nil {
			return analytics.SeriesQuery{}, err
		}
	}
	version := 1
	if in.Mapping != nil {
		version = in.Mapping.Version
	}
	if version != 1 && version != 2 {
		return analytics.SeriesQuery{}, errors.New("unsupported imported identity mapping version")
	}
	if query.IdentitySchemaVersion == 0 {
		query.IdentitySchemaVersion = version
	}
	if query.IdentitySchemaVersion != version {
		return analytics.SeriesQuery{}, errors.New("query identity capability does not match dataset mapping")
	}
	if err := query.Validate(); err != nil {
		return analytics.SeriesQuery{}, err
	}
	if in.Mapping != nil {
		if err := validateImportedMeasureQuery(*in.Mapping, query); err != nil {
			return analytics.SeriesQuery{}, err
		}
	}
	query.Dimensions = maps.Clone(query.Dimensions)
	return query, nil
}

// validateImportedMeasureQuery explains a summary or histogram request that
// cannot be answered before any data is staged. Long-table measures name
// metrics from data, so the engine applies the same rules to those.
func validateImportedMeasureQuery(mapping analysis.ParquetMapping, query analytics.SeriesQuery) error {
	if histogram, found := mapping.HistogramDeclarationFor(query.Metric); found {
		return analysis.ValidateHistogramAggregation(histogram.WindowSeconds, query.BucketSeconds, query.Aggregation)
	}
	declared, legacy := mapping.SummaryDeclarations(query.Metric)
	if len(declared) == 0 {
		if legacy {
			return analysis.ErrSummaryStatisticUnknown
		}
		return nil
	}
	statistics := make([]string, 0, len(declared))
	for _, declaration := range declared {
		if declaration.Statistic == query.Aggregation {
			return analysis.ValidateSummaryAggregation(declaration.Statistic, declaration.WindowSeconds, query.BucketSeconds, query.Aggregation)
		}
		statistics = append(statistics, declaration.Statistic)
	}
	return fmt.Errorf("%s is a precomputed summary; request one of its declared statistics: %s", query.Metric, strings.Join(statistics, ", "))
}

package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import ()

type DataSourceFitReport = importreview.DataSourceFitReport

type DataSourceFitItem = importreview.DataSourceFitItem

type parsedDataSourceShape = importreview.ParsedDataSourceShape

var buildDataSourceFitReport = importreview.BuildDataSourceFitReport

var evaluateParameterDataSourceFit = importreview.EvaluateParameterDataSourceFit

var parseDataSourceShape = importreview.ParseDataSourceShape

var authoringDataSourceDelimiter = importreview.AuthoringDataSourceDelimiter

var detectInlineDataSourceDelimiter = importreview.DetectInlineDataSourceDelimiter

var scoreInlineDataSourceDelimiter = importreview.ScoreInlineDataSourceDelimiter

var requiredRowsForParameter = importreview.RequiredRowsForParameter

var requiresUniqueRows = importreview.RequiresUniqueRows

var stringSliceContainsFold = importreview.StringSliceContainsFold

var trimStringSlice = importreview.TrimStringSlice

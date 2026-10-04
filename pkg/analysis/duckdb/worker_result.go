package duckdb

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
)

func (r workerResult) validate(b workerBootstrap, request workerRequest) error {
	if request.validate(b) != nil {
		return errWorkerProtocol
	}
	n := 0
	for _, present := range []bool{r.Preview != nil, r.Normalize != nil, r.Points != nil} {
		if present {
			n++
		}
	}
	if r.Status != "ok" {
		if n != 0 {
			return errWorkerProtocol
		}
		switch r.Status {
		case "cancelled":
			if r.Code != "cancelled" || r.CancelSequence == 0 {
				return errWorkerProtocol
			}
		case "error":
			if r.CancelSequence != 0 || (r.Code != "invalid_input" && r.Code != "resource_limit" && r.Code != "crash" && r.Code != "unavailable" && r.Code != "internal") {
				return errWorkerProtocol
			}
		default:
			return errWorkerProtocol
		}
		return nil
	}
	if n != 1 || r.Code != "" || r.CancelSequence != 0 {
		return errWorkerProtocol
	}
	if request.Preview != nil {
		if r.Preview == nil {
			return errWorkerProtocol
		}
		input, _ := b.handle(request.Preview.InputHandle, "input")
		s := r.Preview.Schema
		if s.Rows < 0 || s.FileSize != input.Bytes || input.Rows != nil && s.Rows != *input.Rows || len(s.Columns) < 1 || len(s.Columns) > 1024 || len(r.Preview.Samples) > 20 || int64(len(r.Preview.Samples)) > s.Rows {
			return errWorkerProtocol
		}
		seen := map[string]bool{}
		for _, c := range s.Columns {
			if !workerColumnName(c.Path, workerMaxScalarTextBytes) || seen[c.Path] || len(c.PathComponents) < 1 || len(c.PathComponents) > 16 {
				return errWorkerProtocol
			}
			seen[c.Path] = true
			for _, part := range c.PathComponents {
				if !workerColumnName(part, 256) {
					return errWorkerProtocol
				}
			}
			if workerParquetPath(c.PathComponents) != c.Path {
				return errWorkerProtocol
			}
			switch c.PhysicalType {
			case "BOOLEAN", "INT32", "INT64", "INT96", "FLOAT", "DOUBLE", "BYTE_ARRAY", "FIXED_LEN_BYTE_ARRAY":
			default:
				return errWorkerProtocol
			}
			if len(c.ConvertedType) > 64 || len(c.Repetition) > 64 || c.LogicalType != nil && c.LogicalType.CountSetFieldsLogicalType() != 1 {
				return errWorkerProtocol
			}
			if c.Precision != nil && (*c.Precision < 1 || *c.Precision > 1024) || c.Scale != nil && c.Precision == nil || c.Scale != nil && (*c.Scale < 0 || *c.Scale > *c.Precision) || c.TypeLength != nil && *c.TypeLength < 1 {
				return errWorkerProtocol
			}
		}
		for _, row := range r.Preview.Samples {
			if len(row) != len(s.Columns) {
				return errWorkerProtocol
			}
			for i, cell := range row {
				if cell.validate() != nil {
					return errWorkerProtocol
				}
				column := s.Columns[i]
				scalar := !column.Repeated && len(column.PathComponents) == 1 && !credential.SensitiveName(column.Path)
				switch column.PhysicalType {
				case "BOOLEAN", "INT32", "INT64", "FLOAT", "DOUBLE", "INT96":
				default:
					scalar = false
				}
				if !scalar && cell.Kind != "redacted" {
					return errWorkerProtocol
				}
				if scalar && !workerCellMatchesColumn(cell, column) {
					return errWorkerProtocol
				}
			}
		}
		return nil
	}
	if request.Series != nil {
		if r.Points == nil || len(*r.Points) > analysis.MaxSeriesPoints {
			return errWorkerProtocol
		}
		q := request.Series.Query
		var previous time.Time
		maxPoints := int(math.Ceil(q.To.Sub(q.From).Seconds() / float64(q.BucketSeconds)))
		if len(*r.Points) != 0 && len(*r.Points) != maxPoints {
			return errWorkerProtocol
		}
		inputKind, unit := "", ""
		for _, point := range *r.Points {
			if inputKind == "" {
				inputKind, unit = point.InputKind, point.Unit
			}
			lineage, err := analysis.ScalarBucketDerivation(analysis.MetricPoint{Kind: point.InputKind}, q.BucketSeconds, q.Aggregation)
			if err != nil || !slices.Equal(lineage, point.Derivation) || point.InputKind != inputKind || point.Unit != unit || point.Population != "" || point.MetricVersion != "" || !point.ReceivedAt.IsZero() || point.Quality != analysis.QualityValid && point.Quality != analysis.QualityMissing || point.Kind != analysis.KindGauge || point.Source != q.DatasetID || point.RunID != "" || point.ScenarioID != "" || point.Host != "" || point.Service != "" || len(point.Dimensions) != 0 || point.RelativeTimeMS != nil || point.ActiveRuntimeMS != nil || point.Validate() != nil || point.ProjectID != q.ProjectID || point.DatasetID != q.DatasetID || point.MetricName != q.Metric || point.TimestampUTC.Before(q.From) || !point.TimestampUTC.Before(q.To) || !previous.IsZero() && !point.TimestampUTC.After(previous) || point.TimestampUTC.Sub(q.From)%(time.Duration(q.BucketSeconds)*time.Second) != 0 {
				return errWorkerProtocol
			}
			previous = point.TimestampUTC
		}
		return nil
	}
	if r.Normalize == nil {
		return errWorkerProtocol
	}
	return r.Normalize.validate(b, *request.Normalize)
}
func (c workerCell) validate() error {
	n := 0
	for _, p := range []bool{c.Boolean != nil, c.Number != nil, c.Timestamp != nil} {
		if p {
			n++
		}
	}
	switch c.Kind {
	case "null", "redacted", "non_finite":
		if n != 0 {
			return errWorkerProtocol
		}
	case "bool":
		if n != 1 || c.Boolean == nil {
			return errWorkerProtocol
		}
	case "timestamp":
		if n != 1 || c.Timestamp == nil || c.Timestamp.IsZero() || c.Timestamp.Location() != time.UTC || c.Timestamp.Year() < 1 || c.Timestamp.Year() > 9999 {
			return errWorkerProtocol
		}
	case "number":
		if n != 1 || c.Number == nil || len(*c.Number) > 32 {
			return errWorkerProtocol
		}
		decoder := json.NewDecoder(bytes.NewReader([]byte(*c.Number)))
		decoder.UseNumber()
		token, err := decoder.Token()
		if err != nil {
			return errWorkerProtocol
		}
		if _, ok := token.(json.Number); !ok {
			return errWorkerProtocol
		}
		if !json.Valid([]byte(*c.Number)) || strings.TrimSpace(*c.Number) != *c.Number {
			return errWorkerProtocol
		}
		number, err := strconv.ParseFloat(*c.Number, 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return errWorkerProtocol
		}
	default:
		return errWorkerProtocol
	}
	return nil
}
func (r workerNormalizeResult) validate(b workerBootstrap, op workerNormalizeOperation) error {
	if r.Accepted < 0 || r.Rejected < 0 || r.InputRows < 0 || len(r.Outputs) < 1 || len(r.Outputs) > workerMaxOutputs || len(r.Rejections) > 1024 || r.Rejected > 0 && !op.AllowRejected {
		return errWorkerProtocol
	}
	total, err := workerAdd(r.Accepted, r.Rejected)
	if err != nil || r.InputRows > math.MaxInt64/int64(len(op.Mapping.Measures)) || total != r.InputRows*int64(len(op.Mapping.Measures)) {
		return errWorkerProtocol
	}
	knownRows := true
	expectedRows := int64(0)
	for _, id := range op.InputHandles {
		input, _ := b.handle(id, "input")
		if input.Rows == nil {
			knownRows = false
			continue
		}
		expectedRows, err = workerAdd(expectedRows, *input.Rows)
		if err != nil {
			return err
		}
	}
	if knownRows && expectedRows != r.InputRows {
		return errWorkerProtocol
	}
	plans := map[string]workerHandle{}
	for _, id := range op.OutputHandles {
		for _, h := range b.Handles {
			if h.Handle == id {
				plans[id] = h
			}
		}
	}
	actual := map[string]workerOutput{}
	normalizedRows, rejectionRows, bytes := int64(0), int64(0), int64(0)
	for _, out := range r.Outputs {
		plan, ok := plans[out.Handle]
		if !ok || actual[out.Handle].Handle != "" || !workerHex(out.SHA256, 32) || out.Bytes < 12 || out.Bytes > plan.Bytes || out.Rows < 0 {
			return errWorkerProtocol
		}
		actual[out.Handle] = out
		bytes, err = workerAdd(bytes, out.Bytes)
		if err != nil || bytes > b.Limits.OutputBytes {
			return errWorkerProtocol
		}
		if plan.Role == "normalized" {
			normalizedRows, err = workerAdd(normalizedRows, out.Rows)
		} else {
			rejectionRows, err = workerAdd(rejectionRows, out.Rows)
		}
		if err != nil {
			return err
		}
	}
	if normalizedRows != r.Accepted || rejectionRows != r.Rejected {
		return errWorkerProtocol
	}
	summaries := map[string]RejectionSummary{}
	rejected := int64(0)
	for _, summary := range r.Rejections {
		if summary.File < 0 || summary.File >= len(op.InputHandles) || summary.Measure < 0 || summary.Measure >= len(op.Mapping.Measures) || summary.Count < 1 || len(summary.Rows) != min(100, int(summary.Count)) {
			return errWorkerProtocol
		}
		id := op.InputHandles[summary.File]
		key := id + "/" + strconv.Itoa(summary.Measure)
		if _, ok := summaries[key]; ok {
			return errWorkerProtocol
		}
		summaries[key] = summary
		input, _ := b.handle(id, "input")
		for j, row := range summary.Rows {
			if row < 0 || input.Rows != nil && row >= *input.Rows || j > 0 && row <= summary.Rows[j-1] {
				return errWorkerProtocol
			}
		}
		rejected, err = workerAdd(rejected, summary.Count)
		if err != nil {
			return err
		}
	}
	if rejected != r.Rejected {
		return errWorkerProtocol
	}

	inferred := map[string]int64{}
	for id, plan := range plans {
		out, present := actual[id]
		key := plan.InputHandle + "/" + strconv.Itoa(plan.Measure)
		summary := summaries[key]
		if plan.Role == "normalized" && !present || plan.Role == "rejection" && (summary.Count > 0 && !present || present && out.Rows != summary.Count) {
			return errWorkerProtocol
		}
		if plan.Role != "normalized" {
			continue
		}
		slotRows, err := workerAdd(out.Rows, summary.Count)
		if err != nil {
			return errWorkerProtocol
		}
		if previous, known := inferred[plan.InputHandle]; known && previous != slotRows {
			return errWorkerProtocol
		}
		inferred[plan.InputHandle] = slotRows
		input, _ := b.handle(plan.InputHandle, "input")
		if input.Rows != nil && slotRows != *input.Rows {
			return errWorkerProtocol
		}
	}
	inferredTotal := int64(0)
	for _, id := range op.InputHandles {
		rows, present := inferred[id]
		if !present {
			return errWorkerProtocol
		}
		inferredTotal, err = workerAdd(inferredTotal, rows)
		if err != nil {
			return errWorkerProtocol
		}
	}
	if inferredTotal != r.InputRows {
		return errWorkerProtocol
	}
	for _, summary := range r.Rejections {
		rows := inferred[op.InputHandles[summary.File]]
		for _, ordinal := range summary.Rows {
			if ordinal >= rows {
				return errWorkerProtocol
			}
		}
	}

	return nil
}

type workerProtocolState struct {
	bootstrap          workerBootstrap
	request            *workerRequest
	stage              string
	cancelSequence     uint64
	cancelAcknowledged bool
}

func newWorkerProtocolState(b workerBootstrap) (*workerProtocolState, error) {
	if b.validate() != nil {
		return nil, errWorkerProtocol
	}
	body, err := marshalWorkerJSON(b)
	if err != nil {
		return nil, errWorkerProtocol
	}
	var clone workerBootstrap
	if decodeWorkerJSON(body, &clone) != nil {
		return nil, errWorkerProtocol
	}
	return &workerProtocolState{bootstrap: clone, stage: "hello"}, nil
}
func (s *workerProtocolState) begin(request workerRequest) error {
	if s.stage != "ready" || request.validate(s.bootstrap) != nil {
		return errWorkerProtocol
	}
	body, err := marshalWorkerJSON(request)
	if err != nil {
		return errWorkerProtocol
	}
	var clone workerRequest
	if decodeWorkerJSON(body, &clone) != nil {
		return errWorkerProtocol
	}
	s.request = &clone
	s.stage = "running"
	return nil
}
func (s *workerProtocolState) cancel(sequence uint64) error {
	if s.stage != "running" || s.cancelSequence != 0 || sequence == 0 {
		return errWorkerProtocol
	}
	s.cancelSequence = sequence
	return nil
}
func (s *workerProtocolState) accept(frame workerFrame) error {
	if frame.validate() != nil || frame.Identity != s.bootstrap.Identity {
		return errWorkerProtocol
	}
	switch frame.Kind {
	case "hello":
		if s.stage != "hello" {
			return errWorkerProtocol
		}
		s.stage = "ready"
		return nil
	case "cancel_ack":
		if s.stage != "running" || s.cancelSequence == 0 || s.cancelAcknowledged || frame.CancelAck.Sequence != s.cancelSequence {
			return errWorkerProtocol
		}
		s.cancelAcknowledged = true
		return nil
	case "result":
		if s.stage != "running" || s.request == nil || frame.Result.validate(s.bootstrap, *s.request) != nil {
			return errWorkerProtocol
		}
		if frame.Result.Status == "ok" && s.cancelSequence != 0 || frame.Result.Status == "cancelled" && (s.cancelSequence == 0 || !s.cancelAcknowledged || frame.Result.CancelSequence != s.cancelSequence) {
			return errWorkerProtocol
		}
		s.stage = "terminal"
		return nil
	default:
		return errWorkerProtocol
	}
}

func workerCellMatchesColumn(cell workerCell, column analysis.ParquetColumn) bool {
	if cell.Kind == "null" || cell.Kind == "redacted" {
		return true
	}
	timestamp := column.PhysicalType == "INT96" || column.ConvertedType == "DATE" || column.ConvertedType == "TIMESTAMP_MILLIS" || column.ConvertedType == "TIMESTAMP_MICROS" || column.LogicalType != nil && (column.LogicalType.DATE != nil || column.LogicalType.TIMESTAMP != nil)
	if cell.Kind == "timestamp" {
		return timestamp
	}
	switch column.PhysicalType {
	case "BOOLEAN":
		return cell.Kind == "bool"
	case "INT32", "INT64":
		return cell.Kind == "number"
	case "FLOAT", "DOUBLE":
		return cell.Kind == "number" || cell.Kind == "non_finite"
	default:
		return false
	}
}

// Match analysis.flattenParquetSchema: display paths escape literal dots and
// backslashes, while PathComponents retain exact physical field identities.
func workerParquetPath(parts []string) string {
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = strings.ReplaceAll(strings.ReplaceAll(part, "\\", "\\\\"), ".", "\\.")
	}
	return strings.Join(escaped, ".")
}

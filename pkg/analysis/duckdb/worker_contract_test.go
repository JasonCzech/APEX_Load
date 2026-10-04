package duckdb

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func workerTestBootstrap(t *testing.T) workerBootstrap {
	t.Helper()
	rows := int64(3)
	b := workerBootstrap{
		Identity: workerIdentity{Version: 1, BuildID: strings.Repeat("a", 64), AttemptID: strings.Repeat("b", 32), Nonce: strings.Repeat("c", 64)},
		Limits:   workerLimits{Threads: 2, MemoryBytes: 2 << 30, SpillBytes: 8 << 30, InputBytes: 1024, OutputBytes: 2048, RuntimeMS: 900000},
		Handles: []workerHandle{
			{Handle: strings.Repeat("1", 32), Role: "input", RelativeName: "input-0000.parquet", Bytes: 100, SHA256: strings.Repeat("d", 64), Rows: &rows},
			{Handle: strings.Repeat("2", 32), Role: "normalized", RelativeName: "part-0000-00.parquet", Bytes: 1000, InputHandle: strings.Repeat("1", 32)},
			{Handle: strings.Repeat("3", 32), Role: "rejection", RelativeName: "reject-0000-00.parquet", Bytes: 1000, InputHandle: strings.Repeat("1", 32)},
		},
	}
	digest, err := b.manifestDigest()
	if err != nil {
		t.Fatal(err)
	}
	b.Identity.ManifestDigest = digest
	if err := b.validate(); err != nil {
		t.Fatal(err)
	}
	return b
}
func workerTestRequest(b workerBootstrap) workerRequest {
	return workerRequest{Normalize: &workerNormalizeOperation{
		ProjectID: "project", DatasetID: "dataset", InputHandles: []string{b.Handles[0].Handle}, OutputHandles: []string{b.Handles[1].Handle, b.Handles[2].Handle}, AllowRejected: true,
		Mapping: Mapping{Version: 1, Timestamp: Column{"at"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Name: "latency", Value: Column{"value"}, Kind: analysis.KindObservation, Unit: "ms"}}},
	}}
}
func workerTestNormalizeResult(b workerBootstrap) workerResult {
	return workerResult{Status: "ok", Normalize: &workerNormalizeResult{
		InputRows: 3, Accepted: 2, Rejected: 1,
		Outputs:    []workerOutput{{Handle: b.Handles[1].Handle, SHA256: strings.Repeat("e", 64), Bytes: 100, Rows: 2}, {Handle: b.Handles[2].Handle, SHA256: strings.Repeat("f", 64), Bytes: 100, Rows: 1}},
		Rejections: []RejectionSummary{{File: 0, Measure: 0, Count: 1, Rows: []int64{2}}},
	}}
}
func TestWorkerBootstrapFiniteManifestAndLimits(t *testing.T) {
	for name, change := range map[string]func(*workerBootstrap){
		"version":                 func(b *workerBootstrap) { b.Identity.Version = 2 },
		"build alias":             func(b *workerBootstrap) { b.Identity.BuildID = strings.Repeat("A", 64) },
		"attempt":                 func(b *workerBootstrap) { b.Identity.AttemptID = "foreign" },
		"nonce":                   func(b *workerBootstrap) { b.Identity.Nonce = "short" },
		"digest":                  func(b *workerBootstrap) { b.Identity.ManifestDigest = strings.Repeat("0", 64) },
		"off memory":              func(b *workerBootstrap) { b.Limits.MemoryBytes = 0 },
		"unbounded spill":         func(b *workerBootstrap) { b.Limits.SpillBytes = math.MaxInt64 },
		"threads":                 func(b *workerBootstrap) { b.Limits.Threads = 3 },
		"runtime":                 func(b *workerBootstrap) { b.Limits.RuntimeMS = 900001 },
		"input quota":             func(b *workerBootstrap) { b.Limits.InputBytes = 99 },
		"output quota":            func(b *workerBootstrap) { b.Limits.OutputBytes = 1999 },
		"duplicate handle":        func(b *workerBootstrap) { b.Handles[1].Handle = b.Handles[0].Handle },
		"duplicate name":          func(b *workerBootstrap) { b.Handles[1].RelativeName = b.Handles[0].RelativeName },
		"path traversal":          func(b *workerBootstrap) { b.Handles[0].RelativeName = "../input.parquet" },
		"Windows path":            func(b *workerBootstrap) { b.Handles[0].RelativeName = "C:\\input.parquet" },
		"nonregular role":         func(b *workerBootstrap) { b.Handles[0].Role = "directory" },
		"missing source digest":   func(b *workerBootstrap) { b.Handles[0].SHA256 = "" },
		"negative rows":           func(b *workerBootstrap) { v := int64(-1); b.Handles[0].Rows = &v },
		"unknown output source":   func(b *workerBootstrap) { b.Handles[1].InputHandle = strings.Repeat("4", 32) },
		"negative output measure": func(b *workerBootstrap) { b.Handles[1].Measure = -1 },
		"overflow bytes":          func(b *workerBootstrap) { b.Handles[0].Bytes = math.MaxInt64 },
	} {
		t.Run(name, func(t *testing.T) {
			b := workerTestBootstrap(t)
			change(&b)
			if name != "digest" {
				digest, _ := b.manifestDigest()
				b.Identity.ManifestDigest = digest
			}
			if b.validate() == nil {
				t.Fatal("forged/unbounded bootstrap accepted")
			}
		})
	}
}
func TestWorkerRequestOneOperationAndExactOutputPlan(t *testing.T) {
	b := workerTestBootstrap(t)
	if workerTestRequest(b).validate(b) != nil {
		t.Fatal("valid normalization plan rejected")
	}
	for name, change := range map[string]func(*workerRequest){
		"union":         func(r *workerRequest) { r.Preview = &workerPreviewOperation{InputHandle: b.Handles[0].Handle} },
		"foreign input": func(r *workerRequest) { r.Normalize.InputHandles[0] = strings.Repeat("4", 32) },
		"duplicate input": func(r *workerRequest) {
			r.Normalize.InputHandles = append(r.Normalize.InputHandles, r.Normalize.InputHandles[0])
		},
		"foreign output": func(r *workerRequest) { r.Normalize.OutputHandles[0] = strings.Repeat("4", 32) },
		"source output":  func(r *workerRequest) { r.Normalize.OutputHandles[0] = b.Handles[0].Handle },
		"missing output": func(r *workerRequest) { r.Normalize.OutputHandles = r.Normalize.OutputHandles[1:] },
		"duplicate output": func(r *workerRequest) {
			r.Normalize.OutputHandles = append(r.Normalize.OutputHandles, r.Normalize.OutputHandles[0])
		},
		"no rejection approval":         func(r *workerRequest) { r.Normalize.AllowRejected = false },
		"mapping version":               func(r *workerRequest) { r.Normalize.Mapping.Version = 2 },
		"mapping expression":            func(r *workerRequest) { r.Normalize.Mapping.Timestamp = nil },
		"unknown aggregation semantics": func(r *workerRequest) { r.Normalize.Mapping.Measures[0].Kind = "histogram" },
	} {
		t.Run(name, func(t *testing.T) {
			r := workerTestRequest(b)
			change(&r)
			if r.validate(b) == nil {
				t.Fatal("unapproved operation accepted")
			}
		})
	}
}
func TestWorkerNormalizationOutputCountsMembershipAndRejectionOrdinals(t *testing.T) {
	b := workerTestBootstrap(t)
	request := workerTestRequest(b)
	if workerTestNormalizeResult(b).validate(b, request) != nil {
		t.Fatal("qualified result rejected")
	}
	for name, change := range map[string]func(*workerResult){
		"unknown output":            func(r *workerResult) { r.Normalize.Outputs[0].Handle = strings.Repeat("4", 32) },
		"duplicate output":          func(r *workerResult) { r.Normalize.Outputs = append(r.Normalize.Outputs, r.Normalize.Outputs[0]) },
		"missing normalized output": func(r *workerResult) { r.Normalize.Outputs = r.Normalize.Outputs[1:] },
		"missing rejection output":  func(r *workerResult) { r.Normalize.Outputs = r.Normalize.Outputs[:1] },
		"too many bytes":            func(r *workerResult) { r.Normalize.Outputs[0].Bytes = 1001 },
		"wrong hash":                func(r *workerResult) { r.Normalize.Outputs[0].SHA256 = "../file" },
		"wrong accepted":            func(r *workerResult) { r.Normalize.Accepted = 1 },
		"wrong input rows":          func(r *workerResult) { r.Normalize.InputRows = 4 },
		"wrong physical count":      func(r *workerResult) { r.Normalize.Outputs[0].Rows = 3 },
		"unbounded count":           func(r *workerResult) { r.Normalize.Accepted = math.MaxInt64 },
		"negative count":            func(r *workerResult) { r.Normalize.Rejected = -1 },
		"wrong ordinal":             func(r *workerResult) { r.Normalize.Rejections[0].Rows[0] = 3 },
		"negative ordinal":          func(r *workerResult) { r.Normalize.Rejections[0].Rows[0] = -1 },
		"wrong file":                func(r *workerResult) { r.Normalize.Rejections[0].File = 1 },
		"wrong measure":             func(r *workerResult) { r.Normalize.Rejections[0].Measure = 1 },
		"wrong sampled count":       func(r *workerResult) { r.Normalize.Rejections[0].Rows = nil },
		"unqualified rejected":      func(r *workerResult) { r.Normalize.Rejections = nil },
	} {
		t.Run(name, func(t *testing.T) {
			r := workerTestNormalizeResult(b)
			change(&r)
			if r.validate(b, request) == nil {
				t.Fatal("contradictory output metadata accepted")
			}
		})
	}
}
func TestWorkerPreviewTypedSamplesAndClone(t *testing.T) {
	b := workerTestBootstrap(t)
	request := workerRequest{Preview: &workerPreviewOperation{InputHandle: b.Handles[0].Handle}}
	number := "9007199254740993"
	preview := workerResult{Status: "ok", Preview: &workerPreviewResult{Schema: analysis.ParquetSchemaPreview{Rows: 3, FileSize: 100, Columns: []analysis.ParquetColumn{{Path: "value", PathComponents: []string{"value"}, PhysicalType: "INT64"}}}, Samples: [][]workerCell{{{Kind: "number", Number: &number}}}}}
	if preview.validate(b, request) != nil {
		t.Fatal("exact numeric preview rejected")
	}
	frame := workerFrame{Identity: b.Identity, Kind: "result", Result: &preview}
	cloned, err := frame.clone()
	if err != nil {
		t.Fatal(err)
	}
	*cloned.Result.Preview.Samples[0][0].Number = "0"
	cloned.Result.Preview.Schema.Columns[0].PathComponents[0] = "other"
	if number != "9007199254740993" || preview.Preview.Schema.Columns[0].PathComponents[0] != "value" {
		t.Fatal("clone aliases caller samples/schema")
	}
	for _, value := range []string{"NaN", "Infinity", "1e999", "01", "true", "`"} {
		if (workerCell{Kind: "number", Number: &value}).validate() == nil {
			t.Fatal("nonfinite/malformed sample accepted")
		}
	}
	preview.Preview.Samples = append(preview.Preview.Samples, []workerCell{})
	if preview.validate(b, request) == nil {
		t.Fatal("sample/schema width contradiction accepted")
	}
}
func TestWorkerSeriesResultRetainsQueryScopeAndLineage(t *testing.T) {
	b := workerTestBootstrap(t)
	from := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	q := SeriesQuery{ProjectID: "project", DatasetID: "dataset", Metric: "latency", From: from, To: from.Add(time.Second), BucketSeconds: 1, Aggregation: "avg"}
	request := workerRequest{Series: &workerSeriesOperation{InputHandles: []string{b.Handles[0].Handle}, Query: q}}
	value := 1.0
	lineage, _ := analysis.ScalarBucketDerivation(analysis.MetricPoint{Kind: analysis.KindObservation}, 1, "avg")
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: q.ProjectID, DatasetID: q.DatasetID, Source: q.DatasetID, TimestampUTC: from, MetricName: q.Metric, Kind: analysis.KindGauge, InputKind: analysis.KindObservation, Derivation: lineage, Value: &value, Unit: "ms", Quality: analysis.QualityValid}
	points := []analysis.MetricPoint{point}
	result := workerResult{Status: "ok", Points: &points}
	if result.validate(b, request) != nil {
		t.Fatal("qualified series rejected")
	}
	for name, change := range map[string]func(*analysis.MetricPoint){
		"project":       func(p *analysis.MetricPoint) { p.ProjectID = "other" },
		"dataset":       func(p *analysis.MetricPoint) { p.DatasetID = "other" },
		"metric":        func(p *analysis.MetricPoint) { p.MetricName = "other" },
		"source":        func(p *analysis.MetricPoint) { p.Source = "other" },
		"managed run":   func(p *analysis.MetricPoint) { p.RunID = "run" },
		"out of range":  func(p *analysis.MetricPoint) { p.TimestampUTC = q.To },
		"off bucket":    func(p *analysis.MetricPoint) { p.TimestampUTC = from.Add(100 * time.Millisecond) },
		"wrong lineage": func(p *analysis.MetricPoint) { p.Derivation = nil },
		"nonfinite":     func(p *analysis.MetricPoint) { v := math.NaN(); p.Value = &v },
	} {
		t.Run(name, func(t *testing.T) {
			p := point
			change(&p)
			values := []analysis.MetricPoint{p}
			r := workerResult{Status: "ok", Points: &values}
			if r.validate(b, request) == nil {
				t.Fatal("unqualified result series accepted")
			}
		})
	}
	points = append(points, point)
	if result.validate(b, request) == nil {
		t.Fatal("duplicate bucket accepted")
	}
}
func TestWorkerHandshakeCancellationAndExtraFrames(t *testing.T) {
	b := workerTestBootstrap(t)
	state, err := newWorkerProtocolState(b)
	if err != nil {
		t.Fatal(err)
	}
	request := workerTestRequest(b)
	if state.begin(request) == nil {
		t.Fatal("operation before authenticated hello accepted")
	}
	hello := workerFrame{Identity: b.Identity, Kind: "hello"}
	wrong := hello
	wrong.Identity.Nonce = strings.Repeat("d", 64)
	if state.accept(wrong) == nil {
		t.Fatal("wrong nonce authenticated")
	}
	if state.accept(hello) != nil || state.begin(request) != nil {
		t.Fatal("valid one-operation handshake rejected")
	}
	request.Normalize.Mapping.Timestamp[0] = "changed"
	if state.request.Normalize.Mapping.Timestamp[0] != "at" {
		t.Fatal("owned request aliases caller mapping")
	}
	if state.begin(workerTestRequest(b)) == nil || state.accept(hello) == nil {
		t.Fatal("second request/hello accepted")
	}
	if state.cancel(7) != nil || state.cancel(8) == nil {
		t.Fatal("cancellation sequence not owned once")
	}
	success := workerTestNormalizeResult(b)
	if state.accept(workerFrame{Identity: b.Identity, Kind: "result", Result: &success}) == nil {
		t.Fatal("success after cancellation accepted")
	}
	cancelled := workerResult{Status: "cancelled", Code: "cancelled", CancelSequence: 7}
	if state.accept(workerFrame{Identity: b.Identity, Kind: "result", Result: &cancelled}) == nil {
		t.Fatal("result fabricated acknowledgement")
	}
	if state.accept(workerFrame{Identity: b.Identity, Kind: "cancel_ack", CancelAck: &workerCancelAck{Sequence: 8}}) == nil {
		t.Fatal("foreign cancellation acknowledgement accepted")
	}
	if state.accept(workerFrame{Identity: b.Identity, Kind: "cancel_ack", CancelAck: &workerCancelAck{Sequence: 7}}) != nil {
		t.Fatal("owned cancellation acknowledgement rejected")
	}
	if state.accept(workerFrame{Identity: b.Identity, Kind: "result", Result: &cancelled}) != nil {
		t.Fatal("consistent cancellation rejected")
	}
	if state.accept(workerFrame{Identity: b.Identity, Kind: "result", Result: &cancelled}) == nil {
		t.Fatal("trailing frame accepted")
	}
}
func TestWorkerStrictJSONRejectsAmbiguousAndExecutableFields(t *testing.T) {
	b := workerTestBootstrap(t)
	frame := workerFrame{Identity: b.Identity, Kind: "request", Request: &workerRequest{Preview: &workerPreviewOperation{InputHandle: b.Handles[0].Handle}}}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(string(body), `"kind":"request"`, `"kind":"request","kind":"hello"`, 1),
		strings.Replace(string(body), `"kind":"request"`, `"Kind":"request"`, 1),
		strings.Replace(string(body), `"input_handle":`, `"sql":"select 1","input_handle":`, 1),
		strings.Replace(string(body), `"input_handle":`, `"path":"/etc/passwd","input_handle":`, 1),
		strings.Replace(string(body), `"input_handle":`, `"env":{"TOKEN":"secret"},"input_handle":`, 1),
		strings.Replace(string(body), `"input_handle":`, `"credential":"secret","input_handle":`, 1),
		string(body) + "{}",
	} {
		var decoded workerFrame
		if decodeWorkerJSON([]byte(bad), &decoded) == nil {
			t.Fatal("ambiguous/executable operation JSON accepted")
		}
	}
	request := workerTestRequest(b)
	request.Normalize.InputHandles = make([]string, 1025)
	oversized, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	destination := workerRequest{Preview: &workerPreviewOperation{InputHandle: "unchanged"}}
	if decodeWorkerJSON(oversized, &destination) == nil || destination.Preview.InputHandle != "unchanged" {
		t.Fatal("preallocation count guard mutated typed destination")
	}
}

type workerHeaderOnlyReader struct {
	body  []byte
	reads int
}

func (r *workerHeaderOnlyReader) Read(p []byte) (int, error) {
	r.reads++
	if len(r.body) == 0 {
		return 0, errors.New("payload must not be read")
	}
	n := copy(p, r.body)
	r.body = r.body[n:]
	return n, nil
}

type workerPartialWriter struct{ bytes.Buffer }

func (w *workerPartialWriter) Write(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return w.Buffer.Write(p)
}
func TestWorkerLengthPrefixAndPartialIO(t *testing.T) {
	for _, size := range []uint32{0, 1, workerMaxFrameBytes + 1, math.MaxUint32} {
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, size)
		reader := &workerHeaderOnlyReader{body: header}
		if _, err := readWorkerFrame(reader); err != errWorkerProtocol || reader.reads != 1 {
			t.Fatal("oversized frame read/allocated payload before rejection")
		}
	}
	b := workerTestBootstrap(t)
	var bootstrapBytes bytes.Buffer
	if err := writeWorkerBootstrap(&bootstrapBytes, b); err != nil {
		t.Fatal(err)
	}
	if decoded, err := readWorkerBootstrap(&bootstrapBytes); err != nil || decoded.Identity != b.Identity {
		t.Fatal("private bootstrap framing failed", err)
	}
	hello := workerFrame{Identity: b.Identity, Kind: "hello"}
	writer := &workerPartialWriter{}
	if err := writeWorkerFrame(writer, hello); err != nil {
		t.Fatal(err)
	}
	decoded, err := readWorkerFrame(&writer.Buffer)
	if err != nil || decoded.Identity != b.Identity || decoded.Kind != "hello" {
		t.Fatal("partial framing round trip failed", err)
	}
	if _, err := readWorkerFrame(&writer.Buffer); !errors.Is(err, io.EOF) {
		t.Fatal("frame stream EOF not distinguishable")
	}
	tooMany := workerFrame{Identity: b.Identity, Kind: "request", Request: &workerRequest{Series: &workerSeriesOperation{InputHandles: make([]string, 1025)}}}
	writer.Reset()
	if writeWorkerFrame(writer, tooMany) == nil || writer.Len() != 0 {
		t.Fatal("writer allocated/wrote excessive typed frame")
	}
}

func TestWorkerAllCollectionCapsBeforeTypedAllocation(t *testing.T) {
	b := workerTestBootstrap(t)
	points := make([]analysis.MetricPoint, 10001)
	for name, result := range map[string]workerResult{
		"schema columns":      {Status: "ok", Preview: &workerPreviewResult{Schema: analysis.ParquetSchemaPreview{Columns: make([]analysis.ParquetColumn, 1025)}}},
		"sample rows":         {Status: "ok", Preview: &workerPreviewResult{Samples: make([][]workerCell, 21)}},
		"sample cells":        {Status: "ok", Preview: &workerPreviewResult{Samples: [][]workerCell{make([]workerCell, 1025)}}},
		"series points":       {Status: "ok", Points: &points},
		"rejection summaries": {Status: "ok", Normalize: &workerNormalizeResult{Rejections: make([]RejectionSummary, 1025)}},
		"rejection ordinals":  {Status: "ok", Normalize: &workerNormalizeResult{Rejections: []RejectionSummary{{Rows: make([]int64, 101)}}}},
	} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(workerFrame{Identity: b.Identity, Kind: "result", Result: &result})
			if err != nil {
				t.Fatal(err)
			}
			destination := workerFrame{Kind: "unchanged"}
			if decodeWorkerJSON(body, &destination) == nil || destination.Kind != "unchanged" {
				t.Fatal("collection cap checked after typed allocation/mutation")
			}
		})
	}
	for _, body := range []string{
		`{"sequence":18446744073709551616}`,
		`{"sequence":1e0}`,
		`{"sequence":-1}`,
		`{"sequence":"1"}`,
	} {
		ack := workerCancelAck{Sequence: 7}
		if decodeWorkerJSON([]byte(body), &ack) == nil || ack.Sequence != 7 {
			t.Fatal("invalid scalar mutated typed destination")
		}
	}
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "exact_ascii_bound", value: strings.Repeat("a", workerMaxScalarTextBytes), valid: true},
		{name: "one_ascii_byte_beyond", value: strings.Repeat("a", workerMaxScalarTextBytes+1)},
		{name: "exact_escaped_ascii_bound", value: strings.Repeat(`\u0061`, workerMaxScalarTextBytes), valid: true},
		{name: "one_escaped_ascii_byte_beyond", value: strings.Repeat(`\u0061`, workerMaxScalarTextBytes+1)},
		{name: "unmatched_high_surrogate", value: `\uD800`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"text":"` + tc.value + `"}`)
			if actual := workerRawScalarBounds(body); actual != tc.valid {
				t.Fatal("raw scalar bound differs", actual, tc.valid)
			}
			if !tc.valid {
				destination := workerCell{Kind: "unchanged"}
				typed := []byte(`{"kind":"number","number":"` + tc.value + `"}`)
				if decodeWorkerJSON(typed, &destination) == nil || destination.Kind != "unchanged" {
					t.Fatal("invalid raw scalar mutated typed destination")
				}
			}
		})
	}
	if !workerRawScalarBounds([]byte(`{"text":"\uD83D\uDE00"}`)) {
		t.Fatal("valid surrogate pair rejected")
	}
}

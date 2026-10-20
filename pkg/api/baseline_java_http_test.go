package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func baselineJavaFixture(prepared int64) *BaselineMeasurement {
	var s *protocol.JavaHTTPSummary
	encoded, decoded, response, base64 := int64(8), int64(4), int64(3), int64(4)
	decode, encode := 1.0, 2.0
	for range 100 {
		protocol.MergeJavaHTTPDiagnostic(&s, &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "decoded", RequestEncodedBytes: encoded, RequestDecodedBytes: &decoded, PreparedRequestBodyBytes: &prepared, AppliedRequestCompression: "none", ResponseState: "complete", ResponseRetainedBytes: &response, ResponseBase64Bytes: &base64, Base64DecodeMs: &decode, Base64EncodeMs: &encode, EnclosingMs: 10, ObjectFootprint: "unavailable_embedded_opaque_bytes"})
	}
	return &BaselineMeasurement{Samples: 100, Successes: 100, JavaHTTP: s}
}
func TestJavaHTTPBaselineAccumulatorOwnsAndQualifiesActionSummary(t *testing.T) {
	m := baselineJavaFixture(4)
	key := baselineTransactionKey{"group", "java_http", "request"}
	a := &baselineTransactionAccumulator{javaHTTP: m.JavaHTTP, count: 101, digest: engine.NewDurationSketch()}
	run := &baselineComparisonRun{manifest: &RunArchiveManifest{}, windows: []BaselineLoadWindow{{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 100}}}, transactions: map[baselineTransactionKey]map[int]*baselineTransactionAccumulator{key: {0: a}}}
	got := baselineTransactionMeasurement(run, key, 0)
	if got.JavaHTTP == nil || !got.JavaHTTP.Incomplete || got.JavaHTTP.Samples != 100 || got.Samples != 101 {
		t.Fatalf("mixed legacy evidence %+v", got)
	}
	got.JavaHTTP.RequestEncoded.SumBytes = 0
	if a.javaHTTP.RequestEncoded.SumBytes != 800 {
		t.Fatal("baseline snapshot aliases accumulator")
	}
	a.count = 100
	run.manifest.CSVExportIncomplete = "partial source"
	if got := baselineTransactionMeasurement(run, key, 0); got.JavaHTTP == nil || !got.JavaHTTP.Incomplete {
		t.Fatal("partial archive was qualified", got)
	}
	run.manifest.CSVExportIncomplete = ""
	run.incomplete = true
	if got := baselineTransactionMeasurement(run, key, 0); got.JavaHTTP == nil || !got.JavaHTTP.Incomplete {
		t.Fatal("lost telemetry was qualified", got)
	}
	run.incomplete = false
	run.manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: "required", Sources: []RunArchiveSource{{Target: "synthetic-generator", Collected: true}}}
	if got := baselineTransactionMeasurement(run, key, 0); got.JavaHTTP == nil || !got.JavaHTTP.Incomplete {
		t.Fatal("source collection was treated as complete action-window evidence", got)
	}
	a.javaHTTP = nil
	if baselineTransactionMeasurement(run, key, 0).JavaHTTP != nil {
		t.Fatal("legacy diagnostics invented")
	}
}
func TestJavaHTTPBaselineFindingsPreservePairedUnitsAndQualification(t *testing.T) {
	a, b := baselineJavaFixture(8), baselineJavaFixture(4)
	if !a.JavaHTTP.Valid() || !b.JavaHTTP.Valid() {
		t.Fatal("invalid fixture", a.JavaHTTP, b.JavaHTTP)
	}
	window := &BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 100}, Load: 10}
	row := BaselineComparisonRow{ID: "java", Name: "request", Group: "group", Protocol: "java_http", Source: "action", Unit: "ms", Current: a, Window: window, Baselines: map[string]BaselineComparisonCell{"b": {Measurement: b, Window: window}}}
	out := &BaselineComparisonSnapshot{Policy: defaultBaselineComparisonPolicy(), Baselines: []BaselineRunIdentity{{ID: "b"}}, Transactions: []BaselineComparisonRow{row}}
	buildBaselineFindings(out)
	found := map[string]bool{}
	for _, f := range out.Findings {
		if strings.HasPrefix(f.Metric, "Java ") {
			found[f.Metric] = true
			if f.Status != "review" || !strings.Contains(f.Message, "Advisory evidence only") {
				t.Fatal("unqualified acceptance claim", f)
			}
			if strings.Contains(f.Metric, "body mean") && !strings.Contains(f.Message, "bytes") {
				t.Fatal("wrong byte unit", f)
			}
			if strings.Contains(f.Metric, "Prepared / decoded") && !strings.Contains(f.Message, "2 vs 1 ratio") {
				t.Fatal("ratio", f)
			}
		}
	}
	if !found["Java Prepared request body mean"] || !found["Java Prepared / decoded request"] {
		t.Fatal("missing Java findings", found)
	}
	for name, change := range map[string]func(*BaselineComparisonRow){
		"legacy":  func(r *BaselineComparisonRow) { r.Current = &BaselineMeasurement{Samples: 100} },
		"partial": func(r *BaselineComparisonRow) { r.Current = baselineJavaFixture(8); r.Current.Samples++ },
		"version": func(r *BaselineComparisonRow) { r.Current = baselineJavaFixture(8); r.Current.JavaHTTP.Version = 9 },
		"compression": func(r *BaselineComparisonRow) {
			r.Current = baselineJavaFixture(8)
			r.Current.JavaHTTP.RequestCompressionNone = 0
			r.Current.JavaHTTP.RequestCompressionGzip = 100
		},
		"workload": func(r *BaselineComparisonRow) {
			r.Baselines = map[string]BaselineComparisonCell{"b": {Measurement: b, Window: window, Qualification: []string{"Different workload"}}}
		},
		"window": func(r *BaselineComparisonRow) { r.Window = nil },
	} {
		t.Run(name, func(t *testing.T) {
			copy := row
			change(&copy)
			fs := baselineJavaHTTPFindings(copy, "b", copy.Baselines["b"])
			if len(fs) != 1 || fs[0].Status != "unverified" {
				t.Fatal(fs)
			}
		})
	}
}

func TestJavaHTTPBaselineReadsArchivedActionsWithoutBusinessWrapperDuplication(t *testing.T) {
	server, store := baselineTestServer(t)
	run := comparisonArchiveFixture(t, server, store, "java-actual-actions", 10, 20)
	record := server.runner.archives[run.ID]
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	write := func(missing bool) {
		f, err := os.Create(filepath.Join(record.ArchivePath, manifest.TransactionsFile))
		if err != nil {
			t.Fatal(err)
		}
		enc := json.NewEncoder(f)
		for i := range 100 {
			d := nativeJavaDiagnostic(8, 4)
			if missing && i == 99 {
				d = nil
			}
			tx := TransactionEvent{Timestamp: manifest.Provenance.StartedAt.Add(31*time.Second + time.Duration(i)*500*time.Millisecond), ActionName: "request", Group: "checkout", Population: "action", Success: true, DurationMs: 10, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: d}}
			if err := enc.Encode(tx); err != nil {
				t.Fatal(err)
			}
			tx.Population = "transaction"
			tx.BusinessTransaction = true
			tx.SourceTransaction = "wrapper"
			if err := enc.Encode(tx); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, missing := range []bool{false, true} {
		write(missing)
		read, err := server.readBaselineComparisonRun(context.Background(), run, BaselineComparisonRequest{Population: "action"}, defaultBaselineComparisonPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if len(read.transactions) != 1 {
			t.Fatal("wrapper entered action population", read.transactions)
		}
		for key, cells := range read.transactions {
			for index := range cells {
				m := baselineTransactionMeasurement(read, key, index)
				want := int64(100)
				if missing {
					want--
				}
				if m.Samples != 100 || m.JavaHTTP == nil || m.JavaHTTP.Samples != want || m.JavaHTTP.Incomplete != missing || m.JavaHTTP.PreparedRequestBody.SumBytes != 4*want {
					t.Fatalf("archive population: %+v", m)
				}
			}
		}
	}
	read, err := server.readBaselineComparisonRun(context.Background(), run, BaselineComparisonRequest{Population: "transaction"}, defaultBaselineComparisonPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for key, cells := range read.transactions {
		for index := range cells {
			if baselineTransactionMeasurement(read, key, index).JavaHTTP != nil {
				t.Fatal("business wrapper got Java evidence")
			}
		}
	}
}

func TestJavaHTTPBaselineExportsRetainMeasurementsAndEscapeLabels(t *testing.T) {
	row := BaselineComparisonRow{ID: "java", Name: "<script>request</script>", Current: baselineJavaFixture(4), Baselines: map[string]BaselineComparisonCell{"b": {Measurement: baselineJavaFixture(8)}}}
	snapshot := &BaselineComparisonSnapshot{Current: BaselineRunIdentity{ID: "current"}, Baselines: []BaselineRunIdentity{{ID: "b"}}, Transactions: []BaselineComparisonRow{row}}
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{BaselineComparison: snapshot}}
	renderer := baselineComparisonReportRenderer()
	html, err := renderer.HTML(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script>") || !strings.Contains(string(html), "&lt;script&gt;") || !strings.Contains(string(html), "Prepared HTTP request body") {
		t.Fatal("HTML evidence/escaping", html)
	}
	var lines []string
	renderer.PDF(data, &lines)
	if !strings.Contains(strings.Join(lines, "\n"), "JVM object footprint is unavailable") {
		t.Fatal("PDF lost qualification", lines)
	}
	var csv []string
	err = renderer.CSV(data, func(a, b, c, d string) error { csv = append(csv, d); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(csv, "\n"), "request_compression_none") {
		t.Fatal("structured CSV lost compression")
	}
	for range 20 {
		snapshot.Transactions = append(snapshot.Transactions, row)
	}
	lines = baselineJavaHTTPReportLines(snapshot)
	if len(lines) > 201 || !strings.Contains(lines[len(lines)-1], "Additional Java") {
		t.Fatal("text export bound", len(lines))
	}
}

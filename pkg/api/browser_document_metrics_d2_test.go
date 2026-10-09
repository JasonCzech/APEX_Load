package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

const d2Generation = "0123456789abcdef0123456789abcdef"

func d2Cohort(version string) *protocol.BrowserRuntimeCohort {
	yes, no := true, false
	return &protocol.BrowserRuntimeCohort{Version: 1, BrowserVersion: version, Platform: "linux", ViewportWidth: 1440, ViewportHeight: 900, Headless: &yes, Mobile: &no, Touch: &no, DeviceScaleFactor: 1, CPUThrottleRate: 1, ShapingApplied: true, CacheEnabled: &yes, CacheContentURLs: &yes, CheckNewerVersions: &no, NewUserPolicy: "persistent_context", NewUserPolicyApplied: true}
}

// d2Document is one document lifecycle metric's evidence records: progress
// provisional revisions, then its final eligible value (or none when final is
// false), each in its own evidence record as a generator would send them.
type d2Document struct {
	id       int
	metric   string
	engine   string
	cohort   *protocol.BrowserRuntimeCohort
	at       time.Time
	value    float64
	progress int
	final    bool
	group    string
}

func (d d2Document) records() []*protocol.BrowserDocumentEvidence {
	doc, life := fmt.Sprintf("%s-document-%d", d2Generation, d.id), fmt.Sprintf("%s-lifecycle-%d", d2Generation, d.id)
	unit := "ms"
	if d.metric == "cls" {
		unit = "score"
	}
	engine := d.engine
	if engine == "" {
		engine = "chromium"
	}
	observation := func(revision uint64, state, boundary string, value float64) *protocol.BrowserDocumentEvidence {
		v := value
		o := protocol.BrowserDocumentObservation{Version: protocol.BrowserEvidenceVersion, SourceGeneration: d2Generation, DocumentID: doc, LifecycleID: life,
			FrameID: d2Generation + "-frame-1", NavigationID: fmt.Sprintf("%s-navigation-%d", d2Generation, d.id), MeasurementID: doc + "/" + life + "/" + d.metric,
			Revision: revision, Metric: d.metric, Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: unit, Engine: engine,
			DeviceProfile: "desktop", Cohort: d.cohort.Clone(), Group: d.group, FrameScope: "main_frame", LifecyclePhase: "navigation", State: state, Boundary: boundary,
			WindowStart: d.at.Add(-time.Second), WindowEnd: d.at, CapturedAt: d.at, Available: true, Qualified: state == "final", Value: &v}
		if d.metric == "inp" {
			o.InteractionCount = 1
		}
		return &protocol.BrowserDocumentEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: d2Generation, Observations: []protocol.BrowserDocumentObservation{o}}
	}
	var out []*protocol.BrowserDocumentEvidence
	for i := 1; i <= d.progress; i++ {
		// Progress snapshots carry other values; none may enter a distribution.
		out = append(out, observation(uint64(i), "provisional", "progress", d.value*10+float64(i)))
	}
	if d.final {
		out = append(out, observation(uint64(d.progress+1), "final", "graceful_end", d.value))
	}
	return out
}

// attachD2Documents writes documents as an archive's browser document
// sidecar with the hashes its manifest declares.
func attachD2Documents(t *testing.T, record *storage.RunArchiveRecord, documents ...d2Document) {
	t.Helper()
	var body bytes.Buffer
	count := int64(0)
	for _, d := range documents {
		for _, evidence := range d.records() {
			if evidence.Clone() == nil {
				t.Fatalf("invalid fixture evidence %+v", evidence.Observations[0])
			}
			line, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			body.Write(append(line, '\n'))
			count++
		}
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, browserDocumentsFileName), body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body.Bytes())
	parquet := sha256.Sum256([]byte("parquet"))
	manifest.BrowserDocumentsFile, manifest.BrowserDocumentsParquetFile = browserDocumentsFileName, browserDocumentsParquetFileName
	manifest.BrowserDocumentRecordCount = count
	manifest.BrowserDocumentsSHA256, manifest.BrowserDocumentsParquetSHA256 = hex.EncodeToString(sum[:]), hex.EncodeToString(parquet[:])
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeBrowserDocumentSeriesCountsEachDocumentOnce(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	cohort := d2Cohort("141.0")
	var documents []d2Document
	for i, value := range []float64{100, 200, 300, 400} {
		// Ten progress snapshots of each document precede its final value.
		documents = append(documents, d2Document{id: i + 1, metric: "lcp", cohort: cohort, at: start.Add(time.Duration(2+i) * time.Second), value: value, progress: 10, final: true})
	}
	pending := d2Document{id: 9, metric: "lcp", cohort: cohort, at: start.Add(3 * time.Second), value: 5000, progress: 3}
	attachD2Documents(t, record, append(documents, pending)...)
	q := storage.AnalyticsQuery{Population: analysis.PopulationBrowserDocument, Metric: "document_lcp", Aggregation: "p75", BucketSeconds: 10, EndSeconds: 20}
	if err := storage.ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal(err)
	}
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	// Each document counts once and only its final value enters: progress
	// snapshots carried ten times larger values.
	if series.Buckets[0].Count != 4 || series.Buckets[1].Value != nil || series.Buckets[1].Count != 0 || series.Unit != "" {
		t.Fatalf("progress snapshots were counted: %+v", series)
	}
	if v := series.Buckets[0].Value; v == nil || *v < 300 || *v > 360 {
		t.Fatalf("p75 of final values: %+v", series.Buckets[0])
	}
	if series.Excluded["provisional"] != 1 || len(series.Identities) != 1 || !strings.Contains(series.Identities[0], "cohort="+protocol.BrowserRuntimeCohortDigest(cohort)) {
		t.Fatalf("pending measurement or tuple identity: %+v", series)
	}
}

func TestNativeBrowserDocumentSeriesRefusesMixedTuples(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	a, b := d2Cohort("141.0"), d2Cohort("142.0")
	attachD2Documents(t, record,
		d2Document{id: 1, metric: "cls", cohort: a, at: start.Add(2 * time.Second), value: 0.1, final: true},
		d2Document{id: 2, metric: "cls", cohort: b, at: start.Add(3 * time.Second), value: 0.3, final: true},
		d2Document{id: 3, metric: "cls", engine: "webkit", cohort: a, at: start.Add(4 * time.Second), value: 0.2, final: true})
	q := storage.AnalyticsQuery{Population: analysis.PopulationBrowserDocument, Metric: "document_cls", Aggregation: "p50", BucketSeconds: 10, EndSeconds: 20}
	if _, err := buildNativeArchiveSeries(context.Background(), record, q); err == nil || !strings.Contains(err.Error(), "3 browser document measurement tuples") {
		t.Fatalf("mixed tuples merged: %v", err)
	}
	q.Filters = map[string]string{"engine": "chromium"}
	if _, err := buildNativeArchiveSeries(context.Background(), record, q); err == nil || !strings.Contains(err.Error(), "cohort_mismatch") {
		t.Fatalf("mismatched cohorts merged: %v", err)
	}
	q.Filters["cohort"] = protocol.BrowserRuntimeCohortDigest(b)
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || series.Buckets[0].Count != 1 || *series.Buckets[0].Value != 0.3 {
		t.Fatalf("selected cohort: %+v %v", series, err)
	}
}

func TestNativeBrowserDocumentComparisonNeedsTheSameTuple(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	start := *current.StartedAt
	attachD2Documents(t, server.runner.archives[current.ID], d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141.0"), at: start.Add(40 * time.Second), value: 900, final: true})
	attachD2Documents(t, server.runner.archives[baseline.ID], d2Document{id: 1, metric: "lcp", cohort: d2Cohort("142.0"), at: start.Add(40 * time.Second), value: 300, final: true})
	query := func() NativeArchiveResponse {
		t.Helper()
		body, _ := json.Marshal(storage.AnalyticsQuery{Population: analysis.PopulationBrowserDocument, Metric: "document_lcp", Aggregation: "p75", BucketSeconds: 60, EndSeconds: 120, ComparisonRunID: baseline.ID, Derivation: &analysis.QueryDerivation{Operation: "baseline_delta"}})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+current.ID+"/archive/native-series", bytes.NewReader(body))
		request.SetPathValue("id", current.ID)
		recorder := httptest.NewRecorder()
		server.handleNativeArchiveSeries(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
		}
		var response NativeArchiveResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := query()
	if response.Comparable || response.Current.Buckets[0].Reason != "baseline_not_comparable" || !strings.Contains(strings.Join(response.Warnings, " "), "cohort_mismatch") {
		t.Fatalf("different cohorts compared: %+v", response)
	}
	attachD2Documents(t, server.runner.archives[baseline.ID], d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141.0"), at: start.Add(40 * time.Second), value: 300, final: true})
	response = query()
	if !response.Comparable || response.Current.Buckets[0].Value == nil || *response.Current.Buckets[0].Value <= 0 {
		t.Fatalf("the same tuple was not compared: %+v", response)
	}
}

func TestBaselineComparisonComparesQualifiedDocumentTuples(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	start := *current.StartedAt
	same, other := d2Cohort("141.0"), d2Cohort("142.0")
	var currentDocs, baselineDocs []d2Document
	for i := 0; i < 4; i++ {
		at := start.Add(time.Duration(40+i) * time.Second)
		currentDocs = append(currentDocs, d2Document{id: i + 1, metric: "lcp", cohort: same, at: at, value: 800, progress: 10, final: true}, d2Document{id: i + 11, metric: "cls", cohort: same, at: at, value: 0.2, final: true})
		baselineDocs = append(baselineDocs, d2Document{id: i + 1, metric: "lcp", cohort: same, at: at, value: 400, progress: 10, final: true}, d2Document{id: i + 11, metric: "cls", cohort: other, at: at, value: 0.1, final: true})
	}
	attachD2Documents(t, server.runner.archives[current.ID], currentDocs...)
	attachD2Documents(t, server.runner.archives[baseline.ID], baselineDocs...)
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	p := defaultBaselineComparisonPolicy()
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, p)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	if snapshot == nil || snapshot.BrowserSoftNavigation != protocol.BrowserSoftNavigationUnsupported {
		t.Fatalf("soft navigation state missing: %+v", snapshot)
	}
	rows := map[string]protocol.BrowserDocumentComparison{}
	for _, row := range snapshot.BrowserDocuments {
		rows[row.Metric] = row
	}
	lcp, cls := rows["lcp"], rows["cls"]
	if len(snapshot.BrowserDocuments) != 2 || lcp.BaselineID != baseline.ID || !lcp.Comparable || lcp.CurrentFinal != 4 || lcp.BaselineFinal != 4 || lcp.DeltaP75 == nil || *lcp.DeltaP75 <= 0 {
		t.Fatalf("matching tuple, each document once: %+v", snapshot.BrowserDocuments)
	}
	if cls.Comparable || cls.Reason != "cohort_mismatch" || cls.DeltaP75 != nil {
		t.Fatalf("mismatched cohorts compared: %+v", cls)
	}
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{BaselineComparison: snapshot}, Profile: RunArchiveReportProfile{ID: "standard"}}
	renderer := baselineComparisonReportRenderer()
	html, err := renderer.HTML(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Browser documents", "LCP (ms)", "p75 change", "Not compared: cohort_mismatch", "Soft navigations: unsupported"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("HTML report lacks %q", want)
		}
	}
	var lines []string
	renderer.PDF(data, &lines)
	if !strings.Contains(strings.Join(lines, "\n"), "not compared: cohort_mismatch") {
		t.Fatalf("PDF lacks the refusal: %q", lines)
	}
	var csvRows []string
	if err := renderer.CSV(data, func(section, key, metric, value string) error {
		if section == "baseline_browser_document" {
			csvRows = append(csvRows, value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(csvRows) != 2 || !strings.Contains(strings.Join(csvRows, " "), `"reason":"cohort_mismatch"`) {
		t.Fatalf("CSV document rows: %q", csvRows)
	}
}

func TestRCABrowserDocumentSeriesUseFinalDistributionsPerTuple(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	record := server.runner.archives[current.ID]
	start := *current.StartedAt
	a, b := d2Cohort("141.0"), d2Cohort("142.0")
	var documents []d2Document
	for i := 0; i < 6; i++ {
		documents = append(documents, d2Document{id: i + 1, metric: "lcp", cohort: a, at: start.Add(time.Duration(31+i) * time.Second), value: float64(100 * (i + 1)), progress: 10, final: true})
	}
	documents = append(documents, d2Document{id: 20, metric: "lcp", cohort: b, at: start.Add(40 * time.Second), value: 5000, final: true}, d2Document{id: 21, metric: "lcp", cohort: a, at: start.Add(41 * time.Second), value: 9000, progress: 2})
	attachD2Documents(t, record, documents...)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := readAllArchiveSummariesContext(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	series, limitations, err := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 0)
	if err != nil || len(limitations) != 0 || len(series) != 2 {
		t.Fatalf("one series per tuple: %d %+v %v", len(series), limitations, err)
	}
	valid, total := 0, 0.0
	for _, s := range series {
		if s.Kind != "browser_document" || s.Unit != "ms" || s.Identity == "" || !strings.HasPrefix(s.ID, "browser_document/lcp/") || s.Evidence.Kind != "browser_document" {
			t.Fatalf("series contract: %+v", s)
		}
		for _, p := range s.Points {
			if p.Valid {
				valid++
				total += p.Value
			}
		}
	}
	if series[0].Identity == series[1].Identity {
		t.Fatal("different cohorts share a measurement identity")
	}
	if valid == 0 || total >= 9000 {
		t.Fatalf("progress or pending values entered a series: %d %g", valid, total)
	}
	limited, limitations, _ := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, 1, 0)
	if len(limited) != 1 || len(limitations) != 1 {
		t.Fatalf("series budget: %d %+v", len(limited), limitations)
	}
}

// Baseline-mode RCA reads the baseline run's browser document tuples too, so
// a document series is judged against the same tuple of the baseline run
// rather than only within its own run.
func TestRCABaselineEvidenceCarriesBrowserDocumentSeries(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 200, 150)
	for _, run := range []*ManagedTest{current, baseline} {
		record := server.runner.archives[run.ID]
		start := *run.StartedAt
		var documents []d2Document
		for i := 0; i < 6; i++ {
			documents = append(documents, d2Document{id: i + 1, metric: "lcp", cohort: d2Cohort("141.0"), at: start.Add(time.Duration(31+i) * time.Second), value: float64(100 * (i + 1)), progress: 10, final: true})
		}
		attachD2Documents(t, record, documents...)
	}
	manifest, err := loadRunArchiveManifestFromRecord(server.runner.archives[current.ID])
	if err != nil {
		t.Fatal(err)
	}
	digest, err := rcaArchiveDigest(server.runner.archives[baseline.ID])
	if err != nil {
		t.Fatal(err)
	}
	coverage, evidence := server.rcaBaselineEvidence(context.Background(), manifest, baseline.ID, digest, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget)
	if evidence == nil {
		t.Fatalf("no baseline evidence: %+v", coverage)
	}
	// The baseline's tuple pairs with the run's by series ID.
	summaries, err := readAllArchiveSummariesContext(context.Background(), server.runner.archives[current.ID])
	if err != nil {
		t.Fatal(err)
	}
	currentSeries, _, err := rcaBrowserDocumentSeries(context.Background(), server.runner.archives[current.ID], manifest, summaries, -1, 0)
	if err != nil || len(currentSeries) != 1 {
		t.Fatalf("current document series %+v %v", currentSeries, err)
	}
	found := false
	for _, s := range evidence.Series {
		found = found || s.ID == currentSeries[0].ID
	}
	if !found {
		t.Fatal("the baseline's browser document series were not read under the run's series ID")
	}
	// The detector receives the qualification, the groups whose script
	// changed, and whether the baseline's documents were fully read.
	// The fixture's runs record no script digests, so the qualification says
	// so and every group both runs ran counts as changed.
	if evidence.Qualification == "" || !slices.Contains(evidence.QualificationReasons, rca.ReasonScriptUnrecorded) || !evidence.ScriptChangedGroups["checkout"] || !evidence.ScriptChangedGroups["search"] || evidence.DocumentsIncomplete {
		t.Fatalf("baseline evidence = qualification %q reasons %v changed groups %v incomplete %t", evidence.Qualification, evidence.QualificationReasons, evidence.ScriptChangedGroups, evidence.DocumentsIncomplete)
	}
	baselineRecord := server.runner.archives[baseline.ID]
	baselineManifest, err := loadRunArchiveManifestFromRecord(baselineRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baselineRecord.ArchivePath, baselineManifest.BrowserDocumentsFile), []byte("not document evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if digest, err = rcaArchiveDigest(baselineRecord); err != nil {
		t.Fatal(err)
	}
	if _, unread := server.rcaBaselineEvidence(context.Background(), manifest, baseline.ID, digest, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget); unread == nil || !unread.DocumentsIncomplete {
		t.Fatalf("an unreadable baseline document sidecar = %+v", unread)
	}
}

// Document tuples pair through the comparison policy's group mappings, as
// transactions do, and each row names its runtime cohort.
func TestBaselineComparisonMapsDocumentGroups(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	start := *current.StartedAt
	cohort := d2Cohort("141.0")
	var currentDocs, baselineDocs []d2Document
	for i := 0; i < 4; i++ {
		at := start.Add(time.Duration(40+i) * time.Second)
		currentDocs = append(currentDocs, d2Document{id: i + 1, metric: "lcp", cohort: cohort, at: at, value: 800, progress: 10, final: true, group: "Checkout"})
		baselineDocs = append(baselineDocs, d2Document{id: i + 1, metric: "lcp", cohort: cohort, at: at, value: 400, progress: 10, final: true, group: "Checkout v1"})
	}
	attachD2Documents(t, server.runner.archives[current.ID], currentDocs...)
	attachD2Documents(t, server.runner.archives[baseline.ID], baselineDocs...)
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	p := defaultBaselineComparisonPolicy()
	p.GroupMappings = map[string]string{baseline.ID + "/Checkout v1": "Checkout"}
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, p)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	if snapshot == nil || len(snapshot.BrowserDocuments) != 1 || !snapshot.BrowserDocuments[0].Comparable || snapshot.BrowserDocuments[0].Group != "Checkout" || snapshot.BrowserDocuments[0].Cohort != protocol.BrowserRuntimeCohortDigest(cohort) {
		t.Fatalf("mapped group not compared: %+v", snapshot)
	}
}

// Browser baseline input (E2): each document bucket says how many documents
// it summarizes, a republished measurement counts once, and the series names
// its cohort so a changed runtime cohort can be explained.
func TestRCABrowserDocumentSeriesCountDocumentsOnceAndNameTheirCohort(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	record := server.runner.archives[current.ID]
	start := *current.StartedAt
	first := d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141.0"), at: start.Add(31 * time.Second), value: 1200, progress: 3, final: true}
	second := d2Document{id: 2, metric: "lcp", cohort: d2Cohort("141.0"), at: start.Add(31*time.Second + 200*time.Millisecond), value: 1400, final: true}
	// The first document's evidence is published twice, as a retried
	// publication would deliver it.
	attachD2Documents(t, record, first, first, second)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := readAllArchiveSummariesContext(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	series, _, err := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 0)
	if err != nil || len(series) != 1 {
		t.Fatalf("series %d %v", len(series), err)
	}
	var counts int64
	for _, p := range series[0].Points {
		if p.Valid {
			counts += int64(p.Count)
		} else if p.Count != 0 {
			t.Fatalf("a gap carries a count: %+v", p)
		}
	}
	if counts != 2 {
		t.Fatalf("documents counted %d times, want 2", counts)
	}
	document := series[0].Document
	if document == nil || document.Metric != "lcp" || document.Family == "" || document.Fields["browser_version"] != "141.0" || document.Fields["shaping_applied"] != "true" || document.Incomplete {
		t.Fatalf("document cohort = %+v", document)
	}
	// Another runtime cohort of the same measurement shares the family but
	// not the series.
	other := rcaDocumentCohort(protocol.BrowserDocumentObservationTuple(protocol.BrowserDocumentObservation{Metric: "lcp", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256,
		Unit: "ms", Engine: "chromium", DeviceProfile: "desktop", Cohort: d2Cohort("142.0"), FrameScope: "main_frame", LifecyclePhase: "navigation"}), false)
	if other.Family != document.Family || other.Fields["browser_version"] != "142.0" {
		t.Fatalf("cohorts of one measurement: %+v vs %+v", other, document)
	}
}

// Two archives' document series of one cohort meet by series ID, so the
// browser baseline judges the run against the baseline's same tuple; another
// browser version meets only by family and is reported as changed.
func TestRCABrowserDocumentSeriesMatchAcrossArchives(t *testing.T) {
	server, store := baselineTestServer(t)
	build := func(id, version string) rca.Series {
		t.Helper()
		run := comparisonArchiveFixture(t, server, store, id, 200, 150)
		record := server.runner.archives[run.ID]
		start := *run.StartedAt
		attachD2Documents(t, record, d2Document{id: 1, metric: "lcp", cohort: d2Cohort(version), at: start.Add(31 * time.Second), value: 1200, final: true})
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		summaries, err := readAllArchiveSummariesContext(context.Background(), record)
		if err != nil {
			t.Fatal(err)
		}
		series, _, err := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 0)
		if err != nil || len(series) != 1 {
			t.Fatalf("%s series %d %v", id, len(series), err)
		}
		return series[0]
	}
	current, same, other := build("e2-current", "141.0"), build("e2-same", "141.0"), build("e2-other", "142.0")
	if current.ID != same.ID || current.Document.Family != same.Document.Family || current.ID == other.ID || current.Document.Family != other.Document.Family {
		t.Fatalf("cross-run keys: %s/%s %s/%s %s/%s", current.ID, current.Document.Family, same.ID, same.Document.Family, other.ID, other.Document.Family)
	}
	first := current.Points[0].At
	level := rca.LoadLevel{Start: first, End: first.Add(10 * time.Minute), VUsers: 10}
	judge := func(baseline rca.Series) rca.BrowserBaselineResult {
		input := rca.DetectionInput{Levels: []rca.LoadLevel{level}, Series: []rca.Series{current},
			Baseline: &rca.DetectionBaseline{TestID: "baseline", Qualification: rca.BaselineQualified, Levels: []rca.LoadLevel{level}, Series: []rca.Series{baseline}}}
		return rca.DetectBrowserBaseline(input, rca.DefaultBrowserBaselineParams).Results[0]
	}
	// The same tuple is found (one bucket is too few to score).
	if got := judge(same); got.Reason != rca.BrowserBaselineInsufficient || got.BaselineBuckets != 1 {
		t.Fatalf("same cohort = %+v", got)
	}
	if got := judge(other); got.Reason != rca.BrowserBaselineCohortChanged || !strings.Contains(got.Detail, "browser_version") {
		t.Fatalf("other browser version = %+v", got)
	}
}

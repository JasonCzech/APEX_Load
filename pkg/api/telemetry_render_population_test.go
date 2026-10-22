package api

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every eligible publication in the range the reference client reported is
// accounted exactly once: rendered, superseded, unqualified (by reason) or
// unreported. Only the first client to report a visible render of a held
// publication is the reference; ineligible and unplaceable reports are
// counted apart (TEL-A).
func TestTelemetryRenderLedgerAccountsTheReferenceClient(t *testing.T) {
	recorder := newTelemetryStageRecorder()
	var epoch string
	for _, eligible := range []bool{true, true, false, true, true, true, true} {
		e, _ := recorder.nextPublication(eligible)
		epoch = e
	}
	at := func(sequence uint64) clientRenderPublication {
		return clientRenderPublication{Epoch: epoch, Sequence: sequence}
	}
	report := func(client string, outcomes ...clientRenderOutcome) bool {
		return slices.Contains(recorder.renderOutcomes(client, outcomes), true)
	}
	of := func(p clientRenderPublication, outcome string) clientRenderOutcome {
		return clientRenderOutcome{publication: p, outcome: outcome}
	}
	if population := recorder.renderPopulation(); population == nil || population.ReferenceClient != "" || population.PublishedEligible != 6 || population.Eligible != 0 || len(epoch) < 16 {
		t.Fatalf("before any report: %+v", population)
	}
	if report("background-tab", of(at(1), "background_tab")) { // no visible render: no reference yet
		t.Fatal("a report without a visible render became the reference")
	}
	if report("spoof", of(at(99), telemetryRenderRendered)) { // a render of a publication never published
		t.Fatal("a render of an unheld publication became the reference")
	}
	if !report("reference", of(at(2), telemetryRenderRendered)) {
		t.Fatal("the first visible render did not make its client the reference")
	}
	if report("background-tab", of(at(4), telemetryRenderRendered)) { // another client: not accounted
		t.Fatal("another client was accounted")
	}
	report("reference", of(at(3), telemetryRenderRendered), // an ineligible publication
		of(at(4), telemetryRenderSuperseded), of(at(5), "background_tab"),
		of(at(2), telemetryRenderSuperseded), // a publication keeps its first outcome
		of(clientRenderPublication{Epoch: "other", Sequence: 4}, telemetryRenderRendered), of(at(99), telemetryRenderRendered))
	report("reference", of(at(7), telemetryRenderSuperseded), of(at(7), telemetryRenderRendered)) // 6 is never reported; 7 arrived twice and rendered
	population := recorder.renderPopulation()
	if population == nil || population.Population != telemetryRenderPopulationName || population.Epoch != epoch || population.PublishedEligible != 6 ||
		population.ReferenceClient == "" || population.ReferenceClient == "reference" || population.FirstSequence != 2 || population.LastSequence != 7 ||
		population.Eligible != 5 || population.Rendered != 2 || population.Superseded != 1 || population.Unqualified["background_tab"] != 1 || len(population.Unqualified) != 1 ||
		population.Unreported != 1 || population.Ineligible != 1 || population.Unknown != 2 {
		t.Fatalf("population %+v", population)
	}
	if population.Eligible != population.Rendered+population.Superseded+population.Unreported+population.Unqualified["background_tab"] {
		t.Fatal("an eligible publication was not accounted exactly once")
	}
	// A later report of a publication already in range moves it from
	// unreported to its outcome.
	report("reference", of(at(6), "stalled_render"))
	if population := recorder.renderPopulation(); population.Unreported != 0 || population.Unqualified["stalled_render"] != 1 || population.Eligible != 5 {
		t.Fatalf("in-range report %+v", population)
	}
	clone := population.clone()
	clone.Unqualified["background_tab"] = 9
	if population.Unqualified["background_tab"] != 1 {
		t.Fatal("a cloned population shares its reasons")
	}
}

// Eviction loses no classification: a publication is accounted when the range
// passes it, and eligible publications evicted past the range's end are
// unreported once the range reaches beyond them.
func TestTelemetryRenderLedgerAccountsPastEviction(t *testing.T) {
	recorder := newTelemetryStageRecorder()
	epoch, _ := recorder.nextPublication(true)
	report := func(sequence uint64, outcome string) {
		recorder.renderOutcomes("reference", []clientRenderOutcome{{publication: clientRenderPublication{Epoch: epoch, Sequence: sequence}, outcome: outcome}})
	}
	report(1, telemetryRenderRendered)
	for range telemetryRenderLedgerSize + 9 {
		recorder.nextPublication(true)
	}
	// Sequences 2 to 10 were evicted past the range's end (1), unreported.
	population := recorder.renderPopulation()
	if population.Eligible != 1 || population.Rendered != 1 || population.LastSequence != 1 {
		t.Fatalf("before the range grows %+v", population)
	}
	last := uint64(telemetryRenderLedgerSize + 10)
	report(last, telemetryRenderRendered)
	report(5, telemetryRenderRendered) // evicted: cannot be placed
	population = recorder.renderPopulation()
	if population.FirstSequence != 1 || population.LastSequence != last || population.Eligible != int64(last) || population.Rendered != 2 ||
		population.Unreported != int64(last)-2 || population.Unknown != 1 || population.PublishedEligible != int64(last) {
		t.Fatalf("population %+v", population)
	}
	// The whole range evicted later keeps its accounting.
	for range telemetryRenderLedgerSize + 1 {
		recorder.nextPublication(false)
	}
	if evicted := recorder.renderPopulation(); evicted.Eligible != population.Eligible || evicted.Rendered != 2 || evicted.Unreported != population.Unreported {
		t.Fatalf("after the range was evicted %+v", evicted)
	}
	// A controller that adopts the run again starts another epoch, accounted
	// from the adoption.
	adoptedAt := time.Now().Add(time.Minute).UTC()
	recorder.adopted(adoptedAt)
	if again, _ := recorder.nextPublication(true); again == epoch || recorder.renderPopulation().ReferenceClient != "" {
		t.Fatal("re-adoption kept the earlier epoch or reference")
	}
	if from := recorder.renderPopulation().SampledFrom; from == nil || !from.Equal(adoptedAt) {
		t.Fatalf("the adopted population is accounted from %v, not the adoption", from)
	}
}

// Each installed live publication gets the next sequence of one epoch. A
// local point repeating the previous collection time is not eligible, and the
// collection-time bounds come from the point.
func TestLivePublicationsCarryIdentityAndEligibility(t *testing.T) {
	server, test, _ := clientRenderTestServer(t, true)
	collected := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	publish := func(at time.Time) *TelemetryPublication {
		server.runner.publishLiveMetrics(test, &LiveMetrics{Timestamp: time.Now().UTC(), Telemetry: &TelemetryHealth{CollectedAt: at}}, nil)
		server.runner.mu.RLock()
		defer server.runner.mu.RUnlock()
		return test.LiveMetrics.Telemetry.Publication.clone()
	}
	// A point an install guard rejects is not published and takes no sequence.
	server.runner.publishLiveMetrics(test, &LiveMetrics{Timestamp: time.Now().UTC(), Telemetry: &TelemetryHealth{CollectedAt: collected}}, nil, func() bool { return false })
	first, second, repeated := publish(collected), publish(collected.Add(time.Second)), publish(collected.Add(time.Second))
	if first == nil || second == nil || repeated == nil || first.Epoch == "" || second.Epoch != first.Epoch || repeated.Epoch != first.Epoch {
		t.Fatalf("publications %+v %+v %+v", first, second, repeated)
	}
	if first.Sequence != 1 || second.Sequence != 2 || repeated.Sequence != 3 || !first.Eligible || !second.Eligible || repeated.Eligible {
		t.Fatalf("sequences/eligibility %+v %+v %+v", first, second, repeated)
	}
	if first.OldestCollectedAt == nil || !first.OldestCollectedAt.Equal(collected) || !first.NewestCollectedAt.Equal(collected) {
		t.Fatalf("collection bounds %+v", first)
	}
	// A point without a reported collection time states no bounds.
	if unreported := publish(time.Time{}); unreported.OldestCollectedAt != nil || unreported.NewestCollectedAt != nil {
		t.Fatalf("bounds without a collection time %+v", unreported)
	}
}

// A distributed point is repeated only when every collection it carries was
// already published; an unidentified generator's window is never proved
// repeated.
func TestDistributedPublicationRepeatedOnlyWhenEveryCollectionWasPublished(t *testing.T) {
	published := distributedWindowMark{epoch: "e", collection: 5}
	test := &ManagedTest{}
	test.runTotals.publishedWindows = map[string]distributedWindowMark{"gen-a": published}
	timing := distributedPublicationTiming{}
	for name, tc := range map[string]struct {
		publication distributedMetricsPublication
		repeated    bool
	}{
		"replayed":                  {distributedMetricsPublication{collections: map[string]distributedWindowMark{"gen-a": published}, timings: map[string]distributedPublicationTiming{"gen-a": timing}, sources: 1}, true},
		"a batch without telemetry": {distributedMetricsPublication{collections: map[string]distributedWindowMark{"gen-a": published}, timings: map[string]distributedPublicationTiming{"gen-a": timing}, sources: 2}, false},
		"new collection": {distributedMetricsPublication{collections: map[string]distributedWindowMark{"gen-a": {epoch: "e", collection: 6}},
			timings: map[string]distributedPublicationTiming{"gen-a": timing}}, false},
		"unidentified generator": {distributedMetricsPublication{collections: map[string]distributedWindowMark{"gen-a": published},
			timings: map[string]distributedPublicationTiming{"gen-a": timing, "gen-b": timing}}, false},
		"no collection": {distributedMetricsPublication{}, false},
	} {
		if got := distributedPublicationRepeatedLocked(test, tc.publication); got != tc.repeated {
			t.Fatalf("%s: repeated %v", name, got)
		}
	}
}

// A tab's report names the publications it received; the reference client's
// outcomes reach the live projection and the archive manifest.
func TestClientRenderReportAccountsPublications(t *testing.T) {
	server, test, _ := clientRenderTestServer(t, true)
	collected := time.Now().UTC().Add(-time.Minute)
	var publications []*TelemetryPublication
	for i := range 3 {
		server.runner.publishLiveMetrics(test, &LiveMetrics{Timestamp: time.Now().UTC(), Telemetry: &TelemetryHealth{CollectedAt: collected.Add(time.Duration(i) * time.Second)}}, nil)
		server.runner.mu.RLock()
		publications = append(publications, test.LiveMetrics.Telemetry.Publication.clone())
		server.runner.mu.RUnlock()
	}
	now := time.Now().UTC()
	name := func(p *TelemetryPublication) *clientRenderPublication {
		return &clientRenderPublication{Epoch: p.Epoch, Sequence: p.Sequence}
	}
	response := postClientRender(server, test.ID, clientRenderBody("tab-1",
		clientRenderSample{State: "superseded", ObservedAt: now, Publication: name(publications[0])},
		clientRenderSample{ReceiveToRenderMs: 14, State: "visible", ObservedAt: now, Publication: name(publications[1])},
		clientRenderSample{ReceiveToRenderMs: 9, State: "visible", ObservedAt: now, Publication: name(publications[2])},
		clientRenderSample{ReceiveToRenderMs: 11, State: "visible", ObservedAt: now}))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", response.Code, response.Body)
	}
	public, ok := server.runner.GetTest(test.ID)
	if !ok || public.LiveMetrics == nil || public.LiveMetrics.Telemetry == nil {
		t.Fatal("no public run view")
	}
	population := public.LiveMetrics.Telemetry.RenderPopulation
	// The report's visible renders made it the reference client, so every
	// outcome in it counts, the superseded first publication included.
	if population == nil || population.ReferenceClient == "" || population.ReferenceClient == "tab-1" || population.FirstSequence != 1 || population.LastSequence != 3 || population.Eligible != 3 ||
		population.Rendered != 2 || population.Superseded != 1 || population.Unreported != 0 {
		t.Fatalf("population %+v", population)
	}
	var render *TelemetryStageDistribution
	for i := range public.LiveMetrics.Telemetry.StageDistributions {
		if public.LiveMetrics.Telemetry.StageDistributions[i].Stage == telemetryStageBrowserRender {
			render = &public.LiveMetrics.Telemetry.StageDistributions[i]
		}
	}
	if render == nil || render.Samples != 3 || render.Excluded["superseded"] != 1 {
		t.Fatalf("render stage %+v", render)
	}
	manifest := &RunArchiveManifest{}
	server.runner.persistTelemetryStageDistributions(test, manifest, false)
	if manifest.TelemetryRenderPopulation == nil || manifest.TelemetryRenderPopulation.Rendered != 2 {
		t.Fatalf("manifest population %+v", manifest.TelemetryRenderPopulation)
	}
}

// The run report states the archived render population in every format: the
// reader's sentence in HTML and PDF, and each figure in the CSV (TEL-B).
func TestTelemetryAcceptanceReportStatesTheRenderPopulation(t *testing.T) {
	manifest := &RunArchiveManifest{TelemetryRenderPopulation: &TelemetryRenderPopulation{Population: telemetryRenderPopulationName, Epoch: "e1", PublishedEligible: 6, ReferenceClient: "tab-1",
		FirstSequence: 2, LastSequence: 7, Eligible: 5, Rendered: 2, Superseded: 1, Unqualified: map[string]int64{"background_tab": 1}, Unreported: 1, Ineligible: 1}}
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{Manifest: manifest}}
	section := reportSectionRegistry["telemetry_acceptance"]
	html, err := section.HTML(data)
	if err != nil {
		t.Fatal(err)
	}
	sentence := "publications 2 to 7: of 5 eligible publications, 2 rendered, 1 superseded, 1 unqualified (Background tab 1) and 1 unreported; 6 eligible publications were published in all."
	for _, want := range []string{"Render population: The reference dashboard tab", sentence, "1 of publications without a new collection"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("HTML lacks %q: %s", want, html)
		}
	}
	var lines []string
	section.PDF(data, &lines)
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "Render population: The reference dashboard tab's "+sentence) {
		t.Fatalf("PDF lines = %s", joined)
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := section.CSV(data, func(section, key, metric, value string) error {
		return writer.Write([]string{section, key, metric, value})
	}); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	for _, want := range []string{"telemetry_render_population,,population,rendered_publications", "telemetry_render_population,,eligible,5", "telemetry_render_population,,rendered,2",
		"telemetry_render_population,,unqualified:background_tab,1", "telemetry_render_population,,unreported,1", "telemetry_render_population,,unknown,0"} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("CSV lacks %q: %s", want, buffer.String())
		}
	}
	for population, want := range map[*TelemetryRenderPopulation]string{
		nil: "The render population was not recorded for this run.",
		{Population: telemetryRenderPopulationName, PublishedEligible: 1}: "No dashboard tab reported a visible render of a named publication, so no reference client was accounted; 1 eligible publication was published.",
	} {
		if text, _ := telemetryRenderPopulationReport(population, nil); text != want {
			t.Fatalf("population %+v: %q", population, text)
		}
	}
	// A population accounted from an adoption does not claim the whole run.
	started := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	adopted := started.Add(50 * time.Minute)
	for _, population := range []*TelemetryRenderPopulation{
		{Population: telemetryRenderPopulationName, PublishedEligible: 600, SampledFrom: &adopted},
		{Population: telemetryRenderPopulationName, PublishedEligible: 600, SampledFrom: &adopted, ReferenceClient: "ref", FirstSequence: 1, LastSequence: 2, Eligible: 2, Rendered: 2},
	} {
		text, counts := telemetryRenderPopulationReport(population, &started)
		if !strings.Contains(text, "600 eligible publications were published since 2026-10-22T12:50:00Z, when this controller began publishing (it adopted the run).") || strings.Contains(text, "in all") {
			t.Fatalf("adopted population: %q", text)
		}
		if !slices.Contains(counts, [2]string{"sampled_from", "2026-10-22T12:50:00Z"}) {
			t.Fatalf("adopted population counts %v", counts)
		}
	}
	if text, _ := telemetryRenderPopulationReport(&TelemetryRenderPopulation{Population: telemetryRenderPopulationName, PublishedEligible: 3, SampledFrom: &started, ReferenceClient: "ref", FirstSequence: 1, LastSequence: 1, Eligible: 1, Rendered: 1}, &started); !strings.HasSuffix(text, "3 eligible publications were published in all.") {
		t.Fatalf("a population accounted from the start: %q", text)
	}
}

// A malformed publication name refuses the report; another viewer reusing the
// reference tab's identifier is not accounted; outcomes without a measurement
// spend none of the viewer's sample budget; and the run-cumulative population
// stays off archived rows (TEL-A).
func TestClientRenderReportGuardsTheReferenceAndTheBudget(t *testing.T) {
	server, test, _ := clientRenderTestServer(t, true)
	collected := time.Now().UTC().Add(-time.Minute)
	var names []*clientRenderPublication
	for i := range 3 {
		server.runner.publishLiveMetrics(test, &LiveMetrics{Timestamp: time.Now().UTC(), Telemetry: &TelemetryHealth{CollectedAt: collected.Add(time.Duration(i) * time.Second)}}, nil)
		server.runner.mu.RLock()
		names = append(names, &clientRenderPublication{Epoch: test.LiveMetrics.Telemetry.Publication.Epoch, Sequence: test.LiveMetrics.Telemetry.Publication.Sequence})
		server.runner.mu.RUnlock()
	}
	now := time.Now().UTC()
	for _, malformed := range []*clientRenderPublication{{Epoch: "", Sequence: 1}, {Epoch: names[0].Epoch, Sequence: 0}, {Epoch: strings.Repeat("e", 65), Sequence: 1}} {
		if got := postClientRender(server, test.ID, clientRenderBody("tab-1", clientRenderSample{State: "superseded", ObservedAt: now, Publication: malformed})); got.Code != http.StatusBadRequest {
			t.Fatalf("malformed name %+v: %d", malformed, got.Code)
		}
	}
	if got := postClientRender(server, test.ID, `{"client_id":"tab-1","samples":[{"receive_to_render_ms":1,"state":"visible","observed_at":"`+now.Format(time.RFC3339Nano)+`","publication":{"epoch":"e","sequence":1,"extra":true}}]}`); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown publication field: %d", got.Code)
	}
	if got := postClientRender(server, test.ID, clientRenderBody("tab-1", clientRenderSample{ReceiveToRenderMs: 9, State: "visible", ObservedAt: now, Publication: names[0]})); got.Code != http.StatusAccepted {
		t.Fatalf("reference report: %d %s", got.Code, got.Body)
	}
	spoof := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/telemetry/client-render", strings.NewReader(clientRenderBody("tab-1",
		clientRenderSample{ReceiveToRenderMs: 9, State: "visible", ObservedAt: now, Publication: names[1]},
		clientRenderSample{State: "superseded", ObservedAt: now, Publication: names[2]})))
	spoof.RemoteAddr = "198.51.100.7:1234"
	spoof.SetPathValue("id", test.ID)
	recorder := httptest.NewRecorder()
	server.handleReportClientRender(recorder, spoof)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("another viewer's report: %d", recorder.Code)
	}
	server.runner.mu.Lock()
	population := stageRecorderLocked(test).renderPopulation()
	server.runner.mu.Unlock()
	if population.LastSequence != names[0].Sequence || population.Rendered != 1 || population.Superseded != 0 {
		t.Fatalf("another viewer reusing the tab identifier was accounted: %+v", population)
	}
	// A full report of unmeasured outcomes leaves the viewer's budget for a
	// measured render from another of its tabs.
	full := make([]clientRenderSample, clientRenderMaxSamples)
	for i := range full {
		full[i] = clientRenderSample{State: "superseded", ObservedAt: now, Publication: names[1]}
	}
	if got := postClientRender(server, test.ID, clientRenderBody("tab-2", full...)); got.Code != http.StatusAccepted {
		t.Fatalf("unmeasured outcomes: %d", got.Code)
	}
	// The reference tab's report and the outcome-only report each spent one.
	burst := make([]clientRenderSample, clientRenderMaxSamples-2)
	for i := range burst {
		burst[i] = clientRenderSample{ReceiveToRenderMs: 5, State: "visible", ObservedAt: now}
	}
	if got := postClientRender(server, test.ID, clientRenderBody("tab-3", burst...)); got.Code != http.StatusAccepted {
		t.Fatalf("the viewer's measured renders were refused after unmeasured outcomes: %d %s", got.Code, got.Body)
	}
	if archived := archivedTelemetryHealth(&TelemetryHealth{RenderPopulation: population, Publication: &TelemetryPublication{Epoch: "e", Sequence: 1}}); archived.RenderPopulation != nil || archived.Publication == nil {
		t.Fatalf("archived row %+v", archived)
	}
	// A non-reference tab's named visible render that fails admission is
	// still excluded from the stage, like an unnamed one; its named
	// superseded outcome is not.
	excluded := func(reason string) int64 {
		server.runner.mu.Lock()
		defer server.runner.mu.Unlock()
		for _, stage := range stageRecorderLocked(test).snapshot() {
			if stage.Stage == telemetryStageBrowserRender {
				return stage.Excluded[reason]
			}
		}
		return 0
	}
	stalled, superseded := excluded("stalled_render"), excluded("superseded")
	other := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/telemetry/client-render", strings.NewReader(clientRenderBody("tab-4",
		clientRenderSample{ReceiveToRenderMs: clientRenderCeilingMs + 1, State: "visible", ObservedAt: now, Publication: names[2]},
		clientRenderSample{State: "superseded", ObservedAt: now, Publication: names[1]})))
	other.RemoteAddr = "203.0.113.9:1234"
	other.SetPathValue("id", test.ID)
	otherRecorder := httptest.NewRecorder()
	server.handleReportClientRender(otherRecorder, other)
	if otherRecorder.Code != http.StatusAccepted {
		t.Fatalf("non-reference report: %d", otherRecorder.Code)
	}
	if excluded("stalled_render") != stalled+1 || excluded("superseded") != superseded {
		t.Fatalf("non-reference exclusions: stalled %d->%d, superseded %d->%d", stalled, excluded("stalled_render"), superseded, excluded("superseded"))
	}
}

// Unnamed samples that are not visible still spend the viewer's budget, and
// every report spends at least one, so rotating tab identifiers cannot add
// exclusions or reports without limit.
func TestClientRenderUnnamedSamplesSpendTheBudget(t *testing.T) {
	server, test, _ := clientRenderTestServer(t, true)
	now := time.Now().UTC()
	background := make([]clientRenderSample, clientRenderMaxSamples)
	for i := range background {
		background[i] = clientRenderSample{ReceiveToRenderMs: 5, State: "background", ObservedAt: now}
	}
	if got := postClientRender(server, test.ID, clientRenderBody("rotating-1", background...)); got.Code != http.StatusAccepted {
		t.Fatalf("first report: %d", got.Code)
	}
	if got := postClientRender(server, test.ID, clientRenderBody("rotating-2", background...)); got.Code != http.StatusTooManyRequests {
		t.Fatalf("a new tab identity bought another full report of unnamed samples: %d", got.Code)
	}
}

// Each collection-time bound is on its own generator's clock: a fleet's
// newest, chosen on the corrected scale, is kept even when its raw time reads
// earlier than the oldest's.
func TestPublicationBoundsKeepEachGeneratorsClock(t *testing.T) {
	oldest := time.Date(2026, 10, 22, 2, 0, 5, 0, time.UTC) // a clock 5 s fast
	newest := time.Date(2026, 10, 22, 2, 0, 2, 0, time.UTC)
	test := &ManagedTest{ID: "skew"}
	live := &LiveMetrics{Telemetry: &TelemetryHealth{CollectedAt: oldest, newestCollectedAt: newest}}
	assignTelemetryPublicationLocked(test, live)
	if p := live.Telemetry.Publication; p == nil || !p.OldestCollectedAt.Equal(oldest) || !p.NewestCollectedAt.Equal(newest) {
		t.Fatalf("bounds %+v", p)
	}
}

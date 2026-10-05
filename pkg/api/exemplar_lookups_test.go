package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func lookupSetOf(ids ...string) exemplarLookupSet {
	var set exemplarLookupSet
	for _, id := range ids {
		set.add(lookupCandidate{traceID: id, rank: lookupRankSample})
	}
	return set
}

func traceID(n int) string { return fmt.Sprintf("%032x", n) }

func TestLookupSetTakesErrorsThenSlowThenRepresentative(t *testing.T) {
	var set exemplarLookupSet
	live := &LiveMetrics{Transactions: map[string]*TxMetrics{
		"checkout": {Exemplars: []engine.TraceExemplar{
			{TraceID: traceID(1), Reason: engine.ExemplarSample, Success: true, DurationMs: 900},
			{TraceID: traceID(2), Reason: engine.ExemplarSlowestSuccess, Success: true, DurationMs: 500},
			{TraceID: traceID(3), Reason: engine.ExemplarFirstError, Success: false, DurationMs: 10},
		}},
		"search": {Exemplars: []engine.TraceExemplar{{TraceID: traceID(4), Reason: engine.ExemplarSlowestSuccess, Success: true, DurationMs: 800}}},
		// A peer without exemplars contributes correlation IDs last.
		"legacy": {CorrelationIDs: []string{"00-" + traceID(5) + "-00f067aa0ba902b7-01"}},
	}}
	set.observe(live)
	if got, want := set.take(), []string{traceID(3), traceID(4), traceID(2), traceID(1), traceID(5)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lookup order = %v, want %v", got, want)
	}
	if got := set.take(); len(got) != 0 {
		t.Fatalf("take did not start a new window: %v", got)
	}
	// Later windows are always looked up; nothing caps the run at its first 64.
	for window := 0; window < 3; window++ {
		live := &LiveMetrics{Transactions: map[string]*TxMetrics{}}
		for i := 0; i < 100; i++ {
			live.Transactions[fmt.Sprintf("tx-%d", i)] = &TxMetrics{Exemplars: []engine.TraceExemplar{{TraceID: traceID(1000*window + i + 10), Reason: engine.ExemplarSample, Success: true, DurationMs: float64(i)}}}
		}
		set.observe(live)
		ids := set.take()
		if len(ids) != maxProviderLookupTraceIDs || ids[0] != traceID(1000*window+99+10) {
			t.Fatalf("window %d took %d IDs starting %s", window, len(ids), ids[0])
		}
	}
}

func TestLookupSetRestoreKeepsRank(t *testing.T) {
	var set exemplarLookupSet
	set.observe(&LiveMetrics{Transactions: map[string]*TxMetrics{"checkout": {Exemplars: []engine.TraceExemplar{
		{TraceID: traceID(1), Reason: engine.ExemplarFirstError, Success: false, DurationMs: 5},
	}}}})
	failed := set.take()
	// A full window of newer samples arrives before the failed poll is restored.
	live := &LiveMetrics{Transactions: map[string]*TxMetrics{}}
	for i := 0; i < maxLookupCandidates; i++ {
		live.Transactions[fmt.Sprintf("tx-%d", i)] = &TxMetrics{Exemplars: []engine.TraceExemplar{{TraceID: traceID(100 + i), Reason: engine.ExemplarSample, Success: true, DurationMs: 900}}}
	}
	set.observe(live)
	set.restore(failed)
	if got := set.take(); len(got) == 0 || got[0] != traceID(1) {
		t.Fatalf("restored error exemplar lost its rank: %v", got[:min(3, len(got))])
	}
}

func TestProviderLookupOutcomesRecordMisses(t *testing.T) {
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	records := providerLookupOutcomes("elasticsearch", []string{traceID(1), traceID(2)}, []string{traceID(1)}, false, nil, at)
	if records[0].Outcome != LookupFound || records[1].Outcome != LookupNotFound || records[1].ResultLimited || records[0].Schema != providerLookupSchema {
		t.Fatalf("outcomes = %+v", records)
	}
	if limited := providerLookupOutcomes("elasticsearch", []string{traceID(2)}, nil, true, nil, at); !limited[0].ResultLimited {
		t.Fatal("a miss under a full result page must be marked inconclusive")
	}
	if failed := providerLookupOutcomes("dynatrace", []string{traceID(1)}, nil, false, errors.New("timeout"), at); failed[0].Outcome != LookupError || failed[0].Message != "timeout" {
		t.Fatalf("failed lookup = %+v", failed[0])
	}
}

func TestELKPollRecordsLookupOutcomesIncludingMisses(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	found, missing := traceID(1), traceID(2)
	var asked string
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked = string(body)
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":1},"hits":[{"_id":"a","_source":{"@timestamp":"2026-10-04T12:00:00Z","trace.id":"` + found + `","message":"checkout failed"}}]}}`))
	}))
	defer fixture.Close()
	test := &ManagedTest{ID: "lookups", CreatedAt: time.Now(), ELKCapture: &ELKCaptureConfig{ConnectionID: "elk", Endpoint: fixture.URL, IndexPattern: "logs", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "token", TimestampField: "@timestamp", TraceIDField: "trace.id", MessageField: "message"}}
	artifacts := &runArtifacts{runner: NewTestRunner(1, NewHub(), nil), test: test, monitorHTTPClient: fixture.Client()}
	artifacts.elkLookups = lookupSetOf(found, missing)
	artifacts.collectELKMetrics(context.Background(), test, &LiveMetrics{Timestamp: time.Now()})
	if !strings.Contains(asked, missing) {
		t.Fatalf("the poll did not ask about the window's exemplars: %s", asked)
	}
	outcomes := map[string]string{}
	for _, record := range artifacts.providerLookups {
		outcomes[record.TraceID] = record.Outcome
	}
	if outcomes[found] != LookupFound || outcomes[missing] != LookupNotFound || len(outcomes) != 2 {
		t.Fatalf("lookup outcomes = %v", outcomes)
	}
	// With a test ID field the search never filters by trace, so no lookups are claimed.
	test.ELKCapture.TestIDField = "labels.test"
	artifacts.providerLookups = nil
	artifacts.elkLastPoll = time.Time{}
	artifacts.elkLookups = lookupSetOf(found)
	artifacts.collectELKMetrics(context.Background(), test, &LiveMetrics{Timestamp: time.Now()})
	if len(artifacts.providerLookups) != 0 {
		t.Fatalf("test-ID searches recorded lookups: %+v", artifacts.providerLookups)
	}
}

func TestProviderLookupsStreamIntoLocalArchive(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	test := &ManagedTest{ID: "local-lookups", ELKCapture: &ELKCaptureConfig{ConnectionID: "elk"}}
	writer, err := newRunArchiveWriter(test, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &runArtifacts{test: test, recorder: writer}
	artifacts.recordProviderLookups(providerLookupOutcomes("elasticsearch", []string{traceID(1), traceID(2)}, []string{traceID(1)}, false, nil, time.Now()))
	record, status, finalErr := artifacts.finalize(test, "COMPLETE", time.Now(), "")
	if record == nil || status != "COMPLETE" || finalErr != "" {
		t.Fatalf("finalize: %v %s %s", record, status, finalErr)
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ProviderLookupsFile != providerLookupsFileName || manifest.ProviderLookupCount != 2 {
		t.Fatalf("manifest lookups = %q %d", manifest.ProviderLookupsFile, manifest.ProviderLookupCount)
	}
	listed := false
	for _, artifact := range manifest.Artifacts {
		listed = listed || artifact.Name == providerLookupsFileName && artifact.SHA256 != "" && artifact.Count == 2
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, providerLookupsFileName))
	if err != nil || !listed || !strings.Contains(string(data), `"outcome":"not_found"`) {
		t.Fatalf("archived lookups listed=%v err=%v: %s", listed, err, data)
	}
}

func TestDistributedArchiveCarriesProviderLookups(t *testing.T) {
	root := t.TempDir()
	test := &ManagedTest{ID: "remote-lookups", ELKCapture: &ELKCaptureConfig{ConnectionID: "elk"}}
	artifacts := &runArtifacts{test: test, providerLookups: providerLookupOutcomes("elasticsearch", []string{traceID(1)}, nil, false, nil, time.Now()), providerLookupsTruncated: true}
	test.elkArtifacts = artifacts
	manifest := &RunArchiveManifest{}
	if err := writeDistributedProviderLookups(root, manifest, nil, test); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, providerLookupsFileName))
	if err != nil || manifest.ProviderLookupCount != 1 || !manifest.ProviderLookupsTruncated || !strings.Contains(string(data), traceID(1)) {
		t.Fatalf("distributed lookups = %+v %v %s", manifest, err, data)
	}
}

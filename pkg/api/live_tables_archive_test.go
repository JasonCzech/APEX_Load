package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/rca"
)

func TestLiveTableArchiveRetainsOnlyCountsAndFinalizationMetadata(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "discard")
	id := liveTableID(test.ID, "work")
	if _, err := runner.liveClient(test.ID, "local").Execute(context.Background(), id, livetable.Request{ClientID: "private-receipt-client", Sequence: 1, Operation: livetable.OpPop}); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	provenance := buildRunArchiveProvenance(runner, test)
	if len(provenance.LiveTables) != 1 {
		t.Fatalf("missing table summary: %+v", provenance.LiveTables)
	}
	table := provenance.LiveTables[0]
	if table.State != "closed" || table.Counts.Seeded != 2 || table.Counts.Popped != 1 || table.Counts.Discarded != 1 || table.Finalization == nil || table.Finalization.State != "complete" {
		t.Fatalf("wrong disposition summary: %+v", table)
	}
	if table.OpCount != 1 || table.OpErrors != 0 || table.OpLatencyTotalNS == 0 || table.OpLatencyMaxNS == 0 {
		t.Fatalf("missing separate live operation telemetry: %+v", table)
	}
	body, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("private-customer"), []byte("private-receipt-client"), []byte("lease_token"), []byte("seed_metadata"), []byte("wrapped_key")} {
		if bytes.Contains(body, forbidden) {
			t.Fatalf("summary contains private runtime material %s", forbidden)
		}
	}
	masked := maskRunArchiveProvenance(provenance)
	masked.LiveTables[0].Finalization.State = "changed"
	if provenance.LiveTables[0].Finalization.State != "complete" {
		t.Fatal("masked provenance aliases mutable finalization outcome")
	}
}

func TestLiveTableRetainedEvidenceFeedsValidityAndClonesEvents(t *testing.T) {
	manifest, summaries, resources := validityArchive(120)
	at := time.Now().UTC()
	observed := &livetable.Observations{Version: 1, CapturedAt: at, WaitCount: 3, WaitTotalNS: 9000000, EmptyOutcomes: 2, Throttled: 1, Events: []livetable.ObservationEvent{{Kind: "capacity_rejection", First: at, Last: at, Count: 1}}}
	manifest.Provenance.LiveTables = []RunArchiveLiveTableSummary{{TableID: "work", Counts: livetable.Counts{Redeliveries: 2}, Observations: observed}}
	verdict := judgeArchive(manifest, summaries, resources)
	if verdict.Verdict != rca.ValiditySuspect || len(verdict.SharedData) != 1 || verdict.SharedData[0].WaitCount != 3 || verdict.SharedData[0].Redeliveries != 2 || !containsString(verdict.Reasons, rca.RuleSharedDataThrottled) {
		t.Fatalf("retained evidence not used: %+v", verdict)
	}
	masked := maskRunArchiveProvenance(manifest.Provenance)
	masked.LiveTables[0].Observations.Events[0].Count = 99
	if observed.Events[0].Count != 1 {
		t.Fatal("masked evidence aliases source")
	}
	manifest.Provenance.LiveTables[0].Observations = nil
	verdict = judgeArchive(manifest, summaries, resources)
	if verdict.Verdict != rca.ValidityUnknown || !containsString(verdict.Limitations, rca.LimitationSharedDataGap) {
		t.Fatalf("legacy table evidence claimed complete: %+v", verdict)
	}
	manifest.Provenance.LiveTables = nil
	if verdict = judgeArchive(manifest, summaries, resources); verdict.Verdict != rca.ValidityValid {
		t.Fatalf("ordinary archive changed: %+v", verdict)
	}
}

func TestLiveTableValidityNarrativeDoesNotInventSustainedIntervals(t *testing.T) {
	detail := validityRootCauseDetail(&rca.Validity{Verdict: rca.ValiditySuspect, Rules: rca.DefaultValidityRules, Reasons: []string{rca.RuleSharedDataThrottled, rca.RuleSharedDataUnknown}})
	if !strings.Contains(detail, "capacity rejected operations") || !strings.Contains(detail, "unknown outcomes") || strings.Contains(detail, "consecutive intervals") || strings.Contains(detail, "shared_data_") {
		t.Fatalf("incorrect native evidence narrative: %s", detail)
	}
}

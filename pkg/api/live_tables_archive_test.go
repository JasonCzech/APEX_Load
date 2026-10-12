package api

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/livetable"
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

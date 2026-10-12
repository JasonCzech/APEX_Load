package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// The supported revision API copies QUEUED runs, before they have a seed of
// their own. Preserve an inherited original seed, or the explicit fresh-seed
// intent; never manufacture a seed reference to an unstarted queued run.
func TestLiveTableQueuedRevisionPreservesSeedIntentAndStaticSnapshot(t *testing.T) {
	for _, sourcePolicy := range []string{"original_seed", "current_at_start"} {
		t.Run(sourcePolicy, func(t *testing.T) {
			runner, store, retained := newLiveRuntimeFixture(t, "durable", "discard")
			script := httpScript("revision-live-script", "Revision live script")
			if _, err := runner.StoreScriptDurable(script); err != nil {
				t.Fatal(err)
			}
			options := &storage.LiveTableOptions{SeedPolicy: sourcePolicy, Durability: "durable"}
			if sourcePolicy == "original_seed" {
				options.SeedRunID = retained.ID
			}
			source, err := runner.CreateTest(CreateTestRequest{Name: "Queued live source", Duration: "1m", Groups: []CreateGroupEntry{{Name: "Workers", ScriptID: script.ID, VUsers: 1}}, DataPools: []storage.ScenarioDataPoolBinding{
				{PoolID: "live-seed", Alias: "work", AccessMode: "live", LiveOptions: options},
				{PoolID: "live-seed", Alias: "snapshot", AccessMode: "read_only"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if source.Status != "QUEUED" || runner.liveRun(source.ID) != nil {
				t.Fatal("queued source unexpectedly initialized live data")
			}
			before := source.Groups[0].sharedDataSources[0].InlineData
			if !strings.Contains(before, "private-customer-one") {
				t.Fatal("ordinary snapshot was not captured")
			}
			if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "row-one", PoolID: "live-seed", Status: "available", Data: map[string]string{"value": "changed-current"}}); err != nil {
				t.Fatal(err)
			}
			for _, explicitCurrent := range []bool{false, true} {
				req := CreateTestRequest{Name: "Revised queued copy", RevisionSourceTestID: source.ID}
				if explicitCurrent {
					req.DataPools = cloneScenarioDataPoolBindings(source.DataPools)
					req.DataPools[0].LiveOptions.SeedPolicy = "current_at_start"
					req.DataPools[0].LiveOptions.SeedRunID = ""
				}
				revised, err := runner.CreateTest(req)
				if err != nil {
					t.Fatal(err)
				}
				if revised.Groups[0].sharedDataSources[0].InlineData != before {
					t.Fatal("live reseed refreshed the ordinary immutable snapshot")
				}
				want := "changed-current"
				if sourcePolicy == "original_seed" && !explicitCurrent {
					want = "private-customer-one"
					if revised.DataPools[0].LiveOptions.SeedRunID != retained.ID {
						t.Fatal("queued revision replaced the retained original seed")
					}
				}
				if err := runner.prepareLiveTables(context.Background(), revised); err != nil {
					t.Fatal(err)
				}
				table := runner.liveRun(revised.ID).tables[liveTableID(revised.ID, "work")].table
				rows, _, err := liveTableExportRows(table, table.Stats().Revision)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, row := range rows {
					if row.ID == "row-one" && row.Values["value"] == want {
						found = true
					}
				}
				if !found || len(rows) != 2 {
					t.Fatalf("source policy=%s explicit current=%t rows=%+v, want %q", sourcePolicy, explicitCurrent, rows, want)
				}
			}
			if source.DataPools[0].LiveOptions.SeedPolicy != sourcePolicy {
				t.Fatal("revision mutated its source binding")
			}
			liveIntegrationAssertRows(t, store, "live-seed", 3)
		})
	}
}

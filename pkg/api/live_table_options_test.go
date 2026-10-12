package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLiveTableBindingDefaultsAndIsolation(t *testing.T) {
	input := []storage.ScenarioDataPoolBinding{{Alias: "jobs", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"order_id"}}}, {Alias: "static", PoolID: "pool"}}
	got, err := normalizeDataPoolBindingIntent(input)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SelectionStrategy != "manual" || got[0].LiveOptions.Profile != "native_queue" || got[0].LiveOptions.EndMode != "discard" || got[0].LiveOptions.Durability != "memory" || got[1].AccessMode != "read_only" || got[1].SelectionStrategy != "sequential" || got[1].LiveOptions != nil {
		t.Fatalf("defaults=%+v %+v", got[0], got[1])
	}
	got[0].LiveOptions.Columns[0] = "changed"
	got[0].LiveOptions.MaxRows = 2
	if input[0].LiveOptions.Columns[0] != "order_id" || input[0].LiveOptions.MaxRows != 0 {
		t.Fatal("normalization aliased caller configuration")
	}
	cloned := cloneScenarioDataPoolBindings(input)
	cloned[0].LiveOptions.Columns[0] = "copy"
	if input[0].LiveOptions.Columns[0] != "order_id" {
		t.Fatal("snapshot aliased live columns")
	}
}

func TestLiveTableBindingValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storage.ScenarioDataPoolBinding)
		want   string
	}{
		{"missing pool", func(b *storage.ScenarioDataPoolBinding) { b.PoolID = "" }, "requires pool_id"},
		{"empty requires columns", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.SeedPolicy = "empty" }, "requires declared columns"},
		{"duplicate columns", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.Columns = []string{"id", "id"} }, "unique"},
		{"unsupported profile", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.Profile = "other" }, "profile"},
		{"unsupported selection", func(b *storage.ScenarioDataPoolBinding) { b.SelectionStrategy = "unique" }, "selection strategy"},
		{"vts lease", func(b *storage.ScenarioDataPoolBinding) {
			b.LiveOptions.Profile = "vts_columns"
			b.SelectionStrategy = "lease"
		}, "does not support"},
		{"vts writeback", func(b *storage.ScenarioDataPoolBinding) {
			b.LiveOptions.Profile = "vts_columns"
			b.LiveOptions.EndMode = "write_back"
		}, "native queue"},
		{"writeback empty", func(b *storage.ScenarioDataPoolBinding) {
			b.LiveOptions.SeedPolicy = "empty"
			b.LiveOptions.Columns = []string{"id"}
			b.LiveOptions.EndMode = "write_back"
		}, "seeded from"},
		{"unnamed output", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.EndMode = "save_as_new" }, "output pool name"},
		{"unused output name", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.OutputPoolName = "jobs" }, "requires save_as_new"},
		{"unbounded wait", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.EmptyPolicy = "wait" }, "positive"},
		{"durable timeout too short", func(b *storage.ScenarioDataPoolBinding) {
			b.LiveOptions.Durability = "durable"
			b.LiveOptions.OperationTimeoutMS = 64999
		}, "at least 65000"},
		{"wait beyond operation", func(b *storage.ScenarioDataPoolBinding) {
			b.LiveOptions.EmptyPolicy = "wait"
			b.LiveOptions.WaitMS = 11000
		}, "operation timeout"},
		{"negative bytes", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.MaxBytes = -1 }, "outside supported"},
		{"data bytes", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.MaxBytes = maxLiveTableDataBytes + 1 }, "outside supported"},
		{"result bytes", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.MaxResultBytes = maxLiveTableResultBytes + 1 }, "outside supported"},
		{"lease lifetime", func(b *storage.ScenarioDataPoolBinding) { b.LiveOptions.MaxLeaseLifetimeMS = 1000 }, "lease limits"},
		{"read only options", func(b *storage.ScenarioDataPoolBinding) { b.AccessMode = "read_only" }, "require access_mode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := storage.ScenarioDataPoolBinding{PoolID: "pool", Alias: "jobs", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{}}
			tc.mutate(&b)
			_, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{b})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %s", err, tc.want)
			}
		})
	}
	tooMany := make([]storage.ScenarioDataPoolBinding, maxLiveTablesPerRun+1)
	for i := range tooMany {
		tooMany[i] = storage.ScenarioDataPoolBinding{PoolID: "pool", Alias: fmt.Sprintf("table_%d", i), AccessMode: "live"}
	}
	if _, err := normalizeDataPoolBindingIntent(tooMany); err == nil {
		t.Fatal("unbounded live tables admitted")
	}
}

func TestLiveTableBindingDurableTimeoutCoversAdoption(t *testing.T) {
	for _, requested := range []int64{0, 65000, 120000} {
		got, err := normalizeLiveTableBinding(storage.ScenarioDataPoolBinding{PoolID: "pool", Alias: "jobs", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", OperationTimeoutMS: requested}})
		if err != nil {
			t.Fatal(err)
		}
		want := requested
		if want == 0 {
			want = 120000
		}
		if got.LiveOptions.OperationTimeoutMS != want {
			t.Fatalf("durable timeout = %d want %d", got.LiveOptions.OperationTimeoutMS, want)
		}
	}
}

func TestLiveTableBindingRetryWindowCoversOperationDeadline(t *testing.T) {
	for _, tc := range []struct {
		durability         string
		timeout, retention int64
		valid              bool
	}{
		{"memory", 120000, 119999, false},
		{"memory", 120000, 120000, true},
		{"durable", 120000, 134999, false},
		{"durable", 120000, 135000, true},
		{"durable", 65000, 79999, false},
		{"durable", 65000, 80000, true},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d", tc.durability, tc.timeout, tc.retention), func(t *testing.T) {
			_, err := normalizeLiveTableBinding(storage.ScenarioDataPoolBinding{PoolID: "pool", Alias: "jobs", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: tc.durability, OperationTimeoutMS: tc.timeout, RetryWindowMS: tc.retention}})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestLiveTableDoesNotEnableHistoricalWritableModes(t *testing.T) {
	for _, mode := range []string{"read_write", "consume"} {
		b := storage.ScenarioDataPoolBinding{PoolID: "pool", Alias: "jobs", AccessMode: mode}
		if _, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{b}); err == nil {
			t.Fatal("historical mode became writable")
		}
		got, err := normalizeLegacyDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{b})
		if err != nil || got[0].AccessMode != "read_only" || got[0].LiveOptions != nil {
			t.Fatalf("legacy semantics changed %v %v", got, err)
		}
	}
}

func TestLiveTableDomainPolicyAppliesToAuthoringAndMaterialization(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	policyStore := server.store.(storage.DomainFeaturePolicyStore)
	if _, ok, err := policyStore.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"data.live_tables"}, "0"); err != nil || !ok {
		t.Fatalf("disable policy %v %v", ok, err)
	}
	live := []storage.ScenarioDataPoolBinding{{PoolID: poolID, Alias: "jobs", AccessMode: "live"}}
	if _, err := server.runner.CreateTest(directDataPoolContractRequest(scriptID, live)); err == nil || !strings.Contains(err.Error(), "disabled for this domain") {
		t.Fatalf("live materialization bypassed policy: %v", err)
	}
	if _, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Live scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}}, DataPools: live}); err == nil || !strings.Contains(err.Error(), "disabled for this domain") {
		t.Fatalf("live scenario bypassed policy: %v", err)
	}
	if err := server.validateScenarioDataPoolProjects(storage.DefaultProjectID, live); err == nil || !strings.Contains(err.Error(), "disabled for this domain") {
		t.Fatalf("binding edit bypassed policy: %v", err)
	}
	if _, err := server.runner.CreateTest(directDataPoolContractRequest(scriptID, []storage.ScenarioDataPoolBinding{{PoolID: poolID, Alias: "jobs", AccessMode: "read_only"}})); err != nil {
		t.Fatalf("readonly affected by live policy: %v", err)
	}
}

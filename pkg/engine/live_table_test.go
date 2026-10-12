package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
)

type liveTableClientFunc func(context.Context, string, livetable.Request) (livetable.Result, error)

func (f liveTableClientFunc) Execute(ctx context.Context, id string, request livetable.Request) (livetable.Result, error) {
	return f(ctx, id, request)
}

func liveAction(operation string, extra map[string]any) dsl.Action {
	params := map[string]any{"alias": "work", "operation": operation, "result_prefix": "result"}
	for key, value := range extra {
		params[key] = value
	}
	return dsl.Action{Name: operation, Type: "live_table", Params: params}
}

func TestLiveTableSandboxRejectsBoundsAndKnownManagedSecretSeeds(t *testing.T) {
	seed := LiveTableSandboxSeed{Alias: "work", Columns: []string{"id"}, Rows: []livetable.Row{{Values: map[string]string{"id": "safe"}}}}
	if _, _, _, err := NewLiveTableSandbox([]LiveTableSandboxSeed{seed, seed, seed, seed, seed}); err == nil {
		t.Fatal("unbounded sandbox table count accepted")
	}
	oversized := seed
	oversized.Rows = []livetable.Row{{Values: map[string]string{"id": strings.Repeat("x", 65537)}}}
	if _, _, _, err := NewLiveTableSandbox([]LiveTableSandboxSeed{oversized}); err == nil {
		t.Fatal("oversized sandbox accepted")
	}
	for _, value := range []string{"prefix-managed-private-value", "{{ secret.API_KEY}}"} {
		seed.Rows = []livetable.Row{{Values: map[string]string{"id": value}}}
		_, err := ReplayScript(context.Background(), &dsl.Script{Name: "seed rejection", Protocol: "http", Actions: []dsl.Action{liveAction("pop", nil)}}, ReplayOptions{
			LiveTableSeeds: []LiveTableSandboxSeed{seed}, Variables: map[string]string{"token": "managed-private-value"},
		})
		if err == nil || !strings.Contains(err.Error(), "managed secrets cannot seed") || strings.Contains(err.Error(), value) {
			t.Fatalf("managed seed rejection was not private: %v", err)
		}
	}
}

func liveTestVU(client livetable.Client) *VUser {
	return &VUser{ID: 1, Script: &dsl.Script{Name: "live", Protocol: "http"}, Variables: map[string]string{}, LiveTableClient: client, LiveTableClientID: "assignment-incarnation", LiveTableBindings: []LiveTableBinding{{Alias: "work", TableID: "table", Strategy: "none", Profile: livetable.ProfileNative, Columns: []string{"id"}, LeaseDuration: time.Second, OpTimeout: time.Second}}}
}

func TestLiveTableLostReplyRetriesSameIdentityAndUnknownBlocksNewWork(t *testing.T) {
	for _, recoverReply := range []bool{true, false} {
		t.Run(map[bool]string{true: "recover", false: "unknown"}[recoverReply], func(t *testing.T) {
			var requests []livetable.Request
			vu := liveTestVU(liveTableClientFunc(func(_ context.Context, _ string, r livetable.Request) (livetable.Result, error) {
				requests = append(requests, r)
				if len(requests) == 1 || !recoverReply {
					return livetable.Result{}, errors.New("lost reply contains private input")
				}
				return livetable.Result{Status: livetable.StatusOK, Row: &livetable.Row{ID: "record", Version: 1, Values: map[string]string{"id": "a"}}}, nil
			}))
			err := vu.executeLiveTableAction(context.Background(), liveAction("pop", nil))
			if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
				t.Fatalf("retry identity changed: %+v", requests)
			}
			if recoverReply {
				if err != nil || vu.Variables["result.values.id"] != "a" {
					t.Fatalf("result=%v err=%v", vu.Variables, err)
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "private input") {
					t.Fatalf("unsafe error: %v", err)
				}
				if err := vu.executeLiveTableAction(context.Background(), liveAction("pop", nil)); err == nil || len(requests) != 2 {
					t.Fatal("unknown operation allowed a new consume")
				}
				if handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "target", Type: "request"}); !handled || err == nil {
					t.Fatal("unknown live outcome allowed a target action")
				}
			}
		})
	}
}

func TestLiveTableEmptyClearsOutputsAndDoesNotExposeLeaseToken(t *testing.T) {
	calls := 0
	vu := liveTestVU(liveTableClientFunc(func(_ context.Context, _ string, r livetable.Request) (livetable.Result, error) {
		calls++
		if calls == 1 {
			return livetable.Result{Status: livetable.StatusOK, Row: &livetable.Row{ID: "r", Version: 1, Values: map[string]string{"id": "first"}}}, nil
		}
		return livetable.Result{Status: livetable.StatusEmpty}, nil
	}))
	vu.Variables["unrelated"] = "keep"
	if err := vu.executeLiveTableAction(context.Background(), liveAction("pop", nil)); err != nil {
		t.Fatal(err)
	}
	if err := vu.executeLiveTableAction(context.Background(), liveAction("pop", map[string]any{"on_empty": "skip"})); err != nil {
		t.Fatal(err)
	}
	if _, present := vu.Variables["result.values.id"]; present {
		t.Fatal("stale row survived empty result")
	}
	if vu.Variables["unrelated"] != "keep" || vu.Variables["result.status"] != "empty" {
		t.Fatalf("variables=%v", vu.Variables)
	}
	if err := vu.bindLiveResult("result", livetable.Result{Status: livetable.StatusOK, Lease: &livetable.Lease{Token: "private-token"}}); err != nil {
		t.Fatal(err)
	}
	for _, value := range vu.Variables {
		if strings.Contains(value, "private-token") {
			t.Fatal("lease capability leaked into variables")
		}
	}
}

func TestLiveTableManagedSecretCannotBeCopiedFromVariable(t *testing.T) {
	calls := 0
	vu := liveTestVU(liveTableClientFunc(func(context.Context, string, livetable.Request) (livetable.Result, error) {
		calls++
		return livetable.Result{}, nil
	}))
	vu.RedactionConfig = WithSecretValueRedaction(nil, []string{"managed-private-value"})
	vu.Variables["innocent"] = "managed-private-value"
	err := vu.executeLiveTableAction(context.Background(), liveAction("push", map[string]any{"values": map[string]string{"id": "prefix-{{var.innocent}}"}}))
	if err == nil || calls != 0 {
		t.Fatalf("managed secret reached shared data: calls=%d err=%v", calls, err)
	}
}

func TestLiveTableIterationLeaseAcknowledgesOnlySuccessfulIteration(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "continue-on-error releases", false: "success acknowledges"}[fail], func(t *testing.T) {
			var mu sync.Mutex
			var operations []livetable.Operation
			client := liveTableClientFunc(func(_ context.Context, _ string, r livetable.Request) (livetable.Result, error) {
				mu.Lock()
				defer mu.Unlock()
				operations = append(operations, r.Operation)
				if r.Operation == livetable.OpLease {
					return livetable.Result{Status: livetable.StatusOK, Row: &livetable.Row{ID: "r", Version: 1, Values: map[string]string{"id": "work"}}, Lease: &livetable.Lease{Token: "private", RecordID: "r", ExpiresAt: time.Now().Add(time.Hour)}}, nil
				}
				return livetable.Result{Status: livetable.StatusOK}, nil
			})
			proto := &captureProtocol{}
			actions := []dsl.Action{click("submit")}
			if fail {
				actions = []dsl.Action{liveAction("read", map[string]any{"record_id": "missing"})}
			}
			vu := newControlFlowVUser(proto, nil, actions)
			vu.LiveTableClient = client
			vu.LiveTableClientID = "incarnation"
			vu.LiveTableBindings = liveTestVU(client).LiveTableBindings
			vu.LiveTableBindings[0].Strategy = "lease"
			vu.Script.Config.RunLogic.Iterations = 1
			vu.Script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(true)
			if fail {
				vu.LiveTableClient = liveTableClientFunc(func(ctx context.Context, id string, r livetable.Request) (livetable.Result, error) {
					if r.Operation == livetable.OpRead {
						return livetable.Result{}, &livetable.Error{Code: livetable.CodeInvalid, Message: "required action failed"}
					}
					return client.Execute(ctx, id, r)
				})
			}
			vu.Run(context.Background())
			want := livetable.OpAck
			if fail {
				want = livetable.OpRelease
			}
			mu.Lock()
			defer mu.Unlock()
			if len(operations) != 2 || operations[0] != livetable.OpLease || operations[1] != want {
				t.Fatalf("operations=%v", operations)
			}
		})
	}
}

func TestLiveTableBindingsFailClosedWithoutClientAndOnStaticAliasCollision(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{liveAction("next", nil)}}
	if err := validateLiveTableBindings(script, nil, "", nil); err == nil {
		t.Fatal("unbound live action admitted")
	}
	vu := liveTestVU(liveTableClientFunc(func(context.Context, string, livetable.Request) (livetable.Result, error) {
		return livetable.Result{}, nil
	}))
	script.DataSources = []dsl.DataSource{{Name: "work", Type: "csv"}}
	if err := validateLiveTableBindings(script, vu.LiveTableClient, vu.LiveTableClientID, vu.LiveTableBindings); err == nil {
		t.Fatal("static/live alias collision admitted")
	}
	if _, err := New(Config{Script: script, VUsers: 1, Duration: time.Second}); err == nil {
		t.Fatal("unmanaged live action silently ran as a snapshot")
	}
}

func TestLiveTableReplaySandboxIsolatedAndRequiresSeed(t *testing.T) {
	script := &dsl.Script{Name: "sandbox", Protocol: "http", Actions: []dsl.Action{liveAction("pop", nil)}}
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("unseeded replay err=%v", err)
	}
	seed := []LiveTableSandboxSeed{{Alias: "work", Columns: []string{"id"}, Rows: []livetable.Row{{ID: "r", Values: map[string]string{"id": "original"}}}}}
	for range 2 {
		client, bindings, id, err := NewLiveTableSandbox(seed)
		if err != nil {
			t.Fatal(err)
		}
		vu := liveTestVU(client)
		vu.LiveTableBindings = bindings
		vu.LiveTableClientID = id
		if err := vu.executeLiveTableAction(context.Background(), liveAction("pop", nil)); err != nil {
			t.Fatal(err)
		}
		if vu.Variables["result.values.id"] != "original" {
			t.Fatal("sandbox inherited another replay mutation")
		}
	}
}

func TestLiveTableResultCanBeTheNextActionsInput(t *testing.T) {
	var received livetable.Request
	vu := liveTestVU(liveTableClientFunc(func(_ context.Context, _ string, request livetable.Request) (livetable.Result, error) {
		received = request
		return livetable.Result{Status: livetable.StatusOK}, nil
	}))
	vu.Variables["result.record_id"] = "prior-record"
	vu.Variables["result.record_version"] = "3"
	if err := vu.executeLiveTableAction(context.Background(), liveAction("update", map[string]any{"record_id": "{{var.result.record_id}}", "expected_version": "{{var.result.record_version}}", "values": map[string]string{"id": "changed"}})); err != nil {
		t.Fatal(err)
	}
	if received.RecordID != "prior-record" || received.ExpectedVersion == nil || *received.ExpectedVersion != 3 {
		t.Fatalf("request=%+v", received)
	}
}

func TestLiveTableParallelConsumerDoesNotBlockProducerLane(t *testing.T) {
	produced := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	identities := make(map[string]string)
	vu := liveTestVU(liveTableClientFunc(func(ctx context.Context, _ string, request livetable.Request) (livetable.Result, error) {
		mu.Lock()
		identities[string(request.Operation)] = request.ClientID
		mu.Unlock()
		if request.Operation == livetable.OpPop {
			select {
			case <-produced:
			case <-ctx.Done():
				return livetable.Result{}, ctx.Err()
			}
		}
		if request.Operation == livetable.OpPush {
			once.Do(func() { close(produced) })
		}
		return livetable.Result{Status: livetable.StatusOK, Row: &livetable.Row{ID: "r", Version: 1, Values: map[string]string{"id": "shared"}}}, nil
	}))
	vu.Proto = &parallelTestProtocol{}
	vu.Script.Protocol = parallelTestProtocolName
	vu.Metrics = NewMetricsCollector(16)
	if err := vu.prepareRuntimeState(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{{Name: "consumer", Steps: []dsl.Action{liveAction("pop", nil)}}, {Name: "producer", Steps: []dsl.Action{liveAction("push", map[string]any{"values": map[string]string{"id": "shared"}})}}}}
	if !vu.runParallelBranches(ctx, control, "action") {
		t.Fatal("parallel producer/consumer deadlocked or failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if identities["pop"] == "" || identities["pop"] == identities["push"] {
		t.Fatalf("parallel lanes share request ordering identity: %v", identities)
	}
	if vu.Variables["parallel.consumer.result.values.id"] != "shared" {
		t.Fatalf("branch result missing: %v", vu.Variables)
	}
}

func TestLiveTableParallelIterationsReuseBoundedClientSlots(t *testing.T) {
	manager := livetable.NewManager()
	if _, err := manager.Create(livetable.Config{ID: "table", RunID: "run", Profile: livetable.ProfileNative, Columns: []string{"id"}, Limits: livetable.Limits{MaxClients: 2}}); err != nil {
		t.Fatal(err)
	}
	vu := liveTestVU(manager)
	vu.Proto = &parallelTestProtocol{}
	vu.Script.Protocol = parallelTestProtocolName
	vu.Metrics = NewMetricsCollector(1000)
	if err := vu.prepareRuntimeState(); err != nil {
		t.Fatal(err)
	}
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "consumer", Steps: []dsl.Action{liveAction("pop", map[string]any{"wait_ms": 500})}},
		{Name: "producer", Steps: []dsl.Action{liveAction("push", map[string]any{"values": map[string]string{"id": "shared"}})}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for iteration := 0; iteration < 25; iteration++ {
		if !vu.runParallelBranches(ctx, control, "action") {
			t.Fatalf("iteration %d exhausted client slots or failed", iteration)
		}
	}
	if len(vu.liveTables.branches) != 2 {
		t.Fatalf("retained %d lanes", len(vu.liveTables.branches))
	}
	parent := vu.liveTables.branchLane(vu.liveTableLane, 0)
	sibling := vu.liveTables.branchLane(vu.liveTableLane, 1)
	child := vu.liveTables.branchLane(parent, 0)
	if child == parent || child == sibling || child == vu.liveTables.branchLane(sibling, 0) || child != vu.liveTables.branchLane(parent, 0) {
		t.Fatal("nested branch slots collide or do not persist")
	}
}

func TestLiveTableRenewalUsesIndependentLaneWhileConsumerWaits(t *testing.T) {
	waiting, renewed := make(chan struct{}), make(chan struct{})
	var renewOnce sync.Once
	var operationClient, renewClient, owner string
	var mu sync.Mutex
	vu := liveTestVU(liveTableClientFunc(func(ctx context.Context, _ string, request livetable.Request) (livetable.Result, error) {
		switch request.Operation {
		case livetable.OpPop:
			mu.Lock()
			operationClient = request.ClientID
			owner = request.OwnerID
			mu.Unlock()
			close(waiting)
			select {
			case <-renewed:
				return livetable.Result{Status: livetable.StatusEmpty}, nil
			case <-ctx.Done():
				return livetable.Result{}, ctx.Err()
			}
		case livetable.OpRenew:
			<-waiting
			mu.Lock()
			renewClient = request.ClientID
			if owner != request.OwnerID {
				t.Errorf("renewal changed owner")
			}
			mu.Unlock()
			renewOnce.Do(func() { close(renewed) })
			return livetable.Result{Status: livetable.StatusOK, Lease: &livetable.Lease{Token: "private", RecordID: "r", ExpiresAt: time.Now().Add(time.Hour)}}, nil
		default:
			return livetable.Result{Status: livetable.StatusOK}, nil
		}
	}))
	if err := vu.prepareRuntimeState(); err != nil {
		t.Fatal(err)
	}
	binding := vu.liveTables.bindings["work"]
	vu.liveTables.leases["held"] = liveTableAcquisition{binding: binding, lease: livetable.Lease{Token: "private", RecordID: "r", ExpiresAt: time.Now().Add(400 * time.Millisecond)}, laneID: vu.liveTableLane.id}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	vu.startLiveTableRenewal(ctx)
	defer vu.closeLiveTables()
	if err := vu.executeLiveTableAction(ctx, liveAction("pop", map[string]any{"on_empty": "skip"})); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if renewClient == "" || renewClient == operationClient {
		t.Fatal("renewal serialized behind blocking action")
	}
}

func TestLiveTableNativeAndVTSOperationsUseRealCore(t *testing.T) {
	for _, profile := range []livetable.Profile{livetable.ProfileNative, livetable.ProfileVTS} {
		t.Run(string(profile), func(t *testing.T) {
			client, bindings, identity, err := NewLiveTableSandbox([]LiveTableSandboxSeed{{Alias: "work", Profile: profile, Columns: []string{"id"}, Rows: []livetable.Row{{Values: map[string]string{"id": "1"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			vu := liveTestVU(client)
			vu.LiveTableBindings = bindings
			vu.LiveTableClientID = identity
			run := func(op string, params map[string]any) {
				t.Helper()
				if err := vu.executeLiveTableAction(context.Background(), liveAction(op, params)); err != nil {
					t.Fatalf("%s: %v", op, err)
				}
			}
			run("next", nil)
			if profile == livetable.ProfileNative {
				run("update", map[string]any{"record_id": "{{var.result.record_id}}", "expected_version": "{{var.result.record_version}}", "values": map[string]string{"id": "2"}})
				run("lease", nil)
				run("ack", map[string]any{"lease_ref": "result"})
			} else {
				run("update", map[string]any{"position": 1, "expected": map[string]string{"id": "1"}, "values": map[string]string{"id": "2"}})
				run("read_column", map[string]any{"columns": []string{"id"}, "position": 1, "limit": 2})
				if !strings.Contains(vu.Variables["result.cells"], `"value":"2"`) {
					t.Fatal(vu.Variables)
				}
				run("rotate", nil)
			}
			run("push", map[string]any{"values": map[string]string{"id": "3"}})
			run("pop", nil)
		})
	}
}

func TestLiveTableRangeAndColumnOutputsPreserveAbsentCellsAndClearStaleResults(t *testing.T) {
	client, bindings, identity, err := NewLiveTableSandbox([]LiveTableSandboxSeed{{Alias: "work", Profile: livetable.ProfileVTS, Columns: []string{"id", "note"}, Rows: []livetable.Row{
		{Values: map[string]string{"id": "1", "note": ""}},
		{Values: map[string]string{"id": "2"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	vu := liveTestVU(client)
	vu.LiveTableBindings, vu.LiveTableClientID = bindings, identity
	run := func(op string, params map[string]any) {
		t.Helper()
		if err := vu.executeLiveTableAction(context.Background(), liveAction(op, params)); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	run("read", map[string]any{"position": 1, "limit": 2})
	var rows []livetable.Row
	if err := json.Unmarshal([]byte(vu.Variables["result.rows"]), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !reflect.DeepEqual(rows[0].Values, map[string]string{"id": "1", "note": ""}) || !reflect.DeepEqual(rows[1].Values, map[string]string{"id": "2"}) || vu.Variables["result.count"] != "2" {
		t.Fatalf("range result=%+v variables=%v", rows, vu.Variables)
	}
	run("read_column", map[string]any{"columns": []string{"note"}, "position": 1, "limit": 2})
	var columns []string
	var cells []livetable.Cell
	if err := json.Unmarshal([]byte(vu.Variables["result.columns"]), &columns); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(vu.Variables["result.cells"]), &cells); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"note"}) || !reflect.DeepEqual(cells, []livetable.Cell{{Present: true, Value: ""}, {Present: false}}) || vu.Variables["result.rows"] != "" {
		t.Fatalf("column result=%v cells=%+v variables=%v", columns, cells, vu.Variables)
	}
	run("create_column", map[string]any{"columns": []string{"new_column"}})
	if vu.Variables["result.columns"] != `["id","note","new_column"]` || vu.Variables["result.cells"] != "" {
		t.Fatalf("column creation output=%v", vu.Variables)
	}
	run("read", map[string]any{"position": 100, "on_empty": "skip"})
	if vu.Variables["result.columns"] != "" || vu.Variables["result.rows"] != "" || vu.Variables["result.status"] != "empty" {
		t.Fatalf("stale output survived empty result: %v", vu.Variables)
	}
}

func TestLiveTableEmptyPolicySkipsBeforeTargetAndClearsAllBindings(t *testing.T) {
	client := liveTableClientFunc(func(context.Context, string, livetable.Request) (livetable.Result, error) {
		return livetable.Result{Status: livetable.StatusEmpty}, nil
	})
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{click("must-not-run")})
	vu.LiveTableClient = client
	vu.LiveTableClientID = "incarnation"
	vu.LiveTableBindings = liveTestVU(client).LiveTableBindings
	vu.LiveTableBindings[0].Strategy = "pop"
	vu.LiveTableBindings[0].EmptyPolicy = "skip"
	vu.LiveTableBindings = append(vu.LiveTableBindings, LiveTableBinding{Alias: "zlater", TableID: "second", Profile: livetable.ProfileNative, Strategy: "none", Columns: []string{"id"}})
	vu.Variables["work.id"] = "stale"
	vu.Variables["zlater.id"] = "stale"
	vu.Script.Config.RunLogic.Iterations = 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	vu.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("skip did not consume the configured iteration")
	}
	if len(proto.names) > 0 {
		t.Fatal("empty skip executed target actions")
	}
	if _, ok := vu.Variables["zlater.id"]; ok {
		t.Fatal("empty source retained a later binding")
	}
}

func TestLiveTableLostLeaseBlocksSubsequentTargetActions(t *testing.T) {
	vu := liveTestVU(liveTableClientFunc(func(context.Context, string, livetable.Request) (livetable.Result, error) {
		return livetable.Result{}, nil
	}))
	if err := vu.prepareLiveTables(); err != nil {
		t.Fatal(err)
	}
	vu.liveTables.leases["expired"] = liveTableAcquisition{lease: livetable.Lease{ExpiresAt: time.Now().Add(-time.Second)}}
	handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "target", Type: "request"})
	if !handled || err == nil {
		t.Fatal("lost lease allowed target execution")
	}
}

func TestLiveTableAckResponseDoesNotRaceRenewalOrBlockAnotherLease(t *testing.T) {
	ackStarted := make(chan struct{})
	allowAck := make(chan struct{})
	secondRenewed := make(chan struct{}, 1)
	var acquired int
	vu := liveTestVU(liveTableClientFunc(func(ctx context.Context, _ string, r livetable.Request) (livetable.Result, error) {
		switch r.Operation {
		case livetable.OpLease:
			acquired++
			id := map[int]string{1: "first", 2: "second"}[acquired]
			return livetable.Result{Status: livetable.StatusOK, Row: &livetable.Row{ID: id, Values: map[string]string{"id": id}}, Lease: &livetable.Lease{RecordID: id, Token: id, ExpiresAt: time.Now().Add(400 * time.Millisecond)}}, nil
		case livetable.OpAck:
			close(ackStarted)
			select {
			case <-allowAck:
				return livetable.Result{Status: livetable.StatusOK}, nil
			case <-ctx.Done():
				return livetable.Result{}, ctx.Err()
			}
		case livetable.OpRenew:
			if r.RecordID == "first" {
				return livetable.Result{}, &livetable.Error{Code: livetable.CodeLease, Message: "already acknowledged"}
			}
			select {
			case secondRenewed <- struct{}{}:
			default:
			}
			return livetable.Result{Status: livetable.StatusOK, Lease: &livetable.Lease{RecordID: "second", Token: "second", ExpiresAt: time.Now().Add(time.Second)}}, nil
		default:
			return livetable.Result{Status: livetable.StatusOK}, nil
		}
	}))
	for _, prefix := range []string{"first", "second"} {
		if err := vu.executeLiveTableAction(context.Background(), liveAction("lease", map[string]any{"result_prefix": prefix})); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vu.startLiveTableRenewal(ctx)
	done := make(chan error, 1)
	go func() {
		done <- vu.executeLiveTableAction(ctx, liveAction("ack", map[string]any{"lease_ref": "first"}))
	}()
	<-ackStarted
	select {
	case <-secondRenewed:
	case <-time.After(time.Second):
		close(allowAck)
		t.Fatal("ack response blocked another lease renewal")
	}
	close(allowAck)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := vu.liveTableLeaseError(); err != nil {
		t.Fatal(err)
	}
	vu.closeLiveTables()
}

func TestLiveTableRejectedRetryCannotClearOriginalUnknownOutcome(t *testing.T) {
	for _, code := range []livetable.Code{livetable.CodeCapacity, livetable.CodeClosed, livetable.CodeInvalid, livetable.CodeExpired} {
		t.Run(string(code), func(t *testing.T) {
			var requests []livetable.Request
			vu := liveTestVU(liveTableClientFunc(func(_ context.Context, _ string, r livetable.Request) (livetable.Result, error) {
				requests = append(requests, r)
				if len(requests) == 1 {
					return livetable.Result{}, errors.New("original reply lost")
				}
				return livetable.Result{}, &livetable.Error{Code: code, Message: "retry refused"}
			}))
			err := vu.executeLiveTableAction(context.Background(), liveAction("pop", nil))
			var typed *livetable.Error
			if !errors.As(err, &typed) || !typed.OutcomeUnknown || len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
				t.Fatalf("retry erased original uncertainty: %v %+v", err, requests)
			}
			if err := vu.executeLiveTableAction(context.Background(), liveAction("pop", nil)); err == nil || len(requests) != 2 {
				t.Fatal("new consume allowed after uncertain original")
			}
			if handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "target", Type: "request"}); !handled || err == nil {
				t.Fatal("target action allowed after uncertain original")
			}
		})
	}
}

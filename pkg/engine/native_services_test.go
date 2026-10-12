package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func nativeTestVU(t *testing.T, profile livetable.Profile, operations ...string) (*VUser, *livetable.Manager) {
	t.Helper()
	m := livetable.NewManager()
	_, err := m.Create(livetable.Config{ID: "table", RunID: "run", Profile: profile, Columns: []string{"id", "empty", "counter"}})
	if err != nil {
		t.Fatal(err)
	}
	vu := liveTestVU(m)
	vu.LiveTableBindings[0].Profile = profile
	vu.Script.Config.NativeServices = &dsl.NativeServicesConfig{LiveTables: []dsl.NativeLiveTableRequirement{{Alias: "work", Operations: operations}}}
	if err := vu.prepareRuntimeState(); err != nil {
		t.Fatal(err)
	}
	return vu, m
}
func nativeTestInvocation(t *testing.T, vu *VUser) protocol.NativeInvocation {
	t.Helper()
	i, err := vu.nativeServices.BeginInvocation(context.Background(), "trusted-invocation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(i.Close)
	return i
}
func TestNativeServicesLiteralPresenceAndExactRetry(t *testing.T) {
	for _, profile := range []livetable.Profile{livetable.ProfileNative, livetable.ProfileVTS} {
		t.Run(string(profile), func(t *testing.T) {
			vu, _ := nativeTestVU(t, profile, "push", "pop")
			vu.Variables["replacement"] = "should-not-resolve"
			i := nativeTestInvocation(t, vu)
			push := protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "{{var.replacement}} {{secret.literal}} 雪", "empty": ""}}
			first, err := i.Call(context.Background(), 1, push)
			if err != nil {
				t.Fatal(err)
			}
			again, err := i.Call(context.Background(), 1, push)
			if err != nil || first.Revision != again.Revision {
				t.Fatalf("retry %+v %v", again, err)
			}
			out, err := i.Call(context.Background(), 2, protocol.NativeDataRequest{Alias: "work", Operation: "pop"})
			if err != nil || out.Row == nil || out.Row.Values["id"] != push.Values["id"] {
				t.Fatalf("literal result %+v %v", out, err)
			}
			if value, ok := out.Row.Values["empty"]; !ok || value != "" {
				t.Fatal("empty presence lost")
			}
			if _, ok := out.Row.Values["counter"]; ok {
				t.Fatal("absent cell invented")
			}
			out, err = i.Call(context.Background(), 3, protocol.NativeDataRequest{Alias: "work", Operation: "pop"})
			if err != nil || out.Status != "empty" || out.Row != nil {
				t.Fatalf("empty %+v %v", out, err)
			}
		})
	}
}
func TestNativeServicesDeclarationsAndLeasesAreLaneBound(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push", "lease", "ack")
	i := nativeTestInvocation(t, vu)
	for n, q := range []protocol.NativeDataRequest{{Alias: "forged", Operation: "push", Values: map[string]string{"id": "x"}}, {Alias: "work", Operation: "pop"}} {
		if _, err := i.Call(context.Background(), uint64(n+1), q); err == nil {
			t.Fatal("undeclared call accepted")
		}
	}
	if _, err := i.Call(context.Background(), 3, protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "x"}}); err != nil {
		t.Fatal(err)
	}
	lease, err := i.Call(context.Background(), 4, protocol.NativeDataRequest{Alias: "work", Operation: "lease"})
	if err != nil || lease.LeaseRef == "" {
		t.Fatalf("lease %+v %v", lease, err)
	}
	encoded, _ := json.Marshal(lease)
	vu.liveTables.mu.Lock()
	for _, a := range vu.liveTables.leases {
		if strings.Contains(string(encoded), a.lease.Token) {
			t.Fatal("raw lease token exposed")
		}
	}
	vu.liveTables.mu.Unlock()
	branch := liveTestVU(vu.LiveTableClient)
	branch.Script = vu.Script
	branch.liveTables = vu.liveTables
	branch.liveTableLane = vu.liveTables.branchLane(vu.liveTableLane, 0)
	if err := branch.prepareNativeServices(); err != nil {
		t.Fatal(err)
	}
	other := nativeTestInvocation(t, branch)
	if _, err := other.Call(context.Background(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "ack", LeaseRef: lease.LeaseRef}); err == nil {
		t.Fatal("cross-lane handle accepted")
	}
	i.Close()
	next := nativeTestInvocation(t, vu)
	if _, err := next.Call(context.Background(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "ack", LeaseRef: lease.LeaseRef}); err != nil {
		t.Fatalf("same iteration handle did not survive method: %v", err)
	}
	if _, err := next.Call(context.Background(), 2, protocol.NativeDataRequest{Alias: "work", Operation: "ack", LeaseRef: lease.LeaseRef}); err == nil {
		t.Fatal("settled handle accepted")
	}
}
func TestNativeServicesManagedSecretAndIntegerBoundaries(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push", "increment")
	vu.RedactionConfig = WithSecretValueRedaction(nil, []string{"managed-sensitive-value"})
	vu.bindRuntimeTextRedactor()
	i := nativeTestInvocation(t, vu)
	if _, err := i.Call(context.Background(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "managed-sensitive-value"}}); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("secret guard %v", err)
	}
	row, err := i.Call(context.Background(), 2, protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"counter": "9223372036854775806"}})
	if err != nil {
		t.Fatal(err)
	}
	q := protocol.NativeDataRequest{Alias: "work", Operation: "increment", RecordID: row.Row.ID, Columns: []string{"counter"}, Delta: "1"}
	out, err := i.Call(context.Background(), 3, q)
	if err != nil || out.Row.Values["counter"] != "9223372036854775807" {
		t.Fatalf("int64 %+v %v", out, err)
	}
	if _, err := i.Call(context.Background(), 4, q); err == nil {
		t.Fatal("overflow accepted")
	}
	q.Delta = "9223372036854775808"
	if _, err := i.Call(context.Background(), 5, q); err == nil {
		t.Fatal("out-of-range delta accepted")
	}
}
func TestNativeServicesUnknownStopsInvocationButKnownLeaseCanRelease(t *testing.T) {
	vu, m := nativeTestVU(t, livetable.ProfileNative, "push", "lease", "pop")
	i := nativeTestInvocation(t, vu)
	if _, err := i.Call(context.Background(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "held"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Call(context.Background(), 2, protocol.NativeDataRequest{Alias: "work", Operation: "lease"}); err != nil {
		t.Fatal(err)
	}
	vu.liveTables.client = liveTableClientFunc(func(ctx context.Context, id string, r livetable.Request) (livetable.Result, error) {
		if r.Operation == livetable.OpPop {
			return livetable.Result{}, errors.New("lost reply")
		}
		return m.Execute(ctx, id, r)
	})
	_, err := i.Call(context.Background(), 3, protocol.NativeDataRequest{Alias: "work", Operation: "pop"})
	var typed *protocol.NativeDataError
	if !errors.As(err, &typed) || !typed.OutcomeUnknown {
		t.Fatalf("unknown lost: %v", err)
	}
	select {
	case <-i.Done():
	case <-time.After(time.Second):
		t.Fatal("guest not canceled")
	}
	_ = vu.settleLiveTableLeases(context.Background(), false, false)
	if len(vu.liveTables.leases) != 0 {
		t.Fatal("known lease could not be released after lane poisoning")
	}
	result, err := m.Execute(context.Background(), "table", livetable.Request{Operation: livetable.OpRead, Position: 1})
	if err != nil || result.Row == nil {
		t.Fatalf("released work missing %+v %v", result, err)
	}
}

func TestNativeServicesDispatchedDeadlineRetainsUnknownOutcome(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "pop")
	binding := vu.liveTables.bindings["work"]
	binding.OpTimeout = 20 * time.Millisecond
	vu.liveTables.bindings["work"] = binding
	calls := 0
	vu.liveTables.client = liveTableClientFunc(func(ctx context.Context, _ string, _ livetable.Request) (livetable.Result, error) {
		calls++
		<-ctx.Done()
		return livetable.Result{}, ctx.Err()
	})
	i := nativeTestInvocation(t, vu)
	_, err := i.Call(t.Context(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "pop"})
	var typed *protocol.NativeDataError
	if !errors.As(err, &typed) || !typed.OutcomeUnknown {
		t.Fatalf("dispatched deadline lost uncertainty: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expired operation was retried %d times", calls)
	}
	select {
	case <-i.Done():
	default:
		t.Fatal("uncertain deadline retained guest authority")
	}
}

func TestLiveTablePreDispatchCancellationIsDefinite(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "pop")
	vu.liveTables.client = liveTableClientFunc(func(context.Context, string, livetable.Request) (livetable.Result, error) {
		t.Fatal("pre-dispatch canceled operation reached client")
		return livetable.Result{}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := vu.liveTables.execute(ctx, vu.liveTableLane, vu.liveTables.bindings["work"], livetable.Request{Operation: livetable.OpPop})
	if !errors.Is(err, context.Canceled) || vu.liveTableLane.failed || vu.liveTables.failed || vu.liveTableLane.sequence != 0 {
		t.Fatalf("pre-dispatch cancellation changed authority: %v", err)
	}
}
func TestNativeServicesChannelConflictAndCancellation(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push", "pop")
	i := nativeTestInvocation(t, vu)
	q := protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "one"}}
	if _, err := i.Call(context.Background(), 1, q); err != nil {
		t.Fatal(err)
	}
	q.Values["id"] = "two"
	if _, err := i.Call(context.Background(), 1, q); err == nil {
		t.Fatal("changed replay accepted")
	}
	select {
	case <-i.Done():
	default:
		t.Fatal("conflicting channel remains active")
	}
	i.Close()
	ctx, cancel := context.WithCancel(context.Background())
	j, err := vu.nativeServices.BeginInvocation(ctx, "wait")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	cancel()
	if _, err := j.Call(context.Background(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "pop"}); err == nil {
		t.Fatal("canceled invocation accepted work")
	}
}

func TestNativeServicesOwnRequestAndRetryResults(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push", "next")
	invocation := nativeTestInvocation(t, vu)
	request := protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "original"}}
	if _, err := invocation.Call(t.Context(), 1, request); err != nil {
		t.Fatal(err)
	}
	request.Values["id"] = "caller mutation"
	read := protocol.NativeDataRequest{Alias: "work", Operation: "next"}
	first, err := invocation.Call(t.Context(), 2, read)
	if err != nil || first.Row == nil || first.Row.Values["id"] != "original" {
		t.Fatalf("stored request changed: %+v %v", first, err)
	}
	first.Row.Values["id"] = "result mutation"
	retry, err := invocation.Call(t.Context(), 2, read)
	if err != nil || retry.Row == nil || retry.Row.Values["id"] != "original" {
		t.Fatalf("retained retry changed: %+v %v", retry, err)
	}
}

func TestNativeServicesLeaseHandleCapacityAndSettlement(t *testing.T) {
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push", "lease", "ack")
	invocation := nativeTestInvocation(t, vu)
	var sequence uint64
	call := func(request protocol.NativeDataRequest) (protocol.NativeDataResult, error) {
		sequence++
		return invocation.Call(t.Context(), sequence, request)
	}
	var refs []string
	for n := 0; n < maxNativeLeaseHandles; n++ {
		if _, err := call(protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "value"}}); err != nil {
			t.Fatal(err)
		}
		result, err := call(protocol.NativeDataRequest{Alias: "work", Operation: "lease"})
		if err != nil || result.LeaseRef == "" {
			t.Fatal(result, err)
		}
		refs = append(refs, result.LeaseRef)
	}
	if _, err := call(protocol.NativeDataRequest{Alias: "work", Operation: "lease"}); err == nil || !strings.Contains(err.Error(), "lease_capacity") {
		t.Fatal("lease capacity not enforced", err)
	}
	if _, err := call(protocol.NativeDataRequest{Alias: "work", Operation: "ack", LeaseRef: refs[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "replacement"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(protocol.NativeDataRequest{Alias: "work", Operation: "lease"}); err != nil {
		t.Fatal("settled handle capacity leaked", err)
	}
	if err := vu.settleLiveTableLeases(t.Context(), false, false); err != nil {
		t.Fatal(err)
	}
	vu.liveTables.mu.Lock()
	held := len(vu.liveTables.leases)
	pending := vu.liveTables.nativeLeasePending
	vu.liveTables.mu.Unlock()
	if held != 0 || pending != 0 {
		t.Fatalf("native handles retained after settlement %d/%d", held, pending)
	}
}

func TestNativeJavaOldPreparedWorkersRejectBeforeCommit(t *testing.T) {
	t.Run("pooled", func(t *testing.T) {
		peer, cfg, o := newPilotEngineFixture(t)
		vu, _ := nativeTestVU(t, livetable.ProfileNative, "push")
		cfg.LiveTableClient, cfg.LiveTableClientID, cfg.LiveTableBindings = vu.LiveTableClient, vu.LiveTableClientID, vu.LiveTableBindings
		cfg.Script.Config.NativeServices = &dsl.NativeServicesConfig{LiveTables: []dsl.NativeLiveTableRequirement{{Alias: "work", Operations: []string{"push"}}}}
		o.ScriptSHA256, _ = JavaPooledScriptSHA256(cfg.Script)
		e, err := NewJavaPooledPilot(cfg, o)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "native_data_pooled_v1") {
			t.Fatal("old prepared worker accepted", err)
		}
		for _, q := range peer.snapshot() {
			if q.Operation == "commit" || q.Operation == "invoke" {
				t.Fatal("user admission before SDK proof", q.Operation)
			}
		}
	})
	t.Run("broker", func(t *testing.T) {
		peer, cfg, o := brokerEngineFixture(t)
		vu, _ := nativeTestVU(t, livetable.ProfileNative, "push")
		cfg.LiveTableClient, cfg.LiveTableClientID, cfg.LiveTableBindings = vu.LiveTableClient, vu.LiveTableClientID, vu.LiveTableBindings
		cfg.Script.Config.NativeServices = &dsl.NativeServicesConfig{LiveTables: []dsl.NativeLiveTableRequirement{{Alias: "work", Operations: []string{"push"}}}}
		o.ScriptSHA256, _ = JavaPooledScriptSHA256(cfg.Script)
		e, err := NewJavaBrokerExecution(cfg, o)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "native_data_broker_v1") {
			t.Fatal("old prepared worker accepted", err)
		}
		peer.mu.Lock()
		defer peer.mu.Unlock()
		for _, q := range peer.calls {
			if q.Operation == "commit" || q.Operation == "invoke" {
				t.Fatal("user admission before SDK proof", q.Operation)
			}
		}
	})
	t.Run("broker_group", func(t *testing.T) {
		peer, _, cfg := brokerGroupFixture(t)
		vu, _ := nativeTestVU(t, livetable.ProfileNative, "push")
		cfg.Groups[0].LiveTableClient, cfg.Groups[0].LiveTableClientID, cfg.Groups[0].LiveTableBindings = vu.LiveTableClient, vu.LiveTableClientID, vu.LiveTableBindings
		cfg.Groups[0].Script.Config.NativeServices = &dsl.NativeServicesConfig{LiveTables: []dsl.NativeLiveTableRequirement{{Alias: "work", Operations: []string{"push"}}}}
		cfg.Groups[0].JavaBroker.ScriptSHA256, _ = JavaPooledScriptSHA256(cfg.Groups[0].Script)
		e, err := NewMultiGroup(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer e.ClosePreparedRuntime()
		if err = e.PreflightRuntime(t.Context()); err == nil || !strings.Contains(err.Error(), "native_data_broker_v1") {
			t.Fatal("old prepared worker accepted", err)
		}
		peer.mu.Lock()
		defer peer.mu.Unlock()
		for _, q := range peer.calls {
			if q.Operation == "commit" || q.Operation == "invoke" {
				t.Fatal("user admission before SDK proof", q.Operation)
			}
		}
	})
}

func TestNativeServicesManagedSecretsCannotBecomeSchemaOrCursor(t *testing.T) {
	const secret = "managed_secret_column"
	for _, op := range []string{"create_column", "next"} {
		t.Run(op, func(t *testing.T) {
			vu, manager := nativeTestVU(t, livetable.ProfileVTS, "push", op)
			vu.RedactionConfig = WithSecretValueRedaction(nil, []string{secret})
			vu.bindRuntimeTextRedactor()
			invocation := nativeTestInvocation(t, vu)
			if _, err := invocation.Call(t.Context(), 1, protocol.NativeDataRequest{Alias: "work", Operation: "push", Values: map[string]string{"id": "safe"}}); err != nil {
				t.Fatal(err)
			}
			request := protocol.NativeDataRequest{Alias: "work", Operation: op}
			if op == "create_column" {
				request.Columns = []string{secret}
			} else {
				request.Cursor = secret
			}
			if _, err := invocation.Call(t.Context(), 2, request); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("persisted secret admission: %v", err)
			}
			table, _ := manager.Get("table")
			snapshot, err := table.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(snapshot)
			if strings.Contains(string(data), secret) {
				t.Fatal("managed secret persisted in checkpoint")
			}
		})
	}
}

package javasidecar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

type invocationFixture struct {
	run       *PooledRun
	client    *Client
	host      *preparationTestHost
	placement PooledControlResponse
	commit    PooledControlRequest
	paths     map[string]string
	calls     atomic.Uint64
	hostID    string
}

func newInvocationFixture(t *testing.T) *invocationFixture {
	t.Helper()
	f := &invocationFixture{paths: map[string]string{}}
	f.host = &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(_ context.Context, values map[string]map[string]string) error {
		for slot, v := range values {
			f.paths[slot] = v["APEXLOAD_JAVA_VUSER_EVENT_FILE"]
		}
		return nil
	}}
	f.host.invocation = func(_ context.Context, slot, method string, params map[string]string) (javarunner.PooledFrame, bool, error) {
		n := f.calls.Add(1)
		frame := invocationTestFrame(f.hostID, slot, n)
		data := invocationEvent(frame.Request, 1, method)
		if err := os.WriteFile(f.paths[slot], []byte(data), 0600); err != nil {
			return frame, false, err
		}
		return frame, false, nil
	}
	var prepare PooledControlRequest
	f.run, f.client, f.placement, prepare = preparationControlFixture(t, f.host, true, true)
	f.hostID = f.placement.Placements[0].HostID
	if _, err := f.client.PooledControl(context.Background(), prepare); err != nil {
		t.Fatal(err)
	}
	f.commit = prepare
	f.commit.Operation, f.commit.RequestID, f.commit.Slots = "commit", "commit-1", nil
	if _, err := f.client.PooledControl(context.Background(), f.commit); err != nil {
		t.Fatal(err)
	}
	return f
}

func invocationTestFrame(host, slot string, n uint64) javarunner.PooledFrame {
	return javarunner.PooledFrame{Generation: host, Slot: slot, Sequence: n, Request: strconv.FormatUint(n, 10), Operation: "invoke", Fields: map[string]string{"code": "ok", "reap": "false", "return_code": "0", "event_fallback": "false", "event_truncated": "false"}}
}

func invocationEvent(request string, sequence int, message string) string {
	return fmt.Sprintf("{\"contract\":%q,\"request_id\":%q,\"sequence\":%d,\"timestamp_ms\":1,\"type\":\"message\",\"message\":%q}\n", harnessEventContract, request, sequence, message)
}

func (f *invocationFixture) request(slotIndex int, seq uint64, method string) PooledControlRequest {
	q := f.commit
	q.Operation, q.HostID, q.SlotID = "invoke", f.hostID, f.placement.Placements[0].SlotIDs[slotIndex]
	q.Sequence, q.Method, q.TimeoutMS = seq, method, 2000
	q.RequestID = "invoke-" + q.SlotID + "-" + strconv.FormatUint(seq, 10)
	return q
}

func TestPooledInvocationLifecycleAndAtMostOnce(t *testing.T) {
	f := newInvocationFixture(t)
	for i, method := range []string{"init", "action", "action", "end"} {
		q := f.request(0, uint64(i+1), method)
		r, err := f.client.PooledControl(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if r.Invocation == nil || !strings.Contains(r.Invocation.Events, method) || strings.Count(r.Invocation.Events, "\n") != 1 || r.Invocation.EventsTruncated {
			t.Fatalf("bad capture: %+v", r.Invocation)
		}
		_, err = f.client.PooledControl(context.Background(), q)
		requireControlError(t, err, "pooled_request_conflict")
	}
	_, err := f.client.PooledControl(context.Background(), f.request(0, 5, "action"))
	requireControlError(t, err, "pooled_host_state_invalid")
	_, err = f.client.PooledControl(context.Background(), f.request(1, 1, "action"))
	requireControlError(t, err, "pooled_host_state_invalid")
	if _, err := f.client.PooledControl(context.Background(), f.request(1, 1, "init")); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 5 {
		t.Fatal("rejected calls reached host")
	}
	for i := range 2 {
		if _, err := f.client.PooledControl(context.Background(), closeSlotControl(f.commit, f.placement, i)); err != nil {
			t.Fatal(err)
		}
	}
	waitCaptureCleanup(t, preparationRecord(f.run, f.commit.ReservationID))
}

func TestPooledInvocationBusyAndCancellationPreservePeer(t *testing.T) {
	f := newInvocationFixture(t)
	entered := make(chan struct{})
	f.host.invocation = func(ctx context.Context, slot, method string, _ map[string]string) (javarunner.PooledFrame, bool, error) {
		close(entered)
		<-ctx.Done()
		return javarunner.PooledFrame{}, true, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	q := f.request(0, 1, "init")
	go func() { _, err := f.client.PooledControl(ctx, q); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("invocation did not start")
	}
	_, err := f.client.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_slot_busy")
	renew := f.commit
	renew.Operation, renew.RequestID, renew.ReservationID = "renew", "renew-busy", ""
	if _, err := f.client.PooledControl(context.Background(), renew); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled invocation succeeded")
	}
	capture := preparationRecord(f.run, q.ReservationID).captures[q.SlotID]
	capture.mu.Lock()
	capture.mu.Unlock() // wait for the server's closure receipt
	_, usage := f.run.ledger.Snapshot()
	if usage.Hosts != 1 || usage.VUsers != 1 {
		t.Fatalf("cooperative cancellation reaped peer: %+v", usage)
	}
	if err := f.run.ledger.CanInvoke(f.run.owner.epoch, f.hostID, f.placement.Placements[0].SlotIDs[1]); err != nil {
		t.Fatal(err)
	}
	_, err = f.client.PooledControl(context.Background(), f.request(0, 2, "init"))
	requireControlError(t, err, "pooled_host_state_invalid")
}

func TestPooledInvocationRejectsReplacedCapture(t *testing.T) {
	f := newInvocationFixture(t)
	q := f.request(0, 1, "init")
	path := f.paths[q.SlotID]
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("private replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := f.client.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_event_capture_invalid")
	if f.calls.Load() != 0 {
		t.Fatal("invalid capture reached user code")
	}
	waitCaptureCleanup(t, preparationRecord(f.run, q.ReservationID))
}

func TestPooledInvocationMalformedFrameOrEventReapsHost(t *testing.T) {
	for _, failure := range []string{"foreign-event", "malformed-event", "foreign-frame", "failed-init"} {
		t.Run(failure, func(t *testing.T) {
			f := newInvocationFixture(t)
			f.host.invocation = func(_ context.Context, slot, method string, _ map[string]string) (javarunner.PooledFrame, bool, error) {
				frame := invocationTestFrame(f.hostID, slot, 1)
				data := invocationEvent("1", 1, "event")
				switch failure {
				case "foreign-event":
					data = invocationEvent("another", 1, "event")
				case "malformed-event":
					data = "broken\n"
				case "foreign-frame":
					frame.Slot = "another"
				case "failed-init":
					frame.Fields["return_code"] = "1"
				}
				if err := os.WriteFile(f.paths[slot], []byte(data), 0600); err != nil {
					return frame, false, err
				}
				return frame, false, nil
			}
			q := f.request(0, 1, "init")
			r, err := f.client.PooledControl(context.Background(), q)
			if failure == "failed-init" {
				if err != nil || r.Invocation.ReturnCode != 1 {
					t.Fatalf("init failure lost: %+v %v", r, err)
				}
			} else if err == nil {
				t.Fatal("invalid result accepted")
			}
			waitCaptureCleanup(t, preparationRecord(f.run, q.ReservationID))
			if f.host.stops.Load() != 1 {
				t.Fatal("invalid capture did not reap host")
			}
		})
	}
}

func TestPooledInvocationExitAndClientValidation(t *testing.T) {
	f := newInvocationFixture(t)
	if _, err := f.client.PooledControl(context.Background(), f.request(0, 1, "init")); err != nil {
		t.Fatal(err)
	}
	f.host.invocation = func(_ context.Context, slot, method string, _ map[string]string) (javarunner.PooledFrame, bool, error) {
		frame := invocationTestFrame(f.hostID, slot, 2)
		frame.Fields["exit_option"], frame.Fields["exit_status"] = "2", "1"
		frame.Fields["event_fallback"] = "true"
		return frame, false, nil
	}
	q := f.request(0, 2, "action")
	r, err := f.client.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Invocation.Exit == nil || r.Invocation.Exit.Option != 2 || r.Invocation.Exit.Status != 1 || !r.Invocation.EventsTruncated {
		t.Fatalf("exit/capture flags lost: %+v", r.Invocation)
	}
	for _, mutate := range []func(*PooledInvocationResult){
		func(r *PooledInvocationResult) { r.Sequence++ }, func(r *PooledInvocationResult) { r.Method = "end" },
		func(r *PooledInvocationResult) { r.EventRequestID = "01" }, func(r *PooledInvocationResult) { r.Events = invocationEvent("other", 1, "event") },
		func(r *PooledInvocationResult) { r.DurationMS = -1 }, func(r *PooledInvocationResult) { r.Exit = &PooledExit{Option: 99} },
	} {
		bad := *r.Invocation
		mutate(&bad)
		if validPooledInvocationResult(q, &bad) {
			t.Fatalf("invalid result accepted: %+v", bad)
		}
	}
}

func TestPooledCaptureBoundsAndFileIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	c := &pooledSlotCapture{file: file, path: path}
	for _, data := range []string{"{", invocationEvent("other", 1, "event"), invocationEvent("1", 1, "a") + invocationEvent("1", 1, "b")} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.read("1"); !errors.Is(err, errPooledEventCapture) {
			t.Fatalf("invalid capture accepted: %v", err)
		}
	}
	data := invocationEvent("1", 1, "complete") + invocationEvent("1", 2, strings.Repeat("x", MaxCaptureBytes))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, truncated, err := c.read("1")
	if err != nil || !truncated || got != invocationEvent("1", 1, "complete") {
		t.Fatalf("cap split record: len=%d truncated=%v err=%v", len(got), truncated, err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("private replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.reset(); !errors.Is(err, errPooledEventCapture) {
		t.Fatal("replacement was truncated")
	}
	gotBytes, _ := os.ReadFile(path)
	if string(gotBytes) != "private replacement" {
		t.Fatal("changed replacement file")
	}
}

func TestPooledInvocationOptInAndInputValidation(t *testing.T) {
	p := controlProfileFixture(t)
	p.AllowInvocation = true
	if _, err := NewPooledControlProfiles(map[string]PooledControlProfile{"pilot": p}); err == nil {
		t.Fatal("invocation without commit allowed")
	}
	_, c, _ := controlServerFixture(t, true, true)
	claim := claimControl(t, c)
	q := controlRequest(claim, "invoke", "invoke-slot-1")
	q.ReservationID, q.HostID, q.SlotID, q.Method, q.Sequence, q.TimeoutMS = "reservation", "host", "slot", "init", 1, 1000
	_, err := c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_invocation_disabled")
	q.OwnerID, q.OwnerEpoch = "owner", "epoch"
	for _, mutate := range []func(*PooledControlRequest){
		func(q *PooledControlRequest) { q.Method = "full_lifecycle" }, func(q *PooledControlRequest) { q.Sequence = 0 }, func(q *PooledControlRequest) { q.RequestID = "unbound" },
		func(q *PooledControlRequest) { q.TimeoutMS = 0 }, func(q *PooledControlRequest) { q.Params = map[string]string{"": "value"} },
		func(q *PooledControlRequest) { q.Params = map[string]string{"key": strings.Repeat("x", 65537)} },
	} {
		bad := q
		mutate(&bad)
		if validPooledControlRequest(bad) {
			t.Fatalf("invalid invocation accepted: %+v", bad)
		}
	}
}

func TestPooledInvocationConcurrentClosePreservesPeer(t *testing.T) {
	f := newInvocationFixture(t)
	entered, closed := make(chan struct{}), make(chan struct{})
	f.host.invocation = func(context.Context, string, string, map[string]string) (javarunner.PooledFrame, bool, error) {
		close(entered)
		<-closed
		return javarunner.PooledFrame{}, false, errors.New("slot_closed")
	}
	f.host.closeSlot = func(context.Context, string) error { close(closed); return nil }
	result := make(chan error, 1)
	go func() { _, err := f.client.PooledControl(context.Background(), f.request(0, 1, "init")); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("invocation never started")
	}
	if _, err := f.client.PooledControl(context.Background(), closeSlotControl(f.commit, f.placement, 0)); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("closed invocation reported success")
	}
	_, usage := f.run.ledger.Snapshot()
	if usage.Hosts != 1 || usage.VUsers != 1 || f.host.stops.Load() != 0 {
		t.Fatalf("cooperative closure reaped peer: %+v", usage)
	}
	if err := f.run.ledger.CanInvoke(f.run.owner.epoch, f.hostID, f.placement.Placements[0].SlotIDs[1]); err != nil {
		t.Fatal(err)
	}
}

func TestPooledInvocationIdentityNamespace(t *testing.T) {
	q := PooledControlRequest{Operation: "renew", RequestID: "invoke-slot-1", OwnerID: "owner", OwnerEpoch: "epoch", RunID: "run", RunToken: strings.Repeat("a", 64)}
	if validPooledControlRequest(q) {
		t.Fatal("control operation reused reserved invocation namespace")
	}
}

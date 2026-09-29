package javasidecar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

type preparationTestHost struct {
	diagnostics func(context.Context) (javarunner.HostDiagnostics, error)
	invocation  func(context.Context, string, string, map[string]string) (javarunner.PooledFrame, bool, error)
	ownedTestHost
	ready     func(context.Context, map[string]map[string]string) error
	commits   atomic.Int32
	commit    func(context.Context) error
	closeSlot func(context.Context, string) error
}

func (h *preparationTestHost) Diagnostics(ctx context.Context) (javarunner.HostDiagnostics, error) {
	if h.diagnostics != nil {
		return h.diagnostics(ctx)
	}
	return javarunner.HostDiagnostics{}, nil
}

func (h *preparationTestHost) Invoke(ctx context.Context, slot, method string, params map[string]string) (javarunner.PooledFrame, bool, error) {
	if h.invocation != nil {
		return h.invocation(ctx, slot, method, params)
	}
	return h.ownedTestHost.Invoke(ctx, slot, method, params)
}

func (h *preparationTestHost) Ready(ctx context.Context, values map[string]map[string]string) error {
	return h.ready(ctx, values)
}
func (h *preparationTestHost) Commit(ctx context.Context) error {
	h.commits.Add(1)
	if h.commit != nil {
		return h.commit(ctx)
	}
	return nil
}

func (h *preparationTestHost) CloseSlot(ctx context.Context, slot string) error {
	if h.closeSlot != nil {
		return h.closeSlot(ctx, slot)
	}
	return nil
}

// Only the private in-package seam substitutes containment. The production
// HTTP claim still constructs the qualification-enforcing local backend.
func preparationControlFixture(t *testing.T, h *preparationTestHost, allowCommit ...bool) (*PooledRun, *Client, PooledControlResponse, PooledControlRequest) {
	t.Helper()
	enabled := len(allowCommit) > 0 && allowCommit[0]
	invokeEnabled := len(allowCommit) > 1 && allowCommit[1]
	s, c, _ := controlServerFixture(t, true, enabled, invokeEnabled)
	p := s.pooledControlProfiles.profiles["pilot"]
	d := p.Selection.Decision()
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	run, err := s.openPooledRun(context.Background(), owner, epoch, "execution", "group", p.Budget, d.ConfigurationSHA256, javarunner.HostProfile{SlotsPerHost: d.SlotsPerHost, MemoryBytesPerHost: d.HostMemoryBytes}, pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
	if err != nil {
		t.Fatal(err)
	}
	s.pooledMu.Lock()
	run.controlID, run.controlToken = "pool-fixture", strings.Repeat("a", 64)
	run.controlDecision = PooledControlDecision{TargetClass: d.TargetClass, PreparationEnabled: true, CommitEnabled: enabled, InvocationEnabled: invokeEnabled, Mode: d.Mode, ProfileID: "pilot", ResourceProfile: d.ResourceProfile, ConfigurationSHA256: d.ConfigurationSHA256, ContainmentBackend: d.ContainmentBackend, SlotsPerHost: d.SlotsPerHost, HostMemoryBytes: d.HostMemoryBytes, MaxHosts: p.Budget.MaxHosts, MaxVUsers: p.Budget.MaxVUsers, MaxMemoryBytes: p.Budget.MaxMemoryBytes}
	s.pooledMu.Unlock()
	claim := PooledControlResponse{RunID: run.controlID, RunToken: run.controlToken}
	reserve := controlRequest(claim, "reserve", "reserve-1")
	reserve.VUsers, reserve.PrepareTimeoutMS = 2, 2000
	allocation, err := c.PooledControl(context.Background(), reserve)
	if err != nil {
		t.Fatal(err)
	}
	q := controlRequest(claim, "prepare", "prepare-1")
	q.ReservationID = allocation.ReservationID
	for i, slot := range allocation.Placements[0].SlotIDs {
		q.Slots = append(q.Slots, PooledSlotContext{SlotID: slot, VUserID: int64(i + 1), IgnoreThinkTime: i == 0, ThinkTimeLimitMS: int64(i * 100)})
	}
	return run, c, allocation, q
}

func preparationRecord(run *PooledRun, reservation string) *pooledPreparation {
	run.controlMu.Lock()
	defer run.controlMu.Unlock()
	return run.preparations[reservation]
}

func waitCaptureCleanup(t *testing.T, p *pooledPreparation) {
	t.Helper()
	select {
	case <-p.captureDone:
	case <-time.After(3 * time.Second):
		t.Fatal("capture cleanup did not finish")
	}
}

func TestPooledCaptureRelativeTemporaryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR is the Unix temporary-directory setting")
	}
	t.Chdir(t.TempDir())
	if err := os.Mkdir("relative-temp", 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "relative-temp")
	run := &PooledRun{execution: "execution", group: "group"}
	p := &pooledPreparation{slots: []PooledSlotContext{{SlotID: "slot", VUserID: 1}}}
	values, err := run.prepareCaptures(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.closeCaptures(); _ = os.RemoveAll(p.directory) }()
	path := values["slot"]["APEXLOAD_JAVA_VUSER_EVENT_FILE"]
	if !filepath.IsAbs(path) {
		t.Fatalf("JVM working directory would change capture path: %q", path)
	}
	if err := os.Chdir("relative-temp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("capture path changed with cwd: %v", err)
	}
}

func TestPooledPreparationColdContextsReplayAndCleanup(t *testing.T) {
	var received map[string]map[string]string
	var calls atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(_ context.Context, values map[string]map[string]string) error {
		received = values
		calls.Add(1)
		return nil
	}}
	run, c, allocation, q := preparationControlFixture(t, h)
	// Direct engine Vusers use zero-based IDs; preserve them through readiness.
	q.Slots[0].VUserID = 0
	r, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Ready || r.PrepareDeadlineUnixMS != allocation.PrepareDeadlineUnixMS || h.commits.Load() != 0 {
		t.Fatalf("unexpected preparation: %+v", r)
	}
	paths := map[string]bool{}
	for _, slot := range q.Slots {
		values := received[slot.SlotID]
		if len(values) != 6 || values["APEXLOAD_GROUP_NAME"] != "group" || values["APEXLOAD_SCENARIO_ID"] != "execution" || values["APEXLOAD_VUSER_ID"] != strconv.FormatInt(slot.VUserID, 10) {
			t.Fatalf("bad context: %v", values)
		}
		path := values["APEXLOAD_JAVA_VUSER_EVENT_FILE"]
		if paths[path] || !filepath.IsAbs(path) {
			t.Fatal("capture paths not distinct and absolute")
		}
		paths[path] = true
		file, err := os.Stat(path)
		if err != nil || file.Size() != 0 || (runtime.GOOS != "windows" && file.Mode().Perm() != 0600) {
			t.Fatalf("capture file: %v %v", file, err)
		}
		dir, err := os.Stat(filepath.Dir(path))
		if err != nil || (runtime.GOOS != "windows" && dir.Mode().Perm() != 0700) {
			t.Fatalf("capture directory: %v %v", dir, err)
		}
	}
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("replay repeated preparation")
	}
	changed := q
	changed.Slots = append([]PooledSlotContext(nil), q.Slots...)
	changed.Slots[0].IgnoreThinkTime = false
	_, err = c.PooledControl(context.Background(), changed)
	requireControlError(t, err, "pooled_request_conflict")
	changed = q
	changed.RequestID = "new-prepare"
	_, err = c.PooledControl(context.Background(), changed)
	requireControlError(t, err, "pooled_request_conflict")
	abort := q
	abort.Operation, abort.RequestID, abort.Slots = "abort", "abort-1", nil
	if _, err := c.PooledControl(context.Background(), abort); err != nil {
		t.Fatal(err)
	}
	_, err = c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_host_state_invalid")
	p := preparationRecord(run, q.ReservationID)
	waitCaptureCleanup(t, p)
	if _, err := os.Stat(p.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capture retained after positive cleanup: %v", err)
	}
}

func TestPooledPreparationAbortAndRenewDuringBlockedReady(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(ctx context.Context, _ map[string]map[string]string) error {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	run, c, _, q := preparationControlFixture(t, h)
	result := make(chan error, 2)
	go func() { _, err := c.PooledControl(context.Background(), q); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preparation did not enter")
	}
	go func() { _, err := c.PooledControl(context.Background(), q); result <- err }()
	for _, op := range []string{"renew", "abort"} {
		control := controlRequest(PooledControlResponse{RunID: q.RunID, RunToken: q.RunToken}, op, op+"-blocked")
		if op == "abort" {
			control.ReservationID = q.ReservationID
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := c.PooledControl(ctx, control)
		cancel()
		if err != nil {
			t.Fatalf("%s blocked by prepare: %v", op, err)
		}
	}
	close(release)
	for range 2 {
		if err := <-result; err == nil {
			t.Fatal("aborted preparation reported ready")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent retry provisioned twice")
	}
	p := preparationRecord(run, q.ReservationID)
	waitCaptureCleanup(t, p)
}

func TestPooledPreparationRetainsUncertainCaptures(t *testing.T) {
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{}), err: javarunner.ErrProcessContainmentFailed}, ready: func(context.Context, map[string]map[string]string) error { return nil }}
	run, c, _, q := preparationControlFixture(t, h)
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	p := preparationRecord(run, q.ReservationID)
	t.Cleanup(func() { _ = os.RemoveAll(p.directory) }) // mock runtime; no real process
	if err := run.Close(context.Background()); err == nil {
		t.Fatal("uncertain containment reported closed")
	}
	waitCaptureCleanup(t, p)
	if _, err := os.Stat(p.directory); err != nil {
		t.Fatalf("uncertain captures lost: %v", err)
	}
	for _, capture := range p.captures {
		if capture.file != nil {
			t.Fatal("quarantine retained an unnecessary runner file descriptor")
		}
	}
	_, usage := run.ledger.Snapshot()
	if usage.Hosts == 0 {
		t.Fatal("uncertain host reservation released")
	}
}

func TestPooledPreparationOptInAndTypedValidation(t *testing.T) {
	_, c, _ := controlServerFixture(t)
	claim := claimControl(t, c)
	if claim.Decision.PreparationEnabled {
		t.Fatal("preparation enabled by default")
	}
	q := controlRequest(claim, "prepare", "prepare")
	q.ReservationID, q.Slots = "reservation", []PooledSlotContext{{SlotID: "slot", VUserID: 1}}
	_, err := c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_preparation_disabled")
	for _, slots := range [][]PooledSlotContext{nil, {{SlotID: "slot", VUserID: -1}}, {{SlotID: "slot", VUserID: 2147483648}}, {{SlotID: "slot", VUserID: 1, ThinkTimeLimitMS: -1}}, {{SlotID: "slot", VUserID: 1}, {SlotID: "slot", VUserID: 2}}, {{SlotID: "slot", VUserID: 1}, {SlotID: "other", VUserID: 1}}} {
		if validPooledSlots(slots) {
			t.Fatalf("invalid slots accepted: %v", slots)
		}
	}
	s, enabledClient, _ := controlServerFixture(t, true)
	if !claimControl(t, enabledClient).Decision.PreparationEnabled {
		t.Fatal("operator preparation opt-in missing from decision")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/pooled-control", strings.NewReader(`{"slots":[{"slot_id":"slot","vuser_id":1,"event_file":"/tmp/untrusted"}]}`))
	s.handlePooledControl(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("remote path accepted: %d", w.Code)
	}
}

func TestPooledPreparationDisconnectDoesNotRetry(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(ctx context.Context, _ map[string]map[string]string) error {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}
	run, c, _, q := preparationControlFixture(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.PooledControl(ctx, q); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preparation did not start")
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	p := preparationRecord(run, q.ReservationID)
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel preparation")
	}
	_, err := c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_control_failed")
	if calls.Load() != 1 {
		t.Fatal("failed preparation retried")
	}
	waitCaptureCleanup(t, p)
}

func TestPooledPreparationRejectsForeignSlotsBeforeStarting(t *testing.T) {
	var calls atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { calls.Add(1); return nil }}
	run, c, _, q := preparationControlFixture(t, h)
	bad := q
	bad.Slots = append([]PooledSlotContext(nil), q.Slots...)
	bad.Slots[0].SlotID = "foreign-slot"
	_, err := c.PooledControl(context.Background(), bad)
	requireControlError(t, err, "pooled_host_identity_invalid")
	if preparationRecord(run, q.ReservationID) != nil || calls.Load() != 0 {
		t.Fatal("invalid slot context started preparation")
	}
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	// The same Vuser cannot be admitted into another reservation, even with
	// different slot/request IDs. It must not replay initialization later.
	reserve := controlRequest(PooledControlResponse{RunID: q.RunID, RunToken: q.RunToken}, "reserve", "reserve-other")
	reserve.VUsers, reserve.PrepareTimeoutMS = 1, 1000
	other, err := c.PooledControl(context.Background(), reserve)
	if err != nil {
		t.Fatal(err)
	}
	bad.RequestID, bad.ReservationID = "prepare-other", other.ReservationID
	bad.Slots = []PooledSlotContext{{SlotID: other.Placements[0].SlotIDs[0], VUserID: 1}}
	_, err = c.PooledControl(context.Background(), bad)
	requireControlError(t, err, "pooled_host_identity_invalid")
}

func TestPooledPreparationOriginalDeadlineExpiresBlockedReady(t *testing.T) {
	var observed time.Time
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(ctx context.Context, _ map[string]map[string]string) error {
		observed, _ = ctx.Deadline()
		<-ctx.Done()
		return ctx.Err()
	}}
	run, c, allocation, q := preparationControlFixture(t, h)
	_, err := c.PooledControl(context.Background(), q)
	if err == nil {
		t.Fatal("startup hang reported ready")
	}
	p := preparationRecord(run, q.ReservationID)
	<-p.done
	if observed.UnixMilli() != allocation.PrepareDeadlineUnixMS {
		t.Fatalf("preparation extended reservation deadline: %v", observed)
	}
	waitCaptureCleanup(t, p)
	if _, err := os.Stat(p.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired captures retained: %v", err)
	}
}

func TestPooledPreparationAbortBypassesFullReplayCache(t *testing.T) {
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }}
	run, c, _, q := preparationControlFixture(t, h)
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	run.controlMu.Lock()
	for i := len(run.controlReplies); i < maxPooledControlReplies; i++ {
		run.controlReplies[strconv.Itoa(i)] = pooledControlReply{}
	}
	run.controlMu.Unlock()
	abort := q
	abort.Operation, abort.RequestID, abort.Slots = "abort", "abort-full", nil
	for range 2 {
		if _, err := c.PooledControl(context.Background(), abort); err != nil {
			t.Fatal(err)
		}
	}
	run.controlMu.Lock()
	size := len(run.controlReplies)
	run.controlMu.Unlock()
	if size != maxPooledControlReplies {
		t.Fatal("abort grew bounded replay cache")
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
}

func TestPooledPreparationClientRequiresReadinessEvidence(t *testing.T) {
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }}
	_, c, _, q := preparationControlFixture(t, h)
	valid, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PooledControlResponse){
		func(r *PooledControlResponse) { r.Ready = false },
		func(r *PooledControlResponse) { r.Decision.PreparationEnabled = false },
		func(r *PooledControlResponse) { r.ReservationID = "other" },
		func(r *PooledControlResponse) { r.PrepareDeadlineUnixMS = 0 },
		func(r *PooledControlResponse) { r.Reaped = true },
		func(r *PooledControlResponse) { r.Placements = []PooledPlacement{{HostID: "unexpected"}} },
	} {
		r := valid
		mutate(&r)
		if validPooledControlResponse(q, r) {
			t.Fatalf("invalid readiness response accepted: %+v", r)
		}
	}
	q.Operation, valid.Operation = "renew", "renew"
	valid.ReservationID, valid.PrepareDeadlineUnixMS = "", 0
	if validPooledControlResponse(q, valid) {
		t.Fatal("unexpected readiness on renew accepted")
	}
}

package javasidecar

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

var errPooledPreparationDisabled = errors.New("pooled_preparation_disabled")

// PooledSlotContext contains only typed, Vuser-local settings. The runner binds
// group/scenario identity and capture paths; callers cannot supply environment
// variables or paths. Slot identities come from the reservation response.
type PooledSlotContext struct {
	SlotID           string `json:"slot_id"`
	VUserID          int64  `json:"vuser_id"`
	IgnoreThinkTime  bool   `json:"ignore_think_time,omitempty"`
	ThinkTimeLimitMS int64  `json:"think_time_limit_ms,omitempty"`
}

type pooledPreparation struct {
	captures    map[string]*pooledSlotCapture
	slots       []PooledSlotContext
	done        chan struct{}
	captureDone chan struct{}
	response    PooledControlResponse
	err         error
	directory   string
	commit      *pooledControlPhase // phase records are protected by run.controlMu
	closures    map[string]*pooledControlPhase
	// descriptors is this preparation's share of server.captureDescriptors,
	// returned exactly once when its capture files close.
	server       *Server
	descriptorMu sync.Mutex
	descriptors  int
}

func validPooledSlots(slots []PooledSlotContext) bool {
	if len(slots) == 0 || len(slots) > maxPooledControlVUsers {
		return false
	}
	ids, users := map[string]bool{}, map[int64]bool{}
	for _, slot := range slots {
		if !executionIDPattern.MatchString(slot.SlotID) || ids[slot.SlotID] || slot.VUserID < 0 || slot.VUserID > 2147483647 || users[slot.VUserID] || slot.ThinkTimeLimitMS < 0 {
			return false
		}
		ids[slot.SlotID], users[slot.VUserID] = true, true
	}
	return true
}

func (run *PooledRun) prepareControl(ctx context.Context, q PooledControlRequest, response PooledControlResponse) (PooledControlResponse, error) {
	if !run.controlDecision.PreparationEnabled {
		return response, errPooledPreparationDisabled
	}
	finish, err := run.begin(ctx)
	if err != nil {
		return response, err
	}
	defer finish()
	encoded, _ := json.Marshal(q)
	digest := sha256.Sum256(encoded)
	// Only claim/replay bookkeeping holds this lock. Provisioning and waiting
	// never block abort, renewal, or owner cleanup on the replay cache.
	run.controlMu.Lock()
	if previous, ok := run.controlReplies[q.RequestID]; ok {
		run.controlMu.Unlock()
		if previous.digest != digest || previous.preparation == nil {
			return response, errPooledRequestConflict
		}
		return run.waitPreparation(ctx, q.ReservationID, previous.preparation)
	}
	if len(run.controlReplies) >= maxPooledControlReplies {
		run.controlMu.Unlock()
		return response, errPooledControlLimit
	}
	if run.preparations[q.ReservationID] != nil {
		run.controlMu.Unlock()
		return response, errPooledRequestConflict
	}
	// A Vuser identity is admitted at most once in this run, including failed
	// preparations. Changing request IDs must not allow initialization replay.
	for _, previous := range run.preparations {
		for _, old := range previous.slots {
			for _, slot := range q.Slots {
				if old.VUserID == slot.VUserID {
					run.controlMu.Unlock()
					return response, javarunner.ErrHostIdentity
				}
			}
		}
	}
	hosts, _ := run.ledger.Snapshot()
	expected := map[string]bool{}
	for _, host := range hosts {
		if host.ReservationID == q.ReservationID {
			if host.State != javarunner.HostReserved {
				run.controlMu.Unlock()
				return response, javarunner.ErrHostState
			}
			for _, slot := range host.SlotIDs {
				expected[slot] = true
			}
		}
	}
	if !validPooledSlots(q.Slots) || len(expected) != len(q.Slots) {
		run.controlMu.Unlock()
		return response, javarunner.ErrHostIdentity
	}
	for _, slot := range q.Slots {
		if !expected[slot.SlotID] {
			run.controlMu.Unlock()
			return response, javarunner.ErrHostIdentity
		}
	}
	deadline, err := run.ledger.PreparationDeadline(run.owner.epoch, q.ReservationID)
	if err != nil {
		run.controlMu.Unlock()
		return response, err
	}
	response.ReservationID, response.PrepareDeadlineUnixMS = q.ReservationID, deadline.UnixMilli()
	p := &pooledPreparation{slots: append([]PooledSlotContext(nil), q.Slots...), done: make(chan struct{}), captureDone: make(chan struct{}), response: response}
	if run.preparations == nil {
		run.preparations = map[string]*pooledPreparation{}
	}
	if run.controlReplies == nil {
		run.controlReplies = map[string]pooledControlReply{}
	}
	run.preparations[q.ReservationID] = p
	run.controlReplies[q.RequestID] = pooledControlReply{digest: digest, preparation: p}
	run.controlMu.Unlock()

	// The first request owns the operation. Disconnecting it aborts preparation;
	// a concurrent retry only waits and cannot cancel or repeat that operation.
	prepareCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	values, err := run.prepareCaptures(p)
	if err == nil {
		err = run.Prepare(prepareCtx, q.ReservationID, values)
	}
	if err != nil {
		// Also covers failures before the supervisor starts. Fencing/expiry may
		// already have retired the ledger; it must never restore a reservation.
		_ = run.ledger.Abort(run.owner.epoch, q.ReservationID)
	}
	p.err, p.response.Ready = err, err == nil
	p.response.NativeData = err == nil && run.supervisor.NativeDataAvailable(run.owner.epoch, q.ReservationID)
	close(p.done)
	go run.reapPreparationCaptures(q.ReservationID, p)
	return run.waitPreparation(ctx, q.ReservationID, p)
}

func (run *PooledRun) waitPreparation(ctx context.Context, reservation string, p *pooledPreparation) (PooledControlResponse, error) {
	select {
	case <-ctx.Done():
		return PooledControlResponse{}, ctx.Err()
	case <-p.done:
	}
	if p.err != nil {
		return p.response, p.err
	}
	hosts, _ := run.ledger.Snapshot()
	count := 0
	for _, host := range hosts {
		if host.ReservationID == reservation {
			if host.State != javarunner.HostReady {
				return p.response, javarunner.ErrHostState
			}
			count += len(host.SlotIDs)
		}
	}
	if count != len(p.slots) {
		return p.response, javarunner.ErrHostState
	}
	return p.response, nil
}

func (run *PooledRun) prepareCaptures(p *pooledPreparation) (map[string]map[string]string, error) {
	// Admit every capture descriptor before creating anything; the preparation's
	// capture reaper always runs afterwards and returns this share exactly once.
	if !run.server.reserveCaptureDescriptors(len(p.slots)) {
		return nil, javarunner.ErrHostCapacity
	}
	if run.server != nil {
		p.descriptorMu.Lock()
		p.server, p.descriptors = run.server, len(p.slots)
		p.descriptorMu.Unlock()
	}
	parent, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(parent, "apexload-pooled-events-")
	if err != nil {
		return nil, err
	}
	p.directory = directory
	p.captures = make(map[string]*pooledSlotCapture, len(p.slots))
	values := make(map[string]map[string]string, len(p.slots))
	group, scenario := run.group, run.execution
	if run.groupLabel != "" {
		group = run.groupLabel
	}
	if run.scenarioLabel != "" {
		scenario = run.scenarioLabel
	}
	for i, slot := range p.slots {
		path := filepath.Join(directory, strconv.Itoa(i)+".jsonl")
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		p.captures[slot.SlotID] = &pooledSlotCapture{file: file, path: path}
		values[slot.SlotID] = map[string]string{
			"APEXLOAD_VUSER_ID":              strconv.FormatInt(slot.VUserID, 10),
			"APEXLOAD_GROUP_NAME":            group,
			"APEXLOAD_SCENARIO_ID":           scenario,
			"APEXLOAD_IGNORE_THINK_TIME":     strconv.FormatBool(slot.IgnoreThinkTime),
			"APEXLOAD_THINK_TIME_LIMIT_MS":   strconv.FormatInt(slot.ThinkTimeLimitMS, 10),
			"APEXLOAD_JAVA_VUSER_EVENT_FILE": path,
		}
	}
	return values, nil
}

// A transport failure or vanished runtime is not permission to remove files
// that a surviving JVM could still write. Unproven cleanup retains the files.
// This watcher starts only after Prepare returns, so late creation cannot race
// file removal. It exits on run shutdown, retaining quarantined captures.
func (run *PooledRun) reapPreparationCaptures(reservation string, p *pooledPreparation) {
	defer close(p.captureDone)
	defer p.closeCaptures()
	if p.directory == "" {
		return
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		hosts, _ := run.ledger.Snapshot()
		found, reaped := false, true
		for _, host := range hosts {
			if host.ReservationID == reservation {
				found = true
				reaped = reaped && host.State == javarunner.HostReaped
			}
		}
		if found && reaped {
			p.closeCaptures()
			_ = os.RemoveAll(p.directory)
			return
		}
		select {
		case <-run.closed:
			// Recheck once after closure so positive cleanup gets reclaimed even
			// when it occurred between the snapshot and receiving closed.
			hosts, _ = run.ledger.Snapshot()
			for _, host := range hosts {
				if host.ReservationID == reservation && host.State != javarunner.HostReaped {
					return
				}
			}
			if found {
				p.closeCaptures()
				_ = os.RemoveAll(p.directory)
			}
			return
		case <-ticker.C:
		}
	}
}

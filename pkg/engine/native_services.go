package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxNativeLeaseHandles = 32

type nativeServices struct {
	vu      *VUser
	allowed map[string]map[string]bool
	mu      sync.Mutex
	active  *nativeInvocation
}
type nativeInvocation struct {
	broker   *nativeServices
	ctx      context.Context
	cancel   context.CancelCauseFunc
	callMu   sync.Mutex
	sequence uint64
	digest   [32]byte
	reply    []byte
	lastErr  *protocol.NativeDataError
}

func nativeError(code string) *protocol.NativeDataError { return &protocol.NativeDataError{Code: code} }

func (vu *VUser) prepareNativeServices() error {
	if vu.nativeServices != nil {
		return nil
	}
	requirements, err := dsl.ScriptNativeServiceRequirements(vu.Script)
	if err != nil {
		return err
	}
	b := &nativeServices{vu: vu, allowed: map[string]map[string]bool{}}
	for _, requirement := range requirements {
		if vu.liveTables == nil {
			return nativeError("binding_unavailable")
		}
		binding, ok := vu.liveTables.bindings[requirement.Alias]
		if !ok {
			return nativeError("binding_unavailable")
		}
		ops := map[string]bool{}
		for _, op := range requirement.Operations {
			if binding.Profile == livetable.ProfileNative && slices.Contains([]string{"rotate", "create_column", "read_column"}, op) || binding.Profile == livetable.ProfileVTS && slices.Contains([]string{"lease", "renew", "ack", "release"}, op) {
				return nativeError("profile_operation_unavailable")
			}
			ops[op] = true
		}
		b.allowed[requirement.Alias] = ops
	}
	if len(requirements) > 0 {
		if setter, ok := vu.Proto.(protocol.RuntimeNativeServicesSetter); ok {
			if err := setter.SetRuntimeNativeServices(b); err != nil {
				return err
			}
		} else if vu.Script != nil && vu.Script.Protocol == "java_vuser" {
			return nativeError("runner_capability_unavailable")
		}
	}
	vu.nativeServices = b
	return nil
}

func (b *nativeServices) BeginInvocation(ctx context.Context, id string) (protocol.NativeInvocation, error) {
	if ctx == nil || id == "" || len(id) > 256 {
		return nil, nativeError("invocation_invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, nativeError("canceled")
	}
	if err := b.vu.liveTableLeaseError(); err != nil {
		return nil, nativeError("continuity_lost")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != nil {
		return nil, nativeError("invocation_busy")
	}
	call, cancel := context.WithCancelCause(ctx)
	i := &nativeInvocation{broker: b, ctx: call, cancel: cancel}
	b.active = i
	go func() {
		var lost <-chan struct{}
		if b.vu.liveTables != nil {
			lost = b.vu.liveTables.lost
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-call.Done():
				return
			case <-lost:
				cancel(nativeError("continuity_lost"))
				return
			case <-ticker.C:
				if b.vu.liveTableLeaseError() != nil {
					cancel(nativeError("continuity_lost"))
					return
				}
			}
		}
	}()
	return i, nil
}
func (i *nativeInvocation) Done() <-chan struct{} { return i.ctx.Done() }
func (i *nativeInvocation) Err() error            { return context.Cause(i.ctx) }
func (i *nativeInvocation) Close() {
	i.cancel(nativeError("invocation_closed"))
	// Wait for the bounded service call to unwind before permitting a new
	// invocation on this lane. A canceled guest can never overlap its successor.
	i.callMu.Lock()
	defer i.callMu.Unlock()
	i.broker.mu.Lock()
	if i.broker.active == i {
		i.broker.active = nil
	}
	i.broker.mu.Unlock()
	i.reply = nil
}

func (i *nativeInvocation) Call(ctx context.Context, sequence uint64, request protocol.NativeDataRequest) (protocol.NativeDataResult, error) {
	var empty protocol.NativeDataResult
	if !i.callMu.TryLock() {
		return empty, nativeError("call_busy")
	}
	defer i.callMu.Unlock()
	if i.ctx.Err() != nil || ctx == nil || ctx.Err() != nil {
		return empty, nativeError("invocation_closed")
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > protocol.NativeDataRequestBytes {
		return empty, nativeError("request_too_large")
	}
	digest := sha256.Sum256(encoded)
	if sequence == i.sequence && sequence > 0 {
		if digest != i.digest {
			i.cancel(nativeError("sequence_conflict"))
			return empty, nativeError("sequence_conflict")
		}
		if i.lastErr != nil {
			copyErr := *i.lastErr
			return empty, &copyErr
		}
		var cached protocol.NativeDataResult
		if json.Unmarshal(i.reply, &cached) != nil {
			i.cancel(nativeError("reply_unavailable"))
			return empty, nativeError("reply_unavailable")
		}
		return cached, nil
	}
	if sequence == 0 || sequence != i.sequence+1 || i.sequence == ^uint64(0) {
		i.cancel(nativeError("sequence_invalid"))
		return empty, nativeError("sequence_invalid")
	}
	i.sequence, i.digest = sequence, digest
	i.reply, i.lastErr = nil, nil
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(i.ctx, cancel)
	defer func() { stop(); cancel() }()
	result, err := i.broker.execute(call, request)
	if err != nil {
		var typed *protocol.NativeDataError
		if !errors.As(err, &typed) {
			typed = nativeError("operation_rejected")
			var live *livetable.Error
			if errors.As(err, &live) {
				typed.Code = string(live.Code)
				typed.OutcomeUnknown = live.OutcomeUnknown
			}
		}
		copyErr := *typed
		i.lastErr = &copyErr
		if typed.OutcomeUnknown || i.broker.vu.liveTableLeaseError() != nil {
			i.cancel(nativeError("continuity_lost"))
		}
		return empty, typed
	}
	i.reply, err = json.Marshal(result)
	if err != nil || len(i.reply) > protocol.NativeDataResultBytes {
		// Never retry a possibly mutating call under a new core identity when its
		// projection cannot be delivered. The run must retain this uncertainty.
		if runtime := i.broker.vu.liveTables; runtime != nil {
			runtime.mu.Lock()
			runtime.failLocked()
			runtime.mu.Unlock()
		}
		i.lastErr = &protocol.NativeDataError{Code: "result_too_large", OutcomeUnknown: true}
		i.reply = nil
		i.cancel(nativeError("continuity_lost"))
		return empty, i.lastErr
	}
	return result, nil
}

func (b *nativeServices) execute(ctx context.Context, request protocol.NativeDataRequest) (protocol.NativeDataResult, error) {
	var empty protocol.NativeDataResult
	if !b.allowed[request.Alias][request.Operation] {
		return empty, nativeError("operation_not_declared")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return empty, nativeError("request_invalid")
	}
	var params map[string]any
	if json.Unmarshal(data, &params) != nil {
		return empty, nativeError("request_invalid")
	}
	if request.Delta != "" {
		delta, err := strconv.ParseInt(request.Delta, 10, 64)
		if err != nil || strconv.FormatInt(delta, 10) != request.Delta {
			return empty, nativeError("delta_invalid")
		}
		params["delta"] = delta
	}
	acquireRef := ""
	if request.Operation == "lease" {
		acquireRef = "native_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		params["result_prefix"] = acquireRef
	}
	if request.LeaseRef != "" && !strings.HasPrefix(request.LeaseRef, "native_") {
		return empty, nativeError("lease_reference_invalid")
	}
	parsed, err := dsl.ParseLiteralLiveTableAction(params)
	if err != nil {
		return empty, nativeError("request_invalid")
	}
	vu := b.vu
	binding, ok := vu.liveTables.bindings[request.Alias]
	if !ok {
		return empty, nativeError("binding_unavailable")
	}
	if request.Operation == "lease" {
		vu.liveTables.mu.Lock()
		held := len(vu.liveTables.leases) + vu.liveTables.nativeLeasePending
		if held >= maxNativeLeaseHandles {
			vu.liveTables.mu.Unlock()
			return empty, nativeError("lease_capacity")
		}
		vu.liveTables.nativeLeasePending++
		vu.liveTables.mu.Unlock()
		defer func() { vu.liveTables.mu.Lock(); vu.liveTables.nativeLeasePending--; vu.liveTables.mu.Unlock() }()
	}
	core := livetable.Request{Operation: livetable.Operation(parsed.Operation), RecordID: parsed.RecordID, Position: parsed.Position, Columns: parsed.Columns, Values: parsed.Values, Expected: parsed.Expected, Delta: int64(parsed.Delta), Wait: time.Duration(parsed.WaitMS) * time.Millisecond, Limit: parsed.Limit, Cursor: parsed.Cursor}
	if parsed.ExpectedVersion != "" {
		version, _ := strconv.ParseUint(parsed.ExpectedVersion, 10, 64)
		core.ExpectedVersion = &version
	}
	if parsed.Operation == "lease" || parsed.Operation == "renew" {
		core.LeaseDuration = binding.LeaseDuration
	}
	if parsed.LeaseMS > 0 {
		core.LeaseDuration = time.Duration(parsed.LeaseMS) * time.Millisecond
	}
	result, err := vu.executeLiveTableLiteral(ctx, binding, core, parsed.LeaseRef, acquireRef)
	if err != nil {
		return empty, err
	}
	out := protocol.NativeDataResult{Status: string(result.Status), Revision: strconv.FormatUint(result.Revision, 10), Value: result.Value, Count: strconv.FormatInt(result.Count, 10), Columns: result.Columns}
	project := func(row livetable.Row) protocol.NativeDataRow {
		return protocol.NativeDataRow{ID: row.ID, Version: strconv.FormatUint(row.Version, 10), Values: row.Values}
	}
	if result.Row != nil {
		row := project(*result.Row)
		out.Row = &row
	}
	for _, row := range result.Rows {
		out.Rows = append(out.Rows, project(row))
	}
	for _, cell := range result.Cells {
		out.Cells = append(out.Cells, protocol.NativeDataCell{Present: cell.Present, Value: cell.Value})
	}
	if result.Lease != nil {
		out.LeaseRef = acquireRef
		if out.LeaseRef == "" {
			out.LeaseRef = parsed.LeaseRef
		}
	}
	return out, nil
}

func nativeServiceUnavailable(reason string) error {
	return fmt.Errorf("native service capability unavailable: %s", reason)
}

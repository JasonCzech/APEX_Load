package protocol

import (
	"context"
	"errors"
	"strings"
)

// Activity leases prove actual process-local work, independently of telemetry.
const FeatureEventHubsProcessorPauseV1 = "eventhubs_processor_pause_v1"

type RuntimePauseActivity string

const (
	RuntimePauseAction     RuntimePauseActivity = "action"
	RuntimePauseReceive    RuntimePauseActivity = "receive"
	RuntimePauseTail       RuntimePauseActivity = "tail"
	RuntimePauseCheckpoint RuntimePauseActivity = "checkpoint"
)

var ErrRuntimePauseAdmissionInvalid = errors.New("invalid runtime pause admission")
var ErrRuntimePauseSuspended = errors.New("runtime activity suspended for pause")

type RuntimePauseLease interface {
	Context() context.Context
	Release()
}

// Action contexts carry the coordinator's private incarnation-bound authority.
// Receive may delegate under a live action; checkpoint must do so. Release is
// idempotent and occurs only after the actual operation has returned.
type RuntimePauseGate interface {
	Admit(context.Context, RuntimePauseActivity) (RuntimePauseLease, error)
}

// Support inspection is local: no credentials, target probes or user code.
type RuntimePauseProvider interface{ RuntimePauseSupported() bool }

func ReadRuntimePauseSupport(p Protocol) (supported bool, err error) {
	provider, ok := p.(RuntimePauseProvider)
	if !ok {
		return false, nil
	}
	defer func() {
		if recover() != nil {
			supported = false
			err = ErrRuntimePauseAdmissionInvalid
		}
	}()
	return provider.RuntimePauseSupported(), nil
}

type runtimePauseContextKey struct{}
type runtimePauseBinding struct {
	protocol string
	gate     RuntimePauseGate
}

func WithRuntimePauseGate(ctx context.Context, name string, gate RuntimePauseGate) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runtimePauseContextKey{}, runtimePauseBinding{strings.ToLower(strings.TrimSpace(name)), gate})
}

// Present nil or mismatched bindings must fail closed, never use an unmanaged
// fallback. Script maps and remote messages cannot mint the private boxed key.
func RuntimePauseGateFromContext(ctx context.Context, name string) (RuntimePauseGate, bool) {
	if ctx == nil {
		return nil, false
	}
	binding, present := ctx.Value(runtimePauseContextKey{}).(runtimePauseBinding)
	if !present {
		return nil, false
	}
	if binding.protocol != strings.ToLower(strings.TrimSpace(name)) {
		return nil, true
	}
	return binding.gate, true
}

// Reject faulty admissions before any target operation. A rejected returned
// lease is released; sanitised errors do not disclose provider panic values.
func AdmitRuntimePause(ctx context.Context, gate RuntimePauseGate, activity RuntimePauseActivity) (lease RuntimePauseLease, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch activity {
	case RuntimePauseAction, RuntimePauseReceive, RuntimePauseTail, RuntimePauseCheckpoint:
	default:
		return nil, ErrRuntimePauseAdmissionInvalid
	}
	if gate == nil {
		return nil, ErrRuntimePauseAdmissionInvalid
	}
	defer func() {
		if recover() != nil {
			err = ErrRuntimePauseAdmissionInvalid
		}
		if err != nil && lease != nil {
			releaseRuntimePauseInvalid(lease)
			lease = nil
		}
	}()
	lease, err = gate.Admit(ctx, activity)
	if err != nil {
		return lease, err
	}
	if lease == nil || lease.Context() == nil {
		return lease, ErrRuntimePauseAdmissionInvalid
	}
	return lease, nil
}
func releaseRuntimePauseInvalid(lease RuntimePauseLease) {
	defer func() { _ = recover() }()
	lease.Release()
}

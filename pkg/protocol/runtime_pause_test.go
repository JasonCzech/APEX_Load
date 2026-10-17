package protocol

import (
	"context"
	"errors"
	"testing"
)

type pauseTestGate struct {
	lease      RuntimePauseLease
	err        error
	panicAdmit bool
}

func (g *pauseTestGate) Admit(context.Context, RuntimePauseActivity) (RuntimePauseLease, error) {
	if g.panicAdmit {
		panic("secret")
	}
	return g.lease, g.err
}

type pauseTestLease struct {
	ctx          context.Context
	released     int
	panicContext bool
}

func (l *pauseTestLease) Context() context.Context {
	if l.panicContext {
		panic("secret")
	}
	return l.ctx
}
func (l *pauseTestLease) Release() { l.released++ }
func TestRuntimePauseBindingFailsClosed(t *testing.T) {
	gate := &pauseTestGate{}
	if g, present := RuntimePauseGateFromContext(nil, "eventhubs"); present || g != nil { //nolint:staticcheck // the nil context is the input under test
		t.Fatal("invented binding")
	}
	ctx := WithRuntimePauseGate(nil, " EventHubs ", gate) //nolint:staticcheck // the nil context is the input under test
	if g, present := RuntimePauseGateFromContext(ctx, "eventhubs"); !present || g != gate {
		t.Fatal("missing binding")
	}
	if g, present := RuntimePauseGateFromContext(ctx, "other"); !present || g != nil {
		t.Fatal("mismatch fell back")
	}
	ctx = WithRuntimePauseGate(ctx, "eventhubs", nil)
	if g, present := RuntimePauseGateFromContext(ctx, "eventhubs"); !present || g != nil {
		t.Fatal("nil fell back")
	}
}
func TestRuntimePauseAdmissionRejectsFaultyLeases(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate RuntimePauseGate
		kind RuntimePauseActivity
	}{
		{"nil", nil, RuntimePauseReceive}, {"kind", &pauseTestGate{}, "other"},
		{"panic", &pauseTestGate{panicAdmit: true}, RuntimePauseTail},
		{"missing", &pauseTestGate{}, RuntimePauseReceive},
		{"context", &pauseTestGate{lease: &pauseTestLease{}}, RuntimePauseAction},
		{"context_panic", &pauseTestGate{lease: &pauseTestLease{panicContext: true}}, RuntimePauseCheckpoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := AdmitRuntimePause(t.Context(), tc.gate, tc.kind)
			if l != nil || !errors.Is(err, ErrRuntimePauseAdmissionInvalid) {
				t.Fatal(l, err)
			}
		})
	}
	lease := &pauseTestLease{ctx: t.Context()}
	gate := &pauseTestGate{lease: lease}
	l, err := AdmitRuntimePause(t.Context(), gate, RuntimePauseAction)
	if err != nil || l != lease || l.Context() != lease.ctx || lease.released != 0 {
		t.Fatal("changed admitted authority", l, err)
	}
	l.Release()
	if lease.released != 1 {
		t.Fatal("lease not released")
	}
}

type pauseSupportTestProtocol struct {
	Protocol
	supported, panics bool
}

func (p *pauseSupportTestProtocol) RuntimePauseSupported() bool {
	if p.panics {
		panic("credential")
	}
	return p.supported
}
func TestRuntimePauseSupportInspectionIsPureAndPanicSafe(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Protocol
		want bool
		err  bool
	}{{"absent", nil, false, false}, {"unsupported", &pauseSupportTestProtocol{}, false, false}, {"supported", &pauseSupportTestProtocol{supported: true}, true, false}, {"panic", &pauseSupportTestProtocol{panics: true}, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			supported, err := ReadRuntimePauseSupport(tc.p)
			if supported != tc.want || (err != nil) != tc.err {
				t.Fatal(supported, err)
			}
			if tc.err && !errors.Is(err, ErrRuntimePauseAdmissionInvalid) {
				t.Fatal("unsanitized inspection error", err)
			}
		})
	}
}
func TestRuntimePauseRejectedAdmissionReleasesReturnedLease(t *testing.T) {
	lease := &pauseTestLease{ctx: t.Context()}
	_, err := AdmitRuntimePause(t.Context(), &pauseTestGate{lease: lease, err: context.Canceled}, RuntimePauseReceive)
	if !errors.Is(err, context.Canceled) || lease.released != 1 {
		t.Fatal(err, lease.released)
	}
}

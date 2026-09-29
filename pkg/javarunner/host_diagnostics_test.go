package javarunner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDiagnosticSnapshotsBoundedAndIndependent(t *testing.T) {
	c := &hostCapture{}
	_, _ = c.Write(make([]byte, HostDiagnosticCaptureBytes+1))
	d := c.snapshot()
	if len(d.Bytes) != HostDiagnosticCaptureBytes || d.Dropped != 1 || !d.Truncated {
		t.Fatal("capture not bounded")
	}
	d.Bytes[0] = 1
	if c.snapshot().Bytes[0] != 0 {
		t.Fatal("snapshot aliases writer")
	}
	for _, d := range []HostDiagnostics{
		{Stdout: DiagnosticCapture{Bytes: make([]byte, HostDiagnosticCaptureBytes+1)}},
		{Stderr: DiagnosticCapture{Dropped: 1}},
		{Java: DiagnosticCapture{Bytes: make([]byte, 32769)}},
	} {
		if ValidHostDiagnostics(d) {
			t.Fatal("invalid diagnostics accepted")
		}
	}
	if !ValidHostDiagnostics(HostDiagnostics{Stdout: c.snapshot()}) {
		t.Fatal("valid capture rejected")
	}
}

type diagnosticTestHost struct {
	*testManagedHost
	diagnostics func(context.Context) (HostDiagnostics, error)
}

func (h *diagnosticTestHost) Diagnostics(ctx context.Context) (HostDiagnostics, error) {
	return h.diagnostics(ctx)
}

func TestDiagnosticReadIndependentOfCallerCancellation(t *testing.T) {
	l, allocations, contexts := supervisorLedger(t, 1, time.Minute)
	started, release := make(chan struct{}), make(chan struct{})
	h := &diagnosticTestHost{testManagedHost: newTestManagedHost("boundary")}
	h.diagnostics = func(ctx context.Context) (HostDiagnostics, error) {
		close(started)
		select {
		case <-release:
			return HostDiagnostics{}, nil
		case <-ctx.Done():
			return HostDiagnostics{}, ctx.Err()
		}
	}
	s, err := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSupervisor(t, s)
	if err := s.Prepare(t.Context(), "epoch", allocations[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Diagnostics(ctx, "epoch", allocations[0].ID); done <- err }()
	<-started
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal("caller cancellation reached host control", err)
	}
	if _, err := s.Diagnostics(t.Context(), "stale", allocations[0].ID); !errors.Is(err, ErrHostFence) {
		t.Fatal(err)
	}
	if _, err := s.Diagnostics(t.Context(), "epoch", "foreign"); !errors.Is(err, ErrHostState) {
		t.Fatal(err)
	}
	if _, err := s.Diagnostics(ctx, "epoch", allocations[0].ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

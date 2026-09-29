package javarunner

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"
)

const HostDiagnosticCaptureBytes = 256 << 10

// DiagnosticCapture is unattributed host output, never a slot event or a
// transaction. Bytes use JSON base64 so arbitrary native output stays bounded
// without lossy UTF-8 conversion. Treat contents as untrusted script output.
type DiagnosticCapture struct {
	Bytes     []byte `json:"bytes"`
	Dropped   uint64 `json:"dropped"`
	Truncated bool   `json:"truncated"`
}

type HostDiagnostics struct {
	GCPauses *GCPauseSnapshot  `json:"gc_pauses,omitempty"`
	Stdout   DiagnosticCapture `json:"stdout"`
	Stderr   DiagnosticCapture `json:"stderr"`
	Java     DiagnosticCapture `json:"java"`
	Sealed   bool              `json:"sealed"`
}

func ValidHostDiagnostics(d HostDiagnostics) bool {
	if d.GCPauses != nil && !validGCPauses(*d.GCPauses) {
		return false
	}
	for i, capture := range []DiagnosticCapture{d.Stdout, d.Stderr, d.Java} {
		limit := HostDiagnosticCaptureBytes
		if i == 2 {
			limit = 32768
		}
		if len(capture.Bytes) > limit || capture.Dropped > 0 && !capture.Truncated {
			return false
		}
	}
	return true
}

func (c *hostCapture) snapshot() DiagnosticCapture {
	c.mu.Lock()
	defer c.mu.Unlock()
	return DiagnosticCapture{Bytes: append([]byte(nil), c.bytes...), Dropped: c.dropped, Truncated: c.truncated}
}

func (h *localManagedHost) Diagnostics(ctx context.Context) (HostDiagnostics, error) {
	if !h.diagnosticsMu.TryLock() {
		return HostDiagnostics{}, ErrHostState
	}
	defer h.diagnosticsMu.Unlock()
	t, err := h.client()
	if err != nil {
		return HostDiagnostics{}, err
	}
	f, err := t.Call(ctx, "", "health", nil)
	if err != nil {
		return HostDiagnostics{}, err
	}
	java, err := base64.StdEncoding.Strict().DecodeString(f.Fields["diagnostics_base64"])
	if err != nil {
		return HostDiagnostics{}, ErrPooledFrame
	}
	dropped, err := strconv.ParseUint(f.Fields["diagnostics_dropped"], 10, 63)
	if err != nil {
		return HostDiagnostics{}, ErrPooledFrame
	}
	d := HostDiagnostics{Stdout: h.captureOut.snapshot(), Stderr: h.captureErr.snapshot(), Java: DiagnosticCapture{Bytes: java, Dropped: dropped, Truncated: dropped > 0}, Sealed: f.Fields["sealed"] == "true"}
	d.GCPauses, err = parseGCPauses(f.Fields)
	if err != nil {
		return HostDiagnostics{}, err
	}
	if !ValidHostDiagnostics(d) {
		return HostDiagnostics{}, ErrPooledFrame
	}
	return d, nil
}

// Diagnostics reads one currently owned host. A caller abandoning an HTTP read
// must not cancel a Java control exchange and reap active peers. Once started,
// the health exchange has its own two-second deadline and remains linked to
// supervisor retirement. Transport/health uncertainty still reaps the host.
// Completed host buffers are not retained after positive containment cleanup.
func (s *HostSupervisor) Diagnostics(ctx context.Context, epoch, id string) (HostDiagnostics, error) {
	if err := ctx.Err(); err != nil {
		return HostDiagnostics{}, err
	}
	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	queryCtx, finish, err := s.operation(queryCtx)
	if err != nil {
		return HostDiagnostics{}, err
	}
	defer finish()
	s.ledger.mu.Lock()
	err = s.ledger.fence(epoch)
	s.ledger.mu.Unlock()
	if err != nil {
		return HostDiagnostics{}, err
	}
	h := s.runtime(id)
	reporter, ok := h.(interface {
		Diagnostics(context.Context) (HostDiagnostics, error)
	})
	if !ok {
		return HostDiagnostics{}, ErrHostState
	}
	d, err := reporter.Diagnostics(queryCtx)
	if err != nil {
		return HostDiagnostics{}, err
	}
	if !ValidHostDiagnostics(d) {
		return HostDiagnostics{}, ErrPooledFrame
	}
	return d, nil
}

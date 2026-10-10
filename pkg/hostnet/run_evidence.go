package hostnet

import "fmt"

// RunEvidence is one process's network limits for a run and its
// namespace's UDP drop counters when execution started and ended.
type RunEvidence struct {
	// Source names the process: a generator target, or "controller" for a
	// local run. The collecting archive sets it.
	Source       string           `json:"source,omitempty"`
	OpenFiles    OpenFileLimit    `json:"open_files"`
	UDPBuffers   UDPBufferLimits  `json:"udp_buffers"`
	DropsAtStart *UDPDropCounters `json:"udp_drops_at_start,omitempty"`
	DropsAtEnd   *UDPDropCounters `json:"udp_drops_at_end,omitempty"`
}

// StartRunEvidence samples the limits and the starting counters.
func StartRunEvidence(root string, getenv func(string) string) RunEvidence {
	facts := Collect(root, getenv)
	return RunEvidence{OpenFiles: facts.OpenFiles, UDPBuffers: facts.UDPBuffers, DropsAtStart: facts.UDPDrops}
}

// Finish samples the ending counters.
func (e *RunEvidence) Finish(root string, getenv func(string) string) {
	if e == nil {
		return
	}
	e.DropsAtEnd = SampleUDPDrops(root, getenv)
}

// UDPDropDelta is how much each counter grew between the two samples.
type UDPDropDelta struct {
	ReceiveBufferErrors uint64 `json:"receive_buffer_errors"`
	SendBufferErrors    uint64 `json:"send_buffer_errors"`
	InErrors            uint64 `json:"in_errors"` // includes ReceiveBufferErrors
}

// InputErrors is the input error growth. InErrors already counts each
// receive-buffer drop; the larger value guards a kernel that does not.
func (d UDPDropDelta) InputErrors() uint64 { return max(d.InErrors, d.ReceiveBufferErrors) }

// Total is the packets lost in either direction: input errors, receive
// buffer drops included, plus send buffer drops.
func (d UDPDropDelta) Total() uint64 { return d.InputErrors() + d.SendBufferErrors }

// DropDelta compares the two samples. It is unavailable when either is
// missing, they describe different namespaces or scopes, or a counter went
// backwards (the namespace was recreated).
func (e RunEvidence) DropDelta() (UDPDropDelta, bool) {
	start, end := e.DropsAtStart, e.DropsAtEnd
	if start == nil || end == nil || start.Scope != end.Scope || start.ScopeID != end.ScopeID {
		return UDPDropDelta{}, false
	}
	if end.ReceiveBufferErrors < start.ReceiveBufferErrors || end.SendBufferErrors < start.SendBufferErrors || end.InErrors < start.InErrors {
		return UDPDropDelta{}, false
	}
	return UDPDropDelta{
		ReceiveBufferErrors: end.ReceiveBufferErrors - start.ReceiveBufferErrors,
		SendBufferErrors:    end.SendBufferErrors - start.SendBufferErrors,
		InErrors:            end.InErrors - start.InErrors,
	}, true
}

// AttributableDropGrowth reports drops that grew in a scope the deployment
// declared as the process's own. A shared namespace's growth is reported,
// never attributed to the run.
func (e RunEvidence) AttributableDropGrowth() (UDPDropDelta, bool) {
	delta, ok := e.DropDelta()
	if !ok || e.DropsAtEnd.Scope != ScopePod || delta.Total() == 0 {
		return UDPDropDelta{}, false
	}
	return delta, true
}

// DropWarning is the run report's warning for attributable growth, or "".
func (e RunEvidence) DropWarning() string {
	delta, ok := e.AttributableDropGrowth()
	if !ok {
		return ""
	}
	source := e.Source
	if source == "" {
		source = "the load generator"
	}
	return fmt.Sprintf("UDP packets were dropped on %s during the run (input errors %d, receive buffer %d of them; send buffer %d; in its pod's network namespace). HTTP/3 throughput and latency may reflect the generator rather than the target; raise the node's UDP buffer limits (net.core.rmem_max, wmem_max) or lower the VUsers per generator.", source, delta.InputErrors(), delta.ReceiveBufferErrors, delta.SendBufferErrors)
}

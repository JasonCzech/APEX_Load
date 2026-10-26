// Package procidentity names one process exactly and decides, from the host
// it runs on, whether a recorded process provably no longer exists.
//
// A process is recorded with the host's persistent identity (when it has
// one), the kernel boot, the PID namespace, its PID and the start time the
// kernel reports. A recorded process is gone only when the same machine has
// booted again, or, within this boot and PID namespace, no process has that
// PID with that start time. Age, a free lock, a lease or a missing PID alone
// prove nothing. A process recorded on another machine, boot or PID
// namespace is never judged, and anything this host cannot decide is not
// gone.
//
// Hosts that share durable state judged this way must not share both a
// machine ID and a host name (a VM cloned without regenerating its machine
// ID or renaming it would). A process restored by checkpoint/restore (CRIU)
// keeps neither its PID nor its start time and is unsupported.
package procidentity

import (
	"errors"
	"os"
	"sync"
)

// Identity names one process exactly.
type Identity struct {
	// Machine is the host's persistent identity, when it has one; Boot the
	// kernel boot; Namespace the PID namespace the PID belongs to.
	Machine   string `json:"machine,omitempty"`
	Boot      string `json:"boot"`
	Namespace string `json:"namespace"`
	PID       int    `json:"pid"`
	// Start is the process start time as the kernel reports it.
	Start string `json:"start"`
}

// Host is the machine, boot and PID namespace a process runs in.
type Host struct{ Machine, Boot, Namespace string }

// ErrUnavailable reports a platform or environment without exact process
// identity. Nothing recorded there can later be proved gone.
var ErrUnavailable = errors.New("process identity is unavailable on this platform")

var (
	hostOnce sync.Once
	hostSelf Host
	hostErr  error
)

// CurrentHost is the machine, boot and PID namespace this process runs in.
func CurrentHost() (Host, error) {
	hostOnce.Do(func() { hostSelf, hostErr = readHost() })
	return hostSelf, hostErr
}

// ProcessStart reads a process's start time on this host; found is false
// when no such process exists.
func ProcessStart(pid int) (string, bool, error) { return processStart(pid) }

// Of identifies a process of this host by PID.
func Of(pid int) (Identity, error) {
	host, err := CurrentHost()
	if err != nil || pid <= 0 {
		return Identity{}, errors.Join(err, ErrUnavailable)
	}
	start, found, err := processStart(pid)
	if err != nil || !found {
		return Identity{}, errors.Join(err, ErrUnavailable)
	}
	return Identity{Machine: host.Machine, Boot: host.Boot, Namespace: host.Namespace, PID: pid, Start: start}, nil
}

// Self identifies this process.
func Self() (Identity, error) { return Of(os.Getpid()) }

// Gone reports whether a recorded process provably no longer exists, judged
// from host with start reading a PID's start time there.
func Gone(host Host, id Identity, start func(int) (string, bool, error)) bool {
	if id.Boot == "" || id.Namespace == "" || id.PID <= 0 || id.Start == "" || start == nil {
		return false
	}
	if id.Boot != host.Boot {
		// Every process of an earlier boot of this machine has ended; a
		// boot of another or an unidentified machine proves nothing.
		return id.Machine != "" && id.Machine == host.Machine
	}
	// Within one boot value only the same identified machine, in the same
	// PID namespace, can look the PID up. A boot value shared by two hosts
	// (a Windows boot counter, a VM cloned from a memory snapshot) must not
	// let one host's process table answer for another's.
	if id.Namespace != host.Namespace || id.Machine != host.Machine {
		return false
	}
	current, found, err := start(id.PID)
	if err != nil {
		return false
	}
	return !found || current != id.Start
}

// Judgeable reports whether host can ever decide a recorded process, with
// Gone's own scoping: a process of an earlier boot of the same identified
// machine (which Gone proves exited), or one in host's boot, machine
// identity and PID namespace. Any other process is never judged from host.
func Judgeable(host Host, id Identity) bool {
	if id.Boot == "" || id.Namespace == "" || id.PID <= 0 || id.Start == "" {
		return false
	}
	if id.Boot != host.Boot {
		return id.Machine != "" && id.Machine == host.Machine
	}
	return id.Namespace == host.Namespace && id.Machine == host.Machine
}

// Exited reports whether a recorded process provably no longer exists,
// judged from this process's host.
func Exited(id Identity) bool {
	host, err := CurrentHost()
	if err != nil {
		return false
	}
	return Gone(host, id, processStart)
}

package browser

import (
	"errors"
	"os"
	"sync"
)

// Crash reclamation (D4). A generation whose generator was killed never
// writes its writer-joined proof. Its sealed manifest records the generator
// holding the lease; writer-entered.json, written durably before the native
// HAR writer (the Playwright driver) is given the working path, records that
// writer. The generation is reclaimable only when its lease is free and
// every recorded process provably no longer exists: the same machine booted
// again, or, within this boot and PID namespace, no process has that PID
// with that start time. Without writer-entered.json no native writer ever
// had the path. Age, a free lock or a missing PID alone prove nothing, a
// process recorded on another machine, boot or PID namespace is never
// judged, and a writer entered with no recorded identity keeps the
// generation until it is writer-joined.

const (
	harProcessGenerator = "generator"
	harProcessWriter    = "writer"
)

// harProcessIdentity names one process exactly.
type harProcessIdentity struct {
	Role string `json:"role"`
	// Machine is the host's persistent identity, when it has one; Boot the
	// kernel boot; Namespace the PID namespace the PID belongs to.
	Machine   string `json:"machine,omitempty"`
	Boot      string `json:"boot"`
	Namespace string `json:"namespace"`
	PID       int    `json:"pid"`
	// Start is the process start time as the kernel reports it.
	Start string `json:"start"`
}

// harHost is the machine, boot and PID namespace this process runs in.
type harHost struct{ machine, boot, namespace string }

var (
	harHostOnce sync.Once
	harHostSelf harHost
	harHostErr  error
)

func currentHARHost() (harHost, error) {
	harHostOnce.Do(func() { harHostSelf, harHostErr = readHARHost() })
	return harHostSelf, harHostErr
}

var errHARProcessUnavailable = errors.New("process identity is unavailable on this platform")

// harProcessIdentityOf identifies a process of this host by PID.
func harProcessIdentityOf(role string, pid int) (harProcessIdentity, error) {
	host, err := currentHARHost()
	if err != nil || pid <= 0 {
		return harProcessIdentity{}, errors.Join(err, errHARProcessUnavailable)
	}
	start, found, err := harProcessStart(pid)
	if err != nil || !found {
		return harProcessIdentity{}, errors.Join(err, errHARProcessUnavailable)
	}
	return harProcessIdentity{Role: role, Machine: host.machine, Boot: host.boot, Namespace: host.namespace, PID: pid, Start: start}, nil
}

// harNativeWriterPID is the PID of the process that writes native HARs.
var harNativeWriterPID = playwrightDriverPID

// harGeneratorProcesses is the identity a new generation's sealed manifest
// records: this generator, when its identity is available.
func harGeneratorProcesses() []harProcessIdentity {
	if self, err := harProcessIdentityOf(harProcessGenerator, os.Getpid()); err == nil {
		return []harProcessIdentity{self}
	}
	return nil
}

// harWriterProcesses is the identity writer-entered.json records: the
// native writer about to be given the working path, when known.
func harWriterProcesses() []harProcessIdentity {
	if writer, err := harProcessIdentityOf(harProcessWriter, harNativeWriterPID()); err == nil {
		return []harProcessIdentity{writer}
	}
	return nil
}

// harProcessGone reports whether a recorded process provably no longer
// exists. Anything this host cannot decide is not gone.
func harProcessGone(host harHost, id harProcessIdentity, start func(int) (string, bool, error)) bool {
	if id.Boot == "" || id.Namespace == "" || id.PID <= 0 || id.Start == "" {
		return false
	}
	if id.Boot != host.boot {
		// Every process of an earlier boot of this machine has ended; a
		// boot of another or an unidentified machine proves nothing.
		return id.Machine != "" && id.Machine == host.machine
	}
	if id.Namespace != host.namespace {
		return false
	}
	current, found, err := start(id.PID)
	if err != nil {
		return false
	}
	return !found || current != id.Start
}

// harProcessesGone reports whether every recorded process, a generator and
// at most one native writer, provably no longer exists.
func harProcessesGone(processes []harProcessIdentity) bool {
	host, err := currentHARHost()
	if err != nil {
		return false
	}
	roles := map[string]bool{}
	for _, p := range processes {
		if p.Role != harProcessGenerator && p.Role != harProcessWriter || roles[p.Role] || !harProcessGone(host, p, harProcessStart) {
			return false
		}
		roles[p.Role] = true
	}
	return roles[harProcessGenerator]
}

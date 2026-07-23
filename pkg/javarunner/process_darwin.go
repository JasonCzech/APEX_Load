//go:build darwin

package javarunner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const darwinDescendantPollInterval = 25 * time.Millisecond

var (
	managedDarwinProcessTrees sync.Map // map[*exec.Cmd]*managedDarwinProcessTree
	darwinTrackerState        = struct {
		mu      sync.Mutex
		running bool
		active  int
		starts  uint64
		kqueue  int
	}{kqueue: -1}
)

func supportsStrongProcessContainment() bool {
	// Darwin offers no public Job Object/cgroup equivalent. Kqueue and
	// start-time ancestry tracking make legacy cleanup safer, but NOTE_FORK
	// does not expose the child PID and therefore cannot contain an immediate
	// fork+setsid+parent-exit sequence without a polling race.
	return false
}

type darwinProcessIdentity struct {
	pid       int
	parent    int
	group     int
	startSec  int64
	startUsec int32
}

// Darwin has no cgroup or Job Object equivalent. Bind every observed
// descendant to its kernel-reported start time and retain that identity after
// setsid/reparenting. This avoids the unsafe post-Wait "kill(-oldPID)" pattern:
// a reused PID is never signaled unless its start time still matches.
type managedDarwinProcessTree struct {
	mu sync.Mutex

	root       darwinProcessIdentity
	known      map[int]darwinProcessIdentity
	owned      bool
	killed     bool
	trackerErr error

	killOnce sync.Once
	killErr  error
}

func configureProcessTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func startProcessTree(cmd *exec.Cmd) error {
	configureProcessTree(cmd)
	tree := &managedDarwinProcessTree{known: make(map[int]darwinProcessIdentity)}
	if err := registerDarwinProcessTree(cmd, tree); err != nil {
		return fmt.Errorf("initialize Java runner process ownership: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_, _ = takeDarwinProcessTree(cmd)
		return err
	}
	if err := tree.bindRoot(cmd.Process.Pid); err != nil {
		ownedTree, _ := takeDarwinProcessTree(cmd)
		var cleanupErr error
		if ownedTree != nil {
			_, cleanupErr = ownedTree.kill()
		}
		directKillErr := cmd.Process.Kill()
		if directKillErr != nil && errors.Is(directKillErr, os.ErrProcessDone) {
			directKillErr = nil
		}
		// Darwin's legacy process-group path is deliberately best effort. Once
		// root identity/tracker binding fails after Start, neither tree cleanup
		// nor a direct-root kill can prove that a child did not detach first.
		// Transfer Wait to the caller instead of risking an unbounded sync Wait.
		return errors.Join(
			ErrProcessContainmentFailed,
			fmt.Errorf("establish Java runner process ownership: %w", err),
			cleanupErr,
			directKillErr,
		)
	}
	return nil
}

func startStrongProcessTree(_ *exec.Cmd) error {
	return ErrStrongProcessContainmentUnavailable
}

func (t *managedDarwinProcessTree) bindRoot(pid int) error {
	identity, err := darwinProcessIdentityForPID(pid)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.killed {
		t.mu.Unlock()
		return fmt.Errorf("root process was canceled during startup")
	}
	t.root = identity
	t.known[pid] = identity
	t.owned = true
	t.mu.Unlock()
	// Register NOTE_FORK before the more expensive full process snapshot so a
	// short-lived launcher cannot fork and exit between Start and ownership.
	if err := registerDarwinTrackedPID(pid); err != nil {
		return err
	}
	processes, err := darwinProcessSnapshot()
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.discoverFromSnapshotLocked(processes)
	t.mu.Unlock()
	return nil
}

func registerDarwinProcessTree(cmd *exec.Cmd, tree *managedDarwinProcessTree) error {
	darwinTrackerState.mu.Lock()
	defer darwinTrackerState.mu.Unlock()
	if !darwinTrackerState.running {
		kqueue, err := unix.Kqueue()
		if err != nil {
			return err
		}
		darwinTrackerState.running = true
		darwinTrackerState.kqueue = kqueue
		darwinTrackerState.starts++
		go runDarwinProcessTracker(kqueue)
	}
	managedDarwinProcessTrees.Store(cmd, tree)
	darwinTrackerState.active++
	return nil
}

func takeDarwinProcessTree(cmd *exec.Cmd) (*managedDarwinProcessTree, bool) {
	darwinTrackerState.mu.Lock()
	defer darwinTrackerState.mu.Unlock()
	rawTree, ok := managedDarwinProcessTrees.LoadAndDelete(cmd)
	if !ok {
		return nil, false
	}
	if darwinTrackerState.active > 0 {
		darwinTrackerState.active--
	}
	return rawTree.(*managedDarwinProcessTree), true
}

func registerDarwinTrackedPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	darwinTrackerState.mu.Lock()
	defer darwinTrackerState.mu.Unlock()
	if !darwinTrackerState.running || darwinTrackerState.kqueue < 0 {
		return fmt.Errorf("Darwin process tracker is unavailable")
	}
	change := unix.Kevent_t{
		Ident: uint64(pid), Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ENABLE | unix.EV_CLEAR,
		Fflags: unix.NOTE_FORK | unix.NOTE_EXIT,
	}
	_, err := unix.Kevent(darwinTrackerState.kqueue, []unix.Kevent_t{change}, nil, nil)
	return err
}

func runDarwinProcessTracker(kqueue int) {
	events := make([]unix.Kevent_t, 16)
	for {
		timeout := unix.NsecToTimespec(darwinDescendantPollInterval.Nanoseconds())
		_, waitErr := unix.Kevent(kqueue, nil, events, &timeout)

		darwinTrackerState.mu.Lock()
		if darwinTrackerState.kqueue != kqueue {
			darwinTrackerState.mu.Unlock()
			return
		}
		if darwinTrackerState.active == 0 {
			darwinTrackerState.running = false
			darwinTrackerState.kqueue = -1
			darwinTrackerState.mu.Unlock()
			_ = unix.Close(kqueue)
			return
		}
		darwinTrackerState.mu.Unlock()

		processes, snapshotErr := darwinProcessSnapshot()
		managedDarwinProcessTrees.Range(func(_, rawTree any) bool {
			tree := rawTree.(*managedDarwinProcessTree)
			if waitErr != nil && !errors.Is(waitErr, syscall.EINTR) {
				tree.recordTrackerError(waitErr)
			}
			if snapshotErr != nil {
				tree.recordTrackerError(snapshotErr)
			} else {
				tree.discoverFromSnapshot(processes)
				tree.registerKnownPIDs()
			}
			return true
		})
	}
}

func (t *managedDarwinProcessTree) recordTrackerError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.killed && t.trackerErr == nil {
		t.trackerErr = err
	}
}

func (t *managedDarwinProcessTree) discoverFromSnapshot(processes map[int]darwinProcessIdentity) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.killed {
		return
	}
	t.discoverFromSnapshotLocked(processes)
}

func (t *managedDarwinProcessTree) discoverFromSnapshotLocked(processes map[int]darwinProcessIdentity) {
	parents := make(map[int]struct{}, len(t.known))
	ownedGroupLive := false
	for pid, identity := range t.known {
		if current, ok := processes[pid]; ok && current.same(identity) {
			parents[pid] = struct{}{}
			if t.root.group > 0 && current.group == t.root.group {
				ownedGroupLive = true
			}
		}
	}
	// A short-lived launcher can fork and exit between process-table polls.
	// Setpgid gives its ordinary children a unique group, so adopt each current
	// member by start-time identity while continuity is proven by one already
	// owned live member. Once the group is empty, its numeric ID may be reused
	// and can no longer establish ownership.
	if ownedGroupLive {
		for pid, identity := range processes {
			if identity.group == t.root.group {
				if existing, exists := t.known[pid]; !exists || !identity.same(existing) {
					t.known[pid] = identity
				}
				parents[pid] = struct{}{}
			}
		}
	}
	for {
		added := false
		for pid, identity := range processes {
			if existing, exists := t.known[pid]; exists {
				if identity.same(existing) {
					continue
				}
				// A reused PID is a new identity. Adopt it only when the new
				// process is itself descended from a still-owned process.
				if _, parentKnown := parents[identity.parent]; !parentKnown {
					continue
				}
				t.known[pid] = identity
				parents[pid] = struct{}{}
				added = true
				continue
			}
			if _, parentKnown := parents[identity.parent]; !parentKnown {
				continue
			}
			t.known[pid] = identity
			parents[pid] = struct{}{}
			added = true
		}
		if !added {
			return
		}
	}
}

func (t *managedDarwinProcessTree) registerKnownPIDs() {
	for _, identity := range t.knownSnapshot() {
		if err := registerDarwinTrackedPID(identity.pid); err != nil &&
			!errors.Is(err, syscall.ESRCH) {
			t.recordTrackerError(err)
		}
	}
}

func (t *managedDarwinProcessTree) kill() (bool, error) {
	t.killOnce.Do(func() {
		processes, snapshotErr := darwinProcessSnapshot()
		t.mu.Lock()
		t.killed = true
		if snapshotErr == nil {
			t.discoverFromSnapshotLocked(processes)
		}
		trackerErr := t.trackerErr
		t.mu.Unlock()

		var firstErr error
		// Repeated stop/discovery closes the ordinary fork race: after all
		// known ancestors are stopped they cannot create an unobserved child.
		for round := 0; round < 3; round++ {
			if current, err := darwinProcessSnapshot(); err == nil {
				t.mu.Lock()
				// kill() owns the tree now, so discovery is safe despite killed.
				t.discoverFromSnapshotLocked(current)
				t.mu.Unlock()
			} else if snapshotErr == nil {
				snapshotErr = err
			}
			for _, identity := range t.knownSnapshot() {
				if err := signalDarwinProcess(identity, syscall.SIGSTOP); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			time.Sleep(time.Millisecond)
		}
		for _, identity := range t.knownSnapshot() {
			if err := signalDarwinProcess(identity, syscall.SIGKILL); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		// A failed process-table query means ancestry containment could not be
		// proven. Surface that as cleanup failure rather than claiming success.
		t.killErr = errors.Join(firstErr, snapshotErr, trackerErr)
	})
	t.mu.Lock()
	owned := t.owned
	t.mu.Unlock()
	return owned, t.killErr
}

func (t *managedDarwinProcessTree) knownSnapshot() []darwinProcessIdentity {
	t.mu.Lock()
	defer t.mu.Unlock()
	identities := make([]darwinProcessIdentity, 0, len(t.known))
	for _, identity := range t.known {
		identities = append(identities, identity)
	}
	return identities
}

func killProcessTree(cmd *exec.Cmd) error {
	managed := false
	owned := false
	var treeErr error
	if tree, ok := takeDarwinProcessTree(cmd); ok {
		managed = true
		owned, treeErr = tree.kill()
	}
	if treeErr != nil {
		return treeErr
	}
	if managed {
		if !owned && (cmd.ProcessState == nil || !cmd.ProcessState.Exited()) {
			err := cmd.Process.Kill()
			if err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
		}
		return nil
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return nil
	}
	// Compatibility fallback for callers that bypassed StartProcessTree. Never
	// use a numeric PGID after Wait, where it may refer to an unrelated process.
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if groupErr == nil || errors.Is(groupErr, syscall.ESRCH) {
		return nil
	}
	directErr := cmd.Process.Kill()
	if directErr == nil || errors.Is(directErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(groupErr, directErr)
}

// darwinProcessSnapshot copies the required identity and ancestry fields so no
// kernel pointers from KinfoProc escape this function.
func darwinProcessSnapshot() (map[int]darwinProcessIdentity, error) {
	records, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	processes := make(map[int]darwinProcessIdentity, len(records))
	for _, record := range records {
		pid := int(record.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		processes[pid] = darwinProcessIdentity{
			pid:       pid,
			parent:    int(record.Eproc.Ppid),
			group:     int(record.Eproc.Pgid),
			startSec:  record.Proc.P_starttime.Sec,
			startUsec: record.Proc.P_starttime.Usec,
		}
	}
	return processes, nil
}

func darwinProcessIdentityForPID(pid int) (darwinProcessIdentity, error) {
	record, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return darwinProcessIdentity{}, err
	}
	if record == nil || int(record.Proc.P_pid) != pid {
		return darwinProcessIdentity{}, fmt.Errorf("root process %d was unavailable", pid)
	}
	return darwinProcessIdentity{
		pid:       pid,
		parent:    int(record.Eproc.Ppid),
		group:     int(record.Eproc.Pgid),
		startSec:  record.Proc.P_starttime.Sec,
		startUsec: record.Proc.P_starttime.Usec,
	}, nil
}

func (identity darwinProcessIdentity) same(other darwinProcessIdentity) bool {
	return identity.pid == other.pid &&
		identity.startSec == other.startSec &&
		identity.startUsec == other.startUsec
}

func currentDarwinProcess(identity darwinProcessIdentity) bool {
	record, err := unix.SysctlKinfoProc("kern.proc.pid", identity.pid)
	if err != nil || record == nil {
		return false
	}
	current := darwinProcessIdentity{
		pid:       int(record.Proc.P_pid),
		parent:    int(record.Eproc.Ppid),
		group:     int(record.Eproc.Pgid),
		startSec:  record.Proc.P_starttime.Sec,
		startUsec: record.Proc.P_starttime.Usec,
	}
	return current.same(identity)
}

func signalDarwinProcess(identity darwinProcessIdentity, signal syscall.Signal) error {
	if identity.pid <= 0 || identity.pid == os.Getpid() || !currentDarwinProcess(identity) {
		return nil
	}
	err := syscall.Kill(identity.pid, signal)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

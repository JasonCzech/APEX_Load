//go:build linux

package javarunner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	linuxDescendantPollInterval = 25 * time.Millisecond
	linuxProcessTreeKillTimeout = 2 * time.Second
)

var managedLinuxProcessTrees sync.Map // map[*exec.Cmd]*managedLinuxProcessTree

var createPrivateCgroupForProcess = createPrivateCgroup

type linuxContainmentProbe struct {
	once      sync.Once
	available bool
}

var strongLinuxContainmentProbe = &linuxContainmentProbe{}

func supportsStrongProcessContainment() bool {
	probe := strongLinuxContainmentProbe
	probe.once.Do(func() {
		probe.available = probeStrongLinuxProcessContainment()
	})
	return probe.available
}

type linuxProcessIdentity struct {
	pid          int
	parentPID    int
	processGroup int
	startTime    uint64
	pidfd        int
}

// managedLinuxProcessTree uses a private cgroup v2 when the generator's
// cgroup is delegated. Cgroup membership survives setsid, reparenting, and the
// root JVM's exit, while start-time identities protect the /proc fallback from
// signaling a process that reused a former descendant's PID.
type managedLinuxProcessTree struct {
	mu sync.Mutex

	root       linuxProcessIdentity
	known      map[int]linuxProcessIdentity
	cgroupPath string

	trackerStop chan struct{}
	trackerDone chan struct{}
	stopOnce    sync.Once
	killOnce    sync.Once
	killErr     error
	killed      bool
	owned       bool
	strong      bool
}

func configureProcessTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func startProcessTree(cmd *exec.Cmd) error {
	return startLinuxProcessTree(cmd, false)
}

func startStrongProcessTree(cmd *exec.Cmd) error {
	return startLinuxProcessTree(cmd, true)
}

func startLinuxProcessTree(cmd *exec.Cmd, requireStrong bool) error {
	configureProcessTree(cmd)
	tree := &managedLinuxProcessTree{
		known:      make(map[int]linuxProcessIdentity),
		root:       noLinuxProcessIdentity(),
		cgroupPath: createPrivateCgroupForProcess(),
	}
	var cgroupDir *os.File
	if requireStrong {
		if tree.cgroupPath == "" {
			_ = tree.removeCgroup()
			return ErrStrongProcessContainmentUnavailable
		}
		if _, err := os.Stat(filepath.Join(tree.cgroupPath, "cgroup.kill")); err != nil {
			_ = tree.removeCgroup()
			return errors.Join(ErrStrongProcessContainmentUnavailable, err)
		}
		var err error
		cgroupDir, err = os.Open(tree.cgroupPath)
		if err != nil {
			_ = tree.removeCgroup()
			return errors.Join(ErrStrongProcessContainmentUnavailable, err)
		}
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = int(cgroupDir.Fd())
		tree.strong = true
	}
	// Register before Start so CommandContext cancellation can still find the
	// ownership object if its watcher fires immediately after fork.
	managedLinuxProcessTrees.Store(cmd, tree)
	if err := cmd.Start(); err != nil {
		if cgroupDir != nil {
			_ = cgroupDir.Close()
		}
		managedLinuxProcessTrees.Delete(cmd)
		_ = tree.removeCgroup()
		if requireStrong {
			return errors.Join(ErrStrongProcessContainmentUnavailable, err)
		}
		return err
	}
	if cgroupDir != nil {
		_ = cgroupDir.Close()
	}
	if err := tree.bindRoot(cmd.Process.Pid, requireStrong); err != nil {
		managedLinuxProcessTrees.Delete(cmd)
		_, cleanupErr := tree.kill()
		directKillErr := cmd.Process.Kill()
		if directKillErr != nil && errors.Is(directKillErr, os.ErrProcessDone) {
			directKillErr = nil
		}
		// A successfully drained strong cgroup is the only post-start bind
		// failure for which descendant cleanup remains provable. Best-effort
		// process-group ownership was never established, so even a successful
		// direct-root kill cannot rule out an already-detached child.
		if !tree.strong || cleanupErr != nil || directKillErr != nil {
			return errors.Join(
				ErrProcessContainmentFailed,
				err,
				cleanupErr,
				directKillErr,
			)
		}
		_ = cmd.Wait()
		return err
	}
	return nil
}

func (t *managedLinuxProcessTree) bindRoot(pid int, atomicallyContained bool) error {
	identity, identityErr := captureLinuxProcessIdentity(pid)
	if identityErr == nil {
		t.mu.Lock()
		if t.killed {
			t.mu.Unlock()
			closeLinuxProcessIdentity(identity)
			return fmt.Errorf("root process was canceled during startup")
		}
		t.root = identity
		t.known[pid] = identity
		t.owned = true
		t.mu.Unlock()
	}

	t.mu.Lock()
	if t.killed {
		t.mu.Unlock()
		return fmt.Errorf("root process was canceled during startup")
	}
	cgroupPath := t.cgroupPath
	if atomicallyContained {
		t.owned = true
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	if cgroupPath != "" {
		if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o600); err == nil {
			t.mu.Lock()
			t.owned = true
			t.mu.Unlock()
			return nil
		}
		_ = t.removeCgroup()
	}
	if identityErr != nil {
		return fmt.Errorf("capture Java runner root identity: %w", identityErr)
	}
	t.startProcTracker()
	return nil
}

func (t *managedLinuxProcessTree) startProcTracker() {
	t.mu.Lock()
	if t.killed || t.trackerStop != nil {
		t.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	t.trackerStop = stop
	t.trackerDone = done
	t.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(linuxDescendantPollInterval)
		defer ticker.Stop()
		for {
			t.discoverKnownDescendants()
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (t *managedLinuxProcessTree) stopProcTracker() {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		stop, done := t.trackerStop, t.trackerDone
		t.mu.Unlock()
		if stop == nil {
			return
		}
		close(stop)
		select {
		case <-done:
		case <-time.After(2 * linuxDescendantPollInterval):
		}
	})
}

func (t *managedLinuxProcessTree) discoverKnownDescendants() {
	t.mu.Lock()
	queue := make([]linuxProcessIdentity, 0, len(t.known))
	for _, identity := range t.known {
		queue = append(queue, identity)
	}
	t.mu.Unlock()

	visited := make(map[int]struct{}, len(queue))
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if _, ok := visited[parent.pid]; ok {
			continue
		}
		visited[parent.pid] = struct{}{}
		if !sameLinuxProcess(parent) {
			continue
		}
		for _, pid := range linuxProcessChildren(parent.pid) {
			child, err := captureLinuxProcessIdentity(pid)
			if err != nil || child.pid == os.Getpid() {
				closeLinuxProcessIdentity(child)
				continue
			}
			t.mu.Lock()
			existing, exists := t.known[pid]
			if !exists || existing.startTime != child.startTime {
				t.known[pid] = child
				if exists {
					closeLinuxProcessIdentity(existing)
				}
			} else {
				closeLinuxProcessIdentity(child)
			}
			t.mu.Unlock()
			queue = append(queue, child)
		}
	}
}

func (t *managedLinuxProcessTree) rememberCgroupMembers() {
	t.mu.Lock()
	cgroupPath := t.cgroupPath
	t.mu.Unlock()
	if cgroupPath == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(cgroupPath, "cgroup.procs"))
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		identity, err := captureLinuxProcessIdentity(pid)
		if err != nil {
			continue
		}
		t.mu.Lock()
		existing, exists := t.known[pid]
		t.known[pid] = identity
		if exists {
			closeLinuxProcessIdentity(existing)
		}
		t.mu.Unlock()
	}
}

func (t *managedLinuxProcessTree) kill() (bool, error) {
	t.killOnce.Do(func() {
		t.mu.Lock()
		t.killed = true
		t.mu.Unlock()
		t.stopProcTracker()
		t.discoverKnownDescendants()
		t.rememberProcessGroupMembers()
		t.rememberCgroupMembers()

		t.mu.Lock()
		cgroupPath := t.cgroupPath
		t.mu.Unlock()
		cgroupKilled := false
		if cgroupPath != "" {
			if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.kill"), []byte("1"), 0o600); err == nil {
				cgroupKilled = true
			} else if t.strong {
				t.killErr = errors.Join(
					t.killErr,
					ErrStrongProcessContainmentUnavailable,
					fmt.Errorf("kill Java runner cgroup atomically: %w", err),
				)
			}
		}
		// Strong trees must prove that the atomic cgroup primitive itself
		// worked. A pidfd fallback would hide a seccomp/kernel cgroup.kill
		// failure from the capability probe.
		if !t.strong || !cgroupKilled {
			if err := t.freezeAndKillKnown(); err != nil && !cgroupKilled {
				t.killErr = errors.Join(t.killErr, err)
			}
		}
		if cgroupPath != "" {
			deadline := time.Now().Add(linuxProcessTreeKillTimeout)
			drained := false
			var drainErr error
			if t.strong && cgroupKilled {
				drained, drainErr = t.waitCgroupEmpty(deadline)
			} else {
				drained, drainErr = t.killCgroupMembersUntilEmpty(deadline)
			}
			if drainErr != nil {
				t.killErr = errors.Join(t.killErr, fmt.Errorf("verify Java runner cgroup termination: %w", drainErr))
			}
			if !drained {
				t.killErr = errors.Join(t.killErr, fmt.Errorf("Java runner cgroup remained populated after termination"))
				// Best-effort cleanup after recording strong-primitive failure.
				_ = t.freezeAndKillKnown()
				_, _ = t.killCgroupMembersUntilEmpty(time.Now().Add(linuxProcessTreeKillTimeout))
			}
			if err := t.removeCgroup(); err != nil {
				t.killErr = errors.Join(t.killErr, fmt.Errorf("remove Java runner cgroup: %w", err))
			}
		}
		t.closeKnownIdentities()
	})
	t.mu.Lock()
	owned := t.owned
	t.mu.Unlock()
	return owned, t.killErr
}

func (t *managedLinuxProcessTree) waitCgroupEmpty(deadline time.Time) (bool, error) {
	for time.Now().Before(deadline) {
		populated, err := cgroupPopulated(t.cgroupPath)
		if err != nil {
			return false, err
		}
		if !populated {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	populated, err := cgroupPopulated(t.cgroupPath)
	return !populated, err
}

func probeStrongLinuxProcessContainment() bool {
	const probeExecutable = "/bin/sleep"
	if info, err := os.Stat(probeExecutable); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return false
	}
	cmd := exec.Command(probeExecutable, "3600")
	// The containment probe needs no ambient configuration. In particular, do
	// not transiently propagate cloud/API credentials into a subprocess.
	cmd.Env = []string{}
	configureProcessTree(cmd)
	if err := startLinuxProcessTree(cmd, true); err != nil {
		if errors.Is(err, ErrProcessContainmentFailed) {
			markProcessContainmentFailed(nil)
			// A failed capability probe is cached as unavailable, but its
			// direct root still needs the exactly-once Wait transferred by the
			// failed start contract.
			_ = KillProcessTree(cmd)
			_ = ReapFailedStart(cmd)
		}
		return false
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
		close(waitDone)
	}()
	killErr := KillProcessTree(cmd)
	waitErr, reaped := waitForLinuxContainmentProbe(waitDone, linuxProcessTreeKillTimeout)
	if !reaped {
		// cmd.Wait already has one owner. Retain that channel in the global
		// terminal registry instead of issuing a second Wait or blocking this
		// capability probe for the target's full sleep duration.
		markProcessContainmentFailed(waitDone)
		return false
	}
	if killErr != nil || waitErr == nil || cmd.ProcessState == nil {
		return false
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

// recordProcessTreeAtRootExit blocks until cmd's root exits, leaving it
// unreaped (WNOWAIT) so its PID and process group stay reserved, and records
// the tree's surviving members by parentage and process group. Their pidfds
// keep the later kill bound to these exact processes after Wait reaps the root.
func recordProcessTreeAtRootExit(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	rawTree, ok := managedLinuxProcessTrees.Load(cmd)
	if !ok {
		return
	}
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EINTR) {
			return
		}
	}
	tree := rawTree.(*managedLinuxProcessTree)
	tree.discoverKnownDescendants()
	tree.rememberProcessGroupMembers()
}

func waitForLinuxContainmentProbe(waitDone <-chan error, timeout time.Duration) (error, bool) {
	if timeout <= 0 {
		timeout = linuxProcessTreeKillTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case waitErr := <-waitDone:
		return waitErr, true
	case <-timer.C:
		return nil, false
	}
}

func (t *managedLinuxProcessTree) freezeAndKillKnown() error {
	var firstErr error
	// Stop the currently known tree before the final discovery pass. A stopped
	// process cannot fork a last-moment escapee between enumeration and SIGKILL.
	for round := 0; round < 3; round++ {
		t.discoverKnownDescendants()
		t.rememberProcessGroupMembers()
		for _, identity := range t.knownSnapshot() {
			if err := signalLinuxProcess(identity, syscall.SIGSTOP); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.discoverKnownDescendants()
	for _, identity := range t.knownSnapshot() {
		if err := signalLinuxProcess(identity, syscall.SIGKILL); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *managedLinuxProcessTree) rememberProcessGroupMembers() {
	t.mu.Lock()
	processGroup := t.root.processGroup
	ownedGroupLive := false
	for _, identity := range t.known {
		if identity.processGroup == processGroup && sameLinuxProcess(identity) {
			ownedGroupLive = true
			break
		}
	}
	t.mu.Unlock()
	if processGroup <= 0 || !ownedGroupLive {
		return
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		identity, err := captureLinuxProcessIdentity(pid)
		if err != nil || identity.processGroup != processGroup {
			closeLinuxProcessIdentity(identity)
			continue
		}
		t.rememberIdentity(identity)
	}
}

func (t *managedLinuxProcessTree) rememberIdentity(identity linuxProcessIdentity) {
	t.mu.Lock()
	defer t.mu.Unlock()
	existing, exists := t.known[identity.pid]
	if exists && existing.startTime == identity.startTime {
		closeLinuxProcessIdentity(identity)
		return
	}
	t.known[identity.pid] = identity
	if exists {
		closeLinuxProcessIdentity(existing)
	}
}

func (t *managedLinuxProcessTree) knownSnapshot() []linuxProcessIdentity {
	t.mu.Lock()
	defer t.mu.Unlock()
	identities := make([]linuxProcessIdentity, 0, len(t.known))
	for _, identity := range t.known {
		identities = append(identities, identity)
	}
	return identities
}

func (t *managedLinuxProcessTree) closeKnownIdentities() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for pid, identity := range t.known {
		closeLinuxProcessIdentity(identity)
		delete(t.known, pid)
	}
	t.root = noLinuxProcessIdentity()
}

func (t *managedLinuxProcessTree) killCgroupMembersUntilEmpty(deadline time.Time) (bool, error) {
	for time.Now().Before(deadline) {
		t.rememberCgroupMembers()
		for _, identity := range t.knownSnapshot() {
			_ = signalLinuxProcess(identity, syscall.SIGKILL)
		}
		populated, err := cgroupPopulated(t.cgroupPath)
		if err != nil {
			return false, err
		}
		if !populated {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	populated, err := cgroupPopulated(t.cgroupPath)
	return !populated, err
}

func (t *managedLinuxProcessTree) removeCgroup() error {
	t.mu.Lock()
	path := t.cgroupPath
	t.cgroupPath = ""
	t.mu.Unlock()
	if path != "" {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func killProcessTree(cmd *exec.Cmd) error {
	var treeErr error
	managed := false
	owned := false
	if rawTree, ok := managedLinuxProcessTrees.LoadAndDelete(cmd); ok {
		managed = true
		owned, treeErr = rawTree.(*managedLinuxProcessTree).kill()
	}
	if treeErr != nil {
		return treeErr
	}
	if managed {
		// Never signal the numeric process group after Wait: the kernel may have
		// reused that PGID. Managed executions were terminated through cgroup
		// ownership or identity-bound pidfds above.
		if !owned {
			directErr := cmd.Process.Kill()
			if directErr != nil && !errors.Is(directErr, os.ErrProcessDone) {
				return directErr
			}
		}
		return nil
	}
	// os.Process serializes Kill against Wait. Use that exact process handle as
	// the liveness gate instead of racing on exec.Cmd.ProcessState before any
	// numeric process-group fallback.
	directErr := cmd.Process.Kill()
	if errors.Is(directErr, os.ErrProcessDone) {
		return nil
	}
	// Compatibility fallback for a command that was configured but not started
	// through StartProcessTree. It has no ownership object, so only use its PGID
	// while the direct process is still known to be live.
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if groupErr == nil || errors.Is(groupErr, os.ErrProcessDone) || errors.Is(groupErr, syscall.ESRCH) {
		return nil
	}
	if directErr == nil {
		return nil
	}
	return errors.Join(groupErr, directErr)
}

func readLinuxProcessIdentity(pid int) (linuxProcessIdentity, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return noLinuxProcessIdentity(), err
	}
	// comm is parenthesized and may itself contain spaces or parentheses. The
	// final ')' is followed by state (field 3); starttime is field 22.
	closeParen := strings.LastIndexByte(string(data), ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return noLinuxProcessIdentity(), fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(data[closeParen+1:]))
	if len(fields) <= 19 {
		return noLinuxProcessIdentity(), fmt.Errorf("short /proc/%d/stat", pid)
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return noLinuxProcessIdentity(), err
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil {
		return noLinuxProcessIdentity(), err
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return noLinuxProcessIdentity(), err
	}
	return linuxProcessIdentity{
		pid: pid, parentPID: parentPID, processGroup: processGroup,
		startTime: startTime, pidfd: -1,
	}, nil
}

func captureLinuxProcessIdentity(pid int) (linuxProcessIdentity, error) {
	identity, err := readLinuxProcessIdentity(pid)
	if err != nil {
		return noLinuxProcessIdentity(), err
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err == nil {
		identity.pidfd = pidfd
	}
	return identity, nil
}

// noLinuxProcessIdentity owns no descriptor. A zero value would claim fd 0:
// closing it after a failed capture closed whatever held that number, such as
// another identity's pidfd, which later failed to signal with EBADF.
func noLinuxProcessIdentity() linuxProcessIdentity { return linuxProcessIdentity{pidfd: -1} }

func closeLinuxProcessIdentity(identity linuxProcessIdentity) {
	if identity.pidfd >= 0 {
		_ = unix.Close(identity.pidfd)
	}
}

func sameLinuxProcess(identity linuxProcessIdentity) bool {
	if identity.pid <= 0 || identity.pid == os.Getpid() {
		return false
	}
	current, err := readLinuxProcessIdentity(identity.pid)
	return err == nil && current.startTime == identity.startTime
}

func signalLinuxProcess(identity linuxProcessIdentity, signal syscall.Signal) error {
	if identity.pidfd >= 0 {
		err := unix.PidfdSendSignal(identity.pidfd, signal, nil, 0)
		if err == nil || errors.Is(err, unix.ESRCH) {
			return nil
		}
		return err
	}
	if !sameLinuxProcess(identity) {
		return nil
	}
	err := syscall.Kill(identity.pid, signal)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func linuxProcessChildren(pid int) []int {
	taskDir := filepath.Join("/proc", strconv.Itoa(pid), "task")
	tasks, err := os.ReadDir(taskDir)
	if err != nil {
		return nil
	}
	seen := make(map[int]struct{})
	children := make([]int, 0)
	for _, task := range tasks {
		if !task.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(taskDir, task.Name(), "children"))
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(data)) {
			child, err := strconv.Atoi(field)
			if err != nil || child <= 0 {
				continue
			}
			if _, exists := seen[child]; exists {
				continue
			}
			seen[child] = struct{}{}
			children = append(children, child)
		}
	}
	return children
}

func createPrivateCgroup() string {
	parent := currentCgroupV2Directory()
	if parent == "" {
		return ""
	}
	path, err := os.MkdirTemp(parent, ".apexload-java-vuser-")
	if err != nil {
		return ""
	}
	return path
}

func currentCgroupV2Directory() string {
	membershipData, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	membership := ""
	for _, line := range strings.Split(string(membershipData), "\n") {
		if strings.HasPrefix(line, "0::") {
			membership = strings.TrimSpace(strings.TrimPrefix(line, "0::"))
			break
		}
	}
	if membership == "" {
		membership = "/"
	}
	membership = filepath.Clean("/" + strings.TrimPrefix(membership, "/"))

	mountData, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(mountData), "\n") {
		separator := strings.Index(line, " - ")
		if separator < 0 || !strings.HasPrefix(line[separator+3:], "cgroup2 ") {
			continue
		}
		fields := strings.Fields(line[:separator])
		if len(fields) < 5 {
			continue
		}
		mountRoot := filepath.Clean(unescapeMountInfo(fields[3]))
		mountPoint := filepath.Clean(unescapeMountInfo(fields[4]))
		relative, err := filepath.Rel(mountRoot, membership)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		candidate := filepath.Join(mountPoint, relative)
		if _, err := os.Stat(filepath.Join(candidate, "cgroup.procs")); err == nil {
			return candidate
		}
	}
	return ""
}

func unescapeMountInfo(value string) string {
	replacer := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	)
	return replacer.Replace(value)
}

func cgroupPopulated(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			switch fields[1] {
			case "0":
				return false, nil
			case "1":
				return true, nil
			default:
				return false, fmt.Errorf("invalid populated value %q", fields[1])
			}
		}
	}
	return false, fmt.Errorf("cgroup.events did not report populated state")
}

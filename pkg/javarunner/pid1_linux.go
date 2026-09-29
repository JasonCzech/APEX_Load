//go:build linux

package javarunner

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// RequirePID1Isolation rejects hostPID and shared-process pods (the Go process
// would not be PID 1), root execution, retained capabilities and missing NNP.
// Clearing dumpability also prevents ordinary same-UID ptrace/proc-memory reads.
func RequirePID1Isolation() error {
	if os.Getpid() != 1 || os.Geteuid() == 0 {
		return ErrPID1Isolation
	}
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return ErrPID1Isolation
	}
	status, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	_ = file.Close()
	if err != nil || validatePID1Posture(os.Getpid(), os.Geteuid(), status) != nil {
		return ErrPID1Isolation
	}
	var root unix.Statfs_t
	if unix.Statfs("/", &root) != nil || root.Flags&unix.ST_RDONLY == 0 {
		return ErrPID1Isolation
	}
	if unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) != nil {
		return ErrPID1Isolation
	}
	return nil
}

// ReapPodOrphans is used ONLY after preparation probes finish and the sole JVM
// root is registered. It never waits for that root (exec.Cmd owns its Wait),
// never sends signals, and never constitutes container termination evidence.
// The caller exits the pod on an error instead of letting zombie/PID exhaustion
// silently degrade a long-running Vuser.
func ReapPodOrphans(rootPID int) error {
	if os.Getpid() != 1 || os.Geteuid() == 0 || rootPID <= 1 {
		return ErrPID1Isolation
	}
	proc, err := os.Open("/proc")
	if err != nil {
		return err
	}
	defer proc.Close()
	entries, err := proc.ReadDir(4097)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 4096 {
		return errors.New("java_supervisor_pid_limit")
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == rootPID {
			continue
		}
		var status unix.WaitStatus
		_, err = unix.Wait4(pid, &status, unix.WNOHANG, nil)
		if err != nil && !errors.Is(err, unix.ECHILD) && !errors.Is(err, unix.ESRCH) && !errors.Is(err, unix.EINTR) {
			return err
		}
	}
	return nil
}

// RequireReadOnlyAsset checks the opened file's mount, not its spelling. A
// read-only root alone does not make scratch bind mounts (including /tmp) safe
// sources of approved code. Standard image symlinks such as bin/java are allowed.
func RequireReadOnlyAsset(path string) error {
	if !filepath.IsAbs(path) {
		return ErrPID1Isolation
	}
	before, e := os.Stat(path)
	if e != nil || !before.Mode().IsRegular() {
		return ErrPID1Isolation
	}
	file, e := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if e != nil {
		return ErrPID1Isolation
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return ErrPID1Isolation
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Flags&unix.ST_RDONLY == 0 {
		return ErrPID1Isolation
	}
	return nil
}

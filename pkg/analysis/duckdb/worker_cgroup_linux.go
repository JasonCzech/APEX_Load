//go:build linux

package duckdb

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// linuxCgroup is an administrator-approved delegated v2 subtree, never an IPC
// field, artifact handle, user path or ambient environment discovery. If no
// delegation exists the separately described observed-memory profile applies.
type workerLinuxCgroup struct {
	parent      *os.Root
	owner       *workerAttempt
	directory   *os.File
	root        *os.Root
	name        string
	createdInfo os.FileInfo
}

func workerPrepareUnixContainment(a *workerAttempt, profile workerProcessProfile) (workerUnixContainment, error) {
	if profile.linuxCgroup == "" {
		if profile.requireHard {
			return nil, ErrUnavailable
		}
		return nil, nil
	}
	path := filepath.Clean(profile.linuxCgroup)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path || !filepath.IsAbs(path) || !strings.HasPrefix(path, "/sys/fs/cgroup/") {
		return nil, errWorkerOwnership
	}
	parent, err := os.OpenRoot(path)
	if err != nil {
		return nil, errWorkerOwnership
	}
	a.trackDescriptor(parent)
	failed := true
	var c *workerLinuxCgroup
	defer func() {
		if failed {
			if c != nil {
				a.requestDescriptorClose(c)
			} else {
				a.requestDescriptorClose(parent)
			}
		}
	}()
	var status unix.Statfs_t
	if unix.Statfs(path, &status) != nil || status.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, errWorkerOwnership
	}
	controllers, err := workerCgroupRead(a, parent, "cgroup.subtree_control")
	if err != nil || !workerCgroupToken(controllers, "memory") || !workerCgroupToken(controllers, "pids") {
		return nil, ErrUnavailable
	} // no mutation of deployment delegation
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, errWorkerOwnership
	}
	name := "apexload-native-" + hex.EncodeToString(random[:])
	if parent.Mkdir(name, 0700) != nil {
		return nil, errWorkerOwnership
	}
	c = &workerLinuxCgroup{parent: parent, name: name, owner: a}
	a.trackDescriptor(c)
	// Capture creation identity before any setting/open can fail. The compound
	// owner alone closes its leaves; competing error defers cannot invalidate
	// its retained removal proof.
	c.createdInfo, err = parent.Lstat(name)
	if err != nil || !c.createdInfo.IsDir() {
		return nil, errWorkerOwnership
	}
	directory, err := parent.Open(name)
	if err != nil {
		return nil, errWorkerOwnership
	}
	c.directory = directory
	a.trackDescriptor(directory)
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, errWorkerOwnership
	}
	c.root = root
	a.trackDescriptor(root)
	memory, err := workerAdd(profile.memoryBytes, 512<<20)
	if err != nil || profile.memoryBytes <= 0 {
		return nil, errWorkerResourceLimit
	}
	for _, setting := range [][2]string{{"memory.max", strconv.FormatInt(memory, 10)}, {"memory.swap.max", "0"}, {"memory.oom.group", "1"}, {"pids.max", "64"}, {"cgroup.kill", "1"}} {
		if workerCgroupWrite(a, root, setting[0], setting[1]) != nil {
			return nil, errWorkerOwnership
		}
	}

	failed = false
	return c, nil
}
func workerCgroupToken(text, token string) bool {
	for _, value := range strings.Fields(text) {
		if value == token {
			return true
		}
	}
	return false
}
func workerCgroupRead(a *workerAttempt, root *os.Root, name string) (string, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", errWorkerOwnership
	}
	a.trackDescriptor(file)
	body, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := a.awaitDescriptorClose(file)
	if closeErr == nil {
		a.forgetJoinedOwner(file)
	}
	if readErr != nil || closeErr != nil || len(body) > 4096 {
		return "", errWorkerOwnership
	}
	return string(body), nil
}
func workerCgroupWrite(a *workerAttempt, root *os.Root, name, value string) error {
	file, err := root.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return errWorkerOwnership
	}
	a.trackDescriptor(file)
	n, writeErr := io.WriteString(file, value)
	closeErr := a.awaitDescriptorClose(file)
	if closeErr == nil {
		a.forgetJoinedOwner(file)
	}
	if writeErr != nil || closeErr != nil || n != len(value) {
		return errWorkerOwnership
	}
	return nil
}
func (c *workerLinuxCgroup) configure(cmd *exec.Cmd) error {
	if c == nil || c.directory == nil {
		return errWorkerOwnership
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(c.directory.Fd())
	return nil
}
func (c *workerLinuxCgroup) kill() error {
	return workerCgroupWrite(c.owner, c.root, "cgroup.kill", "1")
}
func (c *workerLinuxCgroup) closeAfterWait() error {
	if c.parent == nil || c.createdInfo == nil {
		return errWorkerOwnership
	}
	if c.directory == nil {
		directory, err := c.parent.Open(c.name)
		if err != nil {
			return errWorkerOwnership
		}
		c.directory = directory
		c.owner.trackDescriptor(directory)
	}
	if c.root == nil {
		root, err := c.parent.OpenRoot(c.name)
		if err != nil {
			return errWorkerOwnership
		}
		c.root = root
		c.owner.trackDescriptor(root)
	}
	events, err := workerCgroupRead(c.owner, c.root, "cgroup.events")
	if err != nil {
		return err
	}
	empty := false
	for _, line := range strings.Split(events, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			empty = fields[1] == "0"
		}
	}
	if !empty {
		return errWorkerOwnership
	}
	// Removal must still name the exact kernel-owned directory descriptor.
	old, err := c.directory.Stat()
	current, statErr := c.parent.Lstat(c.name)
	if err != nil || statErr != nil || !os.SameFile(old, current) || !os.SameFile(c.createdInfo, current) || current.Mode()&os.ModeSymlink != 0 {
		return errWorkerOwnership
	}
	if c.parent.Remove(c.name) != nil {
		return errWorkerOwnership
	}
	return errors.Join(c.owner.awaitDescriptorClose(c.root), c.owner.awaitDescriptorClose(c.directory), c.owner.awaitDescriptorClose(c.parent))
}

func (c *workerLinuxCgroup) Close() error { return c.closeAfterWait() }

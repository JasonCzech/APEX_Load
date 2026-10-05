//go:build linux || darwin

package duckdb

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

type workerUnixContainment interface {
	configure(*exec.Cmd) error
	kill() error
	closeAfterWait() error
}

type workerOwnedProcess struct {
	containment       workerUnixContainment
	owner             *workerAttempt
	containmentCloser *workerContainmentCloseOwner
	cmd               *exec.Cmd
	mu                sync.Mutex
	reaped            bool
	waitOnce          sync.Once
	waitErr           error
}

func workerStartOwned(a *workerAttempt, cmd *exec.Cmd, bootstrap, root, commands *os.File, profile workerProcessProfile) (*workerOwnedProcess, error) {
	containment, err := workerPrepareUnixContainment(a, profile)
	if err != nil {
		return nil, err
	}
	var containmentCloser *workerContainmentCloseOwner
	if containment != nil {
		containmentCloser = &workerContainmentCloseOwner{containment: containment, owner: a}
		a.trackDescriptor(containmentCloser)
	}
	cmd.ExtraFiles = []*os.File{bootstrap, root, commands}
	workerConfigureUnix(cmd)
	if containment != nil && containment.configure(cmd) != nil {
		a.requestDescriptorClose(containmentCloser)
		return nil, errWorkerOwnership
	}
	if err := cmd.Start(); err != nil {
		if containment != nil && a.closeDescriptor(containmentCloser) != nil {
			return nil, errWorkerOwnership
		}
		return nil, errWorkerProtocol
	}
	return &workerOwnedProcess{cmd: cmd, containment: containment, owner: a, containmentCloser: containmentCloser}, nil
}
func (p *workerOwnedProcess) wait() error {
	p.waitOnce.Do(func() { p.waitErr = p.cmd.Wait(); p.mu.Lock(); p.reaped = true; p.mu.Unlock() })
	return p.waitErr
}
func (p *workerOwnedProcess) signal(signal os.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaped {
		return nil
	}
	// os.Process owns the kernel identity/handle. Never signal a stale numeric
	// PID/PGID after Wait. This initial profile executes no subprocess/shell.
	if err := p.cmd.Process.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errWorkerOwnership
	}
	return nil
}
func (p *workerOwnedProcess) terminate() error { return p.signal(syscall.SIGTERM) }
func (p *workerOwnedProcess) kill() error {
	if p.containment != nil && p.containment.kill() != nil {
		return errWorkerOwnership
	}
	return p.signal(os.Kill)
}
func (p *workerOwnedProcess) closeAfterWait() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reaped {
		return errWorkerOwnership
	}
	if p.containment != nil {
		return p.owner.awaitDescriptorClose(p.containmentCloser)
	}
	return nil
}
func workerInheritedChannels() (*os.File, *os.File, *os.File, error) {
	// A malformed early worker invocation may share these numbers with Go's
	// kqueue/eventfd/poll wake descriptors. Validate every raw role before
	// adopting any number, changing flags or closing a foreign descriptor.
	if workerValidateRawInheritedChannels() != nil {
		return nil, nil, nil, errWorkerProtocol
	}
	boot := os.NewFile(3, "native-bootstrap")
	root := os.NewFile(4, "native-root")
	commands, commandErr := workerPollableInheritedCommandFile(5)
	if commandErr != nil {
		if boot != nil {
			_ = boot.Close()
		}
		if root != nil {
			_ = root.Close()
		}
		return nil, nil, nil, commandErr
	}
	if boot == nil || root == nil || commands == nil {
		return nil, nil, nil, errWorkerProtocol
	}
	for _, f := range []*os.File{boot, commands} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			boot.Close()
			root.Close()
			commands.Close()
			return nil, nil, nil, errWorkerProtocol
		}
	}
	info, err := root.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		boot.Close()
		root.Close()
		commands.Close()
		return nil, nil, nil, errWorkerProtocol
	}
	return boot, root, commands, nil
}
func workerInheritedRootPath(root *os.File) (string, error) {
	name, pathErr := workerDirectoryFDPath(root)
	if pathErr != nil {
		return "", errWorkerProtocol
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", errWorkerProtocol
	}
	original, err := root.Stat()
	current, statErr := os.Stat(resolved)
	if err != nil || statErr != nil || !current.IsDir() || !os.SameFile(original, current) {
		return "", errWorkerProtocol
	}
	return resolved, nil
}
func workerPlatformEnvironment(root string) []string {
	return []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "TMPDIR=" + root}
}

type workerContainmentCloseOwner struct {
	containment workerUnixContainment
	owner       *workerAttempt
}

func (owner *workerContainmentCloseOwner) Close() error {
	if closer, ok := owner.containment.(io.Closer); ok {
		return owner.owner.awaitDescriptorClose(closer)
	}
	return owner.containment.closeAfterWait()
}

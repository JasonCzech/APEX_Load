package javarunner

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

// LocalHostBackend supplies strong process containment and IPC to HostSupervisor.
// Admission/qualification and shared fleet quotas remain the runner's authority.
type LocalHostBackend struct {
	containmentBackend string
	launch             PooledLaunchProvider
	runtimeSupported   func(ResolvedJDK) bool
	supports           func() bool
	credentialCheck    func() error
	start              func(*exec.Cmd) error
	kill               func(*exec.Cmd) error
}

func NewLocalHostBackend(launch PooledLaunchProvider) (*LocalHostBackend, error) {
	if launch == nil {
		return nil, ErrHostState
	}
	backend := "unsupported"
	switch runtime.GOOS {
	case "linux":
		backend = "delegated_linux"
	case "windows":
		backend = "windows_job"
	}
	return &LocalHostBackend{containmentBackend: backend, launch: launch, runtimeSupported: pooledRuntimeSupported, credentialCheck: subprocessguard.ValidateAmbientCredentials, supports: SupportsStrongProcessContainment, start: StartStrongProcessTree, kill: KillProcessTree}, nil
}

type hostCapture struct {
	mu        sync.Mutex
	bytes     []byte
	dropped   uint64
	truncated bool
}

func (c *hostCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := min(len(p), HostDiagnosticCaptureBytes-len(c.bytes))
	c.bytes = append(c.bytes, p[:n]...)
	extra := uint64(len(p) - n)
	if extra > 0 {
		c.truncated = true
	}
	if extra > ^uint64(0)-c.dropped {
		c.dropped = ^uint64(0)
	} else {
		c.dropped += extra
	}
	return len(p), nil
}

func (c *hostCapture) markTruncated() { c.mu.Lock(); c.truncated = true; c.mu.Unlock() }

type localManagedHost struct {
	diagnosticsMu                          sync.Mutex
	mu                                     sync.Mutex
	id, generation                         string
	diagnosticDirectory                    string
	cmd                                    *exec.Cmd
	listener                               *net.TCPListener
	stdin                                  io.WriteCloser
	stdout, stderr                         *os.File
	stdoutDone, stderrDone, waitDone, done chan struct{}
	signalOnce                             sync.Once
	terminateOnce                          sync.Once
	terminated                             chan struct{}
	terminating                            bool
	terminationErr                         error
	transport                              *PooledTransport
	secret                                 []byte
	kill                                   func(*exec.Cmd) error
	failedStart                            bool
	captureOut, captureErr                 hostCapture
}

func (b *LocalHostBackend) Provision(ctx context.Context, a HostAllocation) (ManagedHost, error) {
	notStarted := func(err error) (ManagedHost, error) { return nil, errors.Join(ErrHostNotStarted, err) }
	if err := ctx.Err(); err != nil {
		return notStarted(err)
	}
	if b.credentialCheck() != nil {
		return notStarted(errors.New("pooled_runner_credentials_present"))
	}
	if !b.supports() || ProcessContainmentFailed() {
		return notStarted(ErrStrongProcessContainmentUnavailable)
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return notStarted(err)
	}
	cleanupListener := true
	defer func() {
		if cleanupListener {
			_ = listener.Close()
		}
	}()
	spec, err := b.launch(ctx, a)
	if err != nil {
		if errors.Is(err, ErrProcessContainmentFailed) {
			return nil, err
		}
		return notStarted(err)
	}
	if spec.ContainmentBackend != "" && spec.ContainmentBackend != b.containmentBackend {
		return notStarted(ErrStrongProcessContainmentUnavailable)
	}
	cmd, err := buildPooledCommand(spec, a, listener.Addr().(*net.TCPAddr).Port, b.runtimeSupported)
	if err != nil {
		return notStarted(err)
	}
	if cmd == nil {
		return notStarted(ErrHostState)
	}
	// The harness owns JFR's repository, never a script-supplied path. Retain
	// it on uncertain containment and remove it only after proven termination.
	var diagnosticDirectory string
	if spec.JDK.VMFamily == JavaVMHotSpot {
		diagnosticDirectory, err = os.MkdirTemp("", "apexload-pooled-jfr-")
		if err != nil {
			return notStarted(err)
		}
		if strings.ContainsAny(diagnosticDirectory, ",\n\r") {
			_ = os.RemoveAll(diagnosticDirectory)
			return notStarted(errors.New("pooled_jfr_repository_invalid"))
		}
		cmd.Args = append([]string{cmd.Args[0], "-XX:FlightRecorderOptions=repository=" + diagnosticDirectory}, cmd.Args[1:]...)
	}
	keepDiagnostics := false
	defer func() {
		if !keepDiagnostics && diagnosticDirectory != "" {
			_ = os.RemoveAll(diagnosticDirectory)
		}
	}()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return notStarted(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return notStarted(err)
	}
	out, outChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return notStarted(err)
	}
	stderr, errChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = out.Close()
		_ = outChild.Close()
		return notStarted(err)
	}
	cmd.Stdout = outChild
	cmd.Stderr = errChild
	cmd.WaitDelay = 2 * time.Second
	h := &localManagedHost{generation: a.ID, diagnosticDirectory: diagnosticDirectory, cmd: cmd, listener: listener, stdin: stdin, stdout: out, stderr: stderr, stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}), waitDone: make(chan struct{}), done: make(chan struct{}), terminated: make(chan struct{}), secret: secret, kill: b.kill}
	ConfigureProcessTree(cmd)
	// Qualification may be slow; cancellation must be checked again before launch.
	if err = ctx.Err(); err == nil {
		if b.credentialCheck() != nil {
			err = errors.New("pooled_runner_credentials_present")
		} else {
			err = b.start(cmd)
		}
	}
	_ = outChild.Close()
	_ = errChild.Close()
	if err != nil && !errors.Is(err, ErrProcessContainmentFailed) {
		_ = stdin.Close()
		_ = out.Close()
		_ = stderr.Close()
		return notStarted(err)
	}
	cleanupListener = false
	keepDiagnostics = true
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	h.id = fmt.Sprintf("local:%s:%d", a.ID, pid)
	go func() {
		defer close(h.stdoutDone)
		if _, err := io.Copy(&h.captureOut, out); err != nil {
			h.captureOut.markTruncated()
		}
		_ = out.Close()
	}()
	go func() {
		defer close(h.stderrDone)
		if _, err := io.Copy(&h.captureErr, stderr); err != nil {
			h.captureErr.markTruncated()
		}
		_ = stderr.Close()
	}()
	if err != nil {
		h.failedStart = true
		reap := ReapFailedStart(cmd)
		go func() { <-reap; close(h.waitDone); h.signal() }()
		return h, err
	}
	go func() { _ = cmd.Wait(); close(h.waitDone); h.signal() }()
	// Returning the handle even after cancellation lets the supervisor retain and
	// clean this exact boundary; it must never infer that cancellation prevented start.
	if err = ctx.Err(); err != nil {
		return h, err
	}
	return h, nil
}
func (h *localManagedHost) Identity() string      { return h.id }
func (h *localManagedHost) Done() <-chan struct{} { return h.done }
func (h *localManagedHost) signal()               { h.signalOnce.Do(func() { close(h.done) }) }

func (h *localManagedHost) Ready(ctx context.Context, contexts map[string]map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Stdin is only a bootstrap secret channel. Close it before any slot can
	// execute so FileDescriptor.in and native fd 0 reads cannot eat controls.
	n, err := h.stdin.Write(h.secret)
	closeErr := h.stdin.Close()
	if err == nil && n != len(h.secret) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("pooled_host_transport_lost")
	}
	connection, err := acceptAuthenticatedPooledHost(ctx, h.listener, h.generation, h.secret)
	if err != nil {
		return errors.New("pooled_host_transport_lost")
	}
	transport, err := NewPooledTransport(connection, connection, h.generation, h.secret, func(error) { h.signal() })
	if err != nil {
		_ = connection.Close()
		return err
	}
	h.mu.Lock()
	if h.terminating || h.transport != nil {
		h.mu.Unlock()
		transport.Close()
		return ErrHostState
	}
	h.transport = transport
	h.mu.Unlock()
	if _, err = transport.Call(ctx, "", "ready", nil); err != nil {
		return err
	}
	slots := make([]string, 0, len(contexts))
	for slot := range contexts {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	for _, slot := range slots {
		if _, err = transport.Call(ctx, slot, "open", contexts[slot]); err != nil {
			return err
		}
	}
	return nil
}

// A loopback peer must prove possession of the per-host bootstrap secret
// before it can claim the only control socket. The proof has its own domain,
// separate from frame MACs, and never exposes the secret on the socket.
func pooledHostProof(generation string, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("apexload.java_vuser.ipc.v3.host_auth:" + generation))
	return mac.Sum(nil)
}

func acceptAuthenticatedPooledHost(ctx context.Context, listener *net.TCPListener, generation string, secret []byte) (*net.TCPConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	defer listener.Close()
	expected := pooledHostProof(generation, secret)
	for {
		connection, err := listener.AcceptTCP()
		if err != nil {
			return nil, err
		}
		// A silent or forged preconnection cannot strand a later real JVM. Each
		// candidate has a short deadline; the whole loop obeys the owner context.
		deadline := time.Now().Add(time.Second)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		_ = connection.SetReadDeadline(deadline)
		proof := make([]byte, sha256.Size)
		cancelRead := context.AfterFunc(ctx, func() { _ = connection.Close() })
		_, err = io.ReadFull(connection, proof)
		cancelRead()
		if err == nil && ctx.Err() == nil && hmac.Equal(proof, expected) {
			if err = connection.SetReadDeadline(time.Time{}); err == nil {
				return connection, nil
			}
		}
		_ = connection.Close()
		if err = ctx.Err(); err != nil {
			return nil, err
		}
	}
}
func (h *localManagedHost) client() (*PooledTransport, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.terminating {
		return nil, ErrPooledHostReaped
	}
	if h.transport == nil {
		return nil, ErrHostState
	}
	return h.transport, nil
}
func (h *localManagedHost) Commit(ctx context.Context) error {
	t, err := h.client()
	if err != nil {
		return err
	}
	_, err = t.Call(ctx, "", "commit", nil)
	return err
}
func (h *localManagedHost) Invoke(ctx context.Context, slot, method string, parameters map[string]string) (PooledFrame, bool, error) {
	t, err := h.client()
	if err != nil {
		return PooledFrame{}, false, err
	}
	return t.InvokeWithClosure(ctx, slot, method, parameters)
}
func (h *localManagedHost) CloseSlot(ctx context.Context, slot string) error {
	t, err := h.client()
	if err != nil {
		return err
	}
	_, err = t.Call(ctx, slot, "close", nil)
	return err
}

func (h *localManagedHost) Terminate(ctx context.Context) error {
	h.terminateOnce.Do(func() {
		h.mu.Lock()
		h.terminating = true
		t := h.transport
		h.mu.Unlock()
		h.signal()
		_ = h.listener.Close()
		_ = h.stdin.Close()
		if t != nil {
			t.Close()
		}
		go func() {
			err := h.kill(h.cmd)
			// A failed atomic start permanently closes process-wide admission. Do not
			// reinterpret a later best-effort kill as proof restoring its reservation.
			if h.failedStart {
				err = ErrProcessContainmentFailed
			}
			if err == nil {
				select {
				case <-h.waitDone:
				case <-ctx.Done():
					err = ctx.Err()
				}
			}
			if err == nil {
				if !WaitForOutputDrain(2*time.Second, []io.Closer{h.stdout, h.stderr}, h.stdoutDone, h.stderrDone) {
					h.captureOut.markTruncated()
					h.captureErr.markTruncated()
				}
			} else {
				h.captureOut.markTruncated()
				h.captureErr.markTruncated()
				_ = h.stdout.Close()
				_ = h.stderr.Close()
			}
			h.mu.Lock()
			if err == nil && h.diagnosticDirectory != "" {
				err = os.RemoveAll(h.diagnosticDirectory)
			}
			h.terminationErr = err
			h.mu.Unlock()
			close(h.terminated)
		}()
	})
	select {
	case <-h.terminated:
		h.mu.Lock()
		err := h.terminationErr
		h.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

package javarunner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPooledHostRejectsPreconnectionBeforeSelectingControlSocket(t *testing.T) {
	for _, mode := range []string{"wrong_proof", "silent"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			secret := make([]byte, 32)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			bogus, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer bogus.Close()
			if mode == "wrong_proof" {
				if _, err = bogus.Write(make([]byte, 32)); err != nil {
					t.Fatal(err)
				}
			}
			type result struct {
				connection *net.TCPConn
				err        error
			}
			resultCh := make(chan result, 1)
			go func() {
				connection, err := acceptAuthenticatedPooledHost(ctx, listener, "generation", secret)
				resultCh <- result{connection, err}
			}()
			if err = bogus.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var b [1]byte
			_, err = bogus.Read(b[:])
			if err == nil {
				t.Fatal("unauthenticated connection received data")
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				t.Fatalf("unauthenticated connection stayed open: %v", err)
			}
			legitimate, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer legitimate.Close()
			if _, err = legitimate.Write(pooledHostProof("generation", secret)); err != nil {
				t.Fatal(err)
			}
			select {
			case selected := <-resultCh:
				if selected.err != nil || selected.connection == nil {
					t.Fatalf("valid JVM connection rejected: %v", selected.err)
				}
				_ = selected.connection.Close()
			case <-ctx.Done():
				t.Fatal("valid JVM connection stranded by preconnection")
			}
		})
	}
}

func TestLocalHostBackendFailsClosedWithoutContainment(t *testing.T) {
	var called atomic.Bool
	b, _ := NewLocalHostBackend(func(context.Context, HostAllocation) (PooledLaunchSpec, error) {
		called.Store(true)
		return PooledLaunchSpec{}, nil
	})
	b.credentialCheck = func() error { return nil }
	b.supports = func() bool { return false }
	h, err := b.Provision(context.Background(), HostAllocation{ID: "host"})
	if h != nil || !errors.Is(err, ErrHostNotStarted) || !errors.Is(err, ErrStrongProcessContainmentUnavailable) || called.Load() {
		t.Fatalf("uncontained launch admitted: %v", err)
	}
}
func TestHostCaptureBoundedAndSaturating(t *testing.T) {
	c := &hostCapture{}
	p := make([]byte, 300<<10)
	if n, err := c.Write(p); n != len(p) || err != nil {
		t.Fatal(n, err)
	}
	if len(c.bytes) != 256<<10 || c.dropped != 44<<10 {
		t.Fatal("capture unbounded")
	}
	c.dropped = ^uint64(0) - 1
	_, _ = c.Write(p)
	if c.dropped != ^uint64(0) {
		t.Fatal("drop count overflow")
	}
}

// Real JVM/component coverage using package-private process hooks. These fixtures
// create no descendants. Passing does NOT qualify Darwin or strong containment.
func TestLocalHostBackendRealJVMComponents(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	java, err = filepath.Abs(java)
	if err != nil {
		t.Fatal(err)
	}
	jdk, err := ResolveJDK(context.Background(), ResolveOptions{ExplicitJavaCommand: java})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	api, shim, script := filepath.Join(dir, "api"), filepath.Join(dir, "shim"), filepath.Join(dir, "script")
	for _, p := range []string{api, shim, script} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	compile := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, e := exec.CommandContext(ctx, javac, args...).CombinedOutput(); e != nil {
			t.Fatalf("javac: %v %s", e, out)
		}
	}
	sources := []string{"--release", "8", "-d", api}
	for _, name := range []string{"bridge/SlotRuntime.java", "host/SlotLoader.java", "host/SlotWorker.java", "host/PooledHost.java", "host/PooledWire.java", "host/PooledMain.java", "host/GCPauses.java"} {
		sources = append(sources, filepath.Join("../../runners/java_vuser/apexload", name))
	}
	compile(sources...)
	compile("--release", "8", "-cp", api, "-d", shim, "../../runners/java_vuser/lrapi/lr.java", "../../runners/java_vuser/lrapi/SlotBridge.java")
	source := filepath.Join(dir, "SupervisedActions.java")
	if err := os.WriteFile(source, []byte(supervisedActions), 0600); err != nil {
		t.Fatal(err)
	}
	compile("--release", "8", "-cp", shim, "-d", script, source)
	apiJar, shimJar, scriptJar := filepath.Join(dir, "host.jar"), filepath.Join(dir, "shim.jar"), filepath.Join(dir, "script.jar")
	jar := filepath.Join(jdk.Home, "bin", "jar")
	if strings.HasSuffix(jdk.JavaBin, ".exe") {
		jar += ".exe"
	}
	for destination, classes := range map[string]string{apiJar: api, shimJar: shim, scriptJar: script} {
		if out, e := exec.Command(jar, "--create", "--file", destination, "-C", classes, ".").CombinedOutput(); e != nil {
			t.Fatalf("jar: %v %s", e, out)
		}
	}
	launch := func(_ context.Context, a HostAllocation) (PooledLaunchSpec, error) {
		return PooledLaunchSpec{ConfigurationSHA256: a.Binding.ConfigurationSHA256, JDK: jdk, HarnessJar: apiJar, ShimJar: shimJar, ScriptClasspath: []string{scriptJar}, VMParameters: []string{"-Xmx128m"}, TargetClass: "SupervisedActions", WorkDir: dir, QualifiedCleanup: true}, nil
	}
	setup := func(t *testing.T) (*HostSupervisor, *HostLedger, []HostAllocation, map[string]map[string]string, *LocalHostBackend) {
		t.Helper()
		l, err := NewHostLedger(HostBinding{"owner", "epoch", "run", "group", strings.Repeat("a", 64)}, HostBudget{1, 2, 512 << 20}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		allocations, err := l.Reserve("epoch", 2, HostProfile{2, 512 << 20}, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		contexts := make(map[string]map[string]string)
		for i, slot := range allocations[0].SlotIDs {
			contexts[slot] = map[string]string{"APEXLOAD_VUSER_ID": strconv.Itoa(i + 1)}
		}
		b, _ := NewLocalHostBackend(launch)
		b.runtimeSupported = func(ResolvedJDK) bool { return true }
		b.credentialCheck = func() error { return nil }
		b.supports = func() bool { return true }
		b.start = func(cmd *exec.Cmd) error { return cmd.Start() }
		b.kill = func(cmd *exec.Cmd) error {
			err := cmd.Process.Kill()
			if errors.Is(err, os.ErrProcessDone) {
				return nil
			}
			return err
		}
		s, _ := NewHostSupervisor(l, b, true)
		t.Cleanup(func() { closeSupervisor(t, s) })
		return s, l, allocations, contexts, b
	}
	t.Run("barrier_cancellation_drain", func(t *testing.T) {
		s, l, a, contexts, _ := setup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Prepare(ctx, "epoch", a[0].ReservationID, contexts); err != nil {
			t.Fatal(err)
		}
		h := s.runtime(a[0].ID).(*localManagedHost)
		assertCold := func() {
			t.Helper()
			transport, err := h.client()
			if err != nil {
				t.Fatal(err)
			}
			reply, err := transport.Call(ctx, "", "health", nil)
			if err != nil {
				t.Fatal(err)
			}
			if reply.Fields["diagnostics_base64"] != "" {
				t.Fatal("user initialization crossed preparation/commit barrier")
			}
		}
		assertCold()
		if _, err := s.Invoke(ctx, "epoch", a[0].ID, a[0].SlotIDs[0], "init", nil); err == nil {
			t.Fatal("precommit init admitted")
		}
		if err := s.Commit(ctx, "epoch", a[0].ReservationID); err != nil {
			t.Fatal(err)
		}
		assertCold()
		for _, slot := range a[0].SlotIDs {
			if _, err := s.Invoke(ctx, "epoch", a[0].ID, slot, "init", nil); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Invoke(ctx, "epoch", a[0].ID, a[0].SlotIDs[0], "action", map[string]string{"mode": "rawstdin"}); err != nil {
			t.Fatal("raw fd 0 consumed host control data:", err)
		}
		diagnostics, err := s.Diagnostics(ctx, "epoch", a[0].ID)
		if err != nil || !strings.Contains(string(diagnostics.Java.Bytes), "initialized") {
			t.Fatalf("Java diagnostics unavailable: %+v %v", diagnostics, err)
		}
		marker := filepath.Join(t.TempDir(), "blocked")
		invokeCtx, stop := context.WithCancel(ctx)
		defer stop()
		invoked := make(chan error, 1)
		go func() {
			_, e := s.Invoke(invokeCtx, "epoch", a[0].ID, a[0].SlotIDs[0], "action", map[string]string{"mode": "block", "marker": marker})
			invoked <- e
		}()
		awaitFile(t, marker)
		stop()
		if err := <-invoked; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		_, usage := l.Snapshot()
		if usage != (HostUsage{1, 1, 512 << 20}) {
			t.Fatal("slot closure released host allocation", usage)
		}
		if _, err := s.Invoke(ctx, "epoch", a[0].ID, a[0].SlotIDs[1], "action", map[string]string{"mode": "output"}); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSlot(ctx, "epoch", a[0].ID, a[0].SlotIDs[1]); err != nil {
			t.Fatal(err)
		}
		awaitUsage(t, l, HostUsage{})
		if h.diagnosticDirectory != "" {
			if _, err := os.Stat(h.diagnosticDirectory); !os.IsNotExist(err) {
				t.Fatalf("JFR repository retained after confirmed cleanup: %v", err)
			}
		}
		h.captureOut.mu.Lock()
		captured, dropped := len(h.captureOut.bytes), h.captureOut.dropped
		h.captureOut.mu.Unlock()
		if captured != 256<<10 || dropped == 0 {
			t.Fatalf("native output capture %d dropped %d", captured, dropped)
		}
		hosts, _ := l.Snapshot()
		if hosts[0].FailureCode != "" {
			t.Fatal("normal drain reported as failure", hosts[0].FailureCode)
		}
	})
	t.Run("lease_loss_ignoring_interrupt", func(t *testing.T) {
		s, l, a, contexts, _ := setup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Prepare(ctx, "epoch", a[0].ReservationID, contexts); err != nil {
			t.Fatal(err)
		}
		if err := s.Commit(ctx, "epoch", a[0].ReservationID); err != nil {
			t.Fatal(err)
		}
		slot := a[0].SlotIDs[0]
		if _, err := s.Invoke(ctx, "epoch", a[0].ID, slot, "init", nil); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(t.TempDir(), "ignoring")
		done := make(chan error, 1)
		go func() {
			_, err := s.Invoke(ctx, "epoch", a[0].ID, slot, "action", map[string]string{"mode": "ignore", "marker": marker})
			done <- err
		}()
		awaitFile(t, marker)
		if err := l.Renew("epoch", 75*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("lost lease returned success")
			}
		case <-time.After(time.Second):
			t.Fatal("lease enforcement waited for interrupt grace")
		}
		awaitUsage(t, l, HostUsage{})
	})
	t.Run("failed_atomic_start_quarantines", func(t *testing.T) {
		// No target is started; the sentinel models an unprovable failed create. The
		// backend must retain it even if a subsequent cleanup call returns nil.
		l, a, contexts := supervisorLedger(t, 1, time.Minute)
		b, _ := NewLocalHostBackend(func(ctx context.Context, a HostAllocation) (PooledLaunchSpec, error) {
			spec, e := launch(ctx, a)
			spec.VMParameters = []string{"-Xmx32"}
			return spec, e
		})
		b.runtimeSupported = func(ResolvedJDK) bool { return true }
		b.credentialCheck = func() error { return nil }
		b.supports = func() bool { return true }
		b.start = func(*exec.Cmd) error { return ErrProcessContainmentFailed }
		b.kill = func(*exec.Cmd) error { return nil }
		s, _ := NewHostSupervisor(l, b, true)
		if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); !errors.Is(err, ErrProcessContainmentFailed) {
			t.Fatal(err)
		}
		h := s.runtime(a[0].ID).(*localManagedHost)
		if h.diagnosticDirectory != "" {
			t.Cleanup(func() { _ = os.RemoveAll(h.diagnosticDirectory) })
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); !errors.Is(err, ErrProcessContainmentFailed) {
			t.Fatal(err)
		}
		_, usage := l.Snapshot()
		if usage.Hosts != 1 {
			t.Fatal("failed start released quarantine", usage)
		}
		if h.diagnosticDirectory != "" {
			if _, err := os.Stat(h.diagnosticDirectory); err != nil {
				t.Fatal("uncertain boundary lost repository", err)
			}
		}
	})
}
func awaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(fmt.Sprintf("fixture did not start: %s", filepath.Base(path)))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const supervisedActions = `
import lrapi.lr;
public final class SupervisedActions {
 static {System.out.print("initialized");}
 public int init(){return 0;}
 public int action() throws Exception {
  String mode=lr.eval_string("{mode}");
  if(mode.equals("rawstdin")){if(new java.io.FileInputStream(java.io.FileDescriptor.in).read()!=-1)throw new AssertionError("control on fd 0");return 0;}
  if(mode.equals("output")){byte[] bytes=new byte[400000];new java.io.FileOutputStream(java.io.FileDescriptor.out).write(bytes);return 0;}
  java.nio.file.Files.write(java.nio.file.Paths.get(lr.eval_string("{marker}")),new byte[]{1});
  if(mode.equals("block"))Thread.sleep(30000);
  if(mode.equals("ignore"))for(;;){try{Thread.sleep(30000);}catch(InterruptedException ignored){}}
  return 0;
 }
 public int end(){return 0;}
}`

func TestLocalHostBackendRefusesAmbientCredentialsBeforeProbe(t *testing.T) {
	b, _ := NewLocalHostBackend(func(context.Context, HostAllocation) (PooledLaunchSpec, error) {
		t.Fatal("command built with ambient credentials")
		return PooledLaunchSpec{}, nil
	})
	b.credentialCheck = func() error { return errors.New("credential value must not escape") }
	b.supports = func() bool { t.Fatal("containment probe ran before credential check"); return true }
	h, err := b.Provision(context.Background(), HostAllocation{ID: "host"})
	if h != nil || !errors.Is(err, ErrHostNotStarted) || strings.Contains(err.Error(), "credential value") {
		t.Fatal(h, err)
	}
}

// A runner sidecar always holds its loopback token; the sidecar's own guard
// accepts it, so the backend must run that guard rather than the generator's.
func TestLocalHostBackendRunsTheSuppliedCredentialCheck(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN", "runner-loopback-token")
	b, _ := NewLocalHostBackend(func(context.Context, HostAllocation) (PooledLaunchSpec, error) {
		return PooledLaunchSpec{}, errors.New("launch reached")
	})
	b.supports = func() bool { return true }
	if _, err := b.Provision(context.Background(), HostAllocation{ID: "host"}); err == nil || strings.Contains(err.Error(), "launch reached") {
		t.Fatalf("the generator guard accepted the runner token: %v", err)
	}
	checked := false
	b = b.WithCredentialCheck(func() error { checked = true; return nil })
	if _, err := b.Provision(context.Background(), HostAllocation{ID: "host"}); !checked || err == nil || !strings.Contains(err.Error(), "launch reached") {
		t.Fatalf("supplied check not used: checked=%v %v", checked, err)
	}
}

func TestLocalHostBackendDoesNotMisclassifyUncertainQualification(t *testing.T) {
	b, _ := NewLocalHostBackend(func(context.Context, HostAllocation) (PooledLaunchSpec, error) {
		return PooledLaunchSpec{}, ErrProcessContainmentFailed
	})
	b.credentialCheck = func() error { return nil }
	b.supports = func() bool { return true }
	h, err := b.Provision(context.Background(), HostAllocation{ID: "host"})
	if h != nil || !errors.Is(err, ErrProcessContainmentFailed) || errors.Is(err, ErrHostNotStarted) {
		t.Fatal("uncertainty reported as never started", err)
	}
}

package javarunner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativePooledFrameBounds(t *testing.T) {
	for _, n := range []int{1, 65536, 1 << 20, 2 << 20} {
		payload := bytes.Repeat([]byte("x"), n)
		frame := PooledFrame{Generation: "host", Slot: "slot", Request: "1", Sequence: 1, Operation: "native_reply", Fields: pooledNativeFields(1, payload)}
		encoded, err := EncodePooledFrame(frame, make([]byte, 32), false)
		if err != nil {
			t.Fatal(n, err)
		}
		decoded, err := ReadPooledFrame(bytes.NewReader(encoded), make([]byte, 32), false)
		if err != nil {
			t.Fatal(n, err)
		}
		seq, got, err := pooledNativePayload(decoded.Fields)
		if err != nil || seq != 1 || !bytes.Equal(got, payload) {
			t.Fatal("payload changed", n, err)
		}
		if n > 1<<20 {
			frame.Operation = "invoke"
			if _, err = EncodePooledFrame(frame, make([]byte, 32), false); err == nil {
				t.Fatal("ordinary frame budget expanded")
			}
		}
	}
}
func TestNativePooledOldWorkerRejectsBeforeInvocation(t *testing.T) {
	in, out := io.Pipe()
	peer, write := io.Pipe()
	defer out.Close()
	defer peer.Close()
	transport, err := NewPooledTransport(in, write, "host", make([]byte, 32), func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx := WithNativeDataCallback(t.Context(), func(context.Context, uint64, []byte) ([]byte, error) { t.Error("unexpected callback"); return nil, nil })
	if _, err = transport.Invoke(ctx, "slot", "init", nil); !errors.Is(err, ErrPooledHostReaped) {
		t.Fatal(err)
	}
	transport.mu.Lock()
	sequence := transport.sequence
	pending := len(transport.pending)
	transport.mu.Unlock()
	if sequence != 0 || pending != 0 {
		t.Fatal("old worker received native invocation")
	}
}

func TestNativePooledPrematureFinalReapsAndCancelsCallback(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	transport, err := NewPooledTransport(local, local, "host", make([]byte, 32), func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	transport.mu.Lock()
	transport.nativeCapable = true
	transport.mu.Unlock()
	entered, cancelled := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ctx = WithNativeDataCallback(ctx, func(ctx context.Context, _ uint64, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	result := make(chan error, 1)
	go func() { _, e := transport.Invoke(ctx, "slot", "init", nil); result <- e }()
	request, err := ReadPooledFrame(peer, make([]byte, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	callback := request
	callback.Operation, callback.Fields = "native_call", pooledNativeFields(1, []byte(`{}`))
	encoded, err := EncodePooledFrame(callback, make([]byte, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = peer.Write(encoded); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("callback did not begin")
	}
	request.Fields = map[string]string{"code": "ok", "reap": "false", "return_code": "0", "event_fallback": "false", "event_truncated": "false"}
	encoded, err = EncodePooledFrame(request, make([]byte, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = peer.Write(encoded)
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("method completed with an outstanding native callback")
		}
	case <-ctx.Done():
		t.Fatal("invalid final did not retire host promptly")
	}
	select {
	case <-cancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("retiring host retained callback work")
	}
}

// Real pooled JVM, SDK and authenticated IPC. The fixture's process hooks are
// only a component test and do not qualify this host's descendant containment.
func TestNativePooledRealJVMCallbacks(t *testing.T) {
	java, classes := nativeJavaClasses(t, nativeSDKActions)
	jdk, err := ResolveJDK(t.Context(), ResolveOptions{ExplicitJavaCommand: java})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	apiJar := filepath.Join(dir, "api.jar")
	shimJar := filepath.Join(dir, "shim.jar")
	scriptJar := filepath.Join(dir, "script.jar")
	shim := filepath.Join(dir, "shim")
	if err = os.Mkdir(shim, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		if out, e := exec.CommandContext(ctx, name, args...).CombinedOutput(); e != nil {
			t.Fatalf("%s: %v %s", name, e, out)
		}
	}
	run("javac", "--release", "8", "-cp", classes, "-d", shim, "../../runners/java_vuser/lrapi/lr.java", "../../runners/java_vuser/lrapi/SlotBridge.java")
	run("jar", "--create", "--file", apiJar, "-C", classes, "apexload")
	run("jar", "--create", "--file", shimJar, "-C", shim, ".")
	run("jar", "--create", "--file", scriptJar, "-C", classes, "Actions.class")
	ledger, err := NewHostLedger(HostBinding{"owner", "epoch", "run", "group", strings.Repeat("a", 64)}, HostBudget{1, 2, 512 << 20}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	allocations, err := ledger.Reserve("epoch", 2, HostProfile{2, 512 << 20}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	allocation := allocations[0]
	backend, err := NewLocalHostBackend(func(_ context.Context, a HostAllocation) (PooledLaunchSpec, error) {
		return PooledLaunchSpec{ConfigurationSHA256: a.Binding.ConfigurationSHA256, JDK: jdk, HarnessJar: apiJar, ShimJar: shimJar, ScriptClasspath: []string{scriptJar}, VMParameters: []string{"-Xmx128m"}, TargetClass: "Actions", WorkDir: dir, QualifiedCleanup: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.runtimeSupported = func(ResolvedJDK) bool { return true }
	backend.credentialCheck = func() error { return nil }
	backend.supports = func() bool { return true }
	backend.start = func(cmd *exec.Cmd) error { return cmd.Start() }
	backend.kill = func(cmd *exec.Cmd) error {
		e := cmd.Process.Kill()
		if errors.Is(e, os.ErrProcessDone) {
			return nil
		}
		return e
	}
	supervisor, err := NewHostSupervisor(ledger, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSupervisor(t, supervisor)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	contexts := map[string]map[string]string{}
	for _, slot := range allocation.SlotIDs {
		contexts[slot] = map[string]string{}
	}
	if err = supervisor.Prepare(ctx, "epoch", allocation.ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	if !supervisor.NativeDataAvailable("epoch", allocation.ReservationID) {
		t.Fatal("actual pooled SDK readiness was lost")
	}
	if err = supervisor.Commit(ctx, "epoch", allocation.ReservationID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, slot := range allocation.SlotIDs {
		for _, method := range []string{"init", "action", "end"} {
			callCtx := WithNativeDataCallback(ctx, func(_ context.Context, seq uint64, payload []byte) ([]byte, error) {
				calls++
				if strings.Contains(string(payload), `"operation":"push"`) {
					if seq != 1 || !bytes.Contains(payload, []byte("{{secret.literal}}雪")) {
						t.Error("literal or sequence changed")
					}
					return []byte(`{"result":{"status":"ok"}}`), nil
				}
				if strings.Contains(string(payload), `"operation":"lease"`) {
					return []byte(`{"result":{"status":"ok","lease_ref":"leased","row":{"id":"row","version":"1","values":{"id":"雪","empty":""}}}}`), nil
				}
				return []byte(`{"result":{"status":"ok"}}`), nil
			})
			if _, err = supervisor.Invoke(callCtx, "epoch", allocation.ID, slot, method, nil); err != nil {
				t.Fatalf("%s: %v", method, err)
			}
		}
	}
	if calls != 6 {
		t.Fatal("callback count", calls)
	}
}

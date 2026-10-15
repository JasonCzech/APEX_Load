package java_vuser_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func testPooledHostProof(t *testing.T, connection net.Conn, generation string, secret []byte) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer connection.SetReadDeadline(time.Time{})
	proof := make([]byte, sha256.Size)
	if _, err := io.ReadFull(connection, proof); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("apexload.java_vuser.ipc.v3.host_auth:" + generation))
	if !hmac.Equal(proof, mac.Sum(nil)) {
		t.Fatal("JVM did not authenticate its control socket")
	}
}

// The normal encoder rejects this frame. Construct a complete authenticated
// ordinary invocation to verify the JVM still enforces its 1 MiB control limit
// after admitting the larger envelope needed by native callbacks.
func testOversizedPooledControlFrame(secret []byte) []byte {
	var payload bytes.Buffer
	writeString := func(value string) {
		_ = binary.Write(&payload, binary.BigEndian, uint32(len(value)))
		payload.WriteString(value)
	}
	for _, value := range []string{javarunner.PooledIPCContract, "gen-1", "a", "1", "invoke"} {
		writeString(value)
	}
	_ = binary.Write(&payload, binary.BigEndian, uint64(1))
	_ = binary.Write(&payload, binary.BigEndian, uint32(18))
	writeString("method")
	writeString("action")
	for i := 0; i < 17; i++ {
		writeString("p.value" + strconv.Itoa(i))
		writeString(strings.Repeat("x", 65536))
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload.Bytes())
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.BigEndian, uint32(payload.Len()+sha256.Size))
	wire.Write(payload.Bytes())
	wire.Write(mac.Sum(nil))
	return wire.Bytes()
}

// These are real-JVM wire/component tests, not delegated containment evidence.
func TestPooledIPCRealJVM(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	dir := t.TempDir()
	api, shim, script := filepath.Join(dir, "api"), filepath.Join(dir, "shim"), filepath.Join(dir, "script")
	for _, p := range []string{api, shim, script} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		if out, e := exec.Command(javac, args...).CombinedOutput(); e != nil {
			t.Fatalf("javac: %v %s", e, out)
		}
	}
	run("--release", "8", "-d", api, "apexload/bridge/SlotRuntime.java", "apexload/host/SlotLoader.java", "apexload/host/SlotWorker.java", "apexload/host/PooledHost.java", "apexload/host/PooledWire.java", "apexload/host/PooledMain.java", "apexload/host/GCPauses.java")
	run("--release", "8", "-cp", api, "-d", shim, "lrapi/lr.java", "lrapi/SlotBridge.java")
	source := filepath.Join(dir, "WireActions.java")
	if err := os.WriteFile(source, []byte(pooledWireActions), 0600); err != nil {
		t.Fatal(err)
	}
	run("--release", "8", "-cp", shim, "-d", script, source)
	listen := func(t *testing.T) (*net.TCPListener, string) {
		t.Helper()
		listener, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = listener.Close() })
		_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
		return listener, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	}
	for _, fault := range []string{"signature", "direction", "oversize", "oversize_control", "replay", "generation"} {
		t.Run("reject_"+fault, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listener, port := listen(t)
			cmd := exec.CommandContext(ctx, java, "-XX:+IgnoreUnrecognizedVMOptions", "-XX:FlightRecorderOptions=repository="+t.TempDir(), "-cp", api, "apexload.host.PooledMain", "gen-1", "2", shim, "WireActions", "true", port, "1", script, "0")
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			output, e := cmd.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			secret := make([]byte, 32)
			if _, e = output.Write(secret); e != nil {
				t.Fatal(e)
			}
			if e = output.Close(); e != nil {
				t.Fatal(e)
			}
			input, e := listener.AcceptTCP()
			if e != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal(e)
			}
			defer input.Close()
			testPooledHostProof(t, input, "gen-1", secret)
			frame := javarunner.PooledFrame{Generation: "gen-1", Request: "1", Operation: "ready", Sequence: 1}
			if fault == "generation" {
				frame.Generation = "old"
			}
			wire, e := javarunner.EncodePooledFrame(frame, secret, fault == "direction")
			if e != nil {
				t.Fatal(e)
			}
			if fault == "signature" {
				wire[len(wire)-1] ^= 1
			}
			if fault == "oversize" {
				// The length-only rejection must exceed the largest envelope,
				// before an authenticated operation is available to narrow it.
				wire = make([]byte, 4)
				binary.BigEndian.PutUint32(wire, (3<<20)+8192+1)
			}
			if fault == "oversize_control" {
				wire = testOversizedPooledControlFrame(secret)
				if _, e = javarunner.ReadPooledFrame(bytes.NewReader(wire), secret, false); !errors.Is(e, javarunner.ErrPooledFrame) {
					t.Fatalf("Go admitted oversized ordinary frame: %v", e)
				}
			}
			if _, e = io.Copy(input, bytes.NewReader(wire)); e != nil {
				t.Fatal(e)
			}
			if fault == "replay" {
				if _, e = javarunner.ReadPooledFrame(input, secret, true); e != nil {
					t.Fatal(e)
				}
				if _, e = input.Write(wire); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = javarunner.ReadPooledFrame(input, secret, true); e == nil {
				t.Fatal("Java accepted invalid frame")
			}
			if e = cmd.Wait(); e == nil {
				t.Fatal("invalid frame did not terminate JVM")
			}
			if ctx.Err() != nil {
				t.Fatal("protocol rejection hung")
			}
		})
	}
	startSized := func(t *testing.T, cleanup bool, slots int) *javarunner.PooledTransport {
		t.Helper()
		qualified := "false"
		if cleanup {
			qualified = "true"
		}
		listener, port := listen(t)
		cmd := exec.Command(java, "-XX:+IgnoreUnrecognizedVMOptions", "-XX:FlightRecorderOptions=repository="+t.TempDir(), "-cp", api, "apexload.host.PooledMain", "gen-1", strconv.Itoa(slots), shim, "WireActions", qualified, port, "1", script, "0")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		output, e := cmd.StdinPipe()
		if e != nil {
			t.Fatal(e)
		}
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		secret := make([]byte, 32)
		if _, e = output.Write(secret); e != nil {
			t.Fatal(e)
		}
		if e = output.Close(); e != nil {
			t.Fatal(e)
		}
		input, e := listener.AcceptTCP()
		if e != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal(e)
		}
		testPooledHostProof(t, input, "gen-1", secret)
		transport, e := javarunner.NewPooledTransportForSlots(input, input, "gen-1", secret, slots, func(error) { _ = cmd.Process.Kill() })
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { transport.Close(); _ = cmd.Wait() })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e = transport.Call(ctx, "", "ready", nil); e != nil {
			t.Fatal(e)
		}
		return transport
	}
	start := func(t *testing.T, cleanup bool) *javarunner.PooledTransport { return startSized(t, cleanup, 2) }
	call := func(t *testing.T, tr *javarunner.PooledTransport, slot, op string, fields map[string]string) javarunner.PooledFrame {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		f, e := tr.Call(ctx, slot, op, fields)
		if e != nil {
			t.Fatal(e)
		}
		return f
	}
	invoke := func(t *testing.T, tr *javarunner.PooledTransport, slot, method string, params map[string]string) javarunner.PooledFrame {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		f, e := tr.Invoke(ctx, slot, method, params)
		if e != nil {
			t.Fatal(e)
		}
		return f
	}
	prepare := func(t *testing.T, tr *javarunner.PooledTransport) {
		call(t, tr, "a", "open", map[string]string{"APEXLOAD_VUSER_ID": "17"})
		call(t, tr, "b", "open", map[string]string{"APEXLOAD_VUSER_ID": "18"})
		call(t, tr, "", "commit", nil)
		invoke(t, tr, "a", "init", nil)
		invoke(t, tr, "b", "init", nil)
	}
	t.Run("barrier_and_abort", func(t *testing.T) {
		tr := start(t, true)
		call(t, tr, "a", "open", nil)
		f := call(t, tr, "", "health", nil)
		if f.Fields["diagnostics_base64"] != "" {
			t.Fatal("user initialization before commit")
		}
		call(t, tr, "", "commit", nil)
		f = call(t, tr, "", "health", nil)
		if f.Fields["diagnostics_base64"] != "" {
			t.Fatal("commit ran user initialization")
		}
		call(t, tr, "a", "close", nil)
	})
	t.Run("precommit_invocation_rejected", func(t *testing.T) {
		tr := start(t, true)
		call(t, tr, "a", "open", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := tr.Invoke(ctx, "a", "init", nil); e == nil || e.Error() != "pooled_operation_rejected" {
			t.Fatal("precommit invocation", e)
		}
	})
	t.Run("private_context_exit_and_bounded_diagnostics", func(t *testing.T) {
		tr := start(t, true)
		prepare(t, tr)
		for _, slot := range []string{"a", "b"} {
			invoke(t, tr, slot, "action", map[string]string{"mode": "verify"})
		}
		invoke(t, tr, "a", "action", map[string]string{"mode": "rawstdin"})
		invoke(t, tr, "b", "action", map[string]string{"mode": "verify"})
		f := invoke(t, tr, "a", "action", map[string]string{"mode": "exit"})
		if f.Fields["exit_option"] != "1" || f.Fields["exit_status"] != "-17" {
			t.Fatal(f.Fields)
		}
		invoke(t, tr, "a", "action", map[string]string{"mode": "output"})
		f = call(t, tr, "", "health", nil)
		if len(f.Fields["diagnostics_base64"]) > 43692 || f.Fields["diagnostics_dropped"] == "0" {
			t.Fatal("capture unbounded", f.Fields["diagnostics_dropped"])
		}
		call(t, tr, "a", "close", nil)
		invoke(t, tr, "b", "action", map[string]string{"mode": "verify"})
		call(t, tr, "b", "close", nil)
	})
	for _, mode := range []string{"block", "ignore"} {
		t.Run("cancellation_"+mode, func(t *testing.T) {
			tr := start(t, true)
			prepare(t, tr)
			marker := filepath.Join(t.TempDir(), "started")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, e := tr.Invoke(ctx, "a", "action", map[string]string{"mode": mode, "marker": marker})
				done <- e
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, e := os.Stat(marker); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("action never started")
				}
				time.Sleep(5 * time.Millisecond)
			}
			// A busy slot cannot starve health/control or an independent peer.
			call(t, tr, "", "health", nil)
			invoke(t, tr, "b", "action", map[string]string{"mode": "verify"})
			cancel()
			select {
			case e := <-done:
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("cancellation hung")
			}
			if mode == "block" {
				invoke(t, tr, "b", "action", map[string]string{"mode": "verify"})
			} else {
				select {
				case <-tr.Done():
				case <-time.After(time.Second):
					t.Fatal("ignored interrupt did not reap host")
				}
			}
		})
	}
	// Event frames precede each result. Without TCP_NODELAY a result that
	// follows an unacknowledged event frame waits for a delayed ACK (about
	// 40 ms) whenever the host is otherwise idle. Linux loopback shows it (see
	// scripts/java-shared-jvm-bench); macOS acknowledges promptly and does not.
	t.Run("idle_round_trips_are_not_delayed", func(t *testing.T) {
		tr := start(t, true)
		prepare(t, tr)
		var rounds []time.Duration
		for range 30 {
			time.Sleep(5 * time.Millisecond)
			begin := time.Now()
			invoke(t, tr, "a", "action", map[string]string{"mode": "transaction"})
			rounds = append(rounds, time.Since(begin))
		}
		slices.Sort(rounds)
		if median := rounds[len(rounds)/2]; median > 20*time.Millisecond {
			t.Fatalf("idle invocation median %s, rounds %v", median, rounds)
		}
	})
	t.Run("full_host_burst", func(t *testing.T) {
		// Every slot of a maximum-size host opens, initializes, acts and closes
		// concurrently. Synchronized completions must queue on both sides of the
		// authenticated socket instead of overflowing into a whole-host reap.
		slots := javarunner.MaxPooledSlotsPerHost
		tr := startSized(t, true, slots)
		for i := 0; i < slots; i++ {
			call(t, tr, "s"+strconv.Itoa(i), "open", map[string]string{"APEXLOAD_VUSER_ID": strconv.Itoa(i)})
		}
		call(t, tr, "", "commit", nil)
		for _, method := range []string{"init", "action"} {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			errs := make(chan error, slots)
			for i := 0; i < slots; i++ {
				go func(slot string) { _, e := tr.Invoke(ctx, slot, method, nil); errs <- e }("s" + strconv.Itoa(i))
			}
			for i := 0; i < slots; i++ {
				if e := <-errs; e != nil {
					cancel()
					t.Fatalf("%s burst: %v", method, e)
				}
			}
			cancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		errs := make(chan error, slots)
		for i := 0; i < slots; i++ {
			go func(slot string) { _, e := tr.Call(ctx, slot, "close", nil); errs <- e }("s" + strconv.Itoa(i))
		}
		for i := 0; i < slots; i++ {
			if e := <-errs; e != nil {
				t.Fatalf("close burst: %v", e)
			}
		}
		select {
		case <-tr.Done():
			t.Fatal("burst retired the host")
		default:
		}
	})
	t.Run("unqualified_cleanup_reaps_peers", func(t *testing.T) {
		tr := start(t, false)
		prepare(t, tr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := tr.Call(ctx, "a", "close", nil); e == nil || e.Error() != "pooled_cleanup_uncertain" {
			t.Fatal(e)
		}
		select {
		case <-tr.Done():
		case <-time.After(time.Second):
			t.Fatal("unqualified cleanup retained peer")
		}
	})
	t.Run("preserve_triggering_failure", func(t *testing.T) {
		tr := start(t, true)
		prepare(t, tr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		marker := filepath.Join(t.TempDir(), "blocked-peer")
		peer := make(chan error, 1)
		go func() {
			_, e := tr.Invoke(ctx, "b", "action", map[string]string{"mode": "block", "marker": marker})
			peer <- e
		}()
		for {
			if _, e := os.Stat(marker); e == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("peer did not start")
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
		if _, e := tr.Invoke(ctx, "a", "action", map[string]string{"mode": "fail"}); e == nil || e.Error() != "pooled_invocation_failed" {
			t.Fatal(e)
		}
		select {
		case <-tr.Done():
		case <-ctx.Done():
			t.Fatal("failed host not reaped")
		}
		if e := <-peer; !errors.Is(e, javarunner.ErrPooledHostReaped) {
			t.Fatalf("peer lost host-reaped classification: %v", e)
		}
		if _, e := tr.Invoke(ctx, "b", "action", nil); !errors.Is(e, javarunner.ErrPooledHostReaped) {
			t.Fatal(e)
		}
	})
}

const pooledWireActions = `
import lrapi.lr;
public class WireActions {
  static {System.out.print("initialized");}
  private static int count;
  private final int identity=lr.get_vuser_id();
  private final Thread owner=Thread.currentThread();
  public int init() throws Exception {if(++count!=1||System.in.read()!=-1)throw new AssertionError("isolation");return 0;}
  public int action() throws Exception {
    if(Thread.currentThread()!=owner||lr.get_vuser_id()!=identity||count!=1)throw new AssertionError("context");
    String mode=lr.eval_string("{mode}");
    if(mode.equals("exit"))lr.exit(1,-17);
    if(mode.equals("transaction")){lr.start_transaction("t");lr.end_transaction("t",lr.PASS);}
    if(mode.equals("fail"))throw new IllegalStateException("private failure details");
    if(mode.equals("rawstdin")&&new java.io.FileInputStream(java.io.FileDescriptor.in).read()!=-1)throw new AssertionError("control leaked to raw stdin");
    if(mode.equals("output")){byte[] b=new byte[100000];System.out.write(b);System.err.write(b);new java.io.FileOutputStream(java.io.FileDescriptor.out).write(b);new java.io.FileOutputStream(java.io.FileDescriptor.err).write(b);}
    if(mode.equals("block")||mode.equals("ignore")){
      java.nio.file.Files.write(java.nio.file.Paths.get(lr.eval_string("{marker}")),new byte[]{1});
      if(mode.equals("block"))Thread.sleep(30000);
      else for(;;){try{Thread.sleep(30000);}catch(InterruptedException ignored){}}
    }
    return 0;
  }
  public int end(){return 0;}
}
`

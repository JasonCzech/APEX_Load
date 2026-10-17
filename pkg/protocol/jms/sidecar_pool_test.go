package jms

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const sidecarPersistentHelperMarker = "--apexload-jms-persistent-helper"

// persistentHelperCommand builds a command line that re-executes this test
// binary as a long-lived NDJSON sidecar. Same trick as the overflow helper:
// it avoids depending on shell utilities being present on the runner.
func persistentHelperCommand(t *testing.T, mode string) string {
	t.Helper()
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(t.TempDir(), "jms-sidecar-persistent-helper")
	if err := os.Symlink(executable, helperPath); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		helperPath,
		"-test.run=^TestJMSSidecarPersistentHelper$",
		"--",
		sidecarPersistentHelperMarker,
		mode,
	}, " ")
}

// TestJMSSidecarPersistentHelper is the sidecar under test, not a real test. It
// speaks the persistent NDJSON contract documented in sidecar_pool.go.
func TestJMSSidecarPersistentHelper(t *testing.T) {
	marker := -1
	for index, arg := range os.Args {
		if arg == sidecarPersistentHelperMarker {
			marker = index
			break
		}
	}
	if marker < 0 {
		return
	}
	if len(os.Args) != marker+2 {
		os.Exit(2)
	}
	mode := os.Args[marker+1]
	token := os.Getpid()

	// "linger" deliberately violates the documented "exit on stdin EOF" contract
	// so terminate() has to burn its full grace period before killing the child.
	// The sleep outlives sidecarCloseGrace; the process is SIGKILLed well before
	// it elapses.
	exitOnEOF := func() {
		if mode == "linger" {
			time.Sleep(5 * time.Second)
		}
		os.Exit(0)
	}

	reader := bufio.NewReader(os.Stdin)
	handled := 0
	for {
		line, err := reader.ReadString('\n')
		if strings.TrimSpace(line) == "" {
			if err != nil {
				exitOnEOF()
			}
			continue
		}
		var req sidecarRequest
		if decodeErr := json.Unmarshal([]byte(strings.TrimSpace(line)), &req); decodeErr != nil {
			os.Exit(3)
		}
		handled++

		if mode == "crash" {
			os.Exit(7)
		}
		// "floodlate" splits one request's stderr flood around its own response
		// line. The part written first is guaranteed to be under the cap when
		// roundTrip checks it; the rest lands afterwards, which is exactly the
		// ordering a real exec copier goroutine produces and an in-memory
		// stand-in cannot.
		if mode == "floodlate" && handled == 1 {
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), floodBeforeResponseBytes))
		}

		resp := sidecarResponse{ID: req.ID}
		switch mode {
		case "desync":
			resp.ID = req.ID + "-stale"
			resp.Message = message{ID: fmt.Sprintf("%d-%d", token, handled)}
		case "reject":
			resp.Error = "broker rejected the request"
		default:
			resp.Message = message{ID: fmt.Sprintf("%d-%d", token, handled)}
		}
		payload, marshalErr := json.Marshal(resp)
		if marshalErr != nil {
			os.Exit(4)
		}
		if _, writeErr := os.Stdout.Write(append(payload, '\n')); writeErr != nil {
			os.Exit(5)
		}
		if mode == "floodlate" && handled == 1 {
			// Hold the tail back long enough that roundTrip has certainly already
			// checked the cap for this request. Without the pause the tail
			// sometimes wins the race and the flood is caught on its own request,
			// which is not the ordering under test.
			time.Sleep(250 * time.Millisecond)
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("y"), floodAfterResponseBytes))
		}
		if err != nil {
			exitOnEOF()
		}
	}
}

// floodBeforeResponseBytes is comfortably under maxSidecarCaptureBytes even
// before the exec pipe drains, so the flooding request itself cannot be failed
// by the cap. floodAfterResponseBytes pushes the request's total past the cap
// once the copier catches up.
const (
	floodBeforeResponseBytes = maxSidecarCaptureBytes - (96 << 10)
	floodAfterResponseBytes  = 128 << 10
)

func newPersistentTestAdapter(t *testing.T, mode string) (sidecarAdapter, *sidecarPool) {
	t.Helper()
	pool := newSidecarPool(persistentHelperCommand(t, mode))
	t.Cleanup(pool.close)
	return sidecarAdapter{
		command: pool.command,
		cfg:     config{Timeout: 30 * time.Second},
		limiter: newSidecarProcessLimiter(4),
		pool:    pool,
	}, pool
}

func splitHelperToken(t *testing.T, messageID string) (pid string, sequence string) {
	t.Helper()
	parts := strings.Split(messageID, "-")
	if len(parts) != 2 {
		t.Fatalf("helper message id = %q, want <pid>-<sequence>", messageID)
	}
	return parts[0], parts[1]
}

// TestJMSSidecarPersistentPoolReusesOneProcess is the core regression for the
// one-process-per-action fork storm: N actions must ride a single OS process.
func TestJMSSidecarPersistentPoolReusesOneProcess(t *testing.T) {
	adapter, pool := newPersistentTestAdapter(t, "echo")

	firstPID := ""
	for attempt := 1; attempt <= 5; attempt++ {
		resp, err := adapter.run(context.Background(), sidecarRequest{
			Operation:   "publish",
			Destination: "queue/orders",
		}, 10*time.Second)
		if err != nil {
			t.Fatalf("request %d returned error: %v", attempt, err)
		}
		pid, sequence := splitHelperToken(t, resp.Message.ID)
		if firstPID == "" {
			firstPID = pid
		}
		if pid != firstPID {
			t.Fatalf("request %d ran on pid %s, want the pooled pid %s", attempt, pid, firstPID)
		}
		if sequence != fmt.Sprint(attempt) {
			t.Fatalf("request %d handled as sidecar request %s, want %d (process was not reused)", attempt, sequence, attempt)
		}
	}
	if got := pool.idleCount(); got != 1 {
		t.Fatalf("pool idle count = %d, want the single reused process parked", got)
	}
}

// A sidecar-reported error is an application verdict, not a transport fault, so
// the process must stay pooled.
func TestJMSSidecarPersistentPoolKeepsProcessAfterSidecarError(t *testing.T) {
	adapter, pool := newPersistentTestAdapter(t, "reject")

	_, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "rejected request") {
		t.Fatalf("error = %v, want a sidecar rejection", err)
	}
	if got := pool.idleCount(); got != 1 {
		t.Fatalf("pool idle count = %d, want 1: an application rejection must not churn the process", got)
	}
}

// A response that does not echo the request id means the stream desynchronised;
// the process must be killed rather than reused for a later action.
func TestJMSSidecarPersistentPoolRejectsCorrelationMismatch(t *testing.T) {
	adapter, pool := newPersistentTestAdapter(t, "desync")

	_, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "correlation id") {
		t.Fatalf("error = %v, want a correlation mismatch rejection", err)
	}
	if got := pool.idleCount(); got != 0 {
		t.Fatalf("pool idle count = %d, want 0: a desynchronised process must not be reused", got)
	}
}

// A sidecar that dies mid-request must surface a transport error and must not
// be handed to the next caller.
func TestJMSSidecarPersistentPoolDiscardsDeadProcess(t *testing.T) {
	adapter, pool := newPersistentTestAdapter(t, "crash")

	_, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "jms sidecar publish") {
		t.Fatalf("error = %v, want a sidecar transport failure", err)
	}
	if got := pool.idleCount(); got != 0 {
		t.Fatalf("pool idle count = %d, want 0 after the sidecar exited", got)
	}
}

// Teardown ended a process under the one-shot protocol; it must keep doing so
// under pooling rather than parking a torn-down session for reuse.
func TestJMSSidecarPersistentPoolRetiresProcessOnTeardown(t *testing.T) {
	adapter, pool := newPersistentTestAdapter(t, "echo")

	first, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second)
	if err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	firstPID, _ := splitHelperToken(t, first.Message.ID)

	if _, err := adapter.run(context.Background(), sidecarRequest{Operation: "teardown"}, 10*time.Second); err != nil {
		t.Fatalf("teardown returned error: %v", err)
	}
	if got := pool.idleCount(); got != 0 {
		t.Fatalf("pool idle count = %d, want 0 after teardown retired the process", got)
	}

	second, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second)
	if err != nil {
		t.Fatalf("publish after teardown returned error: %v", err)
	}
	secondPID, secondSequence := splitHelperToken(t, second.Message.ID)
	if secondPID == firstPID {
		t.Fatalf("publish after teardown reused retired pid %s", firstPID)
	}
	if secondSequence != "1" {
		t.Fatalf("replacement process handled request %s, want 1", secondSequence)
	}
}

// The pool must never outgrow the operator's process budget.
func TestJMSSidecarPersistentPoolStaysWithinProcessLimit(t *testing.T) {
	pool := newSidecarPool(persistentHelperCommand(t, "echo"))
	t.Cleanup(pool.close)
	adapter := sidecarAdapter{
		command: pool.command,
		cfg:     config{Timeout: 30 * time.Second},
		limiter: newSidecarProcessLimiter(2),
		pool:    pool,
	}

	var mu sync.Mutex
	pids := map[string]struct{}{}
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 3; attempt++ {
				resp, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 20*time.Second)
				if err != nil {
					t.Errorf("concurrent request returned error: %v", err)
					return
				}
				pid, _ := splitHelperToken(t, resp.Message.ID)
				mu.Lock()
				pids[pid] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	distinct := len(pids)
	mu.Unlock()
	if distinct == 0 || distinct > 2 {
		t.Fatalf("distinct sidecar processes = %d, want 1..2 for a 2-slot limiter across 18 actions", distinct)
	}
	if got := pool.idleCount(); got > 2 {
		t.Fatalf("pool idle count = %d, want at most the limiter width", got)
	}
}

// close must reap every parked child: no orphans, no zombies.
func TestJMSSidecarPersistentPoolCloseTerminatesParkedProcesses(t *testing.T) {
	pool := newSidecarPool(persistentHelperCommand(t, "echo"))
	adapter := sidecarAdapter{
		command: pool.command,
		cfg:     config{Timeout: 30 * time.Second},
		limiter: newSidecarProcessLimiter(1),
		pool:    pool,
	}
	if _, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}

	pool.mu.Lock()
	if len(pool.idle) != 1 {
		pool.mu.Unlock()
		t.Fatalf("pool idle count = %d, want 1 before close", len(pool.idle))
	}
	parked := pool.idle[0]
	pool.mu.Unlock()

	pool.close()

	if parked.cmd.ProcessState == nil {
		t.Fatal("parked sidecar was not reaped by pool.close")
	}
	if got := pool.idleCount(); got != 0 {
		t.Fatalf("pool idle count = %d after close, want 0", got)
	}
	if _, err := pool.acquire(); err == nil {
		t.Fatal("acquire on a closed pool returned no error")
	}
}

// Idle processes must not be parked forever after a run ends.
func TestJMSSidecarPersistentPoolSweepsIdleProcesses(t *testing.T) {
	pool := newSidecarPool(persistentHelperCommand(t, "echo"))
	t.Cleanup(pool.close)
	pool.idleTTL = time.Millisecond
	adapter := sidecarAdapter{
		command: pool.command,
		cfg:     config{Timeout: 30 * time.Second},
		limiter: newSidecarProcessLimiter(1),
		pool:    pool,
	}
	if _, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}

	pool.sweepIdle(time.Now().Add(time.Minute))
	if got := pool.idleCount(); got != 0 {
		t.Fatalf("pool idle count = %d after sweep, want 0", got)
	}
}

func TestJMSSidecarPersistentModeIsOperatorOwned(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(sidecarPersistentEnv, value)
		if !sidecarPersistentEnabled() {
			t.Fatalf("sidecarPersistentEnabled() = false for %q", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no", "maybe"} {
		t.Setenv(sidecarPersistentEnv, value)
		if sidecarPersistentEnabled() {
			t.Fatalf("sidecarPersistentEnabled() = true for %q", value)
		}
	}
}

// Default (unset) operator environment must keep the one-process-per-action
// transport, so an existing sidecar deployment is untouched.
func TestJMSSidecarDefaultsToOneShotTransport(t *testing.T) {
	t.Setenv(sidecarPersistentEnv, "")
	adapter := sidecarAdapter{command: "/opt/apexload/jms-sidecar"}
	if pool := adapter.persistentPool(); pool != nil {
		t.Fatal("persistentPool() returned a pool with persistent mode disabled")
	}
	t.Setenv(sidecarPersistentEnv, "1")
	pool := adapter.persistentPool()
	if pool == nil {
		t.Fatal("persistentPool() returned nil with persistent mode enabled")
	}
	t.Cleanup(closeAllSidecarPools)
	if same := adapter.persistentPool(); same != pool {
		t.Fatal("persistentPool() did not reuse the registered pool for the same command and config")
	}
}

func TestJMSSidecarPoolKeySeparatesConfigurations(t *testing.T) {
	base := config{BrokerURL: "tcps://a.example:55443", Username: "one"}
	other := config{BrokerURL: "tcps://a.example:55443", Username: "two"}
	if first, second := sidecarPoolKey("/bin/sidecar", base), sidecarPoolKey("/bin/sidecar", base); first != second {
		t.Fatal("pool key is not stable for identical inputs")
	}
	if sidecarPoolKey("/bin/sidecar", base) == sidecarPoolKey("/bin/sidecar", other) {
		t.Fatal("pool key collides across broker configurations")
	}
	if sidecarPoolKey("/bin/sidecar", base) == sidecarPoolKey("/bin/other-sidecar", base) {
		t.Fatal("pool key collides across sidecar commands")
	}
	if strings.Contains(sidecarPoolKey("/bin/sidecar", config{Password: "hunter2"}), "hunter2") {
		t.Fatal("pool key leaked the broker password")
	}
}

// nopWriteCloser stands in for a sidecar's stdin pipe in tests that exercise
// process bookkeeping without forking anything.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(content []byte) (int, error) { return len(content), nil }
func (nopWriteCloser) Close() error                      { return nil }

// blockingWriteCloser stands in for a sidecar's stdin pipe and holds the
// retirement open: kill() closes stdin, so a test can park a process partway
// through being retired and observe what the rest of the code does meanwhile.
type blockingWriteCloser struct {
	entered   chan struct{}
	release   chan struct{}
	closeOnce sync.Once
}

func (b *blockingWriteCloser) Write(content []byte) (int, error) { return len(content), nil }

func (b *blockingWriteCloser) Close() error {
	b.closeOnce.Do(func() { close(b.entered) })
	<-b.release
	return nil
}

// newHermeticSidecarProcess wires a sidecarProcess to in-memory pipes plus a
// responder that emits stderrPerRequest bytes of ordinary sidecar logging before
// answering each request. Writing the log output before the response line is
// what makes the ordering deterministic: roundTrip cannot read its response
// until that request's stderr has landed in the sink, which a real exec copier
// goroutine cannot guarantee.
func newHermeticSidecarProcess(t *testing.T, stderrPerRequest int) *sidecarProcess {
	t.Helper()
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	sink := &sidecarStderrSink{max: maxSidecarCaptureBytes}
	proc := &sidecarProcess{
		cmd:    &exec.Cmd{},
		stdin:  requestWriter,
		stdout: responseReader,
		reader: bufio.NewReaderSize(responseReader, 64<<10),
		stderr: sink,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		requests := bufio.NewReader(requestReader)
		noise := bytes.Repeat([]byte("x"), stderrPerRequest)
		for {
			line, err := requests.ReadString('\n')
			if strings.TrimSpace(line) != "" {
				var req sidecarRequest
				if json.Unmarshal([]byte(strings.TrimSpace(line)), &req) != nil {
					return
				}
				if len(noise) > 0 {
					_, _ = sink.Write(noise)
				}
				payload, marshalErr := json.Marshal(sidecarResponse{ID: req.ID, Message: message{ID: req.ID}})
				if marshalErr != nil {
					return
				}
				if _, writeErr := responseWriter.Write(append(payload, '\n')); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = requestWriter.Close()
		_ = responseWriter.Close()
		_ = responseReader.Close()
		_ = requestReader.Close()
		<-done
	})
	return proc
}

// The stderr cap must be charged per request, the way it was when every action
// forked its own process. Charging it cumulatively over a pooled process's
// lifetime turns ordinary per-action logging into a failed action (and a killed
// process) once the process has served enough requests, even though the broker
// accepted the message and the response decoded correctly.
func TestJMSSidecarPooledStderrCapIsPerRequest(t *testing.T) {
	perRequest := maxSidecarCaptureBytes / 2
	proc := newHermeticSidecarProcess(t, perRequest)

	// Four requests at half the cap each: every request is comfortably inside
	// the cap, but their sum is twice it.
	for attempt := 1; attempt <= 4; attempt++ {
		resp, err := proc.roundTrip(context.Background(), sidecarRequest{Operation: "publish"})
		if err != nil {
			t.Fatalf("request %d returned error: %v (per-action sidecar logging must not accumulate into a failure)", attempt, err)
		}
		if resp.ID == "" {
			t.Fatalf("request %d returned no correlation id", attempt)
		}
		if proc.poisoned.Load() {
			t.Fatalf("request %d retired the pooled process", attempt)
		}
	}
}

// The per-request cap itself must still bite: one action that floods stderr is
// a genuine fault and has to retire the process.
func TestJMSSidecarPooledStderrCapRejectsOneFloodingRequest(t *testing.T) {
	proc := newHermeticSidecarProcess(t, maxSidecarCaptureBytes+1)

	_, err := proc.roundTrip(context.Background(), sidecarRequest{Operation: "publish"})
	if err == nil || !strings.Contains(err.Error(), "stderr exceeded") {
		t.Fatalf("error = %v, want the per-request stderr cap to reject the action", err)
	}
	if !proc.poisoned.Load() {
		t.Fatal("a request that blew the stderr cap must retire the pooled process")
	}
}

// APEXLOAD_JMS_SIDECAR_MAX_PROCESSES is sized against generator memory, so it
// has to bound live sidecars across every pool. Pools are keyed by (command,
// broker configuration), so groups differing only in destination each get their
// own pool; a per-pool cap would multiply the JVM footprint by the number of
// distinct configurations.
func TestJMSSidecarProcessCeilingIsGlobalAcrossPools(t *testing.T) {
	t.Setenv(sidecarPersistentEnv, "1")
	t.Setenv(sidecarMaxProcessesEnv, "2")
	// Start from a clean registry and put it back afterwards so a pool left by
	// another test cannot be mistaken for one of ours.
	closeAllSidecarPools()
	t.Cleanup(closeAllSidecarPools)

	command := persistentHelperCommand(t, "echo")
	limiter := newSidecarProcessLimiter(2)
	pools := map[*sidecarPool]struct{}{}
	var ordered []*sidecarPool
	for index := 0; index < 4; index++ {
		adapter := sidecarAdapter{
			command: command,
			cfg: config{
				Timeout:     30 * time.Second,
				Destination: fmt.Sprintf("queue/orders-%d", index),
			},
			limiter: limiter,
		}
		pool := adapter.persistentPool()
		if pool == nil {
			t.Fatal("persistentPool() returned nil with persistent mode enabled")
		}
		if _, seen := pools[pool]; !seen {
			pools[pool] = struct{}{}
			ordered = append(ordered, pool)
		}
		if _, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second); err != nil {
			t.Fatalf("publish on destination %d returned error: %v", index, err)
		}
	}
	if len(ordered) != 4 {
		t.Fatalf("distinct pools = %d, want 4: the test premise is four differently configured groups", len(ordered))
	}

	parked := 0
	for _, pool := range ordered {
		parked += pool.idleCount()
	}
	// Every request completed, so nothing is checked out: parked == live.
	if parked > 2 {
		t.Fatalf("live sidecar processes = %d across %d pools, want at most the configured ceiling of 2", parked, len(ordered))
	}
}

// waitForLiveSidecars fences a budget test against processes another test left
// running: the global counter has to be back at want before the assertions
// below mean anything.
func waitForLiveSidecars(t *testing.T, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if liveSidecarProcesses.Load() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("live sidecar processes = %d, want %d", liveSidecarProcesses.Load(), want)
}

// The sequential test above cannot see the race that matters: the ceiling used
// to be read and acted on as two separate steps, so concurrent acquires on
// different pools all observed the same free slot and all forked. Production
// sizes the in-flight limiter from the same variable, so up to `ceiling`
// acquires can sit in that window at once and live processes can settle at
// ~2x the number the operator sized generator memory against.
func TestJMSSidecarProcessCeilingHoldsUnderConcurrentAcquires(t *testing.T) {
	const ceiling = 4
	t.Setenv(sidecarPersistentEnv, "1")
	t.Setenv(sidecarMaxProcessesEnv, fmt.Sprint(ceiling))
	closeAllSidecarPools()
	t.Cleanup(closeAllSidecarPools)
	waitForLiveSidecars(t, 0)

	command := persistentHelperCommand(t, "echo")
	limiter := newSidecarProcessLimiter(configuredSidecarMaxProcesses())
	newAdapter := func(index int) sidecarAdapter {
		return sidecarAdapter{
			command: command,
			cfg: config{
				Timeout:     30 * time.Second,
				Destination: fmt.Sprintf("queue/orders-%d", index),
			},
			limiter: limiter,
		}
	}
	publish := func(index int) error {
		_, err := newAdapter(index).run(context.Background(), sidecarRequest{Operation: "publish"}, 10*time.Second)
		return err
	}

	// Warm three differently configured pools: the budget is now one short of
	// full and three processes are parked, so a correct sweep has victims.
	for index := 0; index < ceiling-1; index++ {
		if err := publish(index); err != nil {
			t.Fatalf("warm-up publish on destination %d returned error: %v", index, err)
		}
	}
	if live := liveSidecarProcesses.Load(); live != ceiling-1 {
		t.Fatalf("live sidecar processes after warm-up = %d, want %d", live, ceiling-1)
	}

	var peak atomic.Int64
	sampling := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		for {
			if live := liveSidecarProcesses.Load(); live > peak.Load() {
				peak.Store(live)
			}
			select {
			case <-sampling:
				return
			default:
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()

	// Release four acquires on four further distinct pools at once. Each finds
	// its own idle list empty and takes the fork path.
	release := make(chan struct{})
	var workers sync.WaitGroup
	for index := ceiling - 1; index < 2*ceiling-1; index++ {
		workers.Add(1)
		go func(destination int) {
			defer workers.Done()
			<-release
			if err := publish(destination); err != nil {
				t.Errorf("concurrent publish on destination %d returned error: %v", destination, err)
			}
		}(index)
	}
	close(release)
	workers.Wait()
	close(sampling)
	sampler.Wait()

	if got := peak.Load(); got > ceiling {
		t.Fatalf("peak sidecar processes in service = %d, want at most the configured ceiling of %d", got, ceiling)
	}
	if got := liveSidecarProcesses.Load(); got > ceiling {
		t.Fatalf("sidecar processes in service after the concurrent burst = %d, want at most the configured ceiling of %d", got, ceiling)
	}
	sidecarPoolsMu.Lock()
	pools := make([]*sidecarPool, 0, len(sidecarPools))
	for _, pool := range sidecarPools {
		pools = append(pools, pool)
	}
	sidecarPoolsMu.Unlock()
	if len(pools) != 2*ceiling-1 {
		t.Fatalf("distinct pools = %d, want %d: the test premise is one pool per broker configuration", len(pools), 2*ceiling-1)
	}
	parked := 0
	for _, pool := range pools {
		parked += pool.idleCount()
	}
	// Every request has returned, so nothing is checked out: parked == in service.
	if parked > ceiling {
		t.Fatalf("parked sidecar processes = %d across %d pools, want at most the configured ceiling of %d", parked, len(pools), ceiling)
	}
}

// Making room inside the process budget must not be charged to the VUser action
// that triggered it. A sidecar that ignores the "exit on stdin EOF" contract
// takes the full sidecarCloseGrace to die, and the sweep can have several such
// victims, so terminating them inline turns one publish into a multi-second
// action and corrupts the response time the run is measuring.
func TestJMSSidecarBudgetEvictionDoesNotStallTheActionPath(t *testing.T) {
	t.Setenv(sidecarPersistentEnv, "1")
	t.Setenv(sidecarMaxProcessesEnv, "1")
	closeAllSidecarPools()
	t.Cleanup(closeAllSidecarPools)
	waitForLiveSidecars(t, 0)

	command := persistentHelperCommand(t, "linger")
	limiter := newSidecarProcessLimiter(2)
	publish := func(index int) error {
		adapter := sidecarAdapter{
			command: command,
			cfg: config{
				Timeout:     30 * time.Second,
				Destination: fmt.Sprintf("queue/orders-%d", index),
			},
			limiter: limiter,
		}
		_, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 20*time.Second)
		return err
	}

	// Fill the single-process budget and park the process.
	if err := publish(0); err != nil {
		t.Fatalf("warm-up publish returned error: %v", err)
	}

	// This publish is on a different pool, so it must evict the parked process
	// before it can fork its own.
	start := time.Now()
	if err := publish(1); err != nil {
		t.Fatalf("publish that had to evict for budget returned error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed >= sidecarCloseGrace {
		t.Fatalf("one publish took %s: the budget sweep blocked the action path on a sidecar that ignores the stdin-EOF shutdown contract (grace is %s)", elapsed, sidecarCloseGrace)
	}
	if live := liveSidecarProcesses.Load(); live > 1 {
		t.Fatalf("sidecar processes in service = %d, want at most the configured ceiling of 1", live)
	}
}

// A cancellation that lands after the request already completed must never
// poison the process: by then it has been handed back to the pool and may
// already be serving a different VUser, so poisoning it kills that caller's
// in-flight request and reports a broker failure that never happened.
//
// This is exactly the ordering run() produces: the request finishes, then
// run()'s `defer cancel()` fires, so the watchdog wakes with BOTH the
// completion signal and ctx.Done() ready. Go picks a ready select case at
// random, so a watchdog that only selects between the two channels takes the
// cancellation branch about half the time; over 200 cycles the unfixed code
// poisons an already-completed request with probability 1-2^-200. Nothing here
// yields between arming and completing, so the watchdog goroutine has not
// parked yet — which is the state that makes the choice random.
func TestJMSSidecarWatchdogIgnoresCancellationAfterCompletion(t *testing.T) {
	const attempts = 200
	procs := make([]*sidecarProcess, 0, attempts)
	for attempt := 0; attempt < attempts; attempt++ {
		proc := &sidecarProcess{cmd: &exec.Cmd{}, stdin: nopWriteCloser{}}
		procs = append(procs, proc)
		ctx, cancel := context.WithCancel(context.Background())

		complete := proc.armDeadlineWatchdog(ctx)
		complete()
		cancel()
	}

	countPoisoned := func() int {
		poisoned := 0
		for _, proc := range procs {
			if proc.poisoned.Load() {
				poisoned++
			}
		}
		return poisoned
	}
	// Give every watchdog goroutine time to wake and decide, bailing out early
	// as soon as the defect shows itself.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && countPoisoned() == 0 {
		time.Sleep(time.Millisecond)
	}
	if poisoned := countPoisoned(); poisoned > 0 {
		t.Fatalf("watchdog poisoned %d/%d processes whose request had already completed", poisoned, attempts)
	}
}

// The watchdog must still fire while a request is genuinely outstanding,
// otherwise a blocked read on a persistent pipe never unblocks.
func TestJMSSidecarWatchdogPoisonsOutstandingRequestOnCancel(t *testing.T) {
	proc := &sidecarProcess{cmd: &exec.Cmd{}, stdin: nopWriteCloser{}}
	ctx, cancel := context.WithCancel(context.Background())
	complete := proc.armDeadlineWatchdog(ctx)
	defer complete()

	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for !proc.poisoned.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !proc.poisoned.Load() {
		t.Fatal("watchdog did not poison the process when its request was cancelled mid-flight")
	}
}

// The completion flag alone only orders the two paths when the cancellation
// lands after the request returned. When it lands while the caller is still
// finishing — say between reading the response line and validating it — the
// watchdog sees completed == false and is entitled to poison. It must do so
// before the caller returns, because runPooledSidecar hands the process
// straight to pool.release, which inspects `poisoned` exactly once: a poison
// that lands a moment later kills a process already parked and checked out by
// another VUser, and charges a healthy broker with that VUser's failure.
//
// So the completion callback must not return until the watchdog has finished
// retiring the process. That interlock is what this test drives: stdin.Close is
// held open, which parks the watchdog inside the retirement it has already
// committed to, and complete() must stay blocked for as long as it lasts.
func TestJMSSidecarWatchdogRetiresTheProcessBeforeTheCallerHandsItBack(t *testing.T) {
	stdin := &blockingWriteCloser{entered: make(chan struct{}), release: make(chan struct{})}
	proc := &sidecarProcess{cmd: &exec.Cmd{}, stdin: stdin}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	complete := proc.armDeadlineWatchdog(ctx)
	cancel()
	// The watchdog has claimed this request and is now inside the retirement.
	select {
	case <-stdin.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not retire the process of a request cancelled mid-flight")
	}

	// The caller now finishes, exactly as roundTrip does after its post-read
	// validation. runPooledSidecar hands the process to pool.release the moment
	// this returns, and release inspects it exactly once.
	returned := make(chan struct{})
	go func() {
		complete()
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("the request returned while the watchdog was still retiring its process: the pool would park it as healthy, hand it to another VUser, and only then have it killed under that caller")
	case <-time.After(250 * time.Millisecond):
	}

	close(stdin.release)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("complete() never returned after the watchdog finished retiring the process")
	}
	if !proc.poisoned.Load() {
		t.Fatal("the process was handed back usable even though its request was retired by the watchdog")
	}
}

// The stderr cap is charged per request, but the *verdict* must not be. A
// flooding request's own bytes can still be in flight when roundTrip checks the
// cap, and clearing the verdict at the next request's reset made that flood
// vanish entirely instead of merely being reported late.
func TestJMSSidecarStderrOverflowVerdictSurvivesPerRequestReset(t *testing.T) {
	sink := &sidecarStderrSink{max: 8}

	sink.Write(bytes.Repeat([]byte("x"), 4))
	sink.reset()
	sink.Write(bytes.Repeat([]byte("x"), 4))
	if sink.truncated() {
		t.Fatal("two under-cap requests summed into an overflow: the byte accounting must stay per request")
	}

	sink.Write(bytes.Repeat([]byte("x"), 8))
	if !sink.truncated() {
		t.Fatal("a single request past the cap did not register as an overflow")
	}
	sink.reset()
	if !sink.truncated() {
		t.Fatal("the next request's reset erased an overflow that was never reported on any request")
	}
}

// The hermetic harness writes stderr synchronously before the response line, so
// it proves the cap policy but not the production write ordering: cmd.Stderr is
// not an *os.File, so os/exec interposes a pipe plus a copier goroutine and a
// flooding request's tail can land after roundTrip has already checked the cap.
// This drives a real forked sidecar that splits its flood around its own
// response line, which reproduces that ordering deterministically.
func TestJMSSidecarPooledStderrFloodIsReportedDespiteLateDelivery(t *testing.T) {
	adapter, pool := newPersistentTestAdapter(t, "floodlate")

	// The flooding request cannot be failed by the cap: only part of its stderr
	// has been delivered when the check runs.
	if _, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 20*time.Second); err != nil {
		t.Fatalf("flooding request returned error: %v, want it to pass while its stderr is still in flight", err)
	}
	pool.mu.Lock()
	parked := len(pool.idle)
	var flooder *sidecarProcess
	if parked == 1 {
		flooder = pool.idle[0]
	}
	pool.mu.Unlock()
	if flooder == nil {
		t.Fatalf("pool idle count = %d, want the flooding request's process parked for reuse", parked)
	}

	// Wait for the copier goroutine to deliver the rest of the flood, which is
	// what the next request's reset used to discard.
	deadline := time.Now().Add(10 * time.Second)
	for !flooder.stderr.truncated() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !flooder.stderr.truncated() {
		t.Fatal("the sidecar's stderr flood never reached the sink")
	}

	_, err := adapter.run(context.Background(), sidecarRequest{Operation: "publish"}, 20*time.Second)
	if err == nil || !strings.Contains(err.Error(), "stderr exceeded") {
		t.Fatalf("error = %v, want the stderr flood to be reported rather than erased by the next request's reset", err)
	}
	if got := pool.idleCount(); got != 0 {
		t.Fatalf("pool idle count = %d, want the flooding sidecar retired", got)
	}
}

func TestReadBoundedLineRejectsOversizedRecord(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 128)+"\n"), 16)
	if _, err := readBoundedLine(reader, 32); err == nil {
		t.Fatal("readBoundedLine accepted a record beyond the cap")
	}

	reader = bufio.NewReaderSize(strings.NewReader("{\"id\":\"a\"}\n{\"id\":\"b\"}\n"), 16)
	first, err := readBoundedLine(reader, 1024)
	if err != nil {
		t.Fatalf("readBoundedLine returned error: %v", err)
	}
	if strings.TrimSpace(string(first)) != `{"id":"a"}` {
		t.Fatalf("first record = %q, want the first line only", string(first))
	}
}

package jms

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

// The default Solace sidecar contract is one OS process per messaging action:
// the adapter forks the operator-owned command, writes a single JSON request on
// stdin, reads a single JSON response from stdout, and the process exits. That
// is safe but forks once per action, which does not survive load-test VUser
// counts.
//
// Persistent mode is the pooled alternative. It is opt-in because it changes
// the sidecar wire contract, and only an operator can know whether the deployed
// sidecar binary implements it. Enable it with
// APEXLOAD_JMS_SIDECAR_PERSISTENT=1 alongside APEXLOAD_JMS_SIDECAR_COMMAND.
//
// Persistent wire contract (newline-delimited JSON, one request per line):
//   - The sidecar reads request lines from stdin until stdin reaches EOF, then
//     exits. Exiting on stdin EOF is what makes the sidecar orphan-safe: if the
//     generator dies, the OS closes the pipe and the sidecar terminates.
//   - Every request carries a non-empty "id". The sidecar MUST echo that exact
//     "id" on the corresponding response line. A missing or mismatched id is
//     treated as a desynchronised stream: the process is killed and removed
//     from the pool rather than allowed to answer a later request with an
//     earlier reply.
//   - Exactly one response line per request line, written in order.
//   - Responses that carry "error" are application-level rejections and leave
//     the process healthy and reusable. Anything else (decode failure, id
//     mismatch, oversized output, EOF, timeout) retires the process.
//
// Pool lifecycle:
//
//   - A pool exists per (command, config) pair, so a pooled process only ever
//     sees one broker configuration.
//
//   - Concurrency is bounded by the existing sidecarProcessLimiter, which caps
//     in-flight requests. That alone does not cap live processes, because a
//     process stays alive while parked idle after its slot is released, and
//     each pool parks independently. The live-process ceiling is therefore
//     enforced separately and globally by liveSidecarProcesses /
//     reserveSidecarProcessSlot: a fork must first CLAIM a slot atomically
//     (evicting parked processes from any pool to free one), so concurrent
//     acquires across pools cannot all observe the same free slot and fork past
//     the ceiling.
//
//     What that guarantees precisely: the number of sidecar processes in
//     service — started and not yet retired — never exceeds
//     APEXLOAD_JMS_SIDECAR_MAX_PROCESSES. Two deliberate gaps, both bounded:
//     a retired process gives its slot back when the adapter decides to end it
//     rather than when the child has finished exiting, so a sidecar that
//     ignores the stdin-EOF contract can survive up to sidecarCloseGrace past
//     the ceiling (blocking the VUser action until it exits would corrupt the
//     measured response time instead); and if nothing is parked anywhere the
//     fork proceeds while over budget, because every remaining process is then
//     checked out by an in-flight request — which sidecarProcessLimiter already
//     bounds — and blocking here would deadlock the caller that holds the
//     limiter slot.
//
//   - A process is checked out exclusively for the duration of one request, so
//     requests never interleave on a single pipe.
//
//   - Idle processes are reaped after sidecarIdleTTL, so a finished run does
//     not leave sidecars parked forever.
//
//   - The "teardown" operation retires the process it ran on instead of
//     returning it to the pool, matching the one-shot behaviour where teardown
//     always ended a process.
const (
	sidecarPersistentEnv = "APEXLOAD_JMS_SIDECAR_PERSISTENT"
	sidecarIdleTTL       = 60 * time.Second
	sidecarCloseGrace    = 2 * time.Second
	sidecarReapInterval  = 15 * time.Second
)

var errSidecarLineTooLong = errors.New("jms sidecar response line exceeded the capture limit")

// sidecarPersistentEnabled reports whether the operator opted the sidecar into
// the long-lived pooled protocol. Like the command itself this is deliberately
// operator-owned; scripts cannot reach it.
func sidecarPersistentEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(sidecarPersistentEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// sidecarStderrSink is the concurrency-safe capped stderr buffer used by
// long-lived sidecars. Unlike sidecarCappedBuffer it is read while the exec
// copier goroutine is still writing, so it needs its own lock.
//
// The cap is per request, not per process lifetime: roundTrip resets the sink
// before each request so a sidecar that logs a normal amount per action never
// accumulates its way past the cap. In one-process-per-action mode the cap was
// implicitly per action (a fresh buffer per fork); resetting keeps that
// meaning under pooling instead of turning ordinary logging into a failed
// action once the process has served enough requests.
//
// The overflow verdict, unlike the bytes, is sticky for the life of the
// process. cmd.Stderr is not an *os.File, so os/exec interposes a pipe and a
// copier goroutine: a request that floods stderr and then writes its response
// line can have up to a pipe's worth of its own output still undelivered when
// roundTrip checks the cap. Clearing the verdict as well as the buffer would
// let exactly that flood vanish at the next request's reset and never be
// reported on any request. Keeping it means the fault surfaces on the following
// request instead — late, but never silently dropped. Ordinary per-request
// logging below the cap never sets it, so the per-request accounting above is
// unaffected.
type sidecarStderrSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
	// overflown is the current request's verdict; reset clears it.
	overflown bool
	// sawOverflow latches any overflow for the process lifetime, including one
	// that lands after its own request was already checked. reset does not clear
	// it.
	sawOverflow bool
}

func (s *sidecarStderrSink) Write(content []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remaining := s.max - s.buf.Len(); remaining > 0 {
		if len(content) > remaining {
			_, _ = s.buf.Write(content[:remaining])
			s.markOverflowLocked()
		} else {
			_, _ = s.buf.Write(content)
		}
	} else if len(content) > 0 {
		s.markOverflowLocked()
	}
	return len(content), nil
}

func (s *sidecarStderrSink) markOverflowLocked() {
	s.overflown = true
	s.sawOverflow = true
}

func (s *sidecarStderrSink) truncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overflown || s.sawOverflow
}

// reset clears the captured bytes so the cap applies to the next request only.
// The overflow verdict deliberately survives: see the type comment.
func (s *sidecarStderrSink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.Reset()
	s.overflown = false
}

// sidecarProcess is one long-lived sidecar with its stdin/stdout pipes held
// open across requests.
type sidecarProcess struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	reader    *bufio.Reader
	stderr    *sidecarStderrSink
	seq       atomic.Uint64
	poisoned  atomic.Bool
	closeOnce sync.Once
	idleSince time.Time
	// requestGeneration is the retirement token for the request currently checked
	// out on this process. Whoever moves it past its arming value owns the right
	// to decide that request's fate, which is what keeps a timed-out request's
	// watchdog from poisoning a process that has since been parked and handed to
	// a different VUser. See armDeadlineWatchdog.
	requestGeneration atomic.Uint64
	// budgeted marks a process whose slot in the global live-process ceiling was
	// claimed by reserveSidecarProcessSlot, so only such processes give a slot
	// back when they are retired.
	budgeted bool
	// budgetOnce makes releaseBudget idempotent: the slot is handed back the
	// moment the adapter decides to retire the process, which may be before the
	// terminate/kill that also releases it.
	budgetOnce sync.Once
}

// startSidecarProcess forks one sidecar. The caller must already hold a slot in
// the global live-process budget (reserveSidecarProcessSlot); the returned
// process owns that slot and gives it back exactly once, when it is retired.
func startSidecarProcess(command string) (*sidecarProcess, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, fmt.Errorf("jms sidecar command is not configured")
	}
	cmd := exec.Command(fields[0], fields[1:]...)
	cmd.Env = subprocessguard.Environment(nil)
	cmd.WaitDelay = sidecarCloseGrace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open jms sidecar stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open jms sidecar stdout: %w", err)
	}
	sink := &sidecarStderrSink{max: maxSidecarCaptureBytes}
	cmd.Stderr = sink
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start jms sidecar: %w", err)
	}
	return &sidecarProcess{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		reader:   bufio.NewReaderSize(stdout, 64<<10),
		stderr:   sink,
		budgeted: true,
	}, nil
}

// liveSidecarProcesses counts pooled sidecar processes that are in service —
// claimed by reserveSidecarProcessSlot and not yet retired — across every pool.
// The in-flight request limiter cannot serve as this counter: its slot is
// released when a request finishes while the process stays alive parked in a
// pool, and each (command, config) pair gets its own pool.
var liveSidecarProcesses atomic.Int64

// reserveSidecarProcessSlot claims one slot in the global live-process budget
// for a process that is about to be forked. The claim is atomic with the check,
// so N concurrent acquires across N pools cannot all observe the same free slot
// and fork past the ceiling; when the budget is full it retires parked
// processes from any pool until a slot frees up.
//
// It never blocks. If nothing is parked anywhere it claims the slot regardless
// and returns over budget: every remaining process is then checked out by an
// in-flight request, those are already bounded by sidecarProcessLimiter, and
// waiting here would deadlock the caller, which holds a limiter slot.
func reserveSidecarProcessSlot(ceiling int) {
	if ceiling <= 0 {
		liveSidecarProcesses.Add(1)
		return
	}
	for {
		live := liveSidecarProcesses.Load()
		if live < int64(ceiling) {
			if liveSidecarProcesses.CompareAndSwap(live, live+1) {
				return
			}
			continue
		}
		victim := takeIdleSidecarForEviction()
		if victim == nil {
			liveSidecarProcesses.Add(1)
			return
		}
		retireSidecarProcess(victim)
	}
}

// releaseSidecarProcessSlot returns a slot claimed by reserveSidecarProcessSlot
// that no process ended up owning, i.e. the fork failed.
func releaseSidecarProcessSlot() {
	liveSidecarProcesses.Add(-1)
}

// retireSidecarProcess ends a process the adapter has taken out of service. The
// budget slot is returned synchronously so a caller waiting to fork is not
// blocked behind a child's shutdown grace, while the child itself is closed
// down and reaped in the background.
func retireSidecarProcess(proc *sidecarProcess) {
	if proc == nil {
		return
	}
	proc.releaseBudget()
	go proc.terminate()
}

// releaseBudget drops this process from the global live count exactly once.
func (p *sidecarProcess) releaseBudget() {
	if !p.budgeted {
		return
	}
	p.budgetOnce.Do(releaseSidecarProcessSlot)
}

func (p *sidecarProcess) pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// roundTrip writes one request line and reads the matching response line. Any
// transport-level fault poisons the process so the pool discards it instead of
// handing a desynchronised pipe to the next caller.
func (p *sidecarProcess) roundTrip(ctx context.Context, req sidecarRequest) (sidecarResponse, error) {
	if p.poisoned.Load() {
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s failed: pooled process is no longer usable", req.Operation)
	}
	req.ID = fmt.Sprintf("%d-%d", p.pid(), p.seq.Add(1))
	payload, err := json.Marshal(req)
	if err != nil {
		return sidecarResponse{}, fmt.Errorf("marshal jms sidecar request: %w", err)
	}
	payload = append(payload, '\n')

	// Deadline watchdog: a persistent pipe cannot use exec.CommandContext, so
	// the process is killed on cancellation to unblock the pending read.
	completeRequest := p.armDeadlineWatchdog(ctx)
	defer completeRequest()

	// The stderr cap is per request: anything the sidecar logged for earlier
	// requests must not be charged against this one.
	p.stderr.reset()

	if _, err := p.stdin.Write(payload); err != nil {
		p.poison()
		return sidecarResponse{}, p.wrapFailure(ctx, req.Operation, fmt.Errorf("write request: %w", err))
	}

	line, readErr := readBoundedLine(p.reader, maxSidecarCaptureBytes)
	switch {
	case readErr == nil:
	case errors.Is(readErr, errSidecarLineTooLong):
		p.poison()
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s stdout exceeded %d bytes", req.Operation, maxSidecarCaptureBytes)
	case errors.Is(readErr, io.EOF) && len(bytes.TrimSpace(line)) > 0:
		// A final unterminated response is still usable, but the stream is
		// finished so the process cannot serve another request.
		p.poison()
	default:
		p.poison()
		return sidecarResponse{}, p.wrapFailure(ctx, req.Operation, readErr)
	}

	if p.stderr.truncated() {
		p.poison()
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s stderr exceeded %d bytes", req.Operation, maxSidecarCaptureBytes)
	}
	if len(bytes.TrimSpace(line)) == 0 {
		p.poison()
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s returned an empty response", req.Operation)
	}

	var resp sidecarResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		p.poison()
		return sidecarResponse{}, fmt.Errorf("decode jms sidecar %s response: %w", req.Operation, err)
	}
	if resp.ID != req.ID {
		p.poison()
		return sidecarResponse{}, fmt.Errorf(
			"jms sidecar %s response correlation id %q does not match request id %q", req.Operation, resp.ID, req.ID)
	}
	if strings.TrimSpace(resp.Error) != "" {
		// Application-level rejection: the transport is still healthy, so the
		// process stays in the pool.
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s rejected request: %s", req.Operation, resp.Error)
	}
	return resp, nil
}

// armDeadlineWatchdog starts the goroutine that poisons p when ctx is cancelled
// while a request is still outstanding. The returned function marks the request
// complete and must be called exactly once, on every return path.
//
// Completion is recorded in an atomic flag rather than only by closing a
// channel, because the caller's context is cancelled on every return (run()'s
// `defer cancel()`), so the watchdog routinely wakes with BOTH the completion
// signal and ctx.Done() ready. Go picks a ready select case at random, so a
// channel-only watchdog can take the cancellation branch for a request that
// already succeeded — killing a process that has been handed back to the pool
// and may already be serving a different caller. Checking the flag makes
// completion authoritative: once the request has returned, no cancellation can
// poison the process on its behalf.
//
// The flag alone is not enough, because it only orders the two paths when the
// cancellation lands after the request returned. A cancellation that lands
// while the caller is still finishing (say between reading the response line
// and validating it) sees completed == false, and without further interlocking
// the poison could land after the process had been parked and checked out by
// another VUser — killing that caller's request and charging a healthy broker
// with a failure. So completion and cancellation instead race to claim the
// request via requestGeneration, and exactly one wins:
//   - the caller wins: the watchdog never poisons this request's process;
//   - the watchdog wins: the caller does not return until poisoning is done, so
//     the process is already marked unusable when pool.release inspects it and
//     is killed instead of parked. The process is still checked out by that
//     caller throughout, so no other VUser can be holding it.
//
// The caller therefore waits out the kill in that second case. That only
// happens to a request that has already blown its deadline on a process that
// must die regardless, and the kill is a SIGKILL plus a reap rather than the
// graceful close terminate() uses, so it is bounded and short.
func (p *sidecarProcess) armDeadlineWatchdog(ctx context.Context) func() {
	finished := make(chan struct{})
	settled := make(chan struct{})
	generation := p.requestGeneration.Load()
	var completed atomic.Bool
	go func() {
		defer close(settled)
		select {
		case <-finished:
		case <-ctx.Done():
			if completed.Load() {
				return
			}
			if !p.requestGeneration.CompareAndSwap(generation, generation+1) {
				return
			}
			p.poison()
		}
	}()
	return func() {
		completed.Store(true)
		if !p.requestGeneration.CompareAndSwap(generation, generation+1) {
			// The watchdog claimed this request first. Wait for it to finish
			// poisoning so the process is never handed back to the pool as
			// healthy and then killed under its next caller.
			<-settled
		}
		close(finished)
	}
}

func (p *sidecarProcess) wrapFailure(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("jms sidecar %s timed out: %w", operation, ctx.Err())
	}
	return fmt.Errorf("jms sidecar %s failed: %w", operation, err)
}

// poison marks the process unusable and kills it immediately, unblocking any
// pending read.
func (p *sidecarProcess) poison() {
	p.poisoned.Store(true)
	p.kill()
}

// kill terminates the process without a grace period and reaps it so no zombie
// or leaked pipe is left behind.
func (p *sidecarProcess) kill() {
	p.closeOnce.Do(func() {
		defer p.releaseBudget()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = p.stdin.Close()
		_ = p.cmd.Wait()
	})
}

// terminate closes stdin so a well-behaved sidecar exits on EOF, then falls
// back to a kill if it overstays the grace period. It always reaps the child.
func (p *sidecarProcess) terminate() {
	p.closeOnce.Do(func() {
		defer p.releaseBudget()
		_ = p.stdin.Close()
		waited := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(waited)
		}()
		select {
		case <-waited:
		case <-time.After(sidecarCloseGrace):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			<-waited
		}
	})
}

// readBoundedLine reads one newline-terminated record without letting a hostile
// or broken sidecar grow an unbounded buffer.
func readBoundedLine(reader *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(out)+len(chunk) > max {
			return nil, errSidecarLineTooLong
		}
		out = append(out, chunk...)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return out, err
	}
}

// sidecarPool hands out long-lived sidecar processes for one (command, config)
// pair. In-flight requests are bounded upstream by sidecarProcessLimiter and
// live processes are bounded globally by reserveSidecarProcessSlot; maxIdle is
// this pool's own belt-and-braces cap on parked processes.
type sidecarPool struct {
	command string
	start   func(string) (*sidecarProcess, error)
	idleTTL time.Duration
	maxIdle int

	mu     sync.Mutex
	idle   []*sidecarProcess
	closed bool
}

func newSidecarPool(command string) *sidecarPool {
	return &sidecarPool{
		command: command,
		start:   startSidecarProcess,
		idleTTL: sidecarIdleTTL,
		maxIdle: configuredSidecarMaxProcesses(),
	}
}

func (p *sidecarPool) acquire() (*sidecarProcess, error) {
	now := time.Now()
	var stale []*sidecarProcess
	var reuse *sidecarProcess

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("jms sidecar pool is closed")
	}
	for len(p.idle) > 0 {
		candidate := p.idle[len(p.idle)-1]
		p.idle[len(p.idle)-1] = nil
		p.idle = p.idle[:len(p.idle)-1]
		if candidate.poisoned.Load() || now.Sub(candidate.idleSince) > p.idleTTL {
			stale = append(stale, candidate)
			continue
		}
		reuse = candidate
		break
	}
	p.mu.Unlock()

	for _, candidate := range stale {
		retireSidecarProcess(candidate)
	}
	if reuse != nil {
		return reuse, nil
	}
	// A new process is about to exist, so claim its slot inside the operator's
	// global ceiling before forking. Claiming rather than merely checking is what
	// stops concurrent acquires on different pools from all forking against the
	// same free slot.
	reserveSidecarProcessSlot(configuredSidecarMaxProcesses())
	proc, err := p.start(p.command)
	if err != nil {
		releaseSidecarProcessSlot()
		return nil, err
	}
	if !proc.budgeted {
		// An injected starter that does not own a budget slot must not leak the
		// one claimed above.
		releaseSidecarProcessSlot()
	}
	return proc, nil
}

// popIdle removes the least recently parked process so a global budget sweep can
// retire it. release appends and acquire takes from the back, so index 0 is the
// coldest entry.
func (p *sidecarPool) popIdle() *sidecarProcess {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) == 0 {
		return nil
	}
	proc := p.idle[0]
	copy(p.idle, p.idle[1:])
	p.idle[len(p.idle)-1] = nil
	p.idle = p.idle[:len(p.idle)-1]
	return proc
}

// takeIdleSidecarForEviction pops one parked process from any registered pool so
// reserveSidecarProcessSlot can retire it. Without a global sweep the ceiling
// would only bound each pool independently, so a test whose groups differ by
// broker configuration could run (#pools x ceiling) live JVM sidecars against a
// ceiling the operator sized for generator memory.
func takeIdleSidecarForEviction() *sidecarProcess {
	sidecarPoolsMu.Lock()
	pools := make([]*sidecarPool, 0, len(sidecarPools))
	for _, pool := range sidecarPools {
		pools = append(pools, pool)
	}
	sidecarPoolsMu.Unlock()
	for _, pool := range pools {
		if victim := pool.popIdle(); victim != nil {
			return victim
		}
	}
	return nil
}

// release parks a healthy process for reuse. Poisoned or surplus processes are
// terminated instead so nothing is leaked.
func (p *sidecarPool) release(proc *sidecarProcess) {
	if proc == nil {
		return
	}
	if proc.poisoned.Load() {
		proc.releaseBudget()
		go proc.kill()
		return
	}
	proc.idleSince = time.Now()
	p.mu.Lock()
	if p.closed || len(p.idle) >= p.maxIdle {
		p.mu.Unlock()
		retireSidecarProcess(proc)
		return
	}
	p.idle = append(p.idle, proc)
	p.mu.Unlock()
}

// retire ends a process instead of returning it to the pool. Used for the
// teardown operation, which in the one-shot protocol always ended a process.
func (p *sidecarPool) retire(proc *sidecarProcess) {
	retireSidecarProcess(proc)
}

func (p *sidecarPool) idleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

func (p *sidecarPool) sweepIdle(now time.Time) {
	var stale []*sidecarProcess
	p.mu.Lock()
	kept := make([]*sidecarProcess, 0, len(p.idle))
	for _, proc := range p.idle {
		if proc.poisoned.Load() || now.Sub(proc.idleSince) > p.idleTTL {
			stale = append(stale, proc)
			continue
		}
		kept = append(kept, proc)
	}
	p.idle = kept
	p.mu.Unlock()
	for _, proc := range stale {
		retireSidecarProcess(proc)
	}
}

// close terminates every parked process and refuses further checkouts. It
// blocks until each child has been reaped.
func (p *sidecarPool) close() {
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()

	var wg sync.WaitGroup
	for _, proc := range idle {
		wg.Add(1)
		go func(target *sidecarProcess) {
			defer wg.Done()
			target.terminate()
		}(proc)
	}
	wg.Wait()
}

var (
	sidecarPoolsMu    sync.Mutex
	sidecarPools      = map[string]*sidecarPool{}
	sidecarReaperOnce sync.Once
)

// sidecarPoolKey keys a pool by command plus broker configuration so a pooled
// process never sees more than one configuration. The config is hashed rather
// than stored so credentials never end up in a map key.
func sidecarPoolKey(command string, cfg config) string {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		encoded = []byte(fmt.Sprintf("%#v", cfg))
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(command))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(encoded)
	return hex.EncodeToString(digest.Sum(nil))
}

func sidecarPoolFor(command string, cfg config) *sidecarPool {
	key := sidecarPoolKey(command, cfg)
	sidecarPoolsMu.Lock()
	defer sidecarPoolsMu.Unlock()
	if pool, ok := sidecarPools[key]; ok {
		return pool
	}
	pool := newSidecarPool(command)
	sidecarPools[key] = pool
	sidecarReaperOnce.Do(func() { go sidecarPoolReaper() })
	return pool
}

// sidecarPoolReaper is started lazily the first time persistent mode is used,
// so builds that never enable it pay nothing.
func sidecarPoolReaper() {
	ticker := time.NewTicker(sidecarReapInterval)
	defer ticker.Stop()
	for now := range ticker.C {
		sidecarPoolsMu.Lock()
		pools := make([]*sidecarPool, 0, len(sidecarPools))
		for _, pool := range sidecarPools {
			pools = append(pools, pool)
		}
		sidecarPoolsMu.Unlock()
		for _, pool := range pools {
			pool.sweepIdle(now)
		}
	}
}

// closeAllSidecarPools terminates every pooled sidecar process. It is the
// explicit shutdown hook for hosts that want a deterministic teardown; pools
// are dropped from the registry so a later run starts fresh.
func closeAllSidecarPools() {
	sidecarPoolsMu.Lock()
	pools := make([]*sidecarPool, 0, len(sidecarPools))
	for key, pool := range sidecarPools {
		pools = append(pools, pool)
		delete(sidecarPools, key)
	}
	sidecarPoolsMu.Unlock()
	for _, pool := range pools {
		pool.close()
	}
}

// runPooledSidecar performs one request against a pooled long-lived process.
func runPooledSidecar(ctx context.Context, pool *sidecarPool, req sidecarRequest) (sidecarResponse, error) {
	proc, err := pool.acquire()
	if err != nil {
		return sidecarResponse{}, fmt.Errorf("acquire pooled JMS sidecar: %w", err)
	}
	resp, runErr := proc.roundTrip(ctx, req)
	if strings.EqualFold(strings.TrimSpace(req.Operation), "teardown") {
		pool.retire(proc)
	} else {
		pool.release(proc)
	}
	return resp, runErr
}

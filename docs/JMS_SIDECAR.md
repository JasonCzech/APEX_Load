# JMS Solace sidecar bridge

The `jms-solace` protocol can delegate messaging to an external, operator-owned
sidecar process. Scripts may *select* the sidecar adapter
(`jms_properties.adapter: "sidecar"`), but they can never supply the command —
a script is user-controlled JSON, so honouring a script-supplied command would
be arbitrary code execution on a generator.

## Operator environment

| Variable | Required | Meaning |
| --- | --- | --- |
| `APEXLOAD_JMS_SIDECAR_COMMAND` | yes | Command line for the sidecar. Split on whitespace; no shell is involved. |
| `APEXLOAD_JMS_SIDECAR_MAX_PROCESSES` | no | Concurrent sidecar process ceiling (default 25, hard cap 1024). |
| `APEXLOAD_JMS_SIDECAR_PERSISTENT` | no | `1`/`true`/`yes`/`on` enables the pooled long-lived transport described below. Anything else keeps the default. |

Sidecar processes always run with a scrubbed environment (see
`pkg/internal/subprocessguard`), and the adapter refuses to start one at all if
ambient platform credentials are visible in the parent environment.

## Default transport: one process per action

The adapter forks the command, writes a single JSON request object on stdin,
reads a single JSON response object from stdout, and lets the process exit.
stdout and stderr are each capped at 256 KiB.

This is simple and isolated, but it forks once per messaging action, which does
not hold up at load-test VUser counts.

## Pooled transport (opt-in)

With `APEXLOAD_JMS_SIDECAR_PERSISTENT=1` the adapter keeps sidecar processes
alive and reuses them across actions. Enable it only if your sidecar binary
implements the contract below.

**Wire contract** — newline-delimited JSON, one request object per line:

- Read request lines from stdin until EOF, then exit. Exiting on stdin EOF is
  what makes the sidecar orphan-safe: if the generator dies, the OS closes the
  pipe and the sidecar terminates.
- Every request carries a non-empty `"id"`. Echo that exact `"id"` on the
  matching response line. A missing or mismatched `"id"` is treated as a
  desynchronised stream: the process is killed and dropped from the pool.
- Write exactly one response line per request line, in order.
- A response carrying `"error"` is an application-level rejection. The process
  is considered healthy and stays pooled.

**Pool behaviour**

- One pool per (command, broker configuration) pair, so a pooled process only
  ever sees one configuration.
- `APEXLOAD_JMS_SIDECAR_MAX_PROCESSES` bounds in-flight requests *and* sidecar
  processes in service. The two are enforced separately: a finished request
  releases its concurrency slot while its process stays alive parked in the
  pool, so before forking a replacement the adapter *claims* a slot in the
  process budget, retiring parked processes from any pool until one frees up.
  The claim is atomic, so concurrent actions on different pools cannot all fork
  against the same free slot. The ceiling is global, not per pool, so a test
  whose groups use different broker configurations still fits in the process
  budget you sized for the generator.
- Two bounded exceptions to that ceiling, both deliberate. A retired sidecar
  gives its slot back when the adapter decides to end it, not when the child has
  finished exiting: a sidecar that ignores the "exit on stdin EOF" contract can
  therefore linger for up to its 2-second kill grace beyond the ceiling. Waiting
  for it instead would stall a VUser action and corrupt the response time the
  run is measuring. And if every live sidecar is busy serving a request, the
  fork proceeds anyway — those processes are already bounded by the same
  variable acting as the in-flight limit, and blocking would deadlock the
  action. Size generator memory with a little headroom above the ceiling
  accordingly.
- A process is checked out exclusively for one request, so requests never
  interleave on a single pipe.
- Idle processes are reaped after 60 seconds.
- Any transport-level fault (decode failure, id mismatch, oversized output,
  EOF, timeout) kills and discards the process rather than reusing a pipe of
  unknown state.
- The 256 KiB stdout/stderr caps are charged per request, not per process
  lifetime, so ordinary per-action logging never accumulates its way into a
  failed action. Because the OS delivers a sidecar's stderr asynchronously, a
  request that floods stderr just past the cap is occasionally reported against
  the following request instead of its own; it is never silently dropped.
- The `teardown` operation retires the process it ran on instead of returning
  it to the pool, matching the one-shot behaviour where teardown always ended a
  process.

**Known limitation**: there is no run-scoped shutdown hook wired into the
engine yet, so pooled processes are reclaimed by the idle reaper (or by
generator exit) rather than at the exact end of a run.

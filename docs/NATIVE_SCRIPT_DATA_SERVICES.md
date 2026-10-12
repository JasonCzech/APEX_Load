# Native script data services

New APEX scripts can use the same live-table actor from `live_table` actions,
Java methods, and the `native_javascript` engine action. Existing snapshot data
pool bindings keep their existing behavior. Bind a live table explicitly and
use that binding's alias in the script declaration:

```json
{
  "config": {
    "native_services": {
      "live_tables": [{"alias": "work", "operations": ["push", "lease", "ack"]}]
    }
  }
}
```

A declaration grants only the listed guest operations on the matching scenario
binding. The binding must independently grant those operations. The runtime
adds renewal and release authority when needed to maintain and clean up an
accepted lease; that authority does not grant guest calls to undeclared
operations. At most 16 aliases are declared, each at most 64 characters. Queue
and column profiles enforce their own operation contracts. These are native APEX
contracts, independent of LoadRunner or VTS.

Values crossing the native interface are literal strings. Text such as
`{{var.name}}` returned or created by guest code is stored as text. Managed
secrets cannot be written to a live table. Native code sees only semantic
arguments and opaque lease references, never controller URLs, assignment
credentials, actor epochs, request sequence allocation, or raw lease tokens.

## Java

Build the bundled Java harness/SDK with the repository's Java harness targets.
Compile the script against the harness or its `apexload.data` SDK classes; the
runner supplies its own SDK at execution time. Do not package a replacement SDK
inside the script. Java 8 source compatibility is retained.

```java
import apexload.data.Data;
import java.util.Collections;

public class Actions {
    public int init() { return 0; }
    public int action() {
        Data.Table work = Data.table("work");
        work.push(Collections.singletonMap("id", "C001"));
        Data.Result acquired = work.lease(1000, 30000);
        if ("ok".equals(acquired.status())) {
            String id = acquired.row().values().get("id");
            // Perform the script's work with id.
            work.ack(acquired.leaseRef());
        }
        return 0;
    }
    public int end() { return 0; }
}
```

The SDK is bound during an admitted persistent `init`, `action`, or `end` method
on its invocation thread. The entry class is initialized and constructed before
that binding. Background threads and one-shot `full_lifecycle` calls have no
native service authority. Java remains subject to the existing governed runner
and containment policies; the SDK does not turn a JVM into a security sandbox.

Convenience methods include `push`, `next`, `pop`, `lease`, `read`, `ack`,
`release`, `renew`, `update(record, version, values)`, and
`increment(record, column, decimalDelta)`. `table.call(operation, arguments)`
exposes the remaining declared operations. Revision, row version, count, and
increment delta use decimal strings to preserve 64-bit values. Empty and absent
cells remain distinct. Results are immutable projections. Failures expose a
bounded code and `outcomeUnknown`, without echoing values.

Local persistent sessions, sidecar sessions, pooled slots, and dedicated broker
sessions carry native callbacks in their admitted invocation. Remote callbacks
use the existing authenticated connection toward the runner, with a bounded
duplex exchange; guests do not receive a callback URL. Each prepared JVM must
advertise its native SDK support; pooled and broker workers prove this before
commit, and local and sidecar workers prove it before method admission. An old
runner or old harness fails closed. Pooled callbacks preserve the host, slot,
invocation, and callback sequence in authenticated frames.

## JavaScript

`native_javascript` is an engine action, separate from browser JavaScript:

```json
{
  "type": "native_javascript",
  "name": "Publish work",
  "params": {
    "source": "const r = apex.data.table('work').push({id: input.id}); return {status: r.status};",
    "inputs": {"id": "{{var.customer_id}}"},
    "result_prefix": "published",
    "timeout_ms": 5000
  }
}
```

`source` is a literal function body receiving `input` and `apex`. Only structured
`inputs` undergo normal engine interpolation. The synchronous frozen facade is
`apex.data.table(alias)` or `apex.data.call(request)`. Table operations accept a
semantic options object; `push(values)` and `ack/release(leaseRef)` provide the
common shortcuts. Return a JSON object to bind its fields under `result_prefix`.

The initial implementation supports Linux hosts with enforceable process data
and CPU limits. Other hosts reject preflight. Each action uses a separate process
with a 256 MiB data limit, a two-second cumulative CPU limit, a one-second VM
compute interval between service calls, and the configured wall deadline
(100–120,000 ms). The service-wait interval does not consume the VM compute
interval. The worker installs no filesystem, environment, process, network,
module-loader, or timer APIs. Four worker processes can be active per generator;
additional admission fails with a capacity error.

Source is limited to 128 KiB, structured inputs and returned values to 64 KiB,
returned fields to 128, callback requests to 1 MiB, results to 2 MiB, and callbacks
to 1,024 per invocation. Input nesting and entry counts are independently
bounded. Per-host pooled callback admission is also bounded and has a separate
writer queue from ordinary lifecycle controls.

## Continuity and leases

The engine assigns each VUser and parallel branch its own trusted call lane.
Exact retries reuse the original actor request. A timeout after possible
acceptance never invents a new mutation identity. Unknown outcomes, lost runtime
continuity, or expired lease authority cancel the active native invocation and
contain its execution. A guest catching an exception cannot restore lost
runtime authority. Known leases remain tracked for trusted cleanup, and opaque
references are confined to their originating VUser or branch.

These callbacks use the same live-table actor and permissions as DSL actions;
there is no second table store and no background HTTP polling against an
operator-supplied table endpoint.

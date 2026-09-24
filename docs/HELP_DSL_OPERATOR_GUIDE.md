# DSL scripting for LoadRunner & JMeter operators

You already know the important work: reproduce a user journey, vary its data, correlate responses, check business outcomes, and apply a realistic workload. APEX Load expresses that journey as a structured JSON script. **DSL** means **domain-specific language**: here, a vocabulary of test actions and settings. You do not need to write Go or manage goroutines to author a test.

Use this guide to understand and adapt a script. Use the [DSL action reference](DSL_ACTION_REFERENCE.md) to look up exact action fields. For a first run against a local sample service, start with [Your first test](HELP_GETTING_STARTED.md).

## Translate familiar concepts

These are orientation aids, not promises that every imported feature behaves identically. Review the import findings and effective runtime settings before comparing results.

| Familiar concept | APEX Load equivalent | What to do differently |
| --- | --- | --- |
| VuGen script / JMeter sampler sequence | A script with ordered `actions` | Describe the steps as JSON objects; use the structured Script Editor to edit them. |
| LoadRunner VUser / JMeter thread | A virtual user, or VUser | Author one user's journey. Configure the user count in the scenario. |
| Controller scenario / Thread Group load settings | Scenario script groups and load schedule | Set concurrency, ramp-up, duration, and group overrides outside the action list. |
| `vuser_init` / once-per-user setup | `init` | Runs once for each VUser that starts work, not once for the whole test. |
| `Action` / repeated sampler sequence | `actions` | One full pass is an iteration. Each VUser repeats the list. |
| `vuser_end` / user cleanup | `teardown` | Best-effort cleanup per VUser; this is not JMeter's separate tearDown Thread Group. |
| `web_url`, `web_custom_request` / HTTP Request sampler | `GET`, `POST`, and other HTTP actions | Put the URL in `target`, request headers in `headers`, and payload in `body`. |
| Correlation / extractor Post-Processor | An action's `extractors` | Attach the capture to the request that returns the value, then use `{{var.name}}`. |
| Parameter files / CSV Data Set Config | `data_sources` and named `parameters` | Choose row selection, refresh timing, and exhaustion behavior deliberately. |
| Response checks / Assertions | An action's `assertions` | Check content as well as status so a fast error page cannot pass as a successful journey. |
| Think time / scoped Timers | Think-time settings plus `think_time_after` | Enable the pause on the intended action; JMeter timer scope is not reproduced by JSON nesting. |
| Iteration pacing | `config.pacing` | Distinguish a delay after completion from a start-to-start interval. |
| Transaction markers / Transaction Controller | Named reporting units and automatic transaction settings | Choose the reporting scope and compare equivalent measurements. |
| If / Loop Controllers | `control_if`, `control_loop` | Use explicit nested `control.steps`; indentation alone does not define scope. |

For JMeter's original terminology and scoping rules, see the [Apache JMeter test-plan manual](https://jmeter.apache.org/usermanual/test_plan.html). LoadRunner migration details are in [LoadRunner web coverage](LOADRUNNER_COVERAGE.md) and [Runtime settings support](RUNTIME_SETTINGS_PARITY.md).

## Understand how one user runs

A **goroutine** is the Go engine's lightweight unit of concurrent execution. In the normal closed workload model, each VUser has its own execution flow. The engine schedules these flows for you. There is no goroutine syntax to add to your script.

1. A VUser starts as the scenario ramps up and prepares its protocol session and data.
2. It runs `init`, for example to log in and capture a session token.
3. It runs the `actions` list in order. A later request can use an earlier response.
4. It observes the configured pacing and repeats `actions` while the run permits more work.
5. It attempts `teardown` when its work ends, for example to log out.

With 100 VUsers and three requests in `actions`, you have 100 independent journeys. You do not automatically have 300 simultaneous requests. Each user normally waits for one action before starting the next; other users continue while that user waits on a response or think time.

Extracted variables and HTTP cookies belong to the user's session. A token captured by user 1 does not become user 2's token. Session state can outlive an iteration; review cookie-clearing and parameter-refresh settings when modeling either returning users or a new login on every pass. A login in `init` is performed once per user; put it in `actions` if every iteration must log in again.

Ordinary pause takes effect at execution boundaries and lets current work reach a safe boundary. Graceful stop and immediate stop have different interruption behavior. Teardown is best effort and cannot guarantee server cleanup after a generator crash or forced termination. See [Run & monitor](#/docs?guide=user-guide&article=execution).

## Read the script structure

JSON uses `{ }` for an object, `[ ]` for an ordered list, and `"key": value` for a setting. Strings and property names need double quotes. Numbers such as `200` and booleans such as `true` are not quoted. Separate entries with commas, but do not add a trailing comma or comments.

| Field | Operator meaning |
| --- | --- |
| `name`, `version` | Identify the journey and its revision. |
| `protocol` | Select the runtime family, for example `http` or `browser`. |
| `config` | Base URL, protocol options, timing, errors, and run logic. |
| `data_sources` | Named tables or files used by this script. |
| `parameters` | Named values with explicit generation or refresh rules. |
| `init` | Ordered actions before the user's main iterations. |
| `actions` | Ordered actions for one iteration; the main journey. |
| `teardown` | Ordered cleanup actions after that user's work. |

The examples below are **script objects** for the editor's JSON view or a DSL file. The editor and the DSL parser accept either form: the bare script object or the same object wrapped as `{"script": {...}}`, which is how API requests such as script validation carry it (see [Your first test](HELP_GETTING_STARTED.md)). Smaller examples are explicitly labeled as fragments.

## Walk through a complete HTTP example

This example reads a product and then reads its stock. It includes inline test data, so no CSV file or secret is needed. **Replace `https://shop.example.com` with your approved test service and adapt the paths and checks before execution.** The example expects `GET /api/products/SKU-100` to return JSON with an `id`, and `GET /api/stock/<id>` to return JSON containing `available`.

```json
{
  "name": "Browse product and stock",
  "version": "1.0.0",
  "protocol": "http",
  "config": {
    "base_url": "https://shop.example.com",
    "http": { "request_timeout_ms": 10000 },
    "pacing": { "type": "fixed_interval", "interval_ms": 5000 },
    "error_handling": { "continue_on_error": false, "max_retries": 0 },
    "run_logic": { "auto_transactions": "per_iteration" }
  },
  "data_sources": [
    {
      "name": "products",
      "type": "csv",
      "inline_data": "sku\nSKU-100\nSKU-200\n",
      "strategy": "sequential",
      "first_row_header": true
    }
  ],
  "init": [],
  "actions": [
    {
      "name": "Read product",
      "type": "GET",
      "target": "/api/products/{{data.products.sku}}",
      "extractors": [
        { "name": "product_id", "source": "body", "type": "jsonpath", "expression": "$.id", "on_miss": "fail", "convert": "url_encode" }
      ],
      "assertions": [
        { "type": "status_code", "expected": 200 }
      ],
      "think_time": { "type": "fixed", "mean_ms": 1000 },
      "think_time_after": true
    },
    {
      "name": "Read stock",
      "type": "GET",
      "target": "/api/stock/{{var.product_id}}",
      "assertions": [
        { "type": "status_code", "expected": 200 },
        { "type": "body_contains", "value": "\"available\"" },
        { "type": "response_time_ms", "max": 1500 }
      ]
    }
  ],
  "teardown": []
}
```

Read it in execution order: bind a product row, send **Read product**, capture its `id`, pause for one second, and send **Read stock** using that captured value. The empty lifecycle lists are intentional: this public-catalog journey has no login or logout. Add authentication in `init` and cleanup in `teardown` when your application requires them.

The extractor fails the first action when `id` is missing. With `continue_on_error: false`, the user stops that iteration instead of sending the dependent stock request. The content assertion checks that the stock response contains the field name; it does not prove that stock is positive. Replace it with the business checks your journey needs.

## Correlate and check responses

**Parameterization supplies input you already have; correlation captures input the server creates.** A product SKU from a CSV is parameterization. A session token, cart ID, or CSRF value returned by the server is correlation.

For a login response such as `{"token":"session-value"}`, attach this extractor fragment to the login action:

```json
{
  "extractors": [
    { "name": "session_token", "source": "body", "type": "jsonpath", "expression": "$.token", "on_miss": "fail" }
  ]
}
```

Then add this header fragment to requests that use that session:

```json
{
  "headers": { "Authorization": "Bearer {{var.session_token}}" }
}
```

Unlike a LoadRunner registration call placed before a request, the extractor is declared on the producing request. Never hard-code the token from a recording. Inspect the actual replay response: an HTML sign-in page cannot satisfy a JSONPath for a token.

| Need | Use | Check during replay |
| --- | --- | --- |
| JSON response field | `jsonpath`, for example `$.token` | Correct path, case, and response shape. |
| XML or HTML value | `xpath` or `css` | Correct document and selector. |
| Boundary or pattern capture | `boundary` or `regex` | Scope and capture rules; regex uses Go/RE2 syntax. |
| Response header | `header` with the header name | The response actually carries that header. |
| Required dynamic value | `on_miss: "fail"` | Fail at the producing action, not a later dependent request. |
| Value reused in a URL | Extractor `convert: "url_encode"` | Encode once; do not double-encode an already encoded value. |

Assertions express success: exact status (`status_code`), content (`body_contains`), or an individual action's timing limit (`response_time_ms`). HTTP status outside 200–399 fails by default. A 200 response can still be a business failure, and a per-action response-time assertion is different from an aggregate p95 SLA. See [Capturing values and checking responses](#/docs?guide=user-guide&article=authoring&section=capturing-values-with-extractors) for additional types.

## Choose data and variable lifetimes

| Template | Where the value comes from | Typical use |
| --- | --- | --- |
| `{{data.products.sku}}` | The bound row of a named data source | Product or account input. |
| `{{var.product_id}}` | A value captured earlier for this VUser; can fall back to a named parameter | Correlated server ID. |
| `{{param.request_id}}` | A declared named parameter | A value with controlled refresh timing. |
| `{{env.KEY}}` | Runtime environment values | Environment-specific input. |
| `{{secret.NAME}}` | A project secret | Managed credentials. |
| `{{fn.uuid()}}`, `{{fn.random(1,20)}}` | Built-in generated values | Unique IDs or varied numbers. |

Template expansion happens in supported string fields at runtime. Keep the namespace and spelling exact. JSON `body` is itself a string: quotes inside it must be escaped, as in `"body": "{\"sku\":\"SKU-100\"}"`. Check the rendered payload when data contains quotes, newlines, or URL-special characters; text substitution is not a universal JSON or URL encoder.

Use a named parameter when multiple requests must reuse the same generated value. This fragment belongs in the script's top-level `parameters` list:

```json
{
  "parameters": [
    { "name": "request_id", "type": "uuid", "update": "each_iteration" }
  ]
}
```

Use `{{param.request_id}}` wherever that iteration needs the ID. `once` retains a value for the VUser, `each_iteration` refreshes it on a new iteration, and `each_occurrence` refreshes it on each resolution. Repeating `{{fn.uuid()}}` directly does not promise reuse of one UUID.

For row data, choose the required allocation:

| Strategy | Row selection | Exhaustion or reuse |
| --- | --- | --- |
| `sequential` | Next row on each row request | Wraps to reuse rows. |
| `random` | Random row on each row request | Rows can repeat. |
| `unique` | Row selected by VUser within the assigned data partition | Wraps when users outnumber available rows; uniqueness requires enough rows. |
| `pop` | Consume the next available row | Exhaustion records a failed data-source result and stops the VUser before another submission. |

Repeated selection is acceptable for product browsing but can invalidate tests that require exclusive accounts. Size the data for the workload, review named parameters' refresh and exhaustion settings, and verify allocation across your generator fleet. Shared pools are snapshotted for a run; consuming run data does not delete rows from the repository.

Use [Parameters, data & secrets](#/docs?guide=user-guide&article=data) for data-pool bindings and secret setup. A file on your laptop is not automatically available on remote generators. Prefer governed shared assets or inline data for portable examples.

## Set think time, pacing, and load

**Think time separates actions; pacing separates iterations; ramp-up introduces users.** All `_ms` settings use milliseconds: 1000 ms is one second.

| Intention | Setting | Result |
| --- | --- | --- |
| Pause one second after an action | `think_time: {"type":"fixed","mean_ms":1000}` and `think_time_after: true` on that action | That VUser pauses after the action. |
| Reuse a common think-time distribution | `config.think_time`, with `think_time_after: true` on the intended actions | Those actions use the common timing unless overridden. |
| Wait five seconds after an iteration | `config.pacing: {"type":"fixed_delay","delay_ms":5000}` | Work taking two seconds gives roughly seven seconds between starts. |
| Aim for a five-second start interval | `config.pacing: {"type":"fixed_interval","interval_ms":5000}` | Work taking two seconds leaves roughly three seconds to wait. |
| Vary the delay or start interval | `random_delay` or `random_interval` with their min/max fields | Randomize between the configured bounds. |
| Apply a target rate | `tps` pacing and scenario throughput controls | Review the load model and measured throughput; configured rate is not achieved rate. |

If an iteration takes longer than a fixed interval, the engine adds no interval wait. It does not speed up the application or automatically overlap that user's next iteration. Include response time and think time when estimating cycle length.

For example, 20 users completing one two-request iteration every five seconds produce approximately **4 iterations/second and 8 requests/second** in steady state, excluding initialization, retries, redirects, and other extra traffic. Transaction throughput depends on the reporting scope you selected.

A closed workload reuses a population of users; slower responses reduce how often they finish journeys. An open workload schedules arrivals and uses an in-flight limit. Select it deliberately in the load model and inspect achieved arrivals and capacity. Adding `control_parallel` to a script is not how you convert a closed workload into an open one.

**The scenario owns the run schedule.** A script-only `config.run_logic.iterations` cap can be removed when a duration-driven scenario is compiled. Put an intended fixed count in the scenario group or its runtime settings. Review effective settings before launch; do not assume an imported VuGen iteration count controls the scenario. See [Model load](#/docs?guide=user-guide&article=scenarios) and [Runtime settings support](RUNTIME_SETTINGS_PARITY.md).

## Add conditions and loops deliberately

Use `control_if` for a branch, `control_switch` for ordered alternatives, and `control_loop` for repeated work within one iteration. This action fragment belongs inside `actions` and repeats the stock read twice:

```json
{
  "name": "Repeat stock read",
  "type": "control_loop",
  "control": {
    "count": 2,
    "steps": [
      {
        "name": "Check stock again",
        "type": "GET",
        "target": "/api/stock/{{var.product_id}}",
        "assertions": [{ "type": "status_code", "expected": 200 }]
      }
    ]
  }
}
```

Place it after **Read product** so `product_id` already exists. The loop adds two requests per iteration, not two VUsers. For conditions, use `left`, `op`, and `right` (for example comparing `{{var.status}}` with `ready`) or a supported CEL expression. Bound polling loops with `max_iterations` and include suitable waits. `control_break` and `control_continue` affect the nearest enclosing loop.

`control_parallel` explicitly runs named branches concurrently with isolated protocol state and merges extracted values under `{{var.parallel.<branch>.<name>}}`. Do not assume branches share their parent's logged-in HTTP session. Keep dependent login-and-request steps sequential unless the branch itself establishes the required session. See the [`control_parallel` action contract](DSL_ACTION_REFERENCE.md).

## Name measurements and handle failures

Give actions stable business names such as **Read product**. Avoid embedding account IDs or timestamps in names; they make reports hard to compare.

`config.run_logic.auto_transactions` supports `none`, `per_action`, and `per_iteration`. Select the scope that matches your benchmark. Imported `source_transaction` markers can preserve explicit source transaction boundaries; inspect the converted markers and their results. There is no generic action type named `transaction`. A request count, a named business-transaction count, and an iteration count can all differ.

| Control | Operator decision |
| --- | --- |
| `continue_on_error: false` | Stop the current iteration on an action failure; useful for dependent requests. This is not automatically a whole-test stop. |
| `continue_on_error: true` | Allow later actions after failure; use only when their inputs remain valid. |
| `max_retries`, `retry_delay_ms`, `retry_backoff` | Retries add work and can repeat side effects. Decide whether the operation is safe to retry. |
| `snapshot_on_error` | Capture failure evidence for diagnosis; inspect it during replay. |
| Scenario stop rules / SLA gates | Control aggregate acceptance and run-stop behavior separately from action checks. |

An omitted error policy can inherit a scenario setting. Inspect the effective policy instead of assuming that standalone replay and a scenario have the same default. See [Runtime settings support](RUNTIME_SETTINGS_PARITY.md).

## Validate, replay, then scale

1. Create or import the script in **Scripts**. Resolve import review findings, missing assets, and unsupported steps.
2. Validate the DSL. Validation checks structure and supported fields; it does not prove a server response, credential, or business outcome.
3. Replay one user against the intended environment. Inspect each request, response, assertion, and extracted value. Use the [Replay debugger](SCRIPT_DEBUGGER_GUIDE.md) to locate the first divergence.
4. Run more than one iteration and a small number of users. Check token refresh, cookies, data reuse, exhaustion, and expected request counts.
5. Add the script to a scenario, review the effective runtime settings, then increase load gradually. Compare achieved throughput, response time, errors, and generator health.

| Symptom | First check |
| --- | --- |
| JSON fails validation | Quotes, commas, numeric types, supported action names, and the required top-level fields (`name`, `protocol`, `actions`), whether bare or wrapped in `script`. |
| First request succeeds; next returns 401/403 | Missing or expired correlated token, incorrect header, or cookies cleared between iterations. |
| Literal template or empty value in a request | Namespace, column spelling, parameter declaration, and whether the producing action ran. |
| Replay works; multiple users fail | Account collisions, insufficient rows, shared server-side state, or remote asset availability. |
| Rate is too high | Missing `think_time_after`, no pacing, nested loops, retries, or a requests-versus-iterations mismatch. |
| Rate is too low | Slow responses, delay versus interval confusion, too few users, capacity limits, or the wrong workload model. |
| Imported script validates but behavior differs | Review-only steps and compatibility-only runtime settings; validation is not migration equivalence. |

## Find the exact protocol contract

This guide uses HTTP because the request/correlation/check cycle is familiar. A script selects a protocol family; engine control actions are shared, but protocol actions and assertions are not interchangeable. An HTTP script does not run page JavaScript just because it has a browser-like user agent.

Use [DSL action reference](DSL_ACTION_REFERENCE.md) for all action fields and its downloadable schema, [Browser & mobile support](BROWSER_AND_MOBILE_SUPPORT.md) for browser execution, [Messaging support](MESSAGING_SUPPORT.md) for broker adapters, and [Capabilities & limitations](HELP_CAPABILITIES.md) for runtime boundaries. Authorable or importable does not always mean executable: SAP GUI has no bundled runtime, and Java VUser and Bruno require configured runners. Some messaging integrations require a real adapter instead of the default mock.

Start with one verified journey, keep its inputs and measurements explicit, and use the action catalog as your lookup reference as you extend it.

### HTTP methods, loop bounds, and builtin compatibility

HTTP, SOAP, and Java-over-HTTP actions use a case-insensitive HTTP method as their
`type` (`get` and `GET` both work), or an explicit `method`. Unknown types such as
`post_order` or `request` without a method fail validation and execution, so a
script that needs GET must say so with `type: "GET"` or `method: "GET"`. An omitted
type and method use the runtime GET default.

Count and `for_each` loops execute their declared passes, up to the hard ceiling
of 1,000,000. An explicit `max_iterations` still caps any loop. Only
condition-driven loops use the default safety cap of 1,000.

Zero-argument builtins accept both spellings, including
`{{fn.timestamp.unix}}` and `{{fn.timestamp.unix()}}` (also `unix_ms`, `iso8601`,
and `rfc3339`); templates that use parentheses, such as imported Agentforce
scripts, resolve the same way. A `pop` data source never reuses the last row
after exhaustion: replays and shakeouts report an exhaustion error, and load runs
record a failed result and stop the affected VUser.

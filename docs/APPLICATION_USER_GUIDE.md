# APEX Load User Guide

APEX Load is a Kubernetes-native performance and load testing platform. This guide
walks you through the application end to end — from signing in and finding your way
around, to authoring a test script, assembling a scenario, launching a run, watching
it live, and reading the results.

It is written for the people who run performance tests: engineers, SDETs, and
performance analysts working in the dashboard. For administrative tasks (managing
users, roles, the generator fleet, and system settings) see the [Administration](ADMIN_USER_GUIDE.md).
For automating APEX Load from CI/CD or scripts, see the [API & Automation](API_USER_GUIDE.md) and the [API Reference](#/swagger).

If you have never used APEX Load before, open [Your first test](HELP_CENTER.md#build-and-run-your-first-test) and
come back to the reference sections afterwards.

## What APEX Load Does

APEX Load drives realistic load against your applications and reports how they behave
under it. A few concepts recur throughout the product:

| Concept | What it is |
| --- | --- |
| **Virtual user (VUser)** | One simulated client. During a run it executes your script's actions repeatedly against the target system. |
| **Script** | What a single virtual user does, expressed in a JSON-based DSL with `config`, `data_sources`, `parameters`, `init`, `actions`, and `teardown` sections. There is no proprietary scripting language to learn. |
| **Scenario** | The runnable load model: which scripts run, how many VUsers per group, how fast they ramp, how long they run, and which runtime settings apply. |
| **Run (test)** | One execution of a scenario against a chosen application and release. A run produces live metrics and an archived report. |
| **Generator** | A worker that actually drives the VUsers. A run is distributed across the generator fleet. |
| **Transaction** | A named, timed unit of work inside a script. Response-time percentiles and SLA verdicts are reported per transaction. |

APEX Load can drive HTTP/1.1 and HTTP/2, SOAP/WSDL, WebSocket, gRPC, messaging (JMS,
Kafka, Azure Event Hubs), databases, network protocols, and browser replay — so one
platform covers API, integration, and end-to-end load.

Work is organized as a hierarchy: **Domain → Application Repository → Category →
Project → Release**. The **Project** is the boundary for access control, scripts,
scenarios, and results; the **Release** gives you release-over-release trend
comparisons.

## Ways To Use APEX Load

APEX Load meets you where you work through three surfaces:

| Surface | Use it when |
| --- | --- |
| **Hosted browser dashboard** | The primary, zero-install experience, served by the controller. Everything in this guide refers to it unless noted otherwise. |
| **Desktop workspace** | You want a local controller with local storage that can sync with a remote workspace — offline authoring, or a workstation that cannot reach the shared controller. The dashboard inside it behaves the same. |
| **`apexctl` command line** | Headless runs from a terminal or a CI/CD pipeline, and local script validation before you ever open the dashboard. |

## Quick Start: Your First Test

The shortest path from an empty workspace to a report is a step-by-step walkthrough in
[Your first test](HELP_CENTER.md#build-and-run-your-first-test), which includes a local sample target and a
ready-to-load script. In outline:

1. Set your **Domain**, **Project**, and **Application/Release** in the left sidebar;
   scripts, scenarios, and results are all filtered by it.
2. Create or import a script, add at least one assertion, then **Validate Script(s)**
   and **Debug one iteration** (see [Validating And Replaying](#validating-and-replaying)).
3. Build a one-group scenario, launch it from **New run**, and watch it in
   **Command Center**.
4. When the run reaches `COMPLETE`, read the report from **Results** and pin it as the
   release baseline.

The sections that follow explain each step in depth.

---

## Signing In And Getting Oriented

How you sign in depends on how your deployment is configured — typically single
sign-on (OIDC) or an authentication gateway. Once you are in, the **left sidebar** is
your main map:

| Sidebar item | What it is for |
| --- | --- |
| **Dashboard** | Live overview of tests and recent activity. |
| **New run** | Launch a run from a scenario, and manage run schedules. |
| **Command Center** | Active-run overview and live run controls. |
| **Results** | Browse completed runs and their reports. |
| **Scripts** | Author, import, and manage test scripts. |
| **Scenarios** | Assemble scripts into runnable load models, and manage execution plans. |
| **Repository** | Manage applications, releases, projects, and shared assets. |
| **Monitoring** | System health, telemetry, the generator fleet, and the run calendar. |
| **Help & documentation** | The page you are reading, plus API & Automation and the generated API Reference. |
| **Access** | User bindings, service accounts, and activity. Only appears if you hold one of the access-management capabilities. |
| **Settings** | Your and (for administrators) system-wide configuration. |

At the top of the sidebar is the **scope selector**: choose a **Domain**, then a
**Project**, then an **Application/Release**. This scope filters what you see across
the whole app — scripts, scenarios, and results all follow the selected project. Set
your scope first; most "I can't find my script/run" confusion is a scope mismatch.

### Getting Around By Keyboard

Navigation shortcuts are two-key sequences: press `g`, release, then the second key.

| Keys | Goes to |
| --- | --- |
| `g` `d` | Dashboard |
| `g` `c` | Command Center |
| `g` `n` | New run |
| `g` `t` | Scenarios |
| `g` `s` | Scripts |
| `g` `p` | Repository |
| `g` `r` | Results |
| `g` `v` | Activity |
| `g` `m` | Monitoring |
| `g` `f` | Fleet |
| `g` `l` | Schedules |
| `g` `h` | Help & documentation |
| `g` `,` | Settings |
| `g` `a` | Access (when you have access to it) |

Global keys:

- `Cmd/Ctrl` + `K` (or `Ctrl` + `Shift` + `P`) opens the **command palette**, which
  searches pages and actions. It works even while you are typing in a field.
- `?` opens the shortcut help panel, which also lists the shortcuts that only apply to
  the page you are on.
- `Backspace` goes back; `/` focuses the current page's search or filter box.
- `Cmd/Ctrl` + `=` / `-` / `0` zoom the interface in, out, and back to normal. The
  command palette offers the same three zoom commands.

The script editor adds its own keys while it is open; see **Editor keys** in the
Script Debugger guide.

## The Dashboard

The **Dashboard** is your landing page and live overview. Use it to:

- Review acceptance outcomes and archive coverage for the selected release.
  **COMPLETE** describes execution; it does not establish performance acceptance.
- See active and queued tests, inspect wait reasons, and open the selected queued
  run in **Command Center**.
- Watch real-time metric history — throughput, response time, and error rate — for
  active runs.
- Find completed runs by name or reference and filter by period, execution,
  evidence availability, or acceptance. At narrow widths, expand **Run details**
  for configured load, duration, and archive record counts.
- Use **Load comparison** for selected series at matching load levels and
  **Release trend analysis** for named transaction attempts. The page explains
  missing samples and baseline requirements; one run cannot establish a trend.
- Jump straight into a run's **Command Center** or open a finished run's report.

**Needs attention** puts current issues and queued work ahead of grouped historical
archive gaps. Missing archives have unknown publication progress unless recorded
evidence says otherwise. Run-data freshness, live-stream connectivity, and
system-wide health are shown separately; stale or missing data is not proof of
a healthy workload.

## Authoring A Script

Scripts live under the **Scripts** page. From there you can create a new script,
open an existing one in the **Script Editor**, clone one, or import from an external
format.

In **Author**, select a step in the outline to edit its properties in the main
**Action editor**. The editor opens on the first problem, or on the first action when
there is none. Rename the script in place in the top bar. **Source**, **Runtime
Settings**, **Parameters**, and, for imports, **Source Mapping** are always available
as tabs above the editor. **Overview & bulk** exposes multi-step selection and reusable
block tools. **Add Action** stages a new action or block: choose its phase and
place it before, after, inside a supported block, or at the end of the phase.
Canceling insertion leaves the script unchanged.

Use **Undo** and **Redo** for document changes, including action properties,
runtime settings, and insertions. The keyboard shortcuts are Cmd/Ctrl+Z and
Cmd/Ctrl+Shift+Z; code editors retain their own text history. **Review changes**
compares the current script with its last saved version. Saving retains the
selected action.

**Author Problems** collects current draft issues, including what blocks or weakens
replay (review gates, missing values, project secrets), lists blockers first, and links
each one to the action or field it concerns, including nested steps. Validation and debug evidence are tracked
separately from saved state; an earlier debug result does not validate later edits.

The request body editor supports JSON, XML, and plain text, with syntax checks,
find, explicit formatting, template completion, and selection parameterization.
Its masked template preview does not execute requests or resolve runtime values.
In **Source**, use **Edit in Full Script JSON** for whole-document editing, then
**Apply changes** or **Discard JSON edits**. Invalid JSON keeps recovery controls
available and cannot be applied.

### Script Anatomy

Coming from LoadRunner or JMeter? Start with [DSL scripting for LoadRunner & JMeter operators](HELP_DSL_OPERATOR_GUIDE.md) for the execution model, a worked script, correlation, data lifetimes, and pacing examples.

The Script Editor is a structured editor over the JSON DSL. It presents your script as
the DSL's logical sections rather than raw text:

| Section | Purpose |
| --- | --- |
| `config` | Target base URL, protocol settings, think time, pacing, error handling, logging, and run logic. |
| `data_sources` | The CSV/DAT/file pools your actions draw values from. |
| `parameters` | Named generated parameters (counters, timestamps, VUser id, random values). |
| `init` | Actions that run once per virtual user before the main loop — logging in, for example. |
| `actions` | The main body each virtual user repeats: requests, checks, captures, and control flow. |
| `teardown` | Cleanup actions that run once per virtual user at the end. |

This HTTP example illustrates the full structure. Before running it, replace shop.example.com with an approved reachable test service, provide accounts.csv with username and sku columns, and configure the SHOP_PASSWORD project secret. For an example with no data or secret prerequisites, use [Your first test](HELP_CENTER.md#build-and-run-your-first-test).

```json
{
  "script": {
    "name": "Checkout_Flow",
    "version": "1.0.0",
    "protocol": "http",
    "config": {
      "base_url": "https://shop.example.com",
      "think_time": { "type": "normal", "mean_ms": 3000, "std_ms": 800 },
      "pacing": { "type": "fixed_interval", "interval_ms": 5000 },
      "http": { "connect_timeout_ms": 10000, "request_timeout_ms": 30000 },
      "run_logic": { "auto_transactions": "per_action" }
    },
    "data_sources": [
      {
        "name": "accounts",
        "type": "csv",
        "path": "accounts.csv",
        "strategy": "unique",
        "first_row_header": true
      }
    ],
    "init": [
      {
        "name": "Login",
        "type": "POST",
        "target": "/api/session",
        "headers": { "Content-Type": "application/json" },
        "body": "{\"user\":\"{{data.accounts.username}}\",\"pass\":\"{{secret.SHOP_PASSWORD}}\"}",
        "extractors": [
          { "name": "session_token", "source": "body", "type": "jsonpath", "expression": "$.token" }
        ],
        "assertions": [
          { "type": "status_code", "expected": 200 }
        ]
      }
    ],
    "actions": [
      {
        "name": "Browse_Catalog",
        "type": "GET",
        "target": "/api/products?page={{fn.random(1,20)}}",
        "headers": { "Authorization": "Bearer {{var.session_token}}" },
        "assertions": [
          { "type": "status_code", "expected": 200 },
          { "type": "response_time_ms", "max": 1500 }
        ],
        "think_time_after": true
      },
      {
        "name": "Add_To_Cart",
        "type": "POST",
        "target": "/api/cart/items",
        "headers": {
          "Authorization": "Bearer {{var.session_token}}",
          "Content-Type": "application/json",
          "Idempotency-Key": "{{fn.uuid()}}"
        },
        "body": "{\"sku\":\"{{data.accounts.sku}}\",\"qty\":1}",
        "assertions": [
          { "type": "status_code_range", "min": 200, "max": 299 },
          { "type": "body_contains", "value": "\"cart_id\"" }
        ]
      }
    ],
    "teardown": [
      {
        "name": "Logout",
        "type": "DELETE",
        "target": "/api/session",
        "headers": { "Authorization": "Bearer {{var.session_token}}" }
      }
    ]
  }
}
```

The editor is driven by an action catalog, so the available action types and their
options come directly from what the runtime supports. Templates and inline validation
help you build correct actions without memorizing the schema. The **DSL Action
Reference** document lists every action type and field, and is regenerated from the
catalog rather than maintained by hand.

### Capturing Values With Extractors

An extractor pulls a value out of a response and stores it in the `var` namespace so
later actions can use it as `{{var.name}}`.

| Extractor `type` | Reads from | Expression is |
| --- | --- | --- |
| `jsonpath` (`json_path`) | body | A JSONPath, e.g. `$.data.id` |
| `jmespath` | body | A JMESPath expression |
| `xpath` | body | An XPath expression |
| `css` | body | A CSS selector |
| `regex` | body or headers | A Go/RE2 pattern; capture group 1 wins |
| `regex_count` | body | A pattern; stores how many times it matched |
| `regex_ordinal` | body | A pattern; stores the Nth match |
| `boundary` | body or headers | Left/right boundary text (the LoadRunner-style capture) |
| `header` | headers | A header name |
| `sse_concat`, `sse_event` | body | Server-sent-event streams |

Useful extractor options: `on_miss` (`fail` to fail the request when nothing matches,
`empty` to assign an empty string), `mode: "all"` to save every match as
`name_1`…`name_N` plus `name_count`, `save_offset`/`save_length` to trim the value, and
`convert` (`url_encode`, `url_decode`, `html_decode`, `html_url_encode`).

### Checking Responses With Assertions

HTTP responses outside 200–399 fail by default even without assertions (see the [DSL operator guide](HELP_DSL_OPERATOR_GUIDE.md#correlate-and-check-responses) for the canonical statement). Assertions add checks for expected status, content and timing. HTTP supports:

| Assertion `type` | Fields | Passes when |
| --- | --- | --- |
| `status_code` | `expected` (int) | Status equals `expected` |
| `status_code_not` | `expected` (int) | Status does not equal `expected` |
| `status_code_range` | `min`, `max` | Status falls in the range |
| `response_time_ms` | `max` | Response time is at or under `max` |
| `body_contains` | `value` | Body contains the substring |
| `body_not_contains` | `value` | Body does not contain the substring |
| `header_contains` | `value` (header name), `expected` (substring) | The named header contains the substring |
| `body_regex_capture_equals` | `value` (pattern), `expected` | The first capture group equals `expected` |

### Transactions

There is no `transaction` action type, and you do not need one. Each action's **`name`
is its transaction label** in reporting, so naming actions well *is* naming your
transactions. To change the grouping, set `config.run_logic.auto_transactions`:

- `none` (default) — report on the action names as authored.
- `per_action` — wrap every action in its own transaction.
- `per_iteration` — treat one pass through `actions` as a single transaction.

Imported LoadRunner scripts keep their original transaction markers as
`source_transaction` actions, which start, end, or annotate named transactions.
Review the converted boundaries and reported measurements before comparing them
with the source run. For a new script, choose automatic transaction wrapping to
match the measurement scope you need.

### Control Flow

The engine has real control flow, valid in every protocol:

- `control_if` — run `control.steps` when a condition holds, `control.else` otherwise.
- `control_loop` — repeat for a fixed `count`, `for_each` row of a data source, or
  `while` a condition holds. Each pass exposes a 1-based `{{var.loop_index}}`.
  `do_while` runs the body once before the predicate is first evaluated.
- `control_break` and `control_continue` — exit or skip a pass of the nearest loop.
- `control_switch` — ordered cases; runs the first matching case, or the default
  branch when none match.
- `control_parallel` — run two or more named, isolated branches concurrently
  (`control.concurrency` defaults to 4, max 16; optional `fail_fast`). Variables
  extracted in a branch merge under `var.parallel.<branch>.*`.

Conditions accept either the legacy `left`/`op`/`right` form or a CEL `expr` over the
`var`, `data`, and `env` namespaces (`param` and `secret` are not available in `expr`).

Other engine actions worth knowing: `rendezvous` (hold VUsers until enough have
arrived, then release them together for a true concurrency spike), `cache_clear` and
`cookie_clear`, `http_connections_close`, and `error_message`.

### Think Time And Pacing

These two settings are what separate a realistic load model from a denial-of-service
attempt against your own staging environment.

- **Think time** is the pause a simulated human takes *between actions*. Configure it
  under `config.think_time` with a `type` (fixed, normal, uniform), a `mean_ms`, and
  optionally `std_ms`, `min_ms`, `max_ms`, a `multiplier`, and a `limit_ms` cap. Set
  `ignore: true` to skip think times entirely. Individual actions can override it, and
  `think_time_after: true` applies it once that action completes.
- **Pacing** governs how often a VUser starts a *new iteration*. `config.pacing.type`
  accepts `none`, `fixed_delay`, `random_delay`, `fixed_interval`, `random_interval`,
  and `tps`. Delay modes wait after the iteration ends; interval modes hold a steady
  start-to-start cadence; `tps` targets a throughput figure directly.

### Error Handling And Retries

`config.error_handling` controls what a VUser does when an action fails: `max_retries`
(0–10), `retry_delay_ms`, `retry_backoff` (`fixed` or `exponential`), and
`snapshot_on_error` to capture the request and response for the report. Use retries
deliberately — a retry that hides a real 500 turns a failed run into a slow one.

## Parameterizing And Data

Instead of hard-coding values, use template namespaces so a script behaves like a
real, varied user population. Templates are expanded in string fields at runtime.

| Namespace | Resolves to |
| --- | --- |
| `{{var.name}}` | A variable captured earlier in the script by an extractor, falling back to a named parameter. |
| `{{param.name}}` | A named parameter from the script's `parameters` section only. |
| `{{data.source.column}}` | A value from a bound data pool or data source, addressed as `source.column`. |
| `{{env.KEY}}` | A runtime environment value, including `-env key=value` overrides passed at launch. |
| `{{secret.NAME}}` | An application secret, falling back to the project secret of the same name. Values are never shown in the UI and are always redacted from run output. |
| `{{file.name}}` | The content of a file data source. |
| `{{fn.*}}` / `{{fake.*}}` | A generated value (see below). |

### Generated Values

The builtin function set is small and deliberate:

| Function | Produces |
| --- | --- |
| `{{fn.uuid()}}` (also `fn.uuid`, `fn.uuid.v4`, `fake.uuid`) | A random UUID |
| `{{fake.email}}` | A random address at `example.test` |
| `{{fn.timestamp.unix}}` | Seconds since the epoch |
| `{{fn.timestamp.unix_ms}}` | Milliseconds since the epoch |
| `{{fn.timestamp.iso8601}}` (also `fn.timestamp.rfc3339`) | An RFC 3339 timestamp |
| `{{fn.random(min,max)}}` | A random integer in `[min, max]` |
| `{{fn.random_string(n)}}` | A random string of length `n` |

That is the whole set. If you need richer synthetic data — names, addresses, plausible
order numbers — generate it into a CSV data pool rather than expecting a `fake.*`
function to exist for it. Anything outside this list resolves to nothing and will show
up as an unexpanded template in your request.

### Data Sources And Data Pools

A **data source** is declared inside the script; a **data pool** is a project-scoped,
reusable set of rows managed under [Repository → Shared data pools](#/repository?setting=test-data) and bound to a scenario.
Both are addressed the same way from a script: `{{data.name.column}}`.

The `strategy` field decides how VUsers walk the rows:

- `sequential` — each VUser takes the next row in order, wrapping at the end.
- `random` — each access picks a row at random.
- `unique` — selects a row by VUser within its assigned data partition; wraps if
  users outnumber available rows, so exclusive accounts require enough rows.
- `pop` — rows are consumed and not reused.

Rows can be overridden per execution-plan entry. A run receives an immutable, read-only
snapshot of the selected rows; execution does not update or consume rows in the shared
pool, so a rerun sees exactly the same data.

### Secrets

Application and project secrets back the `{{secret.NAME}}` namespace. Manage
shared application credentials in **Settings → Applications & releases → Application Secret Vault**
and project defaults under [Access → Project secrets](#/access?setting=secrets).
Scripts assigned to one application can use the same secret name. A managed run uses
the application selected for that run, even if a script is assigned elsewhere; direct
validation, replay, and debugging use the script's library application assignment.
An application value overrides a project value of the same name. Unassigned scripts
use project secrets. Rotating a secret changes its value for the next execution of
each affected script; a running test keeps the value pinned at its start, including
after continuity recovery. The UI shows metadata, status, and the effective scope,
never the values. Secret writes require an authorized human action.
Values resolved through `{{secret.NAME}}` are redacted from logs, snapshots, reports,
and archives regardless of any other setting.

Credentials that arrived *inside an imported script* are treated differently: the
script keeps them literally so it stays runnable, and whether they are redacted from
run output is a project setting (`log.redact_run_evidence`, off by default). Move them
into application or project secrets when you want them genuinely protected.

## Validating And Replaying

Before you run a script at scale, prove it works for a single user.

1. **Validate Script(s)** checks the script's structure, action fields, assertions, and settings
   without executing anything. It runs the same validation the runtime applies at
   admission. Launch separately checks current permissions, capacity, secrets, runner capabilities and governance; validation does not guarantee admission.
2. **Debug one iteration** replays the script once, interactively, through the
   controller's replay engine.

The replay debugger gives you:

- **Breakpoints** on a phase and action index, including structured control-flow leaves
  such as `loop[0]`, `then[0]`, or `requests[1]`.
- **Stepping** in three modes:
  - `into` — run one leaf action, then pause.
  - `over` — run through the current container or sub-request scope, then pause before
    the next action outside it.
  - `loop_end` — run to the end of the nearest enclosing loop, then pause after it.
- **Break on failure**, which pauses immediately after a failed action so you can
  inspect the request, response, variables, condition evidence, and logs before
  deciding to resume, step, or stop.
- **Log level** (`trace`, `debug`, `info`, `warn`, `error`) and a session export in
  JSON or raw text.
- For browser scripts, captured screenshots, DOM, console, network, and Core Web
  Vitals as evidence.

Script Editor keys: `Cmd/Ctrl` + `S` saves, `F5` starts or resumes replay,
`Shift` + `F5` stops, `F10` steps over, `F11` steps into, `Shift` + `F11` steps out,
`Cmd/Ctrl` + `B` toggles a breakpoint, and `Escape` closes the panel.

Validate first, replay second, then promote the script into a scenario.

## Recording And Correlation

If you would rather capture a real flow than write one by hand, use the recorder.

The **Recording Agent** is a loopback-only local bridge that lets the dashboard drive a
browser on your own machine, which is what you want whenever the controller runs
remotely or cannot launch a headed browser:

1. Install the `apexload-record-agent` package from the dashboard's **Download the
   APEX Load Recording Agent** link.
2. Open **Recording Studio** in the dashboard.
3. Copy the generated terminal command — the pairing token is already embedded in it.
4. Paste the whole command into Terminal or PowerShell.
5. Click **Verify Agent** to confirm the local bridge is reachable.
6. Click **Pair Local Bridge**.

The agent binds to `127.0.0.1` on a high port and does not normally need admin rights,
though locked-down workstations may need IT approval to run a signed binary, launch
browser automation, or allow private-network access from the dashboard origin to
loopback.

Once you have a recording:

- **Replay** the captured flow with self-healing selectors that tolerate small UI
  changes.
- **Correlation** finds the dynamic values (tokens, session IDs, view state) that must
  be captured from one response and replayed in the next request. **Correlation
  Diagnose** lets you review what was detected and see the evidence. To reuse the
  rules, open **Shared assets** on the **Scripts** page, discover a ruleset from the
  recorded script, then preview and apply it to other scripts.
- **Parameterization review** suggests which recorded literals should become data-pool
  or template values, so the script drives varied data instead of one fixed path.

The result is an executable script you refine in the Script Editor just like a
hand-authored one — and you should refine it. A raw recording replays one person's
exact session; a good script replays a population.

## Importing Existing Scripts

If you already have tests in another tool, import them instead of rewriting. Open the
**Scripts** page and use the import drop zone.

| Source | Category | Typical outcome |
| --- | --- | --- |
| OpenAPI / Swagger | Contracts & collections | Executable API draft |
| Postman collection | Contracts & collections | Executable API draft |
| Bruno collection | Contracts & collections | Converts supported HTTP requests into native scripts; review unsupported behavior before replay |
| WSDL / SOAP | Contracts & collections | Executable SOAP draft |
| AsyncAPI / Kafka | Contracts & collections | Messaging draft |
| RAML / Anypoint | Contracts & collections | API draft |
| HAR traffic (Salesforce traffic is auto-detected) | Traffic & samples | Executable HTTP DSL after auth and data review; a native Salesforce draft when detected |
| Recorder export / Playwright codegen | Traffic & samples | Executable browser script |
| Request / response sample | Traffic & samples | Executable single-request draft |
| LoadRunner script archive | Legacy & tool exports | Depends on the source protocol — see below |
| JMeter test plan | Legacy & tool exports | Executable |
| Akamai CloudTest Export | Legacy & tool exports | Review draft: Clip scripts and Composition scenarios need fixtures |
| k6 script | Legacy & tool exports | Executable (a constrained HTTP subset) |
| DevWeb source | Legacy & tool exports | Executable |
| Event Hubs capture | Messaging & cloud artifacts | Messaging draft |
| Broker config | Messaging & cloud artifacts | Messaging draft |
| Oracle E-Business / Siebel | Enterprise app reviews | Review draft |

Every import runs through a guided **review** flow rather than dumping a script on you.
It shows warnings, statistics, the detected protocol, and any sections that need a
human decision. Each imported script lands in one of four modes:

| Mode | Meaning | What to do |
| --- | --- | --- |
| `executable` | Converts straight to a runnable script | Review the warnings, then validate and replay |
| `review_only` | Preserved faithfully, but a human must decide how it should run | Work through the flagged fragments in the review panel |
| `external_runner_required` | Needs an external execution path; import support alone does not provide a runner | Check [Capabilities & limitations](HELP_CENTER.md#check-current-capabilities). Java VUser has a governed runner; SAP GUI has no standard runtime and Oracle NCA requires an approved bridge |
| `unsupported` | Nothing in the source maps to an executable action | Re-author, or import a different artifact |

The review flow reports back a promotion status — `ready_for_validation`,
`ready_with_warnings`, `review_required`, `external_runner_required`, or `unsupported` —
together with the next action for each one. Map source values, confirm secret bindings,
and save the reviewed draft.

Coverage for the big legacy sources is documented and evidence-backed rather than
claimed: see the [LoadRunner Coverage](LOADRUNNER_COVERAGE.md) reference (Web functions, Java `lr` functions, TruClient steps, Java Vuser imports) for exactly which
functions convert, which are no-ops, and which are preserved for review.

## Building A Scenario

A script says what one user does; a **scenario** says how many users do it, for how
long, and under what conditions. Build scenarios from the **Scenarios** page.

The scenario builder has five steps. **Start** takes the scenario's name and its script
groups, one row per group (an empty draft can instead copy a saved scenario).
**Model & shape** picks the load model: choosing Fixed VUsers or Stepped Ramp selects
its recommended variant, another variant can be picked instead, and **Next** moves on.
Goal-Driven asks for its targets in a sizing dialog, and Traffic Profile for its
demand source. **Run settings** has one row of tabs for target load, ramp pattern, run
phases, advanced settings, the browser and script runtime, and generator assignment;
then come **Data** and **Review**. Every step it has been through is marked done,
and **Save draft** is on every step. The builder warns, without stopping you, when
another scenario already has the name you type.

The scenario library has one row of controls: search, **Source**, and **Filters**,
which hold the archive state and the application and release filters (it shows how
many are set). Each row's action is **New run**; **More** holds **View runs** (or
click the row), **Edit**, which opens the scenario at Start, **Edit run settings** and
**Edit data**, which open it at those steps, and the rest. The **Workload** column
names each scenario's load model, and a name two scenarios share is marked
**Duplicate name** with each one's reference beside it.

A scenario brings together:

- **Groups** — each group runs one script with its own VUser count, ramp-up, ramp-down,
  and optional iteration cap. Use multiple groups to model a realistic mix, for example
  80% browse and 20% checkout. A group's `role` distinguishes a normal **load** group
  from an **observer** group (shown as **Observer canary**) — a browser experience
  canary that measures what a real user sees while the load groups generate the pressure.
- **Load model** — total VUsers, ramp pattern, and duration. Multi-stage groups let you
  express step loads (ramp to 100, hold, ramp to 300, hold) rather than a single slope.
  The load chart draws the plan as the engine runs it, one VUser at a time: a ramp-up
  starts the first VUser at once and the rest evenly through the ramp, a ramp stage
  adds its VUsers in equal steps with the first one interval in, and at the end of the
  duration each group stops its VUsers evenly through its ramp-down. The scenario
  builder, New run, Command Center and the run's results draw the same chart on
  elapsed time; during and after a run, the observed VUsers are drawn over it.
- **Runtime settings** — per-group options such as think time, pacing, network
  simulation, and IP spoofing (a pool of source IPs so the run presents many client
  addresses).
- **Placement** — which generators, regions, or load zones run each group, including
  weighted distribution across zones.
- **Application and release** — the scenario is assigned to the workspace's application
  and release; change it with **Change application/releases…** in the scenario library.
- **Shakeout** and **Resource baseline windows** — whether the run first proves the
  scripts with a canary, and whether it captures an idle resource baseline before load
  starts.

### Sizing From A Goal

If you know the throughput you need but not the VUser count, use the
**build-from-goal** (Little's Law) sizing panel. Give it a target throughput or
latency, and APEX Load reverse-calculates a recommended VUser count.

The arithmetic is worth understanding, because it tells you when the recommendation is
wrong: concurrency ≈ throughput × response time. Assume one transaction per iteration and no think time. At 50 TPS, a fixed 5 s start-to-start pacing interval requires about 250 VUsers while the work fits inside the interval. A 5 s post-iteration delay plus 400 ms response time instead requires 50 × 5.4 = 270 VUsers. Reducing that response time to 200 ms yields 260 VUsers, not half as many. Include think time and all actions when sizing an iteration, and distinguish request, transaction and iteration throughput. Resize from measured behavior under load. Always **validate the load model** before saving. The validator catches settings that
would fail at run time — an invalid source IP, a group referencing a script that no
longer validates, a duration shorter than the ramp.

## Launching A Test

Once you have a scenario, launch a run from the **New run** page (or directly from a
scenario).

1. Confirm your scope, then choose the scenario. Once you choose one (or arrive from
   the scenario builder or the library with one), the list folds away and the
   scenario's card shows its script groups, where **Edit settings** changes a group for
   this run only. **Change scenario** opens the list again; the card's **More** menu
   holds its history and Archive.
2. Confirm the target application and release — this is what the release trend keys
   off. A run in your workspace's application starts at your workspace's release,
   unless the scenario is assigned to other releases of that application; then you
   choose, with the assigned ones marked.
3. Apply any run-time overrides (base URL, environment values, data-pool selections).
   Monitor bindings, criteria, Elastic and Dynatrace capture and trace context are
   under **Monitoring for this run**, folded unless one needs attention.
4. Choose to start now or schedule a start time. If something blocks the launch, each
   reason you can fix on the page links to its field.
5. Start.

Three things can happen between "start" and "running":

- **Scheduled start.** Instead of starting now, pick a start time and a timezone. APEX
  Load stores the run as a reservation (`RESERVED`) or a scheduled run (`SCHEDULED`)
  and begins execution at the chosen window.
- **Shakeout / canary.** If the scenario requires a shakeout, APEX Load first runs a
  single-pass canary of each script group (`SHAKEOUT`) before committing the full load.
  A clean shakeout promotes the run automatically; a failing shakeout parks it in
  `SHAKEOUT_REVIEW` so you can inspect the evidence and either override and continue or
  cancel. This is the single most valuable safety net in the product: a broken script
  fails one canary user instead of a thousand.
- **Capacity preview.** Before a large run, APEX Load estimates the generator capacity
  it needs so a shortfall surfaces before the run starts rather than halfway through
  the ramp.

## Run States

The status badge shows the raw state name. These are the ones you will see:

| State | Meaning | What you can do |
| --- | --- | --- |
| `QUEUED` | Accepted, waiting for capacity or a start command | Start, abort |
| `RESERVED` | Holding a launch window that has not opened yet | Start, abort |
| `SCHEDULED` | Will start automatically at its scheduled time | Start, abort |
| `PENDING` | Awaiting admission | Abort |
| `STARTING` | Transient: the controller has accepted a start and is launching the run | Wait; abort if it does not advance |
| `SHAKEOUT` | Running the single-pass canary | Abort |
| `SHAKEOUT_REVIEW` | The canary failed; waiting for a human decision | Review evidence, override and continue, or cancel |
| `INITIALIZING` | Generators are starting and VUsers are being placed | Stop, abort |
| `RUNNING` | Under load | Pause, stop, abort, scale, adjust goal targets |
| `PAUSED` | Held between actions | Resume, abort |
| `STOPPING` | Graceful stop in progress — teardown and archive finalization | Abort |
| `COMPLETE` | Finished normally | Read the report |
| `FAILED` | Finished with an execution failure | Read the report and the error evidence |
| `ABORTED` | Force-cancelled | Read whatever was archived |

`COMPLETE`, `FAILED`, and `ABORTED` are terminal. Note the spelling: the terminal
success state is `COMPLETE`, not `COMPLETED`.

## Monitoring A Live Run

Open the **Command Center** to see active runs in your selected project, application,
and release. With no release selected, the overview includes all releases in that
scope. Search by name or reference, filter by status, and sort by attention, newest,
or name. Each row shows run status, waiting reasons, load, throughput, cumulative
error percentage, timing, and metric freshness. Elapsed and remaining time leave out
time spent paused, which a run makes up after it resumes; remaining time is an
estimate, and a paused run shows Paused instead.

When there are no active runs, choose **New run** or **Browse scenarios**. New run
requires permission to manage tests. Select a release before opening Results; if
none exists, open the Repository to configure the workspace. A failed refresh shows
an error and Retry instead of claiming the scope is empty; previously loaded runs
remain visible with their last update time.

Select a run to open its live controls. The run ID stays in the URL for reloads and
sharing. Use **Current run** to switch runs or **All active runs** to return to the
overview. Staged target edits are retained separately for each run while this
app session remains open; reloading clears those unsaved edits. The selected
run streams metrics over WebSocket, while the overview refreshes periodically.

From the Command Center you can also control the run while it is in flight:

| Control | Available when | Effect |
| --- | --- | --- |
| **Start** | `QUEUED` or `RESERVED` | Begins execution now |
| **Pause** | `RUNNING` | Holds every VUser between actions. Paused time does not count against a run's duration: the run makes it up after it resumes, so it ends that much later |
| **Resume** | `PAUSED` | Releases the hold |
| **Stop** | `RUNNING`, `INITIALIZING`, or `PAUSED` | Graceful: finishes the action in flight, skips later actions and think time, runs teardown. The run stays `STOPPING` until generator teardown and archive finalization complete |
| **Abort** | Any non-terminal state | Force-cancels active work immediately |
| **Adjust run** | `RUNNING` | Changes the remaining duration and each group's load — its VUser count, or its throughput (TPS) target in goal-oriented runs — instantly or over a ramp |

Pause and Stop become unavailable once a run enters `STOPPING` — from there only Abort
remains.

Each of these asks for confirmation first, and so does a group's **Apply** in the
group load panel: its dialog names the change and what it does to the run (for
example "Store_Browse 2 → 4 VUsers · run total 6 → 8 VUsers"). A run that has not
started takes target edits without asking. Once the page scrolls past the run
controls, they fold into one row (the run, its load, throughput, time and errors,
and the controls) that stays at the top.

Watch throughput, response-time percentiles (p50/p95/p99), and error rate together.
Live throughput and error rates count requests once the run has sent any, as the
run report does: a script without transactions still sends requests, and the
transaction that wraps each of its iterations fails whenever any request in it
does. A run that sends no requests is counted in transactions, and a
goal-oriented run's throughput stays in TPS, the unit of its targets. Every label
names what it counts and its window: rates are averaged over the last 10 s, error
rates and counts are run to date, and the **Transactions** table lists each
transaction's own TPS and the last sample's counts. When request counts would
cover only part of a run — a generator that predates them is in the fleet, or
another controller took the run over from stored history — they are left out
rather than shown as run to date, and the page counts that run in
transactions.
When errors climb, drill into the live error evidence to see representative failures
rather than guessing — a 500 from your application and a connection reset from an
exhausted generator look identical in an error-rate chart and call for opposite
responses.

## Scheduling Recurring Runs

To run a scenario on a cadence — a nightly regression, a weekly soak — use scheduling.

1. Go to **New run** and switch to the **Schedules** view.
2. Pick a stored scenario.
3. Define when it should run with the visual **cron builder**: a five-field editor over
   a standard cron expression, so you do not have to remember cron syntax.
4. Set the schedule's **timezone**. Schedules fire in their declared timezone, not UTC,
   so a schedule set to 02:00 Europe/London fires at 02:00 local time across daylight
   saving changes.
5. Save. Depending on governance settings, a new schedule may enter `pending_approval`
   and wait for sign-off before it becomes active.

The **run calendar**, under **Monitoring → Schedules**, shows upcoming and recent
scheduled runs with their scenario labels and release context, so you can see the
week's load at a glance and spot two teams booked onto the same environment.

## Chaining Runs With Execution Plans

When one scenario is not enough — say you want a baseline, then a spike, then a soak,
in order — use an **execution plan**. Manage plans from the **Scenarios** page.

A plan groups several scenario entries with per-entry runtime and data-pool overrides
and a run mode (parallel or sequential), and runs them as one coordinated unit. A
typical release-qualification plan runs sequentially:

- **Baseline** — the standard scenario at last release's VUser count, to produce a
  comparable number.
- **Spike** — a short, steep ramp to two or three times baseline, to find the breaking
  point and confirm recovery.
- **Soak** — baseline load held for hours, to surface leaks and connection-pool
  exhaustion that a ten-minute run never shows.

Plans can carry governance approvals, so a gated plan waits for sign-off before it
proceeds.

## Reading Results And Reports

Completed runs live under **Results**. Open one to reach its **Run Report** — the
detailed post-run analysis.

A run report gives you:

- **Charts and metric breakdowns** — throughput, response times, errors, and resource
  usage over the run, per transaction and per group.
- **Verdict / pass-fail** against the scenario's SLA gates. Authorized users can
  override a verdict; overrides are audit-logged with the reason.
- **Load comparison** — compare this run against one or more other runs to see what
  changed.
- **Release trend** — how this run's key transactions compare across releases of the
  same application, so regressions surface early.
- **Root-cause analysis** — the run's validity, where its series changed, and
  the ranked likely causes, generated from the archived evidence; see
  [Root-Cause Analysis](#root-cause-analysis).

### Interpret The Outcome

COMPLETE is a lifecycle state: execution ended. Check whether SLA evaluation ran and whether its verdict passed, failed or lacks enough evidence. A technically completed run can breach its SLA; missing evaluation is not an automatic pass.

Action failures describe unsuccessful work, such as HTTP 4xx/5xx, failed assertions or transport problems. SLA gates evaluate aggregate performance thresholds. Read both, and compare request/transaction counts before comparing error rates.

Request throughput counts protocol requests; transaction throughput counts named reporting units; iteration throughput counts full passes through the script. One iteration may produce several requests or transactions, so do not interchange these units when sizing load.

Percentiles describe the distribution: p95 means 95% of the measured samples were at or below that latency. Compare p95/p99 alongside sample counts, throughput, error rate and test phase. A percentile based on a handful of observations is not a stable baseline.

For baseline comparisons, hold target environment, release context, script version, data, load shape, runtime settings and measurement windows constant where possible. Explain intentional differences. Separate warm-up/ramp behavior from steady state, and inspect generator saturation before blaming the target.

The run's validity verdict does that check: `suspect` when a load generator stayed saturated at a steady load, `invalid` when results were lost (an OOM-killed generator, dropped results, shed arrivals), and `unknown` when generator statistics are missing. A closed-model run whose throughput falls while latency rises stays valid; that is the system under test, reported as an anomaly. See [run validity](ANALYSIS_WORKFLOWS.md#run-validity) for the rules.

### Report audiences

A report is assembled from a registry of sections, selected by audience. The
run report's **Report audience** control (Executive, Engineer, Audit) chooses
the audience for the page and for every export from it, including a baseline
comparison's exports; the page opens on Executive. API and `apexctl` requests
that name no profile get the Engineer report (`profile=engineer`).

| Audience | Contains | Export formats |
| --- | --- | --- |
| **Executive** | Acceptance assessment, executive summary, performance analysis, imported dashboard panels, SLA scorecard, release trend, top risks, generated root cause (leading hypothesis), run context and confidence, recommendations, sign-off | HTML, JSON, CSV, analysis ZIP, PDF, DOCX |
| **Engineer** (API default) | Generated root-cause analysis, findings, data sources and glossary, generator assignment manifest, evidence coverage, schema compatibility, errors, resources, actions, browser evidence, SOAP, network phases and more | HTML, JSON, CSV, analysis ZIP, PDF, DOCX, JSONL, Parquet |
| **Audit** | Acceptance assessment, run identity, authorization and sign-off, governance and provenance, evidence tiers, evidence coverage, change attribution, artifact integrity, report authorship | HTML, JSON, CSV, analysis ZIP, PDF, DOCX |

Section names are the same in every format and audience. A section that does
not apply to the run (browser evidence for a run without browser VUsers, say)
is left out of the HTML, PDF and Word documents and named under **Not
captured for this run** in the contents; the CSV keeps every section.

The Export panel offers **Navigable HTML**, **Print-ready PDF**, **Editable
Word**, **Data CSV** and **Analysis tables (ZIP)**.

PDF is offered only for profiles that declare it; a PDF request against a profile
without it is refused rather than silently downgraded.

### Release readiness and root cause on the report

A run without acceptance criteria has no pass/fail verdict, so the report's
Summary shows **Release readiness** instead: how the run compares with the
previous run in its release (the release trend), its top risks (your saved
findings, or the ones derived from the run), and its evidence coverage.

Where the project presents root cause, the Summary's root-cause line names the
leading hypothesis with its confidence; **Investigate root cause** opens it in
Diagnostics, and **Review changes** opens the run's **Largest changes** when no
cause is supported. In a baseline comparison, **Analyze root cause against
this baseline** re-analyzes the run against the one selected baseline.

The generated root cause (**Root-cause analysis**) and the **Findings** are
separate sections. Findings are yours: the ones you curate in the report
composer, or the ones derived from the run when you curate none. The
root-cause section is generated: a project that presents root-cause v2 shows
the latest completed analysis, ranked hypotheses with their confidence, the
evidence that would contradict or disprove each, and its limitations;
otherwise it shows the v1 summary. Every format renders it from the same
model, and the analysis ZIP adds `root_cause_hypotheses.csv`.

The Executive and Engineer HTML, PDF, and Word exports include the archived
throughput, P95 latency, error-rate, active-VUser, and action-latency charts
when those data are available; the action-latency chart draws the 20 busiest
actions and says so when a run has more. The printable files also include a compact
interval table. Audit exports omit performance charts by profile design.

`JSONL` and `Parquet` are not rendered report documents — they are the run's raw
archive datasets (transactions, resource samples, interval summaries), downloaded per
artifact for analysis in your own tooling.

The **Analysis tables** ZIP contains row-based CSV files with stable `run_id` fields
and a data guide. Executive and Engineer reports include run summary,
transaction SLA, findings, and evidence-gap tables; Audit includes run summary,
artifact integrity, and evidence-gap tables. The existing four-column CSV
(`section,key,metric,value`) remains available for integrations using that contract.

### Baselines

Pin a run as a **comparison baseline** for its release so future runs are measured
against a known-good reference. A baseline plus the release trend is what turns "the
API feels slower" into "checkout p95 moved from 420 ms to 890 ms between 4.2 and 4.3".

## Root-Cause Analysis

Root-cause analysis reads a run's archive and its monitor evidence and answers
three questions: could the load generators and telemetry carry the reported load
(**run validity**), which series changed and when (**anomalies**, judged against
the run's own steady load levels and, when you choose one, a baseline run), and
which known failure patterns explain the symptoms (**hypotheses**, ranked, each
with its confidence, the evidence that contradicts it and the check that would
disprove it). Everything it shows is generated; your own findings and notes stay
separate.

A project administrator turns it on under
[Settings → Project analysis](#/settings?setting=analysis): computing analyses,
showing them in summaries and reports, analyzing every archived run
automatically, and capturing provider evidence at archive time. A deployment
can also turn these on for a project; the settings page marks those flags.

**Where you see it.** The run report's Summary has a **Root cause** cell in Run
outcome: the run's validity, then the leading hypothesis, "no cause supported"
with the number of changes, "too little evidence", or the analysis state while
one is queued or running. Its button opens the leading hypothesis, the run's
largest changes, or the analysis in **Diagnostics**. Results rows and the
dashboard's recent runs show validity and the leading hypothesis; the dashboard
flags invalid and suspect runs and found causes, and counts invalid and suspect
runs in Release quality. A finished run's Command Center banner states the root
cause too. A suspect or invalid run also appears among the evidence limitations
to review before acceptance.

**Reading the Root cause panel.** The panel says whether the analysis was
automatic or requested, the run's validity, how many series it judged, and a
sentence on how to read the outcome:

- **Supported hypotheses** — open one with **Investigate** to see its symptoms
  and causes on the run's timeline (with onset uncertainty, load steps and
  suspect spans), the symptom's transaction statistics, exemplars with trace
  links, links that open the same window in your APM or log tools, the cause
  series and the matching error patterns.
- **No supported hypothesis** — the panel still draws the run's largest changes
  on the timeline; focus one to look closer. Check **Evidence coverage** for
  what could not be judged: a monitor source that left a gap links to its
  archived evidence.
- **Insufficient evidence** — too few series were measured. Coverage lists what
  is missing; turning on trace context, binding monitor sources or running
  longer at each load level usually helps.

The same marks appear on the **Performance** timeline, where selecting one
opens its hypothesis, and error patterns name the hypotheses they overlap.

**Acting on a hypothesis.** Editors can **Re-analyze** (optionally capturing
evidence afresh, or against the baseline open in **Compare**; **Baseline
Comparison** offers the same), **Record as finding** (adds it to the current
audience's report findings with where it came from), **Annotate timeline** (adds
a run annotation at its earliest symptom) or **Add test note** (opens a note
drafted from it with its link). **Copy link** copies a link that reopens the
same analysis, hypothesis and window for anyone with access to the project.

**Notifications and CI.** Notification routes can subscribe to *Root-cause
analysis completed* and *Run validity suspect or invalid*. CI runs report the
root cause beside validity; see
[Root-cause summary](CICD_INTEGRATION.md#root-cause-summary). The engine, its
rules and its limits are described in
[Root-cause analysis service](ANALYSIS_WORKFLOWS.md#root-cause-analysis-service).

## Repository And Shared Assets

The **Repository** page is where you manage the structural hierarchy — applications,
releases and projects — and the project's shared test data.

What scripts share lives on the **Shared assets** tab of the **Scripts** page, where
you pick scripts by name:

- **Object repositories** hold named page elements that browser scripts reference.
  Seed one from a script's recorded elements, and optionally point that script's
  actions at it. A changed selector is then fixed once rather than in fourteen scripts.
- **Correlation rulesets** hold the dynamic-value rules learned from one recording.
  Preview a ruleset against another script before applying it; applying saves a new
  script version.

## Settings Reference

Configuration uses task destinations. Use the destination search and check the scope
shown by the page before changing a value. Visibility and editing depend on your
capabilities; a help link does not grant permission.

| Task | Destination |
| --- | --- |
| Theme, editor wrapping, table rows and reduced motion | [Settings → Personal preferences](#/settings?setting=preferences) |
| Controller configuration and deployment limits | [Controller & runtime](#/settings?setting=platform), [Edition & deployment limits](#/settings?setting=entitlements) |
| Domains, projects and application releases | [Domains & placement defaults](#/settings?setting=domains), [Projects](#/settings?setting=projects), [Applications & releases](#/settings?setting=applications) |
| Project limits and usage | [Project limits & quotas](#/settings?setting=limits), [Usage accounting](#/settings?setting=usage) |
| Generator connections and capacity | [Kubernetes clusters](#/settings?setting=clusters), [Generator pools](#/settings?setting=pools), [Standing allocations](#/settings?setting=allocations) |
| Alert thresholds and stop policy | [Alert & stop policies](#/settings?setting=thresholds) |
| Root-cause analysis and business-key lookups for a project | [Project analysis](#/settings?setting=analysis) |
| Schedule behavior and notification delivery | [Schedule defaults](#/settings?setting=scheduling), [Notification routes](#/settings?setting=notifications) |
| Archive and recording retention | [Archive & HAR retention](#/settings?setting=retention) |
| Shared input rows | [Repository → Shared data pools](#/repository?setting=test-data) |
| Authentication, import policy, redaction and secrets | [Access → Authentication](#/access?setting=authentication), [Import credential policy](#/access?setting=imports), [Project redaction rules](#/access?setting=redaction), [Project secrets](#/access?setting=secrets) |

Alert/stop policy affects runtime decisions; a run's SLA verdict and lifecycle status
remain distinct. Shared data pools provide snapshots to runs, as described in
[Parameters, data & secrets](APPLICATION_USER_GUIDE.md#parameterizing-and-data).
See [Administration](ADMIN_USER_GUIDE.md) for scoped roles and operational responsibilities.

## Automating APEX Load

Everything you can do in the dashboard is backed by a REST and WebSocket API.

### The `apexctl` Command Line

`apexctl` runs tests locally or through a controller, and is the usual way to gate a
pipeline:

```bash
apexctl run --script order-flow.json --vusers 100 --duration 5m
```

```bash
apexctl script validate --script order-flow.json --format json
```

```bash
apexctl run --script order-flow.json --p95-latency-ms 800 --max-error-pct 2.5 --junit results.xml
```

| Command | Does |
| --- | --- |
| `run` | Execute a performance test, locally or via `--api` |
| `ci` | Run a CI performance gate locally or through a controller |
| `script` | Validate or explain a JSON DSL script |
| `status` | Print a test's current status and SLA verdict |
| `wait` | Block until a started test finishes, then report the verdict |
| `archive` | Inspect an archive; `verify` checks integrity and `evidence` captures a pinned final result |
| `record` | Record a browser script with the embedded recorder |
| `record-agent` | Start the localhost browser recording bridge |
| `github` | Inspect and run GitHub Enterprise sync/check workflows |
| `import` | Import Bruno collections or Grafana dashboards |
| `library` | Export or import library bundles (scripts, scenarios, results) |
| `version` | Print the `apexctl` version |

### Gating A Pipeline

Exit codes are designed so a non-zero exit fails the build with no extra scripting:

`apexctl` uses one exit-code contract:

| Code | Meaning |
| --- | --- |
| `0` | Success: the run passed, or the test reached a terminal state with no failed status and no SLA breach |
| `1` | Test or SLA failure: an evaluated SLA breach, terminal status `FAILED`/`ABORTED`, or (for a local `apexctl run`) an engine execution error |
| `2` | Usage or configuration error: bad flags or arguments, an unwritable `--junit` path, or (for `ci run`) a configuration error |
| `3` | Controller or transport failure: the controller was unreachable or returned an error, `status` could not fetch results, or `wait` timed out before a terminal state |

`apexctl ci run` maps its outcome the same way: passed is `0`, SLA breach or a run the
project's [validity gate](CICD_INTEGRATION.md#run-validity-gate) fails (`run_invalid`) is `1`,
configuration error is `2`, and any other outcome (execution, timeout, cancellation)
is `3`. A controller-backed `run --api` that hits an observation error is `3`.

Add `--format json` for a machine-readable result object (`status`, `sla_evaluated`,
`sla_breached`, `sla_details`, `transactions`, `errors`, `tps`, `duration_ms`), and
`--junit <path>` for a JUnit XML report your CI test reporter can render. CLI threshold
flags take precedence over thresholds declared in the script's goal config. The
[CI/CD Integration](CICD_INTEGRATION.md) reference has copy-ready GitHub Actions gates for both
GitHub.com with OIDC and GHES with a scoped API key.

### The API And The MCP Server

- The [API overview](API_USER_GUIDE.md) and [API authentication](API_USER_GUIDE.md#authentication) articles explain the base URL,
  first API calls, service-account keys, and how to authenticate.
- The [API Reference](#/swagger) is the generated route reference.
- The [MCP setup](../mcp/README.md) article covers agent-driven automation. The MCP bridge uses
  service-account auth and adds confirmation guardrails — mutating calls require explicit confirmation, and destructive/admin calls additionally require a reason — with tools covering system, auth, projects,
  scripts, scenarios, runs, reports, scheduling, fleet, data pools, imports, and AI
  assistance.

## Integrations

| Integration | What it gives you | Configured under |
| --- | --- | --- |
| **Prometheus** | Controller and generator metrics scraped into your own monitoring | Settings → Integrations → Prometheus |
| **Grafana** | Import an existing Grafana dashboard so live run panels appear alongside APEX Load metrics | Settings → Integrations → Dashboards |
| **Power BI** | Per-run publishing to a push dataset for business reporting | Settings → Integrations → Power BI |
| **Dynatrace** | Native SaaS Classic/Grail test monitoring; separate webhook notifications | [Monitoring setup](DYNATRACE.md), Settings → Integrations → Notifications |
| **OpenTelemetry** | Opt-in OTLP traces from standalone controller and generators; metrics remain Prometheus, with no native OTLP metrics/log export | Deployment configuration |
| **GitHub / GHES** | Script sync between a repository and a project, plus pull-request checks | Settings → Integrations → GitHub |
| **Kubernetes clusters** | Register AKS/EKS/generic control planes so an administrator can place generator pools on them with per-pool CPU, memory, and node placement | Admin cluster registry |

Each has its own reference document: [Prometheus metrics](OBSERVABILITY_INTEGRATIONS.md#prometheus-scrape-endpoint), [Power BI](OBSERVABILITY_INTEGRATIONS.md), [Dynatrace webhooks and monitoring](OBSERVABILITY_INTEGRATIONS.md), [OpenTelemetry](OBSERVABILITY_INTEGRATIONS.md#opentelemetry-tracing), [GitHub integration](CICD_INTEGRATION.md#github-enterprise-integration), and [Cluster connections](CLUSTERS_AND_CLOUD.md#cluster-connections-host-resource-allocation).

## Troubleshooting And FAQ

**My script or run has disappeared.**
Almost always a scope mismatch. Check the domain, project, and application/release in
the sidebar scope selector. Scripts and results are project-scoped, so a script created
under one project is invisible from another.

**A template appears literally in my request, like `{{fn.timestamp()}}`.**
The namespace or function does not exist. Check it against the namespace and function
tables above — the builtin set is small, and anything outside it resolves to nothing.
`fn.timestamp()` in particular is not a function; use `fn.timestamp.iso8601`,
`fn.timestamp.unix`, or `fn.timestamp.unix_ms`.

**`{{var.something}}` is empty after the first action.**
The extractor did not match. Set `on_miss: "fail"` on the extractor so the action fails
loudly instead of quietly carrying an empty value into every later request, then replay
and inspect the response body.

**My run says `external_runner_required` and will not launch.**
Check the import disposition and [Capabilities & limitations](HELP_CENTER.md#check-current-capabilities). Java VUser requires its configured governed sidecar. SAP GUI has no registered runtime in standard binaries, and Oracle Forms/NCA requires an approved bridge; enabling a pool setting alone cannot supply either missing execution path.

**The shakeout failed and my run is parked.**
The run is in `SHAKEOUT_REVIEW`. Open it, read the canary evidence, and fix the script —
that is the shakeout doing its job. If you are certain the failure is environmental, you
can override and continue.

**The launch was refused for capacity.**
The scenario needs more generator capacity than is currently available. Run the
capacity preview, lower the VUser count if that still meets your objective, or ask an administrator
about the generator fleet and reservations under **Capacity planning**.

**A PDF export was refused.**
PDF is declared per report profile. Switch to a profile that offers it, or export HTML.

**Response times look fine but the error rate is high.**
Check whether the errors are application errors or generator-side failures. Fast
responses plus many errors usually means the target is rejecting requests cheaply — an
auth failure or a rate limit — which is not a performance result at all.

**Numbers differ between two runs of the same scenario.**
Confirm both ran against the same release, with the same data-pool selection and the
same runtime overrides. Then compare them directly with load comparison rather than by
eye.

## Glossary

- **Abort** — force-cancel a run immediately, without graceful teardown.
- **Baseline** — a run pinned as the reference for its release, used by load comparison
  and release trend.
- **Correlation** — capturing a dynamic value from one response so a later request can
  replay it.
- **Data pool** — a project-scoped, reusable set of rows bound to a scenario.
- **Generator** — a worker that drives VUsers. Runs are distributed across the fleet.
- **Group** — one script plus its VUser count and ramp inside a scenario.
- **Load zone** — a placement region; groups can be distributed across zones by weight.
- **Observer group** — a small browser canary that measures the experience a real user
  gets while load groups generate the pressure.
- **Pacing** — how often a VUser starts a new iteration.
- **Ramp-up** — the period over which a group's VUsers are added.
- **Rendezvous** — a synchronization point that holds VUsers until enough arrive, then
  releases them together to create a true concurrency spike.
- **Replay** — a single-user, interactive execution of a script, with a debugger.
- **Shakeout** — a single-pass canary of each script group before the full load starts.
- **SLA gate** — a threshold that produces a run's pass/fail verdict.
- **Think time** — the pause a simulated human takes between actions.
- **Transaction** — a named, timed unit of work. Reporting is per transaction.
- **VUser** — one simulated concurrent client.

## Tips For Getting Good Results

- Set your **scope** (domain, project, release) before you start looking for anything.
- **Validate and replay** a script for one user before running it for thousands.
- Give every action a **meaningful name** — that name is your transaction label, and
  you cannot rename it after the fact in an archived report.
- Add at least one **assertion** per action. HTTP 4xx/5xx already fail by default; assertions also verify the expected business response.
- Use **think time and pacing** deliberately. A script with neither is a load generator
  pointed at your own infrastructure, not a model of user behavior.
- Import existing tests through the **review** flow rather than trusting a raw
  conversion — confirm warnings and secret bindings.
- Use a **shakeout** on important runs so a broken script fails one canary user, not
  the whole fleet.
- **Pin a baseline** per release and lean on load comparison and release trend to catch
  regressions early.
- Prefer **data pools and secrets** over hard-coded values so your load looks like real,
  varied traffic — and so no credential ends up in a report.
- Watch **throughput, latency percentiles, and error rate together**. Any one of them
  alone will mislead you.

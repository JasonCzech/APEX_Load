# APEX Load User Guide

APEX Load is a Kubernetes-native performance and load testing platform. This guide
walks you through the application end to end — from signing in and finding your way
around, to authoring a test script, assembling a scenario, launching a run, watching
it live, and reading the results.

It is written for the people who run performance tests: engineers, SDETs, and
performance analysts working in the dashboard. For administrative tasks (managing
users, roles, the generator fleet, and system settings) see the **Admin Guide** tab.
For automating APEX Load from CI/CD or scripts, see the **General**, **Quick Start**,
**Authentication**, and **Endpoint Map** tabs, plus the **Swagger** reference.

If you have never used APEX Load before, jump to **Quick Start: Your First Test** and
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

This is the shortest honest path from an empty workspace to a report. Budget about
fifteen minutes.

1. **Set your scope.** At the top of the left sidebar, choose a **Domain**, then a
   **Project**, then an **Application/Release**. Nothing else in the app makes sense
   until this is right — scripts, scenarios, and results are all filtered by it.
2. **Create a script.** Go to **Scripts** and create a new script, or drop an existing
   HAR/Postman/JMeter/LoadRunner export into the import drop zone. For a first pass,
   a two-action HTTP script against a known-good endpoint is enough.
3. **Point it at your target.** In the Script Editor, set `config.base_url`, then add
   one action per request. Give each action a meaningful `name` — that name is the
   transaction label you will see in the report.
4. **Add at least one assertion.** An action with no assertion can only fail on a
   transport error. `status_code` plus `response_time_ms` is the minimum that makes a
   run meaningful.
5. **Validate.** Click **Validate**. This checks structure and settings without
   sending a single request. Fix everything it reports before going further.
6. **Replay once.** Click **Replay** to execute the script for exactly one user. Step
   through the actions and confirm each request sent what you expected and each
   response was what you assumed. This is where nine out of ten scripting mistakes
   surface.
7. **Build a scenario.** Go to **Scenarios**, create one, and add a single group:
   your script, a small VUser count (10 is plenty for a first run), a short ramp-up
   (30s), and a short duration (2m). Run **Validate the load model**.
8. **Launch.** Go to **New Test**, pick the scenario, confirm the target application
   and release, and start.
9. **Watch it.** The run opens in **Command Center**. Watch throughput, the p95/p99
   response times, and the error rate together — a fast run with a 40% error rate is
   not a fast run.
10. **Read the report.** When the run reaches `COMPLETE`, open it from **Results**.
    Check the verdict, then the per-transaction breakdown.
11. **Pin a baseline.** Pin this run as the comparison baseline for the release, so
    every later run has something to be measured against.

From here, the rest of this guide fills in each of those steps properly.

## Signing In And Getting Oriented

How you sign in depends on how your deployment is configured — typically single
sign-on (OIDC) or an authentication gateway. Once you are in, the **left sidebar** is
your main map:

| Sidebar item | What it is for |
| --- | --- |
| **Dashboard** | Live overview of tests and recent activity. |
| **New Test** | Launch a run from a scenario, and manage run schedules. |
| **Command Center** | The live cockpit for a single running test. |
| **Results** | Browse completed runs and their reports. |
| **Scripts** | Author, import, and manage test scripts. |
| **Scenarios** | Assemble scripts into runnable load models, and manage execution plans. |
| **Repository** | Manage applications, releases, projects, and shared assets. |
| **Monitoring** | System health, telemetry, the generator fleet, and the run calendar. |
| **API Guide** | The page you are reading, plus the API and Swagger references. |
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
| `g` `n` | New Test |
| `g` `s` | Scripts |
| `g` `r` | Results |
| `g` `m` | Monitoring |
| `g` `f` | Fleet |
| `g` `h` | API Guide |
| `g` `,` | Settings |
| `g` `a` | Access (when you have access to it) |

Global keys:

- `Cmd/Ctrl` + `K` (or `Ctrl` + `Shift` + `P`) opens the **command palette**, which
  searches pages and actions. It works even while you are typing in a field.
- `?` opens the shortcut help panel, which also lists the shortcuts that only apply to
  the page you are on.
- `Backspace` goes back; `/` focuses the current page's search or filter box.
- `Cmd/Ctrl` + `=` / `-` / `0` zoom the interface in, out, and back to normal.

## The Dashboard

The **Dashboard** is your landing page and live overview. Use it to:

- See which tests are running right now and their status at a glance.
- Watch real-time metric history — throughput, response time, and error rate — for
  active runs.
- Review the release trend, so you can tell whether the latest build is faster or
  slower than the last.
- Jump straight into a run's **Command Center** or open a finished run's report.

Treat the Dashboard as mission control: it is the fastest way to confirm a run is
healthy and to reach the detail views when it is not.

## Authoring A Script

Scripts live under the **Scripts** page. From there you can create a new script,
open an existing one in the **Script Editor**, clone one, or import from an external
format.

### Script Anatomy

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

Here is a complete, runnable HTTP script showing the shape of all of it:

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

An action with no assertion only fails on a transport error. HTTP supports:

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

Imported LoadRunner scripts keep their original `lr_start_transaction` markers as
`source_transaction` actions. Those preserve provenance for review; they are not the
authoring primitive you should reach for in a new script.

### Control Flow

The engine has real control flow, valid in every protocol:

- `control_if` — run `control.steps` when a condition holds, `control.else` otherwise.
- `control_loop` — repeat for a fixed `count`, `for_each` row of a data source, or
  `while` a condition holds. Each pass exposes a 1-based `{{var.loop_index}}`.
  `do_while` runs the body once before the predicate is first evaluated.
- `control_break` and `control_continue` — exit or skip a pass of the nearest loop.

Conditions accept either the legacy `left`/`op`/`right` form or a CEL `expr` over the
`var`, `data`, `env`, `param`, and `secret` namespaces.

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
| `{{secret.NAME}}` | A project secret. Values are never shown in the UI and are always redacted from run output. |
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
reusable set of rows managed under **Settings → Data Pools** and bound to a scenario.
Both are addressed the same way from a script: `{{data.name.column}}`.

The `strategy` field decides how VUsers walk the rows:

- `sequential` — each VUser takes the next row in order, wrapping at the end.
- `random` — each access picks a row at random.
- `unique` — each VUser gets its own row, so no two users share an account.
- `pop` — rows are consumed and not reused.

Rows can be overridden per execution-plan entry. A run receives an immutable, read-only
snapshot of the selected rows; execution does not update or consume rows in the shared
pool, so a rerun sees exactly the same data.

### Secrets

**Project secrets** back the `{{secret.NAME}}` namespace, managed under
**Settings → Projects & Compliance → Project Secrets**. The UI shows only their metadata
and status — never the values — and secret writes require an authorized human action.
Values resolved through `{{secret.NAME}}` are redacted from logs, snapshots, reports,
and archives regardless of any other setting.

Credentials that arrived *inside an imported script* are treated differently: the
script keeps them literally so it stays runnable, and whether they are redacted from
run output is a project setting (`log.redact_run_evidence`, off by default). Move them
into project secrets when you want them genuinely protected.

## Validating And Replaying

Before you run a script at scale, prove it works for a single user.

1. **Validate** checks the script's structure, action fields, assertions, and settings
   without executing anything. It runs the same validation the runtime applies at
   admission, so a script that validates will not be rejected at launch.
2. **Replay** runs the script once, interactively, through the controller's replay
   engine.

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
- **Log levels** `trace`, `debug`, `info`, `warn`, `error`, and a session export in
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
  be captured from one response and replayed in the next request. The **Correlation
  Diagnose** and **ruleset** panels let you review what was detected, see the evidence,
  and tune the rules.
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
| Bruno collection | Contracts & collections | Executable, bound to the Bruno source bundle |
| WSDL / SOAP | Contracts & collections | Executable SOAP draft |
| AsyncAPI / Kafka | Contracts & collections | Messaging draft |
| RAML / Anypoint | Contracts & collections | API draft |
| HAR traffic | Traffic & samples | Executable HTTP DSL after auth and data review |
| Recorder export / Playwright codegen | Traffic & samples | Executable browser script |
| Request / response sample | Traffic & samples | Executable single-request draft |
| LoadRunner script archive | Legacy & tool exports | Depends on the source protocol — see below |
| JMeter test plan | Legacy & tool exports | Executable |
| k6 script | Legacy & tool exports | Executable (a constrained HTTP subset) |
| DevWeb source | Legacy & tool exports | Executable |
| Event Hubs capture | Messaging & cloud artifacts | Messaging draft |
| Broker config | Messaging & cloud artifacts | Messaging draft |
| Salesforce | Traffic & samples | Executable API draft |
| Oracle E-Business / Siebel | Enterprise app reviews | Review draft |

Every import runs through a guided **review** flow rather than dumping a script on you.
It shows warnings, statistics, the detected protocol, and any sections that need a
human decision. Each imported script lands in one of four modes:

| Mode | Meaning | What to do |
| --- | --- | --- |
| `executable` | Converts straight to a runnable script | Review the warnings, then validate and replay |
| `review_only` | Preserved faithfully, but a human must decide how it should run | Work through the flagged fragments in the review panel |
| `external_runner_required` | Needs a specific runner capability enabled before it can launch — Java VUser, SAP GUI, and Oracle Forms/NCA are the common cases | Ask an administrator to enable the runner for the pool, then relaunch |
| `unsupported` | Nothing in the source maps to an executable action | Re-author, or import a different artifact |

The review flow reports back a promotion status — `ready_for_validation`,
`ready_with_warnings`, `review_required`, `external_runner_required`, or `unsupported` —
together with the next action for each one. Map source values, confirm secret bindings,
and save the reviewed draft.

Coverage for the big legacy sources is documented and evidence-backed rather than
claimed: see the **LoadRunner Web Function Coverage**, **LoadRunner Java lr Function
Coverage**, and **LoadRunner TruClient Step Coverage** references for exactly which
functions convert, which are no-ops, and which are preserved for review.

## Building A Scenario

A script says what one user does; a **scenario** says how many users do it, for how
long, and under what conditions. Build scenarios from the **Scenarios** page.

A scenario brings together:

- **Groups** — each group runs one script with its own VUser count, ramp-up, ramp-down,
  and optional iteration cap. Use multiple groups to model a realistic mix, for example
  80% browse and 20% checkout. A group's `role` distinguishes a normal **load** group
  from an **observer** group — a browser experience canary that measures what a real
  user sees while the load groups generate the pressure.
- **Load model** — total VUsers, ramp pattern, and duration. Multi-stage groups let you
  express step loads (ramp to 100, hold, ramp to 300, hold) rather than a single slope.
- **Runtime settings** — per-group options such as think time, pacing, network
  simulation, and IP spoofing (a pool of source IPs so the run presents many client
  addresses).
- **Placement** — which generators, regions, or load zones run each group, including
  weighted distribution across zones.
- **Release context** — the application and release the scenario targets.
- **Shakeout and resource baseline flags** — whether the run first proves the scripts
  with a canary, and whether it captures an idle resource baseline before load starts.

### Sizing From A Goal

If you know the throughput you need but not the VUser count, use the
**build-from-goal** (Little's Law) sizing panel. Give it a target throughput or
latency, and APEX Load reverse-calculates a recommended VUser count.

The arithmetic is worth understanding, because it tells you when the recommendation is
wrong: concurrency ≈ throughput × response time. At 50 TPS with a 400 ms mean response
time and a 5 s pacing interval, each VUser completes one iteration every ~5.4 s, so you
need roughly 270 VUsers. Halve the response time and you need half as many. If your
measured response time under load differs sharply from the figure you sized with,
resize rather than trusting the original number.

Always **validate the load model** before saving. The validator catches settings that
would fail at run time — an invalid source IP, a group referencing a script that no
longer validates, a duration shorter than the ramp.

## Launching A Test

Once you have a scenario, launch a run from the **New Test** page (or directly from a
scenario).

1. Confirm your scope, then choose the scenario.
2. Confirm the target application and release — this is what the release trend keys off.
3. Apply any run-time overrides (base URL, environment values, data-pool selections).
4. Choose to start now or schedule a start time.
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

Open the **Command Center** to watch a run in real time. If you open it without picking
a run, it finds the live run in your current scope automatically. It streams metrics
over a WebSocket connection, so numbers update continuously without a refresh.

From the Command Center you can also control the run while it is in flight:

| Control | Available when | Effect |
| --- | --- | --- |
| **Start** | `QUEUED` or `RESERVED` | Begins execution now |
| **Pause** | `RUNNING` | Holds every VUser between actions |
| **Resume** | `PAUSED` | Releases the hold |
| **Stop** | `RUNNING`, `INITIALIZING`, or `PAUSED` | Graceful: finishes the action in flight, skips later actions and think time, runs teardown. The run stays `STOPPING` until generator teardown and archive finalization complete |
| **Abort** | Any non-terminal state | Force-cancels active work immediately |
| **Scale VUsers** | `RUNNING` | Raises or lowers a group's VUser count, instantly or over a ramp |
| **Adjust goal target** | `RUNNING`, goal-oriented runs | Changes a group's throughput (TPS) target and lets the system converge on it |

Pause and Stop become unavailable once a run enters `STOPPING` — from there only Abort
remains. Adjusting a run's remaining **duration** is done from the run's Test Execution
workspace rather than the Command Center.

Watch throughput, response-time percentiles (p50/p95/p99), and error rate together.
When errors climb, drill into the live error evidence to see representative failures
rather than guessing — a 500 from your application and a connection reset from an
exhausted generator look identical in an error-rate chart and call for opposite
responses.

## Scheduling Recurring Runs

To run a scenario on a cadence — a nightly regression, a weekly soak — use scheduling.

1. Go to **New Test** and switch to the **Schedules** view.
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
- **Root-cause analysis** — a deterministic breakdown of where time and errors
  concentrated, with optional governed AI assistance where policy allows it.

### Report Profiles

A report is assembled from a registry of sections, selected by profile:

| Profile | Contains | Export formats |
| --- | --- | --- |
| **Executive Summary** | Verdict, overview, graphs, dashboard panels, SLA rollup, release trend, risk summary, run context, recommendations, sign-off | HTML, JSON, CSV, PDF |
| **Detailed Technical** (default) | 21 sections including root cause, raw exports, generator manifest, completeness, schema compatibility, errors, resources, actions, browser evidence, SOAP, network phases | HTML, JSON, CSV, PDF, JSONL, Parquet |
| **Audit** | Official outcome, audit identity, authorization, governance, provenance, evidence tiers, completeness, change attribution, artifact integrity, report authorship | HTML, JSON, CSV, PDF |

PDF is offered only for profiles that declare it; a PDF request against a profile
without it is refused rather than silently downgraded.

`JSONL` and `Parquet` are not rendered report documents — they are the run's raw
archive datasets (transactions, resource samples, interval summaries), downloaded per
artifact for analysis in your own tooling.

### Baselines

Pin a run as a **comparison baseline** for its release so future runs are measured
against a known-good reference. A baseline plus the release trend is what turns "the
API feels slower" into "checkout p95 moved from 420 ms to 890 ms between 4.2 and 4.3".

## Repository And Shared Assets

The **Repository** page is where you manage the structural hierarchy — applications,
releases, projects, and the shared **object repository** that scripts and scenarios
bind to (for example, reusable selectors or shared definitions). Keeping shared assets
here means scripts reference one source of truth instead of duplicating values, so a
changed selector is fixed once rather than in fourteen scripts.

## Settings Reference

**Settings** has eight sections. All eight are visible to everyone; whether you can
*edit* one depends on your capabilities, and a read-only banner appears when you cannot
save.

| Section | Subsections | Editing requires |
| --- | --- | --- |
| **General** | Platform, Usage, Entitlements, Readiness, Authentication, Governance | `manage_system` |
| **Performance Thresholds** | — | `manage_system` |
| **Capacity Planning** | Availability, Generator Profile, Reservations, Retention | `manage_system` |
| **Domains** | — | `manage_domains` |
| **Projects & Compliance** | Project Catalog, Limits, GitHub Policy, Redaction Rules, Project Secrets | `manage_projects` |
| **Applications & Releases** | — | `manage_projects` |
| **Data Pools** | — | `manage_scripts` |
| **Integrations** | Library Federation, GitHub, Prometheus, Dashboards, Power BI, Notifications | `manage_system` |

Two you will use often as a tester: **Performance Thresholds** defines the SLA gates
that produce a run's verdict and can trigger threshold alerts and a sustained-P99
auto-stop, and **Data Pools** is where shared row data lives.

Under **General → Governance** you will find Schedule Defaults, Script Imports, Service
Account APIs, and Recent Audit Events — useful for understanding why a schedule or
import is waiting for approval.

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
| `archive` | Inspect an archived run; `verify` re-checks artifact SHA-256 integrity |
| `record` | Record a browser script with the embedded recorder |
| `record-agent` | Start the localhost browser recording bridge |
| `github` | Inspect and run GitHub Enterprise sync/check workflows |
| `import` | Import Bruno collections or Grafana dashboards |

### Gating A Pipeline

Exit codes are designed so a non-zero exit fails the build with no extra scripting:

| Command | Exit 0 | Exit 1 | Exit 2+ |
| --- | --- | --- | --- |
| `apexctl ci run` | Passed | SLA breach | Configuration, execution, timeout, or cancellation |
| `apexctl run` | Ran, no SLA breach | Execution error or SLA breach | — |
| `apexctl status` | Not breached, not failed | Breached, or `FAILED`/`ABORTED` | Bad arguments or fetch error |
| `apexctl wait` | Terminal `COMPLETE`, not breached | Breach, `FAILED`/`ABORTED`, or timeout | Bad arguments |

Add `--format json` for a machine-readable result object (`status`, `sla_evaluated`,
`sla_breached`, `sla_details`, `transactions`, `errors`, `tps`, `duration_ms`), and
`--junit <path>` for a JUnit XML report your CI test reporter can render. CLI threshold
flags take precedence over thresholds declared in the script's goal config. The
**CI/CD Integration** reference has copy-ready GitHub Actions gates for both
GitHub.com with OIDC and GHES with a scoped API key.

### The API And The MCP Server

- The **General**, **Quick Start**, and **Authentication** tabs on this page explain
  the base URL, service-account keys, and how to authenticate.
- The **Endpoint Map** tab lists the routes available to your current access.
- The **Swagger** tab is the generated route reference.
- The **MCP Server** tab covers agent-driven automation. The MCP bridge uses
  service-account auth and adds confirmation guardrails — mutating calls require an
  explicit confirmation and a reason — with tools covering system, auth, projects,
  scripts, scenarios, runs, reports, scheduling, fleet, data pools, imports, and AI
  assistance.

## Integrations

| Integration | What it gives you | Configured under |
| --- | --- | --- |
| **Prometheus** | Controller and generator metrics scraped into your own monitoring | Settings → Integrations → Prometheus |
| **Grafana** | Import an existing Grafana dashboard so live run panels appear alongside APEX Load metrics | Settings → Integrations → Dashboards |
| **Power BI** | Per-run publishing to a push dataset for business reporting | Settings → Integrations → Power BI |
| **Dynatrace** | Webhook notifications on run and threshold events | Settings → Integrations → Notifications |
| **OpenTelemetry** | Trace and metric export from the controller and generators | Deployment configuration |
| **GitHub / GHES** | Script sync between a repository and a project, plus pull-request checks | Settings → Integrations → GitHub |
| **Kubernetes clusters** | Register AKS/EKS/generic control planes so an administrator can place generator pools on them with per-pool CPU, memory, and node placement | Admin cluster registry |

Each has its own reference document — **Prometheus Metrics**, **Grafana Dashboard
Import**, **Power BI Publishing**, **Dynatrace Webhooks**, **OpenTelemetry**,
**GitHub Integration**, and **Cluster Connections**.

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
The script needs a runner capability that is not enabled — Java VUser, SAP GUI, and
Oracle Forms/NCA are the usual cases. An administrator has to enable that runner for
the pool the run is placed on. This is a deliberate gate, not a bug.

**The shakeout failed and my run is parked.**
The run is in `SHAKEOUT_REVIEW`. Open it, read the canary evidence, and fix the script —
that is the shakeout doing its job. If you are certain the failure is environmental, you
can override and continue.

**The launch was refused for capacity.**
The scenario needs more generator capacity than is currently available. Run the
capacity preview, lower the VUser count, lengthen the ramp, or ask an administrator
about the generator fleet and reservations under **Capacity Planning**.

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
- Add at least one **assertion** per action. Without one, an action can only fail on a
  transport error, and a 500 response counts as a success.
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

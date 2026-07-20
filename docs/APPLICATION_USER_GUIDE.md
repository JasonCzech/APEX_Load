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

## What APEX Load Does

APEX Load drives realistic load against your applications and reports how they behave
under it. A few concepts recur throughout the product:

- **Virtual users (VUsers)** simulate concurrent clients. During a run they execute
  your script's actions repeatedly against the target system.
- **Scripts** describe what a single virtual user does, expressed in a JSON-based DSL
  (Domain-Specific Language) with sections for `config`, `data_sources`, `init`,
  `actions`, and `teardown`. There is no proprietary scripting language to learn.
- **Scenarios** combine one or more scripts into a runnable load model: how many
  VUsers per group, how fast they ramp up, how long they run, and which runtime
  settings apply.
- **Runs (tests)** are individual executions of a scenario against a chosen
  application and release. A run produces live metrics and an archived report.
- **Protocols.** APEX Load can drive HTTP/1.1 and HTTP/2, SOAP/WSDL, WebSocket, gRPC,
  messaging (JMS, Kafka, Azure Event Hubs), databases, network protocols, and browser
  replay — so one platform covers API, integration, and end-to-end load.

Work is organized as a hierarchy: **Domain → Application Repository → Category →
Project → Release**. The **Project** is the boundary for access control, scripts,
scenarios, and results; the **Release** gives you release-over-release trend
comparisons.

## Ways To Use APEX Load

APEX Load meets you where you work through three surfaces:

- **Hosted browser dashboard** — the primary, zero-install experience, served by the
  controller. Everything in this guide refers to it unless noted otherwise.
- **Desktop workspace** — an optional installed application that runs a local
  controller with local storage and can sync with a remote workspace. The dashboard
  inside it behaves the same as the hosted one.
- **`apexctl` command line** — a headless path for running scripts directly from a
  terminal or a CI/CD pipeline.

## Signing In And Getting Oriented

How you sign in depends on how your deployment is configured — typically single
sign-on (OIDC) or an authentication gateway. Once you are in, the **left sidebar** is
your main map. Its items are:

- **Dashboard** — live overview of tests and recent activity.
- **New Test** — launch a run from a scenario.
- **Command Center** — the live cockpit for a single running test.
- **Results** — browse completed runs and their reports.
- **Scripts** — author, import, and manage test scripts.
- **Scenarios** — assemble scripts into runnable load models, and manage schedules
  and execution plans.
- **Repository** — manage applications, releases, projects, and shared assets.
- **Monitoring** — system health, telemetry, the generator fleet, and the run
  calendar.
- **API Guide** — the page you are reading, plus the API and Swagger references.
- **Settings** — your and (for administrators) system-wide configuration.

At the top of the sidebar is the **scope selector**: choose a **Domain**, then a
**Project**, then an **Application/Release**. This scope filters what you see across
the whole app — scripts, scenarios, and results all follow the selected project. Set
your scope first; most "I can't find my script/run" confusion is a scope mismatch.

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
format (see the next chapter).

### The Script Editor

The Script Editor is a structured editor over the JSON DSL. It presents your script
as the DSL's logical sections rather than raw text:

- **`config`** — target base URL, protocol, and script-wide runtime options.
- **`data_sources`** — the data pools and files your actions draw values from.
- **`init`** — actions that run once per virtual user before the main loop (for
  example, logging in).
- **`actions`** — the main body each virtual user repeats: requests, transactions,
  correlations, checks, and control flow.
- **`teardown`** — cleanup actions that run once per virtual user at the end.

The editor is driven by an action catalog, so the available action types and their
options come directly from what the runtime supports. Templates and inline validation
help you build correct actions without memorizing the schema.

### Parameterizing With Templates

Instead of hard-coding values, use template namespaces so a script behaves like a
real, varied user population:

- `{{env.KEY}}` — environment values.
- `{{data.source.column}}` — a value from a bound data pool.
- `{{var.name}}` — a variable captured earlier in the script (for example, a token
  extracted from a response).
- `{{secret.name}}` — a project secret; values are never shown in the UI.
- `{{fn.*}}` / `{{fake.*}}` — generated values such as timestamps or synthetic test
  data.

### Validate And Replay Before You Trust It

Before you run a script at scale, prove it works for a single user:

- **Validate** checks the script's structure and settings without executing it.
- **Replay** runs the script once, interactively. You can pause, set breakpoints,
  step action by action, and inspect what each action sent and received. For browser
  scripts, replay captures screenshots, DOM, console, network, and Core Web Vitals as
  evidence.

Validate first, replay second, then promote the script into a scenario.

## Recording And Correlation

If you would rather capture a real flow than write one by hand, use the recorder.

- **Record** a browser or HTTP session through the local **Recording Agent**, which
  captures the traffic your actions generate.
- **Replay** the captured flow with self-healing selectors that tolerate small UI
  changes.
- **Correlation** finds the dynamic values (tokens, session IDs) that must be
  captured from one response and replayed in the next request, and the
  **Correlation Diagnose** and **ruleset** panels let you review and tune them.
- **Parameterization review** suggests which recorded literals should become data-pool
  or template values, so the script drives varied data instead of one fixed path.

The result is an executable script you can refine in the Script Editor just like a
hand-authored one.

## Importing Existing Scripts

If you already have tests in another tool, import them instead of rewriting. Open the
**Scripts** page and use the import drop zone. Supported sources include:

- **LoadRunner / enterprise script archives** (including Java-family, SAP GUI, and
  TruClient 2 Web).
- **JMeter** test plans.
- **k6** (a constrained HTTP subset).
- **HAR** captures.
- **OpenAPI / Swagger** and **Postman** collections (generate API drafts).
- **WSDL** (generate SOAP drafts).
- **Bruno** collections.
- **AsyncAPI** and broker configurations (messaging).

Every import runs through a guided **review** flow rather than dumping a script on
you: it shows warnings, statistics, detected protocol, and any sections that need a
human decision. Some sources convert straight to an executable script; others produce
a **review-only** draft or an **external-runner-required** draft that needs a specific
capability enabled before it can run. Map source values, confirm secret bindings, and
save the reviewed draft.

## Building A Scenario

A script says what one user does; a **scenario** says how many users do it, for how
long, and under what conditions. Build scenarios from the **Scenarios** page.

A scenario brings together:

- **Groups** — each group runs a script with its own VUser count and ramp-up. Use
  multiple groups to model a realistic mix (for example, 80% browse, 20% checkout).
- **Load model** — total VUsers, ramp pattern, and duration. You can also build a
  **goal-oriented** run that targets a throughput (TPS) figure.
- **Runtime settings** — per-group options such as think time, pacing, network
  simulation, and IP spoofing (a pool of source IPs so the run presents many client
  addresses).
- **Placement** — which generators or regions/load zones run each group.
- **Release context** — the application and release the scenario targets.

### Sizing From A Goal

If you know the throughput you need but not the VUser count, use the **build-from-goal**
(Little's Law) sizing panel. Give it a target throughput or latency, and APEX Load
reverse-calculates a recommended VUser count. Always **validate the load model**
before saving — the validator catches settings that would fail at run time (for
example, an invalid source IP).

## Launching A Test

Once you have a scenario, launch a run from the **New Test** page (or directly from a
scenario). Choose the scenario, confirm the target application/release, and start.

- **Scheduled start.** Instead of starting now, pick a start time. APEX Load stores
  the run as a reservation and begins execution at the chosen window. You can also
  set the schedule's timezone.
- **Shakeout / canary.** If the scenario requires a shakeout, APEX Load first runs a
  single-pass canary of each script group before committing the full load. A clean
  shakeout starts the full run automatically; a failing shakeout pauses for review so
  you can inspect the evidence and either override and continue or cancel.
- **Capacity preview.** Before a large run, APEX Load can estimate the capacity it
  needs so you are not surprised by a shortfall.

## Monitoring A Live Run

Open the **Command Center** to watch a run in real time. If you open it without
picking a run, it finds the live run in your current scope automatically. It streams
metrics over a WebSocket connection, so numbers update continuously without a refresh.

From the Command Center you can also **control** the run while it is in flight:

- **Stop** gracefully: finish the action currently in flight, skip later actions/think time, and run teardown. **Abort** force-cancels active work immediately. An active run remains **Stopping** until generator teardown and archive finalization complete; a queued run is cancelled immediately.
- **Pause** and **resume** between actions.
- **Extend or shorten** the remaining duration.
- **Scale VUsers** for a group up or down — optionally over a ramp so the change is
  gradual rather than instant.
- For goal-oriented runs, **adjust the per-group throughput target** and watch the
  system converge on it.

Watch throughput, response-time percentiles (p50/p95/p99), and error rate together.
When errors climb, drill into the live error evidence to see representative failures
rather than guessing.

## Scheduling Recurring Runs

To run a scenario on a cadence — a nightly regression, a weekly soak — use scheduling.
From the **Scenarios** page, open the **Schedules** view, pick a stored scenario, and
define when it should run using the visual **cron builder** (a five-field editor over
a standard cron expression, so you do not have to remember cron syntax). Set the
schedule's timezone so it fires at the local time you expect.

The **run calendar**, under **Monitoring → Schedules**, shows upcoming and recent
scheduled runs with their scenario labels and release context, so you can see the
week's load at a glance and spot conflicts.

## Chaining Runs With Execution Plans

When one scenario is not enough — say you want a baseline, then a spike, then a soak,
in order — use an **execution plan**. A plan groups several scenario entries with
per-entry runtime and data-pool overrides and a run mode (parallel or sequential),
and runs them as one coordinated unit. Plans can carry governance approvals, so a
gated plan waits for sign-off before it proceeds. Manage plans from the **Scenarios**
page.

## Reading Results And Reports

Completed runs live under **Results**. Open one to reach its **Run Report** — the
detailed post-run analysis.

A run report gives you:

- **Charts and metric breakdowns** — throughput, response times, errors, and
  resource usage over the run.
- **Verdict / pass-fail** against the scenario's SLA gates. Authorized users can
  override a verdict; overrides are audit-logged.
- **Load comparison** — compare this run against one or more other runs to see what
  changed.
- **Release trend** — how this run's key transactions compare across releases of the
  same application, so regressions surface early.
- **Composable, exportable reports** — assemble the sections you need and export in
  formats such as HTML, CSV, JSON, and PDF, with profiles like executive, engineer,
  and audit.
- **Root-cause analysis** — a deterministic breakdown of where time and errors
  concentrated (with optional governed AI assistance where policy allows it).

Pin a run as a **comparison baseline** for its release so future runs are measured
against a known-good reference.

## Repository And Shared Assets

The **Repository** page is where you manage the structural hierarchy —
applications, releases, projects, and the shared **object repository** that scripts
and scenarios bind to (for example, reusable selectors or shared definitions). Keeping
shared assets here means scripts reference one source of truth instead of duplicating
values.

## Data Pools And Secrets

Realistic load needs realistic, varied data.

- **Data pools** are project-scoped, reusable sets of rows (for example, a list of
  test accounts). Bind a pool to a scenario and reference its columns from a script
  with `{{data.pool.column}}`. Rows can be overridden per execution-plan entry. A
  run receives an immutable, read-only snapshot of the selected rows; execution does
  not update or consume rows in the shared pool.
- **Project secrets** back the `{{secret.name}}` namespace. The UI shows only their
  metadata and status — never the values — and secret writes require an authorized
  human action. Manage them from **Settings → Shared Data Pools** and the project
  secrets panel.

## Finding The API And References

Everything you can do in the dashboard is backed by a REST and WebSocket API. When you
are ready to automate:

- The **General**, **Quick Start**, and **Authentication** tabs on this page explain
  the base URL, service-account keys, and how to authenticate.
- The **Endpoint Map** tab lists the routes available to your current access.
- The **Swagger** tab is the complete, generated route reference.
- The **MCP Server** tab covers agent- and CI-driven automation.

## Tips For Getting Good Results

- Set your **scope** (domain, project, release) before you start looking for anything.
- **Validate and replay** a script for one user before running it for thousands.
- Import existing tests through the **review** flow rather than trusting a raw
  conversion — confirm warnings and secret bindings.
- Use a **shakeout** on important runs so a broken script fails one canary user, not
  the whole fleet.
- **Pin a baseline** per release and lean on load comparison and release trend to
  catch regressions early.
- Prefer **data pools and secrets** over hard-coded values so your load looks like
  real, varied traffic.

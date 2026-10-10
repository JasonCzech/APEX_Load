# Help Center

A guided collection of operator articles for the APEX Load dashboard.


## Build and run your first test

Follow a small HTTP test from script to report. This walkthrough assumes an existing APEX Load deployment. Use [Choose an installation](../deployments/README.md) for installation and [Concepts & navigation](APPLICATION_USER_GUIDE.md#what-apex-load-does) for terminology.

### Before you start

You need permission to create scripts, scenarios and runs in a project; a healthy execution target; and an HTTP endpoint you own or have permission to test. A shared generator must be able to reach that endpoint. A workstation's localhost address is not the generator's localhost.

Select Domain, Project, Application and Release in the sidebar. Ask your administrator for the correct project if the scope is missing. Production sign-in uses your deployment's identity provider; local developer personas are for non-production use only.

For a self-contained local exercise, run this dependency-free sample server on the same machine as a local APEX Load execution target. It returns a fixed JSON response and does not require credentials or data files:

```python
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        payload = b'{"status":"ok"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

HTTPServer(("127.0.0.1", 8099), Handler).serve_forever()
```

Save as sample_target.py and run `python3 sample_target.py`. Keep it running until the test completes. For distributed execution, use a reachable approved test service instead and replace the base URL below; do not use this loopback target from remote generators.

### Create and prove the script

Open [Scripts](#/scripts), choose **Create script**, then **Blank & Assisted Editors** › **Start New Script**, and paste the example below into the editor's **Source** tab. The editor accepts the wrapped form shown here (`{"script": {...}}`, the shape API requests use) or the bare script object. Save it in the selected project. The example sends only one GET per iteration and includes explicit response checks.

```json
{
  "script": {
    "name": "First_HTTP_Test",
    "version": "1.0.0",
    "protocol": "http",
    "config": {
      "base_url": "http://127.0.0.1:8099",
      "pacing": { "type": "fixed_interval", "interval_ms": 1000 }
    },
    "actions": [
      {
        "name": "Check_Service",
        "type": "GET",
        "target": "/",
        "assertions": [
          { "type": "status_code", "expected": 200 },
          { "type": "body_contains", "value": "ok" }
        ]
      }
    ]
  }
}
```

Choose **Validate Script(s)** and resolve every reported issue. Validation checks the script contract; launch still checks permissions, capacity and deployment capabilities. Then choose **Debug one iteration** and inspect the response. Expect HTTP 200, a body containing `ok`, and successful checks. If replay cannot reach the endpoint, fix connectivity before increasing load. See [Validate & replay](APPLICATION_USER_GUIDE.md#validating-and-replaying).

### Run a small scenario

1. Open [Scenarios](#/tests) and create a scenario with one group using First_HTTP_Test.
2. Set 1 VUser, a 5-second ramp and a 30-second duration. Validate the load model and save.
3. Open [New Test](#/new-test), choose that scenario, confirm the application/release and start. This creates a run and sends traffic to the configured target.
4. Watch the run in [Command Center](#/command-center). Confirm the Check_Service action succeeds and throughput is near one iteration per second after ramp-up. Scheduling and service time can lower the observed rate.
5. When it reaches COMPLETE, open [Results](#/results) and inspect errors, timing and the SLA verdict. COMPLETE describes lifecycle completion; it does not by itself establish an SLA pass.

If a run waits for approval, capacity or shakeout review, use [Troubleshooting](HELP_CENTER.md#troubleshoot-a-run). Do not repeatedly start duplicates.

### Clean up and continue

Stop the sample server with Ctrl+C after the run ends. Keep the run as learning evidence or remove sample assets using your project's normal retention and deletion controls. Do not delete a shared scenario or baseline used by someone else.

Next, [model load](APPLICATION_USER_GUIDE.md#building-a-scenario), [add data and secrets](APPLICATION_USER_GUIDE.md#parameterizing-and-data), or [interpret results](APPLICATION_USER_GUIDE.md#reading-results-and-reports). Check [Capabilities & limitations](HELP_CENTER.md#check-current-capabilities) before choosing a protocol or importing a legacy script.


## Check current capabilities

Import recognition, authoring, runtime execution and production adapter availability are separate capabilities. A script that imports successfully is not necessarily executable on the selected generator. Check the controller's current support matrices and configured pool before launch.

### Choose an execution path

| Capability | Current boundary | What to check |
| --- | --- | --- |
| HTTP, SOAP, WebSocket, gRPC, database and network | Compiled protocol implementations; individual operations have protocol-specific contracts | DSL action reference, dependencies and target connectivity |
| HTTP/3 (QUIC) | Preview: scripts save everywhere; runs need `APEXLOAD_HTTP3=true` on the controller and every generator | [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md) and the editor's capability note |
| Browser replay and observer canaries | Requires the browser runtime and permitted execution environment | Browser & mobile support and pool readiness |
| Kafka | Default in-memory mock; real network adapter requires kafka_real build | Messaging support and deployment build |
| Azure Event Hubs | Default in-memory mock; real AMQP adapter requires eventhubs_real build | Producer/observer-probe semantics and deployment build |
| JMS Azure Service Bus | Real adapter requires jms_servicebus_real build | Messaging support and credentials |
| Java VUser | Governed external sidecar runner, disabled unless configured | Java VUser runtime and policy |
| Bruno HTTP import | Converts supported requests into native APEX scripts | Review conversion findings before replay; canonical runner retired |
| SAP GUI | Authorable/importable; no registered execution runtime in standard binaries | Preserve import evidence and plan an alternative execution path |
| Oracle Forms/NCA | Import evidence requires an approved external bridge; ordinary settings do not supply that bridge | Import disposition and administrator-supported alternatives |
| Assisted authoring | Deterministic local provider; external LLM providers are not wired by default | Review, validation, replay evidence and approval gates |
| OpenTelemetry | Opt-in traces from standalone controller/generators; no native metrics/log exporter | Prometheus for metrics; deployment tracing configuration |
| Shared data pools | Runs use read-only snapshots; durable consume/read-write execution is not implemented | Row status, snapshot selection and rerun behavior |
| Sequential execution plans | Next entry starts after the previous entry reaches a terminal state; halt/skip policy applies | Failure policy, governance and recovery evidence |
| Capacity claims | 10,000 HTTP VUsers per pod is a design target, not a published achieved benchmark | Measure your script, protocol, generator and environment |

### Import conversion and replay evidence

The September 12, 2026 [fixture matrix](https://github.com/JasonCzech/APEX_Load/blob/main/docs/qa/reviews/import-reviews-2026-09.md#import-fixture-matrix-and-broker-runbook) passed 16 cases, with four blocked by unavailable provider or customer environments. These results establish the coverage below; they do not expand runtime availability or certify complete source-tool parity.

| Source or provider | Validated coverage | Remaining limitation |
| --- | --- | --- |
| Kafka | Imported AsyncAPI actions completed an exact-payload round trip against a real local Kafka broker | Does not certify every schema, security configuration or deployment topology |
| JMS Solace | Imported JNDI queue actions completed an exact-payload AMQP round trip against a real local Solace broker; corrected VPN-related SASL username rewriting | Topic, selector, transaction and HA behavior require separate evidence; use a build containing the authentication fix |
| Other messaging | Mock AsyncAPI, MQSC/JNDI and Event Hubs Capture conversion/replay checks passed | Live Azure Service Bus, Azure Event Hubs and IBM MQ validation remains blocked; the current Event Hubs live smoke also needs exact-marker assertions before establishing payload fidelity |
| Enterprise imports | Persisted CloudTest, SAP GUI and disabled Java runner constraints held; Java lifecycle conversion, TruClient preservation and 26 pinned public source directories passed their checks | Customer enterprise replay baselines remain unavailable. CloudTest unproven mappings remain blocked, SAP GUI has no standard runtime, and Java needs its governed runner. Supported TruClient browser actions are not universally blocked |

Use the [matrix results and logs](https://github.com/JasonCzech/APEX_Load/blob/main/docs/qa/reviews/import-fixture-matrix-2026-09-12/evidence/results.json) and [reproduction runbook](https://github.com/JasonCzech/APEX_Load/blob/main/docs/qa/reviews/import-reviews-2026-09.md#import-fixture-matrix-and-broker-runbook) to distinguish real-provider replay, mock behavior and conversion-only evidence. Public and synthetic fixtures do not replace customer replay baselines.

### Current references

- [DSL action reference](DSL_ACTION_REFERENCE.md)
- [Messaging support](PROTOCOL_SUPPORT.md#broker-and-messaging-support)
- [Browser & mobile support](PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation)
- [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md)
- [Java VUser runtime](PROTOCOL_SUPPORT.md#java-vuser-execution)
- [Runtime settings support](RUNTIME_SETTINGS_PARITY.md)
- [OpenTelemetry traces](OBSERVABILITY_INTEGRATIONS.md#opentelemetry-tracing)
- [API support-matrix operations](#/swagger?search=support-matrix)

The running controller's capabilities, actual build tags, policy and selected execution pool determine availability. Documentation bundled with a different UI build cannot establish that a remote generator is ready.


## Prepare a support request

Contact your organization's APEX Load administrator or service desk for deployment support. This self-hosted application does not configure a universal vendor support address. Your administrator should publish the team's support channel in the deployment's onboarding materials.

### Prepare a useful request

Copy this template and fill in only the information you can safely share:

```text
APEX Load controller/build version:
Dashboard build/version:
Deployment type (hosted browser / desktop / CLI):
Time of problem and timezone:
Project/application/release scope (IDs if permitted):
Run/script/scenario ID:
Action attempted:
Expected outcome:
Actual outcome and exact sanitized error:
Smallest reproduction:
Checks already performed:
Evidence attached after redaction review:
```

The build information control in the sidebar identifies the controller build. Help's footer identifies the dashboard documentation bundle. These can differ; record both when reporting a mismatch.

### Review evidence before sharing

Omit API keys, cookies, pairing tokens, passwords and private request bodies. Imported scripts may contain literal credentials; do not assume an entire archive is safe to attach. Prefer a minimal redacted reproduction and the affected action's sanitized error. Follow your organization's data-sharing policy for logs, screenshots and archives.

### Who can resolve the problem

Project owners can check scope, script configuration and test data. Administrators can review permissions, generator capacity and deployment health. Use [Troubleshooting](HELP_CENTER.md#troubleshoot-a-run) to collect the relevant checks before escalating. For a suspected product defect, your administrator can route the reviewed reproduction through your organization's established issue tracker.


## Documentation version notes

This documentation ships with the dashboard build shown below each article. It describes this checkout's capabilities; a remote controller or generator may run another version. Use the sidebar build information and current API capability responses to compare them.

### October 10, 2026 HTTP/3 and QUIC (preview)

- Added [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md): the `auto+h3` (upgrade when advertised) and `3` (require HTTP/3) policies with worked examples, the QUIC settings panel, what stays on TCP, importing captured HTTP/3, and reading the HTTP version mix in replay, reports, the live run and Power BI.
- Operators enable execution with `APEXLOAD_HTTP3=true` on the controller and every generator (`http3.enabled` in the Helm chart); [HTTP/3 (QUIC) execution](guides/HTTP3_AND_QUIC.md#enable-http-3-execution) covers the QUIC limits, the scenario preflight, UDP egress and buffer sizing.
- Power BI datasets move to schema version 2 (HTTP version mix columns); recreate an existing dataset before publishing.

### October 9, 2026 root-cause analysis across the dashboard

- Added [Root-Cause Analysis](APPLICATION_USER_GUIDE.md#root-cause-analysis): where root cause appears (Summary, Results, the dashboard, Command Center, the Performance timeline, error patterns), how to read the panel when no cause is supported, and the hand-offs to findings, timeline annotations and test notes.
- Projects now turn root-cause analysis on in Settings → Project analysis, including automatic analysis of every archived run; deployment lists still apply.
- Notification routes can subscribe to *Root-cause analysis completed* and *Run validity suspect or invalid*; CI reports the root cause beside validity.

### September 24, 2026 documentation corrections

- Corrected the `apexctl` exit-code table (0 success, 1 test or SLA failure, 2 usage or configuration, 3 controller or transport, including `wait` timeouts) and added the `library` and `version` commands.
- Fixed CEL `expr` namespaces (`var`, `data`, `env`), added `control_switch` and `control_parallel` to the control-flow list, added the transient `STARTING` run state, and added DOCX to report export formats.
- Updated the import-source table (Akamai CloudTest Export added; Salesforce is auto-detected within HAR import).
- API guide: filled empty workflow sections with endpoint tables checked against the endpoint catalog, corrected GitHub policy field names, the IP-spoofing UI labels, TLS compatibility defaults, and the replay/debug session route guidance (shared with the Replay debugger guide).
- Project Secrets: wrapping-key variables are `APEXLOAD_WRAPPING_*`, including the AWS KMS key id.
- Script Editor design memo now records that the Author outline, Action editor and Source tab have shipped; Script lifecycle drops stale line references and lists all converters.
- DSL operator guide: script wrapper wording now matches the parser and editor (both forms accepted), and release-note phrasing became present-tense guidance.

### September 13, 2026 DSL operator guide

- Added [DSL scripting for LoadRunner & JMeter operators](HELP_DSL_OPERATOR_GUIDE.md), with terminology mappings, per-user execution, an annotated HTTP journey, correlation, data lifetimes, pacing, control flow, reporting, and troubleshooting.
- Connected the guide to Help navigation, search, authoring guidance, and the generated action reference.

### September 12, 2026 documentation update

- Organized help into Getting Started, User Guide, Administration, API & Automation, and Troubleshooting.
- Added article search, shareable section links, copyable examples and an in-app runbook index.
- Updated Settings, Access and shared-data links to their current task destinations.
- Corrected HTTP error handling, OpenTelemetry signal coverage, sequential execution, runner availability, pacing arithmetic and MCP confirmation wording.
- Preserved the existing General, Quick Start, Authentication, MCP, Swagger and Repository Guide URLs as navigation aliases.
- Added the import fixture matrix evidence to [Capabilities & limitations](HELP_CENTER.md#check-current-capabilities): canonical Bruno, real Kafka/Solace round trips, enterprise execution constraints, the Solace authentication correction, and the four remaining provider/customer validation gaps.

### June 25, 2026 scenario schedule timezone behavior change

A scenario schedule's cron expression is now evaluated in the schedule's declared `timezone` instead of UTC. Previously a `0 9 * * *` schedule in `America/Chicago` fired at 09:00 UTC (03:00/04:00 local); the timezone was honored only for blackout, maintenance and availability windows.

- `next_run_at` is still stored in UTC but now corresponds to the correct local wall-clock time.
- DST is handled: a time that does not exist on a spring-forward day (for example `30 2 * * *` America/Chicago on the second Sunday of March) is skipped to the next valid day; on a fall-back day the earliest matching instant wins. An empty or unparseable `timezone` evaluates in UTC.
- POSIX day-of-month / day-of-week semantics are fixed: when both fields are restricted, a run fires when either matches (`0 9 15 * 1` fires at 09:00 on the 15th or any Monday).
- Operator impact: existing schedules with a non-UTC timezone shift from their old UTC time to their declared local time. No data migration is needed. A schedule's already-stored `next_run_at` is used for its first post-upgrade firing; edit and re-save the schedule to recompute immediately. UTC schedules are unaffected.
- The run calendar (Monitoring, Schedules) renders event times in each event's declared timezone with a short zone label instead of the browser's local zone.

This is a documentation change log, not an exhaustive product release history. Your deployment's release notes remain the source for upgrade-specific changes.

### Keeping guidance current

The APEX Load maintainers own the help catalogue. The dashboard maintainers review navigation and accessibility; protocol maintainers review execution examples and capability boundaries; platform maintainers review identity, fleet, storage and deployment runbooks. Review content when its related code, route, configuration or workflow changes.

Core help was reviewed for this documentation update on September 24, 2026. Linked technical runbooks retain their own scope and status; listing them here does not imply a fresh deployment certification. See [Capabilities & limitations](HELP_CENTER.md#check-current-capabilities) for known execution boundaries and [Contact support](HELP_CENTER.md#prepare-a-support-request) for reporting discrepancies.


## Troubleshoot a run

Start with the symptom and keep the current run, scope and error evidence. Many problems can be resolved without creating another run. The [testing FAQ](APPLICATION_USER_GUIDE.md#troubleshooting-and-faq) covers additional script and report questions.

### Authentication or permission denied

**Symptom:** Sign-in fails, an action is unavailable, or the API returns 401/403.

Check the controller URL and identity first. A valid identity still needs an active local user binding or a service account with both appropriate roles and token scopes. A 401 indicates missing/invalid authentication; a 403 indicates authorization denial. Ask your administrator to review access for the specific project and operation, using [API authentication](API_USER_GUIDE.md#authentication) and the [identity runbook](AUTH_RBAC_OPERATIONS_RUNBOOK.md). Share the operation, status and sanitized error, never the key or session cookie.

### A script or run is missing

Check Domain, Project, Application and Release in the sidebar. Then check filters and whether the item was archived or deleted. Select the scope in which it was created. If it remains unavailable, ask an administrator to check access and retention using its ID. See [Concepts & navigation](APPLICATION_USER_GUIDE.md#signing-in-and-getting-oriented).

### Missing secrets or unexpanded templates

Replay one user and inspect the failing action. Check namespace spelling, extractor matches and secret status in the selected project. An imported literal credential and a project secret have different disclosure behavior. Do not add real secrets to help requests or logs. Resolve the binding with an authorized human workflow; see [Parameters, data & secrets](APPLICATION_USER_GUIDE.md#parameterizing-and-data).

### Recording agent will not pair

Confirm the recording agent is running on the operator workstation, that the displayed command belongs to the current pairing session, and that Verify Agent can reach its loopback address. Check browser private-network permission and workstation endpoint controls with your administrator. A remote controller cannot reach a workstation's localhost on your behalf. Use the [recording workflow](APPLICATION_USER_GUIDE.md#recording-and-correlation); include agent/build versions and the sanitized pairing error when escalating, but omit pairing tokens.

### An import cannot execute

Read the import disposition before launching. Review-only, unsupported and external-runner-required outcomes are different from executable output. Java VUser needs a configured governed runtime. Bruno Runner is retired; convert supported Bruno HTTP sources into native scripts. SAP GUI has no registered runtime in standard binaries, and Oracle NCA needs a bridge that is not supplied by enabling an ordinary pool setting. See [Capabilities & limitations](HELP_CENTER.md#check-current-capabilities) and [Import existing scripts](APPLICATION_USER_GUIDE.md#importing-existing-scripts).

### Launch refused or waiting for capacity

Inspect the capacity preview, generator health, project limits and reservations. Reduce the requested load only if that still meets your test objective; otherwise ask an administrator to provide capacity. A slower ramp does not necessarily reduce peak VUser capacity requirements. Validation alone does not guarantee admission. See [Model load](APPLICATION_USER_GUIDE.md#building-a-scenario) and [Cluster connections](CLUSTERS_AND_CLOUD.md#cluster-connections-host-resource-allocation).

### Shakeout failed

Open SHAKEOUT_REVIEW and inspect the canary evidence. Correct the script or environment and replay before trying the full load again. Override only through the authorized review workflow with a documented reason. See [Run & monitor](APPLICATION_USER_GUIDE.md#launching-a-test).

### Metrics or traces are missing

Confirm the run is active and the dashboard's live connection is healthy. Check controller/generator reachability and your Prometheus scrape configuration for metrics. OpenTelemetry exports opt-in traces only; enabling it does not create a metrics or log exporter. See [OpenTelemetry traces](OBSERVABILITY_INTEGRATIONS.md#opentelemetry-tracing) and your deployment's monitoring runbook.

### Reports or archives are unavailable

Check whether the run is still STOPPING or finalizing archives, whether your account can read the run, and whether retention has removed its evidence. Use a report profile that supports the requested format. Do not equate a missing archive with a zero-error test. Preserve the run ID and failure message; use [Backup, recovery & retention](ENTERPRISE_PLATFORM_RUNBOOK.md#enterprise-data-platform-operations-runbook).

### Root-cause analysis is missing or pending

If Diagnostics says root-cause analysis is not enabled, a project administrator turns it on in [Settings → Project analysis](#/settings?setting=analysis). If a run shows **No analysis yet**, an editor can **Analyze** it; projects that analyze automatically queue one when the run's archive is finalized, and an archive still being refreshed is analyzed once the refresh finishes. **Analysis queued** waits behind other analyses (requests run before automatic ones). If an analysis failed, the panel names the reason; **Re-analyze**, with **Capture evidence afresh** after late monitoring evidence. **Too little evidence** means few series could be judged: check Evidence coverage for missing monitor sources or trace context. See [Root-Cause Analysis](APPLICATION_USER_GUIDE.md#root-cause-analysis).

### Still blocked

Use [Contact support & collect evidence](HELP_CENTER.md#prepare-a-support-request). Include what you expected, what happened, the smallest reproduction and the exact build. Do not include credentials, private target payloads or unreviewed archives.

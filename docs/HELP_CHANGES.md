# Documentation changes & version

This documentation ships with the dashboard build shown below each article. It describes this checkout's capabilities; a remote controller or generator may run another version. Use the sidebar build information and current API capability responses to compare them.

## September 24, 2026 documentation corrections

- Corrected the `apexctl` exit-code table (0 success, 1 test or SLA failure, 2 usage or configuration, 3 controller or transport, including `wait` timeouts) and added the `library` and `version` commands.
- Fixed CEL `expr` namespaces (`var`, `data`, `env`), added `control_switch` and `control_parallel` to the control-flow list, added the transient `STARTING` run state, and added DOCX to report export formats.
- Updated the import-source table (Akamai CloudTest Export added; Salesforce is auto-detected within HAR import).
- API guide: filled empty workflow sections with endpoint tables checked against the endpoint catalog, corrected GitHub policy field names, the IP-spoofing UI labels, TLS compatibility defaults, and the replay/debug session route guidance (shared with the Replay debugger guide).
- Project Secrets: wrapping-key variables are `APEXLOAD_WRAPPING_*`, including the AWS KMS key id.
- Script Editor design memo now records that the Author outline, Action editor and Source tab have shipped; Script lifecycle drops stale line references and lists all converters.
- DSL operator guide: script wrapper wording now matches the parser and editor (both forms accepted), and release-note phrasing became present-tense guidance.

## September 13, 2026 DSL operator guide

- Added [DSL scripting for LoadRunner & JMeter operators](HELP_DSL_OPERATOR_GUIDE.md), with terminology mappings, per-user execution, an annotated HTTP journey, correlation, data lifetimes, pacing, control flow, reporting, and troubleshooting.
- Connected the guide to Help navigation, search, authoring guidance, and the generated action reference.

## September 12, 2026 documentation update

- Organized help into Getting Started, User Guide, Administration, API & Automation, and Troubleshooting.
- Added article search, shareable section links, copyable examples and an in-app runbook index.
- Updated Settings, Access and shared-data links to their current task destinations.
- Corrected HTTP error handling, OpenTelemetry signal coverage, sequential execution, runner availability, pacing arithmetic and MCP confirmation wording.
- Preserved the existing General, Quick Start, Authentication, MCP, Swagger and Repository Guide URLs as navigation aliases.
- Added the import fixture matrix evidence to [Capabilities & limitations](HELP_CAPABILITIES.md): canonical Bruno, real Kafka/Solace round trips, enterprise execution constraints, the Solace authentication correction, and the four remaining provider/customer validation gaps.

## June 25, 2026 scenario schedule timezone behavior change

A scenario schedule's cron expression is now evaluated in the schedule's declared `timezone` instead of UTC. Previously a `0 9 * * *` schedule in `America/Chicago` fired at 09:00 UTC (03:00/04:00 local); the timezone was honored only for blackout, maintenance and availability windows.

- `next_run_at` is still stored in UTC but now corresponds to the correct local wall-clock time.
- DST is handled: a time that does not exist on a spring-forward day (for example `30 2 * * *` America/Chicago on the second Sunday of March) is skipped to the next valid day; on a fall-back day the earliest matching instant wins. An empty or unparseable `timezone` evaluates in UTC.
- POSIX day-of-month / day-of-week semantics are fixed: when both fields are restricted, a run fires when either matches (`0 9 15 * 1` fires at 09:00 on the 15th or any Monday).
- Operator impact: existing schedules with a non-UTC timezone shift from their old UTC time to their declared local time. No data migration is needed. A schedule's already-stored `next_run_at` is used for its first post-upgrade firing; edit and re-save the schedule to recompute immediately. UTC schedules are unaffected.
- The run calendar (Monitoring, Schedules) renders event times in each event's declared timezone with a short zone label instead of the browser's local zone.

This is a documentation change log, not an exhaustive product release history. Your deployment's release notes remain the source for upgrade-specific changes.

## Keeping guidance current

The APEX Load maintainers own the help catalogue. The dashboard maintainers review navigation and accessibility; protocol maintainers review execution examples and capability boundaries; platform maintainers review identity, fleet, storage and deployment runbooks. Review content when its related code, route, configuration or workflow changes.

Core help was reviewed for this documentation update on September 24, 2026. Linked technical runbooks retain their own scope and status; listing them here does not imply a fresh deployment certification. See [Capabilities & limitations](HELP_CAPABILITIES.md) for known execution boundaries and [Contact support](HELP_SUPPORT.md) for reporting discrepancies.

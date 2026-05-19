# Enterprise Protocol Bundle Parity Plan

## Summary

This plan extends Stormrunner toward parity with additional LoadRunner Enterprise protocol-bundle coverage:

- Database Protocol Bundle
- Network Protocol Bundle
- Oracle E-Business Protocol Bundle
- Templates Protocol Bundle
- Foundation Entitlement for SAP GUI

The plan intentionally separates three kinds of parity:

1. Native Stormrunner protocol execution on Linux generator pods.
2. Review/import/template parity where Stormrunner can ingest and govern assets but not execute them natively yet.
3. Native desktop or proprietary runtime parity that requires Windows or vendor-client runner pools.

This matters because Stormrunner's current architecture is strongest for HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, browser canaries, and import-driven Web/API workflows. Some requested LoadRunner protocols are low-level socket, database-client, desktop GUI, or vendor-proprietary runtimes and should not be forced into the existing high-density Linux generator path without explicit runner boundaries.

## Source Baseline

OpenText now positions LoadRunner Enterprise as OpenText Enterprise Performance Engineering and advertises broad protocol-bundle coverage across Network, Oracle E-Business, SAP, SAP GUI, Templates, Database, Java, SOA, Web, Web 2.0, and related bundles:

- OpenText Enterprise Performance Engineering product page: https://www.opentext.com/products/enterprise-performance-engineering
- OpenText license bundles solution overview: https://www.opentext.com/media/solution-overview/opentext-professional-performance-engineering-and-opentext-enterprise-performance-engineering-license-bundles-slo-en.pdf
- OpenText SAP solutions data sheet: https://www.opentext.com/media/data-sheet/opentext-performance-engineering-for-sap-solutions-ds-en.pdf
- OpenText additional license authorizations: https://www.opentext.com/ca/media/documentation/additional-license-authorizations-for-performance-engineering-software-products-documentation-en.pdf

The 2025 license-bundles overview lists these requested bundle members:

| Bundle | OpenText-listed protocols |
| --- | --- |
| Network | DNS, FTP, IMAP, LDAP, MAPI, POP3, SMTP, Tuxedo, Windows Sockets |
| Oracle E-Business | DevWeb, JMeter, Gatling, Oracle NCA, Oracle-Web, Siebel-Web, Web-HTTP/HTML |
| Templates | C Vuser, C++ .NET, C# .NET, Java Vuser, VB .NET |
| SAP | SAP GUI, SAP-Web, SMP |

The 2023 license-bundles flyer lists Database as ODBC and Oracle-2 Tier. Because OpenText bundle composition is price-list and version dependent, Stormrunner should store the source document and validation date with every externally claimed support matrix row.

## Current Stormrunner Baseline

| Area | Current Stormrunner posture | Gap against requested bundles |
| --- | --- | --- |
| HTTP/Web, SOAP, Java-over-HTTP | Executable | Useful foundation for Oracle-Web, Siebel-Web, SAP-Web, DevWeb/JMeter/Gatling imports |
| WebSocket, gRPC, JMS | Executable | Not directly part of these bundles, but validates pluggable protocol architecture |
| Browser canaries | Executable as diagnostic canaries | Can cover SAP Web/Fiori user journeys, not SAP GUI desktop client parity |
| Runtime settings/import governance | Mature for Web/API and LoadRunner migration | Needs bundle-specific support matrix, import warnings, entitlement gates, and runner placement |
| Native database clients | SQL-style `database` protocol implemented with `database/sql`, SQLite fixture coverage, transactions, binds, row assertions, and read-only guard | Oracle-2 Tier still requires governed client-runner prerequisites |
| Network application protocols | `network` protocol implemented for DNS, FTP control commands, SMTP, POP3, IMAP, LDAP bind/search, and TCP/UDP Winsock-style flows | MAPI and Tuxedo remain review-only/client-runner candidates; FTP data-channel file transfer needs a fixture-backed extension |
| Oracle NCA | Not implemented | Requires feasibility spike and likely proprietary/client runtime boundary |
| Template VUsers | Not implemented | Requires sandboxed external worker SDK for language runtimes |
| SAP GUI | Not implemented | Requires Windows/SAP GUI runner pool and low-density desktop execution governance |

## Implementation Checkpoint

Status as of 2026-05-19:

| Workstream | Status | Evidence |
| --- | --- | --- |
| B0 | Implemented | `GET /api/v1/protocol-bundles/support-matrix`, code-owned bundle catalog, runner classes, entitlement rows, RBAC coverage |
| DB1 | Implemented for native SQL runtime; Oracle-2 Tier explicitly external-runner required | `pkg/protocol/database`, database DSL validation, engine config propagation, support-matrix evidence |
| NET1 | Implemented for standards-based native protocols; MAPI/Tuxedo finalized as review-only | `pkg/protocol/network`, network DSL validation, engine config propagation, support-matrix evidence |
| OEB1 | In progress | Support-matrix rows exist; Oracle-Web/Siebel-Web review packs and NCA review-only endpoint still pending |

## Parity Principles

1. Do not compromise high-density Linux protocol execution.
   HTTP-like protocols should stay on Go generator pods; desktop/client protocols should run in isolated pools.

2. Make protocol bundle support code-owned.
   Add a bundle support matrix in backend code, expose it through API/UI, and keep documentation generated from the same concepts where possible.

3. Treat proprietary desktop protocols as governed runners.
   SAP GUI, Oracle NCA, MAPI, Tuxedo, and native template VUsers need explicit placement, prerequisites, density limits, and audit evidence.

4. Prefer import and review before execution.
   For each bundle, first classify existing LoadRunner assets, preserve source metadata, and show gaps before attempting execution.

5. Separate entitlement from capability.
   The UI/API should say whether a project is entitled, whether the runtime exists, and whether required runner pools are available.

## Importance Grouping

| Importance | Work | Reason |
| --- | --- | --- |
| P0 | Bundle registry, entitlement model, support matrix, import classification | Prevents vague parity claims and gives operators a reliable map of what Stormrunner can run, review, or defer |
| P0 | Network subset MVP: DNS, FTP, SMTP, POP3, IMAP, LDAP | Mostly standards-based and feasible in Go; broad enterprise usefulness |
| P0 | Database MVP: SQL/ODBC-style execution model plus Oracle strategy | High enterprise value; unlocks direct DB service tests and migration of common database scripts |
| P0 | SAP GUI foundation entitlement and runner architecture | SAP GUI is business-critical and operationally different from HTTP/browser canaries |
| P1 | Oracle-Web/Siebel-Web correlation packs | Builds on existing HTTP runtime while addressing packaged-app parity |
| P1 | Template VUser worker SDK | Unlocks manually developed custom protocols without embedding arbitrary code in generator pods |
| P1 | Oracle NCA feasibility and MVP | Valuable but complex; likely needs client/runtime evidence before committing to full native parity |
| P2 | MAPI, Tuxedo, advanced Winsock, C/C++ native template execution | Higher proprietary/runtime risk and lower generalized reuse |

## Workstream B0: Bundle Registry, Entitlement, And Governance

Goal: create the control-plane foundation before adding individual runtimes.

| ID | Work Item | Priority | Exit Criteria |
| --- | --- | --- | --- |
| B0.1 | Add code-owned protocol bundle catalog for Database, Network, Oracle E-Business, Templates, SAP GUI entitlement, and existing Web/API bundles | P0 | API returns protocol, bundle, status, execution posture, runner requirement, density envelope, and source evidence |
| B0.2 | Add `GET /api/v1/protocol-bundles/support-matrix` | P0 | Dashboard and docs can consume one backend-owned matrix |
| B0.3 | Extend project governance with protocol entitlements | P0 | Project admins can allow/deny bundle use independently of runtime availability |
| B0.4 | Extend generator inventory with `runner_os`, `protocol_bundles`, `client_prerequisites`, and `desktop_session_capacity` | P0 | Scheduler can place Linux, Windows, and client-dependent protocols safely |
| B0.5 | Add dashboard bundle cards and parity gaps | P1 | Operators can see supported, review-only, external-runner, deferred, and blocked protocols |
| B0.6 | Add audit events for entitlement changes and external-runner execution | P0 | Protocol use is auditable by project, bundle, script, runner pool, and user |

Recommended API shape:

```json
{
  "bundle": "network",
  "protocol": "smtp",
  "status": "planned",
  "execution_posture": "native_linux",
  "runner_requirement": "go_generator",
  "entitlement_required": "network",
  "density_envelope": "5000-10000 sessions per generator pending benchmark",
  "source": "OpenText license bundles solution overview 2025"
}
```

## Workstream DB1: Database Protocol Bundle

OpenText bundle target: ODBC and Oracle-2 Tier.

### Recommended Parity Position

Stormrunner should implement a first-class `database` protocol with two execution paths:

- Standards-oriented SQL runner for DSN/JDBC/ODBC-like SQL workflows.
- Oracle client runner for Oracle-2 Tier, isolated behind a generator-local client bridge because Oracle client libraries and deployment prerequisites are not equivalent to pure Go HTTP execution.

### Functional Scope

| Capability | MVP | Later |
| --- | --- | --- |
| Connections | Per-VUser connection lifecycle, pooled setup, reconnect policies | Advanced connection storms and warm pool tuning |
| SQL execution | Execute/query/stored procedure actions, bind variables, row assertions | Cursor navigation, array binds, LOB streaming |
| Transactions | Begin/commit/rollback, autocommit, transaction timers | Savepoints and distributed transactions |
| Data correlation | Extract first row/column, named columns, row counts | Multi-result set correlation and Oracle-specific metadata |
| Secrets | DSN/user/password from secret store | Kerberos/wallet/advanced Oracle auth |
| Reporting | SQL timings, rows affected, rows returned, DB error code/state | Wait-event and server-side diagnostics integrations |
| Import | Classify LoadRunner DB assets, extract SQL and runtime settings | Full VuGen database API mapping |

### Work Items

| ID | Work Item | Priority | Exit Criteria |
| --- | --- | --- | --- |
| DB1.1 | Add `database` DSL schema: connection, driver, action type, SQL text, binds, transaction mode, assertions | P0 | Parser/validation supports database scripts without opaque metadata |
| DB1.2 | Add native SQL runtime abstraction using `database/sql` where viable | P0 | Local SQLite/Postgres test fixture executes SQL actions end-to-end |
| DB1.3 | Add ODBC bridge design and runner prerequisites | P0 | Operators can configure ODBC DSN/client requirements without baking them into all generators |
| DB1.4 | Add Oracle-2 Tier bridge strategy | P0 | Oracle client dependency, image, secrets, and licensing assumptions are explicit |
| DB1.5 | Add import classifier for LoadRunner database scripts | P1 | Import returns review sections for SQL, connections, transactions, unsupported APIs, and required clients |
| DB1.6 | Add dashboard database editor panels | P1 | Authors can edit SQL, binds, transaction behavior, and assertions |
| DB1.7 | Publish database density benchmarks | P1 | Density envelope includes connection count, query mix, server latency, and client CPU/memory |

## Workstream NET1: Network Protocol Bundle

OpenText bundle target: DNS, FTP, IMAP, LDAP, MAPI, POP3, SMTP, Tuxedo, Windows Sockets.

### Recommended Parity Position

Implement standards-based protocols natively first, then gate proprietary/binary protocols behind feasibility and external-runner design.

| Protocol | Recommended Stormrunner posture |
| --- | --- |
| DNS | Native Go runtime |
| FTP | Native Go runtime with upload/download/list/delete actions |
| SMTP | Native Go runtime with TLS/SASL and message template support |
| POP3 | Native Go runtime |
| IMAP | Native Go runtime |
| LDAP | Native Go runtime with bind/search/add/modify/delete scope controls |
| Windows Sockets | Native low-level TCP/UDP scripted socket runtime for deterministic byte flows |
| MAPI | Review-only first; likely Windows/client runner or Microsoft Graph/EWS alternative guidance |
| Tuxedo | Review-only first; native client bridge only if customer-backed samples and client libraries are available |

### Work Items

| ID | Work Item | Priority | Exit Criteria |
| --- | --- | --- | --- |
| NET1.1 | Add `network` DSL with subprotocol-specific action schemas | P0 | DNS/FTP/mail/LDAP/socket scripts validate explicitly |
| NET1.2 | Implement DNS runtime | P0 | Resolve/query actions report latency, response code, records, and failures |
| NET1.3 | Implement FTP runtime | P0 | FTP connect/login/control-command diagnostics execute against test server; data-channel upload/download/list/delete remains an explicit fixture-backed extension |
| NET1.4 | Implement SMTP/POP3/IMAP runtimes | P0 | SMTP send and POP3/IMAP command flows execute with auth/TLS settings and protocol diagnostics |
| NET1.5 | Implement LDAP runtime | P0 | Bind/search flows execute through an LDAP client library with base-DN/filter/attribute controls |
| NET1.6 | Implement Windows Sockets runtime | P1 | TCP/UDP connect/send/receive/close actions support binary and text payloads |
| NET1.7 | Add MAPI and Tuxedo classifiers | P1 | Import clearly marks runner/client requirements and blocks unsupported execution |
| NET1.8 | Add network protocol dashboards and CSV exports | P1 | Reports show protocol-specific status codes, bytes, records/messages, and timings |
| NET1.9 | Publish benchmark envelopes | P1 | Each native protocol has generator density guidance and failure-mode evidence |

## Workstream OEB1: Oracle E-Business Protocol Bundle

OpenText bundle target: DevWeb, JMeter, Gatling, Oracle NCA, Oracle-Web, Siebel-Web, Web-HTTP/HTML.

### Recommended Parity Position

Stormrunner already has generic HTTP, DevWeb, JMeter, Gatling, and Web/API foundations. The parity gap is packaged-app fidelity: Oracle-Web/Siebel-Web correlation knowledge, Oracle NCA client/protocol handling, and scenario templates.

### Work Items

| ID | Work Item | Priority | Exit Criteria |
| --- | --- | --- | --- |
| OEB1.1 | Add Oracle E-Business bundle rows to support matrix | P0 | DevWeb/JMeter/Gatling/Web-HTTP rows show current support and package-specific caveats |
| OEB1.2 | Add Oracle-Web import/correlation pack | P1 | Review sections identify session IDs, forms tokens, view state, ICX/session values, and data-source fit |
| OEB1.3 | Add Siebel-Web import/correlation pack | P1 | Review sections identify Siebel session/correlation tokens, dynamic row IDs, and navigation state |
| OEB1.4 | Add Oracle E-Business scenario templates | P1 | Authors can start from login, responsibility selection, search/create/update, and logout flows |
| OEB1.5 | Add Oracle NCA feasibility spike | P0 | Decision record names runtime approach, required client libraries, sample assets, and support/non-support boundary |
| OEB1.6 | Add Oracle NCA review-only import | P1 | Imports preserve source fragments and block execution until runner support exists |
| OEB1.7 | Implement Oracle NCA runner if funded | P2 | End-to-end script runs against representative Oracle Forms/NCA target with diagnostics |

## Workstream TPL1: Templates Protocol Bundle

OpenText bundle target: C Vuser, C++ .NET, C# .NET, Java Vuser, VB .NET. OpenText describes Template VUsers as manually developed and without recording capability.

### Recommended Parity Position

Do not execute arbitrary template code inside the main Go generator process. Build a sandboxed Template VUser worker contract and language-specific worker images.

### Worker Model

Template VUsers should run as sidecar or external worker containers with a narrow gRPC contract:

- `InitVUser`
- `RunIteration`
- `EndVUser`
- `StartTransaction`
- `EndTransaction`
- `GetParameter`
- `SetCorrelation`
- `EmitMetric`
- `LogEvent`

### Work Items

| ID | Work Item | Priority | Exit Criteria |
| --- | --- | --- | --- |
| TPL1.1 | Add `template_vuser` DSL schema and lifecycle contract | P0 | Template scripts validate without embedding language-specific runtime details |
| TPL1.2 | Add Template Worker SDK contract | P0 | Generator can drive external workers and collect metrics/errors deterministically |
| TPL1.3 | Implement Java Vuser worker | P1 | Java template sample runs end-to-end with parameters, transactions, and error reporting |
| TPL1.4 | Implement .NET worker for C# and VB .NET | P1 | .NET template sample runs in isolated worker image |
| TPL1.5 | Decide C/C++ strategy: native container, WASI, or unsupported | P1 | Security review and runtime decision are documented before implementation |
| TPL1.6 | Add LoadRunner template import classifier | P1 | Imports preserve files, language, compile hints, and unsupported APIs |
| TPL1.7 | Add build/test pipeline for template workers | P1 | Workers compile, run unit tests, and publish compatibility metadata |
| TPL1.8 | Add dashboard Template VUser UX | P2 | Authors see lifecycle, files, build status, logs, parameters, and approval gates |

## Workstream SAP1: Foundation Entitlement For SAP GUI

OpenText lists SAP GUI under SAP protocol coverage and the SAP solutions data sheet highlights SAPGUI, SAP WebGUI, Web Dynpro, SAP Fiori/SAPUI5, IDoc, RFC, and OData coverage. For Stormrunner, this workstream defines a Foundation Entitlement that unlocks SAP GUI governance and a low-density SAP GUI runner lane.

### Recommended Parity Position

SAP GUI is not a browser protocol and should not run on Linux HTTP generators. Stormrunner should provide:

- Project-level SAP GUI entitlement.
- Windows SAP GUI runner pool.
- SAP GUI client prerequisite validation.
- Low-density desktop session scheduling.
- Import/review of existing SAP GUI scripts.
- SAP-Web/Fiori guidance using existing HTTP/browser paths where appropriate.

### Work Items

| ID | Work Item | Priority | Exit Criteria |
| --- | --- | --- | --- |
| SAP1.1 | Add SAP GUI entitlement and support-matrix rows | P0 | UI/API distinguish SAP GUI, SAP-Web, SMP, and OData/Fiori pathways |
| SAP1.2 | Add Windows runner pool model | P0 | Fleet inventory reports Windows hosts, SAP GUI version, scripting enabled, capacity, and drain state |
| SAP1.3 | Add SAP GUI script import classifier | P0 | Import preserves transactions, screen/object identifiers, synchronization points, parameters, and unsupported calls |
| SAP1.4 | Define SAP GUI action DSL | P0 | Script model supports logon, transaction code navigation, set field, press button, table/grid interaction, wait/sync, status bar assertion, and logout |
| SAP1.5 | Implement SAP GUI runner bridge MVP | P1 | One SAP GUI workflow executes on a Windows runner with screenshots/logs/timers |
| SAP1.6 | Add SAP GUI authoring/replay dialog | P1 | Authors can review screens, object IDs, synchronization, status-bar checks, and parameter data |
| SAP1.7 | Add SAP GUI scheduling and density controls | P1 | Scheduler enforces desktop session capacity, off-hours rules, credentials, and runner isolation |
| SAP1.8 | Add SAP-specific reporting | P1 | Reports include transaction code, screen, status-bar message, SAP error class, screenshot evidence, and runner host |
| SAP1.9 | Add SAP-Web/Fiori templates using HTTP/browser canary patterns | P1 | SAP WebGUI/Fiori flows can be modeled without claiming SAP GUI desktop parity |

## Cross-Cutting Runtime Architecture

| Concern | Plan |
| --- | --- |
| Runner placement | Extend fleet placement to match protocol bundle, OS, client prerequisites, region, capacity, and drain/cordon state |
| Secrets | Use project-scoped secrets for database credentials, mail accounts, LDAP bind users, SAP users, and Oracle clients |
| Audit | Log protocol entitlement changes, runner assignment, script approval, credential usage reference, and external worker execution |
| Sandbox | External/template/desktop runners must not run arbitrary code in controller or Linux generator process |
| Diagnostics | Every protocol action emits protocol-specific diagnostics plus normalized transaction timing and error snapshots |
| Reporting | Archive exports include protocol, bundle, runner type, client prerequisite version, and protocol-specific fields |
| Load modeling | Scenario validation blocks demand that exceeds protocol-specific runner capacity or client prerequisites |
| Migration | Importers always preserve unsupported source fragments and produce deterministic repair suggestions |

## Implementation Roadmap

| Phase | Scope | Outcome |
| --- | --- | --- |
| Phase 0 | Bundle catalog, entitlement model, support-matrix API, runner placement schema | Operators can see and govern requested bundle gaps before runtime work starts |
| Phase 1 | Database MVP plus Network MVP for DNS/FTP/SMTP/POP3/IMAP/LDAP | First executable standards-based protocol bundle expansion |
| Phase 2 | Network socket runtime, Oracle-Web/Siebel-Web packs, database import, dashboards | Packaged-app and network UX begins to feel LoadRunner-like |
| Phase 3 | Template Worker SDK with Java and .NET workers | Manual Template VUsers have a safe execution path |
| Phase 4 | SAP GUI foundation entitlement, Windows runner pool, import classifier, SAP GUI DSL | SAP GUI assets become governable and runnable in low-density MVP form |
| Phase 5 | Oracle NCA decision/MVP, C/C++ template decision, MAPI/Tuxedo feasibility | High-complexity proprietary gaps are either implemented or explicitly deferred |
| Phase 6 | Benchmarks, release docs, parity dashboard, migration fixtures | Bundle claims become release-grade and evidence-backed |

## Acceptance Criteria

Stormrunner should not mark a requested bundle as complete until:

- The support matrix identifies every OpenText-listed protocol in the bundle.
- Each protocol has one of: executable, review-only, external-runner required, deferred, or out-of-scope.
- Executable protocols have parser, validation, runtime, reporting, and import/review coverage.
- External-runner protocols have runner prerequisites, placement, audit, health, and capacity controls.
- Dashboard authoring surfaces protocol-specific settings and does not hide unsupported behavior in metadata.
- Archive reports and CSV exports include protocol-specific diagnostics.
- Benchmark envelopes are published for each executable runtime path.
- Docs include migration guidance and known non-parity limits.

## Test Plan

| Area | Required Tests |
| --- | --- |
| Bundle catalog | Unit tests for matrix rows, statuses, entitlements, and source metadata |
| API/RBAC | Handler tests for support matrix, entitlement allow/deny, audit events, and project scoping |
| Database | Parser tests, SQL runtime fixture tests, transaction/error tests, secret redaction tests, import classifier tests |
| Network | Containerized DNS/FTP/mail/LDAP fixtures, TLS/auth tests, socket binary payload tests, timeout/failure tests |
| Oracle E-Business | HTTP correlation fixture tests, Oracle/Siebel import review tests, unsupported NCA preservation tests |
| Templates | Worker contract tests, worker timeout/crash tests, Java/.NET sample execution tests, build log preservation tests |
| SAP GUI | Import classifier tests, runner capability tests, scheduler capacity tests, Windows runner smoke tests, screenshot/log evidence tests |
| Reporting | Archive JSON/CSV tests for protocol diagnostics, runner metadata, and entitlement evidence |
| Dashboard | Support matrix cards, protocol editors, import review dialogs, SAP GUI runner status, and template worker logs |
| Benchmarks | Per-protocol density, memory, latency, connection/session capacity, and failure recovery evidence |

## Key Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| SAP GUI and Template VUsers require arbitrary desktop/code execution | Security and stability risk | Isolate in Windows or worker pools; never run inside controller or high-density Go generator |
| Oracle client and ODBC dependencies vary by customer | Deployment complexity | Model client prerequisites in fleet inventory and block placement when missing |
| MAPI/Tuxedo/Winsock scope expands into vendor-specific edge cases | Long delivery cycle | Start with classifiers and customer-backed fixtures before native runtime commitments |
| Database load accidentally becomes production data mutation | Compliance/data risk | Require explicit read/write classification, approval gates, transaction rollback options, and audit events |
| SAP GUI density expectations mirror HTTP density | Capacity surprise | Publish low-density envelopes and schedule by desktop session capacity |
| Template workers execute unsafe code | Platform risk | Signed worker images, restricted network/file permissions, timeouts, and project-level approval gates |

## Open Decisions

1. Which customer fixtures should define MVP parity for ODBC and Oracle-2 Tier?
2. Should ODBC be implemented through native driver bindings, a Windows/Linux bridge, or a customer-provided sidecar image?
3. Which mail/LDAP auth mechanisms are required for the first Network bundle release?
4. Is Windows Sockets parity limited to deterministic TCP/UDP byte flows, or must it import arbitrary VuGen Winsock scripts?
5. Is Oracle NCA customer-funded and supported by available test assets/client libraries?
6. Which Template VUser language should be implemented first: Java or C# .NET?
7. What SAP GUI client versions and SAP scripting settings must the Windows runner support first?
8. Is SAP GUI Foundation Entitlement intended as project-level governance only, or must it also map to license accounting and chargeback?

## Recommended First Implementation Slice

Start with Phase 0 plus two executable standards-based proofs:

1. Add the protocol-bundle support matrix and entitlement skeleton.
2. Add native DNS runtime as the smallest Network bundle proof.
3. Add database DSL and a local SQL fixture runtime before Oracle/ODBC bridge work.
4. Add import classifiers for SAP GUI, Templates, Oracle NCA, MAPI, and Tuxedo so existing LoadRunner assets are preserved and visibly blocked rather than silently lost.

That slice creates immediate governance value, proves the protocol-bundle architecture, and avoids committing to proprietary desktop/client execution before runner prerequisites are explicit.

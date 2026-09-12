# Access audit implementation

Implemented September 12, 2026, against the current working tree. This follow-up addresses the twelve findings in [REPORT.md](REPORT.md). Other tasks are concurrently changing Help, Monitoring, and other application areas; those changes are outside this implementation.

| Finding | Implemented behavior |
| --- | --- |
| F1 | Explicit settings destinations determine the rendered section. Feature policy stays selected during permission loading or denial. Access waits for initial permission resolution before applying subtab eligibility and stays mounted during later permission refreshes. |
| F2 | Events use server pagination with a lookahead row, stable ordering, an upper timestamp, and explicit loaded-page counts. Complete CSV export traverses every matching page, preparing the file before responding so storage failures cannot return a successful partial export. |
| F3 | Events, Usage, and Access Review have distinct titles and CSV exports. Usage exports user buckets and filter metadata; review exports identities, priorities, reasons, owners where available, and generation time. Spreadsheet formulas are escaped. |
| F4 | Each finding links to its exact user or service-account inspector and existing edit/rotation controls. User lookup is by exact ID; service-account remediation refreshes the directory and refuses missing targets instead of selecting another account. Review distinguishes informational disabled identities from actionable expiry/rotation/broad-role findings, displays owners, and offers refresh/retry. No persisted review-disposition system existed, so no fictitious last-reviewed state is shown. |
| F5 | Multiple-selection role/scope buttons expose `aria-pressed`; scoped role selectors name their domain/project/repository. Focus styling is explicit. |
| F6 | Creation views hide existing-principal inspectors and their lifecycle actions. |
| F7 | Review and role-preview tables use the shared table presentation, readable reason badges, horizontal overflow, and consistently sized action buttons. |
| F8 | The tool is named Role permissions preview and states its authorization limits. Scope names are readable; user search and pagination are independent of the Users table. |
| F9 | Access policy destinations share persistent Access navigation and a visible return path. All seven identity/activity views support scoped URLs, reloads, and browser history. Subtab route changes retain the same page instance so filters, selected principals, and one-time key dialogs survive navigation. |
| F10 | Usage has independent domain/project/actor/run-creation-date filters, metric explanations, generated time, and matching run links. Backend predicates apply to the same authorized run set, preventing actor filters from reintroducing another project's runs. Invalid date ranges are rejected. |
| F11 | Creation, empty views, Usage, and Review use full width when no inspector exists. Users/accounts offer optional metadata columns and pinned identity/action columns. |
| F12 | Creation and rotation labels identify the action and days. Onboarding explains provider identity creation, invitations, and local/dev login behavior. Redaction has a resource-specific save label. Read-only secrets link to provider setup guidance. Issued-key examples use the configured API origin, including desktop configurations. |

## Regression coverage

- Backend fixtures cover 1,005 events, exact final pages, timestamp ties, full exports despite a 50-row limit, CSV formula escaping, storage failure before export delivery, delegated scope plus actor filtering, domain/date filtering, usage drill-down pagination, and malformed dates.
- Component coverage exercises event pagination and applied-versus-draft filters; separate Usage exports, dates, retry, and run links; exact-user review remediation; creation inspector visibility; selection semantics; permission-loading/denial routing; and URL/CSV utilities.
- Real-controller browser workflows exercise every Access policy destination, Feature policy reloads, creation deep links, permission selector semantics, 56 matching audit events with a complete download, Usage browser history, review remediation, and narrow layouts.

## Validation status

Browser tests use disposable databases and controller ports; existing live users, credentials, secrets, and policies were not altered.

- **Passed:** 169 focused component/utility tests across AccessPage, SettingsPage, and Access reporting/navigation helpers.
- **Passed:** backend Access history/export and usage-scope regressions, including delegated HTTP requests and failure-before-download behavior.
- **Passed:** three strengthened real-controller Access browser workflows, including actual service-account issuance, retained Events filters, exact-user remediation, newly created service-account remediation from a stale directory, and compact table fit at 1440px. The Settings-search workflow was updated to respect the new Access shell and passed separately.
- **Passed:** final TypeScript check, lint for the changed Access/Settings files, generated TypeScript API-contract freshness check, and visual bundle build. Screenshots were inspected after the final changes.
- **Broader validation is not green:** the full development browser profile reported 93 passes, one skip, and the obsolete Settings-navigation test; that test was corrected and passed on rerun. The local-auth profile reported 14 passes and three failures outside Access (the changing Results-search UI and two Monitoring fixtures lacking authentication).
- The full Go suite was run twice. The latest run failed on concurrent API/schema work (contract-matrix freshness, a fleet-capacity panic, threshold-incident Postgres predicates, foreign-key inventory and schema fingerprint) and a desktop staging-file cleanup test. The desktop test passed in a focused rerun. Access regressions passed.
- Earlier combined frontend-cycle attempts encountered changing Monitoring/Repository lint failures and an import-review timeout. The last combined run passed TypeScript and global lint, then reported **4,124 passing tests and one failing ResultsPage calendar-filter test** (380 passing files, one failing file). Final remediation changes were subsequently verified by the 169 focused tests, changed-file lint, and all three strengthened Access browser workflows.

These results distinguish Access verification from repository-wide integration status; they do not claim a clean build of all concurrent work.

## Visual checks

- [Desktop Users table, compact columns and retained review selection](after-users-desktop.png)
- [Narrow Access Review with readable actions and flags](after-review-narrow.png)
- [Narrow creation form with named permission selectors](after-create-permissions-narrow.png)

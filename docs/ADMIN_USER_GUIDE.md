# APEX Load Admin Guide

This guide is for APEX Load administrators — the people who onboard users, assign
roles, run the generator fleet, and configure the platform. It focuses on the
administrative surfaces of the dashboard rather than day-to-day test authoring, which
is covered in the [User Guide](APPLICATION_USER_GUIDE.md).

You can see this guide because your account holds an administrative capability. Not
every administrator can do everything below: APEX Load uses fine-grained, scoped
permissions, so a project administrator sees a narrower set of controls than a system
administrator. Where a task needs a specific capability, this guide says so.

Administrative controls are grouped by task:

- **Access** (sidebar) — users, roles, service accounts, activity, and audit.
- **Monitoring → Fleet** — generators, pools, capacity, and provisioning.
- **Settings** — runtime configuration, integrations, capacity, scheduling and retention.
- **Access → Authentication & policy** — identity configuration, import policy, redaction and project secrets.

## Administrator Roles At A Glance

APEX Load ships four assignable roles, from least to most privileged:

- **viewer** — read-only access within its scope.
- **editor** — can author scripts, scenarios, and runs within its scope.
- **project_admin** — can administer users' role bindings within a scope (a project,
  a domain and its child projects, an application repository, or globally), but cannot
  create or delete users or grant system-wide roles.
- **system_admin** — full administrative control: manage all users and bindings, grant
  any role, and manage system settings.

Roles are granted as **scoped bindings**, not as a single global label. A user can be
a `viewer` globally, an `editor` on one project, and a `project_admin` on another —
all at once. This lets you delegate administration without handing out full
system access.

### Delegated Administration

Delegation is deliberately bounded so you can hand out administrative help safely:

- **system_admin** manages all users and bindings and can grant global roles,
  including `system_admin`.
- A **project_admin** receives the `manage_user_bindings` capability but **not**
  `manage_users`. That means they can grant and revoke role bindings inside their own
  scope, but they cannot create users, edit user metadata, disable or enable users,
  grant `system_admin`, or create bindings broader than their own scope.
- Scope narrows the reach: a global project admin can manage scoped bindings
  everywhere but cannot grant global roles; a domain project admin manages their
  domain and its child projects; a project project admin manages just their project; a
  repository project admin manages just their application repository.

The practical rule: delegate `project_admin` at the narrowest scope that lets someone
do their job, and reserve `system_admin` for platform owners.

## The Access Console

Open **Access** from the sidebar. This is the primary administrative console, with
tabs for **Users**, **Service Accounts**, and **Activity** (which includes an
audit/review view). What you can do here depends on your capabilities — lifecycle
actions such as creating or deleting users require `manage_users` (system admin);
binding actions require `manage_users` or `manage_user_bindings`.

## Managing Users

Human access to APEX Load is controlled by **local user records** plus **role
bindings**. Even when identity comes from single sign-on (OIDC) or an authentication
gateway, a person needs an active local user whose subject matches their authenticated
identity, and their effective roles come from local bindings — not from upstream role
headers. Onboard people before you turn on production access for a team.

From **Access → Users** you can:

- **Create / onboard a user.** Add a local record for the subject your identity
  provider emits (for example, an OIDC subject), with an email and display name.
  Requires `manage_users`.
- **Assign roles.** Grant scoped role bindings (see the next section).
- **Disable / enable a user.** Prefer **disabling** over deleting — disabling blocks
  access while preserving the user's record, role history, and audit trail. Re-enable
  at any time. Requires `manage_users`.
- **Revoke sessions.** Immediately invalidate a user's active single-sign-on sessions
  (for example, when someone leaves or a device is lost). Existing sessions can no
  longer authenticate after this.
- **Delete a user.** A hard removal — use disabling instead unless you specifically
  need to remove the record. Requires `manage_users`.

## Roles And Scoped Bindings

A **binding** ties a role to a scope. When you grant a binding you choose:

- **Role** — `viewer`, `editor`, `project_admin`, or `system_admin`.
- **Scope type** — `global`, `domain`, `project`, or `application_repository`.
- **Scope** — the specific domain, project, or repository (omit for `global`).

Grant the narrowest scope that fits. If someone only works on one project, bind them
as `editor` on that project rather than globally. Remove bindings when they are no
longer needed; every grant and revocation is recorded in the audit log.

Use **Access → Activity → Access Review → Role permissions preview** to inspect
operations granted by role bindings in the selected scope. This is not a complete
API authorization check: account status, service-account token scopes, and
route-specific policies can still deny those operations.

## Service Accounts And API Keys

Automation — CI/CD pipelines, the MCP server, reporting exporters, external
integrations — should authenticate as a **service account**, not as a person. Manage
these under **Access → Service Accounts**.

- **Create a service account** with the roles and token scopes it needs. APEX Load
  shows the generated **API key exactly once** — copy it immediately and store it in a
  secret manager; only a hash is retained afterward.
- **Rotate a key** when it may be compromised or on a routine schedule. Rotation
  issues a new key (again, shown once) and invalidates the old one.
- **Scope tightly.** A service account must satisfy **both** its RBAC role and its
  token scopes. For example, an account with `system_admin` still needs a compatible
  scope such as `settings:manage` before it can change system settings. Give each
  automation only the roles and scopes it actually uses.

Note that some sensitive operations — notably writing project secrets — are
intentionally unavailable to service accounts even with broad roles, and require an
authorized human workflow.

## Project secret providers

A read-only provider requires the deployment administrator to supply values through
the configured environment or secret manager; the dashboard cannot create or rotate
values there. Writable providers (an encrypted database keyed by a wrapping key, or
Azure Key Vault) need controller configuration and, for Key Vault, the Secrets Officer
role. Protect and back up wrapping keys; losing them makes encrypted values
unreadable. The environment variables, provider table, and migration steps are
maintained in [Project Secrets](PROJECT_SECRETS.md).

## Activity, Audit, And Access Review

The **Access → Activity** tab combines the audit log with per-user run-usage
attribution, so you can answer both "who changed what" and "who ran what."

- **Filter** activity by user, timeframe, domain, project, action, actor type,
  resource, or status to investigate a specific change or usage pattern.
- **Export** audit events or user activity as CSV for governance and compliance
  reviews.
- **Access review** surfaces evidence for the scopes you administer, so periodic
  entitlement reviews are grounded in real data rather than guesswork.

Export audit events after significant governance changes (role grants, policy edits)
so you have a durable record.

## Managing The Generator Fleet

APEX Load runs load from **generators** — the worker nodes that host virtual users.
Manage them under **Monitoring → Fleet**. Some actions here require the
`manage_system` capability.

The Fleet view surfaces:

- **Generators** — live and persistent generator records with health, region, pool,
  and tags. You can update a generator's target, region, pool, provider, cordon
  state, or tags.
- **Capacity** — aggregated placement capacity and generator health, so you can tell
  whether the fleet can absorb a proposed run.
- **Pools and load zones** — group generators into pools and map them to regions/load
  zones, then target scenarios at the right pool.
- **Project pool quotas** — reserve capacity for specific projects.
- **Alerts** — fleet alerts with acknowledgement state.
- **Provisioning requests** — request additional capacity. When no live cloud or
  Kubernetes provisioner is installed, requests are recorded honestly as
  `not_configured` rather than silently doing nothing.

### Generator Lifecycle Controls

When you need to take a generator out of service for maintenance or scaling:

- **Cordon** — exclude a generator from **new** placement while leaving current work
  alone. **Uncordon** returns it to active placement.
- **Drain** — mark a generator as draining so work winds down.
- **Evacuate** — move active distributed runs off a generator. Run it as a dry run
  first (`dry_run`) to preview the impact before you apply it.

Because generators run on Kubernetes, pod restart and drain controls are available for
operational maintenance; follow your deployment's Kubernetes restart runbook when
cycling generator pods so in-flight runs are handled cleanly.

## System And Organization Settings

Use the task navigation and destination search in [Settings](#/settings). System-level
writes require the relevant administrative capability. Common destinations include:

- [Controller & runtime](#/settings?setting=platform) and [Edition & deployment limits](#/settings?setting=entitlements).
- [Alert & stop policies](#/settings?setting=thresholds), [Schedule defaults](#/settings?setting=scheduling), and [Notification routes](#/settings?setting=notifications).
- [Kubernetes clusters](#/settings?setting=clusters), [Generator pools](#/settings?setting=pools), and [Standing allocations](#/settings?setting=allocations).
- [Domains & placement defaults](#/settings?setting=domains), [Projects](#/settings?setting=projects), and [Applications & releases](#/settings?setting=applications).
- [Project limits & quotas](#/settings?setting=limits) and [Archive & HAR retention](#/settings?setting=retention).
- [Access → Authentication](#/access?setting=authentication), [Import credential policy](#/access?setting=imports), [Project redaction rules](#/access?setting=redaction), and [Project secrets](#/access?setting=secrets).
- [Repository → Shared data pools](#/repository?setting=test-data) for reusable input rows.

### Authentication

The **Authentication** settings reflect how identity is proven for your deployment —
one of `dev` (local development only), `local` (seeded test personas, non-production), `trusted_headers` (an upstream gateway forwards
identity), or `oidc` (APEX Load handles login and sessions directly). Regardless of
mode, human authorization always resolves through local user records and role
bindings, so onboarding users is a prerequisite for production access.

### Governance

Policy is grouped by task: [Schedule defaults](#/settings?setting=scheduling),
[Import credential policy](#/access?setting=imports), service-account API governance,
and [Governance audit](#/access?setting=audit). Use these to set the
guardrails that keep self-service safe — for example, requiring review before certain
imports or runs proceed.

## Governance And Approvals

APEX Load can require sign-off before sensitive work proceeds. As an administrator you
review and act on these gates:

- **Execution plan approvals** — gated plans wait for an approver before they run.
  Review and approve or reject plan gates from the plan's approvals view.
- **Schedule approvals** — scheduled occurrences can require approval before they
  fire; approve or reject pending occurrences.
- **Assisted-authoring governance** — AI-assisted drafts cannot become reusable
  scripts on their own. They enter a review queue and must pass structural validation,
  diff review, replay evidence, and explicit approval. The provider allow-list and
  redaction posture are policy-controlled and default to a deterministic local
  provider; external LLM integration is off unless explicitly enabled.
- **External-runner gating** — drafts that require an external runner (for example, a
  Java Vuser JVM runner) may complete authoring review and library approval, but test
  launch remains blocked until the matching runtime capability and capacity are
  available. Combined Java drafts specifically require the generator capability
  `java_vuser_persistent_lifecycle_v1` and
  `java_vuser_session_frame_token_v1`. A qualified local or loopback-sidecar
  runner that advertises both capabilities keeps one JVM and one `Actions`
  instance alive per VUser across `init`, repeated `action` calls, and `end`.
  Windows Job Object containment and narrowly delegated Linux cgroups can
  qualify; the stock non-privileged Helm, Kustomize, and Compose sidecars are
  legacy `full_lifecycle` only. This keeps runtime availability separate from
  import-fidelity review.
- **Capacity reservations** — reservations start pending and require approval before
  they bind capacity to a run.

## Licensing And Entitlements

The [Edition & deployment limits](#/settings?setting=entitlements) view
is a read-only projection of what the deployment is entitled to and how much is being
used:

- **Edition** and configured limits (for example, maximum concurrent tests, service
  accounts, and projects).
- **Enabled protocol bundles** — which protocols this deployment can drive.
- **Usage** — deployment-wide and per-project consumption against the projection.

Entitlements report status; they are a visibility and planning aid rather than a hard
licensing enforcement engine. Use them to see edition, enabled protocols, and where
usage is approaching a configured limit.

## Operational Runbooks

Monitoring → Overview → Readiness & Services always shows the Kubernetes API,
configured metadata store (PostgreSQL or SQLite), and configured cache backend
(Redis, Valkey, or in-memory) with their current probe status and latency.
Kubernetes API or RBAC loss is reported as degraded without taking the
controller out of service; a configured PostgreSQL or Redis/Valkey outage
blocks production readiness.

For Kubernetes installs, restart controls are enabled by default for
`system_admin` users. Restart the controller from Monitoring → Overview →
Control Plane. Open Monitoring → Fleet → Generators → View details to restart
the generator StatefulSet or an individual generator pod. Every action still
requires an operational reason and exact target confirmation and is audit
recorded.

For deeper operational procedures beyond the dashboard, consult the in-repository
runbooks, including the Auth/RBAC operations runbook (user administration, break-glass
recovery, service accounts, and audit export), the enterprise generator fleet and
autoscaling models, the Kubernetes restart controls, and the enterprise data-platform
operations runbook. These live alongside this guide in the project documentation.

## Administrator Best Practices

- **Onboard first.** Create local users before enabling production OIDC or gateway
  access for a team, and confirm each person's effective access before they rely on it.
- **Disable, don't delete.** Disabling preserves audit history; reserve deletion for
  records you genuinely need to remove.
- **Delegate narrowly.** Grant `project_admin` at the smallest scope that works, and
  keep `system_admin` to platform owners.
- **Automate with service accounts.** Never share human credentials with pipelines;
  scope each service account to only the roles and token scopes it needs, and rotate
  keys on a schedule.
- **Keep the fleet healthy.** Cordon and drain before maintenance, dry-run evacuations,
  and watch fleet alerts and capacity before approving large runs.
- **Review regularly.** Use access review and audit export to run periodic entitlement
  reviews, and export audit events after major governance changes.

## In-App Operational References

Start with [Choose an installation](../deployments/README.md) for the environment-specific setup steps.

- [Identity and access operations](AUTH_RBAC_OPERATIONS_RUNBOOK.md)
- [Backup, restore, retention and recovery](ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md)
- [Deployment and HA architecture](ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md)
- [Cluster connections](CLUSTER_CONNECTIONS.md)
- [Generator restart controls](KUBERNETES_RESTART_CONTROLS.md)
- [SQLite to PostgreSQL migration](SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md)
- [Telemetry configuration](OPENTELEMETRY.md)
- [Support escalation template](HELP_SUPPORT.md)

For upgrades, record the current build, review the deployment release notes and storage compatibility, back up metadata and archives, verify a restore in an isolated environment, and follow your deployment's rollout/rollback procedure. Recheck identity, generator readiness and one small test after rollout. The runbooks above describe deployment-specific requirements; do not assume a database rollback is safe after a schema migration.

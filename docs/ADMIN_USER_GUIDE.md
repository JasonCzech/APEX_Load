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
- **Access → Security policy** — authentication, feature and import policy, redaction, project secrets and the governance audit.

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
tabs for **Users**, **Service accounts**, and **Activity** (which includes an
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

Use **Access → Activity → Access review → Role permissions preview** to inspect
operations granted by role bindings in the selected scope. This is not a complete
API authorization check: account status, service-account token scopes, and
route-specific policies can still deny those operations.

## Service Accounts And API Keys

Automation — CI/CD pipelines, the MCP server, reporting exporters, external
integrations — should authenticate as a **service account**, not as a person. Manage
these under **Access → Service accounts**.

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
maintained in [Application and Project Secrets](ADMIN_USER_GUIDE.md#application-and-project-secrets).

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
- [Access → Authentication](#/access?setting=authentication), [Import credential policy](#/access?setting=imports), and [Project redaction rules](#/access?setting=redaction); project and application secrets are in [Shared assets → Secrets](#/shared-assets?section=secrets).
- [Shared assets → Test data](#/shared-assets?section=test-data) for reusable input rows, and [Live tables](#/shared-assets?section=live-tables) for rows runs share and change.

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
- [Backup, restore, retention and recovery](ENTERPRISE_PLATFORM_RUNBOOK.md)
- [Deployment and HA architecture](ENTERPRISE_PLATFORM_RUNBOOK.md)
- [Cluster connections](CLUSTERS_AND_CLOUD.md#cluster-connections--host-resource-allocation)
- [Generator restart controls](CLUSTERS_AND_CLOUD.md#kubernetes-restart-controls)
- [SQLite to PostgreSQL migration](ENTERPRISE_PLATFORM_RUNBOOK.md#sqlite-to-postgresql-migration-guide)
- [Telemetry configuration](OBSERVABILITY_INTEGRATIONS.md#opentelemetry-tracing)
- [Support escalation template](HELP_CENTER.md#prepare-a-support-request)

For upgrades, record the current build, review the deployment release notes and storage compatibility, back up metadata and archives, verify a restore in an isolated environment, and follow your deployment's rollout/rollback procedure. Recheck identity, generator readiness and one small test after rollout. The runbooks above describe deployment-specific requirements; do not assume a database rollback is safe after a schema migration.

## Script and scenario application/release assignments

In the Scripts or Scenarios library, open an item's **More** menu and choose
**Change application/releases…**. Select an application and any existing releases
in the current project, then apply. Selecting **Unassigned (clear assignment)**
removes both the application and releases. An application can also be assigned
without any releases.

Select several local items to use the same action in the selection toolbar.
Bulk changes replace each item's complete assignment. The dialog previews the
replacement, skips unchanged items, and reports individual failures. Retry only
retries failed items. If another operator has changed an assignment, reload the
conflicting assignment and review it before applying again.

The **Assigned application/releases** column and assignment filters describe
editable library metadata. **Run releases** describes historical run associations.
Archived releases can remain assigned but cannot be newly assigned or selected
for a new launch. Repository browsing includes assigned items before their first
run and distinguishes assignments from run history.

Script changes require `manage_scripts`; scenario changes require `manage_tests`.
Assignments remain independent between a scenario and its scripts. They do not
change script definitions, scenario groups, existing runs, or saved schedules.
New and cloned assets begin unassigned.

For a new launch, the scenario's application is suggested. If it is unassigned,
explicitly assigned scripts suggest an application only when they agree. Assigned
releases are labeled in the release selector. Choose a release explicitly for
each fresh launch or new schedule, even when only one is assigned. You may choose
another application or release; changing application clears the pending release.
Restoring a saved draft preserves its chosen release.

The replacement API uses an assignment revision to reject stale writes. See the
[generated API specification](swagger.yaml) and
[endpoint catalog](api-endpoint-catalog.json) for the request and response contracts.
API and CLI execution clients retain their existing placement rules.

## Application and Project Secrets

APEX Load stores scripts with `{{secret.NAME}}` references. Application secrets
are shared by scripts assigned to one application and override same-name project
secrets. Managed runs use their selected application; direct script actions use
the script's library assignment. Rotation affects new executions while active
runs retain their pinned values. Secret values are
managed separately and are never returned by the Secrets APIs, written
into script JSON, archives, logs, fingerprints, or distributed script caches.

### Storage providers

The controller supports an encrypted database for new values and two external
providers for reading and rotating values that administrators provision there:

| Provider | Configuration | Persistence |
|---|---|---|
| Encrypted database | Configure `APEXLOAD_WRAPPING_MASTER_KEY` (local AES key), a versioned Azure Key Vault key in `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID`, or an AWS KMS key in `APEXLOAD_WRAPPING_KMS_KEY_ID`. Set at most one of the Key Vault and KMS ids; setting both is a startup error. | Encrypted value records in the configured SQLite/PostgreSQL store. |
| Azure Key Vault | Set `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED=true` and `APEXLOAD_AZURE_KEY_VAULT_URL`; use workload/managed identity. | Values in Key Vault; managed logical-name metadata in the APEX Load database. |
| AWS Secrets Manager | Set `APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED=true` and `APEXLOAD_AWS_SECRETS_MANAGER_REGION`; use an IAM role or another supported AWS credential source. | Values in Secrets Manager; managed logical-name metadata in the APEX Load database. |

Application secrets use separate encrypted database records or a separate logical
namespace in Azure Key Vault, AWS Secrets Manager, and the read-only environment
provider. Deleting an application removes its database records. The deletion API
reports names and providers of external values that an administrator must remove.
When an external provider is configured, the deletion response also marks
cleanup as required and supplies the deterministic prefix even if no logical
names were indexed before deletion.
Provision an external application value under the binding derived by
`engine.ProjectSecretBindingName(storage.ApplicationSecretScopeID(applicationID), name)`;
for the read-only environment provider, use
`engine.ProjectSecretEnvironmentName(storage.ApplicationSecretScopeID(applicationID), name)`.
These helpers keep application bindings separate from project bindings.

The `APEXLOAD_WRAPPING_*` key settings are shared with encrypted Bruno source
artifacts. A writable encrypted database Project Secrets store requires one of
them for native imports and retained source artifacts after Bruno Runner retirement.

The legacy read-only Key Vault resolver uses
`APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS`. An allowlisted project binding is
reserved for that vault even while the remote value is unavailable, so creating
a database secret with the same name cannot silently shadow it. Secret status
reports a readable legacy vault value as a read-only project secret.

Generate a local wrapping key with:

```bash
openssl rand -base64 32
```

Keep that key stable, backed up, and available to every controller replica.
Losing or changing it makes existing encrypted database values unreadable.
Inject it through a Kubernetes Secret, secret manager, or protected process
environment; do not put production key material in values files or source
control.

For Azure Key Vault, grant the controller identity `Key Vault Secrets Officer`.
Generators do not need write permission. The provided AKS Terraform grants the
controller that role and retains `Key Vault Secrets User` for generators.

### Import migration policy

Source credential migration is controlled in the application by
`system_settings.script_imports.allow_source_secret_migration`. It defaults to
`false` and only a system administrator can enable it. Enabling the policy does
not bypass project authorization: a project or system administrator must still
explicitly select migration for an import.

Imports create missing logical names only. They never replace an existing
Project Secret. Literal `[REDACTED]` values are unrecoverable and remain
deferred for later configuration.

### Imported script proxy and TLS policy

An imported script keeps the credentials from its source. Editors see them
masked and cannot read them, but every run still sends them. To stop an editor
from capturing a credential they cannot read, the settings that decide where
and how it is sent are locked once such a script is saved: the base URL, the
proxy mode and proxy settings, TLS, the HTTP version and QUIC, the redirect
limit, default headers, HTTP authentication, WS-Security and the web runtime's
additional attributes, plus the connection settings of the other protocols
(WebSocket, gRPC, database, messaging, Java and the rest). A save that changes one of them is refused with "script
changes the execution context for retained imported credentials", and the
message says how to proceed.

Other runtime settings still save: think time, pacing, logging, error handling,
timeouts, connection limits and keep-alive, response checks and warnings,
caching, cookies, DNS cache and IP version, the user agent and accept headers,
browser emulation, network simulation and the other web runtime settings, and
SOAP version and fault handling. Choosing **No Proxy** is also
allowed, because it sends the credential straight to the host it already goes
to; the exception is a script that keeps a `Proxy-Authorization` header, which
would then go to that host. After No Proxy, the saved proxy settings can stay
as they were or be blanked, but not be rewritten or extended, because a later
switch back to a proxy would activate them.

`system_settings.script_imports.allow_retained_credential_transport_edits`
([Import credential policy](#/access?setting=imports), under Access) lifts that
lock for the proxy mode, the proxy settings (URL, PAC URL and bypass list) and
TLS. It defaults to `false` and
only a system administrator can change it. Each change is written to the audit
log (`retained_credential_transport_policy_changed`). Enable it only where every
editor is already trusted with the imported credentials, such as a test
environment.

The setting applies to saving a script (including a library bundle import), to
authoring-session drafts and to debugging or replaying an edited draft, so those
accept the same edits. It does not unlock request targets, default headers or
authentication, nor the browser proxy. A new credential must still be bound as a
runtime secret. A script that keeps a proxy username, a proxy password or a
`Proxy-Authorization` header keeps its proxy URL, PAC URL and bypass list
locked (a proxy username or password can still be blanked with them), so the
credential cannot follow a new proxy; a kept `Proxy-Authorization` header also
locks the proxy mode and keeps the block exactly as it is. Bind the
credential as a runtime secret first. GitHub sync, guided repairs and the runtime-override check
at test start stay strict, because repository content can come from people who
are not APEX editors.

Turning the setting off does not undo edits already made. A proxy or TLS change
that was saved while it was on becomes the script's baseline, and an edit staged
in an authoring-session draft is not re-checked when the draft is approved.

### Distributed execution

The controller resolves only the logical names referenced by the selected
script and sends a bounded, memory-only bundle to generators that advertise the
secret-bundle capability. Delivery requires authenticated TLS: mTLS, or TLS
combined with the configured gRPC bearer token.

Production deployments must configure:

- `APEXLOAD_GRPC_TLS_CERT`
- `APEXLOAD_GRPC_TLS_KEY`
- `APEXLOAD_GRPC_TLS_CA`
- `APEXLOAD_GRPC_AUTH_TOKEN` when client authentication is not provided by mTLS

The Helm, AKS, and on-prem production manifests mount TLS identities and share
the bearer token through `apexload-data-platform`. They deliberately do not
enable Key Vault writes in generator pods.

Scripts may still be saved when the selected fleet is legacy or insecure. A
secret-bearing launch then fails closed with provisioning guidance; secret
values are not downgraded to plaintext transport.

### Project deletion

Deleting a project removes its encrypted database secret records. Azure Key
Vault and AWS Secrets Manager values are retained by design. The deletion
response identifies indexed logical names, their providers, and the
deterministic project binding prefix for finding values that were provisioned
externally but never indexed. This also applies to the legacy read-only Key
Vault allowlist.

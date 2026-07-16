# GitHub Enterprise Integration

APEX Load treats GitHub Enterprise Server (GHES) as a first-class source-control
and CI surface for JSON DSL scripts. The preferred automation path is a
GitHub App installation token, per-user OAuth remains available for human browse
and push actions, and a PAT can be configured only as a headless fallback.

## Authentication Model

- **GitHub App automation:** configure `app_id`, optional app slug, and a private
  key env var or file. Project policies bind to an installation id. The
  controller mints cached one-hour installation tokens and scopes them to the
  target repository when the API supports it.
- **Per-user OAuth:** users connect from Settings for interactive repo browsing,
  single-file import, and human-attributed pushes. Stored access and refresh
  tokens are encrypted with `APEXLOAD_GITHUB_TOKEN_KEY`; expiring OAuth tokens
  are refreshed and rotated when possible.
- **PAT fallback:** configure only an env var name. The token value is read from
  the controller environment and is never persisted. Use this for headless labs
  where a GitHub App cannot be installed yet.

## Global Settings

Dashboard -> Settings -> Integrations -> GitHub Script Sync:

| Field | Example | Notes |
| --- | --- | --- |
| Enable GitHub integration | on | Master switch. |
| OAuth Client ID | `Iv1...` | Required only for per-user OAuth. |
| Client Secret Env Var | `APEXLOAD_GITHUB_CLIENT_SECRET` | Env var name only. |
| GitHub App ID | `123456` | Required for App automation and Checks. |
| App Private Key Env/File | `APEXLOAD_GITHUB_APP_PRIVATE_KEY` | Env value may contain escaped `\n`. |
| Webhook Secret Env Var | `APEXLOAD_GITHUB_WEBHOOK_SECRET` | Used for `X-Hub-Signature-256` validation. |
| PAT Fallback Env Var | `APEXLOAD_GITHUB_PAT` | Optional fallback only. |
| Automation Mode | `app_oauth_pat` | Resolution order: App -> OAuth -> PAT. |
| Publish GitHub Checks | on | Requires GitHub App permissions. |
| API/Web Base URL | `https://ghe/api/v3`, `https://ghe` | github.com defaults also work. |
| CA / TLS | `/etc/apexload/ghe-ca.pem` | Internal PKI support; skip verify is lab-only. |

Secrets are not stored in settings; only env var names or file references are.
The configured GHES host is added to the outbound allow-list without disabling
the broader SSRF guard.

## Project Policy

Settings -> Projects & Compliance -> GitHub Project Policy defines the server
authority for each project:

- Allowed repositories and branches, including glob patterns such as
  `perf/*` or `release/*`.
- Path template and default sync folder.
- PR-required vs direct-commit policy.
- Import, push, bulk sync, webhook auto-sync, and Checks enablement.
- GitHub App installation id for project automation.

All GitHub import, push, bulk sync, webhook, and check-run routes enforce this
policy even if the underlying GitHub credential can access more repositories.

## Script Sync

- Single-script push stores both `remote_blob_sha` and `remote_commit_sha`.
  `last_commit_sha` remains for compatibility and represents the remote blob
  SHA used for Contents API optimistic concurrency.
- Single-script import returns source metadata through the standard import-review
  flow.
- Bulk import scans a configured repo path for `.json` DSL files, imports valid
  scripts, records job progress, and links imported scripts to their GitHub file.
- Bulk push writes selected or project scripts through the same policy checks.

Dashboard surfaces include installation health, project policy editing, row-level
drift badges, a bulk import action, per-script push/import modals, webhook logs,
and check-run status links.

## Webhooks

Configure the GHES webhook target:

`POST https://<apexload-host>/api/v1/integrations/github/webhook`

The controller validates `X-Hub-Signature-256`, deduplicates
`X-GitHub-Delivery`, records each delivery, and processes push events to update
linked script commit state. Auto-sync can be enabled by project policy; webhook
processing is designed to create reviewable state rather than silently overwrite
local scripts.

## CI Checks

GitHub Checks are controller-authoritative: a Check is created when the bounded
CI run is accepted and completed from controller state even if the Actions job
disconnects. The caller grants read access and OIDC only; the controller's
GitHub App owns `Checks: write`.

```yaml
permissions:
  contents: read
  id-token: write

jobs:
  performance:
    # Never execute contributor-controlled load scripts from a fork.
    if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.id == github.event.repository.id
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<reviewed-commit-sha>
      - id: apexload
        uses: JasonCzech/Project_Stormrunner/.github/actions/apexload-run@v1
        with:
          controller-url: https://apexload.example.com
          project-id: project-a
          script-path: apexload/scripts/order-flow.json
          p95-latency-ms: "800"
          max-error-pct: "2"
      - name: Retain evidence on GitHub.com
        if: always()
        uses: actions/upload-artifact@<reviewed-v4-commit-sha>
        with:
          name: apexload-evidence
          path: |
            ${{ steps.apexload.outputs.json-path }}
            ${{ steps.apexload.outputs.junit-path }}
          include-hidden-files: true
```

The action downloads a released `apexctl`; the consumer repository does not
need the APEX source tree or a Go toolchain. An operator must commit the release
public key with the action revision or supply `release-public-key`. Missing
signature trust fails closed. GHES callers mirror the action dependencies and
release assets, select the artifact action supported by their server release,
and use a self-hosted runner when the controller is private.

The controller trust policy binds issuer, audience, repository identity,
workflow ref, ref/environment, project, target hosts, protocols, VUsers, and
duration. Pull-request source repository identity is sent separately from the
base repository and is rejected when it is not trusted. Do not use
`pull_request_target` to check out or execute contributor-controlled content.

Target-bearing `{{secret.*}}` references are resolved by the controller before
a CI run is accepted, and the resolved host must match the project allowlist.
Keep the same project-scoped binding available to the controller and every load
generator; the resolved value is used only for preflight/runtime enforcement
and is not written into the stored CI metadata or returned in API responses.

Controller-backed CI currently rejects the `browser` protocol. Browser routing
hooks cannot enforce the target-host boundary across every Chromium redirect
and worker transport, so browser load remains disabled until egress is enforced
below the browser process. Use HTTP/protocol scripts for authoritative gates;
runner-local browser execution is suitable only for explicitly trusted smoke
tests on a network-isolated runner.

## CLI And MCP

`apexctl github` provides headless access to controller-backed GHES workflows:

```bash
apexctl github diagnostics --api http://localhost:8080
apexctl github installations --api http://localhost:8080 --refresh
apexctl github policy --api http://localhost:8080 --project project-a
apexctl github sync --api http://localhost:8080 --project project-a --kind import
apexctl github status --api http://localhost:8080 --script script-a
apexctl github check-run --api http://localhost:8080 --test test-a --commit-sha "$GITHUB_SHA"
```

The MCP endpoint catalog exposes the same diagnostics, policy, sync-job,
import, push, status, webhook-delivery, check-link, and check-run publish routes
with the existing confirmation guard for mutating tools.

## Operations

Readiness reports GHES reachability, auth mode configuration, token encryption,
webhook secret presence, check publishing prerequisites, and cached installation
state. Prometheus includes GitHub integration gauges and sync-job counters.
Audit events are recorded for project policy changes, imports, pushes, sync
jobs, webhook processing, and check-run publishing.

# GitHub Script Sync (GitHub Enterprise Server)

APEX Load can push test scripts (JSON DSL) to a GitHub repository and import them
back, so teams keep their source of truth in **GitHub Enterprise Server (GHES)**
(github.com is also supported). Authentication is a per-user OAuth web login;
each user pushes and pulls under their own GitHub identity.

## How it works

- **Global config (SystemAdmin):** the OAuth App registered on your GHES instance
  (`client_id` + a client secret supplied via an environment variable), the GHES
  base URLs, optional internal CA/TLS settings, and defaults (scopes, repo,
  branch, path template). Stored in System Settings.
- **Per-user connection:** each user clicks **Connect GitHub** (OAuth web flow);
  the resulting token is encrypted at rest (AES-GCM) and keyed to that user.
- **Per-script sync:** push a script as one JSON file (direct commit or pull
  request) and import a file back through the standard import-review flow. A
  `github_script_links` row maps each local script to its remote file for
  idempotent re-push and drift detection.

## Prerequisites

1. **Register an OAuth App on GHES** (`https://<ghes-host>/settings/applications`
   or an org-owned app):
   - **Homepage URL:** your APEX Load dashboard URL.
   - **Authorization callback URL:**
     `https://<apexload-host>/api/v1/integrations/github/callback`
   - Note the **Client ID** and generate a **Client Secret**.

2. **Environment variables on the controller:**
   - `APEXLOAD_GITHUB_CLIENT_SECRET` (or whatever name you configure) — the OAuth
     App client secret. Only the *env var name* is stored in settings; the secret
     value is never persisted or returned by the API.
   - `APEXLOAD_GITHUB_TOKEN_KEY` — a random string used to derive the AES-GCM key
     that encrypts per-user tokens at rest. **Required** to enable the feature;
     in `APEXLOAD_ENV=production` the feature fails closed (stays disabled and is
     surfaced in readiness) if this is unset.

## Configure (SystemAdmin)

Dashboard → **Settings → Integrations → GitHub Script Sync**:

| Field | Example | Notes |
| --- | --- | --- |
| Enable GitHub integration | on | Master switch. |
| OAuth Client ID | `Iv1.abc…` | From the GHES OAuth App. |
| Client Secret Env Var | `APEXLOAD_GITHUB_CLIENT_SECRET` | Name only; value from env. |
| API Base URL | `https://ghe.example.com/api/v3` | GHES REST base (github.com → `https://api.github.com`). |
| Web Base URL | `https://ghe.example.com` | OAuth authorize/token + blob links (github.com → `https://github.com`). |
| Scopes | `repo` | Space/comma separated. |
| Default Repository | `my-org/perf-scripts` | `owner/name`; pre-fills the push/import pickers. |
| Default Branch | `main` | |
| Path Template | `apexload/scripts/{project}/{ref_key}.json` | `{project}`, `{ref_key}`, `{id}`, `{name}`. |
| Internal CA Cert File | `/etc/apexload/ghe-ca.pem` | Optional; for internal PKI. |
| Skip TLS verification | off | Lab only — never in production. |

Save with the main **Save** button (the GitHub config round-trips through
`PUT /api/v1/system/settings`).

### GHES networking (egress)

GHES is usually an internal host that the control-plane SSRF guard would block.
The integration adds a **host-scoped allowance**: outbound is permitted **only**
to the configured GHES host (derived from the base URLs) — the rest of the egress
guard stays intact, and the broad `APEXLOAD_ALLOW_PRIVATE_WEBHOOKS` toggle is
**not** used. For internal TLS, set `Internal CA Cert File`.

## Connect (each user)

In the same panel, click **Connect GitHub**, approve on GHES, and you'll return
connected as your GitHub login. **Disconnect** revokes the stored token.

## Push a script

Script Library → a script row → **GitHub**:

- Pick **Repository** and **Branch**, optionally override the **File Path**
  (defaults from the path template) and **Commit Message**.
- Choose **Direct commit** or **Pull request**.
- The modal shows the current **sync status** and a **View on GitHub** link.

Pushes are idempotent: the link's recorded blob sha is used for GitHub's
optimistic concurrency. If the remote file changed since the last sync, the push
is refused (409) — pull and review first.

## Import a script

Script Library → **Import from GitHub**: browse the repo, pick a `.json` file,
and import. The file is parsed and validated as DSL and saved to your library
with GitHub source metadata (owner/repo/branch/path/commit).

## API reference

See the route table in `CLAUDE.md` (`/api/v1/integrations/github/*` and the
`/api/v1/scripts/{id}/github/*` + `/api/v1/scripts/import/github` endpoints).

## Out of scope (v1)

- PAT fallback for headless/CI (the OAuth web flow needs a browser).
- Whole-project/folder bulk sync and webhook-driven auto-sync.
- OAuth refresh-token rotation for instances that issue expiring tokens.
- In dev/no-auth mode, connections key to a single synthetic `local` user.

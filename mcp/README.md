# APEX Load MCP Server

The APEX Load MCP server lets AI agents operate the APEX Load REST API through the Model Context Protocol. It is an automation and control-plane bridge only; load generators and test execution never depend on this Python process.

## Before You Start

Run this bridge on a machine that can reach your APEX Load controller. File imports read paths on that machine, not on the dashboard server or an agent client elsewhere. Obtain a project-scoped service-account key through your administrator; both roles and token scopes apply. The Python bridge is not required for ordinary dashboard or CLI use.

## Capabilities

The server exposes structured tools for the documented API surface:

- `system_*`: health, readiness, inventory, usage, fleet, parity, support matrices, and settings.
- `auth_*`: identity, effective access, access matrix, diagnostics, service accounts, and audit exports.
- `project_*`, `application_*`, `release_*`, `folder_*`: project and release organization.
- `script_*`: library CRUD, validation, replay, imports, authoring sessions, runtime reports, and support matrices.
- `scenario_*`, `schedule_*`, `data_pool_*`: scenario assembly, load model validation, schedules, and shared data.
- `run_*`, `archive_*`: run lifecycle, live metrics, archive reports, comparisons, and retention.
- `ai_*`, `integrations_*`, `monitoring_*`, `report_*`, `results_*`: AI assistance, integrations, monitoring packs, report profiles, and external imports.

Stable resources are also available:

- `apexload://api-guide`
- `apexload://auth/access-matrix`
- `apexload://system/parity-status`
- `apexload://runtime-settings/support-matrix`
- `apexload://protocol-bundles/support-matrix`

## Installation

Use Python 3.10 or newer.

```bash
cd /path/to/apexload/mcp
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements.txt
```

On Windows, use `.venv\Scripts\python.exe` instead of `.venv/bin/python`. Keep the absolute interpreter path for your client configuration.

## Configuration

Service-account authentication is the recommended mode for agents and CI/CD automation.

| Variable | Default | Purpose |
| --- | --- | --- |
| `APEXLOAD_URL` | `http://localhost:8080/api/v1` | Controller API base URL. |
| `APEXLOAD_API_KEY` | empty | Preferred service-account key header. |
| `APEXLOAD_BEARER_TOKEN` | empty | Alternate bearer token if no API key is set. |
| `APEXLOAD_PROJECT_ID` | empty | Optional default project scope for list/read tools. |
| `APEXLOAD_TIMEOUT_SECONDS` | `30` | HTTP request timeout. |

When both `APEXLOAD_API_KEY` and `APEXLOAD_BEARER_TOKEN` are set, the API key is used.

## Safety Model

APEX Load RBAC remains the source of authorization truth. The MCP server adds an agent-facing guardrail before requests reach the API:

- Mutating tools require `confirm: true`.
- Destructive/admin tools require both `confirm: true` and a non-empty `reason`.
- Validate and preview tools do not require confirmation unless they mutate controller state.
- File import tools require paths that are visible to the machine running the MCP server.

All tool responses use a compact JSON shape:

```json
{"ok":true,"status_code":200,"data":{},"error":null}
```

Errors use the same top-level shape with `ok:false`, normalized `error.code`, `error.message`, `error.path`, and `error.remediation` fields.

## Usage With Claude Desktop

```json
{
  "mcpServers": {
    "apexload": {
      "command": "/path/to/apexload/mcp/.venv/bin/python",
      "args": ["/path/to/apexload/mcp/server.py"],
      "env": {
        "APEXLOAD_URL": "http://localhost:8080/api/v1",
        "APEXLOAD_API_KEY": "srk_...",
        "APEXLOAD_PROJECT_ID": "project-id"
      }
    }
  }
}
```

After configuring the client, restart its MCP connection and invoke a read-only system health tool. Expect an `ok: true` response and HTTP 200. Then inspect identity/effective access before using project tools. Client support and configuration formats vary; use the client's documented stdio MCP configuration with the same interpreter, script and environment values. This example is for Claude Desktop and is not a claim that every client accepts this JSON.

If Python reports a missing module, confirm the client uses the virtual-environment interpreter above. For 401/403 check the controller URL, key and project roles/scopes. If a file cannot be opened, confirm its path exists on the MCP host. See [API authentication](../docs/API_USER_GUIDE.md#authentication) and [Troubleshooting](../docs/HELP_TROUBLESHOOTING.md).

## Running Manually

```bash
cd /path/to/apexload/mcp
APEXLOAD_URL=http://localhost:8080/api/v1 .venv/bin/python server.py
```

## Testing

The following commands are for contributors, from the repository root with the MCP virtual environment installed. Clients normally launch and supervise the stdio process themselves.

Run the mocked unit suite:

```bash
mcp/.venv/bin/python -m pytest mcp
```

Run live integration smoke tests against a controller:

```bash
APEXLOAD_MCP_INTEGRATION=1 \
APEXLOAD_URL=http://localhost:8080/api/v1 \
mcp/.venv/bin/python -m pytest mcp/tests/test_integration_smoke.py
```

Integration tests are intentionally read/validate-only and are skipped unless `APEXLOAD_MCP_INTEGRATION=1` is set.

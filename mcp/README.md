# Stormrunner MCP Server

The Stormrunner MCP server lets AI agents operate the Stormrunner REST API through the Model Context Protocol. It is an automation and control-plane bridge only; load generators and test execution never depend on this Python process.

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

- `stormrunner://api-guide`
- `stormrunner://auth/access-matrix`
- `stormrunner://system/parity-status`
- `stormrunner://runtime-settings/support-matrix`
- `stormrunner://protocol-bundles/support-matrix`

## Installation

Use Python 3.10 or newer.

```bash
cd /path/to/Project_Stormrunner/mcp
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
```

## Configuration

Service-account authentication is the recommended mode for agents and CI/CD automation.

| Variable | Default | Purpose |
| --- | --- | --- |
| `STORMRUNNER_URL` | `http://localhost:8080/api/v1` | Controller API base URL. |
| `STORMRUNNER_API_KEY` | empty | Preferred service-account key header. |
| `STORMRUNNER_BEARER_TOKEN` | empty | Alternate bearer token if no API key is set. |
| `STORMRUNNER_PROJECT_ID` | empty | Optional default project scope for list/read tools. |
| `STORMRUNNER_TIMEOUT_SECONDS` | `30` | HTTP request timeout. |

When both `STORMRUNNER_API_KEY` and `STORMRUNNER_BEARER_TOKEN` are set, the API key is used.

## Safety Model

Stormrunner RBAC remains the source of authorization truth. The MCP server adds an agent-facing guardrail before requests reach the API:

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
    "stormrunner": {
      "command": "python3",
      "args": ["/path/to/Project_Stormrunner/mcp/server.py"],
      "env": {
        "STORMRUNNER_URL": "http://localhost:8080/api/v1",
        "STORMRUNNER_API_KEY": "srk_...",
        "STORMRUNNER_PROJECT_ID": "project-id"
      }
    }
  }
}
```

## Running Manually

```bash
cd /path/to/Project_Stormrunner/mcp
STORMRUNNER_URL=http://localhost:8080/api/v1 python3 server.py
```

## Testing

Run the mocked unit suite:

```bash
python3 -m pytest mcp
```

Run live integration smoke tests against a controller:

```bash
STORMRUNNER_MCP_INTEGRATION=1 \
STORMRUNNER_URL=http://localhost:8080/api/v1 \
python3 -m pytest mcp/tests/test_integration_smoke.py
```

Integration tests are intentionally read/validate-only and are skipped unless `STORMRUNNER_MCP_INTEGRATION=1` is set.

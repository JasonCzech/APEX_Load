import json
import os

import pytest

from apexload_mcp.tools import ENDPOINT_TOOLS, run_endpoint_tool


pytestmark = pytest.mark.skipif(
    os.environ.get("APEXLOAD_MCP_INTEGRATION") != "1",
    reason="set APEXLOAD_MCP_INTEGRATION=1 to run against a live controller",
)


def tool(name: str):
    return next(spec for spec in ENDPOINT_TOOLS if spec.name == name)


@pytest.mark.asyncio
async def test_live_controller_smoke_read_only_tools():
    for name in ("system_health", "auth_effective_access", "project_list", "script_list", "scenario_list", "run_list"):
        result = json.loads(await run_endpoint_tool(tool(name)))
        assert "ok" in result
        assert "status_code" in result


@pytest.mark.asyncio
async def test_live_controller_validate_only_tools():
    script_result = json.loads(await run_endpoint_tool(tool("script_validate_draft"), payload={"name": "MCP Smoke", "protocol": "http", "version": "1.0.0", "actions": []}))
    scenario_result = json.loads(await run_endpoint_tool(tool("scenario_validate_load_model"), payload={"name": "MCP Smoke", "groups": []}))

    assert "ok" in script_result
    assert "ok" in scenario_result

import json
from pathlib import Path

import httpx
import pytest

from apexload_mcp.client import APEX LoadClient, APEX LoadClientConfig
from apexload_mcp.tools import ENDPOINT_TOOLS, EndpointTool, local_api_guide_text, run_endpoint_tool, run_file_import_tool


def tool(name: str) -> EndpointTool:
    return next(spec for spec in ENDPOINT_TOOLS if spec.name == name)


@pytest.mark.asyncio
async def test_mutating_tool_requires_confirmation():
    result = json.loads(await run_endpoint_tool(tool("run_start"), path_params={"id": "run-1"}))

    assert result["ok"] is False
    assert result["error"]["code"] == "confirmation_required"


@pytest.mark.asyncio
async def test_destructive_tool_requires_reason_after_confirmation():
    result = json.loads(await run_endpoint_tool(tool("script_delete"), path_params={"id": "script-1"}, confirm=True))

    assert result["ok"] is False
    assert result["error"]["code"] == "reason_required"


@pytest.mark.asyncio
async def test_correlation_apply_requires_confirmation():
    result = json.loads(await run_endpoint_tool(
        tool("script_authoring_correlation_preview"),
        path_params={"id": "session-1"},
        payload={"apply": True},
    ))

    assert result["ok"] is False
    assert result["error"]["code"] == "confirmation_required"


@pytest.mark.asyncio
async def test_endpoint_tool_applies_path_params_and_default_project():
    requests: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return httpx.Response(200, json={"ok": True})

    client = APEX LoadClient(
        APEX LoadClientConfig(base_url="http://storm.test/api/v1", default_project_id="project-1"),
        transport=httpx.MockTransport(handler),
    )

    result = json.loads(await run_endpoint_tool(tool("script_list"), client=client))

    assert result["ok"] is True
    assert str(requests[0].url) == "http://storm.test/api/v1/scripts?project_id=project-1"


@pytest.mark.asyncio
async def test_endpoint_tool_reports_missing_path_params():
    result = json.loads(await run_endpoint_tool(tool("run_get")))

    assert result["ok"] is False
    assert result["error"]["code"] == "missing_path_params"


@pytest.mark.asyncio
async def test_representative_domain_tools_call_expected_routes():
    seen: list[tuple[str, str]] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, request.url.path))
        return httpx.Response(200, json={"path": request.url.path})

    client = APEX LoadClient(
        APEX LoadClientConfig(base_url="http://storm.test/api/v1"),
        transport=httpx.MockTransport(handler),
    )

    calls = [
        (tool("system_parity_status"), {}),
        (tool("auth_effective_access"), {"query": {"project_id": "p1"}}),
        (tool("project_get"), {"path_params": {"id": "p1"}}),
        (tool("script_validate_draft"), {"payload": {"name": "draft"}}),
        (tool("scenario_validate_load_model"), {"payload": {"name": "scenario"}}),
        (tool("run_archive_report"), {"path_params": {"id": "run-1"}}),
        (tool("report_profiles"), {}),
        (tool("ai_policy"), {"query": {"project_id": "p1"}}),
    ]

    for spec, kwargs in calls:
        result = json.loads(await run_endpoint_tool(spec, client=client, **kwargs))
        assert result["ok"] is True

    assert seen == [
        ("GET", "/api/v1/system/parity-status"),
        ("GET", "/api/v1/auth/effective-access"),
        ("GET", "/api/v1/projects/p1"),
        ("POST", "/api/v1/scripts/validate"),
        ("POST", "/api/v1/scenarios/validate-load-model"),
        ("GET", "/api/v1/tests/run-1/archive/report"),
        ("GET", "/api/v1/reports/profiles"),
        ("GET", "/api/v1/ai/policy"),
    ]


@pytest.mark.asyncio
async def test_file_import_requires_confirmation(tmp_path: Path):
    archive = tmp_path / "script.zip"
    archive.write_bytes(b"zip")

    result = json.loads(await run_file_import_tool("loadrunner", str(archive)))

    assert result["ok"] is False
    assert result["error"]["code"] == "confirmation_required"


@pytest.mark.asyncio
async def test_file_import_posts_accessible_file(tmp_path: Path):
    archive = tmp_path / "script.zip"
    archive.write_bytes(b"zip")
    seen: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        body = await request.aread()
        assert b'name="file"' in body
        assert b"script.zip" in body
        assert b'name="project_id"' in body
        return httpx.Response(200, json={"imported": True})

    client = APEX LoadClient(
        APEX LoadClientConfig(base_url="http://storm.test/api/v1", default_project_id="project-1"),
        transport=httpx.MockTransport(handler),
    )

    result = json.loads(await run_file_import_tool("loadrunner", str(archive), confirm=True, client=client))

    assert result["ok"] is True
    assert seen[0].url.path == "/api/v1/scripts/import/loadrunner"


def test_api_guide_resource_uses_repo_documentation():
    text = local_api_guide_text()

    assert "APEX Load API User Guide" in text
    assert "MCP" in text or "Service-account" in text

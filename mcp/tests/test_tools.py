import json
from pathlib import Path

import httpx
import pytest

from apexload_mcp.client import APEXLoadClient, APEXLoadClientConfig
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

    client = APEXLoadClient(
        APEXLoadClientConfig(base_url="http://storm.test/api/v1", default_project_id="project-1"),
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

    client = APEXLoadClient(
        APEXLoadClientConfig(base_url="http://storm.test/api/v1"),
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

    result = json.loads(await run_file_import_tool("enterprise_script_archive", str(archive)))

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

    client = APEXLoadClient(
        APEXLoadClientConfig(base_url="http://storm.test/api/v1", default_project_id="project-1"),
        transport=httpx.MockTransport(handler),
    )

    result = json.loads(await run_file_import_tool("enterprise_script_archive", str(archive), confirm=True, client=client))

    assert result["ok"] is True
    assert seen[0].url.path == "/api/v1/scripts/import/enterprise-script-archive"


def test_api_guide_resource_uses_repo_documentation():
    text = local_api_guide_text()

    assert "APEX Load API User Guide" in text
    assert "MCP" in text or "Service-account" in text

@pytest.mark.parametrize("name,path", [
    ("script_archive", "/scripts/script-1/archive"),
    ("script_restore", "/scripts/script-1/restore"),
    ("scenario_archive", "/scenarios/script-1/archive"),
    ("scenario_restore", "/scenarios/script-1/restore"),
])
@pytest.mark.asyncio
async def test_object_archive_tools_forward_optimistic_version(name, path):
    async def handler(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/api/v1" + path
        assert json.loads(request.content) == {"updated_at": "2026-10-02T00:00:00Z"}
        return httpx.Response(200, json={"id": "script-1"})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    result = json.loads(await run_endpoint_tool(tool(name), path_params={"id": "script-1"}, payload={"updated_at": "2026-10-02T00:00:00Z"}, confirm=True, client=client))
    assert result["ok"] is True

@pytest.mark.asyncio
async def test_project_activity_tool_forwards_resource_filters():
    async def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/api/v1/projects/project-1/activity"
        assert request.url.params["resource_type"] == "script"
        assert request.url.params["offset"] == "25"
        return httpx.Response(200, json={"events": []})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    result = json.loads(await run_endpoint_tool(tool("project_activity"), path_params={"id": "project-1"}, query={"resource_type": "script", "offset": 25}, client=client))
    assert result["ok"] is True


@pytest.mark.asyncio
async def test_appdynamics_tools_routes_query_and_confirmation():
    seen: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(202, json={"id": "inspection", "status": "pending"})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    params = {"id": "project", "connection_id": "connection"}
    guarded = json.loads(await run_endpoint_tool(tool("appdynamics_discover_metrics"), path_params=params, client=client))
    assert guarded["error"]["code"] == "confirmation_required"
    assert seen == []
    result = json.loads(await run_endpoint_tool(tool("appdynamics_discover_metrics"), path_params=params, query={"application_id": "42", "metric_path": "Business Transactions|Checkout"}, confirm=True, client=client))
    assert result["ok"] is True
    assert seen[-1].method == "GET"
    assert seen[-1].url.path == "/api/v1/projects/project/monitoring/appdynamics-connections/connection/metrics"
    assert seen[-1].url.params["application_id"] == "42"
    assert seen[-1].url.params["metric_path"] == "Business Transactions|Checkout"
    await run_endpoint_tool(tool("appdynamics_connection_delete"), path_params=params, query={"revision": 2}, confirm=True, reason="Retire connection", client=client)
    assert seen[-1].method == "DELETE"
    assert seen[-1].url.params["revision"] == "2"
    await run_endpoint_tool(tool("run_monitor_evidence"), path_params={"id": "run"}, query={"after_key": "cursor"}, client=client)
    assert seen[-1].url.path == "/api/v1/tests/run/monitor-evidence"
    assert seen[-1].url.params["after_key"] == "cursor"


@pytest.mark.asyncio
async def test_appdynamics_source_and_test_selection_parity():
    seen: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"accepted": True})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    source = {"id": "project", "source_id": "source"}
    result = json.loads(await run_endpoint_tool(tool("monitor_source_update"), path_params=source, payload={"revision": 3, "connection_id": "connection", "appdynamics": {"applications": ["42"]}}, confirm=True, client=client))
    assert result["ok"] is True
    assert seen[-1].method == "PUT"
    assert seen[-1].url.path == "/api/v1/projects/project/monitor-sources/source"
    assert json.loads(seen[-1].content)["revision"] == 3
    await run_endpoint_tool(tool("monitor_source_delete"), path_params=source, query={"revision": 4}, confirm=True, reason="Retire source", client=client)
    assert seen[-1].method == "DELETE"
    assert seen[-1].url.params["revision"] == "4"
    payload = {"project_id": "project", "monitor_bindings": [], "goal_config": {"monitor_rules": []}}
    await run_endpoint_tool(tool("run_create"), payload=payload, confirm=True, client=client)
    assert json.loads(seen[-1].content)["monitor_bindings"] == []
    assert json.loads(seen[-1].content)["goal_config"]["monitor_rules"] == []
    await run_endpoint_tool(tool("run_monitor_series"), path_params={"id": "run"}, query={"source_id": "source", "metric": "calls", "after_key": "cursor"}, client=client)
    assert seen[-1].url.params["after_key"] == "cursor"

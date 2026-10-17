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
@pytest.mark.parametrize("kind", ["script", "scenario"])
async def test_authoring_read_and_conditional_save_preserve_the_observed_revision(kind):
    tag = f'"{kind}-authoring-v1:revision-1"'
    calls = []

    async def handler(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        if request.method == "GET":
            assert request.url.params["view"] == "authoring"
            return httpx.Response(200, json={"id": "one"}, headers={"ETag": tag})
        if "If-Match" not in request.headers:
            return httpx.Response(428, json={"error": "conditional save required"})
        if request.headers.get("If-Match") != tag:
            return httpx.Response(412, json={"error": "changed"})
        assert request.headers["X-APEXLoad-API-Key"] == "key"
        return httpx.Response(200, json={"id": "one"}, headers={"ETag": '"revision-2"'})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1", api_key="key"), transport=httpx.MockTransport(handler))
    read = json.loads(await run_endpoint_tool(tool(f"{kind}_get"), path_params={"id": "one"}, query={"view": "authoring"}, client=client))
    assert read["etag"] == tag
    saved = json.loads(await run_endpoint_tool(tool(f"{kind}_update"), path_params={"id": "one"}, payload={"name": "edited"}, if_match=read["etag"], confirm=True, client=client))
    assert saved["ok"] and saved["etag"] == '"revision-2"'
    missing = json.loads(await run_endpoint_tool(tool(f"{kind}_update"), path_params={"id": "one"}, payload={"name": "edited"}, confirm=True, client=client))
    assert missing["status_code"] == 428
    failed = json.loads(await run_endpoint_tool(tool(f"{kind}_update"), path_params={"id": "one"}, payload={"name": "edited"}, if_match='"stale"', confirm=True, client=client))
    assert failed["status_code"] == 412
    assert len(calls) == 4  # Never reload or retry a stale save automatically.


@pytest.mark.asyncio
async def test_registered_tool_exposes_if_match(monkeypatch):
    from apexload_mcp import tools
    seen = []

    async def handler(request):
        seen.append(request.headers.get("If-Match"))
        return httpx.Response(200, json={"id": "one"})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    monkeypatch.setattr(tools, "APEXLoadClient", lambda: client)

    class Registry:
        def __init__(self):
            self.handlers = {}

        def tool(self, *, name, description):
            def register(handler):
                self.handlers[name] = handler
                return handler
            return register

    registry = Registry()
    tools.register_endpoint_tools(registry)
    await registry.handlers["script_update"](path_params={"id": "one"}, payload={}, if_match='"observed"', confirm=True)
    assert seen == ['"observed"']


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


def test_retired_integrations_are_not_registered():
    assert not any(spec.name.startswith("appdynamics_") for spec in ENDPOINT_TOOLS)
    assert not any("bruno-projection" in spec.path or "bruno-sync" in spec.path for spec in ENDPOINT_TOOLS)


@pytest.mark.asyncio
async def test_supported_source_and_test_selection_parity():
    seen: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"accepted": True})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    source = {"id": "project", "source_id": "source"}
    result = json.loads(await run_endpoint_tool(tool("monitor_source_update"), path_params=source, payload={"revision": 3, "connection_id": "connection", "dynatrace": {"entities": ["SERVICE-42"]}}, confirm=True, client=client))
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


@pytest.mark.asyncio
async def test_dynatrace_native_operations_revision_and_evidence_parity():
    seen: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(202 if request.method == "POST" else 200, json={"id": "op", "status": "pending"})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    params = {"id": "project", "connection_id": "connection"}
    payload = {"kind": "discover", "options": {"kind": "entities", "selector": "type(SERVICE)", "limit": 100}}
    guarded = json.loads(await run_endpoint_tool(tool("dynatrace_operation_create"), path_params=params, payload=payload, client=client))
    assert guarded["error"]["code"] == "confirmation_required"
    assert seen == []
    result = json.loads(await run_endpoint_tool(tool("dynatrace_operation_create"), path_params=params, payload=payload, confirm=True, client=client))
    assert result["ok"] is True
    assert seen[-1].url.path == "/api/v1/projects/project/monitoring/dynatrace-connections/connection/operations"
    assert json.loads(seen[-1].content) == payload
    approved_preview = {"kind": "preview", "source_id": "approved-source"}
    await run_endpoint_tool(tool("dynatrace_operation_create"), path_params=params, payload=approved_preview, confirm=True, client=client)
    assert json.loads(seen[-1].content) == approved_preview
    await run_endpoint_tool(tool("dynatrace_operation_get"), path_params={**params, "operation_id": "op"}, client=client)
    assert seen[-1].url.path.endswith("/operations/op")
    await run_endpoint_tool(tool("dynatrace_connection_delete"), path_params=params, query={"expected_revision": 3}, confirm=True, reason="Retire approved connection", client=client)
    assert seen[-1].url.params["expected_revision"] == "3"
    selection = {"project_id": "project", "metrics": [], "problems": True, "grail": []}
    await run_endpoint_tool(tool("monitor_source_create"), path_params={"id": "project"}, payload={"pack": "dynatrace", "connection_id": "connection", "executor_mode": "collector", "dynatrace": selection}, confirm=True, client=client)
    assert json.loads(seen[-1].content)["dynatrace"] == selection
    await run_endpoint_tool(tool("run_monitor_evidence"), path_params={"id": "run"}, query={"kind": "span", "after_key": "page"}, client=client)
    assert seen[-1].url.params["kind"] == "span"
    assert "target or alert" in tool("run_monitor_evidence").description
    assert "problem, span and trace" in tool("run_monitor_evidence").description


@pytest.mark.asyncio
async def test_rca_v2_tools_call_expected_routes():
    seen: list[tuple[str, str, bytes]] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, request.url.path, request.content))
        return httpx.Response(202, json={"analysis_id": "a1", "state": "queued"})

    client = APEXLoadClient(
        APEXLoadClientConfig(base_url="http://storm.test/api/v1"),
        transport=httpx.MockTransport(handler),
    )
    unconfirmed = json.loads(await run_endpoint_tool(tool("run_rca_analyze"), client=client, path_params={"id": "run-1"}, payload={"enrich": True}))
    assert unconfirmed["ok"] is False and seen == []

    calls = [
        (tool("run_rca_analyze"), {"path_params": {"id": "run-1"}, "payload": {"enrich": True}, "confirm": True}),
        (tool("run_rca_status"), {"path_params": {"id": "run-1", "analysis_id": "a1"}}),
        (tool("run_rca_assessment"), {"path_params": {"id": "run-1"}}),
        (tool("run_rca_bundle"), {"path_params": {"id": "run-1", "analysis_id": "a1"}}),
        (tool("run_rca_check_narration"), {"path_params": {"id": "run-1", "analysis_id": "a1"}, "payload": {"narrative": "anomaly-0000000000000001 rose"}}),
        (tool("run_rca_critiques"), {"path_params": {"id": "run-1", "analysis_id": "a1"}}),
        (tool("run_rca_critique_add"), {"path_params": {"id": "run-1", "analysis_id": "a1"}, "payload": {"narrative": "grounded"}, "confirm": True}),
    ]
    for spec, kwargs in calls:
        result = json.loads(await run_endpoint_tool(spec, client=client, **kwargs))
        assert result["ok"] is True

    assert [(method, path) for method, path, _ in seen] == [
        ("POST", "/api/v1/tests/run-1/archive/root-cause/analyses"),
        ("GET", "/api/v1/tests/run-1/archive/root-cause/analyses/a1"),
        ("GET", "/api/v1/tests/run-1/archive/root-cause/v2"),
        ("GET", "/api/v1/tests/run-1/archive/root-cause/analyses/a1/bundle"),
        ("POST", "/api/v1/tests/run-1/archive/root-cause/analyses/a1/narration-check"),
        ("GET", "/api/v1/tests/run-1/archive/root-cause/analyses/a1/critiques"),
        ("POST", "/api/v1/tests/run-1/archive/root-cause/analyses/a1/critiques"),
    ]
    assert json.loads(seen[0][2]) == {"enrich": True}


@pytest.mark.asyncio
async def test_elastic_connection_and_inspection_parity():
    seen: list[tuple[str, str, dict]] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content) if request.content else {}
        seen.append((request.method, request.url.path, payload))
        return httpx.Response(200, json={"id": "op", "status": "complete"})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    params = {"id": "p", "connection_id": "c", "operation_id": "op"}
    guarded = json.loads(await run_endpoint_tool(tool("elastic_inspection_create"), path_params=params, payload={"kind": "verify", "expected_revision": 2}, client=client))
    assert guarded["error"]["code"] == "confirmation_required"
    for name, kwargs in [
        ("elastic_connection_list", {}),
        ("elastic_inspection_create", {"payload": {"kind": "verify", "expected_revision": 2}, "confirm": True}),
        ("elastic_inspection_get", {}),
        ("elastic_inspection_cancel", {"confirm": True, "reason": "Cancel pending discovery"}),
    ]:
        result = json.loads(await run_endpoint_tool(tool(name), path_params=params, client=client, **kwargs))
        assert result["ok"] is True
    assert seen == [
        ("GET", "/api/v1/projects/p/monitoring/elk-connections", {}),
        ("POST", "/api/v1/projects/p/monitoring/elk-connections/c/inspections", {"kind": "verify", "expected_revision": 2}),
        ("GET", "/api/v1/projects/p/monitoring/elk-connections/c/inspections/op", {}),
        ("DELETE", "/api/v1/projects/p/monitoring/elk-connections/c/inspections/op", {}),
    ]


@pytest.mark.asyncio
async def test_elastic_evidence_projection_filter_parity():
    seen: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"evidence": [], "has_more": False})

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(handler))
    filters = {"kind": "span", "source_id": "elastic-source", "service": "checkout", "severity": "error", "trace_id": "trace+id", "from": "2026-10-06T10:00:00Z", "until": "2026-10-06T10:01:00Z", "limit": 100, "after_key": "cursor"}
    result = json.loads(await run_endpoint_tool(tool("run_monitor_evidence"), path_params={"id": "run"}, query=filters, client=client))
    assert result["ok"] is True
    assert dict(seen[-1].url.params) == {key: str(value) for key, value in filters.items()}
    assert "UTC from/until" in tool("run_monitor_evidence").description


@pytest.mark.asyncio
async def test_browser_intelligence_tools_call_expected_routes():
    seen: list[tuple[str, str, str]] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, request.url.path, request.url.query.decode()))
        return httpx.Response(200, json={"ok": True})

    client = APEXLoadClient(
        APEXLoadClientConfig(base_url="http://storm.test/api/v1", default_project_id="p1"),
        transport=httpx.MockTransport(handler),
    )
    unconfirmed = json.loads(await run_endpoint_tool(tool("analysis_policy_update"), client=client, path_params={"id": "policy-1"}, payload={"revision": 3}))
    assert unconfirmed["ok"] is False and seen == []

    calls = [
        (tool("run_archive_browser_findings"), {"path_params": {"id": "run-1"}}),
        (tool("run_archive_browser_interactions"), {"path_params": {"id": "run-1"}}),
        (tool("run_archive_journeys"), {"path_params": {"id": "run-1"}}),
        (tool("run_archive_browser_har_entry"), {"path_params": {"id": "run-1"}, "query": {"request_id": "gen-request-7"}}),
        (tool("analysis_policy_list"), {"query": {"kind": "browser_policy"}}),
        (tool("analysis_policy_get"), {"path_params": {"id": "policy-1"}, "query": {"revision": 2}}),
        (tool("analysis_policy_update"), {"path_params": {"id": "policy-1"}, "payload": {"kind": "browser_policy", "name": "Vitals", "definition": {}, "revision": 3}, "confirm": True}),
    ]
    for spec, kwargs in calls:
        result = json.loads(await run_endpoint_tool(spec, client=client, **kwargs))
        assert result["ok"] is True

    assert [(method, path) for method, path, _ in seen] == [
        ("GET", "/api/v1/tests/run-1/archive/browser-findings"),
        ("GET", "/api/v1/tests/run-1/archive/browser-interactions"),
        ("GET", "/api/v1/tests/run-1/archive/journeys"),
        ("GET", "/api/v1/tests/run-1/archive/browser/har-entry"),
        ("GET", "/api/v1/analysis/policies"),
        ("GET", "/api/v1/analysis/policies/policy-1"),
        ("PUT", "/api/v1/analysis/policies/policy-1"),
    ]
    assert "request_id=gen-request-7" in seen[3][2]
    assert "kind=browser_policy" in seen[4][2] and "project_id=p1" in seen[4][2]
    assert "project_id=p1" in seen[6][2]

import json
from pathlib import Path

import httpx
import jsonschema
import pytest

from apexload_mcp import agent_tools
from apexload_mcp.client import APEXLoadClient, APEXLoadClientConfig
from apexload_mcp.server import create_mcp_server, selected_toolsets
from apexload_mcp.tools import ENDPOINT_TOOLS
from generate_schemas import contracts_current

ROOT = Path(__file__).resolve().parents[2]
EXAMPLES = ROOT / "docs/contracts/agent-v1/examples"
AGENT_NAMES = [name for name, *_ in agent_tools.AGENT_TOOLS]


def example_bundle(name: str) -> dict:
    return {
        "brief": json.loads((EXAMPLES / f"brief.{name}.standard.json").read_text()),
        "markdown": (EXAMPLES / f"brief.{name}.standard.md").read_text(),
    }


def use_transport(monkeypatch, handler, requests):
    async def recording(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return handler(request)

    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://storm.test/api/v1"), transport=httpx.MockTransport(recording))
    monkeypatch.setattr(agent_tools, "new_client", lambda: client)


def test_contract_copies_are_current():
    assert contracts_current(), "run python mcp/generate_schemas.py"


def test_toolset_selection_defaults_to_the_curated_agent_set():
    assert selected_toolsets("") == ("agent",)
    assert selected_toolsets("full, AGENT") == ("agent", "full")
    with pytest.raises(ValueError, match="unknown APEXLOAD_MCP_TOOLSETS"):
        selected_toolsets("agent,everything")


@pytest.mark.asyncio
async def test_default_server_lists_only_the_agent_tools_in_a_stable_order(monkeypatch):
    monkeypatch.delenv("APEXLOAD_MCP_TOOLSETS", raising=False)
    tools = await create_mcp_server().list_tools()
    assert [tool.name for tool in tools] == AGENT_NAMES
    full = {tool.name for tool in await create_mcp_server(["agent", "full"]).list_tools()}
    assert {spec.name for spec in ENDPOINT_TOOLS} | set(AGENT_NAMES) <= full


@pytest.mark.asyncio
async def test_agent_tools_advertise_contract_output_schemas_and_annotations():
    tools = {tool.name: tool for tool in await create_mcp_server(["agent"]).list_tools()}
    assert tools["apex_run_brief"].outputSchema == agent_tools.contract_schema("brief.schema.json")
    assert tools["apex_get_finding"].outputSchema == agent_tools.contract_schema("finding-detail.schema.json")
    for name in ("apex_run_brief", "apex_get_finding", "apex_list_runs", "apex_ci_run", "apex_rca_bundle"):
        annotations = tools[name].annotations
        assert annotations.readOnlyHint is True and annotations.destructiveHint is False and annotations.idempotentHint is True
    assert tools["apex_wait_for_run"].annotations.idempotentHint is False
    assert tools["apex_compare_runs"].annotations.readOnlyHint is False
    for tool in tools.values():
        assert "client" not in tool.inputSchema.get("properties", {})


@pytest.mark.asyncio
async def test_run_brief_returns_schema_valid_structured_content_markdown_and_finding_links(monkeypatch):
    requests: list[httpx.Request] = []
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=example_bundle("regression")), requests)

    result = await agent_tools.apex_run_brief("run-1", detail="summary")

    assert result.isError is False
    jsonschema.validate(result.structuredContent, agent_tools.contract_schema("brief.schema.json"))
    assert result.content[0].type == "text" and result.content[0].text.startswith("## APEX Load:")
    links = [item for item in result.content if item.type == "resource_link"]
    # The example brief used an explicit comparison, so its links name it.
    assert links and str(links[0].uri).startswith("apexload://runs/run-1/comparisons/cmp-1/findings/fnd_")
    assert requests[0].url.path == "/api/v1/tests/run-1/archive/agent-brief"
    assert dict(requests[0].url.params) == {"format": "bundle", "detail": "summary", "baseline": "latest"}


@pytest.mark.asyncio
async def test_api_errors_are_tool_errors_without_structured_content(monkeypatch):
    error = {"error": "the run has not finished", "code": "run_not_finished", "remediation": "wait for the run to finish (apex_wait_for_run)"}
    use_transport(monkeypatch, lambda request: httpx.Response(409, json=error), [])

    result = await agent_tools.apex_run_brief("run-1")

    assert result.isError is True and result.structuredContent is None
    assert json.loads(result.content[0].text)["error"]["code"] == "run_not_finished"


@pytest.mark.asyncio
async def test_compare_runs_requires_confirmation_before_any_request(monkeypatch):
    requests: list[httpx.Request] = []
    use_transport(monkeypatch, lambda request: httpx.Response(201, json={"id": "cmp-1"}), requests)

    refused = await agent_tools.apex_compare_runs("run-1", ["run-0"])
    assert refused.isError is True and json.loads(refused.content[0].text)["error"]["code"] == "confirmation_required"
    assert requests == []

    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            return httpx.Response(201, json={"id": "cmp-1"})
        return httpx.Response(200, json=example_bundle("regression"))

    use_transport(monkeypatch, handler, requests)
    result = await agent_tools.apex_compare_runs("run-1", ["run-0"], confirm=True)
    assert result.isError is False
    assert json.loads(requests[0].content) == {"baseline_ids": ["run-0"]}
    assert dict(requests[1].url.params)["comparison_id"] == "cmp-1"


@pytest.mark.asyncio
async def test_wait_for_run_returns_the_summary_brief_once_finished(monkeypatch):
    monkeypatch.setattr(agent_tools, "WAIT_POLL_SECONDS", 0)
    statuses = iter(["RUNNING", "COMPLETE"])

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/agent-brief"):
            return httpx.Response(200, json=example_bundle("regression"))
        return httpx.Response(200, json={"id": "run-1", "status": next(statuses)})

    use_transport(monkeypatch, handler, [])
    result = await agent_tools.apex_wait_for_run("run-1", timeout_s=5)
    assert result.structuredContent["finished"] is True and result.structuredContent["brief"]["schema"] == "apexload.agent_brief.v1"


@pytest.mark.asyncio
async def test_wait_for_run_tells_the_agent_to_call_again(monkeypatch):
    monkeypatch.setattr(agent_tools, "WAIT_POLL_SECONDS", 10)
    use_transport(monkeypatch, lambda request: httpx.Response(200, json={"id": "run-1", "status": "RUNNING"}), [])
    result = await agent_tools.apex_wait_for_run("run-1", timeout_s=1)
    assert result.structuredContent == {"run_id": "run-1", "status": "RUNNING", "finished": False, "next": "call apex_wait_for_run again"}


@pytest.mark.asyncio
async def test_rca_bundle_finds_the_presented_analysis_or_says_there_is_none(monkeypatch):
    requests: list[httpx.Request] = []
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=example_bundle("regression")), requests)
    result = await agent_tools.apex_rca_bundle("run-1")
    assert result.isError is True and json.loads(result.content[0].text)["error"]["code"] == "rca_unavailable"

    bundle = example_bundle("regression")
    bundle["brief"]["rca"] = {"source": "v2", "analysis_id": "an 1", "top_hypotheses": []}

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/bundle"):
            return httpx.Response(200, json={"schema": "apexload.rca_bundle.v2", "analysis_id": "an 1"})
        return httpx.Response(200, json=bundle)

    requests.clear()
    use_transport(monkeypatch, handler, requests)
    result = await agent_tools.apex_rca_bundle("run-1", max_kib=48)
    assert result.isError is False
    assert requests[-1].url.raw_path.decode().split("?")[0] == "/api/v1/tests/run-1/archive/root-cause/analyses/an%201/bundle"
    assert dict(requests[-1].url.params) == {"max_bytes": str(48 * 1024)}


@pytest.mark.asyncio
async def test_list_runs_is_compact_and_pages(monkeypatch):
    tests = [{"id": f"run-{i}", "name": f"Run {i}", "status": "COMPLETE", "groups": [{"big": "x" * 500}]} for i in range(3)]
    requests: list[httpx.Request] = []
    use_transport(monkeypatch, lambda request: httpx.Response(200, json={"tests": tests, "total": 7}), requests)
    result = await agent_tools.apex_list_runs(limit=3)
    assert result.structuredContent["runs"][0] == {"id": "run-0", "name": "Run 0", "status": "COMPLETE"}
    assert result.structuredContent["next_offset"] == 3
    assert "offset=3" in result.content[0].text


@pytest.mark.asyncio
async def test_get_finding_forwards_the_comparison(monkeypatch):
    requests: list[httpx.Request] = []
    detail = {"finding": {"schema": "apexload.agent_finding_detail.v1", "related": []}, "markdown": "## fnd"}
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=detail), requests)
    await agent_tools.apex_get_finding("run-1", "fnd_reg_000000000000", comparison_id="cmp-2")
    assert dict(requests[0].url.params) == {"format": "bundle", "comparison_id": "cmp-2"}


@pytest.mark.asyncio
async def test_structured_results_survive_the_mcp_protocol(monkeypatch):
    """The installed SDK must pass a returned CallToolResult through FastMCP and
    the low-level server unchanged (the requirements floor depends on it)."""
    from mcp.shared.memory import create_connected_server_and_client_session

    use_transport(monkeypatch, lambda request: httpx.Response(200, json=example_bundle("regression")), [])
    server = create_mcp_server(["agent"])
    async with create_connected_server_and_client_session(server._mcp_server) as session:
        result = await session.call_tool("apex_run_brief", {"run_id": "run-1", "detail": "summary"})
    assert result.isError is False
    assert result.structuredContent["schema"] == "apexload.agent_brief.v1"
    assert [item.type for item in result.content][:1] == ["text"]


@pytest.mark.asyncio
async def test_every_advertised_output_schema_is_an_object_that_accepts_new_fields():
    tools = await create_mcp_server(["agent"]).list_tools()
    advertised = [tool for tool in tools if tool.outputSchema is not None]
    assert advertised
    for tool in advertised:
        assert tool.outputSchema["type"] == "object", tool.name
        assert '"additionalProperties": false' not in json.dumps(tool.outputSchema)
    brief = example_bundle("regression")["brief"]
    brief["added_in_a_later_v1"] = {"anything": True}
    jsonschema.validate(brief, agent_tools.contract_schema("brief.schema.json"))


@pytest.mark.asyncio
async def test_coded_controller_errors_keep_their_remediation_and_ref(monkeypatch):
    body = {"error": "durable comparison storage is unavailable", "code": "comparison_storage_unavailable", "remediation": "use baseline=none", "ref": "req-1"}
    use_transport(monkeypatch, lambda request: httpx.Response(503, json=body), [])
    result = await agent_tools.apex_run_brief("run-1")
    error = json.loads(result.content[0].text)["error"]
    assert error["code"] == "comparison_storage_unavailable" and error["remediation"] == "use baseline=none" and error["ref"] == "req-1"


def test_safe_name_keeps_names_on_one_plain_line():
    name = agent_tools.safe_name("Run | <b>x</b>\n# Ignore")
    assert name == "Run _ _b_x_/b_ # Ignore" and not any(ch in name for ch in "|<>\n`")
    assert len(agent_tools.safe_name("x" * 200)) == 80


@pytest.mark.asyncio
async def test_ci_run_reads_the_gate_from_the_brief(monkeypatch):
    bundle = example_bundle("regression")
    bundle["brief"]["verdict"].update({"source": "ci_outcome", "ci_outcome": "sla_breached", "ci_exit_code": 1, "blocking": True})
    requests: list[httpx.Request] = []
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=bundle), requests)
    result = await agent_tools.apex_ci_run("run-1")
    assert result.structuredContent["ci_exit_code"] == 1 and result.structuredContent["blocking"] is True
    assert requests[0].url.path == "/api/v1/tests/run-1/archive/agent-brief" and dict(requests[0].url.params)["baseline"] == "none"
    assert not any("/ci/runs" in r.url.path for r in requests)

    use_transport(monkeypatch, lambda request: httpx.Response(200, json=example_bundle("regression")), [])
    refused = await agent_tools.apex_ci_run("run-1")
    assert refused.isError is True and json.loads(refused.content[0].text)["error"]["code"] == "not_a_ci_run"

    def pending(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/agent-brief"):
            return httpx.Response(409, json={"error": "not finished", "code": "run_not_finished"})
        return httpx.Response(200, json={"id": "run-1", "status": "RUNNING"})

    use_transport(monkeypatch, pending, [])
    running = await agent_tools.apex_ci_run("run-1")
    assert running.structuredContent == {"test_id": "run-1", "status": "RUNNING", "finished": False, "next": "call apex_wait_for_run"}


@pytest.mark.asyncio
async def test_finding_links_carry_an_explicit_comparison(monkeypatch):
    bundle = example_bundle("regression")
    assert bundle["brief"]["baseline"]["selection"] == "explicit"
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=bundle), [])
    result = await agent_tools.apex_run_brief("run-1", comparison_id="cmp-1")
    links = [item for item in result.content if item.type == "resource_link"]
    assert links and all("/comparisons/cmp-1/findings/" in str(link.uri) for link in links)
    templates = {str(t.uriTemplate) for t in await create_mcp_server(["agent"]).list_resource_templates()}
    assert "apexload://runs/{run_id}/comparisons/{comparison_id}/findings/{finding_id}" in templates



@pytest.mark.asyncio
async def test_get_finding_forwards_baseline_none(monkeypatch):
    requests: list[httpx.Request] = []
    detail = {"finding": {"schema": "apexload.agent_finding_detail.v1", "related": []}, "markdown": "## fnd"}
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=detail), requests)
    await agent_tools.apex_get_finding("run-1", "fnd_err_000000000000", baseline="none")
    assert dict(requests[0].url.params) == {"format": "bundle", "baseline": "none"}


@pytest.mark.asyncio
async def test_finding_links_follow_the_briefs_baseline(monkeypatch):
    bundle = example_bundle("injection")
    assert bundle["brief"]["baseline"]["selection"] == "none"
    use_transport(monkeypatch, lambda request: httpx.Response(200, json=bundle), [])
    result = await agent_tools.apex_run_brief("run-1", baseline="none")
    links = [item for item in result.content if item.type == "resource_link"]
    assert links and all("/baselines/none/findings/" in str(link.uri) for link in links)
    templates = {str(t.uriTemplate) for t in await create_mcp_server(["agent"]).list_resource_templates()}
    assert "apexload://runs/{run_id}/baselines/none/findings/{finding_id}" in templates

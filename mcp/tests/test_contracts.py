"""Pin curated adapters to generated routes before expanding their schemas."""
import json
from pathlib import Path
import re
from urllib.parse import urlparse

import pytest

from apexload_mcp.client import APEXLoadClient, APEXLoadClientConfig, apply_path_params
from apexload_mcp.server import create_mcp_server
from apexload_mcp.tools import ENDPOINT_TOOLS, FILE_IMPORTERS, VALIDATE_IMPORTERS
from generate_schemas import OUTPUT, generate, input_schemas, json_schema

ROOT = Path(__file__).resolve().parents[2]
CATALOG = json.loads((ROOT / "docs/api-endpoint-catalog.json").read_text())
ROUTES = {(route["method"], route["path"]) for route in CATALOG["routes"]}


def test_curated_tool_names_are_unique():
    names = [tool.name for tool in ENDPOINT_TOOLS]
    assert len(names) == len(set(names))


@pytest.mark.parametrize("tool", ENDPOINT_TOOLS, ids=lambda tool: tool.name)
def test_every_curated_method_path_and_placeholder_matches_controller(tool):
    client = APEXLoadClient(APEXLoadClientConfig(base_url="http://controller.test/api/v1"))
    assert (tool.method, urlparse(client.url_for(tool.path)).path) in ROUTES
    names = re.findall(r"\{([^{}]+)\}", tool.path)
    assert len(names) == len(set(names))
    values = {name: f"{name}/space value" for name in names}
    resolved, error = apply_path_params(tool.path, values)
    assert error is None
    assert "{" not in resolved and "}" not in resolved
    for name in names:
        assert f"{name}%2Fspace%20value" in resolved
        missing = dict(values)
        del missing[name]
        path, error = apply_path_params(tool.path, missing)
        assert path is None and error["error"]["code"] == "missing_path_params"
        assert name in error["error"]["message"]


@pytest.mark.parametrize("path", [*FILE_IMPORTERS.values(), *VALIDATE_IMPORTERS.values()])
def test_file_adapters_match_controller(path):
    assert ("POST", "/api/v1" + path) in ROUTES


def test_generated_input_schemas_are_current():
    assert OUTPUT.read_text() == generate()


@pytest.mark.asyncio
async def test_actual_tool_registry_exposes_selected_schemas_and_keeps_curated_contract():
    registered = {tool.name: tool for tool in await create_mcp_server().list_tools()}
    generated = json.loads(OUTPUT.read_text())["tools"]
    for spec in ENDPOINT_TOOLS:
        assert registered[spec.name].description == spec.description
        properties = registered[spec.name].inputSchema["properties"]
        assert set(properties) == {"path_params", "query", "payload", "if_match", "confirm", "reason"}
        for name, schema in generated.get(spec.name, {}).items():
            assert properties[name]["anyOf"] == [schema, {"type": "null"}]
            assert properties[name]["default"] is None
    run_query = registered["run_list"].inputSchema["properties"]["query"]["anyOf"][0]
    assert run_query["properties"]["limit"]["type"] == "integer"
    start = registered["run_start"].inputSchema["properties"]
    assert start["path_params"]["anyOf"][0]["required"] == ["id"]
    assert start["payload"]["anyOf"][0]["properties"]["expected_preparation_revision"]["type"] == "string"
    assert start["payload"]["anyOf"][0]["properties"]["override_capacity_shortfall"]["type"] == "boolean"
    assert registered["start_test"].inputSchema["properties"] == registered["run_start"].inputSchema["properties"]


def test_schema_generator_rejects_missing_path_parameter_and_unresolved_reference():
    with pytest.raises(ValueError, match="path parameters"):
        input_schemas({"parameters": []}, "/tests/{id}")
    with pytest.raises(ValueError, match="self-contained"):
        json_schema({"$ref": "#/components/schemas/Example"})
    assert json_schema({"type": "string", "nullable": True}) == {"anyOf": [{"type": "string"}, {"type": "null"}]}

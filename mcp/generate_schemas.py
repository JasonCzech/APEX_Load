#!/usr/bin/env python3
"""Generate bounded MCP input schemas from the controller's OpenAPI artifact."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re

import yaml

from apexload_mcp.tools import ENDPOINT_TOOLS

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "mcp/apexload_mcp/input_schemas.json"
# The agent tools advertise the controller's agent-v1 schemas as their MCP
# output schemas; the package carries a copy so it works outside a checkout.
CONTRACTS_SOURCE = ROOT / "docs/contracts/agent-v1"
CONTRACTS_OUTPUT = ROOT / "mcp/apexload_mcp/contracts"
# Start with common read/start workflows. Names, descriptions, guards, and
# conditional-save adapters remain curated in tools.py. Aliases share schemas.
WORKFLOWS = {
    ("GET", "/scripts/{id}"),
    ("GET", "/scenarios/{id}"),
    ("GET", "/tests"),
    ("POST", "/tests/{id}/start"),
    ("GET", "/monitoring/prometheus-packs"),
    ("POST", "/llm/prompt-datasets/import"),
    ("POST", "/llm/prompt-datasets/synthesize"),
    ("POST", "/llm/prompt-datasets/calibrate"),
    ("POST", "/llm/capacity-plan"),
    ("POST", "/llm/compare"),
    ("GET", "/tests/{id}/archive/capacity-sweep"),
    ("GET", "/tests/{id}/archive/llm-latency-decomposition"),
    ("GET", "/tests/{id}/archive/llm-samples"),
    ("POST", "/tests/{id}/archive/genai-spans/lookup"),
    ("GET", "/dsl/actions"),
    ("POST", "/correlations/diagnose"),
    ("POST", "/scripts/{id}/repairs/preview"),
    ("POST", "/scenarios/size-from-goal"),
    ("GET", "/replay/sessions/{session_id}"),
    ("GET", "/replay/sessions/{session_id}/export"),
    ("POST", "/replay/sessions/{session_id}/pause"),
    ("POST", "/replay/sessions/{session_id}/resume"),
    ("POST", "/replay/sessions/{session_id}/stop"),
    ("GET", "/tests/{id}/preparation"),
    ("GET", "/tests/{id}/errors/evidence"),
    ("GET", "/tests/{id}/transactions/live/{event_id}"),
    ("GET", "/tests/{id}/archive/exemplars"),
    ("GET", "/tests/{id}/baseline-candidates"),
}


def selected_paths(source: Path, paths: set[str]) -> dict:
    """Read selected generated path blocks without parsing huge response trees."""
    blocks: dict[str, list[str]] = {}
    current = None
    with source.open() as stream:
        for line in stream:
            if line.startswith("  /") and line.endswith(":\n"):
                path = line.strip()[:-1]
                current = path if path in paths else None
                if current is not None:
                    blocks[current] = []
            if current is not None:
                blocks[current].append(line)
    missing = paths - blocks.keys()
    if missing:
        raise ValueError(f"OpenAPI paths missing: {sorted(missing)}")
    loader = getattr(yaml, "CSafeLoader", yaml.SafeLoader)
    return {path: yaml.load("".join(lines), Loader=loader)[path] for path, lines in blocks.items()}


def json_schema(value):
    """Translate OpenAPI nullable while retaining all declared schema detail."""
    if isinstance(value, list):
        return [json_schema(item) for item in value]
    if not isinstance(value, dict):
        return value
    if "$ref" in value:
        raise ValueError("selected MCP input must be self-contained; expand its OpenAPI reference first")
    result = {key: json_schema(item) for key, item in value.items() if key != "nullable"}
    return {"anyOf": [result, {"type": "null"}]} if value.get("nullable") else result


def input_schemas(operation: dict, path: str) -> dict:
    schemas = {}
    for location, name in (("path", "path_params"), ("query", "query")):
        parameters = [p for p in operation.get("parameters", []) if p["in"] == location]
        if not parameters:
            continue
        properties = {p["name"]: json_schema(p["schema"]) for p in parameters}
        required = [p["name"] for p in parameters if p.get("required")]
        schemas[name] = {"type": "object", "properties": properties}
        if required:
            schemas[name]["required"] = sorted(required)
    placeholders = set(re.findall(r"\{([^{}]+)\}", path))
    if set(schemas.get("path_params", {}).get("properties", {})) != placeholders:
        raise ValueError(f"OpenAPI path parameters do not match {path}")
    body = operation.get("requestBody", {}).get("content", {}).get("application/json")
    if body:
        schemas["payload"] = json_schema(body["schema"])
    return schemas


def generate(source: Path = ROOT / "docs/swagger.yaml") -> str:
    paths = selected_paths(source, {"/api/v1" + path for _, path in WORKFLOWS})
    schemas = {}
    for tool in ENDPOINT_TOOLS:
        if (tool.method, tool.path) not in WORKFLOWS:
            continue
        operation = paths["/api/v1" + tool.path][tool.method.lower()]
        schemas[tool.name] = input_schemas(operation, tool.path)
    return json.dumps({"source": "docs/swagger.yaml", "tools": schemas}, indent=2, sort_keys=True) + "\n"


def contract_copies() -> dict[str, str]:
    """The agent-v1 schemas the package copies, by file name."""
    return {path.name: path.read_text() for path in sorted(CONTRACTS_SOURCE.glob("*.schema.json"))}


def contracts_current() -> bool:
    expected = contract_copies()
    actual = {path.name: path.read_text() for path in sorted(CONTRACTS_OUTPUT.glob("*.schema.json"))} if CONTRACTS_OUTPUT.is_dir() else {}
    return actual == expected


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = generate()
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != generated or not contracts_current():
            print("Run the MCP Python environment: python mcp/generate_schemas.py")
            return 1
    else:
        OUTPUT.write_text(generated)
        CONTRACTS_OUTPUT.mkdir(exist_ok=True)
        for stale in CONTRACTS_OUTPUT.glob("*.schema.json"):
            stale.unlink()
        for name, body in contract_copies().items():
            (CONTRACTS_OUTPUT / name).write_text(body)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

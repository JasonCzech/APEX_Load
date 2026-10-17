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
# Start with common read/start workflows. Names, descriptions, guards, and
# conditional-save adapters remain curated in tools.py. Aliases share schemas.
WORKFLOWS = {("GET", "/scripts/{id}"), ("GET", "/scenarios/{id}"),
             ("GET", "/tests"), ("POST", "/tests/{id}/start")}


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


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = generate()
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != generated:
            print("Run the MCP Python environment: python mcp/generate_schemas.py")
            return 1
    else:
        OUTPUT.write_text(generated)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

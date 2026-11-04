from __future__ import annotations

import os
from typing import Any, Callable, Iterable

from mcp.server.fastmcp import FastMCP

from .agent_tools import output_schemas, register_agent_tools
from .tools import register_endpoint_tools, register_resources

# Toolsets an MCP client can be given. "agent" is the curated, verdict-first
# set and the default; "full" is the REST proxy (one tool per endpoint).
TOOLSETS: dict[str, Callable[[Any], None]] = {
    "agent": register_agent_tools,
    "full": register_endpoint_tools,
}
DEFAULT_TOOLSETS = ("agent",)


def selected_toolsets(raw: str | None = None) -> tuple[str, ...]:
    """The toolsets APEXLOAD_MCP_TOOLSETS selects, in TOOLSETS order."""
    value = os.environ.get("APEXLOAD_MCP_TOOLSETS", "") if raw is None else raw
    names = {name.strip().lower() for name in value.split(",") if name.strip()}
    if not names:
        return DEFAULT_TOOLSETS
    unknown = sorted(names - set(TOOLSETS))
    if unknown:
        raise ValueError(f"unknown APEXLOAD_MCP_TOOLSETS value(s) {', '.join(unknown)}; use any of: {', '.join(TOOLSETS)}")
    return tuple(name for name in TOOLSETS if name in names)


class APEXLoadMCP(FastMCP):
    """FastMCP that advertises the agent-v1 output schemas. The agent tools
    return their CallToolResult directly, so FastMCP cannot infer the schema
    from a return annotation."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.output_schemas: dict[str, dict[str, Any]] = {}

    async def list_tools(self):  # type: ignore[override]
        tools = await super().list_tools()
        return [
            tool.model_copy(update={"outputSchema": self.output_schemas[tool.name]}) if tool.name in self.output_schemas else tool
            for tool in tools
        ]


def create_mcp_server(toolsets: Iterable[str] | None = None) -> APEXLoadMCP:
    server = APEXLoadMCP("APEX Load")
    chosen = selected_toolsets() if toolsets is None else selected_toolsets(",".join(toolsets))
    for name in chosen:
        TOOLSETS[name](server)
    if "agent" in chosen:
        server.output_schemas = output_schemas()
    register_resources(server)
    return server


def main() -> None:
    create_mcp_server().run()

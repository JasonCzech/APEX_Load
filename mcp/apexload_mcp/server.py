from __future__ import annotations

from mcp.server.fastmcp import FastMCP

from .tools import register_endpoint_tools, register_resources


def create_mcp_server() -> FastMCP:
    server = FastMCP("APEX Load")
    register_endpoint_tools(server)
    register_resources(server)
    return server


def main() -> None:
    create_mcp_server().run()

import asyncio
import httpx
import os
from mcp.server.fastmcp import FastMCP

# Initialize FastMCP server
mcp = FastMCP("Stormrunner")

# Base URL for the Stormrunner API
STORMRUNNER_URL = os.environ.get("STORMRUNNER_URL", "http://localhost:8080/api/v1")

@mcp.tool()
async def list_tests() -> str:
    """List all performance tests and their current status."""
    async with httpx.AsyncClient() as client:
        response = await client.get(f"{STORMRUNNER_URL}/tests")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def get_test_details(test_id: str) -> str:
    """Get detailed status and live metrics for a specific test."""
    async with httpx.AsyncClient() as client:
        response = await client.get(f"{STORMRUNNER_URL}/tests/{test_id}")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def start_test(test_id: str) -> str:
    """Start a queued performance test."""
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{STORMRUNNER_URL}/tests/{test_id}/start")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def stop_test(test_id: str) -> str:
    """Gracefully stop a running performance test."""
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{STORMRUNNER_URL}/tests/{test_id}/stop")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def pause_test(test_id: str) -> str:
    """Pause a running performance test at the next action boundary."""
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{STORMRUNNER_URL}/tests/{test_id}/pause")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def resume_test(test_id: str) -> str:
    """Resume a paused performance test."""
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{STORMRUNNER_URL}/tests/{test_id}/resume")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def continue_test(test_id: str) -> str:
    """Continue a paused performance test (alias for resume)."""
    return await resume_test(test_id)

@mcp.tool()
async def list_scripts() -> str:
    """List all available test scripts in the library."""
    async with httpx.AsyncClient() as client:
        response = await client.get(f"{STORMRUNNER_URL}/scripts")
        response.raise_for_status()
        return response.text

@mcp.tool()
async def get_system_health() -> str:
    """Check the health status of the Stormrunner controller."""
    async with httpx.AsyncClient() as client:
        response = await client.get(f"{STORMRUNNER_URL}/system/health")
        response.raise_for_status()
        return response.text

if __name__ == "__main__":
    mcp.run()

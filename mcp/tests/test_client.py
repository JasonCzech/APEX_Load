import json

import httpx
import pytest

from stormrunner_mcp.client import StormrunnerClient, StormrunnerClientConfig, normalize_query


def test_auth_headers_prefer_service_account_api_key():
    client = StormrunnerClient(StormrunnerClientConfig(api_key="api-key", bearer_token="bearer"))

    assert client.auth_headers == {"X-Stormrunner-API-Key": "api-key"}


def test_auth_headers_use_bearer_when_no_api_key():
    client = StormrunnerClient(StormrunnerClientConfig(bearer_token="token"))

    assert client.auth_headers == {"Authorization": "Bearer token"}


def test_url_joining_query_encoding_and_metrics_origin():
    client = StormrunnerClient(StormrunnerClientConfig(base_url="http://storm.test/root/api/v1"))

    assert client.url_for("/scripts", {"project_id": "a b", "status": ["ready", "blocked"]}) == (
        "http://storm.test/root/api/v1/scripts?project_id=a+b&status=ready&status=blocked"
    )
    assert client.url_for("/metrics") == "http://storm.test/root/metrics"
    assert normalize_query({"empty": "", "none": None, "flag": True}) == [("flag", "true")]


@pytest.mark.asyncio
async def test_json_response_shape():
    async def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["x-stormrunner-api-key"] == "api-key"
        return httpx.Response(200, json={"status": "healthy"})

    client = StormrunnerClient(
        StormrunnerClientConfig(base_url="http://storm.test/api/v1", api_key="api-key"),
        transport=httpx.MockTransport(handler),
    )

    payload = await client.request("GET", "/system/health")

    assert payload == {"ok": True, "status_code": 200, "data": {"status": "healthy"}, "error": None}


@pytest.mark.asyncio
async def test_non_json_download_metadata():
    async def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, content="id,name\n1,Run\n", headers={"Content-Type": "text/csv"})

    client = StormrunnerClient(
        StormrunnerClientConfig(base_url="http://storm.test/api/v1"),
        transport=httpx.MockTransport(handler),
    )

    payload = await client.request("GET", "/auth/audit-events/export")

    assert payload["ok"] is True
    assert payload["data"]["content_type"] == "text/csv"
    assert payload["data"]["byte_length"] == len("id,name\n1,Run\n")
    assert payload["data"]["text_preview"].startswith("id,name")


@pytest.mark.asyncio
async def test_api_error_is_normalized():
    async def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={"error": "forbidden", "code": "rbac_denied"})

    client = StormrunnerClient(
        StormrunnerClientConfig(base_url="http://storm.test/api/v1"),
        transport=httpx.MockTransport(handler),
    )

    payload = await client.request("DELETE", "/scripts/script-1")

    assert payload["ok"] is False
    assert payload["status_code"] == 403
    assert payload["error"]["code"] == "rbac_denied"
    assert "RBAC" in payload["error"]["remediation"]


@pytest.mark.asyncio
async def test_transport_error_is_normalized():
    async def handler(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("controller down")

    client = StormrunnerClient(
        StormrunnerClientConfig(base_url="http://storm.test/api/v1"),
        transport=httpx.MockTransport(handler),
    )

    payload = await client.request("GET", "/system/health")

    assert payload["ok"] is False
    assert payload["status_code"] == 0
    assert payload["error"]["code"] == "transport_error"


def test_json_text_contract_from_client_response():
    encoded = json.dumps({"ok": True, "status_code": 200, "data": {"a": 1}, "error": None}, separators=(",", ":"), sort_keys=True)

    assert encoded == '{"data":{"a":1},"error":null,"ok":true,"status_code":200}'

import json
import httpx
import pytest
from apexload_mcp.client import APEXLoadClient, APEXLoadClientConfig
from apexload_mcp.tools import ENDPOINT_TOOLS, run_endpoint_tool

@pytest.mark.asyncio
async def test_prometheus_inspection_tools_preserve_selection_and_async_contract():
    seen = []
    async def handler(request):
        seen.append((request.method, request.url.path, json.loads(request.content or b'{}')))
        return httpx.Response(202, json={'id': 'inspection', 'status': 'pending'})
    client = APEXLoadClient(APEXLoadClientConfig(base_url='http://storm.test/api/v1'), transport=httpx.MockTransport(handler))
    tools = {item.name: item for item in ENDPOINT_TOOLS}
    for name, suffix, payload in [
        ('prometheus_connection_test', 'test', {}),
        ('prometheus_discover', 'discover', {'kind': 'label_values', 'selector': '{job="checkout"}', 'label': 'instance'}),
        ('prometheus_preview', 'preview', {'selection': {'project_id': 'p', 'queries': [{'id': 'q', 'name': 'cpu', 'expression': 'up', 'unit': 'count'}]}}),
        ('prometheus_dashboard_adopt', 'adopt-dashboard', {'dashboard_id': 'saved-dashboard'}),
    ]:
        result = json.loads(await run_endpoint_tool(tools[name], path_params={'id': 'p', 'connection_id': 'c'}, payload=payload, confirm=True, client=client))
        assert result['ok'] is True
        assert seen[-1] == ('POST', f'/api/v1/projects/p/monitoring/prometheus-connections/c/{suffix}', payload)
    assert 'target-system' in tools['prometheus_connection_list'].description
    assert tools['prometheus_metrics'].path == '/metrics'

@pytest.mark.asyncio
async def test_prometheus_connection_mutation_requires_confirmation():
    spec = next(item for item in ENDPOINT_TOOLS if item.name == 'prometheus_connection_create')
    result = json.loads(await run_endpoint_tool(spec, path_params={'id': 'p'}, payload={'name': 'draft', 'enabled': False}))
    assert result['error']['code'] == 'confirmation_required'

@pytest.mark.asyncio
@pytest.mark.parametrize('kind', ['target', 'alert'])
async def test_generic_evidence_tool_preserves_prometheus_filters(kind):
    seen = []
    async def handler(request):
        seen.append((request.method, request.url.path, dict(request.url.params)))
        return httpx.Response(200, json={'evidence': [], 'has_more': False})
    client = APEXLoadClient(APEXLoadClientConfig(base_url='http://storm.test/api/v1'), transport=httpx.MockTransport(handler))
    spec = next(item for item in ENDPOINT_TOOLS if item.name == 'run_monitor_evidence')
    query = {'source_id': 'prom', 'kind': kind, 'after_key': 'cursor', 'limit': '50'}
    result = json.loads(await run_endpoint_tool(spec, path_params={'id': 'run'}, query=query, client=client))
    assert result['ok'] is True
    assert seen == [('GET', '/api/v1/tests/run/monitor-evidence', query)]
    assert 'target or alert' in spec.description

import { parseEndpointCatalog, type ApiEndpointCatalog } from './apiEndpointCatalog'
import { apiFetch } from './hooks/apiClient'

async function readTextResponse(path: string, accept: string) {
  const res = await apiFetch(path, {
    headers: { Accept: accept },
  })
  const text = await res.text()
  if (!res.ok) {
    throw new Error(text.trim() || res.statusText || `Request failed with status ${res.status}`)
  }
  const contentType = (res.headers.get('content-type') || '').toLowerCase()
  if (contentType.includes('text/html')) {
    throw new Error('API documentation endpoint returned dashboard HTML. The controller route may be unavailable.')
  }
  if (!text.trim()) {
    throw new Error('API documentation endpoint returned an empty response.')
  }
  return text
}

export async function loadApiEndpointCatalog(): Promise<ApiEndpointCatalog> {
  const text = await readTextResponse('/docs/endpoint-catalog', 'application/json')
  const catalog = parseEndpointCatalog(text)
  if (catalog.version <= 0 || catalog.routes.length === 0) {
    throw new Error('API endpoint catalog is invalid or empty.')
  }
  return catalog
}

export async function loadSwaggerYaml(): Promise<string> {
  return readTextResponse('/docs/swagger.yaml', 'application/yaml, text/yaml, text/plain')
}
